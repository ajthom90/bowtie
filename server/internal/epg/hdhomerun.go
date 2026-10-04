package epg

import (
	"bufio"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ajthom90/bowtie/server/internal/epg/xmltv"
	"github.com/ajthom90/bowtie/server/internal/hdhr"
)

// SiliconDust's free guide for HDHomeRun owners
// (https://github.com/Silicondust/documentation/wiki/XMLTV-Guide-Data).
const (
	sourceHDHomeRun = "hdhomerun"
	// hdhomerunIDPrefix keeps this source's channel ids distinct from an XMLTV
	// source pointed at the same feed (epg_channels.id is unique across sources).
	hdhomerunIDPrefix = "hdhomerun:"

	defaultHDHomeRunGuideURL = "https://api.hdhomerun.com/api/xmltv"

	settingHDHomeRunLastSuccess = "epg.hdhomerun.lastSuccess"
	settingHDHomeRunLastError   = "epg.hdhomerun.lastError"

	// SiliconDust asks for a refresh every 20-28 hours at a random time.
	hdhomerunMinInterval = 20 * time.Hour
	hdhomerunMaxInterval = 28 * time.Hour
	// hdhomerunRetry is the wait after a failed refresh.
	hdhomerunRetry = time.Hour
	// hdhomerunFetchTimeout bounds one guide download.
	hdhomerunFetchTimeout = 2 * time.Minute
)

var errNoHDHomeRun = errors.New("no HDHomeRun found")

// hdhomerunInterval is a random wait in [20h, 28h].
func hdhomerunInterval() time.Duration {
	return hdhomerunMinInterval + time.Duration(rand.Int63n(int64(hdhomerunMaxInterval-hdhomerunMinInterval)+1))
}

// hdhomerunConfigured: the setting is on and at least one tuner is known.
func (s *Service) hdhomerunConfigured() bool {
	g, err := s.prov.HDHomeRunGuide()
	if err != nil || !g.Enabled {
		return false
	}
	devs, err := s.store.ListDevices()
	return err == nil && len(devs) > 0
}

func (s *Service) superviseHDHomeRun(ctx context.Context) {
	var next time.Time // zero until the first configured tick
	for {
		if ctx.Err() != nil {
			return
		}
		if !s.hdhomerunConfigured() {
			s.clearFailure(sourceHDHomeRun)
			if !s.sleepOrDone(ctx, sourceHDHomeRun, unconfiguredPoll) {
				return
			}
			continue
		}
		now := s.now()
		if next.IsZero() {
			next = s.hdhomerunFirstDue(now)
		}
		if now.Before(next) {
			if !s.sleepOrDone(ctx, sourceHDHomeRun, next.Sub(now)) {
				return
			}
			continue
		}

		if err := s.refreshHDHomeRun(ctx); err != nil {
			log.Printf("epg hdhomerun refresh: %v", err)
			next = s.now().Add(withJitter(hdhomerunRetry))
		} else {
			if perr := s.store.PrunePrograms(s.now().Add(-24 * time.Hour)); perr != nil {
				log.Printf("epg prune: %v", perr)
			}
			next = s.now().Add(hdhomerunInterval())
		}
	}
}

// hdhomerunFirstDue: fetch now unless a restart follows a recent success, in
// which case wait out the rest of a fresh 20-28 h interval.
func (s *Service) hdhomerunFirstDue(now time.Time) time.Time {
	v, err := s.store.GetSetting(settingHDHomeRunLastSuccess)
	if err != nil || v == "" {
		return now
	}
	last, err := time.Parse(time.RFC3339, v)
	if err != nil || now.Sub(last) >= hdhomerunMinInterval {
		return now
	}
	return last.Add(hdhomerunInterval())
}

func (s *Service) refreshHDHomeRun(ctx context.Context) error {
	err := s.doRefreshHDHomeRun(ctx)
	s.recordResult(sourceHDHomeRun, err, settingHDHomeRunLastSuccess, settingHDHomeRunLastError)
	return err
}

func (s *Service) doRefreshHDHomeRun(ctx context.Context) error {
	auth, err := s.collectDeviceAuth(ctx)
	if err != nil {
		return err
	}
	tv, err := s.fetchHDHomeRunGuide(ctx, auth)
	if err != nil {
		return err
	}
	chans, progs, skipped := xmltv.ToStore(tv)
	if skipped > 0 {
		log.Printf("epg hdhomerun: skipped %d programmes with bad times", skipped)
	}
	for i := range chans {
		chans[i].ID = hdhomerunIDPrefix + chans[i].ID
		chans[i].Source = sourceHDHomeRun
	}
	for i := range progs {
		progs[i].EPGChannelID = hdhomerunIDPrefix + progs[i].EPGChannelID
	}
	if err := s.store.ReplaceEPG(sourceHDHomeRun, chans, progs); err != nil {
		return fmt.Errorf("store: %w", err)
	}
	if err := s.autoMapHDHomeRun(tv.Channels); err != nil {
		return fmt.Errorf("map channels: %w", err)
	}
	return nil
}

// collectDeviceAuth concatenates the current DeviceAuth of every reachable
// stored tuner, in DeviceID order. It rotates, so it is read fresh each time.
func (s *Service) collectDeviceAuth(ctx context.Context) (string, error) {
	devs, err := s.store.ListDevices()
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, d := range devs {
		info, err := hdhr.FetchDiscover(ctx, hdhr.HTTPBaseURL(d.IP, d.StreamPort))
		if err != nil {
			log.Printf("epg hdhomerun: tuner %s unreachable: %v", d.DeviceID, err)
			continue
		}
		b.WriteString(strings.TrimSpace(info.DeviceAuth))
	}
	if b.Len() == 0 {
		return "", errNoHDHomeRun
	}
	return b.String(), nil
}

func (s *Service) fetchHDHomeRunGuide(ctx context.Context, deviceAuth string) (*xmltv.TV, error) {
	base := s.hdhrGuideURL
	if base == "" {
		base = defaultHDHomeRunGuideURL
	}
	u, err := url.Parse(base)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("DeviceAuth", deviceAuth)
	u.RawQuery = q.Encode()

	ctx, cancel := context.WithTimeout(ctx, hdhomerunFetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	// Asking for gzip explicitly turns off Go's transparent decompression,
	// so the body is unwrapped below.
	req.Header.Set("Accept-Encoding", "gzip")
	client := s.http
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		// The URL carries DeviceAuth; keep it out of logs and status.
		var uerr *url.Error
		if errors.As(err, &uerr) {
			err = uerr.Err
		}
		return nil, fmt.Errorf("fetch: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch: HTTP %d", resp.StatusCode)
	}
	body, err := maybeGunzip(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("gzip: %w", err)
	}
	tv, err := xmltv.Parse(body)
	if err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	return tv, nil
}

// maybeGunzip unwraps a gzip body, detected by its magic bytes (so a missing
// or wrong Content-Encoding header does not matter).
func maybeGunzip(r io.Reader) (io.Reader, error) {
	br := bufio.NewReader(r)
	if magic, err := br.Peek(2); err == nil && magic[0] == 0x1f && magic[1] == 0x8b {
		return gzip.NewReader(br)
	}
	// Plain (or short) body: the XML parser reports anything malformed.
	return br, nil
}

// autoMapHDHomeRun points each unmapped channel at the guide channel carrying
// its guide number. Existing mappings (hand-made or earlier) are never
// changed.
func (s *Service) autoMapHDHomeRun(guide []xmltv.Channel) error {
	chans, err := s.store.ListChannels(false)
	if err != nil {
		return err
	}
	for _, c := range chans {
		if c.EPGChannelID != "" {
			continue
		}
		id := matchGuideNumber(guide, c.GuideNumber)
		if id == "" {
			continue
		}
		// Only fills a never-mapped channel; Enabled and admin choices stay.
		if _, err := s.store.AutoMapChannel(c.ID, hdhomerunIDPrefix+id); err != nil {
			return err
		}
	}
	return nil
}

// matchGuideNumber returns the id of the guide channel whose display-name or
// lcn equals guideNumber ("9.1"), else one whose display-name starts with
// "9.1 " ("9.1 KMSP"), else "".
func matchGuideNumber(guide []xmltv.Channel, guideNumber string) string {
	g := strings.TrimSpace(guideNumber)
	if g == "" {
		return ""
	}
	for _, ch := range guide {
		for _, n := range append(append([]string{}, ch.DisplayNames...), ch.LCNs...) {
			if strings.TrimSpace(n) == g {
				return ch.ID
			}
		}
	}
	for _, ch := range guide {
		for _, n := range ch.DisplayNames {
			if strings.HasPrefix(strings.TrimSpace(n), g+" ") {
				return ch.ID
			}
		}
	}
	return ""
}

// ClearHDHomeRun removes the free guide's programs and the channel mappings it
// made (call when the source is turned off). Admin mappings stay.
func (s *Service) ClearHDHomeRun() error {
	if err := s.store.ReplaceEPG(sourceHDHomeRun, nil, nil); err != nil {
		return err
	}
	return s.store.ClearMappingsWithPrefix(hdhomerunIDPrefix)
}

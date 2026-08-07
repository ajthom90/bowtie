// Package epg orchestrates XMLTV and Schedules Direct guide refresh and
// builds the viewer guide from store data.
package epg

import (
	"context"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/ajthom90/bowtie/server/internal/epg/sd"
	"github.com/ajthom90/bowtie/server/internal/epg/xmltv"
	"github.com/ajthom90/bowtie/server/internal/settings"
	"github.com/ajthom90/bowtie/server/internal/store"
)

const (
	settingXMLTVLastSuccess = "epg.xmltv.lastSuccess"
	settingXMLTVLastError   = "epg.xmltv.lastError"
	settingSDLastSuccess    = "epg.sd.lastSuccess"
	settingSDLastError      = "epg.sd.lastError"

	// unconfiguredPoll is how long each supervisor waits between provider
	// re-reads when its source is not configured (no error logging).
	unconfiguredPoll = 60 * time.Second
	// sdRefreshInterval is the Schedules Direct refresh period (no separate
	// settings key yet; re-evaluated each cycle for consistency with XMLTV).
	sdRefreshInterval = 12 * time.Hour
	sdScheduleDays    = 14
	defaultRefreshH   = 12
)

// Service refreshes EPG sources and serves guide data for enabled channels.
// Settings are read live from the provider on every supervisor tick and
// RefreshAll/Status call — no restart required for source changes.
type Service struct {
	store *store.Store
	prov  *settings.Provider
	sd    *sd.Client
	http  *http.Client
	now   func() time.Time
	// after is the sleep seam (default time.After). Tests inject a controllable
	// channel factory so supervisor loops never real-sleep.
	after func(time.Duration) <-chan time.Time

	waitMu   sync.Mutex
	lastWait map[string]time.Duration // source name → last computed wait
}

// NewService constructs an EPG service backed by store and runtime settings.
// Credentials and sources are read from prov on each refresh/tick; the sd
// client is constructed empty and filled per-call.
func NewService(st *store.Store, prov *settings.Provider) *Service {
	return &Service{
		store:    st,
		prov:     prov,
		http:     http.DefaultClient,
		now:      func() time.Time { return time.Now().UTC() },
		after:    time.After,
		lastWait: make(map[string]time.Duration),
		sd:       &sd.Client{},
	}
}

// SourceStatus is the admin-facing status of both EPG sources.
type SourceStatus struct {
	XMLTV SourceState `json:"xmltv"`
	SD    SourceState `json:"sd"`
}

// SourceState describes one EPG source's configuration and health.
type SourceState struct {
	Configured  bool      `json:"configured"`
	LastSuccess time.Time `json:"lastSuccess"`
	LastError   string    `json:"lastError"`
	Stale       bool      `json:"stale"` // configured && lastSuccess older than 2× interval
}

// GuideChannel is one enabled tuner channel with programmes in range.
type GuideChannel struct {
	ChannelID   int64          `json:"channelId"`
	GuideNumber string         `json:"guideNumber"`
	Name        string         `json:"name"`
	LogoURL     string         `json:"logoUrl"`
	Programs    []GuideProgram `json:"programs"`
}

// GuideProgram is a single programme block for the guide grid.
type GuideProgram struct {
	Start       time.Time `json:"start"`
	Stop        time.Time `json:"stop"`
	Title       string    `json:"title"`
	Subtitle    string    `json:"subtitle"`
	Description string    `json:"description"`
	Category    string    `json:"category"`
}

// RefreshAll refreshes configured EPG sources (provider re-read per call), then prunes.
func (s *Service) RefreshAll(ctx context.Context) error {
	var errs []string
	if s.xmltvConfigured() {
		if err := s.refreshXMLTV(ctx); err != nil {
			errs = append(errs, "xmltv: "+err.Error())
		}
	}
	if s.sdConfigured() {
		if err := s.refreshSD(ctx); err != nil {
			errs = append(errs, "sd: "+err.Error())
		}
	}
	now := s.now()
	if err := s.store.PrunePrograms(now.Add(-24 * time.Hour)); err != nil {
		errs = append(errs, "prune: "+err.Error())
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

// Run starts always-on supervisor loops for XMLTV and SD until ctx is cancelled,
// then waits for both loops to exit (so shutdown does not race the store).
// Each loop re-reads the provider every iteration: unconfigured sources poll
// every 60s without error spam; configured sources refresh then sleep a
// jittered interval re-read on the next cycle.
func (s *Service) Run(ctx context.Context) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		s.superviseXMLTV(ctx)
	}()
	go func() {
		defer wg.Done()
		s.superviseSD(ctx)
	}()
	wg.Wait()
}

func (s *Service) superviseXMLTV(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		x, err := s.prov.XMLTV()
		if err != nil {
			log.Printf("epg xmltv: read settings: %v", err)
			if !s.sleepOrDone(ctx, "xmltv", unconfiguredPoll) {
				return
			}
			continue
		}
		if strings.TrimSpace(x.Source) == "" {
			if !s.sleepOrDone(ctx, "xmltv", unconfiguredPoll) {
				return
			}
			continue
		}

		if err := s.refreshXMLTV(ctx); err != nil {
			log.Printf("epg xmltv refresh: %v", err)
		} else if perr := s.store.PrunePrograms(s.now().Add(-24 * time.Hour)); perr != nil {
			log.Printf("epg prune: %v", perr)
		}

		hours := x.RefreshHours
		if hours <= 0 {
			hours = defaultRefreshH
		}
		wait := withJitter(time.Duration(hours) * time.Hour)
		if !s.sleepOrDone(ctx, "xmltv", wait) {
			return
		}
	}
}

func (s *Service) superviseSD(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		sdCfg, err := s.prov.SD()
		if err != nil {
			log.Printf("epg sd: read settings: %v", err)
			if !s.sleepOrDone(ctx, "sd", unconfiguredPoll) {
				return
			}
			continue
		}
		if !sdCredentialsConfigured(sdCfg) {
			if !s.sleepOrDone(ctx, "sd", unconfiguredPoll) {
				return
			}
			continue
		}

		if err := s.refreshSD(ctx); err != nil {
			log.Printf("epg sd refresh: %v", err)
		} else if perr := s.store.PrunePrograms(s.now().Add(-24 * time.Hour)); perr != nil {
			log.Printf("epg prune: %v", perr)
		}

		wait := withJitter(sdRefreshInterval)
		if !s.sleepOrDone(ctx, "sd", wait) {
			return
		}
	}
}

// sleepOrDone records the wait for tests, then blocks until after(d) or ctx done.
// Returns false when ctx is cancelled (checked again after a timer fire so a
// concurrent cancel+advance does not start another refresh cycle).
func (s *Service) sleepOrDone(ctx context.Context, source string, d time.Duration) bool {
	if ctx.Err() != nil {
		return false
	}
	s.recordWait(source, d)
	after := s.after
	if after == nil {
		after = time.After
	}
	select {
	case <-ctx.Done():
		return false
	case <-after(d):
		return ctx.Err() == nil
	}
}

func (s *Service) recordWait(source string, d time.Duration) {
	s.waitMu.Lock()
	s.lastWait[source] = d
	s.waitMu.Unlock()
}

// lastWaitFor returns the most recently recorded wait for source (test helper).
func (s *Service) lastWaitFor(source string) time.Duration {
	s.waitMu.Lock()
	defer s.waitMu.Unlock()
	return s.lastWait[source]
}

// Status returns configured flag (live from provider), last success/error, and stale.
func (s *Service) Status() SourceStatus {
	return SourceStatus{
		XMLTV: s.sourceState(s.xmltvConfigured(), s.xmltvInterval(), settingXMLTVLastSuccess, settingXMLTVLastError),
		SD:    s.sourceState(s.sdConfigured(), sdRefreshInterval, settingSDLastSuccess, settingSDLastError),
	}
}

func (s *Service) sourceState(configured bool, interval time.Duration, successKey, errorKey string) SourceState {
	st := SourceState{Configured: configured}
	if v, err := s.store.GetSetting(successKey); err == nil && v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			st.LastSuccess = t.UTC()
		}
	}
	if v, err := s.store.GetSetting(errorKey); err == nil {
		st.LastError = v
	}
	if configured {
		// Zero LastSuccess is older than any interval → stale.
		st.Stale = s.now().Sub(st.LastSuccess) > 2*interval
	}
	return st
}

// Guide returns enabled channels with programmes overlapping [start, stop).
// Unmapped channels are included with an empty Programs slice.
// Uses one ProgramsInRange query for all mapped EPG IDs and groups in memory.
func (s *Service) Guide(ctx context.Context, start, stop time.Time) ([]GuideChannel, error) {
	_ = ctx
	chans, err := s.store.ListChannels(true)
	if err != nil {
		return nil, err
	}
	if len(chans) == 0 {
		return []GuideChannel{}, nil
	}

	epgIcons := map[string]string{}
	epgList, err := s.store.ListEPGChannels()
	if err != nil {
		return nil, err
	}
	for _, e := range epgList {
		epgIcons[e.ID] = e.IconURL
	}

	var mappedIDs []string
	seen := map[string]bool{}
	for _, c := range chans {
		if c.EPGChannelID == "" || seen[c.EPGChannelID] {
			continue
		}
		seen[c.EPGChannelID] = true
		mappedIDs = append(mappedIDs, c.EPGChannelID)
	}

	progsByEPG := map[string][]GuideProgram{}
	if len(mappedIDs) > 0 {
		progs, err := s.store.ProgramsInRange(mappedIDs, start, stop)
		if err != nil {
			return nil, err
		}
		for _, p := range progs {
			progsByEPG[p.EPGChannelID] = append(progsByEPG[p.EPGChannelID], GuideProgram{
				Start:       p.Start,
				Stop:        p.Stop,
				Title:       p.Title,
				Subtitle:    p.Subtitle,
				Description: p.Description,
				Category:    p.Category,
			})
		}
	}

	out := make([]GuideChannel, 0, len(chans))
	for _, c := range chans {
		logo := ""
		if c.EPGChannelID != "" {
			logo = epgIcons[c.EPGChannelID]
		}
		programs := progsByEPG[c.EPGChannelID]
		if programs == nil {
			programs = []GuideProgram{}
		}
		out = append(out, GuideChannel{
			ChannelID:   c.ID,
			GuideNumber: c.GuideNumber,
			Name:        c.Name,
			LogoURL:     logo,
			Programs:    programs,
		})
	}
	return out, nil
}

func (s *Service) xmltvConfigured() bool {
	x, err := s.prov.XMLTV()
	if err != nil {
		return false
	}
	return strings.TrimSpace(x.Source) != ""
}

func (s *Service) sdConfigured() bool {
	sdCfg, err := s.prov.SD()
	if err != nil {
		return false
	}
	return sdCredentialsConfigured(sdCfg)
}

func sdCredentialsConfigured(sdCfg settings.SD) bool {
	return strings.TrimSpace(sdCfg.Username) != "" &&
		strings.TrimSpace(sdCfg.Password) != "" &&
		strings.TrimSpace(sdCfg.LineupID) != ""
}

func (s *Service) xmltvInterval() time.Duration {
	x, err := s.prov.XMLTV()
	if err != nil {
		return time.Duration(defaultRefreshH) * time.Hour
	}
	h := x.RefreshHours
	if h <= 0 {
		h = defaultRefreshH
	}
	return time.Duration(h) * time.Hour
}

func (s *Service) refreshXMLTV(ctx context.Context) error {
	err := s.doRefreshXMLTV(ctx)
	s.recordResult("xmltv", err, settingXMLTVLastSuccess, settingXMLTVLastError)
	return err
}

func (s *Service) doRefreshXMLTV(ctx context.Context) error {
	x, err := s.prov.XMLTV()
	if err != nil {
		return err
	}
	src := strings.TrimSpace(x.Source)
	if src == "" {
		return fmt.Errorf("xmltv source not configured")
	}
	r, closer, err := s.openXMLTVSource(ctx, src)
	if err != nil {
		return err
	}
	defer closer()

	tv, err := xmltv.Parse(r)
	if err != nil {
		return fmt.Errorf("parse: %w", err)
	}
	chans, progs, skipped := xmltv.ToStore(tv)
	if skipped > 0 {
		log.Printf("epg xmltv: skipped %d programmes with bad times", skipped)
	}
	if err := s.store.ReplaceEPG("xmltv", chans, progs); err != nil {
		return fmt.Errorf("store: %w", err)
	}
	return nil
}

func (s *Service) openXMLTVSource(ctx context.Context, src string) (io.Reader, func(), error) {
	if strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://") {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, src, nil)
		if err != nil {
			return nil, nil, err
		}
		client := s.http
		if client == nil {
			client = http.DefaultClient
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, nil, fmt.Errorf("fetch: %w", err)
		}
		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			return nil, nil, fmt.Errorf("fetch: HTTP %d", resp.StatusCode)
		}
		return resp.Body, func() { _ = resp.Body.Close() }, nil
	}
	f, err := os.Open(src)
	if err != nil {
		return nil, nil, fmt.Errorf("open file: %w", err)
	}
	return f, func() { _ = f.Close() }, nil
}

func (s *Service) refreshSD(ctx context.Context) error {
	err := s.doRefreshSD(ctx)
	s.recordResult("sd", err, settingSDLastSuccess, settingSDLastError)
	return err
}

func (s *Service) doRefreshSD(ctx context.Context) error {
	if s.sd == nil {
		return fmt.Errorf("sd client not configured")
	}
	sdCfg, err := s.prov.SD()
	if err != nil {
		return err
	}
	s.sd.Username = sdCfg.Username
	s.sd.Password = sdCfg.Password

	if err := s.sd.Token(ctx); err != nil {
		return fmt.Errorf("token: %w", err)
	}
	lineupID := strings.TrimSpace(sdCfg.LineupID)
	lineup, err := s.sd.Lineup(ctx, lineupID)
	if err != nil {
		return fmt.Errorf("lineup: %w", err)
	}

	stationIDs := uniqueStationIDs(lineup)
	dates := scheduleDates(s.now(), sdScheduleDays)
	scheds, err := s.sd.Schedules(ctx, stationIDs, dates)
	if err != nil {
		return fmt.Errorf("schedules: %w", err)
	}

	progIDs := uniqueProgramIDs(scheds)
	details, err := s.sd.Programs(ctx, progIDs)
	if err != nil {
		return fmt.Errorf("programs: %w", err)
	}

	chans, progs := sd.ToStore(lineup, scheds, details)
	if err := s.store.ReplaceEPG("sd", chans, progs); err != nil {
		return fmt.Errorf("store: %w", err)
	}
	return nil
}

func (s *Service) recordResult(name string, err error, successKey, errorKey string) {
	if err != nil {
		if setErr := s.store.SetSetting(errorKey, err.Error()); setErr != nil {
			log.Printf("epg %s: persist lastError: %v", name, setErr)
		}
		return
	}
	now := s.now().UTC().Format(time.RFC3339)
	if setErr := s.store.SetSetting(successKey, now); setErr != nil {
		log.Printf("epg %s: persist lastSuccess: %v", name, setErr)
	}
	if setErr := s.store.SetSetting(errorKey, ""); setErr != nil {
		log.Printf("epg %s: clear lastError: %v", name, setErr)
	}
}

func uniqueStationIDs(lineup sd.Lineup) []string {
	seen := map[string]bool{}
	var ids []string
	for _, m := range lineup.Map {
		if m.StationID == "" || seen[m.StationID] {
			continue
		}
		seen[m.StationID] = true
		ids = append(ids, m.StationID)
	}
	// Fall back to stations list if map is empty.
	if len(ids) == 0 {
		for _, st := range lineup.Stations {
			if st.StationID == "" || seen[st.StationID] {
				continue
			}
			seen[st.StationID] = true
			ids = append(ids, st.StationID)
		}
	}
	return ids
}

func uniqueProgramIDs(scheds []sd.StationSchedule) []string {
	seen := map[string]bool{}
	var ids []string
	for _, sched := range scheds {
		for _, p := range sched.Programs {
			if p.ProgramID == "" || seen[p.ProgramID] {
				continue
			}
			seen[p.ProgramID] = true
			ids = append(ids, p.ProgramID)
		}
	}
	return ids
}

func scheduleDates(now time.Time, days int) []string {
	now = now.UTC()
	out := make([]string, days)
	for i := 0; i < days; i++ {
		out[i] = now.AddDate(0, 0, i).Format("2006-01-02")
	}
	return out
}

// withJitter returns d ±10%.
func withJitter(d time.Duration) time.Duration {
	if d <= 0 {
		return d
	}
	// jitter range is 20% of d (±10%).
	j := int64(d) / 10
	if j <= 0 {
		return d
	}
	offset := rand.Int63n(2*j+1) - j
	return d + time.Duration(offset)
}

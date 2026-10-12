//go:build ffmpeg && realsoak

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ajthom90/bowtie/server/internal/testplayer"
)

// TestRealSoak watches one real channel on a running Bowtie with the virtual
// player following every rendition. It fails on what a viewer would notice,
// and on any FFmpeg restart, session end, stuck kill, or false timestamp
// re-base.
//
// With none of the BOWTIE_SOAK_* variables set, the test skips. It never
// names a host; the controller's run points it at a server already started.
//
//	BOWTIE_SOAK_URL         Bowtie base URL
//	BOWTIE_SOAK_USER        admin username
//	BOWTIE_SOAK_PASS        admin password
//	BOWTIE_SOAK_CHANNEL     guide number
//	BOWTIE_SOAK_FOR         duration (default 1h)
//	BOWTIE_SOAK_DEVICE      HDHomeRun address to add first (optional)
//	BOWTIE_SOAK_SERVER_LOG  file that `docker logs -t -f` is writing
//	FF51_LOGDIR             re-base logs written by scripts/ffmpeg-soak
func TestRealSoak(t *testing.T) {
	need := func(k string) string {
		v := os.Getenv(k)
		if v == "" {
			t.Skipf("skipping: %s not set (real-broadcast soak is opt-in)", k)
		}
		return v
	}
	base := strings.TrimRight(need("BOWTIE_SOAK_URL"), "/")
	user, pass, guide := need("BOWTIE_SOAK_USER"), need("BOWTIE_SOAK_PASS"), need("BOWTIE_SOAK_CHANNEL")
	serverLog := need("BOWTIE_SOAK_SERVER_LOG")
	need("FF51_LOGDIR")
	dur := time.Hour
	if v := os.Getenv("BOWTIE_SOAK_FOR"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			t.Fatal(err)
		}
		dur = d
	}

	c := &soakClient{t: t, base: base, client: &http.Client{Timeout: 30 * time.Second}}
	var login struct {
		AccessToken string `json:"accessToken"`
	}
	c.do(http.MethodPost, "/api/v1/auth/login", map[string]string{"username": user, "password": pass}, &login, false)
	if login.AccessToken == "" {
		t.Fatal("login returned no access token")
	}
	c.token = login.AccessToken
	if dev := os.Getenv("BOWTIE_SOAK_DEVICE"); dev != "" {
		// Optional. A refusal is logged and the soak continues: the device
		// may already be in the lineup.
		c.do(http.MethodPost, "/api/v1/admin/devices", map[string]string{"ip": dev}, nil, true)
	}
	var chans []struct {
		ID          int64  `json:"id"`
		GuideNumber string `json:"guideNumber"`
	}
	c.do(http.MethodGet, "/api/v1/admin/channels", nil, &chans, false)
	var id int64
	for _, ch := range chans {
		if ch.GuideNumber == guide {
			id = ch.ID
		}
	}
	if id == 0 {
		t.Fatalf("channel %s not in the lineup", guide)
	}
	c.do(http.MethodPatch, fmt.Sprintf("/api/v1/admin/channels/%d", id), map[string]bool{"enabled": true}, nil, false)

	var mu sync.Mutex
	var events []testplayer.SegmentEvent
	start := time.Now()
	p, err := testplayer.Start(context.Background(), testplayer.Config{
		BaseURL: base, Token: c.token, ChannelID: id, AllRenditions: true, Probe: FFprobe(ffprobePath()),
		// AC-3 too, so the copy rendition (when the channel has AC-3) is soaked.
		Caps: map[string]any{"videoCodecs": []string{"h264"}, "audioCodecs": []string{"aac", "ac3"}, "maxHeight": 1080},
		OnSegment: func(e testplayer.SegmentEvent) {
			mu.Lock()
			events = append(events, e)
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Window 0 is the moment before this Mark, so coverage checks start with
	// the first real window. Later marks follow soakMarkOffsets: every
	// soakWindowPeriod, except a tick that would leave a stub shorter than
	// soakMinWindow.
	p.Mark()
	ctx, cancel := context.WithTimeout(context.Background(), dur)
	defer cancel()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		tick := time.NewTicker(soakWindowPeriod)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-tick.C:
				if dl, ok := ctx.Deadline(); ok && markSoakWindow(time.Until(dl)) {
					p.Mark()
				}
				r := p.Report()
				t.Logf("%s segments=%d excess=%.2fs audioHole=%v avSkew=%v (%d pairs) startAV=%v measured=%d/%d unmeasured=%v backward=%d fetchErrors=%d tailGap=%v",
					now.UTC().Format(time.RFC3339), r.SegmentsFetched, r.TimelineExcess, r.MaxAudioHole, r.MaxAVStartSkew, r.AVPairs,
					r.MaxStartAVOffset, r.StartAVMeasured, r.StartAVRuns, r.UnmeasuredStartAV,
					r.BackwardJumps, r.FetchErrors, r.TailGap)
			}
		}
	}()
	p.Run(ctx)
	wg.Wait()
	r := p.Report()
	p.Stop(context.Background())
	end := time.Now()

	t.Logf("renditions=%v segments=%d fetchErrors=%d (segments %d, playlists %d, probes %d) excess=%.2fs audioHoleBy=%v avSkew=%v (%d pairs) startAV=%v measured=%d/%d unmeasured=%v probed=%d/%d maxGap=%v tailGap=%v cues=%d (%d mistimed)",
		r.Renditions, r.SegmentsFetched, r.FetchErrors, r.SegmentErrors, r.PlaylistErrors, r.ProbeErrors, r.TimelineExcess, r.AudioHoleBy,
		r.MaxAVStartSkew, r.AVPairs, r.MaxStartAVOffset, r.StartAVMeasured, r.StartAVRuns, r.UnmeasuredStartAV,
		r.ProbedAudio, r.ProbedVideo, r.MaxGap, r.TailGap, r.Cues, r.CueErrors)
	for _, pr := range r.Consistency.Problems {
		t.Logf("consistency: %s", pr)
	}
	for _, e := range r.Errors {
		t.Logf("player error: %s", e)
	}
	cs := r.Consistency
	if r.SessionGone || r.EndList {
		t.Error("session ended during the soak")
	}
	if r.BackwardJumps > 0 || !cs.RenditionsMonotonic {
		t.Error("media sequence went backwards")
	}
	if !cs.PDTMonotonic || !cs.PDTAligned || !cs.DiscontinuitiesAligned || !cs.DiscontinuitySequenceOK {
		t.Errorf("timeline consistency: %+v", cs)
	}
	if r.TimelineExcess > 2 || r.TimelineExcess < -2 {
		t.Errorf("timeline excess %.2fs, want within ±2s", r.TimelineExcess)
	}
	if r.ProbedAudio == 0 || r.MaxAudioHole > 50*time.Millisecond {
		t.Errorf("audio hole %v (by rendition %v, probed %d), want ≤ 50ms", r.MaxAudioHole, r.AudioHoleBy, r.ProbedAudio)
	}
	if r.AVPairs == 0 || r.MaxAVStartSkew > 100*time.Millisecond {
		t.Errorf("A/V start skew %v over %d pairs, want ≤ 100ms over some", r.MaxAVStartSkew, r.AVPairs)
	}
	if r.StartAVRuns == 0 || r.StartAVMeasured != r.StartAVRuns || len(r.UnmeasuredStartAV) != 0 || r.MaxStartAVOffset > 100*time.Millisecond {
		t.Errorf("start A/V offset %v over %d measured of %d runs (unmeasured %v), want ≤ 100ms with every run measured",
			r.MaxStartAVOffset, r.StartAVMeasured, r.StartAVRuns, r.UnmeasuredStartAV)
	}
	if r.FetchErrors > 0 {
		t.Errorf("fetch errors %d: playlists %d, segments %d, probes %d", r.FetchErrors, r.PlaylistErrors, r.SegmentErrors, r.ProbeErrors)
	}
	for _, name := range r.Renditions {
		if r.RenditionSegments[name] == 0 {
			t.Errorf("rendition %s delivered no segments", name)
		}
		if g := r.TailGap[name]; g > 12*time.Second {
			t.Errorf("rendition %s stopped %v before the end of the soak", name, g.Round(time.Second))
		}
	}
	for _, u := range r.UnpairedAfterFaults() {
		t.Errorf("coverage: %s", u)
	}

	for _, hit := range serverLogHits(t, serverLog, start, end, soakFaultNeedles) {
		t.Errorf("server log: %s", hit.Line)
	}

	rebases := rebasesSince(t, start)
	t.Logf("re-bases: %d (%.2f per minute)", len(rebases), float64(len(rebases))/dur.Minutes())
	mu.Lock()
	defer mu.Unlock()
	for _, rb := range rebases {
		if why := falseRebase(start.Add(rb.At), events); why != "" {
			t.Errorf("false re-base at +%v (%s): %s", rb.At.Round(time.Second), why, rb.Line)
		} else {
			t.Logf("true re-base at +%v: %s", rb.At.Round(time.Second), rb.Line)
		}
	}
}

type soakClient struct {
	t      *testing.T
	base   string
	token  string
	client *http.Client
}

func (c *soakClient) do(method, path string, body, out any, allowFail bool) {
	c.t.Helper()
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			c.t.Fatal(err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, c.base+path, rdr)
	if err != nil {
		c.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		c.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		if allowFail {
			c.t.Logf("%s %s: HTTP %d (ignored)", method, path, resp.StatusCode)
			return
		}
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		c.t.Fatalf("%s %s: HTTP %d: %s", method, path, resp.StatusCode, bytes.TrimSpace(msg))
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			c.t.Fatalf("%s %s: decode: %v", method, path, err)
		}
		return
	}
	_, _ = io.Copy(io.Discard, resp.Body)
}

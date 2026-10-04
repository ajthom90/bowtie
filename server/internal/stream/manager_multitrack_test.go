package stream

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/ajthom90/bowtie/server/internal/settings"
	"github.com/ajthom90/bowtie/server/internal/store"
	"github.com/ajthom90/bowtie/server/internal/transcode"
)

// vtCaps is a hardware backend (ladders apply; libx264 never ladders).
func vtCaps() transcode.Capabilities {
	return transcode.Capabilities{
		Available: []transcode.Backend{transcode.BackendVideoToolbox},
		HEVC:      map[transcode.Backend]bool{},
	}
}

// newMultitrackManager builds a manager with captions/extra audio on, the
// shared ladder on or off, and a device that sends PAT/PMT(eng, spa AC-3) plus
// a 720-line MPEG-2 sequence header, then blocks.
func newMultitrackManager(t *testing.T, adaptive bool) (*Manager, *IngestManager, *stubRunner, int64) {
	t.Helper()
	st, cfg, clock, runner, chID, _ := setupEnv(t)
	body := probeBody(audioES(0x81, 0x34, "eng", 0), audioES(0x81, 0x35, "spa", 0))
	im, _ := newManagerIngest(clock, func(context.Context, string) (io.ReadCloser, int, error) {
		return newBlockingBody(body), 200, nil
	})
	prov := settings.NewProvider(st)
	if err := prov.SeedFromConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if err := prov.SetStreaming(settings.Streaming{BufferMinutes: 15, Adaptive: adaptive}); err != nil {
		t.Fatal(err)
	}
	m := NewManager(ManagerDeps{
		Cfg:   cfg,
		Store: st,
		StreamURL: func(ch store.Channel) (string, error) {
			return "http://127.0.0.1:5004/auto/v" + ch.GuideNumber, nil
		},
		Caps:              vtCaps(),
		Runner:            runner,
		Clock:             clock.Now,
		Settings:          prov,
		Ingest:            im,
		Multitrack:        true,
		TrackProbeTimeout: time.Second,
	})
	t.Cleanup(im.Shutdown)
	return m, im, runner, chID
}

func viewer(name, maxQuality string) store.User {
	return store.User{ID: 1, Username: name, Role: "viewer", MaxQuality: maxQuality}
}

func TestLadderModeSharesOneSessionAcrossQualities(t *testing.T) {
	m, im, runner, chID := newMultitrackManager(t, true)
	h1, err := m.Start(context.Background(), viewer("alice", ""), chID, clientCaps(""))
	if err != nil {
		t.Fatal(err)
	}
	h2, err := m.Start(context.Background(), viewer("bob", "low"), chID, clientCaps("high"))
	if err != nil {
		t.Fatal(err)
	}
	if runner.Starts() != 1 || h1.SessionID != h2.SessionID {
		t.Fatalf("starts=%d sessions %s %s: want one ladder per channel", runner.Starts(), h1.SessionID, h2.SessionID)
	}
	spec := runner.LastSpec()
	if len(spec.Layout.Rungs) != 3 || !spec.Layout.Captions || len(spec.Layout.AC3Tracks()) != 2 || spec.CaptionInput == nil {
		t.Fatalf("layout=%+v captionInput=%v", spec.Layout, spec.CaptionInput != nil)
	}
	m1, ok1 := m.SessionMediaOf(h1.ViewerID)
	m2, ok2 := m.SessionMediaOf(h2.ViewerID)
	if !ok1 || !ok2 || m1.MaxHeight != 1080 || m2.MaxHeight != 480 {
		t.Fatalf("ceilings %d %d (ok %v %v)", m1.MaxHeight, m2.MaxHeight, ok1, ok2)
	}
	if n := im.attachCalls.Load(); n != 2 {
		t.Fatalf("attach calls=%d want 2 (video + caption tap)", n)
	}
}

func TestSwitchOffKeysByProfile(t *testing.T) {
	m, _, runner, chID := newMultitrackManager(t, false)
	if _, err := m.Start(context.Background(), viewer("a", ""), chID, clientCaps("")); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Start(context.Background(), viewer("b", ""), chID, clientCaps("low")); err != nil {
		t.Fatal(err)
	}
	if runner.Starts() != 2 {
		t.Fatalf("switch off: different profiles are separate sessions; starts=%d", runner.Starts())
	}
	if r := runner.Specs()[0].Layout.Rungs; len(r) != 1 || r[0].Height != 720 {
		t.Fatalf("original on a 720 source must not upscale: %+v", r)
	}
}

func TestAudioModeNoLongerSplitsSessions(t *testing.T) {
	m, _, runner, chID := newMultitrackManager(t, false)
	ac3 := clientCaps("")
	ac3.AudioCodecs = []string{"aac", "ac3"}
	if _, err := m.Start(context.Background(), viewer("a", ""), chID, ac3); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Start(context.Background(), viewer("b", ""), chID, clientCaps("")); err != nil {
		t.Fatal(err)
	}
	if runner.Starts() != 1 {
		t.Fatalf("AC-3 and AAC clients must share; starts=%d", runner.Starts())
	}
}

func TestFallbackToSafeLayout(t *testing.T) {
	m, _, runner, chID := newMultitrackManager(t, true)
	runner.onStart = func(spec transcode.JobSpec) {
		if !spec.Layout.IsSafe() {
			runner.failNext = true
		}
	}
	if _, err := m.Start(context.Background(), viewer("a", ""), chID, clientCaps("")); err != nil {
		t.Fatalf("Start: %v", err)
	}
	specs := runner.Specs()
	if len(specs) != 2 || specs[0].Layout.IsSafe() || !specs[1].Layout.IsSafe() || specs[1].CaptionInput != nil {
		t.Fatalf("specs=%+v", specs)
	}
}

func TestCaptionTapForceCloseKeepsFFmpeg(t *testing.T) {
	m, _, runner, chID := newMultitrackManager(t, false)
	if _, err := m.Start(context.Background(), viewer("a", ""), chID, clientCaps("")); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	var tap *IngestSub
	for _, s := range m.sessions {
		tap = s.capSub
	}
	m.mu.Unlock()
	if tap == nil {
		t.Fatal("no caption tap")
	}
	tap.forceClose()
	if runner.LastProc().Stopped() {
		t.Fatal("caption tap force-close stopped FFmpeg")
	}
}

func TestMultitrackOffIsVideoPlusFirstAudio(t *testing.T) {
	st, cfg, clock, runner, chID, user := setupEnv(t)
	m, im, _ := newTestManagerWithDial(st, cfg, clock, runner, nil)
	if _, err := m.Start(context.Background(), user, chID, clientCaps("")); err != nil {
		t.Fatal(err)
	}
	if n := im.attachCalls.Load(); n != 1 || !runner.LastSpec().Layout.IsSafe() {
		t.Fatalf("attach=%d layout=%+v", n, runner.LastSpec().Layout)
	}
}

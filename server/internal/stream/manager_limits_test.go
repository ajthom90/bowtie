package stream

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ajthom90/bowtie/server/internal/store"
)

// limitsEnv is setupEnv with three enabled channels and a second account.
func limitsEnv(t *testing.T) (*Manager, *stubRunner, *fakeClock, []int64, store.User, store.User) {
	t.Helper()
	st, cfg, clock, runner, _, alice := setupEnv(t)
	if err := st.SyncLineup("dev1", []store.Channel{
		{DeviceID: "dev1", GuideNumber: "5.1", Name: "NEWS"},
		{DeviceID: "dev1", GuideNumber: "9.1", Name: "FOX"},
		{DeviceID: "dev1", GuideNumber: "11.1", Name: "PBS"},
	}); err != nil {
		t.Fatal(err)
	}
	chans, err := st.ListChannels(false)
	if err != nil || len(chans) != 3 {
		t.Fatalf("channels %v len=%d", err, len(chans))
	}
	var ids []int64
	for _, c := range chans {
		if err := st.UpdateChannel(c.ID, true, ""); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, c.ID)
	}
	bob := store.User{ID: alice.ID + 100, Username: "bob", Role: "viewer"}
	return newTestManager(st, cfg, clock, runner), runner, clock, ids, alice, bob
}

func wantLimit(t *testing.T, err error, kind string, limit int) {
	t.Helper()
	var le *UserLimitError
	if !errors.As(err, &le) || le.Kind != kind || le.Limit != limit {
		t.Fatalf("err=%v, want UserLimitError{%s %d}", err, kind, limit)
	}
}

func TestStreamLimitRejectsThirdViewer(t *testing.T) {
	m, _, _, ch, alice, _ := limitsEnv(t)
	alice.MaxStreams = 2
	for i := 0; i < 2; i++ {
		if _, err := m.Start(context.Background(), alice, ch[i], clientCaps("")); err != nil {
			t.Fatalf("start %d: %v", i, err)
		}
	}
	_, err := m.Start(context.Background(), alice, ch[0], clientCaps(""))
	wantLimit(t, err, "streams", 2)
	if want := "You're already watching on 2 devices — your account allows 2."; err.Error() != want {
		t.Fatalf("message %q", err.Error())
	}
}

func TestTunerLimitAllowsJoiningOthersChannel(t *testing.T) {
	m, _, _, ch, alice, bob := limitsEnv(t)
	alice.MaxTuners = 1
	if _, err := m.Start(context.Background(), bob, ch[1], clientCaps("")); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Start(context.Background(), alice, ch[0], clientCaps("")); err != nil {
		t.Fatalf("first tuner: %v", err)
	}
	if _, err := m.Start(context.Background(), alice, ch[1], clientCaps("")); err != nil {
		t.Fatalf("joining bob's channel costs no tuner: %v", err)
	}
	_, err := m.Start(context.Background(), alice, ch[2], clientCaps(""))
	wantLimit(t, err, "tuners", 1)
	if want := "Your account can use 1 tuner at a time. Stop another channel first."; err.Error() != want {
		t.Fatalf("message %q", err.Error())
	}
}

func TestTunerLimitCountsChannelOnceAcrossSessions(t *testing.T) {
	m, runner, _, ch, alice, _ := limitsEnv(t)
	alice.MaxTuners = 1
	if _, err := m.Start(context.Background(), alice, ch[0], clientCaps("")); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Start(context.Background(), alice, ch[0], clientCaps("low")); err != nil {
		t.Fatalf("second quality of the same channel: %v", err)
	}
	if runner.Starts() != 2 {
		t.Fatalf("starts=%d: want two per-profile sessions", runner.Starts())
	}
}

func TestAdminIgnoresLimits(t *testing.T) {
	m, _, _, ch, alice, _ := limitsEnv(t)
	alice.Role, alice.MaxStreams, alice.MaxTuners = "admin", 1, 1
	for i := 0; i < 3; i++ {
		if _, err := m.Start(context.Background(), alice, ch[i%2], clientCaps("")); err != nil {
			t.Fatalf("admin start %d: %v", i, err)
		}
	}
}

// A force-quit app never DELETEs; its viewer must not block the reopened app
// for the 90 s idle timeout.
func TestStaleViewerDoesNotCount(t *testing.T) {
	m, _, clock, ch, alice, _ := limitsEnv(t)
	alice.MaxStreams, alice.MaxTuners = 1, 1
	if _, err := m.Start(context.Background(), alice, ch[0], clientCaps("")); err != nil {
		t.Fatal(err)
	}
	clock.Advance(limitStaleAfter + time.Second)
	if _, err := m.Start(context.Background(), alice, ch[1], clientCaps("")); err != nil {
		t.Fatalf("stale viewer still counted: %v", err)
	}
}

func TestStopFreesSlot(t *testing.T) {
	m, _, _, ch, alice, _ := limitsEnv(t)
	alice.MaxStreams = 1
	h, err := m.Start(context.Background(), alice, ch[0], clientCaps(""))
	if err != nil {
		t.Fatal(err)
	}
	m.StopViewer(h.ViewerID)
	if _, err := m.Start(context.Background(), alice, ch[1], clientCaps("")); err != nil {
		t.Fatalf("zap after DELETE: %v", err)
	}
}

func TestFailedStartReleasesReservation(t *testing.T) {
	m, runner, _, ch, alice, _ := limitsEnv(t)
	alice.MaxStreams = 1
	runner.startErr = errors.New("ffmpeg missing")
	if _, err := m.Start(context.Background(), alice, ch[0], clientCaps("")); err == nil {
		t.Fatal("want start error")
	}
	runner.startErr = nil
	if _, err := m.Start(context.Background(), alice, ch[0], clientCaps("")); err != nil {
		t.Fatalf("failed start kept its slot: %v", err)
	}
}

func TestConcurrentStartsRespectLimit(t *testing.T) {
	m, _, _, ch, alice, _ := limitsEnv(t)
	alice.MaxStreams = 1
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = m.Start(context.Background(), alice, ch[i], clientCaps(""))
		}(i)
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
		} else {
			wantLimit(t, err, "streams", 1)
		}
	}
	if ok != 1 {
		t.Fatalf("errs=%v: want exactly one start", errs)
	}
}

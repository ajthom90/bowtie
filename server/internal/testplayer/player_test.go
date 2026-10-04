package testplayer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// scripted serves one playlist per poll (repeating the last), plus segments.
type scripted struct {
	mu          sync.Mutex
	playlists   []string
	polls       int
	heartbeats  int
	deleted     bool
	startStatus int
	segStatus   int
	gone        bool
	// master: index.m3u8 is a master playlist; media is served as v720.m3u8.
	master bool
}

func (s *scripted) handler(t *testing.T) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/sessions", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer jwt" {
			t.Errorf("session create auth = %q", r.Header.Get("Authorization"))
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["channelId"] != float64(7) {
			t.Errorf("channelId = %v", body["channelId"])
		}
		if s.startStatus != 0 {
			w.WriteHeader(s.startStatus)
			_, _ = w.Write([]byte(`{"error":"all tuners in use"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"viewerId": "v1", "playlistUrl": "/api/v1/stream/v1/index.m3u8?token=tok"})
	})
	media := func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.gone {
			http.Error(w, "viewer not found", http.StatusNotFound)
			return
		}
		i := s.polls
		if i >= len(s.playlists) {
			i = len(s.playlists) - 1
		}
		s.polls++
		_, _ = w.Write([]byte(s.playlists[i]))
	}
	mux.HandleFunc("GET /api/v1/stream/v1/index.m3u8", func(w http.ResponseWriter, r *http.Request) {
		if s.master {
			_, _ = w.Write([]byte("#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1\nv720.m3u8?token=tok\n"))
			return
		}
		media(w, r)
	})
	mux.HandleFunc("GET /api/v1/stream/v1/v720.m3u8", media)
	mux.HandleFunc("GET /api/v1/stream/v1/{seg}", func(w http.ResponseWriter, r *http.Request) {
		if s.segStatus != 0 {
			w.WriteHeader(s.segStatus)
			return
		}
		_, _ = w.Write([]byte{0x47, 0, 0, 0})
	})
	mux.HandleFunc("POST /api/v1/sessions/v1/heartbeat", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("token") != "tok" {
			t.Errorf("heartbeat token = %q", r.URL.Query().Get("token"))
		}
		s.mu.Lock()
		s.heartbeats++
		s.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("DELETE /api/v1/sessions/v1", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.deleted = true
		s.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	return mux
}

func playlist(seq int, n int, discAt int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:4\n#EXT-X-MEDIA-SEQUENCE:%d\n", seq)
	for i := 0; i < n; i++ {
		if seq+i == discAt {
			b.WriteString("#EXT-X-DISCONTINUITY\n")
		}
		fmt.Fprintf(&b, "#EXTINF:4.0,\n/api/v1/stream/v1/seg%05d.ts?token=tok\n", seq+i)
	}
	return b.String()
}

func run(t *testing.T, s *scripted, polls int, hb time.Duration) (*Player, Report) {
	t.Helper()
	srv := httptest.NewServer(s.handler(t))
	t.Cleanup(srv.Close)
	p, err := Start(context.Background(), Config{BaseURL: srv.URL, Token: "jwt", ChannelID: 7, PollEvery: 10 * time.Millisecond, HeartbeatEvery: hb})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(polls)*10*time.Millisecond+5*time.Millisecond)
	defer cancel()
	p.Run(ctx)
	p.Stop(context.Background())
	return p, p.Report()
}

func TestSteadyGrowth(t *testing.T) {
	s := &scripted{}
	for i := 0; i < 10; i++ {
		s.playlists = append(s.playlists, playlist(0, i+1, -1))
	}
	_, r := run(t, s, 12, 0)
	if r.BackwardJumps != 0 || r.SegmentsFetched != 10 || r.SegmentErrors != 0 || r.SessionGone {
		t.Fatalf("report = %+v", r)
	}
	if r.MaxGap > 200*time.Millisecond {
		t.Fatalf("MaxGap = %v on a steadily growing playlist", r.MaxGap)
	}
	if !s.deleted {
		t.Fatal("Stop did not DELETE the session")
	}
}

func TestResetIsBackwardJumpAndGap(t *testing.T) {
	s := &scripted{playlists: []string{playlist(0, 5, -1), playlist(0, 6, -1), playlist(0, 1, -1), playlist(0, 2, -1)}}
	_, r := run(t, s, 30, 0)
	if r.BackwardJumps != 1 {
		t.Fatalf("BackwardJumps = %d, want 1 (%v)", r.BackwardJumps, r.Newest)
	}
	// A reset playlist never yields a segment newer than seg 5, so the player
	// stalls like a real one: the gap runs to the end of the run.
	if r.MaxGap < 200*time.Millisecond {
		t.Fatalf("MaxGap = %v, want the stall to show", r.MaxGap)
	}
}

func TestDiscontinuityCountedOnce(t *testing.T) {
	s := &scripted{playlists: []string{playlist(0, 3, -1), playlist(0, 5, 4), playlist(1, 5, 4), playlist(2, 5, 4)}}
	_, r := run(t, s, 8, 0)
	if r.Discontinuities != 1 || r.BackwardJumps != 0 {
		t.Fatalf("report = %+v", r)
	}
}

func TestSessionGone(t *testing.T) {
	s := &scripted{playlists: []string{playlist(0, 2, -1)}, gone: true}
	_, r := run(t, s, 5, 0)
	if !r.SessionGone {
		t.Fatalf("report = %+v", r)
	}
}

func TestSegmentErrors(t *testing.T) {
	s := &scripted{playlists: []string{playlist(0, 2, -1)}, segStatus: http.StatusInternalServerError}
	_, r := run(t, s, 3, 0)
	if r.SegmentErrors != 2 || r.SegmentsFetched != 0 {
		t.Fatalf("report = %+v", r)
	}
}

func TestStartError(t *testing.T) {
	s := &scripted{startStatus: http.StatusServiceUnavailable}
	srv := httptest.NewServer(s.handler(t))
	defer srv.Close()
	_, err := Start(context.Background(), Config{BaseURL: srv.URL, Token: "jwt", ChannelID: 7})
	var se *StartError
	if !errors.As(err, &se) || se.Status != http.StatusServiceUnavailable {
		t.Fatalf("err = %v", err)
	}
}

func TestHeartbeats(t *testing.T) {
	s := &scripted{playlists: []string{playlist(0, 1, -1)}}
	run(t, s, 10, 20*time.Millisecond)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.heartbeats < 2 {
		t.Fatalf("heartbeats = %d", s.heartbeats)
	}
}

// A playlist that says the stream is over (#EXT-X-ENDLIST) is recorded: a real
// player stops reloading when it sees one, even if segments appear later.
func TestEndListRecorded(t *testing.T) {
	s := &scripted{playlists: []string{playlist(0, 3, -1), playlist(0, 3, -1) + "#EXT-X-ENDLIST\n", playlist(0, 4, -1)}}
	_, r := run(t, s, 6, 0)
	if !r.EndList {
		t.Fatalf("EndList = false, want true (report %+v)", r)
	}
}

// A master playlist is followed to its first variant, as real players do.
func TestFollowsMasterPlaylist(t *testing.T) {
	s := &scripted{master: true, playlists: []string{playlist(0, 3, -1)}}
	_, r := run(t, s, 6, 0)
	if r.SegmentsFetched != 3 || len(r.Errors) != 0 {
		t.Fatalf("report %+v", r)
	}
}

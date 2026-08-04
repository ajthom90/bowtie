// Package hdhrfake provides an in-process HTTP server that emulates a minimal
// HDHomeRun device for tests (discover, lineup, status, and /auto/v{n} streaming).
//
// LineupEntry is deliberately duplicated from the future hdhr package so this
// package compiles before Task 6; coupling is over JSON, not Go types.
package hdhrfake

import (
	"embed"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

//go:embed testdata/fixture.ts
var fixtureFS embed.FS

// Options configures the fake HDHomeRun.
type Options struct {
	DeviceID   string
	TunerCount int
	Lineup     []LineupEntry
}

// LineupEntry matches the HDHomeRun lineup.json JSON shape.
// Defined here (not imported from hdhr) so this package compiles before Task 6.
type LineupEntry struct {
	GuideNumber string `json:"GuideNumber"`
	GuideName   string `json:"GuideName"`
	URL         string `json:"URL"`
	VideoCodec  string `json:"VideoCodec"`
	AudioCodec  string `json:"AudioCodec"`
}

// Fake is a running fake HDHomeRun HTTP server.
type Fake struct {
	URL string // http://127.0.0.1:port

	opts   Options
	server *httptest.Server

	mu      sync.Mutex
	active  int
	streams map[int]string // tuner index -> VctNumber (guide number)
}

// New starts a fake HDHomeRun on a random local port. It is closed via t.Cleanup.
func New(t testing.TB, opts Options) *Fake {
	t.Helper()
	if opts.DeviceID == "" {
		opts.DeviceID = "DEADBEEF"
	}
	if opts.TunerCount <= 0 {
		opts.TunerCount = 2
	}
	if opts.Lineup == nil {
		opts.Lineup = []LineupEntry{}
	}

	f := &Fake{
		opts:    opts,
		streams: make(map[int]string),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /discover.json", f.handleDiscover)
	mux.HandleFunc("GET /lineup.json", f.handleLineup)
	mux.HandleFunc("GET /status.json", f.handleStatus)
	mux.HandleFunc("GET /auto/{channel}", f.handleStream)

	// Bind to 127.0.0.1 so BaseURL is always usable from the same machine.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("hdhrfake listen: %v", err)
	}
	f.server = httptest.NewUnstartedServer(mux)
	f.server.Listener = ln
	f.server.Start()
	f.URL = f.server.URL

	// Fill lineup URLs if empty so clients see absolute stream URLs.
	for i := range f.opts.Lineup {
		if f.opts.Lineup[i].URL == "" {
			f.opts.Lineup[i].URL = f.URL + "/auto/v" + f.opts.Lineup[i].GuideNumber
		}
	}

	t.Cleanup(func() { f.server.Close() })
	return f
}

// ActiveStreams returns the number of currently open /auto/v* streams.
func (f *Fake) ActiveStreams() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.active
}

func (f *Fake) handleDiscover(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{
		"FriendlyName":    "HDHomeRun FAKE",
		"ModelNumber":     "HDFX-4US",
		"DeviceID":        f.opts.DeviceID,
		"FirmwareVersion": "20260101",
		"TunerCount":      f.opts.TunerCount,
		"BaseURL":         f.URL,
		"LineupURL":       f.URL + "/lineup.json",
	})
}

func (f *Fake) handleLineup(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, f.opts.Lineup)
}

func (f *Fake) handleStatus(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := make([]map[string]any, 0, f.opts.TunerCount)
	for i := 0; i < f.opts.TunerCount; i++ {
		entry := map[string]any{
			"Resource": "tuner" + strconv.Itoa(i),
		}
		if vct, ok := f.streams[i]; ok {
			entry["VctNumber"] = vct
			entry["VctName"] = vct
			entry["SignalStrengthPercent"] = 100
			entry["SignalQualityPercent"] = 100
			entry["SymbolQualityPercent"] = 100
		} else {
			entry["VctNumber"] = ""
			entry["VctName"] = ""
			entry["SignalStrengthPercent"] = 0
			entry["SignalQualityPercent"] = 0
			entry["SymbolQualityPercent"] = 0
		}
		out = append(out, entry)
	}
	writeJSON(w, out)
}

func (f *Fake) handleStream(w http.ResponseWriter, r *http.Request) {
	channel := r.PathValue("channel")
	// Path is /auto/{channel}; clients request /auto/v5.1 so channel is "v5.1".
	guideNumber := strings.TrimPrefix(channel, "v")
	if guideNumber == "" {
		http.Error(w, "bad channel", http.StatusBadRequest)
		return
	}

	f.mu.Lock()
	if f.active >= f.opts.TunerCount {
		f.mu.Unlock()
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, "all tuners in use")
		return
	}
	// Assign lowest free tuner index.
	tunerIdx := -1
	for i := 0; i < f.opts.TunerCount; i++ {
		if _, used := f.streams[i]; !used {
			tunerIdx = i
			break
		}
	}
	if tunerIdx < 0 {
		f.mu.Unlock()
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, "all tuners in use")
		return
	}
	f.streams[tunerIdx] = guideNumber
	f.active++
	f.mu.Unlock()

	defer func() {
		f.mu.Lock()
		delete(f.streams, tunerIdx)
		f.active--
		f.mu.Unlock()
	}()

	fixture, err := fixtureFS.ReadFile("testdata/fixture.ts")
	if err != nil {
		http.Error(w, "fixture missing", http.StatusInternalServerError)
		return
	}
	if len(fixture) == 0 {
		http.Error(w, "empty fixture", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "video/mp2t")
	// Flush headers immediately so clients (and tests) see the stream start.
	if fl, ok := w.(http.Flusher); ok {
		fl.Flush()
	}

	// Stream fixture in a loop at roughly real-time (~360 KB/s is fine).
	// fixture is ~729 KB for 2s → ~365 KB/s. Chunk ~18.8 KB every 50ms ≈ 376 KB/s.
	const chunkSize = 188 * 100 // 100 TS packets
	const tick = 50 * time.Millisecond

	ticker := time.NewTicker(tick)
	defer ticker.Stop()

	offset := 0
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			end := offset + chunkSize
			if end > len(fixture) {
				// Write remainder then wrap.
				if offset < len(fixture) {
					if _, err := w.Write(fixture[offset:]); err != nil {
						return
					}
				}
				offset = 0
				end = chunkSize
				if end > len(fixture) {
					end = len(fixture)
				}
			}
			if _, err := w.Write(fixture[offset:end]); err != nil {
				return
			}
			offset = end
			if fl, ok := w.(http.Flusher); ok {
				fl.Flush()
			}
		}
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		// Client gone; nothing useful to do.
		return
	}
}

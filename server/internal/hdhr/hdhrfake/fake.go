// Package hdhrfake provides an HTTP server that emulates a minimal HDHomeRun
// device (discover, lineup, status, and /auto/v{n} streaming) for tests and
// local development. Each channel broadcasts a continuous, real-time timeline
// (tsloop) and every packet passes through a fault engine (faults), so tests
// can inject signal fades, stalls, drops, corruption and device failures.
//
// LineupEntry is deliberately duplicated from the hdhr package; coupling is
// over JSON, not Go types.
package hdhrfake

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ajthom90/bowtie/server/internal/hdhr/hdhrfake/faults"
	"github.com/ajthom90/bowtie/server/internal/hdhr/hdhrfake/tsloop"
)

//go:embed testdata/fixture.ts
var fixtureFS embed.FS

// maxBurstBacklog caps how much a burst stall withholds per connection.
const maxBurstBacklog = 32 << 20

// Options configures the fake HDHomeRun.
type Options struct {
	DeviceID   string
	TunerCount int
	Lineup     []LineupEntry

	// Channels to broadcast. When nil, one channel per Lineup entry is built
	// from the embedded 480i fixture.
	Channels []Channel
	// Sources are extra named sources a source-switch fault may switch to.
	Sources map[string]*tsloop.Source
	// Clock drives timelines and fault expiry; zero means wall-clock time.
	Clock tsloop.Clock
	// Seed makes fault randomness reproducible.
	Seed int64
	// Listen is the TCP address; default "127.0.0.1:0".
	Listen string
}

// Channel is one broadcast channel.
type Channel struct {
	GuideNumber string
	Name        string
	Source      *tsloop.Source
	// PTSWrapIn, when > 0, starts the channel clock this long before the
	// 33-bit PTS/PCR wrap.
	PTSWrapIn time.Duration
}

// LineupEntry matches the HDHomeRun lineup.json JSON shape.
type LineupEntry struct {
	GuideNumber string `json:"GuideNumber"`
	GuideName   string `json:"GuideName"`
	URL         string `json:"URL"`
	VideoCodec  string `json:"VideoCodec"`
	AudioCodec  string `json:"AudioCodec"`
}

// Event is one entry in the fake's activity log.
type Event struct {
	At      time.Time `json:"at"`
	Kind    string    `json:"kind"` // dial, reject, hang, close, fault, fault-removed
	Channel string    `json:"channel,omitempty"`
	Conn    string    `json:"conn,omitempty"`
	Detail  string    `json:"detail,omitempty"`
}

// ConnInfo describes an open stream connection.
type ConnInfo struct {
	ID       string `json:"id"`
	Channel  string `json:"channel"`
	Tuner    string `json:"tuner"`
	ClientIP string `json:"clientIp"`
	Bytes    int64  `json:"bytes"`
}

type channelRT struct {
	info Channel
	tl   *tsloop.Channel
}

type connRT struct {
	ConnInfo
	bytes atomic.Int64
}

// Fake is a running fake HDHomeRun HTTP server.
type Fake struct {
	URL string // http://127.0.0.1:port

	opts     Options
	clock    tsloop.Clock
	engine   *faults.Engine
	channels map[string]*channelRT
	server   *http.Server
	done     chan struct{}
	close    sync.Once

	mu         sync.Mutex
	active     int
	totalDials int64          // cumulative accepted /auto/v* streams (A3)
	streams    map[int]string // tuner index -> guide number
	conns      map[string]*connRT
	connSeq    int
	events     []Event
	timelineID int
}

var (
	fixtureOnce sync.Once
	fixtureSrc  *tsloop.Source
	fixtureErr  error
)

func embeddedFixture() (*tsloop.Source, error) {
	fixtureOnce.Do(func() {
		var b []byte
		b, fixtureErr = fixtureFS.ReadFile("testdata/fixture.ts")
		if fixtureErr == nil {
			fixtureSrc, fixtureErr = tsloop.Load("fixture", bytes.NewReader(b))
		}
	})
	return fixtureSrc, fixtureErr
}

// New starts a fake HDHomeRun on a random local port. It is closed via t.Cleanup.
func New(t testing.TB, opts Options) *Fake {
	t.Helper()
	f, err := Start(opts)
	if err != nil {
		t.Fatalf("hdhrfake: %v", err)
	}
	t.Cleanup(f.Close)
	return f
}

// Start runs a fake HDHomeRun until Close.
func Start(opts Options) (*Fake, error) {
	if opts.DeviceID == "" {
		opts.DeviceID = "DEADBEEF"
	}
	if opts.TunerCount <= 0 {
		opts.TunerCount = 2
	}
	if opts.Listen == "" {
		opts.Listen = "127.0.0.1:0"
	}
	clock := opts.Clock
	if clock.Now == nil || clock.After == nil {
		clock = tsloop.RealClock()
	}
	if opts.Channels == nil {
		src, err := embeddedFixture()
		if err != nil {
			return nil, fmt.Errorf("embedded fixture: %w", err)
		}
		for _, e := range opts.Lineup {
			opts.Channels = append(opts.Channels, Channel{GuideNumber: e.GuideNumber, Name: e.GuideName, Source: src})
		}
	}
	if opts.Lineup == nil {
		opts.Lineup = []LineupEntry{}
		for _, c := range opts.Channels {
			opts.Lineup = append(opts.Lineup, LineupEntry{GuideNumber: c.GuideNumber, GuideName: c.Name, VideoCodec: "MPEG2", AudioCodec: "AC3"})
		}
	}

	f := &Fake{
		opts:     opts,
		clock:    clock,
		engine:   faults.NewEngine(clock, opts.Seed),
		channels: map[string]*channelRT{},
		done:     make(chan struct{}),
		streams:  map[int]string{},
		conns:    map[string]*connRT{},
	}
	for _, c := range opts.Channels {
		if c.Source == nil {
			return nil, fmt.Errorf("channel %s has no source", c.GuideNumber)
		}
		base := c.Source.Origin90k()
		if c.PTSWrapIn > 0 {
			base = 1<<33 - int64(c.PTSWrapIn/time.Microsecond)*9/100
		}
		f.channels[c.GuideNumber] = &channelRT{info: c, tl: tsloop.NewChannel(c.Source, clock, base)}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /discover.json", f.handleDiscover)
	mux.HandleFunc("GET /lineup.json", f.handleLineup)
	mux.HandleFunc("GET /status.json", f.handleStatus)
	mux.HandleFunc("GET /auto/{channel}", f.handleStream)

	ln, err := net.Listen("tcp", opts.Listen)
	if err != nil {
		return nil, fmt.Errorf("listen %s: %w", opts.Listen, err)
	}
	f.URL = "http://" + ln.Addr().String()
	for i := range f.opts.Lineup {
		if f.opts.Lineup[i].URL == "" {
			f.opts.Lineup[i].URL = f.URL + "/auto/v" + f.opts.Lineup[i].GuideNumber
		}
	}
	f.server = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = f.server.Serve(ln) }()
	return f, nil
}

// Close stops the server and ends every stream.
func (f *Fake) Close() {
	f.close.Do(func() {
		close(f.done)
		_ = f.server.Close()
	})
}

// Engine exposes the fault engine (for inspection; prefer Apply/Remove).
func (f *Fake) Engine() *faults.Engine { return f.engine }

// ActiveStreams returns the number of currently open /auto/v* streams.
func (f *Fake) ActiveStreams() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.active
}

// TotalDials returns the cumulative number of accepted stream connections
// (not rejected 503s). Used by e2e tuner-reuse assertions (A3).
func (f *Fake) TotalDials() int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.totalDials
}

// Events returns a copy of the activity log.
func (f *Fake) Events() []Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Event(nil), f.events...)
}

// Connections returns the open stream connections, oldest first.
func (f *Fake) Connections() []ConnInfo {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]ConnInfo, 0, len(f.conns))
	for i := 1; i <= f.connSeq; i++ {
		if c, ok := f.conns["c"+strconv.Itoa(i)]; ok {
			info := c.ConnInfo
			info.Bytes = c.bytes.Load()
			out = append(out, info)
		}
	}
	return out
}

func (f *Fake) logLocked(kind, channel, conn, detail string) {
	f.events = append(f.events, Event{At: f.clock.Now(), Kind: kind, Channel: channel, Conn: conn, Detail: detail})
}

func (f *Fake) log(kind, channel, conn, detail string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.logLocked(kind, channel, conn, detail)
}

// Apply starts a fault. Timeline faults (timestamp-jump, source-switch) act on
// the channel immediately; the rest go to the fault engine.
func (f *Fake) Apply(t faults.Target, s faults.Spec) (string, error) {
	if err := s.Validate(); err != nil {
		return "", err
	}
	var ch *channelRT
	if !s.Fault.IsDevice() {
		ch = f.channels[t.Channel]
		if ch == nil {
			return "", fmt.Errorf("hdhrfake: unknown channel %q", t.Channel)
		}
	}
	detail := describe(s)
	if s.Fault.IsTimeline() {
		switch s.Fault {
		case faults.TimestampJump:
			ch.tl.Jump(s.By)
		case faults.SourceSwitch:
			src := f.sourceByName(s.To)
			if src == nil {
				return "", fmt.Errorf("hdhrfake: unknown source %q", s.To)
			}
			ch.tl.Switch(src)
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		f.timelineID++
		f.logLocked("fault", t.Channel, "", detail)
		return "t" + strconv.Itoa(f.timelineID), nil
	}
	var live []string
	f.mu.Lock()
	for _, c := range f.conns {
		if c.Channel == t.Channel {
			live = append(live, c.ID)
		}
	}
	f.mu.Unlock()
	id, err := f.engine.Add(t, s, live)
	if err != nil {
		return "", err
	}
	f.log("fault", t.Channel, "", id+" "+detail)
	return id, nil
}

// Remove ends a fault started by Apply.
func (f *Fake) Remove(id string) {
	f.engine.Remove(id)
	f.log("fault-removed", "", "", id)
}

func (f *Fake) sourceByName(name string) *tsloop.Source {
	if src, ok := f.opts.Sources[name]; ok {
		return src
	}
	for _, c := range f.channels {
		if c.info.Source.Name() == name {
			return c.info.Source
		}
	}
	return nil
}

func describe(s faults.Spec) string {
	b, _ := json.Marshal(s)
	return string(b)
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
	tuned := map[int]string{}
	ips := map[int]string{}
	for i, g := range f.streams {
		tuned[i] = g
	}
	for _, c := range f.conns {
		idx, _ := strconv.Atoi(strings.TrimPrefix(c.Tuner, "tuner"))
		ips[idx] = c.ClientIP
	}
	f.mu.Unlock()

	out := make([]map[string]any, 0, f.opts.TunerCount)
	for i := 0; i < f.opts.TunerCount; i++ {
		entry := map[string]any{"Resource": "tuner" + strconv.Itoa(i)}
		if guide, ok := tuned[i]; ok {
			name := guide
			if ch := f.channels[guide]; ch != nil {
				name = ch.info.Name
			}
			sig := f.engine.Signal(guide)
			entry["VctNumber"] = guide
			entry["VctName"] = name
			entry["SignalStrengthPercent"] = sig.Strength
			entry["SignalQualityPercent"] = sig.Quality
			entry["SymbolQualityPercent"] = sig.Symbol
			entry["TargetIP"] = ips[i]
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
	// Path is /auto/{channel}; clients request /auto/v5.1 so channel is "v5.1".
	guide := strings.TrimPrefix(r.PathValue("channel"), "v")
	ch := f.channels[guide]
	if ch == nil {
		http.Error(w, "unknown channel", http.StatusNotFound)
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	go func() {
		select {
		case <-f.done:
			cancel()
		case <-ctx.Done():
		}
	}()

	if f.engine.Hang() {
		f.log("hang", guide, "", r.RemoteAddr)
		<-ctx.Done()
		return
	}

	f.mu.Lock()
	tunerIdx := -1
	if !f.engine.Busy() && f.active < f.opts.TunerCount {
		for i := 0; i < f.opts.TunerCount; i++ {
			if _, used := f.streams[i]; !used {
				tunerIdx = i
				break
			}
		}
	}
	if tunerIdx < 0 {
		f.logLocked("reject", guide, "", "all tuners in use")
		f.mu.Unlock()
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, "all tuners in use")
		return
	}
	f.connSeq++
	conn := &connRT{ConnInfo: ConnInfo{
		ID:       "c" + strconv.Itoa(f.connSeq),
		Channel:  guide,
		Tuner:    "tuner" + strconv.Itoa(tunerIdx),
		ClientIP: hostOf(r.RemoteAddr),
	}}
	f.conns[conn.ID] = conn
	f.streams[tunerIdx] = guide
	f.active++
	f.totalDials++
	f.logLocked("dial", guide, conn.ID, conn.Tuner)
	f.mu.Unlock()

	reason := "client-gone"
	defer func() {
		f.mu.Lock()
		delete(f.streams, tunerIdx)
		delete(f.conns, conn.ID)
		f.active--
		f.logLocked("close", guide, conn.ID, reason)
		f.mu.Unlock()
	}()

	w.Header().Set("Content-Type", "video/mp2t")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	if flusher != nil {
		flusher.Flush()
	}

	reader := ch.tl.NewReader()
	var held [][]byte
	heldBytes := 0
	write := func(b []byte) bool {
		n, err := w.Write(b)
		conn.bytes.Add(int64(n))
		return err == nil
	}
	for {
		batch, err := reader.Next(ctx)
		if err != nil {
			if f.isClosed() {
				reason = "server-close"
			}
			return
		}
		src := ch.tl.Source()
		for _, p := range batch {
			d := f.engine.Decide(guide, conn.ID, p, src.TrackOf(tsloop.PID(p)))
			switch d.Action {
			case faults.Emit:
				for _, b := range held {
					if !write(b) {
						return
					}
				}
				held, heldBytes = nil, 0
				for _, b := range d.Packets {
					if !write(b) {
						return
					}
				}
			case faults.Hold:
				if d.Burst && heldBytes < maxBurstBacklog {
					held = append(held, p)
					heldBytes += len(p)
				}
			case faults.Close:
				if d.Reset {
					reason = "drop-reset"
					if err := abort(w); err != nil {
						reason = "drop-reset (" + err.Error() + ")"
					}
				} else {
					reason = "drop-clean"
				}
				return
			}
		}
		if flusher != nil {
			flusher.Flush()
		}
	}
}

func (f *Fake) isClosed() bool {
	select {
	case <-f.done:
		return true
	default:
		return false
	}
}

// Bounds on how long abort waits for the client to take in-flight data.
const (
	abortDrainMax   = time.Second
	abortDrainPause = 200 * time.Millisecond // when the queue can't be read
)

// abort closes the underlying TCP connection with RST. A RST sent while data
// is still in flight carries a sequence number the client hasn't reached, and
// the kernel may drop it (RFC 5961; macOS loses about a third of them), which
// leaves the client half-open instead of reset. So abort first stops writing
// and waits, up to abortDrainMax, for the send queue to empty. If the client
// isn't reading, the RST goes out anyway and may still be lost, just as on a
// real network.
func abort(w http.ResponseWriter) error {
	hj, ok := w.(http.Hijacker)
	if !ok {
		return errors.New("not hijackable")
	}
	c, _, err := hj.Hijack()
	if err != nil {
		return fmt.Errorf("hijack: %w", err)
	}
	tc, ok := c.(*net.TCPConn)
	if !ok {
		_ = c.Close()
		return fmt.Errorf("not TCP: %T", c)
	}
	drainSendQueue(tc)
	if err := tc.SetLinger(0); err != nil {
		_ = c.Close()
		return fmt.Errorf("linger: %w", err)
	}
	return c.Close()
}

// drainSendQueue waits until tc's send buffer is empty or abortDrainMax passes.
func drainSendQueue(tc *net.TCPConn) {
	rc, err := tc.SyscallConn()
	if err != nil {
		return
	}
	deadline := time.Now().Add(abortDrainMax)
	for {
		var n int
		var known bool
		if err := rc.Control(func(fd uintptr) { n, known = sendQueue(fd) }); err != nil {
			return
		}
		if !known {
			time.Sleep(abortDrainPause)
			return
		}
		if n == 0 || time.Now().After(deadline) {
			return
		}
		time.Sleep(time.Millisecond)
	}
}

func hostOf(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil && !errors.Is(err, net.ErrClosed) {
		// Client gone; nothing useful to do.
		return
	}
}

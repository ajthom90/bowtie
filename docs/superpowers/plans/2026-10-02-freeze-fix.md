# Freeze Fix (Plan 2) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ordinary hiccups (slow FFmpeg, weak signal, dead device connection, slow tuner) never freeze or kill a viewer's stream.

**Architecture:** All changes are in the Go server's ingest (`internal/stream/ingest.go`), session supervisor (`manager.go`), FFmpeg args (`transcode/ffmpeg.go`) and the fake-HDHomeRun harness. Each fix starts from a failing harness test or scenario and ends by deleting its `xfail`/`flaky` marker, which the harness enforces (a marker that outlives its fix fails the test).

**Tech Stack:** Go 1.22, FFmpeg (5.1 in the image, 8.x on dev Macs), fake HDHomeRun (`internal/hdhr/hdhrfake`), e2e harness (`internal/e2e`).

**Spec:** `docs/superpowers/specs/2026-10-02-freeze-fix-design.md`

## Global Constraints

- Device dial: TCP connect timeout **5 s**, response-header timeout **12 s** (spec amended from 15 s; see Task 1), no overall client timeout.
- Idle watchdog: **8 s** without bytes → close body → existing reconnect (1 s backoff, 60 s give-up).
- Per-subscriber queue: **16 MiB**, byte-bounded, drop-oldest; PAT+PMT re-sent at the queue head after any drop.
- Stuck subscriber: force-close only after **30 s** with queued bytes and no consumption.
- Restarts: `-hls_flags delete_segments+temp_file+append_list+discont_start`; first start keeps `delete_segments+temp_file`. Verified 2026-10-02 on FFmpeg 5.1.9 (image) and 8.0.1: numbering continues (`seg00003` after `seg00002`) with `#EXT-X-DISCONTINUITY`.
- All ingest timing uses the injected clock (`im.now`, `im.after`); arm timers before spawning goroutines (the TestTunerFreeBudget lesson).
- Real-HDHomeRun checks: channel 9.1 only, `-parallel 1`, short, not during prime time.
- Gates for every task: `cd server && go test ./...`, `go vet ./...`, `golangci-lint run`. Tasks touching FFmpeg args or scenarios also run `go test -tags ffmpeg -p 1 -parallel 2 ./...`.

## Review Focus

1. **Slow tuner that answers after 10–11 s** (the HDHomeRun's own 807 delay) must still get its answer, not a timeout → Task 1 pins 12 s > 10 s with an `httptest` that answers 807 after a delay just under the timeout.
2. **Abandoned start request** (user taps Back while "Starting…") must leave no FFmpeg process and no ingest subscriber → Task 1 manager test.
3. **Session watched across a restart for minutes** must keep a unique, increasing segment list (no reused `segNNNNN` after `delete_segments`) → Task 5 scenario asserts monotonic sequence and unique URIs.
4. **Slow consumer that recovers** must not lose the stream structure (PAT/PMT) → Task 4 unit test checks the queue head after a drop, and a real-player check plays the drop scenario's output.
5. **Loss of lock lasting longer than the idle timeout** must end in playback resuming, not a torn-down session → Task 3 scenario `nosignal-reconnect`.

---

### Task 1: Device dial timeouts and lifetime

**Files:**
- Modify: `server/internal/stream/ingest.go` (HTTPDial, `channelIngest.attach`)
- Create: `server/internal/stream/dial_timeout_test.go`
- Modify: `server/internal/stream/manager_test.go` (cancelled-start test)
- Modify: `server/internal/e2e/pipeline_test.go` (remove two xfail markers)
- Modify: `docs/superpowers/specs/2026-10-02-freeze-fix-design.md` (§4: 15 s → 12 s, with reason)

**Interfaces:**
- Produces: `func NewHTTPDial(connectTimeout, headerTimeout time.Duration) DialFunc`; `var HTTPDial = NewHTTPDial(deviceConnectTimeout, deviceHeaderTimeout)`; constants `deviceConnectTimeout = 5 * time.Second`, `deviceHeaderTimeout = 12 * time.Second`.

- [ ] **Step 1: Write failing tests** in `dial_timeout_test.go`:

```go
package stream

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// A device that never answers fails at the header timeout instead of hanging.
func TestHTTPDialHeaderTimeout(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-block }))
	defer srv.Close()
	defer close(block)
	dial := NewHTTPDial(time.Second, 300*time.Millisecond)
	start := time.Now()
	_, _, err := dial(context.Background(), srv.URL+"/auto/v9.1")
	if err == nil {
		t.Fatal("want timeout error")
	}
	if el := time.Since(start); el > 2*time.Second {
		t.Fatalf("dial took %v, want about 300ms", el)
	}
}

// The HDHomeRun answers 807 after ~10s; a slower-than-instant answer under the
// header timeout must arrive intact.
func TestHTTPDialSlowNoSignalAnswerArrives(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.Header().Set("X-HDHomeRun-Error", "807 No Video Data")
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	_, _, err := NewHTTPDial(time.Second, 500*time.Millisecond)(context.Background(), srv.URL+"/auto/v9.1")
	if !errors.Is(err, ErrNoSignal) {
		t.Fatalf("err = %v, want ErrNoSignal", err)
	}
}

// The device stream must outlive the request that started it.
func TestAttachStreamOutlivesRequestContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		for i := 0; i < 50; i++ {
			if _, err := w.Write(make([]byte, 188*10)); err != nil {
				return
			}
			w.(http.Flusher).Flush()
			time.Sleep(20 * time.Millisecond)
		}
	}))
	defer srv.Close()
	im := NewIngestManager(HTTPDial)
	defer im.Shutdown()
	ctx, cancel := context.WithCancel(context.Background())
	sub, err := im.Attach(ctx, 1, srv.URL+"/auto/v9.1")
	if err != nil {
		t.Fatal(err)
	}
	cancel() // the POST /sessions handler returned
	buf := make([]byte, 188)
	deadline := time.Now().Add(2 * time.Second)
	got := 0
	for time.Now().Before(deadline) && got < 188*20 {
		n, err := sub.R.Read(buf)
		if err != nil {
			t.Fatalf("stream died after request cancel: %v (read %d bytes)", err, got)
		}
		got += n
	}
	_ = sub.Close()
}
```

Add to `manager_test.go` (uses the file's existing `setupEnv`, `newTestManagerWithDial`, `stubRunner`):

```go
// An abandoned start (request cancelled while waiting for the first playlist)
// leaves no FFmpeg process and no ingest subscriber behind.
func TestCancelledStartCleansUp(t *testing.T) {
	st, cfg, clock, _, chID, user := setupEnv(t)
	runner := &stubRunner{writeM3U: false}
	m, im, _ := newTestManagerWithDial(st, cfg, clock, runner, nil)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	if _, err := m.Start(ctx, user, chID, clientCaps("")); err == nil {
		t.Fatal("want error from cancelled start")
	}
	if n := runner.Running(); n != 0 {
		t.Fatalf("running ffmpeg processes = %d, want 0", n)
	}
	if ch := im.ActiveChannels(); len(ch) != 0 {
		// The 5s tail may hold the device briefly; no subscriber may remain.
		if im.SubCount(chID) != 0 {
			t.Fatalf("ingest subscribers = %d, want 0", im.SubCount(chID))
		}
	}
}
```

If `stubRunner` has no `Running()` count or `IngestManager` has no `SubCount`, add them in this step: `Running()` counts started-minus-stopped processes; `func (im *IngestManager) SubCount(channelID int64) int` returns `len(ch.subs)` under `ch.mu` (0 if no channel).

- [ ] **Step 2: Run** `cd server && go test -run 'HTTPDial|OutlivesRequest|CancelledStart' ./internal/stream/` → FAIL (undefined `NewHTTPDial`; stream dies after cancel).

- [ ] **Step 3: Implement.** Replace `HTTPDial` in `ingest.go`:

```go
const (
	deviceConnectTimeout = 5 * time.Second
	// deviceHeaderTimeout bounds the wait for the device's response headers.
	// The HDHomeRun answers 806/807 after ~10s, so this must stay above that;
	// it must also stay under the 15s "hung device fails fast" bound.
	deviceHeaderTimeout = 12 * time.Second
)

// NewHTTPDial returns a DialFunc with connect and response-header timeouts and
// no overall timeout (the body is a live stream). A non-2xx response is an
// error, classified by X-HDHomeRun-Error (see DeviceError).
func NewHTTPDial(connectTimeout, headerTimeout time.Duration) DialFunc {
	client := &http.Client{Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: connectTimeout}).DialContext,
		ResponseHeaderTimeout: headerTimeout,
	}}
	return func(ctx context.Context, rawURL string) (io.ReadCloser, int, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, 0, err
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, 0, err
		}
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			_ = resp.Body.Close()
			return nil, resp.StatusCode, &DeviceError{Status: resp.StatusCode, Reason: resp.Header.Get("X-HDHomeRun-Error")}
		}
		return resp.Body, resp.StatusCode, nil
	}
}

// HTTPDial is the production DialFunc.
var HTTPDial = NewHTTPDial(deviceConnectTimeout, deviceHeaderTimeout)
```

In `channelIngest.attach`, dial with `context.WithoutCancel(ctx)`:

```go
	// The device stream may be shared by other sessions and must not die when
	// the request that started it returns.
	body, status, err := c.im.dial(context.WithoutCancel(ctx), url)
```

Amend spec §4: "`ResponseHeaderTimeout` **12 s** (the HDHomeRun answers 806/807 after ~10 s; 12 s also keeps a hung device under the harness's 15 s fail-fast bound)."

- [ ] **Step 4: Remove xfail markers** in `server/internal/e2e/pipeline_test.go`: in `TestPipelineDialHangFailsFast` and `TestPipelineStartKeepsFirstDial`, replace `xfail(t, "<reason>", func() error { … })` with the body run directly and `t.Fatal(err)` on a non-nil result, e.g.:

```go
	if err := func() error {
		start := time.Now()
		_, err := pl.h.PlayerWith(t, guide, &http.Client{Timeout: 20 * time.Second})
		if el := time.Since(start); el > 15*time.Second {
			return errorf("session start took %v against a hung device (err=%v)", el.Round(time.Second), err)
		}
		return nil
	}(); err != nil {
		t.Fatal(err)
	}
```

- [ ] **Step 5: Run** `go test ./internal/stream/ ./internal/e2e/` → PASS; `go test ./...`, vet, lint.
- [ ] **Step 6: Commit** `fix(ingest): device dial timeouts; stream outlives the start request`

---

### Task 2: Idle watchdog (dead device connections)

**Files:**
- Modify: `server/internal/stream/ingest.go` (`pump`, new `watchIdle`)
- Modify: `server/internal/stream/ingest_test.go` (ingestClock `Pending()`, new test)
- Modify: `server/internal/e2e/pipeline_test.go` (remove xfail in `TestPipelineHalfOpenStallRedials`)
- Modify: `server/internal/e2e/testdata/scenarios/half-open-stall.yaml` (remove `xfail`)

**Interfaces:**
- Consumes: `im.now`, `im.after` (injected clock).
- Produces: constant `ingestIdleTimeout = 8 * time.Second`; `func (c *channelIngest) watchIdle(body io.Closer, lastRead *atomic.Int64) (stop func())`.

- [ ] **Step 1: Failing test** in `ingest_test.go`. First add to `ingestClock`:

```go
// Pending returns how many timers are armed (tests wait on it before Advance).
func (c *ingestClock) Pending() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.timers)
}
```

Then:

```go
// A device connection that goes silent is closed after ingestIdleTimeout and
// redialed; the subscriber keeps receiving data from the new connection.
func TestIdleWatchdogRedialsSilentDevice(t *testing.T) {
	clock := newIngestClock(time.Date(2026, 10, 2, 20, 0, 0, 0, time.UTC))
	first, second := newPipeBody(), newPipeBody()
	var dials atomic.Int64
	im, _ := newTestIngest(t, func(ctx context.Context, url string) (io.ReadCloser, int, error) {
		if dials.Add(1) == 1 {
			return first, 200, nil
		}
		return second, 200, nil
	}, clock)
	sub, err := im.Attach(context.Background(), 1, "u")
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	go func() { _, _ = first.Write(make([]byte, 188*4)) }() // then silence
	readN(t, sub.R, 188*4, 2*time.Second)

	waitFor(t, func() bool { return clock.Pending() > 0 }) // watchdog armed
	clock.Advance(ingestIdleTimeout)
	waitFor(t, func() bool { return first.closed.Load() })
	// Reconnect backoff (1s) then redial.
	waitFor(t, func() bool { return clock.Pending() > 0 })
	clock.Advance(ingestReconnectMin)
	waitFor(t, func() bool { return dials.Load() == 2 })
	go func() { _, _ = second.Write(make([]byte, 188*4)) }()
	readN(t, sub.R, 188*4, 2*time.Second)
}
```

If `waitFor` does not exist in the file, add: `func waitFor(t *testing.T, cond func() bool) { t.Helper(); deadline := time.Now().Add(3 * time.Second); for !cond() { if time.Now().After(deadline) { t.Fatal("condition not met"); }; time.Sleep(5 * time.Millisecond) } }`.

- [ ] **Step 2: Run** `go test -run TestIdleWatchdog ./internal/stream/` → FAIL (first body never closed).

- [ ] **Step 3: Implement** in `ingest.go`:

```go
// ingestIdleTimeout: a device streams continuously once tuned, so this long
// without a byte means the connection is dead (silent device or a reset the
// kernel dropped). The body is closed and the normal reconnect path runs.
const ingestIdleTimeout = 8 * time.Second

// watchIdle closes body once lastRead (UnixNano, updated by pump) is
// ingestIdleTimeout old. The returned stop func ends the watch.
func (c *channelIngest) watchIdle(body io.Closer, lastRead *atomic.Int64) (stop func()) {
	done := make(chan struct{})
	var once sync.Once
	wait := func() time.Duration {
		return ingestIdleTimeout - c.im.now().Sub(time.Unix(0, lastRead.Load()))
	}
	// Arm before spawning so a test's Advance can't race timer registration.
	timer := c.im.after(wait())
	go func() {
		for {
			select {
			case <-done:
				return
			case <-timer:
			}
			w := wait()
			if w <= 0 {
				log.Printf("ingest: channel %d: no data for %v, reconnecting", c.channelID, ingestIdleTimeout)
				_ = body.Close()
				return
			}
			timer = c.im.after(w)
		}
	}()
	return func() { once.Do(func() { close(done) }) }
}
```

In `pump()`, track the watched body and last-read time:

```go
	var lastRead atomic.Int64
	var watched io.ReadCloser
	stopWatch := func() {}
	defer func() { stopWatch() }()

	for {
		c.mu.Lock()
		body := c.body
		stop := c.stopPump
		running := c.running
		c.mu.Unlock()

		if !running || body == nil {
			return
		}
		if body != watched {
			stopWatch()
			lastRead.Store(c.im.now().UnixNano())
			watched = body
			stopWatch = c.watchIdle(body, &lastRead)
		}

		n, err := body.Read(buf)
		if n > 0 {
			lastRead.Store(c.im.now().UnixNano())
		}
		// … existing stop check, chunk handling and reconnect path unchanged …
```

- [ ] **Step 4: Remove markers.** `TestPipelineHalfOpenStallRedials`: run the check directly and `t.Fatal` on error (as in Task 1 Step 4). `half-open-stall.yaml`: delete the `xfail:` line and its comment; keep `maxGapSeconds: 15`.
- [ ] **Step 5: Run** `go test ./...`, then `go test -tags ffmpeg -p 1 -parallel 2 -run 'TestScenarios/half-open' -v ./internal/e2e/` → PASS; vet, lint.
- [ ] **Step 6: Commit** `fix(ingest): redial a device connection that goes silent for 8s`

---

### Task 3: No signal is transient (fake 807 + reconnect retry)

**Files:**
- Modify: `server/internal/hdhr/hdhrfake/faults/engine.go` (`Levels.Locked`)
- Modify: `server/internal/hdhr/hdhrfake/fake.go` (807 on dial during loss of lock)
- Modify: `server/internal/hdhr/hdhrfake/fake_faults_test.go` (new test)
- Modify: `server/internal/stream/ingest.go` (reconnect loop)
- Modify: `server/internal/stream/ingest_test.go` (new test)
- Create: `server/internal/e2e/testdata/scenarios/nosignal-reconnect.yaml`
- Modify: `docs/dev/fake-hdhomerun.md` (document 807 behavior)

**Interfaces:**
- Produces: `func (l Levels) Locked() bool` (quality ≥ `lockQuality`).

- [ ] **Step 1: Failing tests.**

`fake_faults_test.go` (use the file's existing `apply` helper and fake constructor):

```go
// A new tune while the channel has lost lock gets 503 + 807, like a real
// HDHomeRun, instead of an empty stream.
func TestDialDuringLossOfLockGets807(t *testing.T) {
	f := newTestFake(t) // existing helper in this file; channel "90.1"
	q := 10
	apply(t, f, faults.Target{Channel: "90.1"}, faults.Spec{Fault: faults.Signal, Quality: &q})
	resp, err := http.Get(f.URL() + "/auto/v90.1")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable || resp.Header.Get("X-HDHomeRun-Error") != "807 No Video Data" {
		t.Fatalf("got %d %q, want 503 807", resp.StatusCode, resp.Header.Get("X-HDHomeRun-Error"))
	}
}
```

`ingest_test.go`:

```go
// A reconnect answered with 807 (no signal) keeps retrying instead of tearing
// the channel down as "tuner stolen"; subs survive and get data when the
// signal returns.
func TestReconnectNoSignalKeepsSubs(t *testing.T) {
	clock := newIngestClock(time.Date(2026, 10, 2, 20, 0, 0, 0, time.UTC))
	first, third := newPipeBody(), newPipeBody()
	var dials atomic.Int64
	im, _ := newTestIngest(t, func(ctx context.Context, url string) (io.ReadCloser, int, error) {
		switch dials.Add(1) {
		case 1:
			return first, 200, nil
		case 2:
			return nil, 503, &DeviceError{Status: 503, Reason: "807 No Video Data"}
		default:
			return third, 200, nil
		}
	}, clock)
	sub, err := im.Attach(context.Background(), 1, "u")
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	_ = first.Close() // connection lost
	waitFor(t, func() bool { return clock.Pending() > 0 })
	clock.Advance(ingestReconnectMin) // dial 2 → 807
	waitFor(t, func() bool { return dials.Load() == 2 })
	waitFor(t, func() bool { return clock.Pending() > 0 })
	clock.Advance(2 * ingestReconnectMin) // backoff doubled → dial 3
	waitFor(t, func() bool { return dials.Load() == 3 })
	if sub.isClosed() {
		t.Fatal("807 on reconnect closed the subscriber")
	}
	go func() { _, _ = third.Write(make([]byte, 188*4)) }()
	readN(t, sub.R, 188*4, 2*time.Second)
}
```

- [ ] **Step 2: Run** both → FAIL (fake returns 200; ingest tears down on 503).

- [ ] **Step 3: Implement.**

`faults/engine.go`:

```go
// Locked reports whether the tuner holds lock at these levels.
func (l Levels) Locked() bool { return l.Quality >= lockQuality }
```

`fake.go` in `handleStream`, after the hang check and before tuner allocation:

```go
	// A real HDHomeRun that cannot lock answers 503 "807 No Video Data".
	if !f.engine.Signal(guide).Locked() {
		f.log("reject", guide, "", "no signal")
		w.Header().Set("X-HDHomeRun-Error", "807 No Video Data")
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
```

`ingest.go` reconnect loop, replace the `if status == 503 {` block with:

```go
			if status == http.StatusServiceUnavailable && !errors.Is(dialErr, ErrNoSignal) {
				if body != nil {
					_ = body.Close()
				}
				// 805 / header-less 503 on reconnect: the tuner was taken.
				c.closeAllSubs(ErrTunersBusy)
				c.mu.Lock()
				c.teardownLocked()
				c.mu.Unlock()
				c.im.removeChannel(c.channelID, c)
				return
			}
			if errors.Is(dialErr, ErrNoSignal) {
				log.Printf("ingest: channel %d: no signal on reconnect, retrying", c.channelID)
			}
```

(the no-signal case falls through to the existing failed-dial path: close body if any, double backoff, retry until `ingestGiveUpAfter`).

`nosignal-reconnect.yaml`:

```yaml
name: nosignal-reconnect
duration: 45s
timeline:
  # Loss of lock: the open connection goes silent and new tunes get 807.
  - {at: 10s, fault: signal, quality: 10, for: 12s}
expect:
  sessionSurvives: true
  sequenceMonotonic: true
  maxFfmpegRestarts: 0
```

Docs: under Faults in `docs/dev/fake-hdhomerun.md` add: "While a channel's signal quality is below 30 (loss of lock), open connections go silent and new tunes get `503` with `X-HDHomeRun-Error: 807 No Video Data`, as on a real HDHomeRun."

- [ ] **Step 4: Run** `go test ./...`; `go test -tags ffmpeg -p 1 -parallel 2 -run 'TestScenarios/nosignal' -v ./internal/e2e/` → PASS. Existing `TestReconnect503ClosesSubs` must still pass (805/header-less path unchanged). Vet, lint.
- [ ] **Step 5: Commit** `fix(ingest): treat no-signal on reconnect as transient; fake answers 807 without lock`

---

### Task 4: Slow consumer — packet-aligned chunks, 16 MiB drop-oldest queue

**Files:**
- Create: `server/internal/stream/subqueue.go`, `server/internal/stream/subqueue_test.go`
- Modify: `server/internal/stream/ingest.go` (IngestSub, drain, addSubLocked, removeSub, shutdown, ingestChunk, pump alignment; remove stall timer)
- Modify: `server/internal/stream/ingest_test.go` (replace `TestStalledSubForceClosedOthersFlow`, adjust `TestConcurrentCloseVsForceClose`)
- Modify: `server/internal/e2e/pipeline_test.go` (`TestPipelineSlowConsumerRestarts` → `TestPipelineSlowConsumerKeepsProcess`)
- Rename: `server/internal/e2e/testdata/scenarios/slow-consumer-restart.yaml` → `slow-consumer-drops.yaml`
- Modify: `server/internal/e2e/testdata/scenarios/baseline-1080i.yaml` (remove `flaky`)

**Interfaces:**
- Produces: `type subQueue`; `func newSubQueue(max int, now func() time.Time) *subQueue`; methods `push(chunk, tables []byte) (dropped int)`, `pop() ([]byte, bool)`, `close()`, `idleFor(now time.Time) time.Duration` (time since last pop while bytes are queued; 0 when empty); constants `ingestSubQueueMax = 16 << 20`, `ingestSubStuckTimeout = 30 * time.Second`.

- [ ] **Step 1: Failing tests** in `subqueue_test.go`:

```go
package stream

import (
	"bytes"
	"testing"
	"time"
)

func TestSubQueueDropsOldestAndResendsTables(t *testing.T) {
	q := newSubQueue(3*188, time.Now)
	a, b, c, d := bytes.Repeat([]byte{1}, 188), bytes.Repeat([]byte{2}, 188), bytes.Repeat([]byte{3}, 188), bytes.Repeat([]byte{4}, 188)
	tables := bytes.Repeat([]byte{9}, 188)
	for _, ch := range [][]byte{a, b, c} {
		if n := q.push(ch, tables); n != 0 {
			t.Fatalf("dropped %d before full", n)
		}
	}
	if n := q.push(d, tables); n == 0 {
		t.Fatal("want a drop when full")
	}
	got, _ := q.pop()
	if !bytes.Equal(got, tables) {
		t.Fatalf("after a drop the queue head must be PAT+PMT, got %v", got[:1])
	}
	var rest []byte
	for {
		q.mu.Lock()
		empty := len(q.chunks) == 0
		q.mu.Unlock()
		if empty {
			break
		}
		ch, _ := q.pop()
		rest = append(rest, ch[0])
	}
	if !bytes.Equal(rest, []byte{3, 4}) && !bytes.Equal(rest, []byte{2, 3, 4}) {
		t.Fatalf("remaining order %v: oldest must go first, newest must stay", rest)
	}
}

func TestSubQueuePopBlocksUntilPushOrClose(t *testing.T) {
	q := newSubQueue(1<<20, time.Now)
	got := make(chan bool, 1)
	go func() { _, ok := q.pop(); got <- ok }()
	select {
	case <-got:
		t.Fatal("pop returned on an empty queue")
	case <-time.After(50 * time.Millisecond):
	}
	q.close()
	if ok := <-got; ok {
		t.Fatal("pop after close must report !ok")
	}
}

func TestSubQueueIdleFor(t *testing.T) {
	now := time.Date(2026, 10, 2, 20, 0, 0, 0, time.UTC)
	q := newSubQueue(1<<20, func() time.Time { return now })
	if d := q.idleFor(now.Add(time.Hour)); d != 0 {
		t.Fatalf("empty queue idleFor = %v, want 0", d)
	}
	q.push(make([]byte, 188), nil)
	if d := q.idleFor(now.Add(31 * time.Second)); d < 30*time.Second {
		t.Fatalf("idleFor = %v, want ≥30s with bytes queued and no pop", d)
	}
}
```

Replace `TestStalledSubForceClosedOthersFlow` in `ingest_test.go` with two tests:

```go
// A reader that stops consuming is not cut off: its queue drops old data and
// other subscribers keep flowing.
func TestSlowSubDropsInsteadOfClosing(t *testing.T) {
	clock := newIngestClock(time.Date(2026, 10, 2, 20, 0, 0, 0, time.UTC))
	pb := newPipeBody()
	im, _ := newTestIngest(t, func(ctx context.Context, url string) (io.ReadCloser, int, error) { return pb, 200, nil }, clock)
	slow, _ := im.Attach(context.Background(), 1, "u")
	fast, _ := im.Attach(context.Background(), 1, "u")
	go func() { _, _ = io.Copy(io.Discard, fast.R) }()
	chunk := make([]byte, 188*348) // ≈64 KiB, packet aligned
	for i := 0; i < (ingestSubQueueMax/len(chunk))+20; i++ {
		_, _ = pb.Write(chunk)
	}
	clock.Advance(10 * time.Second)
	if slow.isClosed() {
		t.Fatal("slow subscriber was closed after 10s; want drops instead")
	}
	_ = slow.Close()
	_ = fast.Close()
}

// A reader stuck for ingestSubStuckTimeout with data queued is closed.
func TestStuckSubClosedAfter30s(t *testing.T) {
	clock := newIngestClock(time.Date(2026, 10, 2, 20, 0, 0, 0, time.UTC))
	pb := newPipeBody()
	im, _ := newTestIngest(t, func(ctx context.Context, url string) (io.ReadCloser, int, error) { return pb, 200, nil }, clock)
	stuck, _ := im.Attach(context.Background(), 1, "u")
	_, _ = pb.Write(make([]byte, 188*10))
	clock.Advance(ingestSubStuckTimeout + time.Second)
	_, _ = pb.Write(make([]byte, 188*10)) // stuck check runs on the next chunk
	waitFor(t, stuck.isClosed)
}
```

In `TestConcurrentCloseVsForceClose`, replace the "fill the channel" loop with filling past `ingestSubQueueMax` and advancing the clock past `ingestSubStuckTimeout` before the concurrent Close, so the force-close path is still exercised.

Alignment test:

```go
// Chunks handed to subscribers are whole TS packets even when the device's
// reads split a packet.
func TestPumpAlignsChunksToPackets(t *testing.T) {
	clock := newIngestClock(time.Date(2026, 10, 2, 20, 0, 0, 0, time.UTC))
	pb := newPipeBody()
	im, _ := newTestIngest(t, func(ctx context.Context, url string) (io.ReadCloser, int, error) { return pb, 200, nil }, clock)
	sub, _ := im.Attach(context.Background(), 1, "u")
	defer sub.Close()
	go func() {
		_, _ = pb.Write(make([]byte, 100))
		_, _ = pb.Write(make([]byte, 188*2-100))
	}()
	q := sub.q
	waitFor(t, func() bool { q.mu.Lock(); defer q.mu.Unlock(); return q.bytes > 0 || len(q.chunks) == 0 && false })
	got := readN(t, sub.R, 188*2, 2*time.Second)
	if len(got) != 376 {
		t.Fatalf("read %d bytes", len(got))
	}
	q.mu.Lock()
	for _, ch := range q.chunks {
		if len(ch)%188 != 0 {
			t.Fatalf("queued chunk of %d bytes is not packet aligned", len(ch))
		}
	}
	q.mu.Unlock()
}
```

- [ ] **Step 2: Run** `go test ./internal/stream/` → FAIL (undefined `newSubQueue`, `sub.q`, constants).

- [ ] **Step 3: Implement `subqueue.go`:**

```go
package stream

import (
	"sync"
	"time"
)

// subQueue is one subscriber's byte-bounded FIFO of whole-TS-packet chunks.
// push never blocks: when full it drops the oldest chunks, and after any drop
// it puts the current PAT+PMT at the head so the demuxer resyncs immediately.
type subQueue struct {
	mu      sync.Mutex
	cond    *sync.Cond
	chunks  [][]byte
	bytes   int
	max     int
	closed  bool
	now     func() time.Time
	lastPop time.Time
}

func newSubQueue(max int, now func() time.Time) *subQueue {
	q := &subQueue{max: max, now: now, lastPop: now()}
	q.cond = sync.NewCond(&q.mu)
	return q
}

// push appends chunk, dropping the oldest chunks until it fits, and returns
// how many bytes were dropped.
func (q *subQueue) push(chunk, tables []byte) int {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return 0
	}
	dropped := 0
	for q.bytes+len(chunk)+len(tables) > q.max && len(q.chunks) > 0 {
		dropped += len(q.chunks[0])
		q.bytes -= len(q.chunks[0])
		q.chunks[0] = nil
		q.chunks = q.chunks[1:]
	}
	if dropped > 0 && len(tables) > 0 {
		q.chunks = append([][]byte{tables}, q.chunks...)
		q.bytes += len(tables)
	}
	q.chunks = append(q.chunks, chunk)
	q.bytes += len(chunk)
	q.cond.Signal()
	return dropped
}

// pop blocks until a chunk is available; ok is false once the queue is closed.
func (q *subQueue) pop() ([]byte, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.chunks) == 0 && !q.closed {
		q.cond.Wait()
	}
	if len(q.chunks) == 0 {
		return nil, false
	}
	c := q.chunks[0]
	q.chunks[0] = nil
	q.chunks = q.chunks[1:]
	q.bytes -= len(c)
	q.lastPop = q.now()
	return c, true
}

func (q *subQueue) close() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.closed = true
	q.chunks = nil
	q.bytes = 0
	q.cond.Broadcast()
}

// idleFor is how long data has waited without the reader taking any; 0 when
// nothing is queued.
func (q *subQueue) idleFor(now time.Time) time.Duration {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.bytes == 0 {
		return 0
	}
	return now.Sub(q.lastPop)
}
```

In `ingest.go`:
- Constants: remove `ingestSubChanCap` and `ingestStallTimeout`; add `ingestSubQueueMax = 16 << 20` (≈7 s at 19 Mbps) and `ingestSubStuckTimeout = 30 * time.Second`.
- `IngestSub`: replace `ch chan []byte`, `stalled`, `stallTimerCancel` with `q *subQueue` and `lastDropLog time.Time`.
- `addSubLocked`: `q: newSubQueue(ingestSubQueueMax, c.im.now)`.
- `drain`: after the join write, `for { chunk, ok := s.q.pop(); if !ok { return }; if _, err := s.pw.Write(chunk); err != nil { return } }`.
- `removeSub` and `shutdown`: replace `close(s.ch)` (and the recover wrapper) with `s.q.close()`; delete `cancelSubStallLocked` and its calls.
- Add `func (c *channelIngest) tablesLocked() []byte` returning a fresh copy of `lastPAT` + `lastPMT` (nil if both empty).
- `ingestChunk`:

```go
func (c *channelIngest) ingestChunk(chunk []byte) {
	c.mu.Lock()
	c.updateJoinLocked(chunk)
	tables := c.tablesLocked()
	now := c.im.now()
	var stuck []*IngestSub
	for s := range c.subs {
		if s.isClosed() {
			continue
		}
		if n := s.q.push(chunk, tables); n > 0 && now.Sub(s.lastDropLog) >= 10*time.Second {
			s.lastDropLog = now
			log.Printf("ingest: channel %d: transcoder behind, dropped %d KiB of old data", c.channelID, n>>10)
		}
		if s.q.idleFor(now) >= ingestSubStuckTimeout {
			stuck = append(stuck, s)
		}
	}
	c.mu.Unlock()
	for _, s := range stuck {
		log.Printf("ingest: channel %d: transcoder stuck for %v, closing its input", c.channelID, ingestSubStuckTimeout)
		_ = s.Close()
	}
}
```

- `pump` alignment: keep a `carry []byte` local (reset to nil whenever the watched body changes, Task 2's `body != watched` branch), and replace the chunk copy with:

```go
		if n > 0 {
			failStart = time.Time{}
			backoff = ingestReconnectMin
			data := append(carry, buf[:n]...)
			whole := len(data) - len(data)%tsPacketSize
			carry = append([]byte(nil), data[whole:]...)
			if whole > 0 {
				chunk := make([]byte, whole)
				copy(chunk, data[:whole])
				c.ingestChunk(chunk)
			}
		}
```

- [ ] **Step 4: e2e updates.**
  - Replace `TestPipelineSlowConsumerRestarts` with:

```go
func TestPipelineSlowConsumerKeepsProcess(t *testing.T) {
	t.Parallel()
	pl := newPipeline(t, true)
	// A transcoder that stops reading for 8s loses old data but is not restarted.
	pl.gate.Pause()
	time.Sleep(8 * time.Second)
	pl.gate.Resume()
	if !pl.bytesGrow(5 * time.Second) {
		t.Fatal("transcoder gets no bytes after resuming")
	}
	if n := pl.count.Starts(); n != 1 {
		t.Fatalf("transcoder starts = %d, want 1 (slowness must not restart FFmpeg)", n)
	}
}
```

  - `git mv` `slow-consumer-restart.yaml` → `slow-consumer-drops.yaml`; set `name: slow-consumer-drops`, keep `bowtie.slowConsumer {at: 10s, for: 10s}`, replace `expect` with `sequenceMonotonic: true`, `sessionSurvives: true`, `maxFfmpegRestarts: 0` (no xfail).
  - `baseline-1080i.yaml`: delete the `flaky:` line and its comment.

- [ ] **Step 5: Run** `go test ./...`; `go test -tags ffmpeg -p 1 -parallel 2 ./...` → PASS (run the ffmpeg suite twice; `baseline-1080i` must pass both times). Vet, lint.
- [ ] **Step 6: Real-player check (Mac only, no tuner needed).** Capture the HLS output of `slow-consumer-drops` (`BOWTIE_E2E_KEEP=1` if the harness supports keeping the session dir; otherwise reproduce with the gate locally) and play it with the macOS AVPlayer probe (`swiftc` the probe from the plan's appendix): status `readyToPlay`, rate 1, no `CoreMediaErrorDomain` error. Record the result in the commit message.
- [ ] **Step 7: Commit** `fix(ingest): buffer 16 MiB per transcoder and drop old data instead of restarting it`

---

### Task 5: Playlist continuity across FFmpeg restarts

**Files:**
- Modify: `server/internal/transcode/ffmpeg.go` (`JobSpec.Append`, `BuildArgs`)
- Modify: `server/internal/transcode/ffmpeg_test.go`
- Modify: `server/internal/stream/manager.go` (`restartSessionLocked`)
- Modify: `server/internal/stream/manager_test.go`
- Modify: `server/internal/hdhr/hdhrfake/scenario/scenario.go` (+ test) — `BowtieSpec.KillTranscoderAt`
- Modify: `server/internal/e2e/runners.go` (`CountingRunner.KillCurrent`), `server/internal/e2e/scenarios_test.go`
- Create: `server/internal/e2e/testdata/scenarios/transcoder-restart.yaml`

**Interfaces:**
- Produces: `JobSpec.Append bool`; `BowtieSpec.KillTranscoderAt time.Duration` (`yaml:"killTranscoderAt"`); `func (r *CountingRunner) KillCurrent() bool`.

- [ ] **Step 1: Failing tests.**

`ffmpeg_test.go`:

```go
func TestBuildArgsAppendOnRestart(t *testing.T) {
	s := transcode.JobSpec{OutDir: "/tmp/out", Stdin: strings.NewReader(""), D: transcode.Decision{
		VideoCodec: "h264", VideoEncoder: "libx264", AudioCopy: false,
		Profile: transcode.Profile{Name: "low", Height: 480, VideoKbps: 1500, AudioKbps: 96},
		Backend: transcode.BackendSoftware,
	}}
	if got := flagValue(transcode.BuildArgs(s), "-hls_flags"); got != "delete_segments+temp_file" {
		t.Fatalf("first start -hls_flags = %q", got)
	}
	s.Append = true
	if got := flagValue(transcode.BuildArgs(s), "-hls_flags"); got != "delete_segments+temp_file+append_list+discont_start" {
		t.Fatalf("restart -hls_flags = %q", got)
	}
}

func flagValue(args []string, flag string) string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag {
			return args[i+1]
		}
	}
	return ""
}
```

`manager_test.go` (stubRunner must record specs; add `specs []transcode.JobSpec` guarded by its mutex if absent):

```go
// The first FFmpeg for a session starts fresh; a restart appends to the
// existing playlist.
func TestRestartAppendsToPlaylist(t *testing.T) {
	st, cfg, clock, runner, chID, user := setupEnv(t)
	m, _, _ := newTestManagerWithDial(st, cfg, clock, runner, nil)
	if _, err := m.Start(context.Background(), user, chID, clientCaps("")); err != nil {
		t.Fatal(err)
	}
	runner.CrashLatest(errors.New("boom")) // existing helper, or add: finish the newest stub process with err
	clock.Advance(restartBackoffStart + time.Second)
	m.maintain()
	specs := runner.Specs()
	if len(specs) != 2 {
		t.Fatalf("starts = %d, want 2", len(specs))
	}
	if specs[0].Append || !specs[1].Append {
		t.Fatalf("Append = %v then %v, want false then true", specs[0].Append, specs[1].Append)
	}
}
```

`scenario_test.go`: `TestParseKillTranscoder` parses `bowtie: {killTranscoderAt: 15s}` and rejects `killTranscoderAt` ≥ duration.

`transcoder-restart.yaml`:

```yaml
name: transcoder-restart
duration: 45s
bowtie:
  killTranscoderAt: 15s
expect:
  sequenceMonotonic: true
  sessionSurvives: true
  maxGapSeconds: 14
  xfail: "playlist resets to segment 0 when FFmpeg restarts (Plan 2 Task 5)"
```

- [ ] **Step 2: Run** unit tests → FAIL; run `go test -tags ffmpeg -run 'TestScenarios/transcoder-restart' -v ./internal/e2e/` → the xfail holds (sequence goes backwards). This proves the scenario reproduces the freeze.

- [ ] **Step 3: Implement.**

`ffmpeg.go`:

```go
	// Append marks a restart into an existing session dir: FFmpeg continues the
	// playlist's numbering and marks a discontinuity instead of starting over at
	// seg00000 (which freezes players). Verified on FFmpeg 5.1.9 and 8.0.1.
	Append bool
```

and in `BuildArgs`:

```go
	hlsFlags := "delete_segments+temp_file"
	if s.Append {
		hlsFlags += "+append_list+discont_start"
	}
	// …
		"-hls_flags", hlsFlags,
```

`manager.go` `restartSessionLocked`: `spec := transcode.JobSpec{Stdin: sub.R, OutDir: sess.dir, D: sess.decision, HLSListSize: sess.hlsListSize, Append: true}` and log `stream: session %s: restarting ffmpeg (append to playlist)`.

`scenario.go`: add `KillTranscoderAt time.Duration \`yaml:"killTranscoderAt"\`` to `BowtieSpec`; validate `0 < KillTranscoderAt < Duration` when set.

`runners.go`: `CountingRunner` keeps `current stream.Process` (set in `Start` under `r.mu`); add

```go
// KillCurrent stops the newest transcoder (scenario fault). Reports whether
// one was running.
func (r *CountingRunner) KillCurrent() bool {
	r.mu.Lock()
	p := r.current
	r.mu.Unlock()
	if p == nil {
		return false
	}
	r.logf("killing current transcoder (scenario)")
	p.Stop()
	return true
}
```

`scenarios_test.go` in `runScenario`, next to the slow-consumer goroutine:

```go
		if sc.Bowtie != nil && sc.Bowtie.KillTranscoderAt > 0 {
			go func() {
				select {
				case <-ctx.Done():
				case <-time.After(sc.Bowtie.KillTranscoderAt):
					count.KillCurrent()
				}
			}()
		}
```

Note: `maxFfmpegRestarts` is not set for this scenario (the kill is the restart).

- [ ] **Step 4: Remove the xfail** from `transcoder-restart.yaml`. Run the scenario 3 times: sequence monotonic, gap ≤ 14 s, URIs unique (add `UniqueURIs bool` to the testplayer report and `expect.uniqueSegments: true` if the report lacks it; segment URIs must never repeat across the restart).
- [ ] **Step 5: Run** all gates incl. the ffmpeg suite.
- [ ] **Step 6: Commit** `fix(stream): restarted ffmpeg continues the playlist instead of resetting it`

---

### Task 6: Restart and exit logging

**Files:**
- Modify: `server/internal/stream/manager.go` (`supervise`, `restartSessionLocked`)
- Modify: `server/internal/stream/manager_test.go`

- [ ] **Step 1: Failing test:** capture `log` output (`log.SetOutput` to a buffer, restored in `t.Cleanup`), crash a session's process as in Task 5, run `maintain`, and assert the buffer contains `ffmpeg exited` with the session id, channel id and backend, then `restarting ffmpeg (append to playlist)` and `restarted` with the same session id.
- [ ] **Step 2: Run** → FAIL.
- [ ] **Step 3: Implement** in `supervise` on process exit: `log.Printf("stream: session %s channel %d (%s): ffmpeg exited after %v: %v", sess.id, sess.channelID, sess.decision.Backend, m.now().Sub(sess.procStart).Round(time.Second), err)`; in `restartSessionLocked` on success: `log.Printf("stream: session %s: ffmpeg restarted", sess.id)`, on each failure path include the session id.
- [ ] **Step 4: Run** gates. **Step 5: Commit** `feat(stream): log ffmpeg exits and restarts per session`

---

### Task 7: Verification on real hardware, docs, release

- [ ] **Step 1:** CHANGELOG `## [0.6.0]` with a Fixed section per spec §1–5 and the logging line; README unchanged.
- [ ] **Step 2:** `docs/dev/fake-hdhomerun.md`: `killTranscoderAt`, `slow-consumer-drops`, `transcoder-restart`, `nosignal-reconnect`.
- [ ] **Step 3: Real HDHomeRun (channel 9.1, one tuner, short):** run the local server on the Mac, start a session from Chrome (Playwright) and the iOS Simulator UI test; during playback `kill` the session's FFmpeg PID; both players continue after a short gap (Chrome `video.currentTime` keeps increasing; iOS test passes). Take one screenshot each.
- [ ] **Step 4:** Full gates; push branch; PR; CI green (gate on `gh pr checks --json bucket`); merge; tag `v0.6.0`; watch `release.yml`; confirm `ghcr.io/ajthom90/bowtie:0.6.0` (amd64+arm64).

## Appendix: macOS AVPlayer probe

```swift
import AVFoundation
let item = AVPlayerItem(url: URL(string: CommandLine.arguments[1])!)
let player = AVPlayer(playerItem: item)
player.play()
let start = Date()
while Date().timeIntervalSince(start) < 8 { RunLoop.main.run(until: Date().addingTimeInterval(0.5)) }
print("status=\(item.status.rawValue) rate=\(player.rate) err=\(item.error?.localizedDescription ?? "-")")
```

Build with `swiftc -O -o avprobe avprobe.swift`; serve the HLS dir with `python3 -m http.server`; pass `http://127.0.0.1:<port>/live.m3u8`.

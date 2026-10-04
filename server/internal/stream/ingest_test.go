package stream

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// --- synthetic MPEG-TS builder (A2) ------------------------------------------

// tsBuilder emits valid 188-byte TS packets. PAT (PID 0) + PMT once at stream
// start, then media-only packets thereafter — the join-buffer falsifier shape.
type tsBuilder struct {
	pmtPID   uint16
	mediaPID uint16
	cc       map[uint16]byte // continuity counters per PID
}

func newTSBuilder() *tsBuilder {
	return &tsBuilder{
		pmtPID:   0x0100,
		mediaPID: 0x0101,
		cc:       make(map[uint16]byte),
	}
}

func (b *tsBuilder) nextCC(pid uint16) byte {
	cc := b.cc[pid] & 0x0F
	b.cc[pid] = (cc + 1) & 0x0F
	return cc
}

// packet builds a 188-byte TS packet with the given PID and payload bytes
// (padded with 0xFF). pusi sets the payload_unit_start_indicator.
func (b *tsBuilder) packet(pid uint16, pusi bool, payload []byte) []byte {
	pkt := make([]byte, tsPacketSize)
	pkt[0] = tsSyncByte
	pkt[1] = byte(pid>>8) & 0x1F
	if pusi {
		pkt[1] |= 0x40
	}
	pkt[2] = byte(pid)
	pkt[3] = 0x10 | b.nextCC(pid) // payload only, no adaptation
	// pointer_field when PUSI for PSI
	off := 4
	if pusi {
		pkt[off] = 0 // pointer_field = 0
		off++
	}
	copy(pkt[off:], payload)
	// remainder already zero; fill unused with 0xFF for distinct media patterns
	for i := off + len(payload); i < tsPacketSize; i++ {
		pkt[i] = 0xFF
	}
	return pkt
}

// PAT returns a one-packet PAT: program 1 → pmtPID.
func (b *tsBuilder) PAT() []byte {
	// table_id=0x00, section_syntax=1, section_length computed
	// transport_stream_id=1, version=0 current=1, sec=0 last=0
	// program 1 → reserved 111 + pmtPID
	section := []byte{
		0x00,       // table_id PAT
		0xB0, 0x0D, // section_syntax + reserved + section_length=13
		0x00, 0x01, // transport_stream_id
		0xC1,       // reserved + version 0 + current_next=1
		0x00,       // section_number
		0x00,       // last_section_number
		0x00, 0x01, // program_number 1
		0xE0 | byte(b.pmtPID>>8), byte(b.pmtPID), // reserved + PMT PID
		// CRC32 placeholder (4 bytes) — parser ignores CRC
		0x00, 0x00, 0x00, 0x00,
	}
	// section_length = bytes after section_length field through CRC = 13
	// header after table_id+section_length: 5 + program 4 + CRC 4 = 13 ✓
	return b.packet(tsPIDPAT, true, section)
}

// PMT returns a one-packet PMT on pmtPID with one elementary stream (mediaPID).
func (b *tsBuilder) PMT() []byte {
	section := []byte{
		0x02,       // table_id PMT
		0xB0, 0x12, // section_length = 18
		0x00, 0x01, // program_number
		0xC1,       // version + current
		0x00, 0x00, // section / last
		0xE0 | byte(b.mediaPID>>8), byte(b.mediaPID), // PCR PID = media
		0xF0, 0x00, // program_info_length = 0
		0x1B, // stream_type H.264
		0xE0 | byte(b.mediaPID>>8), byte(b.mediaPID),
		0xF0, 0x00, // ES_info_length
		0x00, 0x00, 0x00, 0x00, // CRC placeholder
	}
	return b.packet(b.pmtPID, true, section)
}

// Media returns one media TS packet on mediaPID with a distinct payload marker.
func (b *tsBuilder) Media(seq byte) []byte {
	payload := make([]byte, 180)
	for i := range payload {
		payload[i] = seq
	}
	return b.packet(b.mediaPID, false, payload)
}

// StreamPATPMTOnceThenMedia returns PAT + PMT + nMedia media packets.
func (b *tsBuilder) StreamPATPMTOnceThenMedia(nMedia int) []byte {
	var out []byte
	out = append(out, b.PAT()...)
	out = append(out, b.PMT()...)
	for i := 0; i < nMedia; i++ {
		out = append(out, b.Media(byte(i))...)
	}
	return out
}

// --- fake clock with After (A1) ----------------------------------------------

type ingestClock struct {
	mu      sync.Mutex
	current time.Time
	timers  []ingestTimer
}

type ingestTimer struct {
	when time.Time
	ch   chan time.Time
}

func newIngestClock(start time.Time) *ingestClock {
	return &ingestClock{current: start}
}

func (c *ingestClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.current
}

func (c *ingestClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := make(chan time.Time, 1)
	when := c.current.Add(d)
	if !when.After(c.current) {
		ch <- c.current
		return ch
	}
	c.timers = append(c.timers, ingestTimer{when: when, ch: ch})
	return ch
}

// Advance moves the clock forward and fires due timers (no wall sleep).
func (c *ingestClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.current = c.current.Add(d)
	now := c.current
	var ready []ingestTimer
	var pending []ingestTimer
	for _, t := range c.timers {
		if !t.when.After(now) {
			ready = append(ready, t)
		} else {
			pending = append(pending, t)
		}
	}
	c.timers = pending
	c.mu.Unlock()
	for _, t := range ready {
		select {
		case t.ch <- now:
		default:
		}
	}
}

// HasTimerIn reports whether a timer is armed to fire exactly d from now.
// Tests wait on it before Advance so they never race a timer's registration
// (Pending alone can be satisfied by an unrelated timer, e.g. the watchdog).
func (c *ingestClock) HasTimerIn(d time.Duration) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, t := range c.timers {
		if t.when.Equal(c.current.Add(d)) {
			return true
		}
	}
	return false
}

// --- counting dial (A3) ------------------------------------------------------

type countingDial struct {
	n  atomic.Int64
	fn DialFunc
}

func (d *countingDial) Dial(ctx context.Context, url string) (io.ReadCloser, int, error) {
	d.n.Add(1)
	return d.fn(ctx, url)
}

func (d *countingDial) DialCalls() int64 { return d.n.Load() }

// --- stream body helpers -----------------------------------------------------

// pipeBody is a controllable live stream: Write from test, Read from pump.
type pipeBody struct {
	pr      *io.PipeReader
	pw      *io.PipeWriter
	closed  atomic.Bool
	onClose func()
}

func newPipeBody() *pipeBody {
	pr, pw := io.Pipe()
	return &pipeBody{pr: pr, pw: pw}
}

func (b *pipeBody) Read(p []byte) (int, error)  { return b.pr.Read(p) }
func (b *pipeBody) Write(p []byte) (int, error) { return b.pw.Write(p) }
func (b *pipeBody) Close() error {
	if b.closed.CompareAndSwap(false, true) {
		_ = b.pw.Close()
		_ = b.pr.Close()
		if b.onClose != nil {
			b.onClose()
		}
	}
	return nil
}
func (b *pipeBody) CloseWrite(err error) {
	_ = b.pw.CloseWithError(err)
}
func (b *pipeBody) Closed() bool { return b.closed.Load() }

// blockingBody blocks Read until Close (or unblock).
type blockingBody struct {
	mu      sync.Mutex
	closed  bool
	unblock chan struct{}
	data    []byte
	off     int
	onClose func()
}

func newBlockingBody(data []byte) *blockingBody {
	return &blockingBody{
		unblock: make(chan struct{}),
		data:    data,
	}
}

func (b *blockingBody) Read(p []byte) (int, error) {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return 0, io.ErrClosedPipe
	}
	if b.off < len(b.data) {
		n := copy(p, b.data[b.off:])
		b.off += n
		b.mu.Unlock()
		return n, nil
	}
	b.mu.Unlock()
	<-b.unblock
	return 0, io.EOF
}

func (b *blockingBody) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil
	}
	b.closed = true
	close(b.unblock)
	if b.onClose != nil {
		b.onClose()
	}
	return nil
}

func (b *blockingBody) Closed() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.closed
}

// --- test helpers ------------------------------------------------------------

func waitCond(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}

func readN(t *testing.T, r io.Reader, n int, timeout time.Duration) []byte {
	t.Helper()
	buf := make([]byte, n)
	got := 0
	deadline := time.Now().Add(timeout)
	for got < n && time.Now().Before(deadline) {
		// Set a short read by using a goroutine + timer when reader blocks.
		type res struct {
			n   int
			err error
		}
		ch := make(chan res, 1)
		go func(off int) {
			nn, err := r.Read(buf[off:])
			ch <- res{nn, err}
		}(got)
		select {
		case r := <-ch:
			if r.n > 0 {
				got += r.n
			}
			if r.err != nil && r.n == 0 {
				t.Fatalf("readN: %v after %d/%d", r.err, got, n)
			}
			if r.err != nil {
				// partial then err — return what we have if enough
				if got >= n {
					return buf[:n]
				}
			}
		case <-time.After(time.Until(deadline)):
			t.Fatalf("readN: timeout after %d/%d", got, n)
		}
	}
	if got < n {
		t.Fatalf("readN: got %d want %d", got, n)
	}
	return buf[:n]
}

func newTestIngest(t *testing.T, dial DialFunc, clock *ingestClock) (*IngestManager, *countingDial) {
	t.Helper()
	cd := &countingDial{fn: dial}
	opts := []IngestOption{}
	if clock != nil {
		opts = append(opts, WithIngestClock(clock.Now, clock.After))
	}
	im := NewIngestManager(cd.Dial, opts...)
	t.Cleanup(func() { im.Shutdown() })
	return im, cd
}

// --- tests -------------------------------------------------------------------

func TestSingleFlightConcurrentAttach(t *testing.T) {
	clock := newIngestClock(time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC))
	body := newBlockingBody(newTSBuilder().StreamPATPMTOnceThenMedia(10))
	var dialStarted sync.WaitGroup
	dialStarted.Add(1)
	var releaseDial sync.WaitGroup
	releaseDial.Add(1)

	dial := func(ctx context.Context, url string) (io.ReadCloser, int, error) {
		dialStarted.Done()
		releaseDial.Wait()
		return body, 200, nil
	}
	im, cd := newTestIngest(t, dial, clock)

	const n = 10
	var start sync.WaitGroup
	start.Add(1)
	errCh := make(chan error, n)
	subs := make([]*IngestSub, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			start.Wait()
			sub, err := im.Attach(context.Background(), 42, "http://hdhr/auto/v1")
			if err != nil {
				errCh <- err
				return
			}
			subs[i] = sub
		}()
	}
	start.Done()
	// Wait until dial is in progress, then release — all 10 must share one dial.
	dialStarted.Wait()
	// Give other attaches time to pile up on the per-channel mutex.
	time.Sleep(20 * time.Millisecond)
	releaseDial.Done()
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("Attach: %v", err)
	}
	if cd.DialCalls() != 1 {
		t.Fatalf("DialCalls=%d want 1", cd.DialCalls())
	}
	if im.AttachCalls() != int64(n) {
		t.Fatalf("AttachCalls=%d want %d", im.AttachCalls(), n)
	}
	for _, s := range subs {
		if s == nil {
			t.Fatal("nil sub")
		}
		_ = s.Close()
	}
}

func TestFanoutDeliversIdenticalBytes(t *testing.T) {
	clock := newIngestClock(time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC))
	b := newTSBuilder()
	payload := b.StreamPATPMTOnceThenMedia(50)
	pb := newPipeBody()

	im, cd := newTestIngest(t, func(ctx context.Context, url string) (io.ReadCloser, int, error) {
		return pb, 200, nil
	}, clock)

	sub1, err := im.Attach(context.Background(), 1, "u")
	if err != nil {
		t.Fatal(err)
	}
	sub2, err := im.Attach(context.Background(), 1, "u")
	if err != nil {
		t.Fatal(err)
	}
	if cd.DialCalls() != 1 {
		t.Fatalf("dial=%d", cd.DialCalls())
	}

	go func() {
		_, _ = pb.Write(payload)
		// keep open
	}()

	n := len(payload)
	got1 := readN(t, sub1.R, n, 2*time.Second)
	got2 := readN(t, sub2.R, n, 2*time.Second)
	if !bytes.Equal(got1, got2) {
		t.Fatalf("fan-out mismatch: len1=%d len2=%d", len(got1), len(got2))
	}
	if !bytes.Equal(got1, payload) {
		t.Fatalf("bytes differ from source (first mismatch near tables/media)")
	}
	_ = sub1.Close()
	_ = sub2.Close()
}

func TestJoinBufferGivesLateSubTables(t *testing.T) {
	// A2 falsifier: PAT+PMT ONCE, then media-only ≥1MB. Late sub's first bytes
	// must be the exact PAT then PMT packets (byte-compare) BEFORE any media.
	clock := newIngestClock(time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC))
	b := newTSBuilder()
	pat := b.PAT()
	pmt := b.PMT()

	// ≥1 MB of media-only after tables. Join buffer caps at 1MB but always
	// retains last PAT+PMT for prepend.
	const mediaBytes = ingestJoinBufMax + 64*1024
	nMedia := mediaBytes / tsPacketSize
	tables := append(append([]byte{}, pat...), pmt...)
	media := make([]byte, 0, nMedia*tsPacketSize)
	for i := 0; i < nMedia; i++ {
		media = append(media, b.Media(byte(i))...)
	}

	pb := newPipeBody()
	im, _ := newTestIngest(t, func(ctx context.Context, url string) (io.ReadCloser, int, error) {
		return pb, 200, nil
	}, clock)

	// Early sub consumes the live stream so the pump runs and join buffer fills.
	early, err := im.Attach(context.Background(), 7, "u")
	if err != nil {
		t.Fatal(err)
	}
	// Discard early sub's data in background so its channel does not stall.
	go func() {
		_, _ = io.Copy(io.Discard, early.R)
	}()

	// Write tables then a lot of media.
	if _, err := pb.Write(tables); err != nil {
		t.Fatal(err)
	}
	// Write media in chunks so pump processes progressively.
	const chunk = 64 * 1024
	for off := 0; off < len(media); off += chunk {
		end := off + chunk
		if end > len(media) {
			end = len(media)
		}
		if _, err := pb.Write(media[off:end]); err != nil {
			t.Fatal(err)
		}
	}
	// Let pump drain join state.
	waitCond(t, 2*time.Second, func() bool {
		// Heuristic: attach late and check — better: wait until Active + time for reads
		return true
	})
	time.Sleep(50 * time.Millisecond)

	late, err := im.Attach(context.Background(), 7, "u")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = late.Close() }()

	head := readN(t, late.R, 2*tsPacketSize, 2*time.Second)
	if !bytes.Equal(head[:tsPacketSize], pat) {
		t.Fatalf("late sub first packet is not PAT\n got %x\nwant %x", head[:tsPacketSize], pat)
	}
	if !bytes.Equal(head[tsPacketSize:2*tsPacketSize], pmt) {
		t.Fatalf("late sub second packet is not PMT\n got %x\nwant %x", head[tsPacketSize:2*tsPacketSize], pmt)
	}
	// Next bytes must be media (PID media), not tables again as the only content —
	// prove tables were prepended then media from join buffer.
	more := readN(t, late.R, tsPacketSize, 2*time.Second)
	if tsPID(more) != b.mediaPID {
		t.Fatalf("after tables expected media PID 0x%04x, got 0x%04x", b.mediaPID, tsPID(more))
	}

	_ = early.Close()
}

func TestSlowSubDropsInsteadOfClosing(t *testing.T) {
	clock := newIngestClock(time.Date(2026, 10, 2, 20, 0, 0, 0, time.UTC))
	pb := newPipeBody()
	im, _ := newTestIngest(t, func(ctx context.Context, url string) (io.ReadCloser, int, error) { return pb, 200, nil }, clock)
	slow, _ := im.Attach(context.Background(), 1, "u") // never reads
	fast, _ := im.Attach(context.Background(), 1, "u")
	var fastBytes atomic.Int64
	go func() {
		buf := make([]byte, 64*1024)
		for {
			n, err := fast.R.Read(buf)
			fastBytes.Add(int64(n))
			if err != nil {
				return
			}
		}
	}()
	chunk := make([]byte, 188*348) // ≈64 KiB, packet aligned
	for i := 0; i < (ingestSubQueueMax/len(chunk))+20; i++ {
		_, _ = pb.Write(chunk)
	}
	// 10s of a live stream: well past the old 2s cutoff, under the 30s rule.
	feedAndStep(pb, clock, chunk, 10)
	if slow.isClosed() {
		t.Fatal("slow subscriber was closed after 10s; want drops instead")
	}
	before := fastBytes.Load()
	feedAndStep(pb, clock, chunk, 1)
	waitFor(t, 2*time.Second, func() bool { return fastBytes.Load() > before })
	_ = slow.Close()
	_ = fast.Close()
}

// A reader stuck for ingestSubStuckTimeout with data queued is closed.
func TestStuckSubClosedAfter30s(t *testing.T) {
	clock := newIngestClock(time.Date(2026, 10, 2, 20, 0, 0, 0, time.UTC))
	pb := newPipeBody()
	im, _ := newTestIngest(t, func(ctx context.Context, url string) (io.ReadCloser, int, error) { return pb, 200, nil }, clock)
	stuck, _ := im.Attach(context.Background(), 1, "u") // never reads
	feedAndStep(pb, clock, make([]byte, 188*10), int(ingestSubStuckTimeout/time.Second)-2)
	if stuck.isClosed() {
		t.Fatal("closed before the 30s stuck timeout")
	}
	feedAndStep(pb, clock, make([]byte, 188*10), 4)
	waitFor(t, 2*time.Second, stuck.isClosed)
}

// feedAndStep plays a live device: write chunk, then advance the clock 1s,
// steps times (a single big jump would look like silence to the watchdog).
func feedAndStep(pb *pipeBody, clock *ingestClock, chunk []byte, steps int) {
	for i := 0; i < steps; i++ {
		_, _ = pb.Write(chunk)
		time.Sleep(2 * time.Millisecond)
		clock.Advance(time.Second)
	}
}

func TestLastCloseTailThenDialClosed(t *testing.T) {
	clock := newIngestClock(time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC))
	var body atomic.Pointer[blockingBody]
	b0 := newBlockingBody(newTSBuilder().StreamPATPMTOnceThenMedia(5))
	body.Store(b0)
	var closes atomic.Int64

	im, cd := newTestIngest(t, func(ctx context.Context, url string) (io.ReadCloser, int, error) {
		bb := body.Load()
		bb.onClose = func() { closes.Add(1) }
		return bb, 200, nil
	}, clock)

	sub, err := im.Attach(context.Background(), 9, "u")
	if err != nil {
		t.Fatal(err)
	}
	if cd.DialCalls() != 1 {
		t.Fatal("expected 1 dial")
	}
	_ = sub.Close()

	// Within tail: re-Attach must NOT redial.
	time.Sleep(10 * time.Millisecond) // schedule tail goroutine
	sub2, err := im.Attach(context.Background(), 9, "u")
	if err != nil {
		t.Fatal(err)
	}
	if cd.DialCalls() != 1 {
		t.Fatalf("re-Attach within tail redialed: DialCalls=%d", cd.DialCalls())
	}
	_ = sub2.Close()

	// Advance past 5s tail → body closes.
	time.Sleep(10 * time.Millisecond)
	clock.Advance(ingestTailDuration + time.Millisecond)
	waitCond(t, 2*time.Second, func() bool {
		return body.Load().Closed()
	})

	// New Attach after tail ends → new dial.
	b1 := newBlockingBody(newTSBuilder().StreamPATPMTOnceThenMedia(2))
	body.Store(b1)
	sub3, err := im.Attach(context.Background(), 9, "u")
	if err != nil {
		t.Fatal(err)
	}
	if cd.DialCalls() != 2 {
		t.Fatalf("after tail DialCalls=%d want 2", cd.DialCalls())
	}
	_ = sub3.Close()
}

func TestDial503ErrTunersBusy(t *testing.T) {
	clock := newIngestClock(time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC))
	im, cd := newTestIngest(t, func(ctx context.Context, url string) (io.ReadCloser, int, error) {
		return nil, 503, errors.New("busy")
	}, clock)

	_, err := im.Attach(context.Background(), 1, "u")
	if !errors.Is(err, ErrTunersBusy) {
		t.Fatalf("err=%v want errors.Is ErrTunersBusy", err)
	}
	if cd.DialCalls() != 1 {
		t.Fatalf("dial=%d", cd.DialCalls())
	}
	// Immediate — no retry storm.
	_, err = im.Attach(context.Background(), 1, "u")
	if !errors.Is(err, ErrTunersBusy) {
		t.Fatalf("second: %v", err)
	}
	if cd.DialCalls() != 2 {
		t.Fatalf("each Attach dials once on 503; got %d", cd.DialCalls())
	}
}

func TestReconnectKeepsSubs(t *testing.T) {
	clock := newIngestClock(time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC))
	b := newTSBuilder()
	part1 := b.StreamPATPMTOnceThenMedia(5)
	// Second dial continues with media (tables already known to join buf from first).
	part2 := b.Media(99)
	part2 = append(part2, b.Media(100)...)

	var phase atomic.Int64

	im, cd := newTestIngest(t, func(ctx context.Context, url string) (io.ReadCloser, int, error) {
		pb := newPipeBody()
		n := phase.Add(1)
		if n == 1 {
			go func() {
				_, _ = pb.Write(part1)
				// EOF to trigger reconnect
				pb.CloseWrite(io.EOF)
			}()
		} else {
			go func() {
				// small delay for attach to be ready — wall << 1s
				time.Sleep(5 * time.Millisecond)
				_, _ = pb.Write(part2)
			}()
		}
		return pb, 200, nil
	}, clock)

	sub, err := im.Attach(context.Background(), 3, "u")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sub.Close() }()

	// Read first part.
	_ = readN(t, sub.R, len(part1), 2*time.Second)

	// Pump should be in reconnect backoff (1s). Advance clock.
	waitCond(t, 2*time.Second, func() bool {
		// dial may not have started after yet — advance repeatedly
		return true
	})
	time.Sleep(20 * time.Millisecond)
	clock.Advance(ingestReconnectMin + time.Millisecond)

	waitCond(t, 2*time.Second, func() bool {
		return cd.DialCalls() >= 2
	})

	// Sub still open and receives part2 bytes.
	got := readN(t, sub.R, len(part2), 2*time.Second)
	if !bytes.Equal(got, part2) {
		t.Fatalf("after reconnect got %x want %x", got, part2)
	}
	if sub.isClosed() {
		t.Fatal("sub should stay open across non-503 reconnect")
	}
}

func TestReconnect503ClosesSubs(t *testing.T) {
	clock := newIngestClock(time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC))
	b := newTSBuilder()
	part1 := b.StreamPATPMTOnceThenMedia(3)
	var dials atomic.Int64

	im, _ := newTestIngest(t, func(ctx context.Context, url string) (io.ReadCloser, int, error) {
		n := dials.Add(1)
		if n == 1 {
			pb := newPipeBody()
			go func() {
				_, _ = pb.Write(part1)
				pb.CloseWrite(io.EOF)
			}()
			return pb, 200, nil
		}
		return nil, 503, errors.New("stolen")
	}, clock)

	sub, err := im.Attach(context.Background(), 4, "u")
	if err != nil {
		t.Fatal(err)
	}
	_ = readN(t, sub.R, len(part1), 2*time.Second)

	time.Sleep(20 * time.Millisecond)
	clock.Advance(ingestReconnectMin + time.Millisecond)

	waitCond(t, 2*time.Second, func() bool {
		return sub.isClosed()
	})
	// Reader should see EOF/error.
	buf := make([]byte, 16)
	_, err = sub.R.Read(buf)
	if err == nil {
		// may still have leftover; drain
		_, err = io.Copy(io.Discard, sub.R)
	}
	if err == nil {
		t.Fatal("expected read error after 503 reconnect closed sub")
	}
}

func TestGiveUpAfter60s(t *testing.T) {
	clock := newIngestClock(time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC))
	b := newTSBuilder()
	part1 := b.StreamPATPMTOnceThenMedia(2)
	var dials atomic.Int64

	im, cd := newTestIngest(t, func(ctx context.Context, url string) (io.ReadCloser, int, error) {
		n := dials.Add(1)
		if n == 1 {
			pb := newPipeBody()
			go func() {
				_, _ = pb.Write(part1)
				pb.CloseWrite(io.EOF)
			}()
			return pb, 200, nil
		}
		// Persistent failure (non-503).
		return nil, 0, errors.New("network down")
	}, clock)

	sub, err := im.Attach(context.Background(), 5, "u")
	if err != nil {
		t.Fatal(err)
	}
	_ = readN(t, sub.R, len(part1), 2*time.Second)

	// Drive reconnect attempts until 60s of failure elapses.
	// Backoff: 1,2,4,8,16,30,30,... — advance in steps.
	time.Sleep(20 * time.Millisecond)
	// Advance in 1s increments totaling >60s, firing each after timer.
	for i := 0; i < 70; i++ {
		clock.Advance(time.Second)
		time.Sleep(2 * time.Millisecond) // let reconnect loop schedule next After
		if sub.isClosed() {
			break
		}
	}
	waitCond(t, 2*time.Second, func() bool {
		return sub.isClosed()
	})
	if cd.DialCalls() < 2 {
		t.Fatalf("expected reconnect attempts, dials=%d", cd.DialCalls())
	}
}

func TestDoubleCloseSafe(t *testing.T) {
	clock := newIngestClock(time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC))
	body := newBlockingBody(newTSBuilder().StreamPATPMTOnceThenMedia(2))
	im, _ := newTestIngest(t, func(ctx context.Context, url string) (io.ReadCloser, int, error) {
		return body, 200, nil
	}, clock)

	sub, err := im.Attach(context.Background(), 1, "u")
	if err != nil {
		t.Fatal(err)
	}
	if err := sub.Close(); err != nil {
		t.Fatal(err)
	}
	if err := sub.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestConcurrentCloseVsForceClose(t *testing.T) {
	clock := newIngestClock(time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC))
	pb := newPipeBody()
	im, _ := newTestIngest(t, func(ctx context.Context, url string) (io.ReadCloser, int, error) {
		return pb, 200, nil
	}, clock)

	sub, err := im.Attach(context.Background(), 1, "u") // never reads
	if err != nil {
		t.Fatal(err)
	}
	// Bring the sub to the edge of the stuck timeout.
	chunk := make([]byte, 188*10)
	feedAndStep(pb, clock, chunk, int(ingestSubStuckTimeout/time.Second)-1)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_ = sub.Close()
	}()
	go func() {
		defer wg.Done()
		feedAndStep(pb, clock, chunk, 2) // force-close path
	}()
	wg.Wait()
	// No panic / race — second path is safe.
	_ = sub.Close()
}

func TestShutdownVsConcurrentAttach(t *testing.T) {
	clock := newIngestClock(time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC))
	var n atomic.Int64
	im, _ := newTestIngest(t, func(ctx context.Context, url string) (io.ReadCloser, int, error) {
		n.Add(1)
		return newBlockingBody(newTSBuilder().StreamPATPMTOnceThenMedia(1)), 200, nil
	}, clock)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(id int64) {
			defer wg.Done()
			sub, err := im.Attach(context.Background(), id%3, "u")
			if err != nil {
				return
			}
			_ = sub.Close()
		}(int64(i))
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(5 * time.Millisecond)
		im.Shutdown()
	}()
	wg.Wait()
	// Post-shutdown Attach fails.
	_, err := im.Attach(context.Background(), 99, "u")
	if err == nil {
		t.Fatal("Attach after Shutdown should fail")
	}
}

func TestActiveChannels(t *testing.T) {
	clock := newIngestClock(time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC))
	im, _ := newTestIngest(t, func(ctx context.Context, url string) (io.ReadCloser, int, error) {
		return newBlockingBody(newTSBuilder().StreamPATPMTOnceThenMedia(1)), 200, nil
	}, clock)

	if len(im.ActiveChannels()) != 0 {
		t.Fatal("expected empty")
	}
	s1, err := im.Attach(context.Background(), 10, "u")
	if err != nil {
		t.Fatal(err)
	}
	s2, err := im.Attach(context.Background(), 20, "u")
	if err != nil {
		t.Fatal(err)
	}
	active := im.ActiveChannels()
	if len(active) != 2 {
		t.Fatalf("active=%v", active)
	}
	_ = s1.Close()
	_ = s2.Close()
}

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
	defer func() { _ = sub.Close() }()
	go func() { _, _ = first.Write(make([]byte, 188*4)) }() // then silence
	readN(t, sub.R, 188*4, 2*time.Second)

	waitFor(t, 3*time.Second, func() bool { return clock.HasTimerIn(ingestIdleTimeout) }) // watchdog armed
	clock.Advance(ingestIdleTimeout)
	waitFor(t, 3*time.Second, func() bool { return first.closed.Load() })
	// Reconnect backoff (1s) then redial.
	waitFor(t, 3*time.Second, func() bool { return clock.HasTimerIn(ingestReconnectMin) })
	clock.Advance(ingestReconnectMin)
	waitFor(t, 3*time.Second, func() bool { return dials.Load() == 2 })
	go func() { _, _ = second.Write(make([]byte, 188*4)) }()
	readN(t, sub.R, 188*4, 2*time.Second)
}

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
	defer func() { _ = sub.Close() }()
	_ = first.Close() // connection lost
	waitFor(t, 3*time.Second, func() bool { return clock.HasTimerIn(ingestReconnectMin) })
	clock.Advance(ingestReconnectMin) // dial 2 → 807
	waitFor(t, 3*time.Second, func() bool { return dials.Load() == 2 })
	waitFor(t, 3*time.Second, func() bool { return clock.HasTimerIn(2 * ingestReconnectMin) })
	clock.Advance(2 * ingestReconnectMin) // backoff doubled → dial 3
	waitFor(t, 3*time.Second, func() bool { return dials.Load() == 3 })
	if sub.isClosed() {
		t.Fatal("807 on reconnect closed the subscriber")
	}
	go func() { _, _ = third.Write(make([]byte, 188*4)) }()
	readN(t, sub.R, 188*4, 2*time.Second)
}

// While one channel's device dial is slow, the admin tuner view and other
// channels' starts must not block on it.
func TestActiveChannelsDoesNotWaitOnDialingChannel(t *testing.T) {
	clock := newIngestClock(time.Date(2026, 10, 2, 22, 0, 0, 0, time.UTC))
	release := make(chan struct{})
	defer close(release)
	other := newPipeBody()
	im, _ := newTestIngest(t, func(ctx context.Context, url string) (io.ReadCloser, int, error) {
		if url == "slow" {
			<-release // tuner still answering
			return nil, 0, errors.New("released")
		}
		return other, 200, nil
	}, clock)
	go func() { _, _ = im.Attach(context.Background(), 1, "slow") }()
	time.Sleep(50 * time.Millisecond) // channel 1 is now dialing under its lock

	done := make(chan struct{})
	go func() {
		_ = im.ActiveChannels()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("ActiveChannels blocked on a channel that is dialing")
	}
	attached := make(chan error, 1)
	go func() {
		sub, err := im.Attach(context.Background(), 2, "fast")
		if err == nil {
			_ = sub.Close()
		}
		attached <- err
	}()
	select {
	case err := <-attached:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("another channel's Attach blocked behind a dialing channel")
	}
}

// When ingest gives up on a stuck transcoder it also runs the owner's
// force-close hook, so an FFmpeg that ignores EOF is still stopped.
func TestStuckSubRunsForceCloseHook(t *testing.T) {
	clock := newIngestClock(time.Date(2026, 10, 2, 22, 0, 0, 0, time.UTC))
	pb := newPipeBody()
	im, _ := newTestIngest(t, func(ctx context.Context, url string) (io.ReadCloser, int, error) { return pb, 200, nil }, clock)
	stuck, _ := im.Attach(context.Background(), 1, "u") // never reads
	var stopped atomic.Bool
	stuck.OnForceClose(func() { stopped.Store(true) })
	feedAndStep(pb, clock, make([]byte, 188*10), int(ingestSubStuckTimeout/time.Second)+2)
	waitFor(t, 2*time.Second, stopped.Load)
}

// While the tuner keeps answering "no signal", retries stay at most
// ingestNoSignalRetry apart instead of doubling toward ingestReconnectMax, so
// playback resumes soon after the signal returns.
func TestNoSignalRetriesStayFrequent(t *testing.T) {
	clock := newIngestClock(time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC))
	first, last := newPipeBody(), newPipeBody()
	var dials atomic.Int64
	im, _ := newTestIngest(t, func(ctx context.Context, url string) (io.ReadCloser, int, error) {
		switch n := dials.Add(1); {
		case n == 1:
			return first, 200, nil
		case n <= 6:
			return nil, 503, &DeviceError{Status: 503, Reason: "807 No Video Data"}
		default:
			return last, 200, nil
		}
	}, clock)
	sub, err := im.Attach(context.Background(), 1, "u")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sub.Close() }()
	_ = first.Close() // connection lost; reconnect starts at the 1s minimum
	waitFor(t, 3*time.Second, func() bool { return clock.HasTimerIn(ingestReconnectMin) })
	clock.Advance(ingestReconnectMin)
	// Five 807 answers in a row: each next retry must be ≤ ingestNoSignalRetry away.
	for want := int64(2); want <= 6; want++ {
		waitFor(t, 3*time.Second, func() bool { return dials.Load() == want })
		waitFor(t, 3*time.Second, func() bool { return clock.HasTimerIn(ingestNoSignalRetry) })
		clock.Advance(ingestNoSignalRetry)
	}
	waitFor(t, 3*time.Second, func() bool { return dials.Load() == 7 })
	go func() { _, _ = last.Write(make([]byte, 188*4)) }()
	readN(t, sub.R, 188*4, 2*time.Second)
}

// Ingest remembers each channel's last tune outcome: a stream means "ok", an
// 806/807 answer means "noSignal" (on first attach and on reconnect).
func TestReceptionRecordsTuneOutcomes(t *testing.T) {
	clock := newIngestClock(time.Date(2026, 10, 3, 10, 0, 0, 0, time.UTC))
	body := newPipeBody()
	var dials atomic.Int64
	im, _ := newTestIngest(t, func(ctx context.Context, url string) (io.ReadCloser, int, error) {
		switch dials.Add(1) {
		case 1:
			return nil, 503, &DeviceError{Status: 503, Reason: "807 No Video Data"}
		case 2:
			return body, 200, nil
		default:
			return nil, 503, &DeviceError{Status: 503, Reason: "807 No Video Data"}
		}
	}, clock)
	if _, ok := im.Reception(1); ok {
		t.Fatal("reception known before any tune")
	}
	if _, err := im.Attach(context.Background(), 1, "u"); !errors.Is(err, ErrNoSignal) {
		t.Fatalf("first attach err = %v, want ErrNoSignal", err)
	}
	if r, _ := im.Reception(1); r.State != ReceptionNoSignal || !r.CheckedAt.Equal(clock.Now()) {
		t.Fatalf("after 807: %+v, want noSignal at %v", r, clock.Now())
	}
	sub, err := im.Attach(context.Background(), 1, "u")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sub.Close() }()
	if r, _ := im.Reception(1); r.State != ReceptionOK {
		t.Fatalf("after stream: %+v, want ok", r)
	}
	_ = body.Close() // signal lost mid-stream; the reconnect gets 807
	waitFor(t, 3*time.Second, func() bool { return clock.HasTimerIn(ingestReconnectMin) })
	clock.Advance(ingestReconnectMin)
	waitFor(t, 3*time.Second, func() bool { r, _ := im.Reception(1); return r.State == ReceptionNoSignal })
}

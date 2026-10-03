package stream

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

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

// ingestIdleTimeout: a device streams continuously once tuned, so this long
// without a byte means the connection is dead (silent device or a reset the
// kernel dropped). The body is closed and the normal reconnect path runs.
const ingestIdleTimeout = 8 * time.Second

// ErrTunersBusy is returned when a device dial reports HTTP 503 (all tuners in
// use). Handlers map it via errors.Is to the standard tuners-busy 503 payload.
var ErrTunersBusy = errors.New("all tuners in use")

// ErrNoSignal is returned when the device tuned but could not deliver the
// channel (HDHomeRun 806 Tune Failed / 807 No Video Data): weak or no signal.
var ErrNoSignal = errors.New("no signal")

// DeviceError is a non-2xx device response. Reason is the X-HDHomeRun-Error
// header ("807 No Video Data"), empty when the device sent none.
type DeviceError struct {
	Status int
	Reason string
}

func (e *DeviceError) Error() string {
	if e.Reason != "" {
		return fmt.Sprintf("device returned HTTP %d: %s", e.Status, e.Reason)
	}
	return fmt.Sprintf("device returned HTTP %d", e.Status)
}

// Is classifies the error: 806/807 → ErrNoSignal; any other 503 (805 or no
// header) → ErrTunersBusy.
func (e *DeviceError) Is(target error) bool {
	noSignal := strings.HasPrefix(e.Reason, "806") || strings.HasPrefix(e.Reason, "807")
	switch target {
	case ErrNoSignal:
		return noSignal
	case ErrTunersBusy:
		return e.Status == http.StatusServiceUnavailable && !noSignal
	}
	return false
}

// DialFunc opens a device stream. It returns the body, the HTTP status code
// (0 when not applicable), and an error. A 503 status maps to ErrTunersBusy.
type DialFunc func(ctx context.Context, url string) (io.ReadCloser, int, error)

const (
	ingestChunkSize  = 64 * 1024
	ingestJoinBufMax = 1 * 1024 * 1024
	// ingestSubQueueMax bounds each transcoder's backlog (≈7s at 19 Mbps);
	// beyond it the oldest data is dropped instead of cutting FFmpeg off.
	ingestSubQueueMax = 16 << 20
	// ingestSubStuckTimeout: a transcoder that takes nothing for this long is
	// hung and its input is closed so the manager restarts it.
	ingestSubStuckTimeout = 30 * time.Second
	ingestTailDuration    = 5 * time.Second
	ingestReconnectMin    = 1 * time.Second
	ingestReconnectMax    = 30 * time.Second
	ingestGiveUpAfter     = 60 * time.Second
	tsPacketSize          = 188
	tsSyncByte            = 0x47
	tsPIDPAT              = 0
)

// IngestManager owns per-channel device streams and fans MPEG-TS out to
// process-scoped IngestSubs. One device dial per channel (single-flight).
type IngestManager struct {
	dial  DialFunc
	now   func() time.Time
	after func(time.Duration) <-chan time.Time

	attachCalls atomic.Int64

	mu       sync.Mutex
	channels map[int64]*channelIngest
	stopped  bool
}

// IngestOption configures NewIngestManager.
type IngestOption func(*IngestManager)

// WithIngestClock injects now/after for tests. ALL ingest timing (idle watchdog, stuck rule, tail,
// reconnect backoff, give-up) must use these — never wall time.
func WithIngestClock(now func() time.Time, after func(time.Duration) <-chan time.Time) IngestOption {
	return func(im *IngestManager) {
		if now != nil {
			im.now = now
		}
		if after != nil {
			im.after = after
		}
	}
}

// NewIngestManager constructs an IngestManager. dial is required.
func NewIngestManager(dial DialFunc, opts ...IngestOption) *IngestManager {
	im := &IngestManager{
		dial:     dial,
		now:      func() time.Time { return time.Now().UTC() },
		after:    time.After,
		channels: make(map[int64]*channelIngest),
	}
	for _, opt := range opts {
		opt(im)
	}
	return im
}

// AttachCalls returns how many times Attach has been entered (test instrumentation).
func (im *IngestManager) AttachCalls() int64 {
	return im.attachCalls.Load()
}

// Attach joins or creates the per-channel ingest and returns a process-scoped
// sub. Concurrent Attaches for one channel single-flight the dial.
// HTTP 503 from dial → ErrTunersBusy (errors.Is-able).
func (im *IngestManager) Attach(ctx context.Context, channelID int64, url string) (*IngestSub, error) {
	im.attachCalls.Add(1)

	im.mu.Lock()
	if im.stopped {
		im.mu.Unlock()
		return nil, errors.New("ingest manager shutdown")
	}
	ch := im.channels[channelID]
	if ch == nil {
		ch = newChannelIngest(im, channelID)
		im.channels[channelID] = ch
	}
	im.mu.Unlock()

	return ch.attach(ctx, url)
}

// ActiveChannels returns channel IDs with an open device connection (including
// the 5s post-last-Close tail).
func (im *IngestManager) ActiveChannels() []int64 {
	// Snapshot under im.mu, then ask each channel without holding it: a
	// channel that is dialing holds its own lock for up to the dial timeout.
	im.mu.Lock()
	ids := make([]int64, 0, len(im.channels))
	chs := make([]*channelIngest, 0, len(im.channels))
	for id, ch := range im.channels {
		ids = append(ids, id)
		chs = append(chs, ch)
	}
	im.mu.Unlock()
	out := make([]int64, 0, len(chs))
	for i, ch := range chs {
		if ch.isActive() {
			out = append(out, ids[i])
		}
	}
	return out
}

// Shutdown closes every channel and rejects further Attach calls.
func (im *IngestManager) Shutdown() {
	im.mu.Lock()
	if im.stopped {
		im.mu.Unlock()
		return
	}
	im.stopped = true
	chs := make([]*channelIngest, 0, len(im.channels))
	for _, ch := range im.channels {
		chs = append(chs, ch)
	}
	im.channels = make(map[int64]*channelIngest)
	im.mu.Unlock()

	for _, ch := range chs {
		ch.shutdown()
	}
}

func (im *IngestManager) removeChannel(channelID int64, ch *channelIngest) {
	im.mu.Lock()
	defer im.mu.Unlock()
	if im.channels[channelID] == ch {
		delete(im.channels, channelID)
	}
}

// SubCount returns the number of subscribers on a channel (0 if none).
func (im *IngestManager) SubCount(channelID int64) int {
	im.mu.Lock()
	ch := im.channels[channelID]
	im.mu.Unlock()
	if ch == nil {
		return 0
	}
	ch.mu.Lock()
	defer ch.mu.Unlock()
	return len(ch.subs)
}

// IngestSub is one process's view of a channel ingest.
// R is the only read handle; Close only via IngestSub.Close (not R.Close alone
// for lifecycle — R.Close is safe but does not drop the refcount; always call Close).
type IngestSub struct {
	R io.ReadCloser

	pr *io.PipeReader
	pw *io.PipeWriter
	q  *subQueue

	ci *channelIngest

	closeOnce sync.Once
	closed    atomic.Bool

	// lastDropLog rate-limits the "transcoder behind" log (under channelIngest.mu).
	lastDropLog time.Time
}

// Close detaches this sub. Double-Close is safe. Last Close on a channel starts
// the 5s device-stream tail.
func (s *IngestSub) Close() error {
	s.closeOnce.Do(func() {
		s.closed.Store(true)
		// Closing the pipe reader unblocks any Write in drain and signals EOF to consumers.
		_ = s.pr.Close()
		if s.ci != nil {
			s.ci.removeSub(s)
		}
	})
	return nil
}

func (s *IngestSub) isClosed() bool {
	return s.closed.Load()
}

func (s *IngestSub) drain(join []byte) {
	defer func() {
		_ = s.pw.Close()
	}()
	if len(join) > 0 {
		if _, err := s.pw.Write(join); err != nil {
			return
		}
	}
	for {
		chunk, ok := s.q.pop()
		if !ok {
			return
		}
		if _, err := s.pw.Write(chunk); err != nil {
			return
		}
	}
}

// channelIngest is the per-channel single-flight device stream + fan-out.
type channelIngest struct {
	im        *IngestManager
	channelID int64

	mu sync.Mutex

	url     string
	body    io.ReadCloser
	running bool // pump goroutine active / device held (incl. tail)
	// active mirrors running for lock-free reads (isActive): attach holds mu
	// for the whole device dial, up to the dial timeout.
	active atomic.Bool

	subs map[*IngestSub]struct{}

	// join buffer state
	lastPAT  []byte
	lastPMT  []byte
	pmtPIDs  map[uint16]struct{}
	sincePAT []byte // non-table packets since last PAT
	tsScrap  []byte // partial packet across chunk boundaries

	// tail: after last Close, keep device open this long
	tailCancel chan struct{}

	// pump control
	stopPump chan struct{}
	pumpDone chan struct{}
}

func newChannelIngest(im *IngestManager, channelID int64) *channelIngest {
	return &channelIngest{
		im:        im,
		channelID: channelID,
		subs:      make(map[*IngestSub]struct{}),
		pmtPIDs:   make(map[uint16]struct{}),
	}
}

func (c *channelIngest) isActive() bool {
	return c.active.Load()
}

func (c *channelIngest) attach(ctx context.Context, url string) (*IngestSub, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Cancel any pending tail — re-Attach reuses the open device stream.
	c.cancelTailLocked()

	if c.running {
		c.url = url
		return c.addSubLocked(), nil
	}

	// The device stream may be shared by other sessions and must not die when
	// the request that started it returns.
	body, status, err := c.im.dial(context.WithoutCancel(ctx), url)
	if err == nil && (status < 200 || status > 299) && status != 0 {
		err = &DeviceError{Status: status}
	}
	if err != nil {
		if body != nil {
			_ = body.Close()
		}
		if status == http.StatusServiceUnavailable && !errors.Is(err, ErrNoSignal) && !errors.Is(err, ErrTunersBusy) {
			return nil, fmt.Errorf("ingest dial: %w (%v)", ErrTunersBusy, err)
		}
		return nil, fmt.Errorf("ingest dial: %w", err)
	}
	if body == nil {
		return nil, errors.New("ingest dial: nil body")
	}

	c.url = url
	c.body = body
	c.running = true
	c.active.Store(true)
	c.stopPump = make(chan struct{})
	c.pumpDone = make(chan struct{})
	go c.pump()

	return c.addSubLocked(), nil
}

func (c *channelIngest) addSubLocked() *IngestSub {
	pr, pw := io.Pipe()
	sub := &IngestSub{
		R:  pr,
		pr: pr,
		pw: pw,
		q:  newSubQueue(ingestSubQueueMax, c.im.now),
		ci: c,
	}
	join := c.joinSnapshotLocked()
	c.subs[sub] = struct{}{}
	go sub.drain(join)
	return sub
}

func (c *channelIngest) joinSnapshotLocked() []byte {
	n := len(c.lastPAT) + len(c.lastPMT) + len(c.sincePAT)
	if n == 0 {
		return nil
	}
	out := make([]byte, 0, n)
	out = append(out, c.lastPAT...)
	out = append(out, c.lastPMT...)
	out = append(out, c.sincePAT...)
	return out
}

func (c *channelIngest) removeSub(s *IngestSub) {
	c.mu.Lock()
	if _, ok := c.subs[s]; ok {
		delete(c.subs, s)
		// Wakes drain, which exits.
		s.q.close()
	}
	n := len(c.subs)
	running := c.running
	c.mu.Unlock()

	if n == 0 && running {
		c.startTail()
	}
}

func (c *channelIngest) startTail() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.subs) > 0 || !c.running {
		return
	}
	c.cancelTailLocked()
	cancel := make(chan struct{})
	c.tailCancel = cancel
	// Arm the timer before spawning so the tail starts now, not whenever the
	// goroutine is scheduled.
	expired := c.im.after(ingestTailDuration)
	go func() {
		select {
		case <-expired:
			c.mu.Lock()
			if len(c.subs) == 0 && c.running && c.tailCancel == cancel {
				c.teardownLocked()
				c.mu.Unlock()
				c.im.removeChannel(c.channelID, c)
				return
			}
			c.mu.Unlock()
		case <-cancel:
		}
	}()
}

func (c *channelIngest) cancelTailLocked() {
	if c.tailCancel != nil {
		close(c.tailCancel)
		c.tailCancel = nil
	}
}

// teardownLocked closes the device body and stops the pump. Caller holds c.mu.
func (c *channelIngest) teardownLocked() {
	c.cancelTailLocked()
	if c.stopPump != nil {
		select {
		case <-c.stopPump:
		default:
			close(c.stopPump)
		}
	}
	if c.body != nil {
		_ = c.body.Close()
		c.body = nil
	}
	c.running = false
	c.active.Store(false)
	// Wait for pump outside lock would deadlock if pump needs mu — pump exits
	// on body close / stopPump without needing to re-enter while we hold mu
	// only if we don't wait here. Callers that need pumpDone wait after unlock.
}

func (c *channelIngest) shutdown() {
	c.mu.Lock()
	subs := make([]*IngestSub, 0, len(c.subs))
	for s := range c.subs {
		subs = append(subs, s)
	}
	c.teardownLocked()
	// Drop subs under lock so removeSub sees them gone / ch already closed carefully
	for s := range c.subs {
		delete(c.subs, s)
		s.q.close()
	}
	c.mu.Unlock()

	for _, s := range subs {
		// Close reader; drain exits; do not call removeSub path again for refcount
		s.closeOnce.Do(func() {
			s.closed.Store(true)
			_ = s.pr.Close()
		})
	}
}

func (c *channelIngest) closeAllSubs(reason error) {
	_ = reason
	c.mu.Lock()
	subs := make([]*IngestSub, 0, len(c.subs))
	for s := range c.subs {
		subs = append(subs, s)
	}
	c.mu.Unlock()
	for _, s := range subs {
		_ = s.Close()
	}
}

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

func (c *channelIngest) pump() {
	defer close(c.pumpDone)

	buf := make([]byte, ingestChunkSize)
	var failStart time.Time
	backoff := ingestReconnectMin

	var lastRead atomic.Int64
	var watched io.ReadCloser
	var carry []byte // partial TS packet from the previous read
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
			carry = nil
			stopWatch()
			lastRead.Store(c.im.now().UnixNano())
			watched = body
			stopWatch = c.watchIdle(body, &lastRead)
		}

		n, err := body.Read(buf)
		if n > 0 {
			lastRead.Store(c.im.now().UnixNano())
		}

		// Check stop before processing (body.Close unblocks Read).
		select {
		case <-stop:
			return
		default:
		}

		if n > 0 {
			failStart = time.Time{}
			backoff = ingestReconnectMin
			var whole []byte
			whole, carry = alignTS(carry, buf[:n])
			if len(whole) > 0 {
				c.ingestChunk(whole)
			}
		}

		if err == nil {
			continue
		}

		// EOF or read error → reconnect path (unless stopped / no longer running).
		c.mu.Lock()
		if !c.running {
			c.mu.Unlock()
			return
		}
		// Close spent body
		if c.body != nil {
			_ = c.body.Close()
			c.body = nil
		}
		url := c.url
		stop = c.stopPump
		c.mu.Unlock()

		if failStart.IsZero() {
			failStart = c.im.now()
		}

		for {
			if c.im.now().Sub(failStart) >= ingestGiveUpAfter {
				c.closeAllSubs(err)
				c.mu.Lock()
				c.teardownLocked()
				c.mu.Unlock()
				c.im.removeChannel(c.channelID, c)
				return
			}

			select {
			case <-stop:
				return
			case <-c.im.after(backoff):
			}

			// Check again after wait (Attach tail teardown / shutdown).
			c.mu.Lock()
			if !c.running {
				c.mu.Unlock()
				return
			}
			stop = c.stopPump
			c.mu.Unlock()

			select {
			case <-stop:
				return
			default:
			}

			body, status, dialErr := c.im.dial(context.Background(), url)
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
			if dialErr == nil && body != nil {
				c.mu.Lock()
				if !c.running {
					c.mu.Unlock()
					_ = body.Close()
					return
				}
				c.body = body
				c.mu.Unlock()
				// Resume outer read loop; reset failure window on successful dial.
				failStart = time.Time{}
				backoff = ingestReconnectMin
				break
			}
			if body != nil {
				_ = body.Close()
			}
			if backoff < ingestReconnectMax {
				backoff *= 2
				if backoff > ingestReconnectMax {
					backoff = ingestReconnectMax
				}
			}
		}
	}
}

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

// tablesLocked returns a fresh copy of the current PAT+PMT (nil if unknown).
func (c *channelIngest) tablesLocked() []byte {
	if len(c.lastPAT)+len(c.lastPMT) == 0 {
		return nil
	}
	out := make([]byte, 0, len(c.lastPAT)+len(c.lastPMT))
	out = append(out, c.lastPAT...)
	return append(out, c.lastPMT...)
}

func (c *channelIngest) updateJoinLocked(chunk []byte) {
	c.tsScrap = append(c.tsScrap, chunk...)
	for {
		// Resync to 0x47
		for len(c.tsScrap) > 0 && c.tsScrap[0] != tsSyncByte {
			c.tsScrap = c.tsScrap[1:]
		}
		if len(c.tsScrap) < tsPacketSize {
			return
		}
		pkt := c.tsScrap[:tsPacketSize]
		c.tsScrap = c.tsScrap[tsPacketSize:]
		// Defensive copy — scrap may be resliced later
		p := make([]byte, tsPacketSize)
		copy(p, pkt)
		c.handleTSPacketLocked(p)
	}
}

func (c *channelIngest) handleTSPacketLocked(pkt []byte) {
	pid := tsPID(pkt)
	switch {
	case pid == tsPIDPAT:
		c.lastPAT = pkt
		c.pmtPIDs = parsePATPMTPIDs(pkt)
		c.sincePAT = c.sincePAT[:0]
		c.capJoinLocked()
	case c.isPMTPID(pid):
		c.lastPMT = pkt
		// Tables are served from lastPAT/lastPMT; do not duplicate into sincePAT.
		c.capJoinLocked()
	default:
		c.sincePAT = append(c.sincePAT, pkt...)
		c.capJoinLocked()
	}
}

func (c *channelIngest) isPMTPID(pid uint16) bool {
	_, ok := c.pmtPIDs[pid]
	return ok
}

func (c *channelIngest) capJoinLocked() {
	for len(c.lastPAT)+len(c.lastPMT)+len(c.sincePAT) > ingestJoinBufMax && len(c.sincePAT) >= tsPacketSize {
		c.sincePAT = c.sincePAT[tsPacketSize:]
	}
	// If still over (tables alone > 1MB — impossible for single packets), drop since.
	if len(c.lastPAT)+len(c.lastPMT)+len(c.sincePAT) > ingestJoinBufMax {
		c.sincePAT = nil
	}
}

func tsPID(pkt []byte) uint16 {
	if len(pkt) < 3 {
		return 0x1FFF
	}
	return uint16(pkt[1]&0x1F)<<8 | uint16(pkt[2])
}

// parsePATPMTPIDs extracts program_map_PIDs from a PAT TS packet (minimal PSI).
func parsePATPMTPIDs(pkt []byte) map[uint16]struct{} {
	out := make(map[uint16]struct{})
	if len(pkt) < tsPacketSize || pkt[0] != tsSyncByte {
		return out
	}
	pusi := pkt[1]&0x40 != 0
	afc := (pkt[3] >> 4) & 0x3
	i := 4
	if afc == 2 || afc == 3 { // adaptation field present
		if i >= len(pkt) {
			return out
		}
		afLen := int(pkt[i])
		i++
		i += afLen
	}
	if i >= len(pkt) {
		return out
	}
	if pusi {
		ptr := int(pkt[i])
		i++
		i += ptr
	}
	// section: table_id(1) + section_length(12 bits in 2 bytes) ...
	if i+8 > len(pkt) {
		return out
	}
	if pkt[i] != 0x00 { // PAT table_id
		return out
	}
	sectionLen := int(pkt[i+1]&0x0F)<<8 | int(pkt[i+2])
	// section starts at table_id; programs begin after 8-byte header:
	// table_id(1)+section_len(2)+ts_id(2)+ver(1)+sec_num(1)+last_sec(1) = 8
	progStart := i + 8
	// end of programs = section start + 3 + section_length - 4 (CRC)
	sectionStart := i
	end := sectionStart + 3 + sectionLen - 4
	if end > len(pkt) {
		end = len(pkt)
	}
	for p := progStart; p+4 <= end; p += 4 {
		progNum := int(pkt[p])<<8 | int(pkt[p+1])
		pid := uint16(pkt[p+2]&0x1F)<<8 | uint16(pkt[p+3])
		if progNum != 0 {
			out[pid] = struct{}{}
		}
	}
	return out
}

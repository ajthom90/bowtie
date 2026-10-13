package stream

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// WallClockLayout is how Bowtie writes wall-clock times (playlists and the
// timeline API): RFC 3339 with milliseconds.
const WallClockLayout = "2006-01-02T15:04:05.000Z07:00"

const (
	// hlsSegmentDuration is FFmpeg's -hls_time for live sessions.
	hlsSegmentDuration = 4 * time.Second
	// timelineGapThreshold: a reference segment whose file says it started
	// this much later than the timeline predicts (beyond the encoder's own
	// steady lag) follows a real gap that FFmpeg closed; the timeline jumps
	// forward instead of absorbing it.
	timelineGapThreshold = 2 * time.Second
	// timelineObserveEvery is how often the observer reads the playlists.
	timelineObserveEvery = 500 * time.Millisecond
	// timelineGateTimeout bounds fed time without publishing: a held run
	// that hasn't opened after this much fed time since its process start,
	// or an open run in which a required rendition published nothing for
	// this much fed time. Fed time accrues only on the manager's ticks
	// where fresh source bytes reached FFmpeg (see ExpireStalledRun). It is
	// the bound a new session gets for its first playlist.
	timelineGateTimeout = playlistTimeout
	// timelineMaxStalls failed runs (expired, crashed, or stopped for the
	// anchor cap) end the session. Opening a run doesn't reset the count;
	// timelineHealthyAfter of every required rendition publishing
	// continuously does, which matches the crash backoff's healthy reset.
	// Continuously means no rendition goes timelineHealthyGap without
	// publishing: segments are 4-6 s and liveness ticks 5 s, so a tick
	// without a new segment is normal, two in a row are not.
	timelineMaxStalls    = 3
	timelineHealthyAfter = healthyResetAfter
	timelineHealthyGap   = 2 * hlsSegmentDuration
	// timelinePTSReads is how many observations may fail to read a
	// segment's first PTS before a segment that isn't its rendition's first
	// in the run chains on EXTINF instead. A first segment never does: its
	// rendition just isn't ready.
	timelinePTSReads = 6
	// timelineMaxPending, timelineMaxAnchors and timelineCaptionKeep bound
	// the per-run state: segments waiting per rendition, gap anchors, and
	// stamps kept below the reference's window for a lagging caption
	// playlist.
	timelineMaxPending  = 64
	timelineMaxAnchors  = 64
	timelineCaptionKeep = 64
	// ptsPeriod is the 33-bit 90 kHz PTS period in seconds.
	ptsPeriod = float64(int64(1)<<33) / 90000
)

// Timeline is a session's wall-clock record of its segments. An observer
// reads every rendition playlist on its own clock (never on client
// requests). Each FFmpeg process is a run. A run is held out of the served
// playlists until every required rendition (the reference, and every
// rendition ever published) has a publishable segment in it, with its
// first PTS read; then it opens and publishes as one discontinuity epoch
// for all renditions at once. A run that never opens is never served.
//
// Within a run, a segment's place on the broadcast clock is its first PTS
// relative to the run's earliest first PTS (all renditions share FFmpeg's
// output clock), so publication timing can't move it. The run's wall-clock
// start comes from the segment files' mtimes. Gaps FFmpeg closed are found
// on the reference rendition (the top rung) only, and a sibling segment is
// stamped only once the reference has passed it, so a gap shifts every
// rendition at the same media point.
type Timeline struct {
	dir        string
	renditions []string
	ref        string
	caption    string // the reference's caption playlist (Layout.CaptionPlaylist without .m3u8)
	now        func() time.Time
	logf       func(format string, args ...any)

	obsMu sync.Mutex // serializes Observe and MarkRestart (file reads included)

	// segmentRead, when non-nil, replaces the stat and first-PTS read of a
	// segment file. Tests stall it. Production leaves it nil. Observe calls
	// it without holding t.mu.
	segmentRead func(path string, dur float64, now time.Time) (obs time.Time, file bool, pts int64, hasPTS bool)

	mu       sync.Mutex
	cur      *tlRun
	runs     int             // runs started so far (run ids, for logs)
	required map[string]bool // every rendition ever published: a run opens only with all of them
	epoch    int64           // epoch of the last opened run (-1: none)
	stalls   int             // failed runs since the last healthy stretch (the stall budget)
	healthy  time.Duration   // how long every required rendition has kept publishing
	maxEnd   time.Time       // latest end of any published segment
	capMseq  int64           // FFmpeg's MEDIA-SEQUENCE of the caption playlist (-1: none seen)
	floors   map[string]int64
	rends    map[string]*tlRend
}

type tlRunState int

const (
	runHeld tlRunState = iota // segments collected, none served
	runOpen                   // published as one epoch
)

type tlRun struct {
	id          int
	state       tlRunState
	expired     bool          // ExpireStalledRun gave up on it: it never opens or publishes again (and is counted)
	started     bool          // a process is writing it (run 0, or after ProcessStarted)
	fed         time.Duration // fed time since the process started
	overflow    []string      // required renditions the anchor cap would cut off: stop and expire
	headShift   time.Duration // shift before anchors[0] (of the anchors compacted away)
	histFloor   float64       // non-required siblings' segments below this lost their gap history
	epoch       int64
	hasBase     bool
	base        int64     // earliest first PTS of any rendition in the run (90 kHz)
	start       time.Time // wall clock of media 0
	floor       time.Time // nothing in this run starts earlier (the previous end)
	refLag      time.Duration
	refMediaEnd float64 // media end of the reference's last stamped segment
	anchors     []tlAnchor
	seen        map[string]bool // renditions with a segment in this run
	pub         map[string]bool // renditions with a published segment in this run
}

type tlAnchor struct {
	media float64       // seconds into the run's media timeline
	shift time.Duration // total forward jump from here on
}

// shiftAt is the shift of the last anchor at or before media (the
// reference's own segments start on anchors). Anchors are added in media
// order with growing shifts.
func (r *tlRun) shiftAt(media float64) time.Duration {
	for i := len(r.anchors) - 1; i >= 0; i-- {
		if r.anchors[i].media <= media+1e-9 {
			return r.anchors[i].shift
		}
	}
	return r.headShift
}

// shiftFor is the shift of the anchor interval that [m, e) overlaps most:
// a sibling segment takes the shift of the reference segment(s) it shares
// most of its span with, so cuts a few milliseconds apart around a gap get
// the same shift.
func (r *tlRun) shiftFor(m, e float64) time.Duration {
	overlap := func(a, b float64) float64 { return math.Min(e, b) - math.Max(m, a) }
	best, bestOv := r.headShift, overlap(math.Inf(-1), r.anchors0())
	for i, a := range r.anchors {
		end := math.Inf(1)
		if i+1 < len(r.anchors) {
			end = r.anchors[i+1].media
		}
		if ov := overlap(a.media, end); ov > bestOv+1e-9 {
			best, bestOv = a.shift, ov
		}
	}
	return best
}

func (r *tlRun) anchors0() float64 {
	if len(r.anchors) == 0 {
		return math.Inf(1)
	}
	return r.anchors[0].media
}

type tlRend struct {
	ffMseq      int64           // FFmpeg's MEDIA-SEQUENCE last observed
	lastSeq     int64           // highest FFmpeg sequence seen (-1: none)
	pending     []*tlItem       // seen in the current run, not yet published
	segs        map[int64]tlSeg // FFmpeg sequence → published stamp (window, and captions' lag)
	skip        map[int64]bool  // FFmpeg sequences never served (window, and the reference's caption lag)
	nextB       int64           // Bowtie's next media sequence number
	prevEnd     time.Time       // end of the last published segment
	everPub     bool            // published at least one segment
	runMediaEnd float64         // media end of the last segment assigned in the run
	asgRun      int             // run of the last assigned segment (-1: none)
	pubTick     bool            // stamped a segment since the last liveness tick
	fedNoPub    time.Duration   // fed time since it last published (open run)
	sincePub    time.Duration   // tick time since it last published (open run; healthy check)
	nextFirst   bool            // the run's first segment was trimmed with an empty queue; the next arrival carries it
}

type tlItem struct {
	seq      int64
	dur      float64
	path     string
	obs      time.Time // file mtime − EXTINF (or now − EXTINF)
	file     bool      // obs came from the segment file
	raw      int64     // first PTS (90 kHz)
	hasPTS   bool
	reads    int     // PTS read attempts
	media    float64 // seconds from the run's base
	hasMedia bool
	first    bool // the rendition's first segment in its run
}

// placeable: the item's PTS was read, or (not a first segment) the reads
// ran out and it may chain on EXTINF.
func (it *tlItem) placeable() bool {
	return it.hasPTS || (!it.first && it.reads >= timelinePTSReads)
}

// tlSeg is a published segment: what serving needs, kept while the
// segment is in FFmpeg's window (or the caption playlist's).
type tlSeg struct {
	bseq  int64 // Bowtie's media sequence number
	start time.Time
	dur   float64
	epoch int64
	tag   bool // first segment of a new epoch (#EXT-X-DISCONTINUITY)
}

// seconds converts a float duration in seconds to the nearest nanosecond.
// Truncating 4.004 * Second yields 4003999999ns, one nanosecond under the
// millisecond the duration already states.
func seconds(f float64) time.Duration {
	return time.Duration(math.Round(f * float64(time.Second)))
}

// FormatWallClock writes t in WallClockLayout, rounded to the nearest
// millisecond. Playlist PDT and the timeline endpoint both use it, so a
// segment start and the live edge derived from it agree.
func FormatWallClock(t time.Time) string {
	return t.UTC().Round(time.Millisecond).Format(WallClockLayout)
}

// NewTimeline returns the timeline of the session in dir. renditions are
// the media playlists (without .m3u8) to observe, reference (top rung)
// first; the reference's caption playlist, <reference>_vtt.m3u8, is read
// for its MEDIA-SEQUENCE only. now must be the real clock: stamps are
// compared with segment file mtimes. Run 0's process start is now.
func NewTimeline(dir string, renditions []string, now func() time.Time) *Timeline {
	t := &Timeline{
		dir: dir, renditions: renditions, now: now, logf: log.Printf,
		epoch: -1, capMseq: -1, required: map[string]bool{}, floors: map[string]int64{}, rends: map[string]*tlRend{},
	}
	if len(renditions) > 0 {
		t.ref = renditions[0]
		t.caption = t.ref + "_vtt"
	}
	t.cur = t.newRun()
	t.cur.started = true // run 0 is the first process's
	return t
}

func (t *Timeline) newRun() *tlRun {
	r := &tlRun{id: t.runs, state: runHeld, histFloor: math.Inf(-1), seen: map[string]bool{}, pub: map[string]bool{}}
	t.runs++
	return r
}

// Run observes every timelineObserveEvery until ctx ends.
func (t *Timeline) Run(ctx context.Context) {
	tick := time.NewTicker(timelineObserveEvery)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			t.Observe()
		}
	}
}

// Observe reads every rendition playlist, records new segments and
// publishes what can be published.
func (t *Timeline) Observe() {
	t.obsMu.Lock()
	defer t.obsMu.Unlock()
	t.observe()
}

// MarkRestart ends the current run (after observing everything the dead
// process wrote) and starts the next one, held until it is complete. Call
// it at the start of every append restart attempt, before anything else.
// A held run is discarded: its segments are never served. A run that
// ended without ExpireStalledRun expiring it (a crash supervise saw first,
// or a stuck kill) counts toward the stall budget too. It returns the
// budget used; at timelineMaxStalls the caller ends the session instead
// of starting FFmpeg.
//
// It is idempotent across attempts that never start FFmpeg (an attach
// that fails during an outage): until ProcessStarted, the current run has
// no process, so a further MarkRestart ends nothing, counts nothing and
// creates no other run.
func (t *Timeline) MarkRestart() (stalls int) {
	t.obsMu.Lock()
	defer t.obsMu.Unlock()
	t.mu.Lock()
	started, stalls := t.cur.started, t.stalls
	t.mu.Unlock()
	if !started {
		return stalls // no process since the last MarkRestart: nothing ended
	}
	t.observe()
	t.mu.Lock()
	defer t.mu.Unlock()
	run := t.cur
	dropped := 0
	for _, name := range t.renditions {
		r := t.rends[name]
		if r == nil {
			continue
		}
		for _, it := range r.pending {
			r.skip[it.seq] = true
			dropped++
		}
		r.pending = nil
	}
	switch {
	case run.state == runHeld && len(run.seen) > 0:
		t.logf("stream: timeline %s: run %d discarded before it was complete (no %s); its %d segments are never served",
			filepath.Base(t.dir), run.id, strings.Join(t.blockingLocked(run), ","), dropped)
	case dropped > 0:
		t.logf("stream: timeline %s: run %d: %d segments past the reference's last are never served",
			filepath.Base(t.dir), run.id, dropped)
	}
	if !run.expired {
		t.stalls++
		t.logf("stream: timeline %s: run %d ended without being expired (%d failed runs in a row)", filepath.Base(t.dir), run.id, t.stalls)
	}
	t.healthy = 0
	t.cur = t.newRun()
	return t.stalls
}

// ProcessStarted records that the restart's FFmpeg is running: the
// current run (made by MarkRestart) now belongs to a process, and the next
// MarkRestart ends it.
func (t *Timeline) ProcessStarted() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.cur.started = true
}

// ExpireStalledRun is the manager's liveness tick. elapsed is the
// manager's time since its previous tick for this process (or since the
// process started); fresh says whether fresh source bytes reached FFmpeg
// in that time (not the cached join or reset snapshot). Fed time accrues
// elapsed on fresh ticks only; a quiet tick pauses it and never erases it.
//
// The current run is expired, atomically, when:
//   - it is held and its fed time since the process start reached
//     timelineGateTimeout;
//   - it is open and a required rendition published nothing for that much
//     fed time (the reference stopped, a sibling's PTS can't be read, or
//     FFmpeg is fed and writes nothing);
//   - the anchor cap would cut a required rendition off its gap history.
//
// An expired run never opens, so nothing can open between this call and
// the caller stopping FFmpeg; the restart's MarkRestart discards it.
// Every expiry counts toward the stall budget, which only
// timelineHealthyAfter of every required rendition publishing
// continuously (none silent for timelineHealthyGap) resets;
// stalls is the budget used, and at timelineMaxStalls the caller ends the
// session.
func (t *Timeline) ExpireStalledRun(elapsed time.Duration, fresh bool) (missing []string, stalls int, expired bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	run := t.cur
	var fed time.Duration
	if fresh {
		fed = elapsed
	}
	run.fed += fed
	healthy := run.state == runOpen && !run.expired
	for _, name := range t.requiredLocked() {
		r := t.rends[name]
		switch {
		case r == nil:
			healthy = false
		case r.pubTick:
			r.fedNoPub, r.sincePub = 0, 0
		default:
			r.fedNoPub += fed
			r.sincePub += elapsed
			if r.sincePub >= timelineHealthyGap {
				healthy = false
			}
		}
	}
	for _, r := range t.rends {
		r.pubTick = false
	}
	if healthy {
		t.healthy += elapsed
		if t.healthy >= timelineHealthyAfter && t.stalls > 0 {
			t.logf("stream: timeline %s: every rendition published for %v; stall budget reset", filepath.Base(t.dir), t.healthy.Round(time.Second))
			t.stalls = 0
		}
	} else {
		t.healthy = 0
	}
	if run.expired {
		return nil, t.stalls, false
	}
	switch {
	case len(run.overflow) > 0:
		missing = run.overflow
	case run.state == runHeld && run.fed >= timelineGateTimeout:
		missing = t.blockingLocked(run)
	case run.state == runOpen:
		for _, name := range t.requiredLocked() {
			if r := t.rends[name]; r != nil && r.fedNoPub >= timelineGateTimeout {
				missing = append(missing, name)
			}
		}
	}
	if len(missing) == 0 {
		return nil, t.stalls, false
	}
	run.expired = true
	t.stalls++
	t.logf("stream: timeline %s: run %d expired: no %s after %v fed (%d failed runs in a row)",
		filepath.Base(t.dir), run.id, strings.Join(missing, ","), run.fed.Round(time.Second), t.stalls)
	return missing, t.stalls, true
}

// requiredLocked: the reference and every rendition ever published, in
// rendition order.
func (t *Timeline) requiredLocked() []string {
	var out []string
	for _, n := range t.renditions {
		if n == t.ref || t.required[n] {
			out = append(out, n)
		}
	}
	return out
}

// blockingLocked lists the required renditions that keep run from opening:
// no segment in it, a first PTS not read yet, or (siblings) a first
// segment whose whole span the reference's staged segments don't cover,
// which could not be published together with them.
func (t *Timeline) blockingLocked(run *tlRun) []string {
	base, hasBase := t.stagedBaseLocked()
	refEnd := 0.0
	if ref := t.rends[t.ref]; hasBase && ref != nil {
		refEnd = stagedEnd(ref.pending, base)
	}
	var out []string
	for _, n := range t.requiredLocked() {
		r := t.rends[n]
		if r == nil || len(r.pending) == 0 || !r.pending[0].first || !r.pending[0].hasPTS || !hasBase {
			out = append(out, n)
			continue
		}
		if n != t.ref && unwrapMedia(r.pending[0].raw-base, 0)+r.pending[0].dur > refEnd+1e-6 {
			out = append(out, n)
		}
	}
	return out
}

// stagedBaseLocked is the earliest first PTS read among the renditions'
// first segments in the current run.
func (t *Timeline) stagedBaseLocked() (int64, bool) {
	var base int64
	has := false
	for _, name := range t.renditions {
		r := t.rends[name]
		if r == nil || len(r.pending) == 0 {
			continue
		}
		if it := r.pending[0]; it.first && it.hasPTS && (!has || ptsBefore(it.raw, base)) {
			base, has = it.raw, true
		}
	}
	return base, has
}

// stagedEnd is the media end the staged items would reach from base,
// stopping at the first one that can't be placed yet (assignMedia's rule).
func stagedEnd(items []*tlItem, base int64) float64 {
	end := 0.0
	for _, it := range items {
		if !it.placeable() {
			break
		}
		m := end
		switch {
		case it.first:
			m = unwrapMedia(it.raw-base, 0)
		case it.hasPTS:
			m = unwrapMedia(it.raw-base, end)
		}
		end = m + it.dur
	}
	return end
}

func (t *Timeline) rendLocked(name string) *tlRend {
	r := t.rends[name]
	if r == nil {
		r = &tlRend{segs: map[int64]tlSeg{}, skip: map[int64]bool{}, lastSeq: -1, asgRun: -1}
		t.rends[name] = r
	}
	return r
}

// segRead is one segment-file read done outside t.mu. retry reads only the
// PTS; a new segment also stats the file.
type segRead struct {
	retry bool
	path  string
	dur   float64
}

// segFile is the result of one segRead.
type segFile struct {
	obs    time.Time
	file   bool
	raw    int64
	hasPTS bool
}

func (t *Timeline) observe() {
	lists := map[string]MediaPlaylist{}
	for _, name := range t.renditions {
		raw, err := os.ReadFile(filepath.Join(t.dir, name+".m3u8"))
		if err != nil {
			continue // not written yet
		}
		lists[name] = ParseMediaPlaylist(raw)
	}
	capMseq := int64(-1)
	capOK := false
	if t.caption != "" {
		if raw, err := os.ReadFile(filepath.Join(t.dir, t.caption+".m3u8")); err == nil {
			capMseq = ParseMediaPlaylist(raw).MediaSequence
			capOK = true
		}
	}
	// Which files to read is decided under t.mu. The reads themselves are
	// not: ExpireStalledRun and ProcessStarted take t.mu while the manager
	// holds its own lock, so a slow disk here would stall every session.
	// obsMu still keeps Observe and MarkRestart from interleaving.
	t.mu.Lock()
	now := t.now()
	jobs := t.planReadsLocked(lists)
	t.mu.Unlock()

	got := make([]segFile, len(jobs))
	for i, j := range jobs {
		if j.retry {
			got[i].raw, got[i].hasPTS = t.readSegmentPTS(j.path)
			continue
		}
		got[i].obs, got[i].file, got[i].raw, got[i].hasPTS = t.readSegmentStart(j.path, j.dur, now)
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	t.commitObservationLocked(lists, capOK, capMseq, got)
}

// planReadsLocked lists the segment files this pass will stat or read.
// It does not modify the timeline. Only observe changes the fields it
// reads, and observe holds obsMu, so the list is still the one
// commitObservationLocked consumes.
func (t *Timeline) planReadsLocked(lists map[string]MediaPlaylist) []segRead {
	var jobs []segRead
	for _, name := range t.renditions {
		r := t.rends[name]
		if r != nil {
			for _, it := range r.pending {
				if !it.hasPTS && (it.first || it.reads < timelinePTSReads) {
					jobs = append(jobs, segRead{retry: true, path: it.path})
				}
			}
		}
		p, ok := lists[name]
		if !ok {
			continue
		}
		if r != nil && p.MediaSequence < r.ffMseq {
			continue // a stale read: never go back
		}
		last := int64(-1)
		if r != nil {
			last = r.lastSeq
		}
		for i, sg := range p.Segments {
			seq := p.MediaSequence + int64(i)
			if seq <= last {
				continue
			}
			last = seq
			jobs = append(jobs, segRead{path: filepath.Join(t.dir, sg.URI), dur: sg.Duration})
		}
	}
	return jobs
}

// commitObservationLocked records got, in plan order, and publishes.
func (t *Timeline) commitObservationLocked(lists map[string]MediaPlaylist, capOK bool, capMseq int64, got []segFile) {
	run := t.cur
	if capOK && capMseq > t.capMseq {
		t.capMseq = capMseq
	}
	n := 0
	take := func() segFile {
		if n >= len(got) {
			return segFile{}
		}
		f := got[n]
		n++
		return f
	}
	for _, name := range t.renditions {
		r := t.rends[name]
		if r != nil {
			// A failed PTS read is retried: the file may not have been
			// complete or readable yet.
			for _, it := range r.pending {
				if !it.hasPTS && (it.first || it.reads < timelinePTSReads) {
					f := take()
					it.raw, it.hasPTS = f.raw, f.hasPTS
					it.reads++
				}
			}
		}
		p, ok := lists[name]
		if !ok {
			continue
		}
		r = t.rendLocked(name)
		if p.MediaSequence < r.ffMseq {
			continue // a stale read: never go back
		}
		for i, sg := range p.Segments {
			seq := p.MediaSequence + int64(i)
			if seq <= r.lastSeq {
				continue
			}
			if r.lastSeq >= 0 && seq > r.lastSeq+1 {
				t.logf("stream: timeline %s: %s: %d segments slid out unobserved", filepath.Base(t.dir), name, seq-r.lastSeq-1)
			}
			r.lastSeq = seq
			path := filepath.Join(t.dir, sg.URI)
			first := !run.seen[name] || r.nextFirst
			r.nextFirst = false
			f := take()
			it := &tlItem{seq: seq, dur: sg.Duration, path: path, first: first, reads: 1}
			it.obs, it.file, it.raw, it.hasPTS = f.obs, f.file, f.raw, f.hasPTS
			run.seen[name] = true
			r.pending = append(r.pending, it)
		}
		r.ffMseq = p.MediaSequence
		t.trimPendingLocked(name, r)
		t.pruneLocked(name, r)
	}
	if run.state == runHeld && !run.expired && len(t.blockingLocked(run)) == 0 {
		t.openLocked(run) // and publish below, in the same pass
	}
	if run.state == runOpen {
		for _, name := range t.renditions {
			if r := t.rends[name]; r != nil {
				assignPending(run, r)
			}
		}
		t.publishLocked(run)
	}
}

// trimPendingLocked bounds r's waiting segments: those that slid out of
// FFmpeg's window unpublished, and the oldest beyond timelineMaxPending,
// are never served.
func (t *Timeline) trimPendingLocked(name string, r *tlRend) {
	n := 0
	carried := false
	for n < len(r.pending) && (r.pending[n].seq < r.ffMseq || len(r.pending)-n > timelineMaxPending) {
		if r.pending[n].first {
			carried = true
		}
		r.skip[r.pending[n].seq] = true
		n++
	}
	if n == 0 {
		return
	}
	if carried {
		if n < len(r.pending) {
			r.pending[n].first = true // the run's first segment for this rendition is now the next one
		} else {
			r.nextFirst = true // queue emptied; the next arrival carries first
		}
	}
	r.pending = r.pending[n:]
	t.logf("stream: timeline %s: %s: %d segments waited too long and are never served", filepath.Base(t.dir), name, n)
}

// openLocked fixes run's base PTS, wall-clock start and epoch from the
// first segment of every rendition, then assigns media offsets. The
// caller publishes in the same locked pass, so every required rendition
// publishes its staged segment as the run opens.
func (t *Timeline) openLocked(run *tlRun) {
	run.base, run.hasBase = t.stagedBaseLocked()
	first := true
	for _, name := range t.renditions {
		r := t.rends[name]
		if r == nil {
			continue
		}
		assignPending(run, r)
		for _, it := range r.pending {
			if it.first && it.hasMedia {
				if s := it.obs.Add(-seconds(it.media)); first || s.Before(run.start) {
					run.start, first = s, false
				}
			}
		}
	}
	run.floor = t.maxEnd
	if run.start.Before(run.floor) {
		run.start = run.floor // a run never starts before what came before it ended
	}
	if ref := t.rends[t.ref]; ref != nil && len(ref.pending) > 0 {
		it := ref.pending[0]
		if lag := it.obs.Sub(run.start.Add(seconds(it.media))); lag > 0 {
			run.refLag = lag
		}
	}
	run.epoch = t.epoch + 1
	t.epoch = run.epoch
	run.state = runOpen // the stall budget is not reset: only a healthy stretch does that
	for _, r := range t.rends {
		r.fedNoPub, r.sincePub = 0, 0
	}
	if run.epoch > 0 {
		t.logf("stream: timeline %s: run %d opened as discontinuity %d", filepath.Base(t.dir), run.id, run.epoch)
	}
}

// assignPending places r's waiting segments in order, up to the first one
// whose PTS isn't read yet.
func assignPending(run *tlRun, r *tlRend) {
	for _, it := range r.pending {
		if it.hasMedia {
			continue
		}
		if !it.placeable() {
			return
		}
		assignMedia(run, r, it)
	}
}

// assignMedia places it on run's media timeline: its first PTS relative to
// the run's base (unwrapped next to where the rendition's chain expects
// it), or the chain itself when a later segment's PTS couldn't be read.
func assignMedia(run *tlRun, r *tlRend, it *tlItem) {
	// A first segment has no chain of its own. Hint from the reference's
	// media clock (0 until it has published): otherwise a sibling joining
	// past half a 33-bit period unwraps a full cycle early.
	expected := r.runMediaEnd
	if it.first {
		expected = run.refMediaEnd
		r.runMediaEnd = 0
	}
	m := r.runMediaEnd
	if it.hasPTS && run.hasBase {
		m = unwrapMedia(it.raw-run.base, expected)
	}
	it.media, it.hasMedia = m, true
	r.runMediaEnd = m + it.dur
	r.asgRun = run.id
}

// unwrapMedia turns a 90 kHz PTS difference into seconds, choosing the
// 33-bit period nearest to expected.
func unwrapMedia(d int64, expected float64) float64 {
	v := float64(d&(int64(1)<<33-1)) / 90000
	return v + math.Round((expected-v)/ptsPeriod)*ptsPeriod
}

// ptsBefore reports whether a is earlier than b on the 33-bit clock.
func ptsBefore(a, b int64) bool {
	return (a-b)&(int64(1)<<33-1) >= int64(1)<<32
}

// publishLocked stamps the reference's placed segments in order (finding
// gaps), stopping at the first one whose PTS isn't read yet, then every
// sibling segment whose whole span the reference covers, with the shift
// of the reference interval it overlaps most.
func (t *Timeline) publishLocked(run *tlRun) {
	if run.expired || len(run.overflow) > 0 {
		return // expired (or being expired for the anchor cap): never publishes again
	}
	if ref := t.rends[t.ref]; ref != nil {
		for len(ref.pending) > 0 && ref.pending[0].hasMedia {
			it := ref.pending[0]
			ref.pending = ref.pending[1:]
			shift := run.shiftAt(it.media)
			start := run.start.Add(seconds(it.media) + shift)
			if !it.first && it.file {
				if gap := it.obs.Sub(start) - run.refLag; gap > timelineGapThreshold {
					run.anchors = append(run.anchors, tlAnchor{media: it.media, shift: shift + gap})
					t.logf("stream: timeline %s: %v gap before %s segment %d",
						filepath.Base(t.dir), gap.Round(100*time.Millisecond), t.ref, it.seq)
					start = start.Add(gap)
				}
			}
			t.stampLocked(run, t.ref, ref, it, start)
			if e := it.media + it.dur; e > run.refMediaEnd {
				run.refMediaEnd = e
			}
		}
	}
	for _, name := range t.renditions[1:] {
		r := t.rends[name]
		if r == nil {
			continue
		}
		for len(r.pending) > 0 && r.pending[0].hasMedia && r.pending[0].media+r.pending[0].dur <= run.refMediaEnd+1e-6 {
			it := r.pending[0]
			r.pending = r.pending[1:]
			if !t.required[name] && it.media < run.histFloor-1e-9 {
				r.skip[it.seq] = true // its gap history was dropped: never stamp from it
				t.logf("stream: timeline %s: %s segment %d predates the kept gap history; never served", filepath.Base(t.dir), name, it.seq)
				continue
			}
			t.stampLocked(run, name, r, it, run.start.Add(seconds(it.media)+run.shiftFor(it.media, it.media+it.dur)))
		}
	}
	t.compactAnchorsLocked(run)
}

// compactAnchorsLocked keeps the last anchor at or before the lowest
// arrival watermark and every anchor after it. A rendition's watermark is
// where its next segment in this run can start: 0 while it has none placed
// in the run (its playlist may simply be late), so its later segments
// still find every gap. Past timelineMaxAnchors, history only a
// non-required rendition needs is dropped (its older segments are then
// never served); history a required rendition needs never is: the run
// stops publishing and ExpireStalledRun expires it.
func (t *Timeline) compactAnchorsLocked(run *tlRun) {
	wmAll, wmReq := run.refMediaEnd, run.refMediaEnd
	for _, name := range t.renditions[1:] {
		w := t.watermarkLocked(run, name)
		wmAll = math.Min(wmAll, w)
		if t.required[name] {
			wmReq = math.Min(wmReq, w)
		}
	}
	lastAtOrBefore := func(wm float64) int {
		k := 0
		for i, a := range run.anchors {
			if a.media <= wm+1e-9 {
				k = i
			}
		}
		return k
	}
	keep := lastAtOrBefore(wmAll)
	if len(run.anchors)-keep > timelineMaxAnchors {
		if len(run.anchors)-lastAtOrBefore(wmReq) > timelineMaxAnchors {
			cut := run.anchors[len(run.anchors)-timelineMaxAnchors].media
			for _, name := range t.renditions[1:] {
				if t.required[name] && t.watermarkLocked(run, name) < cut {
					run.overflow = append(run.overflow, name)
				}
			}
			t.logf("stream: timeline %s: run %d: %s still need(s) more than %d gaps of history; stopping the run",
				filepath.Base(t.dir), run.id, strings.Join(run.overflow, ","), timelineMaxAnchors)
			return
		}
		keep = len(run.anchors) - timelineMaxAnchors
		run.histFloor = run.anchors[keep].media
		t.logf("stream: timeline %s: run %d: more than %d gaps of history; segments of renditions not published yet from before media %.1fs are never served",
			filepath.Base(t.dir), run.id, timelineMaxAnchors, run.histFloor)
	}
	if keep > 0 {
		run.headShift = run.anchors[keep-1].shift
	}
	run.anchors = run.anchors[keep:]
}

// watermarkLocked is the media point from which name's segments in run
// are still to come.
func (t *Timeline) watermarkLocked(run *tlRun, name string) float64 {
	r := t.rends[name]
	if r == nil || r.asgRun != run.id {
		return 0
	}
	if len(r.pending) > 0 && r.pending[0].hasMedia {
		return r.pending[0].media
	}
	return r.runMediaEnd
}

func (t *Timeline) stampLocked(run *tlRun, name string, r *tlRend, it *tlItem, start time.Time) {
	if start.Before(run.floor) {
		start = run.floor
	}
	if start.Before(r.prevEnd) {
		start = r.prevEnd // never overlap the rendition's previous segment
	}
	r.segs[it.seq] = tlSeg{bseq: r.nextB, start: start, dur: it.dur, epoch: run.epoch, tag: it.first && r.everPub}
	r.nextB++
	r.everPub = true
	r.pubTick = true
	run.pub[name] = true
	t.required[name] = true // a published rendition is required from now on, crashes or not
	end := start.Add(seconds(it.dur))
	r.prevEnd = end
	if end.After(t.maxEnd) {
		t.maxEnd = end
	}
}

// readSegmentStart stats path and reads its first PTS. A test hook, when
// set, does both.
func (t *Timeline) readSegmentStart(path string, dur float64, now time.Time) (time.Time, bool, int64, bool) {
	if t.segmentRead != nil {
		return t.segmentRead(path, dur, now)
	}
	obs, file := t.observedStart(path, dur, now)
	raw, has := segmentFirstPTS(path)
	return obs, file, raw, has
}

// readSegmentPTS reads a segment's first PTS, through the same test hook.
func (t *Timeline) readSegmentPTS(path string) (int64, bool) {
	if t.segmentRead != nil {
		_, _, raw, has := t.segmentRead(path, 0, time.Time{})
		return raw, has
	}
	return segmentFirstPTS(path)
}

// observedStart is when the segment's file says it started: its mtime (the
// end) minus EXTINF. Without a usable file it is now minus EXTINF.
func (t *Timeline) observedStart(path string, dur float64, now time.Time) (time.Time, bool) {
	d := seconds(dur)
	if fi, err := os.Stat(path); err == nil && !fi.ModTime().After(now) {
		return fi.ModTime().Add(-d), true
	}
	return now.Add(-d), false
}

// segmentFirstPTS is the PTS of the first PES in a TS segment file that
// carries one (90 kHz). It reads at most the first 64 KiB.
func segmentFirstPTS(path string) (int64, bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	defer func() { _ = f.Close() }()
	buf := make([]byte, 64*1024)
	n, _ := io.ReadFull(f, buf)
	buf = buf[:n]
	for off := 0; off+188 <= len(buf); off += 188 {
		p := buf[off : off+188]
		if p[0] != 0x47 || p[1]&0x40 == 0 || p[3]&0x10 == 0 {
			continue // not a payload unit start
		}
		i := 4
		if p[3]&0x20 != 0 {
			i += 1 + int(p[4])
		}
		if i+14 > len(p) {
			continue
		}
		pes := p[i:]
		if pes[0] != 0 || pes[1] != 0 || pes[2] != 1 || pes[7]&0x80 == 0 {
			continue // a table section, or a PES without PTS
		}
		b := pes[9:14]
		return int64(b[0]>>1&0x07)<<30 | int64(b[1])<<22 | int64(b[2]>>1)<<15 | int64(b[3])<<7 | int64(b[4]>>1), true
	}
	return 0, false
}

// pruneLocked keeps r's stamps and skips for its own window, or, for the
// reference, as far back as the caption playlist still lists (at most
// timelineCaptionKeep segments further). A discarded sequence in that
// range has to stay skipped, or a lagging caption playlist stops there.
func (t *Timeline) pruneLocked(name string, r *tlRend) {
	keep := r.ffMseq
	if name == t.ref && t.capMseq >= 0 && t.capMseq < keep {
		keep = max(t.capMseq, r.ffMseq-timelineCaptionKeep)
	}
	for seq := range r.segs {
		if seq < keep {
			delete(r.segs, seq)
		}
	}
	for seq := range r.skip {
		if seq < keep {
			delete(r.skip, seq)
		}
	}
}

// Retime rewrites one served playlist (FFmpeg's bytes, URIs still bare)
// from the observer's snapshot: Bowtie's media sequence numbers,
// #EXT-X-PROGRAM-DATE-TIME on every segment, #EXT-X-DISCONTINUITY on each
// rendition's first segment of a new epoch and #EXT-X-DISCONTINUITY-SEQUENCE.
// playlist is the served name without .m3u8: a rendition, or the caption
// playlist, which is retimed with the reference's stamps by FFmpeg
// sequence number (repairCaptionPlaylist keeps the numbers equal; its URIs
// pass through). Segments of discarded runs are left out; segments that
// slid out of the window are dropped from the head; segments not published
// yet end the playlist. Each playlist's MEDIA-SEQUENCE never goes below
// what it was last served with: head segments under that floor are left
// out (a stale read).
func (t *Timeline) Retime(playlist string, raw []byte) []byte {
	p := ParseMediaPlaylist(raw)
	t.mu.Lock()
	defer t.mu.Unlock()
	name, caption := playlist, playlist == t.caption && t.caption != ""
	if caption {
		name = t.ref
	}
	r := t.rends[name]
	type out struct {
		PlaylistSegment
		st tlSeg
	}
	var segs []out
	complete := r != nil
	for i, sg := range p.Segments {
		if r == nil {
			break
		}
		seq := p.MediaSequence + int64(i)
		if r.skip[seq] {
			continue // a discarded run, or past a run's reference
		}
		if !caption && seq < r.ffMseq {
			continue // slid out (a stamp may be kept for the captions only)
		}
		st, ok := r.segs[seq]
		if !ok {
			if len(segs) == 0 && seq < r.ffMseq {
				continue // slid out since this playlist was read
			}
			complete = false
			break // not published yet
		}
		segs = append(segs, out{sg, st})
	}
	floor, hasFloor := t.floors[playlist]
	for len(segs) > 0 && hasFloor && segs[0].st.bseq < floor {
		segs = segs[1:] // already behind what this playlist served
	}
	mseq, dseq := int64(0), t.epoch
	if r != nil {
		// Empty: the lowest number still to be served from this read's
		// first sequence on (never below the floor), so a later read that
		// lists those segments starts there rather than going back.
		mseq = r.nextB
		for seq, st := range r.segs {
			if seq >= p.MediaSequence && st.bseq < mseq && (!hasFloor || st.bseq >= floor) {
				mseq = st.bseq
			}
		}
		if hasFloor && floor > mseq {
			mseq = floor
		}
	}
	if dseq < 0 {
		dseq = 0
	}
	if len(segs) > 0 {
		mseq, dseq = segs[0].st.bseq, segs[0].st.epoch
		if segs[0].st.tag {
			dseq--
		}
	}
	if r != nil {
		t.floors[playlist] = mseq // r != nil: a known playlist, so the map stays bounded
	}
	var b bytes.Buffer
	for _, h := range p.Header {
		b.WriteString(h)
		b.WriteByte('\n')
	}
	fmt.Fprintf(&b, "#EXT-X-MEDIA-SEQUENCE:%d\n#EXT-X-DISCONTINUITY-SEQUENCE:%d\n", mseq, dseq)
	for _, s := range segs {
		if s.st.tag {
			b.WriteString("#EXT-X-DISCONTINUITY\n")
		}
		b.WriteString("#EXT-X-PROGRAM-DATE-TIME:" + FormatWallClock(s.st.start) + "\n")
		for _, tag := range s.Tags {
			b.WriteString(tag)
			b.WriteByte('\n')
		}
		b.WriteString(s.URI)
		b.WriteByte('\n')
	}
	if complete {
		for _, tr := range p.Trailer {
			b.WriteString(tr)
			b.WriteByte('\n')
		}
	}
	return b.Bytes()
}

// TimelineSegment is one window segment on the wall clock.
type TimelineSegment struct {
	Start    time.Time
	Duration float64
}

// WindowTimeline maps a rendition's current window to the wall clock.
// MediaSequence is Bowtie's number of the first segment.
type WindowTimeline struct {
	MediaSequence int64
	Segments      []TimelineSegment
}

// WindowStart is the first segment's start (zero when the window is empty).
func (w WindowTimeline) WindowStart() time.Time {
	if len(w.Segments) == 0 {
		return time.Time{}
	}
	return w.Segments[0].Start
}

// LiveEdge is the last segment's end (zero when the window is empty).
func (w WindowTimeline) LiveEdge() time.Time {
	if len(w.Segments) == 0 {
		return time.Time{}
	}
	last := w.Segments[len(w.Segments)-1]
	return last.Start.Add(seconds(last.Duration))
}

// Window is the published window of rendition on the wall clock.
func (t *Timeline) Window(rendition string) WindowTimeline {
	t.mu.Lock()
	defer t.mu.Unlock()
	r := t.rends[rendition]
	if r == nil {
		return WindowTimeline{}
	}
	w := WindowTimeline{MediaSequence: r.nextB}
	var seqs []int64
	for seq := range r.segs {
		if seq >= r.ffMseq { // stamps kept for the captions aren't in the window
			seqs = append(seqs, seq)
		}
	}
	sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })
	for i, seq := range seqs {
		st := r.segs[seq]
		if i == 0 {
			w.MediaSequence = st.bseq
		}
		w.Segments = append(w.Segments, TimelineSegment{Start: st.start, Duration: st.dur})
	}
	return w
}

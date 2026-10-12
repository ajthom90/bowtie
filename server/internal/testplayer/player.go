// Package testplayer is a virtual HLS client for Bowtie: it creates a session,
// polls the playlist and downloads new segments like a player would, and
// reports what a viewer would have experienced (stalls, sequence resets,
// discontinuities, a vanished session).
package testplayer

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Config configures a Player.
type Config struct {
	BaseURL   string // e.g. http://127.0.0.1:1234
	Token     string // bearer JWT
	ChannelID int64
	// Caps is the POST /sessions caps object; default h264/aac at ≤480p.
	Caps map[string]any
	// PollEvery is the playlist poll interval; default 1s.
	PollEvery time.Duration
	// HeartbeatEvery is the session heartbeat interval; default 15s.
	HeartbeatEvery time.Duration
	Client         *http.Client
	// AllRenditions follows every rendition the master lists (all variants,
	// audio and subtitle renditions), not only the first variant.
	AllRenditions bool
	// Probe, when set, runs on every new audio and video segment.
	Probe SegmentProbe
	// OnSegment, when set, is called (on the Run goroutine) after each
	// probed segment with time-local measurements.
	OnSegment func(SegmentEvent)
}

// Packet is one demuxed packet of a probed segment, in seconds.
type Packet struct{ PTS, Dur float64 }

// SegmentProbe lists the packets of the first stream in a TS segment.
type SegmentProbe func(ctx context.Context, seg []byte) ([]Packet, error)

// SegmentEvent is one probed segment, for time-local analysis (the soak).
type SegmentEvent struct {
	At             time.Time
	Rendition      string
	Seq            int64
	DNum           int64
	AudioHole      time.Duration // largest hole in/before this audio segment
	AVSkew         time.Duration // skew against the PDT-paired segment of the other kind
	Paired         bool          // a segment of the other kind was paired with this one
	TimelineExcess float64       // the primary's running excess at this moment
}

// CoverageWindow is what each rendition delivered between two Marks. Window
// 0 starts at Start; each Mark (a fault's end) opens the next.
type CoverageWindow struct {
	Start, End time.Time
	// Segments counts segments fetched OK per rendition.
	Segments map[string]int
	// Epochs counts them per rendition and discontinuity number.
	Epochs map[string]map[int64]int
	// Paired counts, per audio/video rendition and discontinuity number,
	// this window's probed segments that were PDT-paired with a probed
	// segment of the other kind (so their A/V skew was measured).
	Paired map[string]map[int64]int
	// Cues counts WebVTT cues per subtitle rendition; CuesTimed those
	// timed against their video segment (by the window the cue's segment
	// was fetched in).
	Cues, CuesTimed map[string]int
}

// StartError is returned by Start when session creation is refused.
type StartError struct {
	Status int
	Body   string
}

func (e *StartError) Error() string {
	return fmt.Sprintf("start session: HTTP %d: %s", e.Status, e.Body)
}

// Report is the viewer experience of one run.
type Report struct {
	Polls           int
	SegmentsFetched int
	SegmentErrors   int
	BackwardJumps   int     // newest segment number went down between polls
	Discontinuities int     // #EXT-X-DISCONTINUITY on newly seen segments
	Newest          []int64 // newest absolute segment number at each poll
	// MaxGap is the longest stretch without a new segment, including the
	// stretch from the last new segment to the end of the run.
	MaxGap      time.Duration
	SessionGone bool
	// EndList: a polled playlist contained #EXT-X-ENDLIST, which tells a real
	// player the stream is over (it stops reloading).
	EndList bool
	Errors  []string

	// Renditions are the media playlists followed, primary first.
	Renditions []string
	// TimelineExcess is Σ EXTINF of the primary rendition's segments that
	// appeared after the first poll, minus the wall time they took to
	// appear (seconds). ≈0 on a clean stream; negative by the length of an
	// outage the transcoder skipped.
	TimelineExcess float64
	// MaxAudioHole is the largest gap between consecutive audio packets;
	// AudioHoleBy is the same per audio rendition (aac0, ac30, …).
	MaxAudioHole time.Duration
	AudioHoleBy  map[string]time.Duration
	// MaxAVStartSkew is the largest |(ptsA − ptsV) − (pdtA − pdtV)| over
	// audio and video segments paired by PDT overlap (PairByPDT) with the
	// same discontinuity number. AVPairs counts those comparisons.
	MaxAVStartSkew           time.Duration
	AVPairs                  int
	ProbedAudio, ProbedVideo int
	// Consistency checks every polled media playlist (CheckConsistency).
	Consistency Consistency
	// FetchErrors counts every failed fetch: PlaylistErrors (any rendition,
	// except a secondary's 404 before it was first served), SegmentErrors
	// (any rendition) and ProbeErrors.
	FetchErrors                 int
	ProbeErrors, PlaylistErrors int
	// RenditionSegments counts segments fetched OK per rendition;
	// SegmentsSinceMark is the last window's Segments.
	RenditionSegments, SegmentsSinceMark map[string]int
	// Windows are the coverage windows, oldest first (see Mark).
	Windows []CoverageWindow
	// TailGap is, per rendition, the time from its last new segment to the
	// end of the run (from Start when it delivered none).
	TailGap map[string]time.Duration
	// Cues is the number of WebVTT cues fetched; CuesTimed the cues whose
	// time was checked against the PDT-paired video segment's PTS range
	// (±1 s); CueErrors those outside it or with a malformed X-TIMESTAMP-MAP.
	// A segment with no map is timed as MPEGTS:0, LOCAL:00:00:00.000
	// (RFC 8216 §3.5).
	Cues, CuesTimed, CueErrors int
}

// Player is one virtual viewer.
type Player struct {
	cfg         Config
	viewerID    string
	playlistURL string
	streamToken string

	mu       sync.Mutex
	report   Report
	highest  int64
	lastNew  time.Time
	stopped  time.Time
	prevNews int64

	started time.Time
	primary *rendition
	others  []*rendition
	// cons checks every polled media playlist as it arrives; it keeps each
	// rendition's last snapshot, not every poll.
	cons    consistencyChecker
	windows []*CoverageWindow
	// pendingCues wait for the video segment they are timed against.
	pendingCues []pendingCue

	excessStarted bool
	excessBase    int64     // newest segment at the first poll that listed any
	excessFirst   time.Time // poll that first saw a segment after the base
	excessAnchor  int64     // newest segment of that poll; nothing in the poll is counted
	excessLast    time.Time // last poll that found a segment after the reference
	excessSum     float64   // Σ EXTINF of primary segments after the reference
}

// Start creates a session for cfg.ChannelID.
func Start(ctx context.Context, cfg Config) (*Player, error) {
	if cfg.Caps == nil {
		cfg.Caps = map[string]any{"videoCodecs": []string{"h264"}, "audioCodecs": []string{"aac"}, "maxHeight": 480}
	}
	if cfg.PollEvery <= 0 {
		cfg.PollEvery = time.Second
	}
	if cfg.HeartbeatEvery <= 0 {
		cfg.HeartbeatEvery = 15 * time.Second
	}
	if cfg.Client == nil {
		cfg.Client = &http.Client{Timeout: 30 * time.Second}
	}
	body, _ := json.Marshal(map[string]any{"channelId": cfg.ChannelID, "caps": cfg.Caps})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.BaseURL+"/api/v1/sessions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.Token)
	resp, err := cfg.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, &StartError{Status: resp.StatusCode, Body: string(raw)}
	}
	var out struct {
		ViewerID    string `json:"viewerId"`
		PlaylistURL string `json:"playlistUrl"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode session: %w", err)
	}
	u, err := url.Parse(out.PlaylistURL)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	return &Player{
		cfg:         cfg,
		viewerID:    out.ViewerID,
		playlistURL: out.PlaylistURL,
		streamToken: u.Query().Get("token"),
		highest:     -1,
		prevNews:    -1,
		lastNew:     now,
		started:     now,
		primary:     newRendition(renditionName(out.PlaylistURL), out.PlaylistURL, now),
		windows:     []*CoverageWindow{newWindow(now)},
	}, nil
}

// ViewerID returns the session's viewer id.
func (p *Player) ViewerID() string { return p.viewerID }

// Run polls until ctx is done or the session disappears.
func (p *Player) Run(ctx context.Context) {
	defer func() {
		p.drain()
		p.mu.Lock()
		p.stopped = time.Now()
		p.mu.Unlock()
	}()
	poll := time.NewTicker(p.cfg.PollEvery)
	defer poll.Stop()
	hb := time.NewTicker(p.cfg.HeartbeatEvery)
	defer hb.Stop()
	if p.poll(ctx) {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-hb.C:
			p.heartbeat(ctx)
		case <-poll.C:
			if p.poll(ctx) {
				return
			}
		}
	}
}

// Stop deletes the session (best effort).
func (p *Player) Stop(ctx context.Context) {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, p.cfg.BaseURL+"/api/v1/sessions/"+p.viewerID, nil)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+p.cfg.Token)
	if resp, err := p.cfg.Client.Do(req); err == nil {
		_ = resp.Body.Close()
	}
}

// Report returns the run so far.
func (p *Player) Report() Report {
	p.mu.Lock()
	defer p.mu.Unlock()
	r := p.report
	r.Newest = append([]int64(nil), r.Newest...)
	r.Errors = append([]string(nil), r.Errors...)
	end := p.stopped
	if end.IsZero() {
		end = time.Now()
	}
	if tail := end.Sub(p.lastNew); tail > r.MaxGap {
		r.MaxGap = tail
	}
	all := append([]*rendition{p.primary}, p.others...)
	r.RenditionSegments = map[string]int{}
	r.TailGap = map[string]time.Duration{}
	for _, o := range all {
		r.Renditions = append(r.Renditions, o.name)
		r.RenditionSegments[o.name] = o.fetched
		r.TailGap[o.name] = end.Sub(o.lastOK)
	}
	for i, w := range p.windows {
		c := CoverageWindow{Start: w.Start, End: end, Segments: map[string]int{},
			Epochs: map[string]map[int64]int{}, Paired: map[string]map[int64]int{}, Cues: map[string]int{}, CuesTimed: map[string]int{}}
		if i+1 < len(p.windows) {
			c.End = p.windows[i+1].Start
		}
		for _, o := range all {
			c.Segments[o.name] = w.Segments[o.name]
		}
		for k, m := range w.Epochs {
			c.Epochs[k] = copyCounts(m)
		}
		for k, m := range w.Paired {
			c.Paired[k] = copyCounts(m)
		}
		for k, n := range w.Cues {
			c.Cues[k] = n
		}
		for k, n := range w.CuesTimed {
			c.CuesTimed[k] = n
		}
		r.Windows = append(r.Windows, c)
	}
	r.SegmentsSinceMark = r.Windows[len(r.Windows)-1].Segments
	if r.AudioHoleBy != nil {
		m := map[string]time.Duration{}
		for k, v := range r.AudioHoleBy {
			m[k] = v
		}
		r.AudioHoleBy = m
	}
	if !p.excessLast.IsZero() {
		r.TimelineExcess = p.excessSum - p.excessLast.Sub(p.excessFirst).Seconds()
	}
	r.FetchErrors = r.PlaylistErrors + r.SegmentErrors + r.ProbeErrors
	r.Consistency = p.cons.result()
	return r
}

// Mark closes the current coverage window and opens the next. Scenarios
// call it at the end of each fault, so each fault's aftermath is checked
// on its own.
func (p *Player) Mark() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.windows = append(p.windows, newWindow(time.Now()))
}

func newWindow(at time.Time) *CoverageWindow {
	return &CoverageWindow{Start: at, Segments: map[string]int{}, Epochs: map[string]map[int64]int{},
		Paired: map[string]map[int64]int{}, Cues: map[string]int{}, CuesTimed: map[string]int{}}
}

func copyCounts(m map[int64]int) map[int64]int {
	out := make(map[int64]int, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func bump(m map[string]map[int64]int, name string, dnum int64) {
	if m[name] == nil {
		m[name] = map[int64]int{}
	}
	m[name][dnum]++
}

// fetchedLocked records a segment fetched OK in rendition o.
func (p *Player) fetchedLocked(o *rendition, g SegmentObs, at time.Time) int {
	o.fetched++
	o.lastOK = at
	w := p.windows[len(p.windows)-1]
	w.Segments[o.name]++
	bump(w.Epochs, o.name, g.DNum)
	return len(p.windows) - 1
}

func (p *Player) errorf(format string, a ...any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.report.Errors = append(p.report.Errors, fmt.Sprintf(format, a...))
}

func (p *Player) heartbeat(ctx context.Context) {
	u := p.cfg.BaseURL + "/api/v1/sessions/" + p.viewerID + "/heartbeat?token=" + url.QueryEscape(p.streamToken)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, nil)
	if err != nil {
		return
	}
	resp, err := p.cfg.Client.Do(req)
	if err != nil {
		if ctx.Err() == nil {
			p.errorf("heartbeat: %v", err)
		}
		return
	}
	_ = resp.Body.Close()
	if resp.StatusCode >= 300 {
		p.errorf("heartbeat: HTTP %d", resp.StatusCode)
	}
}

// poll fetches the primary playlist, its new segments and (AllRenditions)
// every other rendition; it returns true when the session is gone.
func (p *Player) poll(ctx context.Context) bool {
	pollAt := time.Now()
	body, status, err := p.get(ctx, p.playlistURL)
	p.mu.Lock()
	p.report.Polls++
	p.mu.Unlock()
	if err != nil {
		if ctx.Err() == nil {
			p.playlistErrorf("playlist: %v", err)
		}
		return false
	}
	if status == http.StatusNotFound || status == http.StatusGone {
		p.mu.Lock()
		p.report.SessionGone = true
		p.mu.Unlock()
		return true
	}
	if status != http.StatusOK {
		p.playlistErrorf("playlist: HTTP %d", status)
		return false
	}
	if bytes.Contains(body, []byte("#EXT-X-STREAM-INF")) {
		// Master playlist: follow the first variant, as a real player does,
		// and (AllRenditions) every other rendition too.
		refs := renditionRefs(body)
		if len(refs) > 0 {
			p.mu.Lock()
			base := p.playlistURL
			p.playlistURL = resolveRef(base, refs[0])
			p.primary.name, p.primary.url = renditionName(refs[0]), p.playlistURL
			if p.cfg.AllRenditions && p.others == nil {
				seen := map[string]bool{p.primary.name: true}
				for _, ref := range refs[1:] {
					if n := renditionName(ref); !seen[n] {
						seen[n] = true
						p.others = append(p.others, newRendition(n, resolveRef(base, ref), p.started))
					}
				}
			}
			p.mu.Unlock()
		}
		return false
	}
	if bytes.Contains(body, []byte("#EXT-X-ENDLIST")) {
		p.mu.Lock()
		p.report.EndList = true
		p.mu.Unlock()
	}
	snap := ParseSnapshot(p.primary.name, body, pollAt)
	p.mu.Lock()
	p.cons.add(snap)
	others := append([]*rendition(nil), p.others...)
	if n := len(snap.Segments); n > 0 {
		newest := snap.Segments[n-1].Seq
		p.report.Newest = append(p.report.Newest, newest)
		if p.prevNews >= 0 && newest < p.prevNews {
			p.report.BackwardJumps++
		}
		p.prevNews = newest
		if !p.excessStarted {
			// The clock starts at the next poll that sees anything newer.
			p.excessStarted, p.excessBase = true, newest
		} else if p.excessFirst.IsZero() {
			// Anchor on the newest segment of this poll. A batch that
			// arrives together contributes nothing at zero wall time.
			anchor, found := p.excessBase, false
			for _, g := range snap.Segments {
				if g.Seq > anchor {
					anchor, found = g.Seq, true
				}
			}
			if found {
				p.excessFirst = pollAt
				p.excessAnchor = anchor
			}
		}
	}
	p.mu.Unlock()

	for _, g := range snap.Segments {
		p.mu.Lock()
		isNew := g.Seq > p.highest
		p.mu.Unlock()
		if !isNew {
			continue
		}
		seg, segStatus, err := p.get(ctx, g.URI)
		ok := err == nil && segStatus == http.StatusOK && len(seg) > 0 && seg[0] == 0x47
		now := time.Now()
		p.mu.Lock()
		win := -1
		if ok {
			p.report.SegmentsFetched++
			win = p.fetchedLocked(p.primary, g, now)
		} else {
			p.report.SegmentErrors++
		}
		if g.Discontinuity {
			p.report.Discontinuities++
		}
		if gap := now.Sub(p.lastNew); gap > p.report.MaxGap {
			p.report.MaxGap = gap
		}
		p.lastNew = now
		p.highest = g.Seq
		if !p.excessFirst.IsZero() && g.Seq > p.excessAnchor {
			p.excessSum += g.Duration
			p.excessLast = pollAt
		}
		p.mu.Unlock()
		if ok {
			p.probe(ctx, p.primary, g, win, seg)
		}
	}
	for _, r := range others {
		p.pollRendition(ctx, r)
	}
	p.mu.Lock()
	p.retryCuesLocked()
	p.mu.Unlock()
	return false
}

// playlistErrorf records a failed playlist fetch.
func (p *Player) playlistErrorf(format string, a ...any) {
	p.mu.Lock()
	p.report.PlaylistErrors++
	p.mu.Unlock()
	p.errorf(format, a...)
}

// pollRendition polls a non-primary rendition: snapshot, new segments,
// probes and cues. Only the Run goroutine touches r.highest.
func (p *Player) pollRendition(ctx context.Context, r *rendition) {
	body, status, err := p.get(ctx, r.url)
	switch {
	case err != nil:
		if ctx.Err() == nil {
			p.playlistErrorf("%s playlist: %v", r.name, err)
		}
		return
	case status == http.StatusNotFound && !r.everOK:
		return // not written yet
	case status != http.StatusOK:
		p.playlistErrorf("%s playlist: HTTP %d", r.name, status)
		return
	}
	r.everOK = true
	s := ParseSnapshot(r.name, body, time.Now())
	p.mu.Lock()
	p.cons.add(s)
	p.mu.Unlock()
	for _, g := range s.Segments {
		if g.Seq <= r.highest {
			continue
		}
		r.highest = g.Seq
		seg, st, err := p.get(ctx, g.URI)
		sub := isSubtitleRendition(r.name)
		valid := len(seg) > 0 && seg[0] == 0x47
		if sub {
			valid = bytes.HasPrefix(seg, []byte("WEBVTT"))
		}
		if err != nil || st != http.StatusOK || !valid {
			p.mu.Lock()
			p.report.SegmentErrors++
			p.mu.Unlock()
			if ctx.Err() == nil {
				p.errorf("%s segment %d: HTTP %d, %v", r.name, g.Seq, st, err)
			}
			continue
		}
		p.mu.Lock()
		win := p.fetchedLocked(r, g, time.Now())
		p.mu.Unlock()
		if sub {
			p.cues(r, g, win, seg)
		} else {
			p.probe(ctx, r, g, win, seg)
		}
	}
}

// probe measures audio holes and the A/V skew of one fetched segment of
// rendition r, fetched in coverage window win.
func (p *Player) probe(ctx context.Context, r *rendition, g SegmentObs, win int, seg []byte) {
	if p.cfg.Probe == nil {
		return
	}
	pk, err := p.cfg.Probe(ctx, seg)
	if err != nil || len(pk) == 0 {
		if ctx.Err() == nil {
			p.mu.Lock()
			p.report.ProbeErrors++
			p.mu.Unlock()
			p.errorf("probe %s segment %d: %v (%d packets)", r.name, g.Seq, err, len(pk))
		}
		return
	}
	ev := SegmentEvent{At: time.Now(), Rendition: r.name, Seq: g.Seq, DNum: g.DNum}
	defer func() {
		if p.cfg.OnSegment != nil {
			p.cfg.OnSegment(ev)
		}
	}()
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.excessLast.IsZero() {
		ev.TimelineExcess = p.excessSum - p.excessLast.Sub(p.excessFirst).Seconds()
	}
	audio := isAudioRendition(r.name)
	if audio {
		p.report.ProbedAudio++
		hole := time.Duration(0)
		if r.lastSeq == g.Seq-1 && !g.Discontinuity {
			hole = holeOf(pk[0].PTS - r.lastEnd)
		}
		for i := 1; i < len(pk); i++ {
			if h := holeOf(pk[i].PTS - (pk[i-1].PTS + pk[i-1].Dur)); h > hole {
				hole = h
			}
		}
		ev.AudioHole = hole
		if hole > p.report.MaxAudioHole {
			p.report.MaxAudioHole = hole
		}
		if p.report.AudioHoleBy == nil {
			p.report.AudioHoleBy = map[string]time.Duration{}
		}
		if hole > p.report.AudioHoleBy[r.name] {
			p.report.AudioHoleBy[r.name] = hole
		}
		last := pk[len(pk)-1]
		r.lastSeq, r.lastEnd = g.Seq, last.PTS+last.Dur
	} else {
		p.report.ProbedVideo++
	}
	last := pk[len(pk)-1]
	rec := &probed{seg: PDTSeg{Start: g.PDT, Dur: g.Duration, DNum: g.DNum}, pts: pk[0].PTS, end: last.PTS + last.Dur, win: win}
	if g.PDT.IsZero() {
		return // no wall-clock place: cannot be paired (Consistency flags it)
	}
	r.addProbed(rec)
	ev.Paired, ev.AVSkew = p.pairLocked(r, rec, false)
}

// pairLocked pairs rec (of rendition r) with the PDT-overlapping probed
// segment of every rendition of the other kind and measures the skew.
// A non-overlapping neighbour (within one segment) is accepted only once
// the other rendition has passed rec's end; before that, the overlapping
// segment may not be probed yet and pairs with rec when it is. final (the
// end of observation) retries with overlapping segments only: a rendition
// that stopped leaves its last moments unpaired, which coverage reports.
func (p *Player) pairLocked(r *rendition, rec *probed, final bool) (bool, time.Duration) {
	paired, max := false, time.Duration(0)
	audio := isAudioRendition(r.name)
	for _, o := range append([]*rendition{p.primary}, p.others...) {
		if o == r || isSubtitleRendition(o.name) || isAudioRendition(o.name) == audio || len(o.probed) == 0 {
			continue
		}
		k, ok := PairByPDT(rec.seg, o.pdtSegs(), durOf(rec.seg.Dur))
		if !ok || o.probed[k].seg.DNum != rec.seg.DNum {
			continue // unpaired, or a numbering mismatch Consistency reports
		}
		other := o.probed[k]
		if !overlaps(rec.seg, other.seg) && (final || o.probed[len(o.probed)-1].seg.End().Before(rec.seg.End())) {
			continue
		}
		s := absSeconds(ptsDiff(rec.pts, other.pts) - rec.seg.Start.Sub(other.seg.Start).Seconds())
		p.report.AVPairs++
		paired = true
		if s > max {
			max = s
		}
		if s > p.report.MaxAVStartSkew {
			p.report.MaxAVStartSkew = s
		}
		p.pairedLocked(r, rec)
		p.pairedLocked(o, other)
	}
	return paired, max
}

// drain finishes the comparisons still waiting at the end of observation:
// every unpaired probed segment is retried against everything probed, and
// cues still waiting for their video segment are timed or counted as
// unmatched (CueErrors). Run calls it on return.
func (p *Player) drain() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, r := range append([]*rendition{p.primary}, p.others...) {
		for _, rec := range r.probed {
			if !rec.paired {
				p.pairLocked(r, rec, true)
			}
		}
	}
	waiting := p.pendingCues
	p.pendingCues = nil
	for _, c := range waiting {
		if !p.timeCuesLocked(c) {
			p.report.CueErrors += len(c.starts)
			p.cueErrorf("%s segment %d: %d cue(s) with no probed %s segment to time them against", c.r.name, c.g.Seq, len(c.starts), strings.TrimSuffix(c.r.name, "_vtt"))
		}
	}
}

// pairedLocked counts rec once in the Paired coverage of the window it was
// fetched in.
func (p *Player) pairedLocked(r *rendition, rec *probed) {
	if rec.paired {
		return
	}
	rec.paired = true
	bump(p.windows[rec.win].Paired, r.name, rec.seg.DNum)
}

var (
	cueRe    = regexp.MustCompile(`^((?:\d+:)?\d{2}:\d{2}\.\d{3})\s+-->`)
	mpegtsRe = regexp.MustCompile(`MPEGTS:(\d+)`)
	localRe  = regexp.MustCompile(`LOCAL:((?:\d+:)?\d{2}:\d{2}\.\d{3})`)
)

// cueTolerance is how far a cue may start outside its video segment.
const cueTolerance = 1.0

// maxPendingCues bounds the WebVTT segments waiting for their video segment.
const maxPendingCues = 64

// pendingCue is a WebVTT segment's cues, waiting to be timed.
type pendingCue struct {
	r             *rendition
	g             SegmentObs
	win           int
	starts        []float64
	mpegts, local float64
}

// cues counts a WebVTT segment's cues and checks each cue's start, mapped
// to PTS through X-TIMESTAMP-MAP, against the PTS range of the probed rung
// segment it PDT-pairs with. A segment with no map header is timed as
// MPEGTS:0, LOCAL:00:00:00.000 (RFC 8216 §3.5: cue time 0 maps to MPEG-TS
// timestamp 0). Any line that starts with X-TIMESTAMP-MAP is a map header.
// It is valid only as X-TIMESTAMP-MAP= with parseable MPEGTS and LOCAL;
// anything else is a CueError, and a later valid map line does not clear
// that. When the video segment isn't probed yet, the cues wait (retried
// after every poll; drain counts what never matches).
func (p *Player) cues(r *rendition, g SegmentObs, win int, body []byte) {
	c := pendingCue{r: r, g: g, win: win}
	malformed := false
	sc := bufio.NewScanner(bytes.NewReader(body))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "X-TIMESTAMP-MAP") {
			a, b := mpegtsRe.FindStringSubmatch(line), localRe.FindStringSubmatch(line)
			if strings.HasPrefix(line, "X-TIMESTAMP-MAP=") && a != nil && b != nil {
				if !malformed {
					n, _ := strconv.ParseFloat(a[1], 64)
					c.mpegts, c.local = n/90000, vttSeconds(b[1])
				}
			} else {
				malformed = true
			}
			continue
		}
		if m := cueRe.FindStringSubmatch(line); m != nil {
			c.starts = append(c.starts, vttSeconds(m[1]))
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.report.Cues += len(c.starts)
	p.windows[win].Cues[r.name] += len(c.starts)
	switch {
	case len(c.starts) == 0:
	case malformed:
		p.report.CueErrors += len(c.starts)
		p.cueErrorf("%s segment %d: cues with a malformed X-TIMESTAMP-MAP", r.name, g.Seq)
	case p.timeCuesLocked(c):
	case len(p.pendingCues) >= maxPendingCues:
		p.report.CueErrors += len(c.starts)
		p.cueErrorf("%s segment %d: %d cue(s) never matched a video segment", r.name, g.Seq, len(c.starts))
	default:
		p.pendingCues = append(p.pendingCues, c)
	}
}

// retryCuesLocked times the waiting cues whose video segment has arrived.
func (p *Player) retryCuesLocked() {
	waiting := p.pendingCues[:0]
	for _, c := range p.pendingCues {
		if !p.timeCuesLocked(c) {
			waiting = append(waiting, c)
		}
	}
	p.pendingCues = waiting
}

// timeCuesLocked times c against the rung's PDT-paired probed segment; false
// when there is none yet.
func (p *Player) timeCuesLocked(c pendingCue) bool {
	var rung *rendition
	for _, o := range append([]*rendition{p.primary}, p.others...) {
		if o.name == strings.TrimSuffix(c.r.name, "_vtt") {
			rung = o
		}
	}
	if rung == nil || c.g.PDT.IsZero() || len(rung.probed) == 0 {
		return false
	}
	k, ok := PairByPDT(PDTSeg{Start: c.g.PDT, Dur: c.g.Duration, DNum: c.g.DNum}, rung.pdtSegs(), durOf(c.g.Duration))
	if !ok || !overlaps(rung.probed[k].seg, PDTSeg{Start: c.g.PDT, Dur: c.g.Duration}) {
		return false
	}
	v := rung.probed[k]
	for _, start := range c.starts {
		pts := c.mpegts + (start - c.local)
		p.report.CuesTimed++
		p.windows[c.win].CuesTimed[c.r.name]++
		lo, hi := ptsDiff(pts, v.pts), ptsDiff(pts, v.end)
		if lo < -cueTolerance || hi > cueTolerance {
			p.report.CueErrors++
			p.cueErrorf("%s segment %d: cue at PTS %.3f outside %s segment %.3f–%.3f", c.r.name, c.g.Seq, pts, rung.name, v.pts, v.end)
		}
	}
	return true
}

// cueErrorf records a cue problem (the first few only).
func (p *Player) cueErrorf(format string, a ...any) {
	if p.report.CueErrors <= 5 {
		p.report.Errors = append(p.report.Errors, fmt.Sprintf(format, a...))
	}
}

// vttSeconds parses [hh:]mm:ss.mmm.
func vttSeconds(v string) float64 {
	parts := strings.Split(v, ":")
	sec := 0.0
	for _, part := range parts {
		f, _ := strconv.ParseFloat(part, 64)
		sec = sec*60 + f
	}
	return sec
}

// ptsPeriod is the 33-bit MPEG-TS clock's wrap, in seconds.
const ptsPeriod = float64(1<<33) / 90000

// ptsDiff is a − b, unwrapped to the nearest 33-bit period.
func ptsDiff(a, b float64) float64 {
	d := math.Mod(a-b, ptsPeriod)
	switch {
	case d > ptsPeriod/2:
		d -= ptsPeriod
	case d < -ptsPeriod/2:
		d += ptsPeriod
	}
	return d
}

func overlaps(a, b PDTSeg) bool { return a.Start.Before(b.End()) && b.Start.Before(a.End()) }

func durOf(sec float64) time.Duration { return time.Duration(sec * float64(time.Second)) }

func (p *Player) get(ctx context.Context, ref string) ([]byte, int, error) {
	u := ref
	if !strings.HasPrefix(ref, "http://") && !strings.HasPrefix(ref, "https://") {
		u = p.cfg.BaseURL + ref
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, 0, err
	}
	resp, err := p.cfg.Client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	return b, resp.StatusCode, err
}

// maxProbed bounds the probed segments kept per rendition for pairing.
const maxProbed = 64

// probed is one probed segment: its wall-clock place and its PTS range.
type probed struct {
	seg      PDTSeg
	pts, end float64
	win      int
	paired   bool
}

// rendition is one followed media playlist and its probe state.
type rendition struct {
	name    string
	url     string
	highest int64
	lastSeq int64   // last probed audio segment (-2: none)
	lastEnd float64 // end PTS of that segment
	everOK  bool    // the playlist was served at least once
	fetched int     // segments fetched OK
	lastOK  time.Time
	probed  []*probed // by PDT
}

func newRendition(name, url string, at time.Time) *rendition {
	return &rendition{name: name, url: url, highest: -1, lastSeq: -2, lastOK: at}
}

func (r *rendition) addProbed(rec *probed) {
	r.probed = append(r.probed, rec)
	if len(r.probed) > maxProbed {
		r.probed = append([]*probed(nil), r.probed[len(r.probed)-maxProbed:]...)
	}
}

func (r *rendition) pdtSegs() []PDTSeg {
	out := make([]PDTSeg, len(r.probed))
	for i, x := range r.probed {
		out[i] = x.seg
	}
	return out
}

func isSubtitleRendition(name string) bool { return strings.HasSuffix(name, "_vtt") }

func isAudioRendition(name string) bool {
	return strings.HasPrefix(name, "aac") || strings.HasPrefix(name, "ac3")
}

// renditionName is a playlist reference's file name without .m3u8 or query.
func renditionName(ref string) string {
	if q := strings.IndexByte(ref, '?'); q >= 0 {
		ref = ref[:q]
	}
	ref = ref[strings.LastIndexByte(ref, '/')+1:]
	return strings.TrimSuffix(ref, ".m3u8")
}

var mediaURIRe = regexp.MustCompile(`URI="([^"]+)"`)

// renditionRefs lists a master's variants (in order), then its audio and
// subtitle renditions.
func renditionRefs(master []byte) []string {
	var variants, media []string
	sc := bufio.NewScanner(bytes.NewReader(master))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case strings.HasPrefix(line, "#EXT-X-MEDIA:") &&
			(strings.Contains(line, "TYPE=AUDIO") || strings.Contains(line, "TYPE=SUBTITLES")):
			if m := mediaURIRe.FindStringSubmatch(line); m != nil {
				media = append(media, m[1])
			}
		case line != "" && !strings.HasPrefix(line, "#"):
			variants = append(variants, line)
		}
	}
	return append(variants, media...)
}

func holeOf(sec float64) time.Duration {
	if sec <= 0 {
		return 0
	}
	return time.Duration(sec * float64(time.Second))
}

func absSeconds(sec float64) time.Duration {
	if sec < 0 {
		sec = -sec
	}
	return time.Duration(sec * float64(time.Second))
}

// resolveRef resolves a playlist reference against the playlist it came from.
func resolveRef(base, ref string) string {
	if strings.HasPrefix(ref, "/") || strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://") {
		return ref
	}
	if q := strings.IndexByte(base, '?'); q >= 0 {
		base = base[:q]
	}
	return base[:strings.LastIndexByte(base, '/')+1] + ref
}

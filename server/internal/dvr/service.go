// Package dvr schedules, captures, converts and retains recordings.
//
// Capture takes raw MPEG-TS from the shared channel ingest (a recording on a
// channel someone is watching costs no extra tuner), writing part-NNN.ts
// files; a dropped stream continues in the next part. When the window ends
// the parts are converted to an HLS VOD for playback with seeking. State lives
// in the store, so a restart resumes or finishes whatever was in flight.
package dvr

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/ajthom90/bowtie/server/internal/store"
	"github.com/ajthom90/bowtie/server/internal/stream"
)

// Defaults for padding, retries and disk space.
const (
	DefaultPadStart   = 60 * time.Second
	DefaultPadEnd     = 3 * time.Minute
	defaultRetryEvery = 15 * time.Second
	sweepEvery        = time.Hour
	// partialAfter: a recording that missed more than this is "partial".
	partialAfter = 60 * time.Second
	// minPartBytes: a part smaller than one TS packet holds nothing.
	minPartBytes = 188
	// shortPart: a part that ended this fast means the stream (or the disk)
	// isn't working; wait RetryEvery before trying again.
	shortPart = time.Second
	// diskFloor: below this much free space a capture doesn't start.
	diskFloor = 2 << 30
	// maxSweepDeletes bounds one retention sweep; inUseWindow protects a
	// recording someone saved a position in recently.
	maxSweepDeletes = 10
	inUseWindow     = 30 * time.Minute
	hlsDir          = "hls"
)

// ErrBadWindow: the requested start/stop is backwards or already over.
var ErrBadWindow = errors.New("recording must end after it starts, and in the future")

// ErrNotStoppable: only a scheduled, waiting or recording recording can stop.
var ErrNotStoppable = errors.New("this recording isn't in progress")

// Source opens a raw MPEG-TS stream for a channel (production: the shared
// ingest). It returns stream.ErrTunersBusy / stream.ErrNoSignal when it can't.
type Source interface {
	Open(ctx context.Context, ch store.Channel) (io.ReadCloser, error)
}

// Converter turns capture parts into an HLS VOD in outDir and returns the
// recording's duration.
type Converter interface {
	Convert(ctx context.Context, parts []string, outDir string) (time.Duration, error)
}

// Deps wires a Service.
type Deps struct {
	Store     *store.Store
	Source    Source
	Converter Converter
	// Dir holds one folder per recording (never the segment tmpfs).
	Dir   string
	Clock func() time.Time
	// RetryEvery is the wait between tuner attempts inside a window (15 s).
	RetryEvery time.Duration
	// MinFreeBytes: the retention sweep deletes the oldest unprotected
	// recordings while free space in Dir is below this (0 = never).
	MinFreeBytes int64
	FreeBytes    func(dir string) (int64, error)
}

// Warning accompanies a successful Schedule.
type Warning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ConflictError: the recording would need more tuners than exist.
type ConflictError struct {
	TunerCount int
	Conflicts  []store.Recording // overlapping recordings on other channels, plus none for the new one
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("tuner conflict: %d recordings on different channels overlap, but there are only %d tuners", len(e.Conflicts), e.TunerCount)
}

// ScheduleRequest is a one-off (guide) or manual recording.
type ScheduleRequest struct {
	UserID      int64
	Channel     store.Channel
	Title       string
	Subtitle    string
	Description string
	Category    string
	IconURL     string
	Rating      string
	Start, Stop time.Time
	Force       bool // schedule despite a tuner conflict
	RuleID      int64
	ProgramID   string
}

// Service runs the DVR.
type Service struct {
	deps Deps

	mu        sync.Mutex
	captures  map[int64]*capture // in-flight captures by recording ID
	deleting  map[int64]bool     // Delete in progress: never start a capture
	tickMu    sync.Mutex         // one Tick at a time; Shutdown waits for it
	rulesMu   sync.Mutex         // one ApplyRules at a time
	convQueue chan int64
	queued    map[int64]bool
	lastSweep time.Time
	stopped   bool
	onDeleted func() // test hook: after the sweep deletes a recording

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

type capture struct {
	cancel context.CancelFunc
	done   chan struct{}
	// shutdown: the server is stopping; leave the row as it is.
	shutdown bool
}

// New builds a Service and starts its conversion worker.
func New(deps Deps) *Service {
	if deps.Clock == nil {
		deps.Clock = time.Now
	}
	if deps.RetryEvery <= 0 {
		deps.RetryEvery = defaultRetryEvery
	}
	if deps.FreeBytes == nil {
		deps.FreeBytes = freeBytes
	}
	// FFmpeg's concat list resolves relative entries against the list's own
	// directory, so every path the DVR hands out must be absolute.
	if abs, err := filepath.Abs(deps.Dir); err == nil {
		deps.Dir = abs
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Service{
		deps:      deps,
		captures:  map[int64]*capture{},
		deleting:  map[int64]bool{},
		convQueue: make(chan int64, 256),
		queued:    map[int64]bool{},
		ctx:       ctx,
		cancel:    cancel,
	}
	s.wg.Add(1)
	go s.convertWorker()
	return s
}

// Run ticks every 5 s until ctx ends, then shuts the service down.
func (s *Service) Run(ctx context.Context) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	s.Tick()
	for {
		select {
		case <-ctx.Done():
			s.Shutdown()
			return
		case <-t.C:
			s.Tick()
		}
	}
}

// Shutdown stops captures (leaving their rows to resume on restart) and the
// conversion worker. Safe to call more than once.
func (s *Service) Shutdown() {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return
	}
	s.stopped = true
	s.mu.Unlock()
	s.tickMu.Lock()   // let a running Tick finish; later Ticks see stopped
	s.tickMu.Unlock() //nolint:staticcheck // barrier
	s.mu.Lock()
	caps := make([]*capture, 0, len(s.captures))
	for _, c := range s.captures {
		c.shutdown = true
		c.cancel()
		caps = append(caps, c)
	}
	s.mu.Unlock()
	for _, c := range caps {
		<-c.done
	}
	s.cancel()
	s.wg.Wait()
}

// Schedule validates and stores a recording. It refuses (ConflictError) when
// the recording would need more tuners than all devices have, unless Force;
// overlapping recordings on the same channel share a tuner.
func (s *Service) Schedule(req ScheduleRequest) (store.Recording, []Warning, error) {
	now := s.deps.Clock()
	if !req.Stop.After(req.Start) || !req.Stop.After(now) {
		return store.Recording{}, nil, ErrBadWindow
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = "Recording"
	}
	r := store.Recording{
		UserID: req.UserID, ChannelID: req.Channel.ID,
		ChannelName: strings.TrimSpace(req.Channel.GuideNumber + " " + req.Channel.Name),
		Title:       title, Subtitle: req.Subtitle, Description: req.Description,
		Category: req.Category, IconURL: req.IconURL, Rating: req.Rating,
		RuleID: req.RuleID, ProgramID: req.ProgramID,
		Start: req.Start.UTC(), Stop: req.Stop.UTC(),
		PadStartSec: int(DefaultPadStart / time.Second), PadEndSec: int(DefaultPadEnd / time.Second),
		State: store.RecScheduled, CreatedAt: now.UTC(),
	}

	tuners, err := s.tunerCount()
	if err != nil {
		return store.Recording{}, nil, err
	}
	overlapping, err := s.overlapping(r)
	if err != nil {
		return store.Recording{}, nil, err
	}
	for _, o := range overlapping {
		if o.ChannelID == r.ChannelID && o.Start.Equal(r.Start) {
			return o, nil, nil // already scheduled (double tap, retry, second app)
		}
	}
	peak, atPeak := peakChannels(r, overlapping)
	var warnings []Warning
	if tuners > 0 && peak > tuners && !req.Force {
		return store.Recording{}, nil, &ConflictError{TunerCount: tuners, Conflicts: atPeak}
	}
	if tuners > 0 && peak >= tuners && peak > 1 {
		warnings = append(warnings, Warning{Code: "usesAllTuners",
			Message: "This uses every tuner at that time. If another app or someone watching live TV is using one, this may not record."})
	}

	id, err := s.deps.Store.CreateRecording(r)
	if err != nil {
		return store.Recording{}, nil, err
	}
	r.ID = id
	return r, warnings, nil
}

// overlapping returns pending recordings whose show times overlap r's
// (padding is soft and ignored).
func (s *Service) overlapping(r store.Recording) ([]store.Recording, error) {
	pending, err := s.deps.Store.ListRecordings(store.RecScheduled, store.RecWaiting, store.RecRecording)
	if err != nil {
		return nil, err
	}
	var out []store.Recording
	for _, p := range pending {
		if p.Start.Before(r.Stop) && r.Start.Before(p.Stop) {
			out = append(out, p)
		}
	}
	return out, nil
}

// peakChannels is the most distinct channels recording at any one moment of
// r's show (r included), and the other-channel recordings running then.
// Recordings on r's channel share its tuner.
func peakChannels(r store.Recording, overlapping []store.Recording) (int, []store.Recording) {
	moments := []time.Time{r.Start}
	for _, o := range overlapping {
		if o.Start.After(r.Start) && o.Start.Before(r.Stop) {
			moments = append(moments, o.Start)
		}
	}
	best, bestAt := 0, []store.Recording(nil)
	for _, t := range moments {
		chans := map[int64]bool{r.ChannelID: true}
		var at []store.Recording
		for _, o := range overlapping {
			if !o.Start.After(t) && o.Stop.After(t) && o.ChannelID != r.ChannelID {
				if !chans[o.ChannelID] {
					chans[o.ChannelID] = true
				}
				at = append(at, o)
			}
		}
		if len(chans) > best {
			best, bestAt = len(chans), at
		}
	}
	return best, bestAt
}

func (s *Service) tunerCount() (int, error) {
	devs, err := s.deps.Store.ListDevices()
	if err != nil {
		return 0, err
	}
	n := 0
	for _, d := range devs {
		n += d.TunerCount
	}
	return n, nil
}

// Tick starts captures whose window opened, ends those whose window closed,
// finishes rows left over from a restart, and runs the hourly sweep.
func (s *Service) Tick() {
	s.tickMu.Lock()
	defer s.tickMu.Unlock()
	now := s.deps.Clock()
	rows, err := s.deps.Store.ListRecordings(store.RecScheduled, store.RecWaiting, store.RecRecording, store.RecConverting)
	if err != nil {
		log.Printf("dvr: list recordings: %v", err)
		return
	}
	for _, r := range rows {
		s.mu.Lock()
		c, active := s.captures[r.ID]
		stopped := s.stopped
		s.mu.Unlock()
		if stopped {
			return
		}
		switch {
		case r.State == store.RecConverting:
			s.enqueue(r.ID)
		case active && !now.Before(r.WindowStop()):
			c.cancel() // the capture goroutine finalizes the row
		case !active && !now.Before(r.WindowStop()):
			s.finishCapture(r.ID, now) // window passed while we weren't running
		case !active && !now.Before(r.WindowStart()):
			s.startCapture(r)
		}
	}
	if now.Sub(s.lastSweep) >= sweepEvery {
		s.lastSweep = now
		s.ApplyRules()
		s.sweep()
	}
}

// StopNow ends a recording early, keeping what was captured.
func (s *Service) StopNow(id int64) error {
	now := s.deps.Clock().UTC()
	ok, err := s.deps.Store.StopRecordingAt(id, now)
	if err != nil {
		return err
	}
	if !ok {
		if _, err := s.deps.Store.RecordingByID(id); err != nil {
			return err
		}
		return ErrNotStoppable
	}
	s.mu.Lock()
	c := s.captures[id]
	s.mu.Unlock()
	if c != nil {
		c.cancel() // the capture finalizes the row
		return nil
	}
	s.finishCapture(id, now)
	return nil
}

// Delete cancels a scheduled recording or removes a finished one, with its
// files. An upcoming episode a series rule scheduled is marked skipped
// instead, so the rule doesn't schedule it again.
func (s *Service) Delete(id int64) error { return s.delete(id, true) }

// CancelRule removes a series rule's upcoming episodes (stopping any that is
// capturing). Recorded ones stay.
func (s *Service) CancelRule(ruleID int64) {
	rows, err := s.deps.Store.ListRecordings(store.RecScheduled, store.RecWaiting, store.RecRecording)
	if err != nil {
		return
	}
	for _, r := range rows {
		if r.RuleID == ruleID {
			_ = s.delete(r.ID, false)
		}
	}
}

func (s *Service) delete(id int64, allowSkip bool) error {
	r, err := s.deps.Store.RecordingByID(id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.deleting[id] = true // no capture may start for this row from here on
	c := s.captures[id]
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.deleting, id)
		s.mu.Unlock()
	}()
	if allowSkip && r.RuleID != 0 && r.State == store.RecScheduled && c == nil {
		r.State, r.Failure, r.FailureDetail = store.RecFailed, "skipped", "Skipped"
		return s.deps.Store.UpdateRecording(r)
	}
	if c != nil {
		s.mu.Lock()
		c.shutdown = true // don't finalize: the row is going away
		s.mu.Unlock()
		c.cancel()
		<-c.done
	}
	if err := s.deps.Store.DeleteRecording(id); err != nil {
		return err
	}
	if r.Dir != "" {
		if err := os.RemoveAll(r.Dir); err != nil {
			log.Printf("dvr: remove %s: %v", r.Dir, err)
		}
	}
	return nil
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

func (s *Service) recordingDir(r store.Recording) string {
	slug := strings.Trim(slugRe.ReplaceAllString(strings.ToLower(r.Title), "-"), "-")
	if len(slug) > 40 {
		slug = slug[:40]
	}
	return filepath.Join(s.deps.Dir, fmt.Sprintf("%d-%s", r.ID, slug))
}

func (s *Service) startCapture(r store.Recording) {
	ctx, cancel := context.WithCancel(s.ctx)
	c := &capture{cancel: cancel, done: make(chan struct{})}
	s.mu.Lock()
	if s.stopped || s.deleting[r.ID] || s.captures[r.ID] != nil {
		s.mu.Unlock()
		cancel()
		return
	}
	s.captures[r.ID] = c
	s.mu.Unlock()
	fail := func(failure string, err error) {
		log.Printf("dvr: recording %d: %v", r.ID, err)
		r.State, r.Failure, r.FailureDetail = store.RecFailed, failure, err.Error()
		_ = s.deps.Store.UpdateRecording(r)
		s.mu.Lock()
		delete(s.captures, r.ID)
		s.mu.Unlock()
		cancel()
		close(c.done)
	}
	if free, err := s.deps.FreeBytes(s.deps.Dir); err == nil && free < diskFloor {
		s.sweep()
		if free, err := s.deps.FreeBytes(s.deps.Dir); err == nil && free < diskFloor {
			fail("diskFull", fmt.Errorf("only %d MB free in %s", free>>20, s.deps.Dir))
			return
		}
	}
	if r.Dir == "" {
		r.Dir = s.recordingDir(r)
	}
	if err := os.MkdirAll(r.Dir, 0o755); err != nil {
		fail("diskFull", err)
		return
	}
	if err := s.deps.Store.UpdateRecording(r); err != nil {
		log.Printf("dvr: recording %d: %v", r.ID, err)
		s.mu.Lock()
		delete(s.captures, r.ID)
		s.mu.Unlock()
		cancel()
		close(c.done)
		return
	}
	go s.runCapture(ctx, c, r)
}

// runCapture records r until its context ends, re-opening the stream (into a
// new part) whenever it drops or no tuner is free.
func (s *Service) runCapture(ctx context.Context, c *capture, r store.Recording) {
	defer close(c.done)
	defer func() {
		s.mu.Lock()
		shutdown := c.shutdown
		s.mu.Unlock()
		// Finalize before leaving the captures map, so Tick can't start a
		// second capture for a row that is still "recording".
		if shutdown {
			s.noteStopped(r.ID) // a restart resumes from here; the gap counts as missed
		} else {
			s.finishCapture(r.ID, s.deps.Clock())
		}
		s.mu.Lock()
		delete(s.captures, r.ID)
		s.mu.Unlock()
	}()

	ch, err := s.deps.Store.ChannelByID(r.ChannelID)
	if err != nil {
		ch = store.Channel{ID: r.ChannelID}
	}
	part := lastPart(r.Dir)
	var gapFrom time.Time // when the last part ended (a dropped stream)
	wait := func() bool {
		select {
		case <-ctx.Done():
			return false
		case <-time.After(s.deps.RetryEvery):
			return true
		}
	}
	for ctx.Err() == nil {
		rc, err := s.deps.Source.Open(ctx, ch)
		if err != nil {
			s.noteWaiting(r.ID, err)
			if !wait() {
				return
			}
			continue
		}
		part++
		path := filepath.Join(r.Dir, fmt.Sprintf("part-%03d.ts", part))
		s.noteRecording(r.ID, gapFrom)
		began := time.Now()
		n, err := s.writePart(ctx, rc, path)
		if n < minPartBytes {
			_ = os.Remove(path) // nothing usable; reuse the number
			part--
		}
		gapFrom = s.deps.Clock()
		if err != nil {
			log.Printf("dvr: recording %d part %d: %v", r.ID, part, err)
			if errors.Is(err, syscall.ENOSPC) {
				s.noteFailure(r.ID, "diskFull", err)
			}
		}
		// A part that failed or ended at once means the stream or the disk
		// isn't working: don't spin (and hold the tuner) — wait, then retry.
		if err != nil || time.Since(began) < shortPart {
			if !wait() {
				return
			}
		}
	}
}

func (s *Service) writePart(ctx context.Context, rc io.ReadCloser, path string) (int64, error) {
	stop := context.AfterFunc(ctx, func() { _ = rc.Close() })
	defer stop()
	defer func() { _ = rc.Close() }()
	if ctx.Err() != nil {
		return 0, nil
	}
	f, err := os.Create(path)
	if err != nil {
		return 0, err
	}
	n, copyErr := io.Copy(f, rc)
	if err := f.Close(); err != nil {
		return n, err
	}
	if copyErr != nil && ctx.Err() == nil && !errors.Is(copyErr, io.EOF) && !errors.Is(copyErr, io.ErrClosedPipe) {
		return n, copyErr
	}
	return n, nil
}

// noteFailure records a failure reason on a recording without changing its
// state (the capture keeps retrying).
func (s *Service) noteFailure(id int64, failure string, err error) {
	r, gerr := s.deps.Store.RecordingByID(id)
	if gerr != nil || r.Failure == failure {
		return
	}
	r.Failure, r.FailureDetail = failure, err.Error()
	_ = s.deps.Store.UpdateRecording(r)
}

func (s *Service) noteWaiting(id int64, err error) {
	r, gerr := s.deps.Store.RecordingByID(id)
	if gerr != nil {
		return
	}
	failure := "error"
	switch {
	case errors.Is(err, stream.ErrTunersBusy):
		failure = "noTuner"
	case errors.Is(err, stream.ErrNoSignal):
		failure = "noSignal"
	}
	changed := r.Failure != failure
	r.Failure, r.FailureDetail = failure, err.Error()
	if r.State == store.RecScheduled {
		r.State, changed = store.RecWaiting, true
	}
	if changed {
		_ = s.deps.Store.UpdateRecording(r)
	}
}

// noteRecording marks a part starting. gapFrom (zero for the first part of
// this run) is when the previous part ended; a restart's gap comes from the
// row's ActualStop (noteStopped).
func (s *Service) noteRecording(id int64, gapFrom time.Time) {
	r, err := s.deps.Store.RecordingByID(id)
	if err != nil {
		return
	}
	now := s.deps.Clock().UTC()
	switch {
	case r.ActualStart.IsZero():
		r.ActualStart = now
		if late := now.Sub(captureFrom(r)); late > 0 {
			r.MissedSec = int(late / time.Second)
		}
	case !gapFrom.IsZero():
		r.MissedSec += int(now.Sub(gapFrom) / time.Second)
	case !r.ActualStop.IsZero():
		r.MissedSec += int(now.Sub(r.ActualStop) / time.Second)
	}
	r.ActualStop = time.Time{}
	r.State, r.Failure, r.FailureDetail = store.RecRecording, "", ""
	_ = s.deps.Store.UpdateRecording(r)
}

// noteStopped records when capture stopped for a server shutdown.
func (s *Service) noteStopped(id int64) {
	r, err := s.deps.Store.RecordingByID(id)
	if err != nil || r.ActualStart.IsZero() {
		return
	}
	r.ActualStop = s.deps.Clock().UTC()
	_ = s.deps.Store.UpdateRecording(r)
}

// finishCapture moves a recording whose window ended to converting (if
// anything was captured) or failed.
func (s *Service) finishCapture(id int64, now time.Time) {
	r, err := s.deps.Store.RecordingByID(id)
	if err != nil {
		return
	}
	parts := partFiles(r.Dir)
	var size int64
	for _, p := range parts {
		if fi, err := os.Stat(p); err == nil {
			size += fi.Size()
		}
	}
	if size == 0 {
		r.State = store.RecFailed
		if r.Failure == "" {
			r.Failure = "error"
			r.FailureDetail = "nothing was recorded (the server was not running during the recording)"
		}
		_ = s.deps.Store.UpdateRecording(r)
		return
	}
	stopAt := now.UTC()
	if w := r.WindowStop(); stopAt.After(w) {
		stopAt = w
	}
	if !r.ActualStop.IsZero() && r.ActualStop.Before(stopAt) {
		stopAt = r.ActualStop // stopped by a shutdown and never resumed
	}
	r.ActualStop = stopAt
	r.SizeBytes = size
	r.State = store.RecConverting
	_ = s.deps.Store.UpdateRecording(r)
	s.enqueue(id)
}

func (s *Service) enqueue(id int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.queued[id] || s.stopped {
		return
	}
	s.queued[id] = true
	select {
	case s.convQueue <- id:
	default:
		delete(s.queued, id) // full: the next Tick retries
	}
}

func (s *Service) convertWorker() {
	defer s.wg.Done()
	for {
		select {
		case <-s.ctx.Done():
			return
		case id := <-s.convQueue:
			s.convert(id)
			s.mu.Lock()
			delete(s.queued, id)
			s.mu.Unlock()
		}
	}
}

func (s *Service) convert(id int64) {
	r, err := s.deps.Store.RecordingByID(id)
	if err != nil || r.State != store.RecConverting {
		return
	}
	parts := partFiles(r.Dir)
	out := filepath.Join(r.Dir, hlsDir)
	var dur time.Duration
	if len(parts) == 0 {
		// Converted before, but the "ready" write didn't land: keep the VOD.
		video, rerr := os.ReadFile(filepath.Join(out, vodLayout.TopName()+".m3u8"))
		if _, merr := os.Stat(filepath.Join(out, MasterName)); rerr != nil || merr != nil || !strings.Contains(string(video), "#EXT-X-ENDLIST") {
			r.State, r.Failure, r.FailureDetail = store.RecFailed, "error", "nothing was recorded"
			_ = s.deps.Store.UpdateRecording(r)
			return
		}
		dur = playlistDuration(video)
	} else {
		_ = os.RemoveAll(out)
		dur, err = s.deps.Converter.Convert(s.ctx, parts, out)
		if s.ctx.Err() != nil {
			return // shutting down: convert again after restart
		}
		if err != nil {
			r, gerr := s.deps.Store.RecordingByID(id)
			if gerr != nil {
				return // deleted meanwhile
			}
			log.Printf("dvr: convert recording %d: %v", id, err)
			r.State, r.Failure, r.FailureDetail = store.RecFailed, "error", "conversion failed: "+err.Error()
			_ = s.deps.Store.UpdateRecording(r)
			return
		}
	}
	r, gerr := s.deps.Store.RecordingByID(id)
	if gerr != nil {
		return // deleted meanwhile
	}
	r.DurationSec = int(dur / time.Second)
	r.SizeBytes = dirSize(out)
	captured := r.ActualStop.Sub(r.ActualStart)
	window := r.WindowStop().Sub(captureFrom(r))
	r.Partial = time.Duration(r.MissedSec)*time.Second > partialAfter || window-captured > partialAfter
	r.State = store.RecReady
	if err := s.deps.Store.UpdateRecording(r); err != nil {
		log.Printf("dvr: recording %d: %v", id, err)
		return // keep the parts; the next Tick finishes it
	}
	for _, p := range parts {
		_ = os.Remove(p)
	}
	s.pruneRule(r.RuleID)
}

// sweep deletes the oldest unprotected finished recordings while free space
// is below MinFreeBytes.
func (s *Service) sweep() {
	if s.deps.MinFreeBytes <= 0 {
		return
	}
	ready, err := s.deps.Store.ListRecordings(store.RecReady, store.RecFailed)
	if err != nil {
		return
	}
	sort.SliceStable(ready, func(i, j int) bool { return ready[i].Start.Before(ready[j].Start) })
	deleted := 0
	lastFree := int64(-1)
	for _, r := range ready {
		free, err := s.deps.FreeBytes(s.deps.Dir)
		if err != nil || free >= s.deps.MinFreeBytes || deleted >= maxSweepDeletes {
			return
		}
		if lastFree >= 0 && free <= lastFree {
			// Deleting didn't free anything (the space is taken by something
			// else); stop rather than empty the library.
			log.Printf("dvr: low on space, but deleting recordings isn't freeing any; stopping")
			return
		}
		if r.Protected || r.Failure == "skipped" { // a skip marker keeps a series rule from rescheduling
			continue
		}
		if watching, _ := s.deps.Store.RecordingWatchedSince(r.ID, s.deps.Clock().Add(-inUseWindow)); watching {
			continue
		}
		holdsFiles := r.SizeBytes > 0 || (r.Dir != "" && dirSize(r.Dir) > 0)
		log.Printf("dvr: low on space (%d bytes free): deleting %q (%s)", free, r.Title, r.Start.Format(time.RFC3339))
		if err := s.Delete(r.ID); err == nil {
			if holdsFiles { // only deletions that should free space count
				deleted++
				lastFree = free
			}
			if s.onDeleted != nil {
				s.onDeleted()
			}
		}
	}
}

// captureFrom is when capture could first have started: the padded start,
// or when the recording was scheduled if that was later ("record now").
func captureFrom(r store.Recording) time.Time {
	if from := r.WindowStart(); from.After(r.CreatedAt) {
		return from
	}
	return r.CreatedAt
}

// PlaylistDir is where a ready recording's HLS VOD lives.
func PlaylistDir(r store.Recording) string { return filepath.Join(r.Dir, hlsDir) }

func partFiles(dir string) []string {
	if dir == "" {
		return nil
	}
	m, _ := filepath.Glob(filepath.Join(dir, "part-*.ts"))
	var out []string
	for _, p := range m {
		if fi, err := os.Stat(p); err == nil && fi.Size() < minPartBytes {
			_ = os.Remove(p) // empty part: nothing for the concat list
			continue
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return partNumber(out[i]) < partNumber(out[j]) })
	return out
}

// partNumber is N from part-N.ts (numeric, so part-1000 sorts after part-999).
func partNumber(path string) int {
	n, _ := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(filepath.Base(path), "part-"), ".ts"))
	return n
}

// lastPart is the highest part number already in dir (0 if none).
func lastPart(dir string) int {
	last := 0
	for _, p := range partFiles(dir) {
		if n := partNumber(p); n > last {
			last = n
		}
	}
	return last
}

func dirSize(dir string) int64 {
	var n int64
	_ = filepath.Walk(dir, func(_ string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() {
			n += fi.Size()
		}
		return nil
	})
	return n
}

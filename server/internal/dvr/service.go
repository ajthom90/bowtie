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
	"strings"
	"sync"
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
	hlsDir       = "hls"
)

// ErrBadWindow: the requested start/stop is backwards or already over.
var ErrBadWindow = errors.New("recording must end after it starts, and in the future")

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
	Start, Stop time.Time
	Force       bool // schedule despite a tuner conflict
}

// Service runs the DVR.
type Service struct {
	deps Deps

	mu        sync.Mutex
	captures  map[int64]*capture // in-flight captures by recording ID
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
	ctx, cancel := context.WithCancel(context.Background())
	s := &Service{
		deps:      deps,
		captures:  map[int64]*capture{},
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
		Category: req.Category, IconURL: req.IconURL,
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
	channels := map[int64]bool{r.ChannelID: true}
	for _, o := range overlapping {
		channels[o.ChannelID] = true
	}
	var warnings []Warning
	if tuners > 0 && len(channels) > tuners && !req.Force {
		return store.Recording{}, nil, &ConflictError{TunerCount: tuners, Conflicts: overlapping}
	}
	if tuners > 0 && len(channels) >= tuners && len(channels) > 1 {
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
		s.sweep()
	}
}

// StopNow ends a recording early, keeping what was captured.
func (s *Service) StopNow(id int64) error {
	r, err := s.deps.Store.RecordingByID(id)
	if err != nil {
		return err
	}
	now := s.deps.Clock().UTC()
	if now.Before(r.Stop) {
		r.Stop = now
	}
	r.PadEndSec = 0
	if now.Before(r.Start) {
		r.Start = now
		r.PadStartSec = 0
	}
	if err := s.deps.Store.UpdateRecording(r); err != nil {
		return err
	}
	s.mu.Lock()
	c := s.captures[id]
	s.mu.Unlock()
	if c != nil {
		c.cancel()
		return nil
	}
	if r.State == store.RecScheduled || r.State == store.RecWaiting || r.State == store.RecRecording {
		s.finishCapture(id, now)
	}
	return nil
}

// Delete cancels a scheduled recording or removes a finished one, with its
// files.
func (s *Service) Delete(id int64) error {
	r, err := s.deps.Store.RecordingByID(id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	c := s.captures[id]
	if c != nil {
		c.shutdown = true // don't finalize: the row is going away
		c.cancel()
	}
	s.mu.Unlock()
	if c != nil {
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
	if r.Dir == "" {
		r.Dir = s.recordingDir(r)
	}
	if err := os.MkdirAll(r.Dir, 0o755); err != nil {
		log.Printf("dvr: recording %d: %v", r.ID, err)
		r.State, r.Failure = store.RecFailed, "diskFull"
		r.FailureDetail = err.Error()
		_ = s.deps.Store.UpdateRecording(r)
		return
	}
	if err := s.deps.Store.UpdateRecording(r); err != nil {
		log.Printf("dvr: recording %d: %v", r.ID, err)
		return
	}
	ctx, cancel := context.WithCancel(s.ctx)
	c := &capture{cancel: cancel, done: make(chan struct{})}
	s.mu.Lock()
	s.captures[r.ID] = c
	s.mu.Unlock()
	go s.runCapture(ctx, c, r)
}

// runCapture records r until its context ends, re-opening the stream (into a
// new part) whenever it drops or no tuner is free.
func (s *Service) runCapture(ctx context.Context, c *capture, r store.Recording) {
	defer close(c.done)
	defer func() {
		s.mu.Lock()
		delete(s.captures, r.ID)
		shutdown := c.shutdown
		s.mu.Unlock()
		if shutdown {
			s.noteStopped(r.ID) // a restart resumes from here; the gap counts as missed
			return
		}
		s.finishCapture(r.ID, s.deps.Clock())
	}()

	ch, err := s.deps.Store.ChannelByID(r.ChannelID)
	if err != nil {
		ch = store.Channel{ID: r.ChannelID}
	}
	part := len(partFiles(r.Dir))
	var gapFrom time.Time // when the last part ended (a dropped stream)
	for ctx.Err() == nil {
		rc, err := s.deps.Source.Open(ctx, ch)
		if err != nil {
			s.noteWaiting(r.ID, err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(s.deps.RetryEvery):
			}
			continue
		}
		part++
		s.noteRecording(r.ID, gapFrom)
		if err := s.writePart(ctx, rc, filepath.Join(r.Dir, fmt.Sprintf("part-%03d.ts", part))); err != nil {
			log.Printf("dvr: recording %d part %d: %v", r.ID, part, err)
		}
		gapFrom = s.deps.Clock()
	}
}

func (s *Service) writePart(ctx context.Context, rc io.ReadCloser, path string) error {
	stop := context.AfterFunc(ctx, func() { _ = rc.Close() })
	defer stop()
	defer func() { _ = rc.Close() }()
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(f, rc)
	if err := f.Close(); err != nil {
		return err
	}
	if copyErr != nil && ctx.Err() == nil && !errors.Is(copyErr, io.EOF) && !errors.Is(copyErr, io.ErrClosedPipe) {
		return copyErr
	}
	return nil
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
	_ = os.RemoveAll(out)
	dur, err := s.deps.Converter.Convert(s.ctx, parts, out)
	if s.ctx.Err() != nil {
		return // shutting down: convert again after restart
	}
	r, gerr := s.deps.Store.RecordingByID(id)
	if gerr != nil {
		return // deleted meanwhile
	}
	if err != nil {
		log.Printf("dvr: convert recording %d: %v", id, err)
		r.State, r.Failure, r.FailureDetail = store.RecFailed, "error", "conversion failed: "+err.Error()
		_ = s.deps.Store.UpdateRecording(r)
		return
	}
	for _, p := range parts {
		_ = os.Remove(p)
	}
	r.DurationSec = int(dur / time.Second)
	r.SizeBytes = dirSize(out)
	captured := r.ActualStop.Sub(r.ActualStart)
	window := r.WindowStop().Sub(captureFrom(r))
	r.Partial = time.Duration(r.MissedSec)*time.Second > partialAfter || window-captured > partialAfter
	r.State = store.RecReady
	if err := s.deps.Store.UpdateRecording(r); err != nil {
		log.Printf("dvr: recording %d: %v", id, err)
	}
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
	for _, r := range ready {
		free, err := s.deps.FreeBytes(s.deps.Dir)
		if err != nil || free >= s.deps.MinFreeBytes {
			return
		}
		if r.Protected {
			continue
		}
		log.Printf("dvr: low on space (%d bytes free): deleting %q (%s)", free, r.Title, r.Start.Format(time.RFC3339))
		if err := s.Delete(r.ID); err == nil && s.onDeleted != nil {
			s.onDeleted()
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
	sort.Strings(m)
	return m
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

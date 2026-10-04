package dvr

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ajthom90/bowtie/server/internal/store"
)

// Commercial detection: after a recording is ready, Comskip reads its HLS
// VOD (libavformat opens the master playlist with both renditions, so the
// offsets are on the playback timeline) and writes an EDL of the breaks.
const (
	// minCommercial: a shorter "break" is noise (a fade, a bumper).
	minCommercial = 5.0
	// mergeGap: breaks closer than this are one break.
	mergeGap = 1.0
	// detectWorkDir holds Comskip's output inside the recording's folder (the
	// same disk; Delete removes it with the folder).
	detectWorkDir = ".comskip"
	// detectBatch bounds one look at the backlog.
	detectBatch = 16
)

// Detector finds commercial breaks in a ready recording's HLS VOD. playlist
// is the master playlist; workDir is an empty folder for its output.
type Detector interface {
	Detect(ctx context.Context, playlist, workDir string) ([]store.Commercial, error)
}

// ParseEDL reads an MPlayer/Kodi EDL ("start end type" per line, seconds)
// and returns its cuts (type 0) and commercial breaks (type 3); a line
// without a type is a cut. Other types (mute, scene marker) and lines that
// don't parse are skipped.
func ParseEDL(r io.Reader) ([]store.Commercial, error) {
	var out []store.Commercial
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 2 {
			continue
		}
		start, err1 := strconv.ParseFloat(f[0], 64)
		end, err2 := strconv.ParseFloat(f[1], 64)
		if err1 != nil || err2 != nil {
			continue
		}
		if len(f) >= 3 && f[2] != "0" && f[2] != "3" {
			continue
		}
		out = append(out, store.Commercial{Start: start, End: end})
	}
	return out, sc.Err()
}

// CleanSegments sorts breaks, merges overlapping or nearly touching ones,
// clamps them to [0, dur] (dur <= 0: unknown, no upper clamp) and drops
// those shorter than minCommercial.
func CleanSegments(segs []store.Commercial, dur float64) []store.Commercial {
	in := make([]store.Commercial, 0, len(segs))
	for _, s := range segs {
		if math.IsNaN(s.Start) || math.IsNaN(s.End) || math.IsInf(s.Start, 0) || math.IsInf(s.End, 0) {
			continue
		}
		s.Start = math.Max(s.Start, 0)
		if dur > 0 {
			s.End = math.Min(s.End, dur)
		}
		if s.End > s.Start {
			in = append(in, s)
		}
	}
	sort.Slice(in, func(i, j int) bool { return in[i].Start < in[j].Start })
	var merged []store.Commercial
	for _, s := range in {
		if n := len(merged); n > 0 && s.Start <= merged[n-1].End+mergeGap {
			merged[n-1].End = math.Max(merged[n-1].End, s.End)
			continue
		}
		merged = append(merged, s)
	}
	var out []store.Commercial
	for _, s := range merged {
		if s.End-s.Start >= minCommercial {
			out = append(out, s)
		}
	}
	return out
}

// DefaultComskipINI is written to <data dir>/comskip.ini on first use when no
// ini is configured (BOWTIE_COMSKIP_INI). It's an ordinary Comskip ini; edit
// it to tune detection.
const DefaultComskipINI = `; Comskip settings written by Bowtie on first use. Edit to tune detection;
; Bowtie only needs output_edl=1. Unlisted options keep Comskip's defaults.
[Main Settings]
; 1 black frame + 2 logo + 8 resolution change + 32 aspect ratio (Comskip's default)
detect_method=43
verbose=0
[Detection Settings]
; US breaks: at least 25 s, at most 10 min; one ad 4 to 125 s
max_commercialbreak=600
min_commercialbreak=25
max_commercial_size=125
min_commercial_size=4
min_show_segment_length=125
[Output Control]
output_edl=1
edl_skip_field=0
output_default=0
output_framearray=0
output_videoredo=0
output_womble=0
output_mls=0
output_mpgtx=0
output_dvrmstb=0
output_dvrcut=0
output_ipodchap=0
output_chapters=0
output_zoomplayer_cutlist=0
output_zoomplayer_chapter=0
output_plist_cutlist=0
output_vdr=0
output_projectx=0
output_avisynth=0
output_vcf=0
output_btv=0
output_edlp=0
output_bsplayer=0
output_edlx=0
output_cuttermaran=0
output_scf=0
output_ffmeta=0
output_ffsplit=0
output_incommercial=0
output_tuning=0
output_training=0
output_aspect=0
delete_logo_file=1
live_tv=0
`

// ErrDetectorSetup marks a detection failure that isn't about one recording
// (bad ini, a binary that won't run): detection pauses until restart rather
// than marking every recording "no commercials".
var ErrDetectorSetup = errors.New("commercial detection is misconfigured")

// ErrDetectionOff: commercial detection isn't available (no Comskip, or it
// stopped on a setup error until restart).
var ErrDetectionOff = errors.New("commercial detection isn't available")

// ErrNotReady: the recording hasn't finished converting.
var ErrNotReady = errors.New("this recording isn't ready yet")

// Redetect runs commercial detection on a ready recording again (say after
// comskip.ini was edited). A run already in progress just finishes.
func (s *Service) Redetect(id int64) error {
	r, err := s.deps.Store.RecordingByID(id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	off := s.deps.Detector == nil || s.detectOff
	_, running := s.detecting[id]
	s.mu.Unlock()
	if off {
		return ErrDetectionOff
	}
	if r.State != store.RecReady {
		return ErrNotReady
	}
	if running {
		return nil
	}
	if err := s.deps.Store.ClearRecordingCommercials(id); err != nil {
		return err
	}
	s.mu.Lock()
	s.detectRetry[id] = true
	s.mu.Unlock()
	s.pokeDetect()
	return nil
}

// ComskipDetector runs the comskip binary (at low CPU priority where nice
// exists).
type ComskipDetector struct {
	Path string
	// INI is the comskip.ini to use; empty: DataDir/comskip.ini, written
	// with DefaultComskipINI if it doesn't exist.
	INI     string
	DataDir string
}

// Detect runs comskip on playlist with its output in workDir and parses the
// EDL. Comskip exits 0 when it found breaks and 1 when it found none.
func (d ComskipDetector) Detect(ctx context.Context, playlist, workDir string) ([]store.Commercial, error) {
	ini, err := d.ini()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDetectorSetup, err)
	}
	if _, err := exec.LookPath(d.Path); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDetectorSetup, err)
	}
	args := []string{"--ini=" + ini, "--output=" + workDir, playlist}
	name := d.Path
	if runtime.GOOS != "windows" {
		if nice, err := exec.LookPath("nice"); err == nil {
			name, args = nice, append([]string{"-n", "19", d.Path}, args...)
		}
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = 5 * time.Second
	var out tailBuffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err = cmd.Run()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	var exit *exec.ExitError
	isExit := errors.As(err, &exit)
	noneFound := isExit && exit.ExitCode() == 1
	if err != nil && !noneFound {
		if !isExit || exit.ExitCode() == 126 || exit.ExitCode() == 127 {
			// Couldn't run at all (missing library, not executable).
			return nil, fmt.Errorf("%w: comskip: %v: %s", ErrDetectorSetup, err, strings.TrimSpace(out.String()))
		}
		return nil, fmt.Errorf("comskip: %w: %s", err, strings.TrimSpace(out.String()))
	}
	edls, _ := filepath.Glob(filepath.Join(workDir, "*.edl"))
	if len(edls) == 0 {
		// It ran but wrote no EDL: the ini has output_edl off.
		return nil, fmt.Errorf("%w: comskip wrote no .edl (output_edl=1 in the ini?): %s", ErrDetectorSetup, strings.TrimSpace(out.String()))
	}
	f, err := os.Open(edls[0])
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return ParseEDL(f)
}

func (d ComskipDetector) ini() (string, error) {
	if d.INI != "" {
		return d.INI, nil
	}
	path := filepath.Join(d.DataDir, "comskip.ini")
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	if err := os.WriteFile(path, []byte(DefaultComskipINI), 0o644); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	return path, nil
}

// tailBuffer keeps the last 2 KB written to it (for error messages).
type tailBuffer struct{ b []byte }

func (t *tailBuffer) Write(p []byte) (int, error) {
	const max = 2048
	t.b = append(t.b, p...)
	if len(t.b) > max {
		t.b = append([]byte(nil), t.b[len(t.b)-max:]...)
	}
	return len(p), nil
}

func (t *tailBuffer) String() string { return string(t.b) }

// pokeDetect wakes the detection worker (new work may be ready).
func (s *Service) pokeDetect() {
	if s.deps.Detector == nil {
		return
	}
	select {
	case s.detectPoke <- struct{}{}:
	default:
	}
}

// detectWorker runs detection one recording at a time, newest first, on
// ready recordings it hasn't run on. A recording that failed in this process
// isn't tried again until restart; a setup error stops the worker.
func (s *Service) detectWorker() {
	defer s.wg.Done()
	tried := map[int64]bool{}
	for {
		s.mu.Lock()
		for id := range s.detectRetry {
			delete(tried, id)
		}
		clear(s.detectRetry)
		s.mu.Unlock()
		id, ok := s.nextDetect(tried)
		if !ok {
			select {
			case <-s.ctx.Done():
				return
			case <-s.detectPoke:
			case <-time.After(detectIdlePoll):
			}
			continue
		}
		tried[id] = true
		if err := s.detect(id); errors.Is(err, ErrDetectorSetup) {
			log.Printf("dvr: commercial detection off until restart: %v", err)
			s.mu.Lock()
			s.detectOff = true
			s.mu.Unlock()
			return
		}
		if s.ctx.Err() != nil {
			return
		}
	}
}

// detectIdlePoll: an idle worker also looks for work this often (a missed
// poke or a transient store error doesn't stall the backlog).
const detectIdlePoll = 10 * time.Minute

func (s *Service) nextDetect(tried map[int64]bool) (int64, bool) {
	ids, err := s.deps.Store.RecordingsNeedingCommercials(detectBatch + len(tried))
	if err != nil {
		log.Printf("dvr: commercial detection: %v", err)
		return 0, false
	}
	for _, id := range ids {
		if !tried[id] {
			return id, true
		}
	}
	return 0, false
}

// detect runs the detector on one ready recording and stores the cleaned
// breaks. A failure, delete or shutdown stores nothing (a restart runs it
// again); it returns the detector's error.
func (s *Service) detect(id int64) error {
	r, err := s.deps.Store.RecordingByID(id)
	if err != nil || r.State != store.RecReady || r.CommercialsDetected || r.Dir == "" {
		return nil
	}
	dur := vodDuration(r)
	ctx, cancel := context.WithTimeout(s.ctx, detectTimeout(dur))
	defer cancel()
	s.mu.Lock()
	if s.deleting[id] || s.stopped {
		s.mu.Unlock()
		return nil
	}
	s.detecting[id] = cancel
	s.mu.Unlock()

	playlist := filepath.Join(PlaylistDir(r), MasterName)
	work := filepath.Join(r.Dir, detectWorkDir)
	var segs []store.Commercial
	if _, err = os.Stat(playlist); err == nil {
		_ = os.RemoveAll(work)
		// Mkdir, not MkdirAll: if a delete removed the recording meanwhile,
		// don't bring its folder back.
		if err = os.Mkdir(work, 0o755); err == nil {
			began := time.Now()
			segs, err = s.deps.Detector.Detect(ctx, playlist, work)
			if err == nil {
				log.Printf("dvr: recording %d: %d commercial breaks found in %s", id, len(segs), time.Since(began).Round(time.Second))
			}
		}
		_ = os.RemoveAll(work)
	}
	s.mu.Lock()
	delete(s.detecting, id)
	s.mu.Unlock()
	if s.ctx.Err() != nil || errors.Is(ctx.Err(), context.Canceled) {
		return nil // shutting down, or deleted
	}
	if err != nil {
		if !errors.Is(err, ErrDetectorSetup) {
			log.Printf("dvr: recording %d: commercial detection failed: %v", id, err)
		}
		return err
	}
	if err := s.deps.Store.SetRecordingCommercials(id, CleanSegments(segs, dur)); err != nil {
		log.Printf("dvr: recording %d: store commercials: %v", id, err)
	}
	return nil
}

// vodDuration is a ready recording's playback length in seconds (from its
// video playlist, else the stored whole seconds).
func vodDuration(r store.Recording) float64 {
	if pl, err := os.ReadFile(filepath.Join(PlaylistDir(r), vodLayout.TopName()+".m3u8")); err == nil {
		if d := playlistDuration(pl); d > 0 {
			return d.Seconds()
		}
	}
	return float64(r.DurationSec)
}

// detectTimeout bounds one run so a stuck comskip can't hold the worker:
// three times the recording's length, plus 15 minutes.
func detectTimeout(durSec float64) time.Duration {
	return 3*time.Duration(durSec*float64(time.Second)) + 15*time.Minute
}

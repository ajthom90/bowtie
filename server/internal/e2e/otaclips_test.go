//go:build ffmpeg

package e2e

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ajthom90/bowtie/server/internal/hdhr/hdhrfake/scenario"
)

// otaFor is BOWTIE_OTA_FOR (default 10m), the per-clip cap.
func otaFor(t *testing.T) time.Duration {
	t.Helper()
	if v := os.Getenv("BOWTIE_OTA_FOR"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	return 10 * time.Minute
}

// TestOTAClips plays real over-the-air captures through the production
// layout. The clips are never committed: BOWTIE_OTA_CLIPS names their
// directory (skipped when unset), BOWTIE_OTA_SIGNAL_DIR the capture's tuner
// logs (ch<N>.signal.tsv); BOWTIE_OTA_CLIP_FILES picks some (comma-separated
// base names); BOWTIE_OTA_FOR caps each run. A timestamp re-base more than
// 10 s from a logged signal dip is a false re-base (an ad break, a splice)
// and fails the clip.
func TestOTAClips(t *testing.T) {
	dir := os.Getenv("BOWTIE_OTA_CLIPS")
	if dir == "" {
		t.Skip("BOWTIE_OTA_CLIPS not set")
	}
	if os.Getenv("FF51_LOGDIR") == "" {
		t.Fatal("FF51_LOGDIR not set: re-bases can't be judged without the FFmpeg log")
	}
	maxFor := otaFor(t)
	var clips []string
	if v := os.Getenv("BOWTIE_OTA_CLIP_FILES"); v != "" {
		for _, n := range strings.Split(v, ",") {
			clips = append(clips, filepath.Join(dir, strings.TrimSpace(n)))
		}
	} else {
		clips, _ = filepath.Glob(filepath.Join(dir, "*.ts"))
	}
	if len(clips) == 0 {
		t.Fatalf("no clips in %s", dir)
	}
	for _, path := range clips {
		path := path
		t.Run(filepath.Base(path), func(t *testing.T) {
			clip, err := parseClipName(filepath.Base(path))
			if err != nil {
				t.Fatal(err)
			}
			signal := signalLogFor(t, clip)
			// Leave 20 s for setup: the fake's timeline starts before the
			// viewer, and a clip that loops would re-base at the seam.
			d := clipDuration(t, path) - 20*time.Second
			if d > maxFor {
				d = maxFor
			}
			if d < 30*time.Second {
				t.Skipf("clip shorter than 50 s")
			}
			sc, err := scenario.Parse([]byte(fmt.Sprintf(`name: ota-%s
source: %q
duration: %s
bowtie: {layout: production}
expect:
  sessionSurvives: true
  sequenceMonotonic: true
  maxFfmpegRestarts: 0
  maxTimelineExcessSeconds: 2
  maxAudioHoleMs: 50
  maxAVStartSkewMs: 100
  pairedAfterEachFault: true
  minSegmentsAfterFault: 5
  maxTailGapSeconds: 12
  noFetchErrors: true
`, strings.TrimSuffix(filepath.Base(path), ".ts"), path, d)))
			if err != nil {
				t.Fatal(err)
			}
			sourceStart := runScenario(t, sc)
			rebases, err := rebasesSince(sourceStart, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			bad := falseRebases(signal, clip, rebases)
			for _, rb := range rebases {
				verdict := "true (near a signal dip)"
				for _, b := range bad {
					if b == rb {
						verdict = "FALSE"
					}
				}
				t.Logf("clip re-base at +%v: %s: %s", (clip.Offset + rb.At).Round(time.Second), verdict, rb.Line)
			}
			if len(bad) > 0 {
				t.Errorf("%d false re-base(s) of %d: none within ±%v of a signal dip", len(bad), len(rebases), rebaseDipWindow)
			}
		})
	}
}

// TestOTAClipsWithFaults is the caption row of the Task 6 gate: the
// faults-production scenario (its timeline and expectations, loaded from the
// file) played from a real clip with captions (BOWTIE_OTA_FAULT_CLIP; skipped
// when unset). The clip adds the caption rendition, and cues must keep
// arriving after every fault and stay within their video segment.
func TestOTAClipsWithFaults(t *testing.T) {
	path := os.Getenv("BOWTIE_OTA_FAULT_CLIP")
	if path == "" {
		t.Skip("BOWTIE_OTA_FAULT_CLIP not set")
	}
	sc, err := scenario.Load("testdata/scenarios/faults-production.yaml")
	if err != nil {
		t.Fatal(err)
	}
	// 20 s for setup, as in TestOTAClips: a clip that loops re-bases at the seam.
	if want := sc.Duration + 20*time.Second; clipDuration(t, path) < want {
		t.Fatalf("%s: want a clip of at least %v", path, want)
	}
	sc.Name = "ota-faults-" + strings.TrimSuffix(filepath.Base(path), ".ts")
	sc.Source = path
	timed := true
	renditions := 1 // the caption rendition
	if sc.Expect.MinRenditions != nil {
		renditions += *sc.Expect.MinRenditions
	}
	sc.Expect.CaptionsTimed = &timed
	sc.Expect.MinRenditions = &renditions
	runScenario(t, sc)
}

// clipDuration is the clip's container duration (ffprobe, 30 s deadline).
func clipDuration(t *testing.T, path string) time.Duration {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, ffprobePath(), "-v", "error", "-show_entries", "format=duration", "-of", "csv=p=0", path).Output()
	if err != nil {
		t.Fatalf("ffprobe %s: %v", path, err)
	}
	sec, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil {
		t.Fatalf("duration of %s: %q", path, out)
	}
	return time.Duration(sec * float64(time.Second))
}

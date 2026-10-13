//go:build ffmpeg

package e2e

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// rebaseDipWindow is how close to a logged signal dip a re-base must be to
// count as true (the TSVs sample the tuner every 10 s).
const rebaseDipWindow = 10 * time.Second

// otaClip is a clip file named ch<N>.ts, or ch<N>@<seconds>.ts for a piece
// cut that many seconds into the capture.
type otaClip struct {
	Channel string
	Offset  time.Duration
}

func parseClipName(base string) (otaClip, error) {
	name := strings.TrimSuffix(base, ".ts")
	if !strings.HasPrefix(name, "ch") || name == base {
		return otaClip{}, fmt.Errorf("clip %q: want ch<N>.ts or ch<N>@<seconds>.ts", base)
	}
	ch, off, cut := strings.Cut(strings.TrimPrefix(name, "ch"), "@")
	c := otaClip{Channel: ch}
	if cut {
		sec, err := strconv.ParseFloat(off, 64)
		if err != nil {
			return otaClip{}, fmt.Errorf("clip %q: offset %q", base, off)
		}
		c.Offset = time.Duration(sec * float64(time.Second))
	}
	return c, nil
}

// signalLog is a capture's tuner log, ch<N>.signal.tsv: one row per poll,
// "unix-seconds  tuner  strength%  quality%  symbol-quality%  rate". The
// first row is the capture's start.
type signalLog struct {
	Start time.Time
	Dips  []time.Time // rows with symbol quality < 100 or quality 0
}

func readSignalLog(path string) (signalLog, error) {
	f, err := os.Open(path)
	if err != nil {
		return signalLog{}, err
	}
	defer func() { _ = f.Close() }()
	var l signalLog
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		c := strings.Split(strings.TrimSpace(sc.Text()), "\t")
		if len(c) < 5 {
			continue
		}
		sec, err1 := strconv.ParseInt(c[0], 10, 64)
		quality, err2 := strconv.Atoi(c[3])
		symbol, err3 := strconv.Atoi(c[4])
		if err1 != nil || err2 != nil || err3 != nil {
			continue
		}
		at := time.Unix(sec, 0)
		if l.Start.IsZero() {
			l.Start = at
		}
		if symbol < 100 || quality == 0 {
			l.Dips = append(l.Dips, at)
		}
	}
	if l.Start.IsZero() {
		return l, fmt.Errorf("%s: no rows", path)
	}
	return l, sc.Err()
}

// falseRebases returns the re-bases (offsets from the clip piece's start)
// that are not within rebaseDipWindow of a dip.
func falseRebases(l signalLog, clip otaClip, rebases []rebase) []rebase {
	var out []rebase
	for _, rb := range rebases {
		at := l.Start.Add(clip.Offset + rb.At)
		ok := false
		for _, d := range l.Dips {
			if diff := at.Sub(d); diff >= -rebaseDipWindow && diff <= rebaseDipWindow {
				ok = true
				break
			}
		}
		if !ok {
			out = append(out, rb)
		}
	}
	return out
}

// signalLogFor finds a clip's tuner log in BOWTIE_OTA_SIGNAL_DIR.
func signalLogFor(t testing.TB, clip otaClip) signalLog {
	t.Helper()
	dir := os.Getenv("BOWTIE_OTA_SIGNAL_DIR")
	if dir == "" {
		t.Fatal("BOWTIE_OTA_SIGNAL_DIR not set: re-bases can't be judged without the capture's signal log")
	}
	l, err := readSignalLog(filepath.Join(dir, "ch"+clip.Channel+".signal.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestRebasesJudgedAgainstSignalDips(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BOWTIE_OTA_SIGNAL_DIR", dir)
	tsv := "1791773101\ttuner1\t49\t0\t0\t0\n" + // tuning: a dip at the start
		"1791773111\ttuner1\t57\t84\t100\t1508672\n" +
		"1791773656\ttuner1\t57\t83\t16\t903904\n" + // symbol errors 555 s in
		"1791773666\ttuner1\t58\t80\t100\t1095008\n"
	if err := os.WriteFile(filepath.Join(dir, "ch2.2.signal.tsv"), []byte(tsv), 0o644); err != nil {
		t.Fatal(err)
	}
	clip, err := parseClipName("ch2.2@500.ts")
	if err != nil || clip.Channel != "2.2" || clip.Offset != 500*time.Second {
		t.Fatalf("clip %+v %v", clip, err)
	}
	if c, err := parseClipName("ch9.1.ts"); err != nil || c.Channel != "9.1" || c.Offset != 0 {
		t.Fatalf("whole clip %+v %v", c, err)
	}
	if _, err := parseClipName("news.ts"); err == nil {
		t.Fatal("unnamed clip accepted")
	}
	l := signalLogFor(t, clip)
	got := falseRebases(l, clip, []rebase{
		{At: 50 * time.Second, Line: "near the dip (550 s, dip at 555 s)"},
		{At: 120 * time.Second, Line: "ad break (620 s)"},
	})
	if len(got) != 1 || got[0].At != 120*time.Second {
		t.Fatalf("false re-bases %+v, want only the one at +120 s", got)
	}
}

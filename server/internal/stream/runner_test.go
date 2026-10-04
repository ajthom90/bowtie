package stream

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ajthom90/bowtie/server/internal/transcode"
)

// A failing FFmpeg's exit error carries its last stderr lines, so the cause
// reaches the session-start error instead of only "exit status 1".
func TestFFmpegRunnerExitErrorIncludesStderrTail(t *testing.T) {
	script := filepath.Join(t.TempDir(), "ffmpeg")
	body := "#!/bin/sh\necho 'noise line' >&2\necho '[vpp_qsv @ 0x1] Error initializing filter' >&2\necho 'Conversion failed!' >&2\nexit 1\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	r := &FFmpegRunner{Path: script}
	p, err := r.Start(context.Background(), transcode.JobSpec{OutDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-p.Done():
		if err == nil {
			t.Fatal("want exit error")
		}
		msg := err.Error()
		for _, want := range []string{"exit status 1", "Error initializing filter", "Conversion failed!"} {
			if !strings.Contains(msg, want) {
				t.Errorf("error %q missing %q", msg, want)
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatal("process did not exit")
	}
}

// The caption tap reaches FFmpeg on fd 3 and closes there when its source ends.
func TestRunnerFeedsCaptionInputOnFD3(t *testing.T) {
	out := filepath.Join(t.TempDir(), "fd3.out")
	t.Setenv("FD3_OUT", out)
	script, err := filepath.Abs("testdata/fd3cat.sh")
	if err != nil {
		t.Fatal(err)
	}
	r := &FFmpegRunner{Path: script}
	p, err := r.Start(context.Background(), transcode.JobSpec{
		Stdin: strings.NewReader(""), OutDir: t.TempDir(),
		Layout:       transcode.Layout{Captions: true},
		CaptionInput: strings.NewReader("caption-bytes"),
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-p.Done():
		if err != nil {
			t.Fatalf("fake ffmpeg: %v", err)
		}
	case <-time.After(5 * time.Second):
		p.Stop()
		t.Fatal("fd 3 never reached EOF")
	}
	if got, _ := os.ReadFile(out); string(got) != "caption-bytes" {
		t.Fatalf("fd3 got %q", got)
	}
}

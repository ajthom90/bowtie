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

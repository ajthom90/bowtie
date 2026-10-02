package stream

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"

	"github.com/ajthom90/bowtie/server/internal/transcode"
)

// FFmpegRunner starts FFmpeg processes via transcode.Command.
type FFmpegRunner struct {
	Path string // ffmpeg binary path
}

// Start launches FFmpeg for the given job and returns a supervised Process.
func (r *FFmpegRunner) Start(ctx context.Context, spec transcode.JobSpec) (Process, error) {
	path := r.Path
	if path == "" {
		path = "ffmpeg"
	}
	cmd := transcode.Command(ctx, path, spec)
	tail := &stderrTail{max: 3}
	cmd.Stderr = io.MultiWriter(cmd.Stderr, tail)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	p := &cmdProcess{
		cmd:  cmd,
		done: make(chan error, 1),
	}
	go func() {
		err := cmd.Wait()
		if err != nil {
			if last := tail.String(); last != "" {
				err = fmt.Errorf("%w: %s", err, last)
			}
		}
		p.done <- err
	}()
	return p, nil
}

// stderrTail keeps FFmpeg's last few stderr lines so an exit error can say
// why ("exit status 1" alone does not).
type stderrTail struct {
	mu    sync.Mutex
	max   int
	lines []string
	buf   bytes.Buffer
}

func (t *stderrTail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf.Write(p)
	for {
		line, err := t.buf.ReadString('\n')
		if err != nil {
			t.buf.WriteString(line)
			break
		}
		if line = strings.TrimSpace(line); line != "" {
			t.lines = append(t.lines, line)
			if len(t.lines) > t.max {
				t.lines = t.lines[1:]
			}
		}
	}
	return len(p), nil
}

func (t *stderrTail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.Join(t.lines, " | ")
}

type cmdProcess struct {
	cmd  *exec.Cmd
	done chan error
	once sync.Once
}

func (p *cmdProcess) Done() <-chan error { return p.done }

func (p *cmdProcess) Stop() {
	p.once.Do(func() {
		if p.cmd != nil && p.cmd.Process != nil {
			_ = p.cmd.Process.Kill()
		}
	})
}

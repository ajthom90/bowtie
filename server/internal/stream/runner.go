package stream

import (
	"context"
	"os/exec"
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
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	p := &cmdProcess{
		cmd:  cmd,
		done: make(chan error, 1),
	}
	go func() {
		p.done <- cmd.Wait()
	}()
	return p, nil
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

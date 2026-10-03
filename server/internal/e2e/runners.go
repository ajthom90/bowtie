package e2e

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ajthom90/bowtie/server/internal/stream"
	"github.com/ajthom90/bowtie/server/internal/transcode"
)

// CountingRunner counts process starts (initial start + every restart) and
// logs each process's life: when its input ended and how it exited — the
// facts Bowtie itself does not log.
type CountingRunner struct {
	Inner  stream.Runner
	starts atomic.Int64

	mu  sync.Mutex
	log []string
}

func (r *CountingRunner) logf(format string, a ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.log = append(r.log, time.Now().Format("15:04:05.000")+" "+fmt.Sprintf(format, a...))
}

// Log returns the per-process lifecycle log.
func (r *CountingRunner) Log() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.log...)
}

// Start implements stream.Runner.
func (r *CountingRunner) Start(ctx context.Context, spec transcode.JobSpec) (stream.Process, error) {
	n := r.starts.Add(1)
	if spec.Stdin != nil {
		spec.Stdin = &watchReader{r: spec.Stdin, onErr: func(err error) { r.logf("proc %d: input ended: %v", n, err) }}
	}
	p, err := r.Inner.Start(ctx, spec)
	if err != nil {
		r.logf("proc %d: start failed: %v", n, err)
		return nil, err
	}
	r.logf("proc %d: started", n)
	w := &watchProc{inner: p, done: make(chan error, 1)}
	go func() {
		err := <-p.Done()
		r.logf("proc %d: exited: %v", n, err)
		w.done <- err
	}()
	return w, nil
}

// Starts returns how many processes were started.
func (r *CountingRunner) Starts() int { return int(r.starts.Load()) }

type watchReader struct {
	r     io.Reader
	once  sync.Once
	onErr func(error)
}

func (w *watchReader) Read(p []byte) (int, error) {
	n, err := w.r.Read(p)
	if err != nil {
		w.once.Do(func() { w.onErr(err) })
	}
	return n, err
}

type watchProc struct {
	inner stream.Process
	done  chan error
}

func (w *watchProc) Done() <-chan error { return w.done }

func (w *watchProc) Stop() { w.inner.Stop() }

// Gate pauses readers; the zero value is not usable — use NewGate.
type Gate struct {
	mu     sync.Mutex
	paused bool
	resume chan struct{}
}

// NewGate returns an open gate.
func NewGate() *Gate { return &Gate{resume: make(chan struct{})} }

// Pause makes gated reads block until Resume.
func (g *Gate) Pause() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.paused {
		g.paused = true
		g.resume = make(chan struct{})
	}
}

// Resume releases blocked reads.
func (g *Gate) Resume() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.paused {
		g.paused = false
		close(g.resume)
	}
}

func (g *Gate) wait() {
	g.mu.Lock()
	paused, ch := g.paused, g.resume
	g.mu.Unlock()
	if paused {
		<-ch
	}
}

type gatedReader struct {
	r    io.Reader
	gate *Gate
}

func (g *gatedReader) Read(p []byte) (int, error) {
	g.gate.wait()
	return g.r.Read(p)
}

// GatedRunner simulates a transcoder that stops reading its input while the
// gate is paused (the "slow consumer" Bowtie cuts off).
type GatedRunner struct {
	Inner stream.Runner
	Gate  *Gate
}

// Start implements stream.Runner.
func (r *GatedRunner) Start(ctx context.Context, spec transcode.JobSpec) (stream.Process, error) {
	if spec.Stdin != nil {
		spec.Stdin = &gatedReader{r: spec.Stdin, gate: r.Gate}
	}
	return r.Inner.Start(ctx, spec)
}

// StubRunner is a fake FFmpeg: it drains stdin (counting bytes), writes a
// growing HLS playlist every 500ms starting from segment 0 like a fresh
// FFmpeg, and exits when stdin reaches EOF or Stop is called.
type StubRunner struct {
	bytesIn atomic.Int64
}

// BytesIn returns the total bytes read across all processes.
func (r *StubRunner) BytesIn() int64 { return r.bytesIn.Load() }

// Start implements stream.Runner.
func (r *StubRunner) Start(ctx context.Context, spec transcode.JobSpec) (stream.Process, error) {
	p := &stubProc{done: make(chan error, 1), stop: make(chan struct{})}
	if err := writeStubPlaylist(spec.OutDir, 1); err != nil {
		return nil, err
	}
	eof := make(chan struct{})
	if spec.Stdin != nil {
		go func() {
			defer close(eof)
			buf := make([]byte, 64*1024)
			for {
				n, err := spec.Stdin.Read(buf)
				r.bytesIn.Add(int64(n))
				if err != nil {
					return
				}
			}
		}()
	}
	go func() {
		tick := time.NewTicker(500 * time.Millisecond)
		defer tick.Stop()
		n := 1
		for {
			select {
			case <-ctx.Done():
				p.finish(ctx.Err())
				return
			case <-p.stop:
				p.finish(errors.New("stopped"))
				return
			case <-eof:
				p.finish(nil) // like FFmpeg exiting 0 on end of input
				return
			case <-tick.C:
				n++
				_ = writeStubPlaylist(spec.OutDir, n)
			}
		}
	}()
	return p, nil
}

func writeStubPlaylist(dir string, n int) error {
	var b strings.Builder
	b.WriteString("#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:4\n#EXT-X-MEDIA-SEQUENCE:0\n")
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("seg%05d.ts", i)
		if err := os.WriteFile(filepath.Join(dir, name), []byte{0x47, 0x1F, 0xFF, 0x10}, 0o644); err != nil {
			return err
		}
		fmt.Fprintf(&b, "#EXTINF:4.000000,\n%s\n", name)
	}
	tmp := filepath.Join(dir, "live.m3u8.tmp")
	if err := os.WriteFile(tmp, []byte(b.String()), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, "live.m3u8"))
}

type stubProc struct {
	done chan error
	stop chan struct{}
	once sync.Once
}

func (p *stubProc) Done() <-chan error { return p.done }

func (p *stubProc) Stop() { p.once.Do(func() { close(p.stop) }) }

func (p *stubProc) finish(err error) {
	select {
	case p.done <- err:
	default:
	}
}

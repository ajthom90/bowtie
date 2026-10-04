package dvr

import (
	"context"
	"io"

	"github.com/ajthom90/bowtie/server/internal/store"
	"github.com/ajthom90/bowtie/server/internal/stream"
)

// IngestSource records from the shared channel ingest, so a recording and
// live viewers of the same channel use one tuner.
type IngestSource struct {
	Ingest    *stream.IngestManager
	StreamURL func(store.Channel) (string, error)
}

// Open attaches to the channel's ingest.
func (s IngestSource) Open(ctx context.Context, ch store.Channel) (io.ReadCloser, error) {
	url, err := s.StreamURL(ch)
	if err != nil {
		return nil, err
	}
	sub, err := s.Ingest.Attach(ctx, ch.ID, url)
	if err != nil {
		return nil, err
	}
	return subReader{sub}, nil
}

type subReader struct{ sub *stream.IngestSub }

func (r subReader) Read(p []byte) (int, error) { return r.sub.R.Read(p) }
func (r subReader) Close() error               { return r.sub.Close() }

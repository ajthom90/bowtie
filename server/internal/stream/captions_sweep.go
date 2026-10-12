package stream

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ajthom90/bowtie/server/internal/transcode"
)

// sweepCaptions deletes caption segments FFmpeg left behind: .vtt files the
// caption playlist no longer lists that are older than the window plus one
// segment. (FFmpeg 5.1 loses track of them after an append restart: its
// playlist lists "." and delete_segments then fails on the directory.)
// A playlist that was read also keeps <TopName><seq>.vtt for every seq in
// [MediaSequence-listSize, MediaSequence), clamped at 0: one extra window
// below the first listed sequence, so a segment that just slid out is not
// deleted on the next sweep. A read error other than a missing playlist
// deletes nothing. It returns how many files it removed.
func sweepCaptions(dir string, l transcode.Layout, listSize int, now time.Time) int {
	if !l.Captions {
		return 0
	}
	if listSize <= 0 {
		listSize = transcode.DefaultHLSListSize
	}
	keep := map[string]bool{}
	raw, err := os.ReadFile(filepath.Join(dir, l.CaptionPlaylist()))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return 0
	}
	if err == nil {
		p := ParseMediaPlaylist(raw)
		for i, sg := range p.Segments {
			keep[fmt.Sprintf("%s%d.vtt", l.TopName(), p.MediaSequence+int64(i))] = true
			keep[sg.URI] = true
		}
		start := p.MediaSequence - int64(listSize)
		if start < 0 {
			start = 0
		}
		for seq := start; seq < p.MediaSequence; seq++ {
			keep[fmt.Sprintf("%s%d.vtt", l.TopName(), seq)] = true
		}
	}
	maxAge := time.Duration(listSize+1) * hlsSegmentDuration
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".vtt") || keep[name] {
			continue
		}
		info, err := e.Info()
		if err != nil || now.Sub(info.ModTime()) <= maxAge {
			continue
		}
		if os.Remove(filepath.Join(dir, name)) == nil {
			n++
		}
	}
	return n
}

package stream

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ajthom90/bowtie/server/internal/transcode"
)

// hlsSegmentDuration, MediaPlaylist, and ParseMediaPlaylist are Task 9's.
// This branch does not contain that task, so the sweeper carries the same
// definitions. Delete them when Task 9 is in this package: they redeclare.
const hlsSegmentDuration = 4 * time.Second

// MediaPlaylist is an HLS media playlist as FFmpeg writes it, minus the
// tags Bowtie owns (see bowtieOwnedTag).
type MediaPlaylist struct {
	Header        []string // tags before the first segment (EXTM3U, VERSION, TARGETDURATION, …)
	MediaSequence int64
	Segments      []PlaylistSegment
	Trailer       []string // tags after the last segment (#EXT-X-ENDLIST)
}

// PlaylistSegment is one segment: its tags in order (EXTINF included) and URI.
type PlaylistSegment struct {
	Duration float64
	Tags     []string
	URI      string
}

// bowtieOwnedTag reports the timeline tags Bowtie writes itself. FFmpeg's
// are dropped: its PDT jumps at restarts, and 5.1 writes a spurious
// DISCONTINUITY at the top of a continued playlist and no DISCONTINUITY-SEQUENCE.
func bowtieOwnedTag(line string) bool {
	return line == "#EXT-X-DISCONTINUITY" ||
		strings.HasPrefix(line, "#EXT-X-PROGRAM-DATE-TIME:") ||
		strings.HasPrefix(line, "#EXT-X-DISCONTINUITY-SEQUENCE:")
}

// ParseMediaPlaylist parses raw, dropping the tags Bowtie owns.
func ParseMediaPlaylist(raw []byte) MediaPlaylist {
	var p MediaPlaylist
	var tags []string
	dur := 0.0
	inHeader := true
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "":
		case strings.HasPrefix(line, "#EXT-X-MEDIA-SEQUENCE:"):
			p.MediaSequence, _ = strconv.ParseInt(strings.TrimPrefix(line, "#EXT-X-MEDIA-SEQUENCE:"), 10, 64)
		case bowtieOwnedTag(line):
		case line == "#EXT-X-ENDLIST":
			p.Trailer = append(p.Trailer, line)
		case strings.HasPrefix(line, "#EXTINF:"):
			inHeader = false
			v := strings.TrimPrefix(line, "#EXTINF:")
			if i := strings.IndexByte(v, ','); i >= 0 {
				v = v[:i]
			}
			dur, _ = strconv.ParseFloat(v, 64)
			tags = append(tags, line)
		case strings.HasPrefix(line, "#"):
			if inHeader {
				p.Header = append(p.Header, line)
			} else {
				tags = append(tags, line)
			}
		default:
			p.Segments = append(p.Segments, PlaylistSegment{Duration: dur, Tags: tags, URI: line})
			tags, dur = nil, 0
		}
	}
	return p
}

// sweepCaptions deletes caption segments FFmpeg left behind: .vtt files the
// caption playlist no longer lists that are older than the window plus one
// segment. (FFmpeg 5.1 loses track of them after an append restart: its
// playlist lists "." and delete_segments then fails on the directory.)
// It returns how many files it removed.
func sweepCaptions(dir string, l transcode.Layout, listSize int, now time.Time) int {
	if !l.Captions {
		return 0
	}
	if listSize <= 0 {
		listSize = transcode.DefaultHLSListSize
	}
	keep := map[string]bool{}
	if raw, err := os.ReadFile(filepath.Join(dir, l.CaptionPlaylist())); err == nil {
		p := ParseMediaPlaylist(raw)
		for i, sg := range p.Segments {
			keep[fmt.Sprintf("%s%d.vtt", l.TopName(), p.MediaSequence+int64(i))] = true
			keep[sg.URI] = true
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

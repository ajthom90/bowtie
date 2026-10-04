package api

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

// repairCaptionPlaylist works around FFmpeg 5.1's WebVTT append bug (trac
// #11208): when a restarted FFmpeg continues a caption playlist with
// append_list, the old entries come back as "." instead of file names, no
// #EXT-X-DISCONTINUITY marks the restart, and a graceful stop writes
// #EXT-X-ENDLIST (players would stop loading captions).
//
// Caption segments are named <rung><sequence>.vtt and share sequence numbers
// with the rung's video segments, so entries are rebuilt from the media
// sequence and discontinuities are copied from the video playlist. A healthy
// playlist comes back unchanged.
func repairCaptionPlaylist(vtt, video []byte, rung string) []byte {
	disc := discontinuitySequences(video)
	var out bytes.Buffer
	seq, seqKnown := int64(0), false
	pendingDisc := false
	changed := false
	for _, line := range strings.Split(string(vtt), "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "#EXT-X-ENDLIST":
			changed = true
			continue
		case strings.HasPrefix(trimmed, "#EXT-X-MEDIA-SEQUENCE:"):
			if n, err := strconv.ParseInt(strings.TrimPrefix(trimmed, "#EXT-X-MEDIA-SEQUENCE:"), 10, 64); err == nil {
				seq, seqKnown = n, true
			}
		case trimmed == "#EXT-X-DISCONTINUITY":
			pendingDisc = true
		case strings.HasPrefix(trimmed, "#EXTINF") && seqKnown && disc[seq] && !pendingDisc:
			// The video restarted here; the caption playlist must say so too.
			out.WriteString("#EXT-X-DISCONTINUITY\n")
			changed = true
		case trimmed != "" && !strings.HasPrefix(trimmed, "#"):
			want := fmt.Sprintf("%s%d.vtt", rung, seq)
			if seqKnown && trimmed != want && !captionNameRe.MatchString(trimmed) {
				line = want
				changed = true
			}
			seq++
			pendingDisc = false
		}
		out.WriteString(line)
		out.WriteByte('\n')
	}
	if !changed {
		return vtt
	}
	return bytes.TrimSuffix(out.Bytes(), []byte("\n"))
}

// discontinuitySequences returns the media sequence numbers of the segments a
// playlist marks with #EXT-X-DISCONTINUITY.
func discontinuitySequences(playlist []byte) map[int64]bool {
	out := map[int64]bool{}
	var seq int64
	pending := false
	for _, line := range strings.Split(string(playlist), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "#EXT-X-MEDIA-SEQUENCE:"):
			if n, err := strconv.ParseInt(strings.TrimPrefix(line, "#EXT-X-MEDIA-SEQUENCE:"), 10, 64); err == nil {
				seq = n
			}
		case line == "#EXT-X-DISCONTINUITY":
			pending = true
		case line != "" && !strings.HasPrefix(line, "#"):
			if pending {
				out[seq] = true
			}
			pending = false
			seq++
		}
	}
	return out
}

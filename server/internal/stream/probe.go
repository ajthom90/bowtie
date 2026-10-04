package stream

import (
	"bytes"
	"strings"

	"github.com/ajthom90/bowtie/server/internal/transcode"
)

// ProgramInfo is what a session needs to know about a channel's broadcast.
type ProgramInfo struct {
	Audio        []transcode.AudioTrack
	SourceHeight int    // MPEG-2 vertical size; 0 = unknown
	VideoPID     uint16 // 0 = none
	VideoMPEG2   bool   // video is MPEG-2 (the only kind whose height is parsed)
}

// parsePMT reads a single-packet PMT (ATSC single-program): audio streams in
// PMT order (FFmpeg's 0:a:N) and the first video PID.
func parsePMT(pkt []byte) ProgramInfo {
	var info ProgramInfo
	if len(pkt) < tsPacketSize || pkt[0] != tsSyncByte || pkt[1]&0x40 == 0 {
		return info
	}
	i := 4
	if afc := (pkt[3] >> 4) & 0x3; afc == 2 || afc == 3 {
		i += 1 + int(pkt[4])
	}
	if i >= len(pkt) {
		return info
	}
	i += 1 + int(pkt[i]) // pointer_field
	if i+12 > len(pkt) || pkt[i] != 0x02 {
		return info
	}
	end := min(i+3+(int(pkt[i+1]&0x0F)<<8|int(pkt[i+2]))-4, len(pkt))
	j := i + 12 + (int(pkt[i+10]&0x0F)<<8 | int(pkt[i+11]))
	for j+5 <= end {
		st := pkt[j]
		pid := uint16(pkt[j+1]&0x1F)<<8 | uint16(pkt[j+2])
		n := int(pkt[j+3]&0x0F)<<8 | int(pkt[j+4])
		desc := pkt[j+5 : min(j+5+n, end)]
		switch {
		case isVideoStreamType(st):
			if info.VideoPID == 0 {
				info.VideoPID = pid
				info.VideoMPEG2 = st == 0x01 || st == 0x02
			}
		case isAudioStreamType(st):
			t := audioTrackFrom(desc)
			t.AC3 = st == 0x81
			info.Audio = append(info.Audio, t)
		}
		j += 5 + n
	}
	return info
}

func isVideoStreamType(st byte) bool { return st == 0x01 || st == 0x02 || st == 0x1B || st == 0x24 }

func isAudioStreamType(st byte) bool {
	switch st {
	case 0x03, 0x04, 0x0F, 0x11, 0x81, 0x87:
		return true
	}
	return false
}

// audioTrackFrom reads ISO_639_language_descriptor (0x0A); audio_type 0x03 is
// visual-impaired commentary.
func audioTrackFrom(desc []byte) transcode.AudioTrack {
	for k := 0; k+2 <= len(desc); {
		tag, l := desc[k], int(desc[k+1])
		body := desc[k+2 : min(k+2+l, len(desc))]
		if tag == 0x0A && len(body) >= 4 {
			return transcode.AudioTrack{Lang: strings.ToLower(string(body[:3])), Described: body[3] == 0x03}
		}
		k += 2 + l
	}
	return transcode.AudioTrack{}
}

var seqHeaderCode = []byte{0, 0, 1, 0xB3}

// sequenceHeaderHeight returns the 12-bit vertical_size of the first MPEG-2
// sequence header in payload, or 0.
func sequenceHeaderHeight(payload []byte) int {
	i := bytes.Index(payload, seqHeaderCode)
	if i < 0 || i+7 > len(payload) {
		return 0
	}
	b := payload[i+4:]
	return int(b[1]&0x0F)<<8 | int(b[2])
}

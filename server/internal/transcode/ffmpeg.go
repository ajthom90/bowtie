package transcode

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"os/exec"
	"path/filepath"
	"strings"
)

// JobSpec describes a single FFmpeg HLS transcode job.
type JobSpec struct {
	InputURL string
	OutDir   string
	D        Decision
	// HLSListSize is the -hls_list_size value (DVR window in segments).
	// Zero means the historical default of 30 (~2 min at 4s segments).
	HLSListSize int
	// Stdin, when non-nil, feeds MPEG-TS via pipe:0 instead of InputURL.
	// BuildArgs emits -fflags +discardcorrupt -i pipe:0; Command wires cmd.Stdin.
	// Used by the per-channel ingest fan-out so FFmpeg does not dial the device.
	Stdin io.Reader
	// Append marks a restart into an existing session dir: FFmpeg continues the
	// playlist's numbering and marks a discontinuity instead of starting over at
	// seg00000 (which freezes players). Verified on FFmpeg 5.1.9 and 8.0.1.
	Append bool
	// Layout is the session's HLS output (rungs, audio renditions, captions).
	// The zero Layout is one rung from D.Profile plus one AAC track.
	Layout Layout
	// CaptionInput is fed to FFmpeg on fd 3 by the runner when Layout.Captions
	// is set: a second ingest subscriber of the same channel.
	CaptionInput io.Reader
}

// DefaultHLSListSize is used when JobSpec.HLSListSize is 0 (legacy / unset).
const DefaultHLSListSize = 30

// BuildArgs returns the full FFmpeg argv for s (excluding the binary path).
// Argument order is part of the contract; see plan Task 13.
func BuildArgs(s JobSpec) []string {
	// omit_endlist: FFmpeg exits gracefully when Bowtie closes its input (a
	// restart Bowtie triggers); its trailer must not tell players the stream
	// is over, because the restarted process continues the same playlist.
	hlsFlags := "delete_segments+temp_file+omit_endlist"
	if s.Append {
		hlsFlags += "+append_list+discont_start"
	}
	args := []string{"-hide_banner", "-loglevel", "warning", "-nostats"}

	args = append(args, inputHWAccel(s.D.Backend)...)
	if s.Stdin != nil {
		// Pipe input from ingest fan-out; discardcorrupt hardens dirty ATSC TS.
		args = append(args, "-fflags", "+discardcorrupt", "-i", "pipe:0")
	} else {
		args = append(args, "-i", s.InputURL)
	}

	l := s.Layout
	if l.Captions {
		// Same TS again on fd 3, decoded in software only for CEA-608.
		// Field 1 carries CC1; "auto" can lock onto field 2 (XDS/program info).
		args = append(args, "-data_field", "first", "-f", "lavfi", "-i", "movie='pipe\\:3'[out0+subcc]")
	}
	rungs := l.Rungs
	if len(rungs) == 0 {
		rungs = []Rung{{Height: s.D.Profile.Height, VideoKbps: s.D.Profile.VideoKbps}}
	}
	args = append(args, "-filter_complex", videoGraph(s.D.Backend, rungs))
	for i := range rungs {
		args = append(args, "-map", fmt.Sprintf("[v%d]", i))
	}
	aac := l.AACTracks()
	for i := range aac {
		args = append(args, "-map", fmt.Sprintf("0:a:%d", i))
	}
	for _, i := range l.AC3Indexes() {
		args = append(args, "-map", fmt.Sprintf("0:a:%d", i))
	}
	if l.Captions {
		args = append(args, "-map", "1:s:0")
	}

	args = append(args, "-c:v", s.D.VideoEncoder)
	if s.D.VideoCodec != "hevc" {
		// Pinned so the master playlist's CODECS (avc1.640029) is true.
		args = append(args, "-profile:v", "high", "-level", "4.1")
	}
	for i, r := range rungs {
		args = append(args,
			fmt.Sprintf("-b:v:%d", i), fmt.Sprintf("%dk", r.VideoKbps),
			fmt.Sprintf("-maxrate:v:%d", i), fmt.Sprintf("%dk", r.VideoKbps*12/10),
			fmt.Sprintf("-bufsize:v:%d", i), fmt.Sprintf("%dk", r.VideoKbps*2),
		)
	}
	args = append(args, "-g", "120", "-force_key_frames", "expr:gte(t,n_forced*4)")
	args = append(args, encoderExtras(s.D)...)
	if len(rungs) > 1 {
		// Rung switches need every segment to start on an IDR frame.
		switch s.D.VideoEncoder {
		case "h264_qsv":
			args = append(args, "-forced_idr", "1")
		case "h264_nvenc":
			args = append(args, "-forced-idr", "1")
		}
	}
	if l.Captions {
		switch s.D.VideoEncoder {
		case "libx264", "h264_qsv", "h264_nvenc":
			// The WebVTT rendition carries captions; in-band A53 would show twice.
			args = append(args, "-a53cc", "0")
		}
	}

	akbps := l.AudioKbps
	if akbps == 0 {
		akbps = s.D.Profile.AudioKbps
	}
	n := 0
	for range aac {
		args = append(args,
			fmt.Sprintf("-c:a:%d", n), "aac",
			fmt.Sprintf("-ac:a:%d", n), "2",
			fmt.Sprintf("-b:a:%d", n), fmt.Sprintf("%dk", akbps),
		)
		n++
	}
	for range l.AC3Indexes() {
		args = append(args, fmt.Sprintf("-c:a:%d", n), "copy")
		n++
	}
	if l.Captions {
		args = append(args, "-c:s", "webvtt")
	}

	listSize := s.HLSListSize
	if listSize == 0 {
		listSize = DefaultHLSListSize
	}

	args = append(args,
		"-f", "hls",
		"-hls_time", "4",
		"-hls_list_size", fmt.Sprintf("%d", listSize),
		"-hls_flags", hlsFlags,
		"-hls_segment_type", "mpegts",
		"-hls_segment_filename", filepath.Join(s.OutDir, "%v_%05d.ts"),
		"-var_stream_map", l.withRungs(rungs).VarStreamMap(),
		filepath.Join(s.OutDir, "%v.m3u8"),
	)
	return args
}

// Command builds an *exec.Cmd for the job with Stdout/Stderr logged under a
// fixed "ffmpeg: " prefix. Separate writers avoid races if both streams write.
// When s.Stdin is set, it is wired to cmd.Stdin for pipe:0 input.
func Command(ctx context.Context, ffmpegPath string, s JobSpec) *exec.Cmd {
	cmd := exec.CommandContext(ctx, ffmpegPath, BuildArgs(s)...)
	cmd.Stdout = &prefixLogWriter{prefix: "ffmpeg: "}
	cmd.Stderr = &prefixLogWriter{prefix: "ffmpeg: "}
	if s.Stdin != nil {
		cmd.Stdin = s.Stdin
	}
	return cmd
}

func inputHWAccel(b Backend) []string {
	switch b {
	case BackendQSV:
		return []string{
			"-init_hw_device", "qsv=hw",
			"-hwaccel", "qsv",
			"-hwaccel_output_format", "qsv",
			"-c:v", "mpeg2_qsv",
		}
	case BackendNVENC:
		return []string{
			"-hwaccel", "cuda",
			"-hwaccel_output_format", "cuda",
		}
	case BackendVAAPI:
		return []string{
			"-init_hw_device", "vaapi=va:/dev/dri/renderD128",
			"-hwaccel", "vaapi",
			"-hwaccel_output_format", "vaapi",
		}
	default:
		// videotoolbox / software: software decode
		return nil
	}
}

// videoGraph decodes/deinterlaces once and scales each rung; labels [v0]….
// One rung keeps the single filter chain proven in production.
func videoGraph(b Backend, rungs []Rung) string {
	deint, scale := filterParts(b)
	if len(rungs) == 1 {
		return fmt.Sprintf("[0:v]%s[v0]", singleChain(b, deint, scale(rungs[0].Height)))
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "[0:v]%s,split=%d", deint, len(rungs))
	for i := range rungs {
		fmt.Fprintf(&sb, "[s%d]", i)
	}
	for i, r := range rungs {
		fmt.Fprintf(&sb, ";[s%d]%s[v%d]", i, scale(r.Height), i)
	}
	return sb.String()
}

func filterParts(b Backend) (string, func(int) string) {
	switch b {
	case BackendQSV:
		// No scale_mode: FFmpeg 5.1 (the image's bookworm build) rejects named
		// values like "hq" and ignores the option entirely under its MSDK.
		// Width is an explicit even, aspect-preserving expression: 5.1's
		// vpp_qsv has no "-1 = keep aspect" and turns w=-1 into a 0-wide
		// frame ("Picture size 0x1088 is invalid").
		return "vpp_qsv=deinterlace=2", func(h int) string { return fmt.Sprintf("vpp_qsv=w=trunc(iw*%d/ih/2)*2:h=%d", h, h) }
	case BackendNVENC:
		return "yadif_cuda=0:-1:0", func(h int) string { return fmt.Sprintf("scale_cuda=-2:%d", h) }
	case BackendVAAPI:
		return "deinterlace_vaapi=rate=frame", func(h int) string { return fmt.Sprintf("scale_vaapi=w=-2:h=%d", h) }
	default:
		// videotoolbox / software
		return "yadif=0:-1:0", func(h int) string { return fmt.Sprintf("scale=-2:%d", h) }
	}
}

func singleChain(b Backend, deint, scale string) string {
	if b == BackendQSV {
		// One vpp_qsv does both, exactly as before ladders existed.
		return "vpp_qsv=deinterlace=2:" + strings.TrimPrefix(scale, "vpp_qsv=")
	}
	return deint + "," + scale
}

func encoderExtras(d Decision) []string {
	switch d.VideoEncoder {
	case "libx264":
		return []string{"-preset", "veryfast"}
	case "h264_qsv":
		return []string{"-preset", "veryfast"}
	case "h264_nvenc":
		return []string{"-preset", "p4"}
	case "h264_videotoolbox":
		// -a53cc 0: VideoToolbox's closed-caption SEI is malformed ("Unexpected
		// end of SEI NAL Unit") and AVPlayer rejects the whole stream with
		// CoreMediaErrorDomain -12971. Seen with FFmpeg 8.0.1 on ATSC input.
		return []string{"-realtime", "1", "-a53cc", "0"}
	case "hevc_videotoolbox":
		return []string{"-realtime", "1"}
	default:
		// vaapi and others: none
		return nil
	}
}

// prefixLogWriter logs each complete line with a fixed prefix. Partial lines
// are buffered across Write calls.
type prefixLogWriter struct {
	prefix string
	buf    bytes.Buffer
}

func (w *prefixLogWriter) Write(p []byte) (int, error) {
	w.buf.Write(p)
	for {
		line, err := w.buf.ReadString('\n')
		if err != nil {
			// put incomplete line back
			w.buf.WriteString(line)
			break
		}
		line = strings.TrimRight(line, "\r\n")
		if line != "" {
			log.Print(w.prefix, line)
		}
	}
	return len(p), nil
}

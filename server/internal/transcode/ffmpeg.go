package transcode

import (
	"bytes"
	"context"
	"fmt"
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
}

// BuildArgs returns the full FFmpeg argv for s (excluding the binary path).
// Argument order is part of the contract; see plan Task 13.
func BuildArgs(s JobSpec) []string {
	args := []string{"-hide_banner", "-loglevel", "warning", "-nostats"}

	args = append(args, inputHWAccel(s.D.Backend)...)
	args = append(args, "-i", s.InputURL)

	h := s.D.Profile.Height
	args = append(args, "-vf", videoFilter(s.D.Backend, h))

	vkbps := s.D.Profile.VideoKbps
	maxrate := vkbps * 12 / 10 // 1.2x
	bufsize := vkbps * 2

	args = append(args,
		"-c:v", s.D.VideoEncoder,
		"-b:v", fmt.Sprintf("%dk", vkbps),
		"-maxrate", fmt.Sprintf("%dk", maxrate),
		"-bufsize", fmt.Sprintf("%dk", bufsize),
		"-g", "120",
		"-force_key_frames", "expr:gte(t,n_forced*4)",
	)
	args = append(args, encoderExtras(s.D)...)

	if s.D.AudioCopy {
		args = append(args, "-c:a", "copy")
	} else {
		args = append(args,
			"-c:a", "aac",
			"-ac", "2",
			"-b:a", fmt.Sprintf("%dk", s.D.Profile.AudioKbps),
		)
	}

	segPattern := filepath.Join(s.OutDir, "seg%05d.ts")
	playlist := filepath.Join(s.OutDir, "live.m3u8")
	args = append(args,
		"-f", "hls",
		"-hls_time", "4",
		"-hls_list_size", "30",
		"-hls_flags", "delete_segments+temp_file",
		"-hls_segment_type", "mpegts",
		"-hls_segment_filename", segPattern,
		playlist,
	)
	return args
}

// Command builds an *exec.Cmd for the job with Stdout/Stderr logged under a
// fixed "ffmpeg: " prefix. Separate writers avoid races if both streams write.
func Command(ctx context.Context, ffmpegPath string, s JobSpec) *exec.Cmd {
	cmd := exec.CommandContext(ctx, ffmpegPath, BuildArgs(s)...)
	cmd.Stdout = &prefixLogWriter{prefix: "ffmpeg: "}
	cmd.Stderr = &prefixLogWriter{prefix: "ffmpeg: "}
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

func videoFilter(b Backend, height int) string {
	switch b {
	case BackendQSV:
		return fmt.Sprintf("vpp_qsv=deinterlace=2:scale_mode=hq:w=-1:h=%d", height)
	case BackendNVENC:
		return fmt.Sprintf("yadif_cuda=0:-1:0,scale_cuda=-2:%d", height)
	case BackendVAAPI:
		return fmt.Sprintf("deinterlace_vaapi=rate=frame,scale_vaapi=w=-2:h=%d", height)
	default:
		// videotoolbox / software
		return fmt.Sprintf("yadif=0:-1:0,scale=-2:%d", height)
	}
}

func encoderExtras(d Decision) []string {
	switch d.VideoEncoder {
	case "libx264":
		return []string{"-preset", "veryfast", "-profile:v", "high"}
	case "h264_qsv":
		return []string{"-preset", "veryfast"}
	case "h264_nvenc":
		return []string{"-preset", "p4"}
	case "h264_videotoolbox":
		return []string{"-realtime", "1", "-profile:v", "high"}
	case "hevc_videotoolbox":
		// profile:v high is h264-only per plan
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

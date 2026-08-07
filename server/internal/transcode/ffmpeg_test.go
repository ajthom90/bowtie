package transcode_test

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ajthom90/bowtie/server/internal/transcode"
)

func TestBuildArgsSoftwareAAC(t *testing.T) {
	out := "/tmp/out"
	s := transcode.JobSpec{
		InputURL: "http://hdhr/auto/v7.1",
		OutDir:   out,
		D: transcode.Decision{
			VideoCodec:   "h264",
			VideoEncoder: "libx264",
			AudioCopy:    false,
			Profile:      transcode.Profile{Name: "low", Height: 480, VideoKbps: 1500, AudioKbps: 96},
			Backend:      transcode.BackendSoftware,
		},
	}
	got := transcode.BuildArgs(s)
	want := []string{
		"-hide_banner", "-loglevel", "warning", "-nostats",
		"-i", "http://hdhr/auto/v7.1",
		"-vf", "yadif=0:-1:0,scale=-2:480",
		"-c:v", "libx264",
		"-b:v", "1500k", "-maxrate", "1800k", "-bufsize", "3000k",
		"-g", "120", "-force_key_frames", "expr:gte(t,n_forced*4)",
		"-preset", "veryfast", "-profile:v", "high",
		"-c:a", "aac", "-ac", "2", "-b:a", "96k",
		"-f", "hls", "-hls_time", "4", "-hls_list_size", "30",
		"-hls_flags", "delete_segments+temp_file",
		"-hls_segment_type", "mpegts",
		"-hls_segment_filename", filepath.Join(out, "seg%05d.ts"),
		filepath.Join(out, "live.m3u8"),
	}
	assertArgs(t, got, want)
}

func TestBuildArgsSoftwareAudioCopy(t *testing.T) {
	out := "/tmp/out"
	s := transcode.JobSpec{
		InputURL: "http://hdhr/auto/v7.1",
		OutDir:   out,
		D: transcode.Decision{
			VideoCodec:   "h264",
			VideoEncoder: "libx264",
			AudioCopy:    true,
			Profile:      transcode.Profile{Name: "high", Height: 720, VideoKbps: 4000, AudioKbps: 160},
			Backend:      transcode.BackendSoftware,
		},
	}
	got := transcode.BuildArgs(s)
	want := []string{
		"-hide_banner", "-loglevel", "warning", "-nostats",
		"-i", "http://hdhr/auto/v7.1",
		"-vf", "yadif=0:-1:0,scale=-2:720",
		"-c:v", "libx264",
		"-b:v", "4000k", "-maxrate", "4800k", "-bufsize", "8000k",
		"-g", "120", "-force_key_frames", "expr:gte(t,n_forced*4)",
		"-preset", "veryfast", "-profile:v", "high",
		"-c:a", "copy",
		"-f", "hls", "-hls_time", "4", "-hls_list_size", "30",
		"-hls_flags", "delete_segments+temp_file",
		"-hls_segment_type", "mpegts",
		"-hls_segment_filename", filepath.Join(out, "seg%05d.ts"),
		filepath.Join(out, "live.m3u8"),
	}
	assertArgs(t, got, want)
}

func TestBuildArgsVideoToolbox(t *testing.T) {
	out := "/tmp/out"
	s := transcode.JobSpec{
		InputURL: "http://hdhr/auto/v7.1",
		OutDir:   out,
		D: transcode.Decision{
			VideoCodec:   "h264",
			VideoEncoder: "h264_videotoolbox",
			AudioCopy:    false,
			Profile:      transcode.Profile{Name: "low", Height: 480, VideoKbps: 1500, AudioKbps: 96},
			Backend:      transcode.BackendVideoToolbox,
		},
	}
	got := transcode.BuildArgs(s)
	want := []string{
		"-hide_banner", "-loglevel", "warning", "-nostats",
		"-i", "http://hdhr/auto/v7.1",
		"-vf", "yadif=0:-1:0,scale=-2:480",
		"-c:v", "h264_videotoolbox",
		"-b:v", "1500k", "-maxrate", "1800k", "-bufsize", "3000k",
		"-g", "120", "-force_key_frames", "expr:gte(t,n_forced*4)",
		"-realtime", "1", "-profile:v", "high",
		"-c:a", "aac", "-ac", "2", "-b:a", "96k",
		"-f", "hls", "-hls_time", "4", "-hls_list_size", "30",
		"-hls_flags", "delete_segments+temp_file",
		"-hls_segment_type", "mpegts",
		"-hls_segment_filename", filepath.Join(out, "seg%05d.ts"),
		filepath.Join(out, "live.m3u8"),
	}
	assertArgs(t, got, want)
}

func TestBuildArgsQSV(t *testing.T) {
	out := "/tmp/out"
	s := transcode.JobSpec{
		InputURL: "http://hdhr/auto/v7.1",
		OutDir:   out,
		D: transcode.Decision{
			VideoCodec:   "h264",
			VideoEncoder: "h264_qsv",
			AudioCopy:    false,
			Profile:      transcode.Profile{Name: "low", Height: 480, VideoKbps: 1500, AudioKbps: 96},
			Backend:      transcode.BackendQSV,
		},
	}
	got := transcode.BuildArgs(s)
	want := []string{
		"-hide_banner", "-loglevel", "warning", "-nostats",
		"-init_hw_device", "qsv=hw", "-hwaccel", "qsv", "-hwaccel_output_format", "qsv", "-c:v", "mpeg2_qsv",
		"-i", "http://hdhr/auto/v7.1",
		"-vf", "vpp_qsv=deinterlace=2:scale_mode=hq:w=-1:h=480",
		"-c:v", "h264_qsv",
		"-b:v", "1500k", "-maxrate", "1800k", "-bufsize", "3000k",
		"-g", "120", "-force_key_frames", "expr:gte(t,n_forced*4)",
		"-preset", "veryfast",
		"-c:a", "aac", "-ac", "2", "-b:a", "96k",
		"-f", "hls", "-hls_time", "4", "-hls_list_size", "30",
		"-hls_flags", "delete_segments+temp_file",
		"-hls_segment_type", "mpegts",
		"-hls_segment_filename", filepath.Join(out, "seg%05d.ts"),
		filepath.Join(out, "live.m3u8"),
	}
	assertArgs(t, got, want)
}

func TestBuildArgsVAAPI(t *testing.T) {
	out := "/tmp/out"
	s := transcode.JobSpec{
		InputURL: "http://hdhr/auto/v7.1",
		OutDir:   out,
		D: transcode.Decision{
			VideoCodec:   "h264",
			VideoEncoder: "h264_vaapi",
			AudioCopy:    false,
			Profile:      transcode.Profile{Name: "medium", Height: 720, VideoKbps: 2500, AudioKbps: 128},
			Backend:      transcode.BackendVAAPI,
		},
	}
	got := transcode.BuildArgs(s)
	want := []string{
		"-hide_banner", "-loglevel", "warning", "-nostats",
		"-init_hw_device", "vaapi=va:/dev/dri/renderD128", "-hwaccel", "vaapi", "-hwaccel_output_format", "vaapi",
		"-i", "http://hdhr/auto/v7.1",
		"-vf", "deinterlace_vaapi=rate=frame,scale_vaapi=w=-2:h=720",
		"-c:v", "h264_vaapi",
		"-b:v", "2500k", "-maxrate", "3000k", "-bufsize", "5000k",
		"-g", "120", "-force_key_frames", "expr:gte(t,n_forced*4)",
		"-c:a", "aac", "-ac", "2", "-b:a", "128k",
		"-f", "hls", "-hls_time", "4", "-hls_list_size", "30",
		"-hls_flags", "delete_segments+temp_file",
		"-hls_segment_type", "mpegts",
		"-hls_segment_filename", filepath.Join(out, "seg%05d.ts"),
		filepath.Join(out, "live.m3u8"),
	}
	assertArgs(t, got, want)
}

func TestBuildArgsNVENC(t *testing.T) {
	out := "/tmp/out"
	s := transcode.JobSpec{
		InputURL: "http://hdhr/auto/v7.1",
		OutDir:   out,
		D: transcode.Decision{
			VideoCodec:   "h264",
			VideoEncoder: "h264_nvenc",
			AudioCopy:    false,
			Profile:      transcode.Profile{Name: "low", Height: 480, VideoKbps: 1500, AudioKbps: 96},
			Backend:      transcode.BackendNVENC,
		},
	}
	got := transcode.BuildArgs(s)
	want := []string{
		"-hide_banner", "-loglevel", "warning", "-nostats",
		"-hwaccel", "cuda", "-hwaccel_output_format", "cuda",
		"-i", "http://hdhr/auto/v7.1",
		"-vf", "yadif_cuda=0:-1:0,scale_cuda=-2:480",
		"-c:v", "h264_nvenc",
		"-b:v", "1500k", "-maxrate", "1800k", "-bufsize", "3000k",
		"-g", "120", "-force_key_frames", "expr:gte(t,n_forced*4)",
		"-preset", "p4",
		"-c:a", "aac", "-ac", "2", "-b:a", "96k",
		"-f", "hls", "-hls_time", "4", "-hls_list_size", "30",
		"-hls_flags", "delete_segments+temp_file",
		"-hls_segment_type", "mpegts",
		"-hls_segment_filename", filepath.Join(out, "seg%05d.ts"),
		filepath.Join(out, "live.m3u8"),
	}
	assertArgs(t, got, want)
}

func TestCommandReturnsCmd(t *testing.T) {
	s := transcode.JobSpec{
		InputURL: "http://example/in",
		OutDir:   "/tmp/out",
		D: transcode.Decision{
			VideoCodec:   "h264",
			VideoEncoder: "libx264",
			Profile:      transcode.Profile{Name: "low", Height: 480, VideoKbps: 1500, AudioKbps: 96},
			Backend:      transcode.BackendSoftware,
		},
	}
	cmd := transcode.Command(context.Background(), "/usr/bin/ffmpeg", s)
	if cmd == nil {
		t.Fatal("Command returned nil")
	}
	if cmd.Path != "/usr/bin/ffmpeg" && (len(cmd.Args) == 0 || cmd.Args[0] != "/usr/bin/ffmpeg") {
		// Path may be resolved; Args[0] is the executable.
		t.Logf("cmd.Path=%q cmd.Args[0]=%q", cmd.Path, cmd.Args[0])
	}
	if len(cmd.Args) < 2 {
		t.Fatalf("cmd.Args too short: %v", cmd.Args)
	}
	// Args[0] is the binary path; remaining match BuildArgs.
	want := append([]string{cmd.Args[0]}, transcode.BuildArgs(s)...)
	if !reflect.DeepEqual(cmd.Args, want) {
		t.Errorf("Command args mismatch\ngot:  %v\nwant: %v", cmd.Args, want)
	}
	if cmd.Stdout == nil || cmd.Stderr == nil {
		t.Error("Command should wire Stdout and Stderr to a prefixed logger")
	}
}

// TestHLSListSizeDefaultZero documents that HLSListSize 0 → 30 (legacy default).
func TestHLSListSizeDefaultZero(t *testing.T) {
	s := transcode.JobSpec{
		InputURL:    "http://hdhr/auto/v7.1",
		OutDir:      "/tmp/out",
		HLSListSize: 0,
		D: transcode.Decision{
			VideoCodec:   "h264",
			VideoEncoder: "libx264",
			AudioCopy:    false,
			Profile:      transcode.Profile{Name: "low", Height: 480, VideoKbps: 1500, AudioKbps: 96},
			Backend:      transcode.BackendSoftware,
		},
	}
	got := transcode.BuildArgs(s)
	if !containsAdjacent(got, "-hls_list_size", "30") {
		t.Fatalf("HLSListSize 0 should emit -hls_list_size 30; args=%v", got)
	}
}

// TestHLSListSize225AcrossBackends is A8: non-default (225 = 15 min) goldens
// across all five backends — full argv must match including list size.
func TestHLSListSize225AcrossBackends(t *testing.T) {
	out := "/tmp/out"
	const listSize = 225

	type caseSpec struct {
		name string
		s    transcode.JobSpec
		want []string
	}
	cases := []caseSpec{
		{
			name: "software",
			s: transcode.JobSpec{
				InputURL: "http://hdhr/auto/v7.1", OutDir: out, HLSListSize: listSize,
				D: transcode.Decision{
					VideoCodec: "h264", VideoEncoder: "libx264", AudioCopy: false,
					Profile: transcode.Profile{Name: "low", Height: 480, VideoKbps: 1500, AudioKbps: 96},
					Backend: transcode.BackendSoftware,
				},
			},
			want: []string{
				"-hide_banner", "-loglevel", "warning", "-nostats",
				"-i", "http://hdhr/auto/v7.1",
				"-vf", "yadif=0:-1:0,scale=-2:480",
				"-c:v", "libx264",
				"-b:v", "1500k", "-maxrate", "1800k", "-bufsize", "3000k",
				"-g", "120", "-force_key_frames", "expr:gte(t,n_forced*4)",
				"-preset", "veryfast", "-profile:v", "high",
				"-c:a", "aac", "-ac", "2", "-b:a", "96k",
				"-f", "hls", "-hls_time", "4", "-hls_list_size", "225",
				"-hls_flags", "delete_segments+temp_file",
				"-hls_segment_type", "mpegts",
				"-hls_segment_filename", filepath.Join(out, "seg%05d.ts"),
				filepath.Join(out, "live.m3u8"),
			},
		},
		{
			name: "videotoolbox",
			s: transcode.JobSpec{
				InputURL: "http://hdhr/auto/v7.1", OutDir: out, HLSListSize: listSize,
				D: transcode.Decision{
					VideoCodec: "h264", VideoEncoder: "h264_videotoolbox", AudioCopy: false,
					Profile: transcode.Profile{Name: "low", Height: 480, VideoKbps: 1500, AudioKbps: 96},
					Backend: transcode.BackendVideoToolbox,
				},
			},
			want: []string{
				"-hide_banner", "-loglevel", "warning", "-nostats",
				"-i", "http://hdhr/auto/v7.1",
				"-vf", "yadif=0:-1:0,scale=-2:480",
				"-c:v", "h264_videotoolbox",
				"-b:v", "1500k", "-maxrate", "1800k", "-bufsize", "3000k",
				"-g", "120", "-force_key_frames", "expr:gte(t,n_forced*4)",
				"-realtime", "1", "-profile:v", "high",
				"-c:a", "aac", "-ac", "2", "-b:a", "96k",
				"-f", "hls", "-hls_time", "4", "-hls_list_size", "225",
				"-hls_flags", "delete_segments+temp_file",
				"-hls_segment_type", "mpegts",
				"-hls_segment_filename", filepath.Join(out, "seg%05d.ts"),
				filepath.Join(out, "live.m3u8"),
			},
		},
		{
			name: "qsv",
			s: transcode.JobSpec{
				InputURL: "http://hdhr/auto/v7.1", OutDir: out, HLSListSize: listSize,
				D: transcode.Decision{
					VideoCodec: "h264", VideoEncoder: "h264_qsv", AudioCopy: false,
					Profile: transcode.Profile{Name: "low", Height: 480, VideoKbps: 1500, AudioKbps: 96},
					Backend: transcode.BackendQSV,
				},
			},
			want: []string{
				"-hide_banner", "-loglevel", "warning", "-nostats",
				"-init_hw_device", "qsv=hw", "-hwaccel", "qsv", "-hwaccel_output_format", "qsv", "-c:v", "mpeg2_qsv",
				"-i", "http://hdhr/auto/v7.1",
				"-vf", "vpp_qsv=deinterlace=2:scale_mode=hq:w=-1:h=480",
				"-c:v", "h264_qsv",
				"-b:v", "1500k", "-maxrate", "1800k", "-bufsize", "3000k",
				"-g", "120", "-force_key_frames", "expr:gte(t,n_forced*4)",
				"-preset", "veryfast",
				"-c:a", "aac", "-ac", "2", "-b:a", "96k",
				"-f", "hls", "-hls_time", "4", "-hls_list_size", "225",
				"-hls_flags", "delete_segments+temp_file",
				"-hls_segment_type", "mpegts",
				"-hls_segment_filename", filepath.Join(out, "seg%05d.ts"),
				filepath.Join(out, "live.m3u8"),
			},
		},
		{
			name: "vaapi",
			s: transcode.JobSpec{
				InputURL: "http://hdhr/auto/v7.1", OutDir: out, HLSListSize: listSize,
				D: transcode.Decision{
					VideoCodec: "h264", VideoEncoder: "h264_vaapi", AudioCopy: false,
					Profile: transcode.Profile{Name: "medium", Height: 720, VideoKbps: 2500, AudioKbps: 128},
					Backend: transcode.BackendVAAPI,
				},
			},
			want: []string{
				"-hide_banner", "-loglevel", "warning", "-nostats",
				"-init_hw_device", "vaapi=va:/dev/dri/renderD128", "-hwaccel", "vaapi", "-hwaccel_output_format", "vaapi",
				"-i", "http://hdhr/auto/v7.1",
				"-vf", "deinterlace_vaapi=rate=frame,scale_vaapi=w=-2:h=720",
				"-c:v", "h264_vaapi",
				"-b:v", "2500k", "-maxrate", "3000k", "-bufsize", "5000k",
				"-g", "120", "-force_key_frames", "expr:gte(t,n_forced*4)",
				"-c:a", "aac", "-ac", "2", "-b:a", "128k",
				"-f", "hls", "-hls_time", "4", "-hls_list_size", "225",
				"-hls_flags", "delete_segments+temp_file",
				"-hls_segment_type", "mpegts",
				"-hls_segment_filename", filepath.Join(out, "seg%05d.ts"),
				filepath.Join(out, "live.m3u8"),
			},
		},
		{
			name: "nvenc",
			s: transcode.JobSpec{
				InputURL: "http://hdhr/auto/v7.1", OutDir: out, HLSListSize: listSize,
				D: transcode.Decision{
					VideoCodec: "h264", VideoEncoder: "h264_nvenc", AudioCopy: false,
					Profile: transcode.Profile{Name: "low", Height: 480, VideoKbps: 1500, AudioKbps: 96},
					Backend: transcode.BackendNVENC,
				},
			},
			want: []string{
				"-hide_banner", "-loglevel", "warning", "-nostats",
				"-hwaccel", "cuda", "-hwaccel_output_format", "cuda",
				"-i", "http://hdhr/auto/v7.1",
				"-vf", "yadif_cuda=0:-1:0,scale_cuda=-2:480",
				"-c:v", "h264_nvenc",
				"-b:v", "1500k", "-maxrate", "1800k", "-bufsize", "3000k",
				"-g", "120", "-force_key_frames", "expr:gte(t,n_forced*4)",
				"-preset", "p4",
				"-c:a", "aac", "-ac", "2", "-b:a", "96k",
				"-f", "hls", "-hls_time", "4", "-hls_list_size", "225",
				"-hls_flags", "delete_segments+temp_file",
				"-hls_segment_type", "mpegts",
				"-hls_segment_filename", filepath.Join(out, "seg%05d.ts"),
				filepath.Join(out, "live.m3u8"),
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertArgs(t, transcode.BuildArgs(tc.s), tc.want)
		})
	}
}

func containsAdjacent(args []string, a, b string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == a && args[i+1] == b {
			return true
		}
	}
	return false
}

func assertArgs(t *testing.T, got, want []string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("BuildArgs mismatch\ngot  (%d): %q\nwant (%d): %q", len(got), got, len(want), want)
		// Diff helper for readability.
		max := len(got)
		if len(want) > max {
			max = len(want)
		}
		for i := 0; i < max; i++ {
			var g, w string
			if i < len(got) {
				g = got[i]
			} else {
				g = "<missing>"
			}
			if i < len(want) {
				w = want[i]
			} else {
				w = "<missing>"
			}
			if g != w {
				t.Errorf("  [%d] got %q want %q", i, g, w)
			}
		}
	}
}

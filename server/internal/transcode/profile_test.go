package transcode_test

import (
	"strings"
	"testing"

	"github.com/ajthom90/bowtie/server/internal/transcode"
)

func TestDefaultProfiles(t *testing.T) {
	ps := transcode.DefaultProfiles()
	if len(ps) != 4 {
		t.Fatalf("len(DefaultProfiles) = %d, want 4", len(ps))
	}
	want := []struct {
		name                 string
		height, vkbps, akbps int
	}{
		{"original", 1080, 8000, 160},
		{"high", 720, 4000, 160},
		{"medium", 720, 2500, 128},
		{"low", 480, 1500, 96},
	}
	for i, w := range want {
		p := ps[i]
		if p.Name != w.name || p.Height != w.height || p.VideoKbps != w.vkbps || p.AudioKbps != w.akbps {
			t.Errorf("[%d] got %+v, want name=%s height=%d videoKbps=%d audioKbps=%d",
				i, p, w.name, w.height, w.vkbps, w.akbps)
		}
	}
}

func TestProfileByName(t *testing.T) {
	ps := transcode.DefaultProfiles()
	p, ok := transcode.ProfileByName(ps, "medium")
	if !ok || p.Name != "medium" {
		t.Fatalf("ProfileByName(medium) = %+v, %v", p, ok)
	}
	_, ok = transcode.ProfileByName(ps, "ultra")
	if ok {
		t.Fatal("ProfileByName(ultra) should be false")
	}
}

func TestNegotiate(t *testing.T) {
	profiles := transcode.DefaultProfiles()
	// Software backend with HEVC available — portable for unit tests.
	hwHEVC := transcode.Capabilities{
		Available: []transcode.Backend{transcode.BackendSoftware},
		HEVC:      map[transcode.Backend]bool{transcode.BackendSoftware: true},
	}
	hwNoHEVC := transcode.Capabilities{
		Available: []transcode.Backend{transcode.BackendSoftware},
		HEVC:      map[transcode.Backend]bool{},
	}
	hwVT := transcode.Capabilities{
		Available: []transcode.Backend{transcode.BackendVideoToolbox, transcode.BackendSoftware},
		HEVC: map[transcode.Backend]bool{
			transcode.BackendVideoToolbox: true,
			transcode.BackendSoftware:     true,
		},
	}

	tests := []struct {
		name           string
		caps           transcode.ClientCaps
		userMaxQuality string
		hw             transcode.Capabilities
		forced         string
		allowHEVC      bool
		wantCodec      string
		wantEncoder    string
		wantAudioCopy  bool
		wantProfile    string
		wantBackend    transcode.Backend
		wantErr        bool
		errContains    string
	}{
		{
			name: "h264-only web client",
			caps: transcode.ClientCaps{
				VideoCodecs: []string{"h264"},
				AudioCodecs: []string{"aac"},
				MaxHeight:   0,
				Profile:     "",
			},
			userMaxQuality: "",
			hw:             hwHEVC,
			forced:         "auto",
			allowHEVC:      true,
			wantCodec:      "h264",
			wantEncoder:    "libx264",
			wantAudioCopy:  false,
			wantProfile:    "original",
			wantBackend:    transcode.BackendSoftware,
		},
		{
			name: "hevc TV client with ac3 + allowHEVC",
			caps: transcode.ClientCaps{
				VideoCodecs: []string{"hevc", "h264"},
				AudioCodecs: []string{"ac3", "aac"},
				MaxHeight:   0,
				Profile:     "",
			},
			userMaxQuality: "",
			hw:             hwHEVC,
			forced:         "auto",
			allowHEVC:      true,
			wantCodec:      "hevc",
			wantEncoder:    "libx265",
			wantAudioCopy:  true,
			wantProfile:    "original",
			wantBackend:    transcode.BackendSoftware,
		},
		{
			name: "user capped medium requesting original",
			caps: transcode.ClientCaps{
				VideoCodecs: []string{"h264"},
				AudioCodecs: []string{"aac"},
				MaxHeight:   0,
				Profile:     "original",
			},
			userMaxQuality: "medium",
			hw:             hwNoHEVC,
			forced:         "auto",
			allowHEVC:      false,
			wantCodec:      "h264",
			wantEncoder:    "libx264",
			wantAudioCopy:  false,
			wantProfile:    "medium",
			wantBackend:    transcode.BackendSoftware,
		},
		{
			name: "MaxHeight 480 clamps to low",
			caps: transcode.ClientCaps{
				VideoCodecs: []string{"h264"},
				AudioCodecs: []string{"aac"},
				MaxHeight:   480,
				Profile:     "original",
			},
			userMaxQuality: "",
			hw:             hwNoHEVC,
			forced:         "auto",
			allowHEVC:      false,
			wantCodec:      "h264",
			wantEncoder:    "libx264",
			wantAudioCopy:  false,
			wantProfile:    "low",
			wantBackend:    transcode.BackendSoftware,
		},
		{
			name: "no common video codec",
			caps: transcode.ClientCaps{
				VideoCodecs: []string{"av1"},
				AudioCodecs: []string{"aac"},
			},
			hw:          hwHEVC,
			forced:      "auto",
			allowHEVC:   true,
			wantErr:     true,
			errContains: "", // any error is fine
		},
		{
			name: "forced backend unavailable",
			caps: transcode.ClientCaps{
				VideoCodecs: []string{"h264"},
				AudioCodecs: []string{"aac"},
			},
			hw:          hwNoHEVC,
			forced:      "nvenc",
			allowHEVC:   false,
			wantErr:     true,
			errContains: "nvenc",
		},
		{
			name: "hevc preferred but backend lacks HEVC falls back to h264",
			caps: transcode.ClientCaps{
				VideoCodecs: []string{"hevc", "h264"},
				AudioCodecs: []string{"aac"},
			},
			hw:            hwNoHEVC,
			forced:        "auto",
			allowHEVC:     true,
			wantCodec:     "h264",
			wantEncoder:   "libx264",
			wantAudioCopy: false,
			wantProfile:   "original",
			wantBackend:   transcode.BackendSoftware,
		},
		{
			name: "videotoolbox hevc encoder name",
			caps: transcode.ClientCaps{
				VideoCodecs: []string{"hevc", "h264"},
				AudioCodecs: []string{"ac3"},
			},
			hw:            hwVT,
			forced:        "videotoolbox",
			allowHEVC:     true,
			wantCodec:     "hevc",
			wantEncoder:   "hevc_videotoolbox",
			wantAudioCopy: true,
			wantProfile:   "original",
			wantBackend:   transcode.BackendVideoToolbox,
		},
		{
			name: "user cap lower than requested",
			caps: transcode.ClientCaps{
				VideoCodecs: []string{"h264"},
				AudioCodecs: []string{"aac"},
				Profile:     "high",
			},
			userMaxQuality: "low",
			hw:             hwNoHEVC,
			forced:         "auto",
			wantCodec:      "h264",
			wantEncoder:    "libx264",
			wantProfile:    "low",
			wantBackend:    transcode.BackendSoftware,
		},
		{
			name: "requested lower than user cap keeps requested",
			caps: transcode.ClientCaps{
				VideoCodecs: []string{"h264"},
				AudioCodecs: []string{"aac"},
				Profile:     "low",
			},
			userMaxQuality: "high",
			hw:             hwNoHEVC,
			forced:         "auto",
			wantCodec:      "h264",
			wantEncoder:    "libx264",
			wantProfile:    "low",
			wantBackend:    transcode.BackendSoftware,
		},
		{
			name: "unknown requested profile",
			caps: transcode.ClientCaps{
				VideoCodecs: []string{"h264"},
				AudioCodecs: []string{"aac"},
				Profile:     "ultra",
			},
			hw:      hwNoHEVC,
			forced:  "auto",
			wantErr: true,
		},
		{
			name: "unknown userMaxQuality",
			caps: transcode.ClientCaps{
				VideoCodecs: []string{"h264"},
				AudioCodecs: []string{"aac"},
				Profile:     "original",
			},
			userMaxQuality: "ultra",
			hw:             hwNoHEVC,
			forced:         "auto",
			wantErr:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, err := transcode.Negotiate(tt.caps, tt.userMaxQuality, tt.hw, tt.forced, tt.allowHEVC, profiles)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got decision %+v", d)
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("error %q does not contain %q", err.Error(), tt.errContains)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if d.VideoCodec != tt.wantCodec {
				t.Errorf("VideoCodec = %q, want %q", d.VideoCodec, tt.wantCodec)
			}
			if d.VideoEncoder != tt.wantEncoder {
				t.Errorf("VideoEncoder = %q, want %q", d.VideoEncoder, tt.wantEncoder)
			}
			if d.AudioCopy != tt.wantAudioCopy {
				t.Errorf("AudioCopy = %v, want %v", d.AudioCopy, tt.wantAudioCopy)
			}
			if d.Profile.Name != tt.wantProfile {
				t.Errorf("Profile.Name = %q, want %q", d.Profile.Name, tt.wantProfile)
			}
			if d.Backend != tt.wantBackend {
				t.Errorf("Backend = %q, want %q", d.Backend, tt.wantBackend)
			}
		})
	}
}

func TestSessionKey(t *testing.T) {
	d := transcode.Decision{
		VideoCodec: "h264",
		AudioCopy:  false,
		Profile:    transcode.Profile{Name: "medium"},
	}
	got := transcode.SessionKey(42, d)
	want := "ch42|h264|medium|aac"
	if got != want {
		t.Errorf("SessionKey = %q, want %q", got, want)
	}

	dCopy := d
	dCopy.AudioCopy = true
	dCopy.VideoCodec = "hevc"
	dCopy.Profile.Name = "original"
	got = transcode.SessionKey(7, dCopy)
	want = "ch7|hevc|original|copy"
	if got != want {
		t.Errorf("SessionKey = %q, want %q", got, want)
	}

	// Stability: same inputs → same key
	if transcode.SessionKey(42, d) != "ch42|h264|medium|aac" {
		t.Error("SessionKey not stable across calls")
	}
}

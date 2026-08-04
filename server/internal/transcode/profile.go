package transcode

import (
	"fmt"
	"slices"
)

// Profile is a quality ladder rung: resolution and target bitrates.
type Profile struct {
	Name      string `json:"name"`
	Height    int    `json:"height"`
	VideoKbps int    `json:"videoKbps"`
	AudioKbps int    `json:"audioKbps"`
}

// DefaultProfiles returns the built-in quality ladder, highest first.
// original=1080p/8000/160, high=720p/4000/160, medium=720p/2500/128, low=480p/1500/96.
func DefaultProfiles() []Profile {
	return []Profile{
		{Name: "original", Height: 1080, VideoKbps: 8000, AudioKbps: 160},
		{Name: "high", Height: 720, VideoKbps: 4000, AudioKbps: 160},
		{Name: "medium", Height: 720, VideoKbps: 2500, AudioKbps: 128},
		{Name: "low", Height: 480, VideoKbps: 1500, AudioKbps: 96},
	}
}

// ProfileByName looks up a profile by name in the given slice.
func ProfileByName(ps []Profile, name string) (Profile, bool) {
	for _, p := range ps {
		if p.Name == name {
			return p, true
		}
	}
	return Profile{}, false
}

// ClientCaps describes what the client can decode and prefers.
type ClientCaps struct {
	VideoCodecs []string `json:"videoCodecs"` // "h264","hevc","av1"
	AudioCodecs []string `json:"audioCodecs"` // "aac","ac3","eac3"
	MaxHeight   int      `json:"maxHeight"`   // 0 = no limit
	Profile     string   `json:"profile"`     // requested quality name; "" = original
}

// Decision is the negotiated encode target for a stream session.
type Decision struct {
	VideoCodec   string  // "h264" or "hevc"
	VideoEncoder string  // FFmpeg encoder name, e.g. "libx264", "h264_videotoolbox"
	AudioCopy    bool    // true = copy ac3; false = transcode to aac
	Profile      Profile // selected quality rung
	Backend      Backend
}

// Negotiate picks backend, video codec/encoder, quality profile, and audio mode
// for a client given hardware capabilities and policy flags.
//
// Errors only for: no usable video codec, unknown requested/capped profile name,
// or hw.Select(forced) failure.
func Negotiate(caps ClientCaps, userMaxQuality string, hw Capabilities, forced string, allowHEVC bool, profiles []Profile) (Decision, error) {
	backend, err := hw.Select(forced)
	if err != nil {
		return Decision{}, err
	}

	videoCodec, videoEncoder, err := pickVideo(caps, hw, backend, allowHEVC)
	if err != nil {
		return Decision{}, err
	}

	profile, err := pickProfile(caps, userMaxQuality, profiles)
	if err != nil {
		return Decision{}, err
	}

	return Decision{
		VideoCodec:   videoCodec,
		VideoEncoder: videoEncoder,
		AudioCopy:    slices.Contains(caps.AudioCodecs, "ac3"),
		Profile:      profile,
		Backend:      backend,
	}, nil
}

// SessionKey builds a stable session identity string for session sharing.
// Format: "ch%d|%s|%s|%s" → channelID, VideoCodec, Profile.Name, "copy"|"aac".
func SessionKey(channelID int64, d Decision) string {
	audio := "aac"
	if d.AudioCopy {
		audio = "copy"
	}
	return fmt.Sprintf("ch%d|%s|%s|%s", channelID, d.VideoCodec, d.Profile.Name, audio)
}

func pickVideo(caps ClientCaps, hw Capabilities, backend Backend, allowHEVC bool) (codec, encoder string, err error) {
	if allowHEVC && slices.Contains(caps.VideoCodecs, "hevc") && hw.HEVC[backend] {
		return "hevc", encoderName(backend, true), nil
	}
	if slices.Contains(caps.VideoCodecs, "h264") {
		return "h264", encoderName(backend, false), nil
	}
	return "", "", fmt.Errorf("no usable video codec: client supports %v", caps.VideoCodecs)
}

func encoderName(backend Backend, hevc bool) string {
	names, ok := encoderNames[backend]
	if !ok {
		// Defensive: software fallback names.
		if hevc {
			return "libx265"
		}
		return "libx264"
	}
	if hevc {
		return names.hevc
	}
	return names.h264
}

// pickProfile selects requested (default "original"), clamps to userMaxQuality
// (lower of the two by ladder position), then clamps by caps.MaxHeight
// (highest profile with Height <= MaxHeight; MaxHeight 0 = no limit).
// Ladder order = slice order (highest first). Never upgrades quality.
func pickProfile(caps ClientCaps, userMaxQuality string, profiles []Profile) (Profile, error) {
	if len(profiles) == 0 {
		return Profile{}, fmt.Errorf("no profiles configured")
	}

	reqName := caps.Profile
	if reqName == "" {
		reqName = "original"
	}
	req, ok := ProfileByName(profiles, reqName)
	if !ok {
		return Profile{}, fmt.Errorf("unknown profile %q", reqName)
	}

	chosen := req
	if userMaxQuality != "" {
		capP, ok := ProfileByName(profiles, userMaxQuality)
		if !ok {
			return Profile{}, fmt.Errorf("unknown user max quality %q", userMaxQuality)
		}
		// Lower quality = later in the ladder (higher index).
		if profileIndex(profiles, capP.Name) > profileIndex(profiles, chosen.Name) {
			chosen = capP
		}
	}

	if caps.MaxHeight > 0 && chosen.Height > caps.MaxHeight {
		// Highest profile (by ladder order) whose Height <= MaxHeight.
		found := false
		for _, p := range profiles {
			if p.Height <= caps.MaxHeight {
				chosen = p
				found = true
				break
			}
		}
		if !found {
			// No profile fits; use the lowest rung (still no error per Negotiate contract).
			chosen = profiles[len(profiles)-1]
		}
	}

	return chosen, nil
}

func profileIndex(profiles []Profile, name string) int {
	for i, p := range profiles {
		if p.Name == name {
			return i
		}
	}
	return -1
}

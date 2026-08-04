package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config holds runtime settings loaded from config.yaml under DataDir,
// with environment variables taking precedence over file values.
type Config struct {
	ListenAddr string   `yaml:"listenAddr"` // default ":8400"
	DataDir    string   `yaml:"-"`
	SegmentDir string   `yaml:"segmentDir"` // default filepath.Join(DataDir, "segments")
	FFmpegPath string   `yaml:"ffmpegPath"` // default "ffmpeg"
	Encoder    string   `yaml:"encoder"`    // "auto"|"videotoolbox"|"qsv"|"nvenc"|"vaapi"|"software"
	AllowHEVC  bool     `yaml:"allowHevc"`
	Devices    []string `yaml:"devices"` // manual HDHomeRun IPs
	XMLTV      struct {
		Source       string `yaml:"source"`       // file path or http(s) URL
		RefreshHours int    `yaml:"refreshHours"` // default 12
	} `yaml:"xmltv"`
	SchedulesDirect struct {
		Username string `yaml:"username"`
		Password string `yaml:"password"` // raw; SHA1 computed at request time (SD API requirement)
		LineupID string `yaml:"lineupId"`
	} `yaml:"schedulesDirect"`
}

// Load reads <dataDir>/config.yaml if present, applies defaults, then env overrides.
// Env vars: BOWTIE_LISTEN_ADDR, BOWTIE_FFMPEG_PATH, BOWTIE_ENCODER, BOWTIE_SEGMENT_DIR,
// BOWTIE_DEVICES (comma-separated).
func Load(dataDir string) (Config, error) {
	cfg := Config{
		ListenAddr: ":8400",
		DataDir:    dataDir,
		SegmentDir: filepath.Join(dataDir, "segments"),
		FFmpegPath: "ffmpeg",
		Encoder:    "auto",
	}
	cfg.XMLTV.RefreshHours = 12

	path := filepath.Join(dataDir, "config.yaml")
	if data, err := os.ReadFile(path); err == nil {
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return Config{}, fmt.Errorf("parse config.yaml: %w", err)
		}
		// DataDir is always the argument; yaml must not override it.
		cfg.DataDir = dataDir
		// Re-apply defaults that yaml might have left empty.
		if cfg.ListenAddr == "" {
			cfg.ListenAddr = ":8400"
		}
		if cfg.SegmentDir == "" {
			cfg.SegmentDir = filepath.Join(dataDir, "segments")
		}
		if cfg.FFmpegPath == "" {
			cfg.FFmpegPath = "ffmpeg"
		}
		if cfg.Encoder == "" {
			cfg.Encoder = "auto"
		}
		if cfg.XMLTV.RefreshHours == 0 {
			cfg.XMLTV.RefreshHours = 12
		}
	} else if !os.IsNotExist(err) {
		return Config{}, fmt.Errorf("read config.yaml: %w", err)
	}

	if v := os.Getenv("BOWTIE_LISTEN_ADDR"); v != "" {
		cfg.ListenAddr = v
	}
	if v := os.Getenv("BOWTIE_FFMPEG_PATH"); v != "" {
		cfg.FFmpegPath = v
	}
	if v := os.Getenv("BOWTIE_ENCODER"); v != "" {
		cfg.Encoder = v
	}
	if v := os.Getenv("BOWTIE_SEGMENT_DIR"); v != "" {
		cfg.SegmentDir = v
	}
	if v := os.Getenv("BOWTIE_DEVICES"); v != "" {
		parts := strings.Split(v, ",")
		devices := make([]string, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				devices = append(devices, p)
			}
		}
		cfg.Devices = devices
	}

	return cfg, nil
}

package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ajthom90/bowtie/server/internal/config"
)

func TestLoadDefaults(t *testing.T) {
	dir := t.TempDir()

	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.ListenAddr != ":8400" {
		t.Errorf("ListenAddr = %q, want :8400", cfg.ListenAddr)
	}
	wantSeg := filepath.Join(dir, "segments")
	if cfg.SegmentDir != wantSeg {
		t.Errorf("SegmentDir = %q, want %q", cfg.SegmentDir, wantSeg)
	}
	if cfg.FFmpegPath != "ffmpeg" {
		t.Errorf("FFmpegPath = %q, want ffmpeg", cfg.FFmpegPath)
	}
	if cfg.Encoder != "auto" {
		t.Errorf("Encoder = %q, want auto", cfg.Encoder)
	}
	if cfg.DataDir != dir {
		t.Errorf("DataDir = %q, want %q", cfg.DataDir, dir)
	}
}

func TestLoadYAMLAndEnvOverride(t *testing.T) {
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(yamlPath, []byte("listenAddr: \":9000\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("BOWTIE_LISTEN_ADDR", ":9100")

	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ListenAddr != ":9100" {
		t.Errorf("ListenAddr = %q, want :9100 (env should override yaml)", cfg.ListenAddr)
	}
}

// BOWTIE_MULTITRACK=off|0|false turns off captions and extra audio.
func TestMultitrackKillSwitchEnv(t *testing.T) {
	for _, v := range []string{"off", "0", "false", "OFF"} {
		t.Setenv("BOWTIE_MULTITRACK", v)
		cfg, err := config.Load(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if !cfg.DisableMultitrack {
			t.Errorf("BOWTIE_MULTITRACK=%q: DisableMultitrack=false", v)
		}
	}
	t.Setenv("BOWTIE_MULTITRACK", "")
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DisableMultitrack {
		t.Error("unset: DisableMultitrack=true")
	}
}

func TestRecordingsDirAndMinFree(t *testing.T) {
	dir := t.TempDir()
	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RecordingsDir != filepath.Join(dir, "recordings") || cfg.DVRMinFreeGB != 20 {
		t.Fatalf("defaults: %q %d", cfg.RecordingsDir, cfg.DVRMinFreeGB)
	}
	t.Setenv("BOWTIE_RECORDINGS_DIR", "/recordings")
	t.Setenv("BOWTIE_DVR_MIN_FREE_GB", "50")
	cfg, err = config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RecordingsDir != "/recordings" || cfg.DVRMinFreeGB != 50 {
		t.Fatalf("env: %q %d", cfg.RecordingsDir, cfg.DVRMinFreeGB)
	}
}

func TestComskipSettings(t *testing.T) {
	dir := t.TempDir()
	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ComskipPath != "comskip" || cfg.ComskipINI != "" {
		t.Fatalf("defaults: %q %q", cfg.ComskipPath, cfg.ComskipINI)
	}
	t.Setenv("BOWTIE_COMSKIP_PATH", "/opt/comskip/bin/comskip")
	t.Setenv("BOWTIE_COMSKIP_INI", "/config/comskip.ini")
	cfg, err = config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ComskipPath != "/opt/comskip/bin/comskip" || cfg.ComskipINI != "/config/comskip.ini" {
		t.Fatalf("env: %q %q", cfg.ComskipPath, cfg.ComskipINI)
	}
}

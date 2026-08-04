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

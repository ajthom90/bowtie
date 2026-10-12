package stream

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ajthom90/bowtie/server/internal/transcode"
)

func TestSweepCaptionsRemovesOldUnlistedVTT(t *testing.T) {
	dir := t.TempDir()
	l := transcode.Layout{Rungs: []transcode.Rung{{Height: 480, VideoKbps: 1500}}, Captions: true}
	now := time.Now()
	write := func(name, body string, age time.Duration) {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, now.Add(-age), now.Add(-age)); err != nil {
			t.Fatal(err)
		}
	}
	// FFmpeg 5.1 after an append restart: "." entries; names come from the sequence.
	write("v480_vtt.m3u8", "#EXTM3U\n#EXT-X-MEDIA-SEQUENCE:5\n#EXTINF:4.0,\n.\n#EXTINF:4.0,\nv4806.vtt\n", 0)
	write("v4800.vtt", "WEBVTT\n", time.Minute)   // old, unlisted: removed
	write("v4805.vtt", "WEBVTT\n", time.Minute)   // old but listed (as "."): kept
	write("v4807.vtt", "WEBVTT\n", 2*time.Second) // new, not listed yet: kept
	write("v480_00000.ts", "TS", time.Minute)     // not a caption: kept
	if n := sweepCaptions(dir, l, 3, now); n != 1 {
		t.Fatalf("removed %d files, want 1", n)
	}
	for name, want := range map[string]bool{"v4800.vtt": false, "v4805.vtt": true, "v4807.vtt": true, "v480_00000.ts": true} {
		if _, err := os.Stat(filepath.Join(dir, name)); (err == nil) != want {
			t.Errorf("%s exists=%v, want %v", name, err == nil, want)
		}
	}
	l.Captions = false
	write("v4801.vtt", "WEBVTT\n", time.Minute)
	if n := sweepCaptions(dir, l, 3, now); n != 0 {
		t.Fatalf("swept %d files of a session without captions", n)
	}
}

func TestSweepCaptionsKeepsAgedFilesWhenPlaylistUnreadable(t *testing.T) {
	dir := t.TempDir()
	l := transcode.Layout{Rungs: []transcode.Rung{{Height: 480, VideoKbps: 1500}}, Captions: true}
	now := time.Now()
	// A directory at the playlist path fails ReadFile with an error other
	// than not-exist, including when the process is root.
	if err := os.Mkdir(filepath.Join(dir, l.CaptionPlaylist()), 0o755); err != nil {
		t.Fatal(err)
	}
	aged := filepath.Join(dir, "v4800.vtt")
	if err := os.WriteFile(aged, []byte("WEBVTT\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := now.Add(-time.Hour)
	if err := os.Chtimes(aged, old, old); err != nil {
		t.Fatal(err)
	}
	if n := sweepCaptions(dir, l, 3, now); n != 0 {
		t.Fatalf("removed %d files with an unreadable playlist, want 0", n)
	}
	if _, err := os.Stat(aged); err != nil {
		t.Fatalf("aged caption gone after unreadable playlist: %v", err)
	}
}

func TestSweepCaptionsKeepsOneWindowBelowMediaSequence(t *testing.T) {
	dir := t.TempDir()
	l := transcode.Layout{Rungs: []transcode.Rung{{Height: 480, VideoKbps: 1500}}, Captions: true}
	now := time.Now()
	const listSize = 3
	const mediaSequence = 5
	write := func(name string) {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("WEBVTT\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		old := now.Add(-time.Hour)
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}
	playlist := fmt.Sprintf("#EXTM3U\n#EXT-X-MEDIA-SEQUENCE:%d\n#EXTINF:4.0,\n%s%d.vtt\n",
		mediaSequence, l.TopName(), mediaSequence)
	if err := os.WriteFile(filepath.Join(dir, l.CaptionPlaylist()), []byte(playlist), 0o644); err != nil {
		t.Fatal(err)
	}
	// [MediaSequence-listSize, MediaSequence) stays. One seq before that window does not.
	justBelow := fmt.Sprintf("%s%d.vtt", l.TopName(), mediaSequence-1)
	windowStart := fmt.Sprintf("%s%d.vtt", l.TopName(), mediaSequence-listSize)
	beforeWindow := fmt.Sprintf("%s%d.vtt", l.TopName(), mediaSequence-listSize-1)
	write(justBelow)
	write(windowStart)
	write(beforeWindow)
	if n := sweepCaptions(dir, l, listSize, now); n != 1 {
		t.Fatalf("removed %d files, want 1", n)
	}
	for name, want := range map[string]bool{justBelow: true, windowStart: true, beforeWindow: false} {
		if _, err := os.Stat(filepath.Join(dir, name)); (err == nil) != want {
			t.Errorf("%s exists=%v, want %v", name, err == nil, want)
		}
	}
}

func TestMaintainSweepsStaleCaptions(t *testing.T) {
	m, _, _, chID := newMultitrackManager(t, false)
	h, err := m.Start(context.Background(), viewer("alice", ""), chID, clientCaps(""))
	if err != nil {
		t.Fatal(err)
	}
	media, _ := m.SessionMediaOf(h.ViewerID)
	if !media.Layout.Captions {
		t.Fatal("multitrack session has no captions")
	}
	stale := filepath.Join(h.SessionDir, media.Layout.TopName()+"0.vtt")
	if err := os.WriteFile(stale, []byte("WEBVTT\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}
	m.maintain()
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale caption segment still there (err=%v)", err)
	}
}

package tsloop

import (
	"bytes"
	"os"
	"testing"
	"time"
)

const fixturePath = "../testdata/fixture.ts"

func loadFixture(t testing.TB) *Source {
	t.Helper()
	src, err := LoadFile(fixturePath)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	return src
}

func TestLoadFixture(t *testing.T) {
	src := loadFixture(t)
	// Independently derive each track's span from the file: last − first +
	// typical frame delta. The loop is the longest track so nothing overlaps.
	data, _ := os.ReadFile(fixturePath)
	spans := map[uint16][]int64{}
	for off := 0; off+PacketSize <= len(data); off += PacketSize {
		p := data[off : off+PacketSize]
		pts, dts, hasPTS, hasDTS := PESTimestamps(p)
		if !hasPTS {
			continue
		}
		ts := pts
		if hasDTS {
			ts = dts
		}
		spans[PID(p)] = append(spans[PID(p)], ts)
	}
	var want int64
	for _, ts := range spans {
		d := ts[1] - ts[0]
		if span := ts[len(ts)-1] - ts[0] + d; span > want {
			want = span
		}
	}
	if got := src.LoopDuration(); got != time.Duration(want)*time.Second/90000 {
		t.Fatalf("LoopDuration = %v, want %v (%d ticks)", got, time.Duration(want)*time.Second/90000, want)
	}
	if src.TrackOf(0x100) != TrackVideo || src.TrackOf(0x101) != TrackAudio || src.TrackOf(0x11) != TrackOther {
		t.Fatalf("tracks: video=%v audio=%v sdt=%v", src.TrackOf(0x100), src.TrackOf(0x101), src.TrackOf(0x11))
	}
	if src.Name() != "fixture" {
		t.Fatalf("Name = %q", src.Name())
	}
}

func TestLoadRejectsMisaligned(t *testing.T) {
	if _, err := Load("short", bytes.NewReader(make([]byte, 187))); err == nil {
		t.Fatal("187 bytes accepted")
	}
	data, _ := os.ReadFile(fixturePath)
	bad := append([]byte(nil), data...)
	bad[188*5] = 0x00
	if _, err := Load("badsync", bytes.NewReader(bad)); err == nil {
		t.Fatal("bad sync byte accepted")
	}
}

func TestLoadRejectsNoPCR(t *testing.T) {
	p := make([]byte, PacketSize)
	p[0] = 0x47
	p[3] = 0x10
	if _, err := Load("nopcr", bytes.NewReader(bytes.Repeat(p, 10))); err == nil {
		t.Fatal("stream without PCR accepted")
	}
}

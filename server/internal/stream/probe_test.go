package stream

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"

	"github.com/ajthom90/bowtie/server/internal/transcode"
)

// pmtPacket builds a single-packet PMT: MPEG-2 video on 0x31 then es entries.
func pmtPacket(es ...[]byte) []byte {
	sec := []byte{0x02, 0, 0, 0x00, 0x03, 0xC1, 0x00, 0x00, 0xE0, 0x31, 0xF0, 0x00}
	sec = append(sec, 0x02, 0xE0, 0x31, 0xF0, 0x00)
	for _, e := range es {
		sec = append(sec, e...)
	}
	sec = append(sec, 0, 0, 0, 0)
	l := len(sec) - 3
	sec[1] = 0xB0 | byte(l>>8)
	sec[2] = byte(l)
	pkt := make([]byte, tsPacketSize)
	for i := range pkt {
		pkt[i] = 0xFF
	}
	copy(pkt, []byte{tsSyncByte, 0x40, 0x30, 0x10, 0x00})
	copy(pkt[5:], sec)
	return pkt
}

func audioES(streamType byte, pid uint16, lang string, audioType byte) []byte {
	var desc []byte
	if lang != "" {
		desc = append([]byte{0x0A, 4}, append([]byte(lang), audioType)...)
	}
	return append([]byte{streamType, 0xE0 | byte(pid>>8), byte(pid), 0xF0, byte(len(desc))}, desc...)
}

func TestParsePMT(t *testing.T) {
	got := parsePMT(pmtPacket(
		audioES(0x81, 0x34, "eng", 0),
		audioES(0x81, 0x35, "spa", 0),
		audioES(0x81, 0x36, "eng", 0x03),
		audioES(0x06, 0x37, "", 0),
		audioES(0x0F, 0x38, "", 0),
	))
	want := []transcode.AudioTrack{{Lang: "eng", AC3: true}, {Lang: "spa", AC3: true}, {Lang: "eng", Described: true, AC3: true}, {}}
	if got.VideoPID != 0x31 || len(got.Audio) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got.Audio[i] != want[i] {
			t.Fatalf("track %d = %+v want %+v", i, got.Audio[i], want[i])
		}
	}
}

func TestParsePMTGarbage(t *testing.T) {
	for _, pkt := range [][]byte{nil, make([]byte, 10), make([]byte, tsPacketSize)} {
		if got := parsePMT(pkt); len(got.Audio) != 0 || got.VideoPID != 0 {
			t.Fatalf("garbage parsed: %+v", got)
		}
	}
	trunc := pmtPacket(audioES(0x81, 0x34, "eng", 0))
	trunc[7] = 0xB0 | 0x0F
	_ = parsePMT(trunc) // no panic
}

func TestSequenceHeaderHeight(t *testing.T) {
	// 00 00 01 B3, width 1280 (0x500), height 720 (0x2D0): 12+12 bits = 50 02 D0.
	payload := []byte{0x47, 0, 0, 1, 0xB3, 0x50, 0x02, 0xD0, 0x37}
	if h := sequenceHeaderHeight(payload); h != 720 {
		t.Fatalf("height=%d", h)
	}
	if h := sequenceHeaderHeight([]byte{0, 0, 1, 0x00, 1, 2, 3}); h != 0 {
		t.Fatalf("no header → 0, got %d", h)
	}
	// 1920x1080: 0x780, 0x438 → 78 04 38.
	if h := sequenceHeaderHeight([]byte{0, 0, 1, 0xB3, 0x78, 0x04, 0x38}); h != 1080 {
		t.Fatalf("height=%d", h)
	}
}

// probeBody is a channel start: PAT → PMT(0x30) with MPEG-2 video on 0x31 and
// the given audio, then one video packet carrying a 1280x720 sequence header.
func probeBody(audio ...[]byte) []byte {
	b := newTSBuilder()
	b.pmtPID = 0x30
	body := append([]byte{}, b.PAT()...)
	body = append(body, pmtPacket(audio...)...)
	return append(body, b.packet(0x31, true, []byte{0, 0, 1, 0xB3, 0x50, 0x02, 0xD0, 0x37})...)
}

func TestProgramInfoReadsPMTAndHeight(t *testing.T) {
	body := probeBody(audioES(0x81, 0x34, "eng", 0), audioES(0x81, 0x35, "spa", 0))
	im, _ := newTestIngest(t, func(context.Context, string) (io.ReadCloser, int, error) {
		return newBlockingBody(body), 200, nil
	}, nil)
	sub, err := im.Attach(context.Background(), 7, "http://dev/auto/v7")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sub.Close() }()
	go func() { _, _ = io.Copy(io.Discard, sub.R) }()
	info, ok := im.ProgramInfo(7, 2*time.Second)
	if !ok || info.SourceHeight != 720 || len(info.Audio) != 2 || info.Audio[1].Lang != "spa" || !info.Audio[0].AC3 {
		t.Fatalf("ProgramInfo=%+v ok=%v", info, ok)
	}
}

func TestProgramInfoTimesOutWithoutPMT(t *testing.T) {
	im, _ := newTestIngest(t, func(context.Context, string) (io.ReadCloser, int, error) {
		return newBlockingBody(nil), 200, nil
	}, nil)
	sub, err := im.Attach(context.Background(), 7, "http://dev/auto/v7")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sub.Close() }()
	start := time.Now()
	if _, ok := im.ProgramInfo(7, 100*time.Millisecond); ok {
		t.Fatal("ok without a PMT")
	}
	if time.Since(start) > time.Second {
		t.Fatal("timeout not honored")
	}
}

// H.264 video carries no MPEG-2 sequence header: ProgramInfo must not wait
// out the timeout for a height it can never learn.
func TestProgramInfoDoesNotWaitForH264Height(t *testing.T) {
	pmt := pmtPacket(audioES(0x81, 0x34, "eng", 0))
	pmt[5+12] = 0x1B // first ES (video) → H.264
	b := newTSBuilder()
	b.pmtPID = 0x30
	body := append(append([]byte{}, b.PAT()...), pmt...)
	im, _ := newTestIngest(t, func(context.Context, string) (io.ReadCloser, int, error) {
		return newBlockingBody(body), 200, nil
	}, nil)
	sub, err := im.Attach(context.Background(), 7, "http://dev/auto/v7")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sub.Close() }()
	go func() { _, _ = io.Copy(io.Discard, sub.R) }()
	start := time.Now()
	info, ok := im.ProgramInfo(7, 3*time.Second)
	if !ok || info.VideoMPEG2 || info.SourceHeight != 0 || len(info.Audio) != 1 {
		t.Fatalf("info=%+v ok=%v", info, ok)
	}
	if time.Since(start) > time.Second {
		t.Fatalf("waited %v for an H.264 height", time.Since(start))
	}
}

func TestSourceHeightTS(t *testing.T) {
	// A capture part: PAT, PMT (MPEG-2 video on 0x31), the 720p header.
	if h := SourceHeightTS(bytes.NewReader(probeBody(audioES(0x81, 0x34, "eng", 0))), 1<<20); h != 720 {
		t.Fatalf("height=%d, want 720", h)
	}
	// Leading junk before the first sync byte is skipped.
	junk := append([]byte{1, 2, 3}, probeBody()...)
	if h := SourceHeightTS(bytes.NewReader(junk), 1<<20); h != 720 {
		t.Fatalf("after junk: height=%d", h)
	}
	// H.264 video has no MPEG-2 sequence header: unknown.
	if h := SourceHeightTS(bytes.NewReader(newTSBuilder().StreamPATPMTOnceThenMedia(5)), 1<<20); h != 0 {
		t.Fatalf("h264: height=%d, want 0", h)
	}
	// A header further in is found, unless it's past the read limit.
	b := newTSBuilder()
	b.pmtPID = 0x30
	body := append(append([]byte{}, b.PAT()...), pmtPacket()...)
	for range 10 {
		body = append(body, b.packet(0x31, false, []byte{9, 9, 9})...)
	}
	body = append(body, b.packet(0x31, true, []byte{0, 0, 1, 0xB3, 0x78, 0x04, 0x38})...)
	if h := SourceHeightTS(bytes.NewReader(body), 1<<20); h != 1080 {
		t.Fatalf("height=%d, want 1080", h)
	}
	if h := SourceHeightTS(bytes.NewReader(body), 5*tsPacketSize); h != 0 {
		t.Fatalf("past limit: height=%d, want 0", h)
	}
	if h := SourceHeightTS(bytes.NewReader(nil), 1<<20); h != 0 {
		t.Fatalf("empty: height=%d", h)
	}
}

package stream

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

// SharePlay: a participant joins the sharer's exact session (one playlist,
// one clock), whatever quality it would otherwise get.
func TestJoinSharersSession(t *testing.T) {
	m, runner, _, ch, alice, bob := limitsEnv(t)
	h, err := m.Start(context.Background(), alice, ch[0], clientCaps("high"))
	if err != nil {
		t.Fatal(err)
	}
	j, err := m.Join(context.Background(), bob, h.SessionID, ch[0], clientCaps("low"))
	if err != nil {
		t.Fatal(err)
	}
	if j.SessionID != h.SessionID || runner.Starts() != 1 {
		t.Fatalf("join made a new session: %s vs %s, starts=%d", j.SessionID, h.SessionID, runner.Starts())
	}
	if _, ok := m.SessionMediaOf(j.ViewerID); !ok {
		t.Fatal("joined viewer has no media")
	}
}

func TestJoinRefusesOtherChannelOrUnknownSession(t *testing.T) {
	m, _, _, ch, alice, bob := limitsEnv(t)
	h, err := m.Start(context.Background(), alice, ch[0], clientCaps(""))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Join(context.Background(), bob, h.SessionID, ch[1], clientCaps("")); !errors.Is(err, ErrNotJoinable) {
		t.Fatalf("other channel: %v", err)
	}
	if _, err := m.Join(context.Background(), bob, "nope", ch[0], clientCaps("")); !errors.Is(err, ErrNotJoinable) {
		t.Fatalf("unknown session: %v", err)
	}
}

func TestJoinRespectsStreamLimit(t *testing.T) {
	m, _, _, ch, alice, bob := limitsEnv(t)
	h, _ := m.Start(context.Background(), alice, ch[0], clientCaps(""))
	bob.MaxStreams = 1
	if _, err := m.Start(context.Background(), bob, ch[1], clientCaps("")); err != nil {
		t.Fatal(err)
	}
	_, err := m.Join(context.Background(), bob, h.SessionID, ch[0], clientCaps(""))
	var le *UserLimitError
	if !errors.As(err, &le) || le.Kind != "streams" {
		t.Fatalf("limit not applied to join: %v", err)
	}
}

// pesTS builds one video TS packet (no pointer field) whose payload ends
// exactly at the packet end (adaptation-field stuffing), so a payload can
// be split across packets at a chosen byte.
func pesTS(pid uint16, pusi bool, payload []byte) []byte {
	if len(payload) > 184 {
		panic("payload too long")
	}
	pkt := make([]byte, 0, tsPacketSize)
	b1 := byte(pid>>8) & 0x1F
	if pusi {
		b1 |= 0x40
	}
	pkt = append(pkt, tsSyncByte, b1, byte(pid))
	if len(payload) == 184 {
		pkt = append(pkt, 0x10)
	} else {
		af := 183 - len(payload) // adaptation_field_length
		pkt = append(pkt, 0x30, byte(af))
		if af > 0 {
			pkt = append(pkt, 0x00) // flags
			for i := 1; i < af; i++ {
				pkt = append(pkt, 0xFF)
			}
		}
	}
	return append(pkt, payload...)
}

// pesHead is a video PES header with PTS only.
var pesHead = []byte{0, 0, 1, 0xE0, 0, 0, 0x80, 0x80, 0x05, 0x21, 0, 1, 0, 1}

// nal is an H.264 NAL unit with a 4-byte start code and some body bytes.
func nal(typ byte) []byte { return []byte{0, 0, 0, 1, typ, 0x42, 0x00, 0x1F} }

func cat(parts ...[]byte) []byte { return bytes.Join(parts, nil) }

// h264Ingest is a channelIngest that has seen PAT and an H.264 PMT.
func h264Ingest(t *testing.T) (*channelIngest, *tsBuilder, []byte) {
	t.Helper()
	c := newChannelIngest(nil, 1)
	b := newTSBuilder() // PMT: H.264 (0x1B) on mediaPID
	pat, pmt := b.PAT(), b.PMT()
	c.handleTSPacketLocked(pat)
	c.handleTSPacketLocked(pmt)
	return c, b, cat(pat, pmt)
}

func feed(c *channelIngest, pkts ...[]byte) {
	for _, p := range pkts {
		c.handleTSPacketLocked(p)
	}
}

func TestKeyJoinH264SplitAcrossPackets(t *testing.T) {
	c, b, tables := h264Ingest(t)
	pid := b.mediaPID
	before := pesTS(pid, true, cat(pesHead, nal(0x41))) // non-IDR slice
	// SPS, PPS, then the IDR whose start code is split across two packets.
	au := cat(pesHead, nal(0x67), nal(0x68), make([]byte, 160), []byte{0, 0})
	p1 := pesTS(pid, true, au[:184])
	p2 := pesTS(pid, false, cat(au[184:], []byte{0, 1, 0x65, 0x88}))
	next := pesTS(pid, true, cat(pesHead, nal(0x41)))
	feed(c, before, p1, p2, next)
	if got, want := c.joinSnapshotLocked(), cat(tables, p1, p2, next); !bytes.Equal(got, want) {
		t.Fatalf("join does not start at the SPS/PPS/IDR access unit (%d bytes, want %d)", len(got), len(want))
	}
}

func TestKeyJoinKeepsParameterSetsFromEarlierPES(t *testing.T) {
	c, b, tables := h264Ingest(t)
	pid := b.mediaPID
	params := pesTS(pid, true, cat(pesHead, nal(0x67), nal(0x68), nal(0x06))) // SPS, PPS, SEI
	idr := pesTS(pid, true, cat(pesHead, nal(0x65)))
	tail := pesTS(pid, true, cat(pesHead, nal(0x41)))
	feed(c, pesTS(pid, true, cat(pesHead, nal(0x41))), params, idr, tail)
	if got, want := c.joinSnapshotLocked(), cat(tables, params, idr, tail); !bytes.Equal(got, want) {
		t.Fatal("join dropped the SPS/PPS that precede the IDR")
	}
}

func TestKeyJoinIgnoresIDRWithoutParameterSets(t *testing.T) {
	c, b, _ := h264Ingest(t)
	pid := b.mediaPID
	// The next PES start makes ingest classify the IDR-only PES.
	feed(c, pesTS(pid, true, cat(pesHead, nal(0x65))), pesTS(pid, true, cat(pesHead, nal(0x41))))
	if c.joinValid {
		t.Fatal("an IDR without SPS/PPS made a valid join")
	}
}

func TestKeyJoinPATDoesNotRestartAValidJoin(t *testing.T) {
	c, b, _ := h264Ingest(t)
	pid := b.mediaPID
	key := pesTS(pid, true, cat(pesHead, nal(0x67), nal(0x68), nal(0x65)))
	m := pesTS(pid, true, cat(pesHead, nal(0x41))) // the next PES: the key is classified here
	pat2 := b.PAT()
	m2 := pesTS(pid, false, []byte{4, 5, 6})
	feed(c, key, m, pat2, m2)
	if got, want := c.joinSnapshotLocked(), cat(pat2, b.PMT(), key, m, m2); !bytes.Equal(got[2*tsPacketSize:], want[2*tsPacketSize:]) || !bytes.Equal(got[:tsPacketSize], pat2) {
		t.Fatal("a mid-GOP PAT restarted the key join")
	}
}

func TestKeyJoinInvalidatedWhenTrimmed(t *testing.T) {
	c, b, _ := h264Ingest(t)
	pid := b.mediaPID
	feed(c, pesTS(pid, true, cat(pesHead, nal(0x67), nal(0x68), nal(0x65))))
	filler := pesTS(pid, false, make([]byte, 184))
	for i := 0; i < ingestJoinBufMax/tsPacketSize+10; i++ {
		c.handleTSPacketLocked(filler)
	}
	if c.joinValid {
		t.Fatal("join still claims a key after the key was trimmed away")
	}
	pat := b.PAT()
	c.handleTSPacketLocked(pat)
	if got := c.joinSnapshotLocked(); len(got) != 2*tsPacketSize {
		t.Fatalf("after an invalidated join, a PAT must restart the buffer (got %d bytes)", len(got))
	}
}

func TestKeyJoinMPEG2SequenceHeader(t *testing.T) {
	c := newChannelIngest(nil, 1)
	b := newTSBuilder()
	b.pmtPID = 0x30
	pat, pmt := b.PAT(), pmtPacket() // MPEG-2 video on 0x31
	feed(c, pat, pmt)
	gop1 := pesTS(0x31, true, cat(pesHead, []byte{0, 0, 1, 0xB3, 0x50, 0x02, 0xD0, 0x37}))
	mid := pesTS(0x31, true, cat(pesHead, []byte{0, 0, 1, 0x00, 1}))
	gop2 := pesTS(0x31, true, cat(pesHead, []byte{0, 0, 1, 0xB3, 0x50, 0x02, 0xD0, 0x37}))
	tail := pesTS(0x31, false, []byte{4, 5, 6})
	next := pesTS(0x31, true, cat(pesHead, []byte{0, 0, 1, 0x00, 2})) // classifies gop2
	feed(c, gop1, mid, gop2, tail, next)
	if got, want := c.joinSnapshotLocked(), cat(pat, pmt, gop2, tail, next); !bytes.Equal(got, want) {
		t.Fatal("join is not PAT+PMT+last sequence header and what follows")
	}
}

func TestScanVideoKey(t *testing.T) {
	cases := []struct {
		name string
		b    []byte
		st   byte
		want nalFlags
	}{
		{"h264 sps pps idr", cat(nal(0x67), nal(0x68), nal(0x65)), 0x1B, nalFlags{sps: true, pps: true, idr: true}},
		{"h264 3-byte start code idr", []byte{0, 0, 1, 0x65, 0x88}, 0x1B, nalFlags{idr: true}},
		{"h264 non-idr slice", nal(0x41), 0x1B, nalFlags{}},
		{"mpeg2 sequence header", []byte{0, 0, 1, 0xB3, 1}, 0x02, nalFlags{seq: true}},
		{"mpeg2 picture only", []byte{0, 0, 1, 0x00, 1}, 0x02, nalFlags{}},
		{"hevc not scanned", []byte{0, 0, 1, 0x40, 1}, 0x24, nalFlags{}},
	}
	for _, c := range cases {
		if got := scanVideoKey(c.b, c.st); got != c.want {
			t.Errorf("%s: got %+v, want %+v", c.name, got, c.want)
		}
	}
}

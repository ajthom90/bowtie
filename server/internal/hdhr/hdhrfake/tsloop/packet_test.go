package tsloop

import (
	"os"
	"testing"
)

// pcrPacket returns a packet with an adaptation field carrying a PCR slot.
func pcrPacket() []byte {
	p := make([]byte, PacketSize)
	p[0] = 0x47
	p[1] = 0x01 // PID 0x100
	p[2] = 0x00
	p[3] = 0x30 // adaptation + payload, CC 0
	p[4] = 7    // adaptation_field_length
	p[5] = 0x10 // PCR_flag
	return p
}

// pesPacket returns a PUSI packet starting a PES with the given stream id and
// PTS_DTS_flags (2 = PTS only, 3 = PTS+DTS).
func pesPacket(streamID byte, flags byte) []byte {
	p := make([]byte, PacketSize)
	p[0] = 0x47
	p[1] = 0x41 // PUSI + PID 0x100
	p[2] = 0x00
	p[3] = 0x10 // payload only
	pes := p[4:]
	pes[0], pes[1], pes[2], pes[3] = 0x00, 0x00, 0x01, streamID
	pes[6] = 0x80
	pes[7] = flags << 6
	pes[8] = 10
	if flags == 3 {
		pes[9] = 0x31
		pes[14] = 0x11
	} else {
		pes[9] = 0x21
	}
	return p
}

func TestPCRRoundTrip(t *testing.T) {
	p := pcrPacket()
	SetPCR(p, 0x1_2345_6789, 299)
	base, ext, ok := PCR(p)
	if !ok || base != 0x1_2345_6789 || ext != 299 {
		t.Fatalf("PCR = %#x/%d/%v, want 0x123456789/299/true", base, ext, ok)
	}
	if p[10]&0x7E != 0x7E {
		t.Fatalf("reserved bits = %#x, want all set", p[10]&0x7E)
	}
}

func TestPCRWrapMask(t *testing.T) {
	p := pcrPacket()
	SetPCR(p, 1<<33+5, 0)
	if base, _, _ := PCR(p); base != 5 {
		t.Fatalf("base = %d, want 5", base)
	}
}

func TestPCRAbsentWithoutFlag(t *testing.T) {
	p := pcrPacket()
	p[5] = 0
	if _, _, ok := PCR(p); ok {
		t.Fatal("PCR reported without PCR_flag")
	}
}

func TestPESTimestampsRoundTrip(t *testing.T) {
	p := pesPacket(0xE0, 3)
	SetPESTimestamps(p, 8589934000, 8589933000)
	pts, dts, hasPTS, hasDTS := PESTimestamps(p)
	if !hasPTS || !hasDTS || pts != 8589934000 || dts != 8589933000 {
		t.Fatalf("got pts=%d dts=%d (%v,%v)", pts, dts, hasPTS, hasDTS)
	}
	ptsB, dtsB := p[4+9:4+14], p[4+14:4+19]
	for i, b := range []byte{ptsB[0], ptsB[2], ptsB[4], dtsB[0], dtsB[2], dtsB[4]} {
		if b&1 != 1 {
			t.Fatalf("marker bit %d not set: %#x", i, b)
		}
	}
	if ptsB[0]>>4 != 0x3 || dtsB[0]>>4 != 0x1 {
		t.Fatalf("prefix nibbles = %x/%x, want 3/1", ptsB[0]>>4, dtsB[0]>>4)
	}
}

func TestPESOnlyPTS(t *testing.T) {
	p := pesPacket(0xC0, 2)
	before := append([]byte(nil), p[4+14:4+19]...)
	SetPESTimestamps(p, 12345, 999)
	pts, _, hasPTS, hasDTS := PESTimestamps(p)
	if !hasPTS || hasDTS || pts != 12345 {
		t.Fatalf("got pts=%d hasPTS=%v hasDTS=%v", pts, hasPTS, hasDTS)
	}
	if string(before) != string(p[4+14:4+19]) {
		t.Fatal("SetPESTimestamps wrote DTS bytes on a PTS-only PES")
	}
}

func TestPESSkipsNoHeaderStreams(t *testing.T) {
	p := pesPacket(0xBE, 2)
	if _, _, hasPTS, _ := PESTimestamps(p); hasPTS {
		t.Fatal("padding stream reported a PTS")
	}
}

func TestCCAndPayload(t *testing.T) {
	p := pcrPacket()
	SetCC(p, 0x1D)
	if CC(p) != 0xD || p[3]>>4 != 0x3 {
		t.Fatalf("CC=%#x p[3]=%#x", CC(p), p[3])
	}
	if !HasPayload(p) {
		t.Fatal("afc=3 should have payload")
	}
	p[3] = 0x20
	if HasPayload(p) {
		t.Fatal("afc=2 should have no payload")
	}
}

func TestRealFixtureFields(t *testing.T) {
	data, err := os.ReadFile("../testdata/fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	pcrs := 0
	var firstVideo, firstAudio []byte
	for off := 0; off+PacketSize <= len(data); off += PacketSize {
		p := data[off : off+PacketSize]
		if _, _, ok := PCR(p); ok && PID(p) == 0x100 {
			pcrs++
		}
		if _, _, hasPTS, _ := PESTimestamps(p); hasPTS {
			switch PID(p) {
			case 0x100:
				if firstVideo == nil {
					firstVideo = p
				}
			case 0x101:
				if firstAudio == nil {
					firstAudio = p
				}
			}
		}
	}
	if pcrs != 30 {
		t.Fatalf("PCR packets on 0x100 = %d, want 30", pcrs)
	}
	pts, dts, _, hasDTS := PESTimestamps(firstVideo)
	if pts != 129003 || !hasDTS || dts != 126000 {
		t.Fatalf("first video pts=%d dts=%d hasDTS=%v", pts, dts, hasDTS)
	}
	if sid, ok := StreamID(firstVideo); !ok || sid != 0xE0 {
		t.Fatalf("video stream id = %#x %v", sid, ok)
	}
	apts, _, _, aHasDTS := PESTimestamps(firstAudio)
	if apts != 128481 || aHasDTS {
		t.Fatalf("first audio pts=%d hasDTS=%v", apts, aHasDTS)
	}
}

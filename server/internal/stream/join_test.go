package stream

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync/atomic"
	"testing"
	"time"
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

// ccSeq stamps the continuity counter of video payload packets. dup repeats
// the previous packet (ISO 13818-1 allows one identical copy) and does not
// advance the counter.
type ccSeq byte

func (s *ccSeq) ts(pid uint16, pusi bool, payload []byte) []byte {
	pkt := pesTS(pid, pusi, payload)
	pkt[3] = (pkt[3] & 0xF0) | (byte(*s) & 0x0F)
	(*s)++
	return pkt
}

func (s *ccSeq) skip() { (*s)++ }

func (s *ccSeq) dup(pkt []byte) []byte { return append([]byte(nil), pkt...) }

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
	var cc ccSeq
	before := cc.ts(pid, true, cat(pesHead, nal(0x41))) // non-IDR slice
	// SPS, PPS, then the IDR whose start code is split: p1 ends 00 00, p2 starts 01 65.
	au := cat(pesHead, nal(0x67), nal(0x68), make([]byte, 152), []byte{0, 0})
	p1 := cc.ts(pid, true, au[:184])
	p2 := cc.ts(pid, false, []byte{1, 0x65, 0x88})
	next := cc.ts(pid, true, cat(pesHead, nal(0x41)))
	feed(c, before, p1, p2, next)
	if got, want := c.joinSnapshotLocked(), cat(tables, p1, p2, next); !bytes.Equal(got, want) {
		t.Fatalf("join does not start at the SPS/PPS/IDR access unit (%d bytes, want %d)", len(got), len(want))
	}
}

func TestKeyJoinKeepsParameterSetsFromEarlierPES(t *testing.T) {
	c, b, tables := h264Ingest(t)
	pid := b.mediaPID
	var cc ccSeq
	lead := cc.ts(pid, true, cat(pesHead, nal(0x41)))
	params := cc.ts(pid, true, cat(pesHead, nal(0x67), nal(0x68), nal(0x06))) // SPS, PPS, SEI
	idr := cc.ts(pid, true, cat(pesHead, nal(0x65)))
	tail := cc.ts(pid, true, cat(pesHead, nal(0x41)))
	feed(c, lead, params, idr, tail)
	if got, want := c.joinSnapshotLocked(), cat(tables, params, idr, tail); !bytes.Equal(got, want) {
		t.Fatal("join dropped the SPS/PPS that precede the IDR")
	}
}

func TestKeyJoinIgnoresIDRWithoutParameterSets(t *testing.T) {
	c, b, _ := h264Ingest(t)
	pid := b.mediaPID
	var cc ccSeq
	// The next PES start makes ingest classify the IDR-only PES.
	feed(c, cc.ts(pid, true, cat(pesHead, nal(0x65))), cc.ts(pid, true, cat(pesHead, nal(0x41))))
	if c.joinValid {
		t.Fatal("an IDR without SPS/PPS made a valid join")
	}
	feed(c,
		cc.ts(pid, true, cat(pesHead, nal(0x67), nal(0x68), nal(0x65))),
		cc.ts(pid, true, cat(pesHead, nal(0x41))),
	)
	if !c.joinValid {
		t.Fatal("SPS/PPS/IDR in the same stream did not join")
	}
}

// A PAT that arrives before the key PES is classified must not drop it.
// The next PES start still yields that key.
func TestKeyJoinPATKeepsUnclassifiedCandidate(t *testing.T) {
	c, b, _ := h264Ingest(t)
	pid := b.mediaPID
	var cc ccSeq
	key := cc.ts(pid, true, cat(pesHead, nal(0x67), nal(0x68), nal(0x65)))
	feed(c, key)
	if c.joinValid || c.pesDone || c.pesOff < 0 {
		t.Fatalf("setup: key packet is not an open candidate (valid=%v done=%v off=%d)", c.joinValid, c.pesDone, c.pesOff)
	}
	pat2 := b.PAT()
	c.handleTSPacketLocked(pat2)
	next := cc.ts(pid, true, cat(pesHead, nal(0x41)))
	feed(c, next)
	got := c.joinSnapshotLocked()
	if !c.joinValid || !bytes.Equal(got[:tsPacketSize], pat2) || !bytes.Equal(got[2*tsPacketSize:], cat(key, next)) {
		t.Fatalf("PAT dropped the unclassified key (len=%d want %d, joinValid=%v)", len(got), 4*tsPacketSize, c.joinValid)
	}
}

// SPS/PPS in one PES and the IDR in the next: a PAT between them must keep
// both, so the IDR still becomes a key that starts at the parameter sets.
func TestKeyJoinPATKeepsParameterSetsAheadOfIDR(t *testing.T) {
	c, b, _ := h264Ingest(t)
	pid := b.mediaPID
	var cc ccSeq
	params := cc.ts(pid, true, cat(pesHead, nal(0x67), nal(0x68)))
	idr := cc.ts(pid, true, cat(pesHead, nal(0x65)))
	feed(c, params, idr)
	if c.joinValid || c.pesDone || c.paramOff < 0 || !c.ppsSeen {
		t.Fatalf("setup: parameter sets are not pending (valid=%v done=%v param=%d pps=%v)", c.joinValid, c.pesDone, c.paramOff, c.ppsSeen)
	}
	pat2 := b.PAT()
	c.handleTSPacketLocked(pat2)
	next := cc.ts(pid, true, cat(pesHead, nal(0x41)))
	feed(c, next)
	got := c.joinSnapshotLocked()
	if !c.joinValid || !bytes.Equal(got[:tsPacketSize], pat2) || !bytes.Equal(got[2*tsPacketSize:], cat(params, idr, next)) {
		t.Fatalf("PAT dropped the SPS/PPS ahead of the IDR (valid=%v len=%d want %d)", c.joinValid, len(got), 5*tsPacketSize)
	}
}

func TestKeyJoinPATDoesNotRestartAValidJoin(t *testing.T) {
	c, b, _ := h264Ingest(t)
	pid := b.mediaPID
	var cc ccSeq
	key := cc.ts(pid, true, cat(pesHead, nal(0x67), nal(0x68), nal(0x65)))
	m := cc.ts(pid, true, cat(pesHead, nal(0x41))) // the next PES: the key is classified here
	pat2 := b.PAT()
	m2 := cc.ts(pid, false, []byte{4, 5, 6})
	feed(c, key, m, pat2, m2)
	if got, want := c.joinSnapshotLocked(), cat(pat2, b.PMT(), key, m, m2); !bytes.Equal(got[2*tsPacketSize:], want[2*tsPacketSize:]) || !bytes.Equal(got[:tsPacketSize], pat2) {
		t.Fatal("a mid-GOP PAT restarted the key join")
	}
}

func TestKeyJoinInvalidatedWhenTrimmed(t *testing.T) {
	c, b, _ := h264Ingest(t)
	pid := b.mediaPID
	var cc ccSeq
	feed(c, cc.ts(pid, true, cat(pesHead, nal(0x67), nal(0x68), nal(0x65))))
	filler := pesTS(pid, false, make([]byte, 184))
	n := ingestJoinBufMax/tsPacketSize + 10
	for i := 0; i < n; i++ {
		filler[3] = (filler[3] & 0xF0) | (byte(cc) & 0x0F)
		cc.skip()
		c.handleTSPacketLocked(filler)
		if i == 15 && !c.joinValid {
			t.Fatal("key was not classified once its scan buffer filled")
		}
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
	var cc ccSeq
	gop1 := cc.ts(0x31, true, cat(pesHead, []byte{0, 0, 1, 0xB3, 0x50, 0x02, 0xD0, 0x37}))
	mid := cc.ts(0x31, true, cat(pesHead, []byte{0, 0, 1, 0x00, 1}))
	gop2 := cc.ts(0x31, true, cat(pesHead, []byte{0, 0, 1, 0xB3, 0x50, 0x02, 0xD0, 0x37}))
	tail := cc.ts(0x31, false, []byte{4, 5, 6})
	next := cc.ts(0x31, true, cat(pesHead, []byte{0, 0, 1, 0x00, 2})) // classifies gop2
	feed(c, gop1, mid, gop2, tail, next)
	if got, want := c.joinSnapshotLocked(), cat(pat, pmt, gop2, tail, next); !bytes.Equal(got, want) {
		t.Fatal("join is not PAT+PMT+last sequence header and what follows")
	}
}

// 00 00 at the end of one packet and 01 B3 at the start of the next are one
// sequence header.
func TestKeyJoinMPEG2SplitAcrossPackets(t *testing.T) {
	c := newChannelIngest(nil, 1)
	b := newTSBuilder()
	b.pmtPID = 0x30
	pat, pmt := b.PAT(), pmtPacket() // MPEG-2 video on 0x31
	feed(c, pat, pmt)
	var cc ccSeq
	gop1 := cc.ts(0x31, true, cat(pesHead, []byte{0, 0, 1, 0xB3, 0x50, 0x02, 0xD0, 0x37}))
	mid := cc.ts(0x31, true, cat(pesHead, []byte{0, 0, 1, 0x00, 1}))
	p1 := cc.ts(0x31, true, cat(pesHead, make([]byte, 184-len(pesHead)-2), []byte{0, 0}))
	p2 := cc.ts(0x31, false, []byte{1, 0xB3, 0x50, 0x02, 0xD0, 0x37})
	next := cc.ts(0x31, true, cat(pesHead, []byte{0, 0, 1, 0x00, 2}))
	feed(c, gop1, mid, p1, p2, next)
	if got, want := c.joinSnapshotLocked(), cat(pat, pmt, p1, p2, next); !bytes.Equal(got, want) {
		t.Fatal("MPEG-2 sequence header split across packets was not the join")
	}
}

// HEVC (and any other codec that cannot key) is stored, not scanned.
func TestKeyJoinHEVCDoesNotScan(t *testing.T) {
	c := newChannelIngest(nil, 1)
	b := newTSBuilder()
	pat, pmt := b.PAT(), b.PMT()
	idx := bytes.IndexByte(pmt, 0x1B)
	if idx < 0 {
		t.Fatal("PMT has no H.264 stream type to retarget")
	}
	pmt[idx] = 0x24
	feed(c, pat, pmt)
	if c.videoStreamType != 0x24 {
		t.Fatalf("stream type %02x, want HEVC 0x24", c.videoStreamType)
	}
	var cc ccSeq
	feed(c, cc.ts(b.mediaPID, true, cat(pesHead, nal(0x67), nal(0x68), nal(0x65))))
	if len(c.pesScan) != 0 || len(c.pesHdr) != 0 {
		t.Fatalf("non-key codec was scanned (pesScan=%d pesHdr=%d)", len(c.pesScan), len(c.pesHdr))
	}
	if len(c.sinceJoin) != tsPacketSize {
		t.Fatalf("video packet was not kept (sinceJoin=%d)", len(c.sinceJoin))
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
		{"h264 forbidden zero bit", []byte{0, 0, 1, 0xE5, 0x88}, 0x1B, nalFlags{}},
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

// A PES header split after its first six bytes must not be scanned as
// elementary stream. Stream id 0xE5 is 00 00 01 E5, which is NAL type 5 if
// the header leaks into the scan; SPS+PPS+a non-IDR after it is not a key
// and must not replace one.
func TestKeyJoinSplitPESHeaderDoesNotFakeIDR(t *testing.T) {
	c, b, tables := h264Ingest(t)
	pid := b.mediaPID
	var cc ccSeq
	good := cc.ts(pid, true, cat(pesHead, nal(0x67), nal(0x68), nal(0x65)))
	cls := cc.ts(pid, true, cat(pesHead, nal(0x41)))
	hdr := []byte{0, 0, 1, 0xE5, 0, 0, 0x80, 0x80, 0x05, 0x21, 0, 1, 0, 1}
	p1 := cc.ts(pid, true, hdr[:6])
	p2 := cc.ts(pid, false, cat(hdr[6:], nal(0x67), nal(0x68), nal(0x41)))
	follow := cc.ts(pid, true, cat(pesHead, nal(0x41)))
	feed(c, good, cls, p1, p2, follow)
	if got, want := c.joinSnapshotLocked(), cat(tables, good, cls, p1, p2, follow); !bytes.Equal(got, want) {
		t.Fatalf("split PES header displaced the previous key (len=%d want %d)", len(got), len(want))
	}
}

// A missing video packet must not splice the bytes on either side into a
// start code. 00 00 | gap | 01 65 is not an IDR, and the previous key stays.
func TestKeyJoinCCGapDoesNotSpliceIDR(t *testing.T) {
	c, b, tables := h264Ingest(t)
	pid := b.mediaPID
	var cc ccSeq
	good := cc.ts(pid, true, cat(pesHead, nal(0x67), nal(0x68), nal(0x65)))
	cls := cc.ts(pid, true, cat(pesHead, nal(0x41)))
	head := cc.ts(pid, true, cat(pesHead, nal(0x67), nal(0x68), []byte{0, 0}))
	cc.skip() // the packet that never arrived
	tail := cc.ts(pid, false, []byte{1, 0x65, 0x88})
	follow := cc.ts(pid, true, cat(pesHead, nal(0x41)))
	feed(c, good, cls, head, tail, follow)
	got := c.joinSnapshotLocked()
	if !c.joinValid || !bytes.HasPrefix(got, cat(tables, good)) {
		t.Fatalf("CC gap spliced a fake IDR over the previous key (valid=%v len=%d)", c.joinValid, len(got))
	}
}

// A key promoted at the 16th payload (the scan cap) is still the same PES.
// A continuity gap at payload 20 must drop that key.
func TestKeyJoinDamageAfterPromotionInvalidates(t *testing.T) {
	c, b, _ := h264Ingest(t)
	pid := b.mediaPID
	var cc ccSeq
	// 184 elementary-stream bytes per payload (4-byte start codes, so the
	// PES-header parser does not consume them). 16 payloads fill keyScanBytes.
	first := make([]byte, 184)
	copy(first, cat(nal(0x67), nal(0x68), nal(0x65)))
	c.handleTSPacketLocked(cc.ts(pid, true, first))
	for n := 2; n <= 16; n++ {
		c.handleTSPacketLocked(cc.ts(pid, false, make([]byte, 184)))
	}
	if !c.joinValid {
		t.Fatal("key PES was not promoted once its 16th payload filled the scan")
	}
	for n := 17; n <= 19; n++ {
		c.handleTSPacketLocked(cc.ts(pid, false, make([]byte, 184)))
	}
	cc.skip()
	c.handleTSPacketLocked(cc.ts(pid, false, make([]byte, 184))) // payload 20
	if c.joinValid {
		t.Fatal("CC gap after promotion left the damaged key valid")
	}
}

// feedLongNonKeyAfterGoodKey establishes a short key, then a non-key PES that
// has already been classified at the scan cap. Damage belongs to that PES,
// not to the key.
func feedLongNonKeyAfterGoodKey(t *testing.T, c *channelIngest, pid uint16, cc *ccSeq) []byte {
	t.Helper()
	good := cc.ts(pid, true, cat(pesHead, nal(0x67), nal(0x68), nal(0x65)))
	first := make([]byte, 184)
	copy(first, nal(0x41)) // non-IDR; 4-byte start code, so the whole packet is ES
	c.handleTSPacketLocked(good)
	c.handleTSPacketLocked(cc.ts(pid, true, first))
	if !c.joinValid {
		t.Fatal("setup: short key was not classified")
	}
	for n := 2; n <= 20; n++ {
		c.handleTSPacketLocked(cc.ts(pid, false, make([]byte, 184)))
	}
	if !c.joinValid || !c.pesDone {
		t.Fatalf("setup: non-key PES was not classified past the scan cap (valid=%v done=%v)", c.joinValid, c.pesDone)
	}
	return good
}

// A large P/B-frame PES is classified at the scan cap without becoming the
// join. Loss in its tail must not discard the earlier key.
func TestKeyJoinNonKeyTailCCGapKeepsPreviousKey(t *testing.T) {
	c, b, tables := h264Ingest(t)
	var cc ccSeq
	good := feedLongNonKeyAfterGoodKey(t, c, b.mediaPID, &cc)
	cc.skip()
	c.handleTSPacketLocked(cc.ts(b.mediaPID, false, make([]byte, 184)))
	got := c.joinSnapshotLocked()
	if !c.joinValid || !bytes.HasPrefix(got, cat(tables, good)) {
		t.Fatalf("CC gap in a non-key PES discarded the previous key (valid=%v len=%d)", c.joinValid, len(got))
	}
}

func TestKeyJoinNonKeyTailTEIKeepsPreviousKey(t *testing.T) {
	c, b, tables := h264Ingest(t)
	var cc ccSeq
	good := feedLongNonKeyAfterGoodKey(t, c, b.mediaPID, &cc)
	tei := cc.ts(b.mediaPID, false, []byte{1, 2, 3, 4})
	tei[1] |= 0x80
	c.handleTSPacketLocked(tei)
	got := c.joinSnapshotLocked()
	if !c.joinValid || !bytes.HasPrefix(got, cat(tables, good)) {
		t.Fatalf("TEI in a non-key PES discarded the previous key (valid=%v len=%d)", c.joinValid, len(got))
	}
}

// A discontinuity on the next PES start is a splice, not a reason to classify
// the previous PES. Its missing tail must not replace the last good key.
func TestKeyJoinDiscontinuityPUSIKeepsPreviousKey(t *testing.T) {
	c, b, tables := h264Ingest(t)
	pid := b.mediaPID
	var cc ccSeq
	good := cc.ts(pid, true, cat(pesHead, nal(0x67), nal(0x68), nal(0x65)))
	cls := cc.ts(pid, true, cat(pesHead, nal(0x41)))
	cand := cc.ts(pid, true, cat(pesHead, nal(0x67), nal(0x68), nal(0x65)))
	cc.skip() // the candidate's tail never arrived
	next := cc.ts(pid, true, cat(pesHead, nal(0x41)))
	next[5] |= 0x80 // discontinuity_indicator
	feed(c, good, cls, cand, next)
	got := c.joinSnapshotLocked()
	if !c.joinValid || !bytes.HasPrefix(got, cat(tables, good)) {
		t.Fatalf("discontinuity promoted the truncated candidate (valid=%v len=%d)", c.joinValid, len(got))
	}
}

// A transport_error_indicator packet inside a candidate PES abandons it.
func TestKeyJoinTEIAbandonsCandidate(t *testing.T) {
	c, b, tables := h264Ingest(t)
	pid := b.mediaPID
	var cc ccSeq
	good := cc.ts(pid, true, cat(pesHead, nal(0x67), nal(0x68), nal(0x65)))
	cls := cc.ts(pid, true, cat(pesHead, nal(0x41)))
	cand := cc.ts(pid, true, cat(pesHead, nal(0x67), nal(0x68), nal(0x65)))
	tei := cc.ts(pid, false, []byte{1, 2, 3, 4})
	tei[1] |= 0x80
	follow := cc.ts(pid, true, cat(pesHead, nal(0x41)))
	feed(c, good, cls, cand, tei, follow)
	got := c.joinSnapshotLocked()
	if !c.joinValid || !bytes.HasPrefix(got, cat(tables, good)) {
		t.Fatalf("TEI packet let a damaged candidate replace the previous key (valid=%v len=%d)", c.joinValid, len(got))
	}
}

// One identical copy of a video packet is legal and must not abandon the key.
func TestKeyJoinDuplicatePacketKeepsKey(t *testing.T) {
	c, b, tables := h264Ingest(t)
	pid := b.mediaPID
	var cc ccSeq
	head := cc.ts(pid, true, cat(pesHead, nal(0x67), nal(0x68), []byte{0, 0}))
	idr := cc.ts(pid, false, []byte{1, 0x65, 0x88})
	dup := cc.dup(idr)
	cls := cc.ts(pid, true, cat(pesHead, nal(0x41)))
	feed(c, head, idr, dup, cls)
	got := c.joinSnapshotLocked()
	if !c.joinValid || !bytes.HasPrefix(got, cat(tables, head)) {
		t.Fatalf("duplicate packet abandoned the key (valid=%v len=%d)", c.joinValid, len(got))
	}
}

// A repeated payload is not a legal duplicate when transport_error_indicator
// is set. It abandons the open candidate.
func TestKeyJoinTEIDuplicateAbandonsCandidate(t *testing.T) {
	c, b, tables := h264Ingest(t)
	pid := b.mediaPID
	var cc ccSeq
	good := cc.ts(pid, true, cat(pesHead, nal(0x67), nal(0x68), nal(0x65)))
	cls := cc.ts(pid, true, cat(pesHead, nal(0x41)))
	cand := cc.ts(pid, true, cat(pesHead, nal(0x67), nal(0x68), nal(0x65)))
	tei := cc.dup(cand)
	tei[1] |= 0x80
	follow := cc.ts(pid, true, cat(pesHead, nal(0x41)))
	feed(c, good, cls, cand, tei, follow)
	got := c.joinSnapshotLocked()
	if !c.joinValid || !bytes.HasPrefix(got, cat(tables, good)) {
		t.Fatalf("TEI duplicate did not abandon the candidate (valid=%v len=%d)", c.joinValid, len(got))
	}
}

// An IDR on a new video PID must not become a key by borrowing the previous
// PID's SPS and PPS.
func TestKeyJoinPMTChangeDropsOldParameterSets(t *testing.T) {
	c, b, _ := h264Ingest(t)
	pidA := b.mediaPID
	var cc ccSeq
	feed(c,
		cc.ts(pidA, true, cat(pesHead, nal(0x67), nal(0x68), nal(0x65))),
		cc.ts(pidA, true, cat(pesHead, nal(0x41))),
	)
	if !c.joinValid {
		t.Fatal("setup: H.264 key on PID A did not join")
	}
	const pidB = uint16(0x0102)
	b.mediaPID = pidB
	feed(c, b.PMT(),
		cc.ts(pidB, true, cat(pesHead, nal(0x65))),
		cc.ts(pidB, true, cat(pesHead, nal(0x41))),
	)
	if c.joinValid {
		t.Fatal("IDR on the new video PID reused the old PID's SPS/PPS")
	}
}

// A reconnect used to keep a valid pre-outage key. The next PAT must not:
// while joinValid, a PAT does not restart the buffer, so a sub would get the
// old GOP and then a jump the length of the outage.
func TestReconnectDropsStaleKey(t *testing.T) {
	clock := newIngestClock(time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC))
	b := newTSBuilder()
	pid := b.mediaPID
	pat := b.PAT()
	pmt := b.PMT()
	var cc ccSeq
	key := cc.ts(pid, true, cat(pesHead, nal(0x67), nal(0x68), nal(0x65)))
	next := cc.ts(pid, true, cat(pesHead, nal(0x41)))
	first := cat(pat, pmt, key, next)

	var phase atomic.Int64
	var second atomic.Pointer[pipeBody]
	im, _ := newTestIngest(t, func(ctx context.Context, url string) (io.ReadCloser, int, error) {
		pb := newPipeBody()
		if phase.Add(1) == 1 {
			go func() {
				_, _ = pb.Write(first)
				pb.CloseWrite(io.EOF)
			}()
		} else {
			second.Store(pb)
		}
		return pb, 200, nil
	}, clock)

	sub, err := im.Attach(context.Background(), 1, "u")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sub.Close() }()
	go func() { _, _ = io.Copy(io.Discard, sub.R) }()

	im.mu.Lock()
	ch := im.channels[1]
	im.mu.Unlock()
	waitCond(t, 2*time.Second, func() bool {
		ch.mu.Lock()
		defer ch.mu.Unlock()
		return ch.joinValid
	})
	waitCond(t, 2*time.Second, func() bool {
		return clock.HasTimerIn(ingestReconnectMin)
	})
	clock.Advance(ingestReconnectMin + time.Millisecond)
	var body *pipeBody
	waitCond(t, 2*time.Second, func() bool {
		body = second.Load()
		return body != nil
	})
	pat2 := b.PAT()
	if _, err := body.Write(pat2); err != nil {
		t.Fatal(err)
	}
	waitCond(t, 2*time.Second, func() bool {
		ch.mu.Lock()
		defer ch.mu.Unlock()
		return bytes.Equal(ch.lastPAT, pat2)
	})
	ch.mu.Lock()
	got := ch.joinSnapshotLocked()
	valid := ch.joinValid
	ch.mu.Unlock()
	if len(got) != 2*tsPacketSize || valid {
		t.Fatalf("after reconnect, the next PAT must be a 2-packet since-PAT join (len=%d joinValid=%v)", len(got), valid)
	}
}

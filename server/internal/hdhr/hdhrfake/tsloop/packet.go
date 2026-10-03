// Package tsloop turns a finite MPEG-TS file into an endless, real-time
// broadcast timeline: it loops the file while rewriting PCR/PTS/DTS and
// continuity counters so the stream never jumps back, like a real tuner.
package tsloop

// PacketSize is the MPEG-TS packet length.
const PacketSize = 188

// tsMask wraps 33-bit PTS/DTS/PCR-base values.
const tsMask = 1<<33 - 1

// PID returns the packet identifier.
func PID(p []byte) uint16 { return uint16(p[1]&0x1F)<<8 | uint16(p[2]) }

// PUSI reports the payload_unit_start_indicator.
func PUSI(p []byte) bool { return p[1]&0x40 != 0 }

// CC returns the continuity counter.
func CC(p []byte) byte { return p[3] & 0x0F }

// SetCC sets the continuity counter (low nibble of cc), keeping the other bits.
func SetCC(p []byte, cc byte) { p[3] = p[3]&0xF0 | cc&0x0F }

// HasPayload reports whether adaptation_field_control includes a payload.
func HasPayload(p []byte) bool { return p[3]&0x10 != 0 }

func hasAdaptation(p []byte) bool { return p[3]&0x20 != 0 }

// payloadOffset returns the payload start, or -1 when there is none.
func payloadOffset(p []byte) int {
	if !HasPayload(p) {
		return -1
	}
	if !hasAdaptation(p) {
		return 4
	}
	off := 5 + int(p[4])
	if off >= PacketSize {
		return -1
	}
	return off
}

// PCR returns the program clock reference when the adaptation field has one.
func PCR(p []byte) (base int64, ext int, ok bool) {
	if !hasAdaptation(p) || p[4] < 7 || p[5]&0x10 == 0 {
		return 0, 0, false
	}
	b := p[6:12]
	base = int64(b[0])<<25 | int64(b[1])<<17 | int64(b[2])<<9 | int64(b[3])<<1 | int64(b[4]>>7)
	ext = int(b[4]&1)<<8 | int(b[5])
	return base, ext, true
}

// SetPCR overwrites an existing PCR (base wrapped to 33 bits). The caller must
// only use it on packets where PCR reports ok.
func SetPCR(p []byte, base int64, ext int) {
	base &= tsMask
	b := p[6:12]
	b[0] = byte(base >> 25)
	b[1] = byte(base >> 17)
	b[2] = byte(base >> 9)
	b[3] = byte(base >> 1)
	b[4] = byte(base&1)<<7 | 0x7E | byte(ext>>8)&1
	b[5] = byte(ext)
}

// StreamID returns the PES stream_id when this packet starts a PES.
func StreamID(p []byte) (byte, bool) {
	off := pesStart(p)
	if off < 0 {
		return 0, false
	}
	return p[off+3], true
}

func pesStart(p []byte) int {
	if !PUSI(p) {
		return -1
	}
	off := payloadOffset(p)
	if off < 0 || off+9 > PacketSize {
		return -1
	}
	if p[off] != 0 || p[off+1] != 0 || p[off+2] != 1 {
		return -1
	}
	return off
}

// noHeaderStream reports PES stream ids that carry no optional PES header.
func noHeaderStream(sid byte) bool {
	switch sid {
	case 0xBC, 0xBE, 0xBF, 0xF0, 0xF1, 0xF2, 0xF8, 0xFF:
		return true
	}
	return false
}

// pesTSOffsets returns the offsets of the PTS and DTS fields (-1 if absent).
func pesTSOffsets(p []byte) (ptsOff, dtsOff int) {
	off := pesStart(p)
	if off < 0 || noHeaderStream(p[off+3]) {
		return -1, -1
	}
	flags := p[off+7] >> 6
	ptsOff, dtsOff = -1, -1
	if flags&0x2 != 0 && off+14 <= PacketSize {
		ptsOff = off + 9
	}
	if flags == 0x3 && off+19 <= PacketSize {
		dtsOff = off + 14
	}
	return ptsOff, dtsOff
}

// PESTimestamps returns the PES PTS/DTS when this packet starts a PES.
func PESTimestamps(p []byte) (pts, dts int64, hasPTS, hasDTS bool) {
	ptsOff, dtsOff := pesTSOffsets(p)
	if ptsOff >= 0 {
		pts, hasPTS = readTS(p[ptsOff:]), true
	}
	if dtsOff >= 0 {
		dts, hasDTS = readTS(p[dtsOff:]), true
	}
	return pts, dts, hasPTS, hasDTS
}

// SetPESTimestamps rewrites whichever of PTS/DTS the PES header carries.
func SetPESTimestamps(p []byte, pts, dts int64) {
	ptsOff, dtsOff := pesTSOffsets(p)
	if ptsOff >= 0 {
		writeTS(p[ptsOff:], pts)
	}
	if dtsOff >= 0 {
		writeTS(p[dtsOff:], dts)
	}
}

func readTS(b []byte) int64 {
	return int64(b[0]>>1&0x07)<<30 | int64(b[1])<<22 | int64(b[2]>>1)<<15 | int64(b[3])<<7 | int64(b[4]>>1)
}

func writeTS(b []byte, ts int64) {
	ts &= tsMask
	b[0] = b[0]&0xF1 | byte(ts>>29)&0x0E
	b[1] = byte(ts >> 22)
	b[2] = byte(ts>>14)&0xFE | 1
	b[3] = byte(ts >> 7)
	b[4] = byte(ts<<1)&0xFE | 1
}

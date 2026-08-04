package hdhr

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"strings"
	"time"
)

// Protocol constants from libhdhomerun hdhomerun_pkt.h
// https://github.com/Silicondust/libhdhomerun/blob/master/hdhomerun_pkt.h
const (
	discoverUDPPort = 65001

	typeDiscoverReq uint16 = 0x0002
	typeDiscoverRpy uint16 = 0x0003

	tagDeviceType  uint8 = 0x01
	tagDeviceID    uint8 = 0x02
	tagTunerCount  uint8 = 0x10
	tagLineupURL   uint8 = 0x27
	tagBaseURL     uint8 = 0x2A

	deviceTypeTuner  uint32 = 0x00000001
	deviceIDWildcard uint32 = 0xFFFFFFFF
)

// Discover broadcasts a libhdhomerun discover request on UDP port 65001 and
// collects replies until timeout. On non-socket errors it returns whatever was
// found with a nil error; only a failure to create/bind the socket is fatal.
func Discover(ctx context.Context, timeout time.Duration) ([]DiscoverInfo, error) {
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	deadline := time.Now().Add(timeout)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}

	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		return nil, fmt.Errorf("hdhr discover listen: %w", err)
	}
	defer conn.Close()

	if err := conn.SetWriteDeadline(time.Now().Add(2 * time.Second)); err != nil {
		return nil, fmt.Errorf("hdhr discover write deadline: %w", err)
	}

	req := encodeDiscoverRequest(deviceTypeTuner, deviceIDWildcard)
	dst := &net.UDPAddr{IP: net.IPv4bcast, Port: discoverUDPPort}
	if _, err := conn.WriteToUDP(req, dst); err != nil {
		// Broadcast may fail on some interfaces; try again is not required —
		// still listen in case unicast replies arrive from a prior attempt.
		// But if we cannot send at all and get nothing, return empty + nil.
		_ = err
	}

	if err := conn.SetReadDeadline(deadline); err != nil {
		return nil, fmt.Errorf("hdhr discover read deadline: %w", err)
	}

	seen := map[string]DiscoverInfo{}
	buf := make([]byte, 2048)
	for {
		n, addr, err := conn.ReadFromUDP(buf)
		if err != nil {
			// timeout or cancel → return what we have
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				break
			}
			if ctx.Err() != nil {
				break
			}
			// other read errors: stop, still return found
			break
		}
		info, _, decErr := decodeDiscoverReply(buf[:n], addr.IP.String())
		if decErr != nil {
			continue
		}
		key := info.DeviceID
		if key == "" {
			key = info.BaseURL
		}
		if key == "" {
			key = addr.IP.String()
		}
		// Prefer reply with BaseURL; fill fixup if missing.
		if info.BaseURL == "" {
			info.BaseURL = "http://" + addr.IP.String()
		}
		if info.LineupURL == "" && info.BaseURL != "" {
			info.LineupURL = strings.TrimRight(info.BaseURL, "/") + "/lineup.json"
		}
		seen[key] = info
	}

	out := make([]DiscoverInfo, 0, len(seen))
	for _, v := range seen {
		out = append(out, v)
	}
	return out, nil
}

type tlv struct {
	tag   uint8
	value []byte
}

type discoverReply struct {
	DeviceType uint32
	DeviceID   uint32
	TunerCount int
	BaseURL    string
	LineupURL  string
}

// encodeDiscoverRequest builds a sealed DISCOVER_REQ frame.
// Packet format (hdhomerun_pkt.h): type(u16 BE) | len(u16 BE) | payload | crc(u32 LE).
// Payload TLVs: DEVICE_TYPE + DEVICE_ID (when not wildcard, lib also omits ID for wildcard
// in newer code — we always include both tags for maximum compatibility with older firmware).
func encodeDiscoverRequest(deviceType, deviceID uint32) []byte {
	payload := appendTLV(nil, tagDeviceType, u32be(deviceType))
	payload = appendTLV(payload, tagDeviceID, u32be(deviceID))
	return sealFrame(typeDiscoverReq, payload)
}

func encodeDiscoverReply(r discoverReply) []byte {
	payload := appendTLV(nil, tagDeviceType, u32be(r.DeviceType))
	payload = appendTLV(payload, tagDeviceID, u32be(r.DeviceID))
	if r.TunerCount > 0 {
		payload = appendTLV(payload, tagTunerCount, []byte{byte(r.TunerCount)})
	}
	if r.BaseURL != "" {
		payload = appendTLV(payload, tagBaseURL, []byte(r.BaseURL))
	}
	if r.LineupURL != "" {
		payload = appendTLV(payload, tagLineupURL, []byte(r.LineupURL))
	}
	return sealFrame(typeDiscoverRpy, payload)
}

func decodeDiscoverReply(pkt []byte, fromIP string) (DiscoverInfo, string, error) {
	frameType, tags, err := openFrame(pkt)
	if err != nil {
		return DiscoverInfo{}, fromIP, err
	}
	if frameType != typeDiscoverRpy {
		return DiscoverInfo{}, fromIP, fmt.Errorf("unexpected frame type 0x%04x", frameType)
	}

	var info DiscoverInfo
	var deviceID uint32
	var sawID bool
	for _, t := range tags {
		switch t.tag {
		case tagDeviceID:
			if len(t.value) == 4 {
				deviceID = binary.BigEndian.Uint32(t.value)
				sawID = true
			}
		case tagTunerCount:
			if len(t.value) >= 1 {
				info.TunerCount = int(t.value[0])
			}
		case tagBaseURL:
			info.BaseURL = string(t.value)
		case tagLineupURL:
			info.LineupURL = string(t.value)
		}
	}
	if sawID {
		info.DeviceID = fmt.Sprintf("%08X", deviceID)
	}
	return info, fromIP, nil
}

func appendTLV(dst []byte, tag uint8, value []byte) []byte {
	dst = append(dst, tag)
	dst = appendVarLength(dst, len(value))
	dst = append(dst, value...)
	return dst
}

func appendVarLength(dst []byte, n int) []byte {
	if n <= 127 {
		return append(dst, byte(n))
	}
	// Two-byte length: low 7 bits | 0x80, then high bits.
	return append(dst, byte(n)|0x80, byte(n>>7))
}

func u32be(v uint32) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, v)
	return b
}

// sealFrame prepends type+length and appends CRC32 (little-endian), matching
// hdhomerun_pkt_seal_frame in libhdhomerun.
func sealFrame(frameType uint16, payload []byte) []byte {
	out := make([]byte, 0, 4+len(payload)+4)
	out = append(out, byte(frameType>>8), byte(frameType))
	n := len(payload)
	out = append(out, byte(n>>8), byte(n))
	out = append(out, payload...)
	crc := calcCRC(out)
	out = append(out, byte(crc), byte(crc>>8), byte(crc>>16), byte(crc>>24))
	return out
}

// openFrame validates CRC and returns frame type + TLV list (payload only).
func openFrame(pkt []byte) (uint16, []tlv, error) {
	if len(pkt) < 8 {
		return 0, nil, fmt.Errorf("packet too short")
	}
	frameType := binary.BigEndian.Uint16(pkt[0:2])
	payloadLen := int(binary.BigEndian.Uint16(pkt[2:4]))
	if 4+payloadLen+4 > len(pkt) {
		return 0, nil, fmt.Errorf("truncated length")
	}
	frameEnd := 4 + payloadLen
	calc := calcCRC(pkt[:frameEnd])
	got := uint32(pkt[frameEnd]) |
		uint32(pkt[frameEnd+1])<<8 |
		uint32(pkt[frameEnd+2])<<16 |
		uint32(pkt[frameEnd+3])<<24
	if calc != got {
		return 0, nil, fmt.Errorf("crc mismatch")
	}
	payload := pkt[4:frameEnd]
	tags, err := parseTLVs(payload)
	if err != nil {
		return 0, nil, err
	}
	return frameType, tags, nil
}

func parseTLVs(payload []byte) ([]tlv, error) {
	var tags []tlv
	i := 0
	for i < len(payload) {
		if i+1 > len(payload) {
			return nil, fmt.Errorf("truncated tlv")
		}
		tag := payload[i]
		i++
		if i >= len(payload) {
			return nil, fmt.Errorf("truncated tlv length")
		}
		length := int(payload[i])
		i++
		if length&0x80 != 0 {
			if i >= len(payload) {
				return nil, fmt.Errorf("truncated tlv length2")
			}
			length = (length & 0x7F) | (int(payload[i]) << 7)
			i++
		}
		if i+length > len(payload) {
			return nil, fmt.Errorf("truncated tlv value")
		}
		val := make([]byte, length)
		copy(val, payload[i:i+length])
		tags = append(tags, tlv{tag: tag, value: val})
		i += length
	}
	return tags, nil
}

// calcCRC is the Silicondust packet CRC from hdhomerun_pkt.c (Ethernet-style
// bit-sliced polynomial, not the standard IEEE table CRC-32).
func calcCRC(data []byte) uint32 {
	crc := uint32(0xFFFFFFFF)
	for _, b := range data {
		x := uint8(crc) ^ b
		crc >>= 8
		if x&0x01 != 0 {
			crc ^= 0x77073096
		}
		if x&0x02 != 0 {
			crc ^= 0xEE0E612C
		}
		if x&0x04 != 0 {
			crc ^= 0x076DC419
		}
		if x&0x08 != 0 {
			crc ^= 0x0EDB8832
		}
		if x&0x10 != 0 {
			crc ^= 0x1DB71064
		}
		if x&0x20 != 0 {
			crc ^= 0x3B6E20C8
		}
		if x&0x40 != 0 {
			crc ^= 0x76DC4190
		}
		if x&0x80 != 0 {
			crc ^= 0xEDB88320
		}
	}
	return crc ^ 0xFFFFFFFF
}

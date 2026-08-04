package hdhr

import (
	"encoding/binary"
	"testing"
)

func TestDiscoverPacketRoundTrip(t *testing.T) {
	// Encode a discover request (tuner + wildcard device id) and parse it back.
	req := encodeDiscoverRequest(deviceTypeTuner, deviceIDWildcard)
	if len(req) < 8 {
		t.Fatalf("request too short: %d bytes", len(req))
	}

	// Packet type (big-endian) must be DISCOVER_REQ.
	ptype := binary.BigEndian.Uint16(req[0:2])
	if ptype != typeDiscoverReq {
		t.Errorf("req type = 0x%04x, want 0x%04x", ptype, typeDiscoverReq)
	}
	payloadLen := binary.BigEndian.Uint16(req[2:4])
	if int(payloadLen)+8 != len(req) {
		t.Errorf("payloadLen=%d total=%d", payloadLen, len(req))
	}

	frameType, tags, err := openFrame(req)
	if err != nil {
		t.Fatalf("openFrame request: %v", err)
	}
	if frameType != typeDiscoverReq {
		t.Errorf("openFrame type = 0x%04x", frameType)
	}
	var gotType, gotID uint32
	var sawType, sawID bool
	for _, tag := range tags {
		switch tag.tag {
		case tagDeviceType:
			if len(tag.value) != 4 {
				t.Fatalf("device type len = %d", len(tag.value))
			}
			gotType = binary.BigEndian.Uint32(tag.value)
			sawType = true
		case tagDeviceID:
			if len(tag.value) != 4 {
				t.Fatalf("device id len = %d", len(tag.value))
			}
			gotID = binary.BigEndian.Uint32(tag.value)
			sawID = true
		}
	}
	if !sawType || gotType != deviceTypeTuner {
		t.Errorf("device type: saw=%v got=0x%08x", sawType, gotType)
	}
	if !sawID || gotID != deviceIDWildcard {
		t.Errorf("device id: saw=%v got=0x%08x", sawID, gotID)
	}

	// Hand-build a response with our encoder, then parse via decodeDiscoverReply.
	reply := encodeDiscoverReply(discoverReply{
		DeviceType: deviceTypeTuner,
		DeviceID:   0xAABBCCDD,
		TunerCount: 4,
		BaseURL:    "http://192.168.1.50",
		LineupURL:  "http://192.168.1.50/lineup.json",
	})
	info, fromIP, err := decodeDiscoverReply(reply, "192.168.1.50")
	if err != nil {
		t.Fatalf("decodeDiscoverReply: %v", err)
	}
	if fromIP != "192.168.1.50" {
		t.Errorf("fromIP = %q", fromIP)
	}
	if info.DeviceID != "AABBCCDD" {
		t.Errorf("DeviceID = %q", info.DeviceID)
	}
	if info.TunerCount != 4 {
		t.Errorf("TunerCount = %d", info.TunerCount)
	}
	if info.BaseURL != "http://192.168.1.50" {
		t.Errorf("BaseURL = %q", info.BaseURL)
	}
	if info.LineupURL != "http://192.168.1.50/lineup.json" {
		t.Errorf("LineupURL = %q", info.LineupURL)
	}

	// CRC must reject a corrupted packet.
	bad := append([]byte(nil), reply...)
	bad[len(bad)-1] ^= 0xFF
	if _, _, err := decodeDiscoverReply(bad, "192.168.1.50"); err == nil {
		t.Fatal("expected CRC error on corrupted reply")
	}
}

func TestStreamPortFromBaseURL(t *testing.T) {
	// Note: streamPortFromBaseURL lives in client.go as a shared helper used by the
	// tuner manager path; tested here at the packet/client layer for the rule itself.
	cases := []struct {
		base string
		want int
	}{
		{"http://1.2.3.4:80", 5004},
		{"http://1.2.3.4", 5004},
		{"http://1.2.3.4/", 5004},
		{"http://127.0.0.1:54321", 54321},
		{"http://192.168.1.50:8080", 8080},
	}
	for _, tc := range cases {
		got := StreamPortFromBaseURL(tc.base)
		if got != tc.want {
			t.Errorf("StreamPortFromBaseURL(%q) = %d, want %d", tc.base, got, tc.want)
		}
	}
}

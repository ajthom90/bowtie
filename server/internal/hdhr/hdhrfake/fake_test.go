package hdhrfake_test

import (
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/ajthom90/bowtie/server/internal/hdhr/hdhrfake"
)

func TestFakeServesDiscoverLineupStatus(t *testing.T) {
	lineup := []hdhrfake.LineupEntry{
		{GuideNumber: "5.1", GuideName: "WABC", URL: "http://fake/auto/v5.1", VideoCodec: "MPEG2", AudioCodec: "AC3"},
		{GuideNumber: "7.1", GuideName: "WXYZ", URL: "http://fake/auto/v7.1", VideoCodec: "MPEG2", AudioCodec: "AC3"},
	}
	f := hdhrfake.New(t, hdhrfake.Options{
		DeviceID:   "AABBCCDD",
		TunerCount: 2,
		Lineup:     lineup,
	})

	// discover.json
	resp, err := http.Get(f.URL + "/discover.json")
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("discover status = %d", resp.StatusCode)
	}
	var disc map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&disc); err != nil {
		t.Fatalf("decode discover: %v", err)
	}
	if disc["FriendlyName"] != "HDHomeRun FAKE" {
		t.Errorf("FriendlyName = %v", disc["FriendlyName"])
	}
	if disc["ModelNumber"] != "HDFX-4US" {
		t.Errorf("ModelNumber = %v", disc["ModelNumber"])
	}
	if disc["DeviceID"] != "AABBCCDD" {
		t.Errorf("DeviceID = %v", disc["DeviceID"])
	}
	if disc["FirmwareVersion"] != "20260101" {
		t.Errorf("FirmwareVersion = %v", disc["FirmwareVersion"])
	}
	if int(disc["TunerCount"].(float64)) != 2 {
		t.Errorf("TunerCount = %v", disc["TunerCount"])
	}
	if disc["BaseURL"] != f.URL {
		t.Errorf("BaseURL = %v, want %s", disc["BaseURL"], f.URL)
	}
	if disc["LineupURL"] != f.URL+"/lineup.json" {
		t.Errorf("LineupURL = %v", disc["LineupURL"])
	}

	// lineup.json
	resp, err = http.Get(f.URL + "/lineup.json")
	if err != nil {
		t.Fatalf("lineup: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var gotLineup []hdhrfake.LineupEntry
	if err := json.NewDecoder(resp.Body).Decode(&gotLineup); err != nil {
		t.Fatalf("decode lineup: %v", err)
	}
	if len(gotLineup) != 2 || gotLineup[0].GuideNumber != "5.1" || gotLineup[1].GuideName != "WXYZ" {
		t.Fatalf("lineup = %+v", gotLineup)
	}

	// status.json (idle)
	resp, err = http.Get(f.URL + "/status.json")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var status []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if len(status) != 2 {
		t.Fatalf("status len = %d, want 2", len(status))
	}
	if status[0]["Resource"] != "tuner0" {
		t.Errorf("status[0].Resource = %v", status[0]["Resource"])
	}
}

func TestFakeStreamsAndCountsTuners(t *testing.T) {
	f := hdhrfake.New(t, hdhrfake.Options{
		DeviceID:   "AABBCCDD",
		TunerCount: 2,
		Lineup: []hdhrfake.LineupEntry{
			{GuideNumber: "5.1", GuideName: "WABC", URL: "", VideoCodec: "MPEG2", AudioCodec: "AC3"},
		},
	})

	type result struct {
		n   int
		err error
	}
	var wg sync.WaitGroup
	results := make(chan result, 2)

	// Open two concurrent streams — both should get bytes.
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := http.Get(f.URL + "/auto/v5.1")
			if err != nil {
				results <- result{err: err}
				return
			}
			// Read some bytes then leave the body open briefly so ActiveStreams stays high.
			buf := make([]byte, 188*10)
			n, err := io.ReadFull(resp.Body, buf)
			if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
				_ = resp.Body.Close()
				results <- result{err: err}
				return
			}
			// Hold the connection open a bit so the third request sees both tuners busy.
			time.Sleep(200 * time.Millisecond)
			_ = resp.Body.Close()
			results <- result{n: n}
		}()
	}

	// Wait until both streams are active, then try a third.
	deadline := time.Now().Add(2 * time.Second)
	for f.ActiveStreams() < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if f.ActiveStreams() != 2 {
		t.Fatalf("ActiveStreams = %d, want 2 before third request", f.ActiveStreams())
	}

	resp3, err := http.Get(f.URL + "/auto/v5.1")
	if err != nil {
		t.Fatalf("third stream: %v", err)
	}
	body3, _ := io.ReadAll(resp3.Body)
	_ = resp3.Body.Close()
	if resp3.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("third stream status = %d, want 503, body=%q", resp3.StatusCode, body3)
	}
	if string(body3) != "all tuners in use" {
		t.Fatalf("third stream body = %q, want %q", body3, "all tuners in use")
	}

	wg.Wait()
	close(results)
	for r := range results {
		if r.err != nil {
			t.Fatalf("stream error: %v", r.err)
		}
		if r.n < 188 {
			t.Fatalf("stream bytes = %d, want at least one TS packet", r.n)
		}
	}

	// After both closed, ActiveStreams should drop.
	deadline = time.Now().Add(2 * time.Second)
	for f.ActiveStreams() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if f.ActiveStreams() != 0 {
		t.Fatalf("ActiveStreams after close = %d, want 0", f.ActiveStreams())
	}
}

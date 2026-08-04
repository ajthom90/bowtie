package hdhr_test

import (
	"context"
	"testing"
	"time"

	"github.com/ajthom90/bowtie/server/internal/hdhr"
	"github.com/ajthom90/bowtie/server/internal/hdhr/hdhrfake"
)

func TestFetchDiscoverLineupStatus(t *testing.T) {
	lineup := []hdhrfake.LineupEntry{
		{GuideNumber: "5.1", GuideName: "WABC", VideoCodec: "MPEG2", AudioCodec: "AC3"},
		{GuideNumber: "7.1", GuideName: "WXYZ", VideoCodec: "MPEG2", AudioCodec: "AC3"},
	}
	f := hdhrfake.New(t, hdhrfake.Options{
		DeviceID:   "AABBCCDD",
		TunerCount: 2,
		Lineup:     lineup,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	info, err := hdhr.FetchDiscover(ctx, f.URL)
	if err != nil {
		t.Fatalf("FetchDiscover: %v", err)
	}
	if info.DeviceID != "AABBCCDD" {
		t.Errorf("DeviceID = %q", info.DeviceID)
	}
	if info.FriendlyName != "HDHomeRun FAKE" {
		t.Errorf("FriendlyName = %q", info.FriendlyName)
	}
	if info.ModelNumber != "HDFX-4US" {
		t.Errorf("ModelNumber = %q", info.ModelNumber)
	}
	if info.FirmwareVersion != "20260101" {
		t.Errorf("FirmwareVersion = %q", info.FirmwareVersion)
	}
	if info.TunerCount != 2 {
		t.Errorf("TunerCount = %d", info.TunerCount)
	}
	if info.BaseURL != f.URL {
		t.Errorf("BaseURL = %q, want %q", info.BaseURL, f.URL)
	}
	if info.LineupURL != f.URL+"/lineup.json" {
		t.Errorf("LineupURL = %q", info.LineupURL)
	}

	gotLineup, err := hdhr.FetchLineup(ctx, f.URL)
	if err != nil {
		t.Fatalf("FetchLineup: %v", err)
	}
	if len(gotLineup) != 2 {
		t.Fatalf("lineup len = %d", len(gotLineup))
	}
	if gotLineup[0].GuideNumber != "5.1" || gotLineup[0].GuideName != "WABC" {
		t.Errorf("lineup[0] = %+v", gotLineup[0])
	}
	if gotLineup[1].GuideNumber != "7.1" || gotLineup[1].VideoCodec != "MPEG2" {
		t.Errorf("lineup[1] = %+v", gotLineup[1])
	}
	if gotLineup[0].URL == "" {
		t.Error("lineup[0].URL empty")
	}

	status, err := hdhr.FetchStatus(ctx, f.URL)
	if err != nil {
		t.Fatalf("FetchStatus: %v", err)
	}
	if len(status) != 2 {
		t.Fatalf("status len = %d", len(status))
	}
	if status[0].Resource != "tuner0" || status[1].Resource != "tuner1" {
		t.Errorf("resources = %q, %q", status[0].Resource, status[1].Resource)
	}
	if status[0].VctNumber != "" {
		t.Errorf("idle VctNumber = %q, want empty", status[0].VctNumber)
	}
}

func TestFetchDiscoverBadURL(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := hdhr.FetchDiscover(ctx, "http://127.0.0.1:1")
	if err == nil {
		t.Fatal("expected error for unreachable base URL")
	}
}

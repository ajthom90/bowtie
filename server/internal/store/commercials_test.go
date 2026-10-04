package store_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/ajthom90/bowtie/server/internal/store"
)

func TestRecordingCommercialsRoundTrip(t *testing.T) {
	s := openTestStore(t)
	t0 := time.Date(2026, 10, 4, 1, 0, 0, 0, time.UTC)
	id, err := s.CreateRecording(store.Recording{UserID: 1, ChannelID: 1, ChannelName: "x", Title: "t",
		Start: t0, Stop: t0.Add(time.Hour), State: store.RecReady, CreatedAt: t0})
	if err != nil {
		t.Fatal(err)
	}
	r, _ := s.RecordingByID(id)
	if r.CommercialsDetected || r.Commercials != nil {
		t.Fatalf("new row: detected=%v commercials=%v", r.CommercialsDetected, r.Commercials)
	}

	want := []store.Commercial{{Start: 12.5, End: 190.25}, {Start: 900, End: 1080.04}}
	if err := s.SetRecordingCommercials(id, want); err != nil {
		t.Fatal(err)
	}
	r, _ = s.RecordingByID(id)
	if !r.CommercialsDetected || !reflect.DeepEqual(r.Commercials, want) {
		t.Fatalf("after set: detected=%v commercials=%v", r.CommercialsDetected, r.Commercials)
	}

	// UpdateRecording (the DVR's full-row write) leaves commercials alone.
	r.Commercials, r.CommercialsDetected = nil, false
	r.SizeBytes = 99
	if err := s.UpdateRecording(r); err != nil {
		t.Fatal(err)
	}
	r, _ = s.RecordingByID(id)
	if !r.CommercialsDetected || !reflect.DeepEqual(r.Commercials, want) {
		t.Fatalf("UpdateRecording clobbered commercials: %v", r.Commercials)
	}

	// Ran, none found: detected, empty.
	if err := s.SetRecordingCommercials(id, nil); err != nil {
		t.Fatal(err)
	}
	r, _ = s.RecordingByID(id)
	if !r.CommercialsDetected || len(r.Commercials) != 0 {
		t.Fatalf("none found: detected=%v commercials=%v", r.CommercialsDetected, r.Commercials)
	}
	list, _ := s.ListRecordings(store.RecReady)
	if len(list) != 1 || !list[0].CommercialsDetected {
		t.Fatalf("list: %+v", list)
	}
}

func TestRecordingsNeedingCommercials(t *testing.T) {
	s := openTestStore(t)
	t0 := time.Date(2026, 10, 4, 1, 0, 0, 0, time.UTC)
	mk := func(start time.Time, state string) int64 {
		id, err := s.CreateRecording(store.Recording{UserID: 1, ChannelID: 1, ChannelName: "x", Title: "t",
			Start: start, Stop: start.Add(time.Hour), State: state, CreatedAt: t0})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	old := mk(t0, store.RecReady)
	newer := mk(t0.Add(48*time.Hour), store.RecReady)
	done := mk(t0.Add(72*time.Hour), store.RecReady)
	mk(t0.Add(96*time.Hour), store.RecConverting)
	mk(t0.Add(96*time.Hour), store.RecFailed)
	if err := s.SetRecordingCommercials(done, nil); err != nil {
		t.Fatal(err)
	}
	ids, err := s.RecordingsNeedingCommercials(10)
	if err != nil || !reflect.DeepEqual(ids, []int64{newer, old}) {
		t.Fatalf("ids=%v err=%v, want [%d %d] (newest first, ready, not run)", ids, err, newer, old)
	}
	ids, _ = s.RecordingsNeedingCommercials(1)
	if !reflect.DeepEqual(ids, []int64{newer}) {
		t.Fatalf("limit 1: %v", ids)
	}
}

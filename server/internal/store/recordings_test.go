package store_test

import (
	"testing"
	"time"

	"github.com/ajthom90/bowtie/server/internal/store"
)

func TestRecordingsCRUD(t *testing.T) {
	s := openTestStore(t)
	t0 := time.Date(2026, 10, 4, 1, 0, 0, 0, time.UTC)
	id, err := s.CreateRecording(store.Recording{
		UserID: 1, ChannelID: 7, ChannelName: "9.1 FOX 9",
		Title: "News", Subtitle: "Late", Description: "d", Category: "News",
		Start: t0, Stop: t0.Add(30 * time.Minute), PadStartSec: 60, PadEndSec: 180,
		State: store.RecScheduled, CreatedAt: t0,
	})
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.RecordingByID(id)
	if err != nil || r.Title != "News" || r.State != store.RecScheduled || !r.Stop.Equal(t0.Add(30*time.Minute)) || r.PadEndSec != 180 {
		t.Fatalf("read %+v err=%v", r, err)
	}

	r.State, r.Partial, r.Dir, r.SizeBytes, r.DurationSec = store.RecReady, true, "/rec/1", 1234, 1800
	r.ActualStart = t0.Add(time.Minute)
	r.MissedSec = 60
	if err := s.UpdateRecording(r); err != nil {
		t.Fatal(err)
	}
	r2, _ := s.RecordingByID(id)
	if r2.State != store.RecReady || !r2.Partial || r2.Dir != "/rec/1" || r2.SizeBytes != 1234 || r2.MissedSec != 60 || !r2.ActualStart.Equal(t0.Add(time.Minute)) {
		t.Fatalf("updated %+v", r2)
	}

	id2, _ := s.CreateRecording(store.Recording{UserID: 2, ChannelID: 8, ChannelName: "5.1", Title: "Later",
		Start: t0.Add(time.Hour), Stop: t0.Add(2 * time.Hour), State: store.RecScheduled, CreatedAt: t0})
	got, err := s.ListRecordings(store.RecScheduled)
	if err != nil || len(got) != 1 || got[0].ID != id2 {
		t.Fatalf("list scheduled %+v err=%v", got, err)
	}
	all, _ := s.ListRecordings()
	if len(all) != 2 || all[0].ID != id {
		t.Fatalf("list all (by start) %+v", all)
	}

	if err := s.SetRecordingPosition(id, 1, 412); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRecordingPosition(id, 1, 500); err != nil {
		t.Fatal(err)
	}
	if p, _ := s.RecordingPosition(id, 1); p != 500 {
		t.Fatalf("position %d", p)
	}
	if p, _ := s.RecordingPosition(id, 2); p != 0 {
		t.Fatalf("other user's position %d", p)
	}

	if err := s.DeleteRecording(id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordingByID(id); err == nil {
		t.Fatal("still there")
	}
	if p, _ := s.RecordingPosition(id, 1); p != 0 {
		t.Fatalf("position survived delete: %d", p)
	}
}

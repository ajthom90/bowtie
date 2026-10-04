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

func TestSetRecordingProtectedOnly(t *testing.T) {
	s := openTestStore(t)
	id, _ := s.CreateRecording(store.Recording{UserID: 1, ChannelID: 1, ChannelName: "x", Title: "t",
		Start: time.Now(), Stop: time.Now().Add(time.Hour), State: store.RecRecording, CreatedAt: time.Now()})
	if err := s.SetRecordingProtected(id, true); err != nil {
		t.Fatal(err)
	}
	if r, _ := s.RecordingByID(id); !r.Protected || r.State != store.RecRecording {
		t.Fatalf("%+v", r)
	}
}

func TestRecordingPositionsForUser(t *testing.T) {
	s := openTestStore(t)
	t0 := time.Date(2026, 10, 4, 1, 0, 0, 0, time.UTC)
	mk := func(title string) int64 {
		id, err := s.CreateRecording(store.Recording{UserID: 1, ChannelID: 1, ChannelName: "x", Title: title,
			Start: t0, Stop: t0.Add(time.Hour), State: store.RecReady, CreatedAt: t0})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	a, b, c := mk("a"), mk("b"), mk("c")
	before := time.Now().UTC().Add(-time.Second)
	if err := s.SetRecordingPosition(a, 1, 412); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRecordingPosition(b, 1, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRecordingPosition(c, 2, 99); err != nil {
		t.Fatal(err)
	}
	after := time.Now().UTC().Add(time.Second)

	got, err := s.RecordingPositions(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("user 1 positions %+v", got)
	}
	if p := got[a]; p.Sec != 412 || p.UpdatedAt.Before(before) || p.UpdatedAt.After(after) {
		t.Fatalf("a %+v", p)
	}
	if p, ok := got[b]; !ok || p.Sec != 0 || p.UpdatedAt.IsZero() {
		t.Fatalf("b (reset to 0 still counts as saved) %+v ok=%v", p, ok)
	}
	if _, ok := got[c]; ok {
		t.Fatal("another user's position leaked")
	}

	one, err := s.RecordingPositionInfo(a, 1)
	if err != nil || one.Sec != 412 || !one.UpdatedAt.Equal(got[a].UpdatedAt) {
		t.Fatalf("single %+v err=%v", one, err)
	}
	if none, err := s.RecordingPositionInfo(a, 2); err != nil || none.Sec != 0 || !none.UpdatedAt.IsZero() {
		t.Fatalf("never saved %+v err=%v", none, err)
	}

	if err := s.DeleteRecording(a); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.RecordingPositions(1); len(got) != 1 {
		t.Fatalf("after delete %+v", got)
	}
	if empty, err := s.RecordingPositions(42); err != nil || len(empty) != 0 {
		t.Fatalf("no positions %+v err=%v", empty, err)
	}
}

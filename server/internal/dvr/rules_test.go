package dvr

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ajthom90/bowtie/server/internal/store"
)

func TestApplyRulesSchedulesEachEpisodeOnce(t *testing.T) {
	e := newEnv(t)
	for g, c := range e.chans {
		_ = e.st.UpdateChannel(c.ID, true, "e"+g)
	}
	at := func(h int) time.Time { return t0.Add(time.Duration(h) * time.Hour) }
	_ = e.st.ReplaceEPG("sd", []store.EPGChannel{{ID: "e5.1", Source: "sd"}, {ID: "e9.1", Source: "sd"}}, []store.Program{
		{EPGChannelID: "e5.1", Start: at(1), Stop: at(2), Title: "Drama", Subtitle: "Pilot", ProgramID: "EP1", SeriesID: "SH1", IsNew: true, Rating: "TV-14"},
		{EPGChannelID: "e9.1", Start: at(5), Stop: at(6), Title: "Drama", Subtitle: "Pilot", ProgramID: "EP1", SeriesID: "SH1"}, // rerun of EP1
		{EPGChannelID: "e5.1", Start: at(25), Stop: at(26), Title: "Drama", Subtitle: "Two", ProgramID: "EP2", SeriesID: "SH1", IsNew: true},
		{EPGChannelID: "e5.1", Start: at(3), Stop: at(4), Title: "Other", ProgramID: "EP9", SeriesID: "SH9", IsNew: true},
	})
	rid, _ := e.st.CreateRule(store.RecordingRule{UserID: 1, Title: "Drama", SeriesID: "SH1", CreatedAt: t0})
	if n := e.svc.ApplyRules(); n != 2 {
		t.Fatalf("scheduled %d, want 2 (EP1 once, EP2)", n)
	}
	if n := e.svc.ApplyRules(); n != 0 {
		t.Fatalf("second pass scheduled %d duplicates", n)
	}
	recs, _ := e.st.ListRecordings(store.RecScheduled)
	if len(recs) != 2 || recs[0].ProgramID != "EP1" || recs[0].RuleID != rid || recs[0].Subtitle != "Pilot" || recs[0].Rating != "TV-14" ||
		recs[0].ChannelID != e.chans["5.1"].ID || recs[1].ProgramID != "EP2" {
		t.Fatalf("recordings %+v", recs)
	}
	// Deleting an upcoming episode a rule scheduled skips it: it isn't
	// scheduled again on the next pass.
	if err := e.svc.Delete(recs[1].ID); err != nil {
		t.Fatal(err)
	}
	if n := e.svc.ApplyRules(); n != 0 {
		t.Fatalf("skipped episode came back: %d", n)
	}
	if r, _ := e.st.RecordingByID(recs[1].ID); r.State != store.RecFailed || r.Failure != "skipped" {
		t.Fatalf("skipped row %+v", r)
	}
	// A missed episode (no tuner) can still record at a later airing.
	r0 := recs[0]
	r0.State, r0.Failure = store.RecFailed, "noTuner"
	_ = e.st.UpdateRecording(r0)
	if n := e.svc.ApplyRules(); n != 1 {
		t.Fatalf("rerun after a missed episode: scheduled %d, want 1", n)
	}
}

func TestKeepLatestPrunesOldRuleRecordings(t *testing.T) {
	e := newEnv(t)
	rid, _ := e.st.CreateRule(store.RecordingRule{UserID: 1, Title: "News", KeepLatest: 2, CreatedAt: t0})
	var ids []int64
	for i := 0; i < 4; i++ {
		st := t0.Add(-time.Duration(10-i) * 24 * time.Hour)
		dir := filepath.Join(e.dir, "n", string(rune('a'+i)))
		_ = os.MkdirAll(dir, 0o755)
		id, _ := e.st.CreateRecording(store.Recording{UserID: 1, ChannelID: 1, ChannelName: "x", Title: "News", RuleID: rid,
			Start: st, Stop: st.Add(time.Hour), State: store.RecScheduled, CreatedAt: t0})
		r, _ := e.st.RecordingByID(id)
		r.State, r.Dir = store.RecReady, dir
		_ = e.st.UpdateRecording(r)
		ids = append(ids, id)
	}
	_ = e.st.SetRecordingProtected(ids[0], true)
	e.svc.pruneRule(rid)
	for i, id := range ids {
		_, err := e.st.RecordingByID(id)
		kept := err == nil
		if want := i == 0 || i >= 2; kept != want {
			t.Fatalf("recording %d kept=%v want %v", i, kept, want)
		}
	}
}

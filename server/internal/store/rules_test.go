package store_test

import (
	"testing"
	"time"

	"github.com/ajthom90/bowtie/server/internal/store"
)

func TestRecordingRulesAndMatches(t *testing.T) {
	s, _, u1, _ := favEnv(t)
	chans, _ := s.ListChannels(false)
	g := map[string]int64{}
	for _, c := range chans {
		g[c.GuideNumber] = c.ID
		_ = s.UpdateChannel(c.ID, c.GuideNumber != "11.1", "e"+c.GuideNumber)
	}
	now := time.Date(2026, 10, 4, 6, 0, 0, 0, time.UTC)
	at := func(h int) time.Time { return now.Add(time.Duration(h) * time.Hour) }
	_ = s.ReplaceEPG("sd", []store.EPGChannel{{ID: "e5.1", Source: "sd"}, {ID: "e9.1", Source: "sd"}, {ID: "e11.1", Source: "sd"}}, []store.Program{
		{EPGChannelID: "e5.1", Start: at(-2), Stop: at(-1), Title: "Jeopardy!", ProgramID: "EP1", SeriesID: "SH00000001", IsNew: true}, // over
		{EPGChannelID: "e5.1", Start: at(1), Stop: at(2), Title: "Jeopardy!", ProgramID: "EP2", SeriesID: "SH00000001", IsNew: true},
		{EPGChannelID: "e9.1", Start: at(3), Stop: at(4), Title: "JEOPARDY!", ProgramID: "EP3", SeriesID: "SH00000001"},
		{EPGChannelID: "e11.1", Start: at(5), Stop: at(6), Title: "Jeopardy!", ProgramID: "EP4", SeriesID: "SH00000001", IsNew: true}, // disabled channel
		{EPGChannelID: "e9.1", Start: at(7), Stop: at(8), Title: "Jeopardy! Masters", ProgramID: "EP5"},
		{EPGChannelID: "e5.1", Start: at(400), Stop: at(401), Title: "Jeopardy!", ProgramID: "EP6", SeriesID: "SH00000001"}, // beyond horizon
	})

	id, err := s.CreateRule(store.RecordingRule{UserID: u1, Title: "Jeopardy!", SeriesID: "SH00000001", NewOnly: true, KeepLatest: 5, CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	rules, _ := s.ListRules()
	if len(rules) != 1 || rules[0].ID != id || !rules[0].NewOnly || rules[0].KeepLatest != 5 {
		t.Fatalf("rules %+v", rules)
	}
	hits, err := s.RuleMatches(rules[0], now, now.Add(14*24*time.Hour))
	if err != nil || len(hits) != 1 || hits[0].ProgramID != "EP2" || hits[0].ChannelID != g["5.1"] {
		t.Fatalf("new-only matches %+v err=%v", hits, err)
	}
	r := rules[0]
	r.NewOnly = false
	if hits, _ := s.RuleMatches(r, now, now.Add(14*24*time.Hour)); len(hits) != 2 {
		t.Fatalf("all airings %+v", hits)
	}
	r.ChannelID = g["9.1"]
	if hits, _ := s.RuleMatches(r, now, now.Add(14*24*time.Hour)); len(hits) != 1 || hits[0].ProgramID != "EP3" {
		t.Fatalf("channel-limited %+v", hits)
	}
	// Without a series ID, the title must match exactly (any case).
	byTitle := store.RecordingRule{Title: "jeopardy!"}
	if hits, _ := s.RuleMatches(byTitle, now, now.Add(14*24*time.Hour)); len(hits) != 2 {
		t.Fatalf("title matches %+v", hits)
	}
	if err := s.DeleteRule(id); err != nil {
		t.Fatal(err)
	}
	if rules, _ := s.ListRules(); len(rules) != 0 {
		t.Fatalf("after delete %+v", rules)
	}
}

func TestRecordingRuleAndProgramIDRoundTrip(t *testing.T) {
	s := openTestStore(t)
	id, _ := s.CreateRecording(store.Recording{UserID: 1, ChannelID: 1, ChannelName: "x", Title: "t", RuleID: 7, ProgramID: "EP9",
		Start: time.Now(), Stop: time.Now().Add(time.Hour), State: store.RecScheduled, CreatedAt: time.Now()})
	r, _ := s.RecordingByID(id)
	if r.RuleID != 7 || r.ProgramID != "EP9" {
		t.Fatalf("%+v", r)
	}
}

package store_test

import (
	"testing"
	"time"

	"github.com/ajthom90/bowtie/server/internal/store"
)

func TestSearchPrograms(t *testing.T) {
	s, _, _, _ := favEnv(t) // channels 5.1/9.1/11.1, enabled, unmapped
	chans, _ := s.ListChannels(false)
	byGuide := map[string]store.Channel{}
	for _, c := range chans {
		byGuide[c.GuideNumber] = c
	}
	_ = s.UpdateChannel(byGuide["5.1"].ID, true, "e5")
	_ = s.UpdateChannel(byGuide["9.1"].ID, true, "e9")
	_ = s.UpdateChannel(byGuide["11.1"].ID, false, "e11") // disabled: never in results

	now := time.Date(2026, 10, 4, 6, 0, 0, 0, time.UTC)
	p := func(ch string, startH int, title, sub, desc string) store.Program {
		st := now.Add(time.Duration(startH) * time.Hour)
		return store.Program{EPGChannelID: ch, Start: st, Stop: st.Add(time.Hour), Title: title, Subtitle: sub, Description: desc}
	}
	if err := s.ReplaceEPG("xmltv", []store.EPGChannel{{ID: "e5", Source: "xmltv"}, {ID: "e9", Source: "xmltv"}, {ID: "e11", Source: "xmltv"}}, []store.Program{
		p("e5", -3, "Jeopardy!", "", "old episode"), // over: excluded
		p("e5", 5, "Wheel of Fortune", "", "after Jeopardy! tonight"),
		p("e9", 2, "Jeopardy!", "Tournament", ""),
		p("e9", 0, "Morning News", "100% local", ""), // on now
		p("e11", 1, "Jeopardy!", "", ""),
	}); err != nil {
		t.Fatal(err)
	}

	hits, err := s.SearchPrograms("jeopardy", now, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 || hits[0].Title != "Jeopardy!" || hits[0].GuideNumber != "9.1" || hits[0].ChannelID != byGuide["9.1"].ID || hits[1].Title != "Wheel of Fortune" {
		t.Fatalf("hits %+v", hits)
	}
	if hits, _ := s.SearchPrograms("news", now.Add(30*time.Minute), 10); len(hits) != 1 {
		t.Fatalf("on-now program missing: %+v", hits)
	}
	// LIKE wildcards in the query are literal.
	if hits, _ := s.SearchPrograms("100%", now, 10); len(hits) != 1 || hits[0].Title != "Morning News" {
		t.Fatalf("percent: %+v", hits)
	}
	if hits, _ := s.SearchPrograms("_", now, 10); len(hits) != 0 {
		t.Fatalf("underscore matched: %+v", hits)
	}
	if hits, _ := s.SearchPrograms("jeopardy", now, 1); len(hits) != 1 {
		t.Fatalf("limit: %d", len(hits))
	}
}

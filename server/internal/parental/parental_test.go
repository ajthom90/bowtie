package parental

import "testing"

func TestLevelNormalizesBothSources(t *testing.T) {
	cases := map[string]int{
		"TV-Y": 1, "TVY": 1, "tv-y7": 2, "TV-Y7-FV": 2, "TVY7": 2,
		"TV-G": 3, "G": 3, "TV-PG": 4, "TVPG": 4, "PG": 4,
		"TV-14": 5, "TV14": 5, "PG-13": 5, "PG13": 5,
		"TV-MA": 6, "TVMA": 6, "R": 6, "NC-17": 6, "NC17": 6,
		"": 0, "NR": 0, "X?": 0,
	}
	for code, want := range cases {
		if got := Level(code); got != want {
			t.Errorf("Level(%q)=%d want %d", code, got, want)
		}
	}
}

func TestPolicy(t *testing.T) {
	open := Policy{}
	if !open.ChannelAllowed(7) || !open.ProgramAllowed("TV-MA") || !open.ProgramAllowed("") {
		t.Fatal("zero policy must allow everything")
	}
	p := Policy{Channels: map[int64]bool{7: true}, MaxLevel: Level("TV-PG")}
	if !p.ChannelAllowed(7) || p.ChannelAllowed(8) {
		t.Fatal("channel allowlist")
	}
	if !p.ProgramAllowed("TV-G") || !p.ProgramAllowed("TV-PG") || p.ProgramAllowed("TV-14") {
		t.Fatal("max rating")
	}
	if !p.ProgramAllowed("") {
		t.Fatal("unrated allowed by default")
	}
	p.BlockUnrated = true
	if p.ProgramAllowed("") || p.ProgramAllowed("NR") {
		t.Fatal("block unrated")
	}
	if why := p.Reason("TV-MA"); why != "Blocked by parental controls (rated TV-MA)" {
		t.Fatalf("reason %q", why)
	}
	if why := p.Reason(""); why != "Blocked by parental controls (not rated)" {
		t.Fatalf("reason %q", why)
	}
}

func TestLabel(t *testing.T) {
	if Label(4) != "TV-PG" || Label(0) != "" || Label(6) != "TV-MA" {
		t.Fatal("labels")
	}
}

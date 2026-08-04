package xmltv

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseGolden(t *testing.T) {
	f, err := os.Open(filepath.Join("testdata", "guide.xml"))
	if err != nil {
		t.Fatalf("open golden: %v", err)
	}
	defer func() { _ = f.Close() }()

	tv, err := Parse(f)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if got, want := len(tv.Channels), 2; got != want {
		t.Errorf("channels: got %d, want %d", got, want)
	}
	if got, want := len(tv.Programmes), 3; got != want {
		t.Errorf("programmes: got %d, want %d", got, want)
	}

	if len(tv.Channels) < 1 {
		t.Fatal("expected at least one channel")
	}
	ch1 := tv.Channels[0]
	if ch1.ID != "ch1.example" {
		t.Errorf("channel[0].ID = %q, want ch1.example", ch1.ID)
	}
	found := false
	for _, n := range ch1.DisplayNames {
		if n == "WABC (5.1 ABC)" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("channel[0] missing display-name %q; got %v", "WABC (5.1 ABC)", ch1.DisplayNames)
	}
	if ch1.Icon.Src != "https://example.com/wabc.png" {
		t.Errorf("channel[0].Icon.Src = %q", ch1.Icon.Src)
	}

	if len(tv.Programmes) < 1 {
		t.Fatal("expected at least one programme")
	}
	p0 := tv.Programmes[0]
	if p0.Title != "Evening News" {
		t.Errorf("programme[0].Title = %q, want Evening News", p0.Title)
	}
	if p0.SubTitle != "August 4 Edition" {
		t.Errorf("programme[0].SubTitle = %q", p0.SubTitle)
	}
	if p0.Desc != "Local and national headlines." {
		t.Errorf("programme[0].Desc = %q", p0.Desc)
	}
	if p0.Channel != "ch1.example" {
		t.Errorf("programme[0].Channel = %q", p0.Channel)
	}
	if p0.Icon.Src != "https://example.com/news.png" {
		t.Errorf("programme[0].Icon.Src = %q", p0.Icon.Src)
	}
	if len(p0.Categories) != 2 || p0.Categories[0] != "News" {
		t.Errorf("programme[0].Categories = %v, want [News Local]", p0.Categories)
	}

	// Offset -0500: 2026-08-04 19:00:00 -0500 == 2026-08-05 00:00:00 UTC
	start, err := ParseTime(p0.Start)
	if err != nil {
		t.Fatalf("ParseTime(p0.Start): %v", err)
	}
	wantStart := time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC)
	if !start.Equal(wantStart) {
		t.Errorf("programme[0] start = %v, want %v", start.UTC(), wantStart)
	}
	stop, err := ParseTime(p0.Stop)
	if err != nil {
		t.Fatalf("ParseTime(p0.Stop): %v", err)
	}
	wantStop := time.Date(2026, 8, 5, 1, 0, 0, 0, time.UTC)
	if !stop.Equal(wantStop) {
		t.Errorf("programme[0] stop = %v, want %v", stop.UTC(), wantStop)
	}

	// No-offset UTC programme
	if len(tv.Programmes) < 2 {
		t.Fatal("expected second programme")
	}
	p1 := tv.Programmes[1]
	start1, err := ParseTime(p1.Start)
	if err != nil {
		t.Fatalf("ParseTime(p1.Start): %v", err)
	}
	wantStart1 := time.Date(2026, 8, 5, 1, 0, 0, 0, time.UTC)
	if !start1.Equal(wantStart1) {
		t.Errorf("programme[1] start = %v, want %v", start1.UTC(), wantStart1)
	}
	if start1.Location() != time.UTC {
		t.Errorf("programme[1] start location = %v, want UTC", start1.Location())
	}

	// Bad start must not parse
	if _, err := ParseTime(tv.Programmes[2].Start); err == nil {
		t.Error("expected ParseTime to fail on unparseable start")
	}
}

func TestToStoreSkipsBad(t *testing.T) {
	f, err := os.Open(filepath.Join("testdata", "guide.xml"))
	if err != nil {
		t.Fatalf("open golden: %v", err)
	}
	defer func() { _ = f.Close() }()

	tv, err := Parse(f)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	chans, progs, skipped := ToStore(tv)
	if skipped != 1 {
		t.Errorf("skipped = %d, want 1", skipped)
	}
	if len(chans) != 2 {
		t.Errorf("channels = %d, want 2", len(chans))
	}
	if len(progs) != 2 {
		t.Errorf("programs = %d, want 2 (3 programmes minus 1 skip)", len(progs))
	}

	// ch1: DisplayName first name, Callsign shortest ("5.1")
	found := false
	for _, c := range chans {
		if c.ID != "ch1.example" {
			continue
		}
		found = true
		if c.Source != "xmltv" {
			t.Errorf("ch1.Source = %q, want xmltv", c.Source)
		}
		if c.DisplayName != "WABC (5.1 ABC)" {
			t.Errorf("ch1.DisplayName = %q, want %q", c.DisplayName, "WABC (5.1 ABC)")
		}
		if c.Callsign != "5.1" {
			t.Errorf("ch1.Callsign = %q, want %q (shortest display-name)", c.Callsign, "5.1")
		}
		if c.IconURL != "https://example.com/wabc.png" {
			t.Errorf("ch1.IconURL = %q", c.IconURL)
		}
	}
	if !found {
		t.Error("missing ch1.example in ToStore channels")
	}

	// First good programme fields
	if len(progs) > 0 {
		p := progs[0]
		if p.Title != "Evening News" {
			t.Errorf("prog[0].Title = %q", p.Title)
		}
		if p.Subtitle != "August 4 Edition" {
			t.Errorf("prog[0].Subtitle = %q", p.Subtitle)
		}
		if p.Category != "News" {
			t.Errorf("prog[0].Category = %q, want News (first category)", p.Category)
		}
		if p.EPGChannelID != "ch1.example" {
			t.Errorf("prog[0].EPGChannelID = %q", p.EPGChannelID)
		}
		wantStart := time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC)
		if !p.Start.Equal(wantStart) {
			t.Errorf("prog[0].Start = %v, want %v", p.Start.UTC(), wantStart)
		}
	}
}

func TestParseTime(t *testing.T) {
	tests := []struct {
		in      string
		want    time.Time
		wantErr bool
	}{
		{
			in:   "20260804190000 -0500",
			want: time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC),
		},
		{
			in:   "20260805010000",
			want: time.Date(2026, 8, 5, 1, 0, 0, 0, time.UTC),
		},
		{
			in:      "not-a-valid-time",
			wantErr: true,
		},
		{
			in:      "",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		got, err := ParseTime(tt.in)
		if tt.wantErr {
			if err == nil {
				t.Errorf("ParseTime(%q) expected error", tt.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseTime(%q): %v", tt.in, err)
			continue
		}
		if !got.Equal(tt.want) {
			t.Errorf("ParseTime(%q) = %v, want %v", tt.in, got.UTC(), tt.want)
		}
	}
}

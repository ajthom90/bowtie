package xmltv

import (
	"os"
	"path/filepath"
	"strings"
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

func TestRatingsToStore(t *testing.T) {
	doc := `<tv><channel id="c"><display-name>C</display-name></channel>
<programme start="20261004010000 +0000" stop="20261004020000 +0000" channel="c">
  <title>Late Movie</title>
  <rating system="MPAA"><value>R</value></rating>
  <rating system="VCHIP"><value>TV-MA</value></rating>
</programme>
<programme start="20261004020000 +0000" stop="20261004030000 +0000" channel="c"><title>News</title></programme>
</tv>`
	tv, err := Parse(strings.NewReader(doc))
	if err != nil {
		t.Fatal(err)
	}
	_, progs, _ := ToStore(tv)
	if len(progs) != 2 || progs[0].Rating != "TV-MA" || progs[1].Rating != "" {
		t.Fatalf("%+v", progs)
	}
}

func TestSeriesIDsToStore(t *testing.T) {
	doc := `<tv><channel id="c"><display-name>C</display-name></channel>
<programme start="20261004010000 +0000" stop="20261004020000 +0000" channel="c">
  <title>Drama</title><episode-num system="dd_progid">EP01234567.0012</episode-num><new/>
</programme>
<programme start="20261004020000 +0000" stop="20261004030000 +0000" channel="c">
  <title>Drama</title><episode-num system="xmltv_ns">1.4.</episode-num>
</programme></tv>`
	tv, err := Parse(strings.NewReader(doc))
	if err != nil {
		t.Fatal(err)
	}
	_, progs, _ := ToStore(tv)
	p0, p1 := progs[0], progs[1]
	if p0.ProgramID != "EP012345670012" || p0.SeriesID != "SH01234567" || !p0.IsNew {
		t.Fatalf("p0 %+v", p0)
	}
	if p1.ProgramID != "" || p1.SeriesID != "" || p1.IsNew {
		t.Fatalf("p1 %+v", p1)
	}
}

// SiliconDust's guide (api.hdhomerun.com/api/xmltv): a cseries series-id wins
// over the dd_progid-derived one, previously-shown is never new, and channel
// lcn plus every display-name are kept for guide-number matching.
func TestHDHomeRunStyleGuide(t *testing.T) {
	doc := `<tv>
<channel id="US12345.hdhomerun.com">
  <display-name>9.1 KMSP</display-name><display-name>9.1</display-name><display-name>KMSP</display-name>
  <lcn>9.1</lcn>
</channel>
<programme start="20261004010000 +0000" stop="20261004020000 +0000" channel="US12345.hdhomerun.com">
  <title>Drama</title>
  <series-id system="cseries">C20814443ENX3UM</series-id>
  <episode-num system="dd_progid">EP00001648.0025</episode-num>
  <episode-num system="xmltv_ns">1.4.</episode-num>
  <episode-num system="onscreen">S02E05</episode-num>
  <new/>
</programme>
<programme start="20261004020000 +0000" stop="20261004030000 +0000" channel="US12345.hdhomerun.com">
  <title>Rerun</title>
  <episode-num system="dd_progid">EP00001648.0024</episode-num>
  <new/><previously-shown/>
</programme>
<programme start="20261004030000 +0000" stop="20261004040000 +0000" channel="US12345.hdhomerun.com">
  <title>Other</title>
  <series-id system="other">ignored</series-id>
  <episode-num system="dd_progid">EP00001648.0023</episode-num>
</programme></tv>`
	tv, err := Parse(strings.NewReader(doc))
	if err != nil {
		t.Fatal(err)
	}
	ch := tv.Channels[0]
	if len(ch.DisplayNames) != 3 || ch.DisplayNames[1] != "9.1" {
		t.Fatalf("display names %v", ch.DisplayNames)
	}
	if len(ch.LCNs) != 1 || ch.LCNs[0] != "9.1" {
		t.Fatalf("lcn %v", ch.LCNs)
	}
	_, progs, _ := ToStore(tv)
	p0, p1, p2 := progs[0], progs[1], progs[2]
	if p0.ProgramID != "EP000016480025" || p0.SeriesID != "C20814443ENX3UM" || !p0.IsNew {
		t.Fatalf("p0 %+v", p0)
	}
	if p1.IsNew || p1.SeriesID != "SH00001648" {
		t.Fatalf("p1 (previously shown) %+v", p1)
	}
	if p2.SeriesID != "SH00001648" {
		t.Fatalf("p2 (non-cseries series-id ignored) %+v", p2)
	}
}

package api

import (
	"reflect"
	"testing"
	"time"

	"github.com/ajthom90/bowtie/server/internal/epg"
)

// The XMLTV feed writes each stored category as its own <category>.
func TestXMLTVProgrammeSplitsCategories(t *testing.T) {
	now := time.Now()
	x := xmltvProgramme(epg.GuideChannel{ChannelID: 1}, epg.GuideProgram{Start: now, Stop: now.Add(time.Hour), Title: "Game", Category: "Sports event; Football"})
	if want := []string{"Sports event", "Football"}; !reflect.DeepEqual(x.Categories, want) {
		t.Fatalf("categories %q, want %q", x.Categories, want)
	}
}

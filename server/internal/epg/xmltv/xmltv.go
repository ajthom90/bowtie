// Package xmltv parses XMLTV guide files into store-ready EPG data.
package xmltv

import (
	"encoding/xml"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/ajthom90/bowtie/server/internal/parental"
	"github.com/ajthom90/bowtie/server/internal/store"
)

// TV is a parsed XMLTV document.
type TV struct {
	Channels   []Channel
	Programmes []Programme
}

// Channel is an XMLTV <channel> element.
type Channel struct {
	ID           string   `xml:"id,attr"`
	DisplayNames []string `xml:"display-name"`
	// LCNs are logical channel numbers ("9.1"); SiliconDust's guide may
	// carry the guide number here as well as in a display-name.
	LCNs []string `xml:"lcn"`
	Icon struct {
		Src string `xml:"src,attr"`
	} `xml:"icon"`
}

// Programme is an XMLTV <programme> element.
type Programme struct {
	Start      string   `xml:"start,attr"`
	Stop       string   `xml:"stop,attr"`
	Channel    string   `xml:"channel,attr"`
	Title      string   `xml:"title"`
	SubTitle   string   `xml:"sub-title"`
	Desc       string   `xml:"desc"`
	Categories []string `xml:"category"`
	Icon       struct {
		Src string `xml:"src,attr"`
	} `xml:"icon"`
	Ratings []struct {
		System string `xml:"system,attr"`
		Value  string `xml:"value"`
	} `xml:"rating"`
	EpisodeNums []struct {
		System string `xml:"system,attr"`
		Value  string `xml:",chardata"`
	} `xml:"episode-num"`
	SeriesIDs []struct {
		System string `xml:"system,attr"`
		Value  string `xml:",chardata"`
	} `xml:"series-id"`
	New             *struct{} `xml:"new"`
	PreviouslyShown *struct{} `xml:"previously-shown"`
}

// Parse streams an XMLTV document from r, decoding channel and programme
// elements one at a time so large guides are not loaded wholly into memory.
func Parse(r io.Reader) (*TV, error) {
	dec := xml.NewDecoder(r)
	tv := &TV{}

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("xmltv: decode: %w", err)
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch se.Name.Local {
		case "channel":
			var ch Channel
			if err := dec.DecodeElement(&ch, &se); err != nil {
				return nil, fmt.Errorf("xmltv: channel: %w", err)
			}
			tv.Channels = append(tv.Channels, ch)
		case "programme":
			var p Programme
			if err := dec.DecodeElement(&p, &se); err != nil {
				return nil, fmt.Errorf("xmltv: programme: %w", err)
			}
			tv.Programmes = append(tv.Programmes, p)
		}
	}
	return tv, nil
}

// ParseTime parses an XMLTV timestamp. Supported layouts are
// "20060102150405 -0700" and "20060102150405" (the latter assumed UTC).
func ParseTime(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, fmt.Errorf("xmltv: empty time")
	}
	if t, err := time.Parse("20060102150405 -0700", s); err == nil {
		return t, nil
	}
	if t, err := time.Parse("20060102150405", s); err == nil {
		return t.UTC(), nil
	}
	return time.Time{}, fmt.Errorf("xmltv: unparseable time %q", s)
}

// ToStore converts a parsed TV document into store types.
// Channel Source is "xmltv". Callsign is the shortest display-name.
// Programmes with unparseable start or stop times are skipped; the third
// return value is the count of skipped programmes.
func ToStore(tv *TV) ([]store.EPGChannel, []store.Program, int) {
	if tv == nil {
		return nil, nil, 0
	}

	chans := make([]store.EPGChannel, 0, len(tv.Channels))
	for _, ch := range tv.Channels {
		displayName, callsign := pickNames(ch.DisplayNames)
		chans = append(chans, store.EPGChannel{
			ID:          ch.ID,
			DisplayName: displayName,
			Callsign:    callsign,
			IconURL:     ch.Icon.Src,
			Source:      "xmltv",
		})
	}

	progs := make([]store.Program, 0, len(tv.Programmes))
	skipped := 0
	for _, p := range tv.Programmes {
		start, err := ParseTime(p.Start)
		if err != nil {
			skipped++
			continue
		}
		stop, err := ParseTime(p.Stop)
		if err != nil {
			skipped++
			continue
		}
		pid := programID(p)
		progs = append(progs, store.Program{
			EPGChannelID: p.Channel,
			Start:        start,
			Stop:         stop,
			Title:        p.Title,
			Subtitle:     p.SubTitle,
			Description:  p.Desc,
			Category:     store.JoinCategories(p.Categories),
			IconURL:      p.Icon.Src,
			Rating:       rating(p),
			ProgramID:    pid,
			SeriesID:     seriesID(p, pid),
			IsNew:        p.New != nil && p.PreviouslyShown == nil,
		})
	}
	return chans, progs, skipped
}

// pickNames returns DisplayName (first non-empty) and Callsign (shortest).
func pickNames(names []string) (displayName, callsign string) {
	for _, n := range names {
		if n == "" {
			continue
		}
		if displayName == "" {
			displayName = n
			callsign = n
			continue
		}
		if len(n) < len(callsign) {
			callsign = n
		}
	}
	return displayName, callsign
}

// rating picks the program's rating (US TV first, then MPAA).
func rating(p Programme) string {
	rs := make([]parental.Rated, 0, len(p.Ratings))
	for _, r := range p.Ratings {
		rs = append(rs, parental.Rated{System: r.System, Code: r.Value})
	}
	return parental.Pick(rs)
}

// seriesID prefers SiliconDust's <series-id system="cseries">, else derives
// the show ID from the program ID.
func seriesID(p Programme, programID string) string {
	for _, s := range p.SeriesIDs {
		if s.System == "cseries" {
			if v := strings.TrimSpace(s.Value); v != "" {
				return v
			}
		}
	}
	return store.SeriesIDOf(programID)
}

// programID is the Schedules Direct program ID from a dd_progid episode-num
// ("EP01234567.0012" → "EP012345670012"), or "".
func programID(p Programme) string {
	for _, e := range p.EpisodeNums {
		if e.System == "dd_progid" {
			return strings.ReplaceAll(strings.TrimSpace(e.Value), ".", "")
		}
	}
	return ""
}

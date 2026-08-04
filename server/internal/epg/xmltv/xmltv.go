// Package xmltv parses XMLTV guide files into store-ready EPG data.
package xmltv

import (
	"encoding/xml"
	"fmt"
	"io"
	"strings"
	"time"

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
	Icon         struct {
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
		category := ""
		if len(p.Categories) > 0 {
			category = p.Categories[0]
		}
		progs = append(progs, store.Program{
			EPGChannelID: p.Channel,
			Start:        start,
			Stop:         stop,
			Title:        p.Title,
			Subtitle:     p.SubTitle,
			Description:  p.Desc,
			Category:     category,
			IconURL:      p.Icon.Src,
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

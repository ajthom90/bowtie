// Package parental maps TV and movie ratings onto one ladder and decides
// what a restricted account may watch.
package parental

import (
	"fmt"
	"strings"
)

// Ladder: 1 TV-Y, 2 TV-Y7, 3 TV-G/G, 4 TV-PG/PG, 5 TV-14/PG-13,
// 6 TV-MA/R/NC-17; 0 = not rated (or unknown).
var levels = map[string]int{
	"TVY": 1, "TVY7": 2, "TVY7FV": 2,
	"TVG": 3, "G": 3,
	"TVPG": 4, "PG": 4,
	"TV14": 5, "PG13": 5,
	"TVMA": 6, "R": 6, "NC17": 6,
}

var labels = map[int]string{1: "TV-Y", 2: "TV-Y7", 3: "TV-G", 4: "TV-PG", 5: "TV-14", 6: "TV-MA"}

// Level returns a rating code's place on the ladder. It accepts the XMLTV
// ("TV-14") and Schedules Direct ("TV14") spellings; unknown codes are 0.
func Level(code string) int {
	k := strings.ToUpper(strings.NewReplacer("-", "", " ", "", "_", "").Replace(code))
	return levels[k]
}

// Label is the TV rating shown for a ladder level ("" for 0).
func Label(level int) string { return labels[level] }

// Policy is what a restricted account may watch. The zero Policy allows
// everything (admins, and accounts without parental controls).
type Policy struct {
	Channels     map[int64]bool // nil = every channel
	MaxLevel     int            // 0 = no rating limit
	BlockUnrated bool
}

// ChannelAllowed reports whether the account may see and tune channelID.
func (p Policy) ChannelAllowed(channelID int64) bool {
	return p.Channels == nil || p.Channels[channelID]
}

// ProgramAllowed reports whether a program with this rating may play.
func (p Policy) ProgramAllowed(rating string) bool {
	lvl := Level(rating)
	if lvl == 0 {
		return !p.BlockUnrated
	}
	return p.MaxLevel == 0 || lvl <= p.MaxLevel
}

// Restricted reports whether the policy limits anything.
func (p Policy) Restricted() bool {
	return p.Channels != nil || p.MaxLevel > 0 || p.BlockUnrated
}

// Reason is the message shown when a program is blocked.
func (p Policy) Reason(rating string) string {
	if Level(rating) == 0 {
		return "Blocked by parental controls (not rated)"
	}
	return fmt.Sprintf("Blocked by parental controls (rated %s)", strings.TrimSpace(rating))
}

// Rated is one rating a guide source gives a program.
type Rated struct {
	System string // e.g. "VCHIP", "USA Parental Rating", "MPAA"
	Code   string
}

// Pick chooses the rating to store: a US TV rating first, then MPAA, then any
// code the ladder knows. Unknown systems (e.g. Canadian) give "".
func Pick(rs []Rated) string {
	best, bestRank := "", 0
	for _, r := range rs {
		if Level(r.Code) == 0 {
			continue
		}
		rank := 1
		sys := strings.ToUpper(r.System)
		switch {
		case strings.Contains(sys, "VCHIP") || strings.Contains(sys, "USA PARENTAL") || strings.HasPrefix(strings.ToUpper(r.Code), "TV"):
			rank = 3
		case strings.Contains(sys, "MPAA"):
			rank = 2
		}
		if rank > bestRank {
			best, bestRank = strings.TrimSpace(r.Code), rank
		}
	}
	return best
}

// BlockAll allows nothing: used when the account can't be loaded.
func BlockAll() Policy {
	return Policy{Channels: map[int64]bool{}, MaxLevel: 1, BlockUnrated: true}
}

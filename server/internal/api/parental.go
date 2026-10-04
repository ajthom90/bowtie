package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/ajthom90/bowtie/server/internal/auth"
	"github.com/ajthom90/bowtie/server/internal/epg"
	"github.com/ajthom90/bowtie/server/internal/parental"
	"github.com/ajthom90/bowtie/server/internal/store"
)

// policyFor is the user's parental policy (admins: unrestricted).
func policyFor(u store.User) parental.Policy {
	if u.Role == "admin" {
		return parental.Policy{}
	}
	p := parental.Policy{MaxLevel: u.MaxRating, BlockUnrated: u.BlockUnrated}
	if u.AllowedChannels != nil {
		p.Channels = make(map[int64]bool, len(u.AllowedChannels))
		for _, id := range u.AllowedChannels {
			p.Channels[id] = true
		}
	}
	return p
}

// callerPolicy loads the signed-in user's policy. It fails closed: an
// account that can't be loaded (deleted while its token is still valid, or a
// database error) is allowed nothing.
func (s *Server) callerPolicy(r *http.Request) parental.Policy {
	claims, ok := auth.ClaimsFrom(r.Context())
	if !ok {
		return parental.BlockAll()
	}
	u, err := s.deps.Store.UserByID(claims.UserID)
	if err != nil {
		return parental.BlockAll()
	}
	return policyFor(u)
}

// windowRating is the strictest rating among guide programs on channelID
// overlapping [start, stop) — what a manual recording of that window holds.
func (s *Server) windowRating(r *http.Request, channelID int64, start, stop time.Time) string {
	if s.deps.EPG == nil {
		return ""
	}
	guide, err := s.deps.EPG.Guide(r.Context(), start, stop)
	if err != nil {
		return ""
	}
	best := ""
	for _, g := range guide {
		if g.ChannelID != channelID {
			continue
		}
		for _, p := range g.Programs {
			if p.Start.Before(stop) && start.Before(p.Stop) && parental.Level(p.Rating) > parental.Level(best) {
				best = p.Rating
			}
		}
	}
	return best
}

// writeParentalBlock answers 403 {"error", "code": "parental"}.
func writeParentalBlock(w http.ResponseWriter, msg string) {
	writeJSON(w, http.StatusForbidden, map[string]string{"error": msg, "code": "parental"})
}

// parentalStartBlock returns why u may not start channelID now ("" = allowed).
func (s *Server) parentalStartBlock(r *http.Request, u store.User, channelID int64) string {
	return BlockReason(r.Context(), s.deps.EPG, u, channelID, time.Now().UTC())
}

// BlockReason returns why u may not watch channelID at now under parental
// controls ("" = allowed): a channel outside the allowlist, or a current
// program whose rating is blocked. With no guide data the program counts as
// not rated.
func BlockReason(ctx context.Context, guide *epg.Service, u store.User, channelID int64, now time.Time) string {
	p := policyFor(u)
	if !p.Restricted() {
		return ""
	}
	if !p.ChannelAllowed(channelID) {
		return "Blocked by parental controls (this channel isn't allowed)"
	}
	rating := ""
	if guide != nil {
		if chans, err := guide.Guide(ctx, now, now.Add(time.Second)); err == nil {
			for _, g := range chans {
				if g.ChannelID != channelID {
					continue
				}
				for _, prog := range g.Programs {
					if !prog.Start.After(now) && prog.Stop.After(now) {
						rating = prog.Rating
					}
				}
			}
		}
	}
	if !p.ProgramAllowed(rating) {
		return p.Reason(rating)
	}
	return ""
}

// ParentalBlocker adapts BlockReason for stream.ManagerDeps.BlockedFor.
func ParentalBlocker(st *store.Store, guide *epg.Service) func(userID, channelID int64, now time.Time) string {
	return func(userID, channelID int64, now time.Time) string {
		u, err := st.UserByID(userID)
		if err != nil {
			return ""
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return BlockReason(ctx, guide, u, channelID, now)
	}
}

// parentalJSON is the admin/me view of a user's parental controls.
type parentalJSON struct {
	// AllowedChannelIDs: null = every channel.
	AllowedChannelIDs []int64 `json:"allowedChannelIds"`
	// MaxRating: "" = no limit, else TV-Y, TV-Y7, TV-G, TV-PG, TV-14 or TV-MA.
	MaxRating    string `json:"maxRating"`
	BlockUnrated bool   `json:"blockUnrated"`
}

func parentalToJSON(u store.User) parentalJSON {
	return parentalJSON{AllowedChannelIDs: u.AllowedChannels, MaxRating: parental.Label(u.MaxRating), BlockUnrated: u.BlockUnrated}
}

// applyParentalPatch applies optional parental fields from a create/patch body.
// raw allowedChannelIds: absent = keep, null = every channel, [..] = allowlist.
func applyParentalPatch(u *store.User, allowed json.RawMessage, maxRating *string, blockUnrated *bool) error {
	if len(allowed) > 0 {
		if string(allowed) == "null" {
			u.AllowedChannels = nil
		} else {
			var ids []int64
			if err := json.Unmarshal(allowed, &ids); err != nil {
				return fmt.Errorf("allowedChannelIds must be null or a list of channel IDs")
			}
			if ids == nil {
				ids = []int64{}
			}
			u.AllowedChannels = ids
		}
	}
	if maxRating != nil {
		if *maxRating == "" {
			u.MaxRating = 0
		} else if lvl := parental.Level(*maxRating); lvl > 0 {
			u.MaxRating = lvl
		} else {
			return fmt.Errorf("maxRating must be empty or one of TV-Y, TV-Y7, TV-G, TV-PG, TV-14, TV-MA")
		}
	}
	if blockUnrated != nil {
		u.BlockUnrated = *blockUnrated
	}
	return nil
}

// applyParental drops channels the policy doesn't allow and locks programs
// it blocks (title and rating stay; the description is hidden).
func applyParental(guide []epg.GuideChannel, p parental.Policy) []epg.GuideChannel {
	if !p.Restricted() {
		return guide
	}
	out := guide[:0]
	for _, g := range guide {
		if !p.ChannelAllowed(g.ChannelID) {
			continue
		}
		for j := range g.Programs {
			if !p.ProgramAllowed(g.Programs[j].Rating) {
				g.Programs[j].Locked = true
				g.Programs[j].Description = ""
			}
		}
		out = append(out, g)
	}
	return out
}

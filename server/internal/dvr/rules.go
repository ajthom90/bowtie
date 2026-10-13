package dvr

import (
	"log"
	"sort"
	"time"

	"github.com/ajthom90/bowtie/server/internal/store"
)

// ruleHorizon: how far ahead series rules schedule airings.
const ruleHorizon = 14 * 24 * time.Hour

// ApplyRules schedules every upcoming airing the series rules want that
// isn't scheduled or recorded already (by program ID, else by channel and
// start). An episode missed for lack of a tuner can record at a later
// airing; one the user skipped can't. Returns how many it scheduled.
func (s *Service) ApplyRules() int {
	s.rulesMu.Lock() // the hourly Tick and a new rule may both apply
	defer s.rulesMu.Unlock()
	return s.applyRules()
}

func (s *Service) applyRules() int {
	now := s.deps.Clock()
	rules, err := s.deps.Store.ListRules()
	if err != nil || len(rules) == 0 {
		return 0
	}
	existing, err := s.deps.Store.ListRecordings()
	if err != nil {
		return 0
	}
	type slot struct{ ch, start int64 }
	taken, slots := map[string]bool{}, map[slot]bool{}
	for _, r := range existing {
		if r.State == store.RecFailed && r.Failure != "skipped" {
			continue
		}
		if r.ProgramID != "" {
			taken[r.ProgramID] = true
		}
		slots[slot{r.ChannelID, r.Start.Unix()}] = true
	}
	n := 0
	for _, rule := range rules {
		hits, err := s.deps.Store.RuleMatches(rule, now, now.Add(ruleHorizon))
		if err != nil {
			log.Printf("dvr: rule %d: %v", rule.ID, err)
			continue
		}
		for _, h := range hits {
			k := slot{h.ChannelID, h.Start.Unix()}
			if (h.ProgramID != "" && taken[h.ProgramID]) || slots[k] {
				continue
			}
			ch, err := s.deps.Store.ChannelByID(h.ChannelID)
			if err != nil {
				continue
			}
			// Force: a rule records whenever a tuner turns out to be free.
			if _, _, err := s.Schedule(ScheduleRequest{
				UserID: rule.UserID, Channel: ch, Title: h.Title, Subtitle: h.Subtitle,
				Description: h.Description, Category: h.Category, IconURL: h.IconURL, Rating: h.Rating,
				Start: h.Start, Stop: h.Stop, Force: true, RuleID: rule.ID, ProgramID: h.ProgramID,
			}); err != nil {
				continue
			}
			n++
			if h.ProgramID != "" {
				taken[h.ProgramID] = true
			}
			slots[k] = true
		}
	}
	return n
}

// ReconcileRule removes only future, unstarted recordings that no longer match.
// KeepLatest is deliberately applied only by pruneRule when an episode completes.
func (s *Service) ReconcileRule(id int64) error {
	// Tick takes these locks in this order; holding tickMu prevents capture startup.
	s.tickMu.Lock()
	defer s.tickMu.Unlock()
	s.rulesMu.Lock()
	defer s.rulesMu.Unlock()
	rule, err := s.deps.Store.RuleByID(id)
	if err != nil {
		return err
	}
	now := s.deps.Clock()
	rows, err := s.deps.Store.ListRecordings(store.RecScheduled)
	if err != nil {
		return err
	}
	to := now.Add(ruleHorizon)
	for _, r := range rows {
		if r.RuleID == id && !r.Start.Before(to) {
			to = r.Start.Add(time.Second)
		}
	}
	hits, err := s.deps.Store.RuleMatches(rule, now, to)
	if err != nil {
		return err
	}
	type slot struct{ ch, start int64 }
	matches := map[slot]bool{}
	for _, h := range hits {
		matches[slot{h.ChannelID, h.Start.Unix()}] = true
	}
	for _, r := range rows {
		if r.RuleID != id || !r.Start.After(now) || matches[slot{r.ChannelID, r.Start.Unix()}] {
			continue
		}
		s.mu.Lock()
		active := s.captures[r.ID] != nil
		s.mu.Unlock()
		if active {
			continue
		}
		// Remove the schedule without a skipped marker, so later edits can restore it.
		if err := s.deps.Store.DeleteRuleSchedule(r.ID, id, s.deps.Clock()); err != nil {
			return err
		}
	}
	s.applyRules()
	return nil
}

// pruneRule deletes a rule's oldest ready recordings beyond its KeepLatest
// (protected ones don't count and are never deleted).
func (s *Service) pruneRule(ruleID int64) {
	if ruleID == 0 {
		return
	}
	rule, err := s.deps.Store.RuleByID(ruleID)
	if err != nil || rule.KeepLatest <= 0 {
		return
	}
	ready, err := s.deps.Store.ListRecordings(store.RecReady)
	if err != nil {
		return
	}
	sort.SliceStable(ready, func(i, j int) bool { return ready[i].Start.After(ready[j].Start) })
	kept := 0
	for _, r := range ready {
		if r.RuleID != ruleID || r.Protected {
			continue
		}
		if watching, _ := s.deps.Store.RecordingWatchedSince(r.ID, s.deps.Clock().Add(-inUseWindow)); watching {
			continue // someone is watching it; prune next time
		}
		if kept < rule.KeepLatest {
			kept++
			continue
		}
		if err := s.Delete(r.ID); err != nil {
			log.Printf("dvr: rule %d keep-latest: delete %d: %v", ruleID, r.ID, err)
		}
	}
}

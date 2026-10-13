package api

import (
	"github.com/ajthom90/bowtie/server/internal/store"
	"github.com/ajthom90/bowtie/server/internal/tuner"
)

// watchableChannels reports, per channel ID, whether starting it could work
// right now: its HDHomeRun has a free tuner, or Bowtie is already streaming
// (or recording) it, so a viewer can join. A tuner whose status is unknown
// (unreachable, or status.json unreadable) counts as free — never hide a
// channel on a guess.
func watchableChannels(chans []store.Channel, devices []tuner.DeviceStatus, ingest []int64) map[int64]bool {
	free := make(map[string]bool, len(devices))
	for _, d := range devices {
		if !d.Reachable || len(d.Tuners) == 0 {
			free[d.Device.DeviceID] = true
			continue
		}
		for _, t := range d.Tuners {
			if t.TargetIP == "" && t.VctNumber == "" {
				free[d.Device.DeviceID] = true
				break
			}
		}
		if _, ok := free[d.Device.DeviceID]; !ok {
			free[d.Device.DeviceID] = false
		}
	}
	joinable := make(map[int64]bool, len(ingest))
	for _, id := range ingest {
		joinable[id] = true
	}
	out := make(map[int64]bool, len(chans))
	for _, c := range chans {
		f, known := free[c.DeviceID]
		out[c.ID] = joinable[c.ID] || !known || f
	}
	return out
}

// channelWatchability is watchableChannels for the enabled channels, with
// live tuner status. nil (everything watchable) when tuners or streams aren't
// wired (tests, early startup).
func (s *Server) channelWatchability(chans []store.Channel) map[int64]bool {
	if s.deps.Tuners == nil || s.deps.Streams == nil {
		return nil
	}
	return watchableChannels(chans, s.deps.Tuners.Devices(), s.deps.Streams.IngestChannels())
}

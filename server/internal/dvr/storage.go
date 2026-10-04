package dvr

import (
	"time"

	"github.com/ajthom90/bowtie/server/internal/store"
)

// usedCacheFor bounds how often Storage walks in-progress recording folders.
const usedCacheFor = 30 * time.Second

// Storage is the DVR's disk use, for the admin gauge.
type Storage struct {
	Dir string
	// UsedBytes: ready recordings' size plus whatever other recordings hold
	// on disk (capture parts, conversions in progress).
	UsedBytes  int64
	FreeBytes  int64
	TotalBytes int64
	// FloorBytes: below this much free space a capture doesn't start.
	FloorBytes int64
	// MinFreeBytes: the retention sweep deletes old recordings below this
	// (0 = never).
	MinFreeBytes int64
	Recordings   StorageCounts
}

// StorageCounts counts recordings by state. Scheduled includes waiting;
// Recording includes converting; Failed leaves out series-rule skips.
type StorageCounts struct {
	Ready     int
	Scheduled int
	Recording int
	Failed    int
}

// Storage reports disk space and recording counts. UsedBytes is cached
// briefly; the rest is read on every call.
func (s *Service) Storage() (Storage, error) {
	rows, err := s.deps.Store.ListRecordings()
	if err != nil {
		return Storage{}, err
	}
	free, err := s.deps.FreeBytes(s.deps.Dir)
	if err != nil {
		return Storage{}, err
	}
	total, err := s.deps.TotalBytes(s.deps.Dir)
	if err != nil {
		return Storage{}, err
	}
	out := Storage{Dir: s.deps.Dir, FreeBytes: free, TotalBytes: total, FloorBytes: diskFloor, MinFreeBytes: s.deps.MinFreeBytes}
	for _, r := range rows {
		switch r.State {
		case store.RecReady:
			out.Recordings.Ready++
		case store.RecScheduled, store.RecWaiting:
			out.Recordings.Scheduled++
		case store.RecRecording, store.RecConverting:
			out.Recordings.Recording++
		case store.RecFailed:
			if r.Failure != "skipped" {
				out.Recordings.Failed++
			}
		}
	}
	out.UsedBytes = s.usedBytes(rows)
	return out, nil
}

func (s *Service) usedBytes(rows []store.Recording) int64 {
	s.usedMu.Lock()
	defer s.usedMu.Unlock()
	if !s.usedAt.IsZero() && time.Since(s.usedAt) < usedCacheFor {
		return s.used
	}
	var n int64
	for _, r := range rows {
		switch {
		case r.State == store.RecReady:
			n += r.SizeBytes
		case r.Dir != "":
			n += dirSize(r.Dir)
		}
	}
	s.used, s.usedAt = n, time.Now()
	return n
}

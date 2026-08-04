// Package tuner manages HDHomeRun device discovery, persistence, and stream URLs.
package tuner

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/ajthom90/bowtie/server/internal/config"
	"github.com/ajthom90/bowtie/server/internal/hdhr"
	"github.com/ajthom90/bowtie/server/internal/store"
)

// DeviceStatus is a stored device plus live reachability and tuner status.
type DeviceStatus struct {
	Device    store.Device
	Reachable bool
	Tuners    []hdhr.TunerStatus
}

// DiscoverFunc is UDP discovery; injectable for tests.
type DiscoverFunc func(ctx context.Context, timeout time.Duration) ([]hdhr.DiscoverInfo, error)

// FetchDiscoverFunc fetches /discover.json; injectable for tests.
type FetchDiscoverFunc func(ctx context.Context, baseURL string) (hdhr.DiscoverInfo, error)

// FetchStatusFunc fetches /status.json; injectable for tests.
type FetchStatusFunc func(ctx context.Context, baseURL string) ([]hdhr.TunerStatus, error)

// Manager aggregates discovered, manual, and stored HDHomeRun devices.
type Manager struct {
	store *store.Store
	cfg   config.Config

	discover      DiscoverFunc
	fetchDiscover FetchDiscoverFunc
	fetchStatus   FetchStatusFunc
	now           func() time.Time
	discoverTO    time.Duration

	mu    sync.Mutex
	cache []DeviceStatus
}

// New creates a Manager with default HDHomeRun clients.
func New(st *store.Store, cfg config.Config) *Manager {
	return &Manager{
		store:         st,
		cfg:           cfg,
		discover:      hdhr.Discover,
		fetchDiscover: hdhr.FetchDiscover,
		fetchStatus:   hdhr.FetchStatus,
		now:           func() time.Time { return time.Now().UTC() },
		discoverTO:    2 * time.Second,
	}
}

// SetDiscoverFunc replaces UDP discovery (tests).
func (m *Manager) SetDiscoverFunc(fn DiscoverFunc) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.discover = fn
}

// SetFetchDiscoverFunc replaces FetchDiscover (tests).
func (m *Manager) SetFetchDiscoverFunc(fn FetchDiscoverFunc) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.fetchDiscover = fn
}

// SetFetchStatusFunc replaces FetchStatus (tests).
func (m *Manager) SetFetchStatusFunc(fn FetchStatusFunc) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.fetchStatus = fn
}

// Refresh runs best-effort UDP discovery, probes cfg.Devices and previously
// stored devices via HTTP discover, upserts reachable devices, and keeps
// unreachable stored rows with Reachable=false in the cache.
func (m *Manager) Refresh(ctx context.Context) error {
	m.mu.Lock()
	discover := m.discover
	fetchDiscover := m.fetchDiscover
	fetchStatus := m.fetchStatus
	timeout := m.discoverTO
	now := m.now()
	manualEntries := append([]string(nil), m.cfg.Devices...)
	m.mu.Unlock()

	// Candidate base URLs → whether they came from manual config.
	type candidate struct {
		baseURL string
		manual  bool
	}
	candidates := map[string]candidate{} // key = baseURL

	// 1) UDP discovery (best-effort)
	if discover != nil {
		found, err := discover(ctx, timeout)
		if err == nil {
			for _, info := range found {
				base := info.BaseURL
				if base == "" && info.DeviceID != "" {
					// Should not happen after Discover fixup; skip.
					continue
				}
				if base == "" {
					continue
				}
				candidates[base] = candidate{baseURL: base, manual: false}
			}
		}
		// discovery errors are ignored (best-effort)
	}

	// 2) Manual IPs from config
	for _, entry := range manualEntries {
		base := hdhr.BaseURLFromManual(entry)
		if base == "" {
			continue
		}
		candidates[base] = candidate{baseURL: base, manual: true}
	}

	// 3) Previously stored devices
	stored, err := m.store.ListDevices()
	if err != nil {
		return fmt.Errorf("list devices: %w", err)
	}
	storedByID := make(map[string]store.Device, len(stored))
	for _, d := range stored {
		storedByID[d.DeviceID] = d
		base := hdhr.HTTPBaseURL(d.IP, d.StreamPort)
		if base == "" {
			continue
		}
		if _, exists := candidates[base]; !exists {
			candidates[base] = candidate{baseURL: base, manual: d.Manual}
		} else if d.Manual {
			// Preserve manual flag if either source says manual.
			c := candidates[base]
			c.manual = true
			candidates[base] = c
		}
	}

	// Probe each candidate.
	reachable := map[string]DeviceStatus{} // by DeviceID
	for _, c := range candidates {
		if ctx.Err() != nil {
			break
		}
		info, err := fetchDiscover(ctx, c.baseURL)
		if err != nil || info.DeviceID == "" {
			continue
		}
		ip := hdhr.HostFromBaseURL(info.BaseURL)
		if ip == "" {
			ip = hdhr.HostFromBaseURL(c.baseURL)
		}
		streamPort := hdhr.StreamPortFromBaseURL(info.BaseURL)
		if streamPort <= 0 {
			streamPort = hdhr.StreamPortFromBaseURL(c.baseURL)
		}
		// Preserve manual if previously stored as manual or this candidate is manual.
		manual := c.manual
		if prev, ok := storedByID[info.DeviceID]; ok && prev.Manual {
			manual = true
		}
		dev := store.Device{
			DeviceID:   info.DeviceID,
			IP:         ip,
			Model:      info.ModelNumber,
			TunerCount: info.TunerCount,
			Manual:     manual,
			LastSeen:   now,
			StreamPort: streamPort,
		}
		if err := m.store.UpsertDevice(dev); err != nil {
			return fmt.Errorf("upsert device %s: %w", dev.DeviceID, err)
		}
		// Live status (best-effort)
		var tuners []hdhr.TunerStatus
		baseForStatus := info.BaseURL
		if baseForStatus == "" {
			baseForStatus = c.baseURL
		}
		if st, err := fetchStatus(ctx, baseForStatus); err == nil {
			tuners = st
		}
		reachable[dev.DeviceID] = DeviceStatus{
			Device:    dev,
			Reachable: true,
			Tuners:    tuners,
		}
	}

	// Build final cache: all currently stored devices, mark unreachable ones.
	all, err := m.store.ListDevices()
	if err != nil {
		return fmt.Errorf("list devices after refresh: %w", err)
	}
	cache := make([]DeviceStatus, 0, len(all))
	for _, d := range all {
		if rs, ok := reachable[d.DeviceID]; ok {
			cache = append(cache, rs)
			continue
		}
		cache = append(cache, DeviceStatus{
			Device:    d,
			Reachable: false,
			Tuners:    nil,
		})
	}

	m.mu.Lock()
	m.cache = cache
	m.mu.Unlock()
	return nil
}

// Devices returns the last Refresh cache, refreshing live FetchStatus for
// reachable devices when possible.
func (m *Manager) Devices() []DeviceStatus {
	m.mu.Lock()
	fetchStatus := m.fetchStatus
	cache := make([]DeviceStatus, len(m.cache))
	copy(cache, m.cache)
	m.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	out := make([]DeviceStatus, len(cache))
	for i, ds := range cache {
		out[i] = ds
		if !ds.Reachable {
			continue
		}
		base := hdhr.HTTPBaseURL(ds.Device.IP, ds.Device.StreamPort)
		if base == "" {
			continue
		}
		if st, err := fetchStatus(ctx, base); err == nil {
			out[i].Tuners = st
		}
	}
	return out
}

// StreamURL returns the MPEG-TS URL for a channel on its device.
// Port rule: StreamPort 5004 (or default) for real devices; nonstandard ports
// (hdhrfake) reuse the stored StreamPort derived from BaseURL.
func (m *Manager) StreamURL(ch store.Channel) (string, error) {
	if ch.DeviceID == "" || ch.GuideNumber == "" {
		return "", fmt.Errorf("channel missing device or guide number")
	}
	d, err := m.store.DeviceByID(ch.DeviceID)
	if err != nil {
		return "", fmt.Errorf("device %s: %w", ch.DeviceID, err)
	}
	port := d.StreamPort
	if port <= 0 {
		port = 5004
	}
	return fmt.Sprintf("http://%s:%d/auto/v%s", d.IP, port, ch.GuideNumber), nil
}

package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ajthom90/bowtie/server/internal/auth"
	"github.com/ajthom90/bowtie/server/internal/hdhr"
	"github.com/ajthom90/bowtie/server/internal/store"
	"github.com/ajthom90/bowtie/server/internal/tuner"
)

func (s *Server) handleAdminListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.deps.Store.ListUsers()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list users")
		return
	}
	out := make([]userJSON, 0, len(users))
	for _, u := range users {
		out = append(out, userToJSON(u))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleAdminCreateUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username   string `json:"username"`
		Password   string `json:"password"`
		Role       string `json:"role"`
		MaxQuality string `json:"maxQuality"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	if req.Username == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "username and password required")
		return
	}
	if req.Role != "admin" && req.Role != "viewer" {
		writeError(w, http.StatusBadRequest, "role must be admin or viewer")
		return
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to hash password")
		return
	}
	id, err := s.deps.Store.CreateUser(store.User{
		Username:     req.Username,
		PasswordHash: hash,
		Role:         req.Role,
		MaxQuality:   req.MaxQuality,
		CreatedAt:    time.Now().UTC(),
	})
	if err != nil {
		if isUniqueConstraint(err) {
			writeError(w, http.StatusConflict, "username already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to create user")
		return
	}
	u, err := s.deps.Store.UserByID(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load created user")
		return
	}
	writeJSON(w, http.StatusCreated, userToJSON(u))
}

func (s *Server) handleAdminPatchUser(w http.ResponseWriter, r *http.Request) {
	id, err := parsePathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	u, err := s.deps.Store.UserByID(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "lookup failed")
		return
	}

	var req struct {
		Role       *string `json:"role"`
		MaxQuality *string `json:"maxQuality"`
		Password   *string `json:"password"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Role != nil {
		if *req.Role != "admin" && *req.Role != "viewer" {
			writeError(w, http.StatusBadRequest, "role must be admin or viewer")
			return
		}
		// Refuse demoting the last admin.
		if u.Role == "admin" && *req.Role != "admin" {
			n, err := countAdmins(s.deps.Store)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "failed to count admins")
				return
			}
			if n <= 1 {
				writeError(w, http.StatusConflict, "cannot demote the last admin")
				return
			}
		}
		u.Role = *req.Role
	}
	if req.MaxQuality != nil {
		u.MaxQuality = *req.MaxQuality
	}
	if err := s.deps.Store.UpdateUser(u); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update user")
		return
	}
	if req.Password != nil {
		if *req.Password == "" {
			writeError(w, http.StatusBadRequest, "password must not be empty")
			return
		}
		hash, err := auth.HashPassword(*req.Password)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to hash password")
			return
		}
		if err := s.deps.Store.UpdatePassword(u.ID, hash); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to update password")
			return
		}
	}

	u, err = s.deps.Store.UserByID(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to reload user")
		return
	}
	writeJSON(w, http.StatusOK, userToJSON(u))
}

func (s *Server) handleAdminDeleteUser(w http.ResponseWriter, r *http.Request) {
	id, err := parsePathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	u, err := s.deps.Store.UserByID(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "lookup failed")
		return
	}
	if u.Role == "admin" {
		n, err := countAdmins(s.deps.Store)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to count admins")
			return
		}
		if n <= 1 {
			writeError(w, http.StatusConflict, "cannot delete the last admin")
			return
		}
	}
	if err := s.deps.Store.DeleteUser(id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to delete user")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func parsePathID(r *http.Request, name string) (int64, error) {
	return strconv.ParseInt(r.PathValue(name), 10, 64)
}

func countAdmins(st *store.Store) (int, error) {
	users, err := st.ListUsers()
	if err != nil {
		return 0, err
	}
	n := 0
	for _, u := range users {
		if u.Role == "admin" {
			n++
		}
	}
	return n, nil
}

func isUniqueConstraint(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique constraint") || strings.Contains(msg, "constraint failed")
}

// --- Device / tuner / channel admin (Task 7) ---

type deviceJSON struct {
	DeviceID   string    `json:"deviceId"`
	IP         string    `json:"ip"`
	Model      string    `json:"model"`
	TunerCount int       `json:"tunerCount"`
	Manual     bool      `json:"manual"`
	LastSeen   time.Time `json:"lastSeen"`
	StreamPort int       `json:"streamPort"`
}

type tunerStatusJSON struct {
	Resource               string `json:"resource"`
	VctNumber              string `json:"vctNumber"`
	VctName                string `json:"vctName"`
	Frequency              int64  `json:"frequency"`
	SignalStrengthPercent  int    `json:"signalStrengthPercent"`
	SignalQualityPercent   int    `json:"signalQualityPercent"`
	SymbolQualityPercent   int    `json:"symbolQualityPercent"`
	TargetIP               string `json:"targetIp"`
}

type deviceStatusJSON struct {
	Device    deviceJSON        `json:"device"`
	Reachable bool              `json:"reachable"`
	Tuners    []tunerStatusJSON `json:"tuners"`
}

type adminChannelJSON struct {
	ID           int64  `json:"id"`
	DeviceID     string `json:"deviceId"`
	GuideNumber  string `json:"guideNumber"`
	Name         string `json:"name"`
	Enabled      bool   `json:"enabled"`
	EPGChannelID string `json:"epgChannelId"`
}

type viewerChannelJSON struct {
	ID          int64  `json:"id"`
	GuideNumber string `json:"guideNumber"`
	Name        string `json:"name"`
	LogoURL     string `json:"logoUrl"`
}

func deviceToJSON(d store.Device) deviceJSON {
	return deviceJSON{
		DeviceID:   d.DeviceID,
		IP:         d.IP,
		Model:      d.Model,
		TunerCount: d.TunerCount,
		Manual:     d.Manual,
		LastSeen:   d.LastSeen,
		StreamPort: d.StreamPort,
	}
}

func deviceStatusToJSON(ds tuner.DeviceStatus) deviceStatusJSON {
	tuners := make([]tunerStatusJSON, 0, len(ds.Tuners))
	for _, t := range ds.Tuners {
		tuners = append(tuners, tunerStatusJSON{
			Resource:              t.Resource,
			VctNumber:             t.VctNumber,
			VctName:               t.VctName,
			Frequency:             t.Frequency,
			SignalStrengthPercent: t.SignalStrengthPercent,
			SignalQualityPercent:  t.SignalQualityPercent,
			SymbolQualityPercent:  t.SymbolQualityPercent,
			TargetIP:              t.TargetIP,
		})
	}
	return deviceStatusJSON{
		Device:    deviceToJSON(ds.Device),
		Reachable: ds.Reachable,
		Tuners:    tuners,
	}
}

func adminChannelToJSON(c store.Channel) adminChannelJSON {
	return adminChannelJSON{
		ID:           c.ID,
		DeviceID:     c.DeviceID,
		GuideNumber:  c.GuideNumber,
		Name:         c.Name,
		Enabled:      c.Enabled,
		EPGChannelID: c.EPGChannelID,
	}
}

func (s *Server) handleAdminListTuners(w http.ResponseWriter, r *http.Request) {
	if s.deps.Tuners == nil {
		writeError(w, http.StatusInternalServerError, "tuners not configured")
		return
	}
	statuses := s.deps.Tuners.Devices()
	devices := make([]deviceStatusJSON, 0, len(statuses))
	for _, ds := range statuses {
		devices = append(devices, deviceStatusToJSON(ds))
	}
	// ingestChannels: channel IDs with an open device stream (incl. 5s tail).
	// Payload-only this cycle (A4); no web UI for the field yet.
	ingestChannels := []int64{}
	if s.deps.Streams != nil {
		if chans := s.deps.Streams.IngestChannels(); chans != nil {
			ingestChannels = chans
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"devices":        devices,
		"ingestChannels": ingestChannels,
	})
}

func (s *Server) handleAdminAddDevice(w http.ResponseWriter, r *http.Request) {
	if s.deps.Tuners == nil {
		writeError(w, http.StatusInternalServerError, "tuners not configured")
		return
	}
	var req struct {
		IP string `json:"ip"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.IP = strings.TrimSpace(req.IP)
	if req.IP == "" {
		writeError(w, http.StatusBadRequest, "ip required")
		return
	}

	baseURL := hdhr.BaseURLFromManual(req.IP)
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	info, err := hdhr.FetchDiscover(ctx, baseURL)
	if err != nil || info.DeviceID == "" {
		writeError(w, http.StatusUnprocessableEntity, "device unreachable")
		return
	}

	ip := hdhr.HostFromBaseURL(info.BaseURL)
	if ip == "" {
		ip = hdhr.HostFromBaseURL(baseURL)
	}
	streamPort := hdhr.StreamPortFromBaseURL(info.BaseURL)
	if streamPort <= 0 {
		streamPort = hdhr.StreamPortFromBaseURL(baseURL)
	}
	now := time.Now().UTC()
	dev := store.Device{
		DeviceID:   info.DeviceID,
		IP:         ip,
		Model:      info.ModelNumber,
		TunerCount: info.TunerCount,
		Manual:     true,
		LastSeen:   now,
		StreamPort: streamPort,
	}
	if err := s.deps.Store.UpsertDevice(dev); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save device")
		return
	}

	// Lineup sync for this device.
	lineupBase := info.BaseURL
	if lineupBase == "" {
		lineupBase = baseURL
	}
	if err := syncDeviceLineup(ctx, s.deps.Store, info.DeviceID, lineupBase); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to sync lineup")
		return
	}

	// Refresh manager cache so GET /admin/tuners sees the device.
	if err := s.deps.Tuners.Refresh(ctx); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to refresh tuners")
		return
	}

	writeJSON(w, http.StatusCreated, deviceToJSON(dev))
}

func (s *Server) handleAdminDeleteDevice(w http.ResponseWriter, r *http.Request) {
	if s.deps.Tuners == nil {
		writeError(w, http.StatusInternalServerError, "tuners not configured")
		return
	}
	deviceID := r.PathValue("deviceId")
	if deviceID == "" {
		writeError(w, http.StatusBadRequest, "device id required")
		return
	}
	if err := s.deps.Store.DeleteDevice(deviceID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete device")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	// Best-effort refresh; device is already gone from store.
	_ = s.deps.Tuners.Refresh(ctx)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAdminSyncChannels(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	devices, err := s.deps.Store.ListDevices()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list devices")
		return
	}
	for _, d := range devices {
		base := hdhr.HTTPBaseURL(d.IP, d.StreamPort)
		if base == "" {
			continue
		}
		if err := syncDeviceLineup(ctx, s.deps.Store, d.DeviceID, base); err != nil {
			// Skip unreachable devices; continue with others.
			continue
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAdminListChannels(w http.ResponseWriter, r *http.Request) {
	chans, err := s.deps.Store.ListChannels(false)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list channels")
		return
	}
	out := make([]adminChannelJSON, 0, len(chans))
	for _, c := range chans {
		out = append(out, adminChannelToJSON(c))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleAdminPatchChannel(w http.ResponseWriter, r *http.Request) {
	id, err := parsePathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid channel id")
		return
	}
	ch, err := s.deps.Store.ChannelByID(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "channel not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "lookup failed")
		return
	}

	var req struct {
		Enabled      *bool   `json:"enabled"`
		EPGChannelID *string `json:"epgChannelId"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Enabled != nil {
		ch.Enabled = *req.Enabled
	}
	if req.EPGChannelID != nil {
		ch.EPGChannelID = *req.EPGChannelID
	}
	if err := s.deps.Store.UpdateChannel(ch.ID, ch.Enabled, ch.EPGChannelID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "channel not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to update channel")
		return
	}
	ch, err = s.deps.Store.ChannelByID(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to reload channel")
		return
	}
	writeJSON(w, http.StatusOK, adminChannelToJSON(ch))
}

// handleListChannels is the viewer-facing enabled-channel list.
func (s *Server) handleListChannels(w http.ResponseWriter, r *http.Request) {
	chans, err := s.deps.Store.ListChannels(true)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list channels")
		return
	}
	epgIcons, err := epgIconByID(s.deps.Store)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load epg icons")
		return
	}
	out := make([]viewerChannelJSON, 0, len(chans))
	for _, c := range chans {
		logo := ""
		if c.EPGChannelID != "" {
			logo = epgIcons[c.EPGChannelID]
		}
		out = append(out, viewerChannelJSON{
			ID:          c.ID,
			GuideNumber: c.GuideNumber,
			Name:        c.Name,
			LogoURL:     logo,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func syncDeviceLineup(ctx context.Context, st *store.Store, deviceID, baseURL string) error {
	entries, err := hdhr.FetchLineup(ctx, baseURL)
	if err != nil {
		return err
	}
	chans := make([]store.Channel, 0, len(entries))
	for _, e := range entries {
		chans = append(chans, store.Channel{
			DeviceID:    deviceID,
			GuideNumber: e.GuideNumber,
			Name:        e.GuideName,
		})
	}
	return st.SyncLineup(deviceID, chans)
}

func epgIconByID(st *store.Store) (map[string]string, error) {
	epgs, err := st.ListEPGChannels()
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(epgs))
	for _, e := range epgs {
		out[e.ID] = e.IconURL
	}
	return out, nil
}

package api

import (
	"fmt"
	"log"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/ajthom90/bowtie/server/internal/epg/sd"
	"github.com/ajthom90/bowtie/server/internal/notify"
	"github.com/ajthom90/bowtie/server/internal/settings"
	"github.com/ajthom90/bowtie/server/internal/transcode"
)

// --- Admin settings GET/PUT (v0.4.0 Task 4) ---

type settingsXMLTVJSON struct {
	Source       string `json:"source"`
	RefreshHours int    `json:"refreshHours"`
}

type settingsSDJSON struct {
	Username           string `json:"username"`
	PasswordConfigured bool   `json:"passwordConfigured"`
	LineupID           string `json:"lineupId"`
}

type settingsTranscodeJSON struct {
	Encoder     string          `json:"encoder"`
	AllowHEVC   bool            `json:"allowHevc"`
	Available   []string        `json:"available"`
	HEVCCapable map[string]bool `json:"hevcCapable"`
}

type settingsStreamingJSON struct {
	BufferMinutes int  `json:"bufferMinutes"`
	Adaptive      bool `json:"adaptive"`
}

// settingsHDHomeRunJSON is the free SiliconDust guide section.
type settingsHDHomeRunJSON struct {
	Enabled bool `json:"enabled"`
}

// settingsDVRJSON is the recording section: padding and conversion quality.
type settingsDVRJSON struct {
	PadStartSeconds int    `json:"padStartSeconds"`
	PadEndSeconds   int    `json:"padEndSeconds"`
	Quality         string `json:"quality"`
}

type settingsResponseJSON struct {
	XMLTV           settingsXMLTVJSON     `json:"xmltv"`
	SchedulesDirect settingsSDJSON        `json:"schedulesDirect"`
	Transcode       settingsTranscodeJSON `json:"transcode"`
	Streaming       settingsStreamingJSON `json:"streaming"`
	HDHomeRun       settingsHDHomeRunJSON `json:"hdhomerun"`
	DVR             settingsDVRJSON       `json:"dvr"`
	Notifications   settingsNotifyJSON    `json:"notifications"`
}

// settingsNotifyJSON is the admin notification section.
type settingsNotifyJSON struct {
	URL    string                   `json:"url"`
	Events settingsNotifyEventsJSON `json:"events"`
}

type settingsNotifyEventsJSON struct {
	RecordingFailed bool `json:"recordingFailed"`
	DiskLow         bool `json:"diskLow"`
	RecordingReady  bool `json:"recordingReady"`
	GuideFailed     bool `json:"guideFailed"`
}

// putSettingsRequest is a section-merge body: nil section = untouched.
// Within a present section every field is required except schedulesDirect.password
// (absent or empty = keep existing), streaming.adaptive and dvr.quality (absent =
// keep existing). streaming is optional (omit = leave unchanged).
type putSettingsRequest struct {
	XMLTV           *putXMLTVSection     `json:"xmltv"`
	SchedulesDirect *putSDSection        `json:"schedulesDirect"`
	Transcode       *putTranscodeSection `json:"transcode"`
	Streaming       *putStreamingSection `json:"streaming"`
	HDHomeRun       *putHDHomeRunSection `json:"hdhomerun"`
	DVR             *putDVRSection       `json:"dvr"`
	Notifications   *putNotifySection    `json:"notifications"`
}

// putNotifySection: url is required (empty turns notifications off); events
// and each event are optional (absent keeps the stored choice).
type putNotifySection struct {
	URL    *string `json:"url"`
	Events *struct {
		RecordingFailed *bool `json:"recordingFailed,omitempty"`
		DiskLow         *bool `json:"diskLow,omitempty"`
		RecordingReady  *bool `json:"recordingReady,omitempty"`
		GuideFailed     *bool `json:"guideFailed,omitempty"`
	} `json:"events,omitempty"`
}

type putDVRSection struct {
	// Both are required within the section.
	PadStartSeconds *int `json:"padStartSeconds"`
	PadEndSeconds   *int `json:"padEndSeconds"`
	// Quality is optional (older clients omit it): nil keeps the stored value.
	Quality *string `json:"quality,omitempty"`
}

type putHDHomeRunSection struct {
	// Enabled is required within the section.
	Enabled *bool `json:"enabled"`
}

type putXMLTVSection struct {
	Source       string `json:"source"`
	RefreshHours int    `json:"refreshHours"`
}

type putSDSection struct {
	Username string `json:"username"`
	Password string `json:"password"`
	LineupID string `json:"lineupId"`
}

type putTranscodeSection struct {
	Encoder   string `json:"encoder"`
	AllowHEVC bool   `json:"allowHevc"`
}

type putStreamingSection struct {
	BufferMinutes int `json:"bufferMinutes"`
	// Adaptive is optional: nil keeps the stored value.
	Adaptive *bool `json:"adaptive,omitempty"`
}

type lineupJSON struct {
	LineupID  string `json:"lineupId"`
	Name      string `json:"name"`
	Location  string `json:"location"`
	Transport string `json:"transport"`
}

func (s *Server) handleAdminGetSettings(w http.ResponseWriter, r *http.Request) {
	if s.deps.Settings == nil {
		writeError(w, http.StatusInternalServerError, "settings not configured")
		return
	}
	out, err := s.buildSettingsResponse()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load settings")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleAdminPutSettings(w http.ResponseWriter, r *http.Request) {
	if s.deps.Settings == nil {
		writeError(w, http.StatusInternalServerError, "settings not configured")
		return
	}

	var req putSettingsRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// A3: validate ALL present sections fully BEFORE any write.
	kv, errMsg := s.validateAndBuildSettingsMap(req)
	if errMsg != "" {
		writeError(w, http.StatusBadRequest, errMsg)
		return
	}

	if len(kv) > 0 {
		if err := s.deps.Settings.Apply(kv); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to save settings")
			return
		}
		// Turning the free guide off removes its data and automatic mappings.
		if kv[settings.KeyEPGHDHomeRun] == "false" && s.deps.EPG != nil {
			if err := s.deps.EPG.ClearHDHomeRun(); err != nil {
				log.Printf("settings: clear hdhomerun guide: %v", err)
			}
		}
	}

	out, err := s.buildSettingsResponse()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load settings")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleAdminEPGLineups(w http.ResponseWriter, r *http.Request) {
	client, ok := s.sdClientFromSettings(w)
	if !ok {
		return
	}
	list, err := client.Lineups(r.Context())
	if err != nil {
		writeSDError(w, "lineups", err)
		return
	}
	writeLineups(w, list)
}

// handleAdminEPGHeadends searches the Schedules Direct lineups available in
// a postal code: GET /api/v1/admin/epg/headends?country=USA&postalcode=56071
func (s *Server) handleAdminEPGHeadends(w http.ResponseWriter, r *http.Request) {
	country := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("country")))
	postal := strings.TrimSpace(r.URL.Query().Get("postalcode"))
	if !sdCountryRe.MatchString(country) || postal == "" || len(postal) > 16 {
		writeError(w, http.StatusBadRequest, "country (3 letters, e.g. USA) and postal code are required")
		return
	}
	client, ok := s.sdClientFromSettings(w)
	if !ok {
		return
	}
	list, err := client.Headends(r.Context(), country, postal)
	if err != nil {
		writeSDError(w, "headends", err)
		return
	}
	writeLineups(w, list)
}

// handleAdminEPGAddLineup adds a lineup to the Schedules Direct account
// (SD-JSON lineups are managed by the app, not on the SD website).
func (s *Server) handleAdminEPGAddLineup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		LineupID string `json:"lineupId"`
	}
	if err := decodeJSON(r, &body); err != nil || !sdLineupIDRe.MatchString(body.LineupID) {
		writeError(w, http.StatusBadRequest, "a lineup id is required")
		return
	}
	client, ok := s.sdClientFromSettings(w)
	if !ok {
		return
	}
	if err := client.AddLineup(r.Context(), body.LineupID); err != nil {
		writeSDError(w, "add lineup", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

var (
	sdCountryRe  = regexp.MustCompile(`^[A-Z]{3}$`)
	sdLineupIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
)

// sdClientFromSettings builds an SD client from the saved credentials, or
// writes the error and returns ok=false.
func (s *Server) sdClientFromSettings(w http.ResponseWriter) (*sd.Client, bool) {
	if s.deps.Settings == nil {
		writeError(w, http.StatusInternalServerError, "settings not configured")
		return nil, false
	}
	sdCfg, err := s.deps.Settings.SD()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load schedules direct settings")
		return nil, false
	}
	if strings.TrimSpace(sdCfg.Username) == "" || sdCfg.Password == "" {
		writeError(w, http.StatusUnprocessableEntity, "schedules direct credentials not configured")
		return nil, false
	}
	return s.newSDClient(sdCfg.Username, sdCfg.Password), true
}

// writeSDError maps a Schedules Direct failure for the admin UI: 401 for
// rejected credentials, 502 with SD's own message when SD answered with an
// error, and 502 "unreachable" only for transport failures.
func writeSDError(w http.ResponseWriter, op string, err error) {
	log.Printf("api: schedules direct %s: %v", op, err)
	if sd.IsAuthError(err) {
		writeError(w, http.StatusUnauthorized, "schedules direct rejected the credentials")
		return
	}
	if msg, ok := sd.APIMessage(err); ok {
		writeError(w, http.StatusBadGateway, "Schedules Direct: "+msg)
		return
	}
	writeError(w, http.StatusBadGateway, "schedules direct is unreachable")
}

func writeLineups(w http.ResponseWriter, list []sd.LineupSummary) {
	out := make([]lineupJSON, 0, len(list))
	for _, lu := range list {
		out = append(out, lineupJSON{
			LineupID:  lu.LineupID,
			Name:      lu.Name,
			Location:  lu.Location,
			Transport: lu.Transport,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) newSDClient(username, password string) *sd.Client {
	c := &sd.Client{
		Username: username,
		Password: password,
	}
	if s.deps.SDBaseURL != "" {
		c.BaseURL = s.deps.SDBaseURL
	}
	if s.deps.SDHTTP != nil {
		c.HTTP = s.deps.SDHTTP
	}
	return c
}

func (s *Server) buildSettingsResponse() (settingsResponseJSON, error) {
	xmltv, err := s.deps.Settings.XMLTV()
	if err != nil {
		return settingsResponseJSON{}, err
	}
	sdCfg, err := s.deps.Settings.SD()
	if err != nil {
		return settingsResponseJSON{}, err
	}
	tc, err := s.deps.Settings.Transcode()
	if err != nil {
		return settingsResponseJSON{}, err
	}
	stream, err := s.deps.Settings.Streaming()
	if err != nil {
		return settingsResponseJSON{}, err
	}
	hdhrGuide, err := s.deps.Settings.HDHomeRunGuide()
	if err != nil {
		return settingsResponseJSON{}, err
	}
	dvrCfg, err := s.deps.Settings.DVR()
	if err != nil {
		return settingsResponseJSON{}, err
	}
	notif, err := s.deps.Settings.Notifications()
	if err != nil {
		return settingsResponseJSON{}, err
	}

	caps := s.probeCaps()
	available := make([]string, 0, len(caps.Available))
	for _, b := range caps.Available {
		available = append(available, string(b))
	}
	hevc := make(map[string]bool, len(caps.HEVC))
	for b, ok := range caps.HEVC {
		hevc[string(b)] = ok
	}

	return settingsResponseJSON{
		XMLTV: settingsXMLTVJSON{
			Source:       xmltv.Source,
			RefreshHours: xmltv.RefreshHours,
		},
		SchedulesDirect: settingsSDJSON{
			Username:           sdCfg.Username,
			PasswordConfigured: sdCfg.Password != "",
			LineupID:           sdCfg.LineupID,
		},
		Transcode: settingsTranscodeJSON{
			Encoder:     tc.Encoder,
			AllowHEVC:   tc.AllowHEVC,
			Available:   available,
			HEVCCapable: hevc,
		},
		Streaming: settingsStreamingJSON{
			BufferMinutes: stream.BufferMinutes,
			Adaptive:      stream.Adaptive,
		},
		HDHomeRun: settingsHDHomeRunJSON{Enabled: hdhrGuide.Enabled},
		DVR:       settingsDVRJSON{PadStartSeconds: dvrCfg.PadStartSeconds, PadEndSeconds: dvrCfg.PadEndSeconds, Quality: dvrCfg.Quality},
		Notifications: settingsNotifyJSON{URL: notif.URL, Events: settingsNotifyEventsJSON{
			RecordingFailed: notif.Events.RecordingFailed,
			DiskLow:         notif.Events.DiskLow,
			RecordingReady:  notif.Events.RecordingReady,
			GuideFailed:     notif.Events.GuideFailed,
		}},
	}, nil
}

func (s *Server) probeCaps() transcode.Capabilities {
	if s.deps.Probe == nil {
		return transcode.Capabilities{HEVC: map[transcode.Backend]bool{}}
	}
	caps := s.deps.Probe()
	if caps.HEVC == nil {
		caps.HEVC = map[transcode.Backend]bool{}
	}
	return caps
}

// validateAndBuildSettingsMap validates every present section and returns the
// key map for a single transactional Apply. On validation failure returns a
// non-empty error message and a nil map (nothing must be written).
func (s *Server) validateAndBuildSettingsMap(req putSettingsRequest) (map[string]string, string) {
	kv := make(map[string]string)

	if req.XMLTV != nil {
		if msg := validateXMLTVSource(req.XMLTV.Source); msg != "" {
			return nil, msg
		}
		if req.XMLTV.RefreshHours < 1 || req.XMLTV.RefreshHours > 168 {
			return nil, "refreshHours must be between 1 and 168"
		}
		kv[settings.KeyXMLTVSource] = req.XMLTV.Source
		kv[settings.KeyXMLTVRefreshHours] = strconv.Itoa(req.XMLTV.RefreshHours)
	}

	if req.SchedulesDirect != nil {
		// Empty username clears username + password + lineupId (full SD clear).
		if strings.TrimSpace(req.SchedulesDirect.Username) == "" {
			kv[settings.KeySDUsername] = ""
			kv[settings.KeySDPassword] = ""
			kv[settings.KeySDLineupID] = ""
		} else {
			kv[settings.KeySDUsername] = req.SchedulesDirect.Username
			kv[settings.KeySDLineupID] = req.SchedulesDirect.LineupID
			// Password: absent or empty = keep existing (omit key when empty).
			if req.SchedulesDirect.Password != "" {
				kv[settings.KeySDPassword] = req.SchedulesDirect.Password
			}
		}
	}

	if req.Transcode != nil {
		caps := s.probeCaps()
		if !validEncoder(req.Transcode.Encoder, caps.Available) {
			return nil, "encoder must be \"auto\" or a probed-available backend"
		}
		kv[settings.KeyTranscodeEncoder] = req.Transcode.Encoder
		kv[settings.KeyTranscodeAllowHEVC] = strconv.FormatBool(req.Transcode.AllowHEVC)
	}

	if req.Streaming != nil {
		if req.Streaming.BufferMinutes < 2 || req.Streaming.BufferMinutes > 60 {
			return nil, "bufferMinutes must be between 2 and 60"
		}
		kv[settings.KeyStreamingBufferMinutes] = strconv.Itoa(req.Streaming.BufferMinutes)
		if req.Streaming.Adaptive != nil {
			kv[settings.KeyStreamingAdaptive] = strconv.FormatBool(*req.Streaming.Adaptive)
		}
	}

	if req.HDHomeRun != nil {
		if req.HDHomeRun.Enabled == nil {
			return nil, "hdhomerun.enabled is required"
		}
		kv[settings.KeyEPGHDHomeRun] = strconv.FormatBool(*req.HDHomeRun.Enabled)
	}

	if req.DVR != nil {
		start, end := req.DVR.PadStartSeconds, req.DVR.PadEndSeconds
		switch {
		case start == nil:
			return nil, "dvr.padStartSeconds is required"
		case end == nil:
			return nil, "dvr.padEndSeconds is required"
		case *start < 0 || *start > settings.MaxPadStartSeconds:
			return nil, fmt.Sprintf("dvr.padStartSeconds must be between 0 and %d", settings.MaxPadStartSeconds)
		case *end < 0 || *end > settings.MaxPadEndSeconds:
			return nil, fmt.Sprintf("dvr.padEndSeconds must be between 0 and %d", settings.MaxPadEndSeconds)
		case req.DVR.Quality != nil && !settings.ValidDVRQuality(*req.DVR.Quality):
			return nil, fmt.Sprintf("dvr.quality must be one of %s", strings.Join(settings.DVRQualities, ", "))
		}
		kv[settings.KeyDVRPadStartSeconds] = strconv.Itoa(*start)
		kv[settings.KeyDVRPadEndSeconds] = strconv.Itoa(*end)
		if req.DVR.Quality != nil {
			kv[settings.KeyDVRQuality] = *req.DVR.Quality
		}
	}

	if req.Notifications != nil {
		n := req.Notifications
		if n.URL == nil {
			return nil, "notifications.url is required"
		}
		u := strings.TrimSpace(*n.URL)
		if u != "" {
			if err := notify.ValidateURL(u); err != nil {
				return nil, "notifications.url " + err.Error()
			}
		}
		kv[settings.KeyNotifyURL] = u
		if e := n.Events; e != nil {
			for key, v := range map[string]*bool{
				settings.KeyNotifyRecordingFailed: e.RecordingFailed,
				settings.KeyNotifyDiskLow:         e.DiskLow,
				settings.KeyNotifyRecordingReady:  e.RecordingReady,
				settings.KeyNotifyGuideFailed:     e.GuideFailed,
			} {
				if v != nil {
					kv[key] = strconv.FormatBool(*v)
				}
			}
		}
	}

	return kv, ""
}

func validateXMLTVSource(source string) string {
	if source == "" {
		return ""
	}
	if strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") {
		u, err := url.Parse(source)
		if err != nil || u.Host == "" {
			return "xmltv.source must be empty, an http(s) URL, or an absolute path"
		}
		return ""
	}
	if filepath.IsAbs(source) {
		return ""
	}
	return "xmltv.source must be empty, an http(s) URL, or an absolute path"
}

func validEncoder(enc string, available []transcode.Backend) bool {
	if enc == "auto" {
		return true
	}
	if enc == "" {
		return false
	}
	for _, b := range available {
		if string(b) == enc {
			return true
		}
	}
	return false
}

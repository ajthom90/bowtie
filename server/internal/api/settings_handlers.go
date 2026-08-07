package api

import (
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ajthom90/bowtie/server/internal/epg/sd"
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

type settingsResponseJSON struct {
	XMLTV           settingsXMLTVJSON     `json:"xmltv"`
	SchedulesDirect settingsSDJSON        `json:"schedulesDirect"`
	Transcode       settingsTranscodeJSON `json:"transcode"`
}

// putSettingsRequest is a section-merge body: nil section = untouched.
// Within a present section every field is required except schedulesDirect.password
// (absent or empty = keep existing).
type putSettingsRequest struct {
	XMLTV           *putXMLTVSection     `json:"xmltv"`
	SchedulesDirect *putSDSection        `json:"schedulesDirect"`
	Transcode       *putTranscodeSection `json:"transcode"`
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
	}

	out, err := s.buildSettingsResponse()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load settings")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleAdminEPGLineups(w http.ResponseWriter, r *http.Request) {
	if s.deps.Settings == nil {
		writeError(w, http.StatusInternalServerError, "settings not configured")
		return
	}
	sdCfg, err := s.deps.Settings.SD()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load schedules direct settings")
		return
	}
	if strings.TrimSpace(sdCfg.Username) == "" || sdCfg.Password == "" {
		writeError(w, http.StatusUnprocessableEntity, "schedules direct credentials not configured")
		return
	}

	client := s.newSDClient(sdCfg.Username, sdCfg.Password)
	list, err := client.Lineups(r.Context())
	if err != nil {
		if sd.IsAuthError(err) {
			writeError(w, http.StatusUnauthorized, "schedules direct rejected the credentials")
			return
		}
		writeError(w, http.StatusBadGateway, "schedules direct is unreachable")
		return
	}

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

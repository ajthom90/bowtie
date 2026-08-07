// Package sd is a Schedules Direct JSON API (20141201) client.
package sd

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ajthom90/bowtie/server/internal/store"
)

const (
	defaultBaseURL   = "https://json.schedulesdirect.org/20141201"
	defaultUserAgent = "Bowtie/1.0"
	// programBatchSize is the client-side batch limit for POST /programs.
	// The server allows up to 5000; we use 500 for smaller responses.
	programBatchSize = 500
	// tokenExpiredCode is the SD error code for an expired token (HTTP 403).
	// Note: 4003 is INVALID_USER; TOKEN_EXPIRED is 4006 on the wire.
	tokenExpiredCode = 4006
)

// Client talks to the Schedules Direct JSON API.
type Client struct {
	BaseURL  string // default https://json.schedulesdirect.org/20141201
	HTTP     *http.Client
	Username string
	Password string // raw; SHA-1 hex is computed at request time
	token    string
}

// Lineup is a station/channel mapping from GET /lineups/{id}.
type Lineup struct {
	Map []struct {
		StationID string `json:"stationID"`
		Channel   string `json:"channel"`
	} `json:"map"`
	Stations []struct {
		StationID string `json:"stationID"`
		Callsign  string `json:"callsign"`
		Name      string `json:"name"`
		Logo      struct {
			URL string `json:"URL"`
		} `json:"logo"`
	} `json:"stations"`
}

// LineupSummary is one entry from GET /lineups (account lineup list).
// Wire keys verified against Schedules Direct API 20141201 wiki:
// lineup, name, location, transport (plus uri, ignored).
type LineupSummary struct {
	LineupID  string `json:"lineup"`
	Name      string `json:"name"`
	Location  string `json:"location"`
	Transport string `json:"transport"`
}

// StationSchedule is one element of the POST /schedules response.
// The server may return one object per stationID/date combination.
type StationSchedule struct {
	StationID string `json:"stationID"`
	Programs  []struct {
		ProgramID   string    `json:"programID"`
		AirDateTime time.Time `json:"airDateTime"`
		Duration    int       `json:"duration"` // seconds
	} `json:"programs"`
}

// ProgramDetail is program metadata from POST /programs.
type ProgramDetail struct {
	ProgramID string `json:"programID"`
	Titles    []struct {
		Title120 string `json:"title120"`
	} `json:"titles"`
	Descriptions struct {
		Description1000 []struct {
			Description string `json:"description"`
		} `json:"description1000"`
	} `json:"descriptions"`
	EpisodeTitle150 string   `json:"episodeTitle150"`
	Genres          []string `json:"genres"`
}

type apiError struct {
	Response string `json:"response"`
	Code     int    `json:"code"`
	Message  string `json:"message"`
}

func (e apiError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("sd: %s (code %d)", e.Message, e.Code)
	}
	return fmt.Sprintf("sd: error code %d", e.Code)
}

// Token authenticates and caches the session token.
// POST /token with {username, password: sha1hex(password)}.
func (c *Client) Token(ctx context.Context) error {
	body := map[string]string{
		"username": c.Username,
		"password": sha1Hex(c.Password),
	}
	var resp struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Token   string `json:"token"`
	}
	if err := c.do(ctx, http.MethodPost, "/token", body, &resp, false); err != nil {
		return err
	}
	if resp.Code != 0 {
		return fmt.Errorf("sd: token: code %d: %s", resp.Code, resp.Message)
	}
	if resp.Token == "" {
		return fmt.Errorf("sd: token: empty token in response")
	}
	c.token = resp.Token
	return nil
}

// Lineup downloads the channel map and stations for a lineup ID.
// GET /lineups/{lineupID}
func (c *Client) Lineup(ctx context.Context, lineupID string) (Lineup, error) {
	var lu Lineup
	path := "/lineups/" + strings.TrimPrefix(lineupID, "/")
	if err := c.doAuthed(ctx, http.MethodGet, path, nil, &lu); err != nil {
		return Lineup{}, err
	}
	return lu, nil
}

// Lineups lists lineups the account has added at Schedules Direct.
// GET /lineups — response shape: {code, lineups:[{lineup,name,location,transport,uri}, ...]}.
func (c *Client) Lineups(ctx context.Context) ([]LineupSummary, error) {
	var resp struct {
		Code    int             `json:"code"`
		Lineups []LineupSummary `json:"lineups"`
	}
	if err := c.doAuthed(ctx, http.MethodGet, "/lineups", nil, &resp); err != nil {
		return nil, err
	}
	if resp.Code != 0 {
		return nil, fmt.Errorf("sd: lineups: code %d", resp.Code)
	}
	if resp.Lineups == nil {
		return []LineupSummary{}, nil
	}
	return resp.Lineups, nil
}

// IsAuthError reports whether err is an SD authentication-class failure suitable
// for mapping to HTTP 401 at the admin API:
//   - apiError with code 4003 or response INVALID_USER
//   - Token() rejection including HTTP-200-with-nonzero-code token responses
//
// Transport errors, timeouts, and 5xx are not auth-class (map to 502).
func IsAuthError(err error) bool {
	if err == nil {
		return false
	}
	var ae apiError
	if errors.As(err, &ae) {
		if ae.Code == 4003 || strings.EqualFold(ae.Response, "INVALID_USER") {
			return true
		}
		return false
	}
	// Token() returns fmt.Errorf("sd: token: code %d: %s", ...) on HTTP 200
	// responses with a nonzero code (credential / account rejection).
	msg := err.Error()
	return strings.Contains(msg, "sd: token: code ")
}

// Schedules fetches schedule data for the given stations and dates.
// POST /schedules with body [{stationID, date:[...]}, ...].
// If dates is empty, the request omits the date field so the server returns
// the full available range.
func (c *Client) Schedules(ctx context.Context, stationIDs []string, dates []string) ([]StationSchedule, error) {
	type reqItem struct {
		StationID string   `json:"stationID"`
		Date      []string `json:"date,omitempty"`
	}
	req := make([]reqItem, 0, len(stationIDs))
	for _, id := range stationIDs {
		item := reqItem{StationID: id}
		if len(dates) > 0 {
			item.Date = dates
		}
		req = append(req, item)
	}
	var out []StationSchedule
	if err := c.doAuthed(ctx, http.MethodPost, "/schedules", req, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Programs fetches program details, batching programIDs into POSTs of at most 500.
// POST /programs with body ["EP...", ...]; response is an array keyed into a map by programID.
func (c *Client) Programs(ctx context.Context, programIDs []string) (map[string]ProgramDetail, error) {
	out := make(map[string]ProgramDetail, len(programIDs))
	for i := 0; i < len(programIDs); i += programBatchSize {
		end := i + programBatchSize
		if end > len(programIDs) {
			end = len(programIDs)
		}
		batch := programIDs[i:end]
		var details []ProgramDetail
		if err := c.doAuthed(ctx, http.MethodPost, "/programs", batch, &details); err != nil {
			return nil, err
		}
		for _, d := range details {
			if d.ProgramID == "" {
				continue
			}
			out[d.ProgramID] = d
		}
	}
	return out, nil
}

// ToStore converts SD lineup, schedules, and program details into store types.
// EPGChannel.ID = "sd-"+stationID; Source = "sd".
// Program times = AirDateTime .. AirDateTime+Duration seconds.
// Title from title120, description from description1000[0], subtitle from
// episodeTitle150, category from genres[0].
func ToStore(lineup Lineup, scheds []StationSchedule, details map[string]ProgramDetail) ([]store.EPGChannel, []store.Program) {
	chans := make([]store.EPGChannel, 0, len(lineup.Stations))
	for _, st := range lineup.Stations {
		chans = append(chans, store.EPGChannel{
			ID:          "sd-" + st.StationID,
			DisplayName: st.Name,
			Callsign:    st.Callsign,
			IconURL:     st.Logo.URL,
			Source:      "sd",
		})
	}

	var progs []store.Program
	for _, sched := range scheds {
		epgID := "sd-" + sched.StationID
		for _, sp := range sched.Programs {
			d := details[sp.ProgramID]
			title := ""
			if len(d.Titles) > 0 {
				title = d.Titles[0].Title120
			}
			desc := ""
			if len(d.Descriptions.Description1000) > 0 {
				desc = d.Descriptions.Description1000[0].Description
			}
			cat := ""
			if len(d.Genres) > 0 {
				cat = d.Genres[0]
			}
			progs = append(progs, store.Program{
				EPGChannelID: epgID,
				Start:        sp.AirDateTime,
				Stop:         sp.AirDateTime.Add(time.Duration(sp.Duration) * time.Second),
				Title:        title,
				Subtitle:     d.EpisodeTitle150,
				Description:  desc,
				Category:     cat,
			})
		}
	}
	return chans, progs
}

func (c *Client) base() string {
	if c.BaseURL != "" {
		return strings.TrimRight(c.BaseURL, "/")
	}
	return defaultBaseURL
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

// doAuthed ensures a token is present, sends the token header, and on
// HTTP 403 with code 4006 (TOKEN_EXPIRED) re-authenticates once and retries.
func (c *Client) doAuthed(ctx context.Context, method, path string, body, dest any) error {
	if c.token == "" {
		if err := c.Token(ctx); err != nil {
			return err
		}
	}
	err := c.do(ctx, method, path, body, dest, true)
	if err == nil {
		return nil
	}
	ae, ok := err.(apiError)
	if !ok || ae.Code != tokenExpiredCode {
		return err
	}
	// Re-authenticate once and retry.
	c.token = ""
	if err := c.Token(ctx); err != nil {
		return err
	}
	return c.do(ctx, method, path, body, dest, true)
}

func (c *Client) do(ctx context.Context, method, path string, body, dest any, withToken bool) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("sd: marshal: %w", err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base()+path, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", defaultUserAgent)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if withToken && c.token != "" {
		req.Header.Set("token", c.token)
	}

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("sd: %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("sd: read body: %w", err)
	}

	if resp.StatusCode >= 400 {
		var ae apiError
		if json.Unmarshal(data, &ae) == nil && (ae.Code != 0 || ae.Message != "") {
			return ae
		}
		return fmt.Errorf("sd: %s %s: HTTP %d: %s", method, path, resp.StatusCode, truncate(string(data), 200))
	}

	if dest == nil {
		return nil
	}
	if err := json.Unmarshal(data, dest); err != nil {
		return fmt.Errorf("sd: decode %s: %w", path, err)
	}
	return nil
}

func sha1Hex(password string) string {
	sum := sha1.Sum([]byte(password))
	return hex.EncodeToString(sum[:]) // lowercase
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

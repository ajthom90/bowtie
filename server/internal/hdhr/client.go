// Package hdhr is an HDHomeRun HTTP client and UDP discovery implementation.
package hdhr

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DiscoverInfo is the device identity returned by /discover.json (and partially by UDP discover).
type DiscoverInfo struct {
	FriendlyName    string `json:"FriendlyName"`
	ModelNumber     string `json:"ModelNumber"`
	DeviceID        string `json:"DeviceID"`
	FirmwareVersion string `json:"FirmwareVersion"`
	BaseURL         string `json:"BaseURL"`
	LineupURL       string `json:"LineupURL"`
	TunerCount      int    `json:"TunerCount"`
}

// LineupEntry is one channel from /lineup.json.
type LineupEntry struct {
	GuideNumber string `json:"GuideNumber"`
	GuideName   string `json:"GuideName"`
	URL         string `json:"URL"`
	VideoCodec  string `json:"VideoCodec"`
	AudioCodec  string `json:"AudioCodec"`
}

// TunerStatus is one tuner entry from /status.json.
type TunerStatus struct {
	Resource               string `json:"Resource"`
	VctNumber              string `json:"VctNumber"`
	VctName                string `json:"VctName"`
	Frequency              int64  `json:"Frequency"`
	SignalStrengthPercent  int    `json:"SignalStrengthPercent"`
	SignalQualityPercent   int    `json:"SignalQualityPercent"`
	SymbolQualityPercent   int    `json:"SymbolQualityPercent"`
	TargetIP               string `json:"TargetIP"`
}

// DefaultHTTPClient is used by Fetch* helpers. Tests may replace it.
var DefaultHTTPClient = &http.Client{Timeout: 5 * time.Second}

// FetchDiscover GETs <baseURL>/discover.json.
func FetchDiscover(ctx context.Context, baseURL string) (DiscoverInfo, error) {
	var info DiscoverInfo
	if err := getJSON(ctx, joinURL(baseURL, "/discover.json"), &info); err != nil {
		return DiscoverInfo{}, err
	}
	return info, nil
}

// FetchLineup GETs <baseURL>/lineup.json.
func FetchLineup(ctx context.Context, baseURL string) ([]LineupEntry, error) {
	var out []LineupEntry
	if err := getJSON(ctx, joinURL(baseURL, "/lineup.json"), &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []LineupEntry{}
	}
	return out, nil
}

// FetchStatus GETs <baseURL>/status.json.
func FetchStatus(ctx context.Context, baseURL string) ([]TunerStatus, error) {
	var out []TunerStatus
	if err := getJSON(ctx, joinURL(baseURL, "/status.json"), &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []TunerStatus{}
	}
	return out, nil
}

// StreamPortFromBaseURL implements the stream-port rule:
// BaseURL port 80 or empty → 5004; otherwise reuse the BaseURL port.
func StreamPortFromBaseURL(baseURL string) int {
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" {
		return 5004
	}
	port := u.Port()
	if port == "" || port == "80" {
		return 5004
	}
	p, err := strconv.Atoi(port)
	if err != nil || p <= 0 {
		return 5004
	}
	return p
}

// HostFromBaseURL returns the hostname (no port) from a BaseURL.
func HostFromBaseURL(baseURL string) string {
	u, err := url.Parse(baseURL)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// HTTPBaseURL builds the HTTP base URL used to reach a device for discover/lineup/status.
// When streamPort is 5004 (or 0), HTTP is assumed on the default port (80).
// Otherwise HTTP shares the stream port (hdhrfake / nonstandard setups).
func HTTPBaseURL(ip string, streamPort int) string {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return ""
	}
	// ip may already include a port (manual cfg host:port).
	if strings.Contains(ip, ":") && !strings.HasPrefix(ip, "[") {
		// IPv4 host:port or bare "host:port"
		return "http://" + ip
	}
	if streamPort <= 0 || streamPort == 5004 {
		return "http://" + ip
	}
	return fmt.Sprintf("http://%s:%d", ip, streamPort)
}

// BaseURLFromManual turns a cfg Devices entry (IP or host:port) into an HTTP base URL.
func BaseURLFromManual(entry string) string {
	entry = strings.TrimSpace(entry)
	if entry == "" {
		return ""
	}
	if strings.HasPrefix(entry, "http://") || strings.HasPrefix(entry, "https://") {
		return strings.TrimRight(entry, "/")
	}
	return "http://" + entry
}

func joinURL(base, path string) string {
	return strings.TrimRight(base, "/") + path
}

func getJSON(ctx context.Context, rawURL string, dest any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	resp, err := DefaultHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("hdhr %s: status %d", rawURL, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(dest); err != nil {
		return fmt.Errorf("hdhr %s: decode: %w", rawURL, err)
	}
	return nil
}

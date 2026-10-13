package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// Delivery targets, chosen from the URL's shape.
const (
	TargetNtfy    = "ntfy"
	TargetDiscord = "discord"
	TargetWebhook = "webhook"
)

// SendTimeout bounds one delivery attempt.
const SendTimeout = 5 * time.Second

// maxURLLen bounds the configured URL.
const maxURLLen = 2048

// discordMaxContent is Discord's message length limit.
const discordMaxContent = 2000

// Result is the outcome of one delivery attempt.
type Result struct {
	// Target is how the message was formatted: ntfy, discord or webhook.
	Target string `json:"target"`
	OK     bool   `json:"ok"`
	// Status is the HTTP status the target answered (0: no answer).
	Status int `json:"status,omitempty"`
	// Error says what went wrong; it never contains the URL's path, query
	// or credentials.
	Error string `json:"error,omitempty"`
}

// retryable: no answer, a server error or "slow down" may pass on a retry; a
// 4xx (wrong topic, deleted webhook) won't.
func (r Result) retryable() bool {
	return !r.OK && (r.Status == 0 || r.Status >= 500 || r.Status == http.StatusTooManyRequests)
}

// ValidateURL accepts an absolute http(s) URL with a host.
func ValidateURL(raw string) error {
	if len(raw) > maxURLLen {
		return fmt.Errorf("must be at most %d characters", maxURLLen)
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return errors.New("must be an http(s) URL")
	}
	return nil
}

// Host is the URL's host name, the only part of it that is ever logged (the
// path or credentials may hold a secret).
func Host(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return "(invalid URL)"
	}
	return u.Hostname()
}

// TargetFor picks the message format for a URL: ntfy when the host contains
// "ntfy" (ntfy.sh or a self-hosted ntfy.example.com), Discord for its webhook
// URLs, otherwise a generic JSON POST.
func TargetFor(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return TargetWebhook
	}
	host := strings.ToLower(u.Hostname())
	switch {
	case strings.Contains(host, "ntfy"):
		return TargetNtfy
	case isDiscordHost(host) && discordWebhookPath.MatchString(u.Path):
		return TargetDiscord
	}
	return TargetWebhook
}

// discordWebhookPath: /api/webhooks/... or a versioned /api/v10/webhooks/...
var discordWebhookPath = regexp.MustCompile(`^/api/(v\d+/)?webhooks/`)

// maxTitleRunes caps a notification title (it can carry a recording title,
// which is user input, into an ntfy header).
const maxTitleRunes = 200

func capTitle(ev Event) Event {
	ev.Title = truncateRunes(ev.Title, maxTitleRunes)
	return ev
}

// noRedirects: a redirect is an answer, not followed — a 303 would turn the
// POST into a bodiless GET and look like success; a 307 would re-send the
// body (and maybe credentials) to another host.
func noRedirects(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

func isDiscordHost(host string) bool {
	for _, d := range []string{"discord.com", "discordapp.com"} {
		if host == d || strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return false
}

// Send delivers ev to rawURL once (no retry) and reports the outcome. A nil
// client uses one with SendTimeout.
func Send(ctx context.Context, client *http.Client, rawURL string, ev Event) Result {
	return SendTo(ctx, client, Destination{URL: rawURL}, ev)
}

// Destination is where notifications go. Username/Password, when either is
// set, are sent as HTTP Basic auth and override credentials in the URL (an
// ntfy access token goes in Password with an empty Username).
type Destination struct {
	URL      string
	Username string
	Password string
}

func (d Destination) hasCredentials() bool { return d.Username != "" || d.Password != "" }

// SendTo is Send with credentials kept apart from the URL.
func SendTo(ctx context.Context, client *http.Client, dest Destination, ev Event) Result {
	rawURL := dest.URL
	if client == nil {
		client = &http.Client{Timeout: SendTimeout, CheckRedirect: noRedirects}
	}
	ev = capTitle(ev)
	if ev.Time.IsZero() {
		ev.Time = time.Now()
	}
	target := TargetFor(rawURL)
	res := Result{Target: target}
	if err := ValidateURL(rawURL); err != nil {
		res.Error = "notification URL " + err.Error()
		return res
	}
	req, err := buildRequest(ctx, dest, target, ev)
	if err != nil {
		res.Error = cleanError(err, rawURL)
		return res
	}
	resp, err := client.Do(req)
	if err != nil {
		res.Error = cleanError(err, rawURL)
		return res
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	res.Status = resp.StatusCode
	res.OK = resp.StatusCode >= 200 && resp.StatusCode < 300
	if !res.OK {
		res.Error = fmt.Sprintf("%s answered %s", Host(rawURL), resp.Status)
	}
	return res
}

// cleanError drops the URL from an HTTP client error ("Post \"https://…\":")
// so a token in the path never reaches a log or the admin API.
func cleanError(err error, rawURL string) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		if ue.Timeout() {
			return fmt.Sprintf("%s didn't answer within %s", Host(rawURL), SendTimeout)
		}
		err = ue.Err
	}
	return fmt.Sprintf("%s: %s", Host(rawURL), strings.ReplaceAll(err.Error(), rawURL, Host(rawURL)))
}

func buildRequest(ctx context.Context, dest Destination, target string, ev Event) (*http.Request, error) {
	u, err := url.Parse(dest.URL)
	if err != nil {
		return nil, err
	}
	user := u.User
	u.User = nil // sent as an Authorization header instead
	var (
		body        []byte
		contentType string
	)
	switch target {
	case TargetNtfy:
		body, contentType = []byte(ev.Message), "text/plain; charset=utf-8"
	case TargetDiscord:
		body, err = json.Marshal(discordPayload{
			Content: truncateRunes(discordContent(ev), discordMaxContent),
			// A recording title with @everyone must not ping a server.
			AllowedMentions: allowedMentions{Parse: []string{}},
		})
		contentType = "application/json"
	default:
		body, err = json.Marshal(webhookPayload{
			Event:       ev.Kind,
			Title:       ev.Title,
			Message:     ev.Message,
			RecordingID: ev.RecordingID,
			Time:        ev.Time.UTC().Format(time.RFC3339),
		})
		contentType = "application/json"
	}
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("User-Agent", "Bowtie")
	switch {
	case dest.hasCredentials():
		req.SetBasicAuth(dest.Username, dest.Password)
	case user != nil:
		pass, _ := user.Password()
		req.SetBasicAuth(user.Username(), pass)
	}
	if target == TargetNtfy {
		req.Header.Set("Title", headerText(ev.Title))
		req.Header.Set("Priority", ntfyPriority(ev.Kind))
		req.Header.Set("Tags", ntfyTags(ev.Kind))
	}
	return req, nil
}

type discordPayload struct {
	Content         string          `json:"content"`
	AllowedMentions allowedMentions `json:"allowed_mentions"`
}

type allowedMentions struct {
	Parse []string `json:"parse"`
}

type webhookPayload struct {
	Event       string `json:"event"`
	Title       string `json:"title"`
	Message     string `json:"message"`
	RecordingID int64  `json:"recordingId,omitempty"`
	Time        string `json:"time"`
}

func discordContent(ev Event) string {
	if ev.Title == "" {
		return ev.Message
	}
	return "**" + ev.Title + "**\n" + ev.Message
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}

// headerText makes a header-safe title: one line, RFC 2047-encoded when it
// isn't plain ASCII (ntfy decodes it).
func headerText(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	return mime.BEncoding.Encode("utf-8", s)
}

func ntfyPriority(kind string) string {
	switch kind {
	case EventRecordingFailed, EventDiskLow, EventGuideFailed:
		return "high"
	}
	return "default"
}

// ntfyTags are ntfy emoji short codes shown next to the title.
func ntfyTags(kind string) string {
	switch kind {
	case EventRecordingFailed:
		return "warning,tv"
	case EventDiskLow:
		return "warning,floppy_disk"
	case EventGuideFailed:
		return "warning,calendar"
	case EventRecordingReady:
		return "white_check_mark,tv"
	}
	return "bell"
}

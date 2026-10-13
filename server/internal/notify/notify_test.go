package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ajthom90/bowtie/server/internal/settings"
)

// captured is one request a fake target received.
type captured struct {
	path   string
	header http.Header
	body   []byte
}

// target is an httptest server that records requests and answers with the
// next status in its script (200 once the script runs out).
type target struct {
	*httptest.Server
	mu     sync.Mutex
	reqs   []captured
	script []int
	got    chan struct{}
}

func newTarget(t *testing.T, script ...int) *target {
	t.Helper()
	tg := &target{script: script, got: make(chan struct{}, 16)}
	tg.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		tg.mu.Lock()
		tg.reqs = append(tg.reqs, captured{path: r.URL.Path, header: r.Header.Clone(), body: body})
		status := http.StatusOK
		if len(tg.script) > 0 {
			status, tg.script = tg.script[0], tg.script[1:]
		}
		tg.mu.Unlock()
		w.WriteHeader(status)
		tg.got <- struct{}{}
	}))
	t.Cleanup(tg.Close)
	return tg
}

func (tg *target) requests() []captured {
	tg.mu.Lock()
	defer tg.mu.Unlock()
	return append([]captured(nil), tg.reqs...)
}

func (tg *target) wait(t *testing.T, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		select {
		case <-tg.got:
		case <-time.After(3 * time.Second):
			t.Fatalf("got %d requests, want %d", len(tg.requests()), n)
		}
	}
}

// quiet asserts no request arrives for a moment.
func (tg *target) quiet(t *testing.T) {
	t.Helper()
	select {
	case <-tg.got:
		t.Fatalf("unexpected request; all: %d", len(tg.requests()))
	case <-time.After(100 * time.Millisecond):
	}
}

// rewrite sends every request to the httptest server, whatever host the URL
// names, so ntfy.sh / discord.com URLs are exercised without leaving the box.
func rewrite(tg *target) *http.Client {
	return &http.Client{Timeout: SendTimeout, Transport: rewriteTransport{to: tg.Listener.Addr().String()}}
}

type rewriteTransport struct{ to string }

func (rt rewriteTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.URL.Scheme, r.URL.Host = "http", rt.to
	return http.DefaultTransport.RoundTrip(r)
}

var when = time.Date(2026, 10, 4, 18, 30, 0, 0, time.UTC)

func failedEvent(id int64) Event {
	return Event{
		Kind:        EventRecordingFailed,
		Title:       "Recording failed: The Evening News",
		Message:     "The Evening News on 9.1 KARE didn't record: No signal.",
		RecordingID: id,
		Key:         "recordingFailed:" + strconv.FormatInt(id, 10),
		Time:        when,
	}
}

func TestTargetFor(t *testing.T) {
	for url, want := range map[string]string{
		"https://ntfy.sh/bowtie":                         TargetNtfy,
		"https://user:pass@ntfy.example.com/alerts":      TargetNtfy,
		"http://my-ntfy:8080/topic":                      TargetNtfy,
		"https://discord.com/api/webhooks/1/abc":         TargetDiscord,
		"https://discordapp.com/api/webhooks/1/abc":      TargetDiscord,
		"https://ptb.discord.com/api/webhooks/1/abc":     TargetDiscord,
		"https://discord.com/channels/1":                 TargetWebhook,
		"https://notdiscord.com/api/webhooks/1/abc":      TargetWebhook,
		"https://hooks.example.com/bowtie":               TargetWebhook,
		"http://homeassistant.local:8123/api/webhook/xy": TargetWebhook,
	} {
		if got := TargetFor(url); got != want {
			t.Errorf("TargetFor(%q) = %q, want %q", url, got, want)
		}
	}
}

func TestValidateURL(t *testing.T) {
	for _, ok := range []string{"https://ntfy.sh/x", "http://10.0.0.5:8080/hook", "https://u:p@ntfy.example.com/t"} {
		if err := ValidateURL(ok); err != nil {
			t.Errorf("ValidateURL(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"ntfy.sh/x", "ftp://ntfy.sh/x", "https://", "/local/path", "javascript:alert(1)", "https://x/" + strings.Repeat("a", 2100)} {
		if err := ValidateURL(bad); err == nil {
			t.Errorf("ValidateURL(%q) = nil, want error", bad)
		}
	}
}

func TestSendNtfyHeadersAndBasicAuth(t *testing.T) {
	tg := newTarget(t)
	res := Send(context.Background(), rewrite(tg), "https://alice:s3cret@ntfy.sh/bowtie-alerts", failedEvent(7))
	if !res.OK || res.Target != TargetNtfy || res.Status != 200 {
		t.Fatalf("result = %+v", res)
	}
	r := tg.requests()[0]
	if r.path != "/bowtie-alerts" {
		t.Errorf("path = %q", r.path)
	}
	if string(r.body) != "The Evening News on 9.1 KARE didn't record: No signal." {
		t.Errorf("body = %q", r.body)
	}
	for k, want := range map[string]string{
		"Title":        "Recording failed: The Evening News",
		"Priority":     "high",
		"Tags":         "warning,tv",
		"Content-Type": "text/plain; charset=utf-8",
	} {
		if got := r.header.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	req := &http.Request{Header: r.header}
	if u, p, ok := req.BasicAuth(); !ok || u != "alice" || p != "s3cret" {
		t.Errorf("basic auth = %q %q %v", u, p, ok)
	}
}

func TestSendNtfyReadyIsDefaultPriorityAndEncodesUnicodeTitle(t *testing.T) {
	tg := newTarget(t)
	ev := Event{Kind: EventRecordingReady, Title: "Recording ready: Café\nNoir", Message: "ready", Time: when}
	if res := Send(context.Background(), rewrite(tg), "https://ntfy.sh/t", ev); !res.OK {
		t.Fatalf("result = %+v", res)
	}
	h := tg.requests()[0].header
	if h.Get("Priority") != "default" || h.Get("Tags") != "white_check_mark,tv" {
		t.Errorf("priority=%q tags=%q", h.Get("Priority"), h.Get("Tags"))
	}
	if title := h.Get("Title"); !strings.HasPrefix(title, "=?utf-8?b?") || strings.ContainsAny(title, "\r\n") {
		t.Errorf("Title = %q, want one RFC 2047 line", title)
	}
}

func TestSendDiscordJSON(t *testing.T) {
	tg := newTarget(t, http.StatusNoContent)
	res := Send(context.Background(), rewrite(tg), "https://discord.com/api/webhooks/123/tok", failedEvent(7))
	if !res.OK || res.Target != TargetDiscord || res.Status != 204 {
		t.Fatalf("result = %+v", res)
	}
	r := tg.requests()[0]
	if r.header.Get("Content-Type") != "application/json" || r.path != "/api/webhooks/123/tok" {
		t.Errorf("content-type=%q path=%q", r.header.Get("Content-Type"), r.path)
	}
	var got map[string]any
	if err := json.Unmarshal(r.body, &got); err != nil {
		t.Fatal(err)
	}
	if got["content"] != "**Recording failed: The Evening News**\nThe Evening News on 9.1 KARE didn't record: No signal." {
		t.Errorf("content = %q", got["content"])
	}
	am, _ := got["allowed_mentions"].(map[string]any)
	if parse, ok := am["parse"].([]any); !ok || len(parse) != 0 {
		t.Errorf("allowed_mentions = %v, want parse: []", got["allowed_mentions"])
	}
}

func TestSendDiscordTruncatesTo2000(t *testing.T) {
	tg := newTarget(t)
	ev := Event{Kind: EventRecordingFailed, Title: "t", Message: strings.Repeat("é", 3000), Time: when}
	Send(context.Background(), rewrite(tg), "https://discord.com/api/webhooks/1/x", ev)
	var got struct{ Content string }
	_ = json.Unmarshal(tg.requests()[0].body, &got)
	if n := len([]rune(got.Content)); n != 2000 {
		t.Fatalf("content runes = %d, want 2000", n)
	}
}

func TestSendGenericJSON(t *testing.T) {
	tg := newTarget(t)
	res := Send(context.Background(), nil, tg.URL+"/hook", failedEvent(7))
	if !res.OK || res.Target != TargetWebhook {
		t.Fatalf("result = %+v", res)
	}
	var got map[string]any
	if err := json.Unmarshal(tg.requests()[0].body, &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"event":       "recordingFailed",
		"title":       "Recording failed: The Evening News",
		"message":     "The Evening News on 9.1 KARE didn't record: No signal.",
		"recordingId": float64(7),
		"time":        "2026-10-04T18:30:00Z",
	}
	if len(got) != len(want) {
		t.Errorf("payload = %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %v, want %v", k, got[k], v)
		}
	}
	// No recording: no recordingId.
	Send(context.Background(), nil, tg.URL+"/hook", TestEvent(when))
	got = map[string]any{}
	_ = json.Unmarshal(tg.requests()[1].body, &got)
	if _, has := got["recordingId"]; has || got["event"] != "test" {
		t.Errorf("test payload = %v", got)
	}
}

// A failure reports the status, and an unreachable host's error never repeats
// the URL's path (which may be a secret topic or webhook token).
func TestSendFailureHidesURL(t *testing.T) {
	tg := newTarget(t, http.StatusForbidden)
	res := Send(context.Background(), nil, tg.URL+"/secret-topic", TestEvent(when))
	if res.OK || res.Status != 403 || strings.Contains(res.Error, "secret-topic") || !strings.Contains(res.Error, "403") {
		t.Fatalf("403 result = %+v", res)
	}
	ln := tg.Listener.Addr().String()
	tg.Close()
	res = Send(context.Background(), nil, "http://"+ln+"/secret-topic", TestEvent(when))
	if res.OK || res.Status != 0 || res.Error == "" || strings.Contains(res.Error, "secret-topic") {
		t.Fatalf("unreachable result = %+v", res)
	}
	res = Send(context.Background(), nil, "http://u:hunter2@"+ln+"/secret-topic", TestEvent(when))
	if strings.Contains(res.Error, "hunter2") || strings.Contains(res.Error, "secret-topic") {
		t.Fatalf("credentials leaked: %+v", res)
	}
}

func TestSendTimesOut(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-block }))
	defer srv.Close()
	defer close(block)
	res := Send(context.Background(), &http.Client{Timeout: 50 * time.Millisecond}, srv.URL+"/t", TestEvent(when))
	if res.OK || !strings.Contains(res.Error, "didn't answer") {
		t.Fatalf("result = %+v", res)
	}
}

// --- Service ------------------------------------------------------------------

type cfgBox struct {
	mu sync.Mutex
	n  settings.Notifications
	// failures: how many reads fail before they work again.
	failures int
}

func (c *cfgBox) get() (settings.Notifications, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failures > 0 {
		c.failures--
		return settings.Notifications{}, errors.New("database is locked")
	}
	return c.n, nil
}

func (c *cfgBox) set(n settings.Notifications) {
	c.mu.Lock()
	c.n = n
	c.mu.Unlock()
}

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time      { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *fakeClock) Add(d time.Duration) { c.mu.Lock(); c.now = c.now.Add(d); c.mu.Unlock() }

func startService(t *testing.T, url string, opts Options) (*Service, *cfgBox, *fakeClock) {
	t.Helper()
	box := &cfgBox{n: settings.Notifications{URL: url, Events: settings.DefaultNotificationEvents}}
	clk := &fakeClock{now: when}
	opts.Now = clk.Now
	if opts.RetryAfter == 0 {
		opts.RetryAfter = 20 * time.Millisecond
	}
	s := New(box.get, opts)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return s, box, clk
}

func diskLow() Event {
	return Event{Kind: EventDiskLow, Title: "Bowtie is low on disk space", Message: "Only 1.5 GB free."}
}

func TestServiceRateLimitsSameKey(t *testing.T) {
	tg := newTarget(t)
	s, _, clk := startService(t, tg.URL+"/h", Options{})
	s.Notify(diskLow())
	tg.wait(t, 1)
	s.Notify(diskLow())
	tg.quiet(t)
	clk.Add(5 * time.Hour)
	s.Notify(diskLow())
	tg.quiet(t)
	clk.Add(time.Hour + time.Second) // 6 h after the first
	s.Notify(diskLow())
	tg.wait(t, 1)
	// A different key isn't limited by diskLow's.
	s.Notify(Event{Kind: EventGuideFailed, Key: "guideFailed:xmltv", Title: "g", Message: "m"})
	tg.wait(t, 1)
}

func TestServiceRecordingFailedOncePerRecording(t *testing.T) {
	tg := newTarget(t)
	s, _, clk := startService(t, tg.URL+"/h", Options{})
	s.Notify(failedEvent(1))
	s.Notify(failedEvent(2))
	tg.wait(t, 2)
	clk.Add(48 * time.Hour)
	s.Notify(failedEvent(1))
	tg.quiet(t)
}

func TestServiceHonorsEventChoicesAndEmptyURL(t *testing.T) {
	tg := newTarget(t)
	s, box, _ := startService(t, tg.URL+"/h", Options{})
	s.Notify(Event{Kind: EventRecordingReady, Title: "ready", Message: "m"}) // off by default
	tg.quiet(t)
	box.set(settings.Notifications{URL: "", Events: settings.NotificationEvents{DiskLow: true}})
	s.Notify(diskLow())
	tg.quiet(t)
	// The skipped events didn't use up the rate limit.
	box.set(settings.Notifications{URL: tg.URL + "/h", Events: settings.NotificationEvents{DiskLow: true, RecordingReady: true}})
	s.Notify(diskLow())
	s.Notify(Event{Kind: EventRecordingReady, Title: "ready", Message: "m"})
	tg.wait(t, 2)
}

func TestServiceRetriesOnceAfter5xx(t *testing.T) {
	tg := newTarget(t, http.StatusBadGateway, http.StatusBadGateway, http.StatusBadGateway)
	s, _, _ := startService(t, tg.URL+"/h", Options{})
	s.Notify(diskLow())
	tg.wait(t, 2) // the first try and one retry
	tg.quiet(t)   // and no more
}

func TestServiceRetrySucceeds(t *testing.T) {
	tg := newTarget(t, http.StatusServiceUnavailable)
	s, _, _ := startService(t, tg.URL+"/h", Options{})
	start := time.Now()
	s.Notify(diskLow())
	tg.wait(t, 2)
	if time.Since(start) < 20*time.Millisecond {
		t.Fatal("retried before RetryAfter")
	}
	if got := tg.requests(); string(got[0].body) != string(got[1].body) {
		t.Fatal("retry sent a different payload")
	}
}

func TestServiceDoesNotRetry4xx(t *testing.T) {
	tg := newTarget(t, http.StatusNotFound)
	s, _, _ := startService(t, tg.URL+"/h", Options{})
	s.Notify(diskLow())
	tg.wait(t, 1)
	tg.quiet(t)
}

// Notify returns at once even when the target hangs, and a full queue drops
// (and logs) instead of blocking.
func TestServiceNotifyNeverBlocks(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-block }))
	defer srv.Close()
	defer close(block)

	var logs syncBuffer
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	s, _, _ := startService(t, srv.URL+"/secret-topic", Options{QueueSize: 2, Client: &http.Client{Timeout: 2 * time.Second}})
	start := time.Now()
	for i := 0; i < 20; i++ {
		s.Notify(Event{Kind: EventGuideFailed, Key: "guideFailed:" + strconv.Itoa(i), Title: "g", Message: "m"})
	}
	if d := time.Since(start); d > 200*time.Millisecond {
		t.Fatalf("Notify blocked for %s", d)
	}
	if !strings.Contains(logs.String(), "queue full") {
		t.Fatalf("no drop logged: %q", logs.String())
	}
}

// Failures are logged with the host only.
func TestServiceLogsHostOnly(t *testing.T) {
	tg := newTarget(t, http.StatusNotFound)
	var logs syncBuffer
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	s, _, _ := startService(t, "http://bob:pw@"+tg.Listener.Addr().String()+"/secret-topic", Options{})
	s.Notify(diskLow())
	tg.wait(t, 1)
	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(logs.String(), "failed") && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	out := logs.String()
	if !strings.Contains(out, "127.0.0.1") || strings.Contains(out, "secret-topic") || strings.Contains(out, "pw@") {
		t.Fatalf("log = %q", out)
	}
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

func TestTargetForVersionedDiscordWebhook(t *testing.T) {
	if got := TargetFor("https://discord.com/api/v10/webhooks/123/tok"); got != TargetDiscord {
		t.Fatalf("versioned webhook: %q", got)
	}
}

// A redirect isn't followed: a 303 would turn the POST into a bodiless GET
// (and report success), a 307 would re-send the body to another host.
func TestSendDoesNotFollowRedirects(t *testing.T) {
	hit := false
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hit = true }))
	defer other.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/x", http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	res := Send(context.Background(), nil, srv.URL+"/hook", Event{Kind: "test", Title: "t", Message: "m"})
	if res.OK || hit {
		t.Fatalf("redirect followed: ok=%v hit=%v", res.OK, hit)
	}
}

// Very long titles (a recording title is user input) are cut so the ntfy
// Title header stays small.
func TestCapTitle(t *testing.T) {
	ev := capTitle(Event{Title: strings.Repeat("é", 5000)})
	if n := len([]rune(ev.Title)); n > maxTitleRunes {
		t.Fatalf("title %d runes, want <= %d", n, maxTitleRunes)
	}
	if got := capTitle(Event{Title: "short"}).Title; got != "short" {
		t.Fatalf("short title changed: %q", got)
	}
}

// A settings read that fails (a busy database) doesn't lose the event: it's
// tried again after RetryAfter.
func TestServiceRetriesWhenSettingsCantBeRead(t *testing.T) {
	tg := newTarget(t)
	s, box, _ := startService(t, tg.URL+"/h", Options{})
	box.mu.Lock()
	box.failures = 1
	box.mu.Unlock()
	s.Notify(failedEvent(7))
	tg.wait(t, 1)
}

// The rate-limit memory drops entries once their window has passed.
func TestServiceForgetsOldRateEntries(t *testing.T) {
	tg := newTarget(t)
	s, _, clk := startService(t, tg.URL+"/h", Options{})
	for i := 0; i < 5; i++ {
		s.Notify(Event{Kind: EventGuideFailed, Key: fmt.Sprintf("guideFailed:%d", i), Title: "g", Message: "m"})
	}
	tg.wait(t, 5)
	clk.Add(7 * time.Hour)
	s.Notify(diskLow())
	tg.wait(t, 1)
	s.mu.Lock()
	n := len(s.sent)
	s.mu.Unlock()
	if n != 1 {
		t.Fatalf("rate memory has %d entries, want 1", n)
	}
}

func basicAuthOf(t *testing.T, r captured) (string, string) {
	t.Helper()
	u, p, ok := (&http.Request{Header: r.header}).BasicAuth()
	if !ok {
		t.Fatalf("no basic auth; Authorization = %q", r.header.Get("Authorization"))
	}
	return u, p
}

func TestSendToUsesUsernameAndPassword(t *testing.T) {
	// Passwords with URL-special characters work as separate fields.
	tg := newTarget(t)
	res := SendTo(context.Background(), rewrite(tg),
		Destination{URL: "https://ntfy.example.com/alerts", Username: "alice", Password: "p@ss:w/rd#1"}, failedEvent(7))
	if !res.OK {
		t.Fatalf("result = %+v", res)
	}
	if u, p := basicAuthOf(t, tg.requests()[0]); u != "alice" || p != "p@ss:w/rd#1" {
		t.Fatalf("basic auth = %q %q", u, p)
	}
}

func TestSendToTokenAsPasswordWithEmptyUsername(t *testing.T) {
	// ntfy takes an access token as the password with an empty username.
	tg := newTarget(t)
	SendTo(context.Background(), rewrite(tg), Destination{URL: "https://ntfy.sh/t", Password: "tk_abc123"}, failedEvent(7))
	if u, p := basicAuthOf(t, tg.requests()[0]); u != "" || p != "tk_abc123" {
		t.Fatalf("basic auth = %q %q", u, p)
	}
}

func TestSendToFieldsOverrideURLCredentials(t *testing.T) {
	tg := newTarget(t)
	SendTo(context.Background(), rewrite(tg),
		Destination{URL: "https://old:stale@ntfy.sh/t", Username: "alice", Password: "new"}, failedEvent(7))
	if u, p := basicAuthOf(t, tg.requests()[0]); u != "alice" || p != "new" {
		t.Fatalf("basic auth = %q %q", u, p)
	}
}

func TestSendToNoCredentialsSendsNoAuth(t *testing.T) {
	tg := newTarget(t)
	SendTo(context.Background(), rewrite(tg), Destination{URL: "https://ntfy.sh/t"}, failedEvent(7))
	if h := tg.requests()[0].header.Get("Authorization"); h != "" {
		t.Fatalf("Authorization = %q, want none", h)
	}
}

func TestServiceSendsSavedCredentials(t *testing.T) {
	tg := newTarget(t)
	s, box, _ := startService(t, tg.URL+"/h", Options{})
	box.set(settings.Notifications{URL: tg.URL + "/h", Username: "bob", Password: "hunter2", Events: settings.DefaultNotificationEvents})
	s.Notify(diskLow())
	tg.wait(t, 1)
	if u, p := basicAuthOf(t, tg.requests()[0]); u != "bob" || p != "hunter2" {
		t.Fatalf("basic auth = %q %q", u, p)
	}
}

func TestSendFailureHidesPassword(t *testing.T) {
	ln := "127.0.0.1:1" // nothing listens
	res := SendTo(context.Background(), nil, Destination{URL: "http://" + ln + "/t", Username: "u", Password: "hunter2"}, TestEvent(when))
	if res.OK || strings.Contains(res.Error, "hunter2") {
		t.Fatalf("result = %+v", res)
	}
}

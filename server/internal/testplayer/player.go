// Package testplayer is a virtual HLS client for Bowtie: it creates a session,
// polls the playlist and downloads new segments like a player would, and
// reports what a viewer would have experienced (stalls, sequence resets,
// discontinuities, a vanished session).
package testplayer

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Config configures a Player.
type Config struct {
	BaseURL   string // e.g. http://127.0.0.1:1234
	Token     string // bearer JWT
	ChannelID int64
	// Caps is the POST /sessions caps object; default h264/aac at ≤480p.
	Caps map[string]any
	// PollEvery is the playlist poll interval; default 1s.
	PollEvery time.Duration
	// HeartbeatEvery is the session heartbeat interval; default 15s.
	HeartbeatEvery time.Duration
	Client         *http.Client
}

// StartError is returned by Start when session creation is refused.
type StartError struct {
	Status int
	Body   string
}

func (e *StartError) Error() string {
	return fmt.Sprintf("start session: HTTP %d: %s", e.Status, e.Body)
}

// Report is the viewer experience of one run.
type Report struct {
	Polls           int
	SegmentsFetched int
	SegmentErrors   int
	BackwardJumps   int     // newest segment number went down between polls
	Discontinuities int     // #EXT-X-DISCONTINUITY on newly seen segments
	Newest          []int64 // newest absolute segment number at each poll
	// MaxGap is the longest stretch without a new segment, including the
	// stretch from the last new segment to the end of the run.
	MaxGap      time.Duration
	SessionGone bool
	Errors      []string
}

// Player is one virtual viewer.
type Player struct {
	cfg         Config
	viewerID    string
	playlistURL string
	streamToken string

	mu       sync.Mutex
	report   Report
	highest  int64
	lastNew  time.Time
	stopped  time.Time
	prevNews int64
}

// Start creates a session for cfg.ChannelID.
func Start(ctx context.Context, cfg Config) (*Player, error) {
	if cfg.Caps == nil {
		cfg.Caps = map[string]any{"videoCodecs": []string{"h264"}, "audioCodecs": []string{"aac"}, "maxHeight": 480}
	}
	if cfg.PollEvery <= 0 {
		cfg.PollEvery = time.Second
	}
	if cfg.HeartbeatEvery <= 0 {
		cfg.HeartbeatEvery = 15 * time.Second
	}
	if cfg.Client == nil {
		cfg.Client = &http.Client{Timeout: 30 * time.Second}
	}
	body, _ := json.Marshal(map[string]any{"channelId": cfg.ChannelID, "caps": cfg.Caps})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.BaseURL+"/api/v1/sessions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.Token)
	resp, err := cfg.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, &StartError{Status: resp.StatusCode, Body: string(raw)}
	}
	var out struct {
		ViewerID    string `json:"viewerId"`
		PlaylistURL string `json:"playlistUrl"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode session: %w", err)
	}
	u, err := url.Parse(out.PlaylistURL)
	if err != nil {
		return nil, err
	}
	return &Player{
		cfg:         cfg,
		viewerID:    out.ViewerID,
		playlistURL: out.PlaylistURL,
		streamToken: u.Query().Get("token"),
		highest:     -1,
		prevNews:    -1,
		lastNew:     time.Now(),
	}, nil
}

// ViewerID returns the session's viewer id.
func (p *Player) ViewerID() string { return p.viewerID }

// Run polls until ctx is done or the session disappears.
func (p *Player) Run(ctx context.Context) {
	defer func() {
		p.mu.Lock()
		p.stopped = time.Now()
		p.mu.Unlock()
	}()
	poll := time.NewTicker(p.cfg.PollEvery)
	defer poll.Stop()
	hb := time.NewTicker(p.cfg.HeartbeatEvery)
	defer hb.Stop()
	if p.poll(ctx) {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-hb.C:
			p.heartbeat(ctx)
		case <-poll.C:
			if p.poll(ctx) {
				return
			}
		}
	}
}

// Stop deletes the session (best effort).
func (p *Player) Stop(ctx context.Context) {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, p.cfg.BaseURL+"/api/v1/sessions/"+p.viewerID, nil)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+p.cfg.Token)
	if resp, err := p.cfg.Client.Do(req); err == nil {
		_ = resp.Body.Close()
	}
}

// Report returns the run so far.
func (p *Player) Report() Report {
	p.mu.Lock()
	defer p.mu.Unlock()
	r := p.report
	r.Newest = append([]int64(nil), r.Newest...)
	r.Errors = append([]string(nil), r.Errors...)
	end := p.stopped
	if end.IsZero() {
		end = time.Now()
	}
	if tail := end.Sub(p.lastNew); tail > r.MaxGap {
		r.MaxGap = tail
	}
	return r
}

func (p *Player) errorf(format string, a ...any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.report.Errors = append(p.report.Errors, fmt.Sprintf(format, a...))
}

func (p *Player) heartbeat(ctx context.Context) {
	u := p.cfg.BaseURL + "/api/v1/sessions/" + p.viewerID + "/heartbeat?token=" + url.QueryEscape(p.streamToken)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, nil)
	if err != nil {
		return
	}
	resp, err := p.cfg.Client.Do(req)
	if err != nil {
		if ctx.Err() == nil {
			p.errorf("heartbeat: %v", err)
		}
		return
	}
	_ = resp.Body.Close()
	if resp.StatusCode >= 300 {
		p.errorf("heartbeat: HTTP %d", resp.StatusCode)
	}
}

type entry struct {
	abs           int64
	uri           string
	discontinuity bool
}

// poll fetches the playlist and any new segments; it returns true when the
// session is gone.
func (p *Player) poll(ctx context.Context) bool {
	body, status, err := p.get(ctx, p.playlistURL)
	p.mu.Lock()
	p.report.Polls++
	p.mu.Unlock()
	if err != nil {
		if ctx.Err() == nil {
			p.errorf("playlist: %v", err)
		}
		return false
	}
	if status == http.StatusNotFound || status == http.StatusGone {
		p.mu.Lock()
		p.report.SessionGone = true
		p.mu.Unlock()
		return true
	}
	if status != http.StatusOK {
		p.errorf("playlist: HTTP %d", status)
		return false
	}
	entries := parsePlaylist(body)
	if len(entries) == 0 {
		return false
	}
	newest := entries[len(entries)-1].abs
	p.mu.Lock()
	p.report.Newest = append(p.report.Newest, newest)
	if p.prevNews >= 0 && newest < p.prevNews {
		p.report.BackwardJumps++
	}
	p.prevNews = newest
	p.mu.Unlock()

	for _, e := range entries {
		p.mu.Lock()
		isNew := e.abs > p.highest
		p.mu.Unlock()
		if !isNew {
			continue
		}
		seg, segStatus, err := p.get(ctx, e.uri)
		p.mu.Lock()
		switch {
		case err != nil || segStatus != http.StatusOK || len(seg) == 0 || seg[0] != 0x47:
			p.report.SegmentErrors++
		default:
			p.report.SegmentsFetched++
		}
		if e.discontinuity {
			p.report.Discontinuities++
		}
		now := time.Now()
		if gap := now.Sub(p.lastNew); gap > p.report.MaxGap {
			p.report.MaxGap = gap
		}
		p.lastNew = now
		p.highest = e.abs
		p.mu.Unlock()
	}
	return false
}

func (p *Player) get(ctx context.Context, ref string) ([]byte, int, error) {
	u := ref
	if !strings.HasPrefix(ref, "http://") && !strings.HasPrefix(ref, "https://") {
		u = p.cfg.BaseURL + ref
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, 0, err
	}
	resp, err := p.cfg.Client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	return b, resp.StatusCode, err
}

func parsePlaylist(body []byte) []entry {
	var out []entry
	var seq int64
	idx := int64(0)
	disc := false
	sc := bufio.NewScanner(bytes.NewReader(body))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case strings.HasPrefix(line, "#EXT-X-MEDIA-SEQUENCE:"):
			seq, _ = strconv.ParseInt(strings.TrimPrefix(line, "#EXT-X-MEDIA-SEQUENCE:"), 10, 64)
		case line == "#EXT-X-DISCONTINUITY":
			disc = true
		case line == "" || strings.HasPrefix(line, "#"):
		default:
			out = append(out, entry{abs: seq + idx, uri: line, discontinuity: disc})
			idx++
			disc = false
		}
	}
	return out
}

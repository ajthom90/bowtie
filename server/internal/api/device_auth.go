package api

import (
	"crypto/rand"
	"encoding/base64"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/ajthom90/bowtie/server/internal/auth"
	qrcode "github.com/skip2/go-qrcode"
)

// Quick sign-in for TV apps (device authorization): the TV shows a QR code
// and a short code; a signed-in phone or browser approves it at /link; the
// TV polls for its tokens.

const (
	deviceAuthTTL      = 10 * time.Minute
	deviceAuthInterval = 2 // seconds between polls
	maxPendingDevices  = 1000
	userCodeAlphabet   = "BCDFGHJKLMNPQRSTVWXZ23456789" // no vowels or look-alikes
)

type pendingDevice struct {
	userCode   string // 8 chars, no dash
	deviceName string
	expires    time.Time
	userID     int64 // set when approved
}

type deviceAuths struct {
	mu       sync.Mutex
	byDevice map[string]*pendingDevice // deviceCode → pending
}

func newDeviceAuths() *deviceAuths { return &deviceAuths{byDevice: map[string]*pendingDevice{}} }

func (d *deviceAuths) pruneLocked(now time.Time) {
	for k, p := range d.byDevice {
		if now.After(p.expires) {
			delete(d.byDevice, k)
		}
	}
}

func (d *deviceAuths) byUserCodeLocked(code string) (string, *pendingDevice) {
	for k, p := range d.byDevice {
		if p.userCode == code {
			return k, p
		}
	}
	return "", nil
}

func normalizeUserCode(s string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(s) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func randomUserCode() (string, error) {
	var b strings.Builder
	for i := 0; i < 8; i++ {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(userCodeAlphabet))))
		if err != nil {
			return "", err
		}
		b.WriteByte(userCodeAlphabet[n.Int64()])
	}
	return b.String(), nil
}

// handleDeviceStart serves POST /api/v1/auth/device (no auth).
func (s *Server) handleDeviceStart(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DeviceName string `json:"deviceName"`
	}
	_ = decodeJSON(r, &req)
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to start sign-in")
		return
	}
	deviceCode := base64.RawURLEncoding.EncodeToString(raw)
	now := time.Now()
	d := s.devices
	d.mu.Lock()
	d.pruneLocked(now)
	if len(d.byDevice) >= maxPendingDevices {
		d.mu.Unlock()
		writeError(w, http.StatusTooManyRequests, "too many sign-ins in progress; try again shortly")
		return
	}
	code := ""
	for code == "" {
		c, err := randomUserCode()
		if err != nil {
			d.mu.Unlock()
			writeError(w, http.StatusInternalServerError, "failed to start sign-in")
			return
		}
		if _, taken := d.byUserCodeLocked(c); taken == nil {
			code = c
		}
	}
	name := strings.TrimSpace(req.DeviceName)
	if len(name) > 60 {
		name = name[:60]
	}
	d.byDevice[deviceCode] = &pendingDevice{userCode: code, deviceName: name, expires: now.Add(deviceAuthTTL)}
	d.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"deviceCode": deviceCode,
		"userCode":   code[:4] + "-" + code[4:],
		"verifyUrl":  baseURL(r) + "/link?code=" + code,
		"qrUrl":      "/api/v1/auth/device/qr/" + code + ".png",
		"expiresIn":  int(deviceAuthTTL / time.Second),
		"interval":   deviceAuthInterval,
	})
}

// handleDeviceLookup serves GET /api/v1/auth/device/{userCode} (signed in):
// what is asking to sign in, so the approver can confirm.
func (s *Server) handleDeviceLookup(w http.ResponseWriter, r *http.Request) {
	code := normalizeUserCode(r.PathValue("userCode"))
	d := s.devices
	d.mu.Lock()
	d.pruneLocked(time.Now())
	_, p := d.byUserCodeLocked(code)
	var name string
	if p != nil {
		name = p.deviceName
	}
	d.mu.Unlock()
	if p == nil {
		writeError(w, http.StatusNotFound, "that code has expired or doesn't exist")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"deviceName": name})
}

// handleDeviceApprove serves POST /api/v1/auth/device/approve (signed in):
// the device signs in as the approver.
func (s *Server) handleDeviceApprove(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFrom(r.Context())
	var req struct {
		UserCode string `json:"userCode"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	d := s.devices
	d.mu.Lock()
	d.pruneLocked(time.Now())
	_, p := d.byUserCodeLocked(normalizeUserCode(req.UserCode))
	if p != nil {
		p.userID = claims.UserID
	}
	d.mu.Unlock()
	if p == nil {
		writeError(w, http.StatusNotFound, "that code has expired or doesn't exist")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleDeviceToken serves POST /api/v1/auth/device/token (the device's
// poll): 428 authorization_pending until approved, then the token pair once;
// 410 when expired, used or unknown.
func (s *Server) handleDeviceToken(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DeviceCode string `json:"deviceCode"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	now := time.Now()
	d := s.devices
	d.mu.Lock()
	d.pruneLocked(now)
	p, ok := d.byDevice[req.DeviceCode]
	approved := ok && p.userID != 0
	var userID int64
	if approved {
		userID = p.userID
		delete(d.byDevice, req.DeviceCode) // one use
	}
	d.mu.Unlock()
	switch {
	case !ok:
		writeJSON(w, http.StatusGone, map[string]string{"error": "expired_token"})
	case !approved:
		writeJSON(w, http.StatusPreconditionRequired, map[string]string{"error": "authorization_pending"})
	default:
		u, err := s.deps.Store.UserByID(userID)
		if err != nil {
			writeJSON(w, http.StatusGone, map[string]string{"error": "expired_token"})
			return
		}
		s.issueTokens(w, u, now.UTC())
	}
}

// handleDeviceQR serves GET /api/v1/auth/device/qr/{file} ("<userCode>.png"):
// the verify URL as a QR code, so TV apps can show it as a plain image.
func (s *Server) handleDeviceQR(w http.ResponseWriter, r *http.Request) {
	code := normalizeUserCode(strings.TrimSuffix(r.PathValue("file"), ".png"))
	d := s.devices
	d.mu.Lock()
	d.pruneLocked(time.Now())
	_, p := d.byUserCodeLocked(code)
	d.mu.Unlock()
	if p == nil {
		writeError(w, http.StatusNotFound, "that code has expired or doesn't exist")
		return
	}
	png, err := qrcode.Encode(baseURL(r)+"/link?code="+code, qrcode.Medium, 512)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to draw the code")
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(png)
}

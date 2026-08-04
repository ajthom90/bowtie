package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ajthom90/bowtie/server/internal/config"
	"github.com/ajthom90/bowtie/server/internal/store"
)

// TestSmokeAssembledServer starts the full assembly on a random free port,
// hits /healthz and login with the bootstrap admin, then shuts down cleanly.
func TestSmokeAssembledServer(t *testing.T) {
	dataDir := t.TempDir()
	segDir := filepath.Join(dataDir, "segments")
	if err := os.MkdirAll(segDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Pre-bootstrap admin so we can capture the password (run() logs it but
	// we need it for the login assertion without parsing logs).
	st, err := store.Open(filepath.Join(dataDir, "bowtie.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	adminPW, err := bootstrapAdmin(st)
	if err != nil {
		st.Close()
		t.Fatalf("bootstrapAdmin: %v", err)
	}
	if adminPW == "" {
		st.Close()
		t.Fatal("expected bootstrap password on empty store")
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close bootstrap store: %v", err)
	}

	cfg := config.Config{
		ListenAddr: "127.0.0.1:0",
		DataDir:    dataDir,
		SegmentDir: segDir,
		FFmpegPath: "ffmpeg",
		Encoder:    "auto",
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	addr, shutdown, err := run(ctx, cfg)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	defer shutdown()

	base := "http://" + addr
	client := &http.Client{Timeout: 5 * time.Second}

	// /healthz
	resp, err := client.Get(base + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("healthz status=%d body=%q", resp.StatusCode, body)
	}
	if string(body) != "ok" {
		t.Fatalf("healthz body=%q, want ok", body)
	}

	// Login with bootstrap admin.
	loginBody, _ := json.Marshal(map[string]string{
		"username": "admin",
		"password": adminPW,
	})
	resp, err = client.Post(base+"/api/v1/auth/login", "application/json", bytes.NewReader(loginBody))
	if err != nil {
		t.Fatalf("POST /auth/login: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login status=%d body=%q", resp.StatusCode, body)
	}
	var loginResp struct {
		AccessToken string `json:"accessToken"`
		User        struct {
			Username string `json:"username"`
			Role     string `json:"role"`
		} `json:"user"`
	}
	if err := json.Unmarshal(body, &loginResp); err != nil {
		t.Fatalf("decode login: %v body=%q", err, body)
	}
	if loginResp.AccessToken == "" {
		t.Fatal("login missing accessToken")
	}
	if loginResp.User.Username != "admin" || loginResp.User.Role != "admin" {
		t.Fatalf("login user=%+v", loginResp.User)
	}

	// Clean shutdown should complete without hang.
	done := make(chan struct{})
	go func() {
		shutdown()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("shutdown did not complete within 15s")
	}
}

// TestBootstrapAdminIdempotent ensures a second bootstrap leaves the password
// empty (no re-create) and does not wipe the existing admin.
func TestBootstrapAdminIdempotent(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	pw1, err := bootstrapAdmin(st)
	if err != nil {
		t.Fatal(err)
	}
	if pw1 == "" {
		t.Fatal("first bootstrap should return password")
	}
	pw2, err := bootstrapAdmin(st)
	if err != nil {
		t.Fatal(err)
	}
	if pw2 != "" {
		t.Fatalf("second bootstrap returned password %q, want empty", pw2)
	}
	n, err := st.CountUsers()
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("user count=%d, want 1", n)
	}
	u, err := st.UserByUsername("admin")
	if err != nil {
		t.Fatal(err)
	}
	if u.Role != "admin" {
		t.Fatalf("role=%q", u.Role)
	}
	if !strings.HasPrefix(u.PasswordHash, "$argon2id$") {
		t.Fatalf("unexpected hash prefix: %s", u.PasswordHash[:min(20, len(u.PasswordHash))])
	}
}

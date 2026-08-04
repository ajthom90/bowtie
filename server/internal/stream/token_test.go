package stream

import (
	"strings"
	"testing"
	"time"
)

func TestSignVerifyRoundTrip(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	exp := now.Add(12 * time.Hour)
	tok := SignStreamToken(secret, "viewer-abc", exp)
	if tok == "" {
		t.Fatal("empty token")
	}
	// Must be base64url without obvious pipe characters.
	if strings.Contains(tok, "|") {
		t.Fatalf("token should be base64url-encoded, got %q", tok)
	}
	vid, err := VerifyStreamToken(secret, tok, now)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if vid != "viewer-abc" {
		t.Fatalf("viewerID = %q, want viewer-abc", vid)
	}
}

func TestVerifyExpired(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	tok := SignStreamToken(secret, "v1", now.Add(time.Hour))
	// At exactly exp, token is expired (exp must be strictly after now).
	_, err := VerifyStreamToken(secret, tok, now.Add(time.Hour))
	if err == nil {
		t.Fatal("want expiry error at exact exp")
	}
	_, err = VerifyStreamToken(secret, tok, now.Add(time.Hour+time.Second))
	if err == nil {
		t.Fatal("want expiry error after exp")
	}
	// Still valid just before expiry.
	if _, err := VerifyStreamToken(secret, tok, now.Add(time.Hour-time.Second)); err != nil {
		t.Fatalf("should be valid before exp: %v", err)
	}
}

func TestVerifyTampered(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	tok := SignStreamToken(secret, "v1", now.Add(time.Hour))

	// Flip a character in the middle of the token.
	b := []byte(tok)
	mid := len(b) / 2
	if b[mid] == 'A' {
		b[mid] = 'B'
	} else {
		b[mid] = 'A'
	}
	if _, err := VerifyStreamToken(secret, string(b), now); err == nil {
		t.Fatal("want error for tampered token")
	}

	// Wrong secret.
	if _, err := VerifyStreamToken([]byte("other-secret-0123456789abcdef!!"), tok, now); err == nil {
		t.Fatal("want error for wrong secret")
	}
}

func TestVerifyEmptyAndMalformed(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	now := time.Now().UTC()
	if _, err := VerifyStreamToken(secret, "", now); err == nil {
		t.Fatal("want error for empty token")
	}
	if _, err := VerifyStreamToken(nil, "abc", now); err == nil {
		t.Fatal("want error for empty secret")
	}
	if _, err := VerifyStreamToken(secret, "not-valid-base64!!!", now); err == nil {
		t.Fatal("want error for bad encoding")
	}
}

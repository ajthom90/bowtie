package stream

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// SignStreamToken builds a signed, base64url-encoded stream access token.
// Format (before base64url): viewerID|expUnix|hex(hmacSHA256(secret, viewerID|expUnix))
func SignStreamToken(secret []byte, viewerID string, exp time.Time) string {
	payload := viewerID + "|" + strconv.FormatInt(exp.Unix(), 10)
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(payload))
	sig := hex.EncodeToString(mac.Sum(nil))
	raw := payload + "|" + sig
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// VerifyStreamToken checks HMAC (constant-time) and expiry, returning viewerID.
func VerifyStreamToken(secret []byte, token string, now time.Time) (viewerID string, err error) {
	if len(secret) == 0 {
		return "", errors.New("stream: empty token secret")
	}
	if token == "" {
		return "", errors.New("stream: empty token")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		// Also accept standard base64url with padding.
		decoded, err = base64.URLEncoding.DecodeString(token)
		if err != nil {
			return "", fmt.Errorf("stream: invalid token encoding: %w", err)
		}
	}
	parts := strings.Split(string(decoded), "|")
	if len(parts) != 3 {
		return "", errors.New("stream: malformed token")
	}
	vid, expStr, gotSig := parts[0], parts[1], parts[2]
	if vid == "" || expStr == "" || gotSig == "" {
		return "", errors.New("stream: malformed token")
	}
	payload := vid + "|" + expStr
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(payload))
	wantSig := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(gotSig), []byte(wantSig)) {
		return "", errors.New("stream: invalid token signature")
	}
	expUnix, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil {
		return "", errors.New("stream: invalid token expiry")
	}
	if !now.Before(time.Unix(expUnix, 0)) {
		return "", errors.New("stream: token expired")
	}
	return vid, nil
}

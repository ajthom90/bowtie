package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	argonTime    = 3
	argonMemory  = 64 * 1024 // KiB = 64 MiB
	argonThreads = 2
	argonKeyLen  = 32
	argonSaltLen = 16
)

// HashPassword returns a PHC-encoded Argon2id hash of pw.
// Format: $argon2id$v=19$m=65536,t=3,p=2$<salt-b64>$<key-b64>
func HashPassword(pw string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}
	key := argon2.IDKey([]byte(pw), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	b64Salt := base64.RawStdEncoding.EncodeToString(salt)
	b64Key := base64.RawStdEncoding.EncodeToString(key)
	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads, b64Salt, b64Key,
	), nil
}

// VerifyPassword checks pw against a PHC-encoded Argon2id hash.
// Returns (false, nil) for a well-formed hash that does not match.
func VerifyPassword(pw, encoded string) (bool, error) {
	salt, key, timeCost, memory, threads, keyLen, err := parseArgon2id(encoded)
	if err != nil {
		return false, err
	}
	other := argon2.IDKey([]byte(pw), salt, timeCost, memory, threads, keyLen)
	if subtle.ConstantTimeCompare(key, other) == 1 {
		return true, nil
	}
	return false, nil
}

func parseArgon2id(encoded string) (salt, key []byte, timeCost, memory uint32, threads uint8, keyLen uint32, err error) {
	// $argon2id$v=19$m=65536,t=3,p=2$salt$key
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 {
		return nil, nil, 0, 0, 0, 0, errors.New("invalid argon2id hash format")
	}
	if parts[1] != "argon2id" {
		return nil, nil, 0, 0, 0, 0, fmt.Errorf("unsupported algorithm %q", parts[1])
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return nil, nil, 0, 0, 0, 0, fmt.Errorf("parse version: %w", err)
	}
	if version != argon2.Version {
		return nil, nil, 0, 0, 0, 0, fmt.Errorf("unsupported argon2 version %d", version)
	}

	var m, t, p int
	for _, kv := range strings.Split(parts[3], ",") {
		name, val, ok := strings.Cut(kv, "=")
		if !ok {
			return nil, nil, 0, 0, 0, 0, fmt.Errorf("invalid param %q", kv)
		}
		n, err := strconv.Atoi(val)
		if err != nil {
			return nil, nil, 0, 0, 0, 0, fmt.Errorf("parse param %s: %w", name, err)
		}
		switch name {
		case "m":
			m = n
		case "t":
			t = n
		case "p":
			p = n
		default:
			return nil, nil, 0, 0, 0, 0, fmt.Errorf("unknown param %q", name)
		}
	}
	if m <= 0 || t <= 0 || p <= 0 {
		return nil, nil, 0, 0, 0, 0, errors.New("invalid argon2id cost parameters")
	}

	salt, err = base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return nil, nil, 0, 0, 0, 0, fmt.Errorf("decode salt: %w", err)
	}
	key, err = base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return nil, nil, 0, 0, 0, 0, fmt.Errorf("decode key: %w", err)
	}
	if len(key) == 0 {
		return nil, nil, 0, 0, 0, 0, errors.New("empty argon2id key")
	}
	return salt, key, uint32(t), uint32(m), uint8(p), uint32(len(key)), nil
}

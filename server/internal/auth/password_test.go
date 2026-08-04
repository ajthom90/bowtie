package auth_test

import (
	"strings"
	"testing"

	"github.com/ajthom90/bowtie/server/internal/auth"
)

func TestHashVerifyRoundTrip(t *testing.T) {
	hash, err := auth.HashPassword("s3cret-pass")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=65536,t=3,p=2$") {
		t.Fatalf("hash format = %q, want PHC argon2id prefix", hash)
	}
	parts := strings.Split(hash, "$")
	// "", "argon2id", "v=19", "m=...,t=...,p=...", salt, key
	if len(parts) != 6 {
		t.Fatalf("hash parts = %d, want 6: %q", len(parts), hash)
	}

	ok, err := auth.VerifyPassword("s3cret-pass", hash)
	if err != nil {
		t.Fatalf("VerifyPassword: %v", err)
	}
	if !ok {
		t.Fatal("VerifyPassword: want true for correct password")
	}
}

func TestVerifyWrongPassword(t *testing.T) {
	hash, err := auth.HashPassword("correct")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	ok, err := auth.VerifyPassword("wrong", hash)
	if err != nil {
		t.Fatalf("VerifyPassword: %v", err)
	}
	if ok {
		t.Fatal("VerifyPassword: want false for wrong password")
	}
}

package store_test

import (
	"testing"
	"time"

	"github.com/ajthom90/bowtie/server/internal/store"
)

func TestFeedKeyLookup(t *testing.T) {
	s := openTestStore(t)
	id, _ := s.CreateUser(store.User{Username: "tv", PasswordHash: "h", Role: "viewer", CreatedAt: time.Now()})
	if _, err := s.UserByFeedKeyHash("abc"); err == nil {
		t.Fatal("unknown key matched")
	}
	if err := s.SetFeedKeyHash(id, "abc"); err != nil {
		t.Fatal(err)
	}
	u, err := s.UserByFeedKeyHash("abc")
	if err != nil || u.ID != id {
		t.Fatalf("%+v %v", u, err)
	}
	if _, err := s.UserByFeedKeyHash(""); err == nil {
		t.Fatal("empty key matched a user without a feed")
	}
}

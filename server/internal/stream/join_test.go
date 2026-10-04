package stream

import (
	"context"
	"errors"
	"testing"
)

// SharePlay: a participant joins the sharer's exact session (one playlist,
// one clock), whatever quality it would otherwise get.
func TestJoinSharersSession(t *testing.T) {
	m, runner, _, ch, alice, bob := limitsEnv(t)
	h, err := m.Start(context.Background(), alice, ch[0], clientCaps("high"))
	if err != nil {
		t.Fatal(err)
	}
	j, err := m.Join(context.Background(), bob, h.SessionID, ch[0], clientCaps("low"))
	if err != nil {
		t.Fatal(err)
	}
	if j.SessionID != h.SessionID || runner.Starts() != 1 {
		t.Fatalf("join made a new session: %s vs %s, starts=%d", j.SessionID, h.SessionID, runner.Starts())
	}
	if _, ok := m.SessionMediaOf(j.ViewerID); !ok {
		t.Fatal("joined viewer has no media")
	}
}

func TestJoinRefusesOtherChannelOrUnknownSession(t *testing.T) {
	m, _, _, ch, alice, bob := limitsEnv(t)
	h, err := m.Start(context.Background(), alice, ch[0], clientCaps(""))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Join(context.Background(), bob, h.SessionID, ch[1], clientCaps("")); !errors.Is(err, ErrNotJoinable) {
		t.Fatalf("other channel: %v", err)
	}
	if _, err := m.Join(context.Background(), bob, "nope", ch[0], clientCaps("")); !errors.Is(err, ErrNotJoinable) {
		t.Fatalf("unknown session: %v", err)
	}
}

func TestJoinRespectsStreamLimit(t *testing.T) {
	m, _, _, ch, alice, bob := limitsEnv(t)
	h, _ := m.Start(context.Background(), alice, ch[0], clientCaps(""))
	bob.MaxStreams = 1
	if _, err := m.Start(context.Background(), bob, ch[1], clientCaps("")); err != nil {
		t.Fatal(err)
	}
	_, err := m.Join(context.Background(), bob, h.SessionID, ch[0], clientCaps(""))
	var le *UserLimitError
	if !errors.As(err, &le) || le.Kind != "streams" {
		t.Fatalf("limit not applied to join: %v", err)
	}
}

package stream

import (
	"bytes"
	"testing"
	"time"
)

func TestSubQueueDropsOldestAndResendsTables(t *testing.T) {
	q := newSubQueue(3*188, time.Now)
	a, b, c, d := bytes.Repeat([]byte{1}, 188), bytes.Repeat([]byte{2}, 188), bytes.Repeat([]byte{3}, 188), bytes.Repeat([]byte{4}, 188)
	tables := bytes.Repeat([]byte{9}, 188)
	for _, ch := range [][]byte{a, b, c} {
		if n := q.push(ch, tables); n != 0 {
			t.Fatalf("dropped %d before full", n)
		}
	}
	if n := q.push(d, tables); n == 0 {
		t.Fatal("want a drop when full")
	}
	got, _ := q.pop()
	if !bytes.Equal(got, tables) {
		t.Fatalf("after a drop the queue head must be PAT+PMT, got %v", got[:1])
	}
	var rest []byte
	for {
		q.mu.Lock()
		empty := len(q.chunks) == 0
		q.mu.Unlock()
		if empty {
			break
		}
		ch, _ := q.pop()
		rest = append(rest, ch[0])
	}
	if !bytes.Equal(rest, []byte{3, 4}) && !bytes.Equal(rest, []byte{2, 3, 4}) {
		t.Fatalf("remaining order %v: oldest must go first, newest must stay", rest)
	}
}

func TestSubQueuePopBlocksUntilPushOrClose(t *testing.T) {
	q := newSubQueue(1<<20, time.Now)
	got := make(chan bool, 1)
	go func() { _, ok := q.pop(); got <- ok }()
	select {
	case <-got:
		t.Fatal("pop returned on an empty queue")
	case <-time.After(50 * time.Millisecond):
	}
	q.close()
	if ok := <-got; ok {
		t.Fatal("pop after close must report !ok")
	}
}

func TestSubQueueIdleFor(t *testing.T) {
	now := time.Date(2026, 10, 2, 20, 0, 0, 0, time.UTC)
	q := newSubQueue(1<<20, func() time.Time { return now })
	if d := q.idleFor(now.Add(time.Hour)); d != 0 {
		t.Fatalf("empty queue idleFor = %v, want 0", d)
	}
	q.push(make([]byte, 188), nil)
	if d := q.idleFor(now.Add(31 * time.Second)); d < 30*time.Second {
		t.Fatalf("idleFor = %v, want ≥30s with bytes queued and no pop", d)
	}
}

// Device reads can split a TS packet; only whole packets are handed on and the
// remainder carries into the next read.
func TestAlignTS(t *testing.T) {
	whole, rest := alignTS(nil, make([]byte, 100))
	if len(whole) != 0 || len(rest) != 100 {
		t.Fatalf("100 bytes: whole=%d rest=%d, want 0/100", len(whole), len(rest))
	}
	whole, rest = alignTS(rest, make([]byte, 188*2-100+50))
	if len(whole) != 188*2 || len(rest) != 50 {
		t.Fatalf("carry+426: whole=%d rest=%d, want 376/50", len(whole), len(rest))
	}
	whole, rest = alignTS(nil, make([]byte, 188*3))
	if len(whole) != 188*3 || len(rest) != 0 {
		t.Fatalf("exact: whole=%d rest=%d", len(whole), len(rest))
	}
}

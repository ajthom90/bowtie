package stream

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// A device that never answers fails at the header timeout instead of hanging.
func TestHTTPDialHeaderTimeout(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-block }))
	defer srv.Close()
	defer close(block)
	dial := NewHTTPDial(time.Second, 300*time.Millisecond)
	start := time.Now()
	_, _, err := dial(context.Background(), srv.URL+"/auto/v9.1")
	if err == nil {
		t.Fatal("want timeout error")
	}
	if el := time.Since(start); el > 2*time.Second {
		t.Fatalf("dial took %v, want about 300ms", el)
	}
}

// The HDHomeRun answers 807 after ~10s; a slower-than-instant answer under the
// header timeout must arrive intact.
func TestHTTPDialSlowNoSignalAnswerArrives(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.Header().Set("X-HDHomeRun-Error", "807 No Video Data")
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	_, _, err := NewHTTPDial(time.Second, 500*time.Millisecond)(context.Background(), srv.URL+"/auto/v9.1")
	if !errors.Is(err, ErrNoSignal) {
		t.Fatalf("err = %v, want ErrNoSignal", err)
	}
}

// The device stream must outlive the request that started it: cancelling the
// request must not kill the connection (which would show up as a redial).
func TestAttachStreamOutlivesRequestContext(t *testing.T) {
	var requests atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		for i := 0; i < 50; i++ {
			if _, err := w.Write(make([]byte, 188*10)); err != nil {
				return
			}
			w.(http.Flusher).Flush()
			time.Sleep(20 * time.Millisecond)
		}
	}))
	defer srv.Close()
	im := NewIngestManager(HTTPDial)
	defer im.Shutdown()
	ctx, cancel := context.WithCancel(context.Background())
	sub, err := im.Attach(ctx, 1, srv.URL+"/auto/v9.1")
	if err != nil {
		t.Fatal(err)
	}
	cancel() // the POST /sessions handler returned
	buf := make([]byte, 188)
	deadline := time.Now().Add(2 * time.Second)
	got := 0
	for time.Now().Before(deadline) && got < 188*20 {
		n, err := sub.R.Read(buf)
		if err != nil {
			t.Fatalf("stream died after request cancel: %v (read %d bytes)", err, got)
		}
		got += n
	}
	_ = sub.Close()
	if n := requests.Load(); n != 1 {
		t.Fatalf("device saw %d requests, want 1 (the stream died with the request and was redialed)", n)
	}
}

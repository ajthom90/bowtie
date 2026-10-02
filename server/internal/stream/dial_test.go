package stream

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func deviceServer(t *testing.T, status int, hdhrErr string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hdhrErr != "" {
			w.Header().Set("X-HDHomeRun-Error", hdhrErr)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte("body"))
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/auto/v5.1"
}

func TestHTTPDialClassifiesDeviceErrors(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		hdhrErr  string
		busy     bool
		noSignal bool
	}{
		{"all tuners in use", 503, "805 All Tuners In Use", true, false},
		{"503 without header", 503, "", true, false},
		{"tune failed", 503, "806 Tune Failed", false, true},
		{"no video data", 503, "807 No Video Data", false, true},
		{"unknown channel", 404, "", false, false},
		{"device error", 500, "", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, status, err := HTTPDial(context.Background(), deviceServer(t, tc.status, tc.hdhrErr))
			if body != nil {
				t.Fatal("non-2xx response returned a body to stream from")
			}
			if status != tc.status {
				t.Errorf("status = %d, want %d", status, tc.status)
			}
			if err == nil {
				t.Fatal("non-2xx response returned no error")
			}
			if got := errors.Is(err, ErrTunersBusy); got != tc.busy {
				t.Errorf("errors.Is(ErrTunersBusy) = %v, want %v (err %v)", got, tc.busy, err)
			}
			if got := errors.Is(err, ErrNoSignal); got != tc.noSignal {
				t.Errorf("errors.Is(ErrNoSignal) = %v, want %v (err %v)", got, tc.noSignal, err)
			}
			if tc.hdhrErr != "" && !strings.Contains(err.Error(), tc.hdhrErr[4:]) {
				t.Errorf("error %q does not mention the device's reason %q", err, tc.hdhrErr[4:])
			}
		})
	}
}

func TestHTTPDialOK(t *testing.T) {
	body, status, err := HTTPDial(context.Background(), deviceServer(t, 200, ""))
	if err != nil || status != 200 || body == nil {
		t.Fatalf("got body=%v status=%d err=%v", body != nil, status, err)
	}
	_ = body.Close()
}

// A dial func that reports a non-2xx status without an error (as tests and
// older DialFuncs do) must never be treated as a stream.
func TestAttachRejectsNon2xx(t *testing.T) {
	im := NewIngestManager(func(ctx context.Context, url string) (io.ReadCloser, int, error) {
		return io.NopCloser(strings.NewReader("<html>not found</html>")), 404, nil
	})
	defer im.Shutdown()
	if _, err := im.Attach(context.Background(), 1, "http://dev/auto/v5.1"); err == nil {
		t.Fatal("Attach accepted a 404 as a device stream")
	}
}

func TestAttachSurfacesNoSignal(t *testing.T) {
	im := NewIngestManager(HTTPDial)
	defer im.Shutdown()
	_, err := im.Attach(context.Background(), 1, deviceServer(t, 503, "807 No Video Data"))
	if !errors.Is(err, ErrNoSignal) {
		t.Fatalf("err = %v, want ErrNoSignal", err)
	}
	if errors.Is(err, ErrTunersBusy) {
		t.Fatalf("no-signal reported as tuners busy: %v", err)
	}
}

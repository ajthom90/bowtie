package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/ajthom90/bowtie/server/internal/api"
	"github.com/ajthom90/bowtie/server/internal/auth"
	"github.com/ajthom90/bowtie/server/internal/config"
	"github.com/ajthom90/bowtie/server/internal/hdhr"
	"github.com/ajthom90/bowtie/server/internal/store"
	"github.com/ajthom90/bowtie/server/internal/stream"
)

// signalAPI: one HDHomeRun with channel 9.1, a viewer watching it, and a
// status.json that reports poor reception on 9.1.
func signalAPI(t *testing.T, statuses func() []hdhr.TunerStatus) (http.Handler, string, string) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "sig.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.UpsertDevice(store.Device{DeviceID: "D1", IP: "192.168.50.32", TunerCount: 2, LastSeen: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := st.SyncLineup("D1", []store.Channel{{GuideNumber: "9.1", Name: "FOX 9"}}); err != nil {
		t.Fatal(err)
	}
	chans, _ := st.ListChannels(false)
	ss := newStubStreams()
	viewerID := "viewer-sig"
	ss.register(viewerID, t.TempDir())
	ss.mu.Lock()
	ss.sessions = []stream.SessionInfo{{ID: "s1", ChannelID: chans[0].ID, Viewers: []stream.ViewerInfo{{ID: viewerID}}}}
	ss.mu.Unlock()
	h := api.New(api.Deps{
		Cfg:               config.Config{ListenAddr: ":0", Encoder: "auto"},
		Store:             st,
		Auth:              &auth.Auth{Secret: []byte("0123456789abcdef0123456789abcdef"), Store: st},
		Streams:           ss,
		StreamTokenSecret: []byte(streamSecret),
		SignalFetch: func(ctx context.Context, baseURL string) ([]hdhr.TunerStatus, error) {
			return statuses(), nil
		},
	})
	tok := stream.SignStreamToken([]byte(streamSecret), viewerID, time.Now().UTC().Add(time.Hour))
	return h, viewerID, tok
}

func poor() []hdhr.TunerStatus {
	return []hdhr.TunerStatus{{Resource: "tuner0", VctNumber: "9.1", SignalStrengthPercent: 96, SignalQualityPercent: 46, SymbolQualityPercent: 0}}
}

func TestHeartbeatWithoutSignalStays204(t *testing.T) {
	// Older players (the web app checks for exactly 204) are unaffected.
	h, viewerID, tok := signalAPI(t, poor)
	rr := doJSON(t, h, "POST", "/api/v1/sessions/"+viewerID+"/heartbeat?token="+tok, nil, nil)
	if rr.Code != http.StatusNoContent || rr.Body.Len() != 0 {
		t.Fatalf("status=%d body=%q, want 204 with no body", rr.Code, rr.Body.String())
	}
}

func TestHeartbeatReportsSignal(t *testing.T) {
	h, viewerID, tok := signalAPI(t, poor)
	rr := doJSON(t, h, "POST", "/api/v1/sessions/"+viewerID+"/heartbeat?signal=1&token="+tok, nil, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q, want 200", rr.Code, rr.Body.String())
	}
	var body struct {
		Signal *struct {
			Strength      int  `json:"strength"`
			Quality       int  `json:"quality"`
			SymbolQuality int  `json:"symbolQuality"`
			Weak          bool `json:"weak"`
		} `json:"signal"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Signal == nil || body.Signal.Quality != 46 || body.Signal.SymbolQuality != 0 || body.Signal.Strength != 96 {
		t.Fatalf("signal = %+v", body.Signal)
	}
	if body.Signal.Weak {
		t.Fatal("one reading must not be weak yet")
	}
}

func TestHeartbeatSignalUnknownIsNull(t *testing.T) {
	h, viewerID, tok := signalAPI(t, func() []hdhr.TunerStatus { return []hdhr.TunerStatus{{Resource: "tuner0"}} })
	rr := doJSON(t, h, "POST", "/api/v1/sessions/"+viewerID+"/heartbeat?signal=1&token="+tok, nil, nil)
	if rr.Code != http.StatusOK || rr.Body.String() != "{\"signal\":null}\n" {
		t.Fatalf("status=%d body=%q, want 200 {\"signal\":null}", rr.Code, rr.Body.String())
	}
}

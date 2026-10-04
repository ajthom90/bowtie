package api_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/ajthom90/bowtie/server/internal/store"
)

// I-1: a manual window over a blocked program can't be used to record it,
// and a recording made by someone else carries the window's strictest rating.
func TestManualRecordingRespectsParental(t *testing.T) {
	e := newDVREnv(t)
	at := e.showAt.Add(24 * time.Hour)
	_ = e.st.ReplaceEPG("xmltv", []store.EPGChannel{{ID: "epg-9.1", Source: "xmltv"}}, []store.Program{
		{EPGChannelID: "epg-9.1", Start: at, Stop: at.Add(30 * time.Minute), Title: "Kids", Rating: "TV-G"},
		{EPGChannelID: "epg-9.1", Start: at.Add(30 * time.Minute), Stop: at.Add(2 * time.Hour), Title: "Late Movie", Rating: "TV-MA"},
	})
	kid := seedUser(t, e.st, "kid", "pw", "viewer")
	kidTok := decodeLogin(t, doJSON(t, e.h, "POST", "/api/v1/auth/login", map[string]string{"username": "kid", "password": "pw"}, nil))
	kidH := map[string]string{"Authorization": "Bearer " + kidTok.AccessToken}
	if rr := doJSON(t, e.h, "PATCH", fmt.Sprintf("/api/v1/admin/users/%d", kid.ID), map[string]any{"allowedChannelIds": []int64{e.ids["9.1"]}, "maxRating": "TV-PG"}, e.admin); rr.Code != http.StatusOK {
		t.Fatalf("patch %d", rr.Code)
	}
	window := map[string]any{"channelId": e.ids["9.1"], "start": at, "stop": at.Add(2 * time.Hour), "title": "sneaky"}
	if rr, _ := e.record(t, kidH, window); rr.Code != http.StatusForbidden {
		t.Fatalf("kid manual window over TV-MA: %d", rr.Code)
	}
	if rr, _ := e.record(t, kidH, map[string]any{"channelId": e.ids["5.1"], "start": at, "stop": at.Add(time.Hour)}); rr.Code != http.StatusForbidden {
		t.Fatalf("kid records a disallowed channel: %d", rr.Code)
	}
	rr, rec := e.record(t, e.alice, window)
	if rr.Code != http.StatusCreated {
		t.Fatalf("alice %d", rr.Code)
	}
	if r, _ := e.st.RecordingByID(rec.ID); r.Rating != "TV-MA" {
		t.Fatalf("window rating %q, want the strictest (TV-MA)", r.Rating)
	}
}

// I-2: a deleted account's still-valid token can't play anything.
func TestPlayFailsClosedForDeletedUser(t *testing.T) {
	e := newDVREnv(t)
	gone := seedUser(t, e.st, "gone", "pw", "viewer")
	tok := decodeLogin(t, doJSON(t, e.h, "POST", "/api/v1/auth/login", map[string]string{"username": "gone", "password": "pw"}, nil))
	id, _ := e.st.CreateRecording(store.Recording{UserID: e.aliceID, ChannelID: e.ids["9.1"], ChannelName: "9.1", Title: "R",
		Start: e.showAt.Add(-48 * time.Hour), Stop: e.showAt.Add(-47 * time.Hour), State: store.RecScheduled, CreatedAt: time.Now()})
	r, _ := e.st.RecordingByID(id)
	r.State, r.Dir = store.RecReady, e.dir
	_ = e.st.UpdateRecording(r)
	if err := e.st.DeleteUser(gone.ID); err != nil {
		t.Fatal(err)
	}
	rr := doJSON(t, e.h, "POST", fmt.Sprintf("/api/v1/recordings/%d/play", id), nil, map[string]string{"Authorization": "Bearer " + tok.AccessToken})
	if rr.Code == http.StatusOK {
		t.Fatal("deleted account got a playback token")
	}
	if rr := doJSON(t, e.h, "GET", "/api/v1/channels", nil, map[string]string{"Authorization": "Bearer " + tok.AccessToken}); rr.Code == http.StatusOK && rr.Body.String() != "[]\n" {
		t.Fatalf("deleted account sees channels: %s", rr.Body.String())
	}
}

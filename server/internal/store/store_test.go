package store_test

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/ajthom90/bowtie/server/internal/store"
)

func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := store.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestMigrateAndCRUDUsers(t *testing.T) {
	s := openTestStore(t)

	n, err := s.CountUsers()
	if err != nil {
		t.Fatalf("CountUsers: %v", err)
	}
	if n != 0 {
		t.Fatalf("CountUsers = %d, want 0", n)
	}

	createdAt := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	id, err := s.CreateUser(store.User{
		Username:     "alice",
		PasswordHash: "hash1",
		Role:         "admin",
		MaxQuality:   "",
		CreatedAt:    createdAt,
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if id == 0 {
		t.Fatal("CreateUser returned id 0")
	}

	u, err := s.UserByUsername("alice")
	if err != nil {
		t.Fatalf("UserByUsername: %v", err)
	}
	if u.ID != id || u.Username != "alice" || u.PasswordHash != "hash1" || u.Role != "admin" || u.MaxQuality != "" {
		t.Errorf("UserByUsername got %+v", u)
	}
	if !u.CreatedAt.Equal(createdAt) {
		t.Errorf("CreatedAt = %v, want %v", u.CreatedAt, createdAt)
	}

	byID, err := s.UserByID(id)
	if err != nil {
		t.Fatalf("UserByID: %v", err)
	}
	if byID.Username != "alice" {
		t.Errorf("UserByID username = %q", byID.Username)
	}

	// Duplicate username should error
	_, err = s.CreateUser(store.User{
		Username:     "alice",
		PasswordHash: "other",
		Role:         "viewer",
		CreatedAt:    createdAt,
	})
	if err == nil {
		t.Fatal("CreateUser duplicate username: want error")
	}

	// Update user fields
	err = s.UpdateUser(store.User{
		ID:         id,
		Username:   "alice2",
		Role:       "viewer",
		MaxQuality: "medium",
	})
	if err != nil {
		t.Fatalf("UpdateUser: %v", err)
	}
	u, err = s.UserByID(id)
	if err != nil {
		t.Fatalf("UserByID after update: %v", err)
	}
	if u.Username != "alice2" || u.Role != "viewer" || u.MaxQuality != "medium" {
		t.Errorf("after UpdateUser got %+v", u)
	}

	if err := s.UpdatePassword(id, "hash2"); err != nil {
		t.Fatalf("UpdatePassword: %v", err)
	}
	u, err = s.UserByID(id)
	if err != nil {
		t.Fatalf("UserByID after password: %v", err)
	}
	if u.PasswordHash != "hash2" {
		t.Errorf("PasswordHash = %q, want hash2", u.PasswordHash)
	}

	// Second user for list
	id2, err := s.CreateUser(store.User{
		Username:     "bob",
		PasswordHash: "hb",
		Role:         "viewer",
		CreatedAt:    createdAt,
	})
	if err != nil {
		t.Fatalf("CreateUser bob: %v", err)
	}

	list, err := s.ListUsers()
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("ListUsers len = %d, want 2", len(list))
	}

	n, err = s.CountUsers()
	if err != nil {
		t.Fatalf("CountUsers: %v", err)
	}
	if n != 2 {
		t.Fatalf("CountUsers = %d, want 2", n)
	}

	if err := s.DeleteUser(id2); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	n, err = s.CountUsers()
	if err != nil {
		t.Fatalf("CountUsers after delete: %v", err)
	}
	if n != 1 {
		t.Fatalf("CountUsers after delete = %d, want 1", n)
	}

	_, err = s.UserByID(id2)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("UserByID deleted: err = %v, want sql.ErrNoRows", err)
	}
	_, err = s.UserByUsername("missing")
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("UserByUsername missing: err = %v, want sql.ErrNoRows", err)
	}
}

func TestSyncLineupPreservesEnabled(t *testing.T) {
	s := openTestStore(t)
	deviceID := "AABBCCDD"

	if err := s.UpsertDevice(store.Device{
		DeviceID:   deviceID,
		IP:         "192.168.1.50",
		Model:      "HDHR5-4US",
		TunerCount: 4,
		Manual:     true,
		LastSeen:   time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("UpsertDevice: %v", err)
	}

	err := s.SyncLineup(deviceID, []store.Channel{
		{DeviceID: deviceID, GuideNumber: "5.1", Name: "WABC", Enabled: false},
		{DeviceID: deviceID, GuideNumber: "7.1", Name: "WABC-DT2", Enabled: false},
		{DeviceID: deviceID, GuideNumber: "9.1", Name: "WWOR", Enabled: false},
	})
	if err != nil {
		t.Fatalf("SyncLineup initial: %v", err)
	}

	chans, err := s.ListChannels(false)
	if err != nil {
		t.Fatalf("ListChannels: %v", err)
	}
	if len(chans) != 3 {
		t.Fatalf("ListChannels len = %d, want 3", len(chans))
	}

	// Enable 5.1 and map EPG id
	var ch51 store.Channel
	for _, c := range chans {
		if c.GuideNumber == "5.1" {
			ch51 = c
			break
		}
	}
	if ch51.ID == 0 {
		t.Fatal("channel 5.1 not found")
	}
	if err := s.UpdateChannel(ch51.ID, true, "epg-wabc"); err != nil {
		t.Fatalf("UpdateChannel: %v", err)
	}

	// Re-sync: same 5.1 and 7.1, remove 9.1, add 11.1
	err = s.SyncLineup(deviceID, []store.Channel{
		{DeviceID: deviceID, GuideNumber: "5.1", Name: "WABC HD", Enabled: false},
		{DeviceID: deviceID, GuideNumber: "7.1", Name: "WABC-DT2", Enabled: false},
		{DeviceID: deviceID, GuideNumber: "11.1", Name: "WPIX", Enabled: false},
	})
	if err != nil {
		t.Fatalf("SyncLineup second: %v", err)
	}

	chans, err = s.ListChannels(false)
	if err != nil {
		t.Fatalf("ListChannels after re-sync: %v", err)
	}
	if len(chans) != 3 {
		t.Fatalf("after re-sync len = %d, want 3", len(chans))
	}

	byGuide := map[string]store.Channel{}
	for _, c := range chans {
		byGuide[c.GuideNumber] = c
	}

	// 5.1: enabled + EPG mapping preserved; name may update
	c51 := byGuide["5.1"]
	if !c51.Enabled {
		t.Error("5.1 Enabled should be preserved true")
	}
	if c51.EPGChannelID != "epg-wabc" {
		t.Errorf("5.1 EPGChannelID = %q, want epg-wabc", c51.EPGChannelID)
	}
	if c51.Name != "WABC HD" {
		t.Errorf("5.1 Name = %q, want WABC HD", c51.Name)
	}

	// 11.1 new, disabled
	c11 := byGuide["11.1"]
	if c11.ID == 0 {
		t.Fatal("11.1 missing")
	}
	if c11.Enabled {
		t.Error("11.1 should be disabled (new)")
	}
	if c11.EPGChannelID != "" {
		t.Errorf("11.1 EPGChannelID = %q, want empty", c11.EPGChannelID)
	}

	// 9.1 removed
	if _, ok := byGuide["9.1"]; ok {
		t.Error("9.1 should be gone")
	}

	// enabledOnly filter
	enabled, err := s.ListChannels(true)
	if err != nil {
		t.Fatalf("ListChannels enabledOnly: %v", err)
	}
	if len(enabled) != 1 || enabled[0].GuideNumber != "5.1" {
		t.Errorf("enabledOnly = %+v, want only 5.1", enabled)
	}

	// ChannelByID
	got, err := s.ChannelByID(c51.ID)
	if err != nil {
		t.Fatalf("ChannelByID: %v", err)
	}
	if got.GuideNumber != "5.1" {
		t.Errorf("ChannelByID guide = %q", got.GuideNumber)
	}

	// Devices
	devs, err := s.ListDevices()
	if err != nil {
		t.Fatalf("ListDevices: %v", err)
	}
	if len(devs) != 1 || devs[0].DeviceID != deviceID {
		t.Errorf("ListDevices = %+v", devs)
	}
	if err := s.DeleteDevice(deviceID); err != nil {
		t.Fatalf("DeleteDevice: %v", err)
	}
	devs, err = s.ListDevices()
	if err != nil {
		t.Fatalf("ListDevices after delete: %v", err)
	}
	if len(devs) != 0 {
		t.Errorf("ListDevices after delete len = %d", len(devs))
	}
}

func TestReplaceEPGAndRange(t *testing.T) {
	s := openTestStore(t)

	// Two sources of EPG data
	xmltvChans := []store.EPGChannel{
		{ID: "xml-1", DisplayName: "Channel One", Callsign: "ONE", IconURL: "http://i/1.png", Source: "xmltv"},
		{ID: "xml-2", DisplayName: "Channel Two", Callsign: "TWO", IconURL: "", Source: "xmltv"},
	}
	// Fixed times for overlap tests
	// Window: 2026-08-04 14:00–16:00 UTC
	// progA: 13:00–15:00 (overlaps)
	// progB: 15:00–17:00 (overlaps)
	// progC: 12:00–13:00 (no overlap — ends exactly at window start? Stop > start required, so stop==14:00 no; use 12-13)
	// progD: 16:00–18:00 (Start < stop is false for stop==16:00 window end? Start < stop && Stop > start: 16:00 < 16:00 is false — no overlap)
	t0 := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	t1 := time.Date(2026, 8, 4, 13, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 8, 4, 14, 0, 0, 0, time.UTC)
	t3 := time.Date(2026, 8, 4, 15, 0, 0, 0, time.UTC)
	t4 := time.Date(2026, 8, 4, 16, 0, 0, 0, time.UTC)
	t5 := time.Date(2026, 8, 4, 17, 0, 0, 0, time.UTC)
	t6 := time.Date(2026, 8, 4, 18, 0, 0, 0, time.UTC)

	xmltvProgs := []store.Program{
		{EPGChannelID: "xml-1", Start: t1, Stop: t3, Title: "A", Subtitle: "subA", Description: "descA", Category: "News", IconURL: ""},
		{EPGChannelID: "xml-1", Start: t3, Stop: t5, Title: "B", Subtitle: "", Description: "", Category: "Sports", IconURL: ""},
		{EPGChannelID: "xml-1", Start: t0, Stop: t1, Title: "Before", Subtitle: "", Description: "", Category: "", IconURL: ""},
		{EPGChannelID: "xml-1", Start: t4, Stop: t6, Title: "After", Subtitle: "", Description: "", Category: "", IconURL: ""},
		{EPGChannelID: "xml-2", Start: t2, Stop: t4, Title: "OtherCh", Subtitle: "", Description: "", Category: "", IconURL: ""},
	}
	if err := s.ReplaceEPG("xmltv", xmltvChans, xmltvProgs); err != nil {
		t.Fatalf("ReplaceEPG xmltv: %v", err)
	}

	sdChans := []store.EPGChannel{
		{ID: "sd-99", DisplayName: "SD Station", Callsign: "SDS", IconURL: "", Source: "sd"},
	}
	sdProgs := []store.Program{
		{EPGChannelID: "sd-99", Start: t2, Stop: t4, Title: "SD Show", Subtitle: "", Description: "", Category: "", IconURL: ""},
	}
	if err := s.ReplaceEPG("sd", sdChans, sdProgs); err != nil {
		t.Fatalf("ReplaceEPG sd: %v", err)
	}

	allChans, err := s.ListEPGChannels()
	if err != nil {
		t.Fatalf("ListEPGChannels: %v", err)
	}
	if len(allChans) != 3 {
		t.Fatalf("ListEPGChannels len = %d, want 3", len(allChans))
	}

	// Replace only xmltv — should leave sd intact
	xmltvChans2 := []store.EPGChannel{
		{ID: "xml-1", DisplayName: "Channel One Updated", Callsign: "ONE", IconURL: "", Source: "xmltv"},
	}
	xmltvProgs2 := []store.Program{
		{EPGChannelID: "xml-1", Start: t2, Stop: t4, Title: "OnlyOne", Subtitle: "", Description: "", Category: "", IconURL: ""},
	}
	if err := s.ReplaceEPG("xmltv", xmltvChans2, xmltvProgs2); err != nil {
		t.Fatalf("ReplaceEPG xmltv again: %v", err)
	}

	allChans, err = s.ListEPGChannels()
	if err != nil {
		t.Fatalf("ListEPGChannels after replace: %v", err)
	}
	// xml-2 gone, xml-1 + sd-99 remain
	ids := map[string]bool{}
	for _, c := range allChans {
		ids[c.ID] = true
	}
	if !ids["xml-1"] || !ids["sd-99"] || ids["xml-2"] {
		t.Errorf("channels after xmltv replace: %v", ids)
	}

	// ProgramsInRange: window 14:00–16:00, channel xml-1 → OnlyOne (14–16)
	// sd-99 still has SD Show
	progs, err := s.ProgramsInRange([]string{"xml-1"}, t2, t4)
	if err != nil {
		t.Fatalf("ProgramsInRange: %v", err)
	}
	if len(progs) != 1 || progs[0].Title != "OnlyOne" {
		t.Errorf("ProgramsInRange xml-1 = %+v, want OnlyOne", progs)
	}

	// Overlap semantics with original data would use Stop > start && Start < stop
	// Restore fuller set to test boundary overlap
	if err := s.ReplaceEPG("xmltv", xmltvChans, xmltvProgs); err != nil {
		t.Fatalf("ReplaceEPG restore: %v", err)
	}
	// Window 14:00–16:00 on xml-1: A (13–15) and B (15–17) overlap; Before (12–13) and After (16–18) do not
	progs, err = s.ProgramsInRange([]string{"xml-1"}, t2, t4)
	if err != nil {
		t.Fatalf("ProgramsInRange window: %v", err)
	}
	titles := map[string]bool{}
	for _, p := range progs {
		titles[p.Title] = true
	}
	if !titles["A"] || !titles["B"] {
		t.Errorf("want A and B overlapping, got %v", titles)
	}
	if titles["Before"] || titles["After"] {
		t.Errorf("Before/After should not overlap window, got %v", titles)
	}

	// Multi-channel
	progs, err = s.ProgramsInRange([]string{"xml-1", "xml-2"}, t2, t4)
	if err != nil {
		t.Fatalf("ProgramsInRange multi: %v", err)
	}
	if len(progs) < 3 {
		t.Errorf("multi-channel programs len = %d, want >= 3", len(progs))
	}

	// Prune: remove programs ending before t3 (15:00)
	if err := s.PrunePrograms(t3); err != nil {
		t.Fatalf("PrunePrograms: %v", err)
	}
	progs, err = s.ProgramsInRange([]string{"xml-1", "xml-2", "sd-99"}, t0, t6)
	if err != nil {
		t.Fatalf("ProgramsInRange after prune: %v", err)
	}
	for _, p := range progs {
		if !p.Stop.After(t3) && !p.Stop.Equal(t3) {
			// stop is before olderThan — should be gone
			// prune: olderThan means Stop < olderThan (or <=)? Plan says PrunePrograms(olderThan time.Time)
			// Typically prune programs that ended before olderThan
			if p.Stop.Before(t3) {
				t.Errorf("program %q stop %v should have been pruned (olderThan %v)", p.Title, p.Stop, t3)
			}
		}
	}
}

func TestRefreshTokens(t *testing.T) {
	s := openTestStore(t)

	uid, err := s.CreateUser(store.User{
		Username:     "tokuser",
		PasswordHash: "h",
		Role:         "viewer",
		CreatedAt:    time.Date(2026, 8, 4, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	exp := time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC)
	if err := s.SaveRefreshToken(store.RefreshToken{
		UserID:    uid,
		TokenHash: "abc123hash",
		ExpiresAt: exp,
	}); err != nil {
		t.Fatalf("SaveRefreshToken: %v", err)
	}

	tok, err := s.RefreshTokenByHash("abc123hash")
	if err != nil {
		t.Fatalf("RefreshTokenByHash: %v", err)
	}
	if tok.UserID != uid || tok.TokenHash != "abc123hash" {
		t.Errorf("token = %+v", tok)
	}
	if !tok.ExpiresAt.Equal(exp) {
		t.Errorf("ExpiresAt = %v, want %v", tok.ExpiresAt, exp)
	}

	if err := s.DeleteRefreshToken("abc123hash"); err != nil {
		t.Fatalf("DeleteRefreshToken: %v", err)
	}
	_, err = s.RefreshTokenByHash("abc123hash")
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("after delete: err = %v, want sql.ErrNoRows", err)
	}

	// Expire cleanup
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	if err := s.SaveRefreshToken(store.RefreshToken{
		UserID:    uid,
		TokenHash: "expired",
		ExpiresAt: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("SaveRefreshToken expired: %v", err)
	}
	if err := s.SaveRefreshToken(store.RefreshToken{
		UserID:    uid,
		TokenHash: "valid",
		ExpiresAt: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("SaveRefreshToken valid: %v", err)
	}
	if err := s.DeleteExpiredRefreshTokens(now); err != nil {
		t.Fatalf("DeleteExpiredRefreshTokens: %v", err)
	}
	_, err = s.RefreshTokenByHash("expired")
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("expired token still present: %v", err)
	}
	tok, err = s.RefreshTokenByHash("valid")
	if err != nil {
		t.Fatalf("valid token missing: %v", err)
	}
	if tok.TokenHash != "valid" {
		t.Errorf("unexpected token %+v", tok)
	}
}

func TestSettings(t *testing.T) {
	s := openTestStore(t)

	v, err := s.GetSetting("missing")
	if err != nil {
		t.Fatalf("GetSetting missing: %v", err)
	}
	if v != "" {
		t.Errorf("GetSetting missing = %q, want empty", v)
	}

	if err := s.SetSetting("jwt_secret", "deadbeef"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	v, err = s.GetSetting("jwt_secret")
	if err != nil {
		t.Fatalf("GetSetting: %v", err)
	}
	if v != "deadbeef" {
		t.Errorf("GetSetting = %q, want deadbeef", v)
	}

	// Upsert overwrite
	if err := s.SetSetting("jwt_secret", "cafebabe"); err != nil {
		t.Fatalf("SetSetting overwrite: %v", err)
	}
	v, err = s.GetSetting("jwt_secret")
	if err != nil {
		t.Fatalf("GetSetting after overwrite: %v", err)
	}
	if v != "cafebabe" {
		t.Errorf("GetSetting = %q, want cafebabe", v)
	}
}

func TestHasSettingDistinguishesEmptyFromAbsent(t *testing.T) {
	s := openTestStore(t)

	has, err := s.HasSetting("unknown.key")
	if err != nil {
		t.Fatalf("HasSetting unknown: %v", err)
	}
	if has {
		t.Fatal("HasSetting(unknown) = true, want false")
	}

	if err := s.SetSetting("xmltv.source", ""); err != nil {
		t.Fatalf("SetSetting empty: %v", err)
	}
	has, err = s.HasSetting("xmltv.source")
	if err != nil {
		t.Fatalf("HasSetting empty value: %v", err)
	}
	if !has {
		t.Fatal("HasSetting after SetSetting(\"\") = false, want true (empty is a real value)")
	}

	// GetSetting still returns "" for the empty-but-present key (and for missing).
	v, err := s.GetSetting("xmltv.source")
	if err != nil {
		t.Fatalf("GetSetting: %v", err)
	}
	if v != "" {
		t.Errorf("GetSetting empty-present = %q, want \"\"", v)
	}
}

func TestSetSettingsAtomicUpsert(t *testing.T) {
	s := openTestStore(t)

	if err := s.SetSettings(map[string]string{
		"a": "1",
		"b": "2",
	}); err != nil {
		t.Fatalf("SetSettings: %v", err)
	}
	for k, want := range map[string]string{"a": "1", "b": "2"} {
		v, err := s.GetSetting(k)
		if err != nil {
			t.Fatalf("GetSetting %s: %v", k, err)
		}
		if v != want {
			t.Errorf("GetSetting(%s) = %q, want %q", k, v, want)
		}
	}

	// Overwrite a, add c
	if err := s.SetSettings(map[string]string{
		"a": "one",
		"c": "3",
	}); err != nil {
		t.Fatalf("SetSettings update: %v", err)
	}
	a, _ := s.GetSetting("a")
	b, _ := s.GetSetting("b")
	c, _ := s.GetSetting("c")
	if a != "one" || b != "2" || c != "3" {
		t.Errorf("after update a=%q b=%q c=%q", a, b, c)
	}

	if err := s.SetSettings(map[string]string{}); err != nil {
		t.Fatalf("SetSettings empty map: %v", err)
	}
}

func TestUserLimitsRoundTrip(t *testing.T) {
	s := openTestStore(t)
	id, err := s.CreateUser(store.User{
		Username: "friend", PasswordHash: "h", Role: "viewer",
		MaxStreams: 2, MaxTuners: 1, CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	u, err := s.UserByID(id)
	if err != nil || u.MaxStreams != 2 || u.MaxTuners != 1 {
		t.Fatalf("created: %+v err=%v", u, err)
	}
	u.MaxStreams, u.MaxTuners = 0, 3
	if err := s.UpdateUser(u); err != nil {
		t.Fatal(err)
	}
	users, err := s.ListUsers()
	if err != nil || len(users) != 1 || users[0].MaxStreams != 0 || users[0].MaxTuners != 3 {
		t.Fatalf("updated: %+v err=%v", users, err)
	}
}

func TestProgramRatingRoundTrip(t *testing.T) {
	s := openTestStore(t)
	st := time.Date(2026, 10, 4, 6, 0, 0, 0, time.UTC)
	if err := s.ReplaceEPG("xmltv", []store.EPGChannel{{ID: "e", Source: "xmltv"}},
		[]store.Program{{EPGChannelID: "e", Start: st, Stop: st.Add(time.Hour), Title: "Late Show", Rating: "TV-14"}}); err != nil {
		t.Fatal(err)
	}
	ps, err := s.ProgramsInRange([]string{"e"}, st, st.Add(time.Hour))
	if err != nil || len(ps) != 1 || ps[0].Rating != "TV-14" {
		t.Fatalf("%+v %v", ps, err)
	}
}

func TestUserParentalRoundTrip(t *testing.T) {
	s := openTestStore(t)
	id, err := s.CreateUser(store.User{Username: "kid", PasswordHash: "h", Role: "viewer",
		AllowedChannels: []int64{3, 7}, MaxRating: 4, BlockUnrated: true, CreatedAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	u, _ := s.UserByID(id)
	if len(u.AllowedChannels) != 2 || u.AllowedChannels[1] != 7 || u.MaxRating != 4 || !u.BlockUnrated {
		t.Fatalf("created %+v", u)
	}
	u.AllowedChannels, u.MaxRating, u.BlockUnrated = nil, 0, false
	if err := s.UpdateUser(u); err != nil {
		t.Fatal(err)
	}
	u, _ = s.UserByID(id)
	if u.AllowedChannels != nil || u.MaxRating != 0 || u.BlockUnrated {
		t.Fatalf("cleared %+v", u)
	}
}

func TestProgramSeriesFieldsRoundTrip(t *testing.T) {
	s := openTestStore(t)
	st := time.Date(2026, 10, 4, 6, 0, 0, 0, time.UTC)
	if err := s.ReplaceEPG("sd", []store.EPGChannel{{ID: "e", Source: "sd"}}, []store.Program{
		{EPGChannelID: "e", Start: st, Stop: st.Add(time.Hour), Title: "Show", ProgramID: "EP012345670012", SeriesID: "SH01234567", IsNew: true}}); err != nil {
		t.Fatal(err)
	}
	ps, err := s.ProgramsInRange([]string{"e"}, st, st.Add(time.Hour))
	if err != nil || len(ps) != 1 || ps[0].ProgramID != "EP012345670012" || ps[0].SeriesID != "SH01234567" || !ps[0].IsNew {
		t.Fatalf("%+v %v", ps, err)
	}
}

func TestSeriesIDOf(t *testing.T) {
	for in, want := range map[string]string{"EP012345670012": "SH01234567", "SH012345670000": "SH01234567", "MV000111220000": "", "": "", "EP12": ""} {
		if got := store.SeriesIDOf(in); got != want {
			t.Errorf("SeriesIDOf(%q)=%q want %q", in, got, want)
		}
	}
}

// I-3: automatic guide mapping only fills a channel that was never mapped:
// an admin's "no guide" stays, and Enabled is never touched.
func TestAutoMapOnlyFillsNeverMapped(t *testing.T) {
	s := openTestStore(t)
	_ = s.UpsertDevice(store.Device{DeviceID: "d", IP: "1.2.3.4", Model: "m", TunerCount: 1, StreamPort: 5004, LastSeen: time.Now()})
	_ = s.SyncLineup("d", []store.Channel{{DeviceID: "d", GuideNumber: "5.1", Name: "A"}, {DeviceID: "d", GuideNumber: "9.1", Name: "B"}})
	chans, _ := s.ListChannels(false)
	a, b := chans[0], chans[1]
	if err := s.UpdateChannel(b.ID, true, store.NoGuide); err != nil { // admin: no guide
		t.Fatal(err)
	}
	if ok, err := s.AutoMapChannel(a.ID, "hd:1"); err != nil || !ok {
		t.Fatalf("map never-mapped: %v %v", ok, err)
	}
	if ok, _ := s.AutoMapChannel(b.ID, "hd:2"); ok {
		t.Fatal("overwrote the admin's no-guide choice")
	}
	if ok, _ := s.AutoMapChannel(a.ID, "hd:3"); ok {
		t.Fatal("overwrote an existing mapping")
	}
	got, _ := s.ChannelByID(a.ID)
	if got.EPGChannelID != "hd:1" || got.Enabled {
		t.Fatalf("a=%+v (Enabled must be untouched)", got)
	}
}

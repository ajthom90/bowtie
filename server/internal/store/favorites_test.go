package store_test

import (
	"testing"
	"time"

	"github.com/ajthom90/bowtie/server/internal/store"
)

// favEnv: a store with one device, channels 5.1/9.1/11.1 (all enabled) and
// two users.
func favEnv(t *testing.T) (*store.Store, []int64, int64, int64) {
	t.Helper()
	s := openTestStore(t)
	now := time.Now().UTC()
	if err := s.UpsertDevice(store.Device{DeviceID: "d1", IP: "127.0.0.1", Model: "m", TunerCount: 2, Manual: true, LastSeen: now, StreamPort: 5004}); err != nil {
		t.Fatal(err)
	}
	if err := s.SyncLineup("d1", []store.Channel{
		{DeviceID: "d1", GuideNumber: "5.1", Name: "A"},
		{DeviceID: "d1", GuideNumber: "9.1", Name: "B"},
		{DeviceID: "d1", GuideNumber: "11.1", Name: "C"},
	}); err != nil {
		t.Fatal(err)
	}
	chans, err := s.ListChannels(false)
	if err != nil || len(chans) != 3 {
		t.Fatalf("channels %v %d", err, len(chans))
	}
	var ids []int64
	for _, c := range chans {
		if err := s.UpdateChannel(c.ID, true, ""); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, c.ID)
	}
	u1, _ := s.CreateUser(store.User{Username: "a", PasswordHash: "h", Role: "viewer", CreatedAt: now})
	u2, _ := s.CreateUser(store.User{Username: "b", PasswordHash: "h", Role: "viewer", CreatedAt: now})
	return s, ids, u1, u2
}

func TestFavoritesPerUserAndIdempotent(t *testing.T) {
	s, ch, a, b := favEnv(t)
	for i := 0; i < 2; i++ {
		if err := s.SetFavorite(a, ch[1], true); err != nil {
			t.Fatal(err)
		}
	}
	fa, err := s.FavoriteIDs(a)
	if err != nil || len(fa) != 1 || !fa[ch[1]] {
		t.Fatalf("a favorites %v err=%v", fa, err)
	}
	if fb, _ := s.FavoriteIDs(b); len(fb) != 0 {
		t.Fatalf("b sees a's favorites: %v", fb)
	}
	for i := 0; i < 2; i++ {
		if err := s.SetFavorite(a, ch[1], false); err != nil {
			t.Fatal(err)
		}
	}
	if fa, _ := s.FavoriteIDs(a); len(fa) != 0 {
		t.Fatalf("unstar: %v", fa)
	}
}

func TestRecentsOrderTrimAndEnabledOnly(t *testing.T) {
	s, ch, a, _ := favEnv(t)
	t0 := time.Date(2026, 10, 3, 20, 0, 0, 0, time.UTC)
	_ = s.RecordWatch(a, ch[0], t0)
	_ = s.RecordWatch(a, ch[1], t0.Add(time.Minute))
	_ = s.RecordWatch(a, ch[0], t0.Add(2*time.Minute)) // re-watch moves to front
	got, err := s.Recents(a, 8)
	if err != nil || len(got) != 2 || got[0].ChannelID != ch[0] || got[1].ChannelID != ch[1] {
		t.Fatalf("recents %+v err=%v", got, err)
	}
	if got[0].GuideNumber == "" || got[0].Name == "" || !got[0].WatchedAt.Equal(t0.Add(2*time.Minute)) {
		t.Fatalf("row %+v", got[0])
	}
	if one, _ := s.Recents(a, 1); len(one) != 1 {
		t.Fatalf("limit: %d", len(one))
	}
	// Disabled channels are hidden, not forgotten.
	if err := s.UpdateChannel(ch[0], false, ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Recents(a, 8); len(got) != 1 || got[0].ChannelID != ch[1] {
		t.Fatalf("disabled shown: %+v", got)
	}
	if err := s.ClearRecents(a); err != nil {
		t.Fatal(err)
	}
	_ = s.UpdateChannel(ch[0], true, "")
	if got, _ := s.Recents(a, 8); len(got) != 0 {
		t.Fatalf("clear: %+v", got)
	}
}

func TestRecentsKeepNewest20(t *testing.T) {
	s, ch, a, _ := favEnv(t)
	// Only 3 channels exist, so trim is checked on the stored rows via many
	// distinct watch times of 3 channels plus a direct count.
	t0 := time.Date(2026, 10, 3, 20, 0, 0, 0, time.UTC)
	for i := 0; i < 30; i++ {
		_ = s.RecordWatch(a, ch[i%3], t0.Add(time.Duration(i)*time.Minute))
	}
	got, err := s.Recents(a, 20)
	if err != nil || len(got) != 3 || got[0].ChannelID != ch[29%3] {
		t.Fatalf("recents %+v err=%v", got, err)
	}
}

func TestDeleteUserAndLineupRemovalClearRows(t *testing.T) {
	s, _, a, _ := favEnv(t)
	byGuide := map[string]int64{}
	chans, _ := s.ListChannels(false)
	for _, c := range chans {
		byGuide[c.GuideNumber] = c.ID
	}
	_ = s.SetFavorite(a, byGuide["11.1"], true)
	_ = s.RecordWatch(a, byGuide["11.1"], time.Now().UTC())
	// 11.1 leaves the lineup.
	if err := s.SyncLineup("d1", []store.Channel{
		{DeviceID: "d1", GuideNumber: "5.1", Name: "A"},
		{DeviceID: "d1", GuideNumber: "9.1", Name: "B"},
	}); err != nil {
		t.Fatal(err)
	}
	if fa, _ := s.FavoriteIDs(a); len(fa) != 0 {
		t.Fatalf("favorite rows for removed channel: %v", fa)
	}
	_ = s.SetFavorite(a, byGuide["9.1"], true)
	_ = s.RecordWatch(a, byGuide["9.1"], time.Now().UTC())
	if err := s.DeleteUser(a); err != nil {
		t.Fatal(err)
	}
	if fa, _ := s.FavoriteIDs(a); len(fa) != 0 {
		t.Fatalf("favorite rows after DeleteUser: %v", fa)
	}
	if got, _ := s.Recents(a, 20); len(got) != 0 {
		t.Fatalf("recents after DeleteUser: %+v", got)
	}
}

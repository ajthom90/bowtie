package store_test

import (
	"reflect"
	"testing"

	"github.com/ajthom90/bowtie/server/internal/store"
)

func TestJoinAndSplitCategories(t *testing.T) {
	got := store.JoinCategories([]string{"Sports event", " Football ", "sports EVENT", "", "Basketball", "Soccer", "Golf"})
	if want := "Sports event; Football; Basketball; Soccer"; got != want {
		t.Fatalf("Join = %q, want %q (trimmed, deduped, at most 4)", got, want)
	}
	if got := store.JoinCategories(nil); got != "" {
		t.Fatalf("Join(nil) = %q", got)
	}
	if got := store.SplitCategories("Sports event; Football"); !reflect.DeepEqual(got, []string{"Sports event", "Football"}) {
		t.Fatalf("Split = %q", got)
	}
	if got := store.SplitCategories("  "); got != nil {
		t.Fatalf("Split(blank) = %q", got)
	}
}

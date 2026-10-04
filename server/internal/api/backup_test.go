package api_test

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ajthom90/bowtie/server/internal/store"
)

func TestAdminBackupDownloadsDatabase(t *testing.T) {
	h, st, _ := testAPI(t)
	seedUser(t, st, "admin", "adminpass", "admin")
	seedUser(t, st, "viewer", "viewerpass", "viewer")
	bearer := func(user, pw string) map[string]string {
		tok := decodeLogin(t, doJSON(t, h, "POST", "/api/v1/auth/login", map[string]string{"username": user, "password": pw}, nil))
		return map[string]string{"Authorization": "Bearer " + tok.AccessToken}
	}

	if rr := doJSON(t, h, "GET", "/api/v1/admin/backup", nil, bearer("viewer", "viewerpass")); rr.Code != http.StatusForbidden {
		t.Fatalf("viewer backup = %d, want 403", rr.Code)
	}

	rr := doJSON(t, h, "GET", "/api/v1/admin/backup", nil, bearer("admin", "adminpass"))
	if rr.Code != http.StatusOK {
		t.Fatalf("backup = %d %s", rr.Code, rr.Body.String())
	}
	if cd := rr.Header().Get("Content-Disposition"); !strings.Contains(cd, "attachment") || !strings.Contains(cd, ".db") {
		t.Fatalf("Content-Disposition = %q", cd)
	}
	if cc := rr.Header().Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Fatalf("Cache-Control = %q", cc)
	}
	path := filepath.Join(t.TempDir(), "b.db")
	if err := os.WriteFile(path, rr.Body.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := store.Open(path)
	if err != nil {
		t.Fatalf("open backup: %v", err)
	}
	defer func() { _ = c.Close() }()
	if n, err := c.CountUsers(); err != nil || n != 2 {
		t.Fatalf("backup users = %d, %v", n, err)
	}
}

package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/session"
)

func TestServerFixtureRootsKeepDefaultStoragePrivateAndIsolated(t *testing.T) {
	var firstRoot string
	for _, name := range []string{"first", "second"} {
		t.Run(name, func(t *testing.T) {
			root := newServerFixtureRoot(t)
			if root == firstRoot {
				t.Fatal("fixtures shared a root")
			}
			if firstRoot == "" {
				firstRoot = root
			}
			for path, mode := range map[string]os.FileMode{
				root:                               0700,
				filepath.Join(root, "sessions.db"): 0600,
			} {
				info, err := os.Stat(path)
				if err != nil || info.Mode().Perm() != mode {
					t.Fatalf("fixture permissions for %q: info=%v err=%v, want %o", path, info, err, mode)
				}
			}

			// No injected store/factory: exercise the same default constructor
			// the full builder uses, rooted by the fixture's explicit data dir.
			t.Setenv("ORI_DATA_DIR", root)
			store, err := session.NewHybridStore(t.Context(), session.DefaultHybridStoreConfig())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := store.Close(); err != nil {
					t.Errorf("close default fixture store: %v", err)
				}
			})
			if got := store.DB().Path(); got != filepath.Join(root, "sessions.db") {
				t.Fatalf("default store path = %q, want fixture-owned sessions.db", got)
			}
			var sessions int
			if err := store.DB().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM sessions").Scan(&sessions); err != nil {
				t.Fatal(err)
			}
			if sessions != 0 {
				t.Fatalf("fixture inherited %d sessions", sessions)
			}
			// Reusing this identity in both roots proves even later clones do
			// not contain writes made to an earlier, already closed fixture.
			if err := store.CreateSession(t.Context(), &session.Session{
				ID: "fixture-only", AgentName: "Atlas", Title: name,
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

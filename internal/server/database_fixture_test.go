package server

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/testutil/testdb"
)

// newServerFixtureRoot gives ordinary full-builder tests a private, already
// migrated sessions.db. The builder still runs its real default storage
// constructor, including guards, pragmas, backfills and background workers.
// Only immutable schema/seed bytes are reused via testdb; neither connections
// nor application state are shared. Startup/reset/migration/reopen fixtures
// must keep their existing empty roots instead.
func newServerFixtureRoot(t *testing.T) string {
	t.Helper()
	root, err := os.MkdirTemp(t.TempDir(), "server-")
	if err != nil {
		t.Fatal(err)
	}
	db := testdb.Open(t)
	closeDB := sync.OnceValue(db.Close)
	t.Cleanup(func() {
		if err := closeDB(); err != nil {
			t.Errorf("close server fixture source: %v", err)
		}
	})
	// Copy only a sealed main file: SQLite's Close logs checkpoint errors, so
	// verify the WAL checkpoint explicitly before closing the sole connection.
	var busy, pages, checkpointed int
	if err := db.QueryRowContext(t.Context(), "PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &pages, &checkpointed); err != nil {
		t.Fatal(err)
	}
	if busy != 0 || pages != checkpointed {
		t.Fatalf("incomplete fixture checkpoint: busy=%d pages=%d checkpointed=%d", busy, pages, checkpointed)
	}
	if err := closeDB(); err != nil {
		t.Fatal(err)
	}
	// #nosec G304 G703 -- testdb allocated this private path; its sole connection is closed.
	image, err := os.ReadFile(db.Path())
	if err != nil {
		t.Fatal(err)
	}
	if len(image) == 0 {
		t.Fatal("empty server fixture database")
	}
	if err := os.WriteFile(filepath.Join(root, "sessions.db"), image, 0600); err != nil {
		t.Fatal(err)
	}
	return root
}

// newFixtureDatabase is only for ordinary fixtures whose services do not own
// database shutdown. Reset/startup/reopen fixtures retain real database.Open.
func newFixtureDatabase(t *testing.T) *database.DB {
	t.Helper()
	db := testdb.Open(t)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close fixture database: %v", err)
		}
	})
	return db
}

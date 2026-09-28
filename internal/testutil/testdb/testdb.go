// Package testdb provides isolated, pre-migrated databases for ordinary tests.
// Migration, startup, reset and close/reopen tests must use database.Open itself.
package testdb

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/database"
)

// Only immutable bytes survive initialization, never a shared connection or a
// directory belonging to the first caller. OnceValues also shares failures.
var pristineImage = sync.OnceValues(func() ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	return buildImage(ctx)
})

// Open returns an independently owned temporary file database. The caller MUST
// close it, either directly or through its owning store, before test cleanup.
// Register that owner's cleanup immediately; Open deliberately does not install
// a competing DB close that could run before a store drains background writes.
// It does register removal of the test-owned directory through t.TempDir.
// See README.md for storage/timestamp differences and excluded test categories.
func Open(t testing.TB) *database.DB {
	t.Helper()
	image, err := pristineImage()
	if err != nil {
		t.Fatalf("initialize test database image: %v", err)
	}
	db, err := openImage(t.Context(), t.TempDir(), image)
	if err != nil {
		t.Fatalf("open isolated test database: %v", err)
	}
	return db
}

func buildImage(ctx context.Context) (image []byte, err error) {
	dir, err := os.MkdirTemp("", "ori-testdb-template-")
	if err != nil {
		return nil, err
	}
	defer func() {
		err = errors.Join(err, os.RemoveAll(dir))
		if err != nil {
			image = nil // Never publish an image after partial initialization/cleanup.
		}
	}()
	path := filepath.Join(dir, "template.db")
	// Pre-create privately instead of inheriting SQLite's default file mode.
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		return nil, err
	}
	db, err := database.Open(ctx, &database.Config{Path: path, WALMode: true})
	if err != nil {
		return nil, err
	}

	// Close only logs checkpoint failures, so verify completion explicitly.
	// No other connection/writer exists. Read the main file only AFTER close.
	var busy, pages, checkpointed int
	checkpointErr := db.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &pages, &checkpointed)
	if checkpointErr == nil && (busy != 0 || pages != checkpointed) {
		checkpointErr = fmt.Errorf("incomplete template checkpoint: busy=%d pages=%d checkpointed=%d", busy, pages, checkpointed)
	}
	if err := errors.Join(checkpointErr, db.Close()); err != nil {
		return nil, err
	}
	// #nosec G304 G703 -- path is a fixed filename in our newly allocated private directory; the only writer is closed.
	return os.ReadFile(path)
}

// dir must be newly allocated by this package's caller (Open uses t.TempDir).
// No exported API accepts a destination or a production path.
func openImage(ctx context.Context, dir string, image []byte) (db *database.DB, err error) {
	defer func() {
		if err != nil {
			err = errors.Join(err, os.RemoveAll(dir))
		}
	}()
	// testing.TempDir's numbered child may be 0755. MkdirTemp guarantees a
	// private 0700 directory for the database and all SQLite sidecars.
	privateDir, err := os.MkdirTemp(dir, "db-")
	if err != nil {
		return nil, err
	}
	path := filepath.Join(privateDir, "test.db")
	if len(image) == 0 {
		return nil, errors.New("empty test database image")
	}
	if err := os.WriteFile(path, image, 0o600); err != nil {
		return nil, err
	}
	return database.Open(ctx, &database.Config{Path: path, WALMode: true})
}

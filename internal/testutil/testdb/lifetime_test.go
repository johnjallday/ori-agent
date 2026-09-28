package testdb

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/johnjallday/ori-agent/internal/database"
)

func TestIsolationMutationCloseAndLaterFixture(t *testing.T) {
	first := Open(t)
	closeFirst := sync.OnceFunc(func() { closeDB(t, first) })
	t.Cleanup(closeFirst)
	second := Open(t)
	t.Cleanup(func() { closeDB(t, second) })
	execSQL(t, first, "UPDATE users SET display_name = 'changed' WHERE id = 'local'")
	execSQL(t, first, "CREATE TABLE only_first (id INTEGER)")
	wantInt(t, second, "SELECT COUNT(*) FROM users WHERE display_name = ''", 1)
	wantInt(t, second, "SELECT COUNT(*) FROM sqlite_master WHERE name = 'only_first'", 0)
	closeFirst()
	execSQL(t, second, "UPDATE users SET display_name = 'second' WHERE id = 'local'")
	later := Open(t)
	t.Cleanup(func() { closeDB(t, later) })
	wantInt(t, later, "SELECT COUNT(*) FROM users WHERE display_name = ''", 1)
	wantInt(t, later, "SELECT COUNT(*) FROM sqlite_master WHERE name = 'only_first'", 0)
	if first.Path() == second.Path() || second.Path() == later.Path() {
		t.Fatal("fixtures share a database path")
	}
}

func TestConcurrentOpen(t *testing.T) {
	var paths sync.Map
	for i := range 8 {
		t.Run(fmt.Sprintf("fixture%d", i), func(t *testing.T) {
			t.Parallel() // No environment or CWD changes in this test family.
			db := Open(t)
			t.Cleanup(func() { closeDB(t, db) })
			if _, loaded := paths.LoadOrStore(db.Path(), true); loaded {
				t.Error("shared fixture path")
			}
			execSQL(t, db, "INSERT INTO users (id, created_at, updated_at) VALUES (?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)", t.Name())
			wantInt(t, db, "SELECT COUNT(*) FROM users WHERE id != 'local'", 1)
			wantInt(t, db, "SELECT COUNT(*) FROM users WHERE id = ?", 1, t.Name())
		})
	}
}

func TestTemplateOutlivesFirstCallerAndIgnoresDataEnvironment(t *testing.T) {
	// A private cache exercises genuine first initialization regardless of the
	// ordering/shuffling of other tests, without resetting the package global.
	load := sync.OnceValues(func() ([]byte, error) { return buildImage(context.Background()) })
	var firstHash [32]byte
	var sourceParent string
	t.Run("first", func(t *testing.T) {
		sourceParent = t.TempDir()
		t.Setenv("TMPDIR", sourceParent)
		for _, key := range []string{"HOME", "ORI_DATA_DIR"} {
			dir := t.TempDir()
			t.Setenv(key, dir)
			defer assertEmpty(t, dir)
		}
		cwd := t.TempDir()
		t.Chdir(cwd)
		defer assertEmpty(t, cwd)
		image, err := load()
		if err != nil {
			t.Fatal(err)
		}
		firstHash = sha256.Sum256(image)
		assertEmpty(t, sourceParent) // No source directory/connection outlives initialization.
	})
	if _, err := os.Stat(sourceParent); !os.IsNotExist(err) {
		t.Fatalf("first caller's temporary directory survived: %v", err)
	}
	image, err := load()
	if err != nil || sha256.Sum256(image) != firstHash {
		t.Fatalf("immutable template did not survive first caller cleanup: %v", err)
	}
	db, err := openImage(t.Context(), t.TempDir(), image)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeDB(t, db) })
	wantInt(t, db, "SELECT COUNT(*) FROM users WHERE id = 'local'", 1)
}

func TestOwnerCleanupPrecedesDirectoryRemoval(t *testing.T) {
	var db *database.DB
	closes := 0
	t.Run("owner", func(t *testing.T) {
		db = Open(t)
		// Models a store's final flush followed by its single DB close. Open
		// must not close earlier or install another close afterwards.
		t.Cleanup(func() {
			execSQL(t, db, "UPDATE users SET display_name = 'final flush' WHERE id = 'local'")
			closes++
			closeDB(t, db)
		})
		info, err := os.Stat(db.Path())
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("private database permissions: %v, %v", info, err)
		}
		info, err = os.Stat(filepath.Dir(db.Path()))
		if err != nil || info.Mode().Perm()&0o077 != 0 {
			t.Fatalf("private directory permissions: %v, %v", info, err)
		}
	})
	if closes != 1 || db.Stats().OpenConnections != 0 {
		t.Fatalf("owner close count=%d, connections=%d", closes, db.Stats().OpenConnections)
	}
	if _, err := os.Stat(db.Path()); !os.IsNotExist(err) {
		t.Fatalf("fixture not removed after owner cleanup: %v", err)
	}
}

func TestInitializerFailureIsSharedAndLeavesNoImage(t *testing.T) {
	parent := t.TempDir()
	t.Setenv("TMPDIR", parent)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	load := sync.OnceValues(func() ([]byte, error) {
		calls++
		return buildImage(ctx)
	})
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Go(func() {
			image, err := load()
			if len(image) != 0 || !errors.Is(err, context.Canceled) {
				t.Errorf("partial image published or cancellation lost: bytes=%d err=%v", len(image), err)
			}
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	var first error
	for err := range errs {
		if first == nil {
			first = err
		}
		if err != first {
			t.Error("callers received different initialization failures")
		}
	}
	if calls != 1 {
		t.Fatalf("initialized %d times", calls)
	}
	assertEmpty(t, parent)
}

func TestInitializerTemporaryDirectoryFailure(t *testing.T) {
	parent := t.TempDir()
	t.Setenv("TMPDIR", filepath.Join(parent, "absent"))
	image, err := buildImage(t.Context())
	if err == nil || image != nil {
		t.Fatalf("directory failure returned image=%d err=%v", len(image), err)
	}
	assertEmpty(t, parent)
}

func TestCloneFailuresCleanUpWithoutDamagingTemplate(t *testing.T) {
	image, err := pristineImage()
	if err != nil {
		t.Fatal(err)
	}
	before := sha256.Sum256(image)
	for _, kind := range []string{"file", "connection", "corrupt", "empty"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			ctx := t.Context()
			candidate := image
			switch kind {
			case "file":
				// An owned file where the fixed destination should be a directory.
				dir = filepath.Join(dir, "not-a-directory")
				if err := os.WriteFile(dir, nil, 0o600); err != nil {
					t.Fatal(err)
				}
			case "connection":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "corrupt":
				candidate = []byte("not a SQLite database")
			case "empty":
				candidate = nil
			}
			db, err := openImage(ctx, dir, candidate)
			if err == nil || db != nil {
				t.Fatalf("failed clone returned db=%v err=%v", db, err)
			}
			if _, err := os.Stat(dir); !os.IsNotExist(err) {
				t.Fatalf("partial clone survived: %v", err)
			}
		})
	}
	if sha256.Sum256(image) != before {
		t.Fatal("clone failure changed the template")
	}
	db := Open(t)
	t.Cleanup(func() { closeDB(t, db) })
	wantInt(t, db, "SELECT COUNT(*) FROM users", 1)
}

func assertEmpty(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("expected empty owned directory %s: entries=%v err=%v", dir, entries, err)
	}
}

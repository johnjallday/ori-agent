package learningmissions

import (
	"errors"
	"os"
	"testing"
)

// The two fixture families promise different storage: ordinary tests get a
// private clone of a migrated template (write-ahead log), lifecycle tests a
// freshly initialized database (rollback journal). Both keep the fixtures'
// busy timeout, foreign keys and the seeded practice workspace.
func TestFixtureDatabasesKeepTheirStorageContract(t *testing.T) {
	cases := []struct {
		name    string
		open    storeOpener
		journal string
		fresh   bool
	}{
		{"ordinary clone", testStore, "wal", false},
		{"fresh lifecycle", freshTestStore, "delete", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, db, path := tc.open(t)
			var journal string
			if err := db.QueryRowContext(t.Context(), `PRAGMA journal_mode`).Scan(&journal); err != nil || journal != tc.journal {
				t.Fatalf("journal_mode = %q, %v; want %q", journal, err, tc.journal)
			}
			var busy, foreignKeys int
			if err := db.QueryRowContext(t.Context(), `PRAGMA busy_timeout`).Scan(&busy); err != nil || busy != fixtureBusyTimeoutMillis {
				t.Fatalf("busy_timeout = %d, %v; the fixtures have always waited %d ms", busy, err, fixtureBusyTimeoutMillis)
			}
			if err := db.QueryRowContext(t.Context(), `PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil || foreignKeys != 1 {
				t.Fatalf("foreign_keys = %d, %v", foreignKeys, err)
			}
			var name string
			if err := db.QueryRowContext(t.Context(), `SELECT name FROM workspaces WHERE id='workspace'`).Scan(&name); err != nil || name != "Practice" {
				t.Fatalf("practice workspace not seeded: %q %v", name, err)
			}
			if path == "" {
				t.Fatal("fixture reported no database path")
			}
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("reported path is not the database file: %v", err)
			}
			if problem := freshnessProblem(t.Context(), db); (problem == "") != tc.fresh {
				t.Fatalf("freshness guard = %q, want fresh=%v", problem, tc.fresh)
			}
		})
	}
}

// Ordinary tests may share nothing: a write, or a close, in one fixture must
// be invisible to the next.
func TestOrdinaryFixturesAreIndependentDatabases(t *testing.T) {
	a, dbA, _ := testStore(t)
	b, _, _ := testStore(t)
	seedInstance(t, a, "local", "mission")
	if _, err := b.Instance(t.Context(), "local", "mission"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("a mutation in one fixture reached another: %v", err)
	}
	if owner, err := b.Owner(t.Context(), "local"); err != nil || owner.Revision != 0 {
		t.Fatalf("a second fixture saw the first one's enrollment: %+v %v", owner, err)
	}
	if err := dbA.Close(); err != nil {
		t.Fatal(err)
	}
	// Closing a fixture early is safe for its own cleanup (a second Close is a
	// no-op) and leaves the other database fully usable.
	seedInstance(t, b, "local", "mission")
	if _, err := b.Instance(t.Context(), "local", "mission"); err != nil {
		t.Fatalf("second fixture unusable after the first closed: %v", err)
	}
}

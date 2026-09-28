package testdb

import (
	"database/sql"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/database"
)

func closeDB(t testing.TB, db *database.DB) {
	t.Helper()
	if err := db.Close(); err != nil {
		t.Error(err)
	}
}

func execSQL(t testing.TB, db *database.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("execute %s: %v", query, err)
	}
}

func wantInt(t testing.TB, db *database.DB, query string, want int, args ...any) {
	t.Helper()
	var got int
	if err := db.QueryRow(query, args...).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("%s: got %d, want %d", query, got, want)
	}
}

// Compare every schema object and every seeded row, including FTS shadow tables.
// Only initialization timestamps differ intentionally; no schema/seed snapshot
// is checked in and new migrations automatically join this comparison.
func TestEquivalentSchemaSeedsAndPragmas(t *testing.T) {
	fresh, err := database.Open(t.Context(), &database.Config{InMemory: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeDB(t, fresh) })
	clone := Open(t)
	t.Cleanup(func() { closeDB(t, clone) })

	schema := "SELECT type, name, tbl_name, sql FROM sqlite_master"
	if got, want := snapshotRows(t, clone, schema), snapshotRows(t, fresh, schema); !reflect.DeepEqual(got, want) {
		t.Fatalf("schema mismatch:\nclone %v\nfresh %v", got, want)
	}
	rows, err := fresh.Query("SELECT name FROM sqlite_master WHERE type = 'table'")
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, table)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	for _, table := range tables {
		// Identifiers come only from our freshly generated SQLite schema and
		// are quoted, never supplied by a caller or a production installation.
		query := `SELECT * FROM "` + strings.ReplaceAll(table, `"`, `""`) + `"`
		if got, want := snapshotRows(t, clone, query), snapshotRows(t, fresh, query); !reflect.DeepEqual(got, want) {
			t.Errorf("seed mismatch in %s:\nclone %v\nfresh %v", table, got, want)
		}
	}
	for pragma, want := range map[string]int{
		"foreign_keys": 1, "busy_timeout": 0, "synchronous": 1,
		"cache_size": -8000, "temp_store": 2,
	} {
		wantInt(t, fresh, "PRAGMA "+pragma, want)
		wantInt(t, clone, "PRAGMA "+pragma, want)
	}
	for _, db := range []*database.DB{fresh, clone} {
		if stats := db.Stats(); stats.MaxOpenConnections != 1 || stats.OpenConnections != 1 || stats.Idle != 1 {
			t.Fatalf("unexpected connection pool: %+v", stats)
		}
		wantInt(t, db, "SELECT COUNT(*) FROM users WHERE id = 'local'", 1)
		wantInt(t, db, "SELECT COUNT(*) FROM user_preference_revisions WHERE user_id = 'local' AND revision = 1", 3)
		var integrity string
		if err := db.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
			t.Fatalf("integrity=%q: %v", integrity, err)
		}
	}
	// These are deliberate file-vs-memory differences, not copied pragmas.
	wantInt(t, clone, "PRAGMA mmap_size", 67108864)
	for db, want := range map[*database.DB]string{fresh: "memory", clone: "wal"} {
		var mode string
		if err := db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil || mode != want {
			t.Fatalf("journal mode=%q, want %q: %v", mode, want, err)
		}
	}
	if fresh.Path() != ":memory:" || clone.Path() == "" || clone.Path() == fresh.Path() {
		t.Fatalf("path identities: fresh=%q clone=%q", fresh.Path(), clone.Path())
	}
}

func snapshotRows(t *testing.T, db *database.DB, query string) []string {
	t.Helper()
	rows, err := db.Query(query)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			t.Error(err)
		}
	}()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var result []string
	for rows.Next() {
		values := make([]any, len(columns))
		dests := make([]any, len(columns))
		for i := range values {
			dests[i] = &values[i]
		}
		if err := rows.Scan(dests...); err != nil {
			t.Fatal(err)
		}
		for i, col := range columns {
			if col == "applied_at" || col == "created_at" || col == "updated_at" {
				if values[i] == nil {
					t.Fatalf("missing initialization timestamp: %s", col)
				}
				values[i] = "<initialization timestamp>"
			}
		}
		result = append(result, fmt.Sprintf("%#v", values))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	sort.Strings(result)
	return result
}

func TestEquivalentConstraintsAndFTSLifecycle(t *testing.T) {
	for _, kind := range []string{"fresh", "clone"} {
		t.Run(kind, func(t *testing.T) {
			var db *database.DB
			if kind == "fresh" {
				var err error
				db, err = database.Open(t.Context(), &database.Config{InMemory: true})
				if err != nil {
					t.Fatal(err)
				}
			} else {
				db = Open(t)
			}
			t.Cleanup(func() { closeDB(t, db) })
			execSQL(t, db, `INSERT INTO workspaces (id, name, created_at, updated_at) VALUES ('w', 'workspace', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
			sessionInsert := `INSERT INTO sessions (id, title, agent_name, workspace_id, created_at, updated_at) VALUES ('s', 'orchid', 'agent', 'w', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`
			execSQL(t, db, sessionInsert)
			for _, query := range []string{
				sessionInsert, // primary-key uniqueness
				`INSERT INTO messages (id, session_id, role, content, created_at) VALUES ('bad', 'missing', 'user', 'body', CURRENT_TIMESTAMP)`,
				`INSERT INTO user_preference_revisions VALUES ('local', 'invalid-field', 1)`,
				`UPDATE user_preference_revisions SET revision = 0 WHERE user_id = 'local'`,
			} {
				if _, err := db.Exec(query); err == nil {
					t.Fatalf("constraint unexpectedly allowed: %s", query)
				}
			}
			execSQL(t, db, `INSERT INTO messages (id, session_id, role, content, created_at) VALUES ('m', 's', 'user', 'body', CURRENT_TIMESTAMP)`)
			execSQL(t, db, `INSERT INTO workspace_notes (id, workspace_id, name, content, created_at, updated_at) VALUES ('n', 'w', 'orchid', 'orchid', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
			execSQL(t, db, `INSERT INTO note_headings (note_id, level, text, position) VALUES ('n', 1, 'orchid', 0)`)
			ftsCount := func(word string, want int) {
				t.Helper()
				for _, table := range []string{"sessions_fts", "workspace_notes_fts", "note_headings_fts"} {
					wantInt(t, db, "SELECT COUNT(*) FROM "+table+" WHERE "+table+" MATCH ?", want, word)
				}
			}
			ftsCount("orchid", 1)
			execSQL(t, db, "UPDATE sessions SET title = 'cedar' WHERE id = 's'")
			execSQL(t, db, "UPDATE workspace_notes SET name = 'cedar', content = 'cedar' WHERE id = 'n'")
			// Heading writers use delete+insert, not SQL UPDATE (production contract).
			execSQL(t, db, "DELETE FROM note_headings WHERE note_id = 'n'")
			execSQL(t, db, `INSERT INTO note_headings (note_id, level, text, position) VALUES ('n', 1, 'cedar', 0)`)
			ftsCount("orchid", 0)
			ftsCount("cedar", 1)
			execSQL(t, db, `UPDATE users SET preferences = '{"language":"English"}' WHERE id = 'local'`)
			wantInt(t, db, "SELECT revision FROM user_preference_revisions WHERE user_id = 'local' AND field = 'language'", 2)
			wantInt(t, db, "SELECT revision FROM user_preference_revisions WHERE user_id = 'local' AND field = 'units'", 1)
			execSQL(t, db, "DELETE FROM sessions WHERE id = 's'")
			wantInt(t, db, "SELECT COUNT(*) FROM messages", 0)
			execSQL(t, db, "DELETE FROM workspaces WHERE id = 'w'")
			wantInt(t, db, "SELECT COUNT(*) FROM workspace_notes", 0)
			wantInt(t, db, "SELECT COUNT(*) FROM note_headings", 0)
			ftsCount("cedar", 0)
			var violation any
			if err := db.QueryRow("PRAGMA foreign_key_check").Scan(&violation); err != sql.ErrNoRows {
				t.Fatalf("foreign-key violations: %v", err)
			}
		})
	}
}

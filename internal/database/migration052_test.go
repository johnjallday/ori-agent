package database

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

// openMigratedFromV51 builds a database stopped at schema version 51, seeds the
// relationship shapes that exist in the field, and then opens it so the
// remaining migrations run.
func openMigratedFromV51(t *testing.T) (*DB, context.Context) {
	t.Helper()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "v51.db")

	staging, err := Open(ctx, &Config{Path: dbPath, WALMode: false})
	if err != nil {
		t.Fatalf("open staging database: %v", err)
	}
	// Restore the genuine v51 table — the narrow status constraint and no HQ
	// journal columns — and rewind the recorded version so migration 52 has real
	// work to do on a real pre-amendment shape.
	rewind := []string{
		`DELETE FROM schema_migrations WHERE version > 51`,
		`PRAGMA foreign_keys = OFF`,
		`DROP TABLE personal_assistant_state`,
		`CREATE TABLE personal_assistant_state (
			user_id TEXT PRIMARY KEY,
			assistant_id TEXT NOT NULL UNIQUE,
			status TEXT NOT NULL DEFAULT 'not_hired'
				CHECK (status IN ('not_hired', 'hiring', 'active', 'paused', 'repair_needed')),
			display_name TEXT NOT NULL DEFAULT '',
			appearance_json TEXT NOT NULL DEFAULT '{}',
			hq_workspace_id TEXT NOT NULL DEFAULT '',
			hq_entry_agent_instance_id TEXT NOT NULL DEFAULT '',
			global_agent_profile_name TEXT NOT NULL DEFAULT '',
			mandate TEXT NOT NULL DEFAULT '',
			focus_areas_json TEXT NOT NULL DEFAULT '[]',
			first_assignment_status TEXT NOT NULL DEFAULT 'not_started'
				CHECK (first_assignment_status IN ('not_started', 'previewed', 'applying', 'completed', 'failed')),
			last_hire_request_id TEXT NOT NULL DEFAULT '',
			hire_payload_hash TEXT NOT NULL DEFAULT '',
			hire_payload_json TEXT NOT NULL DEFAULT '',
			repair_step TEXT NOT NULL DEFAULT '',
			rename_from_name TEXT NOT NULL DEFAULT '',
			rename_to_name TEXT NOT NULL DEFAULT '',
			rename_step TEXT NOT NULL DEFAULT '',
			state_version INTEGER NOT NULL DEFAULT 1 CHECK (state_version > 0),
			hired_at DATETIME,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			UNIQUE (user_id, assistant_id)
		)`,
		`PRAGMA foreign_keys = ON`,
	}
	for _, stmt := range rewind {
		if _, err := staging.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("restore v51 schema: %v", err)
		}
	}

	seed := `INSERT INTO personal_assistant_state (
		user_id, assistant_id, status, display_name, hq_workspace_id,
		hq_entry_agent_instance_id, global_agent_profile_name, mandate,
		first_assignment_status, last_hire_request_id, repair_step,
		state_version, hired_at, created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	hired := "2026-01-01T00:00:00Z"
	rows := []struct {
		user, assistant, status, workspace, instance, first, repair string
		version                                                     int
	}{
		{"user-active", "assistant-active", "active", "ws-active", "inst-active", "completed", "", 9},
		{"user-paused", "assistant-paused", "paused", "ws-paused", "inst-paused", "not_started", "", 3},
		{"user-hiring", "assistant-hiring", "hiring", "", "", "not_started", "", 1},
		{"user-repair", "assistant-repair", "repair_needed", "ws-repair", "", "not_started", "designation", 5},
	}
	for _, row := range rows {
		if _, err := staging.ExecContext(ctx, seed, row.user, row.assistant, row.status,
			"Ada", row.workspace, row.instance, "Ada", "Keep it visible.",
			row.first, "hire-"+row.user, row.repair, row.version, hired, hired, hired); err != nil {
			t.Fatalf("seed %s relationship: %v", row.status, err)
		}
	}
	// One assignment row so the child journal's foreign key is exercised by the
	// parent-table rebuild.
	if _, err := staging.ExecContext(ctx, `
		INSERT INTO personal_assistant_assignment (
			preview_id, user_id, assistant_id, assignment_version,
			normalized_payload_json, normalized_payload_hash, status,
			created_canonical_refs_json, created_at, updated_at
		) VALUES ('preview-1', 'user-active', 'assistant-active', 2, '{}', 'hash-1',
			'completed', '[]', ?, ?)
	`, hired, hired); err != nil {
		t.Fatalf("seed assignment: %v", err)
	}
	// Guard the fixture itself: this must really be the pre-amendment schema, or
	// the assertions below would pass against an already-rebuilt table.
	if _, err := staging.ExecContext(ctx, seed, "user-guard", "assistant-guard", "awaiting_hq",
		"Ada", "", "", "Ada", "", "not_started", "hire-guard", "", 1, hired, hired, hired); err == nil {
		t.Fatal("fixture is not a v51 schema: awaiting_hq was already accepted")
	}
	if err := staging.Close(); err != nil {
		t.Fatalf("close staging database: %v", err)
	}

	db, err := Open(ctx, &Config{Path: dbPath, WALMode: false})
	if err != nil {
		t.Fatalf("migrate v51 database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, ctx
}

func TestMigration052PreservesExistingRelationships(t *testing.T) {
	db, ctx := openMigratedFromV51(t)

	version, err := db.GetSchemaVersion(ctx)
	if err != nil || version != schemaVersion {
		t.Fatalf("schema version = %d, %v; want %d", version, err, schemaVersion)
	}

	for _, want := range []struct {
		user, status, workspace, instance, repair string
		version                                   int
	}{
		{"user-active", "active", "ws-active", "inst-active", "", 9},
		{"user-paused", "paused", "ws-paused", "inst-paused", "", 3},
		{"user-hiring", "hiring", "", "", "", 1},
		{"user-repair", "repair_needed", "ws-repair", "", "designation", 5},
	} {
		var status, workspace, instance, repair, hiredAt, createdAt, updatedAt, hireRequest string
		var stateVersion int
		err := db.QueryRowContext(ctx, `
			SELECT status, hq_workspace_id, hq_entry_agent_instance_id, repair_step,
				state_version, last_hire_request_id, hired_at, created_at, updated_at
			FROM personal_assistant_state WHERE user_id = ?
		`, want.user).Scan(&status, &workspace, &instance, &repair, &stateVersion,
			&hireRequest, &hiredAt, &createdAt, &updatedAt)
		if err != nil {
			t.Fatalf("read migrated %s: %v", want.user, err)
		}
		if status != want.status || workspace != want.workspace || instance != want.instance ||
			repair != want.repair || stateVersion != want.version {
			t.Fatalf("%s changed across migration: status=%q ws=%q inst=%q repair=%q version=%d",
				want.user, status, workspace, instance, repair, stateVersion)
		}
		if hireRequest != "hire-"+want.user {
			t.Fatalf("%s lost its hire request id: %q", want.user, hireRequest)
		}
		if hiredAt == "" || createdAt == "" || updatedAt == "" {
			t.Fatalf("%s lost a lifecycle timestamp", want.user)
		}
		// The HQ journal starts empty for every pre-amendment row.
		var requestID, hash, payload string
		if err := db.QueryRowContext(ctx, `
			SELECT last_hq_request_id, hq_payload_hash, hq_payload_json
			FROM personal_assistant_state WHERE user_id = ?
		`, want.user).Scan(&requestID, &hash, &payload); err != nil {
			t.Fatalf("read hq journal for %s: %v", want.user, err)
		}
		if requestID != "" || hash != "" || payload != "" {
			t.Fatalf("%s gained a phantom hq operation: %q/%q/%q", want.user, requestID, hash, payload)
		}
	}
}

func TestMigration052PreservesAssignmentForeignKeyAndIndexes(t *testing.T) {
	db, ctx := openMigratedFromV51(t)

	var previewID string
	if err := db.QueryRowContext(ctx, `
		SELECT preview_id FROM personal_assistant_assignment WHERE user_id = 'user-active'
	`).Scan(&previewID); err != nil || previewID != "preview-1" {
		t.Fatalf("assignment row = %q, %v", previewID, err)
	}

	// The rebuilt parent must still enforce the cascade relationship.
	row := db.QueryRowContext(ctx, `PRAGMA foreign_key_check`)
	var table, parent sql.NullString
	var rowid, fkid sql.NullInt64
	if err := row.Scan(&table, &rowid, &parent, &fkid); err == nil {
		t.Fatalf("foreign key violation after migration in %s", table.String)
	} else if err != sql.ErrNoRows {
		t.Fatalf("foreign_key_check: %v", err)
	}

	if _, err := db.ExecContext(ctx, `
		INSERT INTO personal_assistant_assignment (
			preview_id, user_id, assistant_id, assignment_version,
			normalized_payload_json, normalized_payload_hash, status,
			created_canonical_refs_json, created_at, updated_at
		) VALUES ('preview-orphan', 'ghost-user', 'ghost-assistant', 1, '{}', 'h',
			'previewed', '[]', datetime('now'), datetime('now'))
	`); err == nil {
		t.Fatal("assignment journal accepted a row with no owning relationship")
	}

	if _, err := db.ExecContext(ctx, `DELETE FROM personal_assistant_state WHERE user_id = 'user-active'`); err != nil {
		t.Fatalf("delete relationship: %v", err)
	}
	var remaining int
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM personal_assistant_assignment WHERE user_id = 'user-active'
	`).Scan(&remaining); err != nil {
		t.Fatalf("count cascaded assignments: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("ON DELETE CASCADE lost across the rebuild: %d rows remain", remaining)
	}

	for _, index := range []string{
		"idx_personal_assistant_assignment_owner",
		"idx_personal_assistant_assignment_hash",
		"idx_personal_assistant_assignment_apply_request",
	} {
		var name string
		if err := db.QueryRowContext(ctx,
			`SELECT name FROM sqlite_master WHERE type = 'index' AND name = ?`, index).Scan(&name); err != nil {
			t.Fatalf("index %s missing after migration: %v", index, err)
		}
	}
}

func TestMigration052StatusConstraintStaysClosed(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, &Config{InMemory: true, WALMode: false})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer func() { _ = db.Close() }()

	insert := func(user, status string) error {
		_, execErr := db.ExecContext(ctx, `
			INSERT INTO personal_assistant_state (user_id, assistant_id, status, created_at, updated_at)
			VALUES (?, ?, ?, datetime('now'), datetime('now'))
		`, user, "assistant-"+user, status)
		return execErr
	}
	for _, status := range []string{
		"not_hired", "hiring", "awaiting_hq", "provisioning_hq", "active", "paused", "repair_needed",
	} {
		if err := insert("user-"+status, status); err != nil {
			t.Fatalf("status %q rejected: %v", status, err)
		}
	}
	// The projection-only names and typos must stay out of durable storage.
	for _, status := range []string{"needs_hq", "needs_hire", "building_hq", "", "awaiting-hq"} {
		if err := insert("bad-"+status, status); err == nil {
			t.Fatalf("status %q accepted into the closed constraint", status)
		}
	}
}

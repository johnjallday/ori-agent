package vault

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func TestEnsureVaultFileSchema(t *testing.T) {
	ctx := context.Background()

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite database: %v", err)
	}
	defer func() { _ = db.Close() }()

	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		t.Fatalf("enable foreign keys: %v", err)
	}

	if err := ensureVaultFileSchema(ctx, db); err != nil {
		t.Fatalf("ensure vault file schema: %v", err)
	}

	tables := []string{
		"vault_schema_migrations",
		"vault_metadata",
		"vault_records",
		"vault_folders",
		"vault_record_attachments",
		"vault_grants",
		"vault_audit_events",
	}
	for _, table := range tables {
		var name string
		if err := db.QueryRowContext(ctx, `
			SELECT name
			FROM sqlite_master
			WHERE type = 'table' AND name = ?
		`, table).Scan(&name); err != nil {
			t.Fatalf("expected table %s to exist: %v", table, err)
		}
	}

	metadataColumns := map[string]bool{
		"vault_id":       false,
		"name":           false,
		"description":    false,
		"key_salt":       false,
		"key_nonce":      false,
		"key_ciphertext": false,
	}

	rows, err := db.QueryContext(ctx, "PRAGMA table_info(vault_metadata)")
	if err != nil {
		t.Fatalf("inspect vault_metadata columns: %v", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var cid int
		var name string
		var columnType string
		var notNull int
		var defaultValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
			t.Fatalf("scan vault_metadata column: %v", err)
		}
		if _, ok := metadataColumns[name]; ok {
			metadataColumns[name] = true
		}
	}

	for name, found := range metadataColumns {
		if !found {
			t.Fatalf("expected vault_metadata column %s to exist", name)
		}
	}

	var version int
	if err := db.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(version), 0)
		FROM vault_schema_migrations
	`).Scan(&version); err != nil {
		t.Fatalf("query vault schema version: %v", err)
	}
	if version != vaultFileSchemaVersion {
		t.Fatalf("expected vault schema version %d, got %d", vaultFileSchemaVersion, version)
	}
}

func TestEnsureVaultFileSchemaIsIdempotent(t *testing.T) {
	ctx := context.Background()

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite database: %v", err)
	}
	defer func() { _ = db.Close() }()

	if err := ensureVaultFileSchema(ctx, db); err != nil {
		t.Fatalf("first ensure vault schema: %v", err)
	}
	if err := ensureVaultFileSchema(ctx, db); err != nil {
		t.Fatalf("second ensure vault schema: %v", err)
	}

	var versionCount int
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM vault_schema_migrations
		WHERE version = ?
	`, vaultFileSchemaVersion).Scan(&versionCount); err != nil {
		t.Fatalf("count vault schema versions: %v", err)
	}
	if versionCount != 1 {
		t.Fatalf("expected one applied vault schema version row, got %d", versionCount)
	}
}

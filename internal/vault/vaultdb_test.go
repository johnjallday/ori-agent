package vault

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
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

func TestNormalizeVaultStorageDirectoryCreatesAndNormalizesDirectory(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "nested", "..", "nested", "vaults")

	normalized, err := normalizeVaultStorageDirectory(target)
	if err != nil {
		t.Fatalf("normalize vault storage directory: %v", err)
	}

	if normalized != filepath.Clean(target) {
		t.Fatalf("expected normalized path %q, got %q", filepath.Clean(target), normalized)
	}

	info, err := os.Stat(normalized)
	if err != nil {
		t.Fatalf("stat normalized directory: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("expected %q to be a directory", normalized)
	}
}

func TestVaultFileExistsReportsMissingAndExistingFiles(t *testing.T) {
	root := t.TempDir()
	existingFile := filepath.Join(root, "vault.db")
	if err := os.WriteFile(existingFile, []byte("vault"), 0o644); err != nil {
		t.Fatalf("write existing file: %v", err)
	}

	exists, err := vaultFileExists(existingFile)
	if err != nil {
		t.Fatalf("vaultFileExists existing: %v", err)
	}
	if !exists {
		t.Fatalf("expected existing file to be reported as present")
	}

	missingFile := filepath.Join(root, "missing.db")
	exists, err = vaultFileExists(missingFile)
	if err != nil {
		t.Fatalf("vaultFileExists missing: %v", err)
	}
	if exists {
		t.Fatalf("expected missing file to be reported as absent")
	}
}

func TestDefaultVaultPackageFilePath(t *testing.T) {
	got := defaultVaultPackageFilePath("vault-123")
	want := filepath.Join("vault-123.orivault", "vault.db")
	if got != want {
		t.Fatalf("expected package file path %q, got %q", want, got)
	}
}

func TestVaultPackageDirectoryForFilePath(t *testing.T) {
	path := filepath.Join("/tmp", "vault-123.orivault", "vault.db")
	if got := vaultPackageDirectoryForFilePath(path); got != filepath.Join("/tmp", "vault-123.orivault") {
		t.Fatalf("expected package directory to resolve, got %q", got)
	}

	legacyPath := filepath.Join("/tmp", "vault-123.db")
	if got := vaultPackageDirectoryForFilePath(legacyPath); got != "" {
		t.Fatalf("expected legacy file path to have no package dir, got %q", got)
	}
}

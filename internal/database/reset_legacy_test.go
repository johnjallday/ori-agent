package database

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// Reset preflight cannot use Open to inspect an old installation: Open migrates
// before returning. Characterize that fact with the real v11 schema and only
// synthetic vault content. This package uses its own TempDir (resetfixture's
// seed imports database); no config, native secret store or external service is
// constructed, and every SQLite open uses this explicit test-owned path.
func TestResetLifecycleOpenDropsLegacyDBOnlyVaultEvidence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	legacy := &DB{DB: raw, path: path}
	closed := false
	t.Cleanup(func() {
		if !closed {
			if err := legacy.Close(); err != nil {
				t.Error(err)
			}
		}
	})
	if err := legacy.applyPragmas(t.Context(), DefaultConfig()); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at DATETIME DEFAULT CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	for version := 1; version <= 11; version++ {
		if err := legacy.runMigration(t.Context(), version); err != nil {
			t.Fatal(err)
		}
		if _, err := raw.Exec(`INSERT INTO schema_migrations (version) VALUES (?)`, version); err != nil {
			t.Fatal(err)
		}
	}
	for _, statement := range []string{
		`INSERT INTO vaults (id, name, key_salt, key_nonce, key_ciphertext, created_at, updated_at)
		 VALUES ('legacy', 'Fixture Vault', 'synthetic-salt', 'synthetic-nonce', 'synthetic-wrapped-key', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		`INSERT INTO vault_records (id, vault_id, type, metadata_nonce, metadata_ciphertext, payload_nonce, payload_ciphertext, created_at, updated_at)
		 VALUES ('fixture-record', 'legacy', 'personal_note', 'synthetic', 'synthetic', 'synthetic', 'synthetic-payload', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
	} {
		if _, err := raw.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	var before int
	if err := raw.QueryRow(`SELECT COUNT(*) FROM vault_records`).Scan(&before); err != nil || before != 1 {
		t.Fatal("legacy fixture did not contain DB-only vault data:", err)
	}
	closeErr := legacy.Close()
	closed = true
	if closeErr != nil {
		t.Fatal(closeErr)
	}

	current, err := Open(t.Context(), &Config{Path: path, WALMode: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := current.Close(); err != nil {
			t.Error(err)
		}
	})
	var contentTables, catalogs int
	if err := current.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'vault_records'`).Scan(&contentTables); err != nil {
		t.Fatal(err)
	}
	if err := current.QueryRow(`SELECT COUNT(*) FROM vaults WHERE id = 'legacy'`).Scan(&catalogs); err != nil {
		t.Fatal(err)
	}
	if contentTables != 0 || catalogs != 0 {
		t.Fatal("characterization changed: migrations no longer discard legacy vault evidence")
	}
}

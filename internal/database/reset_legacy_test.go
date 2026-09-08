package database

import (
	"bytes"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// Use explicit, newly allocated paths and real v11 migrations. This package
// cannot import resetfixture (its seed imports database). No config, native
// secret store, server or provider is constructed by these tests.
func legacyResetDatabase(t *testing.T) *DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sessions.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	legacy := &DB{DB: raw, path: path}
	t.Cleanup(func() {
		if err := raw.Close(); err != nil {
			t.Error(err)
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
	return legacy
}

func seedLegacyResetKey(t *testing.T, db *DB) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO vaults (id, name, key_salt, key_nonce, key_ciphertext, created_at, updated_at)
	 VALUES ('legacy', 'Fixture Vault', 'synthetic-salt', 'synthetic-nonce', 'synthetic-wrapped-key', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
	if err != nil {
		t.Fatal(err)
	}
}

func TestResetLifecycleOpenBlocksLegacyDBOnlyVaultBeforeMigration(t *testing.T) {
	legacy := legacyResetDatabase(t)
	seedLegacyResetKey(t, legacy)
	_, err := legacy.Exec(`INSERT INTO vault_records (id, vault_id, type, metadata_nonce, metadata_ciphertext, payload_nonce, payload_ciphertext, created_at, updated_at)
	 VALUES ('fixture-record', 'legacy', 'personal_note', 'synthetic', 'synthetic', 'synthetic', 'synthetic-payload', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
	if err != nil {
		t.Fatal(err)
	}
	inspection, err := InspectReset(t.Context(), legacy.DB)
	if err != nil {
		t.Fatal(err)
	}
	blocked := false
	for _, problem := range inspection.Problems {
		blocked = blocked || problem == "legacy_vault_schema"
	}
	if !blocked || inspection.Version != 11 {
		t.Fatal("read-only preflight did not recognize the legacy layout")
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(legacy.Path())
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		opened, err := Open(t.Context(), &Config{Path: legacy.Path(), WALMode: true})
		if opened != nil || !errors.Is(err, ErrRetainedVaultMigration) {
			t.Fatal("startup did not block destructive legacy migration:", err)
		}
	}
	after, err := os.ReadFile(legacy.Path())
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("blocked startup changed retained database bytes:", err)
	}
	raw, err := sql.Open("sqlite", legacy.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := raw.Close(); err != nil {
			t.Error(err)
		}
	}()
	var records, catalogs, version int
	if err := raw.QueryRow(`SELECT COUNT(*) FROM vault_records`).Scan(&records); err != nil {
		t.Fatal(err)
	}
	if err := raw.QueryRow(`SELECT COUNT(*) FROM vaults WHERE length(key_ciphertext) > 0`).Scan(&catalogs); err != nil {
		t.Fatal(err)
	}
	if err := raw.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if records != 1 || catalogs != 1 || version != 11 {
		t.Fatal("legacy content, keys or migration boundary changed")
	}
}

func TestResetLifecycleOpenBlocksOnlyCopyKeyInLiveWAL(t *testing.T) {
	legacy := legacyResetDatabase(t)
	seedLegacyResetKey(t, legacy) // no content records: key material alone must block
	walPath := legacy.Path() + "-wal"
	before, err := os.ReadFile(walPath)
	if err != nil || len(before) == 0 {
		t.Fatal("expected populated fixture WAL:", err)
	}
	opened, err := Open(t.Context(), &Config{Path: legacy.Path(), WALMode: true})
	if opened != nil || !errors.Is(err, ErrRetainedVaultMigration) {
		t.Fatal("only-copy key material in WAL was not protected:", err)
	}
	after, err := os.ReadFile(walPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("guard changed or checkpointed legacy WAL:", err)
	}
}

func TestResetLifecycleRefusalDoesNotCheckpointAnOfflineWAL(t *testing.T) {
	legacy := legacyResetDatabase(t)
	seedLegacyResetKey(t, legacy)
	// Copy a committed DB+WAL while the source is idle, without closing its
	// connection. The copy has no connection holding its WAL open, like a
	// crashed installation. Reserved URI characters also exercise safe quoting.
	path := filepath.Join(t.TempDir(), "offline #%.db")
	before := make(map[string][]byte)
	for _, suffix := range []string{"", "-wal"} {
		data, err := os.ReadFile(legacy.Path() + suffix)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path+suffix, data, 0o600); err != nil {
			t.Fatal(err)
		}
		before[suffix] = data
	}
	for range 2 {
		opened, err := Open(t.Context(), &Config{Path: path, WALMode: true})
		if opened != nil || !errors.Is(err, ErrRetainedVaultMigration) {
			t.Fatal("offline WAL was not inspected before writable open:", err)
		}
		for suffix, expected := range before {
			actual, err := os.ReadFile(path + suffix)
			if err != nil || !bytes.Equal(expected, actual) {
				t.Fatal("refusal implicitly checkpointed or modified offline DB/WAL:", suffix, err)
			}
		}
	}
}

func TestResetLifecycleOpenBlocksUnclassifiedVaultColumns(t *testing.T) {
	legacy := legacyResetDatabase(t)
	if _, err := legacy.Exec(`ALTER TABLE vaults ADD COLUMN other_key_copy TEXT`); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}
	opened, err := Open(t.Context(), &Config{Path: legacy.Path(), WALMode: true})
	if opened != nil || !errors.Is(err, ErrRetainedVaultMigration) {
		t.Fatal("unknown vault material was treated as disposable metadata:", err)
	}
}

func TestResetLifecycleEmptyLegacyLayoutCanStillUpgrade(t *testing.T) {
	legacy := legacyResetDatabase(t)
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}
	current, err := Open(t.Context(), &Config{Path: legacy.Path(), WALMode: true})
	if err != nil {
		t.Fatal("empty legacy schema incorrectly blocked:", err)
	}
	defer func() {
		if err := current.Close(); err != nil {
			t.Error(err)
		}
	}()
	version, err := current.GetSchemaVersion(t.Context())
	if err != nil || version != schemaVersion {
		t.Fatal("empty legacy layout did not reach canonical schema:", err)
	}
}

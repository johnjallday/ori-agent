package vault

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const vaultFileSchemaVersion = 1
const vaultPackageExtension = ".orivault"
const vaultPackageDatabaseFileName = "vault.db"

type vaultFileMetadata struct {
	VaultID       string
	Name          string
	Description   string
	KeySalt       string
	KeyNonce      string
	KeyCiphertext string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

func defaultVaultFilesBaseDir(dbPath string) string {
	dbPath = strings.TrimSpace(dbPath)
	if dbPath != "" && dbPath != ":memory:" {
		return filepath.Dir(dbPath)
	}

	tmpDir, err := os.MkdirTemp("", "ori-vault-files-*")
	if err == nil {
		return tmpDir
	}

	if cwd, cwdErr := os.Getwd(); cwdErr == nil {
		return cwd
	}
	return os.TempDir()
}

func normalizeVaultStorageMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", VaultStorageModeManaged:
		return VaultStorageModeManaged
	case VaultStorageModeCustomDir:
		return VaultStorageModeCustomDir
	default:
		return strings.ToLower(strings.TrimSpace(mode))
	}
}

func defaultVaultPackageName(vaultID string) string {
	vaultID = normalizeVaultID(vaultID)
	if vaultID == "" {
		vaultID = "vault"
	}
	return vaultID + vaultPackageExtension
}

func defaultVaultPackageFilePath(vaultID string) string {
	return filepath.Join(defaultVaultPackageName(vaultID), vaultPackageDatabaseFileName)
}

func vaultPackageDirectoryForFilePath(path string) string {
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "" {
		return ""
	}

	if filepath.Base(path) != vaultPackageDatabaseFileName {
		return ""
	}

	packageDir := filepath.Dir(path)
	if strings.HasSuffix(strings.ToLower(filepath.Base(packageDir)), vaultPackageExtension) {
		return packageDir
	}
	return ""
}

func resolveVaultPackageDirectory(directory string, vaultID string) string {
	directory = filepath.Clean(strings.TrimSpace(directory))
	if directory == "" {
		return ""
	}
	if strings.HasSuffix(strings.ToLower(filepath.Base(directory)), vaultPackageExtension) {
		return directory
	}
	return filepath.Join(directory, defaultVaultPackageName(vaultID))
}

func normalizeVaultStorageDirectory(directory string) (string, error) {
	directory = strings.TrimSpace(directory)
	if directory == "" {
		return "", ErrVaultStoragePathRequired
	}

	absolutePath, err := filepath.Abs(directory)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrVaultStoragePathInvalid, err)
	}
	absolutePath = filepath.Clean(absolutePath)

	info, err := os.Stat(absolutePath)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if err := os.MkdirAll(absolutePath, 0o755); err != nil {
			return "", fmt.Errorf("%w: %v", ErrVaultStoragePathInvalid, err)
		}
		return absolutePath, nil
	case err != nil:
		return "", fmt.Errorf("%w: %v", ErrVaultStoragePathInvalid, err)
	case !info.IsDir():
		return "", fmt.Errorf("%w: path is not a directory", ErrVaultStoragePathInvalid)
	default:
		return absolutePath, nil
	}
}

func vaultFileExists(path string) (bool, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return false, nil
	}

	info, err := os.Stat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return false, nil
	case err != nil:
		return false, err
	case info.IsDir():
		return false, fmt.Errorf("%w: path is a directory", ErrVaultStoragePathInvalid)
	default:
		return true, nil
	}
}

func openVaultFile(ctx context.Context, path string) (*sql.DB, error) {
	return openVaultFileWithMode(ctx, path, true)
}

func openExistingVaultFile(ctx context.Context, path string) (*sql.DB, error) {
	return openVaultFileWithMode(ctx, path, false)
}

func openVaultFileWithMode(ctx context.Context, path string, allowCreate bool) (*sql.DB, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("vault file path is required")
	}
	if allowCreate {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, fmt.Errorf("create vault file directory: %w", err)
		}
	} else {
		info, err := os.Stat(path)
		switch {
		case errors.Is(err, os.ErrNotExist):
			return nil, ErrVaultFileMissing
		case err != nil:
			return nil, fmt.Errorf("stat vault file: %w", err)
		case info.IsDir():
			return nil, fmt.Errorf("%w: path is a directory", ErrVaultFileCorrupt)
		}
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		if allowCreate {
			return nil, fmt.Errorf("open vault file database: %w", err)
		}
		return nil, fmt.Errorf("%w: open vault file database: %v", ErrVaultFileCorrupt, err)
	}

	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	if err := applyVaultFilePragmas(ctx, db); err != nil {
		_ = db.Close()
		if allowCreate {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %v", ErrVaultFileCorrupt, err)
	}

	var schemaErr error
	if allowCreate {
		schemaErr = ensureVaultFileSchema(ctx, db)
	} else {
		schemaErr = validateVaultFileSchema(ctx, db)
	}
	if schemaErr != nil {
		_ = db.Close()
		return nil, schemaErr
	}

	return db, nil
}

func applyVaultFilePragmas(ctx context.Context, db *sql.DB) error {
	pragmas := []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA busy_timeout = 5000",
		"PRAGMA synchronous = NORMAL",
		"PRAGMA mmap_size = 67108864",
		"PRAGMA cache_size = -8000",
		"PRAGMA temp_store = MEMORY",
		"PRAGMA journal_mode = WAL",
	}

	for _, pragma := range pragmas {
		if _, err := db.ExecContext(ctx, pragma); err != nil {
			return fmt.Errorf("apply vault file pragma %q: %w", pragma, err)
		}
	}
	return nil
}

func upsertVaultFileMetadata(ctx context.Context, db *sql.DB, metadata vaultFileMetadata) error {
	if db == nil {
		return fmt.Errorf("vault file database is required")
	}

	metadata.VaultID = normalizeVaultID(metadata.VaultID)
	metadata.Name = strings.TrimSpace(metadata.Name)
	metadata.Description = strings.TrimSpace(metadata.Description)
	if metadata.VaultID == "" {
		return ErrVaultRequired
	}
	if metadata.Name == "" {
		return ErrVaultNameRequired
	}

	if metadata.CreatedAt.IsZero() {
		metadata.CreatedAt = metadata.UpdatedAt
	}
	if metadata.CreatedAt.IsZero() {
		metadata.CreatedAt = time.Now()
	}
	if metadata.UpdatedAt.IsZero() {
		metadata.UpdatedAt = metadata.CreatedAt
	}

	if _, err := db.ExecContext(ctx, `
		INSERT INTO vault_metadata (
			vault_id, name, description, key_salt, key_nonce, key_ciphertext, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(vault_id) DO UPDATE SET
			name = excluded.name,
			description = excluded.description,
			key_salt = excluded.key_salt,
			key_nonce = excluded.key_nonce,
			key_ciphertext = excluded.key_ciphertext,
			updated_at = excluded.updated_at
	`, metadata.VaultID, metadata.Name, metadata.Description, metadata.KeySalt, metadata.KeyNonce, metadata.KeyCiphertext, metadata.CreatedAt, metadata.UpdatedAt); err != nil {
		return fmt.Errorf("upsert vault file metadata: %w", err)
	}
	return nil
}

func loadVaultFileMetadata(ctx context.Context, db *sql.DB, vaultID string) (vaultFileMetadata, error) {
	if db == nil {
		return vaultFileMetadata{}, fmt.Errorf("vault file database is required")
	}

	vaultID = normalizeVaultID(vaultID)
	if vaultID == "" {
		return vaultFileMetadata{}, ErrVaultRequired
	}

	var metadata vaultFileMetadata
	err := db.QueryRowContext(ctx, `
		SELECT vault_id, name, description, key_salt, key_nonce, key_ciphertext, created_at, updated_at
		FROM vault_metadata
		WHERE vault_id = ?
	`, vaultID).Scan(
		&metadata.VaultID,
		&metadata.Name,
		&metadata.Description,
		&metadata.KeySalt,
		&metadata.KeyNonce,
		&metadata.KeyCiphertext,
		&metadata.CreatedAt,
		&metadata.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return vaultFileMetadata{}, ErrVaultFileCorrupt
	}
	if err != nil {
		return vaultFileMetadata{}, fmt.Errorf("load vault file metadata: %w", err)
	}
	return metadata, nil
}

func validateVaultFileSchema(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("vault file database is required")
	}

	requiredTables := []string{
		"vault_schema_migrations",
		"vault_metadata",
	}
	for _, tableName := range requiredTables {
		var existingName string
		err := db.QueryRowContext(ctx, `
			SELECT name
			FROM sqlite_master
			WHERE type = 'table' AND name = ?
		`, tableName).Scan(&existingName)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			return fmt.Errorf("%w: missing %s table", ErrVaultFileCorrupt, tableName)
		case err != nil:
			return fmt.Errorf("%w: inspect %s table: %v", ErrVaultFileCorrupt, tableName, err)
		}
	}

	var currentVersion int
	if err := db.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(version), 0)
		FROM vault_schema_migrations
	`).Scan(&currentVersion); err != nil {
		return fmt.Errorf("%w: query vault schema version: %v", ErrVaultFileCorrupt, err)
	}
	if currentVersion != vaultFileSchemaVersion {
		return fmt.Errorf("%w: unsupported vault schema version %d", ErrVaultFileCorrupt, currentVersion)
	}

	return nil
}

// ensureVaultFileSchema prepares a per-vault SQLite database for encrypted
// vault data. Each vault package keeps metadata, wrapped key material, records,
// folders, attachments, grants, and audit events in its internal `vault.db`.
func ensureVaultFileSchema(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("vault file database is required")
	}

	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS vault_schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`); err != nil {
		return fmt.Errorf("create vault schema migrations table: %w", err)
	}

	var currentVersion int
	if err := db.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(version), 0)
		FROM vault_schema_migrations
	`).Scan(&currentVersion); err != nil {
		return fmt.Errorf("query vault schema version: %w", err)
	}

	if currentVersion > vaultFileSchemaVersion {
		return fmt.Errorf("vault file schema version %d is newer than supported %d", currentVersion, vaultFileSchemaVersion)
	}
	if currentVersion == vaultFileSchemaVersion {
		return nil
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin vault schema transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, stmt := range vaultFileSchemaStatements() {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("apply vault file schema: %w", err)
		}
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO vault_schema_migrations (version) VALUES (?)
	`, vaultFileSchemaVersion); err != nil {
		return fmt.Errorf("record vault schema version: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit vault schema transaction: %w", err)
	}
	return nil
}

func vaultFileSchemaStatements() []string {
	return []string{
		`
		CREATE TABLE IF NOT EXISTS vault_metadata (
			vault_id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			description TEXT DEFAULT '',
			key_salt TEXT NOT NULL DEFAULT '',
			key_nonce TEXT NOT NULL DEFAULT '',
			key_ciphertext TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)
	`,
		`
		CREATE TABLE IF NOT EXISTS vault_records (
			id TEXT PRIMARY KEY,
			vault_id TEXT NOT NULL,
			type TEXT NOT NULL,
			workspace_id TEXT DEFAULT '',
			source TEXT DEFAULT '',
			retention_policy TEXT DEFAULT '',
			metadata_nonce TEXT NOT NULL,
			metadata_ciphertext TEXT NOT NULL,
			payload_nonce TEXT NOT NULL,
			payload_ciphertext TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)
	`,
		`
		CREATE TABLE IF NOT EXISTS vault_folders (
			id TEXT PRIMARY KEY,
			vault_id TEXT NOT NULL,
			path_hash TEXT NOT NULL UNIQUE,
			path_nonce TEXT NOT NULL,
			path_ciphertext TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)
	`,
		`
		CREATE TABLE IF NOT EXISTS vault_record_attachments (
			id TEXT PRIMARY KEY,
			record_id TEXT NOT NULL,
			vault_id TEXT NOT NULL,
			metadata_nonce TEXT NOT NULL,
			metadata_ciphertext TEXT NOT NULL,
			data_nonce TEXT NOT NULL,
			data_ciphertext TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			FOREIGN KEY (record_id) REFERENCES vault_records(id) ON DELETE CASCADE
		)
	`,
		`
		CREATE TABLE IF NOT EXISTS vault_grants (
			id TEXT PRIMARY KEY,
			vault_id TEXT NOT NULL,
			workspace_id TEXT NOT NULL,
			actor_type TEXT NOT NULL,
			actor_id TEXT NOT NULL,
			capability TEXT NOT NULL,
			record_type TEXT NOT NULL DEFAULT '*',
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)
	`,
		`
		CREATE TABLE IF NOT EXISTS vault_audit_events (
			id TEXT PRIMARY KEY,
			vault_id TEXT NOT NULL,
			workspace_id TEXT DEFAULT '',
			actor_type TEXT DEFAULT '',
			actor_id TEXT DEFAULT '',
			action TEXT NOT NULL,
			record_id TEXT DEFAULT '',
			record_type TEXT DEFAULT '',
			outcome TEXT NOT NULL,
			details TEXT DEFAULT '',
			created_at DATETIME NOT NULL
		)
	`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_vault_grants_scope ON vault_grants(vault_id, workspace_id, actor_type, actor_id, capability, record_type)`,
		`CREATE INDEX IF NOT EXISTS idx_vault_grants_vault_id ON vault_grants(vault_id)`,
		`CREATE INDEX IF NOT EXISTS idx_vault_records_vault_id ON vault_records(vault_id)`,
		`CREATE INDEX IF NOT EXISTS idx_vault_records_workspace_id ON vault_records(workspace_id)`,
		`CREATE INDEX IF NOT EXISTS idx_vault_records_type ON vault_records(type)`,
		`CREATE INDEX IF NOT EXISTS idx_vault_records_updated_at ON vault_records(updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_vault_folders_vault_id ON vault_folders(vault_id)`,
		`CREATE INDEX IF NOT EXISTS idx_vault_folders_created_at ON vault_folders(created_at ASC)`,
		`CREATE INDEX IF NOT EXISTS idx_vault_record_attachments_vault_id ON vault_record_attachments(vault_id)`,
		`CREATE INDEX IF NOT EXISTS idx_vault_record_attachments_record_id ON vault_record_attachments(record_id)`,
		`CREATE INDEX IF NOT EXISTS idx_vault_record_attachments_record_created_at ON vault_record_attachments(record_id, created_at ASC)`,
		`CREATE INDEX IF NOT EXISTS idx_vault_audit_vault_created_at ON vault_audit_events(vault_id, created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_vault_audit_workspace_created_at ON vault_audit_events(workspace_id, created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_vault_audit_actor_created_at ON vault_audit_events(actor_type, actor_id, created_at DESC)`,
	}
}

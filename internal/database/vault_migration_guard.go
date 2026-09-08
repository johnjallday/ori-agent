package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// ErrRetainedVaultMigration blocks startup before a migration can drop the only
// copy of legacy vault content or its encryption material. Recovery must use a
// compatible reader/exporter, never removal of the database or its WAL files.
var ErrRetainedVaultMigration = errors.New("legacy vault content or encryption material requires recovery: preserve this database and its WAL files, and recover/export the vault with a compatible Ori version before upgrading or resetting")

// guardRetainedVaultFile probes existing installations through SQLite mode=ro.
// Opening a writable connection just to refuse migration could checkpoint a
// crash-left WAL implicitly when its last connection closes.
func guardRetainedVaultFile(ctx context.Context, path string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return fmt.Errorf("inspect database location: %w", err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve database location: %w", err)
	}
	uriPath := filepath.ToSlash(abs)
	if !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	query := url.Values{"mode": {"ro"}, "_pragma": {"busy_timeout(5000)", "query_only(1)"}}
	uri := url.URL{Scheme: "file", Path: uriPath, RawQuery: query.Encode()}
	probe, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return fmt.Errorf("open read-only vault inspection: %w", err)
	}
	probe.SetMaxOpenConns(1)
	inspectErr := guardRetainedVaultMigration(ctx, probe)
	closeErr := probe.Close()
	if inspectErr != nil {
		return inspectErr
	}
	if closeErr != nil {
		return fmt.Errorf("close read-only vault inspection: %w", closeErr)
	}
	return nil
}

// guardRetainedVaultMigration is deliberately smaller than reset preview. It
// runs before pragmas/migrations on every Open, including callers that bypass
// the server builder. Only fixed SQLite metadata/EXISTS queries are used; no
// crypto values are loaded, logged, decrypted or rewritten.
func guardRetainedVaultMigration(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return fmt.Errorf("inspect retained vault migration safety: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, table := range []string{"vault_records", "vault_record_attachments", "vault_folders", "vault_grants", "vault_audit_events"} {
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = ?)`, table).Scan(&exists); err != nil {
			return fmt.Errorf("inspect legacy vault tables: %w", err)
		}
		if !exists {
			continue
		}
		var containsData bool
		// table is from the compiled legacy allowlist above, never schema text
		// or client input. EXISTS avoids scanning/loading encrypted records.
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM "`+table+`" LIMIT 1)`).Scan(&containsData); err != nil {
			return fmt.Errorf("inspect legacy vault content presence: %w", err)
		}
		if containsData {
			return ErrRetainedVaultMigration
		}
	}
	rows, err := tx.QueryContext(ctx, `PRAGMA table_info(vaults)`)
	if err != nil {
		return fmt.Errorf("inspect legacy vault key schema: %w", err)
	}
	var predicates []string
	for rows.Next() {
		var cid, notNull, pk int
		var name, kind string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &kind, &notNull, &defaultValue, &pk); err != nil {
			_ = rows.Close()
			return fmt.Errorf("inspect legacy vault key schema: %w", err)
		}
		// Append constant SQL, not the discovered column identifier.
		switch name {
		case "id", "name", "description", "file_path", "created_at", "updated_at":
			// Known catalog-only metadata.
		case "key_salt":
			predicates = append(predicates, `length(COALESCE(key_salt, '')) > 0`)
		case "key_nonce":
			predicates = append(predicates, `length(COALESCE(key_nonce, '')) > 0`)
		case "key_ciphertext":
			predicates = append(predicates, `length(COALESCE(key_ciphertext, '')) > 0`)
		default:
			_ = rows.Close()
			return fmt.Errorf("%w: unclassified vault schema", ErrRetainedVaultMigration)
		}
	}
	readErr := rows.Err()
	closeErr := rows.Close()
	if readErr != nil {
		return fmt.Errorf("inspect legacy vault key schema: %w", readErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close legacy vault schema inspection: %w", closeErr)
	}
	if len(predicates) != 0 {
		var containsKey bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM vaults WHERE `+strings.Join(predicates, " OR ")+` LIMIT 1)`).Scan(&containsKey); err != nil {
			return fmt.Errorf("inspect legacy vault key presence: %w", err)
		}
		if containsKey {
			return ErrRetainedVaultMigration
		}
	}
	return nil
}

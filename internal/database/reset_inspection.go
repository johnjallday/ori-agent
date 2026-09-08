package database

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// ResetRecordTables is the explicit app-record domain inventory, not an SQL
// identifier supplied by a browser. Return a copy so callers cannot broaden it.
func ResetRecordTables() []string {
	return []string{
		"users", "workspaces", "sessions", "messages", "session_tags", "tool_calls",
		"review_issues", "review_runs", "session_review_status", "session_tasks",
		"scheduled_task_reminders", "smart_input_overrides", "workspace_notes",
		"note_links", "note_tags", "note_headings", "workspace_runs", "workspace_run_trace",
		"workspace_run_artifacts", "home_assistant_intake_traces", "workspace_map_layouts",
		"workspace_map_positions", "workspace_map_group_presentations", "personal_assistant_state",
		"personal_assistant_assignment", "personal_hq_followup", "calendar_meeting_prep",
		"daily_brief_config", "daily_brief_revision", "daily_brief_generation_claim",
		"daily_brief_notification", "workspace_plans", "workspace_plan_versions",
		"workspace_plan_clarifications", "workspace_plan_approvals", "workspace_plan_task_links",
		"workspace_plan_run_links", "workspace_plan_activity", "workspace_plan_draft_snapshots",
		"workspace_plan_execution_slots", "workspace_plan_execution_queue",
		"workspace_plan_execution_generations", "workspace_plan_reconciliations",
		"setup_journey_run", "setup_journey_operation_receipt",
		"setup_journey_declaration_migration_receipt", "setup_journey_review_receipt",
		"sample_library_state", "sample_library_root", "sample_library_entry",
		"sample_library_content_fact", "sample_library_annotation", "sample_library_collection",
		"sample_library_collection_member", "sample_library_child_copy",
		"sample_library_review_receipt", "sample_library_operation_receipt",
		"agent_map_layouts", "agent_map_positions", "vaults",
	}
}

// ResetInspection contains metadata only. It never contains vault key material,
// record contents or a raw SQL/backend error. Nil counts are unavailable, not 0.
type ResetInspection struct {
	Version      int
	SchemaDigest string
	Counts       map[string]*int64
	VaultPaths   []string
	Problems     []string
}

// InspectReset reads the supplied existing connection without constructors,
// migrations, checkpoints or writes. The pre-start caller must open a raw
// read-only connection; normal database.Open has already erased legacy evidence.
func InspectReset(ctx context.Context, db *sql.DB) (ResetInspection, error) {
	result := ResetInspection{Counts: make(map[string]*int64), VaultPaths: []string{}, Problems: []string{}}
	for _, name := range ResetRecordTables() {
		result.Counts[name] = nil
	}
	if db == nil {
		return result, errors.New("database inspection unavailable")
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return result, errors.New("database inspection unavailable")
	}
	defer func() { _ = tx.Rollback() }()

	tables, digest, err := resetSchema(ctx, tx)
	if err != nil {
		return result, err
	}
	result.SchemaDigest = digest
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&result.Version); err != nil {
		result.Problems = append(result.Problems, "schema_unavailable")
	} else if result.Version != schemaVersion {
		result.Problems = append(result.Problems, "unsupported_schema")
	}
	known := map[string]bool{"schema_migrations": true, "sqlite_sequence": true, "sqlite_stat1": true, "sqlite_stat4": true}
	for _, name := range ResetRecordTables() {
		known[name] = true
		if !tables[name] {
			result.Problems = append(result.Problems, "missing_database_domain")
			continue
		}
		var count int64
		// name comes exclusively from ResetRecordTables, never discovered SQL
		// text, browser input or a persisted apply journal.
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM "`+name+`"`).Scan(&count); err != nil {
			result.Problems = append(result.Problems, "database_count_unavailable")
		} else {
			result.Counts[name] = &count
		}
	}
	for _, name := range []string{"sessions_fts", "workspace_notes_fts", "note_headings_fts"} {
		known[name] = true
		for _, suffix := range []string{"_data", "_idx", "_content", "_docsize", "_config"} {
			known[name+suffix] = true
		}
	}
	for name := range tables {
		if !known[name] {
			result.Problems = append(result.Problems, "unclassified_database_domain")
		}
	}
	// Refuse legacy layouts even if a normal catalog listing would hide them.
	// No attempt is made to inspect/decrypt the content or migrate it here.
	if tables["vault_records"] || tables["vault_grants"] || tables["vault_record_attachments"] || tables["vault_folders"] || tables["vault_audit_events"] {
		result.Problems = append(result.Problems, "legacy_vault_schema")
	}
	if tables["vaults"] {
		if err := resetVaultPaths(ctx, tx, &result); err != nil {
			result.Problems = append(result.Problems, "vault_catalog_unavailable")
		}
	} else {
		result.Problems = append(result.Problems, "vault_catalog_unavailable")
	}
	return result, nil // rollback ends a read-only snapshot; no commit is needed
}

func resetSchema(ctx context.Context, tx *sql.Tx) (map[string]bool, string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT type, substr(name, 1, 257), substr(COALESCE(sql, ''), 1, 65537) FROM sqlite_master ORDER BY type, name LIMIT 257`)
	if err != nil {
		return nil, "", errors.New("database schema unavailable")
	}
	defer func() { _ = rows.Close() }()
	tables := make(map[string]bool)
	hash := sha256.New()
	count, size := 0, 0
	for rows.Next() {
		var kind, name, statement string
		if err := rows.Scan(&kind, &name, &statement); err != nil {
			return nil, "", errors.New("database schema unavailable")
		}
		count++
		size += len(statement) + len(name)
		if count > 256 || len(name) > 256 || size > 256*1024 || len(statement) > 65536 {
			return nil, "", errors.New("database schema exceeds inspection limits")
		}
		if kind == "table" {
			tables[name] = true
		}
		if _, err := fmt.Fprintf(hash, "%d:%s%d:%s%d:%s", len(kind), kind, len(name), name, len(statement), statement); err != nil {
			return nil, "", err
		}
	}
	if rows.Err() != nil {
		return nil, "", errors.New("database schema unavailable")
	}
	return tables, hex.EncodeToString(hash.Sum(nil)), nil
}

func resetVaultPaths(ctx context.Context, tx *sql.Tx, result *ResetInspection) error {
	columns, err := tx.QueryContext(ctx, `PRAGMA table_info(vaults)`)
	if err != nil {
		return err
	}
	filePath := false
	for columns.Next() {
		var cid, notNull, pk int
		var name, kind string
		var defaultValue any
		if err := columns.Scan(&cid, &name, &kind, &notNull, &defaultValue, &pk); err != nil {
			_ = columns.Close()
			return err
		}
		filePath = filePath || name == "file_path"
		switch name {
		case "id", "name", "description", "file_path", "created_at", "updated_at":
			// Current catalog-only schema. Anything else might hold the only
			// copy of retained content or encryption material: fail closed.
		case "key_ciphertext", "key_salt", "key_nonce":
			result.Problems = append(result.Problems, "legacy_vault_schema")
		default:
			result.Problems = append(result.Problems, "unclassified_vault_column")
		}
	}
	err = columns.Err()
	closeErr := columns.Close()
	if err != nil || closeErr != nil || !filePath {
		return errors.New("vault catalog unavailable")
	}
	rows, err := tx.QueryContext(ctx, `SELECT substr(COALESCE(file_path, ''), 1, 4097) FROM vaults ORDER BY id LIMIT 129`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return err
		}
		if len(result.VaultPaths) >= 128 || len(path) > 4096 || strings.TrimSpace(path) == "" {
			return errors.New("vault paths unavailable or exceed inspection limits")
		}
		result.VaultPaths = append(result.VaultPaths, path)
	}
	return rows.Err()
}

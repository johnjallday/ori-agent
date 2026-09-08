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

// InspectResetFile opens an existing database read-only without migrations or
// writable pragmas. Closing this probe cannot checkpoint a crash-left WAL.
func InspectResetFile(ctx context.Context, path string) (ResetInspection, error) {
	var empty ResetInspection
	if _, err := os.Stat(path); err != nil {
		return empty, fmt.Errorf("inspect reset database: %w", err)
	}
	probe, err := sql.Open("sqlite", resetSQLiteURI(path, "ro", true))
	if err != nil {
		return empty, errors.New("open reset database inspection")
	}
	probe.SetMaxOpenConns(1)
	result, inspectErr := InspectReset(ctx, probe)
	closeErr := probe.Close()
	if inspectErr != nil {
		return result, inspectErr
	}
	if closeErr != nil {
		return result, errors.New("close reset database inspection")
	}
	return result, nil
}

// ResetAppRecords deletes only the fixed shared-database domains returned by
// ResetRecordTables. It preserves the database schema and files, commits the
// domain deletion atomically, checkpoints its own WAL, and verifies every named
// domain before returning success.
func ResetAppRecords(ctx context.Context, path string) (map[string]int64, error) {
	before, err := InspectResetFile(ctx, path)
	if err != nil {
		return nil, err
	}
	if len(before.Problems) != 0 {
		return nil, errors.New("database reset preflight found unsupported domains")
	}

	db, err := sql.Open("sqlite", resetSQLiteURI(path, "rw", false))
	if err != nil {
		return nil, errors.New("open reset database")
	}
	db.SetMaxOpenConns(1)
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		return nil, errors.New("prepare reset database")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, errors.New("begin database reset")
	}
	deleted := make(map[string]int64, len(ResetRecordTables()))
	tables := ResetRecordTables()
	for i := len(tables) - 1; i >= 0; i-- {
		name := tables[i]
		// #nosec G202 -- name comes only from the compiled ResetRecordTables allowlist.
		result, execErr := tx.ExecContext(ctx, `DELETE FROM "`+name+`"`)
		if execErr != nil {
			_ = tx.Rollback()
			return nil, fmt.Errorf("clear database domain %s", name)
		}
		count, rowsErr := result.RowsAffected()
		if rowsErr != nil {
			_ = tx.Rollback()
			return nil, fmt.Errorf("count cleared database domain %s", name)
		}
		deleted[name] = count
	}
	if err := tx.Commit(); err != nil {
		return nil, errors.New("commit database reset")
	}
	if _, err := db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		return nil, errors.New("checkpoint database reset")
	}
	if err := db.Close(); err != nil {
		return nil, errors.New("close reset database")
	}

	after, err := InspectResetFile(ctx, path)
	if err != nil {
		return nil, err
	}
	if len(after.Problems) != 0 {
		return nil, errors.New("database reset verification found unsupported domains")
	}
	for _, name := range ResetRecordTables() {
		if after.Counts[name] == nil || *after.Counts[name] != 0 {
			return nil, fmt.Errorf("database domain %s did not verify empty", name)
		}
	}
	return deleted, nil
}

func resetSQLiteURI(path, mode string, queryOnly bool) string {
	absolute, err := filepath.Abs(path)
	if err != nil {
		absolute = path
	}
	uriPath := filepath.ToSlash(absolute)
	if !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	pragmas := []string{"busy_timeout(5000)"}
	if queryOnly {
		pragmas = append(pragmas, "query_only(1)")
	}
	query := url.Values{"mode": {mode}, "_pragma": pragmas}
	return (&url.URL{Scheme: "file", Path: uriPath, RawQuery: query.Encode()}).String()
}

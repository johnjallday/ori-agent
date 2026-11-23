package database

import (
	"context"
	"database/sql"
)

// Device-local search sources never belong to a workspace or its export.
// Index rows have no file content, chat association or ambient tool authority.
func (db *DB) migration093FilenameSearch(ctx context.Context) error {
	return db.InTransaction(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
 CREATE TABLE IF NOT EXISTS filename_sources (
 id TEXT PRIMARY KEY, owner TEXT NOT NULL, root TEXT NOT NULL, witness TEXT NOT NULL,
 generation INTEGER NOT NULL CHECK(generation>0), state TEXT NOT NULL,
 reason TEXT NOT NULL DEFAULT '', mapped_at INTEGER NOT NULL DEFAULT 0,
 UNIQUE(owner,witness));
 CREATE TABLE IF NOT EXISTS filename_entries (
 id TEXT PRIMARY KEY, source TEXT NOT NULL REFERENCES filename_sources(id) ON DELETE CASCADE,
 generation INTEGER NOT NULL, relative TEXT NOT NULL, basename TEXT NOT NULL, folded TEXT NOT NULL,
 modified INTEGER NOT NULL, witness TEXT NOT NULL, ancestors TEXT NOT NULL,
 UNIQUE(source,generation,relative));`)
		return err
	})
}

package database

import "context"

// migration090WorkspaceFolderReports records where a folder agent's report was
// posted in the workspace Commander's conversation (workspace chat sync), and
// whether the owner has seen it. Local delivery bookkeeping, like the welcome
// receipts: no message or session FK, so deleting history never lends an old
// receipt to a new thread, and nothing is backfilled.
func (db *DB) migration090WorkspaceFolderReports(ctx context.Context) error {
	_, err := db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS workspace_folder_reports (
 workspace_id TEXT NOT NULL,
 report_key TEXT NOT NULL CHECK(length(report_key) BETWEEN 1 AND 200),
 session_id TEXT NOT NULL,
 message_id TEXT NOT NULL UNIQUE,
 delivered_at DATETIME NOT NULL,
 seen_at DATETIME,
 PRIMARY KEY (workspace_id, report_key)
);
CREATE INDEX IF NOT EXISTS idx_workspace_folder_reports_latest
 ON workspace_folder_reports(workspace_id, delivered_at);`)
	return err
}

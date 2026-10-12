package database

import "context"

// Assistant operations are device-local app records: one bounded row per
// operation the hired assistant prepared, written by compare-and-swap on
// revision. They are not portable history. Reset clears them, deleting the
// owning conversation removes them, and the byte bounds here repeat the ones
// internal/assistantops enforces before it stores anything.
func (db *DB) migration080AssistantOperations(ctx context.Context) error {
	_, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS assistant_operations (
			id TEXT PRIMARY KEY,
			contract_version INTEGER NOT NULL CHECK(contract_version > 0),
			kind TEXT NOT NULL CHECK(kind IN ('navigation','task')),
			status TEXT NOT NULL CHECK(status IN ('requested','prepared','reviewed','start_requested','started','unresolved','cancelled','expired','invalidated')),
			revision INTEGER NOT NULL CHECK(revision > 0),
			user_id TEXT NOT NULL,
			assistant_id TEXT NOT NULL,
			state_version INTEGER NOT NULL CHECK(state_version > 0),
			hq_workspace_id TEXT NOT NULL,
			profile_name TEXT NOT NULL,
			conversation_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
			turn_message_id TEXT NOT NULL DEFAULT '',
			tab_id TEXT NOT NULL DEFAULT '' CHECK(length(tab_id) <= 64),
			watch INTEGER NOT NULL DEFAULT 0 CHECK(watch IN (0,1)),
			workspace_id TEXT NOT NULL,
			context_json TEXT NOT NULL CHECK(json_valid(context_json) AND length(CAST(context_json AS BLOB)) <= 4000),
			commands_json TEXT NOT NULL CHECK(json_valid(commands_json) AND length(CAST(commands_json AS BLOB)) <= 16000),
			draft_json TEXT CHECK(draft_json IS NULL OR (json_valid(draft_json) AND length(CAST(draft_json AS BLOB)) <= 24000)),
			task_id TEXT NOT NULL DEFAULT '',
			run_id TEXT NOT NULL DEFAULT '',
			claimed_at_ms INTEGER NOT NULL DEFAULT 0,
			receipts_json TEXT NOT NULL CHECK(json_valid(receipts_json) AND length(CAST(receipts_json AS BLOB)) <= 16000),
			created_at_ms INTEGER NOT NULL,
			updated_at_ms INTEGER NOT NULL,
			expires_at_ms INTEGER NOT NULL CHECK(expires_at_ms > created_at_ms)
		);
		CREATE INDEX IF NOT EXISTS idx_assistant_operations_owner ON assistant_operations(user_id, updated_at_ms);
		CREATE INDEX IF NOT EXISTS idx_assistant_operations_expiry ON assistant_operations(expires_at_ms);
		CREATE UNIQUE INDEX IF NOT EXISTS idx_assistant_operations_task ON assistant_operations(task_id) WHERE task_id <> '';
	`)
	return err
}

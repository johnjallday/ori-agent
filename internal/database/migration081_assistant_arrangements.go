package database

import "context"

// Device-local inert drafts belong to their canonical conversation/answer, not
// portable message JSON. Older databases gain an empty table, never a plan or
// grant. Context/byte/count checks are additionally enforced by the atomic writer.
func (db *DB) migration081AssistantArrangements(ctx context.Context) error {
	_, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS assistant_arrangements (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
			message_id TEXT NOT NULL UNIQUE REFERENCES messages(id) ON DELETE CASCADE,
			user_id TEXT NOT NULL,
			workspace_id TEXT NOT NULL,
			agent_name TEXT NOT NULL,
			state_version INTEGER NOT NULL CHECK(state_version > 0),
			context_revision TEXT NOT NULL,
			folder_revision TEXT NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
			revision INTEGER NOT NULL CHECK(revision > 0),
			state TEXT NOT NULL CHECK(state IN ('draft','needs_more_evidence','stale','cancelled')),
			record_json TEXT NOT NULL CHECK(json_valid(record_json) AND length(CAST(record_json AS BLOB)) <= 1048576),
			CHECK(json_extract(record_json,'$.version') IS 1),
			CHECK(json_extract(record_json,'$.id') IS id),
			CHECK(json_extract(record_json,'$.revision') IS revision),
			CHECK(json_extract(record_json,'$.state') IS state)
		);
		CREATE INDEX IF NOT EXISTS idx_assistant_arrangements_session ON assistant_arrangements(session_id);
	`)
	return err
}

package database

import "context"

// Attribution is bounded historical data in canonical messages. General and
// imported message writers leave it NULL; source bodies are never stored here.
func (db *DB) migration077AssistantTurnAttribution(ctx context.Context) error {
	var exists, column int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='messages'`).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return nil
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('messages') WHERE name='turn_context_json'`).Scan(&column); err != nil {
		return err
	}
	if column == 0 {
		_, err := db.ExecContext(ctx, `ALTER TABLE messages ADD COLUMN turn_context_json TEXT CHECK (turn_context_json IS NULL OR (length(CAST(turn_context_json AS BLOB)) <= 32000 AND role IN ('user','assistant') AND json_valid(turn_context_json)))`)
		return err
	}
	return nil
}

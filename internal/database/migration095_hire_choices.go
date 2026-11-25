package database

import (
	"context"
	"database/sql"
)

// SQLite cannot extend a CHECK constraint in place. Preserve proposal identity,
// including historical declined/adjusted cards, while adding the pre-effect states.
func (db *DB) migration095HireChoices(ctx context.Context) error {
	return db.InTransaction(ctx, func(tx *sql.Tx) error {
		for _, q := range []string{
			`CREATE TABLE assistant_hire_choices_v95 (
    id TEXT PRIMARY KEY, user_id TEXT NOT NULL, conversation_id TEXT NOT NULL,
    message_id TEXT NOT NULL DEFAULT '', role TEXT NOT NULL, name TEXT NOT NULL,
    domain TEXT NOT NULL DEFAULT '', brief TEXT NOT NULL, workspace_name TEXT NOT NULL,
    blueprint TEXT NOT NULL, blueprint_label TEXT NOT NULL, open_roles INTEGER NOT NULL DEFAULT 0,
    state TEXT NOT NULL CHECK(state IN ('offered','declined','adjusted','hiring','hired','workspace_draft','workspace_created')),
    hire_id TEXT NOT NULL DEFAULT '', agent_name TEXT NOT NULL DEFAULT '', workspace_id TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL)`,
			`INSERT INTO assistant_hire_choices_v95 SELECT id,user_id,conversation_id,message_id,role,name,domain,brief,workspace_name,blueprint,blueprint_label,open_roles,state,hire_id,agent_name,workspace_id,created_at,updated_at FROM assistant_hire_proposals`,
			`DROP TABLE assistant_hire_proposals`, `ALTER TABLE assistant_hire_choices_v95 RENAME TO assistant_hire_proposals`,
			`CREATE INDEX idx_assistant_hire_proposals_conversation ON assistant_hire_proposals(user_id,conversation_id,created_at)`,
		} {
			if _, err := tx.ExecContext(ctx, q); err != nil {
				return err
			}
		}
		return nil
	})
}

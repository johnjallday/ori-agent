package database

import (
	"context"
	"fmt"
)

// migration063PreferenceRevisions records only monotonic per-field generations,
// not preference values. A SQL trigger observes all userprofile write paths,
// including legacy whole-profile PUT and updates from another store instance.
// An unrelated field edit cannot invalidate a reviewed preference, while
// clearing and re-entering identical wording advances its generation twice.
func (db *DB) migration063PreferenceRevisions(ctx context.Context) error {
	// Legacy migration tests intentionally reconstruct only their target tables.
	// A database without the baseline users table has no profile to version;
	// profile reads fail closed rather than inventing a review generation.
	var usersTable int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'users'`).Scan(&usersTable); err != nil {
		return fmt.Errorf("inspect profile schema for revisions: %w", err)
	}
	if usersTable == 0 {
		return nil
	}
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS user_preference_revisions (
			user_id TEXT NOT NULL,
			field TEXT NOT NULL CHECK (field IN ('response_style', 'units', 'language')),
			revision INTEGER NOT NULL CHECK (revision > 0),
			PRIMARY KEY (user_id, field),
			FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
		)`,
		`CREATE TRIGGER IF NOT EXISTS user_preference_revisions_insert
		AFTER INSERT ON users BEGIN
			INSERT INTO user_preference_revisions (user_id, field, revision) VALUES
				(NEW.id, 'response_style', 1), (NEW.id, 'units', 1), (NEW.id, 'language', 1);
		END`,
		`CREATE TRIGGER IF NOT EXISTS user_preference_revisions_update
		AFTER UPDATE OF preferences ON users BEGIN
			UPDATE user_preference_revisions SET revision = revision + 1
			WHERE user_id = NEW.id AND field = 'response_style' AND
				(CASE WHEN json_valid(OLD.preferences) THEN json_extract(OLD.preferences, '$.response_style') END)
				IS NOT (CASE WHEN json_valid(NEW.preferences) THEN json_extract(NEW.preferences, '$.response_style') END);
			UPDATE user_preference_revisions SET revision = revision + 1
			WHERE user_id = NEW.id AND field = 'units' AND
				(CASE WHEN json_valid(OLD.preferences) THEN json_extract(OLD.preferences, '$.units') END)
				IS NOT (CASE WHEN json_valid(NEW.preferences) THEN json_extract(NEW.preferences, '$.units') END);
			UPDATE user_preference_revisions SET revision = revision + 1
			WHERE user_id = NEW.id AND field = 'language' AND
				(CASE WHEN json_valid(OLD.preferences) THEN json_extract(OLD.preferences, '$.language') END)
				IS NOT (CASE WHEN json_valid(NEW.preferences) THEN json_extract(NEW.preferences, '$.language') END);
		END`,
		`INSERT OR IGNORE INTO user_preference_revisions (user_id, field, revision)
		SELECT id, 'response_style', 1 FROM users`,
		`INSERT OR IGNORE INTO user_preference_revisions (user_id, field, revision)
		SELECT id, 'units', 1 FROM users`,
		`INSERT OR IGNORE INTO user_preference_revisions (user_id, field, revision)
		SELECT id, 'language', 1 FROM users`,
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("preference revision migration: %w", err)
		}
	}
	return nil
}

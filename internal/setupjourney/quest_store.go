package setupjourney

import (
	"context"
	"database/sql"
	"fmt"
)

// FindQuestRoot reuses the exact user/quest root. Roots created under the
// retired assistant-slug identity are never adopted (they stay unreachable);
// it never rewrites identity, copies receipts, chooses among ambiguous roots,
// or grants access.
func (s *SQLiteStore) FindQuestRoot(ctx context.Context, userID string, key QuestKey) (*Run, error) {
	if err := s.configured(); err != nil {
		return nil, err
	}
	key = normalizeQuestKey(key)
	if !validateCanonicalRef(userID, false) || !validQuestKey(key) {
		return nil, ErrInvalid
	}
	var rows *sql.Rows
	var err error
	switch key.Source {
	case QuestSourceUserTemplate:
		rows, err = s.db.QueryContext(ctx, `SELECT `+runColumns+` FROM setup_journey_run
			WHERE run_kind = 'root' AND owner_user_id = ? AND journey_id = ?
			  AND relationship_id = ? AND specialist_slug = 'user_template_quest'
			ORDER BY created_at LIMIT 2`, userID, key.ID, questRelationshipID(key))
	case QuestSourceHost:
		// A host root is only ever its exact relationship + slug; it never adopts
		// a plugin, user-template, or assistant-owned root.
		rows, err = s.db.QueryContext(ctx, `SELECT `+runColumns+` FROM setup_journey_run
			WHERE run_kind = 'root' AND owner_user_id = ? AND journey_id = ?
			  AND relationship_id = ? AND specialist_slug = 'host_quest'
			ORDER BY created_at LIMIT 2`, userID, key.ID, questRelationshipID(key))
	case QuestSourcePlugin:
		rows, err = s.db.QueryContext(ctx, `SELECT `+runColumns+` FROM setup_journey_run
			WHERE run_kind = 'root' AND owner_user_id = ? AND journey_id = ?
			  AND relationship_id = ? AND specialist_slug = 'plugin_quest'
			ORDER BY created_at LIMIT 2`, userID, key.ID, questRelationshipID(key))
	default:
		return nil, ErrInvalid
	}
	if err != nil {
		return nil, fmt.Errorf("setup quest: find root: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var found *Run
	for rows.Next() {
		if found != nil {
			return nil, ErrConflict
		}
		found, err = scanRun(rows)
		if err != nil {
			return nil, err
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if found == nil {
		return nil, ErrNotFound
	}
	return found, nil
}

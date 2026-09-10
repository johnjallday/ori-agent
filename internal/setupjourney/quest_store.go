package setupjourney

import (
	"context"
	"database/sql"
	"fmt"
)

// FindQuestRoot reuses an exact user/quest root, including one historical
// assistant-owned root for the compiled legacy specialist. It never rewrites
// identity, copies receipts, chooses among ambiguous roots, or grants access.
func (s *SQLiteStore) FindQuestRoot(ctx context.Context, userID string, key QuestKey, legacySlug string) (*Run, error) {
	if err := s.configured(); err != nil {
		return nil, err
	}
	key = normalizeQuestKey(key)
	if !validateCanonicalRef(userID, false) || !validQuestKey(key) || (legacySlug != "" && !validateStableID(legacySlug)) {
		return nil, ErrInvalid
	}
	var rows *sql.Rows
	var err error
	if key.Source == QuestSourceUserTemplate {
		rows, err = s.db.QueryContext(ctx, `SELECT `+runColumns+` FROM setup_journey_run
			WHERE run_kind = 'root' AND owner_user_id = ? AND journey_id = ?
			  AND relationship_id = ? AND specialist_slug = 'user_template_quest'
			ORDER BY created_at LIMIT 2`, userID, key.ID, questRelationshipID(key))
	} else {
		rows, err = s.db.QueryContext(ctx, `SELECT `+runColumns+` FROM setup_journey_run
			WHERE run_kind = 'root' AND owner_user_id = ? AND journey_id = ? AND (
				(relationship_id = ? AND specialist_slug = 'plugin_quest') OR
				(? != '' AND specialist_slug = ? AND (integration_plugin_id = '' OR integration_plugin_id = ?))
			) ORDER BY created_at LIMIT 2`, userID, key.ID, questRelationshipID(key), legacySlug, legacySlug, key.PluginID)
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

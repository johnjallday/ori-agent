package session

import (
	"context"
	"encoding/json"
	"strings"
	"unicode"
	"unicode/utf8"
)

const MaxWorkspaceUpdatePage = 100

// WorkspaceUpdateTarget is primary-store navigation metadata, not a work or
// conversation snapshot. The aggregate never loads the whole workspace tree.
type WorkspaceUpdateTarget struct {
	ID, Name, FolderSlug string
	Version              int64
}

func ValidWorkspaceUpdatePage(after string, limit int) bool {
	return limit >= 1 && limit <= MaxWorkspaceUpdatePage && len(after) <= 128 &&
		utf8.ValidString(after) && strings.TrimSpace(after) == after && strings.IndexFunc(after, unicode.IsControl) < 0
}

// ListWorkspaceUpdateIDs uses an owner-scoped identity keyset, not mutable
// display names or offsets. IDs order navigation only, never delivery or seen
// state. One extra ID reports continuation without projecting an extra source.
func (h *hybridStore) ListWorkspaceUpdateIDs(ctx context.Context, owner, after string, limit int) ([]string, error) {
	if owner == "" || !ValidWorkspaceUpdatePage(after, limit) {
		return nil, ErrInvalidID
	}
	rows, err := h.sqlite.db.QueryContext(ctx, `SELECT id FROM workspaces
		WHERE COALESCE(NULLIF(TRIM(owner_user_id), ''), 'local')=? AND id>?
		AND COALESCE(status, '') NOT IN ('trashed','missing') ORDER BY id LIMIT ?`, owner, after, limit+1)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	ids := make([]string, 0, limit+1)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ReadWorkspaceUpdateTargets rechecks the exact bounded set after source reads.
// A transfer/trash during another workspace's read removes the old authority.
// Even malformed orchestration JSON cannot prevent an honest unavailable row.
func (h *hybridStore) ReadWorkspaceUpdateTargets(ctx context.Context, owner string, ids []string) ([]WorkspaceUpdateTarget, error) {
	if owner == "" || len(ids) > MaxWorkspaceUpdatePage {
		return nil, ErrInvalidID
	}
	out := []WorkspaceUpdateTarget{}
	if len(ids) == 0 {
		return out, nil
	}
	encoded, err := json.Marshal(ids)
	if err != nil {
		return nil, err
	}
	rows, err := h.sqlite.db.QueryContext(ctx, `SELECT id, name, COALESCE(folder_slug, ''), version FROM workspaces
		WHERE COALESCE(NULLIF(TRIM(owner_user_id), ''), 'local')=?
		AND COALESCE(status, '') NOT IN ('trashed','missing')
		AND id IN (SELECT value FROM json_each(?)) ORDER BY id`, owner, string(encoded))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var target WorkspaceUpdateTarget
		if err := rows.Scan(&target.ID, &target.Name, &target.FolderSlug, &target.Version); err != nil {
			return nil, err
		}
		out = append(out, target)
	}
	return out, rows.Err()
}

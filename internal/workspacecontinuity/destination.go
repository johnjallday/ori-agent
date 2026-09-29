package workspacecontinuity

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// DestinationDigest fingerprints only destination SQL identity and topology in
// one read view. The coordinator must additionally validate local folder slug,
// profile/appearance, reset and runtime state before importing. This digest is
// not a credential, consent token, or permission to adopt an HQ. Read it again
// in the receipt's transaction; a separate preview read is never a write CAS.
func DestinationDigest(ctx context.Context, q Queryer, userID string) (string, error) {
	if q == nil || !ValidID(userID) {
		return "", ErrInvalid
	}
	type workspaceRow struct {
		ID, Owner, Parent, Slug, Status, Deleted string
		Version                                  int64
	}
	type attachmentRow struct {
		ID, State, Disposition string
		Version                int64
	}
	state := struct {
		UserID      string
		HQ          string
		Assistant   []any
		Workspaces  []workspaceRow
		Attachments []attachmentRow
	}{UserID: userID, Assistant: []any{}, Workspaces: []workspaceRow{}, Attachments: []attachmentRow{}}
	if err := q.QueryRowContext(ctx, `SELECT COALESCE(personal_workspace_id,'') FROM users WHERE id=?`, userID).Scan(&state.HQ); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrConflict
		}
		return "", err
	}
	var assistantID, status, hq, entry, profile string
	var version int64
	err := q.QueryRowContext(ctx, `SELECT assistant_id,status,hq_workspace_id,hq_entry_agent_instance_id,global_agent_profile_name,state_version
		FROM personal_assistant_state WHERE user_id=?`, userID).Scan(&assistantID, &status, &hq, &entry, &profile, &version)
	if err == nil {
		state.Assistant = []any{assistantID, status, hq, entry, profile, version}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	rows, err := q.QueryContext(ctx, `SELECT id,owner_user_id,COALESCE(parent_id,''),COALESCE(folder_slug,''),status,COALESCE(CAST(deleted_at AS TEXT),''),version
		FROM workspaces ORDER BY id LIMIT ?`, MaxFiles+1)
	if err != nil {
		return "", err
	}
	for rows.Next() {
		if len(state.Workspaces) == MaxFiles {
			_ = rows.Close()
			return "", ErrLimit
		}
		var item workspaceRow
		if err := rows.Scan(&item.ID, &item.Owner, &item.Parent, &item.Slug, &item.Status, &item.Deleted, &item.Version); err != nil {
			_ = rows.Close()
			return "", err
		}
		state.Workspaces = append(state.Workspaces, item)
	}
	err = rows.Err()
	closeErr := rows.Close()
	if err != nil {
		return "", err
	}
	if closeErr != nil {
		return "", closeErr
	}
	rows, err = q.QueryContext(ctx, `SELECT workspace_id,state,disposition,version FROM continuity_attachments ORDER BY workspace_id LIMIT ?`, MaxFiles+1)
	if err != nil {
		return "", err
	}
	for rows.Next() {
		if len(state.Attachments) == MaxFiles {
			_ = rows.Close()
			return "", ErrLimit
		}
		var item attachmentRow
		if err := rows.Scan(&item.ID, &item.State, &item.Disposition, &item.Version); err != nil {
			_ = rows.Close()
			return "", err
		}
		state.Attachments = append(state.Attachments, item)
	}
	err = rows.Err()
	closeErr = rows.Close()
	if err != nil {
		return "", err
	}
	if closeErr != nil {
		return "", closeErr
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	data, err := json.Marshal(state)
	if err != nil {
		return "", ErrInvalid
	}
	return Digest(data), nil
}

package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

// AgentReferenceReader includes active, archived and restorable workspace rows.
// It reads only attachment/entry/binding metadata, never messages or Trash.
// Absence of this optional capability is unknown, not proof of no references.
type AgentReferenceReader interface {
	HasWorkspaceAgentReference(context.Context, string, string) (bool, error)
}

func (a *WorkspaceStoreAdapter) HasWorkspaceAgentReference(ctx context.Context, name, excludingID string) (bool, error) {
	if reader, ok := a.store.(AgentReferenceReader); ok {
		return reader.HasWorkspaceAgentReference(ctx, name, excludingID)
	}
	return false, errors.New("workspace reference verification is unavailable")
}

func (h *hybridStore) HasWorkspaceAgentReference(ctx context.Context, name, excludingID string) (bool, error) {
	return h.sqlite.HasWorkspaceAgentReference(ctx, name, excludingID)
}

func (s *SQLiteStore) HasWorkspaceAgentReference(ctx context.Context, name, excludingID string) (bool, error) {
	if strings.TrimSpace(name) == "" {
		return false, errors.New("agent reference name is empty")
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT agent_instances, json_extract(shared_data, '$.entry_agent_name'),
			json_extract(assistant_program_json, '$.state.home_bindings.bindings'),
			json_extract(assistant_program_json, '$.link.project_bindings.bindings'),
			json_extract(assistant_program_json, '$.state.project_staffing_consent.roles')
		FROM workspaces WHERE id != ?`, excludingID)
	if err != nil {
		return false, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var instances, entry, home, project, consent sql.NullString
		if err := rows.Scan(&instances, &entry, &home, &project, &consent); err != nil {
			return false, err
		}
		if strings.EqualFold(strings.TrimSpace(entry.String), name) {
			return true, nil
		}
		for _, raw := range []sql.NullString{instances, home, project, consent} {
			if !raw.Valid || raw.String == "" {
				continue
			}
			var refs []struct {
				Name        string `json:"name"`
				AgentName   string `json:"agent_name"`
				PendingName string `json:"pending_name"`
			}
			if err := json.Unmarshal([]byte(raw.String), &refs); err != nil {
				return false, errors.New("workspace agent references are unreadable")
			}
			for _, ref := range refs {
				for _, candidate := range []string{ref.Name, ref.AgentName, ref.PendingName} {
					if strings.EqualFold(strings.TrimSpace(candidate), name) {
						return true, nil
					}
				}
			}
		}
	}
	return false, rows.Err()
}

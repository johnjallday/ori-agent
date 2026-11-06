package learningmissions

import "context"

// SourceIncarnation reads a device-local workspace attachment identity, never a
// portable timestamp/ID. Workspace deletion cascades this row so a reimport
// with the same IDs cannot revive old learning authority.
func (s *SQLiteStore) SourceIncarnation(ctx context.Context, owner, workspace string) (string, error) {
	if s == nil || s.db == nil || !validID(owner) || !validID(workspace) {
		return "", ErrUnavailable
	}
	var incarnation string
	err := s.db.QueryRowContext(ctx, `SELECT incarnation FROM learning_sources WHERE owner_id=? AND workspace_id=?`, owner, workspace).Scan(&incarnation)
	if err != nil || !validID(incarnation) {
		return "", ErrUnavailable
	}
	return incarnation, nil
}

func (m *Mutation) CheckSource(d Definition) error {
	var incarnation string
	err := m.tx.QueryRowContext(m.ctx, `SELECT incarnation FROM learning_sources WHERE owner_id=? AND workspace_id=?`, m.owner.ID, d.WorkspaceID).Scan(&incarnation)
	if err != nil || incarnation != d.Incarnation {
		return ErrUnavailable
	}
	return nil
}

func (m *Mutation) BindSource(workspace, incarnation string) error {
	if !validID(workspace) || !validID(incarnation) {
		return ErrInvalid
	}
	if _, err := m.tx.ExecContext(m.ctx, `INSERT INTO learning_sources(owner_id,workspace_id,incarnation) VALUES(?,?,?) ON CONFLICT(owner_id,workspace_id) DO NOTHING`, m.owner.ID, workspace, incarnation); err != nil {
		return ErrUnavailable
	}
	var current string
	if err := m.tx.QueryRowContext(m.ctx, `SELECT incarnation FROM learning_sources WHERE owner_id=? AND workspace_id=?`, m.owner.ID, workspace).Scan(&current); err != nil {
		return ErrUnavailable
	}
	if current != incarnation {
		return ErrConflict
	}
	return nil
}

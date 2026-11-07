package learningmissions

import "errors"

// Retirement is the only write allowed on an unsupported immutable definition.
// It never interprets/re-encodes future fields, starts work or changes evidence.
func (m *Mutation) retireUnsupported(next Instance, expected int64) error {
	prior, err := m.Instance(next.ID)
	if err != nil || !errors.Is(prior.Validate(), ErrUnsupported) || prior.Revision != expected || next.CreatedAt != prior.CreatedAt || next.UpdatedAt < prior.UpdatedAt ||
		next.Definition.Structure != prior.Definition.Structure || next.Definition.Version != prior.Definition.Version || next.Definition.SupportVersion != prior.Definition.SupportVersion {
		return ErrConflict
	}
	dismissal := prior.State == Offered && next.State == Invalidated
	stop := (prior.State == Active || prior.State == Blocked) && next.State == Stopped
	if !dismissal && !stop {
		return ErrUnsupported
	}
	m.storageControl = true
	result, err := m.tx.ExecContext(m.ctx, `UPDATE learning_instances SET revision=?,state=?,updated_at_ms=? WHERE owner_id=? AND id=? AND epoch=? AND revision=?`, next.Revision, next.State, next.UpdatedAt, next.OwnerID, next.ID, next.Epoch, expected)
	if err != nil {
		return ErrUnavailable
	}
	count, err := result.RowsAffected()
	if err != nil || count != 1 {
		return ErrConflict
	}
	return nil
}

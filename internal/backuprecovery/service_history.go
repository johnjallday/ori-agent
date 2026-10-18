package backuprecovery

import "context"

type History struct {
	Items      []Attempt `json:"items"`
	NextCursor uint64    `json:"next_cursor,omitempty"`
}

// GetHistory reads only the installation-owned encrypted journal. A cursor is
// an exclusive sequence boundary, not an offset into mutable filesystem data.
func (s *Service) GetHistory(ctx context.Context, owner, workspace string, cursor uint64) (History, error) {
	release, err := s.enter()
	if err != nil {
		return History{}, err
	}
	defer release()
	if cursor > maxSafeRevision {
		return History{}, ErrInvalidRequest
	}
	record, err := s.record(ctx, owner, workspace)
	if err != nil {
		return History{}, err
	}
	history := History{Items: []Attempt{}}
	if record.state == nil {
		return history, nil
	}
	for _, attempt := range record.state.Attempts {
		if cursor != 0 && attempt.Sequence >= cursor {
			continue
		}
		if len(history.Items) == 25 {
			history.NextCursor = history.Items[len(history.Items)-1].Sequence
			break
		}
		history.Items = append(history.Items, Attempt(attempt))
	}
	return history, nil
}

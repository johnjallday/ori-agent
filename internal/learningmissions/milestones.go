package learningmissions

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

const (
	MilestoneDelay    = 2 * time.Second
	MilestoneLifetime = 5 * time.Minute
)

// Milestone is a content-free, locally earned review event, not a generic
// Ticket/Run event or a caller-authored actor label. Only fresh distinct credit
// can queue it, in the same writer transaction as the consumed decision.
type Milestone struct {
	ID                 string `json:"id"`
	Epoch              int64  `json:"epoch"`
	SuggestionRevision int64  `json:"suggestion_revision"`
	WorkspaceID        string `json:"studio_id"`
	Incarnation        string `json:"incarnation"`
	ReviewID           string `json:"review_id"`
	EvidenceKey        string `json:"evidence_key"`
	ModelFingerprint   string `json:"model_fingerprint"`
	CreatedAt          int64  `json:"created_at_ms"`
	NotBefore          int64  `json:"not_before_ms"`
	ExpiresAt          int64  `json:"expires_at_ms"`
}

func (m Milestone) Validate() error {
	if !validID(m.ID) || m.Epoch < 1 || m.SuggestionRevision < 1 || !validID(m.WorkspaceID) || !validID(m.Incarnation) ||
		!validID(m.ReviewID) || !validDigest(m.EvidenceKey) || !validDigest(m.ModelFingerprint) || m.CreatedAt <= 0 ||
		m.NotBefore != m.CreatedAt+MilestoneDelay.Milliseconds() || m.ExpiresAt != m.CreatedAt+MilestoneLifetime.Milliseconds() {
		return ErrInvalid
	}
	return nil
}

func (m Milestone) allowed(owner Owner) bool {
	p := owner.Preferences
	return m.Validate() == nil && m.Epoch == owner.Epoch && p.Mode == "automatic" && p.SuggestionRevision == m.SuggestionRevision &&
		p.SuggestionSince > 0 && m.CreatedAt >= p.SuggestionSince && p.ModelFingerprint == m.ModelFingerprint && p.AllowsScope(m.WorkspaceID, m.Incarnation)
}

func (m *Mutation) queueMilestone(review Review, evidenceKey string) (string, error) {
	p := m.owner.Preferences
	if evidenceKey == "" || p.Mode != "automatic" || p.SuggestionRevision <= 0 || p.SuggestionSince <= 0 ||
		review.DecidedAt < p.SuggestionSince || !p.AllowsScope(review.Result.WorkspaceID, review.Result.Incarnation) {
		return "", nil
	}
	if review.Validate() != nil || review.Decision == "" || !validDigest(evidenceKey) {
		return "", ErrInvalid
	}
	var occupied int
	if err := m.tx.QueryRowContext(m.ctx, `SELECT COUNT(*) FROM learning_instances WHERE owner_id=? AND state IN ('active','blocked','offered')`, m.owner.ID).Scan(&occupied); err != nil {
		return "", ErrUnavailable
	}
	if occupied > 0 {
		return "", nil
	}
	// Expiry is a mutation-only retirement. It never dispatches missed work.
	if _, err := m.tx.ExecContext(m.ctx, `UPDATE learning_operations SET status='invalidated',record=json_set(record,'$.status','invalidated','$.failure','milestone_expired')
 WHERE owner_id=? AND kind='milestone' AND status='pending' AND created_at_ms<=?`, m.owner.ID, review.DecidedAt-MilestoneLifetime.Milliseconds()); err != nil {
		return "", ErrUnavailable
	}
	var pending string
	err := m.tx.QueryRowContext(m.ctx, `SELECT id FROM learning_operations WHERE owner_id=? AND kind='milestone' AND status='pending' LIMIT 1`, m.owner.ID).Scan(&pending)
	if err == nil {
		return pending, nil
	} // coalesce, never extend the first event's deadline
	if !errors.Is(err, sql.ErrNoRows) {
		return "", ErrUnavailable
	}
	identity, err := encode(struct {
		Owner    string
		Epoch    int64
		Review   string
		Evidence string
	}{m.owner.ID, m.owner.Epoch, review.ID, evidenceKey}, MaxRecordBytes)
	if err != nil {
		return "", err
	}
	hash := digest([]byte(identity))
	event := Milestone{ID: "milestone-" + hash, Epoch: m.owner.Epoch, SuggestionRevision: p.SuggestionRevision,
		WorkspaceID: review.Result.WorkspaceID, Incarnation: review.Result.Incarnation, ReviewID: review.ID, EvidenceKey: evidenceKey,
		ModelFingerprint: p.ModelFingerprint, CreatedAt: review.DecidedAt, NotBefore: review.DecidedAt + MilestoneDelay.Milliseconds(), ExpiresAt: review.DecidedAt + MilestoneLifetime.Milliseconds()}
	if !event.allowed(m.owner) {
		return "", ErrUnavailable
	}
	if err := m.InsertOperation(Operation{ID: event.ID, Kind: "milestone", Status: "pending", Epoch: event.Epoch, RequestDigest: hash, CreatedAt: event.CreatedAt, Milestone: &event}); err != nil {
		return "", err
	}
	return event.ID, nil
}

func (m *Mutation) invalidatePendingSuggestions(reason string) error {
	_, err := m.tx.ExecContext(m.ctx, `UPDATE learning_operations SET status='invalidated',record=json_set(record,'$.status','invalidated','$.failure',?)
 WHERE owner_id=? AND kind IN ('generate','milestone') AND status='pending'`, reason, m.owner.ID)
	if err != nil {
		return ErrUnavailable
	}
	return nil
}

// RetirePendingMilestones is startup recovery, never an automatic dispatcher.
// Pending events are deliberately not caught up after a process restart.
func (s *Service) RetirePendingMilestones(ctx context.Context, owner string) error {
	release, err := s.permit()
	if err != nil {
		return err
	}
	defer release()
	s.effects.Lock()
	defer s.effects.Unlock()
	current, err := s.store.Owner(ctx, owner)
	if err != nil || current.Revision == 0 {
		return err
	}
	var pending int
	if err := s.store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM learning_operations WHERE owner_id=? AND kind='milestone' AND status='pending'`, owner).Scan(&pending); err != nil {
		return ErrUnavailable
	}
	if pending == 0 {
		return nil
	}
	_, err = s.store.Change(ctx, owner, current.Revision, current.Epoch, false, func(m *Mutation) error {
		_, err := m.tx.ExecContext(ctx, `UPDATE learning_operations SET status='invalidated',record=json_set(record,'$.status','invalidated','$.failure','process_restarted') WHERE owner_id=? AND kind='milestone' AND status='pending'`, owner)
		return err
	})
	return err
}

package learningmissions

import (
	"context"
	"testing"
)

type milestoneReviewSource struct{ *reviewSource }

func (s milestoneReviewSource) LearningScope(_ context.Context, owner, workspace string) (string, error) {
	if owner != "local" || workspace != "workspace" {
		return "", ErrUnavailable
	}
	return "incarnation", nil
}

func enableMilestoneConsent(t *testing.T, s *Service, operation string) *designStub {
	t.Helper()
	d, _ := installDesigner(s)
	owner, _ := s.store.Owner(t.Context(), "local")
	req := preferenceRequest(operation, owner.Revision)
	req.Mode, req.Observe, req.ModelFingerprint = "automatic", false, d.config.Fingerprint
	if _, err := s.UpdatePreferences(t.Context(), "local", req); err != nil {
		t.Fatal(err)
	}
	return d
}

func milestoneFixture(t *testing.T, automatic bool) (*Service, *designStub, Outcome, ReviewRequest) {
	t.Helper()
	s, _, i := reviewFixture(t)
	s.source.(*reviewSource).proof.InputText = "reviewed input"
	s.source.(*reviewSource).proof.Process = PlainDraftProcess
	s.source = milestoneReviewSource{s.source.(*reviewSource)}
	var d *designStub
	if automatic {
		d = enableMilestoneConsent(t, s, "automatic")
	}
	owner, _ := s.store.Owner(t.Context(), "local")
	prepared, err := s.ReviewResult(t.Context(), "local", i.ID, ReviewRequest{MutationRequest: MutationRequest{OperationID: "prepare", ExpectedRevision: owner.Revision}, InstanceRevision: i.Revision, Action: "prepare"})
	if err != nil {
		t.Fatal(err)
	}
	owner, _ = s.store.Owner(t.Context(), "local")
	req := ReviewRequest{MutationRequest: MutationRequest{OperationID: "decision", ExpectedRevision: owner.Revision}, InstanceRevision: prepared.Instance.Revision, Action: "submit", ReviewID: prepared.Review.ID, Decision: "reject", Reason: "incorrect"}
	out, err := s.ReviewResult(t.Context(), "local", i.ID, req)
	if err != nil {
		t.Fatal(err)
	}
	return s, d, out, req
}

func TestMilestoneOnlyFreshPostConsentDistinctReviewQueuesOneEvent(t *testing.T) {
	s, designer, out, request := milestoneFixture(t, true)
	if out.Operation.MilestoneID == "" {
		t.Fatal("new opted-in review did not queue a milestone")
	}
	event, err := s.store.Operation(t.Context(), "local", out.Operation.MilestoneID)
	owner, _ := s.store.Owner(t.Context(), "local")
	if err != nil || event.Status != "pending" || event.Milestone == nil || !event.Milestone.allowed(owner) || designer.calls.Load() != 0 {
		t.Fatal("unbounded or unauthorized event", event, err)
	}
	wakes := 0
	s.milestoneWake = func(string, string) { wakes++ }
	if _, err := s.ReviewResult(t.Context(), "local", out.Instance.ID, request); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if _, err := s.Status(t.Context(), "local"); err != nil {
			t.Fatal(err)
		}
	}
	if wakes != 0 || designer.calls.Load() != 0 {
		t.Fatal("replay or visits dispatched work")
	}
	var events int
	if err := s.store.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM learning_operations WHERE kind='milestone'`).Scan(&events); err != nil || events != 1 {
		t.Fatal("event duplicated", events, err)
	}
}

func TestMilestoneLaterOptInCannotAdoptAnEarlierReview(t *testing.T) {
	s, _, out, _ := milestoneFixture(t, false)
	if out.Operation.MilestoneID != "" {
		t.Fatal("default mode scheduled generation")
	}
	d := enableMilestoneConsent(t, s, "later-opt-in")
	var events int
	if err := s.store.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM learning_operations WHERE kind='milestone'`).Scan(&events); err != nil || events != 0 || d.calls.Load() != 0 {
		t.Fatal("preference save backfilled a milestone", err)
	}
}

func TestMilestoneSameMillisecondReapprovalCannotReviveOldGrant(t *testing.T) {
	s, _, out, _ := milestoneFixture(t, true)
	event, _ := s.store.Operation(t.Context(), "local", out.Operation.MilestoneID)
	owner, _ := s.store.Owner(t.Context(), "local")
	before := owner.Preferences.SuggestionRevision
	if _, err := s.ClearPreferences(t.Context(), "local", MutationRequest{OperationID: "revoke", ExpectedRevision: owner.Revision}); err != nil {
		t.Fatal(err)
	}
	enableMilestoneConsent(t, s, "reapprove")
	owner, _ = s.store.Owner(t.Context(), "local")
	current, _ := s.store.Operation(t.Context(), "local", event.ID)
	if owner.Preferences.SuggestionRevision <= before || event.Milestone.allowed(owner) || current.Status != "invalidated" {
		t.Fatal("old event survived revocation/reapproval")
	}
}

func TestMilestoneQueueCoalescesWithoutExtendingTheFirstDeadline(t *testing.T) {
	s, _, out, _ := milestoneFixture(t, true)
	first, _ := s.store.Operation(t.Context(), "local", out.Operation.MilestoneID)
	owner, _ := s.store.Owner(t.Context(), "local")
	// The queue receives consumed metadata from its owning review transaction;
	// this focused store test supplies a second such event without dispatching.
	review := *out.Review
	review.ID, review.DecidedAt = "second-review", review.DecidedAt+1000
	var id string
	_, err := s.store.Change(t.Context(), "local", owner.Revision, owner.Epoch, false, func(m *Mutation) error {
		var err error
		id, err = m.queueMilestone(review, digest([]byte("second-evidence")))
		return err
	})
	if err != nil || id != first.ID {
		t.Fatal("events did not coalesce", err)
	}
	current, _ := s.store.Operation(t.Context(), "local", id)
	if *current.Milestone != *first.Milestone {
		t.Fatal("coalescing extended or rewrote the queued authority")
	}
	owner, _ = s.store.Owner(t.Context(), "local")
	review.ID, review.DecidedAt = "later-review", first.Milestone.ExpiresAt+1
	_, err = s.store.Change(t.Context(), "local", owner.Revision, owner.Epoch, false, func(m *Mutation) error {
		var err error
		id, err = m.queueMilestone(review, digest([]byte("later-evidence")))
		return err
	})
	if err != nil || id == first.ID {
		t.Fatal("expired event was revived", err)
	}
	old, _ := s.store.Operation(t.Context(), "local", first.ID)
	if old.Status != "invalidated" || old.Failure != "milestone_expired" {
		t.Fatal("expiry lost its terminal receipt")
	}
}

func TestMilestoneRestartRetiresQueuedEventsWithoutCatchupOrEnrollment(t *testing.T) {
	s, d, out, _ := milestoneFixture(t, true)
	restarted := NewService(s.store, s.gate, s.source)
	restarted.SetDesigner(d)
	if err := restarted.RetirePendingMilestones(t.Context(), "local"); err != nil {
		t.Fatal(err)
	}
	event, _ := s.store.Operation(t.Context(), "local", out.Operation.MilestoneID)
	if event.Status != "invalidated" || event.Failure != "process_restarted" || d.calls.Load() != 0 {
		t.Fatal("startup caught up an old event")
	}
	if err := restarted.RetirePendingMilestones(t.Context(), "unenrolled"); err != nil {
		t.Fatal(err)
	}
	owner, _ := s.store.Owner(t.Context(), "unenrolled")
	if owner.Revision != 0 {
		t.Fatal("startup enrolled an owner")
	}
}

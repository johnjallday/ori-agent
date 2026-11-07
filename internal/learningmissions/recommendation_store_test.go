package learningmissions

import (
	"errors"
	"testing"
	"time"
)

func TestRecommendationDeclineSurvivesRestartHistoryClearAndBlocksModelAdmission(t *testing.T) {
	s, _ := testService(t)
	out := propose(t, s)
	if len(out.Instance.Definition.Signals) == 0 || !validDigest(out.Instance.Definition.FocusTopic) {
		t.Fatal("offer omitted host recommendation basis")
	}
	if _, err := s.Decide(t.Context(), "local", out.Instance.ID, DecisionRequest{MutationRequest: MutationRequest{OperationID: "decline", ExpectedRevision: 1}, InstanceRevision: 1, Decision: "decline"}); err != nil {
		t.Fatal(err)
	}
	restarted := NewService(s.store, s.gate, sourceStub{})
	restarted.now = s.now
	d, req := installDesigner(restarted)
	req.ExpectedRevision = 2
	if _, err := restarted.Propose(t.Context(), "local", req); !errors.Is(err, ErrUnavailable) || d.calls.Load() != 0 {
		t.Fatalf("decline admitted a model call after restart: %v", err)
	}
	if _, err := restarted.ClearHistory(t.Context(), "local", MutationRequest{OperationID: "clear", ExpectedRevision: 2}); err != nil {
		t.Fatal(err)
	}
	owner, _ := s.store.Owner(t.Context(), "local")
	if len(owner.Preferences.Exclusions) != 1 || owner.Preferences.Exclusions[0].Until != 0 {
		t.Fatal("history clear forgot a durable preference")
	}
	generic := ProposalRequest{MutationRequest: MutationRequest{OperationID: "generic", ExpectedRevision: owner.Revision}, WorkspaceID: "workspace"}
	if _, err := restarted.Propose(t.Context(), "local", generic); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("generic bypassed decline: %v", err)
	}
	if _, err := restarted.ClearPreferences(t.Context(), "local", MutationRequest{OperationID: "reset-preferences", ExpectedRevision: owner.Revision}); err != nil {
		t.Fatal(err)
	}
	owner, _ = s.store.Owner(t.Context(), "local")
	generic.ExpectedRevision = owner.Revision
	if _, err := restarted.Propose(t.Context(), "local", generic); err != nil {
		t.Fatalf("explicit preference reset did not release exclusion: %v", err)
	}
}

func TestRecommendationDeferExpiresOnlyForAnExplicitLaterRequest(t *testing.T) {
	s, _ := testService(t)
	out := propose(t, s)
	if _, err := s.Decide(t.Context(), "local", out.Instance.ID, DecisionRequest{MutationRequest: MutationRequest{OperationID: "defer", ExpectedRevision: 1}, InstanceRevision: 1, Decision: "defer"}); err != nil {
		t.Fatal(err)
	}
	req := ProposalRequest{MutationRequest: MutationRequest{OperationID: "after-defer", ExpectedRevision: 2}, WorkspaceID: "workspace"}
	if _, err := s.Propose(t.Context(), "local", req); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("cooldown bypassed: %v", err)
	}
	owner, _ := s.store.Owner(t.Context(), "local")
	if owner.Preferences.Exclusions[0].Until != s.now().Add(DeferCooldown).UnixMilli() {
		t.Fatal("cooldown not durably bounded")
	}
	now := s.now().Add(DeferCooldown + time.Millisecond)
	s.now = func() time.Time { return now }
	for range 3 {
		if _, err := s.Status(t.Context(), "local"); err != nil {
			t.Fatal(err)
		}
	}
	owner, _ = s.store.Owner(t.Context(), "local")
	if owner.Revision != 2 {
		t.Fatal("time or visits mutated a recommendation")
	}
	if _, err := s.Propose(t.Context(), "local", req); err != nil {
		t.Fatalf("explicit later request refused after cooldown: %v", err)
	}
}

func TestRecommendationPracticeReadsOnlyMatchingLocalReceiptScopeAndPreservesUnknown(t *testing.T) {
	s, db, instance := reviewFixture(t)
	prepared := prepareReview(t, s, instance)
	if _, err := s.ReviewResult(t.Context(), "local", instance.ID, ReviewRequest{MutationRequest: MutationRequest{OperationID: "review", ExpectedRevision: 3}, InstanceRevision: 3,
		Action: "submit", ReviewID: prepared.Review.ID, Decision: "reject", Reason: "incorrect"}); err != nil {
		t.Fatal(err)
	}
	owner, _ := s.store.Owner(t.Context(), "local")
	owner = generationOwner(owner, "workspace")
	candidate, _ := s.source.Candidate(t.Context(), "local", "workspace")
	eligible := []EligibleCandidate{{Candidate: candidate, Structure: DelegateInspect}}
	policy, err := s.recommendationContext(t.Context(), owner, eligible, "")
	if err != nil || !policy.PracticeKnown || policy.Practice["delegate"] != 1 || policy.Practice["evaluate"] != 1 || policy.Active {
		t.Fatalf("canonical local counts: %+v %v", policy, err)
	}
	eligible[0].Incarnation = "reimported"
	policy, err = s.recommendationContext(t.Context(), owner, eligible, "")
	if err != nil || len(policy.Practice) != 0 || len(policy.Recent) != 0 {
		t.Fatalf("old identity counted in a reimported scope: %+v %v", policy, err)
	}
	eligible[0].Incarnation = candidate.Incarnation
	if _, err := db.ExecContext(t.Context(), `DELETE FROM learning_reviews WHERE owner_id='local'`); err != nil {
		t.Fatal(err)
	}
	policy, err = s.recommendationContext(t.Context(), owner, eligible, "")
	if err != nil || policy.PracticeKnown || len(policy.Practice) != 0 {
		t.Fatalf("missing receipt became zero-practice certainty: %+v %v", policy, err)
	}
}

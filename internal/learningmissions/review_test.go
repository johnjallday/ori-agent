package learningmissions

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/database"
)

type reviewSource struct {
	sourceStub
	proof ResultProof
}

func (s *reviewSource) Result(context.Context, string, Instance) (ResultProof, error) {
	return s.proof, nil
}

func reviewFixture(t *testing.T) (*Service, *database.DB, Instance) {
	t.Helper()
	return reviewFixtureOn(t, testService)
}

// freshReviewFixture is reviewFixture on a freshly initialized database, for
// the restart and reset tests.
func freshReviewFixture(t *testing.T) (*Service, *database.DB, Instance) {
	t.Helper()
	return reviewFixtureOn(t, freshTestService)
}

func reviewFixtureOn(t *testing.T, newService serviceOpener) (*Service, *database.DB, Instance) {
	t.Helper()
	s, db := newService(t)
	offer := propose(t, s)
	i, proof := resultFixture()
	proof.Ref.Incarnation = offer.Instance.Definition.Incarnation
	i.ID, i.Definition = offer.Instance.ID, offer.Instance.Definition
	i.Revision = 2
	_, err := s.store.Change(t.Context(), "local", 1, 1, false, func(m *Mutation) error { return m.UpdateInstance(i, 1) })
	if err != nil {
		t.Fatal(err)
	}
	s.source = &reviewSource{proof: proof}
	s.now = func() time.Time { return time.UnixMilli(proof.FinishedAt + 1) }
	return s, db, i
}

func prepareReview(t *testing.T, s *Service, i Instance) Outcome {
	t.Helper()
	out, err := s.ReviewResult(t.Context(), "local", i.ID, ReviewRequest{
		MutationRequest: MutationRequest{OperationID: "prepare-review", ExpectedRevision: 2}, InstanceRevision: 2, Action: "prepare"})
	if err != nil || out.Review == nil || out.Instance == nil || out.Instance.State != Active {
		t.Fatalf("prepare review must not complete: %+v %v", out, err)
	}
	return out
}

func TestReviewCannotPredateCanonicalResultCompletion(t *testing.T) {
	s, _, instance := reviewFixture(t)
	finished := s.source.(*reviewSource).proof.FinishedAt
	s.now = func() time.Time { return time.UnixMilli(finished - 1) }
	if _, err := s.ReviewResult(t.Context(), "local", instance.ID, ReviewRequest{MutationRequest: MutationRequest{OperationID: "future-result", ExpectedRevision: 2}, InstanceRevision: 2, Action: "prepare"}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("review predating result: %v", err)
	}
	owner, _ := s.store.Owner(t.Context(), "local")
	if owner.Revision != 2 {
		t.Fatal("future result manufactured a challenge")
	}
}

func TestReviewReceiptCompletesAtomicallyAndReplaysWithoutDuplicateCredit(t *testing.T) {
	s, db, i := reviewFixture(t)
	out := prepareReview(t, s, i)
	req := ReviewRequest{MutationRequest: MutationRequest{OperationID: "submit-review", ExpectedRevision: 3},
		InstanceRevision: 3, Action: "submit", ReviewID: out.Review.ID, Decision: "reject", Reason: "incorrect"}
	for range 2 {
		out, err := s.ReviewResult(t.Context(), "local", i.ID, req)
		if err != nil || out.Instance == nil || out.Instance.State != Completed || out.Review == nil || out.Review.Decision != "reject" {
			t.Fatalf("review: %+v %v", out, err)
		}
	}
	var credits int
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM learning_evidence`).Scan(&credits); err != nil || credits != 2 {
		t.Fatalf("duplicate practice credit: %d %v", credits, err)
	}
	req.OperationID = "new-submit"
	req.ExpectedRevision = 4
	if _, err := s.ReviewResult(t.Context(), "local", i.ID, req); !errors.Is(err, ErrConflict) {
		t.Fatalf("consumed challenge reused: %v", err)
	}
}

func TestReviewReceiptRollsBackDecisionAndCompletionOnCreditFailure(t *testing.T) {
	s, db, i := reviewFixture(t)
	out := prepareReview(t, s, i)
	if _, err := db.ExecContext(t.Context(), `CREATE TRIGGER fail_learning_credit BEFORE INSERT ON learning_evidence BEGIN SELECT RAISE(ABORT,'injected'); END`); err != nil {
		t.Fatal(err)
	}
	_, err := s.ReviewResult(t.Context(), "local", i.ID, ReviewRequest{MutationRequest: MutationRequest{OperationID: "submit-review", ExpectedRevision: 3},
		InstanceRevision: 3, Action: "submit", ReviewID: out.Review.ID, Decision: "use", Reason: "meets_scope"})
	if err == nil {
		t.Fatal("injected credit failure ignored")
	}
	review, err := s.store.Review(t.Context(), "local", out.Review.ID)
	if err != nil || review.Decision != "" {
		t.Fatalf("decision survived rollback: %+v %v", review, err)
	}
	instance, _ := s.store.Instance(t.Context(), "local", i.ID)
	if instance.State != Active || instance.Revision != 3 {
		t.Fatal("completion survived failed transaction")
	}
}

func TestReviewExpiredNonceCannotComplete(t *testing.T) {
	s, _, i := reviewFixture(t)
	out := prepareReview(t, s, i)
	now := s.now().Add(ReviewLifetime + time.Second)
	s.now = func() time.Time { return now }
	_, err := s.ReviewResult(t.Context(), "local", i.ID, ReviewRequest{
		MutationRequest:  MutationRequest{OperationID: "expired-submit", ExpectedRevision: 3},
		InstanceRevision: 3, Action: "submit", ReviewID: out.Review.ID, Decision: "use", Reason: "meets_scope"})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("expired review: %v", err)
	}
	got, _ := s.store.Instance(t.Context(), "local", i.ID)
	if got.State != Active {
		t.Fatal("expired review advanced progress")
	}
}

func TestReviewRejectsStaleResultAndNonceFromAnotherOwner(t *testing.T) {
	s, _, i := reviewFixture(t)
	out := prepareReview(t, s, i)
	req := ReviewRequest{MutationRequest: MutationRequest{OperationID: "submit-review", ExpectedRevision: 3},
		InstanceRevision: 3, Action: "submit", ReviewID: "foreign", Decision: "use", Reason: "meets_scope"}
	if _, err := s.ReviewResult(t.Context(), "local", i.ID, req); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("foreign nonce: %v", err)
	}
	req.ReviewID = out.Review.ID
	s.source.(*reviewSource).proof.Ref.ArtifactID = "replaced-artifact"
	if _, err := s.ReviewResult(t.Context(), "local", i.ID, req); !errors.Is(err, ErrConflict) {
		t.Fatalf("result changed since challenge: %v", err)
	}
}

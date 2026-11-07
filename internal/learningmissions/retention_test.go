package learningmissions

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"
)

func seedRetentionHistory(t *testing.T, s *Service, now int64) {
	t.Helper()
	// Disposable scalar fixtures provide pressure without inventing earned
	// practice. Only the separately consumed real review below earns evidence.
	i := testInstance("local", "unused")
	definition, _ := json.Marshal(i.Definition)
	if _, err := s.store.db.ExecContext(t.Context(), `WITH RECURSIVE n(value) AS (SELECT 0 UNION ALL SELECT value+1 FROM n WHERE value+1<?)
 INSERT INTO learning_instances(owner_id,id,epoch,revision,state,definition,progress,created_at_ms,updated_at_ms)
 SELECT 'local','terminal-'||value,1,1,'stopped',?,'{}',?,? FROM n`, RetainedTerminalInstances, string(definition), now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.db.ExecContext(t.Context(), `WITH RECURSIVE n(value) AS (SELECT 0 UNION ALL SELECT value+1 FROM n WHERE value+1<?)
 INSERT INTO learning_reviews(owner_id,id,epoch,record,created_at_ms) SELECT 'local','recent-'||value,1,'{}',? FROM n`, MaxReviewReceipts-1, now); err != nil {
		t.Fatal(err)
	}
}

func TestRetentionCompactsOldReceiptsWithoutErasingPracticeOrLegacyDedupAuthority(t *testing.T) {
	s, db, i := reviewFixture(t)
	prepared := prepareReview(t, s, i)
	if _, err := s.ReviewResult(t.Context(), "local", i.ID, ReviewRequest{MutationRequest: MutationRequest{OperationID: "consume", ExpectedRevision: 3}, InstanceRevision: 3, Action: "submit", ReviewID: prepared.Review.ID, Decision: "reject", Reason: "incorrect", Note: "PRIVATE_NOTE_TO_COMPACT"}); err != nil {
		t.Fatal(err)
	}
	// Old support is never upgraded by retention; legacy secondary identity
	// still depends on exact content-free receipt correlations after compaction.
	if _, err := db.ExecContext(t.Context(), `UPDATE learning_evidence SET record=json_remove(record,'$.attempt_digest')`); err != nil {
		t.Fatal(err)
	}
	before, _ := s.store.Competencies(t.Context(), "local")
	future := s.now().Add(RetentionAge + time.Hour)
	s.now = func() time.Time { return future }
	seedRetentionHistory(t, s, future.UnixMilli())
	if _, err := s.Status(t.Context(), "local"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.Instance(t.Context(), "local", i.ID); err != nil {
		t.Fatal("GET pruned history", err)
	}
	owner, _ := s.store.Owner(t.Context(), "local")
	if _, err := s.apply(t.Context(), "local", MutationRequest{OperationID: "explicit-mutation", ExpectedRevision: owner.Revision}, "fixture", nil, nil, func(*Mutation, *Operation) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.Instance(t.Context(), "local", i.ID); !errors.Is(err, ErrUnavailable) {
		t.Fatal("old unreferenced terminal instance retained", err)
	}
	after, _ := s.store.Competencies(t.Context(), "local")
	if !reflect.DeepEqual(before, after) {
		t.Fatal("compaction changed earned practice", before, after)
	}
	e, err := s.store.Evidence(t.Context(), "local", after.Items[0].EvidenceKeys[0])
	if err != nil || !e.Unavailable {
		t.Fatal("compacted evidence was not marked unavailable", e, err)
	}
	if _, err := s.EvidenceView(t.Context(), "local", e.Key); !errors.Is(err, ErrUnavailable) {
		t.Fatal("receipt reconstructed from tombstone", err)
	}
	var record string
	if err := db.QueryRowContext(t.Context(), `SELECT record FROM learning_reviews WHERE id=?`, prepared.Review.ID).Scan(&record); err != nil {
		t.Fatal(err)
	}
	var compact map[string]any
	if json.Unmarshal([]byte(record), &compact) != nil || compact["compacted"] != true || compact["note"] != nil || compact["instance_id"] != nil || compact["original"] != nil {
		t.Fatal("private review retained", record)
	}
	current, _ := s.store.Owner(t.Context(), "local")
	policy, err := s.recommendationContext(t.Context(), generationOwner(current, "workspace"), []EligibleCandidate{{Candidate: Candidate{WorkspaceID: "workspace", Incarnation: i.Definition.Incarnation}, Structure: DelegateInspect}}, DelegateInspect)
	if err != nil || !policy.PracticeKnown || policy.Practice["delegate"] != 1 || policy.Practice["evaluate"] != 1 {
		t.Fatal("retention erased scoped practice metadata", policy, err)
	}
	if _, err := s.store.Review(t.Context(), "local", prepared.Review.ID); !errors.Is(err, ErrUnavailable) {
		t.Fatal("compacted replay metadata became a fresh review", err)
	}
	if reviews, err := s.store.RecentReviews(t.Context(), "local", "workspace", MaxCandidates); err != nil || len(reviews) != 0 {
		t.Fatal("compacted receipt became an eligible original", reviews, err)
	}
	// A changed exact result for the same reviewed input/Ticket still cannot
	// upgrade support or count again, including legacy no-attempt-digest rows.
	r, err := s.store.Review(t.Context(), "local", prepared.Review.ID)
	if err == nil {
		t.Fatal("unexpected full receipt", r)
	}
	r = *prepared.Review
	r.ID = "retry-review"
	r.Decision, r.Reason, r.DecidedAt = "use", "meets_scope", r.CreatedAt
	r.Result.RunID = "new-run"
	r.Result.ArtifactID = "new-artifact"
	_, err = s.store.Change(t.Context(), "local", current.Revision, current.Epoch, false, func(m *Mutation) error {
		challenge := r
		challenge.Decision, challenge.Reason, challenge.DecidedAt = "", "", 0
		if err := m.InsertReview(challenge); err != nil {
			return err
		}
		if err := m.ConsumeReview(r); err != nil {
			return err
		}
		credits, err := creditsForReview("local", r, []string{"delegate", "evaluate"}, GuidanceBasic, false, r.InputDigest)
		if err != nil {
			return err
		}
		for _, credit := range credits {
			added, err := m.Credit(credit)
			if err != nil {
				return err
			}
			if added {
				t.Fatal("compaction lost legacy dedup authority")
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRetentionPrunedDecisionReceiptCannotReplayOrReenableDeclinedTopics(t *testing.T) {
	s, db := testService(t)
	offer := propose(t, s)
	if _, err := s.Decide(t.Context(), "local", offer.Instance.ID, DecisionRequest{MutationRequest: MutationRequest{OperationID: "decline", ExpectedRevision: 1}, InstanceRevision: 1, Decision: "decline"}); err != nil {
		t.Fatal(err)
	}
	future := s.now().Add(RetentionAge + time.Hour)
	s.now = func() time.Time { return future }
	owner, _ := s.store.Owner(t.Context(), "local")
	_, err := s.store.Change(t.Context(), "local", owner.Revision, owner.Epoch, false, func(m *Mutation) error {
		for n := range RetainedTerminalOperations {
			id := fmt.Sprintf("recent-op-%03d", n)
			if err := m.InsertOperation(Operation{ID: id, Kind: "fixture", Status: "succeeded", Epoch: 1, RequestDigest: digest([]byte(id)), CreatedAt: future.UnixMilli()}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	owner, _ = s.store.Owner(t.Context(), "local")
	if _, err := s.apply(t.Context(), "local", MutationRequest{OperationID: "trigger-retention", ExpectedRevision: owner.Revision}, "fixture", nil, nil, func(*Mutation, *Operation) error { return nil }); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM learning_operations WHERE id IN ('proposal','decline')`).Scan(&count); err != nil || count != 0 {
		t.Fatal("old unneeded decision receipts retained", count, err)
	}
	after, _ := s.store.Owner(t.Context(), "local")
	if _, err := s.Propose(t.Context(), "local", ProposalRequest{MutationRequest: MutationRequest{OperationID: "proposal", ExpectedRevision: 0}, WorkspaceID: "workspace"}); !errors.Is(err, ErrConflict) {
		t.Fatal("pruned old receipt replayed", err)
	}
	if _, err := s.Propose(t.Context(), "local", ProposalRequest{MutationRequest: MutationRequest{OperationID: "new-request", ExpectedRevision: after.Revision}, WorkspaceID: "workspace"}); !errors.Is(err, ErrUnavailable) {
		t.Fatal("pruning re-enabled declined topic", err)
	}
}

func TestRetentionPreservesLiveLineageUncertainOperationsRecentRecordsAndExclusions(t *testing.T) {
	s, db := testService(t)
	offer := propose(t, s)
	old := s.now().Add(-RetentionAge - time.Hour).UnixMilli()
	now := s.now().UnixMilli()
	seedRetentionHistory(t, s, now)
	definition, _ := json.Marshal(testInstance("local", "original").Definition)
	if _, err := db.ExecContext(t.Context(), `INSERT INTO learning_instances(owner_id,id,epoch,revision,state,definition,progress,created_at_ms,updated_at_ms) VALUES('local','original',1,1,'completed',?,json_object('operation_id','needed'),?,?)`, string(definition), old, old); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `INSERT INTO learning_reviews(owner_id,id,epoch,record,created_at_ms) VALUES('local','original-review',1,json_object('instance_id','original'),?)`, old); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `UPDATE learning_instances SET definition=json_set(definition,'$.original_receipt','original-review') WHERE id=?`, offer.Instance.ID); err != nil {
		t.Fatal(err)
	}
	owner, _ := s.store.Owner(t.Context(), "local")
	_, err := s.store.Change(t.Context(), "local", owner.Revision, owner.Epoch, false, func(m *Mutation) error {
		m.owner.Preferences.Exclusions = []RecommendationExclusion{{WorkspaceID: "workspace", Structure: RepeatSuccess}}
		for n := range RetainedTerminalOperations + 5 {
			id := fmt.Sprintf("old-op-%03d", n)
			if err := m.InsertOperation(Operation{ID: id, Kind: "fixture", Status: "succeeded", Epoch: 1, RequestDigest: digest([]byte(id)), CreatedAt: old}); err != nil {
				return err
			}
		}
		for _, id := range []string{"pending", "unknown", "needed", "automatic", "recent"} {
			op := Operation{ID: id, Kind: "fixture", Status: "succeeded", Epoch: 1, RequestDigest: digest([]byte(id)), CreatedAt: old - 1}
			if id == "pending" || id == "unknown" {
				op.Status = id
			}
			if id == "recent" {
				op.CreatedAt = now
			}
			if id == "automatic" {
				op.Kind = "generate"
				op.Automatic = true
				op.MilestoneID = "event"
				op.CandidateFingerprint = digest([]byte("candidate"))
			}
			if err := m.InsertOperation(op); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	owner, _ = s.store.Owner(t.Context(), "local")
	if _, err := s.apply(t.Context(), "local", MutationRequest{OperationID: "explicit", ExpectedRevision: owner.Revision}, "fixture", nil, nil, func(*Mutation, *Operation) error { return nil }); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"pending", "unknown", "needed", "automatic", "recent"} {
		if _, err := s.store.Operation(t.Context(), "local", id); err != nil {
			t.Fatal("pruned required operation", id, err)
		}
	}
	if _, err := s.store.Instance(t.Context(), "local", "original"); err != nil {
		t.Fatal("pruned active original lineage", err)
	}
	var compacted int
	if err := db.QueryRowContext(t.Context(), `SELECT COALESCE(json_extract(record,'$.compacted'),0) FROM learning_reviews WHERE id='original-review'`).Scan(&compacted); err != nil || compacted != 0 {
		t.Fatal("compacted live original", err)
	}
	var dropped int
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM learning_operations WHERE id LIKE 'old-op-%'`).Scan(&dropped); err != nil || dropped >= RetainedTerminalOperations+5 {
		t.Fatal("eligible old operations never bounded", dropped, err)
	}
	after, _ := s.store.Owner(t.Context(), "local")
	if !reflect.DeepEqual(after.Preferences.Exclusions, owner.Preferences.Exclusions) {
		t.Fatal("retention re-enabled a declined topic")
	}
}

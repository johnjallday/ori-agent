package learningmissions

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// Padding is owned test-only operation metadata, never a prompt or a live store.
func fillLearningStorage(t *testing.T, s *Service, target int64) {
	t.Helper()
	usage, err := learningStorageUsage(t.Context(), s.store.db, "local")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := s.store.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	for n := 0; usage.Bytes < target; n++ {
		id := fmt.Sprintf("padding-%d", n)
		padding := min(int64(12000), target-usage.Bytes)
		record, err := json.Marshal(map[string]any{"id": id, "kind": "fixture", "status": "succeeded", "created_at_ms": s.now().UnixMilli(), "padding": strings.Repeat("x", int(padding))})
		if err != nil {
			t.Fatal(err)
		}
		hash := digest([]byte(id))
		if _, err := tx.ExecContext(t.Context(), `INSERT INTO learning_operations(owner_id,id,epoch,kind,status,request_digest,record,created_at_ms) VALUES('local',?,1,'fixture','succeeded',?,?,?)`, id, hash, string(record), s.now().UnixMilli()); err != nil {
			t.Fatal(err)
		}
		usage.Bytes += int64(len(record) + len("local") + len(id) + len(hash) + 96)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestStorageReservationRefusesModelAdmissionBeforeCallingAndKeepsOffUsable(t *testing.T) {
	s, _ := testService(t)
	owner, err := s.store.Change(t.Context(), "local", 0, 0, true, func(m *Mutation) error { return m.BindSource("workspace", "incarnation") })
	if err != nil {
		t.Fatal(err)
	}
	d, req := installDesigner(s)
	req.ExpectedRevision = owner.Revision
	fillLearningStorage(t, s, MaxLearningBytes-StorageControlReserve-16*1024)
	if _, err := s.Propose(t.Context(), "local", req); !errors.Is(err, ErrStorageLimit) || d.calls.Load() != 0 {
		t.Fatal("model called before reserving publication space", err, d.calls.Load())
	}
	after, _ := s.store.Owner(t.Context(), "local")
	if after.Revision != owner.Revision {
		t.Fatal("refused admission mutated the owner")
	}
	if _, err := s.store.Operation(t.Context(), "local", req.OperationID); !errors.Is(err, ErrUnavailable) {
		t.Fatal("failed reservation was committed", err)
	}
	preferences := preferenceRequest("off", owner.Revision)
	preferences.Mode = "off"
	if _, err := s.UpdatePreferences(t.Context(), "local", preferences); err != nil {
		t.Fatal("capacity prevented disabling coaching", err)
	}
}

func TestStorageControlReserveAllowsStopAndExplicitClearKeepsCallAccounting(t *testing.T) {
	s, host, id, _ := workFixture(t)
	owner, _ := s.store.Owner(t.Context(), "local")
	owner, err := s.store.Change(t.Context(), "local", owner.Revision, owner.Epoch, false, func(m *Mutation) error {
		return m.InsertOperation(Operation{ID: "counted-design", Kind: "generate", Status: "unknown", Epoch: 1, RequestDigest: digest([]byte("counted")), CreatedAt: s.now().UnixMilli(), ReservedTokens: GenerationReservedTokens})
	})
	if err != nil {
		t.Fatal(err)
	}
	fillLearningStorage(t, s, MaxLearningBytes-StorageControlReserve+4096)
	if _, err := s.Decide(t.Context(), "local", id, DecisionRequest{MutationRequest: MutationRequest{OperationID: "stop-at-capacity", ExpectedRevision: owner.Revision}, InstanceRevision: 2, Decision: "stop"}); err != nil {
		t.Fatal("capacity prevented stopping tracking", err)
	}
	owner, _ = s.store.Owner(t.Context(), "local")
	if _, err := s.ClearHistory(t.Context(), "local", MutationRequest{OperationID: "clear-at-capacity", ExpectedRevision: owner.Revision}); err != nil {
		t.Fatal("capacity prevented explicit clear", err)
	}
	usage, err := learningStorageUsage(t.Context(), s.store.db, "local")
	if err != nil || usage.Bytes > 32*1024 || host.calls != 0 {
		t.Fatal("history clear kept non-billing history or touched work", usage, err)
	}
	accounting, err := s.Diagnostics(t.Context(), "local")
	if err != nil || accounting.DesignCalls != 1 || accounting.UnknownUsageCalls != 1 || accounting.ReservedTokens != GenerationReservedTokens {
		t.Fatal("history clear forgot uncertain costs", accounting, err)
	}
}

func TestStorageEvidenceCapRollsBackNewCreditButNeverDeletesOrRejectsDuplicates(t *testing.T) {
	s, db := testService(t)
	owner, err := s.store.Change(t.Context(), "local", 0, 0, true, func(*Mutation) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `WITH RECURSIVE n(value) AS (SELECT 0 UNION ALL SELECT value+1 FROM n WHERE value+1<?)
 INSERT INTO learning_evidence(owner_id,dedup_key,record,created_at_ms) SELECT 'local',printf('%064x',value),'{}',? FROM n`, MaxEvidence, s.now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	e := Evidence{Key: digest([]byte("new")), ReviewID: "review", Structure: DelegateInspect, Version: 1, Competency: "evaluate", Guidance: GuidanceUnknown, CreatedAt: s.now().UnixMilli()}
	if _, err := s.store.Change(t.Context(), "local", owner.Revision, owner.Epoch, false, func(m *Mutation) error { _, err := m.Credit(e); return err }); !errors.Is(err, ErrStorageLimit) {
		t.Fatal("dedup capacity admitted new credit", err)
	}
	usage, _ := learningStorageUsage(t.Context(), s.store.db, "local")
	after, _ := s.store.Owner(t.Context(), "local")
	if usage.Evidence != MaxEvidence || after.Revision != owner.Revision {
		t.Fatal("capacity failure deleted or partly committed evidence")
	}
	e.Key = fmt.Sprintf("%064x", 0)
	if _, err := s.store.Change(t.Context(), "local", owner.Revision, owner.Epoch, false, func(m *Mutation) error {
		added, err := m.Credit(e)
		if added {
			t.Fatal("duplicate became new credit")
		}
		return err
	}); err != nil {
		t.Fatal("dedup recovery refused at capacity", err)
	}
}

func TestHistoryClearForgetsOldUnknownDesignsButNotRecentBudgetImpact(t *testing.T) {
	s, _ := testService(t)
	owner, err := s.store.Change(t.Context(), "local", 0, 0, true, func(m *Mutation) error {
		for _, id := range []string{"old", "recent"} {
			at := s.now().UnixMilli()
			if id == "old" {
				at -= RetentionAge.Milliseconds() + 1
			}
			if err := m.InsertOperation(Operation{ID: id, Kind: "generate", Status: "unknown", Epoch: 1, RequestDigest: digest([]byte(id)), CreatedAt: at, ReservedTokens: GenerationReservedTokens}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClearHistory(t.Context(), "local", MutationRequest{OperationID: "clear", ExpectedRevision: owner.Revision}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.Operation(t.Context(), "local", "old"); !errors.Is(err, ErrUnavailable) {
		t.Fatal("history clear retained expired unknown cost forever", err)
	}
	recent, err := s.store.Operation(t.Context(), "local", "recent")
	if err != nil || recent.ReservedTokens != GenerationReservedTokens {
		t.Fatal("history clear erased recent uncertain spending", recent, err)
	}
}

func TestStorageReviewCapCannotPartiallyCreateAChallenge(t *testing.T) {
	s, db := testService(t)
	owner, err := s.store.Change(t.Context(), "local", 0, 0, true, func(*Mutation) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `WITH RECURSIVE n(value) AS (SELECT 0 UNION ALL SELECT value+1 FROM n WHERE value+1<?)
 INSERT INTO learning_reviews(owner_id,id,epoch,record,created_at_ms) SELECT 'local','old-'||value,1,'{}',? FROM n`, MaxReviewReceipts, s.now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	i, proof := resultFixture()
	r := Review{ID: "new-challenge", InstanceID: i.ID, Revision: i.Revision, Epoch: 1, Result: proof.Ref, InputDigest: i.Progress.InputDigest, Structure: DelegateInspect, Version: 1, Guidance: GuidanceGuided, CreatedAt: proof.FinishedAt + 1, ExpiresAt: proof.FinishedAt + 10000}
	if _, err := s.store.Change(t.Context(), "local", owner.Revision, owner.Epoch, false, func(m *Mutation) error { return m.InsertReview(r) }); !errors.Is(err, ErrStorageLimit) {
		t.Fatal("unbounded review admission", err)
	}
	if _, err := s.store.Review(t.Context(), "local", r.ID); !errors.Is(err, ErrUnavailable) {
		t.Fatal("failed challenge remained readable", err)
	}
}

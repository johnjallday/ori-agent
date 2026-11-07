package learningmissions

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func adjustmentRequest(t *testing.T, s *Service, id, operation, kind string) AdjustmentRequest {
	t.Helper()
	owner, err := s.store.Owner(t.Context(), "local")
	if err != nil {
		t.Fatal(err)
	}
	i, err := s.store.Instance(t.Context(), "local", id)
	if err != nil {
		t.Fatal(err)
	}
	return AdjustmentRequest{MutationRequest: MutationRequest{OperationID: operation, ExpectedRevision: owner.Revision}, InstanceRevision: i.Revision, Kind: kind}
}

func TestAdjustmentPreservesActiveWorkUntilAtomicRenewedAcceptance(t *testing.T) {
	s, host, id, work := workFixture(t)
	prepared, err := s.PrepareWork(t.Context(), "local", id, work)
	if err != nil {
		t.Fatal(err)
	}
	before := *prepared.Instance
	req := adjustmentRequest(t, s, id, "smaller", "smaller")
	out, err := s.Adjust(t.Context(), "local", id, req)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := s.Adjust(t.Context(), "local", id, req)
	if err != nil || replayed.Instance.ID != out.Instance.ID {
		t.Fatal("lost adjustment response created another offer", err)
	}
	after, _ := s.store.Instance(t.Context(), "local", id)
	if !reflect.DeepEqual(before, after) || host.calls != 1 || host.ticket != before.Progress.TicketID || out.Instance.Progress.TicketID != "" {
		t.Fatal("proposal changed current contract or actual work")
	}
	definition := out.Instance.Definition
	if definition.ReplacesID != id || definition.ReplacesRevision != before.Revision || definition.InputLimit != MaxDraftBytes/2 || !reflect.DeepEqual(definition.Steps, before.Definition.Steps) || definition.Personalized {
		t.Fatalf("smaller contract: %+v", definition)
	}
	owner, _ := s.store.Owner(t.Context(), "local")
	decision := DecisionRequest{MutationRequest: MutationRequest{OperationID: "accept-replacement", ExpectedRevision: owner.Revision}, InstanceRevision: out.Instance.Revision, Decision: "accept"}
	if _, err := s.store.db.ExecContext(t.Context(), `CREATE TRIGGER fail_replacement BEFORE INSERT ON learning_operations WHEN NEW.id='accept-replacement' BEGIN SELECT RAISE(ABORT,'forced'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Decide(t.Context(), "local", out.Instance.ID, decision); err == nil {
		t.Fatal("forced commit failure ignored")
	}
	current, _ := s.store.Instance(t.Context(), "local", id)
	preview, _ := s.store.Instance(t.Context(), "local", out.Instance.ID)
	if !reflect.DeepEqual(before, current) || preview.State != Offered {
		t.Fatal("failed replacement retired current work")
	}
	if _, err := s.store.db.ExecContext(t.Context(), `DROP TRIGGER fail_replacement`); err != nil {
		t.Fatal(err)
	}
	accepted, err := s.Decide(t.Context(), "local", out.Instance.ID, decision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Decide(t.Context(), "local", out.Instance.ID, decision); err != nil {
		t.Fatal(err)
	}
	current, _ = s.store.Instance(t.Context(), "local", id)
	if current.State != Stopped || !reflect.DeepEqual(current.Definition, before.Definition) || current.Progress.TicketID != before.Progress.TicketID || accepted.Instance.State != Active || host.calls != 1 {
		t.Fatal("acceptance rewrote a saved contract or touched actual work")
	}
	owner, _ = s.store.Owner(t.Context(), "local")
	_, err = s.PrepareWork(t.Context(), "local", accepted.Instance.ID, WorkRequest{MutationRequest: MutationRequest{OperationID: "oversized", ExpectedRevision: owner.Revision}, InstanceRevision: accepted.Instance.Revision, Input: strings.Repeat("x", definition.InputLimit+1), Assignee: "Ori"})
	if !errors.Is(err, ErrInvalid) || host.calls != 1 {
		t.Fatal("smaller limit was prose only", err)
	}
}

func TestAdjustmentDiscardAndStopNeverLeaveAnOrphanedReplacement(t *testing.T) {
	s, _, id, _ := workFixture(t)
	out, err := s.Adjust(t.Context(), "local", id, adjustmentRequest(t, s, id, "smaller", "smaller"))
	if err != nil {
		t.Fatal(err)
	}
	owner, _ := s.store.Owner(t.Context(), "local")
	if _, err := s.Decide(t.Context(), "local", out.Instance.ID, DecisionRequest{MutationRequest: MutationRequest{OperationID: "keep-current", ExpectedRevision: owner.Revision}, InstanceRevision: 1, Decision: "discard"}); err != nil {
		t.Fatal(err)
	}
	owner, _ = s.store.Owner(t.Context(), "local")
	current, _ := s.store.Instance(t.Context(), "local", id)
	if current.State != Active || current.Revision != 2 || len(owner.Preferences.Exclusions) != 0 {
		t.Fatal("discard declined the current contract or invented a preference")
	}
	out, err = s.Adjust(t.Context(), "local", id, adjustmentRequest(t, s, id, "again", "smaller"))
	if err != nil {
		t.Fatal(err)
	}
	owner, _ = s.store.Owner(t.Context(), "local")
	if _, err := s.Decide(t.Context(), "local", id, DecisionRequest{MutationRequest: MutationRequest{OperationID: "stop", ExpectedRevision: owner.Revision}, InstanceRevision: current.Revision, Decision: "stop"}); err != nil {
		t.Fatal(err)
	}
	preview, _ := s.store.Instance(t.Context(), "local", out.Instance.ID)
	if preview.State != Invalidated || preview.Progress.Blocker != "replacement_changed" {
		t.Fatal("stopped parent left an actionable orphan")
	}
}

func TestAdjustmentThinContextAndStaleTabsPreserveSavedContract(t *testing.T) {
	s, host, id, _ := workFixture(t)
	req := adjustmentRequest(t, s, id, "alternative", "alternative")
	if _, err := s.Adjust(t.Context(), "local", id, req); !errors.Is(err, ErrUnavailable) {
		t.Fatal("same candidate repackaged as another option", err)
	}
	owner, _ := s.store.Owner(t.Context(), "local")
	if owner.Revision != req.ExpectedRevision || host.calls != 0 {
		t.Fatal("thin context caused effects")
	}
	req.Kind, req.ExpectedRevision = "smaller", owner.Revision-1
	if _, err := s.Adjust(t.Context(), "local", id, req); !errors.Is(err, ErrConflict) {
		t.Fatal("stale tab replaced work", err)
	}
	req.ExpectedRevision = owner.Revision
	out, err := s.Adjust(t.Context(), "local", id, req)
	if err != nil {
		t.Fatal(err)
	}
	owner, _ = s.store.Owner(t.Context(), "local")
	if _, err := s.PrepareWork(t.Context(), "local", id, WorkRequest{MutationRequest: MutationRequest{OperationID: "old-work", ExpectedRevision: owner.Revision}, InstanceRevision: 2, Input: "Work on the original", Assignee: "Ori"}); err != nil {
		t.Fatal(err)
	}
	owner, _ = s.store.Owner(t.Context(), "local")
	if _, err := s.Decide(t.Context(), "local", out.Instance.ID, DecisionRequest{MutationRequest: MutationRequest{OperationID: "stale-accept", ExpectedRevision: owner.Revision}, InstanceRevision: 1, Decision: "accept"}); !errors.Is(err, ErrConflict) {
		t.Fatal("changed active work was stopped by stale preview", err)
	}
	if visible := s.visibleInstance(t.Context(), "local", *out.Instance); visible.Progress.Blocker != "replacement_changed" {
		t.Fatal("stale preview remained actionable")
	}
}

type otherScopeSource struct{ sourceStub }

func (s otherScopeSource) Candidate(ctx context.Context, owner, workspace string) (Candidate, error) {
	c, err := s.sourceStub.Candidate(ctx, owner, "workspace")
	c.WorkspaceID, c.Ref, c.ScopeDigest = workspace, "candidate-"+workspace, digest([]byte(workspace))
	return c, err
}
func (s otherScopeSource) Validate(_ context.Context, _ string, d Definition) error {
	if d.ScopeDigest != digest([]byte(d.WorkspaceID)) {
		return ErrUnavailable
	}
	return nil
}

func TestAdjustmentExplicitOtherWorkspaceRespectsExclusionsAndNeverCallsDesigner(t *testing.T) {
	s, _ := testService(t)
	s.source = otherScopeSource{}
	if _, err := s.store.db.ExecContext(t.Context(), `INSERT INTO workspaces(id,name,created_at,updated_at) VALUES('other','Other',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	original := propose(t, s)
	designer, _ := installDesigner(s)
	req := adjustmentRequest(t, s, original.Instance.ID, "other", "alternative")
	req.WorkspaceID = "other"
	out, err := s.Adjust(t.Context(), "local", original.Instance.ID, req)
	if err != nil {
		t.Fatal(err)
	}
	old, _ := s.store.Instance(t.Context(), "local", original.Instance.ID)
	if old.State != Invalidated || out.Instance.Definition.WorkspaceID != "other" || out.Instance.Definition.ReplacesID != "" || designer.calls.Load() != 0 {
		t.Fatal("alternative changed scope silently, called a model, or kept two offers")
	}
	owner, _ := s.store.Owner(t.Context(), "local")
	if _, err := s.store.Change(t.Context(), "local", owner.Revision, owner.Epoch, false, func(m *Mutation) error {
		m.owner.Preferences.Exclusions = []RecommendationExclusion{{WorkspaceID: "workspace", Structure: DelegateInspect}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	req = adjustmentRequest(t, s, out.Instance.ID, "suppressed", "alternative")
	req.WorkspaceID = "workspace"
	if _, err := s.Adjust(t.Context(), "local", out.Instance.ID, req); !errors.Is(err, ErrUnavailable) {
		t.Fatal("alternative bypassed a durable decline", err)
	}
}

package learningmissions

import (
	"strings"
	"testing"
)

func TestUnsupportedDefinitionsRemainOpaqueAndCanOnlyRetireWithoutReencoding(t *testing.T) {
	for _, state := range []State{Offered, Active} {
		t.Run(string(state), func(t *testing.T) {
			s, db := testService(t)
			offer := propose(t, s)
			if state == Active {
				if _, err := s.Decide(t.Context(), "local", offer.Instance.ID, DecisionRequest{MutationRequest: MutationRequest{OperationID: "accept", ExpectedRevision: 1}, InstanceRevision: 1, Decision: "accept"}); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := db.ExecContext(t.Context(), `UPDATE learning_instances SET definition=json_set(definition,'$.version',2,'$.goal',json('{"FUTURE_PRIVATE":"preserve opaque"}')),progress=json_set(progress,'$.input',json('{"FUTURE_INPUT":"never decode"}')) WHERE id=?`, offer.Instance.ID); err != nil {
				t.Fatal(err)
			}
			var definition, progress string
			if err := db.QueryRowContext(t.Context(), `SELECT definition,progress FROM learning_instances WHERE id=?`, offer.Instance.ID).Scan(&definition, &progress); err != nil {
				t.Fatal(err)
			}
			status, err := s.Status(t.Context(), "local")
			if err != nil || len(status.Instances) != 1 || status.Instances[0].Progress.Blocker != "definition_unavailable" || strings.Contains(status.Instances[0].Definition.Goal, "FUTURE") || status.Instances[0].Progress.Input != "" {
				t.Fatal("unsupported private schema decoded or hidden history lost", status, err)
			}
			instance := status.Instances[0]
			for _, decision := range []string{"accept", "defer", "decline"} {
				if _, err := s.Decide(t.Context(), "local", instance.ID, DecisionRequest{MutationRequest: MutationRequest{OperationID: "bad-" + decision, ExpectedRevision: status.Owner.Revision}, InstanceRevision: instance.Revision, Decision: decision}); err == nil {
					t.Fatal("unsupported definition used as current authority", decision)
				}
			}
			out, err := s.Decide(t.Context(), "local", instance.ID, DecisionRequest{MutationRequest: MutationRequest{OperationID: "retire", ExpectedRevision: status.Owner.Revision}, InstanceRevision: instance.Revision, Decision: "stop"})
			want := Stopped
			if state == Offered {
				want = Invalidated
			}
			if err != nil || out.Instance == nil || out.Instance.State != want {
				t.Fatal("could not retire unsupported contract", out, err)
			}
			var afterDefinition, afterProgress string
			if err := db.QueryRowContext(t.Context(), `SELECT definition,progress FROM learning_instances WHERE id=?`, instance.ID).Scan(&afterDefinition, &afterProgress); err != nil {
				t.Fatal(err)
			}
			if afterDefinition != definition || afterProgress != progress {
				t.Fatal("retirement reencoded future private fields")
			}
			owner, _ := s.store.Owner(t.Context(), "local")
			if _, err := s.Propose(t.Context(), "local", ProposalRequest{MutationRequest: MutationRequest{OperationID: "new-current-practice", ExpectedRevision: owner.Revision}, WorkspaceID: "workspace"}); err != nil {
				t.Fatal("retired contract still occupied learning", err)
			}
		})
	}
}

func TestPreferenceClearCanInvalidateUnsupportedOffersWithoutRemovingHistory(t *testing.T) {
	s, db := testService(t)
	offer := propose(t, s)
	if _, err := db.ExecContext(t.Context(), `UPDATE learning_instances SET definition=json_set(definition,'$.support_version',2) WHERE id=?`, offer.Instance.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClearPreferences(t.Context(), "local", MutationRequest{OperationID: "clear-preferences", ExpectedRevision: 1}); err != nil {
		t.Fatal(err)
	}
	status, err := s.Status(t.Context(), "local")
	if err != nil || len(status.Instances) != 1 || status.Instances[0].State != Invalidated || status.Instances[0].Progress.Blocker != "definition_unavailable" {
		t.Fatal("preference clear required executing/erasing unsupported history", status, err)
	}
}

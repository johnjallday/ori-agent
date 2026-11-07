package learningmissions

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestDiagnosticsReadsOnlyOwnedMetadataAndHistoryClearRetainsCallAccounting(t *testing.T) {
	s, db, instance := reviewFixture(t)
	prepared := prepareReview(t, s, instance)
	if _, err := s.ReviewResult(t.Context(), "local", instance.ID, ReviewRequest{MutationRequest: MutationRequest{OperationID: "review", ExpectedRevision: 3}, InstanceRevision: 3,
		Action: "submit", ReviewID: prepared.Review.ID, Decision: "reject", Reason: "incorrect", Note: "NEVER_INCLUDE_PRIVATE_NOTE"}); err != nil {
		t.Fatal(err)
	}
	owner, _ := s.store.Owner(t.Context(), "local")
	known := 15
	_, err := s.store.Change(t.Context(), "local", owner.Revision, owner.Epoch, false, func(m *Mutation) error {
		for index, actual := range []*int{&known, nil} {
			if err := m.InsertOperation(Operation{ID: fmt.Sprintf("design-%d", index), Kind: "generate", Status: "unknown", Epoch: owner.Epoch, RequestDigest: digest([]byte(fmt.Sprint(index))), CreatedAt: s.now().UnixMilli(), ReservedTokens: GenerationReservedTokens, ActualTokens: actual}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// Unrelated private fields cannot be decoded merely to display counters.
	if _, err := db.ExecContext(t.Context(), `UPDATE learning_reviews SET record=json_set(record,'$.note',json('{"NEVER_DECODE_PRIVATE_NOTE":true}')) WHERE owner_id='local'`); err != nil {
		t.Fatal(err)
	}
	s.source = nil
	owner, _ = s.store.Owner(t.Context(), "local")
	out, err := s.Diagnostics(t.Context(), "local")
	if err != nil || out.Coverage != "available" || out.Offers != 1 || out.AcceptedOffers != 1 || out.Decisions["reject"] != 1 || out.DesignCalls != 2 || out.KnownTokens != 15 || out.UnknownUsageCalls != 1 || out.ReservedTokens != 2*GenerationReservedTokens {
		t.Fatalf("diagnostics: %+v %v", out, err)
	}
	data, _ := json.Marshal(out)
	if strings.Contains(string(data), "NEVER_") || strings.Contains(string(data), instance.ID) || strings.Contains(string(data), prepared.Review.ID) || len(out.Practice.Items[0].EvidenceKeys) != 0 {
		t.Fatal("diagnostics exposed source content/identities")
	}
	foreign, err := s.Diagnostics(t.Context(), "foreign")
	if err != nil || foreign.Offers != 0 || foreign.DesignCalls != 0 || foreign.Decisions["reject"] != 0 {
		t.Fatal("foreign counters leaked", err)
	}
	after, _ := s.store.Owner(t.Context(), "local")
	if after.Revision != owner.Revision {
		t.Fatal("read mutated the owner")
	}
	if _, err := s.ClearHistory(t.Context(), "local", MutationRequest{OperationID: "clear", ExpectedRevision: owner.Revision}); err != nil {
		t.Fatal(err)
	}
	cleared, err := s.Diagnostics(t.Context(), "local")
	if err != nil || cleared.Offers != 0 || cleared.Decisions["reject"] != 0 || cleared.Practice.Items[0].Attempts != 0 || cleared.DesignCalls != 2 || cleared.UnknownUsageCalls != 1 {
		t.Fatal("clear erased recent budget accounting or retained learning history", err)
	}
}

func TestDiagnosticsBoundsCoverageAndNeverEnrollsAnOwner(t *testing.T) {
	s, _ := testService(t)
	out, err := s.Diagnostics(t.Context(), "local")
	if err != nil || out.Coverage != "available" || out.DesignCalls != 0 {
		t.Fatal(err)
	}
	owner, _ := s.store.Owner(t.Context(), "local")
	if owner.Revision != 0 {
		t.Fatal("diagnostics enrolled owner")
	}
	_, err = s.store.Change(t.Context(), "local", 0, owner.Epoch, true, func(m *Mutation) error {
		for n := range MaxDiagnosticRows + 1 {
			if err := m.InsertOperation(Operation{ID: fmt.Sprintf("design-%d", n), Kind: "generate", Status: "failed", Epoch: m.owner.Epoch, RequestDigest: digest([]byte(fmt.Sprint(n))), CreatedAt: s.now().UnixMilli(), ReservedTokens: GenerationReservedTokens}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	out, err = s.Diagnostics(t.Context(), "local")
	if err != nil || out.Coverage != "partial" || out.DesignCalls != MaxDiagnosticRows || out.UnknownUsageCalls != MaxDiagnosticRows {
		t.Fatal("unbounded or falsely complete diagnostics", out, err)
	}
}

package personalassistant

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

func TestSQLiteStore_HQSetupLifecycleTransitions(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestStore(t)

	// Hire creates the profile-only relationship.
	awaiting := awaitingHQTestState("user-a", "assistant-a")
	created, err := store.CreateState(ctx, awaiting)
	if err != nil {
		t.Fatalf("CreateState(awaiting_hq): %v", err)
	}
	if created.Status != StatusAwaitingHQ || created.HQWorkspaceID != "" || created.StateVersion != 1 {
		t.Fatalf("created = %#v", created)
	}

	// The user confirms Build My HQ: the operation claim is written before any
	// creation, so a restart here still resumes the same request.
	claim := created.Clone()
	claim.Status = StatusProvisioningHQ
	claim.LastHQRequestID = "hq-req-1"
	claim.HQPayloadHash = "hash-1"
	claim.HQPayloadJSON = `{"hq_name":"Personal HQ"}`
	claimed, err := store.UpdateState(ctx, claim, created.StateVersion)
	if err != nil {
		t.Fatalf("claim hq operation: %v", err)
	}
	if claimed.StateVersion != 2 || claimed.LastHQRequestID != "hq-req-1" || claimed.HQPayloadJSON == "" {
		t.Fatalf("claimed = %#v", claimed)
	}

	// First safe checkpoint: the canonical workspace and entry instance land.
	checkpoint := claimed.Clone()
	checkpoint.HQWorkspaceID = "ws-1"
	checkpoint.HQEntryAgentInstanceID = "inst-1"
	checkpoint.RepairStep = RepairDesignation
	saved, err := store.UpdateState(ctx, checkpoint, claimed.StateVersion)
	if err != nil {
		t.Fatalf("persist hq checkpoint: %v", err)
	}
	if saved.HQWorkspaceID != "ws-1" || saved.RepairStep != RepairDesignation || saved.StateVersion != 3 {
		t.Fatalf("checkpoint = %#v", saved)
	}

	// Activation reduces the provisional payload to its receipt.
	final := saved.Clone()
	final.Status = StatusActive
	final.RepairStep = RepairNone
	active, err := store.UpdateState(ctx, final, saved.StateVersion)
	if err != nil {
		t.Fatalf("activate relationship: %v", err)
	}
	if active.Status != StatusActive || active.HQPayloadJSON != "" {
		t.Fatalf("activation kept a duplicate daily brief payload: %#v", active)
	}
	if active.LastHQRequestID != "hq-req-1" || active.HQPayloadHash != "hash-1" {
		t.Fatalf("activation dropped the replay receipt: %#v", active)
	}
	if err := active.ValidateStateInvariants(); err != nil {
		t.Fatalf("activated state violates invariants: %v", err)
	}
}

func TestSQLiteStore_RejectsMalformedHQOperationFields(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestStore(t)

	tests := []struct {
		name   string
		mutate func(*State)
	}{
		{"payload without a claimed request id", func(s *State) {
			s.HQPayloadJSON = `{"hq_name":"Personal HQ"}`
		}},
		{"hash without a claimed request id", func(s *State) {
			s.HQPayloadHash = "hash-1"
		}},
		{"non-json payload", func(s *State) {
			s.LastHQRequestID = "hq-req-1"
			s.HQPayloadJSON = "not json"
		}},
		{"oversized payload", func(s *State) {
			s.LastHQRequestID = "hq-req-1"
			s.HQPayloadJSON = `"` + strings.Repeat("x", MaxAssignmentJSONBytes) + `"`
		}},
		{"unbounded request id", func(s *State) {
			s.LastHQRequestID = strings.Repeat("r", 201)
		}},
		{"unknown repair step", func(s *State) {
			s.RepairStep = RepairStep("provider timeout: dial tcp")
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state := awaitingHQTestState("user-"+test.name, "assistant-"+test.name)
			test.mutate(state)
			if _, err := store.CreateState(ctx, state); err == nil {
				t.Fatal("malformed hq operation accepted")
			}
		})
	}
}

func TestSQLiteStore_RejectsIllegalSetupStageWrites(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestStore(t)

	tests := []struct {
		name   string
		mutate func(*State)
	}{
		{"awaiting_hq claiming a workspace", func(s *State) { s.HQWorkspaceID = "ws-1" }},
		{"awaiting_hq claiming an entry instance", func(s *State) {
			s.HQWorkspaceID = "ws-1"
			s.HQEntryAgentInstanceID = "inst-1"
		}},
		{"awaiting_hq with no owned profile", func(s *State) { s.GlobalAgentProfileName = "" }},
		{"provisioning_hq with no claimed request", func(s *State) { s.Status = StatusProvisioningHQ }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state := awaitingHQTestState("user-"+test.name, "assistant-"+test.name)
			test.mutate(state)
			if _, err := store.CreateState(ctx, state); err == nil {
				t.Fatal("store accepted an impossible setup-stage combination")
			}
		})
	}

	// Pre-amendment statuses keep their existing latitude: tightening the new
	// stages must not reject a row an older release could have written.
	legacy := activeTestState("user-legacy", "assistant-legacy")
	legacy.Status = StatusHiring
	legacy.HQWorkspaceID = "ws-legacy"
	legacy.HQEntryAgentInstanceID = ""
	if _, err := store.CreateState(ctx, legacy); err != nil {
		t.Fatalf("legacy hiring row rejected: %v", err)
	}
}

func TestSQLiteStore_HQRepairStepsStayClosed(t *testing.T) {
	for _, step := range []RepairStep{
		RepairNone, RepairProfileCreation, RepairHQCreation,
		RepairDesignation, RepairDailyBriefConfig, RepairFinalization,
	} {
		if _, err := NormalizeRepairStep(string(step)); err != nil {
			t.Fatalf("NormalizeRepairStep(%q): %v", step, err)
		}
	}
	for _, raw := range []string{"workspace_creation", "sql: no rows in result set", "hq"} {
		if _, err := NormalizeRepairStep(raw); err == nil {
			t.Fatalf("NormalizeRepairStep(%q) accepted an open value", raw)
		}
	}
}

func TestSQLiteStore_StaleHQClaimLosesCompareAndSwap(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestStore(t)
	created, err := store.CreateState(ctx, awaitingHQTestState("user-a", "assistant-a"))
	if err != nil {
		t.Fatalf("CreateState: %v", err)
	}

	first := created.Clone()
	first.Status = StatusProvisioningHQ
	first.LastHQRequestID = "hq-req-1"
	if _, err := store.UpdateState(ctx, first, created.StateVersion); err != nil {
		t.Fatalf("first claim: %v", err)
	}

	// A second submit still holding the pre-claim version must not overwrite it.
	second := created.Clone()
	second.Status = StatusProvisioningHQ
	second.LastHQRequestID = "hq-req-2"
	if _, err := store.UpdateState(ctx, second, created.StateVersion); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale hq claim error = %v; want ErrConflict", err)
	}

	loaded, err := store.GetState(ctx, "user-a")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if loaded.LastHQRequestID != "hq-req-1" {
		t.Fatalf("stale write won the race: %q", loaded.LastHQRequestID)
	}
}

func TestSQLiteStore_ConcurrentHQClaimsProduceOneWinner(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestStore(t)
	created, err := store.CreateState(ctx, awaitingHQTestState("user-a", "assistant-a"))
	if err != nil {
		t.Fatalf("CreateState: %v", err)
	}

	var wg sync.WaitGroup
	results := make([]error, 4)
	for i := range results {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			claim := created.Clone()
			claim.Status = StatusProvisioningHQ
			claim.LastHQRequestID = "hq-req-" + string(rune('a'+index))
			_, results[index] = store.UpdateState(ctx, claim, created.StateVersion)
		}(i)
	}
	wg.Wait()

	winners := 0
	for _, err := range results {
		switch {
		case err == nil:
			winners++
		case errors.Is(err, ErrConflict):
		default:
			t.Fatalf("unexpected concurrent error: %v", err)
		}
	}
	if winners != 1 {
		t.Fatalf("concurrent hq claims produced %d winners", winners)
	}
	loaded, err := store.GetState(ctx, "user-a")
	if err != nil || loaded.StateVersion != 2 {
		t.Fatalf("state after race = %#v, %v", loaded, err)
	}
}

func TestSQLiteStore_HQOperationIsolatedPerUser(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestStore(t)

	a := awaitingHQTestState("user-a", "assistant-a")
	a.Status = StatusProvisioningHQ
	a.LastHQRequestID = "hq-req-a"
	a.HQPayloadHash = "hash-a"
	if _, err := store.CreateState(ctx, a); err != nil {
		t.Fatalf("CreateState(user-a): %v", err)
	}
	if _, err := store.CreateState(ctx, awaitingHQTestState("user-b", "assistant-b")); err != nil {
		t.Fatalf("CreateState(user-b): %v", err)
	}

	other, err := store.GetState(ctx, "user-b")
	if err != nil {
		t.Fatalf("GetState(user-b): %v", err)
	}
	if other.LastHQRequestID != "" || other.HQPayloadHash != "" || other.Status != StatusAwaitingHQ {
		t.Fatalf("another user's hq operation leaked: %#v", other)
	}

	// Defensive copies: mutating a returned state must not reach storage.
	other.LastHQRequestID = "hq-req-a"
	reloaded, err := store.GetState(ctx, "user-b")
	if err != nil {
		t.Fatalf("GetState(user-b) reload: %v", err)
	}
	if reloaded.LastHQRequestID != "" {
		t.Fatal("returned state aliased stored data")
	}
}

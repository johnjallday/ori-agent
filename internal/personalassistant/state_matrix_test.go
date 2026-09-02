package personalassistant

import (
	"context"
	"testing"
	"time"
)

// awaitingHQTestState is the canonical post-hire, pre-HQ fixture: one owned
// global profile, a hire timestamp, and deliberately no HQ identifiers.
func awaitingHQTestState(userID, assistantID string) *State {
	state := NewState(userID)
	state.AssistantID = assistantID
	state.Status = StatusAwaitingHQ
	state.DisplayName = "Atlas"
	state.GlobalAgentProfileName = "Atlas"
	state.Mandate = "Keep the important work visible."
	state.FocusAreas = []FocusArea{FocusPlanMyDay}
	state.FirstAssignmentStatus = FirstAssignmentNotStarted
	state.LastHireRequestID = "hire-req-1"
	state.StateVersion = 4
	hiredAt := time.Now().UTC().Round(0)
	state.HiredAt = &hiredAt
	return state
}

func TestNormalizeRelationshipStatus_ClosedEnumIncludesSetupStages(t *testing.T) {
	for _, raw := range []string{"awaiting_hq", "AWAITING_HQ", " provisioning_hq "} {
		status, err := NormalizeRelationshipStatus(raw)
		if err != nil {
			t.Fatalf("NormalizeRelationshipStatus(%q): %v", raw, err)
		}
		if status != StatusAwaitingHQ && status != StatusProvisioningHQ {
			t.Fatalf("NormalizeRelationshipStatus(%q) = %q", raw, status)
		}
	}
	for _, raw := range []string{"needs_hq", "awaiting-hq", "hq", "building_hq"} {
		if _, err := NormalizeRelationshipStatus(raw); err == nil {
			t.Fatalf("NormalizeRelationshipStatus(%q) accepted a non-durable value", raw)
		}
	}
}

func TestRelationshipStatus_ProfileAndHQRequirements(t *testing.T) {
	for _, status := range []RelationshipStatus{StatusAwaitingHQ, StatusProvisioningHQ, StatusActive, StatusPaused} {
		if !status.HasOwnedProfile() {
			t.Fatalf("%s should guarantee an owned profile", status)
		}
	}
	for _, status := range []RelationshipStatus{StatusNotHired, StatusHiring, StatusRepairNeeded} {
		if status.HasOwnedProfile() {
			t.Fatalf("%s must not guarantee an owned profile", status)
		}
	}
	// Only a completed relationship requires the full HQ linkage. The pre-HQ
	// stages are healthy without it — that is the whole point of the amendment.
	for _, status := range []RelationshipStatus{StatusAwaitingHQ, StatusProvisioningHQ} {
		if status.RequiresHQ() {
			t.Fatalf("%s must not require hq linkage", status)
		}
	}
	if !StatusActive.RequiresHQ() || !StatusPaused.RequiresHQ() {
		t.Fatal("active/paused must require hq linkage")
	}
}

func TestValidateStateInvariants_LegalCombinations(t *testing.T) {
	tests := []struct {
		name  string
		build func() *State
	}{
		{"fresh not_hired", func() *State { return NewState("local") }},
		{"awaiting_hq without hq ids", func() *State { return awaitingHQTestState("local", "assistant-a") }},
		{"provisioning_hq claim only", func() *State {
			state := awaitingHQTestState("local", "assistant-a")
			state.Status = StatusProvisioningHQ
			state.LastHQRequestID = "hq-req-1"
			return state
		}},
		{"provisioning_hq after workspace checkpoint", func() *State {
			state := awaitingHQTestState("local", "assistant-a")
			state.Status = StatusProvisioningHQ
			state.LastHQRequestID = "hq-req-1"
			state.HQWorkspaceID = "hq-local"
			return state
		}},
		{"active with complete linkage", func() *State { return activeTestState("local", "assistant-a") }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.build().ValidateStateInvariants(); err != nil {
				t.Fatalf("legal combination rejected: %v", err)
			}
		})
	}
}

func TestValidateStateInvariants_RejectsMalformedCombinations(t *testing.T) {
	tests := []struct {
		name  string
		build func() *State
	}{
		{"awaiting_hq carrying an hq workspace", func() *State {
			state := awaitingHQTestState("local", "assistant-a")
			state.HQWorkspaceID = "hq-local"
			return state
		}},
		{"awaiting_hq carrying an entry instance", func() *State {
			state := awaitingHQTestState("local", "assistant-a")
			state.HQEntryAgentInstanceID = "instance-local"
			return state
		}},
		{"awaiting_hq without an owned profile", func() *State {
			state := awaitingHQTestState("local", "assistant-a")
			state.GlobalAgentProfileName = "  "
			return state
		}},
		{"awaiting_hq without a hire timestamp", func() *State {
			state := awaitingHQTestState("local", "assistant-a")
			state.HiredAt = nil
			return state
		}},
		{"provisioning_hq without a claimed request", func() *State {
			state := awaitingHQTestState("local", "assistant-a")
			state.Status = StatusProvisioningHQ
			return state
		}},
		{"not_hired carrying a durable result", func() *State {
			state := NewState("local")
			state.GlobalAgentProfileName = "Atlas"
			return state
		}},
		{"active missing the entry instance", func() *State {
			state := activeTestState("local", "assistant-a")
			state.HQEntryAgentInstanceID = ""
			return state
		}},
		{"entry instance without a workspace", func() *State {
			state := activeTestState("local", "assistant-a")
			state.HQWorkspaceID = ""
			return state
		}},
		{"workspace without an owned profile", func() *State {
			state := activeTestState("local", "assistant-a")
			state.GlobalAgentProfileName = ""
			return state
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.build().ValidateStateInvariants(); err == nil {
				t.Fatal("malformed combination accepted")
			}
		})
	}
}

// fakeProfileReader is the narrow provenance seam: it answers ownership and
// nothing else.
type fakeProfileReader struct {
	profiles map[string]ProfileProvenance
	reads    int
}

func ownedProfileReader() *fakeProfileReader {
	const name = "Atlas"
	const assistantID = "assistant-a"
	return &fakeProfileReader{profiles: map[string]ProfileProvenance{
		name: {Name: name, AssistantID: assistantID, HireRequestID: "hire-req-1"},
	}}
}

func (f *fakeProfileReader) PersonalAssistantProfileProvenance(name string) (ProfileProvenance, bool) {
	f.reads++
	provenance, ok := f.profiles[name]
	return provenance, ok
}

func TestServiceGet_AwaitingHQProjectsNamedButUnbuilt(t *testing.T) {
	store := &readTrackingStore{state: awaitingHQTestState("local", "assistant-a")}
	hq := &fakeHQReader{}
	briefs := &fakeBriefReader{}
	service := NewService(store, hq, briefs,
		fakeModelReader{availability: SourceAvailability{Available: true, Status: AvailabilityAvailable}}).
		WithProfileReader(ownedProfileReader())

	projection, err := service.Get(context.Background(), "local")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if projection.State != APIStateNeedsHQ || projection.NextAction != NextActionBuildHQ {
		t.Fatalf("state=%q next_action=%q", projection.State, projection.NextAction)
	}
	// The hire is real, so the identity is real.
	if projection.AssistantID != "assistant-a" || projection.DisplayName != "Atlas" ||
		projection.GlobalAgentProfile != "Atlas" || projection.Appearance == nil ||
		projection.Mandate == "" || projection.StateVersion != 4 {
		t.Fatalf("named identity not projected: %#v", projection)
	}
	// Nothing HQ-shaped may be advertised before the user confirms Build My HQ.
	if projection.HQWorkspaceID != "" || projection.HQAgentInstanceID != "" || projection.DailyBrief != nil {
		t.Fatalf("pre-HQ projection advertised hq records: %#v", projection)
	}
	for name, source := range map[string]SourceAvailability{
		"personal_hq":    projection.Availability.PersonalHQ,
		"agent_instance": projection.Availability.AgentInstance,
		"daily_brief":    projection.Availability.DailyBrief,
	} {
		if source.Status != AvailabilityNotConfigured || source.Reason != ReasonHQNotBuilt {
			t.Fatalf("%s = %#v; want not_configured/%s", name, source, ReasonHQNotBuilt)
		}
		if source.Available {
			t.Fatalf("%s reported available before HQ exists", name)
		}
	}
	// A missing HQ is not a reason to touch the HQ or brief stores.
	if hq.reads != 0 || briefs.reads != 0 || store.mutationHit {
		t.Fatalf("pre-HQ read touched canonical sources: hq=%d briefs=%d mutated=%v", hq.reads, briefs.reads, store.mutationHit)
	}
}

func TestServiceGet_ProvisioningHQProjectsResumable(t *testing.T) {
	state := awaitingHQTestState("local", "assistant-a")
	state.Status = StatusProvisioningHQ
	state.LastHQRequestID = "hq-req-1"
	store := &readTrackingStore{state: state}
	service := NewService(store, &fakeHQReader{}, &fakeBriefReader{},
		fakeModelReader{availability: SourceAvailability{Available: true, Status: AvailabilityAvailable}}).
		WithProfileReader(ownedProfileReader())

	projection, err := service.Get(context.Background(), "local")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if projection.State != APIStateProvisioningHQ || projection.NextAction != NextActionResumeHQ {
		t.Fatalf("state=%q next_action=%q", projection.State, projection.NextAction)
	}
	if projection.Availability.PersonalHQ.Reason != ReasonHQSetupIncomplete {
		t.Fatalf("personal_hq = %#v", projection.Availability.PersonalHQ)
	}
	if projection.DisplayName != "Atlas" {
		t.Fatal("resumable setup dropped the hired identity")
	}
}

func TestServiceGet_MalformedPreHQStateIsBoundedRepairNotFabrication(t *testing.T) {
	state := awaitingHQTestState("local", "assistant-a")
	state.HQWorkspaceID = "hq-local" // impossible before Build My HQ is confirmed
	store := &readTrackingStore{state: state}
	service := NewService(store, &fakeHQReader{}, &fakeBriefReader{},
		fakeModelReader{availability: SourceAvailability{Available: true, Status: AvailabilityAvailable}}).
		WithProfileReader(ownedProfileReader())

	projection, err := service.Get(context.Background(), "local")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if projection.State != APIStateRepairNeeded || projection.NextAction != NextActionRepair {
		t.Fatalf("state=%q next_action=%q", projection.State, projection.NextAction)
	}
	if projection.HQWorkspaceID != "" || projection.Mandate != "" || projection.FocusAreas != nil {
		t.Fatalf("repair projection leaked an unverified hq: %#v", projection)
	}
}

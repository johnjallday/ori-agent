package workspace

import (
	"encoding/json"
	"testing"
	"time"
)

func validWorkspaceRuntimeContract() *RuntimeRequirementsContract {
	return &RuntimeRequirementsContract{
		SchemaVersion: RuntimeRequirementsSchemaVersion,
		OperatingModes: []RuntimeOperatingMode{
			{ID: "file_only", Label: "File-only", Description: "Edit project files."},
			{ID: "assisted", Label: "Assisted", Description: "Use live control.", Requires: []string{"reaper_live_control"}},
		},
		Requirements: []RuntimeRequirement{{
			Key:         "reaper_live_control",
			Label:       "Local REAPER control",
			Description: "Control the open workspace project.",
			Disclosure:  "Uses loopback and the dedicated runner exchange.",
			Adapter:     "reaper_live_control",
		}},
	}
}

func TestRuntimeRequirementsContract_LookupAndImplicitMode(t *testing.T) {
	contract := validWorkspaceRuntimeContract()
	if !contract.StructurallyValid() {
		t.Fatal("fixture contract should be structurally valid")
	}
	mode, ok := contract.Mode(" Assisted ")
	if !ok || mode.ID != "assisted" || len(mode.Requires) != 1 {
		t.Fatalf("normalized mode lookup = %+v, %v", mode, ok)
	}
	mode.Requires[0] = "mutated"
	if contract.OperatingModes[1].Requires[0] != "reaper_live_control" {
		t.Fatal("Mode returned the contract's reference slice")
	}
	requirement, ok := contract.Requirement(" REAPER_LIVE_CONTROL ")
	if !ok || requirement.Adapter != "reaper_live_control" {
		t.Fatalf("normalized requirement lookup = %+v, %v", requirement, ok)
	}
	resolved, ok := contract.RequirementsForMode("assisted")
	if !ok || len(resolved) != 1 || resolved[0].Key != "reaper_live_control" {
		t.Fatalf("RequirementsForMode = %+v, %v", resolved, ok)
	}
	if _, ok := contract.ImplicitMode(); ok {
		t.Fatal("a multi-mode contract must not silently pick a default")
	}

	one := CloneRuntimeRequirementsContract(contract)
	one.OperatingModes = one.OperatingModes[:1]
	one.Requirements = nil
	implicit, ok := one.ImplicitMode()
	if !ok || implicit.ID != "file_only" {
		t.Fatalf("one mode should be implicit: %+v, %v", implicit, ok)
	}
}

func TestRuntimeRequirementsContract_CloneIsDeep(t *testing.T) {
	source := validWorkspaceRuntimeContract()
	clone := CloneRuntimeRequirementsContract(source)
	source.OperatingModes[1].Requires[0] = "changed"
	source.Requirements[0].Label = "Changed"
	if clone.OperatingModes[1].Requires[0] != "reaper_live_control" || clone.Requirements[0].Label != "Local REAPER control" {
		t.Fatalf("contract clone shares declaration data: %+v", clone)
	}
	clone.OperatingModes[1].Requires[0] = "changed-again"
	if source.OperatingModes[1].Requires[0] != "changed" {
		t.Fatal("mutating a clone reached its source")
	}
}

func TestRuntimeRequirementsContract_MalformedIdentifiersNeverResolve(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*RuntimeRequirementsContract)
	}{
		{name: "unsupported schema", mutate: func(c *RuntimeRequirementsContract) { c.SchemaVersion = 2 }},
		{name: "blank mode", mutate: func(c *RuntimeRequirementsContract) { c.OperatingModes[0].ID = " " }},
		{name: "duplicate mode", mutate: func(c *RuntimeRequirementsContract) { c.OperatingModes[1].ID = " FILE_ONLY " }},
		{name: "blank requirement", mutate: func(c *RuntimeRequirementsContract) { c.Requirements[0].Key = " " }},
		{name: "duplicate requirement", mutate: func(c *RuntimeRequirementsContract) { c.Requirements = append(c.Requirements, c.Requirements[0]) }},
		{name: "undeclared reference", mutate: func(c *RuntimeRequirementsContract) { c.OperatingModes[1].Requires[0] = "missing" }},
		{name: "duplicate reference", mutate: func(c *RuntimeRequirementsContract) {
			c.OperatingModes[1].Requires = []string{"reaper_live_control", " REAPER_LIVE_CONTROL "}
		}},
		{name: "path-shaped adapter", mutate: func(c *RuntimeRequirementsContract) { c.Requirements[0].Adapter = "../adapter" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			contract := validWorkspaceRuntimeContract()
			tc.mutate(contract)
			if contract.StructurallyValid() {
				t.Fatalf("malformed contract reported valid: %+v", contract)
			}
			if _, ok := contract.Mode("assisted"); ok {
				t.Fatal("a malformed contract activated a mode")
			}
			if _, ok := contract.Requirement("reaper_live_control"); ok {
				t.Fatal("a malformed contract activated a requirement")
			}
		})
	}
}

func TestNormalizeRuntimeConfigurationState_FailsSafe(t *testing.T) {
	cases := map[string]string{
		"":                RuntimeConfigurationNotStarted,
		" configured ":    RuntimeConfigurationConfigured,
		"needs_attention": RuntimeConfigurationNeedsAttention,
		"not_started":     RuntimeConfigurationNotStarted,
		"in_progress":     RuntimeConfigurationInProgress,
		"ready":           RuntimeConfigurationInProgress,
		"future_state":    RuntimeConfigurationInProgress,
	}
	for input, want := range cases {
		if got := NormalizeRuntimeConfigurationState(input); got != want {
			t.Errorf("NormalizeRuntimeConfigurationState(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestWorkspaceRuntimeState_SetGetClonesAndFailsSafe(t *testing.T) {
	ws := NewWorkspace(CreateWorkspaceParams{Name: "Runtime"})
	first := time.Date(2026, 2, 1, 12, 0, 0, 0, time.UTC)
	last := first.Add(time.Hour)
	revoked := last.Add(time.Hour)
	wantFirst, wantRevoked := first, revoked
	state := &WorkspaceRuntimeState{
		SelectedModeID: " Assisted ",
		RequirementStates: []RuntimeRequirementState{
			{RequirementKey: " REAPER_LIVE_CONTROL ", ConfigurationState: "future_ready", FirstVerifiedAt: &first, LastVerifiedAt: &last},
		},
		Grants: []RuntimeCapabilityGrant{{CapabilityKey: " REAPER_LIVE_CONTROL ", AgentInstanceID: " agent-1 ", GrantedAt: first, RevokedAt: &revoked}},
	}
	ws.SetRuntimeState(state)

	// Mutating caller-owned values after Set must not reach workspace state.
	state.RequirementStates[0].RequirementKey = "mutated"
	*state.RequirementStates[0].FirstVerifiedAt = time.Time{}
	state.Grants[0].CapabilityKey = "mutated"
	*state.Grants[0].RevokedAt = time.Time{}

	got := ws.GetRuntimeState()
	if got.SelectedModeID != "assisted" {
		t.Fatalf("selected mode = %q", got.SelectedModeID)
	}
	if len(got.RequirementStates) != 1 || got.RequirementStates[0].RequirementKey != "reaper_live_control" {
		t.Fatalf("requirement state not normalized/cloned: %+v", got.RequirementStates)
	}
	if got.RequirementStates[0].ConfigurationState != RuntimeConfigurationInProgress {
		t.Fatalf("unknown state became authoritative: %+v", got.RequirementStates[0])
	}
	if got.RequirementStates[0].FirstVerifiedAt == nil || !got.RequirementStates[0].FirstVerifiedAt.Equal(wantFirst) {
		t.Fatalf("verification history was shared or lost: %+v", got.RequirementStates[0])
	}
	if len(got.Grants) != 1 || got.Grants[0].CapabilityKey != "reaper_live_control" || got.Grants[0].AgentInstanceID != "agent-1" || got.Grants[0].RevokedAt == nil || !got.Grants[0].RevokedAt.Equal(wantRevoked) {
		t.Fatalf("grant not normalized/cloned: %+v", got.Grants)
	}
	if got.Grants[0].Active() {
		t.Fatal("a revoked grant must not be active")
	}

	// Reads are copies too.
	got.RequirementStates[0].ConfigurationState = RuntimeConfigurationConfigured
	got.Grants[0].RevokedAt = nil
	again := ws.GetRuntimeState()
	if again.RequirementStates[0].ConfigurationState != RuntimeConfigurationInProgress || again.Grants[0].RevokedAt == nil {
		t.Fatalf("GetRuntimeState exposed workspace-owned state: %+v", again)
	}
}

func TestWorkspaceRuntimeState_JSONRoundTrip(t *testing.T) {
	ws := NewWorkspace(CreateWorkspaceParams{Name: "Runtime"})
	verified := time.Date(2026, 2, 1, 12, 0, 0, 0, time.UTC)
	ws.SetRuntimeState(&WorkspaceRuntimeState{
		SelectedModeID: "assisted",
		RequirementStates: []RuntimeRequirementState{{
			RequirementKey:     "reaper_live_control",
			ConfigurationState: RuntimeConfigurationConfigured,
			FirstVerifiedAt:    &verified,
			LastVerifiedAt:     &verified,
		}},
		Grants: []RuntimeCapabilityGrant{{CapabilityKey: "reaper_live_control", AgentInstanceID: "agent-1", GrantedAt: verified}},
	})

	data, err := json.Marshal(ws)
	if err != nil {
		t.Fatalf("marshal workspace: %v", err)
	}
	var decoded Workspace
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal workspace: %v", err)
	}
	got := decoded.GetRuntimeState()
	if got == nil || got.SelectedModeID != "assisted" || len(got.RequirementStates) != 1 || len(got.Grants) != 1 {
		t.Fatalf("runtime state did not round-trip: %s / %+v", data, got)
	}
	if got.RequirementStates[0].ConfigurationState != RuntimeConfigurationConfigured || !got.Grants[0].Active() {
		t.Fatalf("runtime state changed across JSON: %+v", got)
	}
}

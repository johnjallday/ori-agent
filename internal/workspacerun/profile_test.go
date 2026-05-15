package workspacerun

import "testing"

func TestProfileRegistrySnapshotReturnsCopy(t *testing.T) {
	registry := NewProfileRegistry()

	snapshot, err := registry.Snapshot(ProfileEngineering)
	if err != nil {
		t.Fatalf("snapshot profile: %v", err)
	}
	if snapshot.ID != ProfileEngineering {
		t.Fatalf("snapshot ID = %q, want %q", snapshot.ID, ProfileEngineering)
	}

	snapshot.RequiredArtifacts[0] = ArtifactFile

	again, err := registry.Snapshot(ProfileEngineering)
	if err != nil {
		t.Fatalf("snapshot profile again: %v", err)
	}
	if again.RequiredArtifacts[0] != ArtifactChangedFiles {
		t.Fatalf("profile snapshot was not defensive-copied")
	}
}

func TestMergePolicyRequestOverridesScalarsAndLists(t *testing.T) {
	defaults := Policy{
		Mutation:        PolicyMutationDenied,
		Approval:        PolicyApprovalFinalOnly,
		ToolAllow:       []string{"mcp:read"},
		ToolDeny:        []string{"mcp:write"},
		ExternalEffects: PolicyExternalEffectsDenied,
	}
	request := Policy{
		Mutation:  PolicyMutationAllowed,
		ToolAllow: []string{"skill:git-*"},
	}

	got := MergePolicy(defaults, request)
	if got.Mutation != PolicyMutationAllowed {
		t.Fatalf("Mutation = %q, want %q", got.Mutation, PolicyMutationAllowed)
	}
	if got.Approval != PolicyApprovalFinalOnly {
		t.Fatalf("Approval = %q, want default %q", got.Approval, PolicyApprovalFinalOnly)
	}
	if len(got.ToolAllow) != 1 || got.ToolAllow[0] != "skill:git-*" {
		t.Fatalf("ToolAllow = %v, want request replacement", got.ToolAllow)
	}
	if len(got.ToolDeny) != 1 || got.ToolDeny[0] != "mcp:write" {
		t.Fatalf("ToolDeny = %v, want default", got.ToolDeny)
	}
}

func TestDefaultMVPProfileContracts(t *testing.T) {
	registry := NewProfileRegistry()

	general, err := registry.Snapshot(ProfileGeneral)
	if err != nil {
		t.Fatalf("snapshot general: %v", err)
	}
	if len(general.RequiredArtifacts) != 0 || len(general.Validation.RequiredChecks) != 0 {
		t.Fatalf("general profile should not require artifacts/checks: %+v", general)
	}

	engineering, err := registry.Snapshot(ProfileEngineering)
	if err != nil {
		t.Fatalf("snapshot engineering: %v", err)
	}
	if len(engineering.RequiredArtifacts) == 0 {
		t.Fatal("engineering profile should require change evidence")
	}
	if len(engineering.Validation.RequiredChecks) == 0 {
		t.Fatal("engineering profile should require validation contract checks")
	}
}

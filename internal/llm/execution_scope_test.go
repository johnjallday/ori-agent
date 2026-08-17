package llm

import "testing"

func TestCloneCLIExecutionScopeIsDefensive(t *testing.T) {
	source := &CLIExecutionScope{
		WorkspaceRoot:           "/workspace",
		AdditionalWritableRoots: []string{"/runner"},
		NetworkPosture:          CLINetworkCapabilityLocal,
		CapabilityKeys:          []string{"reaper_live_control"},
	}
	clone := CloneCLIExecutionScope(source)
	source.AdditionalWritableRoots[0] = "/outside"
	source.CapabilityKeys[0] = "other"
	if clone.WorkspaceRoot != "/workspace" || clone.AdditionalWritableRoots[0] != "/runner" || clone.CapabilityKeys[0] != "reaper_live_control" || clone.NetworkPosture != CLINetworkCapabilityLocal {
		t.Fatalf("execution scope clone changed/shared data: %+v", clone)
	}
	if CloneCLIExecutionScope(nil) != nil {
		t.Fatal("nil scope should remain nil")
	}
}

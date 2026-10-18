package workspace

import "testing"

func missionGateTask(workspaceID string, policy AutonomyPolicy) Task {
	return Task{WorkspaceID: workspaceID, Context: map[string]any{
		MissionTaskContextOriginKey: MissionTaskContextOriginValue,
		MissionTaskContextPolicyKey: string(policy),
	}}
}

// gateWorkspace saves a workspace with Ori's workspace-files binding (left
// unclassified, as Ori creates it) plus any extra bindings.
func gateWorkspace(t *testing.T, store Store, extra ...MCPBinding) *Workspace {
	t.Helper()
	ws := NewWorkspace(CreateWorkspaceParams{Name: "Gate"})
	ws.MCPBindings = append([]MCPBinding{{
		ID: "files", ServerName: "filesystem", Alias: WorkspaceFilesBindingAlias, Enabled: true,
	}}, extra...)
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}
	return ws
}

// SM4 / FR10: under Watch a goal run may read what the workspace owns and
// nothing else; under Propose it may also write inside the workspace;
// anything that reaches beyond the workspace or starts work is denied under
// both — whatever the workspace's binding defaults say.
func TestMissionGate_NativeAndWorkspaceFilesToolsPerLevel(t *testing.T) {
	cases := []struct {
		tool                     string
		allowWatch, allowPropose bool
	}{
		{"workspace_notes", true, true},
		{"workspace_tasks", true, true},
		{"workspace_sessions", true, true},
		{"workspace_files", true, true},
		{"workspace_directory_list", true, true},
		{"workspace_directory_read", true, true},
		{MemoryWriteToolName, true, true},
		{"read_file", true, true},
		{"list_directory", true, true},
		{"directory_tree", true, true},
		{"workspace_save_note", false, true},
		{"write_file", false, true},
		{"edit_file", false, true},
		{"delegate_task", false, false},
		{"workspace_create_project", false, false},
		{"profile_set", false, false},
		{"mail_get_thread", false, false},
		{"web_search", false, false},
		{"web_fetch", false, false},
	}
	// A read-default binding next to the files binding is exactly the shape
	// that used to turn workspace_save_note and delegate_task into "reads".
	for _, extra := range [][]MCPBinding{
		nil,
		{{ID: "calendar", ServerName: "calendar", Enabled: true, DefaultSideEffect: SideEffectRead}},
	} {
		store := NewInMemoryStore()
		ws := gateWorkspace(t, store, extra...)
		h := &LLMTaskHandler{workspaceStore: store}
		for _, c := range cases {
			for _, level := range []struct {
				policy AutonomyPolicy
				allow  bool
			}{{AutonomyWatch, c.allowWatch}, {AutonomyPropose, c.allowPropose}} {
				err := h.evaluateMissionGate(missionGateTask(ws.ID, level.policy), c.tool)
				if (err == nil) != level.allow {
					t.Errorf("bindings=%d %s under %s: allowed=%v, want %v (err %v)",
						len(ws.MCPBindings), c.tool, level.policy, err == nil, level.allow, err)
				}
			}
		}
	}
}

// An external binding fails closed for the files binding's unlisted tools.
func TestMissionGate_WorkspaceFilesWritesInheritAStricterBinding(t *testing.T) {
	store := NewInMemoryStore()
	ws := gateWorkspace(t, store, MCPBinding{ID: "gh", ServerName: "github", Enabled: true, DefaultSideEffect: SideEffectExternal})
	h := &LLMTaskHandler{workspaceStore: store}
	if err := h.evaluateMissionGate(missionGateTask(ws.ID, AutonomyPropose), "write_file"); err == nil {
		t.Fatal("a write that cannot be attributed must take the strictest default")
	}
	if err := h.evaluateMissionGate(missionGateTask(ws.ID, AutonomyWatch), "read_file"); err != nil {
		t.Fatalf("the files binding's read override still applies: %v", err)
	}
}

// Ori's workspace-files binding counts as classified, so a new workspace can
// run its goal; a user's own classification of it still wins.
func TestWorkspaceFilesBindingIsClassifiedByDefault(t *testing.T) {
	builtIn := MCPBinding{ID: "files", ServerName: "filesystem", Alias: WorkspaceFilesBindingAlias, Enabled: true}
	ws := &Workspace{MCPBindings: []MCPBinding{builtIn}}
	if !MissionBindingsReady(ws) {
		t.Fatal("the built-in files binding must not block goal runs")
	}
	defaultSE, overrides := EffectiveMCPBindingClassification(builtIn)
	if defaultSE != SideEffectWrite || overrides["read_file"] != SideEffectRead || overrides["write_file"] != "" {
		t.Fatalf("built-in classification = %q %v", defaultSE, overrides)
	}

	userSet := builtIn
	userSet.DefaultSideEffect = SideEffectRead
	userSet.ToolOverrides = map[string]SideEffect{"read_file": SideEffectExternal}
	defaultSE, overrides = EffectiveMCPBindingClassification(userSet)
	if defaultSE != SideEffectRead || overrides["read_file"] != SideEffectExternal || overrides["list_directory"] != SideEffectRead {
		t.Fatalf("user values must win: %q %v", defaultSE, overrides)
	}

	// Any other unclassified binding still blocks, as before.
	other := &Workspace{MCPBindings: []MCPBinding{builtIn, {ID: "x", ServerName: "filesystem", Alias: "my-files", Enabled: true}}}
	if mcp, _ := UnclassifiedBindings(other); len(mcp) != 1 || mcp[0] != "x" {
		t.Fatalf("unclassified = %v, want only the user's binding", mcp)
	}
}

// Ordinary tasks stay ungated; delegated subtasks keep the workspace policy.
func TestExecutionGate_OrdinaryTasksAreNotGated(t *testing.T) {
	store := NewInMemoryStore()
	ws := gateWorkspace(t, store)
	ws.AutonomyPolicy = AutonomyWatch
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}
	h := &LLMTaskHandler{workspaceStore: store}
	ordinary := Task{WorkspaceID: ws.ID}
	for _, tool := range []string{"workspace_save_note", "delegate_task", "write_file", "web_search"} {
		if err := h.evaluateExecutionAutonomyGate(ordinary, tool); err != nil {
			t.Errorf("ordinary task %s gated: %v", tool, err)
		}
	}
	delegated := Task{WorkspaceID: ws.ID, AssignmentMode: TaskAssignmentModeDynamicDelegation}
	if err := h.evaluateExecutionAutonomyGate(delegated, "workspace_save_note"); err == nil {
		t.Error("a delegated subtask in a Watch workspace must not write")
	}
}

func TestEffectiveAutonomyPolicy(t *testing.T) {
	for in, want := range map[AutonomyPolicy]AutonomyPolicy{
		"": AutonomyWatch, AutonomyWatch: AutonomyWatch, AutonomyPropose: AutonomyPropose, "act": AutonomyWatch,
	} {
		if got := EffectiveAutonomyPolicy(in); got != want {
			t.Errorf("EffectiveAutonomyPolicy(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTaskToolRoundLimit(t *testing.T) {
	if got := taskToolRoundLimit(Task{}); got != 6 {
		t.Fatalf("ordinary task cap = %d, want 6", got)
	}
	mission := Task{Context: map[string]any{MissionTaskContextOriginKey: MissionTaskContextOriginValue}}
	if got := taskToolRoundLimit(mission); got != 10 {
		t.Fatalf("goal run cap = %d, want 10", got)
	}
}

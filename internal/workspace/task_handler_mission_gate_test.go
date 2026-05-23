package workspace

import (
	"strings"
	"testing"
)

func TestEvaluateMissionToolCallDecision_PolicyMatrix(t *testing.T) {
	cases := []struct {
		name           string
		policy         AutonomyPolicy
		classification SideEffect
		wantAllowed    bool
	}{
		{"watch+read allows", AutonomyWatch, SideEffectRead, true},
		{"watch+write denies", AutonomyWatch, SideEffectWrite, false},
		{"watch+external denies", AutonomyWatch, SideEffectExternal, false},
		{"propose+read allows", AutonomyPropose, SideEffectRead, true},
		{"propose+write allows", AutonomyPropose, SideEffectWrite, true},
		{"propose+external denies", AutonomyPropose, SideEffectExternal, false},
		{"unclassified always denies (propose)", AutonomyPropose, "", false},
		{"unclassified always denies (watch)", AutonomyWatch, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dec := EvaluateMissionToolCallDecision(c.policy, c.classification, "tool_x")
			if dec.Allowed != c.wantAllowed {
				t.Errorf("Allowed = %v; want %v (reason=%q)", dec.Allowed, c.wantAllowed, dec.Reason)
			}
			if !c.wantAllowed && dec.Reason == "" {
				t.Error("denials must carry a non-empty reason")
			}
		})
	}
}

func TestResolveMissionToolSideEffect_OverrideWins(t *testing.T) {
	store := NewInMemoryStore()
	ws := NewWorkspace(CreateWorkspaceParams{Name: "X"})
	ws.MCPBindings = []WorkspaceMCPBinding{
		{
			ID:                "b1",
			Enabled:           true,
			DefaultSideEffect: SideEffectExternal, // would deny most
			ToolOverrides: map[string]SideEffect{
				"specific_tool": SideEffectRead, // override allows
			},
		},
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("Save: %v", err)
	}

	h := &LLMTaskHandler{workspaceStore: store}
	if got := h.resolveMissionToolSideEffect(ws.ID, "specific_tool"); got != SideEffectRead {
		t.Errorf("override should win; got %q", got)
	}
}

func TestResolveMissionToolSideEffect_FallsBackToHeuristic(t *testing.T) {
	store := NewInMemoryStore()
	ws := NewWorkspace(CreateWorkspaceParams{Name: "X"})
	// No overrides; tool name matches read-prefix heuristic.
	ws.MCPBindings = []WorkspaceMCPBinding{{ID: "b1", Enabled: true}}
	_ = store.Save(ws)

	h := &LLMTaskHandler{workspaceStore: store}
	if got := h.resolveMissionToolSideEffect(ws.ID, "read_file"); got != SideEffectRead {
		t.Errorf("heuristic should classify read_file as read; got %q", got)
	}
}

func TestResolveMissionToolSideEffect_UnknownReturnsEmpty(t *testing.T) {
	store := NewInMemoryStore()
	ws := NewWorkspace(CreateWorkspaceParams{Name: "X"})
	ws.MCPBindings = []WorkspaceMCPBinding{{
		ID:                "b1",
		Enabled:           true,
		DefaultSideEffect: SideEffectExternal,
	}}
	_ = store.Save(ws)

	h := &LLMTaskHandler{workspaceStore: store}
	// No override for this tool, no heuristic match — must remain
	// unclassified so the gate denies. (We deliberately don't let the
	// binding's permissive default leak to unknown tools.)
	got := h.resolveMissionToolSideEffect(ws.ID, "wipe_database")
	if got != "" {
		t.Errorf("unknown tool with no override should be unclassified; got %q", got)
	}
}

func TestResolveMissionToolSideEffect_IgnoresDisabledBindings(t *testing.T) {
	store := NewInMemoryStore()
	ws := NewWorkspace(CreateWorkspaceParams{Name: "X"})
	ws.MCPBindings = []WorkspaceMCPBinding{
		{
			ID:      "b1",
			Enabled: false, // disabled — override must be ignored
			ToolOverrides: map[string]SideEffect{
				"tool_y": SideEffectRead,
			},
		},
	}
	_ = store.Save(ws)

	h := &LLMTaskHandler{workspaceStore: store}
	// Heuristic doesn't match either, so should be unclassified.
	if got := h.resolveMissionToolSideEffect(ws.ID, "tool_y"); got != "" {
		t.Errorf("disabled binding should not contribute; got %q", got)
	}
}

func TestEvaluateMissionGate_BlocksMissingPolicy(t *testing.T) {
	h := &LLMTaskHandler{workspaceStore: NewInMemoryStore()}
	task := Task{
		Context: map[string]any{
			MissionTaskContextOriginKey: MissionTaskContextOriginValue,
			// MissionTaskContextPolicyKey deliberately omitted
		},
	}
	err := h.evaluateMissionGate(task, "read_file")
	if err == nil {
		t.Fatal("expected denial when policy missing from context")
	}
	if !strings.Contains(err.Error(), "policy") {
		t.Errorf("error should mention missing policy: %v", err)
	}
}

func TestEvaluateMissionGate_EndToEndAllowsRead(t *testing.T) {
	store := NewInMemoryStore()
	ws := NewWorkspace(CreateWorkspaceParams{Name: "X"})
	_ = store.Save(ws)
	h := &LLMTaskHandler{workspaceStore: store}
	task := Task{
		WorkspaceID: ws.ID,
		Context: map[string]any{
			MissionTaskContextOriginKey: MissionTaskContextOriginValue,
			MissionTaskContextPolicyKey: string(AutonomyWatch),
		},
	}
	if err := h.evaluateMissionGate(task, "read_file"); err != nil {
		t.Errorf("Watch should allow read_file (heuristic); got: %v", err)
	}
}

func TestEvaluateMissionGate_EndToEndBlocksUnknownToolUnderWatch(t *testing.T) {
	store := NewInMemoryStore()
	ws := NewWorkspace(CreateWorkspaceParams{Name: "X"})
	_ = store.Save(ws)
	h := &LLMTaskHandler{workspaceStore: store}
	task := Task{
		WorkspaceID: ws.ID,
		Context: map[string]any{
			MissionTaskContextOriginKey: MissionTaskContextOriginValue,
			MissionTaskContextPolicyKey: string(AutonomyWatch),
		},
	}
	err := h.evaluateMissionGate(task, "send_email")
	if err == nil {
		t.Fatal("Watch should deny unclassified send_email")
	}
	if !strings.Contains(err.Error(), "unclassified") {
		t.Errorf("denial reason should mention unclassified: %v", err)
	}
}

func TestEvaluateMissionGate_EndToEndUsesPerToolOverride(t *testing.T) {
	store := NewInMemoryStore()
	ws := NewWorkspace(CreateWorkspaceParams{Name: "X"})
	ws.MCPBindings = []WorkspaceMCPBinding{{
		ID:      "b1",
		Enabled: true,
		ToolOverrides: map[string]SideEffect{
			"send_email": SideEffectExternal,
		},
	}}
	_ = store.Save(ws)

	h := &LLMTaskHandler{workspaceStore: store}
	task := Task{
		WorkspaceID: ws.ID,
		Context: map[string]any{
			MissionTaskContextOriginKey: MissionTaskContextOriginValue,
			MissionTaskContextPolicyKey: string(AutonomyPropose),
		},
	}
	err := h.evaluateMissionGate(task, "send_email")
	if err == nil {
		t.Fatal("Propose must deny external tools even when binding default would allow")
	}
	if !strings.Contains(err.Error(), "external") {
		t.Errorf("denial should cite classification: %v", err)
	}
}

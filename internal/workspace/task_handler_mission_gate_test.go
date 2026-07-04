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
	ws.MCPBindings = []MCPBinding{
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
	ws.MCPBindings = []MCPBinding{{ID: "b1", Enabled: true}}
	_ = store.Save(ws)

	h := &LLMTaskHandler{workspaceStore: store}
	if got := h.resolveMissionToolSideEffect(ws.ID, "read_file"); got != SideEffectRead {
		t.Errorf("heuristic should classify read_file as read; got %q", got)
	}
}

func TestResolveMissionToolSideEffect_InheritsBindingDefault(t *testing.T) {
	store := NewInMemoryStore()
	ws := NewWorkspace(CreateWorkspaceParams{Name: "X"})
	ws.MCPBindings = []MCPBinding{{
		ID:                "b1",
		Enabled:           true,
		DefaultSideEffect: SideEffectExternal,
	}}
	_ = store.Save(ws)

	h := &LLMTaskHandler{workspaceStore: store}
	// No override and no read-prefix match: the tool inherits the binding's
	// classified default. external still denies under every policy, so the
	// gate outcome is unchanged — but a write default must now classify as
	// write (see TestResolveMissionToolSideEffect_WriteDefaultAllowsWrites).
	got := h.resolveMissionToolSideEffect(ws.ID, "wipe_database")
	if got != SideEffectExternal {
		t.Errorf("unknown tool should inherit the binding default; got %q", got)
	}
}

func TestResolveMissionToolSideEffect_WriteDefaultAllowsWrites(t *testing.T) {
	store := NewInMemoryStore()
	ws := NewWorkspace(CreateWorkspaceParams{Name: "X"})
	// A write-classified binding with no per-tool overrides — the common case
	// after the one-time mission classification flow sets a binding default.
	ws.MCPBindings = []MCPBinding{{
		ID:                "b1",
		Enabled:           true,
		DefaultSideEffect: SideEffectWrite,
	}}
	_ = store.Save(ws)

	h := &LLMTaskHandler{workspaceStore: store}
	// Regression: this used to resolve to "" (unclassified → denied), which
	// blocked Propose missions from performing any binding-authorized write.
	if got := h.resolveMissionToolSideEffect(ws.ID, "create_note"); got != SideEffectWrite {
		t.Fatalf("write-default binding should classify writes; got %q", got)
	}
	task := Task{
		WorkspaceID: ws.ID,
		Context: map[string]any{
			MissionTaskContextOriginKey: MissionTaskContextOriginValue,
			MissionTaskContextPolicyKey: string(AutonomyPropose),
		},
	}
	if err := h.evaluateMissionGate(task, "create_note"); err != nil {
		t.Errorf("Propose should allow a write tool authorized by the binding default; got: %v", err)
	}
	// The same write tool must still be denied under Watch (observe-only).
	task.Context[MissionTaskContextPolicyKey] = string(AutonomyWatch)
	if err := h.evaluateMissionGate(task, "create_note"); err == nil {
		t.Error("Watch should deny a write tool")
	}
}

func TestResolveMissionToolSideEffect_MostRestrictiveDefaultWins(t *testing.T) {
	store := NewInMemoryStore()
	ws := NewWorkspace(CreateWorkspaceParams{Name: "X"})
	// Mixed defaults: without per-tool attribution we fail closed to the most
	// restrictive (external) so an unknown tool is never under-classified.
	ws.MCPBindings = []MCPBinding{
		{ID: "b1", Enabled: true, DefaultSideEffect: SideEffectRead},
		{ID: "b2", Enabled: true, DefaultSideEffect: SideEffectExternal},
	}
	_ = store.Save(ws)

	h := &LLMTaskHandler{workspaceStore: store}
	if got := h.resolveMissionToolSideEffect(ws.ID, "do_thing"); got != SideEffectExternal {
		t.Errorf("mixed defaults should resolve to the most restrictive; got %q", got)
	}
}

func TestResolveMissionToolSideEffect_ExternalDefaultBeatsReadHeuristic(t *testing.T) {
	store := NewInMemoryStore()
	ws := NewWorkspace(CreateWorkspaceParams{Name: "X"})
	// fetch_ matches the read-prefix heuristic, but the binding is external —
	// the default must win so a read-named external tool isn't wrongly allowed.
	ws.MCPBindings = []MCPBinding{{
		ID:                "b1",
		Enabled:           true,
		DefaultSideEffect: SideEffectExternal,
	}}
	_ = store.Save(ws)

	h := &LLMTaskHandler{workspaceStore: store}
	if got := h.resolveMissionToolSideEffect(ws.ID, "fetch_url"); got != SideEffectExternal {
		t.Errorf("binding default must take precedence over the read heuristic; got %q", got)
	}
}

func TestResolveMissionToolSideEffect_IgnoresDisabledBindings(t *testing.T) {
	store := NewInMemoryStore()
	ws := NewWorkspace(CreateWorkspaceParams{Name: "X"})
	ws.MCPBindings = []MCPBinding{
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
	ws.MCPBindings = []MCPBinding{{
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

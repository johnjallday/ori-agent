package workspace

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderAgentRefinement(t *testing.T) {
	if got := RenderAgentRefinement(AgentInstance{}); got != "" {
		t.Errorf("empty instance should render nothing, got %q", got)
	}

	got := RenderAgentRefinement(AgentInstance{
		Role:               "Voice keeper",
		Description:        "Owns tone",
		CustomInstructions: "Favor short-form social copy.\nKeep it punchy.",
	})
	for _, want := range []string{"Voice keeper", "Owns tone", "Favor short-form social copy.", "Keep it punchy.", "only in this workspace"} {
		if !strings.Contains(got, want) {
			t.Errorf("refinement missing %q in:\n%s", want, got)
		}
	}

	// custom_instructions alone still renders.
	if got := RenderAgentRefinement(AgentInstance{CustomInstructions: "Just this."}); !strings.Contains(got, "Just this.") {
		t.Errorf("custom-only refinement missing content: %q", got)
	}
}

func TestAgentInstanceByName(t *testing.T) {
	ws := &Workspace{
		AgentInstances: []AgentInstance{
			{Name: "Writer", InstanceNumber: 1},
			{Name: "Writer", InstanceNumber: 2, EntryPoint: true, CustomInstructions: "entry"},
			{Name: "Editor"},
		},
	}
	// Entry-point instance is preferred among same-named matches.
	inst, ok := AgentInstanceByName(ws, "writer")
	if !ok || !inst.EntryPoint || inst.CustomInstructions != "entry" {
		t.Fatalf("expected entry-point Writer, got %+v ok=%v", inst, ok)
	}
	if _, ok := AgentInstanceByName(ws, "Nonexistent"); ok {
		t.Error("expected not-found for unattached agent")
	}
	if _, ok := AgentInstanceByName(nil, "x"); ok {
		t.Error("nil workspace should be not-found")
	}
}

func TestAppendAgentRefinement(t *testing.T) {
	store, err := NewFileStore(filepath.Join(t.TempDir(), "workspaces"))
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	ws := &Workspace{
		ID:   "ws-1",
		Name: "WS",
		AgentInstances: []AgentInstance{
			{ID: "i1", Name: "Copywriter", EntryPoint: true, CustomInstructions: "Be concise."},
			{ID: "i2", Name: "Plain"},
		},
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Agent with refinement: appended after the base.
	out := AppendAgentRefinement("BASE", store, "ws-1", "Copywriter")
	if !strings.Contains(out, "BASE") || !strings.Contains(out, "Be concise.") {
		t.Errorf("expected base + refinement, got %q", out)
	}

	// Agent without refinement: base unchanged.
	if out := AppendAgentRefinement("BASE", store, "ws-1", "Plain"); out != "BASE" {
		t.Errorf("expected unchanged base for refinement-less agent, got %q", out)
	}

	// Missing pieces are no-ops returning base.
	if out := AppendAgentRefinement("BASE", store, "ws-1", "Nonexistent"); out != "BASE" {
		t.Errorf("unattached agent should return base, got %q", out)
	}
	if out := AppendAgentRefinement("BASE", nil, "ws-1", "Copywriter"); out != "BASE" {
		t.Errorf("nil store should return base, got %q", out)
	}
}

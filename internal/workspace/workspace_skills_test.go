package workspace

import (
	"strings"
	"sync"
	"testing"
)

func TestSkillBinding_CRUD(t *testing.T) {
	ws := &Workspace{ID: "ws-1"}

	// Create
	err := ws.UpsertSkillBinding(SkillBinding{
		ID:        "sb-1",
		SkillName: "code-review",
		Enabled:   true,
		Trusted:   false,
	})
	if err != nil {
		t.Fatalf("UpsertSkillBinding() error = %v", err)
	}

	// Read
	bindings := ws.GetSkillBindings()
	if len(bindings) != 1 {
		t.Fatalf("expected 1 binding, got %d", len(bindings))
	}
	if bindings[0].SkillName != "code-review" {
		t.Fatalf("expected skill_name=code-review, got %q", bindings[0].SkillName)
	}

	// Get by ID
	got, ok := ws.GetSkillBinding("sb-1")
	if !ok || got == nil {
		t.Fatalf("GetSkillBinding(sb-1) not found")
	}
	if got.SkillName != "code-review" {
		t.Fatalf("expected code-review, got %q", got.SkillName)
	}

	// Update
	err = ws.UpsertSkillBinding(SkillBinding{
		ID:        "sb-1",
		SkillName: "code-review",
		Enabled:   false,
		Trusted:   true,
	})
	if err != nil {
		t.Fatalf("UpsertSkillBinding(update) error = %v", err)
	}
	got, _ = ws.GetSkillBinding("sb-1")
	if got.Enabled {
		t.Fatal("expected Enabled=false after update")
	}
	if !got.Trusted {
		t.Fatal("expected Trusted=true after update")
	}

	// Delete
	err = ws.DeleteSkillBinding("sb-1")
	if err != nil {
		t.Fatalf("DeleteSkillBinding() error = %v", err)
	}
	bindings = ws.GetSkillBindings()
	if len(bindings) != 0 {
		t.Fatalf("expected 0 bindings after delete, got %d", len(bindings))
	}
}

func TestSkillBinding_ValidationErrors(t *testing.T) {
	ws := &Workspace{ID: "ws-1"}

	err := ws.UpsertSkillBinding(SkillBinding{SkillName: "test"})
	if err == nil || !strings.Contains(err.Error(), "binding ID is required") {
		t.Fatalf("expected binding ID error, got %v", err)
	}

	err = ws.UpsertSkillBinding(SkillBinding{ID: "sb-1"})
	if err == nil || !strings.Contains(err.Error(), "skill name is required") {
		t.Fatalf("expected skill name error, got %v", err)
	}
}

func TestSkillBinding_DeleteNotFound(t *testing.T) {
	ws := &Workspace{ID: "ws-1"}
	err := ws.DeleteSkillBinding("nonexistent")
	if err == nil {
		t.Fatal("expected error deleting nonexistent binding")
	}
}

func TestSkillBinding_DeleteCascadesToAccess(t *testing.T) {
	ws := &Workspace{ID: "ws-1"}

	_ = ws.UpsertSkillBinding(SkillBinding{ID: "sb-1", SkillName: "skill-a", Enabled: true})
	_ = ws.UpsertSkillBinding(SkillBinding{ID: "sb-2", SkillName: "skill-b", Enabled: true})
	_ = ws.SetAgentSkillAccess(AgentSkillAccess{
		AgentInstanceID:   "agent-1",
		EnabledBindingIDs: []string{"sb-1", "sb-2"},
	})

	_ = ws.DeleteSkillBinding("sb-1")

	access, ok := ws.GetAgentSkillAccess("agent-1")
	if !ok {
		t.Fatal("expected access entry to exist after binding delete")
	}
	for _, id := range access.EnabledBindingIDs {
		if strings.EqualFold(id, "sb-1") {
			t.Fatal("expected sb-1 to be removed from access entry")
		}
	}
	if len(access.EnabledBindingIDs) != 1 || access.EnabledBindingIDs[0] != "sb-2" {
		t.Fatalf("expected [sb-2], got %v", access.EnabledBindingIDs)
	}
}

func TestAgentSkillAccess_CRUD(t *testing.T) {
	ws := &Workspace{ID: "ws-1"}

	// Create
	err := ws.SetAgentSkillAccess(AgentSkillAccess{
		AgentInstanceID:   "agent-1",
		EnabledBindingIDs: []string{"sb-1", "sb-2"},
	})
	if err != nil {
		t.Fatalf("SetAgentSkillAccess() error = %v", err)
	}

	// Read
	entries := ws.ListAgentSkillAccess()
	if len(entries) != 1 {
		t.Fatalf("expected 1 access entry, got %d", len(entries))
	}

	got, ok := ws.GetAgentSkillAccess("agent-1")
	if !ok || got == nil {
		t.Fatal("GetAgentSkillAccess(agent-1) not found")
	}
	if len(got.EnabledBindingIDs) != 2 {
		t.Fatalf("expected 2 binding IDs, got %d", len(got.EnabledBindingIDs))
	}

	// Update
	err = ws.SetAgentSkillAccess(AgentSkillAccess{
		AgentInstanceID:   "agent-1",
		EnabledBindingIDs: []string{"sb-2"},
	})
	if err != nil {
		t.Fatalf("SetAgentSkillAccess(update) error = %v", err)
	}
	got, _ = ws.GetAgentSkillAccess("agent-1")
	if len(got.EnabledBindingIDs) != 1 {
		t.Fatalf("expected 1 binding ID after update, got %d", len(got.EnabledBindingIDs))
	}

	// Delete
	err = ws.DeleteAgentSkillAccess("agent-1")
	if err != nil {
		t.Fatalf("DeleteAgentSkillAccess() error = %v", err)
	}
	entries = ws.ListAgentSkillAccess()
	if len(entries) != 0 {
		t.Fatalf("expected 0 access entries, got %d", len(entries))
	}
}

func TestAgentSkillAccess_EmptyID(t *testing.T) {
	ws := &Workspace{ID: "ws-1"}
	err := ws.SetAgentSkillAccess(AgentSkillAccess{})
	if err == nil || !strings.Contains(err.Error(), "agent instance ID is required") {
		t.Fatalf("expected error, got %v", err)
	}
}

func TestAgentSkillAccess_Deduplication(t *testing.T) {
	ws := &Workspace{ID: "ws-1"}
	_ = ws.SetAgentSkillAccess(AgentSkillAccess{
		AgentInstanceID:   "agent-1",
		EnabledBindingIDs: []string{"sb-1", "sb-1", "sb-2"},
	})
	got, _ := ws.GetAgentSkillAccess("agent-1")
	if len(got.EnabledBindingIDs) != 2 {
		t.Fatalf("expected deduped to 2, got %d: %v", len(got.EnabledBindingIDs), got.EnabledBindingIDs)
	}
}

func TestSkillBinding_ConcurrentAccess(t *testing.T) {
	ws := &Workspace{ID: "ws-1"}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			id := "sb-" + strings.Repeat("x", n%5)
			_ = ws.UpsertSkillBinding(SkillBinding{ID: id, SkillName: "skill-" + id, Enabled: true})
			_ = ws.GetSkillBindings()
			_, _ = ws.GetSkillBinding(id)
			_ = ws.DeleteSkillBinding(id)
		}(i)
	}
	wg.Wait()
}

func TestSkillBinding_CaseInsensitiveLookup(t *testing.T) {
	ws := &Workspace{ID: "ws-1"}
	_ = ws.UpsertSkillBinding(SkillBinding{ID: "SB-1", SkillName: "Test", Enabled: true})

	got, ok := ws.GetSkillBinding("sb-1")
	if !ok || got == nil {
		t.Fatal("expected case-insensitive lookup to succeed")
	}
}

func TestSkillBinding_ConfigCloned(t *testing.T) {
	ws := &Workspace{ID: "ws-1"}
	cfg := map[string]any{"key": "value"}
	_ = ws.UpsertSkillBinding(SkillBinding{
		ID:        "sb-1",
		SkillName: "test",
		Enabled:   true,
		Config:    cfg,
	})

	got, _ := ws.GetSkillBinding("sb-1")
	got.Config["key"] = "mutated"

	original, _ := ws.GetSkillBinding("sb-1")
	if original.Config["key"] != "value" {
		t.Fatal("expected config to be deep cloned, but mutation leaked through")
	}
}

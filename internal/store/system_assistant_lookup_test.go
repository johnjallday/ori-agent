package store

import (
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/systemassistant"
	"github.com/johnjallday/ori-agent/internal/types"
)

func lookupStore(t *testing.T, names ...string) Store {
	t.Helper()
	st, err := NewFileStore(
		filepath.Join(t.TempDir(), "agents_index.json"),
		types.Settings{Model: "gpt-4o-mini", Temperature: 1.0},
	)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	for _, name := range names {
		if err := st.CreateAgent(name, &CreateAgentConfig{Type: agent.TypeGeneral}); err != nil {
			t.Fatalf("CreateAgent(%q): %v", name, err)
		}
	}
	return st
}

// FR57: a workspace roster, task assignment or session row written before the
// migration still says "Workspace Manager". It must keep resolving.
func TestResolveAgentFollowsRetiredSystemAssistantNames(t *testing.T) {
	st := lookupStore(t, systemassistant.CanonicalName)

	for _, persisted := range systemassistant.LegacyNames {
		ag, resolved, ok := ResolveAgent(st, persisted)
		if !ok || ag == nil {
			t.Errorf("ResolveAgent(%q) did not resolve", persisted)
			continue
		}
		if resolved != systemassistant.CanonicalName {
			t.Errorf("ResolveAgent(%q) resolved to %q, want %q",
				persisted, resolved, systemassistant.CanonicalName)
		}
	}
}

// The reverse direction matters too: a canonical reference must still resolve
// while an install is only part-way migrated.
func TestResolveAgentFindsTheAssistantStillUnderALegacyName(t *testing.T) {
	st := lookupStore(t, "Workspace Manager")

	ag, resolved, ok := ResolveAgent(st, systemassistant.CanonicalName)
	if !ok || ag == nil {
		t.Fatal("a canonical reference must resolve to the not-yet-migrated record")
	}
	if resolved != "Workspace Manager" {
		t.Errorf("resolved to %q, want %q", resolved, "Workspace Manager")
	}
}

// An exact match always wins, so a user who owns "Ask Ori" after a collision
// still reaches their own agent rather than being redirected.
func TestResolveAgentPrefersTheExactRecord(t *testing.T) {
	st := lookupStore(t, systemassistant.CanonicalName, "Workspace Manager")

	_, resolved, ok := ResolveAgent(st, "Workspace Manager")
	if !ok || resolved != "Workspace Manager" {
		t.Fatalf("exact lookup resolved to %q, want %q", resolved, "Workspace Manager")
	}
	_, resolved, ok = ResolveAgent(st, systemassistant.CanonicalName)
	if !ok || resolved != systemassistant.CanonicalName {
		t.Fatalf("exact lookup resolved to %q, want %q", resolved, systemassistant.CanonicalName)
	}
}

// FR56: compatibility applies only to the protected identity. An ordinary
// missing agent must stay missing rather than being pointed somewhere else.
func TestResolveAgentDoesNotRedirectOrdinaryNames(t *testing.T) {
	st := lookupStore(t, systemassistant.CanonicalName, "Research Buddy")

	for _, name := range []string{"Workspace Assistant", "Task Assistant", "Deleted Agent", ""} {
		if _, _, ok := ResolveAgent(st, name); ok {
			t.Errorf("ResolveAgent(%q) resolved, want not found", name)
		}
	}
	if _, resolved, ok := ResolveAgent(st, "Research Buddy"); !ok || resolved != "Research Buddy" {
		t.Errorf("an ordinary agent must resolve to itself, got %q", resolved)
	}
}

func TestResolveAgentHandlesNilStoreAndMissingAssistant(t *testing.T) {
	if _, _, ok := ResolveAgent(nil, systemassistant.CanonicalName); ok {
		t.Error("a nil store must not resolve")
	}
	st := lookupStore(t, "Research Buddy")
	if _, _, ok := ResolveAgent(st, "Workspace Manager"); ok {
		t.Error("a legacy reference must not resolve when no assistant record exists")
	}
}

func TestAgentExistsAppliesTheSameCompatibility(t *testing.T) {
	st := lookupStore(t, systemassistant.CanonicalName)
	if !AgentExists(st, "Workspace Manager") {
		t.Error("AgentExists must follow retired system-assistant names")
	}
	if AgentExists(st, "Nope") {
		t.Error("AgentExists must not invent agents")
	}
}

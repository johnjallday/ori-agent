package skills

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// fakeLoadoutResolver returns a fixed loadout for every agent.
type fakeLoadoutResolver struct {
	loadout AgentLoadout
	ok      bool
}

func (f fakeLoadoutResolver) ResolveAgentLoadout(string) (AgentLoadout, bool) {
	return f.loadout, f.ok
}

// newLoadoutTestManager builds a manager backed by `count` repo skills named
// skill-0..skill-(count-1), all initially disabled, plus the given resolver.
func newLoadoutTestManager(t *testing.T, count int, resolver LoadoutResolver) *Manager {
	t.Helper()
	tmpDir := t.TempDir()
	agentStorePath := filepath.Join(tmpDir, "agents.json")
	if err := os.WriteFile(agentStorePath, []byte(`{}`), 0o644); err != nil {
		t.Fatalf("write agents.json: %v", err)
	}
	for i := 0; i < count; i++ {
		name := "skill-" + string(rune('0'+i))
		dir := filepath.Join(tmpDir, "agents", "skills", name)
		writeTestSkill(t, dir, name, "desc", "prompt")
	}
	m := NewManager(ManagerConfig{AgentStorePath: agentStorePath})
	m.SetLoadoutResolver(resolver)
	return m
}

func enabledSkillNames(t *testing.T, m *Manager, agent string) []string {
	t.Helper()
	list, err := m.ListSkills(agent)
	if err != nil {
		t.Fatalf("ListSkills: %v", err)
	}
	var out []string
	for _, s := range list {
		if s.Enabled {
			out = append(out, s.Name)
		}
	}
	sort.Strings(out)
	return out
}

// Learning is unbounded (PRD FR-3, task 3.1).
//
// This test previously asserted the opposite — that a third enable past the
// spark cap was rejected. Named Toolboxes moved capacity from "enabled" to
// "selected": enabling adds a skill to the agent's COLLECTION, which grants
// nothing on its own, so there is nothing here to protect the user from. The
// cap now binds where a Toolbox selects from that collection, and the old
// check was additionally counting the wrong set — it ran before
// workspace-provided skills were merged in, so an agent "at cap" could still
// receive several more skills at runtime (FR-56).
func TestSetSkillEnabled_LearningIsNotCapped(t *testing.T) {
	m := newLoadoutTestManager(t, 5, fakeLoadoutResolver{
		loadout: AgentLoadout{SlotCap: 2, Stage: "spark"},
		ok:      true,
	})

	for _, name := range []string{"skill-0", "skill-1", "skill-2", "skill-3", "skill-4"} {
		if err := m.SetSkillEnabled("agent", name, true); err != nil {
			t.Fatalf("collection growth must not be capped, got %v enabling %s", err, name)
		}
	}
	if got := enabledSkillNames(t, m, "agent"); len(got) != 5 {
		t.Errorf("expected every skill to join the collection, got %v", got)
	}
}

func TestSetSkillEnabled_ReEnableAlreadyEnabledAllowedAtCap(t *testing.T) {
	m := newLoadoutTestManager(t, 5, fakeLoadoutResolver{
		loadout: AgentLoadout{SlotCap: 2, Stage: "spark"},
		ok:      true,
	})
	mustEnable(t, m, "skill-0")
	mustEnable(t, m, "skill-1")

	// Re-enabling an already-active skill at cap is a no-op, never rejected.
	if err := m.SetSkillEnabled("agent", "skill-0", true); err != nil {
		t.Fatalf("re-enable of already-enabled skill should be allowed, got %v", err)
	}
}

// A lowered stage never takes a learned skill away (FR-3, FR-33).
//
// Progression can move an agent's capacity, and forgetting is not how that
// should be expressed: the collection is what the agent knows, and knowing
// costs nothing until a Toolbox selects it.
func TestSetSkillEnabled_LoweredCapacityNeverForgetsASkill(t *testing.T) {
	resolver := &mutableLoadoutResolver{loadout: AgentLoadout{SlotCap: 4, Stage: "learner"}, ok: true}
	m := newLoadoutTestManager(t, 5, resolver)

	for _, n := range []string{"skill-0", "skill-1", "skill-2", "skill-3"} {
		mustEnable(t, m, n)
	}
	resolver.loadout = AgentLoadout{SlotCap: 2, Stage: "spark"}

	if got := enabledSkillNames(t, m, "agent"); len(got) != 4 {
		t.Fatalf("a lowered capacity must not un-learn anything, got %v", got)
	}
	if err := m.SetSkillEnabled("agent", "skill-4", true); err != nil {
		t.Fatalf("learning another skill stays possible below capacity, got %v", err)
	}
	if err := m.SetSkillEnabled("agent", "skill-0", false); err != nil {
		t.Fatalf("forgetting a skill should always be allowed, got %v", err)
	}
	if got := enabledSkillNames(t, m, "agent"); len(got) != 4 {
		t.Errorf("expected 4 after learning one and forgetting one, got %v", got)
	}
}

func TestSetSkillEnabled_ExpertModeBypassesCap(t *testing.T) {
	m := newLoadoutTestManager(t, 5, fakeLoadoutResolver{
		loadout: AgentLoadout{SlotCap: 2, Stage: "spark", ExpertMode: true},
		ok:      true,
	})
	for _, n := range []string{"skill-0", "skill-1", "skill-2", "skill-3", "skill-4"} {
		if err := m.SetSkillEnabled("agent", n, true); err != nil {
			t.Fatalf("expert-mode enable of %s should never be capped, got %v", n, err)
		}
	}
	if got := enabledSkillNames(t, m, "agent"); len(got) != 5 {
		t.Errorf("expected all 5 skills enabled under expert mode, got %v", got)
	}
}

func TestSetSkillEnabled_NoResolverMeansNoCap(t *testing.T) {
	m := newLoadoutTestManager(t, 5, nil)
	m.SetLoadoutResolver(nil) // explicit: no enforcement wired
	for _, n := range []string{"skill-0", "skill-1", "skill-2", "skill-3", "skill-4"} {
		if err := m.SetSkillEnabled("agent", n, true); err != nil {
			t.Fatalf("without a resolver there is no cap, got %v enabling %s", err, n)
		}
	}
}

func TestSetSkillEnabled_UnresolvableAgentFailsOpen(t *testing.T) {
	m := newLoadoutTestManager(t, 3, fakeLoadoutResolver{ok: false})
	for _, n := range []string{"skill-0", "skill-1", "skill-2"} {
		if err := m.SetSkillEnabled("agent", n, true); err != nil {
			t.Fatalf("unresolvable agent should fail open (no cap), got %v", err)
		}
	}
}

// Bulk "*" writes explicit state for every skill rather than leaving a wildcard
// default that would silently enable future skills too (FR-2, FR-32).
func TestSetSkillEnabled_BulkWildcardWritesExplicitStateForEverySkill(t *testing.T) {
	m := newLoadoutTestManager(t, 5, fakeLoadoutResolver{
		loadout: AgentLoadout{SlotCap: 3, Stage: "learner"},
		ok:      true,
	})

	if err := m.SetSkillEnabled("agent", "*", true); err != nil {
		t.Fatalf("bulk enable failed: %v", err)
	}

	got := enabledSkillNames(t, m, "agent")
	if len(got) != 5 {
		t.Fatalf("expected the whole collection to be enabled, got %v", got)
	}
}

func TestSetSkillEnabled_BulkWildcardExpertEnablesAll(t *testing.T) {
	m := newLoadoutTestManager(t, 5, fakeLoadoutResolver{
		loadout: AgentLoadout{SlotCap: 2, Stage: "spark", ExpertMode: true},
		ok:      true,
	})
	if err := m.SetSkillEnabled("agent", "*", true); err != nil {
		t.Fatalf("bulk enable failed: %v", err)
	}
	if got := enabledSkillNames(t, m, "agent"); len(got) != 5 {
		t.Errorf("expert bulk enable should enable all 5 skills, got %v", got)
	}
}

// mustEnable enables a skill and fails the test on error.
func mustEnable(t *testing.T, m *Manager, name string) {
	t.Helper()
	if err := m.SetSkillEnabled("agent", name, true); err != nil {
		t.Fatalf("enable %s: %v", name, err)
	}
}

// mutableLoadoutResolver lets a test change the loadout mid-run to simulate
// stage/cap changes.
type mutableLoadoutResolver struct {
	loadout AgentLoadout
	ok      bool
}

func (r *mutableLoadoutResolver) ResolveAgentLoadout(string) (AgentLoadout, bool) {
	return r.loadout, r.ok
}

package skills

import (
	"errors"
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

func TestSetSkillEnabled_RejectsEnableAtCap(t *testing.T) {
	m := newLoadoutTestManager(t, 5, fakeLoadoutResolver{
		loadout: AgentLoadout{SlotCap: 2, Stage: "spark"},
		ok:      true,
	})

	if err := m.SetSkillEnabled("agent", "skill-0", true); err != nil {
		t.Fatalf("first enable failed: %v", err)
	}
	if err := m.SetSkillEnabled("agent", "skill-1", true); err != nil {
		t.Fatalf("second enable failed: %v", err)
	}
	// Third enable is over the spark cap of 2.
	err := m.SetSkillEnabled("agent", "skill-2", true)
	if !errors.Is(err, ErrSkillSlotCapReached) {
		t.Fatalf("expected ErrSkillSlotCapReached, got %v", err)
	}
	if got := enabledSkillNames(t, m, "agent"); len(got) != 2 {
		t.Errorf("expected 2 enabled skills after rejected enable, got %v", got)
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

func TestSetSkillEnabled_OverCapGrandfatherKeepsAndBlocksNew(t *testing.T) {
	// Start with a generous cap so we can enable 4 skills, then drop the cap
	// to 2 to simulate an agent grandfathered over its stage cap.
	resolver := &mutableLoadoutResolver{loadout: AgentLoadout{SlotCap: 4, Stage: "learner"}, ok: true}
	m := newLoadoutTestManager(t, 5, resolver)

	for _, n := range []string{"skill-0", "skill-1", "skill-2", "skill-3"} {
		mustEnable(t, m, n)
	}

	// Now the agent is over its (lowered) cap.
	resolver.loadout = AgentLoadout{SlotCap: 2, Stage: "spark"}

	// Existing skills all remain enabled (never auto-disabled).
	if got := enabledSkillNames(t, m, "agent"); len(got) != 4 {
		t.Fatalf("grandfathered agent should keep all 4 skills, got %v", got)
	}
	// A new enable is blocked while over cap.
	if err := m.SetSkillEnabled("agent", "skill-4", true); !errors.Is(err, ErrSkillSlotCapReached) {
		t.Fatalf("expected over-cap new enable to be rejected, got %v", err)
	}
	// Disabling is still allowed even while over cap.
	if err := m.SetSkillEnabled("agent", "skill-0", false); err != nil {
		t.Fatalf("disable while over cap should be allowed, got %v", err)
	}
	if got := enabledSkillNames(t, m, "agent"); len(got) != 3 {
		t.Errorf("expected 3 enabled after disabling one, got %v", got)
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

func TestSetSkillEnabled_BulkWildcardFillsUpToCap(t *testing.T) {
	m := newLoadoutTestManager(t, 5, fakeLoadoutResolver{
		loadout: AgentLoadout{SlotCap: 3, Stage: "learner"},
		ok:      true,
	})

	if err := m.SetSkillEnabled("agent", "*", true); err != nil {
		t.Fatalf("bulk enable failed: %v", err)
	}

	got := enabledSkillNames(t, m, "agent")
	if len(got) != 3 {
		t.Fatalf("expected bulk enable to fill exactly the cap (3), got %v", got)
	}
	// All repo skills share the same source rank, so the deterministic order is
	// alphabetical: skill-0, skill-1, skill-2.
	want := []string{"skill-0", "skill-1", "skill-2"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("deterministic fill mismatch at %d: got %v, want %v", i, got, want)
		}
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

package workspace

import (
	"strings"
	"testing"
)

func TestAppendSkillPromptsFromResolved_AppendsBodies(t *testing.T) {
	base := "BASE SYSTEM PROMPT"
	skills := []ResolvedSkill{
		{Name: "reaper-web-remote", Prompt: "Drive REAPER over Web Remote with curl."},
		{Name: "reaper-session-setup", Prompt: "Write Lua to the inbox and trigger the runner."},
	}

	out := AppendSkillPromptsFromResolved(base, skills)

	if !strings.HasPrefix(out, base) {
		t.Fatalf("expected output to start with base prompt, got: %q", out)
	}
	if !strings.Contains(out, "# Active Skills") {
		t.Errorf("expected Active Skills header in output")
	}
	for _, s := range skills {
		if !strings.Contains(out, s.Name) {
			t.Errorf("expected skill name %q in output", s.Name)
		}
		if !strings.Contains(out, s.Prompt) {
			t.Errorf("expected skill body %q in output", s.Prompt)
		}
	}
}

func TestAppendSkillPromptsFromResolved_NoSkillsReturnsBaseUnchanged(t *testing.T) {
	base := "BASE"
	if got := AppendSkillPromptsFromResolved(base, nil); got != base {
		t.Errorf("expected base unchanged for nil skills, got %q", got)
	}
	// Skills with no prompt and no runtime settings should not add a section.
	empty := []ResolvedSkill{{Name: "noop"}}
	if got := AppendSkillPromptsFromResolved(base, empty); got != base {
		t.Errorf("expected base unchanged for empty-prompt skills, got %q", got)
	}
}

func TestAppendSkillPromptsFromResolved_PlanningProfileSettings(t *testing.T) {
	base := "BASE"
	skills := []ResolvedSkill{{
		Name:            "workspace-planning",
		PlanningProfile: true,
		Config:          map[string]any{"mode": "feature", "write_prd": true},
	}}

	out := AppendSkillPromptsFromResolved(base, skills)
	if !strings.Contains(out, "Workspace Binding Settings") {
		t.Errorf("expected planning binding settings section, got: %q", out)
	}
	if !strings.Contains(out, "Planning mode: feature") {
		t.Errorf("expected planning mode line in output")
	}
}

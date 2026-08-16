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

// A skill's config no longer reaches the prompt (FR-181, FR-182).
//
// The old "Workspace Binding Settings" block serialized planning config —
// write_prd, require_branch, default_execution_mode — into the system prompt.
// It read like policy and was a paragraph a model could ignore. Those controls
// are compiled now; a skill carrying config gets no special prompt treatment.
func TestAppendSkillPromptsFromResolved_ConfigIsNotRenderedAsPolicy(t *testing.T) {
	base := "BASE"
	skills := []ResolvedSkill{{
		Name:   "workspace-planning",
		Config: map[string]any{"mode": "feature", "write_prd": true},
	}}

	out := AppendSkillPromptsFromResolved(base, skills)
	if strings.Contains(out, "Workspace Binding Settings") {
		t.Errorf("skill config was rendered as policy: %q", out)
	}
	if strings.Contains(out, "write_prd") || strings.Contains(out, "Planning mode") {
		t.Errorf("skill config leaked into the prompt: %q", out)
	}
	// A config-only skill with no prompt contributes nothing at all.
	if out != base {
		t.Errorf("a skill with no prompt changed the base prompt: %q", out)
	}
}

// A skill's PROMPT still reaches the agent. Skills remain context and
// capability; only their authority to declare policy is gone (FR-190).
func TestAppendSkillPromptsFromResolved_PromptsStillReachTheAgent(t *testing.T) {
	out := AppendSkillPromptsFromResolved("BASE", []ResolvedSkill{{
		Name:   "file-janitor",
		Prompt: "Tidy downloads before filing them.",
		Config: map[string]any{"mode": "feature"},
	}})

	if !strings.Contains(out, "file-janitor") {
		t.Errorf("the skill name is missing: %q", out)
	}
	if !strings.Contains(out, "Tidy downloads before filing them.") {
		t.Errorf("the skill prompt is missing: %q", out)
	}
}

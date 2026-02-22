package chathttp

import (
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/skills"
)

type mockSkillsManager struct {
	enabledSkills []skills.Skill
	err           error
}

func (m *mockSkillsManager) GetSkill(string, string) (*skills.Skill, bool, error) {
	return nil, false, nil
}

func (m *mockSkillsManager) ListSkills(string) ([]skills.Skill, error) {
	return nil, nil
}

func (m *mockSkillsManager) ListEnabledSkillsWithPrompts(string) ([]skills.Skill, error) {
	return m.enabledSkills, m.err
}

func TestBuildSystemPromptWithSkills_NilManager(t *testing.T) {
	h := &Handler{}
	ag := &agent.Agent{}
	result := h.buildSystemPromptWithSkills(ag, "test-agent", "default prompt")
	if result != "default prompt" {
		t.Fatalf("expected default prompt, got %q", result)
	}
}

func TestBuildSystemPromptWithSkills_NoEnabledSkills(t *testing.T) {
	h := &Handler{
		skillsManager: &mockSkillsManager{enabledSkills: nil},
	}
	ag := &agent.Agent{}
	result := h.buildSystemPromptWithSkills(ag, "test-agent", "default prompt")
	if result != "default prompt" {
		t.Fatalf("expected default prompt, got %q", result)
	}
}

func TestBuildSystemPromptWithSkills_WithSkills(t *testing.T) {
	h := &Handler{
		skillsManager: &mockSkillsManager{
			enabledSkills: []skills.Skill{
				{Name: "mac-automation", Prompt: "Use osascript for macOS automation."},
			},
		},
	}
	ag := &agent.Agent{}
	result := h.buildSystemPromptWithSkills(ag, "test-agent", "default prompt")

	if !strings.Contains(result, "default prompt") {
		t.Fatalf("expected base prompt to be included")
	}
	if !strings.Contains(result, "# Active Skills") {
		t.Fatalf("expected Active Skills header")
	}
	if !strings.Contains(result, "## mac-automation") {
		t.Fatalf("expected skill name header")
	}
	if !strings.Contains(result, "Use osascript for macOS automation.") {
		t.Fatalf("expected skill prompt content")
	}
}

func TestBuildSystemPromptWithSkills_EmptyAgentName(t *testing.T) {
	h := &Handler{
		skillsManager: &mockSkillsManager{
			enabledSkills: []skills.Skill{
				{Name: "test", Prompt: "Test prompt"},
			},
		},
	}
	ag := &agent.Agent{}
	result := h.buildSystemPromptWithSkills(ag, "", "default prompt")
	if result != "default prompt" {
		t.Fatalf("expected default prompt when agentName is empty, got %q", result)
	}
}

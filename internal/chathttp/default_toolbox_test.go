package chathttp

import (
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/skills"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Direct-chat Default Toolbox coverage (task 1.17; PRD FR-24–FR-28).

func newDefaultToolboxHandler(available ...skills.Skill) *Handler {
	return &Handler{skillsManager: &mockSkillsManager{enabledSkills: available}}
}

// The Default Toolbox NARROWS what direct chat injects: a skill the agent has
// enabled but has not selected stays out (FR-24, FR-2).
func TestDirectChat_DefaultToolboxSelectsWhichSkillsAreActive(t *testing.T) {
	h := newDefaultToolboxHandler(
		skills.Skill{Name: "mac-automation", Prompt: "Use osascript."},
		skills.Skill{Name: "code-review", Prompt: "Review carefully."},
	)
	ag := &resolvedChatAgent{Agent: &agent.Agent{
		DefaultToolbox: &types.AgentDefaultToolbox{
			Skills: []types.DefaultToolboxSkillRef{{CapabilityID: "code-review", DisplayName: "code-review"}},
		},
	}}

	result := h.buildSystemPromptWithSkills(ag, "test-agent", "default prompt")

	if !strings.Contains(result, "## code-review") {
		t.Fatalf("expected the selected skill to be injected, got %q", result)
	}
	if strings.Contains(result, "mac-automation") {
		t.Fatalf("expected the unselected skill to stay out of the prompt, got %q", result)
	}
}

// Selection is case-insensitive, matching how skill identities normalize
// everywhere else (FR-30).
func TestDirectChat_DefaultToolboxMatchesSkillNamesCaseInsensitively(t *testing.T) {
	h := newDefaultToolboxHandler(skills.Skill{Name: "Mac-Automation", Prompt: "Use osascript."})
	ag := &resolvedChatAgent{Agent: &agent.Agent{
		DefaultToolbox: &types.AgentDefaultToolbox{
			Skills: []types.DefaultToolboxSkillRef{{CapabilityID: "mac-automation"}},
		},
	}}

	if result := h.buildSystemPromptWithSkills(ag, "test-agent", "default prompt"); !strings.Contains(result, "## Mac-Automation") {
		t.Fatalf("expected a case-insensitive match, got %q", result)
	}
}

// An empty Default Toolbox is a real selection: nothing is active.
func TestDirectChat_EmptyDefaultToolboxActivatesNothing(t *testing.T) {
	h := newDefaultToolboxHandler(skills.Skill{Name: "mac-automation", Prompt: "Use osascript."})
	ag := &resolvedChatAgent{Agent: &agent.Agent{DefaultToolbox: &types.AgentDefaultToolbox{}}}

	if result := h.buildSystemPromptWithSkills(ag, "test-agent", "default prompt"); result != "default prompt" {
		t.Fatalf("expected an empty default toolbox to activate nothing, got %q", result)
	}
}

// An agent that predates the field keeps the pre-Toolbox behavior until
// migration fills it, so an upgrade never silently strips an agent's skills
// (FR-28).
func TestDirectChat_UnmigratedAgentKeepsEveryEnabledSkill(t *testing.T) {
	h := newDefaultToolboxHandler(
		skills.Skill{Name: "mac-automation", Prompt: "Use osascript."},
		skills.Skill{Name: "code-review", Prompt: "Review carefully."},
	)
	ag := &resolvedChatAgent{Agent: &agent.Agent{}}

	result := h.buildSystemPromptWithSkills(ag, "test-agent", "default prompt")
	if !strings.Contains(result, "## mac-automation") || !strings.Contains(result, "## code-review") {
		t.Fatalf("expected an unmigrated agent to keep every enabled skill, got %q", result)
	}
}

// Workspace resolution supplies its own effective skills, and the global
// Default Toolbox must not second-guess them (FR-25, FR-26).
func TestDirectChat_WorkspaceResolvedSkillsBypassTheDefaultToolbox(t *testing.T) {
	h := newDefaultToolboxHandler(skills.Skill{Name: "mac-automation", Prompt: "Use osascript."})
	ag := &resolvedChatAgent{
		Agent: &agent.Agent{DefaultToolbox: &types.AgentDefaultToolbox{}},
		EffectiveSkills: []workspace.ResolvedSkill{
			{Name: "workspace-planning", Prompt: "Plan work before executing it."},
		},
	}

	if result := h.buildSystemPromptWithSkills(ag, "test-agent", "default prompt"); !strings.Contains(result, "workspace-planning") {
		t.Fatalf("expected workspace-resolved skills to be used as-is, got %q", result)
	}
}

// cloneAgentForChat must deep-copy the Default Toolbox so a chat-scoped agent
// copy can never write through into the reusable definition (FR-156).
func TestCloneAgentForChat_DeepCopiesTheDefaultToolbox(t *testing.T) {
	original := &agent.Agent{DefaultToolbox: &types.AgentDefaultToolbox{
		Skills: []types.DefaultToolboxSkillRef{{CapabilityID: "code-review"}},
	}}

	clone := cloneAgentForChat(original)
	clone.DefaultToolbox.Skills[0].CapabilityID = "mutated"

	if original.DefaultToolbox.Skills[0].CapabilityID != "code-review" {
		t.Fatalf("expected the original default toolbox to be untouched, got %q", original.DefaultToolbox.Skills[0].CapabilityID)
	}
}

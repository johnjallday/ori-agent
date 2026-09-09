package workspaceroles

import (
	"testing"

	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// TestFromTemplateAgentsProposesTheBlueprintSetup covers what the Create form
// needs to stop hiding: every ordinary blueprint role declares a real system
// prompt, and the form has to show it rather than an empty box.
func TestFromTemplateAgentsProposesTheBlueprintSetup(t *testing.T) {
	roles := FromTemplateAgents([]projecttemplates.AgentSpec{{
		Name:         "Content Lead",
		Type:         "general",
		Model:        "gpt-5",
		Provider:     "openai",
		SystemPrompt: "You are the content lead. Hold the brand voice.",
	}})
	if len(roles) != 1 || roles[0].Proposed == nil {
		t.Fatalf("roles = %#v", roles)
	}
	proposed := *roles[0].Proposed
	if proposed.SystemPrompt != "You are the content lead. Hold the brand voice." {
		t.Fatalf("proposed prompt = %q", proposed.SystemPrompt)
	}
	if proposed.Type != "general" || proposed.Model != "gpt-5" || proposed.Provider != "openai" {
		t.Fatalf("proposed setup = %#v", proposed)
	}
}

// TestFromAssistantProgramProposesNoPrompt pins the boundary the staffing
// adapter already enforces: an assistant program's role prompts are never
// disclosed in a projection. The server applies them itself at commit time.
func TestFromAssistantProgramProposesNoPrompt(t *testing.T) {
	roles := FromAssistantProgram(&workspace.AssistantProgramDeclaration{
		Roles: []workspace.AssistantProgramRoleSpec{{
			ID: "home_guide", Label: "Home Guide", Required: true, Primary: true,
			SystemPrompt: "home-only prompt",
		}},
	})
	if len(roles) != 1 {
		t.Fatalf("roles = %#v", roles)
	}
	if roles[0].Proposed != nil {
		t.Fatalf("assistant-program role disclosed a proposed setup: %#v", *roles[0].Proposed)
	}
}

// TestBuildDropsProposedOnceFilled: a filled role has nothing left to propose,
// and the holder's real setup lives on its own definition.
func TestBuildDropsProposedOnceFilled(t *testing.T) {
	roles := []Role{{
		ID: "producer", Label: "Producer", Scope: ScopeProject, Required: true, Primary: true,
		Proposed: &ProposedSetup{SystemPrompt: "run the session"},
	}}
	empty := Build(Input{WorkspaceID: "ws", ViewScope: ScopeProject, Roles: roles, Lookup: lookupOf()})
	if empty.Roles[0].Proposed == nil {
		t.Fatal("an empty role lost its proposed setup")
	}

	filled := Build(Input{
		WorkspaceID: "ws", ViewScope: ScopeProject, Roles: roles,
		Attachments: []Attachment{{Name: "Producer", RoleID: "producer"}},
		Lookup:      lookupOf("Producer"),
	})
	if filled.Roles[0].Proposed != nil {
		t.Fatalf("a filled role still proposes a setup: %#v", *filled.Roles[0].Proposed)
	}
}

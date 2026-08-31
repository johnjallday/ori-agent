package sessionhttp

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/personalhq"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
)

const testPAFPromptFragment = "You are the user's chosen personal assistant. Keep the working agreement visible and use the existing confirmation gates."

func TestPersonalAssistantTemplatePlanSubstitutesIdentityWithoutMutatingBase(t *testing.T) {
	handler, _, _, cleanup := templateTestEnv(t)
	defer cleanup()
	if err := projecttemplates.EnsureLibrary(handler.templatesRootResolver()); err != nil {
		t.Fatalf("EnsureLibrary: %v", err)
	}
	base, err := handler.personalHQTemplate()
	if err != nil {
		t.Fatalf("personalHQTemplate: %v", err)
	}
	baseEntry := base.Agents[0]
	baseJournal := base.Agents[1]
	appearance := &types.AgentAppearance{
		Mode:      types.AppearanceModeCharacter,
		Generated: &types.GeneratedAppearance{Color: "#112233"},
		Character: &types.CharacterAppearance{CatalogID: "navigator", CatalogVersion: 1},
	}
	customized, err := applyPersonalAssistantTemplateOptions(base, personalhq.AssistantCreationOptions{
		DisplayName: "Ada", Appearance: appearance, Role: types.RoleOrchestrator,
		SystemPromptFragment: testPAFPromptFragment,
	})
	if err != nil {
		t.Fatalf("applyPersonalAssistantTemplateOptions: %v", err)
	}

	if customized.Agents[0].Name != "Ada" || customized.Agents[0].Role != string(types.RoleOrchestrator) {
		t.Fatalf("custom entry = %#v", customized.Agents[0])
	}
	if customized.Agents[0].Appearance == appearance || !reflect.DeepEqual(customized.Agents[0].Appearance, appearance) {
		t.Fatal("appearance was not defensively reused through the shared appearance type")
	}
	if !strings.Contains(customized.Agents[0].SystemPrompt, "own prioritization, briefs, follow-ups, and routing") ||
		!strings.Contains(customized.Agents[0].SystemPrompt, testPAFPromptFragment) {
		t.Fatal("custom prompt did not preserve the bounded Chief of Staff scope plus PAF fragment")
	}
	if customized.Agents[1].Name != "Journal" || customized.Agents[1].Role != baseJournal.Role ||
		customized.Agents[1].SystemPrompt != baseJournal.SystemPrompt {
		t.Fatal("Journal support specialist was changed")
	}
	if !reflect.DeepEqual(base.Agents[0], baseEntry) || !reflect.DeepEqual(base.Agents[1], baseJournal) {
		t.Fatal("PAF substitution mutated the shared base template")
	}

	plan := handler.buildTemplateAgentPlan(customized)
	if len(plan.Agents) != 2 || plan.Agents[0].Name != "Ada" || plan.Agents[0].Appearance == nil ||
		plan.Agents[0].Appearance.CharacterCatalogID() != "navigator" {
		t.Fatalf("template plan = %#v", plan)
	}
}

func TestCreatePersonalAssistantHQCreatesOneSelectedEntryAndSupportMetadata(t *testing.T) {
	handler, _, _, cleanup := templateTestEnv(t)
	defer cleanup()
	if err := projecttemplates.EnsureLibrary(handler.templatesRootResolver()); err != nil {
		t.Fatalf("EnsureLibrary: %v", err)
	}
	appearance := &types.AgentAppearance{
		Mode:      types.AppearanceModeGenerated,
		Generated: &types.GeneratedAppearance{Color: "#225588"},
	}
	result, err := handler.CreatePersonalAssistantHQ(context.Background(), "My HQ", personalhq.AssistantCreationOptions{
		AssistantID: "assistant-id", RequestID: "request-id",
		DisplayName: "Assistant", Appearance: appearance, Role: types.RoleOrchestrator,
		SystemPromptFragment: testPAFPromptFragment,
	})
	if err != nil {
		t.Fatalf("CreatePersonalAssistantHQ: %v", err)
	}
	if result.WorkspaceID == "" || result.EntryAgentInstanceID == "" || result.GlobalAgentProfileName != "Assistant" {
		t.Fatalf("result = %#v", result)
	}
	replayed, err := handler.CreatePersonalAssistantHQ(context.Background(), "Ignored on replay", personalhq.AssistantCreationOptions{
		AssistantID: "assistant-id", RequestID: "request-id",
		DisplayName: "Assistant", Appearance: appearance, Role: types.RoleOrchestrator,
		SystemPromptFragment: testPAFPromptFragment,
	})
	if err != nil || !reflect.DeepEqual(replayed, result) {
		t.Fatalf("idempotent replay = %#v, %v; want %#v", replayed, err, result)
	}
	workspace, err := handler.store.GetWorkspace(context.Background(), result.WorkspaceID)
	if err != nil {
		t.Fatalf("GetWorkspace: %v", err)
	}
	if len(workspace.AgentInstances) != 2 {
		t.Fatalf("agent instances = %#v", workspace.AgentInstances)
	}
	var entryCount, chiefCount, journalCount int
	for _, instance := range workspace.AgentInstances {
		if instance.EntryPoint {
			entryCount++
			if instance.ID != result.EntryAgentInstanceID || instance.Name != "Assistant" {
				t.Fatalf("entry instance = %#v", instance)
			}
		}
		if instance.Name == "Personal Chief of Staff" {
			chiefCount++
		}
		if instance.Name == "Journal" {
			journalCount++
		}
	}
	if entryCount != 1 || chiefCount != 0 || journalCount != 1 {
		t.Fatalf("entry=%d chief=%d journal=%d", entryCount, chiefCount, journalCount)
	}
	presentation, ok := workspace.SharedData[personalAssistantSupportSharedDataKey].(map[string]any)
	if !ok || presentation["support_group"] != personalAssistantSupportGroup {
		t.Fatalf("support metadata = %#v", workspace.SharedData[personalAssistantSupportSharedDataKey])
	}
	supportIDs, ok := presentation["support_agent_instance_ids"].([]any)
	if !ok || len(supportIDs) != 1 || supportIDs[0] == "" {
		t.Fatalf("support ids = %#v", presentation["support_agent_instance_ids"])
	}

	agent, exists := handler.agentStore.GetAgent("Assistant")
	if !exists || agent == nil {
		t.Fatal("selected global agent profile was not created")
	}
	if agent.Appearance == nil || agent.Appearance.GeneratedColor() != "#225588" {
		t.Fatalf("global appearance = %#v", agent.Appearance)
	}
	if !strings.Contains(agent.Settings.SystemPrompt, testPAFPromptFragment) || agent.Role != types.RoleOrchestrator {
		t.Fatalf("global agent config = %#v", agent)
	}
	if len(agent.Capabilities) != 0 || agent.Settings.IsNativeMCPToolsAllowed() {
		t.Fatalf("hire broadened agent capabilities or native MCP: %#v", agent)
	}
	// Canonical workspace creation may install its sandboxed filesystem binding,
	// but hire must not grant either selected agent access to MCP or skills.
	for label, raw := range map[string]json.RawMessage{
		"mcp access": workspace.AgentMCPAccessJSON, "skill access": workspace.AgentSkillAccessJSON,
	} {
		normalized := strings.TrimSpace(string(raw))
		if normalized != "" && normalized != "null" && normalized != "{}" && normalized != "[]" {
			t.Fatalf("hire added %s: %s", label, normalized)
		}
	}
	if _, exists := handler.agentStore.GetAgent("Personal Chief of Staff"); exists {
		t.Fatal("PAF creation also created a duplicate Personal Chief of Staff")
	}
}

func TestPersonalAssistantTemplateRejectsInvalidAndCollidingNamesBeforeCreation(t *testing.T) {
	handler, _, _, cleanup := templateTestEnv(t)
	defer cleanup()
	if err := projecttemplates.EnsureLibrary(handler.templatesRootResolver()); err != nil {
		t.Fatalf("EnsureLibrary: %v", err)
	}
	if err := handler.agentStore.CreateAgent("Taken", &store.CreateAgentConfig{}); err != nil {
		t.Fatalf("CreateAgent(Taken): %v", err)
	}

	for _, name := range []string{"", "Ori", "Ask Ori", "Journal", "bad/name", "Taken"} {
		t.Run(name, func(t *testing.T) {
			before, listErr := handler.store.ListWorkspaces(context.Background())
			if listErr != nil {
				t.Fatalf("ListWorkspaces before: %v", listErr)
			}
			_, err := handler.CreatePersonalAssistantHQ(context.Background(), "My HQ", personalhq.AssistantCreationOptions{
				AssistantID: "assistant-" + strings.ReplaceAll(name, " ", "-"), RequestID: "request-" + strings.ReplaceAll(name, " ", "-"),
				DisplayName: name, Role: types.RoleOrchestrator,
			})
			if err == nil {
				t.Fatalf("name %q was accepted", name)
			}
			after, listErr := handler.store.ListWorkspaces(context.Background())
			if listErr != nil {
				t.Fatalf("ListWorkspaces after: %v", listErr)
			}
			if len(after) != len(before) {
				t.Fatalf("invalid name %q created a workspace", name)
			}
		})
	}
}

func TestPersonalAssistantCreationDoesNotChangeLegacyPersonalHQSetup(t *testing.T) {
	handler, _, _, cleanup := templateTestEnv(t)
	defer cleanup()
	if err := projecttemplates.EnsureLibrary(handler.templatesRootResolver()); err != nil {
		t.Fatalf("EnsureLibrary: %v", err)
	}

	workspaceID, err := handler.CreateFromTemplate(context.Background(), "Legacy HQ", personalhq.PersonalHQTemplateID)
	if err != nil {
		t.Fatalf("CreateFromTemplate: %v", err)
	}
	workspace, err := handler.store.GetWorkspace(context.Background(), workspaceID)
	if err != nil {
		t.Fatalf("GetWorkspace: %v", err)
	}
	if got := currentWorkspaceEntryAgentName(workspace); got != "Personal Chief of Staff" {
		t.Fatalf("legacy entry agent = %q", got)
	}
	if _, exists := workspace.SharedData[personalAssistantSupportSharedDataKey]; exists {
		t.Fatal("legacy setup received PAF-only presentation metadata")
	}
	if len(workspace.AgentInstances) != 2 {
		t.Fatalf("legacy roster = %#v", workspace.AgentInstances)
	}
}

package sessionhttp

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/personalhq"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/types"
)

func profileTestHandler(t *testing.T) (*Handler, func()) {
	t.Helper()
	handler, _, _, cleanup := templateTestEnv(t)
	if err := projecttemplates.EnsureLibrary(handler.templatesRootResolver()); err != nil {
		cleanup()
		t.Fatalf("EnsureLibrary: %v", err)
	}
	return handler, cleanup
}

func profileOptions(name string) personalhq.AssistantCreationOptions {
	return personalhq.AssistantCreationOptions{
		AssistantID: "assistant-id", RequestID: "hire-request-id",
		DisplayName: name, Role: types.RoleOrchestrator,
		Appearance: &types.AgentAppearance{
			Mode:      types.AppearanceModeGenerated,
			Generated: &types.GeneratedAppearance{Color: "#225588"},
		},
		SystemPromptFragment: testPAFPromptFragment,
	}
}

func TestCreatePersonalAssistantProfileUsesTheCanonicalEntrySpecOnly(t *testing.T) {
	handler, cleanup := profileTestHandler(t)
	defer cleanup()

	base, err := handler.personalHQTemplate()
	if err != nil {
		t.Fatalf("personalHQTemplate: %v", err)
	}
	baseEntry := base.Agents[0]

	result, err := handler.CreatePersonalAssistantProfile(context.Background(), profileOptions("Atlas"))
	if err != nil {
		t.Fatalf("CreatePersonalAssistantProfile: %v", err)
	}
	if result.GlobalAgentProfileName != "Atlas" || result.Reused {
		t.Fatalf("result = %#v", result)
	}

	record, found := handler.agentStore.GetAgent("Atlas")
	if !found || record == nil {
		t.Fatal("hired profile was not created")
	}
	// The same orchestrator role, Chief of Staff prompt plus PAF fragment, and
	// validated appearance the eventual HQ entry agent must use.
	if record.Role != types.RoleOrchestrator {
		t.Fatalf("role = %q", record.Role)
	}
	if !strings.Contains(record.Settings.SystemPrompt, "own prioritization, briefs, follow-ups, and routing") ||
		!strings.Contains(record.Settings.SystemPrompt, testPAFPromptFragment) {
		t.Fatalf("prompt did not layer the PAF fragment onto the canonical entry prompt")
	}
	if record.Appearance == nil || record.Appearance.GeneratedColor() != "#225588" {
		t.Fatalf("appearance = %#v", record.Appearance)
	}

	// Nothing else may come into existence.
	if _, journal := handler.agentStore.GetAgent("Journal"); journal {
		t.Fatal("hiring created the Journal support profile")
	}
	if _, chief := handler.agentStore.GetAgent("Personal Chief of Staff"); chief {
		t.Fatal("hiring created a Personal Chief of Staff profile")
	}
	workspaces, err := handler.store.ListWorkspaces(context.Background())
	if err != nil {
		t.Fatalf("ListWorkspaces: %v", err)
	}
	if len(workspaces) != 0 {
		t.Fatalf("hiring created %d workspace(s)", len(workspaces))
	}
	// The immutable base template is untouched.
	if reloaded, reloadErr := handler.personalHQTemplate(); reloadErr != nil ||
		reloaded.Agents[0].Name != baseEntry.Name || reloaded.Agents[0].SystemPrompt != baseEntry.SystemPrompt {
		t.Fatal("profile creation mutated the shared base template")
	}
}

func TestCreatePersonalAssistantProfileStampsDurableProvenance(t *testing.T) {
	handler, cleanup := profileTestHandler(t)
	defer cleanup()

	if _, err := handler.CreatePersonalAssistantProfile(context.Background(), profileOptions("Atlas")); err != nil {
		t.Fatalf("CreatePersonalAssistantProfile: %v", err)
	}
	record, _ := handler.agentStore.GetAgent("Atlas")
	if record == nil || record.Metadata == nil {
		t.Fatal("profile carries no metadata")
	}
	provenance := personalassistant.ProfileProvenanceFromTags("Atlas", record.Metadata.Tags)
	if !provenance.OwnedBy("assistant-id") || provenance.HireRequestID != "hire-request-id" {
		t.Fatalf("provenance = %#v from tags %v", provenance, record.Metadata.Tags)
	}
}

func TestCreatePersonalAssistantProfileReplayResolvesItsOwnProfile(t *testing.T) {
	handler, cleanup := profileTestHandler(t)
	defer cleanup()

	first, err := handler.CreatePersonalAssistantProfile(context.Background(), profileOptions("Atlas"))
	if err != nil {
		t.Fatalf("first create: %v", err)
	}
	second, err := handler.CreatePersonalAssistantProfile(context.Background(), profileOptions("Atlas"))
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if second.GlobalAgentProfileName != first.GlobalAgentProfileName || !second.Reused {
		t.Fatalf("replay = %#v; want the same profile marked reused", second)
	}
	// The markers must not stack on replay.
	record, _ := handler.agentStore.GetAgent("Atlas")
	markers := 0
	for _, tag := range record.Metadata.Tags {
		if strings.HasPrefix(tag, personalassistant.ProfileAssistantMarkerPrefix) {
			markers++
		}
	}
	if markers != 1 {
		t.Fatalf("replay stacked %d ownership markers: %v", markers, record.Metadata.Tags)
	}
}

func TestCreatePersonalAssistantProfileRejectsUnrelatedNameCollisions(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(*testing.T, *Handler)
		options personalhq.AssistantCreationOptions
	}{
		{
			name: "user-created agent with the same name",
			prepare: func(t *testing.T, handler *Handler) {
				if err := handler.agentStore.CreateAgent("Atlas", nil); err != nil {
					t.Fatalf("seed unrelated agent: %v", err)
				}
			},
			options: profileOptions("Atlas"),
		},
		{
			name: "profile owned by another relationship",
			prepare: func(t *testing.T, handler *Handler) {
				other := profileOptions("Atlas")
				other.AssistantID = "another-assistant"
				other.RequestID = "another-request"
				if _, err := handler.CreatePersonalAssistantProfile(context.Background(), other); err != nil {
					t.Fatalf("seed foreign profile: %v", err)
				}
			},
			options: profileOptions("Atlas"),
		},
		{
			name: "same assistant but a different hire request",
			prepare: func(t *testing.T, handler *Handler) {
				if _, err := handler.CreatePersonalAssistantProfile(context.Background(), profileOptions("Atlas")); err != nil {
					t.Fatalf("seed own profile: %v", err)
				}
			},
			options: func() personalhq.AssistantCreationOptions {
				options := profileOptions("Atlas")
				options.RequestID = "a-different-request"
				return options
			}(),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler, cleanup := profileTestHandler(t)
			defer cleanup()
			test.prepare(t, handler)
			_, err := handler.CreatePersonalAssistantProfile(context.Background(), test.options)
			if !errors.Is(err, personalhq.ErrAssistantNameConflict) {
				t.Fatalf("error = %v; want a name conflict", err)
			}
		})
	}
}

func TestCreatePersonalAssistantProfileRejectsReservedAndRosterNames(t *testing.T) {
	handler, cleanup := profileTestHandler(t)
	defer cleanup()

	// "Ask Ori"/"Ori" are the protected system assistant; "Journal" is the
	// Personal HQ support roster. "Personal Chief of Staff" is deliberately NOT
	// in this list: it is the template's own entry-agent name, which the chosen
	// name replaces, so the HQ path allows it and this path must match rather
	// than invent a stricter rule for one of the two.
	for _, name := range []string{"Ask Ori", "Ori", "Journal", "", "   "} {
		if _, err := handler.CreatePersonalAssistantProfile(context.Background(), profileOptions(name)); err == nil {
			t.Fatalf("name %q was accepted", name)
		}
		if _, created := handler.agentStore.GetAgent(name); created {
			t.Fatalf("rejected name %q still created a profile", name)
		}
	}
}

func TestCreatePersonalAssistantProfileMatchesTheHQPathsNameRules(t *testing.T) {
	// The two creation paths must accept and reject exactly the same names, or a
	// hire could succeed and its later HQ build fail on the same input.
	handler, cleanup := profileTestHandler(t)
	defer cleanup()
	base, err := handler.personalHQTemplate()
	if err != nil {
		t.Fatalf("personalHQTemplate: %v", err)
	}

	for _, name := range []string{"Atlas", "Ask Ori", "Ori", "Journal", "Personal Chief of Staff", ""} {
		options := profileOptions(name)
		_, templateErr := applyPersonalAssistantTemplateOptions(base, options)
		_, profileErr := handler.CreatePersonalAssistantProfile(context.Background(), options)
		if (templateErr == nil) != (profileErr == nil) {
			t.Fatalf("name %q: template err=%v but profile err=%v", name, templateErr, profileErr)
		}
		if profileErr == nil {
			// Clean up so the next accepted name is not a self-collision.
			if err := handler.agentStore.DeleteAgent(strings.TrimSpace(name)); err != nil {
				t.Fatalf("cleanup %q: %v", name, err)
			}
		}
	}
}

func TestCreatePersonalAssistantProfileRequiresBoundedOperationIdentity(t *testing.T) {
	handler, cleanup := profileTestHandler(t)
	defer cleanup()

	for _, mutate := range []func(*personalhq.AssistantCreationOptions){
		func(o *personalhq.AssistantCreationOptions) { o.AssistantID = "" },
		func(o *personalhq.AssistantCreationOptions) { o.RequestID = "  " },
	} {
		options := profileOptions("Atlas")
		mutate(&options)
		if _, err := handler.CreatePersonalAssistantProfile(context.Background(), options); err == nil {
			t.Fatal("profile created without a bounded operation identity")
		}
	}
	if _, created := handler.agentStore.GetAgent("Atlas"); created {
		t.Fatal("an unidentified request still created a profile")
	}
}

func TestCreatePersonalAssistantProfileRejectsSmuggledAppearance(t *testing.T) {
	handler, cleanup := profileTestHandler(t)
	defer cleanup()

	options := profileOptions("Atlas")
	// An uploaded image must come from the dedicated upload endpoint; a hire
	// request cannot put a path into the agent record.
	options.Appearance = &types.AgentAppearance{
		Mode:     types.AppearanceModeUploaded,
		Uploaded: &types.UploadedAppearance{Image: "/etc/passwd"},
	}
	if _, err := handler.CreatePersonalAssistantProfile(context.Background(), options); err == nil {
		t.Fatal("uploaded appearance path was accepted")
	}
	if _, created := handler.agentStore.GetAgent("Atlas"); created {
		t.Fatal("rejected appearance still created a profile")
	}
}

func TestCreatePersonalAssistantProfileRejectsNonOrchestratorRole(t *testing.T) {
	handler, cleanup := profileTestHandler(t)
	defer cleanup()

	options := profileOptions("Atlas")
	options.Role = types.RoleResearcher
	if _, err := handler.CreatePersonalAssistantProfile(context.Background(), options); err == nil {
		t.Fatal("a non-orchestrator personal assistant role was accepted")
	}
}

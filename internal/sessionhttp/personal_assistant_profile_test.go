package sessionhttp

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
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

// --- HQ creation around an already-hired profile ---------------------------

func TestCreatePersonalAssistantHQReusesTheHiredProfileAsEntryAgent(t *testing.T) {
	handler, cleanup := profileTestHandler(t)
	defer cleanup()
	ctx := context.Background()

	// Hire first: one profile, no workspace.
	hire := profileOptions("Atlas")
	if _, err := handler.CreatePersonalAssistantProfile(ctx, hire); err != nil {
		t.Fatalf("hire: %v", err)
	}
	before, _ := handler.agentStore.GetAgent("Atlas")
	beforePrompt := before.Settings.SystemPrompt
	beforeModel := before.Settings.Model
	beforeTags := append([]string(nil), before.Metadata.Tags...)

	// Then build HQ under a DIFFERENT request id — the HQ request, not the hire.
	build := profileOptions("Atlas")
	build.RequestID = "hq-request-1"
	result, err := handler.CreatePersonalAssistantHQ(ctx, "Personal HQ", build)
	if err != nil {
		t.Fatalf("CreatePersonalAssistantHQ: %v", err)
	}
	if result.WorkspaceID == "" || result.EntryAgentInstanceID == "" ||
		result.GlobalAgentProfileName != "Atlas" {
		t.Fatalf("result = %#v", result)
	}

	// Exactly one global profile: the hired one, reused rather than duplicated.
	names := handler.agentStore.ListAgents()
	atlasCount := 0
	for _, name := range names {
		if name == "Atlas" {
			atlasCount++
		}
		if name == "Personal Chief of Staff" {
			t.Fatal("HQ creation created a Personal Chief of Staff profile")
		}
	}
	if atlasCount != 1 {
		t.Fatalf("expected one Atlas profile, found %d in %v", atlasCount, names)
	}

	// The reused profile is preserved, not rewritten from the HQ request.
	after, _ := handler.agentStore.GetAgent("Atlas")
	if after.Settings.SystemPrompt != beforePrompt || after.Settings.Model != beforeModel {
		t.Fatal("HQ creation mutated the hired profile's saved prompt or model")
	}
	if len(after.Metadata.Tags) != len(beforeTags) {
		t.Fatalf("HQ creation changed the profile's provenance markers: %v -> %v",
			beforeTags, after.Metadata.Tags)
	}

	// The workspace carries the hired assistant as its stable entry instance,
	// plus the Journal support instance exactly once.
	workspace, err := handler.store.GetWorkspace(ctx, result.WorkspaceID)
	if err != nil {
		t.Fatalf("GetWorkspace: %v", err)
	}
	var entries, journals int
	for _, instance := range workspace.AgentInstances {
		if instance.EntryPoint {
			entries++
			if instance.ID != result.EntryAgentInstanceID || instance.Name != "Atlas" {
				t.Fatalf("entry instance = %#v", instance)
			}
		}
		if instance.Name == "Journal" {
			journals++
		}
		if instance.Name == "Personal Chief of Staff" {
			t.Fatal("HQ workspace attached a Personal Chief of Staff instance")
		}
	}
	if entries != 1 || journals != 1 {
		t.Fatalf("entry=%d journal=%d instances=%#v", entries, journals, workspace.AgentInstances)
	}

	// Restart-safe lookup is by stable assistant/request identity, not name.
	metadata, ok := workspace.SharedData[personalAssistantSupportSharedDataKey].(map[string]any)
	if !ok {
		t.Fatal("workspace carries no personal-assistant provenance metadata")
	}
	if metadata["assistant_id"] != "assistant-id" || metadata["request_id"] != "hq-request-1" {
		t.Fatalf("workspace provenance = %#v", metadata)
	}

	// Hiring and HQ creation grant no new permission surface: no MCP binding, no
	// directory/filesystem reference beyond the template's own defaults, and the
	// reused profile's tags carry only the two PAF provenance markers.
	// The template's own workspace-scoped filesystem binding is expected —
	// every workspace gets one rooted at its own folder. What must NOT appear is
	// any binding to an external server, a root outside the workspace's own
	// folder, or a native MCP/CLI opt-in beyond the template default.
	var bindings []map[string]any
	if err := json.Unmarshal(workspace.MCPBindingsJSON, &bindings); err != nil {
		t.Fatalf("decode mcp bindings: %v", err)
	}
	for _, binding := range bindings {
		if binding["server_name"] != "filesystem" {
			t.Fatalf("hq creation attached an unexpected mcp server: %#v", binding)
		}
		config, _ := binding["config"].(map[string]any)
		roots, _ := config["roots"].([]any)
		for _, root := range roots {
			rootPath, _ := root.(string)
			if !strings.HasSuffix(rootPath, "/personal-hq") {
				t.Fatalf("hq creation widened filesystem scope beyond its own folder: %q", rootPath)
			}
		}
	}
	// Likewise, the template's own directory reference to the workspace's own
	// folder is expected; anything else is a filesystem-scope expansion.
	var directories []map[string]any
	if err := json.Unmarshal(workspace.DirectoryReferencesJSON, &directories); err != nil {
		t.Fatalf("decode directory references: %v", err)
	}
	for _, dir := range directories {
		path, _ := dir["path"].(string)
		if !strings.HasSuffix(path, "/personal-hq") {
			t.Fatalf("hq creation widened filesystem scope beyond its own folder: %q", path)
		}
	}
	if workspace.AllowNativeMCPCLI {
		t.Fatal("hq creation enabled native MCP/CLI beyond the template default")
	}
	if len(after.Metadata.Tags) != 2 {
		t.Fatalf("hq creation added unexpected profile tags: %v", after.Metadata.Tags)
	}
}

func TestCreatePersonalAssistantHQReplayReturnsTheSameCanonicalRecords(t *testing.T) {
	handler, cleanup := profileTestHandler(t)
	defer cleanup()
	ctx := context.Background()

	if _, err := handler.CreatePersonalAssistantProfile(ctx, profileOptions("Atlas")); err != nil {
		t.Fatalf("hire: %v", err)
	}
	build := profileOptions("Atlas")
	build.RequestID = "hq-request-1"

	first, err := handler.CreatePersonalAssistantHQ(ctx, "Personal HQ", build)
	if err != nil {
		t.Fatalf("first build: %v", err)
	}
	second, err := handler.CreatePersonalAssistantHQ(ctx, "Ignored on replay", build)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("replay = %#v; want %#v", second, first)
	}
	workspaces, err := handler.store.ListWorkspaces(ctx)
	if err != nil {
		t.Fatalf("ListWorkspaces: %v", err)
	}
	if len(workspaces) != 1 {
		t.Fatalf("replay created %d workspaces", len(workspaces))
	}
}

func TestCreatePersonalAssistantHQRejectsAForeignSameNamedProfile(t *testing.T) {
	handler, cleanup := profileTestHandler(t)
	defer cleanup()
	ctx := context.Background()

	// An unrelated user-created agent that happens to share the name.
	if err := handler.agentStore.CreateAgent("Atlas", nil); err != nil {
		t.Fatalf("seed unrelated agent: %v", err)
	}
	build := profileOptions("Atlas")
	build.RequestID = "hq-request-1"
	if _, err := handler.CreatePersonalAssistantHQ(ctx, "Personal HQ", build); !errors.Is(err, personalhq.ErrAssistantNameConflict) {
		t.Fatalf("error = %v; want a name conflict", err)
	}
	workspaces, err := handler.store.ListWorkspaces(ctx)
	if err != nil {
		t.Fatalf("ListWorkspaces: %v", err)
	}
	if len(workspaces) != 0 {
		t.Fatal("a rejected build still created a workspace")
	}
}

func TestCreatePersonalAssistantHQRejectsAnotherRelationshipsProfile(t *testing.T) {
	handler, cleanup := profileTestHandler(t)
	defer cleanup()
	ctx := context.Background()

	other := profileOptions("Atlas")
	other.AssistantID = "another-assistant"
	if _, err := handler.CreatePersonalAssistantProfile(ctx, other); err != nil {
		t.Fatalf("seed foreign profile: %v", err)
	}
	build := profileOptions("Atlas")
	build.RequestID = "hq-request-1"
	if _, err := handler.CreatePersonalAssistantHQ(ctx, "Personal HQ", build); !errors.Is(err, personalhq.ErrAssistantNameConflict) {
		t.Fatalf("error = %v; want a name conflict", err)
	}
}

func TestCreatePersonalAssistantHQStillWorksWithoutAPriorProfile(t *testing.T) {
	// The legacy single-transaction path: no profile exists yet, so the template
	// creates one. This must remain unchanged for pre-amendment operations.
	handler, cleanup := profileTestHandler(t)
	defer cleanup()

	build := profileOptions("Atlas")
	build.RequestID = "hire-request-id"
	result, err := handler.CreatePersonalAssistantHQ(context.Background(), "Personal HQ", build)
	if err != nil {
		t.Fatalf("CreatePersonalAssistantHQ with no prior profile: %v", err)
	}
	if result.GlobalAgentProfileName != "Atlas" || result.EntryAgentInstanceID == "" {
		t.Fatalf("result = %#v", result)
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

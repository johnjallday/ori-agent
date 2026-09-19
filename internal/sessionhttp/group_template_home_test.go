package sessionhttp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/blueprintreadiness"
	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

func newGroupTemplateHomeHandler(t *testing.T) (*Handler, agentworkspace.Store, string, string, func()) {
	t.Helper()
	template := groupedPlanTemplate(projecttemplates.GroupPolicyRequired)
	handler, store, cleanup := newPolicyHandler(t, &template)
	installPlanAgentStore(t, handler)
	handler.SetGroupTemplateCatalog(func(string) ([]GroupTemplateCatalogEntry, bool, error) {
		return []GroupTemplateCatalogEntry{{Template: template, Readiness: handler.revalidateBlueprintReadiness(template), Active: true}}, false, nil
	})
	managed := getGroupTemplates(t, handler).GroupTemplates[1]
	return handler, store, managed.ID, managed.Revision, cleanup
}

func postGroupTemplateHome(t *testing.T, handler *Handler, commit bool, raw string) (int, map[string]any) {
	t.Helper()
	path, serve := "/api/workspaces/group-templates/review", handler.ReviewGroupTemplateHome
	if commit {
		path, serve = "/api/workspaces/group-templates/commit", handler.CommitGroupTemplateHome
	}
	response := httptest.NewRecorder()
	serve(response, httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(raw)))
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %d %s: %v", response.Code, response.Body.String(), err)
	}
	return response.Code, body
}

func groupTemplateHomeBody(t *testing.T, fields map[string]any) string {
	t.Helper()
	encoded, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func TestGroupTemplateHome_RejectsForgedAndMixedPayloadsBeforeSideEffects(t *testing.T) {
	handler, store, id, revision, cleanup := newGroupTemplateHomeHandler(t)
	defer cleanup()

	for name, raw := range map[string]string{
		"project template ref": groupTemplateHomeBody(t, map[string]any{"group_template_id": id, "revision": revision, "name": "Lab", "template_id": "plugin:neutral:project"}),
		"caller owner":         groupTemplateHomeBody(t, map[string]any{"group_template_id": id, "revision": revision, "name": "Lab", "owner_user_id": "someone-else"}),
		"parent":               groupTemplateHomeBody(t, map[string]any{"group_template_id": id, "revision": revision, "name": "Lab", "parent_id": "group-1"}),
		"project team":         groupTemplateHomeBody(t, map[string]any{"group_template_id": id, "revision": revision, "name": "Lab", "role_staffing": []any{}}),
		"trailing data":        groupTemplateHomeBody(t, map[string]any{"group_template_id": id, "revision": revision, "name": "Lab"}) + "{}",
		"path name":            groupTemplateHomeBody(t, map[string]any{"group_template_id": id, "revision": revision, "name": "../Lab"}),
		"empty name":           groupTemplateHomeBody(t, map[string]any{"group_template_id": id, "revision": revision, "name": " "}),
		"review commit fields": groupTemplateHomeBody(t, map[string]any{"group_template_id": id, "revision": revision, "name": "Lab", "idempotency_key": "x"}),
		"general":              groupTemplateHomeBody(t, map[string]any{"group_template_id": projecttemplates.GroupTemplateGeneralID, "revision": "none", "name": "Lab"}),
	} {
		t.Run(name, func(t *testing.T) {
			if code, body := postGroupTemplateHome(t, handler, false, raw); code != http.StatusBadRequest {
				t.Fatalf("status = %d body=%v, want 400", code, body)
			}
		})
	}
	if code, body := postGroupTemplateHome(t, handler, false, groupTemplateHomeBody(t, map[string]any{"group_template_id": id, "revision": "stale", "name": "Lab"})); code != http.StatusConflict {
		t.Fatalf("stale revision status = %d body=%v", code, body)
	}
	if code, body := postGroupTemplateHome(t, handler, true, groupTemplateHomeBody(t, map[string]any{"group_template_id": id, "revision": revision, "name": "Lab"})); code != http.StatusBadRequest {
		t.Fatalf("commit without consent status = %d body=%v", code, body)
	}
	if ids, err := store.List(); err != nil || len(ids) != 0 {
		t.Fatalf("rejected requests created workspaces: ids=%v err=%v", ids, err)
	}
}

func TestGroupTemplateHome_CreatesNamedHomeThenReplaysAndReusesHonestly(t *testing.T) {
	handler, store, id, revision, cleanup := newGroupTemplateHomeHandler(t)
	defer cleanup()
	allowlist := agentworkspace.NewAllowlist(filepath.Join(t.TempDir(), agentworkspace.DefaultAllowlistFilename))
	handler.SetWorkspaceAllowlist(allowlist)

	selection := map[string]any{"group_template_id": id, "revision": revision, "name": "Lab Portfolio"}
	code, body := postGroupTemplateHome(t, handler, false, groupTemplateHomeBody(t, selection))
	review, _ := body["group_template_review"].(map[string]any)
	if code != http.StatusOK || review == nil || review["reuse"] != false || review["home_name"] != "Lab Portfolio" || review["review_token"] == "" {
		t.Fatalf("create review = %d %v", code, body)
	}
	if ids, _ := store.List(); len(ids) != 0 {
		t.Fatalf("review created workspaces %v", ids)
	}

	commit := map[string]any{
		"group_template_id": id, "revision": revision, "name": "Lab Portfolio",
		"group_review_token": review["review_token"], "idempotency_key": "create-lab",
	}
	code, body = postGroupTemplateHome(t, handler, true, groupTemplateHomeBody(t, commit))
	result, _ := body["group_template"].(map[string]any)
	if code != http.StatusOK || result == nil || result["home_created"] != true || result["created_by_this_operation"] != true ||
		result["name_applied"] != true || result["home_name"] != "Lab Portfolio" || result["idempotent_replay"] != false {
		t.Fatalf("commit = %d %v", code, body)
	}
	homeID, _ := result["home_workspace_id"].(string)
	home, err := store.Get(homeID)
	if err != nil || home.Kind != "group" || home.GetAssistantProgramState().GroupTemplate == nil || len(home.GetAgentInstances()) != 0 {
		t.Fatalf("created Home = %+v, %v", home, err)
	}
	// Like any local creation, the new Home is owned by this data directory, so
	// a coordinator staffed on it later is restored after a restart.
	if !allowlist.Contains(homeID) {
		t.Fatal("a Home created by this operation was not recorded as locally owned")
	}

	code, body = postGroupTemplateHome(t, handler, true, groupTemplateHomeBody(t, commit))
	replay, _ := body["group_template"].(map[string]any)
	if code != http.StatusOK || replay["idempotent_replay"] != true || replay["home_workspace_id"] != homeID || replay["created_by_this_operation"] != true {
		t.Fatalf("replay = %d %v", code, body)
	}

	listed := getGroupTemplates(t, handler).GroupTemplates[1]
	if listed.Availability.State != groupTemplateAvailabilityReusable || listed.Home.WorkspaceID != homeID {
		t.Fatalf("listing after create = %+v %+v", listed.Availability, listed.Home)
	}
	reuseSelection := map[string]any{"group_template_id": id, "revision": revision, "name": "Ignored Name"}
	code, body = postGroupTemplateHome(t, handler, false, groupTemplateHomeBody(t, reuseSelection))
	reuseReview, _ := body["group_template_review"].(map[string]any)
	if code != http.StatusOK || reuseReview["reuse"] != true || reuseReview["home_workspace_id"] != homeID || reuseReview["home_name"] != "Lab Portfolio" {
		t.Fatalf("reuse review = %d %v", code, body)
	}
	reuseCommit := map[string]any{
		"group_template_id": id, "revision": revision, "name": "Ignored Name",
		"group_review_token": reuseReview["review_token"], "idempotency_key": "reuse-lab",
	}
	code, body = postGroupTemplateHome(t, handler, true, groupTemplateHomeBody(t, reuseCommit))
	reused, _ := body["group_template"].(map[string]any)
	if code != http.StatusOK || reused["home_created"] != false || reused["created_by_this_operation"] != false ||
		reused["name_applied"] != false || reused["home_name"] != "Lab Portfolio" {
		t.Fatalf("reuse commit = %d %v", code, body)
	}
	if ids, _ := store.List(); len(ids) != 1 {
		t.Fatalf("create/replay/reuse produced workspaces %v", ids)
	}
}

func TestGroupTemplateHome_IndependentProviderCreatesExactHomeWithoutProject(t *testing.T) {
	home := projecttemplates.AssistantProgramHome{
		SchemaVersion: 1, Version: 1, ID: "music-producer-assistant",
		StationName: "Music Production Home", DefaultPrimaryName: "Portfolio Manager", HireTitle: "Staff this Home",
		Roles:      []projecttemplates.AssistantProgramHomeRole{{ID: "portfolio_manager", Label: "Portfolio Manager", Required: true, Primary: true, SystemPrompt: "Coordinate reviewed Home work.", Skills: []string{"music-project-management"}}},
		Stages:     []agentworkspace.AssistantProgramStageSpec{{ID: "foundation", Label: "Foundation", AcceptedCompletionThreshold: 0}},
		Reflection: agentworkspace.AssistantReflectionConfig{MinimumProjects: 3, CadenceHours: 168, MaxProjects: 8, MaxEventsPerProject: 8, MaxCandidates: 8, MaxEvidence: 8, Rubric: "Use approved evidence."},
	}
	if err := projecttemplates.NormalizeAssistantProgramHome(&home); err != nil {
		t.Fatal(err)
	}
	owner := agentworkspace.AssistantProgramHomeOwner{
		PluginID: "music-project-management", PluginVersion: "0.1.0", ProgramID: home.ID,
		HomeSchemaVersion: 1, HomeVersion: 1, DeclarationDigest: projecttemplates.AssistantProgramHomeDigest(home),
		PluginGeneration: 4, ComponentFingerprint: strings.Repeat("a", 64),
	}
	template := projecttemplates.AssistantProgramHomeTemplate(home, owner)
	handler, store, cleanup := newPolicyHandler(t, &template)
	defer cleanup()
	template.Path, template.HasSkeleton, template.ProjectEntry = "", false, nil
	handler.SetInstalledPluginLister(assistantInstalledPluginLister{installed: []plugin.InstalledPlugin{{
		Name: owner.PluginID, Version: owner.PluginVersion, Enabled: true, Generation: owner.PluginGeneration,
		Skills: []string{"music-project-management"}, ComponentFingerprint: owner.ComponentFingerprint,
		WorkspaceSurfaces: &plugin.SurfaceContribution{
			SchemaVersion: 1, Name: owner.PluginID, Version: owner.PluginVersion,
			Protocol: plugin.ProtocolRange{Min: 1, Max: 1}, RequiresHostFeatures: []string{plugin.HostFeatureIndependentProgramHomesV1},
			AssistantProgramHomes: []projecttemplates.AssistantProgramHome{home},
		},
	}}})
	installPlanAgentStore(t, handler)
	handler.SetGroupTemplateCatalog(func(string) ([]GroupTemplateCatalogEntry, bool, error) {
		return []GroupTemplateCatalogEntry{{Template: template, Readiness: blueprintreadiness.Ready(blueprintreadiness.OwnershipPlugin), Active: true}}, false, nil
	})
	legacyKey := agentworkspace.AssistantProgramKey{OwnerUserID: "local", PluginID: "reaper", ProgramID: home.ID}
	legacy, legacyCreated, err := agentworkspace.NewAssistantProgramStore(store).EnsureNamedStation(legacyKey, home.AssistantProgram(), home.StationName)
	if err != nil || !legacyCreated {
		t.Fatalf("legacy same-name Home = %#v, %t, %v", legacy, legacyCreated, err)
	}
	legacyPersisted, err := store.Get(legacy.ID)
	if err != nil {
		t.Fatal(err)
	}
	legacyBefore, err := legacyPersisted.ToJSON()
	if err != nil {
		t.Fatal(err)
	}
	managed := getGroupTemplates(t, handler).GroupTemplates[1]
	if managed.Availability.State == groupTemplateAvailabilityReusable || managed.Home.WorkspaceID == legacy.ID {
		t.Fatalf("legacy same-name Home was offered for independent reuse: %+v %+v", managed.Availability, managed.Home)
	}
	selection := map[string]any{"group_template_id": managed.ID, "revision": managed.Revision, "name": "Renamed Music Home"}
	code, body := postGroupTemplateHome(t, handler, false, groupTemplateHomeBody(t, selection))
	review, _ := body["group_template_review"].(map[string]any)
	if code != http.StatusOK || review == nil {
		t.Fatalf("review = %d %v", code, body)
	}
	selection["group_review_token"] = review["review_token"]
	selection["idempotency_key"] = "independent-home"
	code, body = postGroupTemplateHome(t, handler, true, groupTemplateHomeBody(t, selection))
	result, _ := body["group_template"].(map[string]any)
	if code != http.StatusOK || result["home_created"] != true {
		t.Fatalf("commit = %d %v", code, body)
	}
	workspaceID, _ := result["home_workspace_id"].(string)
	code, body = postGroupTemplateHome(t, handler, true, groupTemplateHomeBody(t, selection))
	replay, _ := body["group_template"].(map[string]any)
	if code != http.StatusOK || replay["idempotent_replay"] != true || replay["home_workspace_id"] != workspaceID {
		t.Fatalf("independent Home replay = %d %v", code, body)
	}
	created, err := store.Get(workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	state := created.GetAssistantProgramState()
	if created.Name != "Renamed Music Home" || created.Kind != "group" || created.ParentID != "" || len(created.GetAgentInstances()) != 0 || state == nil || len(state.LinkedProjectIDs) != 0 {
		t.Fatalf("created independent Home = %+v state=%+v", created, state)
	}
	if state.Key.PluginID != owner.PluginID || state.Key.ProgramID != owner.ProgramID || state.GroupTemplate == nil || state.GroupTemplate.ProgramHomeOwner == nil || state.GroupTemplate.PluginOwner != nil {
		t.Fatalf("independent Home provenance = %+v / %+v", state.Key, state.GroupTemplate)
	}
	legacyAfter, err := store.Get(legacy.ID)
	if err != nil {
		t.Fatal(err)
	}
	legacyBytes, err := legacyAfter.ToJSON()
	if err != nil || !bytes.Equal(legacyBefore, legacyBytes) {
		t.Fatalf("independent setup rewrote the legacy same-name Home: equal=%t err=%v", bytes.Equal(legacyBefore, legacyBytes), err)
	}
	if ids, err := store.List(); err != nil || len(ids) != 2 {
		t.Fatalf("same-name setup workspace ids = %v, %v", ids, err)
	}
	changed := plugin.InstalledPlugin{
		Name: owner.PluginID, Version: owner.PluginVersion, Enabled: true, Generation: owner.PluginGeneration + 1,
		ComponentFingerprint: strings.Repeat("b", 64), WorkspaceSurfaces: &plugin.SurfaceContribution{AssistantProgramHomes: []projecttemplates.AssistantProgramHome{home}},
	}
	handler.SetInstalledPluginLister(assistantInstalledPluginLister{installed: []plugin.InstalledPlugin{changed}})
	if handler.assistantHomeProviderAvailable(created) {
		t.Fatal("changed provider generation left independent Home writable")
	}
	preserved, err := store.Get(workspaceID)
	if err != nil || !preserved.GetAssistantProgramState().PluginAvailable || preserved.Name != "Renamed Music Home" || preserved.GetAssistantProgramState().GroupTemplate.ProgramHomeOwner.DeclarationDigest != owner.DeclarationDigest {
		t.Fatalf("provider change damaged stored Home: %+v, %v", preserved, err)
	}
}

func TestGroupTemplateHome_OnlyAHomeItCreatesIsRecordedAsLocallyOwned(t *testing.T) {
	handler, _, id, revision, cleanup := newGroupTemplateHomeHandler(t)
	defer cleanup()
	allowlist := agentworkspace.NewAllowlist(filepath.Join(t.TempDir(), agentworkspace.DefaultAllowlistFilename))

	// The direct-recovery path records the Home it creates.
	handler.SetWorkspaceAllowlist(allowlist)
	homeID := preparePolicyHome(t, handler)
	if !allowlist.Contains(homeID) {
		t.Fatal("direct recovery did not record the Home it created")
	}

	// Reusing a Home this data directory did not record never records it.
	otherAllowlist := agentworkspace.NewAllowlist(filepath.Join(t.TempDir(), agentworkspace.DefaultAllowlistFilename))
	handler.SetWorkspaceAllowlist(otherAllowlist)
	selection := map[string]any{"group_template_id": id, "revision": revision, "name": "Ignored"}
	_, body := postGroupTemplateHome(t, handler, false, groupTemplateHomeBody(t, selection))
	review, _ := body["group_template_review"].(map[string]any)
	commit := map[string]any{
		"group_template_id": id, "revision": revision, "name": "Ignored",
		"group_review_token": review["review_token"], "idempotency_key": "reuse-unrecorded",
	}
	code, body := postGroupTemplateHome(t, handler, true, groupTemplateHomeBody(t, commit))
	result, _ := body["group_template"].(map[string]any)
	if code != http.StatusOK || result["created_by_this_operation"] != false || result["home_workspace_id"] != homeID {
		t.Fatalf("reuse commit = %d %v", code, body)
	}
	if otherAllowlist.Contains(homeID) {
		t.Fatal("reuse recorded an existing Home as created here")
	}
}

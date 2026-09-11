package sessionhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/grouprequirements"
	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/session"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

func policyTemplate(policy projecttemplates.GroupPolicy) projecttemplates.Template {
	requirement := &projecttemplates.GroupRequirement{SchemaVersion: 1, Policy: policy}
	template := projecttemplates.Template{
		ID: "plugin:neutral:project", Name: "Neutral Project", Revision: strings.Repeat("a", 64),
		PluginOwner: &agentworkspace.PluginTemplateOwner{
			PluginID: "neutral", PluginVersion: "1.0.0", BlueprintID: "project", BlueprintVersion: 1,
		},
		GroupRequirement: requirement,
	}
	if policy == projecttemplates.GroupPolicyNone {
		return template
	}
	requirement.AssistantProgramID = "neutral-program"
	requirement.MissingHome = projecttemplates.MissingHomeOfferCreate
	requirement.DefaultHomeName = "Neutral Program Home"
	template.AssistantProgram = &agentworkspace.AssistantProgramDeclaration{
		SchemaVersion: agentworkspace.AssistantProgramSchemaVersion, ID: "neutral-program", StationName: "Neutral Program Home",
	}
	return template
}

func newPolicyHandler(t *testing.T, template *projecttemplates.Template) (*Handler, agentworkspace.Store, func()) {
	t.Helper()
	skeleton := t.TempDir()
	if err := os.WriteFile(filepath.Join(skeleton, "project.demo"), []byte("neutral project\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	template.Path = skeleton
	template.HasSkeleton = true
	template.ProjectEntry = &projecttemplates.ProjectEntry{RelativePath: "project.demo"}
	ctx := context.Background()
	db, err := database.Open(ctx, &database.Config{InMemory: true})
	if err != nil {
		t.Fatal(err)
	}
	hybrid := session.NewHybridStoreWithDB(db, 20)
	fileStore, err := agentworkspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	primary := session.NewWorkspaceStoreAdapter(hybrid)
	syncStore := agentworkspace.NewSyncStore(primary, fileStore)
	handler := New(hybrid)
	handler.SetWorkspaceStore(fileStore)
	handler.SetWorkspaceTaskStore(syncStore)
	handler.SetInstalledPluginLister(assistantInstalledPluginLister{installed: []plugin.InstalledPlugin{{
		Name: "neutral", Version: "1.0.0", Enabled: true, Generation: 1,
		WorkspaceSurfaces: &plugin.SurfaceContribution{
			SchemaVersion: 1, Name: "neutral", Version: "1.0.0",
			Protocol: plugin.ProtocolRange{Min: plugin.SurfaceProtocolVersion, Max: plugin.SurfaceProtocolVersion},
		},
		ResolvedArtifacts: []plugin.ResolvedArtifact{{ServiceID: "neutral-service", Available: true}},
		ResolvedBlueprints: []plugin.ResolvedBlueprint{{
			ID: "project", QualifiedID: "plugin:neutral:project", Version: 1, Template: *template,
		}},
	}}})
	handler.SetProjectTemplateResolver(func(id, path string) (projecttemplates.Template, error) {
		if id == template.ID && path == "" {
			return *template, nil
		}
		return projecttemplates.Template{}, projecttemplates.ErrTemplateNotFound
	})
	handler.SetGroupRequirementService(grouprequirements.NewService(syncStore, grouprequirements.NewMemoryStore()), func(context.Context) (string, error) {
		return "local", nil
	})
	return handler, syncStore, func() { _ = hybrid.Close() }
}

func postPolicyWorkspace(t *testing.T, handler *Handler, payload map[string]any) (int, map[string]any) {
	t.Helper()
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces", bytes.NewReader(encoded))
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.HandleWorkspaces(response, req)
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response %d: %v: %s", response.Code, err, response.Body.String())
	}
	return response.Code, body
}

func postPolicyProject(t *testing.T, handler *Handler, workspaceID string, payload map[string]any) (int, map[string]any) {
	t.Helper()
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/"+workspaceID+"/project", bytes.NewReader(encoded))
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.HandleWorkspaces(response, req)
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode project response %d: %v: %s", response.Code, err, response.Body.String())
	}
	return response.Code, body
}

func postPolicyHome(t *testing.T, handler *Handler, commit bool, payload map[string]any) (int, map[string]any) {
	t.Helper()
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/workspaces/group-requirement/home/review"
	handle := handler.ReviewGroupRequirementHome
	if commit {
		path = "/api/workspaces/group-requirement/home/commit"
		handle = handler.CommitGroupRequirementHome
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(encoded))
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handle(response, req)
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode Home response %d: %v: %s", response.Code, err, response.Body.String())
	}
	return response.Code, body
}

func preparePolicyHome(t *testing.T, handler *Handler) string {
	t.Helper()
	selection := map[string]any{"template_id": "plugin:neutral:project"}
	code, body := postPolicyHome(t, handler, false, selection)
	if code != http.StatusOK {
		t.Fatalf("Home review status=%d body=%v", code, body)
	}
	review := body["group_requirement_review"].(map[string]any)
	if review["state"] != "ready_grouped" || review["home_will_be_created"] != true {
		t.Fatalf("Home review=%#v", review)
	}
	commit := map[string]any{
		"template_id": "plugin:neutral:project", "group_review_token": review["review_token"],
		"idempotency_key": "prepare-neutral-home",
	}
	code, body = postPolicyHome(t, handler, true, commit)
	if code != http.StatusOK {
		t.Fatalf("Home commit status=%d body=%v", code, body)
	}
	result := body["group_requirement"].(map[string]any)
	if result["state"] != "home_ready" || result["home_created"] != true {
		t.Fatalf("Home result=%#v", result)
	}
	homeID, _ := result["home_workspace_id"].(string)
	if homeID == "" {
		t.Fatalf("Home result has no ID: %#v", result)
	}
	return homeID
}

func postAssistantTopology(t *testing.T, handler func(http.ResponseWriter, *http.Request), workspaceID string, payload map[string]any) (int, map[string]any) {
	t.Helper()
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/assistant-program", bytes.NewReader(encoded))
	req.SetPathValue("workspaceID", workspaceID)
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler(response, req)
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode topology response %d: %v: %s", response.Code, err, response.Body.String())
	}
	return response.Code, body
}

func reviewPayload(name string, createHome bool) map[string]any {
	return map[string]any{
		"name": name, "template_id": "plugin:neutral:project", "group_composition": "grouped",
		"create_required_home": createHome, "group_requirement_review": true,
	}
}

func TestCreateWorkspaceRequiredReviewCommitAndReuse(t *testing.T) {
	template := policyTemplate(projecttemplates.GroupPolicyRequired)
	handler, store, cleanup := newPolicyHandler(t, &template)
	defer cleanup()

	code, body := postPolicyWorkspace(t, handler, reviewPayload("First Project", false))
	if code != http.StatusOK {
		t.Fatalf("initial review status = %d, body=%v", code, body)
	}
	initial := body["group_requirement_review"].(map[string]any)
	if initial["state"] != "home_creation_review_required" || initial["review_token"] != nil {
		t.Fatalf("initial review = %#v", initial)
	}
	ids, _ := store.List()
	if len(ids) != 0 {
		t.Fatalf("review created state: %v", ids)
	}

	// The former combined-create flag cannot bypass the separate Home action.
	bypassPayload := reviewPayload("First Project", true)
	code, body = postPolicyWorkspace(t, handler, bypassPayload)
	if code != http.StatusOK {
		t.Fatalf("combined review status = %d, body=%v", code, body)
	}
	blocked := body["group_requirement_review"].(map[string]any)
	if blocked["state"] != "home_creation_review_required" || blocked["review_token"] != nil {
		t.Fatalf("combined review bypassed Home preparation: %#v", blocked)
	}
	ids, _ = store.List()
	if len(ids) != 0 {
		t.Fatalf("combined review created state: %v", ids)
	}

	homeID := preparePolicyHome(t, handler)
	ids, _ = store.List()
	if len(ids) != 1 || ids[0] != homeID {
		t.Fatalf("Home-only action created project state: %v", ids)
	}
	preparedHome, err := store.Get(homeID)
	if err != nil {
		t.Fatal(err)
	}
	preparedState := preparedHome.GetAssistantProgramState()
	if preparedState == nil || len(preparedState.LinkedProjectIDs) != 0 || len(preparedHome.AgentInstances) != 0 {
		t.Fatalf("prepared Home is not inert: %#v", preparedHome)
	}

	confirmedPayload := reviewPayload("First Project", false)
	code, body = postPolicyWorkspace(t, handler, confirmedPayload)
	if code != http.StatusOK {
		t.Fatalf("project review status = %d, body=%v", code, body)
	}
	confirmed := body["group_requirement_review"].(map[string]any)
	token, _ := confirmed["review_token"].(string)
	if confirmed["state"] != "ready_grouped" || token == "" || confirmed["home_workspace_id"] != homeID {
		t.Fatalf("project review = %#v", confirmed)
	}

	delete(confirmedPayload, "group_requirement_review")
	confirmedPayload["group_review_token"] = token
	confirmedPayload["idempotency_key"] = "first-project"
	code, body = postPolicyWorkspace(t, handler, confirmedPayload)
	if code != http.StatusCreated {
		t.Fatalf("commit status = %d, body=%v", code, body)
	}
	folder := body["folder"].(map[string]any)
	projectID := folder["id"].(string)
	project, err := store.Get(projectID)
	if err != nil {
		t.Fatal(err)
	}
	link := project.GetAssistantProjectLink()
	if project.ParentID != homeID || link == nil || link.StationWorkspaceID != homeID {
		t.Fatalf("project placement = parent %q link %#v, home %q", project.ParentID, link, homeID)
	}
	canonical, err := store.(interface {
		GetFolderWorkspace(string) (*agentworkspace.Workspace, error)
	}).GetFolderWorkspace(projectID)
	if err != nil {
		t.Fatal(err)
	}
	provenance := canonical.GetTemplateProvenance()
	if provenance == nil || provenance.GroupRequirement == nil || !provenance.GroupRequirement.StructurallyValid() {
		t.Fatalf("canonical provenance = %#v", provenance)
	}
	home, err := store.Get(homeID)
	if err != nil {
		t.Fatal(err)
	}
	state := home.GetAssistantProgramState()
	if state == nil || len(state.LinkedProjectIDs) != 1 || state.LinkedProjectIDs[0] != projectID {
		t.Fatalf("reciprocal Home state = %#v", state)
	}

	code, body = postPolicyWorkspace(t, handler, confirmedPayload)
	if code != http.StatusCreated || body["idempotent_replay"] != true {
		t.Fatalf("replay status=%d body=%v", code, body)
	}
	ids, _ = store.List()
	if len(ids) != 2 {
		t.Fatalf("replay duplicated state: %v", ids)
	}

	secondReview := reviewPayload("Second Project", false)
	code, body = postPolicyWorkspace(t, handler, secondReview)
	if code != http.StatusOK {
		t.Fatalf("reuse review status = %d, body=%v", code, body)
	}
	review := body["group_requirement_review"].(map[string]any)
	if review["state"] != "ready_grouped" || review["home_workspace_id"] != homeID || review["home_will_be_created"] == true {
		t.Fatalf("reuse review = %#v", review)
	}
	delete(secondReview, "group_requirement_review")
	secondReview["group_review_token"] = review["review_token"]
	secondReview["idempotency_key"] = "second-project"
	code, body = postPolicyWorkspace(t, handler, secondReview)
	if code != http.StatusCreated || body["assistant_station_id"] != homeID {
		t.Fatalf("reuse commit status=%d body=%v", code, body)
	}
	ids, _ = store.List()
	if len(ids) != 3 {
		t.Fatalf("workspace count = %d (%v), want one Home and two projects", len(ids), ids)
	}

	home, _ = store.Get(homeID)
	code, body = postAssistantTopology(t, handler.ReviewAssistantDisconnect, homeID, map[string]any{
		"project_workspace_id": projectID, "state_revision": home.GetAssistantProgramState().StateRevision,
	})
	if code != http.StatusOK {
		t.Fatalf("disconnect review status=%d body=%v", code, body)
	}
	disconnectToken := body["token"]
	code, body = postAssistantTopology(t, handler.CommitAssistantDisconnect, homeID, map[string]any{
		"token": disconnectToken, "idempotency_key": "disconnect-first",
	})
	if code != http.StatusOK {
		t.Fatalf("disconnect commit status=%d body=%v", code, body)
	}
	disconnected, _ := store.(interface {
		GetFolderWorkspace(string) (*agentworkspace.Workspace, error)
	}).GetFolderWorkspace(projectID)
	if status := agentworkspace.EvaluateGroupRequirementLifecycle(disconnected, store.Get); status == nil || status.State != agentworkspace.GroupRequirementStatusUnfulfilled {
		t.Fatalf("disconnect status = %#v", status)
	}
	activateResponse := httptest.NewRecorder()
	handler.ActivateAssistantProgram(activateResponse, assistantProgramRequest(http.MethodPost, "/activate", projectID, ""))
	if activateResponse.Code != http.StatusConflict || !strings.Contains(activateResponse.Body.String(), "reviewed reconnect") {
		t.Fatalf("Required activation bypass status=%d body=%s", activateResponse.Code, activateResponse.Body.String())
	}
	deleteRequest := httptest.NewRequest(http.MethodDelete, "/api/workspaces/"+projectID+"?confirm=true", nil)
	deleteResponse := httptest.NewRecorder()
	handler.HandleWorkspaces(deleteResponse, deleteRequest)
	if deleteResponse.Code != http.StatusConflict {
		t.Fatalf("Required delete status=%d body=%s", deleteResponse.Code, deleteResponse.Body.String())
	}
	code, body = postAssistantTopology(t, handler.ReviewAssistantReconnect, projectID, map[string]any{})
	if code != http.StatusOK {
		t.Fatalf("reconnect review status=%d body=%v", code, body)
	}
	reconnectToken := body["token"]
	code, body = postAssistantTopology(t, handler.CommitAssistantReconnect, projectID, map[string]any{
		"token": reconnectToken, "idempotency_key": "reconnect-first",
	})
	if code != http.StatusOK {
		t.Fatalf("reconnect commit status=%d body=%v", code, body)
	}
	code, body = postAssistantTopology(t, handler.CommitAssistantReconnect, projectID, map[string]any{
		"token": reconnectToken, "idempotency_key": "reconnect-first",
	})
	if code != http.StatusOK || body["replayed"] != true {
		t.Fatalf("reconnect replay status=%d body=%v", code, body)
	}
	reconnected, _ := store.(interface {
		GetFolderWorkspace(string) (*agentworkspace.Workspace, error)
	}).GetFolderWorkspace(projectID)
	if status := agentworkspace.EvaluateGroupRequirementLifecycle(reconnected, store.Get); status == nil || status.State != agentworkspace.GroupRequirementStatusReadyGrouped {
		t.Fatalf("reconnect status = %#v", status)
	}
}

func TestPrepareHomeCommitReplayNeverCreatesProjectConsequences(t *testing.T) {
	template := policyTemplate(projecttemplates.GroupPolicyRequired)
	handler, store, cleanup := newPolicyHandler(t, &template)
	defer cleanup()

	selection := map[string]any{"template_id": template.ID}
	code, body := postPolicyHome(t, handler, false, selection)
	if code != http.StatusOK {
		t.Fatalf("Home review status=%d body=%v", code, body)
	}
	review := body["group_requirement_review"].(map[string]any)
	commit := map[string]any{
		"template_id": template.ID, "group_review_token": review["review_token"],
		"idempotency_key": "home-only-replay",
	}
	code, body = postPolicyHome(t, handler, true, commit)
	if code != http.StatusOK {
		t.Fatalf("Home commit status=%d body=%v", code, body)
	}
	first := body["group_requirement"].(map[string]any)
	if first["home_created"] != true || first["idempotent_replay"] != false {
		t.Fatalf("first Home result=%#v", first)
	}
	code, body = postPolicyHome(t, handler, true, commit)
	if code != http.StatusOK {
		t.Fatalf("Home replay status=%d body=%v", code, body)
	}
	replayed := body["group_requirement"].(map[string]any)
	if replayed["home_workspace_id"] != first["home_workspace_id"] || replayed["idempotent_replay"] != true {
		t.Fatalf("Home replay=%#v first=%#v", replayed, first)
	}
	ids, _ := store.List()
	if len(ids) != 1 {
		t.Fatalf("Home-only replay created project state: %v", ids)
	}
	home, err := store.Get(ids[0])
	if err != nil {
		t.Fatal(err)
	}
	state := home.GetAssistantProgramState()
	if state == nil || len(state.LinkedProjectIDs) != 0 || len(home.AgentInstances) != 0 || home.ParentID != "" {
		t.Fatalf("Home-only replay added project, roster, or parent consequences: %#v", home)
	}
}

func TestCreateWorkspaceNonePersistsStandaloneWithoutHome(t *testing.T) {
	template := policyTemplate(projecttemplates.GroupPolicyNone)
	handler, store, cleanup := newPolicyHandler(t, &template)
	defer cleanup()
	payload := map[string]any{
		"name": "Standalone Project", "template_id": template.ID, "parent_id": "",
		"group_composition": "standalone", "group_requirement_review": true,
	}
	code, body := postPolicyWorkspace(t, handler, payload)
	if code != http.StatusOK {
		t.Fatalf("review status=%d body=%v", code, body)
	}
	review := body["group_requirement_review"].(map[string]any)
	delete(payload, "group_requirement_review")
	payload["group_review_token"] = review["review_token"]
	payload["idempotency_key"] = "standalone-project"
	code, body = postPolicyWorkspace(t, handler, payload)
	if code != http.StatusCreated {
		t.Fatalf("commit status=%d body=%v", code, body)
	}
	ids, _ := store.List()
	if len(ids) != 1 {
		t.Fatalf("standalone created extra Home state: %v", ids)
	}
	project, err := store.Get(ids[0])
	if err != nil {
		t.Fatal(err)
	}
	if project.ParentID != "" || project.GetAssistantProjectLink() != nil || project.GetAssistantProgramState() != nil {
		t.Fatalf("standalone project gained Home state: %#v", project)
	}
}

func TestCreateProjectForExistingWorkspaceUsesReviewedGroupPlacement(t *testing.T) {
	template := policyTemplate(projecttemplates.GroupPolicyRequired)
	handler, store, cleanup := newPolicyHandler(t, &template)
	defer cleanup()
	code, body := postPolicyWorkspace(t, handler, map[string]any{"name": "Existing Project"})
	if code != http.StatusCreated {
		t.Fatalf("workspace create status=%d body=%v", code, body)
	}
	workspaceID := body["folder"].(map[string]any)["id"].(string)
	blockedPayload := map[string]any{
		"template_id": template.ID, "project_name": "Existing Project", "group_composition": "grouped",
		"create_required_home": true, "group_requirement_review": true,
	}
	code, body = postPolicyProject(t, handler, workspaceID, blockedPayload)
	if code != http.StatusOK {
		t.Fatalf("combined project review status=%d body=%v", code, body)
	}
	blocked := body["group_requirement_review"].(map[string]any)
	if blocked["state"] != "home_creation_review_required" || blocked["review_token"] != nil {
		t.Fatalf("combined project review bypassed Home preparation: %#v", blocked)
	}
	ids, _ := store.List()
	if len(ids) != 1 || ids[0] != workspaceID {
		t.Fatalf("blocked project review changed state: %v", ids)
	}

	homeID := preparePolicyHome(t, handler)
	payload := map[string]any{
		"template_id": template.ID, "project_name": "Existing Project", "group_composition": "grouped",
		"group_requirement_review": true,
	}
	code, body = postPolicyProject(t, handler, workspaceID, payload)
	if code != http.StatusOK {
		t.Fatalf("project review status=%d body=%v", code, body)
	}
	review := body["group_requirement_review"].(map[string]any)
	ids, _ = store.List()
	if len(ids) != 2 {
		t.Fatalf("project review changed workspace state: %v", ids)
	}
	delete(payload, "group_requirement_review")
	payload["group_review_token"] = review["review_token"]
	payload["idempotency_key"] = "existing-project"
	code, body = postPolicyProject(t, handler, workspaceID, payload)
	if code != http.StatusCreated {
		project, projectErr := store.Get(workspaceID)
		t.Fatalf("project commit status=%d body=%v project=%#v err=%v", code, body, project, projectErr)
	}
	project, err := store.Get(workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	link := project.GetAssistantProjectLink()
	if project.ParentID != homeID || link == nil || link.StationWorkspaceID != homeID {
		t.Fatalf("existing project placement = parent %q link %#v home=%q", project.ParentID, link, homeID)
	}
	canonical, err := store.(interface {
		GetFolderWorkspace(string) (*agentworkspace.Workspace, error)
	}).GetFolderWorkspace(workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	provenance := canonical.GetTemplateProvenance()
	if provenance == nil || provenance.GroupRequirement == nil || !provenance.GroupRequirement.StructurallyValid() {
		t.Fatalf("existing project provenance = %#v", provenance)
	}
	code, body = postPolicyProject(t, handler, workspaceID, payload)
	if code != http.StatusCreated || body["idempotent_replay"] != true {
		t.Fatalf("project replay status=%d body=%v", code, body)
	}
}

func TestCreateWorkspaceRejectsStaleGroupReviewBeforeMutation(t *testing.T) {
	template := policyTemplate(projecttemplates.GroupPolicyRequired)
	handler, store, cleanup := newPolicyHandler(t, &template)
	defer cleanup()
	homeID := preparePolicyHome(t, handler)
	payload := reviewPayload("Stale Project", false)
	code, body := postPolicyWorkspace(t, handler, payload)
	if code != http.StatusOK {
		t.Fatalf("review status=%d body=%v", code, body)
	}
	token := body["group_requirement_review"].(map[string]any)["review_token"]
	template.Revision = strings.Repeat("b", 64)
	delete(payload, "group_requirement_review")
	payload["group_review_token"] = token
	payload["idempotency_key"] = "stale-project"
	code, body = postPolicyWorkspace(t, handler, payload)
	if code != http.StatusConflict || body["group_requirement"] == nil {
		t.Fatalf("stale commit status=%d body=%v", code, body)
	}
	ids, _ := store.List()
	if len(ids) != 1 || ids[0] != homeID {
		t.Fatalf("stale project commit changed Home-only state: %v", ids)
	}
}

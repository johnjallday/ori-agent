package sessionhttp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/projectlibrary"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestAssistantLibraryLinkedProjectsHTTP_ExactOwnerReviewAndInertPendingRead(t *testing.T) {
	handler, store, station, original := assistantPortfolioHTTPFixture(t)
	handler.currentUserID = func(context.Context) (string, error) { return station.OwnerUserID, nil }
	state := station.GetAssistantProgramState()
	home := &projecttemplates.AssistantProgramHome{
		SchemaVersion: 1, Version: 1, ID: state.Key.ProgramID,
		StationName: "Portfolio Home", DefaultPrimaryName: "Guide", HireTitle: "Staff guide",
		Roles: []projecttemplates.AssistantProgramHomeRole{{ID: "guide", Label: "Guide", Required: true,
			Primary: true, SystemPrompt: "Coordinate reviewed Home work.", Skills: []string{"music-project-management"}}},
		Stages: []workspace.AssistantProgramStageSpec{{ID: "helper", Label: "Helper"}},
		Reflection: workspace.AssistantReflectionConfig{MinimumProjects: 3, CadenceHours: 168,
			MaxProjects: 8, MaxEventsPerProject: 8, MaxCandidates: 8, MaxEvidence: 8, Rubric: "Use approved evidence."},
		AllowedProjectAttachments: []projecttemplates.AssistantProgramAllowedProjectAttachment{{
			ProviderPluginID: "reaper-plugin", BlueprintID: "reaper-song", ProjectTeamID: "reaper-song-team",
			ProjectTeamSchemaVersion: 1, MinProjectTeamVersion: 1, MaxProjectTeamVersion: 1,
		}},
	}
	if err := projecttemplates.NormalizeAssistantProgramHome(home); err != nil {
		t.Fatal(err)
	}
	owner := workspace.AssistantProgramHomeOwner{
		PluginID: state.Key.PluginID, PluginVersion: "0.1.0", ProgramID: state.Key.ProgramID,
		HomeSchemaVersion: 1, HomeVersion: 1, DeclarationDigest: projecttemplates.AssistantProgramHomeDigest(*home),
		PluginGeneration: 4, ComponentFingerprint: strings.Repeat("a", 64),
	}
	if err := store.Update(station.ID, func(ws *workspace.Workspace) error {
		current := ws.GetAssistantProgramState()
		current.HomeProvider = &owner
		ws.SetAssistantProgramState(current)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	handler.SetInstalledPluginLister(assistantInstalledPluginLister{installed: []plugin.InstalledPlugin{{
		Name: owner.PluginID, Version: owner.PluginVersion, Enabled: true, Generation: owner.PluginGeneration,
		Skills: []string{"music-project-management"}, ComponentFingerprint: owner.ComponentFingerprint,
		InstalledAt: station.CreatedAt.Add(-time.Minute),
		WorkspaceSurfaces: &plugin.SurfaceContribution{
			SchemaVersion: 1, Name: owner.PluginID, Version: owner.PluginVersion,
			Protocol:              plugin.ProtocolRange{Min: 1, Max: 1},
			RequiresHostFeatures:  []string{plugin.HostFeatureIndependentProgramHomesV1},
			AssistantProgramHomes: []projecttemplates.AssistantProgramHome{*home},
		},
	}}})
	scope := projectlibrary.Scope{OwnerUserID: station.OwnerUserID, HomeID: station.ID,
		ProviderID: state.Key.PluginID, ProgramID: state.Key.ProgramID}
	library := projectlibrary.NewStore(store)
	initReview, err := library.ReviewInitialize(scope)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := library.CommitInitialize(scope, initReview.Token, "links-http-init"); err != nil {
		t.Fatal(err)
	}
	// A separate canonical direct-creator child already linked to this Home,
	// but not yet in its library. No root grant is introduced by association.
	child := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Direct song"})
	child.OwnerUserID = scope.OwnerUserID
	child.SharedData = map[string]any{}
	child.DirectoryReferences = []workspace.DirectoryReference{{ID: "project-ref", WorkspaceID: child.ID,
		Name: "Selected independently", Path: filepath.Join(t.TempDir(), "offline-song")}}
	if err := workspace.SetProjectEntryLocator(child.SharedData, workspace.ProjectEntryLocator{
		SchemaVersion: workspace.ProjectEntryLocatorSchemaVersion, Kind: workspace.ProjectEntryDirectoryReference,
		DirectoryReferenceID: "project-ref", RelativePath: "Song.rpp"}); err != nil {
		t.Fatal(err)
	}
	child.SetAssistantProjectLink(&workspace.AssistantProjectLink{ID: workspace.AssistantProjectLinkID(scope.HomeID, child.ID),
		SchemaVersion: 1, StationWorkspaceID: scope.HomeID, StateRevision: 1, Key: state.Key})
	if err := store.Save(child); err != nil {
		t.Fatal(err)
	}
	if err := store.Update(scope.HomeID, func(ws *workspace.Workspace) error {
		current := ws.GetAssistantProgramState()
		current.LinkedProjectIDs = append(current.LinkedProjectIDs, child.ID)
		ws.SetAssistantProgramState(current)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	send := func(method, homeID, childID, body string, handlerFunc http.HandlerFunc) (int, map[string]any) {
		t.Helper()
		req := assistantProgramRequest(method, "/library/linked-projects/pending", homeID, body)
		req.SetPathValue("projectID", childID)
		result := httptest.NewRecorder()
		handlerFunc(result, req)
		var response map[string]any
		if err := json.Unmarshal(result.Body.Bytes(), &response); err != nil {
			t.Fatalf("invalid response %d: %v: %s", result.Code, err, result.Body.String())
		}
		return result.Code, response
	}
	code, pending := send(http.MethodGet, scope.HomeID, "", "", handler.ListAssistantLibraryPendingLinks)
	if code != http.StatusOK || pending["total"] != float64(1) || len(pending["rows"].([]any)) != 1 {
		t.Fatalf("pending exact child unavailable: %d %+v", code, pending)
	}
	if code, _ := send(http.MethodGet, original.ID, "", "", handler.ListAssistantLibraryPendingLinks); code != http.StatusNotFound {
		t.Fatalf("linked project URL disclosed Home links: %d", code)
	}
	if code, _ := send(http.MethodPost, scope.HomeID, "foreign", fmt.Sprintf(`{"revision":%v}`, pending["revision"]),
		handler.ReviewAssistantLibraryLinkedProject); code != http.StatusConflict {
		t.Fatalf("foreign project got a Home review: %d", code)
	}
	code, reviewed := send(http.MethodPost, scope.HomeID, child.ID, fmt.Sprintf(`{"revision":%v}`, pending["revision"]),
		handler.ReviewAssistantLibraryLinkedProject)
	if code != http.StatusOK || reviewed["link_only"] != true || reviewed["workspace_id"] != child.ID {
		t.Fatalf("valid review failed: %d %+v", code, reviewed)
	}
	token := reviewed["token"].(string)
	body := fmt.Sprintf(`{"review_token":%q,"idempotency_key":"direct-http","confirm":false}`, token)
	if code, _ := send(http.MethodPost, scope.HomeID, child.ID, body,
		handler.CommitAssistantLibraryLinkedProject); code != http.StatusBadRequest {
		t.Fatalf("unconfirmed commit succeeded: %d", code)
	}
	body = fmt.Sprintf(`{"review_token":%q,"idempotency_key":"direct-http","confirm":true}`, token)
	code, accepted := send(http.MethodPost, scope.HomeID, child.ID, body, handler.CommitAssistantLibraryLinkedProject)
	if code != http.StatusOK || accepted["entry_id"] == "" || accepted["replay"] != false {
		t.Fatalf("accepted direct child missing: %d %+v", code, accepted)
	}
	code, replay := send(http.MethodPost, scope.HomeID, child.ID, body, handler.CommitAssistantLibraryLinkedProject)
	if code != http.StatusOK || replay["replay"] != true || replay["entry_id"] != accepted["entry_id"] {
		t.Fatalf("direct association replay failed: %d %+v", code, replay)
	}
	if code, pending = send(http.MethodGet, scope.HomeID, "", "", handler.ListAssistantLibraryPendingLinks); code != http.StatusOK || pending["total"] != float64(0) {
		t.Fatalf("associated child remained pending: %d %+v", code, pending)
	}
}

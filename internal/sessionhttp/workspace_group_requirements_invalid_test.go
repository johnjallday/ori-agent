package sessionhttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// A workspace whose Required contract record is structurally invalid (a Home
// provider recorded without a project provider, no link ever written) used to
// dead-end: the deletion review answered 409 "membership changed" although
// nothing was connected to it. It is now reviewed and deleted like any other
// detached Required project.
func TestInvalidRequiredContractIsReviewedAndDeletedWhenDetached(t *testing.T) {
	template := policyTemplate(projecttemplates.GroupPolicyRequired)
	handler, store, cleanup := newPolicyHandler(t, &template)
	defer cleanup()

	project := agentworkspace.NewWorkspace(agentworkspace.CreateWorkspaceParams{Name: "Ori Music Demo"})
	project.ID = "invalid-contract-delete"
	project.OwnerUserID = "local"
	key := agentworkspace.AssistantProgramKey{OwnerUserID: "local", PluginID: "neutral", ProgramID: "neutral-program"}.Normalize()
	digest := strings.Repeat("e", 64)
	snapshot := &agentworkspace.GroupRequirementSnapshot{
		SchemaVersion: agentworkspace.GroupRequirementSnapshotSchemaVersion, Policy: "required",
		SelectedComposition: agentworkspace.GroupRequirementCompositionGrouped,
		TemplateID:          template.ID, TemplateRevision: digest, DefinitionDigest: digest,
		HomeProvider: &agentworkspace.AssistantProgramHomeOwner{
			PluginID: "neutral", PluginVersion: "1.0.0", ProgramID: "neutral-program",
			HomeSchemaVersion: 1, HomeVersion: 1, DeclarationDigest: digest,
			PluginGeneration: 1, ComponentFingerprint: digest,
		},
		ProgramKey: &key, HomeWorkspaceID: "music-home",
		ProjectLinkID: agentworkspace.AssistantProjectLinkID("music-home", project.ID),
		ReviewDigest:  digest, OperationDigest: digest, AppliedAt: time.Now().UTC(),
	}
	if snapshot.StructurallyValid() {
		t.Fatal("fixture snapshot must be structurally invalid")
	}
	project.SetTemplateProvenance(&agentworkspace.TemplateProvenance{TemplateID: template.ID, GroupRequirement: snapshot})
	if err := store.Save(project); err != nil {
		t.Fatal(err)
	}

	deleteURL := "/api/workspaces/" + project.ID + "?confirm=true&delete_sessions=true"
	blocked := httptest.NewRecorder()
	handler.HandleWorkspaces(blocked, httptest.NewRequest(http.MethodDelete, deleteURL, nil))
	if blocked.Code != http.StatusConflict || !strings.Contains(blocked.Body.String(), groupRequirementDeletionReviewRequired) {
		t.Fatalf("unreviewed delete status=%d body=%s", blocked.Code, blocked.Body.String())
	}

	reviewRequest := httptest.NewRequest(http.MethodPost,
		"/api/workspaces/"+project.ID+"/group-requirement/delete/review",
		strings.NewReader(`{"delete_sessions":true}`))
	reviewRequest.Header.Set("Content-Type", "application/json")
	reviewResponse := httptest.NewRecorder()
	handler.HandleWorkspaces(reviewResponse, reviewRequest)
	if reviewResponse.Code != http.StatusOK {
		t.Fatalf("review status=%d body=%s", reviewResponse.Code, reviewResponse.Body.String())
	}
	var reviewBody map[string]any
	if err := json.Unmarshal(reviewResponse.Body.Bytes(), &reviewBody); err != nil {
		t.Fatal(err)
	}
	review := reviewBody["group_requirement_review"].(map[string]any)
	token, _ := review["review_token"].(string)
	summary, _ := review["summary"].(string)
	if token == "" || !strings.Contains(summary, "invalid") {
		t.Fatalf("review = %#v", review)
	}

	confirmedRequest := httptest.NewRequest(http.MethodDelete, deleteURL, nil)
	confirmedRequest.Header.Set(groupRequirementDeletionReviewHeader, token)
	confirmedResponse := httptest.NewRecorder()
	handler.HandleWorkspaces(confirmedResponse, confirmedRequest)
	if confirmedResponse.Code != http.StatusNoContent {
		t.Fatalf("confirmed delete status=%d body=%s", confirmedResponse.Code, confirmedResponse.Body.String())
	}
	if _, err := store.Get(project.ID); err == nil {
		t.Fatal("reviewed invalid-contract project survived primary deletion")
	}
	if _, err := handler.workspaceStore.Get(project.ID); err == nil {
		t.Fatal("reviewed invalid-contract project survived folder deletion")
	}
}

func TestInvalidRequiredContractReviewNamesTheHomeWhenStillConnected(t *testing.T) {
	template := policyTemplate(projecttemplates.GroupPolicyRequired)
	handler, store, cleanup := newPolicyHandler(t, &template)
	defer cleanup()

	key := agentworkspace.AssistantProgramKey{OwnerUserID: "local", PluginID: "neutral", ProgramID: "neutral-program"}.Normalize()
	home := agentworkspace.NewWorkspace(agentworkspace.CreateWorkspaceParams{Name: "Music Home"})
	home.ID = "music-home-connected"
	home.OwnerUserID = "local"
	home.Kind = "group"
	home.SetAssistantProgramState(&agentworkspace.AssistantProgramState{Key: key, LinkedProjectIDs: []string{"invalid-contract-connected"}})
	if err := store.Save(home); err != nil {
		t.Fatal(err)
	}
	project := agentworkspace.NewWorkspace(agentworkspace.CreateWorkspaceParams{Name: "Ori Music Demo"})
	project.ID = "invalid-contract-connected"
	project.OwnerUserID = "local"
	digest := strings.Repeat("f", 64)
	project.SetTemplateProvenance(&agentworkspace.TemplateProvenance{
		TemplateID: template.ID,
		GroupRequirement: &agentworkspace.GroupRequirementSnapshot{
			SchemaVersion: agentworkspace.GroupRequirementSnapshotSchemaVersion, Policy: "required",
			SelectedComposition: agentworkspace.GroupRequirementCompositionGrouped,
			TemplateID:          template.ID, TemplateRevision: digest, DefinitionDigest: digest,
			HomeProvider: &agentworkspace.AssistantProgramHomeOwner{
				PluginID: "neutral", PluginVersion: "1.0.0", ProgramID: "neutral-program",
				HomeSchemaVersion: 1, HomeVersion: 1, DeclarationDigest: digest,
				PluginGeneration: 1, ComponentFingerprint: digest,
			},
			ProgramKey: &key, HomeWorkspaceID: home.ID,
			ProjectLinkID: agentworkspace.AssistantProjectLinkID(home.ID, project.ID),
			ReviewDigest:  digest, OperationDigest: digest, AppliedAt: time.Now().UTC(),
		},
	})
	if err := store.Save(project); err != nil {
		t.Fatal(err)
	}

	reviewRequest := httptest.NewRequest(http.MethodPost,
		"/api/workspaces/"+project.ID+"/group-requirement/delete/review",
		strings.NewReader(`{"delete_sessions":false}`))
	reviewRequest.Header.Set("Content-Type", "application/json")
	reviewResponse := httptest.NewRecorder()
	handler.HandleWorkspaces(reviewResponse, reviewRequest)
	if reviewResponse.Code != http.StatusConflict || !strings.Contains(reviewResponse.Body.String(), "still connected to an Assistant Program Home") {
		t.Fatalf("connected review status=%d body=%s", reviewResponse.Code, reviewResponse.Body.String())
	}
	if _, err := store.Get(project.ID); err != nil {
		t.Fatalf("review changed the project: %v", err)
	}
}

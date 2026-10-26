package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

func TestInboxPlacementRecovery_LostHoldsKeepEditsButRequireTrustedRepick(t *testing.T) {
	f := newDraftServerFixture(t)
	build, root := seedEditableInbox(t, f)
	build["draft"].(map[string]any)["name"] = "Keep my edited name"
	build["draft"].(map[string]any)["mission"] = "Keep this exact goal."
	build = saveEditedInbox(t, f, build, true)
	review := reviewEditedInbox(t, f, build, true)
	id := build["id"].(string)
	original := build["folder"].(map[string]any)["observation"].(map[string]any)["id"]
	beforeDraft, _ := json.Marshal(build["draft"])
	workspaces, profiles := len(f.builder.workspaceFileStore.CachedWorkspaces()), len(f.builder.st.ListAgents())

	// Reconstruct the process-local observer with the same canonical stores and
	// host guards. A persisted observation is not a restored filesystem hold.
	observations := personalassistant.NewFolderObservationService(f.builder.personalAssistantFolderDigest)
	f.builder.server.Storage.PersonalAssistantFolderObservations = observations
	observations.ConfigureBuildReviews(f.builder.sessionHandler.WorkspaceBuildReviewAuthority())
	_ = f.builder.server.newHomeAssistantAskHandler()
	status, latest := f.call(t, http.MethodGet, "/api/workspaces/build-sessions/"+id, nil)
	if status != http.StatusOK {
		t.Fatalf("read retained draft: %d %v", status, latest)
	}
	afterDraft, _ := json.Marshal(latest["session"].(map[string]any)["draft"])
	if string(beforeDraft) != string(afterDraft) || latest["session"].(map[string]any)["placement_monitoring"] != true {
		t.Fatal("losing selection authority discarded material choices")
	}
	status, response := f.call(t, http.MethodPost, "/api/workspaces/build-sessions/"+id+"/janitor-prepare", map[string]any{
		"version": build["version"], "monitoring": true, "review_revision": review["review_revision"],
	})
	if status < 400 {
		t.Fatalf("persisted snapshot restored permission: %d %v", status, response)
	}
	status, response = f.call(t, http.MethodPost, "/api/workspaces/build-sessions/"+id+"/folder", map[string]any{
		"version": build["version"], "mode": "chip", "chip": "desktop",
	})
	if status != http.StatusOK {
		t.Fatalf("trusted re-pick after lost hold: %d %v", status, response)
	}
	build = response["session"].(map[string]any)
	afterDraft, _ = json.Marshal(build["draft"])
	if string(beforeDraft) != string(afterDraft) || build["placement_monitoring"] != true || build["folder_path"] != root ||
		build["folder"].(map[string]any)["observation"].(map[string]any)["id"] == original {
		t.Fatalf("re-pick lost choices or reused old authority: %v", build)
	}
	reviewEditedInbox(t, f, build, true)
	if len(f.builder.workspaceFileStore.CachedWorkspaces()) != workspaces || len(f.builder.st.ListAgents()) != profiles {
		t.Fatal("recovery created domain resources without confirmation")
	}
	if _, err := os.Stat(filepath.Join(root, "Filed")); !os.IsNotExist(err) {
		t.Fatal("recovery granted folder access")
	}
}

func TestInboxPlacementRecovery_DeletedWorkspaceCannotReceivePositionRepairOrBeRecreated(t *testing.T) {
	f := newDraftServerFixture(t)
	build, root := seedEditableInbox(t, f)
	review := reviewEditedInbox(t, f, build, false)
	request := map[string]any{"version": build["version"], "monitoring": false, "review_revision": review["review_revision"]}
	endpoint := "/api/workspaces/build-sessions/" + build["id"].(string)
	status, prepared := f.call(t, http.MethodPost, endpoint+"/janitor-prepare", request)
	if status != http.StatusOK {
		t.Fatalf("prepare: %d %v", status, prepared)
	}
	id := prepared["workspace_id"].(string)
	profiles := len(f.builder.st.ListAgents())
	status, layout := f.call(t, http.MethodGet, "/api/workspace-map/layout", nil)
	if status != http.StatusOK {
		t.Fatalf("layout: %d %v", status, layout)
	}
	before, _ := json.Marshal(layout)
	// Delete only the synthetic workspace, not its external linked source.
	if err := f.builder.workspaceStore.Delete(id); err != nil {
		t.Fatal(err)
	}
	status, response := f.call(t, http.MethodPatch, "/api/workspace-map/layout", map[string]any{
		"operations": []any{map[string]any{"op": "set_positions", "positions": map[string]any{id: map[string]any{"x": 760, "y": 456}}}},
	})
	if status < 400 {
		t.Fatalf("deleted id received layout: %d %v", status, response)
	}
	_, layout = f.call(t, http.MethodGet, "/api/workspace-map/layout", nil)
	after, _ := json.Marshal(layout)
	if string(before) != string(after) {
		t.Fatal("refused repair changed layout")
	}
	read := reviewEditedInbox(t, f, build, false)
	if read["outcome"].(map[string]any)["state"] != "reconciliation_required" {
		t.Fatalf("deleted workspace looked prepared: %v", read)
	}
	if status, response := f.call(t, http.MethodPost, endpoint+"/janitor-prepare", request); status < 400 {
		t.Fatalf("deleted workspace was recreated: %d %v", status, response)
	}
	if _, err := f.builder.workspaceStore.Get(id); err == nil {
		t.Fatal("repair recreated the workspace")
	}
	if len(f.builder.st.ListAgents()) != profiles {
		t.Fatal("repair changed profiles")
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatal("workspace deletion or repair touched the linked source")
	}
}

func TestInboxPlacementRecovery_AcceptedHistoryCanReopenAfterDetachWithoutContinuingWork(t *testing.T) {
	f := newDraftServerFixture(t)
	input, _ := stageInboxPlacement(t, f)
	status, response := f.call(t, http.MethodPost, "/api/workspaces/build-sessions/from-inbox", input)
	if status != http.StatusCreated {
		t.Fatalf("handoff: %d %v", status, response)
	}
	build := response["session"].(map[string]any)
	id := build["id"].(string)
	review := reviewEditedInbox(t, f, build, false)
	status, prepared := f.call(t, http.MethodPost, "/api/workspaces/build-sessions/"+id+"/janitor-prepare", map[string]any{
		"version": build["version"], "monitoring": false, "review_revision": review["review_revision"],
	})
	if status != http.StatusOK {
		t.Fatalf("prepare: %d %v", status, prepared)
	}
	workspaceID := prepared["workspace_id"].(string)
	offerID := input["offer_id"].(string)
	source, err := f.builder.personalAssistantFolderDigest.StoredOffer(context.Background(), "local", offerID)
	if err != nil || source.Status != personalassistant.FolderOfferResolved || source.PlacementHandoff == nil || source.Setup != nil || source.DecidedAt != nil ||
		source.Outcome == nil || source.Outcome.WorkspaceID != workspaceID || source.Outcome.FirstTaskID != "" || source.Outcome.FirstLook == nil || source.Outcome.FirstLook.TaskID != "" || source.Outcome.FirstLook.Started || source.Outcome.FirstLook.Batch == nil {
		t.Fatalf("source is not a verified, task-free receipt: %+v %v", source, err)
	}
	batchURL, err := url.Parse(source.Outcome.FirstLook.Batch.Route)
	if err != nil || batchURL.Query().Get("preparation") != prepared["preparation"].(map[string]any)["operation_id"] {
		t.Fatalf("source filing link lost the actual preparation identity: %v %v", batchURL, err)
	}
	assigned := false
	for _, row := range source.Outcome.Receipt {
		if row.Kind == "agent" && row.Name == "File Curator" && row.Assigned {
			assigned = true
		}
	}
	if !assigned {
		t.Fatal("source reported a planned role rather than actual membership")
	}
	originalSource, _ := json.Marshal(source)
	_, saved := f.call(t, http.MethodGet, "/api/workspaces/build-sessions/"+id, nil)
	claim, _ := json.Marshal(saved["session"].(map[string]any)["preparation"])
	workspaces, profiles := len(f.builder.workspaceFileStore.CachedWorkspaces()), len(f.builder.st.ListAgents())
	status, response = f.call(t, http.MethodPost, "/api/home-assistant/folder-context/detach", map[string]any{
		"conversation_id": input["conversation_id"], "revision": input["source_revision"],
	})
	if status != http.StatusOK {
		t.Fatalf("detach accepted source: %d %v", status, response)
	}
	input["name"] = "Must not replace accepted choices"
	status, response = f.call(t, http.MethodPost, "/api/workspaces/build-sessions/from-inbox", input)
	if status != http.StatusOK || response["resumed"] != true {
		t.Fatalf("reopen accepted source: %d %v", status, response)
	}
	reopened := response["session"].(map[string]any)
	afterClaim, _ := json.Marshal(reopened["preparation"])
	if reopened["id"] != id || string(afterClaim) != string(claim) {
		t.Fatal("recovery changed the accepted envelope")
	}
	read := reviewEditedInbox(t, f, reopened, false)
	if read["state"] != "accepted" || read["workspace_id"] != workspaceID || read["outcome"].(map[string]any)["state"] != "prepared" {
		t.Fatalf("accepted read lost ownership: %v", read)
	}
	source, err = f.builder.personalAssistantFolderDigest.StoredOffer(context.Background(), "local", offerID)
	afterSource, _ := json.Marshal(source)
	if err != nil || string(originalSource) != string(afterSource) {
		t.Fatal("reopening rewrote the consumed source receipt")
	}
	stored, err := f.builder.workspaceBuildStore.Read(context.Background(), "local")
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"owner", "build", "observation", "revision", "conversation", "workspace", "task", "claim"} {
		bad := *stored.Session(id)
		binding := *bad.PlacementSource
		bad.PlacementSource = &binding
		outcome := *source.Outcome
		look := *outcome.FirstLook
		outcome.FirstLook = &look
		owner := "local"
		const foreign = "00000000-0000-4000-8000-000000000001"
		switch field {
		case "owner":
			owner = "another-owner"
		case "build":
			bad.ID = foreign
		case "observation":
			binding.ObservationID = foreign
		case "revision":
			binding.Revision = foreign
		case "conversation":
			binding.Target.ConversationID = foreign
		case "workspace":
			outcome.WorkspaceID = foreign
		case "task":
			outcome.FirstLook.TaskID = foreign
		case "claim":
			bad.Preparation = nil
		}
		if err := f.builder.personalAssistantFolderDigest.RecordInboxPlacementOutcome(context.Background(), owner, bad, outcome); err == nil {
			t.Fatalf("completion accepted changed %s", field)
		}
	}
	input["source_revision"] = "foreign-revision"
	if status, response := f.call(t, http.MethodPost, "/api/workspaces/build-sessions/from-inbox", input); status < 400 {
		t.Fatalf("foreign source replaced accepted binding: %d %v", status, response)
	}
	batches, err := f.builder.fileJanitorService.ListBatches(workspaceID)
	if err != nil || len(batches) != 1 || len(f.builder.workspaceFileStore.CachedWorkspaces()) != workspaces || len(f.builder.st.ListAgents()) != profiles {
		t.Fatalf("reopening continued or repeated work: %v %v", batches, err)
	}
	_, err = f.builder.workspaceBuildStore.Mutate(context.Background(), "local", func(doc *personalassistant.WorkspaceBuildDocument) error {
		doc.Sessions = nil
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	input["source_revision"] = source.PlacementHandoff.Revision
	if status, response := f.call(t, http.MethodPost, "/api/workspaces/build-sessions/from-inbox", input); status < 400 {
		t.Fatalf("pruned accepted history was reconstructed: %d %v", status, response)
	}
	source, err = f.builder.personalAssistantFolderDigest.StoredOffer(context.Background(), "local", offerID)
	afterSource, _ = json.Marshal(source)
	if err != nil || string(originalSource) != string(afterSource) {
		t.Fatal("pruning the build or refused recovery unconsumed its source")
	}
}

package server

import (
	"github.com/johnjallday/ori-agent/internal/foldercontext"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestFolderLibraryReview_MissingProviderKeepsCanonicalPrerequisitesAndReload(t *testing.T) {
	f := newDraftServerFixture(t)
	f.builder.server.reviewedReleases = newHomeReleases(t, "0.4.0").updates()
	root := seedAlbumsCollectionVariant(t, true)
	hash := folderTreeHash(t, root)
	before := len(f.builder.workspaceFileStore.CachedWorkspaces())
	draft := uuid.NewString()
	status, attached := f.call(t, http.MethodPost, "/api/home-assistant/folder-context/select", map[string]any{"draft_id": draft, "revision": "", "mode": "chip", "chip": "music"})
	if status != http.StatusOK {
		t.Fatal(attached)
	}
	selection := attached["observation"].(map[string]any)["id"]
	hq, err := f.builder.workspaceStore.Get(f.hqID)
	if err != nil {
		t.Fatal(err)
	}
	conversation, revision, offer := reviewFolder(t, f, map[string]any{"draft_id": draft, "revision": "", "selection_id": selection, "candidate_id": "candidate-0", "context": placementContext(hq), "structure": map[string]any{"group_type": "library"}})
	_, history := f.call(t, http.MethodGet, "/api/home-assistant/conversations/"+conversation, nil)
	view := history["folder_reviews"].(map[string]any)[offer].(map[string]any)
	if view["operation"] != "create_project_workspace" || view["destination_status"] != "unavailable" || view["prerequisites_available"] != true || view["readiness"] == nil || view["plan"] != nil {
		t.Fatalf("library prerequisite review: %v", view)
	}
	status, preview := f.call(t, http.MethodPost, "/api/personal-assistant/folder-digest/offers/"+offer+"/prerequisites", map[string]any{"kind": "provider", "request_id": "library-provider-preview"})
	if status != http.StatusOK {
		t.Fatalf("provider preview: %d %v", status, preview)
	}
	// Package preview does not accept the folder setup or silently switch to a group.
	_, history = f.call(t, http.MethodGet, "/api/home-assistant/conversations/"+conversation, nil)
	view = history["folder_reviews"].(map[string]any)[offer].(map[string]any)
	if view["status"] != "pending" || view["structure"].(map[string]any)["group"].(map[string]any)["type"] != "library" || history["folder_context"].(map[string]any)["revision"] != revision {
		t.Fatalf("reload changed consent: %v", view)
	}
	if folderTreeHash(t, root) != hash || len(f.builder.workspaceFileStore.CachedWorkspaces()) != before {
		t.Fatal("review produced effects")
	}
}

func TestFolderLibraryReview_ExactAlbumsStayDraftOnlyForBuildAndExplicitSupport(t *testing.T) {
	f := newDraftServerFixture(t)
	root := seedAlbumsCollectionVariant(t, true)
	hash := folderTreeHash(t, root)
	ctx := t.Context()
	relation, err := f.builder.personalAssistantService.Get(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	observations := f.builder.server.Storage.PersonalAssistantFolderObservations
	target := foldercontext.Target{UserID: "local", WorkspaceID: f.hqID, AgentName: strings.ToLower(relation.GlobalAgentProfile), DraftID: "build:" + uuid.NewString()}
	builds, err := f.builder.workspaceBuildStore.Read(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	observation, err := observations.Observe(ctx, target, "chip", "music")
	if err != nil || observation.Files != 8 || observation.Entries != 15 {
		t.Fatalf("exact build observation: %+v %v", observation, err)
	}
	recognized, err := observations.Recognize(ctx, target, observation.ID)
	if err != nil || recognized.Verdict != personalassistant.BuildFolderCollection || recognized.ProjectCount != 6 {
		t.Fatalf("build collection: %+v %v", recognized, err)
	}
	plan, err := observations.BuildPlan(ctx, target, observation.ID, "candidate-0", "Draft name")
	if err != nil || plan == nil || plan.Ready || plan.Digest != "" {
		t.Fatalf("collection became an automatic build plan: %+v %v", plan, err)
	}
	after, err := f.builder.workspaceBuildStore.Read(ctx, "local")
	if err != nil || !reflect.DeepEqual(builds, after) {
		t.Fatal("collection discovery changed a build", err)
	}
	destination := placementWorkspace(t, f, "Existing project", "", "")
	before := len(f.builder.workspaceFileStore.CachedWorkspaces())
	draft := uuid.NewString()
	status, attached := f.call(t, http.MethodPost, "/api/home-assistant/folder-context/select", map[string]any{"draft_id": draft, "revision": "", "mode": "chip", "chip": "music"})
	if status != http.StatusOK {
		t.Fatal(attached)
	}
	selection := attached["observation"].(map[string]any)["id"]
	request := map[string]any{"draft_id": draft, "revision": "", "selection_id": selection, "candidate_id": "candidate-0", "context": placementContext(destination), "operation": "link_supporting_folder", "destination_id": destination.ID}
	// A collection root is not offered as a supporting link. Preserve this
	// boundary; selecting an individual child can still review its own link.
	if status, _ := f.call(t, http.MethodPost, "/api/home-assistant/folder-context/review", request); status != http.StatusConflict {
		t.Fatal("collection acquired a new supporting operation", status)
	}
	request["candidate_id"] = "candidate-1"
	conversation, _, offer := reviewFolder(t, f, request)
	_, history := f.call(t, http.MethodGet, "/api/home-assistant/conversations/"+conversation, nil)
	view := history["folder_reviews"].(map[string]any)[offer].(map[string]any)
	if view["operation"] != "link_supporting_folder" || view["plan"] != nil || view["prerequisites_available"] != nil || view["destination"].(map[string]any)["workspace_id"] != destination.ID {
		t.Fatalf("support inherited library execution: %v", view)
	}
	if len(f.builder.workspaceFileStore.CachedWorkspaces()) != before || folderTreeHash(t, root) != hash {
		t.Fatal("draft/support review performed setup")
	}
}

package server

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func placementWorkspace(t *testing.T, f *draftServerFixture, name, kind, parent string) *workspace.Workspace {
	t.Helper()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: name})
	ws.OwnerUserID, ws.Kind, ws.ParentID = "local", kind, parent
	if err := workspace.CreateNativeWorkspace(context.Background(), f.builder.workspaceStore, ws); err != nil {
		t.Fatal(err)
	}
	saved, err := f.builder.workspaceStore.Get(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	return saved
}
func placementContext(ws *workspace.Workspace) map[string]any {
	return map[string]any{"context_version": 1, "origin": "personal_assistant_panel", "surface": "workspace_detail", "page_path": "/workspaces/" + ws.FolderSlug, "workspace_slug": ws.FolderSlug, "workspace_id": ws.ID}
}
func TestFolderPlacement_ProjectChoiceThenSupportingConfirmationDoesNotReplaceProject(t *testing.T) {
	f := newDraftServerFixture(t)
	ws := placementWorkspace(t, f, "Existing project", "", "")
	original, err := f.builder.workspaceStore.Get(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	request := stageReviewFolder(t, f)
	request["context"] = placementContext(ws)
	before := len(f.builder.workspaceFileStore.CachedWorkspaces())
	status, choice := f.call(t, http.MethodPost, "/api/home-assistant/folder-context/review", request)
	if status != http.StatusOK || choice["folder_placement_choice"] == nil || choice["conversation"] != nil {
		t.Fatalf("choice: %d %v", status, choice)
	}
	_, list := f.call(t, http.MethodGet, "/api/home-assistant/conversations", nil)
	if len(list["conversations"].([]any)) != 0 {
		t.Fatal("choice allocated canonical review/history")
	}
	request["operation"], request["destination_id"] = "link_supporting_folder", ws.ID
	id, _, offer := reviewFolder(t, f, request)
	_, history := f.call(t, http.MethodGet, "/api/home-assistant/conversations/"+id, nil)
	view := history["folder_reviews"].(map[string]any)[offer].(map[string]any)
	if view["operation"] != "link_supporting_folder" || view["plan"] != nil || view["remember"] != false || view["destination"].(map[string]any)["workspace_id"] != ws.ID {
		t.Fatal("wrong support disclosure", view)
	}
	confirmation := map[string]any{"decision": "yes", "choice": "project", "create": true, "request_id": "support-once", "review_digest": view["review_digest"]}
	status, result := f.call(t, http.MethodPost, "/api/personal-assistant/folder-digest/offers/"+offer+"/decide", confirmation)
	if status != http.StatusOK {
		t.Fatalf("link: %d %v", status, result)
	}
	after, err := f.builder.workspaceStore.Get(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.DirectoryReferences) != len(original.DirectoryReferences)+1 || after.DirectoryReferences[len(after.DirectoryReferences)-1].Purpose != "" {
		t.Fatal("not a supporting reference", after.DirectoryReferences)
	}
	if len(f.builder.workspaceFileStore.CachedWorkspaces()) != before || !reflect.DeepEqual(after.SharedData, original.SharedData) || !reflect.DeepEqual(after.AgentInstances, original.AgentInstances) || !reflect.DeepEqual(after.Tasks, original.Tasks) || after.ParentID != original.ParentID {
		t.Fatal("supporting link changed primary, roster, tasks or placement")
	}
	status, _ = f.call(t, http.MethodPost, "/api/personal-assistant/folder-digest/offers/"+offer+"/decide", confirmation)
	replay, _ := f.builder.workspaceStore.Get(ws.ID)
	if status != http.StatusOK || len(replay.DirectoryReferences) != len(after.DirectoryReferences) {
		t.Fatal("replay duplicated grant")
	}
	bytes, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), "Documents", "Chosen", "notes.txt"))
	if err != nil || string(bytes) != "source must stay unchanged" {
		t.Fatal("source modified", err)
	}
}
func TestFolderPlacement_InvalidExplicitDestinationOnlyReturnsDecisionWithoutAllocation(t *testing.T) {
	f := newDraftServerFixture(t)
	project := placementWorkspace(t, f, "Project", "", "")
	request := stageReviewFolder(t, f)
	request["context"] = placementContext(project)
	request["operation"], request["destination_id"] = "create_project_workspace", "missing-or-foreign"
	count := len(f.builder.workspaceFileStore.CachedWorkspaces())
	status, result := f.call(t, http.MethodPost, "/api/home-assistant/folder-context/review", request)
	if status != http.StatusOK || result["folder_placement_choice"] == nil || result["conversation"] != nil {
		t.Fatal("invalid destination created review", status, result)
	}
	if len(f.builder.workspaceFileStore.CachedWorkspaces()) != count {
		t.Fatal("placement changed workspaces")
	}
}

func TestFolderPlacement_ProjectChoiceCreatesSeparateChildInReviewedGenericParent(t *testing.T) {
	f := newDraftServerFixture(t)
	group := placementWorkspace(t, f, "Group", "group", "")
	original := placementWorkspace(t, f, "Original project", "", group.ID)
	request := stageReviewFolder(t, f)
	request["context"] = placementContext(original)
	status, choice := f.call(t, http.MethodPost, "/api/home-assistant/folder-context/review", request)
	if status != http.StatusOK {
		t.Fatal(status, choice)
	}
	options := choice["folder_placement_choice"].(map[string]any)["options"].([]any)
	create := options[1].(map[string]any)
	if create["destination_id"] != group.ID {
		t.Fatal("separate choice lost canonical parent", create)
	}
	request["operation"], request["destination_id"] = create["operation"], create["destination_id"]
	id, _, offer := reviewFolder(t, f, request)
	_, saved := f.call(t, http.MethodGet, "/api/home-assistant/conversations/"+id, nil)
	view := saved["folder_reviews"].(map[string]any)[offer].(map[string]any)
	// A legacy refresh cannot silently reinterpret a contextual operation.
	request["conversation_id"], request["revision"] = id, saved["folder_context"].(map[string]any)["revision"]
	delete(request, "draft_id")
	delete(request, "context")
	delete(request, "operation")
	delete(request, "destination_id")
	if status, result := f.call(t, http.MethodPost, "/api/home-assistant/folder-context/review", request); status != http.StatusOK || result["folder_context"].(map[string]any)["offer_id"] != offer {
		t.Fatal("legacy refresh duplicated canonical review", status, result)
	}
	status, result := f.call(t, http.MethodPost, "/api/personal-assistant/folder-digest/offers/"+offer+"/decide", map[string]any{"decision": "yes", "choice": "project", "create": true, "request_id": "separate", "review_digest": view["review_digest"]})
	if status != http.StatusOK {
		t.Fatal(status, result)
	}
	childID := result["offer"].(map[string]any)["outcome"].(map[string]any)["workspace_id"].(string)
	child, _ := f.builder.workspaceStore.Get(childID)
	unchanged, _ := f.builder.workspaceStore.Get(original.ID)
	if child.ParentID != group.ID || childID == original.ID || len(unchanged.DirectoryReferences) != 0 || unchanged.ParentID != group.ID {
		t.Fatal("separate choice replaced original")
	}
}

func TestFolderPlacement_StaleSupportWitnessAndForgedOperationDoNotLink(t *testing.T) {
	f := newDraftServerFixture(t)
	ws := placementWorkspace(t, f, "Existing project", "", "")
	request := stageReviewFolder(t, f)
	request["context"] = placementContext(ws)
	request["operation"], request["destination_id"] = "link_supporting_folder", "foreign-or-missing"
	if status, _ := f.call(t, http.MethodPost, "/api/home-assistant/folder-context/review", request); status != http.StatusConflict {
		t.Fatal("forged support target accepted", status)
	}
	request["destination_id"] = ws.ID
	id, _, offer := reviewFolder(t, f, request)
	_, history := f.call(t, http.MethodGet, "/api/home-assistant/conversations/"+id, nil)
	view := history["folder_reviews"].(map[string]any)[offer].(map[string]any)
	if status, _ := f.call(t, http.MethodPut, "/api/workspaces/"+ws.ID, map[string]any{"name": "Renamed project"}); status != http.StatusOK {
		t.Fatal(status)
	}
	status, result := f.call(t, http.MethodPost, "/api/personal-assistant/folder-digest/offers/"+offer+"/decide", map[string]any{"decision": "yes", "choice": "project", "create": true, "request_id": "stale-support", "review_digest": view["review_digest"]})
	if status != http.StatusConflict {
		t.Fatal("stale support accepted", status, result)
	}
	after, _ := f.builder.workspaceStore.Get(ws.ID)
	for _, ref := range after.DirectoryReferences {
		if filepath.Clean(ref.Path) == filepath.Join(os.Getenv("HOME"), "Documents", "Chosen") {
			t.Fatal("stale confirmation granted source access")
		}
	}
}

func TestFolderPlacement_PendingElsewhereFocusesCanonicalConversationWithoutNewOffer(t *testing.T) {
	f := newDraftServerFixture(t)
	ws := placementWorkspace(t, f, "Project", "", "")
	first := stageReviewFolder(t, f)
	id, _, offer := reviewFolder(t, f, first)
	second := stageReviewFolder(t, f)
	second["context"] = placementContext(ws)
	status, result := f.call(t, http.MethodPost, "/api/home-assistant/folder-context/review", second)
	if status != http.StatusOK || result["conversation"] != nil || result["folder_pending_elsewhere"].(map[string]any)["conversation_id"] != id {
		t.Fatal("pending review appropriated", status, result)
	}
	_, saved := f.call(t, http.MethodGet, "/api/home-assistant/conversations/"+id, nil)
	if saved["folder_context"].(map[string]any)["offer_id"] != offer {
		t.Fatal("existing reference changed")
	}
}

// namedPlacementTurn sends one folder turn from page and returns the saved
// conversation ID plus the server-authored suggestion, if any.
func namedPlacementTurn(t *testing.T, f *draftServerFixture, staged map[string]any, page *workspace.Workspace, prompt string) (string, map[string]any) {
	t.Helper()
	status, reply := f.call(t, http.MethodPost, "/api/home-assistant/ask", map[string]any{
		"prompt": prompt, "intent": "assistant_conversation", "context": placementContext(page), "conversation": map[string]string{},
		"folder_context": map[string]any{"selection_id": staged["selection_id"], "revision": "", "draft_id": staged["draft_id"]},
	})
	if status != http.StatusOK || reply["conversation"].(map[string]any)["stored"] != true {
		t.Fatalf("turn: %d %v", status, reply)
	}
	suggestion, _ := reply["folder_setup_suggestion"].(map[string]any)
	return reply["conversation"].(map[string]any)["id"].(string), suggestion
}

func namedPlacementFixture(t *testing.T) (*draftServerFixture, *workspace.Workspace, *workspace.Workspace, map[string]any) {
	t.Helper()
	f := newDraftServerFixture(t)
	f.builder.llmFactory.Register("claude_code", &capturingChatProvider{})
	if status, body := f.call(t, http.MethodPost, "/api/settings/system-model", map[string]string{"provider": "claude_code", "model": "sonnet"}); status != http.StatusOK {
		t.Fatal(status, body)
	}
	named := placementWorkspace(t, f, "Release Portfolio", "group", "")
	page := placementWorkspace(t, f, "Unrelated project", "", "")
	return f, named, page, stageReviewFolder(t, f)
}

// The user names a group while another project is on screen. Review must use
// the named group, not offer to link the folder into the page's project.
func TestFolderPlacement_ExplicitlyNamedWorkspaceCarriesIntoReview(t *testing.T) {
	f, named, page, staged := namedPlacementFixture(t)
	before := len(f.builder.workspaceFileStore.CachedWorkspaces())
	id, suggestion := namedPlacementTurn(t, f, staged, page, "Add this folder to Release Portfolio")
	subject, _ := suggestion["subject"].(map[string]any)
	if suggestion["offer_id"] != nil || subject["workspace_id"] != named.ID || subject["name"] != named.Name || subject["kind"] != "group" {
		t.Fatalf("suggestion lost the named workspace: %v", suggestion)
	}
	_, saved := f.call(t, http.MethodGet, "/api/home-assistant/conversations/"+id, nil)
	hydrated, _ := saved["folder_setup_suggestion"].(map[string]any)
	if hydrated == nil || hydrated["subject"].(map[string]any)["workspace_id"] != named.ID {
		t.Fatalf("reload lost the named workspace: %v", saved["folder_setup_suggestion"])
	}
	request := map[string]any{"conversation_id": id, "revision": suggestion["revision"], "selection_id": staged["selection_id"], "candidate_id": staged["candidate_id"]}
	// Control: the page alone still asks the project-local question.
	request["context"] = placementContext(page)
	if status, choice := f.call(t, http.MethodPost, "/api/home-assistant/folder-context/review", request); status != http.StatusOK || choice["folder_placement_choice"] == nil {
		t.Fatalf("page-following review changed: %d %v", status, choice)
	}
	context := placementContext(page)
	context["subject_workspace_id"] = subject["workspace_id"]
	request["context"] = context
	_, _, offer := reviewFolder(t, f, request)
	_, history := f.call(t, http.MethodGet, "/api/home-assistant/conversations/"+id, nil)
	view := history["folder_reviews"].(map[string]any)[offer].(map[string]any)
	destination := view["destination"].(map[string]any)
	if view["operation"] == "link_supporting_folder" || destination["workspace_id"] != named.ID || destination["name"] != named.Name {
		t.Fatalf("review did not target the named workspace: %v", view)
	}
	unchanged, _ := f.builder.workspaceStore.Get(page.ID)
	if len(f.builder.workspaceFileStore.CachedWorkspaces()) != before || len(unchanged.DirectoryReferences) != 0 {
		t.Fatal("review created a workspace or linked the page's project")
	}
}

func TestFolderPlacement_PageFollowingSuggestionNamesNoWorkspace(t *testing.T) {
	f, _, page, staged := namedPlacementFixture(t)
	_, suggestion := namedPlacementTurn(t, f, staged, page, "What is in this folder?")
	if suggestion == nil || suggestion["subject"] != nil {
		t.Fatalf("a page default was presented as a named workspace: %v", suggestion)
	}
}

// Saved attribution names a workspace; it cannot keep one readable.
func TestFolderPlacement_NamedWorkspaceNoLongerReadableWithdrawsSuggestionAndReview(t *testing.T) {
	f, named, page, staged := namedPlacementFixture(t)
	id, suggestion := namedPlacementTurn(t, f, staged, page, "Add this folder to Release Portfolio")
	if suggestion == nil || suggestion["subject"] == nil {
		t.Fatalf("fixture suggestion: %v", suggestion)
	}
	if err := f.builder.workspaceStore.Update(named.ID, func(ws *workspace.Workspace) error {
		ws.Status = workspace.StatusTrashed
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	_, saved := f.call(t, http.MethodGet, "/api/home-assistant/conversations/"+id, nil)
	if saved["folder_setup_suggestion"] != nil {
		t.Fatalf("history revived an unreadable workspace: %v", saved["folder_setup_suggestion"])
	}
	context := placementContext(page)
	context["subject_workspace_id"] = named.ID
	status, result := f.call(t, http.MethodPost, "/api/home-assistant/folder-context/review", map[string]any{
		"conversation_id": id, "revision": suggestion["revision"], "selection_id": staged["selection_id"], "candidate_id": staged["candidate_id"], "context": context,
	})
	if status == http.StatusOK || result["folder_context"] != nil || result["folder_placement_choice"] != nil {
		t.Fatalf("review fell back to the page for an unreadable named workspace: %d %v", status, result)
	}
	_, history := f.call(t, http.MethodGet, "/api/home-assistant/conversations/"+id, nil)
	if history["folder_context"].(map[string]any)["offer_id"] != nil && history["folder_context"].(map[string]any)["offer_id"] != "" {
		t.Fatal("refused review still allocated an offer")
	}
}

// completedPlacementProject confirms the staged folder as a child of a group and
// returns that group, the owning conversation and the created project.
func completedPlacementProject(t *testing.T) (*draftServerFixture, *workspace.Workspace, string, *workspace.Workspace) {
	t.Helper()
	f := newDraftServerFixture(t)
	group := placementWorkspace(t, f, "Group", "group", "")
	request := stageReviewFolder(t, f)
	request["context"] = placementContext(group)
	id, _, offer := reviewFolder(t, f, request)
	_, history := f.call(t, http.MethodGet, "/api/home-assistant/conversations/"+id, nil)
	view := history["folder_reviews"].(map[string]any)[offer].(map[string]any)
	status, result := f.call(t, http.MethodPost, "/api/personal-assistant/folder-digest/offers/"+offer+"/decide", map[string]any{"decision": "yes", "choice": "project", "create": true, "request_id": "first-project", "review_digest": view["review_digest"]})
	if status != http.StatusOK {
		t.Fatal(status, result)
	}
	child, err := f.builder.workspaceStore.Get(result["offer"].(map[string]any)["outcome"].(map[string]any)["workspace_id"].(string))
	if err != nil {
		t.Fatal(err)
	}
	return f, group, id, child
}

// The same folder shown again, in another conversation or for another parent,
// points at the project it already is. Nothing is prepared or created.
func TestFolderPlacement_CompletedProjectIsPointedAtNotDuplicated(t *testing.T) {
	f, group, owner, child := completedPlacementProject(t)
	elsewhere := placementWorkspace(t, f, "Another group", "group", "")
	before := len(f.builder.workspaceFileStore.CachedWorkspaces())
	for _, page := range []*workspace.Workspace{group, elsewhere} {
		request := stageReviewFolder(t, f)
		request["context"] = placementContext(page)
		status, result := f.call(t, http.MethodPost, "/api/home-assistant/folder-context/review", request)
		existing, _ := result["folder_existing_project"].(map[string]any)
		if status != http.StatusOK || result["conversation"] != nil || existing == nil {
			t.Fatalf("completed project not reused from %s: %d %v", page.Name, status, result)
		}
		if existing["workspace"] != child.Name || existing["route"] != "/workspaces/"+child.FolderSlug || existing["conversation_id"] != owner || existing["subject"] != "Chosen" {
			t.Fatalf("wrong existing project: %v", existing)
		}
	}
	_, list := f.call(t, http.MethodGet, "/api/home-assistant/conversations", nil)
	if len(list["conversations"].([]any)) != 1 || len(f.builder.workspaceFileStore.CachedWorkspaces()) != before {
		t.Fatal("pointing at the project allocated a review or a workspace")
	}
	// Linking the same folder as a supporting source elsewhere is another
	// operation on another workspace, not a second copy of that project.
	project := placementWorkspace(t, f, "Other project", "", "")
	request := stageReviewFolder(t, f)
	request["context"] = placementContext(project)
	request["operation"], request["destination_id"] = "link_supporting_folder", project.ID
	id, _, offer := reviewFolder(t, f, request)
	_, history := f.call(t, http.MethodGet, "/api/home-assistant/conversations/"+id, nil)
	if history["folder_reviews"].(map[string]any)[offer].(map[string]any)["operation"] != "link_supporting_folder" {
		t.Fatal("supporting review was treated as the completed project")
	}
}

// Identity, not the name or the path, decides reuse: a different folder that
// took the same place is reviewed as new.
func TestFolderPlacement_ReplacementFolderAtTheSamePathIsNotTheCompletedProject(t *testing.T) {
	f, group, _, _ := completedPlacementProject(t)
	chosen := filepath.Join(os.Getenv("HOME"), "Documents", "Chosen")
	// Keep the original alive beside it, so the two cannot share an identity.
	if err := os.Rename(chosen, filepath.Join(os.Getenv("HOME"), "Documents", "Chosen (moved)")); err != nil {
		t.Fatal(err)
	}
	request := stageReviewFolder(t, f)
	request["context"] = placementContext(group)
	status, result := f.call(t, http.MethodPost, "/api/home-assistant/folder-context/review", request)
	if status != http.StatusOK || result["folder_existing_project"] != nil || result["folder_context"] == nil {
		t.Fatalf("a replacement folder adopted the completed project: %d %v", status, result)
	}
}

// A stored outcome is not proof: the project must still exist and hold the folder.
func TestFolderPlacement_TrashedProjectIsNotOfferedAsExisting(t *testing.T) {
	f, group, _, child := completedPlacementProject(t)
	if err := f.builder.workspaceStore.Update(child.ID, func(ws *workspace.Workspace) error {
		ws.Status = workspace.StatusTrashed
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	request := stageReviewFolder(t, f)
	request["context"] = placementContext(group)
	status, result := f.call(t, http.MethodPost, "/api/home-assistant/folder-context/review", request)
	if status != http.StatusOK || result["folder_existing_project"] != nil || result["folder_context"] == nil {
		t.Fatalf("a trashed project was offered as existing: %d %v", status, result)
	}
}

func TestFolderPlacement_GenericGroupCreatesSeparateChildOnlyAfterConfirmation(t *testing.T) {
	f := newDraftServerFixture(t)
	home := placementWorkspace(t, f, "Project group", "group", "")
	sibling := placementWorkspace(t, f, "Sibling", "", home.ID)
	request := stageReviewFolder(t, f)
	request["context"] = placementContext(home)
	before := len(f.builder.workspaceFileStore.CachedWorkspaces())
	id, _, offer := reviewFolder(t, f, request)
	_, history := f.call(t, http.MethodGet, "/api/home-assistant/conversations/"+id, nil)
	view := history["folder_reviews"].(map[string]any)[offer].(map[string]any)
	if view["destination"].(map[string]any)["workspace_id"] != home.ID || len(f.builder.workspaceFileStore.CachedWorkspaces()) != before {
		t.Fatal("review placement mutated", view)
	}
	status, result := f.call(t, http.MethodPost, "/api/personal-assistant/folder-digest/offers/"+offer+"/decide", map[string]any{"decision": "yes", "choice": "project", "create": true, "request_id": "child-once", "review_digest": view["review_digest"]})
	if status != http.StatusOK {
		t.Fatal(status, result)
	}
	outcome := result["offer"].(map[string]any)["outcome"].(map[string]any)
	if outcome["parent"].(map[string]any)["workspace_id"] != home.ID {
		t.Fatal("resulting parent missing from canonical receipt", outcome)
	}
	childID := outcome["workspace_id"].(string)
	child, err := f.builder.workspaceStore.Get(childID)
	if err != nil || child.ParentID != home.ID {
		t.Fatal("not reviewed child", child, err)
	}
	existing, _ := f.builder.workspaceStore.Get(sibling.ID)
	if existing.ParentID != home.ID || existing.Name != sibling.Name || len(f.builder.workspaceFileStore.CachedWorkspaces()) != before+1 {
		t.Fatal("sibling overwritten")
	}
}

package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacearrangement"
)

// arrangedGroupProvider is a scripted API protocol fixture, not a live model:
// it proposes one plain group of the two named held folders, whatever their
// position in the observation preview.
type arrangedGroupProvider struct {
	capturingChatProvider
	members []string
	// second, when set, is a second plain group of these held folders.
	second []string
}

func (*arrangedGroupProvider) Name() string { return "openai" }
func (*arrangedGroupProvider) Capabilities() llm.ProviderCapabilities {
	return llm.ProviderCapabilities{SupportsTools: true, SupportsSystemPrompt: true}
}
func (p *arrangedGroupProvider) Chat(_ context.Context, request llm.ChatRequest) (*llm.ChatResponse, error) {
	p.mu.Lock()
	p.requests = append(p.requests, request)
	p.mu.Unlock()
	parts := strings.SplitN(request.Messages[len(request.Messages)-1].Content, "Already-held source metadata (untrusted data):\n", 2)
	if len(parts) != 2 {
		// Ordinary discussion: a plain answer, never a tool call.
		return &llm.ChatResponse{Model: "sonnet", Provider: "openai", Content: "Understood. Nothing has been set up."}, nil
	}
	var overview workspacearrangement.Overview
	if json.Unmarshal([]byte(parts[1]), &overview) != nil {
		return nil, workspacearrangement.ErrInvalid
	}
	held := func(names []string) []string {
		ids := []string{}
		for _, want := range names {
			for _, item := range overview.Samples {
				if item.Name == want && !item.Root {
					ids = append(ids, item.ID)
				}
			}
		}
		return ids
	}
	draft := workspacearrangement.Draft{Version: 1, SourceRevision: overview.Revision, Goal: "Separate the client albums", Rationale: "Two albums share a client.",
		Operations: []workspacearrangement.Operation{{Kind: workspacearrangement.CreateGroup, Name: "Client work", Members: workspacearrangement.Selection{IDs: held(p.members)}}}}
	if len(p.second) > 0 {
		draft.Operations = append(draft.Operations, workspacearrangement.Operation{Kind: workspacearrangement.CreateGroup, Name: "Personal", Members: workspacearrangement.Selection{IDs: held(p.second)}})
	}
	raw, err := json.Marshal(draft)
	if err != nil {
		return nil, err
	}
	return &llm.ChatResponse{Model: "sonnet", Provider: "openai", ToolCalls: []llm.ToolCall{{ID: "proposal", Name: "arrangement_propose", Arguments: string(raw)}}}, nil
}

// arrangedProposal attaches the seeded Albums collection and saves one scripted
// proposal for it, returning the conversation, folder revision and record path.
func arrangedProposal(t *testing.T, f *draftServerFixture, provider *arrangedGroupProvider) (conversation, revision, historyPath string) {
	t.Helper()
	f.builder.llmFactory.Register("claude_code", provider)
	if status, body := f.call(t, http.MethodPost, "/api/settings/system-model", map[string]string{"provider": "claude_code", "model": "sonnet"}); status != http.StatusOK {
		t.Fatal(status, body)
	}
	draftID := uuid.NewString()
	status, selected := f.call(t, http.MethodPost, "/api/home-assistant/folder-context/select", map[string]any{"draft_id": draftID, "revision": "", "mode": "chip", "chip": "music"})
	if status != http.StatusOK {
		t.Fatalf("select: %d %v", status, selected)
	}
	observation := selected["observation"].(map[string]any)
	status, answer := f.call(t, http.MethodPost, "/api/home-assistant/ask", map[string]any{
		"prompt": "Separate the client albums", "conversation": map[string]any{},
		"folder_context": map[string]any{"draft_id": draftID, "revision": "", "selection_id": observation["id"]},
		"arrangement":    map[string]any{"share_held_metadata": true, "expected_provider": "claude_code", "expected_model": "sonnet"},
	})
	if status != http.StatusOK || answer["arrangement"] == nil {
		t.Fatalf("planning: %d %v", status, answer)
	}
	conversation = answer["conversation"].(map[string]any)["id"].(string)
	revision = answer["folder_context"].(map[string]any)["revision"].(string)
	historyPath = "/api/home-assistant/conversations/" + conversation + "/arrangements/" + answer["arrangement"].(map[string]any)["id"].(string)
	return conversation, revision, historyPath
}

func arrangedExecution(t *testing.T, f *draftServerFixture, historyPath string) map[string]any {
	t.Helper()
	status, history := f.call(t, http.MethodGet, historyPath, nil)
	execution, ok := history["execution"].(map[string]any)
	if status != http.StatusOK || !ok {
		t.Fatalf("history: %d %v", status, history)
	}
	return execution
}

func TestPersonalAssistantArrangement_ReviewsAndAppliesOneArrangedGroup(t *testing.T) {
	f := newDraftServerFixture(t)
	root := seedAlbumsCollection(t)
	hash := folderTreeHash(t, root)
	files := f.builder.workspaceFileStore
	before := len(files.CachedWorkspaces())
	provider := &arrangedGroupProvider{members: []string{"Album-2", "Album-4"}}
	conversation, revision, historyPath := arrangedProposal(t, f, provider)
	calls := len(provider.requests)

	execution := arrangedExecution(t, f, historyPath)
	groups := execution["groups"].([]any)
	group := groups[0].(map[string]any)
	if execution["reviewable"] != true || execution["declinable"] != true || execution["state"] != "draft" || len(groups) != 1 || group["state"] != "not_reviewed" || group["name"] != "Client work" || group["members"] != float64(2) {
		t.Fatalf("execution before review: %v", execution)
	}
	reviewPath := historyPath + "/group-review"
	// Malformed, stale and unknown requests open nothing.
	for name, body := range map[string]map[string]any{
		"no operation":       {"revision": revision, "expected_record_revision": 1},
		"old record":         {"revision": revision, "expected_record_revision": 2, "operation": 0},
		"missing intention":  {"revision": revision, "expected_record_revision": 1, "operation": 1},
		"old folder":         {"revision": "an-old-row", "expected_record_revision": 1, "operation": 0},
		"an injected member": {"revision": revision, "expected_record_revision": 1, "operation": 0, "members": []string{"candidate-1"}},
	} {
		if status, response := f.call(t, http.MethodPost, reviewPath, body); status == http.StatusOK {
			t.Fatalf("%s opened a review: %v", name, response)
		}
	}
	if len(files.CachedWorkspaces()) != before {
		t.Fatal("a refused review created a workspace")
	}
	status, opened := f.call(t, http.MethodPost, reviewPath, map[string]any{"revision": revision, "expected_record_revision": 1, "operation": 0})
	if status != http.StatusOK {
		t.Fatalf("review: %d %v", status, opened)
	}
	folderState := opened["folder_context"].(map[string]any)
	offer := folderState["offer_id"].(string)
	if offer == "" || folderState["revision"] == revision || len(files.CachedWorkspaces()) != before || len(provider.requests) != calls {
		t.Fatalf("review created, called or bound wrongly: %v", opened)
	}
	review := albumsReview{conversation: conversation, offer: offer}
	view := review.read(t, f)
	structure := view["structure"].(map[string]any)
	members := structure["members"].([]any)
	if structure["version"] != float64(2) || structure["arrangement"] == nil || len(members) != 2 ||
		members[0].(map[string]any)["folder"] != "Album-2" || members[1].(map[string]any)["folder"] != "Album-4" {
		t.Fatalf("arranged structure: %v", structure)
	}
	encoded, _ := json.Marshal(view)
	for _, forbidden := range []string{root, "rel_path", "identity", "Album-1", "Album-3", "Logic Sketch"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("the review carries %q: %s", forbidden, encoded)
		}
	}
	want := []string{
		"group: Creates group Client work",
		"workspaces: Creates 2 workspaces, each linked to its own folder",
		"member: Album-2", "member: Album-4",
		"arrangement: Only the folders your saved arrangement selected",
		"folder: Nothing is moved, copied or read",
		"task: No task is created or started",
	}
	if got := planLineNames(view); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("plan:\n%s", strings.Join(got, "\n"))
	}
	if arrangedExecution(t, f, historyPath)["groups"].([]any)[0].(map[string]any)["state"] != "awaiting_user_review" {
		t.Fatal("history did not show the open review")
	}

	resolved := setUpAndWait(t, f, offer, view, "arranged-set-up")
	outcome := resolved["outcome"].(map[string]any)
	created, err := files.Get(outcome["workspace_id"].(string))
	if err != nil || created.Kind != "group" || created.Name != "Client work" {
		t.Fatalf("group: %+v, %v", created, err)
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	children := map[string]*workspace.Workspace{}
	for _, ws := range files.CachedWorkspaces() {
		if ws != nil && ws.ParentID == created.ID {
			children[ws.Name] = ws
		}
	}
	if len(children) != 2 || children["Album-2"] == nil || children["Album-4"] == nil || len(files.CachedWorkspaces()) != before+3 {
		t.Fatalf("children: %v; %d workspaces (was %d)", children, len(files.CachedWorkspaces()), before)
	}
	for name, ws := range children {
		primary, _ := ws.SharedData[projecttemplates.PrimaryDirectoryIDKey].(string)
		ref, err := ws.GetDirectoryReference(primary)
		if err != nil || ref.Path != filepath.Join(canonical, name) || len(ws.Tasks) != 0 {
			t.Fatalf("%s: %+v %v", name, ref, err)
		}
	}
	if folderTreeHash(t, root) != hash || len(provider.requests) != calls {
		t.Fatal("applying changed the folder or called the model")
	}

	// The review's own canonical event did not make the record look stale.
	if _, history := f.call(t, http.MethodGet, historyPath, nil); history["context_current"] != true {
		t.Fatalf("a clean apply reported changed context: %v", history)
	}
	execution = arrangedExecution(t, f, historyPath)
	group = execution["groups"].([]any)[0].(map[string]any)
	if execution["state"] != "completed" || group["state"] != "completed" || group["route"] != outcome["route"] || group["workspaces"] != float64(2) ||
		execution["reviewable"] != false || execution["declinable"] != false {
		t.Fatalf("history after apply: %v", execution)
	}
	// A confirmed group cannot be reviewed again, and declining cannot undo it.
	if status, body := f.call(t, http.MethodPost, reviewPath, map[string]any{"revision": folderState["revision"], "expected_record_revision": 1, "operation": 0}); status != http.StatusConflict || body["error"] != "arrangement_group_confirmed" {
		t.Fatalf("second review: %d %v", status, body)
	}
	if status, body := f.call(t, http.MethodPost, historyPath+"/decline", map[string]any{"expected_record_revision": 1}); status != http.StatusConflict || body["error"] != "arrangement_effects_applied" {
		t.Fatalf("decline after apply: %d %v", status, body)
	}
	if len(files.CachedWorkspaces()) != before+3 {
		t.Fatal("later requests created workspaces")
	}
}

func TestPersonalAssistantArrangement_StaleRuntimeDeclineAndClosedReviewsCreateNothing(t *testing.T) {
	f := newDraftServerFixture(t)
	seedAlbumsCollection(t)
	files := f.builder.workspaceFileStore
	before := len(files.CachedWorkspaces())
	provider := &arrangedGroupProvider{members: []string{"Album-1", "Album-3"}}
	conversation, revision, historyPath := arrangedProposal(t, f, provider)
	reviewPath := historyPath + "/group-review"
	request := map[string]any{"revision": revision, "expected_record_revision": 1, "operation": 0}

	// A different registered runtime under the same configuration is not the
	// runtime that wrote this intent.
	f.builder.llmFactory.Register("claude_code", &arrangedGroupProvider{members: provider.members})
	if status, body := f.call(t, http.MethodPost, reviewPath, request); status != http.StatusConflict || body["error"] != "arrangement_runtime_changed" {
		t.Fatalf("drifted runtime: %d %v", status, body)
	}
	f.builder.llmFactory.Register("claude_code", provider)

	status, opened := f.call(t, http.MethodPost, reviewPath, request)
	if status != http.StatusOK {
		t.Fatalf("review: %d %v", status, opened)
	}
	offer := opened["folder_context"].(map[string]any)["offer_id"].(string)
	view := albumsReview{conversation: conversation, offer: offer}.read(t, f)
	// Declining fences the open review: its Set up is refused afterwards.
	status, declined := f.call(t, http.MethodPost, historyPath+"/decline", map[string]any{"expected_record_revision": 1})
	if status != http.StatusOK || declined["arrangement"].(map[string]any)["state"] != "cancelled" {
		t.Fatalf("decline: %d %v", status, declined)
	}
	path := "/api/personal-assistant/folder-digest/offers/" + offer + "/setup"
	if status, body := f.call(t, http.MethodPost, path, map[string]any{"request_id": "after-decline", "plan_digest": view["plan"].(map[string]any)["digest"]}); status == http.StatusOK {
		t.Fatalf("a declined arrangement was set up: %v", body)
	}
	execution := arrangedExecution(t, f, historyPath)
	if execution["state"] != "cancelled" || execution["reviewable"] != false || execution["groups"].([]any)[0].(map[string]any)["state"] != "closed" {
		t.Fatalf("history after decline: %v", execution)
	}
	if status, body := f.call(t, http.MethodPost, reviewPath, map[string]any{"revision": opened["folder_context"].(map[string]any)["revision"], "expected_record_revision": 2, "operation": 0}); status != http.StatusConflict || body["error"] != "arrangement_stale" {
		t.Fatalf("review after decline: %d %v", status, body)
	}
	if len(files.CachedWorkspaces()) != before {
		t.Fatal("a stale, drifted or declined arrangement created a workspace")
	}
}

// waitForSetup reads the offer until its run stops or resolves.
func waitForSetup(t *testing.T, f *draftServerFixture, offer string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		status, body := f.call(t, http.MethodGet, "/api/personal-assistant/folder-digest?offer_id="+offer, nil)
		current, _ := body["folder_digest"].(map[string]any)["offer"].(map[string]any)
		if status != http.StatusOK || current == nil {
			t.Fatalf("read offer: %d %v", status, body)
		}
		setup, _ := current["setup"].(map[string]any)
		if current["status"] == "resolved" || (setup != nil && setup["status"] == "stopped") {
			return current
		}
		if time.Now().After(deadline) {
			t.Fatalf("the setup did not settle: %v", current)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestPersonalAssistantArrangement_APartialRunContinuesFromCanonicalIDs(t *testing.T) {
	f := newDraftServerFixture(t)
	root := seedAlbumsCollection(t)
	files := f.builder.workspaceFileStore
	before := len(files.CachedWorkspaces())
	provider := &arrangedGroupProvider{members: []string{"Album-1", "Album-3"}}
	conversation, revision, historyPath := arrangedProposal(t, f, provider)
	status, opened := f.call(t, http.MethodPost, historyPath+"/group-review", map[string]any{"revision": revision, "expected_record_revision": 1, "operation": 0})
	if status != http.StatusOK {
		t.Fatalf("review: %d %v", status, opened)
	}
	offer := opened["folder_context"].(map[string]any)["offer_id"].(string)
	view := albumsReview{conversation: conversation, offer: offer}.read(t, f)
	digest := view["plan"].(map[string]any)["digest"]
	// The second member's folder moves away after review: its line fails and
	// the run stops with the group and the first workspace done.
	moved := filepath.Join(root, "Album-3")
	if err := os.Rename(moved, moved+"-away"); err != nil {
		t.Fatal(err)
	}
	path := "/api/personal-assistant/folder-digest/offers/" + offer + "/setup"
	if status, body := f.call(t, http.MethodPost, path, map[string]any{"request_id": "first-pass", "plan_digest": digest}); status != http.StatusOK {
		t.Fatalf("set up: %d %v", status, body)
	}
	stopped := waitForSetup(t, f, offer)
	if setup := stopped["setup"].(map[string]any); setup["stop_reason"] != "member_failed" {
		t.Fatalf("stop: %v", setup)
	}
	if len(files.CachedWorkspaces()) != before+2 {
		t.Fatalf("partial run: %d workspaces (was %d)", len(files.CachedWorkspaces()), before)
	}
	execution := arrangedExecution(t, f, historyPath)
	if execution["state"] != "partially_applied" || execution["groups"].([]any)[0].(map[string]any)["state"] != "partially_applied" {
		t.Fatalf("history after a partial run: %v", execution)
	}
	// Declining cannot undo the part that ran.
	if status, body := f.call(t, http.MethodPost, historyPath+"/decline", map[string]any{"expected_record_revision": 1}); status != http.StatusConflict || body["error"] != "arrangement_effects_applied" {
		t.Fatalf("decline after a partial run: %d %v", status, body)
	}
	// The folder is back; Continue carries on from the canonical IDs the first
	// pass recorded, creating neither a second group nor a second workspace.
	if err := os.Rename(moved+"-away", moved); err != nil {
		t.Fatal(err)
	}
	if status, body := f.call(t, http.MethodPost, path, map[string]any{"request_id": "continue", "plan_digest": digest}); status != http.StatusOK {
		t.Fatalf("continue: %d %v", status, body)
	}
	resolved := waitForSetup(t, f, offer)
	if resolved["status"] != "resolved" || len(files.CachedWorkspaces()) != before+3 {
		t.Fatalf("continue: %v; %d workspaces", resolved["status"], len(files.CachedWorkspaces()))
	}
	groups := 0
	for _, ws := range files.CachedWorkspaces() {
		if ws != nil && ws.Kind == "group" && ws.Name == "Client work" {
			groups++
		}
	}
	execution = arrangedExecution(t, f, historyPath)
	if groups != 1 || execution["state"] != "completed" || execution["groups"].([]any)[0].(map[string]any)["workspaces"] != float64(2) {
		t.Fatalf("after continue: %d groups, %v", groups, execution)
	}
}

// A finished group is history, not an open review: the next group of the same
// arrangement can be reviewed and set up after it, each from its own review.
func TestPersonalAssistantArrangement_ASecondGroupFollowsAFinishedFirst(t *testing.T) {
	f := newDraftServerFixture(t)
	seedAlbumsCollection(t)
	files := f.builder.workspaceFileStore
	before := len(files.CachedWorkspaces())
	provider := &arrangedGroupProvider{members: []string{"Album-1", "Album-2"}, second: []string{"Album-4"}}
	conversation, revision, historyPath := arrangedProposal(t, f, provider)
	reviewPath := historyPath + "/group-review"
	status, first := f.call(t, http.MethodPost, reviewPath, map[string]any{"revision": revision, "expected_record_revision": 1, "operation": 0})
	if status != http.StatusOK {
		t.Fatalf("first review: %d %v", status, first)
	}
	firstOffer := first["folder_context"].(map[string]any)["offer_id"].(string)
	// While the first review is open, the second is refused.
	firstRevision := first["folder_context"].(map[string]any)["revision"].(string)
	if status, body := f.call(t, http.MethodPost, reviewPath, map[string]any{"revision": firstRevision, "expected_record_revision": 1, "operation": 1}); status != http.StatusConflict || body["error"] != "folder_review_pending" {
		t.Fatalf("second review over an open first: %d %v", status, body)
	}
	setUpAndWait(t, f, firstOffer, albumsReview{conversation: conversation, offer: firstOffer}.read(t, f), "first-group")
	execution := arrangedExecution(t, f, historyPath)
	if execution["state"] != "partially_applied" || execution["reviewable"] != true || execution["declinable"] != false {
		t.Fatalf("history after the first group: %v", execution)
	}
	status, second := f.call(t, http.MethodPost, reviewPath, map[string]any{"revision": firstRevision, "expected_record_revision": 1, "operation": 1})
	if status != http.StatusOK {
		t.Fatalf("second review after the first finished: %d %v", status, second)
	}
	secondOffer := second["folder_context"].(map[string]any)["offer_id"].(string)
	if secondOffer == firstOffer {
		t.Fatal("the second group reused the first group's review")
	}
	setUpAndWait(t, f, secondOffer, albumsReview{conversation: conversation, offer: secondOffer}.read(t, f), "second-group")
	execution = arrangedExecution(t, f, historyPath)
	groups := execution["groups"].([]any)
	if execution["state"] != "completed" || execution["reviewable"] != false || len(groups) != 2 ||
		groups[0].(map[string]any)["workspaces"] != float64(2) || groups[1].(map[string]any)["workspaces"] != float64(1) {
		t.Fatalf("history after both groups: %v", execution)
	}
	// Two groups and three member workspaces, each once.
	if len(files.CachedWorkspaces()) != before+5 {
		t.Fatalf("%d workspaces (was %d)", len(files.CachedWorkspaces()), before)
	}
}

// A stopped arranged run whose folder is attached again (as after a restart)
// is recovered by picking the folder again, never set up a second time.
func TestPersonalAssistantArrangement_AStoppedRunIsRecoveredAfterTheFolderIsPickedAgain(t *testing.T) {
	f := newDraftServerFixture(t)
	root := seedAlbumsCollection(t)
	files := f.builder.workspaceFileStore
	before := len(files.CachedWorkspaces())
	provider := &arrangedGroupProvider{members: []string{"Album-1", "Album-3"}}
	conversation, revision, historyPath := arrangedProposal(t, f, provider)
	status, opened := f.call(t, http.MethodPost, historyPath+"/group-review", map[string]any{"revision": revision, "expected_record_revision": 1, "operation": 0})
	if status != http.StatusOK {
		t.Fatalf("review: %d %v", status, opened)
	}
	offer := opened["folder_context"].(map[string]any)["offer_id"].(string)
	digest := albumsReview{conversation: conversation, offer: offer}.read(t, f)["plan"].(map[string]any)["digest"]
	moved := filepath.Join(root, "Album-3")
	if err := os.Rename(moved, moved+"-away"); err != nil {
		t.Fatal(err)
	}
	path := "/api/personal-assistant/folder-digest/offers/" + offer + "/setup"
	if status, body := f.call(t, http.MethodPost, path, map[string]any{"request_id": "first-pass", "plan_digest": digest}); status != http.StatusOK {
		t.Fatalf("set up: %d %v", status, body)
	}
	waitForSetup(t, f, offer)
	if err := os.Rename(moved+"-away", moved); err != nil {
		t.Fatal(err)
	}
	// The folder is attached again: a new observation of the same folder.
	status, selected := f.call(t, http.MethodPost, "/api/home-assistant/folder-context/select", map[string]any{
		"conversation_id": conversation, "revision": opened["folder_context"].(map[string]any)["revision"], "mode": "chip", "chip": "music",
	})
	if status != http.StatusOK {
		t.Fatalf("attach again: %d %v", status, selected)
	}
	observation := selected["observation"].(map[string]any)["id"].(string)
	reselected := selected["revision"].(string)
	// Reviewing the folder again returns the same confirmed run.
	_, _, recoveredOffer := reviewFolder(t, f, map[string]any{"conversation_id": conversation, "revision": reselected, "selection_id": observation, "candidate_id": "candidate-0"})
	if recoveredOffer != offer {
		t.Fatalf("picking the folder again made a new review %s, not the stopped run %s", recoveredOffer, offer)
	}
	if status, body := f.call(t, http.MethodPost, path, map[string]any{"request_id": "continue", "plan_digest": digest}); status != http.StatusOK {
		t.Fatalf("continue after picking again: %d %v", status, body)
	}
	resolved := waitForSetup(t, f, offer)
	if resolved["status"] != "resolved" || len(files.CachedWorkspaces()) != before+3 {
		t.Fatalf("continue: %v; %d workspaces (was %d)", resolved["status"], len(files.CachedWorkspaces()), before)
	}
	execution := arrangedExecution(t, f, historyPath)
	if execution["state"] != "completed" {
		t.Fatalf("history after recovery: %v", execution)
	}
}

func TestPersonalAssistantArrangement_PendingReviewsProtectEachOther(t *testing.T) {
	f := newDraftServerFixture(t)
	seedAlbumsCollection(t)
	files := f.builder.workspaceFileStore
	before := len(files.CachedWorkspaces())
	provider := &arrangedGroupProvider{members: []string{"Album-2", "Album-5"}}
	conversation, revision, historyPath := arrangedProposal(t, f, provider)
	status, answer := f.call(t, http.MethodGet, "/api/home-assistant/conversations/"+conversation, nil)
	if status != http.StatusOK {
		t.Fatal(status, answer)
	}
	selection := answer["folder_context"].(map[string]any)["observation"].(map[string]any)["id"].(string)
	// A manual review of the root opens first, in the same conversation.
	_, manualRevision, manualOffer := reviewFolder(t, f, map[string]any{"conversation_id": conversation, "revision": revision, "selection_id": selection, "candidate_id": "candidate-0"})
	if status, body := f.call(t, http.MethodPost, historyPath+"/group-review", map[string]any{"revision": manualRevision, "expected_record_revision": 1, "operation": 0}); status != http.StatusConflict || body["error"] != "folder_review_pending" {
		t.Fatalf("an arranged review replaced a manual one: %d %v", status, body)
	}
	// Closing the manual review (Keep chatting) lets the arranged one open.
	status, closed := f.call(t, http.MethodPost, "/api/home-assistant/folder-context/review/close", map[string]any{"conversation_id": conversation, "revision": manualRevision, "offer_id": manualOffer})
	if status != http.StatusOK {
		t.Fatalf("close: %d %v", status, closed)
	}
	afterClose := closed["folder_context"].(map[string]any)["revision"].(string)
	status, opened := f.call(t, http.MethodPost, historyPath+"/group-review", map[string]any{"revision": afterClose, "expected_record_revision": 1, "operation": 0})
	if status != http.StatusOK {
		t.Fatalf("arranged review after closing the manual one: %d %v", status, opened)
	}
	arrangedRevision := opened["folder_context"].(map[string]any)["revision"].(string)
	// Now a manual review (or its Adjust) cannot retire or rewrite the arranged one.
	status, body := f.call(t, http.MethodPost, "/api/home-assistant/folder-context/review", map[string]any{
		"conversation_id": conversation, "revision": arrangedRevision, "selection_id": selection, "candidate_id": "candidate-0",
		"structure": map[string]any{"group_name": "Everything"},
	})
	if status == http.StatusOK {
		t.Fatalf("a manual Adjust replaced the arranged review: %v", body)
	}
	offer := opened["folder_context"].(map[string]any)["offer_id"].(string)
	view := albumsReview{conversation: conversation, offer: offer}.read(t, f)
	if structure := view["structure"].(map[string]any); structure["version"] != float64(2) || len(structure["members"].([]any)) != 2 || view["status"] != "pending" {
		t.Fatalf("the arranged review changed: %v", view)
	}
	if len(files.CachedWorkspaces()) != before {
		t.Fatal("protecting reviews created a workspace")
	}
}

// Chat text is never a confirmation: "yes, apply it" after a proposal is an
// ordinary turn that opens no review and creates nothing.
func TestPersonalAssistantArrangement_ABareYesInChatAppliesNothing(t *testing.T) {
	f := newDraftServerFixture(t)
	seedAlbumsCollection(t)
	files := f.builder.workspaceFileStore
	before := len(files.CachedWorkspaces())
	provider := &arrangedGroupProvider{members: []string{"Album-1", "Album-2"}}
	conversation, revision, historyPath := arrangedProposal(t, f, provider)
	selection := ""
	if _, read := f.call(t, http.MethodGet, "/api/home-assistant/conversations/"+conversation, nil); read["folder_context"] != nil {
		selection = read["folder_context"].(map[string]any)["observation"].(map[string]any)["id"].(string)
	}
	for _, prompt := range []string{"Yes", "Yes, apply it and create the Client work group", "Confirm the arrangement"} {
		status, answer := f.call(t, http.MethodPost, "/api/home-assistant/ask", map[string]any{
			"prompt": prompt, "conversation": map[string]any{"id": conversation},
			"folder_context": map[string]any{"revision": revision, "selection_id": selection},
		})
		if status != http.StatusOK || answer["arrangement"] != nil || answer["arrangement_review"] != nil {
			t.Fatalf("%q: %d %v", prompt, status, answer)
		}
		if folder, ok := answer["folder_context"].(map[string]any); ok && folder["revision"] != nil {
			revision = folder["revision"].(string)
			if folder["offer_id"] != nil && folder["offer_id"] != "" {
				t.Fatalf("%q opened a review: %v", prompt, folder)
			}
		}
	}
	execution := arrangedExecution(t, f, historyPath)
	if execution["groups"].([]any)[0].(map[string]any)["state"] != "not_reviewed" || len(files.CachedWorkspaces()) != before {
		t.Fatalf("chat applied an arrangement: %v; %d workspaces (was %d)", execution, len(files.CachedWorkspaces()), before)
	}
	var effects int
	if err := f.builder.sessionStore.DB().QueryRowContext(context.Background(), `SELECT COUNT(*) FROM assistant_arrangement_effects`).Scan(&effects); err != nil || effects != 0 {
		t.Fatalf("chat bound a review: %d %v", effects, err)
	}
}

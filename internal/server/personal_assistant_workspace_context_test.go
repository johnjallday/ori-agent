package server

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/agenthttp"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// workspaceAwarenessFixture deliberately uses ordinary workspaces. A group
// named Music Home is NOT evidence of an assistant-program Home or plugin
// compatibility. All files, stores and the assistant relationship are disposable.
// These seams can also serve the later resolver/navigation regression tests.
type workspaceAwarenessFixture struct {
	*draftServerFixture
	provider                  *capturingChatProvider
	home, project, other      *workspace.Workspace
	selection                 map[string]any
	conversationID, revision  string
	offerID, source, sentinel string
}

func newWorkspaceAwarenessFixture(t *testing.T) *workspaceAwarenessFixture {
	t.Helper()
	f := &workspaceAwarenessFixture{draftServerFixture: newDraftServerFixture(t), provider: &capturingChatProvider{}}
	f.builder.llmFactory.Register("claude_code", f.provider)
	if status, body := f.call(t, http.MethodPost, "/api/settings/system-model", map[string]string{"provider": "claude_code", "model": "sonnet"}); status != http.StatusOK {
		t.Fatal(status, body)
	}
	create := func(name, kind, parent string) *workspace.Workspace {
		t.Helper()
		ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: name})
		ws.Kind, ws.ParentID, ws.OwnerUserID = kind, parent, "local"
		if err := workspace.CreateNativeWorkspace(context.Background(), f.builder.workspaceStore, ws); err != nil {
			t.Fatal(err)
		}
		canonical, err := f.builder.workspaceFileStore.Get(ws.ID)
		if err != nil || canonical.FolderSlug == "" {
			t.Fatal("fixture must publish an actual canonical page slug", err)
		}
		return canonical
	}
	f.home = create("Music Home fixture", "group", "")
	f.project = create("Album-1 fixture", "", f.home.ID)
	f.other = create("Second project fixture", "", "")
	folder := filepath.Join(os.Getenv("HOME"), "Documents", "Album-5 fixture")
	if err := os.MkdirAll(folder, 0750); err != nil {
		t.Fatal(err)
	}
	f.source, f.sentinel = filepath.Join(folder, "release-notes.txt"), "UNLINKED_FILE_BODY_MUST_NOT_REACH_THE_MODEL"
	if err := os.WriteFile(f.source, []byte(f.sentinel), 0600); err != nil {
		t.Fatal(err)
	}
	draft := uuid.NewString()
	status, selected := f.call(t, http.MethodPost, "/api/home-assistant/folder-context/select", map[string]any{"draft_id": draft, "mode": "chip", "chip": "documents"})
	if status != http.StatusOK {
		t.Fatal(status, selected)
	}
	observation := selected["observation"].(map[string]any)
	candidate := ""
	for _, value := range observation["projects"].([]any) {
		project := value.(map[string]any)
		if project["name"] == "Album-5 fixture" {
			candidate = project["id"].(string)
		}
	}
	if candidate == "" {
		t.Fatal("fixture folder was not observed")
	}
	f.selection = map[string]any{"draft_id": draft, "revision": "", "selection_id": observation["id"], "candidate_id": candidate}
	f.conversationID, f.revision, f.offerID = reviewFolder(t, f.draftServerFixture, f.selection)
	return f
}

func (f *workspaceAwarenessFixture) turn(prompt string, location *workspace.Workspace) map[string]any {
	return map[string]any{
		"prompt": prompt, "intent": "assistant_conversation",
		"context":        map[string]string{"origin": "personal_assistant_panel", "surface": "workspace", "page_path": "/workspaces/" + location.FolderSlug, "workspace_id": location.ID},
		"conversation":   map[string]string{"id": f.conversationID},
		"folder_context": map[string]any{"selection_id": f.selection["selection_id"], "revision": f.revision},
	}
}

func TestAssistantWorkspaceContext_BaselineNavigationCannotMoveOwnerOrReadFolder(t *testing.T) {
	f := newWorkspaceAwarenessFixture(t)
	for _, location := range []*workspace.Workspace{f.home, f.project, f.other} {
		status, reply := f.call(t, http.MethodPost, "/api/home-assistant/ask", f.turn("Discuss the attached folder structure", location))
		if status != http.StatusOK || reply["conversation"].(map[string]any)["stored"] != true {
			t.Fatalf("turn: %d %v", status, reply)
		}
		folder := reply["folder_context"].(map[string]any)
		f.revision = folder["revision"].(string)
		if folder["offer_id"] != f.offerID {
			t.Fatal("navigation replaced pending review")
		}
		call := f.provider.requests[len(f.provider.requests)-1]
		assertNoPanelExecutionAuthority(t, call)
		input := mustJSON(t, call.Messages)
		if strings.Contains(input, f.sentinel) || strings.Contains(input, f.source) {
			t.Fatal("metadata selection became a file reader")
		}
		saved, err := f.builder.sessionStore.GetSession(context.Background(), f.conversationID)
		if err != nil || saved.FolderID != f.hqID {
			t.Fatal("navigation changed conversation ownership", err)
		}
	}
	before := len(f.provider.requests)
	status, reply := f.call(t, http.MethodPost, "/api/home-assistant/ask", f.turn("Summarize these documents", f.project))
	if status != http.StatusOK || !strings.Contains(reply["response"].(string), "File contents have not been read") || len(f.provider.requests) != before {
		t.Fatal("metadata content request reached a provider or fabricated a read", reply)
	}
}

// This is the canonical store-read baseline, not the unimplemented workspace
// overview or model latency. Keep the warm fixture/sample method for group 2.
func TestAssistantWorkspaceContext_WarmCanonicalReadBaseline(t *testing.T) {
	f := newWorkspaceAwarenessFixture(t)
	if err := f.builder.workspaceStore.Update(f.project.ID, func(ws *workspace.Workspace) error {
		for range 100 {
			ws.Tasks = append(ws.Tasks, workspace.Task{ID: uuid.NewString(), Description: "Recorded fixture task", Status: workspace.TaskStatusPending})
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	durations := make([]time.Duration, 0, 200)
	for i := 0; i < 201; i++ {
		start := time.Now()
		project, err := f.builder.workspaceStore.Get(f.project.ID)
		if err != nil || len(project.Tasks) != 100 {
			t.Fatal("baseline tasks changed", err)
		}
		if _, err := f.builder.workspaceStore.Get(f.home.ID); err != nil {
			t.Fatal(err)
		}
		if i > 0 { // discard the initial warm-up
			durations = append(durations, time.Since(start))
		}
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	t.Logf("warm canonical project+parent reads (100 tasks, 200 samples): p50=%s p95=%s; excludes context projection, notes/files and provider", durations[99], durations[189])
	resolver := &agenthttp.AssistantWorkspaceResolver{Source: f.builder.workspaceStore}
	durations = durations[:0]
	for i := 0; i < 201; i++ {
		start := time.Now()
		projection := resolver.Resolve(context.Background(), "local", "Hello", &agenthttp.HomeAssistantRouteContext{WorkspaceID: f.project.ID})
		if projection.Overview == nil || len(projection.Overview.Tasks) != 5 {
			t.Fatal("overview omitted bounded task previews", projection.Reason)
		}
		if i > 0 {
			durations = append(durations, time.Since(start))
		}
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	t.Logf("warm bounded workspace overview (same 100 tasks, 200 samples): p50=%s p95=%s; includes resolver/projection, excludes provider and deeper reads", durations[99], durations[189])
}

func assertNoPanelExecutionAuthority(t *testing.T, request llm.ChatRequest) {
	t.Helper()
	if request.WorkspaceID != "" || request.WorkspaceDir != "" || request.ExecutionScope != nil || len(request.MCPServers) != 0 {
		t.Fatal("panel context became CLI execution authority")
	}
	// Every panel tool is a read. Nothing here saves a note, changes a task,
	// writes memory, manages agents or delegates.
	allowed := map[string]bool{"home_workspaces": true, "home_tasks": true, "home_sessions": true, "home_opportunities": true, "home_usage": true, "home_agents": true, "assistant_workspace_discovery": true,
		"assistant_workspace_notes": true, "assistant_workspace_note": true, "assistant_workspace_tasks": true, "assistant_workspace_task": true}
	for _, tool := range request.Tools {
		if !allowed[tool.Name] {
			t.Fatalf("unexpected panel tool %q", tool.Name)
		}
	}
}

// Current workspace and the independently attached observation/review are
// separate canonical facts. No new options does not mean no existing review.
func TestAssistantWorkspaceContext_CurrentWorkspaceWithPendingReview(t *testing.T) {
	f := newWorkspaceAwarenessFixture(t)
	before := len(f.builder.workspaceFileStore.CachedWorkspaces())
	request := f.turn("Add this to my workspace", f.home)
	status, route := f.call(t, http.MethodPost, "/api/home-assistant/route", request)
	if status != http.StatusOK || route["intent"] != "assistant_conversation" || len(f.provider.requests) != 0 {
		t.Fatalf("route: %d %v", status, route)
	}
	status, history := f.call(t, http.MethodGet, "/api/home-assistant/conversations/"+f.conversationID, nil)
	if status != http.StatusOK {
		t.Fatal(status, history)
	}
	review := history["folder_reviews"].(map[string]any)[f.offerID].(map[string]any)
	if review["status"] != "pending" || review["review_digest"] == "" || review["subject"].(map[string]any)["name"] != "Album-5 fixture" {
		t.Fatalf("expected canonical pending review: %v", review)
	}
	status, reply := f.call(t, http.MethodPost, "/api/home-assistant/ask", request)
	if status != http.StatusOK || reply["conversation"].(map[string]any)["stored"] != true || len(f.provider.requests) != 1 {
		t.Fatalf("ask: %d %v", status, reply)
	}
	call := f.provider.requests[0]
	assertNoPanelExecutionAuthority(t, call)
	t.Logf("fixture Route/Ask payload: %s", mustJSON(t, request))
	t.Logf("fixture canonical review: %s", mustJSON(t, review))
	t.Logf("fixture actual provider messages (metadata only): %s", mustJSON(t, call.Messages))
	input := mustJSON(t, call.Messages)
	user := call.Messages[len(call.Messages)-1].Content
	if !strings.Contains(user, "<folder_setup_options>[]</folder_setup_options>") || !strings.Contains(user, "empty means no new suggestion") ||
		!strings.Contains(user, "<folder_review_context>") || !strings.Contains(user, `"status":"awaiting_confirmation"`) || !strings.Contains(user, `"subject":"Album-5 fixture"`) {
		t.Fatal("missing canonical existing-review projection")
	}
	for _, present := range []string{f.home.Name, f.home.ID, "workspace_turn"} {
		if !strings.Contains(input, present) {
			t.Fatalf("missing validated workspace fact %q", present)
		}
	}
	for _, absent := range []string{f.offerID, f.sentinel, f.source} {
		if strings.Contains(input, absent) {
			t.Fatalf("unexpected baseline model input %q", absent)
		}
	}
	if !strings.Contains(user, "File contents have not been read") || !strings.Contains(user, "Album-5 fixture") {
		t.Fatal("missing metadata-only folder evidence")
	}
	handoff, ok := reply["folder_setup_suggestion"].(map[string]any)
	if reply["folder_context"].(map[string]any)["offer_id"] != f.offerID || !ok || handoff["offer_id"] != f.offerID || handoff["options"] != nil {
		t.Fatal("Ask replaced the existing review rather than focusing its canonical card")
	}
	saved, err := f.builder.sessionStore.GetSession(context.Background(), f.conversationID)
	if err != nil || saved.FolderID != f.hqID {
		t.Fatal("page context moved the canonical HQ conversation", err)
	}
	if len(f.builder.workspaceFileStore.CachedWorkspaces()) != before {
		t.Fatal("Route, hydration or Ask created a workspace")
	}
	contents, err := os.ReadFile(f.source)
	if err != nil || string(contents) != f.sentinel {
		t.Fatal("source file changed", err)
	}
	status, history = f.call(t, http.MethodGet, "/api/home-assistant/conversations/"+f.conversationID, nil)
	if status != http.StatusOK || history["folder_reviews"].(map[string]any)[f.offerID].(map[string]any)["status"] != "pending" {
		t.Fatal("conversational request executed or lost review", history)
	}
}

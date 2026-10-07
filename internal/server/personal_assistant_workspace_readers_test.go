package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// toolChatProvider can run Ori's brokered tools. It replays a script of tool
// calls and answers, records each request, and can change host state between
// rounds to stand in for something happening while the model works.
type toolChatProvider struct {
	capturingChatProvider
	script []llm.ChatResponse
	before map[int]func()
}

func (p *toolChatProvider) Capabilities() llm.ProviderCapabilities {
	return llm.ProviderCapabilities{SupportsSystemPrompt: true, SupportsTools: true}
}

func (p *toolChatProvider) Chat(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	p.mu.Lock()
	p.requests = append(p.requests, req)
	round := len(p.requests)
	p.mu.Unlock()
	if hook := p.before[round]; hook != nil {
		hook()
	}
	if round > len(p.script) {
		return &llm.ChatResponse{Content: "Done.", Model: "sonnet", Provider: "claude_code"}, nil
	}
	step := p.script[round-1]
	return &step, nil
}

func readerCall(name string, args map[string]any) llm.ChatResponse {
	encoded, _ := json.Marshal(args)
	return llm.ChatResponse{ToolCalls: []llm.ToolCall{{Name: name, Arguments: string(encoded)}}}
}

type workspaceReaderFixture struct {
	*draftServerFixture
	provider *toolChatProvider
	project  *workspace.Workspace
	noteID   string
	taskID   string
}

const readerNoteTail = "REAL_STORE_NOTE_TAIL master the single before the Friday release"

func newWorkspaceReaderFixture(t *testing.T) *workspaceReaderFixture {
	t.Helper()
	f := &workspaceReaderFixture{draftServerFixture: newDraftServerFixture(t), provider: &toolChatProvider{before: map[int]func(){}}}
	f.builder.llmFactory.Register("claude_code", f.provider)
	if status, body := f.call(t, http.MethodPost, "/api/settings/system-model", map[string]string{"provider": "claude_code", "model": "sonnet"}); status != http.StatusOK {
		t.Fatal(status, body)
	}
	f.project = placementWorkspace(t, f.draftServerFixture, "Album-1 reader fixture", "", "")
	now := time.Now().UTC().Truncate(time.Second)
	f.noteID, f.taskID = uuid.NewString(), uuid.NewString()
	if err := f.builder.sessionStore.CreateNote(context.Background(), &session.WorkspaceNote{
		ID: f.noteID, WorkspaceID: f.project.ID, Name: "Release plan", CreatedAt: now, UpdatedAt: now,
		Content: strings.Repeat("An opening paragraph that is longer than any list preview. ", 4) + readerNoteTail + ".",
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.builder.workspaceStore.Update(f.project.ID, func(ws *workspace.Workspace) error {
		ws.Tasks = append(ws.Tasks, workspace.Task{ID: f.taskID, WorkspaceID: ws.ID, Description: "Master the single", Details: "Send stems to mastering.", Status: workspace.TaskStatusPending, CreatedAt: now, UpdatedAt: now})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *workspaceReaderFixture) ask(t *testing.T, conversationID, prompt string) map[string]any {
	t.Helper()
	conversation := map[string]string{}
	if conversationID != "" {
		conversation["id"] = conversationID
	}
	status, reply := f.call(t, http.MethodPost, "/api/home-assistant/ask", map[string]any{
		"prompt": prompt, "intent": "assistant_conversation", "context": placementContext(f.project), "conversation": conversation,
	})
	if status != http.StatusOK {
		t.Fatalf("ask: %d %v", status, reply)
	}
	return reply
}

func (f *workspaceReaderFixture) lastToolResult(t *testing.T) string {
	t.Helper()
	request := f.provider.requests[len(f.provider.requests)-1]
	assertNoPanelExecutionAuthority(t, request)
	return request.Messages[len(request.Messages)-1].Content
}

func replySources(t *testing.T, value any) []map[string]any {
	t.Helper()
	context, _ := value.(map[string]any)
	raw, _ := context["sources"].([]any)
	sources := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		sources = append(sources, item.(map[string]any))
	}
	return sources
}

// Through the real routes and canonical stores: the whole note and the task are
// read, the reply's citations are checked, the references are saved with the
// turn without the bodies, and a later turn reads the records as they are now.
func TestAssistantWorkspaceReaders_RealStoreReadCiteSaveAndReadAgain(t *testing.T) {
	f := newWorkspaceReaderFixture(t)
	f.provider.script = []llm.ChatResponse{
		readerCall("assistant_workspace_note", map[string]any{"title": "Release plan"}),
		readerCall("assistant_workspace_task", map[string]any{"task_id": f.taskID}),
		{Content: "Master the single first [S1]; that task is still open [S2]. Unread claim [S8].", Model: "sonnet", Provider: "claude_code"},
	}
	sessionsBefore := len(f.builder.workspaceFileStore.CachedWorkspaces())
	reply := f.ask(t, "", "Based on my release plan and open tasks, what should I do next?")
	conversation := reply["conversation"].(map[string]any)
	if conversation["stored"] != true || !strings.Contains(reply["response"].(string), "[S1]") || strings.Contains(reply["response"].(string), "[S8]") {
		t.Fatalf("reply: %v", reply)
	}
	if len(f.provider.requests) != 3 || !strings.Contains(f.provider.requests[1].Messages[len(f.provider.requests[1].Messages)-1].Content, readerNoteTail) {
		t.Fatal("the note's full canonical content did not reach the model")
	}
	if task := f.lastToolResult(t); !strings.Contains(task, "Send stems to mastering.") || !strings.Contains(task, `"state":"backlog"`) && !strings.Contains(task, `"state":"ready"`) {
		t.Fatalf("task detail: %s", task)
	}
	sources := replySources(t, reply["workspace_context"])
	if len(sources) != 2 || sources[0]["kind"] != "note" || sources[0]["id"] != f.noteID || sources[0]["cited"] != true || sources[0]["coverage"] != "full" ||
		sources[0]["href"] != "/workspaces/"+f.project.FolderSlug+"/notes/"+f.noteID || sources[1]["kind"] != "task" || sources[1]["href"] != "/workspaces/"+f.project.FolderSlug+"/task/"+f.taskID {
		t.Fatalf("sources: %v", sources)
	}

	// Saved with the turn as references, and reloaded as history.
	id := conversation["id"].(string)
	_, saved := f.call(t, http.MethodGet, "/api/home-assistant/conversations/"+id, nil)
	messages := saved["messages"].([]any)
	answer := messages[len(messages)-1].(map[string]any)
	stored := replySources(t, answer["workspace_context"])
	if answer["role"] != "assistant" || len(stored) != 2 || stored[0]["version"] != sources[0]["version"] || answer["workspace_context"].(map[string]any)["historical"] != true {
		t.Fatalf("saved sources: %v", answer)
	}
	if encoded := mustJSON(t, saved); strings.Contains(encoded, readerNoteTail) || strings.Contains(encoded, "Send stems to mastering.") {
		t.Fatal("a source body was saved with the turn")
	}

	// The records change. The next turn reads them as they are now; the earlier
	// turn keeps what it read then.
	note, err := f.builder.sessionStore.GetNote(context.Background(), f.noteID)
	if err != nil {
		t.Fatal(err)
	}
	note.Content, note.UpdatedAt = "Plan changed: release moved to next month.", time.Now().UTC().Add(time.Minute)
	if err := f.builder.sessionStore.UpdateNote(context.Background(), note); err != nil {
		t.Fatal(err)
	}
	if err := f.builder.workspaceStore.Update(f.project.ID, func(ws *workspace.Workspace) error {
		for i := range ws.Tasks {
			if ws.Tasks[i].ID == f.taskID {
				ws.Tasks[i].Status, ws.Tasks[i].Result, ws.Tasks[i].UpdatedAt = workspace.TaskStatusCompleted, "Mastered and delivered.", time.Now().UTC().Add(time.Minute)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	first := len(f.provider.requests)
	f.provider.script = append(make([]llm.ChatResponse, first),
		readerCall("assistant_workspace_note", map[string]any{"note_id": f.noteID}),
		readerCall("assistant_workspace_task", map[string]any{"task_id": f.taskID}),
		llm.ChatResponse{Content: "The release moved [S1] and mastering is done [S2].", Model: "sonnet", Provider: "claude_code"})
	again := f.ask(t, id, "And now?")
	if !strings.Contains(f.provider.requests[first+1].Messages[len(f.provider.requests[first+1].Messages)-1].Content, "release moved to next month") ||
		!strings.Contains(f.lastToolResult(t), "Mastered and delivered.") {
		t.Fatal("the second turn answered from an earlier read")
	}
	fresh := replySources(t, again["workspace_context"])
	if len(fresh) != 2 || fresh[0]["version"] == sources[0]["version"] || fresh[1]["version"] == sources[1]["version"] {
		t.Fatalf("changed records kept their earlier version: %v", fresh)
	}
	// History restates where the earlier turn was asked, not what it read.
	history := mustJSON(t, f.provider.requests[first].Messages[:len(f.provider.requests[first].Messages)-1])
	if !strings.Contains(history, "earlier_workspace") || strings.Contains(history, f.noteID) || strings.Contains(history, readerNoteTail) {
		t.Fatal("history carried an earlier turn's source references or content")
	}
	_, reloaded := f.call(t, http.MethodGet, "/api/home-assistant/conversations/"+id, nil)
	rows := reloaded["messages"].([]any)
	var answers []map[string]any
	for _, row := range rows {
		if message := row.(map[string]any); message["role"] == "assistant" {
			answers = append(answers, message)
		}
	}
	if len(answers) != 2 || replySources(t, answers[0]["workspace_context"])[0]["version"] != sources[0]["version"] || replySources(t, answers[1]["workspace_context"])[0]["version"] != fresh[0]["version"] {
		t.Fatal("an earlier answer's source was rewritten as a fresh read")
	}

	// Reading wrote nothing: one note, one task, no new workspace.
	notes, _ := f.builder.sessionStore.ListNotesByWorkspace(context.Background(), f.project.ID)
	after, _ := f.builder.workspaceStore.Get(f.project.ID)
	if len(notes) != 1 || len(after.Tasks) != 1 || len(f.builder.workspaceFileStore.CachedWorkspaces()) != sessionsBefore {
		t.Fatal("a read created a note, a task or a workspace")
	}
}

// Access is checked again for every read. A workspace that is removed while the
// model works stops the turn: nothing more is read and nothing is saved.
func TestAssistantWorkspaceReaders_RemovedWorkspaceStopsTheTurnMidRead(t *testing.T) {
	f := newWorkspaceReaderFixture(t)
	f.provider.script = []llm.ChatResponse{
		readerCall("assistant_workspace_tasks", nil),
		readerCall("assistant_workspace_note", map[string]any{"note_id": f.noteID}),
		{Content: "This must not be saved.", Model: "sonnet", Provider: "claude_code"},
	}
	f.provider.before[2] = func() {
		if err := f.builder.workspaceStore.Update(f.project.ID, func(ws *workspace.Workspace) error {
			ws.Status = workspace.StatusTrashed
			return nil
		}); err != nil {
			t.Error(err)
		}
	}
	reply := f.ask(t, "", "What is in my release plan?")
	if reply["model_unavailable"] != true || reply["conversation"].(map[string]any)["stored"] == true || len(replySources(t, reply["workspace_context"])) != 0 {
		t.Fatalf("a turn finished after its workspace was removed: %v", reply)
	}
	for _, request := range f.provider.requests {
		if strings.Contains(mustJSON(t, request.Messages), readerNoteTail) {
			t.Fatal("the note was read after the workspace was removed")
		}
	}
	_, list := f.call(t, http.MethodGet, "/api/home-assistant/conversations", nil)
	if len(list["conversations"].([]any)) != 0 {
		t.Fatal("the stopped turn was saved")
	}
}

// A note deleted or moved after it was listed is reported as not found, in the
// same words as a note that never existed here.
func TestAssistantWorkspaceReaders_DeletedOrMovedNoteIsNotRead(t *testing.T) {
	f := newWorkspaceReaderFixture(t)
	other := placementWorkspace(t, f.draftServerFixture, "Other reader fixture", "", "")
	note, err := f.builder.sessionStore.GetNote(context.Background(), f.noteID)
	if err != nil {
		t.Fatal(err)
	}
	note.WorkspaceID = other.ID
	if err := f.builder.sessionStore.UpdateNote(context.Background(), note); err != nil {
		t.Fatal(err)
	}
	f.provider.script = []llm.ChatResponse{
		readerCall("assistant_workspace_note", map[string]any{"note_id": f.noteID}),
		{Content: "I could not read that note.", Model: "sonnet", Provider: "claude_code"},
	}
	reply := f.ask(t, "", "Summarize my release plan")
	result := f.lastToolResult(t)
	if !strings.Contains(result, "note_not_found_in_this_workspace") || strings.Contains(result, readerNoteTail) || len(replySources(t, reply["workspace_context"])) != 0 {
		t.Fatalf("a note that moved to another workspace was read: %s", result)
	}
}

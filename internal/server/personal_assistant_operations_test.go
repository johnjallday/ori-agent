package server

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/assistantops"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// operationHostFixture is the real host: production routes, the SQLite
// operation store, canonical conversations and the real relationship. Only the
// model's replies are scripted.
type operationHostFixture struct {
	*workspaceReaderFixture
}

func newOperationHostFixture(t *testing.T) *operationHostFixture {
	t.Helper()
	f := &operationHostFixture{workspaceReaderFixture: newWorkspaceReaderFixture(t)}
	if err := f.builder.workspaceStore.Update(f.project.ID, func(ws *workspace.Workspace) error { return ws.AddAgent("Mixer") }); err != nil {
		t.Fatal(err)
	}
	return f
}

// operate sends one turn from tab-a, which chose to watch.
func (f *operationHostFixture) operate(t *testing.T, conversationID, prompt string) map[string]any {
	t.Helper()
	conversation := map[string]string{}
	if conversationID != "" {
		conversation["id"] = conversationID
	}
	status, reply := f.call(t, http.MethodPost, "/api/home-assistant/ask", map[string]any{
		"prompt": prompt, "intent": "assistant_conversation", "context": placementContext(f.project),
		"conversation": conversation, "operation": map[string]any{"tab_id": "tab-a", "watch": true},
	})
	if status != http.StatusOK {
		t.Fatalf("ask: %d %v", status, reply)
	}
	return reply
}

func (f *operationHostFixture) rows(t *testing.T, where string, args ...any) int {
	t.Helper()
	var count int
	if err := f.builder.sessionStore.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM assistant_operations `+where, args...).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

// assertOperatingTurnAuthority is the panel's closed tool list plus exactly the
// named operations, and only those. The ordinary guard stays strict for every
// other turn: an operation tool on a turn that did not ask for one fails it.
func assertOperatingTurnAuthority(t *testing.T, request llm.ChatRequest) {
	t.Helper()
	operations := 0
	ordinary := request
	ordinary.Tools = nil
	for _, tool := range request.Tools {
		if assistantops.IsTool(tool.Name) {
			operations++
			continue
		}
		ordinary.Tools = append(ordinary.Tools, tool)
	}
	if operations != len(assistantops.ToolNames()) {
		t.Fatalf("an operating turn offers each named operation once, got %d", operations)
	}
	assertNoPanelExecutionAuthority(t, ordinary)
}

func operationOf(t *testing.T, reply map[string]any) map[string]any {
	t.Helper()
	op, _ := reply["operation"].(map[string]any)
	if op == nil {
		t.Fatalf("no operation in %v", reply)
	}
	return op
}

func commandsOf(op map[string]any) []map[string]any {
	raw, _ := op["commands"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		out = append(out, item.(map[string]any))
	}
	return out
}

// Through the real routes and stores: the router keeps the request in the
// conversation, a new conversation's first turn records the operation only
// once it is saved, one tab is handed the commands, acknowledgements become
// view facts, and nothing in the workspace is written.
func TestAssistantOperations_RealHostOpenAndSelectForOneTab(t *testing.T) {
	f := newOperationHostFixture(t)
	const prompt = "Open the Album-1 reader fixture workspace and select Mixer"

	status, route := f.call(t, http.MethodPost, "/api/home-assistant/route", map[string]any{"prompt": prompt, "context": placementContext(f.project)})
	if status != http.StatusOK || route["intent"] != "assistant_conversation" || route["route_mode"] != "home_inline" {
		t.Fatalf("the request must stay with the hired assistant: %d %v", status, route)
	}
	if status, capability := f.call(t, http.MethodGet, "/api/home-assistant/operations/capability", nil); status != http.StatusOK || capability["available"] != true {
		t.Fatalf("capability: %d %v", status, capability)
	}
	// The drawer learns the same thing from the status it already reads.
	status, state := f.call(t, http.MethodGet, "/api/personal-assistant", nil)
	relationship, _ := state["personal_assistant"].(map[string]any)
	availability, _ := relationship["availability"].(map[string]any)
	if operations, _ := availability["operations"].(map[string]any); status != http.StatusOK || operations["available"] != true || operations["status"] != "available" {
		t.Fatalf("relationship status must report operations: %d %v", status, availability)
	}

	before, _ := f.builder.workspaceStore.Get(f.project.ID)
	f.provider.script = []llm.ChatResponse{
		readerCall("assistant_operate_open", map[string]any{"workspace_id": f.project.ID, "agent_name": "Mixer"}),
		{Content: "I asked Ori to open the workspace and select Mixer.", Model: "sonnet", Provider: "claude_code"},
	}
	reply := f.operate(t, "", prompt)
	for _, request := range f.provider.requests {
		assertOperatingTurnAuthority(t, request)
	}
	conversation := reply["conversation"].(map[string]any)
	op := operationOf(t, reply)
	if conversation["stored"] != true || conversation["started"] != true || op["status"] != "requested" || op["watching"] != true {
		t.Fatalf("the first turn of a new conversation records its operation once saved: %v", reply)
	}
	id := op["id"].(string)
	if f.rows(t, `WHERE id=? AND conversation_id=? AND turn_message_id=? AND tab_id='tab-a' AND user_id='local'`,
		id, conversation["id"], conversation["assistant_message_id"]) != 1 {
		t.Fatal("the stored operation is not bound to the saved turn")
	}
	commands := commandsOf(op)
	if len(commands) != 2 || commands[0]["action"] != "open_workspace" || commands[0]["workspace_slug"] != f.project.FolderSlug ||
		commands[1]["action"] != "select_agent" || commands[1]["agent_name"] != "Mixer" || commands[1]["agent_node_id"] == "" {
		t.Fatalf("commands are named actions with host-resolved references: %v", commands)
	}

	base := "/api/home-assistant/operations/" + id
	// Another tab reads the facts and is handed nothing to do.
	status, other := f.call(t, http.MethodGet, base+"?tab=tab-b", nil)
	if seen := operationOf(t, other); status != http.StatusOK || seen["watching"] != false || len(commandsOf(seen)) != 0 || seen["pending"] != float64(2) {
		t.Fatalf("second tab: %d %v", status, other)
	}
	// After a full-page navigation the arriving page reads, then reports.
	status, arrived := f.call(t, http.MethodGet, base+"?tab=tab-a", nil)
	revision := operationOf(t, arrived)["revision"]
	if status != http.StatusOK || len(commandsOf(operationOf(t, arrived))) != 2 {
		t.Fatalf("the watching tab gets its commands after a reload: %d %v", status, arrived)
	}
	status, opened := f.call(t, http.MethodPost, base+"/ack", map[string]any{
		"command_id": commands[0]["id"], "revision": revision, "tab_id": "tab-a", "outcome": "acknowledged",
		"observed": map[string]any{"path": "/workspaces/" + f.project.FolderSlug}})
	if status != http.StatusOK || operationOf(t, opened)["pending"] != float64(1) {
		t.Fatalf("arrival: %d %v", status, opened)
	}
	status, selected := f.call(t, http.MethodPost, base+"/ack", map[string]any{
		"command_id": commands[1]["id"], "revision": operationOf(t, opened)["revision"], "tab_id": "tab-a", "outcome": "acknowledged",
		"observed": map[string]any{"agent_key": "mixer"}})
	done := operationOf(t, selected)
	receipts, _ := done["receipts"].([]any)
	if status != http.StatusOK || done["pending"] != float64(0) || len(receipts) != 2 ||
		receipts[0].(map[string]any)["kind"] != "view_opened" || receipts[1].(map[string]any)["kind"] != "agent_selected" {
		t.Fatalf("acknowledgements become distinct view facts: %d %v", status, selected)
	}

	// Opening and selecting wrote nothing to the workspace and ran nothing.
	after, _ := f.builder.workspaceStore.Get(f.project.ID)
	if len(after.Tasks) != len(before.Tasks) || len(after.AgentInstances) != len(before.AgentInstances) {
		t.Fatal("an operation changed the workspace")
	}
	var runs int
	if err := f.builder.sessionStore.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM workspace_runs`).Scan(&runs); err != nil || runs != 0 {
		t.Fatal("an operation started a run", runs, err)
	}
	// The saved reply is the assistant's own words; reloading shows no command.
	saved := reloadedConversation(t, f.draftServerFixture, conversation["id"].(string))
	text := mustJSON(t, saved)
	for _, private := range []string{id, "assistant_operate_open", commands[0]["id"].(string)} {
		if strings.Contains(text, private) {
			t.Fatalf("operation identity %q was stored in the transcript", private)
		}
	}
}

// Canonical lifecycle changes end an operation's continuation: the
// relationship changing under it, and its conversation being deleted.
func TestAssistantOperations_RealHostLifecycleEndsContinuation(t *testing.T) {
	prepare := func(t *testing.T) (*operationHostFixture, string, map[string]any) {
		f := newOperationHostFixture(t)
		f.provider.script = []llm.ChatResponse{
			readerCall("assistant_operate_open", map[string]any{"workspace_id": f.project.ID}),
			{Content: "I asked Ori to open the workspace.", Model: "sonnet", Provider: "claude_code"},
		}
		reply := f.operate(t, "", "Open the Album-1 reader fixture workspace")
		return f, reply["conversation"].(map[string]any)["id"].(string), operationOf(t, reply)
	}
	ack := func(f *operationHostFixture, op map[string]any) (int, map[string]any) {
		return f.call(t, http.MethodPost, "/api/home-assistant/operations/"+op["id"].(string)+"/ack", map[string]any{
			"command_id": commandsOf(op)[0]["id"], "revision": op["revision"], "tab_id": "tab-a", "outcome": "acknowledged",
			"observed": map[string]any{"path": "/workspaces/" + f.project.FolderSlug}})
	}

	t.Run("relationship changed", func(t *testing.T) {
		f, _, op := prepare(t)
		version, _ := f.memoryFacts(t)
		if status, paused := f.call(t, http.MethodPost, "/api/personal-assistant/pause", map[string]any{"if_version": version}); status != http.StatusOK {
			t.Fatalf("pause: %d %v", status, paused)
		}
		if status, refused := ack(f, op); status != http.StatusGone || refused["code"] != "operation_invalidated" {
			t.Fatalf("an operation prepared under the earlier relationship must end: %d %v", status, refused)
		}
		status, read := f.call(t, http.MethodGet, "/api/home-assistant/operations/"+op["id"].(string)+"?tab=tab-a", nil)
		if ended := operationOf(t, read); status != http.StatusOK || ended["status"] != "invalidated" || ended["pending"] != float64(0) {
			t.Fatalf("the ended operation stays readable and hands out nothing: %d %v", status, read)
		}
	})

	t.Run("conversation deleted", func(t *testing.T) {
		f, conversationID, op := prepare(t)
		if err := f.builder.sessionStore.DeleteSession(context.Background(), conversationID); err != nil {
			t.Fatal(err)
		}
		if status, refused := ack(f, op); status != http.StatusNotFound || refused["code"] != "operation_not_found" {
			t.Fatalf("an operation does not outlive its conversation: %d %v", status, refused)
		}
		if f.rows(t, `WHERE id=?`, op["id"]) != 0 {
			t.Fatal("deleting the conversation left its operation behind")
		}
	})

	t.Run("workspace trashed", func(t *testing.T) {
		f, _, op := prepare(t)
		if err := f.builder.workspaceStore.Update(f.project.ID, func(ws *workspace.Workspace) error {
			ws.Status = workspace.StatusTrashed
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if status, refused := ack(f, op); status != http.StatusGone || refused["code"] != "operation_invalidated" {
			t.Fatalf("losing the workspace ends the operation: %d %v", status, refused)
		}
	})
}

// A turn whose own words do not ask for it gets no operation, whatever the
// model tries and whatever the history says.
func TestAssistantOperations_RealHostDiscussionRecordsNothing(t *testing.T) {
	f := newOperationHostFixture(t)
	f.provider.script = []llm.ChatResponse{{Content: "Happy to help.", Model: "sonnet", Provider: "claude_code"}}
	first := f.operate(t, "", "What do you think about the Album-1 reader fixture workspace? Should I open it?")
	if first["operation"] != nil {
		t.Fatalf("discussion produced an operation: %v", first)
	}
	id := first["conversation"].(map[string]any)["id"].(string)
	before := len(f.provider.requests)
	f.provider.script = append(make([]llm.ChatResponse, before),
		readerCall("assistant_operate_open", map[string]any{"workspace_id": f.project.ID}),
		llm.ChatResponse{Content: "Understood.", Model: "sonnet", Provider: "claude_code"})
	reply := f.operate(t, id, "yes")
	for _, tool := range f.provider.requests[before].Tools {
		if tool.Name == "assistant_operate_open" {
			t.Fatal("a bare yes was offered the operation tool")
		}
	}
	if reply["operation"] != nil || f.rows(t, ``) != 0 {
		t.Fatalf("history cannot authorize an operation: %v", reply)
	}
}

package server

import (
	"net/http"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func (f *operationHostFixture) prepareScript(agent, title string) {
	f.provider.script = append(make([]llm.ChatResponse, len(f.provider.requests)),
		readerCall("assistant_operate_prepare_task", map[string]any{
			"workspace_id": f.project.ID, "agent_name": agent, "title": title, "brief": "As the user asked."}),
		llm.ChatResponse{Content: "A draft is ready to review. Nothing is created until you save it.", Model: "sonnet", Provider: "claude_code"})
}

func (f *operationHostFixture) taskCount(t *testing.T) int {
	t.Helper()
	ws, err := f.builder.workspaceStore.Get(f.project.ID)
	if err != nil {
		t.Fatal(err)
	}
	return len(ws.Tasks)
}

// Through the real routes and stores: preparing a task draft, showing it, and
// preparing another are all free of task and run writes; the draft is bound to
// the roster's own instance; and one request has one owner.
func TestAssistantOperations_RealHostPrepareTaskWritesNoTaskOrRun(t *testing.T) {
	f := newOperationHostFixture(t)
	if err := f.builder.workspaceStore.Update(f.project.ID, func(ws *workspace.Workspace) error { return ws.AddAgent("Engineer") }); err != nil {
		t.Fatal(err)
	}
	const prompt = "Create a task for Engineer to bounce the stems"
	tasksBefore := f.taskCount(t)

	// The router keeps the request with the hired assistant instead of handing
	// it off as an execution request.
	status, route := f.call(t, http.MethodPost, "/api/home-assistant/route", map[string]any{"prompt": prompt, "context": placementContext(f.project)})
	if status != http.StatusOK || route["intent"] != "assistant_conversation" || route["route_mode"] != "home_inline" {
		t.Fatalf("route: %d %v", status, route)
	}

	f.prepareScript("engineer", "Bounce the stems")
	reply := f.operate(t, "", prompt)
	for _, request := range f.provider.requests {
		assertOperatingTurnAuthority(t, request)
	}
	if reply["requires_confirmation"] == true || reply["confirmation"] != nil {
		t.Fatalf("the one-click create also fired: %v", reply["confirmation"])
	}
	op := operationOf(t, reply)
	draft, _ := op["draft"].(map[string]any)
	if op["kind"] != "task" || op["status"] != "prepared" || op["task"] != nil || draft == nil {
		t.Fatalf("a prepared draft is not a task: %v", op)
	}
	current, _ := f.builder.workspaceStore.Get(f.project.ID)
	var engineerNode string
	for _, instance := range current.AgentInstances {
		if instance.Name == "Engineer" {
			engineerNode = instance.NodeID
		}
	}
	if draft["title"] != "Bounce the stems" || draft["agent_name"] != "Engineer" || draft["agent_node_id"] != engineerNode || engineerNode == "" ||
		current.EntryAgentName() == "Engineer" {
		t.Fatalf("the draft must name the non-entry agent's own instance: %v (entry %q)", draft, current.EntryAgentName())
	}
	id := op["id"].(string)
	if f.rows(t, `WHERE id=? AND kind='task' AND status='prepared' AND task_id='' AND draft_json IS NOT NULL`, id) != 1 {
		t.Fatal("the stored operation must hold a draft and no task")
	}

	// The page reports arrival and the form by its revision: still no task.
	commands := commandsOf(op)
	base := "/api/home-assistant/operations/" + id
	status, opened := f.call(t, http.MethodPost, base+"/ack", map[string]any{
		"command_id": commands[0]["id"], "revision": op["revision"], "tab_id": "tab-a", "outcome": "acknowledged",
		"observed": map[string]any{"path": "/workspaces/" + f.project.FolderSlug}})
	if status != http.StatusOK {
		t.Fatalf("arrival: %d %v", status, opened)
	}
	// A form showing some other revision is not this draft.
	if status, refused := f.call(t, http.MethodPost, base+"/ack", map[string]any{
		"command_id": commands[1]["id"], "revision": operationOf(t, opened)["revision"], "tab_id": "tab-a", "outcome": "acknowledged",
		"observed": map[string]any{"draft_revision": 2}}); status != http.StatusBadRequest || refused["code"] != "operation_invalid" {
		t.Fatalf("wrong revision: %d %v", status, refused)
	}
	status, shown := f.call(t, http.MethodPost, base+"/ack", map[string]any{
		"command_id": commands[1]["id"], "revision": operationOf(t, opened)["revision"], "tab_id": "tab-a", "outcome": "acknowledged",
		"observed": map[string]any{"draft_revision": 1}})
	settled := operationOf(t, shown)
	if status != http.StatusOK || settled["status"] != "prepared" || settled["pending"] != float64(0) || settled["task"] != nil {
		t.Fatalf("showing the form changes no task state: %d %v", status, shown)
	}

	// A second draft from the same tab is its own operation; the first keeps
	// its content for a form the user may still have open.
	conversation := reply["conversation"].(map[string]any)["id"].(string)
	f.prepareScript("Mixer", "Mix the chorus")
	second := operationOf(t, f.operate(t, conversation, "Create a task for Mixer to mix the chorus"))
	if second["id"] == id || second["draft"].(map[string]any)["agent_name"] != "Mixer" {
		t.Fatalf("second draft: %v", second)
	}
	status, first := f.call(t, http.MethodGet, base+"?tab=tab-a", nil)
	if kept := operationOf(t, first); status != http.StatusOK || kept["status"] != "prepared" || kept["draft"].(map[string]any)["title"] != "Bounce the stems" {
		t.Fatalf("the earlier draft is still readable: %d %v", status, first)
	}

	// Dismissing a draft ends it and touches nothing in the workspace.
	status, cancelled := f.call(t, http.MethodPost, "/api/home-assistant/operations/"+second["id"].(string)+"/cancel",
		map[string]any{"tab_id": "tab-a", "revision": second["revision"]})
	if status != http.StatusOK || operationOf(t, cancelled)["status"] != "cancelled" {
		t.Fatalf("dismiss: %d %v", status, cancelled)
	}

	if after := f.taskCount(t); after != tasksBefore {
		t.Fatalf("preparing, showing and dismissing drafts changed the task count: %d -> %d", tasksBefore, after)
	}
	var runs int
	if err := f.builder.sessionStore.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM workspace_runs`).Scan(&runs); err != nil || runs != 0 {
		t.Fatal("a draft started a run", runs, err)
	}
}

// An agent the workspace does not have gets no draft and no stand-in.
func TestAssistantOperations_RealHostPrepareTaskNeverSubstitutesAnAgent(t *testing.T) {
	f := newOperationHostFixture(t)
	tasksBefore := f.taskCount(t)
	f.prepareScript("Editor", "Proofread the notes")
	reply := f.operate(t, "", "Create a task for Editor to proofread the notes")
	if reply["operation"] != nil || f.rows(t, ``) != 0 {
		t.Fatalf("an agent outside the roster produced a draft: %v", reply)
	}
	// The last model request carries what the tool answered. It is an operating
	// turn, so the strict panel allowlist plus exactly the operations applies.
	request := f.provider.requests[len(f.provider.requests)-1]
	assertOperatingTurnAuthority(t, request)
	result := request.Messages[len(request.Messages)-1].Content
	for _, want := range []string{`"status":"not_found"`, `"reason":"agent_not_in_workspace"`, "Mixer"} {
		if !strings.Contains(result, want) {
			t.Errorf("tool result lacks %s: %s", want, result)
		}
	}
	if after := f.taskCount(t); after != tasksBefore {
		t.Fatalf("task count changed: %d -> %d", tasksBefore, after)
	}
}

package agenthttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/johnjallday/ori-agent/internal/assistantops"
	"github.com/johnjallday/ori-agent/internal/llm"
)

// scriptedTaskHost is the task owner as the operation service sees it.
type scriptedTaskHost struct {
	mu       sync.Mutex
	tasks    map[string]assistantops.TaskState
	startErr error
	starts   int
}

func (h *scriptedTaskHost) Task(_ context.Context, _ string, taskID string) (assistantops.TaskState, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	state, ok := h.tasks[taskID]
	if !ok {
		return assistantops.TaskState{}, fmt.Errorf("%w: %s", assistantops.ErrTaskMissing, taskID)
	}
	return state, nil
}

func (h *scriptedTaskHost) Start(context.Context, string, string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.starts++
	return h.startErr
}

// reviewedOperation asks for a draft and carries its save through the service
// exactly as the task create handler does.
func (f *operationFixture) reviewedOperation(t *testing.T, host *scriptedTaskHost, taskID string) *assistantops.Snapshot {
	t.Helper()
	f.provider.script = append(make([]llm.ChatResponse, len(f.provider.requests)),
		toolCall(assistantops.ToolPrepareTask, map[string]any{"workspace_id": f.alpha.ID, "agent_name": "Mixer", "title": "Mix the chorus"}),
		llm.ChatResponse{Content: "A draft for Mixer is ready to review."})
	prepared := f.ask("Create a task for Mixer to mix the chorus", true).Operation
	if prepared == nil || prepared.Status != assistantops.StatusPrepared {
		t.Fatalf("fixture draft missing: %+v", prepared)
	}
	if taskID == "" {
		return prepared
	}
	claim, err := f.handler.Operations.ClaimTaskCreate(context.Background(), assistantops.TaskCreate{
		OperationID: prepared.ID, DraftRevision: prepared.Draft.Revision, WorkspaceID: f.alpha.ID,
		AgentName: prepared.Draft.AgentName, AgentNodeID: prepared.Draft.AgentNodeID, TaskID: taskID,
	})
	if err != nil || claim.Existing {
		t.Fatalf("claim: %+v %v", claim, err)
	}
	host.mu.Lock()
	host.tasks[taskID] = assistantops.TaskState{TaskID: taskID, OperationID: prepared.ID, AgentName: claim.AgentName,
		AgentNodeID: claim.AgentNodeID, Status: "pending", Startable: true}
	host.mu.Unlock()
	if err := f.handler.Operations.ConfirmTaskCreate(context.Background(), prepared.ID, taskID); err != nil {
		t.Fatal(err)
	}
	return prepared
}

func TestOperationStartEndpoint_ClaimsOnceAndARefusalCarriesTheOperation(t *testing.T) {
	f := newOperationFixture(t)
	host := &scriptedTaskHost{tasks: map[string]assistantops.TaskState{}}
	f.handler.Operations.SetTaskHost(host)
	server := f.server(t)

	// Starting is the user's own request to this endpoint. The model is offered
	// nothing that creates or starts a task.
	for _, tool := range assistantops.ToolNames() {
		for _, verb := range []string{"start", "run", "execute", "create", "save"} {
			if strings.Contains(tool, verb) {
				t.Fatalf("operation tool %q must not create or start anything", tool)
			}
		}
	}

	draft := f.reviewedOperation(t, host, "")
	status, payload := operationCall(t, server, http.MethodPost, "/api/home-assistant/operations/"+draft.ID+"/start", map[string]any{"tab_id": "tab-a"})
	if status != http.StatusConflict || errorCode(payload) != "operation_ended" || host.starts != 0 {
		t.Fatalf("a draft that was never saved must not start: %d %v", status, payload)
	}

	reviewed := f.reviewedOperation(t, host, "task-1")
	base := "/api/home-assistant/operations/" + reviewed.ID
	for i, tab := range []string{"tab-a", "tab-a", "tab-b", ""} {
		status, payload := operationCall(t, server, http.MethodPost, base+"/start", map[string]any{"tab_id": tab})
		if op := decodeOperation(t, payload); status != http.StatusOK || op.Status != assistantops.StatusStartRequested || op.Task == nil || op.Task.TaskID != "task-1" {
			t.Fatalf("start %d: %d %+v", i, status, op)
		}
	}
	if host.starts != 1 {
		t.Fatalf("Start was delivered %d times", host.starts)
	}
	if status, payload := operationCall(t, server, http.MethodPost, base+"/start", map[string]any{"tab_id": "not a tab!"}); status != http.StatusBadRequest || errorCode(payload) != "operation_invalid" {
		t.Fatalf("malformed tab: %d %v", status, payload)
	}

	// A refusal: its own bounded reason, and the operation as it now stands.
	refusable := f.reviewedOperation(t, host, "task-2")
	host.startErr = &assistantops.StartRefusal{Reason: "Ori is being reset, so nothing can start right now."}
	status, payload = operationCall(t, server, http.MethodPost, "/api/home-assistant/operations/"+refusable.ID+"/start", map[string]any{"tab_id": "tab-a"})
	var message string
	_ = json.Unmarshal(payload["message"], &message)
	if status != http.StatusConflict || errorCode(payload) != "operation_start_refused" || message != "Ori is being reset, so nothing can start right now." {
		t.Fatalf("refusal: %d %v", status, payload)
	}
	var details map[string]json.RawMessage
	if err := json.Unmarshal(payload["details"], &details); err != nil {
		t.Fatalf("a refusal must carry the operation: %v", payload)
	}
	if after := decodeOperation(t, details); after.Status != assistantops.StatusReviewed || after.Task == nil || after.Task.TaskID != "task-2" {
		t.Fatalf("refused operation: %+v", after)
	}

	// An internal error from the start owner is never shown.
	host.startErr = errors.New("open /Users/x/secret.db: permission denied")
	status, payload = operationCall(t, server, http.MethodPost, "/api/home-assistant/operations/"+refusable.ID+"/start", map[string]any{"tab_id": "tab-a"})
	_ = json.Unmarshal(payload["message"], &message)
	if status != http.StatusConflict || errorCode(payload) != "operation_start_refused" || strings.Contains(string(payload["message"])+string(payload["details"]), "secret") {
		t.Fatalf("internal error leaked: %d %v", status, payload)
	}

	// Without a task owner nothing can be started, and nothing is claimed.
	f.handler.Operations.SetTaskHost(nil)
	if status, payload := operationCall(t, server, http.MethodPost, "/api/home-assistant/operations/"+refusable.ID+"/start", map[string]any{"tab_id": "tab-a"}); status != http.StatusServiceUnavailable || errorCode(payload) != "operation_unavailable" {
		t.Fatalf("no task owner: %d %v", status, payload)
	}
}

func TestOperationErrorCode_TaskRefusalsHaveStableCategories(t *testing.T) {
	cases := []struct {
		err     error
		status  int
		code    string
		message string
	}{
		{&assistantops.Refusal{Kind: assistantops.ErrStartRefused, Reason: "The task is in the backlog."}, http.StatusConflict, "operation_start_refused", "The task is in the backlog."},
		{&assistantops.Refusal{Kind: assistantops.ErrAssignee, Reason: "Mixer is not in this workspace any more."}, http.StatusConflict, "operation_assignee", "Mixer is not in this workspace any more."},
		{fmt.Errorf("%w: internal", assistantops.ErrStartRefused), http.StatusConflict, "operation_start_refused", "Ori could not start the task right now."},
		{fmt.Errorf("%w: Ghost", assistantops.ErrAssignee), http.StatusConflict, "operation_assignee", "The task's agent is not in this workspace. Reassign the task, then start it."},
		{fmt.Errorf("%w: task-1", assistantops.ErrTaskMissing), http.StatusGone, "operation_invalidated", "The task no longer exists."},
	}
	for _, tc := range cases {
		status, code, message := operationErrorCode(tc.err)
		if status != tc.status || code != tc.code || message != tc.message {
			t.Errorf("%v: got %d %s %q", tc.err, status, code, message)
		}
	}
}

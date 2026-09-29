package orchestrationhttp_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agentcomm"
	"github.com/johnjallday/ori-agent/internal/orchestrationhttp"
	"github.com/johnjallday/ori-agent/internal/resetstate"
	"github.com/johnjallday/ori-agent/internal/testutil/resetfixture"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type continuityTaskStore struct {
	workspace.Store
	manual bool
}

func (s *continuityTaskStore) CheckWorkspaceExecution(_ context.Context, _ string, automatic bool) error {
	if s.manual && !automatic {
		return nil
	}
	return workspace.ErrWorkspaceExecutionInactive
}

func TestContinuityAdmissionSeparatesManualTaskFromFirstOpen(t *testing.T) {
	fixture := resetfixture.New(t)
	base, err := workspace.NewFileStore(fixture.Paths().Workspaces)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = base.Close() })
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "continuity admission", Agents: []string{"fixture-agent"}})
	if err := ws.AddTask(workspace.Task{ID: "task", WorkspaceID: ws.ID, To: "fixture-agent", Description: "Calculate six times seven", Status: workspace.TaskStatusCompleted}); err != nil {
		t.Fatal(err)
	}
	if err := base.Save(ws); err != nil {
		t.Fatal(err)
	}
	source := &continuityTaskStore{Store: base}
	provider := &resetTaskProvider{started: make(chan context.Context, 1), release: make(chan struct{})}
	gate := &resetstate.WorkGate{}
	handler := orchestrationhttp.NewTaskHandler(source, agentcomm.NewCommunicator(source), provider, nil)
	handler.SetAdmissionGate(gate)
	finish := sync.OnceFunc(func() { close(provider.release) })
	t.Cleanup(func() { finish(); resetTaskIdle(t, gate) })
	request := func() *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		handler.ExecuteTaskHandler(response, httptest.NewRequest(http.MethodPost, "/execute", strings.NewReader(`{"task_id":"task"}`)))
		return response
	}
	if response := request(); response.Code != http.StatusConflict {
		t.Fatal(response.Code, response.Body.String())
	}
	got, err := base.Get(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	task, err := got.GetTask("task")
	if err != nil || task.Status != workspace.TaskStatusCompleted {
		t.Fatal("unadmitted manual request reset completed work", err)
	}
	select {
	case <-provider.started:
		t.Fatal("unadmitted request reached a provider")
	default:
	}
	// Imported-inactive policy admits explicit manual requests, not first-open
	// work that merely uses the same task execution implementation.
	source.manual = true
	if err := handler.StartTaskAsync(ws.ID, "task"); !errors.Is(err, workspace.ErrWorkspaceExecutionInactive) {
		t.Fatal("first-open work gained manual authority", err)
	}
	if response := request(); response.Code != http.StatusAccepted {
		t.Fatal(response.Code, response.Body.String())
	}
	resetTaskWait(t, provider.started)
	finish()
	resetTaskIdle(t, gate)
}

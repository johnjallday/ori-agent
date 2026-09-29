package triggerhttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/johnjallday/ori-agent/internal/trigger"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type inactiveTriggerSource struct {
	fakeWSSource
	inactive atomic.Bool
}

func (s *inactiveTriggerSource) CheckWorkspaceExecution(_ context.Context, _ string, automatic bool) error {
	if automatic && s.inactive.Load() {
		return workspace.ErrWorkspaceExecutionInactive
	}
	return nil
}

func TestContinuityTriggerHTTPRefusesInactiveTokenAndTestFire(t *testing.T) {
	source := &inactiveTriggerSource{fakeWSSource: fakeWSSource{folder: t.TempDir()}}
	ws := &workspace.Workspace{ID: "ws1"}
	service, err := trigger.NewService(trigger.ServiceConfig{Source: source, WorkspaceStore: &fakeWSStore{ws: ws}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Close)
	tr := createWebhook(t, service, "synthetic-secret")
	source.inactive.Store(true)
	handler := NewHandler(service)
	if response := postHook(handler, tr.Webhook.Token, "application/json", "synthetic-secret", "{}"); response.Code != http.StatusNotFound {
		t.Fatal("inactive known token exposed an endpoint", response.Code)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/ws1/triggers/"+tr.ID+"/test-fire", nil)
	req.SetPathValue("workspaceID", "ws1")
	req.SetPathValue("triggerID", tr.ID)
	response := httptest.NewRecorder()
	handler.TestFire(response, req)
	if response.Code != http.StatusConflict {
		t.Fatal("test-fire did not explain missing local activation", response.Code, response.Body.String())
	}
	got, err := service.Get("ws1", tr.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ws.Tasks) != 0 || got.FireCount != 0 || got.PendingFire != nil || len(got.FireHistory) != 0 {
		t.Fatal("denied HTTP trigger produced work or history")
	}
}

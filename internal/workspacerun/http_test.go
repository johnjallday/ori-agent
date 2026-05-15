package workspacerun

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandlerCreateAndListRuns(t *testing.T) {
	store := NewMemoryStore()
	profiles := NewProfileRegistry()
	executors := NewExecutorRegistry()
	executors.Register(ExecutorKindOriAgent, NewOriAgentExecutor())
	service := NewService(store, profiles, executors, NewLocalEnvironmentManager(t.TempDir()), NewValidator(), nil)
	handler := NewHandler(store, service)

	body := []byte(`{
		"profile_id": "general",
		"executor": {"kind": "ori_agent", "ref": "Researcher"},
		"prompt": "do a general task",
		"policy": {"approval": "none"}
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/workspace-1/runs", bytes.NewReader(body))
	req.SetPathValue("workspaceID", "workspace-1")
	w := httptest.NewRecorder()
	handler.CreateRun(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("CreateRun status = %d, body %s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/workspaces/workspace-1/runs", nil)
	req.SetPathValue("workspaceID", "workspace-1")
	w = httptest.NewRecorder()
	handler.ListRuns(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ListRuns status = %d, body %s", w.Code, w.Body.String())
	}
	var resp struct {
		Runs []Run `json:"runs"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Runs) != 1 || resp.Runs[0].WorkspaceID != "workspace-1" {
		t.Fatalf("runs = %+v, want one workspace run", resp.Runs)
	}
}

func TestHandlerTracePollingCapsPage(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	if err := store.CreateRun(ctx, &Run{ID: "run-1", WorkspaceID: "workspace-1"}); err != nil {
		t.Fatalf("create run: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := store.AppendTrace(ctx, "workspace-1", "run-1", NewTraceEvent("run-1", TraceMessage)); err != nil {
			t.Fatalf("append trace: %v", err)
		}
	}
	handler := NewHandler(store, NewService(store, nil, nil, nil, nil, nil))

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/workspace-1/runs/run-1/trace?since=0&limit=2", nil)
	req.SetPathValue("workspaceID", "workspace-1")
	req.SetPathValue("runID", "run-1")
	w := httptest.NewRecorder()
	handler.ListTrace(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("ListTrace status = %d, body %s", w.Code, w.Body.String())
	}
	var page TracePage
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode page: %v", err)
	}
	if len(page.Events) != 2 || page.NextSince != 2 || !page.HasMore {
		t.Fatalf("page = %+v, want capped page", page)
	}
}

func TestHandlerCreateRejectsInvalidProfileAndUnsupportedExecutor(t *testing.T) {
	store := NewMemoryStore()
	executors := NewExecutorRegistry()
	executors.Register(ExecutorKindOriAgent, NewOriAgentExecutor())
	handler := NewHandler(store, NewService(store, NewProfileRegistry(), executors, NewLocalEnvironmentManager(t.TempDir()), NewValidator(), nil))

	tests := []struct {
		name string
		body string
	}{
		{
			name: "invalid profile",
			body: `{"profile_id":"missing","executor":{"kind":"ori_agent","ref":"agent"},"prompt":"do work"}`,
		},
		{
			name: "unsupported executor",
			body: `{"profile_id":"general","executor":{"kind":"system_tool","ref":"stub"},"prompt":"do work"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/workspaces/workspace-1/runs", bytes.NewReader([]byte(tt.body)))
			req.SetPathValue("workspaceID", "workspace-1")
			w := httptest.NewRecorder()
			handler.CreateRun(w, req)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("CreateRun status = %d, body %s, want bad request", w.Code, w.Body.String())
			}
		})
	}
}

func TestHandlerRunCommandsAndArtifacts(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	executors := NewExecutorRegistry()
	executors.Register(ExecutorKindSystemTool, &stubLifecycleExecutor{})
	service := NewService(store, NewProfileRegistry(), executors, &stubEnvironmentManager{}, NewValidator(), nil)
	handler := NewHandler(store, service)

	run, err := service.CreateRun(ctx, "workspace-1", CreateRunRequest{
		ProfileID: ProfileGeneral,
		Executor:  Executor{Kind: ExecutorKindSystemTool, Ref: "stub"},
		Prompt:    "do work",
		Policy:    Policy{Approval: PolicyApprovalFinalOnly},
		ValidationRequest: &ValidationRequest{
			Profile: ValidationProfileNone,
		},
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	if err := service.ExecuteRun(ctx, "workspace-1", run.ID); err != nil {
		t.Fatalf("execute run: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/workspace-1/runs/"+run.ID, nil)
	req.SetPathValue("workspaceID", "workspace-1")
	req.SetPathValue("runID", run.ID)
	w := httptest.NewRecorder()
	handler.GetRun(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GetRun status = %d, body %s", w.Code, w.Body.String())
	}
	var got Run
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode run: %v", err)
	}
	if got.Status != RunStatusAwaitingApproval || len(got.TraceTail) == 0 {
		t.Fatalf("run = %+v, want awaiting approval with trace tail", got)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/workspaces/workspace-1/runs/"+run.ID+"/artifacts", nil)
	req.SetPathValue("workspaceID", "workspace-1")
	req.SetPathValue("runID", run.ID)
	w = httptest.NewRecorder()
	handler.ListArtifacts(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ListArtifacts status = %d, body %s", w.Code, w.Body.String())
	}
	var artifactsResp struct {
		Artifacts []Artifact `json:"artifacts"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &artifactsResp); err != nil {
		t.Fatalf("decode artifacts: %v", err)
	}
	if !hasArtifactKind(artifactsResp.Artifacts, ArtifactLog) {
		t.Fatalf("artifacts = %+v, want log artifact", artifactsResp.Artifacts)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/workspaces/workspace-1/runs/"+run.ID+"/approve", nil)
	req.SetPathValue("workspaceID", "workspace-1")
	req.SetPathValue("runID", run.ID)
	w = httptest.NewRecorder()
	handler.ApproveRun(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ApproveRun status = %d, body %s", w.Code, w.Body.String())
	}

	rejectRun, err := service.CreateRun(ctx, "workspace-1", CreateRunRequest{
		ProfileID: ProfileGeneral,
		Executor:  Executor{Kind: ExecutorKindSystemTool, Ref: "stub"},
		Prompt:    "do work",
		Policy:    Policy{Approval: PolicyApprovalFinalOnly},
		ValidationRequest: &ValidationRequest{
			Profile: ValidationProfileNone,
		},
	})
	if err != nil {
		t.Fatalf("create reject run: %v", err)
	}
	if err := service.ExecuteRun(ctx, "workspace-1", rejectRun.ID); err != nil {
		t.Fatalf("execute reject run: %v", err)
	}
	req = httptest.NewRequest(http.MethodPost, "/api/workspaces/workspace-1/runs/"+rejectRun.ID+"/reject", bytes.NewReader([]byte(`{"reason":"needs changes"}`)))
	req.SetPathValue("workspaceID", "workspace-1")
	req.SetPathValue("runID", rejectRun.ID)
	w = httptest.NewRecorder()
	handler.RejectRun(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("RejectRun status = %d, body %s", w.Code, w.Body.String())
	}
	gotReject, err := store.GetRun(ctx, "workspace-1", rejectRun.ID)
	if err != nil {
		t.Fatalf("get rejected run: %v", err)
	}
	if gotReject.Status != RunStatusRejected || gotReject.Error != "needs changes" {
		t.Fatalf("rejected run = %+v, want rejected with reason", gotReject)
	}

	stopRun, err := service.CreateRun(ctx, "workspace-1", CreateRunRequest{
		ProfileID: ProfileGeneral,
		Executor:  Executor{Kind: ExecutorKindSystemTool, Ref: "stub"},
		Prompt:    "do work",
	})
	if err != nil {
		t.Fatalf("create stop run: %v", err)
	}
	req = httptest.NewRequest(http.MethodPost, "/api/workspaces/workspace-1/runs/"+stopRun.ID+"/stop", nil)
	req.SetPathValue("workspaceID", "workspace-1")
	req.SetPathValue("runID", stopRun.ID)
	w = httptest.NewRecorder()
	handler.StopRun(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("StopRun status = %d, body %s", w.Code, w.Body.String())
	}
	gotStop, err := store.GetRun(ctx, "workspace-1", stopRun.ID)
	if err != nil {
		t.Fatalf("get stopped run: %v", err)
	}
	if gotStop.Status != RunStatusCancelled {
		t.Fatalf("stopped status = %q, want cancelled", gotStop.Status)
	}
}

func TestHandlerMissingIDsAndTraceValidation(t *testing.T) {
	store := NewMemoryStore()
	handler := NewHandler(store, NewService(store, nil, nil, nil, nil, nil))

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces//runs", nil)
	w := httptest.NewRecorder()
	handler.ListRuns(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("ListRuns missing workspace status = %d, want bad request", w.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/workspaces/workspace-1/runs/", nil)
	req.SetPathValue("workspaceID", "workspace-1")
	w = httptest.NewRecorder()
	handler.GetRun(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("GetRun missing run status = %d, want bad request", w.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/workspaces/workspace-1/runs/missing", nil)
	req.SetPathValue("workspaceID", "workspace-1")
	req.SetPathValue("runID", "missing")
	w = httptest.NewRecorder()
	handler.GetRun(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("GetRun missing run status = %d, body %s, want not found", w.Code, w.Body.String())
	}

	if err := store.CreateRun(context.Background(), &Run{ID: "run-1", WorkspaceID: "workspace-1"}); err != nil {
		t.Fatalf("create run: %v", err)
	}
	req = httptest.NewRequest(http.MethodGet, "/api/workspaces/workspace-1/runs/run-1/trace?since=abc", nil)
	req.SetPathValue("workspaceID", "workspace-1")
	req.SetPathValue("runID", "run-1")
	w = httptest.NewRecorder()
	handler.ListTrace(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("ListTrace invalid since status = %d, want bad request", w.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/workspaces/workspace-1/runs/run-1/trace?limit=abc", nil)
	req.SetPathValue("workspaceID", "workspace-1")
	req.SetPathValue("runID", "run-1")
	w = httptest.NewRecorder()
	handler.ListTrace(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("ListTrace invalid limit status = %d, want bad request", w.Code)
	}
}

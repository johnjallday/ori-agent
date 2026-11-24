package chathttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/agentcomm"
	"github.com/johnjallday/ori-agent/internal/orchestration"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// spyPlanOpener records how often chat tries to open a Plan and can refuse, the
// way a disabled planning policy or an unavailable store does.
type spyPlanOpener struct {
	mu    sync.Mutex
	calls int
	id    string
	err   error
}

func (s *spyPlanOpener) OpenPlan(_ context.Context, _, _, _ string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return s.id, s.err
}

func (s *spyPlanOpener) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// planRouteFixture is a chat handler whose planner has no model, so it falls
// back to heuristics and proposes at least one task for every request. With
// plan_before_action on, that is the first-party path that used to open a Plan.
type planRouteFixture struct {
	handler    *Handler
	workspaces workspace.Store
}

func newPlanRouteFixture(t *testing.T) *planRouteFixture {
	t.Helper()

	st := newPreflightStore("Ori", &agent.Agent{
		Settings: types.Settings{Model: "gpt-5-nano", Temperature: 0.3},
	})
	wsStore, err := workspace.NewFileStore(filepath.Join(t.TempDir(), "workspaces"))
	if err != nil {
		t.Fatalf("workspace store: %v", err)
	}

	h := NewHandler(st, nil)
	h.SetWorkspaceStore(wsStore)
	h.SetOrchestrator(orchestration.NewOrchestrator(
		st, wsStore, nil, agentcomm.NewCommunicator(wsStore), nil, nil, nil))
	return &planRouteFixture{handler: h, workspaces: wsStore}
}

func (f *planRouteFixture) post(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/chat", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	f.handler.ChatHandler(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp
}

func (f *planRouteFixture) workspaceCount(t *testing.T) int {
	t.Helper()
	ids, err := f.workspaces.List()
	if err != nil {
		t.Fatalf("list workspaces: %v", err)
	}
	return len(ids)
}

// Plan-required chat work must end in a terminal reply. These invariants hold
// whatever the reply says about where to go next: it is not an approval, not an
// executed action, not a queued agent, and it leaves no workspace behind. They
// are what stops a retired Plan route from falling through into execution.
func assertTerminalPlanReply(t *testing.T, f *planRouteFixture, resp map[string]any, before int) {
	t.Helper()

	if got, _ := resp["requires_plan"].(bool); !got {
		t.Fatalf("plan-required work must say so, got requires_plan=%v in %v", resp["requires_plan"], resp)
	}
	if got, _ := resp["requires_approval"].(bool); got {
		t.Fatalf("plan-required work must not carry an inline approval: %v", resp)
	}
	for _, key := range []string{
		"action_plan_id", "action_preview", "action_preview_id", "approval_type",
		"orchestrated", "dynamic_agent_requests", "pending_plan_id", "planner_plan",
	} {
		if _, present := resp[key]; present {
			t.Fatalf("plan-required work must not carry %q: %v", key, resp)
		}
	}
	if text, _ := resp["response"].(string); text == "" {
		t.Fatalf("a terminal reply must explain itself: %v", resp)
	}
	if after := f.workspaceCount(t); after != before {
		t.Fatalf("plan-required chat created %d workspace(s)", after-before)
	}
}

func TestPlanBeforeAction_PlanWorkEndsTerminalWhenAPlanOpens(t *testing.T) {
	f := newPlanRouteFixture(t)
	f.handler.SetPlanOpener(&spyPlanOpener{id: "plan_test"})
	before := f.workspaceCount(t)

	resp := f.post(t, map[string]any{
		"question":           "research and analyze the quarterly report",
		"plan_before_action": true,
	})
	assertTerminalPlanReply(t, f, resp, before)
}

func TestPlanBeforeAction_PlanWorkEndsTerminalWhenOpeningFails(t *testing.T) {
	f := newPlanRouteFixture(t)
	opener := &spyPlanOpener{err: errors.New("planning is not enabled for this workspace")}
	f.handler.SetPlanOpener(opener)
	before := f.workspaceCount(t)

	resp := f.post(t, map[string]any{
		"question":           "research and analyze the quarterly report",
		"plan_before_action": true,
		// A workspace in the route context is what lets chat try to open a Plan.
		"workspace_id": "ws_without_a_store_entry",
	})
	assertTerminalPlanReply(t, f, resp, before)
}

func TestPlanBeforeAction_PlanWorkEndsTerminalWithNoOpenerWired(t *testing.T) {
	f := newPlanRouteFixture(t)
	before := f.workspaceCount(t)

	resp := f.post(t, map[string]any{
		"question":           "research and analyze the quarterly report",
		"plan_before_action": true,
	})
	assertTerminalPlanReply(t, f, resp, before)
}

// Every message classifies as plan work while the heuristic planner proposes a
// task for it. Pinned so that copy for this route has to read sensibly for any
// message, not only for obvious multi-step ones.
func TestPlanBeforeAction_HeuristicPlannerClassifiesOrdinaryMessagesAsPlanWork(t *testing.T) {
	f := newPlanRouteFixture(t)
	before := f.workspaceCount(t)

	resp := f.post(t, map[string]any{
		"question":           "hello there",
		"plan_before_action": true,
	})
	assertTerminalPlanReply(t, f, resp, before)
}

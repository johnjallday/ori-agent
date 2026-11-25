package chathttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/agentcomm"
	"github.com/johnjallday/ori-agent/internal/orchestration"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// planRouteFixture is a chat handler whose planner has no model, so it falls
// back to heuristics and proposes at least one task for every request. With
// plan_before_action on, that is the first-party path that used to open a Plan.
// It now ends in advice that starts nothing, and these tests hold that line:
// whatever the reply says, it is not an approval, not an executed action, not a
// queued agent, not a ticket, and it leaves no workspace behind.
type planRouteFixture struct {
	handler    *Handler
	workspaces workspace.Store
	agents     *preflightStore
	// workspaceID is a real workspace in the route context, the case in which
	// chat used to be able to open a Plan.
	workspaceID string
}

func newPlanRouteFixture(t *testing.T) *planRouteFixture {
	t.Helper()

	st := newPreflightStore("Atlas", &agent.Agent{
		Settings: types.Settings{Model: "gpt-5-nano", Temperature: 0.3},
	})
	wsStore, err := workspace.NewFileStore(filepath.Join(t.TempDir(), "workspaces"))
	if err != nil {
		t.Fatalf("workspace store: %v", err)
	}
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{
		Name:   "Chat Route Workspace",
		Agents: []string{"Atlas"},
	})
	if err := workspace.CreateNativeWorkspace(context.Background(), wsStore, ws); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	h := NewHandler(st, nil)
	h.SetPersonalAssistantProfileReader(func(context.Context) string { return "Atlas" })
	h.SetWorkspaceStore(wsStore)
	h.SetOrchestrator(orchestration.NewOrchestrator(
		st, wsStore, nil, agentcomm.NewCommunicator(wsStore), nil, nil, nil))
	return &planRouteFixture{handler: h, workspaces: wsStore, agents: st, workspaceID: ws.ID}
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

type effects struct {
	workspaces int
	tickets    int
	agents     int
}

func (f *planRouteFixture) effects(t *testing.T) effects {
	t.Helper()
	ids, err := f.workspaces.List()
	if err != nil {
		t.Fatalf("list workspaces: %v", err)
	}
	tickets := 0
	for _, id := range ids {
		ws, err := f.workspaces.Get(id)
		if err != nil {
			t.Fatalf("get workspace %s: %v", id, err)
		}
		tickets += len(ws.Tasks)
	}
	return effects{workspaces: len(ids), tickets: tickets, agents: len(f.agents.ListAgents())}
}

// The reply to multi-step work is terminal advice. It names Give work, says
// nothing was started, and offers no Plan, approval, link, ticket, agent or run.
func assertAdviceThatStartsNothing(t *testing.T, f *planRouteFixture, resp map[string]any, before effects) {
	t.Helper()

	if got, _ := resp["manual_next_step"].(string); got != manualNextStepGiveWork {
		t.Fatalf("multi-step work must name the manual next step, got %v in %v", resp["manual_next_step"], resp)
	}
	if got, _ := resp["requires_approval"].(bool); got {
		t.Fatalf("multi-step work must not carry an inline approval: %v", resp)
	}
	for _, key := range []string{
		// A Plan, or anything that points at one.
		"requires_plan", "plan_id", "plan_url", "plan_reason",
		// An inline approval or preview.
		"action_plan_id", "action_preview", "action_preview_id", "approval_type",
		// Executed or queued multi-agent work.
		"orchestrated", "dynamic_agent_requests", "pending_plan_id", "planner_plan",
	} {
		if _, present := resp[key]; present {
			t.Fatalf("multi-step work must not carry %q: %v", key, resp)
		}
	}
	text, _ := resp["response"].(string)
	for _, want := range []string{"Give work", "I haven't started anything"} {
		if !strings.Contains(text, want) {
			t.Fatalf("the reply must say %q, got %q", want, text)
		}
	}
	if lower := strings.ToLower(text); strings.Contains(lower, "plan") || strings.Contains(lower, "approve") {
		t.Fatalf("there is no plan to open or approve, but the reply says so: %q", text)
	}
	if after := f.effects(t); after != before {
		t.Fatalf("multi-step chat changed state: before %+v, after %+v", before, after)
	}
}

func TestPlanBeforeAction_MultiStepWorkEndsInAdviceThatStartsNothing(t *testing.T) {
	f := newPlanRouteFixture(t)
	before := f.effects(t)

	resp := f.post(t, map[string]any{
		"question":           "research and analyze the quarterly report",
		"plan_before_action": true,
		// A real workspace in the route context is what used to let chat create a
		// Plan in it. It must leave that workspace exactly as it was.
		"workspace_id": f.workspaceID,
	})
	assertAdviceThatStartsNothing(t, f, resp, before)
}

func TestPlanBeforeAction_MultiStepWorkEndsInAdviceWithoutAWorkspace(t *testing.T) {
	f := newPlanRouteFixture(t)
	before := f.effects(t)

	resp := f.post(t, map[string]any{
		"question":           "research and analyze the quarterly report",
		"plan_before_action": true,
	})
	assertAdviceThatStartsNothing(t, f, resp, before)
}

func TestPlanBeforeAction_MultiStepWorkEndsInAdviceForAnUnknownWorkspace(t *testing.T) {
	f := newPlanRouteFixture(t)
	before := f.effects(t)

	resp := f.post(t, map[string]any{
		"question":           "research and analyze the quarterly report",
		"plan_before_action": true,
		"workspace_id":       "ws_without_a_store_entry",
	})
	assertAdviceThatStartsNothing(t, f, resp, before)
}

// Every message classifies as multi-step while the heuristic planner proposes a
// task for it. Pinned so that the reply has to read sensibly for any message, not
// only for obvious multi-step ones.
func TestPlanBeforeAction_HeuristicPlannerClassifiesOrdinaryMessagesAsMultiStep(t *testing.T) {
	f := newPlanRouteFixture(t)
	before := f.effects(t)

	resp := f.post(t, map[string]any{
		"question":           "hello there",
		"plan_before_action": true,
	})
	assertAdviceThatStartsNothing(t, f, resp, before)
}

// Planned work that needs a new agent gets the same terminal advice, with the
// planner's own decision attached and none of the receipt, approval or queued
// request fields an executed or proposed run carries.
func TestNeedsNewAgentReplyIsTerminalAdvice(t *testing.T) {
	rr := httptest.NewRecorder()
	writeNeedsNewAgentReply(rr, &types.PlannerDecision{MultiAgent: true, Mode: "auto"})

	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got, _ := resp["manual_next_step"].(string); got != manualNextStepGiveWork {
		t.Fatalf("manual_next_step = %v", resp["manual_next_step"])
	}
	if got, _ := resp["requires_approval"].(bool); got {
		t.Fatalf("must not require an approval: %v", resp)
	}
	for _, key := range []string{
		"orchestrated", "dynamic_agent_requests", "pending_plan_id", "planner_plan",
		"action_receipts", "plan_id", "plan_url", "requires_plan", "status",
	} {
		if _, present := resp[key]; present {
			t.Fatalf("must not carry %q: %v", key, resp)
		}
	}
	text, _ := resp["response"].(string)
	for _, want := range []string{"needs an agent that doesn't exist yet", "I haven't started anything", "Give work"} {
		if !strings.Contains(text, want) {
			t.Fatalf("reply lacks %q: %q", want, text)
		}
	}
	if lower := strings.ToLower(text); strings.Contains(lower, "plan") || strings.Contains(lower, "approve") {
		t.Fatalf("there is no plan or approval to mention: %q", text)
	}
}

// With the toggle off nothing is classified and ordinary chat is untouched by
// this route: it is never answered with the multi-step advice.
func TestOrdinaryChatWithoutPlanBeforeActionIsNotAnsweredWithTheAdvice(t *testing.T) {
	f := newPlanRouteFixture(t)
	st := f.agents
	if _, ok := st.GetAgent("Atlas"); !ok {
		t.Fatal("fixture agent missing")
	}

	resp := f.post(t, map[string]any{
		"question": "what time is it in tokyo",
	})
	if _, present := resp["manual_next_step"]; present {
		t.Fatalf("ordinary chat must not be answered with the multi-step advice: %v", resp)
	}
}

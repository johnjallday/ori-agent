package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/learningmissions"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func learningRoute(t *testing.T, h http.Handler, method, path string, body any, status int, into any) {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(method, "http://127.0.0.1:8765"+path, bytes.NewReader(data))
	r.Header.Set("Origin", "http://127.0.0.1:8765")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != status {
		t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
	}
	if into != nil {
		// Reused destinations must not retain fields omitted by a later reply.
		reflect.ValueOf(into).Elem().SetZero()
		if json.Unmarshal(w.Body.Bytes(), into) != nil {
			t.Fatalf("invalid reply: %s", w.Body.String())
		}
	}
}

func TestLearningMissionsProductionRoutesExecuteAndReviewExactResult(t *testing.T) {
	b, h := newLearningTestServer(t)
	if b.learningMissions == nil || b.learningMissionsHandler == nil {
		t.Fatal("learning owner not wired")
	}
	provider := &assessmentProvider{replies: []llm.ChatResponse{{Content: "A short draft for review.", FinishReason: llm.FinishReasonStop}, {Content: "A later ordinary draft.", FinishReason: llm.FinishReasonStop}}}
	b.llmFactory.Register("openai", provider)
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Learning fixture", Agents: []string{"Draft writer"}})
	ws.OwnerUserID = "local"
	if err := b.workspaceStore.Save(ws); err != nil {
		t.Fatal(err)
	}
	if err := b.workspaceStore.SaveWorkspaceAgent(ws.ID, "Draft writer", &agent.Agent{Settings: types.Settings{Provider: "openai", Model: "gpt-assess"}}); err != nil {
		t.Fatal(err)
	}
	status := func() learningmissions.Status {
		var out learningmissions.Status
		learningRoute(t, h, http.MethodGet, "/api/learning-missions", nil, 200, &out)
		return out
	}
	if status().Owner.Revision != 0 {
		t.Fatal("GET enrolled owner")
	}
	var out learningmissions.Outcome
	learningRoute(t, h, http.MethodPost, "/api/learning-missions/proposals", map[string]any{"operation_id": "proposal", "expected_revision": 0, "studio_id": ws.ID}, 200, &out)
	id := out.Instance.ID
	base := "/api/learning-missions/instances/" + id
	learningRoute(t, h, http.MethodPost, base+"/decisions", map[string]any{"operation_id": "accept", "expected_revision": status().Owner.Revision, "instance_revision": out.Instance.Revision, "decision": "accept"}, 200, &out)
	learningRoute(t, h, http.MethodPost, base+"/work", map[string]any{"operation_id": "prepare", "expected_revision": status().Owner.Revision, "instance_revision": out.Instance.Revision, "input": "Draft a brief welcome from this supplied text: welcome to the workshop.", "assignee": "Draft writer"}, 200, &out)
	ticketID := out.Instance.Progress.TicketID
	if ticketID == "" || out.Instance.Progress.RunID != "" || len(provider.sent()) != 0 {
		t.Fatal("learning admission executed work")
	}
	ticketPath := fmt.Sprintf("/api/workspaces/%s/tickets/%s", ws.ID, ticketID)
	var view struct {
		Executor workspace.RecipeExecutor `json:"executor"`
		Input    string                   `json:"input"`
	}
	learningRoute(t, h, http.MethodGet, ticketPath+"/execution", nil, 200, &view)
	if view.Executor.Digest == "" || view.Input == "" || len(provider.sent()) != 0 {
		t.Fatal("execution disclosure sent data")
	}
	learningRoute(t, h, http.MethodPost, ticketPath+"/start", map[string]string{"executor_digest": "stale"}, 409, nil)
	learningRoute(t, h, http.MethodPost, ticketPath+"/start", map[string]string{"executor_digest": view.Executor.Digest}, 200, nil)
	waitResult := func(expectedCalls int) learningmissions.Status {
		t.Helper()
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			s := status()
			if len(s.Instances) > 0 && s.Instances[0].Progress.Result != nil && s.Instances[0].Progress.Blocker == "" && len(provider.sent()) == expectedCalls {
				return s
			}
			time.Sleep(20 * time.Millisecond)
		}
		saved, _ := b.learningMissionsHandler.source.learning.Instance(t.Context(), "local", id)
		proof, proofErr := b.learningMissionsHandler.source.Result(t.Context(), "local", saved)
		run, _ := b.workspaceRunStore.GetRun(t.Context(), ws.ID, saved.Progress.RunID)
		artifacts, _ := b.workspaceRunStore.ListArtifacts(t.Context(), ws.ID, saved.Progress.RunID)
		t.Fatalf("result unavailable: %+v; proof=%+v %v verification=%v; run=%+v artifacts=%+v; calls=%d", saved, proof, proofErr, learningmissions.VerifyResult("local", saved, proof), run, artifacts, len(provider.sent()))
		return learningmissions.Status{}
	}
	s := waitResult(1)
	i := s.Instances[0]
	if i.State != learningmissions.Active {
		t.Fatal("execution alone completed learning")
	}
	learningRoute(t, h, http.MethodPost, base+"/review", map[string]any{"operation_id": "review-open", "expected_revision": s.Owner.Revision, "instance_revision": i.Revision, "action": "prepare"}, 200, &out)
	if out.Review == nil || out.Review.Result.RunID != i.Progress.RunID {
		t.Fatal("review not bound to canonical Run")
	}
	learningRoute(t, h, http.MethodPost, base+"/review", map[string]any{"operation_id": "review-reject", "expected_revision": status().Owner.Revision, "instance_revision": out.Instance.Revision, "action": "submit", "review_id": out.Review.ID, "decision": "reject", "reason": "missing_detail"}, 200, &out)
	if out.Instance.State != learningmissions.Completed {
		t.Fatal("informed rejection did not record practice")
	}
	var count int
	if err := b.sessionStore.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM learning_evidence WHERE owner_id='local'`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("delegate/evaluate credit: %d %v", count, err)
	}
	req := provider.sent()[0]
	if err := llm.CheckIsolatedRequest(req); err != nil || req.MaxTokens != 1200 || !req.SingleAttempt || req.ProviderConfiguration == "" {
		t.Fatalf("execution scope: %+v %v", req, err)
	}

	recorded := status()
	progress := recorded.Competencies.Items[1]
	if progress.Competency != "evaluate" || progress.Guided != 1 || progress.State != "practiced_with_guidance" || len(progress.EvidenceKeys) != 1 {
		t.Fatalf("versioned practice projection: %+v", recorded.Competencies)
	}
	evidencePath := "/api/learning-missions/evidence/" + progress.EvidenceKeys[0]
	for range 2 {
		var evidence learningmissions.EvidenceView
		learningRoute(t, h, http.MethodGet, evidencePath, nil, 200, &evidence)
		if evidence.ResultText == "" || evidence.InputText != view.Input || evidence.Review.Result.RunID != i.Progress.RunID || evidence.Review.Decision != "reject" {
			t.Fatalf("canonical review inspection: %+v", evidence)
		}
	}
	if status().Owner.Revision != recorded.Owner.Revision || len(provider.sent()) != 1 {
		t.Fatal("evidence inspection enrolled, credited or executed")
	}

	// Reset Getting Started owns only the fixed progression graph, not this
	// separate learning contract, consumed review or deduplicated practice.
	// A newcomer with no hired assistant must be able to reset this graph too.
	learningRoute(t, h, http.MethodPost, "/api/progression/reset", map[string]any{}, 200, nil)
	afterStarterReset := status()
	if afterStarterReset.Owner.Revision != recorded.Owner.Revision || !reflect.DeepEqual(afterStarterReset.Competencies, recorded.Competencies) || afterStarterReset.Instances[0].Revision != recorded.Instances[0].Revision || len(provider.sent()) != 1 {
		t.Fatal("Getting Started reset changed separate learning or executed work")
	}
	learningRoute(t, h, http.MethodGet, evidencePath, nil, 200, nil)

	// Clearing learning removes authority, not the actual Ticket or its recipe.
	learningRoute(t, h, http.MethodPost, "/api/learning-missions/clear", map[string]any{"operation_id": "clear", "expected_revision": status().Owner.Revision, "scope": "history"}, 200, nil)
	if len(status().Instances) != 0 {
		t.Fatal("history not cleared")
	}
	learningRoute(t, h, http.MethodGet, evidencePath, nil, 404, nil)
	service := workspace.NewTicketService(b.workspaceStore)
	if _, err := service.Transition(ws.ID, ticketID, workspace.TicketTransitionInput{To: workspace.TicketStateDone, Actor: workspace.TicketActorUser}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Transition(ws.ID, ticketID, workspace.TicketTransitionInput{To: workspace.TicketStateReady, Actor: workspace.TicketActorUser}); err != nil {
		t.Fatal(err)
	}
	learningRoute(t, h, http.MethodGet, ticketPath+"/execution", nil, 200, &view)
	learningRoute(t, h, http.MethodPost, ticketPath+"/start", map[string]string{"executor_digest": view.Executor.Digest}, 200, nil)
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		ticket, err := service.Get(ws.ID, ticketID)
		if err == nil && ticket.State == workspace.TicketStateReview && len(provider.sent()) == 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(provider.sent()) != 2 || len(status().Instances) != 0 {
		t.Fatal("clear blocked ordinary work or revived learning")
	}
}

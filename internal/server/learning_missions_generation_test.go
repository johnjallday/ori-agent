package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/learningmissions"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestLearningMissionsProductionGenerationRequiresOneReviewedDefaultModelCall(t *testing.T) {
	b, handler := newLearningTestServer(t)
	provider := &designerProvider{assessmentProvider: &assessmentProvider{}, isolation: true}
	b.llmFactory.Register("openai", provider)
	if err := b.configManager.SetSystemModel("openai", "gpt-assess"); err != nil {
		t.Fatal(err)
	}
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Community workshop", Agents: []string{"Draft writer"}})
	ws.OwnerUserID = "local"
	ws.SharedData = map[string]any{"private": "NEVER_SEND_PRIVATE_BODY"}
	if err := b.workspaceStore.Save(ws); err != nil {
		t.Fatal(err)
	}
	if err := b.workspaceStore.SaveWorkspaceAgent(ws.ID, "Draft writer", &agent.Agent{Settings: types.Settings{Provider: "openai", Model: "gpt-assess"}}); err != nil {
		t.Fatal(err)
	}
	provider.respond = func(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
		if strings.Contains(req.Messages[0].Content, "NEVER_SEND") || strings.Contains(req.Messages[0].Content, ws.ID) {
			t.Fatal("private canonical data reached designer")
		}
		var data learningmissions.DesignContext
		if err := json.Unmarshal([]byte(req.Messages[0].Content), &data); err != nil || len(data.Candidates) != 1 {
			t.Fatalf("context: %+v %v", data, err)
		}
		c := data.Candidates[0]
		body, err := json.Marshal(learningmissions.GeneratedProposal{CandidateRef: c.Ref, Structure: c.Structure, Version: c.Version, Steps: c.Steps,
			Goal: "Draft a short community workshop welcome", Scope: "Use only supplied workshop details", Why: "You selected this workshop workspace", Hints: []string{}})
		if err != nil {
			t.Fatal(err)
		}
		return &llm.ChatResponse{Content: string(body), FinishReason: llm.FinishReasonStop, Usage: llm.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}}, nil
	}
	var status learningmissions.Status
	for range 3 {
		learningRoute(t, handler, http.MethodGet, "/api/learning-missions", nil, 200, &status)
	}
	if status.Owner.Revision != 0 || status.Model == nil || provider.calls != 0 {
		t.Fatalf("read enrolled/called: %+v", status)
	}
	req := map[string]any{"operation_id": "personalized", "expected_revision": 0, "studio_id": ws.ID, "personalize": true}
	learningRoute(t, handler, http.MethodPost, "/api/learning-missions/proposals", req, 400, nil)
	req["consent_version"], req["model_fingerprint"] = 1, strings.Repeat("a", 64)
	learningRoute(t, handler, http.MethodPost, "/api/learning-missions/proposals", req, 409, nil)
	if provider.calls != 0 {
		t.Fatal("missing/stale disclosure sent data")
	}
	req["model_fingerprint"] = status.Model.Fingerprint
	var out learningmissions.Outcome
	learningRoute(t, handler, http.MethodPost, "/api/learning-missions/proposals", req, 200, &out)
	if out.Instance == nil || !out.Instance.Definition.Personalized || out.Operation.ReservedTokens != learningmissions.GenerationReservedTokens || provider.calls != 1 {
		t.Fatalf("generation: %+v", out)
	}
	id := out.Instance.ID
	learningRoute(t, handler, http.MethodPost, "/api/learning-missions/proposals", req, 200, &out)
	learningRoute(t, handler, http.MethodGet, "/api/learning-missions/operations/personalized", nil, 200, &out)
	learningRoute(t, handler, http.MethodGet, "/api/learning-missions", nil, 200, &status)
	if out.Instance.ID != id || provider.calls != 1 {
		t.Fatal("replay/hydration regenerated")
	}
	var count int
	if err := b.sessionStore.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM learning_evidence`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("generation granted practice: %d %v", count, err)
	}
	fresh, err := b.workspaceStore.Get(ws.ID)
	if err != nil || len(fresh.Tasks) != 0 {
		t.Fatalf("generation created work: %v", err)
	}
	if runs, err := b.workspaceRunStore.ListRuns(t.Context(), ws.ID); err != nil || len(runs) != 0 {
		t.Fatalf("generation executed work: %v", err)
	}
}

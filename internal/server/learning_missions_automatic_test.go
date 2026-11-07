package server

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/learningmissions"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type automaticLearningProvider struct{ assessmentProvider }

func (p *automaticLearningProvider) Chat(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	if req.SystemPrompt != learningmissions.DesignSystemPrompt {
		return p.assessmentProvider.Chat(ctx, req)
	}
	p.mu.Lock()
	p.requests = append(p.requests, req)
	p.mu.Unlock()
	var data learningmissions.DesignContext
	if err := json.Unmarshal([]byte(req.Messages[0].Content), &data); err != nil || len(data.Candidates) != 1 {
		return nil, learningmissions.ErrInvalid
	}
	c := data.Candidates[0]
	body, err := json.Marshal(learningmissions.GeneratedProposal{CandidateRef: c.Ref, Structure: c.Structure, Version: c.Version, Steps: c.Steps,
		Goal: "Refine a short workshop welcome", Scope: "Use the selected result and supplied correction", Why: "A local workshop review is available", Hints: []string{}})
	if err != nil {
		return nil, err
	}
	return &llm.ChatResponse{Content: string(body), FinishReason: llm.FinishReasonStop, Usage: llm.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}}, nil
}

func TestLearningMissionsAutomaticWorkerUsesOnlyFreshConsentedReviewMilestones(t *testing.T) {
	b, h := newLearningTestServer(t)
	provider := &automaticLearningProvider{assessmentProvider: assessmentProvider{replies: []llm.ChatResponse{{Content: "Welcome to the workshop.", FinishReason: llm.FinishReasonStop}}}}
	b.llmFactory.Register("openai", provider)
	if err := b.configManager.SetSystemModel("openai", "gpt-assess"); err != nil {
		t.Fatal(err)
	}
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Workshop", Agents: []string{"Draft writer"}})
	ws.OwnerUserID = "local"
	if err := b.workspaceStore.Save(ws); err != nil {
		t.Fatal(err)
	}
	if err := b.workspaceStore.SaveWorkspaceAgent(ws.ID, "Draft writer", &agent.Agent{Settings: types.Settings{Provider: "openai", Model: "gpt-assess"}}); err != nil {
		t.Fatal(err)
	}
	status := func() learningmissions.Status {
		var s learningmissions.Status
		learningRoute(t, h, http.MethodGet, "/api/learning-missions", nil, 200, &s)
		return s
	}
	s := status()
	learningRoute(t, h, http.MethodPost, "/api/learning-missions/preferences", map[string]any{"operation_id": "consent", "expected_revision": s.Owner.Revision, "mode": "automatic", "workspace_ids": []string{ws.ID}, "observe": false, "guidance": "guided", "effort": "small", "consent_version": 1, "model_fingerprint": s.Model.Fingerprint}, 200, nil)
	for range 3 {
		_ = status()
	}
	if len(provider.sent()) != 0 {
		t.Fatal("saving consent or reads generated")
	}
	var out learningmissions.Outcome
	learningRoute(t, h, http.MethodPost, "/api/learning-missions/proposals", map[string]any{"operation_id": "proposal", "expected_revision": status().Owner.Revision, "studio_id": ws.ID}, 200, &out)
	base := "/api/learning-missions/instances/" + out.Instance.ID
	learningRoute(t, h, http.MethodPost, base+"/decisions", map[string]any{"operation_id": "accept", "expected_revision": status().Owner.Revision, "instance_revision": out.Instance.Revision, "decision": "accept"}, 200, &out)
	learningRoute(t, h, http.MethodPost, base+"/work", map[string]any{"operation_id": "prepare", "expected_revision": status().Owner.Revision, "instance_revision": out.Instance.Revision, "input": "Write a workshop welcome.", "assignee": "Draft writer"}, 200, &out)
	ticketPath := "/api/workspaces/" + ws.ID + "/tickets/" + out.Instance.Progress.TicketID
	var execution struct {
		Executor workspace.RecipeExecutor `json:"executor"`
	}
	learningRoute(t, h, http.MethodGet, ticketPath+"/execution", nil, 200, &execution)
	learningRoute(t, h, http.MethodPost, ticketPath+"/start", map[string]string{"executor_digest": execution.Executor.Digest}, 200, nil)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		s = status()
		if s.Instances[0].Progress.Result != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if s.Instances[0].Progress.Result == nil || len(provider.sent()) != 1 {
		t.Fatal("execution did not finish, or execution alone generated a suggestion")
	}
	learningRoute(t, h, http.MethodPost, base+"/review", map[string]any{"operation_id": "inspect", "expected_revision": s.Owner.Revision, "instance_revision": s.Instances[0].Revision, "action": "prepare"}, 200, &out)
	decision := map[string]any{"operation_id": "decision", "expected_revision": status().Owner.Revision, "instance_revision": out.Instance.Revision, "action": "submit", "review_id": out.Review.ID, "decision": "reject", "reason": "missing_detail"}
	learningRoute(t, h, http.MethodPost, base+"/review", decision, 200, &out)
	if out.Operation.MilestoneID != "" {
		t.Fatal("internal trigger identity escaped projection")
	}
	deadline = time.Now().Add(10 * time.Second)
	var automatic *learningmissions.Instance
	for time.Now().Before(deadline) {
		s = status()
		for _, i := range s.Instances {
			if i.State == learningmissions.Offered && i.Definition.Automatic {
				automatic = &i
				break
			}
		}
		if automatic != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if automatic == nil || len(provider.sent()) != 2 || automatic.Progress.TicketID != "" {
		t.Fatalf("worker did not publish one proposal: %+v calls=%d", s, len(provider.sent()))
	}
	learningRoute(t, h, http.MethodPost, base+"/review", decision, 200, &out)
	for range 3 {
		_ = status()
	}
	if len(provider.sent()) != 2 {
		t.Fatal("replay or visits generated again")
	}
	actual, err := b.workspaceStore.Get(ws.ID)
	if err != nil || len(actual.Tasks) != 1 {
		t.Fatal("automatic suggestion created actual work", err)
	}
	if err := llm.CheckIsolatedRequest(provider.sent()[1]); err != nil {
		t.Fatal(err)
	}
}

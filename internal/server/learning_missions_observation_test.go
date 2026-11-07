package server

import (
	"net/http"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/learningmissions"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestLearningMissionsOrdinaryReviewedStartHasNoSyntheticEnrollment(t *testing.T) {
	b, h := newLearningTestServer(t)
	provider := &assessmentProvider{replies: []llm.ChatResponse{{Content: "A bounded ordinary draft.", FinishReason: llm.FinishReasonStop}}}
	b.llmFactory.Register("openai", provider)
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Ordinary practice", Agents: []string{"Draft writer"}})
	ws.OwnerUserID = "local"
	if err := b.workspaceStore.Save(ws); err != nil {
		t.Fatal(err)
	}
	if err := b.workspaceStore.SaveWorkspaceAgent(ws.ID, "Draft writer", &agent.Agent{Settings: types.Settings{Provider: "openai", Model: "gpt-assess"}}); err != nil {
		t.Fatal(err)
	}
	// A compiled ordinary bounded draft, with no learning instance/binding.
	tickets := workspace.NewTicketService(b.workspaceStore)
	ticket, err := tickets.Create(workspace.TicketCreateInput{WorkspaceID: ws.ID, State: workspace.TicketStateReady, Title: "Ordinary draft", Description: "Draft a short welcome for a local workshop.", Assignee: "Draft writer", Actor: workspace.TicketActorUser})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.workspaceStore.Update(ws.ID, func(saved *workspace.Workspace) error {
		return saved.MutateTask(ticket.ID, func(task *workspace.Task) error {
			if task.Context == nil {
				task.Context = map[string]any{}
			}
			task.Context[workspace.TaskContextFirstTaskRecipe], task.Context[workspace.TaskContextFirstTaskRecipeVersion] = workspace.LearningDraftRecipe, 1
			return nil
		})
	}); err != nil {
		t.Fatal(err)
	}
	status := func() learningmissions.Status {
		var out learningmissions.Status
		learningRoute(t, h, http.MethodGet, "/api/learning-missions", nil, 200, &out)
		return out
	}
	if status().Owner.Revision != 0 {
		t.Fatal("ordinary task creation/visit enrolled learning")
	}
	learningRoute(t, h, http.MethodPost, "/api/learning-missions/preferences", map[string]any{"operation_id": "consent", "expected_revision": 0, "mode": "on_request", "workspace_ids": []string{ws.ID}, "observe": true, "guidance": "basic", "effort": "small", "focus": "", "consent_version": 1}, 200, nil)
	path := "/api/workspaces/" + ws.ID + "/tickets/" + ticket.ID
	var execution struct {
		Executor workspace.RecipeExecutor `json:"executor"`
	}
	learningRoute(t, h, http.MethodGet, path+"/execution", nil, 200, &execution)
	if len(provider.sent()) != 0 {
		t.Fatal("observation consent or disclosure called a model")
	}
	learningRoute(t, h, http.MethodPost, path+"/start", map[string]string{"executor_digest": execution.Executor.Digest}, 200, nil)
	var saved learningmissions.Status
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		saved = status()
		if len(saved.Observations) == 1 && saved.Observations[0].Ready {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(saved.Observations) != 1 || !saved.Observations[0].Ready || len(saved.Instances) != 0 || len(provider.sent()) != 1 {
		t.Fatalf("ordinary canonical observation: %+v calls=%d", saved, len(provider.sent()))
	}
	observed := saved.Observations[0]
	learningRoute(t, h, http.MethodPost, "/api/learning-missions/proposals", map[string]any{"operation_id": "prefer-existing", "expected_revision": saved.Owner.Revision, "studio_id": ws.ID}, 404, nil)
	if status().Owner.Revision != saved.Owner.Revision || len(provider.sent()) != 1 {
		t.Fatal("existing work priority created another offer/call")
	}
	base := "/api/learning-missions/observations/" + observed.ID + "/review"
	learningRoute(t, h, http.MethodPost, base, map[string]any{"operation_id": "forged", "expected_revision": saved.Owner.Revision, "observed_revision": observed.Revision, "action": "prepare", "actor": "user"}, 400, nil)
	var out learningmissions.Outcome
	prepare := map[string]any{"operation_id": "prepare", "expected_revision": saved.Owner.Revision, "observed_revision": observed.Revision, "action": "prepare"}
	learningRoute(t, h, http.MethodPost, base, prepare, 200, &out)
	if out.Instance != nil || out.Observation == nil || out.ResultText != "A bounded ordinary draft." || out.ObservedInput != "Draft a short welcome for a local workshop." {
		t.Fatalf("ordinary exact review: %+v", out)
	}
	var recovered learningmissions.Outcome
	learningRoute(t, h, http.MethodGet, "/api/learning-missions/operations/prepare", nil, 200, &recovered)
	if recovered.ResultText != out.ResultText || recovered.Review.ID != out.Review.ID {
		t.Fatal("lost response recovery fabricated/replaced a review")
	}
	submit := map[string]any{"operation_id": "decide", "expected_revision": status().Owner.Revision, "observed_revision": out.Observation.Revision, "action": "submit", "review_id": out.Review.ID, "decision": "reject", "reason": "incorrect"}
	learningRoute(t, h, http.MethodPost, base, submit, 200, &out)
	learningRoute(t, h, http.MethodPost, base, submit, 200, nil)
	saved = status()
	if len(saved.Instances) != 0 || len(saved.Observations) != 0 || saved.Competencies.Items[1].Attempts != 1 || saved.Competencies.Items[1].State != "support_unknown" {
		t.Fatalf("actor/basic preference became enrollment/independence: %+v", saved)
	}
	var evidence learningmissions.EvidenceView
	learningRoute(t, h, http.MethodGet, "/api/learning-missions/evidence/"+saved.Competencies.Items[1].EvidenceKeys[0], nil, 200, &evidence)
	if evidence.ResultText != "A bounded ordinary draft." || evidence.Review.InstanceID != "" || evidence.Review.ObservationID != observed.ID {
		t.Fatalf("ordinary saved evidence: %+v", evidence)
	}
	if len(provider.sent()) != 1 {
		t.Fatal("review/inspection repeated execution")
	}
	actual, err := tickets.Get(ws.ID, ticket.ID)
	if err != nil || actual.State != workspace.TicketStateReview {
		t.Fatal("learning changed ordinary Ticket review semantics", err)
	}
	// A later explicit improvement can use this real reviewed original; no
	// synthetic delegate mission is needed to manufacture its prerequisites.
	learningRoute(t, h, http.MethodPost, "/api/learning-missions/proposals", map[string]any{"operation_id": "improve", "expected_revision": saved.Owner.Revision, "studio_id": ws.ID, "structure": "improve_result"}, 200, &out)
	if out.Instance == nil || out.Instance.Definition.OriginalReceipt != evidence.Review.ID {
		t.Fatalf("ordinary original not selected: %+v", out)
	}
	var original learningmissions.OriginalView
	learningRoute(t, h, http.MethodGet, "/api/learning-missions/instances/"+out.Instance.ID+"/original", nil, 200, &original)
	if original.Result != evidence.Review.Result || original.Input != evidence.InputText || original.ResultText != evidence.ResultText || len(provider.sent()) != 1 {
		t.Fatalf("ordinary lineage inspection: %+v", original)
	}
}

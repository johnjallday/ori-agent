package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/learningmissions"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestLearningMissionsProductionRevisionAndReuseBindDistinctReviewedResults(t *testing.T) {
	b, handler := newLearningTestServer(t)
	provider := &assessmentProvider{replies: []llm.ChatResponse{
		{Content: "Welcome to our workshop.", FinishReason: llm.FinishReasonStop},
		{Content: "Welcome to our workshop at 10:00.", FinishReason: llm.FinishReasonStop},
		{Content: "Join the evening volunteer meetup.", FinishReason: llm.FinishReasonStop},
		{Content: "An unsuitable reading group invitation.", FinishReason: llm.FinishReasonStop},
	}}
	b.llmFactory.Register("openai", provider)
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Lineage workshop", Agents: []string{"Draft writer"}})
	ws.OwnerUserID = "local"
	if err := b.workspaceStore.Save(ws); err != nil {
		t.Fatal(err)
	}
	if err := b.workspaceStore.SaveWorkspaceAgent(ws.ID, "Draft writer", &agent.Agent{Settings: types.Settings{Provider: "openai", Model: "gpt-assess"}}); err != nil {
		t.Fatal(err)
	}
	status := func() learningmissions.Status {
		var out learningmissions.Status
		learningRoute(t, handler, http.MethodGet, "/api/learning-missions", nil, 200, &out)
		return out
	}
	find := func(id string) learningmissions.Instance {
		for _, instance := range status().Instances {
			if instance.ID == id {
				return instance
			}
		}
		t.Fatalf("missing instance %s", id)
		return learningmissions.Instance{}
	}
	type journey struct {
		structure                            learningmissions.StructureID
		input, inputChange, decision, change string
	}
	journeys := []journey{
		{structure: learningmissions.DelegateInspect, input: "Draft a workshop welcome.", decision: "use"},
		{structure: learningmissions.ImproveResult, input: "Add the opening time, 10:00.", decision: "reject", change: "It now includes a time, but still does not say where to meet."},
		{structure: learningmissions.RepeatSuccess, input: "Draft an announcement for an evening volunteer meetup.", inputChange: "A different event and audience, in the evening.", decision: "use"},
		{structure: learningmissions.RepeatSuccess, input: "Draft an invitation to a reading group at the library.", inputChange: "A new event at the library rather than the volunteer meetup.", decision: "reject"},
	}
	var first learningmissions.Instance
	for n, test := range journeys {
		prefix := fmt.Sprintf("journey-%d", n)
		var out learningmissions.Outcome
		learningRoute(t, handler, http.MethodPost, "/api/learning-missions/proposals", map[string]any{
			"operation_id": prefix + "-offer", "expected_revision": status().Owner.Revision, "studio_id": ws.ID, "structure": test.structure,
		}, 200, &out)
		id, base := out.Instance.ID, "/api/learning-missions/instances/"+out.Instance.ID
		if out.Instance.Definition.Structure != test.structure || out.Instance.Definition.Personalized {
			t.Fatal("wrong generic structure")
		}
		var original learningmissions.OriginalView
		if n > 0 {
			before := status().Owner.Revision
			learningRoute(t, handler, http.MethodGet, base+"/original", nil, 200, &original)
			if original.ResultText == "" || original.Input == "" || status().Owner.Revision != before || len(provider.sent()) != n {
				t.Fatal("original view mutated/generated or guessed missing evidence")
			}
			if n == 1 && original.Result != *first.Progress.Result {
				t.Fatal("revision chose mutable latest instead of pinned original")
			}
		}
		learningRoute(t, handler, http.MethodPost, base+"/decisions", map[string]any{
			"operation_id": prefix + "-accept", "expected_revision": status().Owner.Revision, "instance_revision": out.Instance.Revision, "decision": "accept",
		}, 200, &out)
		if test.structure == learningmissions.RepeatSuccess {
			learningRoute(t, handler, http.MethodPost, base+"/work", map[string]any{
				"operation_id": prefix + "-unchanged", "expected_revision": status().Owner.Revision, "instance_revision": out.Instance.Revision,
				"input": "  " + strings.ReplaceAll(original.Input, " ", "  ") + "  ", "input_change": "A claim cannot make unchanged input different.", "assignee": "Draft writer",
			}, 400, nil)
			learningRoute(t, handler, http.MethodPost, base+"/work", map[string]any{
				"operation_id": prefix + "-no-explanation", "expected_revision": status().Owner.Revision, "instance_revision": out.Instance.Revision,
				"input": test.input, "assignee": "Draft writer",
			}, 400, nil)
		}
		learningRoute(t, handler, http.MethodPost, base+"/work", map[string]any{
			"operation_id": prefix + "-work", "expected_revision": status().Owner.Revision, "instance_revision": out.Instance.Revision,
			"input": test.input, "input_change": test.inputChange, "assignee": "Draft writer",
		}, 200, &out)
		if out.Operation.Status != "succeeded" || out.Instance.Progress.TicketID == "" || len(provider.sent()) != n {
			t.Fatalf("preparation executed or lost lineage: %+v", out)
		}
		ticketPath := fmt.Sprintf("/api/workspaces/%s/tickets/%s", ws.ID, out.Instance.Progress.TicketID)
		var execution struct {
			Executor     workspace.RecipeExecutor `json:"executor"`
			Input        string                   `json:"input"`
			OriginalText string                   `json:"original_text"`
		}
		learningRoute(t, handler, http.MethodGet, ticketPath+"/execution", nil, 200, &execution)
		if execution.Input != test.input || (test.structure == learningmissions.ImproveResult && execution.OriginalText != original.ResultText) {
			t.Fatal("execution disclosure omitted original/correction")
		}
		learningRoute(t, handler, http.MethodPost, ticketPath+"/start", map[string]string{"executor_digest": execution.Executor.Digest}, 200, nil)
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			current := find(id)
			if current.Progress.Result != nil && current.Progress.Blocker == "" {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		current := find(id)
		if current.Progress.Result == nil || current.State != learningmissions.Active || len(provider.sent()) != n+1 {
			t.Fatalf("exact result missing or execution counted as practice: %+v", current)
		}
		learningRoute(t, handler, http.MethodPost, base+"/review", map[string]any{
			"operation_id": prefix + "-inspect", "expected_revision": status().Owner.Revision, "instance_revision": current.Revision, "action": "prepare",
		}, 200, &out)
		if n > 0 && (out.OriginalText != original.ResultText || out.Review.Original == nil || *out.Review.Original != original.Result ||
			out.Review.Result.RunID == original.Result.RunID || out.Review.Result.TicketID == original.Result.TicketID || out.Review.Result.ArtifactID == original.Result.ArtifactID) {
			t.Fatal("comparison/reuse did not bind distinct exact identities")
		}
		decision := map[string]any{"operation_id": prefix + "-decision", "expected_revision": status().Owner.Revision, "instance_revision": out.Instance.Revision,
			"action": "submit", "review_id": out.Review.ID, "decision": test.decision, "reason": "missing_detail", "change": test.change}
		if test.structure == learningmissions.ImproveResult {
			decision["change"] = ""
			learningRoute(t, handler, http.MethodPost, base+"/review", decision, 400, nil)
			decision["change"] = test.change
		}
		learningRoute(t, handler, http.MethodPost, base+"/review", decision, 200, &out)
		learningRoute(t, handler, http.MethodPost, base+"/review", decision, 200, &out)
		if n == 3 {
			if out.Instance.State != learningmissions.Blocked || out.Instance.Progress.Blocker != "result_not_useful" {
				t.Fatal("rejected reuse claimed success")
			}
		} else if out.Instance.State != learningmissions.Completed {
			t.Fatalf("practice not completed: %+v", out)
		}
		if n == 0 {
			first = *out.Instance
			if err := b.workspaceStore.Update(ws.ID, func(ws *workspace.Workspace) error {
				return ws.MutateTask(first.Progress.TicketID, func(task *workspace.Task) error {
					task.CurrentRunID, task.Result = "later-unrelated", "This mutable latest result must not be used."
					return nil
				})
			}); err != nil {
				t.Fatal(err)
			}
		}
	}
	var reuseCredits int
	if err := b.sessionStore.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM learning_evidence WHERE json_extract(record,'$.competency')='reuse'`).Scan(&reuseCredits); err != nil || reuseCredits != 1 {
		t.Fatalf("reuse credit/replay: %d %v", reuseCredits, err)
	}
	requests := provider.sent()
	var revision map[string]string
	if err := json.Unmarshal([]byte(requests[1].Messages[0].Content), &revision); err != nil || revision["original_result"] != "Welcome to our workshop." || revision["reviewed_correction"] != journeys[1].input || len(revision) != 2 {
		t.Fatalf("revision input scope: %+v %v", revision, err)
	}
	var reuse map[string]string
	if err := json.Unmarshal([]byte(requests[2].Messages[0].Content), &reuse); err != nil || reuse["reviewed_input"] != journeys[2].input || len(reuse) != 1 {
		t.Fatalf("reuse silently composed another process: %+v %v", reuse, err)
	}
}

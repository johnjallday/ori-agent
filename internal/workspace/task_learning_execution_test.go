package workspace

import (
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/llm"
)

func learningExecutionFixture(t *testing.T) (*recipeRunFixture, Task) {
	t.Helper()
	f := newRecipeRunFixture(t, privileged)
	f.provider.responses[0].FinishReason = llm.FinishReasonStop
	f.handler.SetRecipeRunner(LearningDraftRecipe, f.runner)
	ws := NewWorkspace(CreateWorkspaceParams{Name: "Drafts", Agents: []string{"Robin"}})
	task := Task{ID: "draft", WorkspaceID: ws.ID, To: "Robin", Description: "Draft", Details: "Supplied text"}
	if _, err := ws.AddRecipeTask(task, LearningDraftRecipe, 1); err != nil {
		t.Fatal(err)
	}
	store := NewInMemoryStore()
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}
	f.handler.workspaceStore = store
	return f, task
}

func TestLearningExecutionRequiresExactOnceOnlyReview(t *testing.T) {
	f, task := learningExecutionFixture(t)
	if _, err := f.handler.ExecuteTask(t.Context(), "Robin", task); err == nil {
		t.Fatal("unreviewed execution")
	}
	view, reason := f.handler.RecipeExecutorDisclosure(task)
	if view == nil {
		t.Fatal(reason)
	}
	if err := f.handler.ReviewLearningExecutor(t.Context(), task.WorkspaceID, task.ID, "Robin", "forged"); err == nil {
		t.Fatal("forged disclosure")
	}
	if err := f.handler.ReviewLearningExecutor(t.Context(), task.WorkspaceID, task.ID, "Robin", view.Digest); err != nil {
		t.Fatal(err)
	}
	if _, err := f.handler.ExecuteTask(t.Context(), "Robin", task); err != nil {
		t.Fatal(err)
	}
	if _, err := f.handler.ExecuteTask(t.Context(), "Robin", task); err == nil {
		t.Fatal("review reused on another attempt")
	}
	if len(f.provider.requests) != 1 {
		t.Fatalf("calls: %d", len(f.provider.requests))
	}
	req := f.provider.requests[0]
	if err := llm.CheckIsolatedRequest(req); err != nil || req.MaxTokens != LearningDraftMaxTokens || len(req.Messages) != 1 || !req.SingleAttempt || req.ProviderConfiguration == "" {
		t.Fatalf("not bounded/isolated: %+v %v", req, err)
	}
	if f.factory != 0 || f.registry.starts != 0 || f.registry.lists != 0 || f.search.calls != 0 {
		t.Fatal("resolved ordinary tools")
	}
}

func TestLearningExecutionEditedInputInvalidatesReview(t *testing.T) {
	f, task := learningExecutionFixture(t)
	view, _ := f.handler.RecipeExecutorDisclosure(task)
	if err := f.handler.ReviewLearningExecutor(t.Context(), task.WorkspaceID, task.ID, "", view.Digest); err != nil {
		t.Fatal(err)
	}
	if err := f.handler.workspaceStore.Update(task.WorkspaceID, func(ws *Workspace) error {
		return ws.MutateTask(task.ID, func(saved *Task) error { saved.Details = "Edited after review"; return nil })
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.handler.ExecuteTask(t.Context(), "Robin", task); err == nil {
		t.Fatal("stale review executed edited input")
	}
	if len(f.provider.requests) != 0 || f.runner.briefs != 0 {
		t.Fatal("sent unreviewed data")
	}
	fresh, _ := f.handler.RecipeExecutorDisclosure(task)
	if fresh.Digest == view.Digest {
		t.Fatal("text not bound to disclosure")
	}
}

func TestLearningExecutionRefusesPartialOrOverBudgetReplies(t *testing.T) {
	for _, reply := range []llm.ChatResponse{
		{Content: "Incomplete", FinishReason: llm.FinishReasonLength},
		{Content: "Unknown finish"},
		{Content: "Too many tokens", FinishReason: llm.FinishReasonStop, Usage: llm.Usage{CompletionTokens: LearningDraftMaxTokens + 1}},
	} {
		f, task := learningExecutionFixture(t)
		f.provider.responses = []llm.ChatResponse{reply}
		view, _ := f.handler.RecipeExecutorDisclosure(task)
		if err := f.handler.ReviewLearningExecutor(t.Context(), task.WorkspaceID, task.ID, "", view.Digest); err != nil {
			t.Fatal(err)
		}
		if _, err := f.handler.ExecuteTask(t.Context(), "Robin", task); err == nil || len(f.runner.finished) != 0 || len(f.provider.requests) != 1 {
			t.Fatalf("partial reply became a result or retried: %v", err)
		}
	}
}

type learningBoundedProvider struct {
	llm.Provider
	configuration string
}

func (p *learningBoundedProvider) BoundedTextConfiguration() string { return p.configuration }

func TestLearningExecutionRequiresBoundedProviderAndFreshDestinationReview(t *testing.T) {
	f, task := learningExecutionFixture(t)
	// Embedding only Provider deliberately omits the optional hard-limit contract.
	f.handler.llmFactory.Register("openai", struct{ llm.Provider }{f.provider})
	if view, _ := f.handler.RecipeExecutorDisclosure(task); view != nil {
		t.Fatal("isolation alone claimed an output cap")
	}
	provider := &learningBoundedProvider{Provider: f.provider, configuration: strings.Repeat("a", 64)}
	f.handler.llmFactory.Register("openai", provider)
	view, _ := f.handler.RecipeExecutorDisclosure(task)
	if err := f.handler.ReviewLearningExecutor(t.Context(), task.WorkspaceID, task.ID, "", view.Digest); err != nil {
		t.Fatal(err)
	}
	provider.configuration = strings.Repeat("b", 64)
	if _, err := f.handler.ExecuteTask(t.Context(), "Robin", task); err == nil || len(f.provider.requests) != 0 {
		t.Fatalf("changed destination reached model: %v", err)
	}
}

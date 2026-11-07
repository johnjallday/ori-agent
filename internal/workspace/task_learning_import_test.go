package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLegacyImportedDraftNeedsFreshDestinationReviewDespiteCopiedNativeApproval(t *testing.T) {
	f, task := learningExecutionFixture(t)
	view, _ := f.handler.RecipeExecutorDisclosure(task)
	if err := f.handler.ReviewLearningExecutor(t.Context(), task.WorkspaceID, task.ID, "Robin", view.Digest); err != nil {
		t.Fatal(err)
	}
	ws, err := f.handler.workspaceStore.Get(task.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	native, err := ws.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	oldToken := LearningReviewedStart(*native)
	if oldToken == "" || native.Context[TaskContextReviewedExecutor] == nil {
		t.Fatal("fixture lacks a real native approval")
	}
	if StripLearningImportAuthority(nil) {
		t.Fatal("nil projection changed")
	}
	// Normal native serialization/reopen is not import and retains its own
	// single-use reviewed approval until execution consumes it.
	data, err := ws.ToJSON()
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := FromJSON(data)
	if err != nil || LearningReviewedStart(reopened.Tasks[0]) != oldToken {
		t.Fatal("normal reopen was treated as a transfer", err)
	}
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, WorkspaceConfigFile), data, 0600); err != nil {
		t.Fatal(err)
	}
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	imported, _, err := store.Import(source)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []*Workspace{imported} {
		if value.Tasks[0].Context[TaskContextReviewedExecutor] != nil || LearningReviewedStart(value.Tasks[0]) != "" {
			t.Fatal("returned legacy import kept approval")
		}
	}
	saved, err := store.Get(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	copied := saved.Tasks[0]
	if copied.Context[TaskContextReviewedExecutor] != nil || LearningReviewedStart(copied) != "" || FirstTaskRecipe(&copied) != LearningDraftRecipe {
		t.Fatal("physical destination kept approval or lost restriction")
	}
	f.handler.workspaceStore = store
	if _, err := f.handler.ExecuteTask(t.Context(), "Robin", copied); err == nil || len(f.provider.requests) != 0 {
		t.Fatal("copied native review authorized execution", err)
	}
	fresh, _ := f.handler.RecipeExecutorDisclosure(copied)
	if fresh == nil {
		t.Fatal("ordinary destination review unavailable")
	}
	if err := f.handler.ReviewLearningExecutor(t.Context(), ws.ID, copied.ID, "Robin", fresh.Digest); err != nil {
		t.Fatal(err)
	}
	saved, err = store.Get(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	if token := LearningReviewedStart(saved.Tasks[0]); token == "" || token == oldToken {
		t.Fatal("explicit destination review reused a portable Start identity")
	}
	if _, err := f.handler.ExecuteTask(t.Context(), "Robin", copied); err != nil || len(f.provider.requests) != 1 {
		t.Fatal("import prevented separately approved bounded ordinary execution", err)
	}
}

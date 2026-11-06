package server

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/learningmissions"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestLearningDraftRecipeReadsOnlyCanonicalReviewedText(t *testing.T) {
	store := workspace.NewInMemoryStore()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "PRIVATE WORKSPACE NAME"})
	ws.SharedData = map[string]any{"secret": "PRIVATE WORKSPACE DATA"}
	task := workspace.Task{ID: "draft", WorkspaceID: ws.ID, Description: "UNREVIEWED TITLE", Details: "A workshop welcome"}
	if _, err := ws.AddRecipeTask(task, workspace.LearningDraftRecipe, 1); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}
	runner := learningDraftRunner{store: store}
	brief, err := runner.Brief(t.Context(), task, 0)
	if err != nil {
		t.Fatal(err)
	}
	var input map[string]string
	if err := json.Unmarshal([]byte(brief.User), &input); err != nil || len(input) != 1 || input["reviewed_input"] != task.Details {
		t.Fatalf("input projection: %s %v", brief.User, err)
	}
	for _, secret := range []string{ws.Name, "PRIVATE WORKSPACE DATA", task.Description, ws.ID} {
		if strings.Contains(brief.System+brief.User, secret) {
			t.Fatalf("unreviewed context leaked: %q", secret)
		}
	}
	// A copied payload cannot replace saved input or smuggle linked history.
	task.Details = "Copied, different input"
	if _, err := runner.Brief(t.Context(), task, 0); err == nil {
		t.Fatal("copied input accepted")
	}
	if _, err := runner.Finish(brief, strings.Repeat("x", learningmissions.MaxResultBytes+1)); err == nil {
		t.Fatal("oversized result accepted")
	}
	if err := store.Update(ws.ID, func(ws *workspace.Workspace) error {
		return ws.MutateTask(task.ID, func(saved *workspace.Task) error {
			saved.Context[workspace.TaskContextFirstTaskRecipeVersion] = 2
			return nil
		})
	}); err != nil {
		t.Fatal(err)
	}
	task.Details = "A workshop welcome"
	if runner.Ready(task) == nil {
		t.Fatal("future recipe version executed")
	}
}

package workspace

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestLearningOriginalIsReservedAndBoundToInputAndExecutionReview(t *testing.T) {
	original := &LearningDraftOriginal{TicketID: "original-ticket", RunID: "original-run", ArtifactID: "original-artifact", Digest: strings.Repeat("a", 64)}
	plain, err := LearningInputDigest(" correction ", nil)
	if err != nil {
		t.Fatal(err)
	}
	revised, err := LearningInputDigest("correction", original)
	if err != nil || revised == plain {
		t.Fatalf("original not bound: %v", err)
	}
	changed := *original
	changed.ArtifactID = "different-artifact"
	other, err := LearningInputDigest("correction", &changed)
	if err != nil || other == revised {
		t.Fatal("artifact replacement retained consent")
	}
	if !ReservedTaskContextKey(TaskContextLearningOriginal) {
		t.Fatal("public callers can supply original references")
	}
	task := Task{ID: "revision", WorkspaceID: "workspace", Details: "correction", Context: map[string]any{
		TaskContextFirstTaskRecipe: LearningDraftRecipe, TaskContextFirstTaskRecipeVersion: 1, TaskContextLearningOriginal: *original,
	}}
	typed := recipeExecutor(task, "writer", recipeProvider{})
	data, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Task
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if after := recipeExecutor(decoded, "writer", recipeProvider{}); after.Digest != typed.Digest {
		t.Fatal("reload changed the canonical original digest")
	}
	if got, err := LearningTaskInputDigest(decoded); err != nil || got != revised {
		t.Fatalf("input identity after reload: %s %v", got, err)
	}
	decoded.Context[TaskContextLearningOriginal] = changed
	if after := recipeExecutor(decoded, "writer", recipeProvider{}); after.Digest == typed.Digest {
		t.Fatal("original not covered by executor review")
	}
	for _, invalid := range []any{nil, "path", map[string]any{"path": "/private/file"}, LearningDraftOriginal{TicketID: "../private", RunID: "run", ArtifactID: "artifact", Digest: original.Digest}} {
		decoded.Context[TaskContextLearningOriginal] = invalid
		if _, err := LearningTaskInputDigest(decoded); err == nil {
			t.Fatalf("invalid reference accepted: %+v", invalid)
		}
	}
}

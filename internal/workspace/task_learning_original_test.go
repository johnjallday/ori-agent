package workspace

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestLearningOriginalIdentifierAllowlist(t *testing.T) {
	for r := rune(0); r < 128; r++ {
		id := "a" + string(r) + "b"
		ref := LearningDraftOriginal{TicketID: id, RunID: id, ArtifactID: id, Digest: strings.Repeat("a", 64)}
		want := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.'
		if ref.valid() != want {
			t.Fatalf("identifier rune %U: expected allowed=%t", r, want)
		}
	}
	for _, id := range []string{"", ".", "..", "café", "😀", strings.Repeat("a", 129)} {
		ref := LearningDraftOriginal{TicketID: id, RunID: "run", ArtifactID: "artifact", Digest: strings.Repeat("a", 64)}
		if ref.valid() {
			t.Fatalf("invalid identifier accepted: %q", id)
		}
	}
	ref := LearningDraftOriginal{TicketID: strings.Repeat("a", 128), RunID: "run", ArtifactID: "artifact", Digest: strings.Repeat("a", 64)}
	if !ref.valid() {
		t.Fatal("maximum-length safe identifier refused")
	}
}

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

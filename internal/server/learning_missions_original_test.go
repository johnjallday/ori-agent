package server

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacerun"
)

func TestLearningRevisionRecipeUsesExactOriginalAndCorrectionWithoutLearningAuthority(t *testing.T) {
	source, instance, db := learningSourceFixture(t)
	proof, err := source.Result(t.Context(), "local", instance)
	if err != nil {
		t.Fatal(err)
	}
	original := workspace.LearningDraftOriginal{TicketID: proof.Ref.TicketID, RunID: proof.Ref.RunID, ArtifactID: proof.Ref.ArtifactID, Digest: proof.Ref.Digest}
	input := "Add a specific opening time, 10:00."
	inputDigest, err := workspace.LearningInputDigest(input, &original)
	if err != nil {
		t.Fatal(err)
	}
	_, err = workspace.NewTicketService(source.workspaces).CreateLearningDraft(workspace.LearningDraftInput{
		TicketID: "revision", WorkspaceID: instance.Definition.WorkspaceID, Title: "Revise the welcome", Input: input, Original: &original,
		Binding: workspace.LearningTicketBinding{OwnerID: "local", InstanceID: "revision-instance", OperationID: "revision-work", Epoch: 1, InputDigest: inputDigest},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := source.workspaces.Update(instance.Definition.WorkspaceID, func(ws *workspace.Workspace) error {
		ws.SharedData = map[string]any{"private": "NEVER_SEND_SHARED_DATA"}
		return ws.MutateTask(original.TicketID, func(task *workspace.Task) error {
			task.CurrentRunID, task.Result = "unrelated-latest-run", "NEVER_SEND_LATEST_RESULT"
			return nil
		})
	}); err != nil {
		t.Fatal(err)
	}
	ws, err := source.workspaces.Get(instance.Definition.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	task, err := ws.GetTask("revision")
	if err != nil {
		t.Fatal(err)
	}
	runner := learningDraftRunner{store: source.workspaces, runs: source.runs}
	for _, clearLearning := range []bool{false, true} {
		if clearLearning {
			if _, err := db.ExecContext(t.Context(), `DELETE FROM learning_owners WHERE owner_id='local'`); err != nil {
				t.Fatal(err)
			}
		}
		brief, err := runner.Brief(t.Context(), *task, 0)
		if err != nil {
			t.Fatal(err)
		}
		var data map[string]string
		if err := json.Unmarshal([]byte(brief.User), &data); err != nil || len(data) != 2 || data["original_result"] != proof.Text || data["reviewed_correction"] != input {
			t.Fatalf("exact revision inputs: %s %v", brief.User, err)
		}
		if strings.Contains(brief.User, "NEVER_SEND") || strings.Contains(brief.User, original.RunID) || strings.Contains(brief.User, "revision-instance") {
			t.Fatal("unreviewed data reached prompt")
		}
	}
	// A second task_result is ambiguity, not permission to pick the latest.
	if _, err := source.runs.AddArtifact(t.Context(), ws.ID, original.RunID, workspacerun.NewArtifact(original.RunID, workspacerun.ArtifactFile,
		workspacerun.ArtifactInline([]byte("another result")), workspacerun.ArtifactMetadata(map[string]any{"role": "task_result"}))); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Brief(t.Context(), *task, 0); err == nil || runner.Ready(*task) == nil {
		t.Fatal("ambiguous original executed")
	}
}

package server

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/learningmissions"
	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacerun"
)

func learningSourceFixture(t *testing.T) (learningMissionSource, learningmissions.Instance, *database.DB) {
	t.Helper()
	db, err := database.Open(t.Context(), &database.Config{Path: filepath.Join(t.TempDir(), "learning.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	workspaces, err := workspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = workspaces.Close() })
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Practice"})
	ws.OwnerUserID = "local"
	if err := workspaces.Save(ws); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `INSERT INTO workspaces(id,name,created_at,updated_at) VALUES(?,'Practice',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, ws.ID); err != nil {
		t.Fatal(err)
	}
	learning := learningmissions.NewSQLiteStore(db)
	if _, err := learning.Change(t.Context(), "local", 0, 0, true, func(m *learningmissions.Mutation) error { return m.BindSource(ws.ID, "incarnation") }); err != nil {
		t.Fatal(err)
	}
	source := learningMissionSource{workspaces: workspaces, runs: workspacerun.NewSQLiteStore(db), learning: learning, recipeAvailable: func() bool { return true }, checkWorkspace: func(context.Context, string) error { return nil }}
	candidate, err := source.Candidate(t.Context(), "local", ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-time.Minute)
	i := learningmissions.Instance{ID: "mission", OwnerID: "local", Epoch: 1, Revision: 1, State: learningmissions.Active,
		Definition: learningmissions.Definition{Structure: learningmissions.DelegateInspect, Version: 1, WorkspaceID: ws.ID,
			Incarnation: candidate.Incarnation, CandidateRef: candidate.Ref, ScopeDigest: candidate.ScopeDigest,
			Goal: "Inspect a draft", Scope: "Only supplied text", Why: "Practice", Steps: []string{"scope", "execute", "review"}, Guidance: learningmissions.GuidanceBasic, ExpiresAt: at.Add(time.Hour).UnixMilli()},
		Progress: learningmissions.Progress{TicketID: "ticket", OperationID: "work", AttemptID: "attempt", RunID: "exact-run", Input: "Reviewed input",
			InputDigest: learningDigest([]byte("Reviewed input")), AcceptedAt: at.UnixMilli()}, CreatedAt: at.UnixMilli(), UpdatedAt: at.UnixMilli()}
	_, err = workspace.NewTicketService(workspaces).CreateLearningDraft(workspace.LearningDraftInput{TicketID: "ticket", WorkspaceID: ws.ID,
		Title: "Draft", Input: i.Progress.Input, Binding: workspace.LearningTicketBinding{OwnerID: "local", InstanceID: i.ID, OperationID: "work", Epoch: 1, InputDigest: i.Progress.InputDigest}})
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := workspaces.Get(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	task, err := fresh.GetTask("ticket")
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	raw := json.RawMessage(payload)
	config, err := json.Marshal(workspacerun.OriAgentExecutorConfig{TaskPayload: &raw})
	if err != nil {
		t.Fatal(err)
	}
	rawConfig := json.RawMessage(config)
	start, finish := at.Add(time.Second), at.Add(2*time.Second)
	run := &workspacerun.Run{ID: "exact-run", WorkspaceID: ws.ID, Scope: workspacerun.Scope{TargetTaskID: "ticket"},
		Executor: workspacerun.Executor{Kind: workspacerun.ExecutorKindOriAgent, Config: &rawConfig}, Status: workspacerun.RunStatusSucceeded,
		StartedAt: &start, FinishedAt: &finish, CreatedAt: at}
	if err := source.runs.CreateRun(t.Context(), run); err != nil {
		t.Fatal(err)
	}
	_, err = source.runs.AddArtifact(t.Context(), ws.ID, run.ID, workspacerun.NewArtifact(run.ID, workspacerun.ArtifactFile,
		workspacerun.ArtifactInline([]byte("The exact original result")), workspacerun.ArtifactMetadata(map[string]any{"role": "task_result"})))
	if err != nil {
		t.Fatal(err)
	}
	return source, i, db
}

func TestLearningMissionsCanonicalResultIgnoresLatestTicketText(t *testing.T) {
	source, i, _ := learningSourceFixture(t)
	if err := source.workspaces.Update(i.Definition.WorkspaceID, func(ws *workspace.Workspace) error {
		return ws.MutateTask(i.Progress.TicketID, func(task *workspace.Task) error {
			task.CurrentRunID, task.Result = "later-unrelated-run", "Not this practice result"
			task.ExecutionHistory = nil
			return nil
		})
	}); err != nil {
		t.Fatal(err)
	}
	proof, err := source.Result(t.Context(), "local", i)
	if err != nil || proof.Text != "The exact original result" || learningmissions.VerifyResult("local", i, proof) != nil {
		t.Fatalf("canonical proof: %+v %v", proof, err)
	}
	if _, err := source.Result(t.Context(), "foreign", i); !errors.Is(err, learningmissions.ErrUnavailable) {
		t.Fatalf("foreign proof: %v", err)
	}
}

func TestLearningMissionsCanonicalResultRefusesAmbiguityAndDeletedRegistration(t *testing.T) {
	source, i, db := learningSourceFixture(t)
	if _, err := source.runs.AddArtifact(t.Context(), i.Definition.WorkspaceID, i.Progress.RunID,
		workspacerun.NewArtifact(i.Progress.RunID, workspacerun.ArtifactFile, workspacerun.ArtifactInline([]byte("Another result")),
			workspacerun.ArtifactMetadata(map[string]any{"role": "task_result"}))); err != nil {
		t.Fatal(err)
	}
	if _, err := source.Result(t.Context(), "local", i); !errors.Is(err, learningmissions.ErrUnavailable) {
		t.Fatalf("ambiguous artifacts: %v", err)
	}
	if _, err := db.ExecContext(t.Context(), `DELETE FROM workspaces WHERE id=?`, i.Definition.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	// The folder can still exist, but the local registration no longer does.
	if err := source.Validate(t.Context(), "local", i.Definition); !errors.Is(err, learningmissions.ErrUnavailable) {
		t.Fatalf("deleted SQL registration kept authority: %v", err)
	}
}

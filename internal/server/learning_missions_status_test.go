package server

import (
	"context"
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/learningmissions"
	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacerun"
)

type statusOnlyRuns struct {
	workspacerun.Store
	bodyReads int
}

func (s *statusOnlyRuns) GetRun(context.Context, string, string) (*workspacerun.Run, error) {
	s.bodyReads++
	return nil, errors.New("body read forbidden")
}
func (s *statusOnlyRuns) ListArtifacts(context.Context, string, string) ([]workspacerun.Artifact, error) {
	s.bodyReads++
	return nil, errors.New("body read forbidden")
}
func (s *statusOnlyRuns) OutcomeMetadata(ctx context.Context, workspace, run string) (workspacerun.OutcomeMetadata, error) {
	return s.Store.(workspacerun.OutcomeMetadataReader).OutcomeMetadata(ctx, workspace, run)
}

func TestLearningMissionsStatusUsesOnlyMetadataAndRedactsDeletedPinnedSources(t *testing.T) {
	source, i, db := learningSourceFixture(t)
	proof, err := source.Result(t.Context(), "local", i)
	if err != nil {
		t.Fatal(err)
	}
	i.Progress.Result = &proof.Ref
	_, err = source.learning.Change(t.Context(), "local", 1, 1, false, func(m *learningmissions.Mutation) error {
		offer := i
		offer.State = learningmissions.Offered
		offer.Progress = learningmissions.Progress{}
		if err := m.InsertInstance(offer); err != nil {
			return err
		}
		i.Revision++
		return m.UpdateInstance(i, 1)
	})
	if err != nil {
		t.Fatal(err)
	}
	guard := &statusOnlyRuns{Store: source.runs}
	source.runs = guard
	service := learningmissions.NewService(source.learning, nil, source)
	for range 3 {
		status, err := service.Status(t.Context(), "local")
		if err != nil || len(status.Instances) != 1 || status.Instances[0].Progress.Blocker != "" || guard.bodyReads != 0 {
			t.Fatal("status loaded canonical contents", status, err, guard.bodyReads)
		}
	}
	if _, err := db.ExecContext(t.Context(), `DELETE FROM workspace_run_artifacts WHERE id=?`, proof.Ref.ArtifactID); err != nil {
		t.Fatal(err)
	}
	status, err := service.Status(t.Context(), "local")
	if err != nil || status.Instances[0].Progress.Blocker != "source_unavailable" || status.Instances[0].Progress.Input != "" || status.Instances[0].TicketHref != "" || guard.bodyReads != 0 {
		t.Fatal("deleted result leaked prior source details", status, err)
	}
}

func TestLearningMissionsLineageStatusReadsNoNotesOriginalInputOrResultBodies(t *testing.T) {
	source, i, db := learningSourceFixture(t)
	proof, err := source.Result(t.Context(), "local", i)
	if err != nil {
		t.Fatal(err)
	}
	r := learningmissions.Review{ID: "review", InstanceID: i.ID, Revision: i.Revision, Epoch: 1, Result: proof.Ref, InputDigest: i.Progress.InputDigest, Structure: learningmissions.DelegateInspect, Version: 1, Guidance: i.Definition.Guidance, CreatedAt: proof.FinishedAt + 1, ExpiresAt: proof.FinishedAt + 10000}
	_, err = source.learning.Change(t.Context(), "local", 1, 1, false, func(m *learningmissions.Mutation) error {
		if err := m.InsertReview(r); err != nil {
			return err
		}
		r.Decision, r.Reason, r.DecidedAt = "use", "meets_scope", r.CreatedAt
		return m.ConsumeReview(r)
	})
	if err != nil {
		t.Fatal(err)
	}
	base, err := source.Candidate(t.Context(), "local", i.Definition.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	candidate := learningReceiptCandidate(base, learningmissions.ImproveResult, r)
	i.ID = "revision-offer"
	i.State = learningmissions.Offered
	i.Progress = learningmissions.Progress{}
	i.Definition.Structure, i.Definition.Steps = learningmissions.ImproveResult, []string{"original", "correction", "execute", "compare"}
	i.Definition.OriginalReceipt, i.Definition.CandidateRef, i.Definition.ScopeDigest = r.ID, candidate.Ref, candidate.ScopeDigest
	if _, err := db.ExecContext(t.Context(), `UPDATE learning_reviews SET record=json_set(record,'$.note',json('{"private":"never decode"}'))`); err != nil {
		t.Fatal(err)
	}
	guard := &statusOnlyRuns{Store: source.runs}
	source.runs = guard
	if _, err := source.InstanceStatus(t.Context(), "local", i); err != nil || guard.bodyReads != 0 {
		t.Fatal("metadata projection depended on private original fields", err, guard.bodyReads)
	}
	if _, err := source.learning.ReviewMetadata(t.Context(), "foreign", r.ID); !errors.Is(err, learningmissions.ErrUnavailable) {
		t.Fatal("foreign receipt metadata", err)
	}
	if _, err := db.ExecContext(t.Context(), `DELETE FROM workspace_run_artifacts WHERE id=?`, proof.Ref.ArtifactID); err != nil {
		t.Fatal(err)
	}
	if _, err := source.InstanceStatus(t.Context(), "local", i); !errors.Is(err, learningmissions.ErrUnavailable) {
		t.Fatal("removed original remained available", err)
	}
}

func TestLearningMissionsPinnedSourceLifecycleMetadataFailsClosed(t *testing.T) {
	for _, change := range []string{"ticket_deleted", "owner_changed", "registration_deleted", "run_deleted", "artifact_replaced", "ambiguous_artifacts", "empty_artifact", "run_failed", "wrong_ticket", "executor_changed"} {
		t.Run(change, func(t *testing.T) {
			source, i, db := learningSourceFixture(t)
			proof, err := source.Result(t.Context(), "local", i)
			if err != nil {
				t.Fatal(err)
			}
			i.Progress.Result = &proof.Ref
			switch change {
			case "ticket_deleted":
				err = source.workspaces.Update(i.Definition.WorkspaceID, func(ws *workspace.Workspace) error { return ws.DeleteTask(i.Progress.TicketID) })
			case "owner_changed":
				err = source.workspaces.Update(i.Definition.WorkspaceID, func(ws *workspace.Workspace) error { ws.OwnerUserID = "foreign"; return nil })
			case "registration_deleted":
				_, err = db.ExecContext(t.Context(), `DELETE FROM workspaces WHERE id=?`, i.Definition.WorkspaceID)
			case "run_deleted":
				_, err = db.ExecContext(t.Context(), `DELETE FROM workspace_runs WHERE id=?`, i.Progress.RunID)
			case "artifact_replaced":
				_, err = db.ExecContext(t.Context(), `UPDATE workspace_run_artifacts SET id='replacement-artifact' WHERE id=?`, proof.Ref.ArtifactID)
			case "ambiguous_artifacts":
				_, err = source.runs.AddArtifact(t.Context(), i.Definition.WorkspaceID, i.Progress.RunID, workspacerun.NewArtifact(i.Progress.RunID, workspacerun.ArtifactFile, workspacerun.ArtifactInline([]byte("Another")), workspacerun.ArtifactMetadata(map[string]any{"role": "task_result"})))
			case "empty_artifact":
				_, err = db.ExecContext(t.Context(), `UPDATE workspace_run_artifacts SET inline=X'' WHERE id=?`, proof.Ref.ArtifactID)
			case "run_failed":
				_, err = db.ExecContext(t.Context(), `UPDATE workspace_runs SET status='failed' WHERE id=?`, i.Progress.RunID)
			case "wrong_ticket":
				_, err = db.ExecContext(t.Context(), `UPDATE workspace_runs SET scope_json=json_set(scope_json,'$.target_task_id','unrelated') WHERE id=?`, i.Progress.RunID)
			case "executor_changed":
				_, err = db.ExecContext(t.Context(), `UPDATE workspace_runs SET executor_json=json_set(executor_json,'$.kind','unrelated') WHERE id=?`, i.Progress.RunID)
			}
			if err != nil {
				t.Fatal(err)
			}
			guard := &statusOnlyRuns{Store: source.runs}
			source.runs = guard
			if _, err := source.InstanceStatus(t.Context(), "local", i); !errors.Is(err, learningmissions.ErrUnavailable) || guard.bodyReads != 0 {
				t.Fatal("unavailable source retained access", err, guard.bodyReads)
			}
		})
	}
}

func TestLearningMissionsMetadataIgnoresLatestPointersMovedSlugAndUnavailableAssignment(t *testing.T) {
	source, i, _ := learningSourceFixture(t)
	if err := source.workspaces.Update(i.Definition.WorkspaceID, func(ws *workspace.Workspace) error {
		ws.FolderSlug = "moved-practice"
		return ws.MutateTask(i.Progress.TicketID, func(task *workspace.Task) error {
			task.CurrentRunID = "later"
			task.Result = "latest text is not evidence"
			task.ExecutionHistory = nil
			task.To = "removed-agent"
			return nil
		})
	}); err != nil {
		t.Fatal(err)
	}
	guard := &statusOnlyRuns{Store: source.runs}
	source.runs = guard
	view, err := source.InstanceStatus(t.Context(), "local", i)
	if err != nil || !view.ResultReady || view.TicketHref != "/workspaces/moved-practice/task/ticket" || guard.bodyReads != 0 {
		t.Fatal("historical metadata became latest-pointer/assignment dependent", view, err)
	}
	if err := source.CheckWork(t.Context(), "local", i, "removed-agent"); !errors.Is(err, learningmissions.ErrUnavailable) {
		t.Fatal("missing assignee admitted new work", err)
	}
}

func TestLearningMissionsUnavailableExecutorDoesNotRevokeOwnedPastEvidence(t *testing.T) {
	source, i, _ := learningSourceFixture(t)
	source.recipeAvailable = func() bool { return false }
	if _, err := source.Candidate(t.Context(), "local", i.Definition.WorkspaceID); !errors.Is(err, learningmissions.ErrUnavailable) {
		t.Fatal("unavailable executor proposed work", err)
	}
	if _, err := source.Result(t.Context(), "local", i); err != nil {
		t.Fatal("lost read authority merely because executor is unavailable", err)
	}
	if err := source.CheckWork(t.Context(), "local", i, "unavailable"); !errors.Is(err, learningmissions.ErrUnavailable) {
		t.Fatal("unavailable executor admitted new work", err)
	}
}

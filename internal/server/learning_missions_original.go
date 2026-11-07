package server

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/johnjallday/ori-agent/internal/learningmissions"
	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacerun"
)

// readLearningOriginal is a canonical data read, not learning authority. It
// remains usable for a separately reviewed Ticket after learning is cleared.
// No latest pointer, path, history snippet or generated link is followed.
func readLearningOriginal(ctx context.Context, workspaces workspace.Store, runs workspacerun.Store, workspaceID string, original workspace.LearningDraftOriginal) (string, error) {
	if workspaces == nil || runs == nil {
		return "", learningmissions.ErrUnavailable
	}
	ws, err := workspaces.Get(workspaceID)
	if err != nil || ws == nil || ws.GetStatus() != workspace.StatusActive {
		return "", learningmissions.ErrUnavailable
	}
	ticket, err := ws.GetTask(original.TicketID)
	if err != nil || workspace.FirstTaskRecipe(ticket) != workspace.LearningDraftRecipe {
		return "", learningmissions.ErrUnavailable
	}
	run, err := runs.GetRun(ctx, workspaceID, original.RunID)
	if err != nil || run == nil || run.WorkspaceID != workspaceID || run.Scope.TargetTaskID != original.TicketID ||
		run.Status != workspacerun.RunStatusSucceeded || run.Executor.Kind != workspacerun.ExecutorKindOriAgent || run.StartedAt == nil || run.FinishedAt == nil || run.FinishedAt.Before(*run.StartedAt) {
		return "", learningmissions.ErrUnavailable
	}
	artifacts, err := runs.ListArtifacts(ctx, workspaceID, original.RunID)
	if err != nil {
		return "", learningmissions.ErrUnavailable
	}
	var selected *workspacerun.Artifact
	for n := range artifacts {
		artifact := &artifacts[n]
		if artifact.Kind != workspacerun.ArtifactFile || artifact.Metadata["role"] != "task_result" {
			continue
		}
		if selected != nil {
			return "", learningmissions.ErrUnavailable
		}
		selected = artifact
	}
	if selected == nil || selected.ID != original.ArtifactID || selected.RunID != original.RunID || len(selected.Inline) > learningmissions.MaxResultBytes ||
		!utf8.Valid(selected.Inline) || strings.TrimSpace(string(selected.Inline)) == "" || strings.ContainsRune(string(selected.Inline), 0) || learningDigest(selected.Inline) != original.Digest {
		return "", learningmissions.ErrUnavailable
	}
	return string(selected.Inline), nil
}

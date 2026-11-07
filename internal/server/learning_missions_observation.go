package server

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"

	"github.com/johnjallday/ori-agent/internal/learningmissions"
	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacerun"
)

func (s learningMissionSource) ObservedStart(ctx context.Context, owner, workspaceID, ticket, token string) (learningmissions.ObservedWork, error) {
	ws, err := s.owned(owner, workspaceID)
	if err != nil || s.learning == nil {
		return learningmissions.ObservedWork{}, learningmissions.ErrUnavailable
	}
	task, err := ws.GetTask(ticket)
	if err != nil || workspace.FirstTaskRecipe(task) != workspace.LearningDraftRecipe || workspace.LearningReviewedStart(*task) != token || token == "" {
		return learningmissions.ObservedWork{}, learningmissions.ErrUnavailable
	}
	approval, _ := task.Context[workspace.TaskContextReviewedExecutor].(string)
	original, originalErr := workspace.LearningOriginal(*task)
	version, versionErr := json.Marshal(task.Context[workspace.TaskContextFirstTaskRecipeVersion])
	inputDigest, inputErr := workspace.LearningTaskInputDigest(*task)
	if len(approval) != 64 || originalErr != nil || original != nil || versionErr != nil || string(version) != "1" || inputErr != nil {
		return learningmissions.ObservedWork{}, learningmissions.ErrUnavailable
	}
	incarnation, err := s.learning.SourceIncarnation(ctx, owner, workspaceID)
	if err != nil {
		return learningmissions.ObservedWork{}, learningmissions.ErrUnavailable
	}
	return learningmissions.ObservedWork{WorkspaceID: workspaceID, Incarnation: incarnation, TicketID: ticket, StartToken: token, InputDigest: inputDigest}, nil
}

func (s learningMissionSource) BindObservedStart(ctx context.Context, owner string, work learningmissions.ObservedWork) error {
	if work.Validate() != nil || s.tickets == nil {
		return learningmissions.ErrUnavailable
	}
	current, err := s.ObservedStart(ctx, owner, work.WorkspaceID, work.TicketID, work.StartToken)
	if err != nil || current.Incarnation != work.Incarnation || current.InputDigest != work.InputDigest {
		return learningmissions.ErrUnavailable
	}
	return s.tickets.BindLearningObservation(work.WorkspaceID, work.TicketID, workspace.LearningObservationBinding{OwnerID: owner, OperationID: work.ID, StartToken: work.StartToken})
}

func (s learningMissionSource) observedScope(ctx context.Context, owner string, work learningmissions.ObservedWork) (*workspace.Workspace, error) {
	if work.Validate() != nil || s.learning == nil {
		return nil, learningmissions.ErrUnavailable
	}
	ws, err := s.owned(owner, work.WorkspaceID)
	if err != nil {
		return nil, learningmissions.ErrUnavailable
	}
	incarnation, err := s.learning.SourceIncarnation(ctx, owner, ws.ID)
	task, taskErr := ws.GetTask(work.TicketID)
	if err != nil || incarnation != work.Incarnation || taskErr != nil || workspace.FirstTaskRecipe(task) != workspace.LearningDraftRecipe {
		return nil, learningmissions.ErrUnavailable
	}
	return ws, nil
}

func (s learningMissionSource) ObservedReady(ctx context.Context, owner string, work learningmissions.ObservedWork) (bool, error) {
	if _, err := s.observedScope(ctx, owner, work); err != nil {
		return false, err
	}
	reader, ok := s.runs.(workspacerun.OutcomeMetadataReader)
	if !ok || work.RunID == "" {
		return false, learningmissions.ErrUnavailable
	}
	result, err := reader.OutcomeMetadata(ctx, work.WorkspaceID, work.RunID)
	if err != nil || result.RunID != work.RunID || result.WorkspaceID != work.WorkspaceID || result.TicketID != work.TicketID ||
		result.Executor != workspacerun.ExecutorKindOriAgent || result.StartedAt == nil || result.FinishedAt == nil ||
		result.StartedAt.UnixMilli() < work.CreatedAt || result.FinishedAt.Before(*result.StartedAt) {
		return false, learningmissions.ErrUnavailable
	}
	return result.Status == workspacerun.RunStatusSucceeded && result.ResultCount == 1 && result.ResultBytes > 0 && result.ResultBytes <= learningmissions.MaxResultBytes, nil
}

func (s learningMissionSource) ObservedTicketLink(ctx context.Context, owner string, work learningmissions.ObservedWork) string {
	ws, err := s.observedScope(ctx, owner, work)
	if err != nil || ws.FolderSlug == "" {
		return ""
	}
	return "/workspaces/" + url.PathEscape(ws.FolderSlug) + "/task/" + url.PathEscape(work.TicketID)
}

func (s learningMissionSource) ObservedResult(ctx context.Context, owner string, work learningmissions.ObservedWork) (learningmissions.ResultProof, error) {
	unavailable := learningmissions.ErrUnavailable
	if s.runs == nil {
		return learningmissions.ResultProof{}, unavailable
	}
	ws, err := s.observedScope(ctx, owner, work)
	if err != nil {
		return learningmissions.ResultProof{}, unavailable
	}
	run, err := s.runs.GetRun(ctx, ws.ID, work.RunID)
	if err != nil || run == nil || run.ID != work.RunID || run.WorkspaceID != ws.ID || run.Scope.TargetTaskID != work.TicketID ||
		run.Executor.Kind != workspacerun.ExecutorKindOriAgent || run.StartedAt == nil || run.FinishedAt == nil {
		return learningmissions.ResultProof{}, unavailable
	}
	cfg, err := workspacerun.DecodeExecutorConfig[workspacerun.OriAgentExecutorConfig](run.Executor)
	if err != nil || cfg.TaskPayload == nil || len(*cfg.TaskPayload) > 128*1024 {
		return learningmissions.ResultProof{}, unavailable
	}
	var snapshot workspace.Task
	if json.Unmarshal(*cfg.TaskPayload, &snapshot) != nil || snapshot.ID != work.TicketID || snapshot.WorkspaceID != ws.ID || workspace.FirstTaskRecipe(&snapshot) != workspace.LearningDraftRecipe {
		return learningmissions.ResultProof{}, unavailable
	}
	binding, ok := workspace.LearningObservation(snapshot)
	inputDigest, inputErr := workspace.LearningTaskInputDigest(snapshot)
	original, originalErr := workspace.LearningOriginal(snapshot)
	version, versionErr := json.Marshal(snapshot.Context[workspace.TaskContextFirstTaskRecipeVersion])
	if !ok || binding.OwnerID != owner || binding.OperationID != work.ID || binding.StartToken != work.StartToken || inputErr != nil || inputDigest != work.InputDigest ||
		originalErr != nil || original != nil || versionErr != nil || string(version) != "1" {
		return learningmissions.ResultProof{}, unavailable
	}
	artifacts, err := s.runs.ListArtifacts(ctx, ws.ID, run.ID)
	if err != nil {
		return learningmissions.ResultProof{}, unavailable
	}
	var result *workspacerun.Artifact
	for n := range artifacts {
		a := &artifacts[n]
		if a.Kind != workspacerun.ArtifactFile || a.Metadata["role"] != "task_result" {
			continue
		}
		if result != nil || a.RunID != run.ID || a.ID == "" || len(a.Inline) == 0 || len(a.Inline) > learningmissions.MaxResultBytes {
			return learningmissions.ResultProof{}, unavailable
		}
		result = a
	}
	if result == nil {
		return learningmissions.ResultProof{}, unavailable
	}
	proof := learningmissions.ResultProof{OwnerID: owner, AttemptID: work.StartToken, InputDigest: inputDigest, InputText: strings.TrimSpace(snapshot.Details), Process: learningmissions.PlainDraftProcess,
		Status: string(run.Status), Text: string(result.Inline), StartedAt: run.StartedAt.UnixMilli(), FinishedAt: run.FinishedAt.UnixMilli(),
		Ref: learningmissions.ResultRef{WorkspaceID: ws.ID, Incarnation: work.Incarnation, TicketID: work.TicketID, RunID: run.ID, ArtifactID: result.ID, Digest: learningDigest(result.Inline)}}
	if err := learningmissions.VerifyObservedResult(owner, work, proof); err != nil {
		return learningmissions.ResultProof{}, err
	}
	return proof, nil
}

package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"strings"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/learningmissions"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacerun"
)

// learningMissionSource reads existing owners. It neither lists unrelated
// workspaces nor reads files/transcripts, resolves tools or executes a model.
type learningMissionSource struct {
	workspaces workspace.Store
	tickets    *workspace.TicketService
	runs       workspacerun.Store
	learning   *learningmissions.SQLiteStore
	// wired only when the bounded recipe is available on this server
	recipeAvailable func() bool
	checkAssignee   func(context.Context, string, string) error
	checkWorkspace  func(context.Context, string) error
}

func (s learningMissionSource) owned(owner, id string) (*workspace.Workspace, error) {
	if s.workspaces == nil || strings.TrimSpace(owner) == "" {
		return nil, learningmissions.ErrUnavailable
	}
	ws, err := s.workspaces.Get(id)
	if err != nil || ws == nil || ws.ID != id || ws.GetStatus() != workspace.StatusActive {
		return nil, learningmissions.ErrUnavailable
	}
	actual := ws.OwnerUserID
	if actual == "" {
		actual = userprofile.LocalUserID
	}
	if actual != owner {
		return nil, learningmissions.ErrUnavailable
	}
	return ws, nil
}

func (s learningMissionSource) Candidate(ctx context.Context, owner, id string) (learningmissions.Candidate, error) {
	if s.checkWorkspace == nil || s.checkWorkspace(ctx, id) != nil {
		return learningmissions.Candidate{}, learningmissions.ErrUnavailable
	}
	return s.candidate(ctx, owner, id)
}

func (s learningMissionSource) candidate(ctx context.Context, owner, id string) (learningmissions.Candidate, error) {
	ws, err := s.owned(owner, id)
	if err != nil || s.learning == nil || s.recipeAvailable == nil || !s.recipeAvailable() {
		return learningmissions.Candidate{}, learningmissions.ErrUnavailable
	}
	incarnation, err := s.learning.SourceIncarnation(ctx, owner, id)
	if err != nil {
		// A new inert candidate identity, persisted only by the explicit
		// proposal transaction. This read does not register a source.
		incarnation = uuid.NewString()
	}
	name := []rune(strings.TrimSpace(ws.Name))
	if len(name) > 120 {
		name = name[:120]
	}
	// This effect reads only explicit supplied text, never the workspace's
	// files or tools. Its scope is stable across unrelated Ticket updates.
	scope := learningDigest([]byte("learning-draft-v1\x00" + owner + "\x00" + id + "\x00" + incarnation))
	return learningmissions.Candidate{Ref: learningDigest([]byte("candidate\x00" + scope)), WorkspaceID: id,
		Incarnation: incarnation, ScopeDigest: scope, Name: string(name)}, nil
}

func (s learningMissionSource) Validate(ctx context.Context, owner string, d learningmissions.Definition) error {
	if d.Validate() != nil || s.learning == nil {
		return learningmissions.ErrUnavailable
	}
	incarnation, err := s.learning.SourceIncarnation(ctx, owner, d.WorkspaceID)
	if err != nil || incarnation != d.Incarnation {
		return learningmissions.ErrUnavailable
	}
	candidate, err := s.candidate(ctx, owner, d.WorkspaceID)
	if err != nil || candidate.Ref != d.CandidateRef || candidate.ScopeDigest != d.ScopeDigest {
		return learningmissions.ErrUnavailable
	}
	return nil
}

func (s learningMissionSource) Result(ctx context.Context, owner string, i learningmissions.Instance) (learningmissions.ResultProof, error) {
	unavailable := learningmissions.ErrUnavailable
	if s.runs == nil || s.Validate(ctx, owner, i.Definition) != nil || i.OwnerID != owner {
		return learningmissions.ResultProof{}, unavailable
	}
	ws, err := s.owned(owner, i.Definition.WorkspaceID)
	if err != nil {
		return learningmissions.ResultProof{}, unavailable
	}
	task, err := ws.GetTask(i.Progress.TicketID)
	if err != nil || workspace.FirstTaskRecipe(task) != workspace.LearningDraftRecipe {
		return learningmissions.ResultProof{}, unavailable
	}
	binding, ok := workspace.LearningBinding(*task)
	if !ok || binding.OwnerID != owner || binding.InstanceID != i.ID || binding.Epoch != i.Epoch ||
		binding.OperationID != i.Progress.OperationID || binding.InputDigest != i.Progress.InputDigest {
		return learningmissions.ResultProof{}, unavailable
	}
	run, err := s.runs.GetRun(ctx, ws.ID, i.Progress.RunID)
	if err != nil || run == nil || run.ID != i.Progress.RunID || run.WorkspaceID != ws.ID || run.Scope.TargetTaskID != task.ID ||
		run.Executor.Kind != workspacerun.ExecutorKindOriAgent || run.StartedAt == nil || run.FinishedAt == nil {
		return learningmissions.ResultProof{}, unavailable
	}
	cfg, err := workspacerun.DecodeExecutorConfig[workspacerun.OriAgentExecutorConfig](run.Executor)
	if err != nil || cfg.TaskPayload == nil || len(*cfg.TaskPayload) > 128*1024 {
		return learningmissions.ResultProof{}, unavailable
	}
	var snapshot workspace.Task
	if json.Unmarshal(*cfg.TaskPayload, &snapshot) != nil || snapshot.ID != task.ID || snapshot.WorkspaceID != ws.ID ||
		workspace.FirstTaskRecipe(&snapshot) != workspace.LearningDraftRecipe || learningDigest([]byte(strings.TrimSpace(snapshot.Details))) != binding.InputDigest {
		return learningmissions.ResultProof{}, unavailable
	}
	originalBinding, ok := workspace.LearningBinding(snapshot)
	if !ok || originalBinding != binding {
		return learningmissions.ResultProof{}, unavailable
	}
	artifacts, err := s.runs.ListArtifacts(ctx, ws.ID, run.ID)
	if err != nil {
		return learningmissions.ResultProof{}, unavailable
	}
	var result *workspacerun.Artifact
	for n := range artifacts {
		artifact := &artifacts[n]
		if artifact.Kind != workspacerun.ArtifactFile || artifact.Metadata["role"] != "task_result" {
			continue
		}
		if result != nil || artifact.RunID != run.ID || artifact.ID == "" || len(artifact.Inline) == 0 || len(artifact.Inline) > learningmissions.MaxResultBytes {
			return learningmissions.ResultProof{}, unavailable
		}
		result = artifact
	}
	if result == nil {
		return learningmissions.ResultProof{}, unavailable
	}
	return learningmissions.ResultProof{OwnerID: owner, AttemptID: i.Progress.AttemptID, InputDigest: binding.InputDigest,
		Status: string(run.Status), Text: string(result.Inline), StartedAt: run.StartedAt.UnixMilli(), FinishedAt: run.FinishedAt.UnixMilli(),
		Ref: learningmissions.ResultRef{WorkspaceID: ws.ID, Incarnation: i.Definition.Incarnation, TicketID: task.ID, RunID: run.ID,
			ArtifactID: result.ID, Digest: learningDigest(result.Inline)}}, nil
}

func (s learningMissionSource) CheckWork(ctx context.Context, owner string, i learningmissions.Instance, assignee string) error {
	ws, err := s.owned(owner, i.Definition.WorkspaceID)
	if err != nil || s.Validate(ctx, owner, i.Definition) != nil || !ws.HasAgent(assignee) || s.checkAssignee == nil {
		return learningmissions.ErrUnavailable
	}
	return s.checkAssignee(ctx, ws.ID, assignee)
}

func (s learningMissionSource) CreateTicket(ctx context.Context, owner string, i learningmissions.Instance) error {
	if s.tickets == nil || s.CheckWork(ctx, owner, i, i.Progress.Assignee) != nil {
		return learningmissions.ErrUnavailable
	}
	_, err := s.tickets.CreateLearningDraft(workspace.LearningDraftInput{
		TicketID: i.Progress.TicketID, WorkspaceID: i.Definition.WorkspaceID, Title: i.Definition.Goal,
		Input: i.Progress.Input, Assignee: i.Progress.Assignee,
		Binding: workspace.LearningTicketBinding{OwnerID: owner, InstanceID: i.ID, OperationID: i.Progress.OperationID,
			Epoch: i.Epoch, InputDigest: i.Progress.InputDigest},
	})
	return err
}

func (s learningMissionSource) MatchTicket(ctx context.Context, owner string, i learningmissions.Instance) error {
	ws, err := s.owned(owner, i.Definition.WorkspaceID)
	if err != nil || s.Validate(ctx, owner, i.Definition) != nil {
		return learningmissions.ErrUnavailable
	}
	task, err := ws.GetTask(i.Progress.TicketID)
	if err != nil || workspace.FirstTaskRecipe(task) != workspace.LearningDraftRecipe {
		return learningmissions.ErrUnavailable
	}
	binding, ok := workspace.LearningBinding(*task)
	if !ok || binding.OwnerID != owner || binding.InstanceID != i.ID || binding.Epoch != i.Epoch ||
		binding.OperationID != i.Progress.OperationID || binding.InputDigest != i.Progress.InputDigest {
		return learningmissions.ErrUnavailable
	}
	return nil
}

// Browser pages use the current canonical slug, never a guessed UUID URL or
// model-authored action link. Missing/deleted Tickets have no usable link.
func (s learningMissionSource) TicketLink(ctx context.Context, owner string, i learningmissions.Instance) string {
	if i.Progress.TicketID == "" || s.MatchTicket(ctx, owner, i) != nil {
		return ""
	}
	ws, err := s.owned(owner, i.Definition.WorkspaceID)
	if err != nil || ws.FolderSlug == "" {
		return ""
	}
	return "/workspaces/" + url.PathEscape(ws.FolderSlug) + "/task/" + url.PathEscape(i.Progress.TicketID)
}

func learningDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

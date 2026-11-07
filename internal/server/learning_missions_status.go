package server

import (
	"context"
	"net/url"

	"github.com/johnjallday/ori-agent/internal/learningmissions"
	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacerun"
)

// InstanceStatus is an availability projection only. Neither it nor its exact
// original-reference check loads Run payloads, artifacts, input or review notes.
// Every action and explicit evidence inspection still verifies canonical bytes.
func (s learningMissionSource) InstanceStatus(ctx context.Context, owner string, i learningmissions.Instance) (learningmissions.InstanceAvailability, error) {
	var out learningmissions.InstanceAvailability
	unavailable := learningmissions.ErrUnavailable
	if i.OwnerID != owner || i.Definition.Validate() != nil {
		return out, unavailable
	}
	d := i.Definition
	base, err := s.scopeCandidate(ctx, owner, d.WorkspaceID)
	if err != nil || base.Incarnation != d.Incarnation {
		return out, unavailable
	}
	ws, err := s.owned(owner, d.WorkspaceID)
	if err != nil {
		return out, unavailable
	}
	if d.Structure != learningmissions.DelegateInspect {
		prior, err := s.learning.ReviewMetadata(ctx, owner, d.OriginalReceipt)
		if err != nil || prior.Result.WorkspaceID != d.WorkspaceID || prior.Result.Incarnation != d.Incarnation || (d.Structure == learningmissions.RepeatSuccess && prior.Decision != "use") {
			return out, unavailable
		}
		if _, err := ws.GetTask(prior.Result.TicketID); err != nil {
			return out, unavailable
		}
		result, err := s.resultMetadata(ctx, d.WorkspaceID, prior.Result.TicketID, prior.Result.RunID)
		if err != nil || !readyLearningMetadata(result) || result.ResultArtifactID != prior.Result.ArtifactID {
			return out, unavailable
		}
		base = learningReceiptCandidateRef(base, d.Structure, prior).Candidate
	}
	if base.Ref != d.CandidateRef || base.ScopeDigest != d.ScopeDigest {
		return out, unavailable
	}
	if i.Progress.TicketID == "" {
		return out, nil
	}
	ticket, err := ws.GetTask(i.Progress.TicketID)
	if err != nil || workspace.FirstTaskRecipe(ticket) != workspace.LearningDraftRecipe {
		return out, unavailable
	}
	binding, ok := workspace.LearningBinding(*ticket)
	if !ok || binding.OwnerID != owner || binding.InstanceID != i.ID || binding.Epoch != i.Epoch || binding.OperationID != i.Progress.OperationID || binding.InputDigest != i.Progress.InputDigest {
		return out, unavailable
	}
	if ws.FolderSlug != "" {
		out.TicketHref = "/workspaces/" + url.PathEscape(ws.FolderSlug) + "/task/" + url.PathEscape(ticket.ID)
	}
	if i.Progress.RunID != "" {
		result, err := s.resultMetadata(ctx, ws.ID, ticket.ID, i.Progress.RunID)
		out.ResultReady = err == nil && readyLearningMetadata(result) && result.StartedAt.UnixMilli() >= i.Progress.AcceptedAt
		if i.Progress.Result != nil && (!out.ResultReady || result.ResultArtifactID != i.Progress.Result.ArtifactID) {
			return learningmissions.InstanceAvailability{}, unavailable
		}
	}
	return out, nil
}

func (s learningMissionSource) resultMetadata(ctx context.Context, workspaceID, ticket, run string) (workspacerun.OutcomeMetadata, error) {
	reader, ok := s.runs.(workspacerun.OutcomeMetadataReader)
	if !ok {
		return workspacerun.OutcomeMetadata{}, learningmissions.ErrUnavailable
	}
	out, err := reader.OutcomeMetadata(ctx, workspaceID, run)
	if err != nil || out.WorkspaceID != workspaceID || out.TicketID != ticket || out.RunID != run {
		return workspacerun.OutcomeMetadata{}, learningmissions.ErrUnavailable
	}
	return out, nil
}

func readyLearningMetadata(out workspacerun.OutcomeMetadata) bool {
	return out.Executor == workspacerun.ExecutorKindOriAgent && out.Status == workspacerun.RunStatusSucceeded && out.StartedAt != nil && out.FinishedAt != nil &&
		!out.FinishedAt.Before(*out.StartedAt) && out.ResultCount == 1 && out.ResultArtifactID != "" && out.ResultBytes > 0 && out.ResultBytes <= learningmissions.MaxResultBytes
}

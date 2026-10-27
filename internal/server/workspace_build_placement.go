package server

import (
	"context"
	"net/url"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/sessionhttp"
)

// Resolve the observation service at call time, as ordinary build folders do:
// the assistant conversation service is wired later than the workspace handler.
type inboxBuildPlacement struct{ builder *ServerBuilder }

func (p inboxBuildPlacement) Handoff(ctx context.Context, owner string, input personalassistant.InboxPlacementInput, seed personalassistant.InboxPlacementSeed) (personalassistant.InboxPlacementResult, error) {
	service := buildFolderObservations(p).service()
	if service == nil || p.builder.workspaceBuildStore == nil {
		return personalassistant.InboxPlacementResult{}, personalassistant.ErrRepairNeeded
	}
	return service.HandoffInbox(ctx, owner, input, p.builder.workspaceBuildStore, seed)
}

func (p inboxBuildPlacement) Acquire(ctx context.Context, owner string, build personalassistant.WorkspaceBuildSession) (func(), error) {
	service := buildFolderObservations(p).service()
	if service == nil {
		return nil, personalassistant.ErrRepairNeeded
	}
	return service.AcquireInboxPlacement(ctx, owner, build)
}

// This adapter receives the preparation owner's verified observation, never a
// browser receipt or the default source plan. It records facts only: no first
// task, provider call, scan, dossier production or attachment selection.
func (p inboxBuildPlacement) RecordPreparedOutcome(ctx context.Context, owner string, build personalassistant.WorkspaceBuildSession, observed sessionhttp.WorkspaceBuildPreparationObservation) error {
	if p.builder == nil || p.builder.personalAssistantFolderDigest == nil || observed.State != "prepared" ||
		build.Preparation == nil || observed.WorkspaceID != build.Preparation.WorkspaceID || observed.FolderSlug == "" ||
		observed.Preparation == nil || observed.Preparation.Result == nil {
		return personalassistant.ErrFolderWorkspaceRefused
	}
	scan := observed.Preparation.Result
	route := "/workspaces/" + url.PathEscape(observed.FolderSlug)
	batchRoute := route + "?panel=file-janitor&preparation=" + url.QueryEscape(observed.Preparation.OperationID)
	if scan.BatchID != "" {
		batchRoute += "&batch_id=" + url.QueryEscape(scan.BatchID)
	}
	batch := &personalassistant.FolderFirstBatch{Outcome: scan.Outcome, BatchID: scan.BatchID,
		Eligible: scan.EligibleCount, Ineligible: scan.IneligibleCount, NeedsReview: observed.NeedsReview, Route: batchRoute}
	rows := []personalassistant.FolderReceiptRow{
		{Kind: "workspace", Name: observed.Name, Route: route, Operation: "create"},
		{Kind: "folder", Name: observed.FolderName, Detail: "Linked, not moved; top-level metadata only."},
	}
	for _, name := range observed.Members {
		rows = append(rows, personalassistant.FolderReceiptRow{Kind: "agent", Name: name, Assigned: true})
	}
	rows = append(rows, personalassistant.FolderReceiptRow{Kind: "batch", Name: "First batch", Route: batchRoute,
		Detail: inboxBatchDetail(batch.Outcome, batch.Eligible, batch.NeedsReview)})
	outcome := personalassistant.FolderOutcome{Kind: personalassistant.FolderChoiceProject,
		WorkspaceID: observed.WorkspaceID, Route: route, Blueprint: "file-janitor", Receipt: rows,
		FirstLook: &personalassistant.FolderFirstLookRecord{Batch: batch, At: scan.CompletedAt}}
	return p.builder.personalAssistantFolderDigest.RecordInboxPlacementOutcome(ctx, owner, build, outcome)
}

package sessionhttp

import (
	"context"
	"strings"

	"github.com/johnjallday/ori-agent/internal/filejanitor"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type buildPreparationFailure struct{ code, message, stage string }

type verifiedBuildWorkspace struct {
	workspace *workspace.Workspace
	terms     filejanitor.SetupReviewTerms
}

// Shared by explicit continuation and observational recovery. Neither a reserved
// UUID nor an identically named workspace establishes this operation's ownership.
func (h *Handler) preparedBuildWorkspace(owner string, build personalassistant.WorkspaceBuildSession, janitor *filejanitor.Service) (*verifiedBuildWorkspace, *buildPreparationFailure) {
	claim := build.Preparation
	refuse := func(code, message, stage string) (*verifiedBuildWorkspace, *buildPreparationFailure) {
		return nil, &buildPreparationFailure{code, message, stage}
	}
	if claim == nil || h.workspaceTaskStore == nil || janitor == nil {
		return refuse("creation_unconfirmed", "The accepted workspace cannot be confirmed. Keep this build for reconciliation; it will not create another workspace.", "creation")
	}
	primary, primaryErr := h.workspaceTaskStore.Get(claim.WorkspaceID)
	if primaryErr != nil || primary == nil {
		return refuse("creation_unconfirmed", "The accepted workspace cannot be confirmed. Keep this build for reconciliation; it will not create another workspace.", "creation")
	}
	primaryOwner := strings.TrimSpace(primary.OwnerUserID)
	if primaryOwner == "" {
		primaryOwner = userprofile.LocalUserID
	}
	if (primary.Status != "" && primary.Status != workspace.StatusActive) || primaryOwner != owner {
		return refuse("creation_changed", "The accepted workspace is no longer active or owned by you. No setup was resumed.", "creation")
	}
	ws, err := h.readReviewedWorkspace(claim.WorkspaceID)
	if err != nil || ws == nil {
		return refuse("creation_unconfirmed", "The accepted workspace cannot be confirmed. Keep this build for reconciliation; it will not create another workspace.", "creation")
	}
	workspaceOwner := strings.TrimSpace(ws.OwnerUserID)
	if workspaceOwner == "" {
		workspaceOwner = userprofile.LocalUserID
	}
	prov := ws.GetTemplateProvenance()
	if workspaceOwner != owner || (ws.Status != "" && ws.Status != workspace.StatusActive) || prov == nil || prov.AssistantSetup == nil ||
		prov.AssistantSetup.RunID != build.ID || prov.AssistantSetup.OperationID != claim.ID || prov.AssistantSetup.ReviewDigest != claim.ReviewRevision {
		return refuse("creation_changed", "The accepted workspace is unavailable or its authority changed. No setup was resumed.", "creation")
	}
	marker := prov.AssistantSetup
	teamRevision, teamErr := h.preparedWorkspaceTeamDigest(ws)
	if teamErr != nil || teamRevision != marker.TeamRevision {
		return refuse("team_changed", "The saved workspace team changed. Open the workspace and review its current setup.", "folder")
	}
	install, found := ws.GetInstalledCapability(workspace.CapabilityFileJanitor)
	install.OwnedResources = nil
	capabilityRevision, digestErr := digestJSON(install)
	if !found || digestErr != nil || capabilityRevision != marker.CapabilityRevision {
		return refuse("setup_changed", "File Janitor was removed or changed. Open the workspace's current settings; the old review cannot restore access.", "folder")
	}
	ref, err := ws.GetDirectoryReference(marker.FolderReferenceID)
	if err != nil || ref == nil {
		return refuse("folder_changed", "The accepted folder link changed. Open this workspace and review its current folder.", "folder")
	}
	root, err := janitor.ReviewSelectedSetup(ws.ID, ref.Path, prov.AutomationRecipes)
	termsRevision, digestErr := digestJSON(root.Terms)
	if err != nil || digestErr != nil || root.Revision != claim.RootRevision || termsRevision != claim.TermsRevision {
		return refuse("folder_changed", "The folder or monitoring terms changed. Open this workspace for a fresh permission review.", "folder")
	}
	return &verifiedBuildWorkspace{workspace: ws, terms: root.Terms}, nil
}

// WorkspaceBuildPreparationObservation is a read-only projection, not an accepted
// request or permission to resume. Callers supply an owner-scoped stored build.
// Result is included only after the domain verifies the original scan receipt.
type WorkspaceBuildPreparationObservation struct {
	State                string                              `json:"state"`
	Goal                 string                              `json:"goal,omitempty"`
	Terms                *filejanitor.SetupReviewTerms       `json:"terms,omitempty"`
	FolderPath           string                              `json:"folder_path,omitempty"`
	Message              string                              `json:"message,omitempty"`
	WorkspaceID          string                              `json:"workspace_id,omitempty"`
	FolderSlug           string                              `json:"folder_slug,omitempty"`
	Name                 string                              `json:"name,omitempty"`
	Members              []string                            `json:"members,omitempty"`
	FolderName           string                              `json:"folder_name,omitempty"`
	NeedsReview          int                                 `json:"needs_review,omitempty"`
	SourceReceiptPending bool                                `json:"source_receipt_pending,omitempty"`
	Preparation          *filejanitor.SetupPreparationResult `json:"preparation,omitempty"`
}

func (h *Handler) ObserveWorkspaceBuildPreparation(owner string, build personalassistant.WorkspaceBuildSession) WorkspaceBuildPreparationObservation {
	view := WorkspaceBuildPreparationObservation{State: "reconciliation_required"}
	if h.workspaceBuild == nil || h.workspaceBuild.FileJanitor == nil {
		view.Message = "The saved preparation cannot be read. No work was continued."
		return view
	}
	janitor, _ := h.workspaceBuild.FileJanitor()
	verified, failure := h.preparedBuildWorkspace(owner, build, janitor)
	if failure != nil {
		view.Message = failure.message
		return view
	}
	ws := verified.workspace
	slug := h.preparedBuildFolderSlug(owner, build)
	if slug == "" {
		view.Message = "The saved workspace route cannot be verified. No work was continued."
		return view
	}
	view.WorkspaceID, view.FolderSlug, view.Name = ws.ID, slug, ws.Name
	view.Goal, view.Terms = ws.Mission, &verified.terms
	for _, member := range ws.AgentInstances {
		view.Members = append(view.Members, member.Name)
	}
	claim := build.Preparation
	view.State, view.Message = "incomplete", "The workspace exists, but this preparation has not finished. Continuing requires an explicit action."
	marker := ws.GetTemplateProvenance().AssistantSetup
	ref, err := ws.GetDirectoryReference(marker.FolderReferenceID)
	if err != nil || ref == nil {
		view.State, view.Message = "reconciliation_required", "The saved folder reference is unavailable. No work was continued."
		return view
	}
	view.FolderName, view.FolderPath = ref.Name, ref.Path
	if claim.LinkedReviewRevision == "" {
		return view
	}
	review, err := janitor.ReviewLinkedSetup(ws.ID, owner, marker.FolderReferenceID, claim.Monitoring)
	if err != nil || review.Revision != claim.LinkedReviewRevision || review.Monitoring != claim.Monitoring ||
		(review.State != "prepared" && review.State != "preparation_incomplete") {
		view.State, view.Message = "reconciliation_required", "The original preparation or its result could not be verified. Open the workspace's current setup; do not create another workspace."
		return view
	}
	if review.State == "prepared" && review.OperationID != "" && review.Result != nil {
		if review.Result.Outcome == filejanitor.ScanOutcomeBatch {
			batches, err := janitor.ListBatches(ws.ID)
			found := false
			for _, batch := range batches {
				if batch.ID == review.Result.BatchID && batch.WorkspaceID == ws.ID {
					view.NeedsReview = batch.Summary.NeedsReview
					found = true
					break
				}
			}
			if err != nil || !found || view.NeedsReview < 0 || view.NeedsReview > review.Result.EligibleCount {
				view.State, view.Message = "reconciliation_required", "The original batch is no longer available. Nothing was rescanned."
				return view
			}
		}
		view.State, view.Message = "prepared", "The original filing review is saved. No work was continued."
		view.Preparation = &filejanitor.SetupPreparationResult{WorkspaceID: ws.ID, OperationID: review.OperationID, Stage: "result", Result: review.Result}
	}
	return view
}

// Recording a verified source receipt is projection bookkeeping, not a setup
// continuation. Keep it outside the pure observer and outside build-store locks.
func (h *Handler) recordObservedBuildPreparation(ctx context.Context, owner string, build personalassistant.WorkspaceBuildSession, view *WorkspaceBuildPreparationObservation) {
	if build.PlacementSource == nil || view.State != "prepared" {
		return
	}
	recorder, ok := h.workspaceBuild.Placement.(interface {
		RecordPreparedOutcome(context.Context, string, personalassistant.WorkspaceBuildSession, WorkspaceBuildPreparationObservation) error
	})
	view.SourceReceiptPending = !ok
	if ok {
		view.SourceReceiptPending = recorder.RecordPreparedOutcome(ctx, owner, build, *view) != nil
	}
}

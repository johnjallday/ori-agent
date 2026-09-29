package projectlibrary

import (
	"errors"
	"sort"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// ManagedPortfolioBridge lets the existing Home portfolio UI use the same
// reviewed library field owner after an explicit authority switch. It never
// writes Portfolio.Projects again. The host injects installed-provider
// evidence so direct reviewed service calls cannot bypass HTTP's policy.
type ManagedPortfolioBridge struct{ library *Store }

func NewManagedPortfolioBridge(store workspace.Store) *ManagedPortfolioBridge {
	return &ManagedPortfolioBridge{library: NewStore(store)}
}

func (b *ManagedPortfolioBridge) WithProviderEvidence(check func(Scope, *workspace.Workspace) bool) *ManagedPortfolioBridge {
	b.library.WithProviderEvidence(check)
	return b
}

func (b *ManagedPortfolioBridge) snapshot(stationID string) (Scope, Document, *workspace.AssistantProgramState, error) {
	if b == nil || b.library == nil || b.library.workspaces == nil || stationID == "" {
		return Scope{}, Document{}, nil, workspace.ErrAssistantPortfolioInvalid
	}
	home, err := b.library.workspaces.Get(stationID)
	if err != nil || home == nil || home.GetAssistantProgramState() == nil {
		return Scope{}, Document{}, nil, workspace.ErrAssistantStationNotFound
	}
	key := home.GetAssistantProgramState().Key.Normalize()
	scope := Scope{HomeID: stationID, OwnerUserID: key.OwnerUserID,
		ProviderID: key.PluginID, ProgramID: key.ProgramID}
	doc, state, err := b.library.readSnapshot(scope)
	if err != nil {
		return Scope{}, Document{}, nil, workspace.ErrAssistantPortfolioLibraryOwned
	}
	return scope, doc, state, nil
}

func portfolioBridgeFields(fields Fields) workspace.AssistantPortfolioUpdate {
	update := workspace.AssistantPortfolioUpdate{Status: fields.Status,
		SessionDate: fields.SessionDate, ReleaseDate: fields.ReleaseDate,
		Blockers:           append([]string(nil), fields.Blockers...),
		Deliverables:       append([]string(nil), fields.Deliverables...),
		ArchiveReviewState: fields.ArchiveReviewState}
	if fields.Priority != nil {
		update.Priority = *fields.Priority
	}
	for _, milestone := range fields.Milestones {
		update.Milestones = append(update.Milestones, workspace.AssistantPortfolioMilestone{
			ID: milestone.ID, Label: milestone.Label,
			DueDate: milestone.DueDate, Complete: milestone.Complete})
	}
	return update
}

func portfolioBridgePatch(update workspace.AssistantPortfolioUpdate) FieldsPatch {
	milestones := make([]Milestone, 0, len(update.Milestones))
	for _, milestone := range update.Milestones {
		milestones = append(milestones, Milestone{ID: milestone.ID, Label: milestone.Label,
			DueDate: milestone.DueDate, Complete: milestone.Complete})
	}
	priority := update.Priority
	return FieldsPatch{Status: &update.Status, Priority: &priority,
		SessionDate: &update.SessionDate, ReleaseDate: &update.ReleaseDate,
		Milestones: &milestones, Blockers: &update.Blockers,
		Deliverables: &update.Deliverables, ArchiveReviewState: &update.ArchiveReviewState}
}

func (b *ManagedPortfolioBridge) linked(scope Scope, doc Document, state *workspace.AssistantProgramState,
	linkID string) (Entry, *workspace.Workspace, error) {
	var found *Entry
	for i := range doc.Entries {
		entry := &doc.Entries[i]
		if entry.Link != nil && entry.Link.LinkID == linkID {
			if found != nil {
				return Entry{}, nil, workspace.ErrAssistantPortfolioConflict
			}
			found = entry
		}
	}
	if found == nil {
		return Entry{}, nil, workspace.ErrAssistantPortfolioLinkNotFound
	}
	listed := false
	for _, id := range state.LinkedProjectIDs {
		listed = listed || id == found.Link.WorkspaceID
	}
	if !listed || found.Link.LinkID != workspace.AssistantProjectLinkID(scope.HomeID, found.Link.WorkspaceID) {
		return Entry{}, nil, workspace.ErrAssistantPortfolioConflict
	}
	child, err := b.library.workspaces.Get(found.Link.WorkspaceID)
	if err != nil || child == nil || child.OwnerUserID != scope.OwnerUserID ||
		child.Status == workspace.StatusTrashed || child.Status == workspace.StatusMissing {
		return Entry{}, nil, workspace.ErrAssistantPortfolioConflict
	}
	link := child.GetAssistantProjectLink()
	key := workspace.AssistantProgramKey{OwnerUserID: scope.OwnerUserID,
		PluginID: scope.ProviderID, ProgramID: scope.ProgramID}
	if link == nil || link.ID != found.Link.LinkID || link.StationWorkspaceID != scope.HomeID ||
		link.StateRevision != found.Link.Revision || link.Key.Normalize() != key.Normalize() {
		return Entry{}, nil, workspace.ErrAssistantPortfolioConflict
	}
	if mirror, ok := b.library.workspaces.(workspace.MirrorWorkspaceProvider); ok {
		folder, mirrored, mirrorErr := mirror.GetMirrorWorkspace(child.ID)
		if mirrored {
			if mirrorErr != nil || folder == nil || folder.OwnerUserID != child.OwnerUserID {
				return Entry{}, nil, workspace.ErrAssistantPortfolioConflict
			}
			folderLink := folder.GetAssistantProjectLink()
			if folderLink == nil || folderLink.ID != link.ID || folderLink.StateRevision != link.StateRevision ||
				folderLink.StationWorkspaceID != link.StationWorkspaceID || folderLink.Key.Normalize() != link.Key.Normalize() {
				return Entry{}, nil, workspace.ErrAssistantPortfolioConflict
			}
		}
	}
	return *found, child, nil
}

func (b *ManagedPortfolioBridge) List(stationID string) ([]workspace.AssistantPortfolioProjectProjection, error) {
	scope, doc, state, err := b.snapshot(stationID)
	if err != nil {
		return nil, err
	}
	projections := make([]workspace.AssistantPortfolioProjectProjection, 0, len(state.LinkedProjectIDs))
	legacy := workspace.NewAssistantPortfolioService(b.library.workspaces)
	for _, id := range state.LinkedProjectIDs {
		linkID := workspace.AssistantProjectLinkID(scope.HomeID, id)
		entry, child, linkErr := b.linked(scope, doc, state, linkID)
		if errors.Is(linkErr, workspace.ErrAssistantPortfolioLinkNotFound) {
			// A normal creator can durably attach a reciprocal child after the
			// Home library is initialized. It belongs on the separate, reviewed
			// pending-link shelf until the owner associates it; legacy portfolio
			// reads must not hide the Home or silently adopt its catalog identity.
			continue
		}
		if linkErr != nil {
			return nil, workspace.ErrAssistantPortfolioConflict
		}
		projections = append(projections, legacy.ProjectProjection(child, linkID, doc.Revision,
			portfolioBridgeFields(entry.Fields)))
	}
	sort.Slice(projections, func(i, j int) bool {
		if projections[i].ProjectName == projections[j].ProjectName {
			return projections[i].LinkID < projections[j].LinkID
		}
		return projections[i].ProjectName < projections[j].ProjectName
	})
	return projections, nil
}

func (b *ManagedPortfolioBridge) Review(stationID, linkID string, expectedRevision int64,
	update workspace.AssistantPortfolioUpdate) (*workspace.AssistantPortfolioReview, error) {
	scope, doc, state, err := b.snapshot(stationID)
	if err != nil {
		return nil, err
	}
	if expectedRevision != doc.Revision {
		return nil, workspace.ErrAssistantPortfolioConflict
	}
	entry, child, err := b.linked(scope, doc, state, linkID)
	if err != nil {
		return nil, err
	}
	review, err := b.library.ReviewFields(scope, entry.ID, entry.Fields.Revision,
		portfolioBridgePatch(update), scope.OwnerUserID)
	if err != nil {
		return nil, managedPortfolioError(err)
	}
	projection := workspace.NewAssistantPortfolioService(b.library.workspaces).ProjectProjection(child, linkID,
		expectedRevision, update)
	return &workspace.AssistantPortfolioReview{Token: review.Token, ExpiresAt: review.ExpiresAt,
		Project: projection}, nil
}

func (b *ManagedPortfolioBridge) Commit(stationID, token, key string,
	update workspace.AssistantPortfolioUpdate) (*workspace.AssistantPortfolioReceipt, error) {
	scope, doc, state, err := b.snapshot(stationID)
	if err != nil {
		return nil, err
	}
	var review *ReviewReceipt
	for i := range doc.Reviews {
		if doc.Reviews[i].Token == token && doc.Reviews[i].Action == "edit_fields" {
			review = &doc.Reviews[i]
			break
		}
	}
	if review == nil {
		return nil, workspace.ErrAssistantPortfolioReviewExpired
	}
	var entry *Entry
	for i := range doc.Entries {
		if doc.Entries[i].ID == review.TargetID {
			entry = &doc.Entries[i]
			break
		}
	}
	if entry == nil || entry.Link == nil {
		return nil, workspace.ErrAssistantPortfolioConflict
	}
	_, _, err = b.linked(scope, doc, state, entry.Link.LinkID)
	if err != nil {
		return nil, err
	}
	changed, replay, err := b.library.commitFields(scope, entry.ID, token, key, review.FieldsRevision,
		portfolioBridgePatch(update), scope.OwnerUserID,
		func(current *workspace.AssistantProgramState, _ *workspace.Workspace) bool {
			_, _, linkErr := b.linked(scope, doc, current, entry.Link.LinkID)
			return linkErr == nil
		})
	if err != nil {
		return nil, managedPortfolioError(err)
	}
	committed, err := b.library.Read(scope)
	if err != nil || changed.Link == nil {
		return nil, workspace.ErrAssistantPortfolioConflict
	}
	for _, op := range committed.Operations {
		if op.Key == key && op.Action == "edit_fields" && op.ConsequenceID == entry.ID {
			return &workspace.AssistantPortfolioReceipt{LinkID: changed.Link.LinkID,
				ProjectWorkspaceID: changed.Link.WorkspaceID, StateRevision: op.Revision,
				RecordedAt: op.RecordedAt, Replayed: replay}, nil
		}
	}
	return nil, workspace.ErrAssistantPortfolioConflict
}

func managedPortfolioError(err error) error {
	switch {
	case errors.Is(err, ErrUnavailable), errors.Is(err, ErrMirrorDiverged), errors.Is(err, ErrNotInitialized):
		return workspace.ErrAssistantPortfolioLibraryOwned
	case errors.Is(err, ErrConflict):
		return workspace.ErrAssistantPortfolioConflict
	case errors.Is(err, ErrLimit), errors.Is(err, ErrCorrupt):
		return workspace.ErrAssistantPortfolioInvalid
	default:
		return err
	}
}

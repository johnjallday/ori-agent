package server

import (
	"context"
	"errors"
	"strings"

	"github.com/johnjallday/ori-agent/internal/economy"
	"github.com/johnjallday/ori-agent/internal/featureflags"
	"github.com/johnjallday/ori-agent/internal/filejanitor"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacecapability"
)

func (b *ServerBuilder) filingExpansionQuote(ctx context.Context) economy.ExpansionQuote {
	if store := b.filingClaimStore(); store != nil {
		claim, err := store.FindFilingClaim(ctx, userprofile.LocalUserID, "")
		if err != nil {
			return emptyFilingQuote(economy.ExpansionUnavailable)
		}
		if claim != nil {
			return b.quoteFilingClaim(ctx, claim)
		}
	}
	return b.quoteNewFiling(ctx)
}

// quoteNewFiling excludes persisted purchase claims. The caller always checks
// those first, so a replay can never become a second price check or purchase.
func (b *ServerBuilder) quoteNewFiling(ctx context.Context) economy.ExpansionQuote {
	quote := emptyFilingQuote(economy.ExpansionDisabled)
	if !featureflags.EconomyEnabled() {
		return quote
	}
	quote.State = economy.ExpansionUnavailable
	if b.personalHQService == nil {
		return quote
	}
	hq, err := b.personalHQService.Status(ctx, userprofile.LocalUserID)
	if err != nil {
		return quote
	}
	if !hq.Valid {
		quote.State = economy.ExpansionNeedsHQ
		return quote
	}
	destinations, err := b.filingExpansionDestinations(ctx, userprofile.LocalUserID)
	if err != nil {
		return quote
	}
	priced, err := b.economyService.QuoteFilingExpansion(ctx)
	if err == nil {
		quote = priced
	}
	if quote.State == economy.ExpansionInsufficient && hq.Workspace != nil && workspace.IsCanonicalWorkspaceSlug(hq.Workspace.FolderSlug) {
		quote.ManualEarnURL = "/workspaces/" + hq.Workspace.FolderSlug + "?panel=tasks"
	}
	if len(destinations) != 0 {
		quote.ManualEarnURL = ""
		quote.State = economy.ExpansionBuilt
		quote.EffectiveCost = nil
		quote.Missing = 0
		quote.Destinations = destinations
	}
	return quote
}

func (b *ServerBuilder) filingExpansionDestinations(ctx context.Context, owner string) ([]economy.ExpansionDestination, error) {
	if b.sessionStore == nil || b.workspaceFileStore == nil || b.fileJanitorService == nil {
		return nil, errors.New("Filing workspace storage is unavailable")
	}
	rows, err := b.sessionStore.ListWorkspaces(ctx)
	if err != nil {
		return nil, err
	}
	return existingFilingDestinations(rows, b.workspaceFileStore, owner, b.fileJanitorService.HasConfiguredSetup)
}

// existingFilingDestinations first restricts the canonical metadata search to
// live, owner-visible registrations. An ordinary workspace named Downloads,
// an anchor, and the blueprint's suggested path are not evidence. We never
// inspect a selected root to decide if a workspace is a match.
func existingFilingDestinations(rows []session.Workspace, folders starterWorkspaceSource, owner string, configured func(string) (bool, error)) ([]economy.ExpansionDestination, error) {
	cached := folders.CachedWorkspaces()
	var matches []economy.ExpansionDestination
	for _, row := range rows {
		if row.OwnerUserID != owner || row.IsGroup() || (row.Status != "" && row.Status != session.WorkspaceStatusActive) {
			continue
		}
		lean := cached[row.ID]
		if !visibleFilingRecord(lean, row, owner) {
			return nil, errors.New("registered workspace metadata is not available")
		}
		match, err := matchesExistingFiling(lean, configured)
		if err != nil {
			return nil, err
		}
		if !match {
			continue
		}
		// Prove the candidate still exists rather than linking to a stale cache
		// entry; a missing/contradictory record is not a reason to sell another.
		live, err := folders.Get(row.ID)
		if err != nil || !visibleFilingRecord(live, row, owner) || !workspace.IsCanonicalWorkspaceSlug(row.FolderSlug) {
			return nil, errors.New("Filing workspace route is not available")
		}
		match, err = matchesExistingFiling(live, configured)
		if err != nil {
			return nil, err
		}
		if match {
			matches = append(matches, economy.ExpansionDestination{
				Name: row.Name, Route: "/workspaces/" + row.FolderSlug + "?panel=file-janitor",
			})
		}
	}
	return matches, nil
}

func visibleFilingRecord(ws *workspace.Workspace, row session.Workspace, owner string) bool {
	return ws != nil && ws.ID == row.ID && ws.FolderSlug == row.FolderSlug &&
		ws.GetStatus() == workspace.StatusActive && !strings.EqualFold(ws.Kind, "group") && ownedBy(ws, owner)
}

func matchesExistingFiling(ws *workspace.Workspace, configured func(string) (bool, error)) (bool, error) {
	if ws.CapabilityWasRemoved(workspace.CapabilityFileJanitor) {
		return false, nil
	}
	if ws.HasInstalledCapability(workspace.CapabilityFileJanitor) {
		return true, nil
	}
	if provenance := ws.GetTemplateProvenance(); provenance != nil && provenance.Builtin &&
		(ws.IsFromTemplate(fileJanitorBlueprintID) || ws.IsFromTemplate(filejanitor.LegacyTemplateID)) {
		return true, nil
	}
	setup := workspacecapability.FileJanitorDefinition().Setup
	for _, req := range ws.PendingDirectoryRequirements() {
		if setup.MatchesDirectoryRequirementKey(req.Key) {
			return true, nil
		}
	}
	return configured(ws.ID)
}

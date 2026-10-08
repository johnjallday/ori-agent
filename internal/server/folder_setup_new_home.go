package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func (h *folderSetupHost) newHomeDestination(ctx context.Context, userID string, provider reviewedintegration.HomeProvider) (*personalassistant.FolderSetupDestination, error) {
	if h == nil || h.builder == nil || h.builder.sessionHandler == nil {
		return nil, errSetupUnavailable
	}
	entry, err := h.builder.sessionHandler.PortfolioHomeTemplate(ctx, provider.PluginID)
	if err != nil || entry.Representative == nil || entry.Representative.AssistantProgram == nil || entry.Representative.ProgramHomeOwner == nil {
		return nil, errSetupUnavailable
	}
	owner, decl := entry.Representative.ProgramHomeOwner, entry.Representative.AssistantProgram
	if !owner.Valid() || owner.PluginID != provider.PluginID || owner.ProgramID != provider.ProgramID || decl.ID != provider.ProgramID {
		return nil, errSetupUnavailable
	}
	name := strings.TrimSpace(entry.ProposedGroupName)
	if name == "" {
		name = strings.TrimSpace(decl.StationName)
	}
	data, err := json.Marshal(struct {
		Owner       workspace.AssistantProgramHomeOwner
		Declaration *workspace.AssistantProgramDeclaration
	}{*owner, decl})
	if err != nil {
		return nil, errSetupUnavailable
	}
	sum := sha256.Sum256(data)
	destination := &personalassistant.FolderSetupDestination{Status: "new", Name: name, Kind: "home", OwnerUserID: userID, ProviderPlugin: owner.PluginID, ProviderVersion: owner.PluginVersion, ProgramID: owner.ProgramID, DeclarationDigest: owner.DeclarationDigest, CompatibilityHash: hex.EncodeToString(sum[:])}
	if destination.Validate() != nil {
		return nil, errSetupUnavailable
	}
	return destination, nil
}

func (h *folderSetupHost) validateNewHomeDestination(ctx context.Context, req personalassistant.FolderSetupRequest, expected *personalassistant.FolderSetupDestination) error {
	var provider *reviewedintegration.HomeProvider
	for _, item := range reviewedintegration.HomeProviders() {
		if item.PluginID == expected.ProviderPlugin && item.ProgramID == expected.ProgramID {
			copy := item
			provider = &copy
		}
	}
	if provider == nil || expected.OwnerUserID != req.UserID {
		return personalassistant.ErrFolderPlanChanged
	}
	home, err := h.station(req.UserID, *provider)
	if err != nil {
		return personalassistant.ErrFolderPlanChanged
	}
	if home == nil {
		actual, err := h.newHomeDestination(ctx, req.UserID, *provider)
		if err != nil || *actual != *expected {
			return personalassistant.ErrFolderPlanChanged
		}
		return nil
	}
	// A timestamp or matching name cannot establish that this run created the
	// Home. Only its persisted progress receipt may identify the resulting parent.
	if h.builder.personalAssistantFolderDigest == nil {
		return personalassistant.ErrFolderPlanChanged
	}
	id, err := h.builder.personalAssistantFolderDigest.SetupHomeIdentity(ctx, req.UserID, req.Offer.ID, req.Plan.Digest)
	if err != nil || id == "" || id != home.ID {
		return personalassistant.ErrFolderPlanChanged
	}
	actual, err := folderSetupHomeDestination(home)
	if err != nil {
		return personalassistant.ErrFolderPlanChanged
	}
	actual.Status, actual.WorkspaceID, actual.RecordVersion, actual.ProgramRevision = "new", "", 0, 0
	if *actual != *expected {
		return personalassistant.ErrFolderPlanChanged
	}
	return nil
}

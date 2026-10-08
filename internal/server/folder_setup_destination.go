package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Material Home evidence is read fresh, never taken from cached software facts
// or inferred from the active page/name. The typed provider owner carries the
// host-validated Home declaration/schema/version and contribution fingerprint.
func folderSetupHomeDestination(home *workspace.Workspace) (*personalassistant.FolderSetupDestination, error) {
	if home == nil || home.OwnerUserID == "" || home.Kind != "group" || home.Status == workspace.StatusTrashed || home.Status == workspace.StatusMissing {
		return nil, errSetupUnavailable
	}
	state := home.GetAssistantProgramState()
	if state == nil || state.SchemaVersion != workspace.AssistantProgramStateSchemaVersion || state.HomeProvider == nil || !state.HomeProvider.Valid() || state.Declaration == nil ||
		state.Key.OwnerUserID != home.OwnerUserID || state.Key.PluginID != state.HomeProvider.PluginID || state.Key.ProgramID != state.HomeProvider.ProgramID || state.Declaration.ID != state.Key.ProgramID {
		return nil, errSetupUnavailable
	}
	data, err := json.Marshal(struct {
		Owner       workspace.AssistantProgramHomeOwner
		Declaration *workspace.AssistantProgramDeclaration
	}{*state.HomeProvider, state.Declaration})
	if err != nil {
		return nil, errSetupUnavailable
	}
	sum := sha256.Sum256(data)
	destination := &personalassistant.FolderSetupDestination{
		Status: "existing", WorkspaceID: home.ID, Name: home.Name, Kind: "home", ParentID: home.ParentID,
		OwnerUserID: home.OwnerUserID, RecordVersion: home.Version, ProgramRevision: state.StateRevision,
		ProviderPlugin: state.HomeProvider.PluginID, ProviderVersion: state.HomeProvider.PluginVersion,
		ProgramID: state.HomeProvider.ProgramID, DeclarationDigest: state.HomeProvider.DeclarationDigest,
		CompatibilityHash: hex.EncodeToString(sum[:]),
	}
	if destination.Validate() != nil {
		return nil, errSetupUnavailable
	}
	return destination, nil
}

func (h *folderSetupHost) ReadSetupDestination(ctx context.Context, req personalassistant.FolderSetupRequest) (*personalassistant.FolderSetupDestination, error) {
	if h == nil || h.builder == nil {
		return nil, errSetupUnavailable
	}
	requestedID := ""
	if review := req.Offer.ConversationReview; review != nil {
		requestedID = review.DestinationID
		if review.Operation == personalassistant.FolderOperationSupport {
			if h.builder.workspaceStore == nil || requestedID == "" {
				return nil, errSetupUnavailable
			}
			ws, err := h.builder.workspaceStore.Get(requestedID)
			if err != nil || ws == nil || ws.Kind == "group" {
				return nil, errSetupUnavailable
			}
			return folderSetupWorkspaceDestination(ws, req.UserID)
		}
	}
	var provider *reviewedintegration.HomeProvider
	if req.Offer.Portfolio != nil {
		for _, candidate := range reviewedintegration.HomeProviders() {
			if candidate.Key == req.Offer.Portfolio.ProviderKey {
				value := candidate
				provider = &value
				break
			}
		}
		if provider == nil {
			return nil, errSetupUnavailable
		}
	} else {
		target, err := h.target(req.Offer)
		if err != nil {
			// Unsupported generic placement remains undisclosed, not adopted
			// into HQ or a Home merely because of the active page.
			if requestedID != "" {
				if h.builder.workspaceStore == nil {
					return nil, errSetupUnavailable
				}
				ws, err := h.builder.workspaceStore.Get(requestedID)
				if err != nil || ws == nil || ws.Kind != "group" || ws.GetAssistantProgramState() != nil {
					return nil, errSetupUnavailable
				}
				return folderSetupWorkspaceDestination(ws, req.UserID)
			}
			return nil, nil
		}
		provider = target.provider
		if provider == nil {
			return &personalassistant.FolderSetupDestination{Status: "standalone"}, nil
		}
	}
	home, err := h.station(req.UserID, *provider)
	if err != nil {
		return nil, err
	}
	if home == nil {
		if requestedID != "" {
			return nil, errSetupUnavailable
		}
		return h.newHomeDestination(ctx, req.UserID, *provider)
	}
	if requestedID != "" && home.ID != requestedID {
		return nil, errSetupUnavailable
	}
	return folderSetupHomeDestination(home)
}

func (h *folderSetupHost) ValidateSetupDestination(ctx context.Context, req personalassistant.FolderSetupRequest) error {
	expected := req.Plan.Destination
	if expected == nil {
		return nil
	} // historical line-only plans retain their old guards
	if expected.Validate() != nil || h == nil || h.builder == nil {
		return personalassistant.ErrFolderPlanChanged
	}
	if expected.Status == "new" {
		return h.validateNewHomeDestination(ctx, req, expected)
	}
	if expected.Status != "existing" {
		if expected.Status != "standalone" || req.Offer.Portfolio != nil {
			return personalassistant.ErrFolderPlanChanged
		}
		// No existing Home is silently selected for a witnessed standalone plan.
		target, err := h.target(req.Offer)
		if err != nil || target.provider != nil {
			return personalassistant.ErrFolderPlanChanged
		}
		return nil
	}
	var provider *reviewedintegration.HomeProvider
	if req.Offer.Portfolio != nil {
		target, err := h.portfolioTarget(req.Offer)
		if err != nil {
			return personalassistant.ErrFolderPlanChanged
		}
		provider = &target.provider
	} else {
		target, err := h.target(req.Offer)
		if err != nil || target.provider == nil {
			return personalassistant.ErrFolderPlanChanged
		}
		provider = target.provider
	}
	home, err := h.station(req.UserID, *provider)
	if err != nil {
		return personalassistant.ErrFolderPlanChanged
	}
	actual, err := folderSetupHomeDestination(home)
	if err != nil {
		return personalassistant.ErrFolderPlanChanged
	}
	if req.Offer.Setup != nil {
		// This run's own staffing/consent steps bump record revisions. They do
		// not permit a changed Home ID/name/parent/owner/provider/declaration.
		actual.RecordVersion, actual.ProgramRevision = expected.RecordVersion, expected.ProgramRevision
	}
	if *actual != *expected {
		return personalassistant.ErrFolderPlanChanged
	}
	return nil
}

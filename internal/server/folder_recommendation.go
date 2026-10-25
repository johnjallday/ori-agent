package server

import (
	"context"
	"errors"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// ReadFolderRecommendation reads installed metadata and the canonical Home.
// Release verification, staffing, models and grants belong to explicit review.
func (h *folderSetupHost) ReadFolderRecommendation(ctx context.Context, userID, providerKey string) personalassistant.FolderRecommendationDestination {
	result := personalassistant.FolderRecommendationDestination{State: "unknown", Readiness: "unavailable"}
	if h == nil || h.builder == nil {
		return result
	}
	for _, provider := range reviewedintegration.HomeProviders() {
		if provider.Key != providerKey {
			continue
		}
		home, err := h.station(userID, provider)
		if err != nil {
			if errors.Is(err, workspace.ErrAssistantStationAmbiguous) {
				result.Readiness = "destination_ambiguous"
			}
			return result
		}
		if home != nil {
			destination, err := folderSetupHomeDestination(home)
			if err != nil {
				result.Readiness = "destination_incompatible"
				return result
			}
			result.State, result.Name = "existing", destination.Name
		} else {
			result.State = "absent"
		}
		if h.builder.pluginHandler == nil {
			return result
		}
		installed, err := h.builder.pluginHandler.Manager().List()
		if err != nil {
			return result
		}
		result.Readiness = "provider_missing"
		for _, item := range installed {
			if item.Name != provider.PluginID {
				continue
			}
			switch {
			case !item.Enabled:
				result.Readiness = "provider_disabled"
			case !reviewedintegration.AtLeast(item.Version, provider.MinimumVersion):
				result.Readiness = "provider_outdated"
			default:
				result.Readiness = "review"
				if home != nil && !plugin.IndependentHomeProviderEvidenceAvailable(installed, home.GetAssistantProgramState().HomeProvider) {
					result.Readiness = "destination_incompatible"
				}
				// Template compatibility is a local host declaration read. A
				// present but incompatible provider cannot imply a new Home.
				if home == nil {
					if _, err := h.newHomeDestination(ctx, userID, provider); err != nil {
						result.Readiness = "unavailable"
					}
				}
			}
			return result
		}
		return result
	}
	return result
}

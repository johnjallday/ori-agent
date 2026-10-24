package agenthttp

import (
	"context"
	"net/http"
	"strings"

	"github.com/johnjallday/ori-agent/internal/assistantdiscovery"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
)

// writeFolderPlacementChoice is the placement answer the drawer renders: the
// host's sentence, the operations it offers, and any guided setup it attaches.
// Actions are presentation for this response only; none is stored.
func writeFolderPlacementChoice(w http.ResponseWriter, candidateID, subject, message string, options []folderPlacementOption, actions []HomeAction, readiness ...*personalassistant.FolderSetupVisual) {
	choice := map[string]any{"candidate_id": candidateID, "subject": subject, "message": message, "options": options}
	if len(actions) > 0 {
		choice["actions"] = actions
	}
	if len(readiness) == 1 && readiness[0] != nil {
		choice["readiness"] = personalassistant.CloneFolderSetupVisual(readiness[0])
	}
	orihttp.WriteJSON(w, map[string]any{"folder_placement_choice": choice})
}

// placementBlocker is what blocks the selected placement, when the host can
// name it. The bound read names it when no destination was requested. When the
// handler asked for the page's own workspace by default (a create with no
// destination chosen, as on Home), that read is a plain miss; the unbound read,
// which looked for the Home the project needs, is the one that knows. A
// supporting-folder link needs no Home, so it is never blamed on one.
func placementBlocker(selected, unbound personalassistant.FolderOfferView, operation string) *personalassistant.FolderDestinationBlocker {
	if selected.DestinationBlocker != nil {
		return selected.DestinationBlocker
	}
	if operation == personalassistant.FolderOperationCreate && unbound.DestinationStatus == "unavailable" {
		return unbound.DestinationBlocker
	}
	return nil
}

// folderDestinationBlockedChoice is the placement answer when a reviewed
// folder's destination is unavailable because the integration that provides it
// is not set up. It names that integration from the registry and attaches the
// same Guided Setup button a conversation turn attaches; once there is nothing
// left to set up for it, the Home provider is what is missing and is named
// without a button. Without a blocker, or with keys the registry does not
// list, it says nothing and the generic sentence stands.
func (h *HomeAssistantAskHandler) folderDestinationBlockedChoice(ctx context.Context, userID string, blocker *personalassistant.FolderDestinationBlocker) (string, []HomeAction, bool) {
	if blocker == nil {
		return "", nil, false
	}
	if integration, ok := h.reviewedIntegrationByKey(ctx, userID, blocker.IntegrationKey); ok {
		if action, ok := openSetupQuestAction(integration); ok {
			text := "Setting this folder up needs Ori's reviewed " + integration.DisplayName + " integration. " +
				reviewedIntegrationState(integration) +
				" The button below opens its Guided Setup; review the folder again once that setup is finished."
			return text, []HomeAction{action}, true
		}
	}
	if provider, ok := homeProviderByKey(blocker.HomeProviderKey); ok {
		return "Setting this folder up needs the Home that the " + provider.DisplayName + " plugin provides, and that plugin is not ready here. No setup has been prepared.", nil, true
	}
	return "", nil, false
}

// reviewedIntegrationByKey reads one reviewed integration's current state
// through the same brokered reader the assistant uses. A host without the
// reader, a reader that cannot answer, or a key it does not list is not found.
func (h *HomeAssistantAskHandler) reviewedIntegrationByKey(ctx context.Context, userID, key string) (assistantdiscovery.ReviewedIntegration, bool) {
	key = strings.ToLower(strings.TrimSpace(key))
	reader := configuredReviewedIntegrations(h)
	if reader == nil || key == "" {
		return assistantdiscovery.ReviewedIntegration{}, false
	}
	result := reader.ReviewedIntegrations(ctx, userID)
	if result.Status != string(assistantdiscovery.Available) {
		return assistantdiscovery.ReviewedIntegration{}, false
	}
	for _, integration := range result.Integrations {
		if strings.ToLower(strings.TrimSpace(integration.Key)) == key {
			return integration, true
		}
	}
	return assistantdiscovery.ReviewedIntegration{}, false
}

// homeProviderByKey is the reviewed Home provider with this key, when the
// registry lists one with a name to show.
func homeProviderByKey(key string) (reviewedintegration.HomeProvider, bool) {
	key = strings.ToLower(strings.TrimSpace(key))
	if key == "" {
		return reviewedintegration.HomeProvider{}, false
	}
	for _, provider := range reviewedintegration.HomeProviders() {
		if strings.ToLower(strings.TrimSpace(provider.Key)) == key && strings.TrimSpace(provider.DisplayName) != "" {
			return provider, true
		}
	}
	return reviewedintegration.HomeProvider{}, false
}

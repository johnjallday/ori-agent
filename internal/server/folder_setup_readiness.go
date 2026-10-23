package server

import (
	"context"
	"time"

	"github.com/johnjallday/ori-agent/internal/foldersetup"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

type cachedSetupReadiness struct {
	at     time.Time
	visual *personalassistant.FolderSetupVisual
}

// ReadSetupReadiness describes independent prerequisites when a complete plan
// cannot be built (for example, the Home's installed blueprint is missing).
// Only explicit/current review reads call this seam, never attachment's
// HireCandidate/ReviewOptions. It cannot create a journey, destination or plan.
func (h *folderSetupHost) ReadSetupReadiness(ctx context.Context, req personalassistant.FolderSetupRequest) *personalassistant.FolderSetupVisual {
	if h == nil || req.Offer.Portfolio != nil || req.Offer.Structure != nil || req.Offer.CapabilitySuppressed ||
		(req.Offer.ConversationReview != nil && req.Offer.ConversationReview.Operation == personalassistant.FolderOperationSupport) {
		return nil
	}
	target, err := h.target(req.Offer)
	if err != nil {
		return nil // no plugin requirement is imposed on built-in/unknown recipes
	}
	key := req.UserID + "|" + target.entry.Key
	h.mu.Lock()
	cached, known := h.readiness[key]
	plan, planned := h.cache[key]
	h.mu.Unlock()
	if known && time.Since(cached.at) < planFactsTTL {
		return personalassistant.CloneFolderSetupVisual(cached.visual)
	}
	visual := &personalassistant.FolderSetupVisual{RequirementsChecked: true}
	if planned && time.Since(plan.at) < planFactsTTL {
		visual.Plugins = append(visual.Plugins, foldersetup.PluginVisual(personalassistant.FolderPlanIntegration, plan.facts.Integration))
		if plan.facts.Provider != nil {
			visual.Plugins = append(visual.Plugins, foldersetup.PluginVisual(personalassistant.FolderPlanProvider, *plan.facts.Provider))
		}
	} else {
		integration, integrationErr := h.integrationFacts(ctx, req.UserID, target.entry)
		visual.Plugins = append(visual.Plugins, foldersetup.PluginVisual(personalassistant.FolderPlanIntegration, integration))
		visual.RequirementsChecked = integrationErr == nil
		// Failure of one prerequisite must not hide the other known requirement.
		if target.provider != nil {
			provider, providerErr := h.providerFacts(ctx, *target.provider)
			visual.Plugins = append(visual.Plugins, foldersetup.PluginVisual(personalassistant.FolderPlanProvider, provider))
			visual.RequirementsChecked = visual.RequirementsChecked && providerErr == nil
		}
	}
	visual = personalassistant.CloneFolderSetupVisual(visual)
	h.mu.Lock()
	if h.readiness == nil {
		h.readiness = map[string]cachedSetupReadiness{}
	}
	h.readiness[key] = cachedSetupReadiness{at: time.Now(), visual: visual}
	h.mu.Unlock()
	return personalassistant.CloneFolderSetupVisual(visual)
}

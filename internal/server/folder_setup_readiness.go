package server

import (
	"context"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/foldersetup"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

// InvalidateSetupReadiness is used only by explicit, unaccepted review. It
// invalidates observations, not an accepted run, destination or consent.
func (h *folderSetupHost) InvalidateSetupReadiness(userID string) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.epoch++
	for key := range h.cache {
		if strings.HasPrefix(key, userID+"|") {
			delete(h.cache, key)
		}
	}
	for key := range h.readiness {
		if strings.HasPrefix(key, userID+"|") {
			delete(h.readiness, key)
		}
	}
}

type cachedSetupReadiness struct {
	at     time.Time
	visual *personalassistant.FolderSetupVisual
}

// ReadSetupReadiness describes independent prerequisites when a complete plan
// cannot be built (for example, the Home's installed blueprint is missing).
// Only explicit/current review reads call this seam, never attachment's
// HireCandidate/ReviewOptions. It cannot create a journey, destination or plan.
func (h *folderSetupHost) ReadSetupReadiness(ctx context.Context, req personalassistant.FolderSetupRequest) *personalassistant.FolderSetupVisual {
	library := req.Offer.Portfolio != nil && req.Offer.Structure != nil && req.Offer.Structure.Group.Type == personalassistant.FolderGroupLibrary
	if h == nil || ((req.Offer.Portfolio != nil || req.Offer.Structure != nil) && !library) || req.Offer.CapabilitySuppressed ||
		(req.Offer.ConversationReview != nil && req.Offer.ConversationReview.Operation == personalassistant.FolderOperationSupport) {
		return nil
	}
	target, err := h.prerequisiteTarget(req.Offer)
	if err != nil {
		return nil // no plugin requirement is imposed on built-in/unknown recipes
	}
	withModel := func(visual *personalassistant.FolderSetupVisual) *personalassistant.FolderSetupVisual {
		visual = personalassistant.CloneFolderSetupVisual(visual)
		if visual == nil {
			return nil
		}
		integration := foldersetup.Plugin{}
		for _, row := range visual.Plugins {
			if row.Kind == personalassistant.FolderPlanIntegration {
				integration.State = row.Operation
			}
		}
		if library {
			if home, err := h.station(req.UserID, *target.provider); err == nil && (home == nil || !homeStaffed(home)) {
				visual.Model = h.portfolioModel()
			}
		} else {
			_, _, visual.Model = h.pluginHire(req.UserID, target, integration)
		}
		return personalassistant.CloneFolderSetupVisual(visual)
	}
	key := req.UserID + "|" + target.entry.Key
	if library {
		key += "|library:" + target.provider.Key
	}
	h.mu.Lock()
	cached, known := h.readiness[key]
	plan, planned := h.cache[key]
	epoch := h.epoch
	h.mu.Unlock()
	if known && time.Since(cached.at) < planFactsTTL {
		return withModel(cached.visual)
	}
	visual := &personalassistant.FolderSetupVisual{RequirementsChecked: true}
	if planned && time.Since(plan.at) < planFactsTTL {
		visual.Plugins = append(visual.Plugins, foldersetup.PluginVisual(personalassistant.FolderPlanIntegration, plan.facts.Integration))
		if plan.facts.Provider != nil {
			visual.Plugins = append(visual.Plugins, foldersetup.PluginVisual(personalassistant.FolderPlanProvider, *plan.facts.Provider))
		}
	} else {
		if target.entry.Key != "" {
			integration, integrationErr := h.integrationFacts(ctx, req.UserID, target.entry)
			visual.Plugins = append(visual.Plugins, foldersetup.PluginVisual(personalassistant.FolderPlanIntegration, integration))
			visual.RequirementsChecked = integrationErr == nil
		}
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
	if epoch == h.epoch {
		h.readiness[key] = cachedSetupReadiness{at: time.Now(), visual: visual}
	}
	h.mu.Unlock()
	return withModel(visual)
}

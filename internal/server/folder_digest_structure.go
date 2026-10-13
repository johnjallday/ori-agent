package server

import (
	"context"
	"errors"
	"time"

	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
)

var _ personalassistant.FolderStructureAvailabilityReader = (*folderSetupHost)(nil)

type cachedAvailability struct {
	at     time.Time
	reason string
}

// availability answers one of a proposal's two questions from the short cache,
// so a structure that is described on every turn and every poll reads a
// plugin's release once per planFactsTTL. A failed read is logged through
// notePlan, which keeps it to one line.
func (h *folderSetupHost) availability(userID, kind, pluginKey string, read func() (string, error)) string {
	key := userID + "|" + kind + "|" + pluginKey
	h.mu.Lock()
	cached, ok := h.availabilities[key]
	h.mu.Unlock()
	if ok && time.Since(cached.at) <= planFactsTTL {
		return cached.reason
	}
	reason, err := read()
	if err != nil {
		reason = personalassistant.FolderAvailabilityPlanUnavailable
		if reasoned, ok := err.(interface{ SetupAvailability() string }); ok && reasoned.SetupAvailability() != "" {
			reason = reasoned.SetupAvailability()
		}
		h.notePlan(userID, err)
	} else if reason != "" && h.firstReport(userID, pluginKey, reason) {
		// Nothing failed: the plugin is simply not there to use. The proposal
		// carries on in its plain form, and the log says why, once.
		logger.Info("Folder proposal carries on without a plugin", logger.Fields{
			"reason": reason, "provider_or_integration_key": pluginKey, "user_id": userID,
		})
	}
	h.mu.Lock()
	if h.availabilities == nil {
		h.availabilities = map[string]cachedAvailability{}
	}
	h.availabilities[key] = cachedAvailability{at: time.Now(), reason: reason}
	h.mu.Unlock()
	return reason
}

// HomeAvailability says whether a structure can use the shape's Home as its
// group. What this computer already holds is read first, so a folder that is
// only being attached never waits on a release lookup: the owner's existing
// Home is enough, and a provider that is not installed is known without asking
// anyone. Only an installed provider with no Home yet has its reviewed release
// verified. It installs and creates nothing.
func (h *folderSetupHost) HomeAvailability(ctx context.Context, userID, providerKey string) string {
	if h == nil || h.builder == nil {
		return personalassistant.FolderAvailabilityPlanUnavailable
	}
	var provider *reviewedintegration.HomeProvider
	for _, candidate := range reviewedintegration.HomeProviders() {
		if candidate.Key == providerKey {
			value := candidate
			provider = &value
			break
		}
	}
	if provider == nil {
		return personalassistant.FolderAvailabilityPlanUnavailable
	}
	return h.availability(userID, "home", providerKey, func() (string, error) {
		home, err := h.station(userID, *provider)
		if err != nil {
			return personalassistant.FolderAvailabilityPlanUnavailable, nil
		}
		if home != nil {
			if _, err := folderSetupHomeDestination(home); err != nil {
				return personalassistant.FolderAvailabilityPlanUnavailable, nil
			}
			return "", nil
		}
		// No Home yet: one can be created only from the provider's own template,
		// which is there once the provider is installed and enabled.
		if _, err := h.newHomeDestination(ctx, userID, *provider); err != nil {
			if errors.Is(err, errHomeTemplateNotReady) {
				return personalassistant.FolderAvailabilityProviderNotInstalled, nil
			}
			return personalassistant.FolderAvailabilityPlanUnavailable, nil
		}
		// The template is there. A structure setup installs nothing, so the
		// installed provider must be the reviewed release, enabled and current.
		plugin, err := h.providerFacts(ctx, *provider)
		if err != nil {
			return "", err
		}
		if plugin.State != personalassistant.FolderInstallReady {
			return personalassistant.FolderAvailabilityPlanUnavailable, nil
		}
		return "", nil
	})
}

// IntegrationAvailability says whether a workspace can start from a reviewed
// integration's blueprint with nothing to install: the integration is installed,
// enabled and current. A structure setup never installs a plugin, so an
// integration that could be installed is reported as not installed.
func (h *folderSetupHost) IntegrationAvailability(ctx context.Context, userID, integrationKey string) string {
	if h == nil || h.builder == nil {
		return personalassistant.FolderAvailabilityPlanUnavailable
	}
	entry, ok := reviewedintegration.Get(integrationKey)
	if !ok {
		return personalassistant.FolderAvailabilityPlanUnavailable
	}
	return h.availability(userID, "integration", integrationKey, func() (string, error) {
		// A plugin that is not on this computer is known without reading its
		// releases; only an installed one is checked for being current.
		if handler := h.builder.pluginHandler; handler != nil {
			if installed, err := handler.Manager().List(); err == nil {
				present := false
				for _, item := range installed {
					present = present || item.Name == entry.PluginID
				}
				if !present {
					return personalassistant.FolderAvailabilityIntegrationNotInstalled, nil
				}
			}
		}
		plugin, err := h.integrationFacts(ctx, userID, entry)
		if err != nil {
			return "", err
		}
		switch plugin.State {
		case personalassistant.FolderInstallReady:
			return "", nil
		case personalassistant.FolderInstallInstall:
			return personalassistant.FolderAvailabilityIntegrationNotInstalled, nil
		default:
			return personalassistant.FolderAvailabilityPlanUnavailable, nil
		}
	})
}

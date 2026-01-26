// Package server provides plugin initialization methods for the ServerBuilder.
// This file contains methods for plugin infrastructure, loading, and health checks.
package server

import (
	"os"

	"github.com/johnjallday/ori-agent/internal/healthhttp"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/updatemanager"
	"github.com/johnjallday/ori-agent/internal/version"
)

// initializePluginInfrastructure sets up plugin downloader and refreshes local registry.
func (b *ServerBuilder) initializePluginInfrastructure() error {
	pluginCacheDir := resolvePluginCacheDir()
	b.pluginDownloader = createPluginDownloader(pluginCacheDir)

	// Refresh local plugin registry
	if err := refreshLocalPluginRegistry(b.registryManager); err != nil {
		// Log but don't fail - this is non-critical
		return nil
	}

	return nil
}

// initializeUpdateManager creates the update manager.
func (b *ServerBuilder) initializeUpdateManager() error {
	currentVersion := version.GetVersion()
	b.updateMgr = updatemanager.NewManager(currentVersion, "johnjallday", "ori-agent")
	return nil
}

// validatePluginPaths validates plugin paths for all agents without loading them.
// Plugins are loaded lazily on first use to improve startup time.
func (b *ServerBuilder) validatePluginPaths() error {
	verbose := os.Getenv("ORI_VERBOSE") == "true"
	names, _ := b.st.ListAgents()

	for _, agName := range names {
		ag, ok := b.st.GetAgent(agName)
		if !ok {
			continue
		}

		var invalidPlugins []string

		for key, lp := range ag.Plugins {
			// Skip if already loaded (shouldn't happen on startup, but be safe)
			if lp.Tool != nil {
				continue
			}

			// Validate plugin path exists
			if _, err := os.Stat(lp.Path); os.IsNotExist(err) {
				logger.Warn("Plugin path does not exist, removing from agent", logger.Fields{
					"plugin": key,
					"path":   lp.Path,
					"agent":  agName,
				})
				invalidPlugins = append(invalidPlugins, key)
				continue
			}

			if verbose {
				logger.Debug("Plugin registered for lazy loading", logger.Fields{
					"plugin": key,
					"agent":  agName,
				})
			}
		}

		// Remove invalid plugins
		for _, pluginKey := range invalidPlugins {
			delete(ag.Plugins, pluginKey)
		}

		if len(invalidPlugins) > 0 {
			if err := b.st.SetAgent(agName, ag); err != nil {
				logger.Error("Failed to save agent after removing invalid plugins", logger.Fields{
					"agent": agName,
					"err":   err,
				})
			}
		}
	}

	pluginCount := 0
	for _, agName := range names {
		if ag, ok := b.st.GetAgent(agName); ok {
			pluginCount += len(ag.Plugins)
		}
	}
	if pluginCount > 0 {
		logger.Info("Plugins registered for lazy loading", logger.Fields{"count": pluginCount})
	}

	return nil
}

// loadPluginRegistry loads the plugin registry and sets it for the health manager.
func (b *ServerBuilder) loadPluginRegistry() error {
	reg, _, err := b.registryManager.Load()
	if err != nil {
		logger.Error("failed to load plugin registry", logger.Fields{"plugin": err})
		return nil // Non-critical, continue
	}

	b.pluginReg = reg

	// Set registry for health manager
	b.healthManager.SetRegistry(func() []healthhttp.PluginRegistryEntry {
		entries := make([]healthhttp.PluginRegistryEntry, len(reg.Plugins))
		for i, p := range reg.Plugins {
			entries[i] = healthhttp.PluginRegistryEntry{
				Name:    p.Name,
				Version: p.Version,
				URL:     p.URL,
			}
		}
		return entries
	})

	return nil
}

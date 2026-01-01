// Package server provides plugin initialization methods for the ServerBuilder.
// This file contains methods for plugin infrastructure, loading, and health checks.
package server

import (
	"log"
	"os"
	"strings"

	"github.com/johnjallday/ori-agent/internal/healthhttp"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/updatemanager"
	"github.com/johnjallday/ori-agent/internal/version"
)

// initializePluginInfrastructure sets up plugin downloader and refreshes local registry.
func (b *ServerBuilder) initializePluginInfrastructure() error {
	pluginCacheDir := resolvePluginCacheDir()
	b.server.pluginDownloader = createPluginDownloader(pluginCacheDir)

	// Refresh local plugin registry
	if err := refreshLocalPluginRegistry(b.server.registryManager); err != nil {
		// Log but don't fail - this is non-critical
		return nil
	}

	return nil
}

// initializeUpdateManager creates the update manager.
func (b *ServerBuilder) initializeUpdateManager() error {
	currentVersion := version.GetVersion()
	b.server.updateMgr = updatemanager.NewManager(currentVersion, "johnjallday", "ori-agent")
	return nil
}

// validatePluginPaths validates plugin paths for all agents without loading them.
// Plugins are loaded lazily on first use to improve startup time.
func (b *ServerBuilder) validatePluginPaths() error {
	verbose := os.Getenv("ORI_VERBOSE") == "true"
	names, _ := b.server.st.ListAgents()

	for _, agName := range names {
		ag, ok := b.server.st.GetAgent(agName)
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
			if err := b.server.st.SetAgent(agName, ag); err != nil {
				logger.Error("Failed to save agent after removing invalid plugins", logger.Fields{
					"agent": agName,
					"err":   err,
				})
			}
		}
	}

	pluginCount := 0
	for _, agName := range names {
		if ag, ok := b.server.st.GetAgent(agName); ok {
			pluginCount += len(ag.Plugins)
		}
	}
	if pluginCount > 0 {
		logger.Info("Plugins registered for lazy loading", logger.Fields{"count": pluginCount})
	}

	return nil
}

// printHealthSummary prints a formatted health summary table.
func (b *ServerBuilder) printHealthSummary(healthy, degraded, unhealthy []string) {
	log.Println("")
	log.Println("╔════════════════════════════════════════════════════════════════════════════════╗")
	log.Println("║  🏥 Plugin Health Summary                                                      ║")
	log.Println("╠════════════════════════════════════════════════════════════════════════════════╣")

	if len(healthy) > 0 {
		logger.Info("║ Healthy: 66s║", logger.Fields{"value1": len(healthy), "join(healthy, \", \"), 66)": truncateString(strings.Join(healthy, ", "), 66)})
		if len(healthy) > 1 {
			healthyList := strings.Join(healthy, ", ")
			for i := 66; i < len(healthyList); i += 73 {
				end := i + 73
				if end > len(healthyList) {
					end = len(healthyList)
				}
				logger.Debug("║ 74s║", logger.Fields{"value1": healthyList[i:end]})
			}
		}
	} else {
		log.Println("║  ✅ 0 Healthy                                                                  ║")
	}

	if len(degraded) > 0 {
		logger.Warn("║ Degraded: 64s║", logger.Fields{"join(degraded, \", \"), 64)": truncateString(strings.Join(degraded, ", "), 64), "value1": len(degraded)})
		if len(degraded) > 1 {
			degradedList := strings.Join(degraded, ", ")
			for i := 64; i < len(degradedList); i += 73 {
				end := i + 73
				if end > len(degradedList) {
					end = len(degradedList)
				}
				logger.Debug("║ 74s║", logger.Fields{"value1": degradedList[i:end]})
			}
		}
	} else {
		log.Println("║  ⚠️  0 Degraded                                                                ║")
	}

	if len(unhealthy) > 0 {
		logger.Error("║ Unhealthy: 63s║", logger.Fields{"value1": len(unhealthy), "join(unhealthy, \", \"), 63)": truncateString(strings.Join(unhealthy, ", "), 63)})
		if len(unhealthy) > 1 {
			unhealthyList := strings.Join(unhealthy, ", ")
			for i := 63; i < len(unhealthyList); i += 73 {
				end := i + 73
				if end > len(unhealthyList) {
					end = len(unhealthyList)
				}
				logger.Debug("║ 74s║", logger.Fields{"value1": unhealthyList[i:end]})
			}
		}
	} else {
		log.Println("║  ❌ 0 Unhealthy                                                                ║")
	}

	log.Println("╚════════════════════════════════════════════════════════════════════════════════╝")
	log.Println("")
}

// loadPluginRegistry loads the plugin registry and sets it for the health manager.
func (b *ServerBuilder) loadPluginRegistry() error {
	reg, _, err := b.server.registryManager.Load()
	if err != nil {
		logger.Error("failed to load plugin registry", logger.Fields{"plugin": err})
		return nil // Non-critical, continue
	}

	b.server.pluginReg = reg

	// Set registry for health manager
	b.server.healthManager.SetRegistry(func() []healthhttp.PluginRegistryEntry {
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

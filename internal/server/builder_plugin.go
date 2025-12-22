// Package server provides plugin initialization methods for the ServerBuilder.
// This file contains methods for plugin infrastructure, loading, and health checks.
package server

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/johnjallday/ori-agent/internal/healthhttp"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/pluginloader"
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

// loadPluginsAndHealthCheck restores plugins for all agents and runs health checks.
func (b *ServerBuilder) loadPluginsAndHealthCheck() error {
	verbose := os.Getenv("ORI_VERBOSE") == "true"
	names, _ := b.server.st.ListAgents()
	var healthySummary, degradedSummary, unhealthySummary []string

	for _, agName := range names {
		ag, ok := b.server.st.GetAgent(agName)
		if !ok {
			continue
		}

		var failedPlugins []string

		for key, lp := range ag.Plugins {
			if lp.Tool != nil {
				continue
			}

			tool, err := pluginloader.LoadPluginUnified(lp.Path)
			if err != nil {
				logger.Error("Failed to load plugin for agent", logger.Fields{"err": err, "agent": lp.Path, "agName": agName})
				logger.Error("Removing failed plugin from agent config", logger.Fields{"plugin": key, "agName": agName})
				failedPlugins = append(failedPlugins, key)
				continue
			}

			// Run health check
			healthResult := b.server.healthManager.CheckAndCachePlugin(key, tool)
			if !healthResult.Health.Compatible {
				if healthResult.Health.Status == "unhealthy" {
					if verbose {
						logger.Error("Plugin is UNHEALTHY", logger.Fields{"plugin": key})
						for _, err := range healthResult.Health.Errors {
							logger.Error("Error", logger.Fields{"error": err})
						}
						if healthResult.Health.Recommendation != "" {
							logger.Debug("💡 Recommendation", logger.Fields{"recommendation": healthResult.Health.Recommendation})
						}
					}
					unhealthySummary = append(unhealthySummary, fmt.Sprintf("%s v%s", key, healthResult.Health.Version))
				} else {
					if verbose {
						logger.Warn("Plugin is DEGRADED", logger.Fields{"plugin": key})
						for _, warn := range healthResult.Health.Warnings {
							logger.Warn("Warning", logger.Fields{"warn": warn})
						}
					}
					degradedSummary = append(degradedSummary, fmt.Sprintf("%s v%s", key, healthResult.Health.Version))
				}
			} else {
				if verbose {
					logger.Info("Plugin v health check passed", logger.Fields{"plugin": key, "version": healthResult.Health.Version})
				}
				healthySummary = append(healthySummary, fmt.Sprintf("%s v%s", key, healthResult.Health.Version))
			}

			agentSpecificStorePath := filepath.Join("agents", agName, "config.json")
			if abs, err := filepath.Abs(agentSpecificStorePath); err == nil {
				agentSpecificStorePath = abs
			}

			currentLocation := ""
			if b.server.locationManager != nil {
				currentLocation = b.server.locationManager.GetCurrentLocation()
			}
			pluginloader.SetAgentContext(tool, agName, agentSpecificStorePath, currentLocation)

			if err := pluginloader.ExtractPluginSettingsSchema(tool, agName); err != nil {
				if verbose {
					logger.Error("failed to extract settings schema for plugin in agent", logger.Fields{"plugin": lp.Path, "agName": agName, "err": err})
				}
			}

			lp.Tool = tool
			lp.Definition = tool.Definition()
			ag.Plugins[key] = lp
		}

		for _, pluginKey := range failedPlugins {
			delete(ag.Plugins, pluginKey)
		}

		if err := b.server.st.SetAgent(agName, ag); err != nil {
			logger.Error("failed to restore plugins for agent", logger.Fields{"agent": agName, "err": err})
		}
	}

	// Print health summary
	if verbose && (len(healthySummary) > 0 || len(degradedSummary) > 0 || len(unhealthySummary) > 0) {
		b.printHealthSummary(healthySummary, degradedSummary, unhealthySummary)
	}

	// Health check all uploaded plugins
	if verbose {
		log.Println("Running initial health checks for all uploaded plugins...")
	}
	localReg, err := b.server.registryManager.LoadLocal()
	if err == nil {
		for _, pluginEntry := range localReg.Plugins {
			if _, exists := b.server.healthManager.GetPluginHealth(pluginEntry.Name); exists {
				continue
			}

			tool, err := pluginloader.LoadPluginRPC(pluginEntry.Path)
			if err != nil {
				if verbose {
					logger.Warn("could not load plugin for initial health check", logger.Fields{"plugin": pluginEntry.Name, "err": err})
				}
				continue
			}

			healthResult := b.server.healthManager.CheckAndCachePlugin(pluginEntry.Name, tool)
			if verbose {
				if healthResult.Health.Compatible {
					logger.Info("Plugin v health check passed", logger.Fields{"plugin": pluginEntry.Name, "version": healthResult.Health.Version})
				} else {
					logger.Warn("Plugin v health check issues", logger.Fields{"plugin": pluginEntry.Name, "version": healthResult.Health.Version, "warnings": healthResult.Health.Warnings})
				}
			}
		}
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

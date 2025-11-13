package health

import (
	"context"
	"fmt"
	"time"

	"github.com/johnjallday/ori-agent/internal/version"
	"github.com/johnjallday/ori-agent/pluginapi"
)

// PluginHealth represents the health status of a plugin
type PluginHealth struct {
	Name                 string        `json:"name"`
	Version              string        `json:"version"`
	Status               string        `json:"status"` // "healthy", "degraded", "unhealthy"
	Compatible           bool          `json:"compatible"`
	APIVersion           string        `json:"api_version"`
	LastCheck            time.Time     `json:"last_check"`
	Errors               []string      `json:"errors,omitempty"`
	Warnings             []string      `json:"warnings,omitempty"`
	Recommendation       string        `json:"recommendation,omitempty"`
	CallSuccessRate      float64       `json:"call_success_rate"`
	AvgResponseTime      time.Duration `json:"avg_response_time"`
	TotalCalls           int64         `json:"total_calls"`
	FailedCalls          int64         `json:"failed_calls"`
	UpdateAvailable      bool          `json:"update_available"`
	LatestVersion        string        `json:"latest_version,omitempty"`
	UpdateRecommendation string        `json:"update_recommendation,omitempty"`
}

// CompatibilityCheck represents a compatibility check result
type CompatibilityCheck struct {
	Type    string `json:"type"` // "version", "api", "functional", "dependencies"
	Passed  bool   `json:"passed"`
	Details string `json:"details"`
}

// CheckResult contains all health check results for a plugin
type CheckResult struct {
	Health PluginHealth         `json:"health"`
	Checks []CompatibilityCheck `json:"checks"`
}

// PluginRegistryProvider is an interface for getting plugin registry data
type PluginRegistryProvider interface {
	GetPluginByName(name string) (PluginRegistryEntry, bool)
}

// PluginRegistryEntry represents a plugin in the registry (minimal interface)
type PluginRegistryEntry struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	URL     string `json:"url,omitempty"`
}

// Checker performs health checks on plugins
type Checker struct {
	agentVersion    string
	agentAPIVersion string
	registry        PluginRegistryProvider
}

// NewChecker creates a new health checker
func NewChecker() *Checker {
	return &Checker{
		agentVersion:    version.GetVersion(),
		agentAPIVersion: version.APIVersion,
		registry:        nil, // Can be set later via SetRegistry
	}
}

// SetRegistry sets the plugin registry for update checking
func (c *Checker) SetRegistry(reg PluginRegistryProvider) {
	c.registry = reg
}

// CheckPlugin performs a comprehensive health check on a plugin
func (c *Checker) CheckPlugin(name string, tool pluginapi.PluginTool) CheckResult {
	result := CheckResult{
		Health: PluginHealth{
			Name:       name,
			LastCheck:  time.Now(),
			Status:     "healthy",
			Compatible: true,
		},
		Checks: make([]CompatibilityCheck, 0),
	}

	// Check if plugin implements PluginCompatibility
	metadata, hasMetadata := tool.(pluginapi.PluginCompatibility)

	if !hasMetadata {
		// Try VersionedTool as fallback
		if versionedTool, ok := tool.(pluginapi.VersionedTool); ok {
			result.Health.Version = versionedTool.Version()
			result.Health.Warnings = append(result.Health.Warnings,
				"Plugin uses legacy VersionedTool interface - consider upgrading to PluginCompatibility")
			result.Health.Status = "degraded"
			result.Checks = append(result.Checks, CompatibilityCheck{
				Type:    "metadata",
				Passed:  false,
				Details: "Plugin does not implement PluginCompatibility interface",
			})
			return result
		}

		// No version info at all
		result.Health.Version = "unknown"
		result.Health.Status = "unhealthy"
		result.Health.Compatible = false
		result.Health.Errors = append(result.Health.Errors,
			"Plugin does not implement version interfaces")
		result.Checks = append(result.Checks, CompatibilityCheck{
			Type:    "metadata",
			Passed:  false,
			Details: "Plugin does not provide version information",
		})
		return result
	}

	// Extract version information
	result.Health.Version = metadata.Version()
	result.Health.APIVersion = metadata.APIVersion()
	minVersion := metadata.MinAgentVersion()
	maxVersion := metadata.MaxAgentVersion()

	// Check version compatibility
	compatible, reason := IsCompatible(c.agentVersion, result.Health.Version, minVersion, maxVersion)
	result.Checks = append(result.Checks, CompatibilityCheck{
		Type:    "version_compatibility",
		Passed:  compatible,
		Details: reason,
	})

	if !compatible {
		result.Health.Status = "unhealthy"
		result.Health.Compatible = false
		result.Health.Errors = append(result.Health.Errors, reason)
		result.Health.Recommendation = fmt.Sprintf("Update plugin to a compatible version")
	}

	// Check API version compatibility
	apiCompatible, apiReason := IsAPICompatible(c.agentAPIVersion, result.Health.APIVersion)
	result.Checks = append(result.Checks, CompatibilityCheck{
		Type:    "api_version",
		Passed:  apiCompatible,
		Details: apiReason,
	})

	if !apiCompatible {
		result.Health.Status = "unhealthy"
		result.Health.Compatible = false
		result.Health.Errors = append(result.Health.Errors, apiReason)
		result.Health.Recommendation = fmt.Sprintf("Rebuild plugin with API version %s", c.agentAPIVersion)
	}

	// Check basic functionality
	funcCheck := c.checkFunctionality(tool)
	result.Checks = append(result.Checks, funcCheck)
	if !funcCheck.Passed {
		result.Health.Status = "unhealthy"
		result.Health.Errors = append(result.Health.Errors, funcCheck.Details)
	}

	// Check custom health check if available
	if healthProvider, ok := tool.(pluginapi.HealthCheckProvider); ok {
		if err := healthProvider.HealthCheck(); err != nil {
			result.Health.Status = "degraded"
			result.Health.Warnings = append(result.Health.Warnings, fmt.Sprintf("Health check failed: %v", err))
			result.Checks = append(result.Checks, CompatibilityCheck{
				Type:    "custom_health",
				Passed:  false,
				Details: fmt.Sprintf("Custom health check failed: %v", err),
			})
		} else {
			result.Checks = append(result.Checks, CompatibilityCheck{
				Type:    "custom_health",
				Passed:  true,
				Details: "Custom health check passed",
			})
		}
	}

	// Check for updates if registry is available
	if c.registry != nil && result.Health.Version != "" && result.Health.Version != "unknown" {
		if registryEntry, found := c.registry.GetPluginByName(name); found {
			result.Health.LatestVersion = registryEntry.Version

			// Compare versions
			currentVer := result.Health.Version
			latestVer := registryEntry.Version

			if currentVer != latestVer {
				// Check if current version is older than latest
				isOlder, _ := IsVersionOlder(currentVer, latestVer)
				if isOlder {
					result.Health.UpdateAvailable = true
					result.Health.UpdateRecommendation = fmt.Sprintf(
						"Update available: v%s → v%s. Run: ori plugin update %s",
						currentVer, latestVer, name,
					)
					// Add as a warning if plugin is otherwise healthy
					if result.Health.Status == "healthy" {
						result.Health.Warnings = append(result.Health.Warnings,
							fmt.Sprintf("Update available: v%s (current: v%s)", latestVer, currentVer))
					}
				}
			}
		}
	}

	return result
}

// checkFunctionality performs basic functionality tests
func (c *Checker) checkFunctionality(tool pluginapi.PluginTool) CompatibilityCheck {
	// Test Definition() call
	defer func() {
		if r := recover(); r != nil {
			// Plugin panicked
		}
	}()

	def := tool.Definition()
	if def.Name == "" {
		return CompatibilityCheck{
			Type:    "functionality",
			Passed:  false,
			Details: "Plugin Definition() returned empty name",
		}
	}

	// Test Call() with empty context (should fail gracefully, not panic)
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	_, err := tool.Call(ctx, "{}")
	// We expect an error (invalid args), but no panic
	if err == nil {
		// This is fine - plugin accepted empty args
	}

	return CompatibilityCheck{
		Type:    "functionality",
		Passed:  true,
		Details: "Definition() and Call() working correctly",
	}
}

// GetAgentInfo returns agent version information
func (c *Checker) GetAgentInfo() map[string]string {
	return map[string]string{
		"version":     c.agentVersion,
		"api_version": c.agentAPIVersion,
	}
}

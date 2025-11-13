package healthhttp

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/johnjallday/ori-agent/internal/health"
	"github.com/johnjallday/ori-agent/internal/notifications"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/pluginapi"
)

// PluginStats tracks runtime statistics for a plugin
type PluginStats struct {
	TotalCalls    int64
	FailedCalls   int64
	TotalDuration time.Duration
	LastCallTime  time.Time
	LastError     string
	LastErrorTime time.Time
}

// HealthEvent represents a health status change event
type HealthEvent struct {
	Timestamp   time.Time `json:"timestamp"`
	PluginName  string    `json:"plugin_name"`
	Status      string    `json:"status"` // "healthy", "degraded", "unhealthy"
	OldStatus   string    `json:"old_status,omitempty"`
	Details     string    `json:"details"`
	CallCount   int64     `json:"call_count"`
	FailedCalls int64     `json:"failed_calls"`
	SuccessRate float64   `json:"success_rate"`
}

// Manager tracks plugin health information
type Manager struct {
	mu                    sync.RWMutex
	healthCache           map[string]health.CheckResult // key: plugin name
	statsCache            map[string]*PluginStats       // key: plugin name
	healthHistory         []HealthEvent                 // circular buffer of health events
	maxHistorySize        int
	checker               *health.Checker
	notifier              *notifications.Notifier
	stopChan              chan struct{}
	periodicCheckInterval time.Duration
}

// NewManager creates a new health manager
func NewManager() *Manager {
	return &Manager{
		healthCache:           make(map[string]health.CheckResult),
		statsCache:            make(map[string]*PluginStats),
		healthHistory:         make([]HealthEvent, 0, 1000), // Pre-allocate for 1000 events
		maxHistorySize:        1000,                         // Keep last 1000 health events
		checker:               health.NewChecker(),
		notifier:              notifications.NewNotifier(),
		stopChan:              make(chan struct{}),
		periodicCheckInterval: 5 * time.Minute, // Run health checks every 5 minutes
	}
}

// RegistryAdapter adapts the types.PluginRegistry to health.PluginRegistryProvider
type RegistryAdapter struct {
	getRegistry func() []PluginRegistryEntry
}

// PluginRegistryEntry represents a minimal plugin registry entry
type PluginRegistryEntry struct {
	Name    string
	Version string
	URL     string
}

// GetPluginByName implements health.PluginRegistryProvider
func (r *RegistryAdapter) GetPluginByName(name string) (health.PluginRegistryEntry, bool) {
	plugins := r.getRegistry()
	for _, plugin := range plugins {
		if plugin.Name == name {
			return health.PluginRegistryEntry{
				Name:    plugin.Name,
				Version: plugin.Version,
				URL:     plugin.URL,
			}, true
		}
	}
	return health.PluginRegistryEntry{}, false
}

// SetRegistry sets the plugin registry for update checking
func (m *Manager) SetRegistry(getRegistry func() []PluginRegistryEntry) {
	adapter := &RegistryAdapter{
		getRegistry: getRegistry,
	}
	m.checker.SetRegistry(adapter)
}

// GetChecker returns the health checker for external use
func (m *Manager) GetChecker() *health.Checker {
	return m.checker
}

// logHealthEvent logs a health status event to history
func (m *Manager) logHealthEvent(event HealthEvent) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Add event to history
	m.healthHistory = append(m.healthHistory, event)

	// Trim history if it exceeds max size
	if len(m.healthHistory) > m.maxHistorySize {
		// Keep only the most recent events
		m.healthHistory = m.healthHistory[len(m.healthHistory)-m.maxHistorySize:]
	}
}

// GetHealthHistory returns recent health events, optionally filtered by plugin name
func (m *Manager) GetHealthHistory(pluginName string, limit int) []HealthEvent {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// If no limit specified, return last 100 events
	if limit == 0 {
		limit = 100
	}

	var filtered []HealthEvent
	// Iterate backwards to get most recent events first
	for i := len(m.healthHistory) - 1; i >= 0 && len(filtered) < limit; i-- {
		event := m.healthHistory[i]
		if pluginName == "" || event.PluginName == pluginName {
			filtered = append(filtered, event)
		}
	}

	return filtered
}

// StartPeriodicChecks starts background periodic health checks
// It accepts a function that returns all loaded plugins
func (m *Manager) StartPeriodicChecks(getPlugins func() map[string]pluginapi.PluginTool) {
	go func() {
		ticker := time.NewTicker(m.periodicCheckInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				log.Printf("Running periodic plugin health checks...")
				plugins := getPlugins()

				for name, tool := range plugins {
					result := m.checker.CheckPlugin(name, tool)

					m.mu.Lock()
					m.healthCache[name] = result
					m.mu.Unlock()

					if !result.Health.Compatible {
						log.Printf("⚠️  Plugin %s health degraded: %v", name, result.Health.Warnings)
					}
				}

				log.Printf("Periodic health check complete. Checked %d plugins", len(plugins))

			case <-m.stopChan:
				log.Printf("Stopping periodic health checks")
				return
			}
		}
	}()
}

// StopPeriodicChecks stops the background periodic health checks
func (m *Manager) StopPeriodicChecks() {
	close(m.stopChan)
}

// CheckAndCachePlugin performs a health check and caches the result
func (m *Manager) CheckAndCachePlugin(name string, tool pluginapi.PluginTool) health.CheckResult {
	result := m.checker.CheckPlugin(name, tool)

	// Get old status for comparison
	m.mu.Lock()
	oldResult, exists := m.healthCache[name]
	oldStatus := ""
	if exists {
		oldStatus = oldResult.Health.Status
	}

	// Update health cache
	m.healthCache[name] = result
	m.mu.Unlock()

	// Log health event if status changed or first check
	if !exists || oldStatus != result.Health.Status {
		stats := m.GetPluginStats(name)
		var callCount, failedCalls int64
		var successRate float64

		if stats != nil {
			callCount = stats.TotalCalls
			failedCalls = stats.FailedCalls
			if callCount > 0 {
				successRate = float64(callCount-failedCalls) / float64(callCount) * 100
			}
		}

		details := result.Health.Status
		if len(result.Health.Errors) > 0 {
			details = fmt.Sprintf("%s: %s", result.Health.Status, strings.Join(result.Health.Errors, "; "))
		} else if len(result.Health.Warnings) > 0 {
			details = fmt.Sprintf("%s: %s", result.Health.Status, strings.Join(result.Health.Warnings, "; "))
		}

		m.logHealthEvent(HealthEvent{
			Timestamp:   time.Now(),
			PluginName:  name,
			Status:      result.Health.Status,
			OldStatus:   oldStatus,
			Details:     details,
			CallCount:   callCount,
			FailedCalls: failedCalls,
			SuccessRate: successRate,
		})

		// Send notification when status changes
		m.sendHealthNotification(name, result, oldStatus)
	}

	// Check for available updates and notify
	if result.Health.UpdateAvailable {
		m.notifier.Notify(
			notifications.NotifyUpdateAvailable,
			"Plugin Update Available",
			fmt.Sprintf("Update available for %s: v%s → v%s", name, result.Health.Version, result.Health.LatestVersion),
			"info",
			map[string]interface{}{
				"plugin_name":     name,
				"current_version": result.Health.Version,
				"latest_version":  result.Health.LatestVersion,
			},
		)
	}

	return result
}

// sendHealthNotification sends a notification when plugin health changes
func (m *Manager) sendHealthNotification(pluginName string, result health.CheckResult, oldStatus string) {
	var notifType notifications.NotificationType
	var severity string
	var title string
	var message string

	switch result.Health.Status {
	case "healthy":
		notifType = notifications.NotifyPluginHealthy
		severity = "info"
		title = "Plugin Healthy"
		message = fmt.Sprintf("Plugin %s v%s is now healthy", pluginName, result.Health.Version)

	case "degraded":
		notifType = notifications.NotifyPluginDegraded
		severity = "warning"
		title = "Plugin Degraded"
		message = fmt.Sprintf("Plugin %s v%s is degraded", pluginName, result.Health.Version)
		if len(result.Health.Warnings) > 0 {
			message += ": " + strings.Join(result.Health.Warnings, "; ")
		}

	case "unhealthy":
		notifType = notifications.NotifyPluginUnhealthy
		severity = "error"
		title = "Plugin Unhealthy"
		message = fmt.Sprintf("Plugin %s v%s is unhealthy", pluginName, result.Health.Version)
		if len(result.Health.Errors) > 0 {
			message += ": " + strings.Join(result.Health.Errors, "; ")
		}
	}

	data := map[string]interface{}{
		"plugin_name":    pluginName,
		"plugin_version": result.Health.Version,
		"old_status":     oldStatus,
		"new_status":     result.Health.Status,
		"compatible":     result.Health.Compatible,
	}

	if result.Health.Recommendation != "" {
		data["recommendation"] = result.Health.Recommendation
	}

	m.notifier.Notify(notifType, title, message, severity, data)
}

// GetPluginHealth returns cached health info for a plugin
func (m *Manager) GetPluginHealth(name string) (health.CheckResult, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result, ok := m.healthCache[name]
	return result, ok
}

// GetAllHealth returns all cached health information
func (m *Manager) GetAllHealth() map[string]health.CheckResult {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Create a copy to avoid race conditions
	copy := make(map[string]health.CheckResult, len(m.healthCache))
	for k, v := range m.healthCache {
		copy[k] = v
	}
	return copy
}

// RecordCallStart records the start of a plugin call
func (m *Manager) RecordCallStart(pluginName string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.statsCache[pluginName] == nil {
		m.statsCache[pluginName] = &PluginStats{}
	}

	m.statsCache[pluginName].LastCallTime = time.Now()
}

// RecordCallSuccess records a successful plugin call
func (m *Manager) RecordCallSuccess(pluginName string, duration time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.statsCache[pluginName] == nil {
		m.statsCache[pluginName] = &PluginStats{}
	}

	stats := m.statsCache[pluginName]
	stats.TotalCalls++
	stats.TotalDuration += duration

	// Update health cache with new stats
	if result, ok := m.healthCache[pluginName]; ok {
		result.Health.TotalCalls = stats.TotalCalls
		result.Health.FailedCalls = stats.FailedCalls
		result.Health.AvgResponseTime = time.Duration(int64(stats.TotalDuration) / stats.TotalCalls)
		if stats.TotalCalls > 0 {
			result.Health.CallSuccessRate = float64(stats.TotalCalls-stats.FailedCalls) / float64(stats.TotalCalls) * 100
		}
		m.healthCache[pluginName] = result
	}
}

// RecordCallFailure records a failed plugin call
func (m *Manager) RecordCallFailure(pluginName string, duration time.Duration, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.statsCache[pluginName] == nil {
		m.statsCache[pluginName] = &PluginStats{}
	}

	stats := m.statsCache[pluginName]
	stats.TotalCalls++
	stats.FailedCalls++
	stats.TotalDuration += duration
	stats.LastError = err.Error()
	stats.LastErrorTime = time.Now()

	// Send notification for plugin error
	m.notifier.Notify(
		notifications.NotifyPluginError,
		"Plugin Error",
		fmt.Sprintf("Plugin %s call failed: %v", pluginName, err),
		"warning",
		map[string]interface{}{
			"plugin_name": pluginName,
			"error":       err.Error(),
			"duration_ms": duration.Milliseconds(),
		},
	)

	// Update health cache with new stats
	if result, ok := m.healthCache[pluginName]; ok {
		result.Health.TotalCalls = stats.TotalCalls
		result.Health.FailedCalls = stats.FailedCalls
		result.Health.AvgResponseTime = time.Duration(int64(stats.TotalDuration) / stats.TotalCalls)

		var successRate float64
		if stats.TotalCalls > 0 {
			successRate = float64(stats.TotalCalls-stats.FailedCalls) / float64(stats.TotalCalls) * 100
			result.Health.CallSuccessRate = successRate
		}
		m.healthCache[pluginName] = result

		// Check for high failure rate (> 20%) after at least 10 calls
		if stats.TotalCalls >= 10 && successRate < 80.0 {
			m.notifier.Notify(
				notifications.NotifyHighFailureRate,
				"High Plugin Failure Rate",
				fmt.Sprintf("Plugin %s has high failure rate: %.1f%% success (%d/%d calls)",
					pluginName, successRate, stats.TotalCalls-stats.FailedCalls, stats.TotalCalls),
				"error",
				map[string]interface{}{
					"plugin_name":  pluginName,
					"success_rate": successRate,
					"total_calls":  stats.TotalCalls,
					"failed_calls": stats.FailedCalls,
				},
			)
		}
	}
}

// GetPluginStats returns runtime statistics for a plugin
func (m *Manager) GetPluginStats(pluginName string) *PluginStats {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if stats, ok := m.statsCache[pluginName]; ok {
		// Return a copy to avoid race conditions
		statsCopy := *stats
		return &statsCopy
	}
	return nil
}

// Handler serves health check endpoints
type Handler struct {
	manager *Manager
	store   store.Store
}

// NewHandler creates a new health check handler
func NewHandler(manager *Manager, store store.Store) *Handler {
	return &Handler{
		manager: manager,
		store:   store,
	}
}

// AllPluginsHealthResponse is the response format for GET /api/plugins/health
type AllPluginsHealthResponse struct {
	AgentVersion    string                `json:"agent_version"`
	AgentAPIVersion string                `json:"agent_api_version"`
	Timestamp       time.Time             `json:"timestamp"`
	Plugins         []health.PluginHealth `json:"plugins"`
}

// HandleAllPluginsHealth returns health info for all loaded plugins
// GET /api/plugins/health
func (h *Handler) HandleAllPluginsHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Get agent info from checker
	agentInfo := h.manager.checker.GetAgentInfo()

	// Collect health info for ALL health-checked plugins (not just agent-loaded ones)
	var pluginHealthList []health.PluginHealth
	allHealth := h.manager.GetAllHealth()
	for _, result := range allHealth {
		pluginHealthList = append(pluginHealthList, result.Health)
	}

	response := AllPluginsHealthResponse{
		AgentVersion:    agentInfo["version"],
		AgentAPIVersion: agentInfo["api_version"],
		Timestamp:       time.Now(),
		Plugins:         pluginHealthList,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Printf("Error encoding health response: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
}

// HandlePluginHealth returns detailed health info for a specific plugin
// GET /api/plugins/{name}/health
func (h *Handler) HandlePluginHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract plugin name from URL
	// Expected format: /api/plugins/{name}/health
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 3 {
		http.Error(w, "Invalid URL format", http.StatusBadRequest)
		return
	}
	pluginName := parts[2]

	// Get health info for the plugin
	result, ok := h.manager.GetPluginHealth(pluginName)
	if !ok {
		http.Error(w, fmt.Sprintf("No health information for plugin: %s", pluginName), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(result); err != nil {
		log.Printf("Error encoding health response: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
}

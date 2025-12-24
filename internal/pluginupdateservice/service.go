package pluginupdateservice

import (
	"fmt"
	"sync"
	"time"

	"github.com/johnjallday/ori-agent/internal/health"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/registry"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/pluginapi"
)

const defaultCheckInterval = 12 * time.Hour

// PluginUpdateInfo represents update details for a plugin.
type PluginUpdateInfo struct {
	PluginName     string    `json:"plugin_name"`
	CurrentVersion string    `json:"current_version"`
	LatestVersion  string    `json:"latest_version"`
	DownloadURL    string    `json:"download_url"`
	CheckedAt      time.Time `json:"checked_at"`
}

// Service checks for plugin updates and caches results.
type Service struct {
	store    store.Store
	registry *types.PluginRegistry

	mu          sync.RWMutex
	updates     []PluginUpdateInfo
	lastChecked time.Time
	ticker      *time.Ticker
	stopCh      chan struct{}
}

// NewService creates a new plugin update service.
func NewService(st store.Store, registry *types.PluginRegistry) *Service {
	return &Service{
		store:    st,
		registry: registry,
		stopCh:   make(chan struct{}),
	}
}

// Start runs the initial check and starts the periodic ticker.
func (s *Service) Start() {
	if err := s.CheckNow(); err != nil {
		logger.Warn("Initial plugin update check failed", logger.Fields{"error": err})
	}

	s.ticker = time.NewTicker(defaultCheckInterval)
	go func() {
		for {
			select {
			case <-s.ticker.C:
				if err := s.CheckNow(); err != nil {
					logger.Warn("Periodic plugin update check failed", logger.Fields{"error": err})
				}
			case <-s.stopCh:
				return
			}
		}
	}()
}

// Stop stops the periodic ticker.
func (s *Service) Stop() {
	if s.ticker != nil {
		s.ticker.Stop()
	}
	select {
	case <-s.stopCh:
		return
	default:
		close(s.stopCh)
	}
}

// CheckNow checks all agent plugins against the registry and updates the cache.
func (s *Service) CheckNow() error {
	if s.registry == nil {
		return fmt.Errorf("plugin registry not available")
	}

	registryIndex := make(map[string]types.PluginRegistryEntry, len(s.registry.Plugins))
	for _, entry := range s.registry.Plugins {
		registryIndex[registry.NormalizePluginNameForLookup(entry.Name)] = entry
	}

	agentNames, _ := s.store.ListAgents()
	checkedPlugins := make(map[string]bool)
	updates := make([]PluginUpdateInfo, 0)
	checkedAt := time.Now()

	for _, agentName := range agentNames {
		agent, ok := s.store.GetAgent(agentName)
		if !ok {
			continue
		}

		for pluginName, lp := range agent.Plugins {
			if checkedPlugins[pluginName] {
				continue
			}
			checkedPlugins[pluginName] = true

			entry, exists := registryIndex[registry.NormalizePluginNameForLookup(pluginName)]
			if !exists {
				continue
			}

			currentVersion := lp.Version
			if lp.Tool != nil {
				if versionedTool, ok := lp.Tool.(pluginapi.VersionedTool); ok {
					currentVersion = versionedTool.Version()
				}
			}

			if currentVersion == "" || entry.Version == "" {
				continue
			}

			isOlder, err := health.IsVersionOlder(currentVersion, entry.Version)
			if err != nil || !isOlder {
				continue
			}

			updates = append(updates, PluginUpdateInfo{
				PluginName:     pluginName,
				CurrentVersion: currentVersion,
				LatestVersion:  entry.Version,
				DownloadURL:    entry.DownloadURL,
				CheckedAt:      checkedAt,
			})
		}
	}

	s.mu.Lock()
	s.updates = updates
	s.lastChecked = checkedAt
	s.mu.Unlock()

	return nil
}

// GetAvailableUpdates returns cached updates.
func (s *Service) GetAvailableUpdates() []PluginUpdateInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	updates := make([]PluginUpdateInfo, len(s.updates))
	copy(updates, s.updates)
	return updates
}

// GetUpdateCount returns the number of cached updates.
func (s *Service) GetUpdateCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.updates)
}

// HasUpdateForPlugin checks if a plugin has an update available.
func (s *Service) HasUpdateForPlugin(name string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, update := range s.updates {
		if update.PluginName == name {
			return true
		}
	}
	return false
}

// LastChecked returns the last time updates were checked.
func (s *Service) LastChecked() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastChecked
}

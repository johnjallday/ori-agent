package marketplace

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"sync"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/types"
)

// Store manages marketplace configuration persistence
type Store struct {
	mu       sync.RWMutex
	filePath string
	config   types.MarketplaceConfig
}

// NewStore creates a new marketplace store
func NewStore(filePath string) *Store {
	return &Store{
		filePath: filePath,
		config:   types.MarketplaceConfig{},
	}
}

// Load reads marketplace configuration from file
// If the file doesn't exist, it initializes with defaults
func (s *Store) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			// Initialize with defaults
			s.config = types.MarketplaceConfig{
				Marketplaces: []types.Marketplace{types.DefaultOfficialMarketplace()},
			}
			return s.saveUnsafe()
		}
		return fmt.Errorf("failed to read marketplace config: %w", err)
	}

	if err := json.Unmarshal(data, &s.config); err != nil {
		return fmt.Errorf("failed to parse marketplace config: %w", err)
	}

	// Ensure official marketplace exists
	s.ensureDefaultsUnsafe()

	return nil
}

// Save writes marketplace configuration to file
func (s *Store) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveUnsafe()
}

// saveUnsafe writes to file without locking (caller must hold lock)
func (s *Store) saveUnsafe() error {
	data, err := json.MarshalIndent(s.config, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal marketplace config: %w", err)
	}

	if err := os.WriteFile(s.filePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write marketplace config: %w", err)
	}

	return nil
}

// List returns all configured marketplaces
func (s *Store) List() []types.Marketplace {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Return a copy sorted by order
	result := make([]types.Marketplace, len(s.config.Marketplaces))
	copy(result, s.config.Marketplaces)
	sort.Slice(result, func(i, j int) bool {
		return result[i].Order < result[j].Order
	})
	return result
}

// Get returns a marketplace by ID
func (s *Store) Get(id string) (*types.Marketplace, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, mp := range s.config.Marketplaces {
		if mp.ID == id {
			return &mp, nil
		}
	}
	return nil, fmt.Errorf("marketplace not found: %s", id)
}

// Add adds a new marketplace
func (s *Store) Add(m types.Marketplace) error {
	if err := m.Validate(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Generate ID if not provided
	if m.ID == "" {
		m.ID = uuid.New().String()[:8]
	}

	// Check for duplicate ID
	for _, existing := range s.config.Marketplaces {
		if existing.ID == m.ID {
			return fmt.Errorf("marketplace with ID %s already exists", m.ID)
		}
	}

	// Auto-detect source type if not set
	if m.SourceType == "" {
		m.SourceType = types.DetectMarketplaceSourceType(m.Source)
	}

	// Set order to be last
	m.Order = len(s.config.Marketplaces)

	// Ensure it's not marked as official
	m.IsOfficial = false

	s.config.Marketplaces = append(s.config.Marketplaces, m)
	return s.saveUnsafe()
}

// Update updates an existing marketplace
func (s *Store) Update(m types.Marketplace) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, existing := range s.config.Marketplaces {
		if existing.ID == m.ID {
			// Protect official marketplace from certain changes
			if existing.IsOfficial {
				// Can only toggle enabled status for official
				s.config.Marketplaces[i].Enabled = m.Enabled
			} else {
				// Preserve IsOfficial flag
				m.IsOfficial = existing.IsOfficial
				s.config.Marketplaces[i] = m
			}
			return s.saveUnsafe()
		}
	}

	return fmt.Errorf("marketplace not found: %s", m.ID)
}

// Remove removes a marketplace by ID (except official)
func (s *Store) Remove(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, mp := range s.config.Marketplaces {
		if mp.ID == id {
			if mp.IsOfficial {
				return fmt.Errorf("cannot remove official marketplace")
			}
			// Remove the marketplace
			s.config.Marketplaces = append(s.config.Marketplaces[:i], s.config.Marketplaces[i+1:]...)
			// Reindex orders
			for j := range s.config.Marketplaces {
				s.config.Marketplaces[j].Order = j
			}
			return s.saveUnsafe()
		}
	}

	return fmt.Errorf("marketplace not found: %s", id)
}

// Reorder updates the order of marketplaces
// ids should contain all marketplace IDs in the desired order
func (s *Store) Reorder(ids []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(ids) != len(s.config.Marketplaces) {
		return fmt.Errorf("reorder requires all marketplace IDs")
	}

	// Create a map for quick lookup
	mpMap := make(map[string]*types.Marketplace)
	for i := range s.config.Marketplaces {
		mpMap[s.config.Marketplaces[i].ID] = &s.config.Marketplaces[i]
	}

	// Validate all IDs exist
	for _, id := range ids {
		if _, ok := mpMap[id]; !ok {
			return fmt.Errorf("unknown marketplace ID: %s", id)
		}
	}

	// Update orders based on position in ids slice
	for order, id := range ids {
		mpMap[id].Order = order
	}

	// Sort marketplaces by new order
	sort.Slice(s.config.Marketplaces, func(i, j int) bool {
		return s.config.Marketplaces[i].Order < s.config.Marketplaces[j].Order
	})

	return s.saveUnsafe()
}

// GetEnabled returns enabled marketplaces sorted by priority (order)
func (s *Store) GetEnabled() []types.Marketplace {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var enabled []types.Marketplace
	for _, mp := range s.config.Marketplaces {
		if mp.Enabled {
			enabled = append(enabled, mp)
		}
	}

	sort.Slice(enabled, func(i, j int) bool {
		return enabled[i].Order < enabled[j].Order
	})

	return enabled
}

// ensureDefaultsUnsafe ensures the official marketplace exists (caller must hold lock)
func (s *Store) ensureDefaultsUnsafe() {
	hasOfficial := false
	for _, mp := range s.config.Marketplaces {
		if mp.IsOfficial || mp.ID == types.OfficialMarketplaceID {
			hasOfficial = true
			break
		}
	}

	if !hasOfficial {
		official := types.DefaultOfficialMarketplace()
		// Insert at the beginning
		s.config.Marketplaces = append([]types.Marketplace{official}, s.config.Marketplaces...)
		// Reindex orders
		for i := range s.config.Marketplaces {
			s.config.Marketplaces[i].Order = i
		}
	}
}

// EnsureDefaults ensures the official marketplace exists
func (s *Store) EnsureDefaults() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.ensureDefaultsUnsafe()
	return s.saveUnsafe()
}

// SetLastFetched updates the last fetched time for a marketplace
func (s *Store) SetLastFetched(id string, t *types.Marketplace) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, mp := range s.config.Marketplaces {
		if mp.ID == id {
			s.config.Marketplaces[i].LastFetched = t.LastFetched
			s.config.Marketplaces[i].LastError = t.LastError
			return s.saveUnsafe()
		}
	}

	return fmt.Errorf("marketplace not found: %s", id)
}

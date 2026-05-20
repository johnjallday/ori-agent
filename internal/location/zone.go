package location

import (
	"encoding/json"
	"errors"
	"os"

	"github.com/google/uuid"
)

// AddZone adds a new zone to the manager
func (m *Manager) AddZone(zone Zone) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Validate zone
	if zone.Name == "" {
		return errors.New("zone name is required")
	}

	// Generate ID if not provided
	if zone.ID == "" {
		zone.ID = uuid.New().String()
	}

	// Check for duplicate ID
	if _, exists := m.zones[zone.ID]; exists {
		return errors.New("zone with this ID already exists")
	}

	m.zones[zone.ID] = zone

	// Persist to disk if path is set
	if m.zonesFilePath != "" {
		if err := m.saveZonesLocked(m.zonesFilePath); err != nil {
			return err
		}
	}

	return nil
}

// RemoveZone removes a zone by ID
func (m *Manager) RemoveZone(zoneID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.zones[zoneID]; !exists {
		return errors.New("zone not found")
	}

	delete(m.zones, zoneID)

	// Persist to disk if path is set
	if m.zonesFilePath != "" {
		if err := m.saveZonesLocked(m.zonesFilePath); err != nil {
			return err
		}
	}

	return nil
}

// UpdateZone updates an existing zone
func (m *Manager) UpdateZone(zone Zone) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if zone.ID == "" {
		return errors.New("zone ID is required")
	}

	if zone.Name == "" {
		return errors.New("zone name is required")
	}

	if _, exists := m.zones[zone.ID]; !exists {
		return errors.New("zone not found")
	}

	m.zones[zone.ID] = zone

	// Persist to disk if path is set
	if m.zonesFilePath != "" {
		if err := m.saveZonesLocked(m.zonesFilePath); err != nil {
			return err
		}
	}

	return nil
}

// GetZones returns all zones
func (m *Manager) GetZones() []Zone {
	m.mu.RLock()
	defer m.mu.RUnlock()

	zones := make([]Zone, 0, len(m.zones))
	for _, zone := range m.zones {
		zones = append(zones, zone)
	}
	return zones
}

// GetZoneByID returns a zone by ID
func (m *Manager) GetZoneByID(id string) (Zone, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	zone, exists := m.zones[id]
	if !exists {
		return Zone{}, errors.New("zone not found")
	}
	return zone, nil
}

// SaveZones writes zones to a JSON file
func (m *Manager) SaveZones(filepath string) error {
	m.mu.RLock()
	zones := make([]Zone, 0, len(m.zones))
	for _, zone := range m.zones {
		zones = append(zones, zone)
	}
	m.mu.RUnlock()

	return m.saveZonesToFile(zones, filepath)
}

// saveZonesLocked writes zones to a JSON file (caller must hold the lock)
func (m *Manager) saveZonesLocked(filepath string) error {
	zones := make([]Zone, 0, len(m.zones))
	for _, zone := range m.zones {
		zones = append(zones, zone)
	}
	return m.saveZonesToFile(zones, filepath)
}

func (m *Manager) saveZonesToFile(zones []Zone, filepath string) error {
	// Marshal zones to JSON
	data, err := json.MarshalIndent(zones, "", "  ")
	if err != nil {
		return err
	}

	// Write to file with 600 permissions (privacy)
	return os.WriteFile(filepath, data, 0600)
}

// LoadZones reads zones from a JSON file
func LoadZones(filepath string) ([]Zone, error) {
	// Check if file exists
	if _, err := os.Stat(filepath); os.IsNotExist(err) {
		// File doesn't exist, return empty zones
		return []Zone{}, nil
	}

	// Read file
	data, err := os.ReadFile(filepath)
	if err != nil {
		return nil, err
	}

	// Unmarshal JSON
	var zones []Zone
	err = json.Unmarshal(data, &zones)
	if err != nil {
		return nil, err
	}

	return zones, nil
}

// MarshalJSON implements custom JSON marshaling for DetectionRule
func (z Zone) MarshalJSON() ([]byte, error) {
	type Alias Zone
	return json.Marshal(&struct {
		*Alias
	}{
		Alias: (*Alias)(&z),
	})
}

// UnmarshalJSON implements custom JSON unmarshaling for DetectionRule
func (z *Zone) UnmarshalJSON(data []byte) error {
	type Alias Zone
	aux := &struct {
		DetectionRules []json.RawMessage `json:"detection_rules"`
		*Alias
	}{
		Alias: (*Alias)(z),
	}

	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	// Unmarshal detection rules
	z.DetectionRules = make([]DetectionRule, 0)
	for _, rawRule := range aux.DetectionRules {
		// Try to determine rule type by checking for SSID field
		var temp map[string]any
		if err := json.Unmarshal(rawRule, &temp); err != nil {
			continue
		}

		if _, hasSSID := temp["ssid"]; hasSSID {
			var wifiRule WiFiRule
			if err := json.Unmarshal(rawRule, &wifiRule); err == nil {
				z.DetectionRules = append(z.DetectionRules, wifiRule)
			}
		}
	}

	return nil
}

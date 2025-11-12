package location

import (
	"context"
	"log"
	"sync"
	"time"
)

// Manager manages location detection and zone matching
type Manager struct {
	mu                sync.RWMutex
	detectors         []Detector
	zones             map[string]Zone // zone ID -> Zone
	currentLocation   string
	manualDetector    *ManualDetector
	eventCallbacks    []func(LocationChangeEvent)
	detectionInterval time.Duration
	ctx               context.Context
	cancel            context.CancelFunc
	zonesFilePath     string // Path to zones file for persistence
}

// NewManager creates a new location manager
func NewManager(detectors []Detector, zones []Zone) *Manager {
	// Find or create manual detector
	var manualDetector *ManualDetector
	for _, d := range detectors {
		if md, ok := d.(*ManualDetector); ok {
			manualDetector = md
			break
		}
	}
	if manualDetector == nil {
		manualDetector = NewManualDetector()
		detectors = append([]Detector{manualDetector}, detectors...)
	}

	// Convert zones slice to map
	zoneMap := make(map[string]Zone)
	for _, z := range zones {
		zoneMap[z.ID] = z
	}

	return &Manager{
		detectors:         detectors,
		zones:             zoneMap,
		currentLocation:   "Unknown",
		manualDetector:    manualDetector,
		eventCallbacks:    []func(LocationChangeEvent){},
		detectionInterval: 60 * time.Second,
		zonesFilePath:     "", // Will be set when needed
	}
}

// SetZonesFilePath sets the file path for zone persistence
func (m *Manager) SetZonesFilePath(path string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.zonesFilePath = path
}

// Start begins the location detection loop
func (m *Manager) Start(ctx context.Context, interval time.Duration) {
	m.mu.Lock()
	if interval > 0 {
		m.detectionInterval = interval
	}
	m.ctx, m.cancel = context.WithCancel(ctx)
	m.mu.Unlock()

	// Initial detection
	m.detectAndUpdate()

	// Start periodic detection
	go m.detectionLoop()
}

// Stop stops the location detection loop
func (m *Manager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cancel != nil {
		m.cancel()
	}
}

// detectionLoop runs periodic location detection
func (m *Manager) detectionLoop() {
	ticker := time.NewTicker(m.detectionInterval)
	defer ticker.Stop()

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.detectAndUpdate()
		}
	}
}

// detectAndUpdate detects location and updates if changed
func (m *Manager) detectAndUpdate() {
	detectedValue, method := m.detectLocation()
	if detectedValue == "" {
		return
	}

	var zoneName string
	// For manual detection, use the value directly as the zone name
	if method == DetectionMethodManual {
		zoneName = detectedValue
	} else {
		zoneName = m.matchZone(detectedValue)
	}

	m.mu.Lock()
	previousLocation := m.currentLocation
	m.currentLocation = zoneName
	m.mu.Unlock()

	// Emit event if location changed
	if previousLocation != zoneName {
		event := LocationChangeEvent{
			PreviousLocation: previousLocation,
			CurrentLocation:  zoneName,
			Timestamp:        time.Now(),
			DetectionMethod:  method,
		}
		m.emitLocationChange(event)
		log.Printf("Location changed: %s -> %s (method: %s)", previousLocation, zoneName, method)
	}
}

// detectLocation tries detectors in priority order with fallback
func (m *Manager) detectLocation() (string, DetectionMethod) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for _, detector := range m.detectors {
		value, err := detector.Detect(ctx)
		if err == nil && value != "" {
			// Determine detection method
			var method DetectionMethod
			detectorName := detector.Name()
			switch {
			case detectorName == "manual":
				method = DetectionMethodManual
			case detectorName == "wifi-darwin" || detectorName == "wifi-linux" || detectorName == "wifi-windows" || detectorName == "mock-wifi":
				method = DetectionMethodWiFi
			default:
				method = DetectionMethodManual
			}
			return value, method
		}
	}

	return "", DetectionMethodManual
}

// matchZone finds a matching zone for the detected value
func (m *Manager) matchZone(detectedValue string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, zone := range m.zones {
		for _, rule := range zone.DetectionRules {
			if rule.Matches(detectedValue) {
				return zone.Name
			}
		}
	}

	return "Unknown"
}

// GetCurrentLocation returns the current location zone name
func (m *Manager) GetCurrentLocation() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.currentLocation
}

// SetManualLocation sets a manual location override
func (m *Manager) SetManualLocation(location string) {
	m.manualDetector.SetLocation(location)
	// Trigger immediate detection
	m.detectAndUpdate()
}

// ClearManualLocation clears the manual location override
func (m *Manager) ClearManualLocation() {
	m.manualDetector.ClearLocation()
	// Trigger immediate detection
	m.detectAndUpdate()
}

// OnLocationChange registers a callback for location change events
func (m *Manager) OnLocationChange(callback func(LocationChangeEvent)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.eventCallbacks = append(m.eventCallbacks, callback)
}

// emitLocationChange emits a location change event to all registered callbacks
func (m *Manager) emitLocationChange(event LocationChangeEvent) {
	m.mu.RLock()
	callbacks := make([]func(LocationChangeEvent), len(m.eventCallbacks))
	copy(callbacks, m.eventCallbacks)
	m.mu.RUnlock()

	for _, callback := range callbacks {
		go callback(event)
	}
}

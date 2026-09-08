package location

import (
	"context"
	"sync"
	"time"

	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/resetstate"
)

// Manager manages location detection and zone matching
type Manager struct {
	admissionGate     *resetstate.WorkGate
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
	stopping          bool
	detectionWorkers  sync.WaitGroup
	callbackWorkers   sync.WaitGroup
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

// SetAdmissionGate configures reset admission before Start or other callers.
func (m *Manager) SetAdmissionGate(gate *resetstate.WorkGate) { m.admissionGate = gate }

// SetZonesFilePath sets the file path for zone persistence
func (m *Manager) SetZonesFilePath(path string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.zonesFilePath = path
}

// PersistencePath reports the authoritative zones document.
func (m *Manager) PersistencePath() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.zonesFilePath
}

// Start begins the location detection loop
func (m *Manager) Start(ctx context.Context, interval time.Duration) {
	m.mu.Lock()
	if interval > 0 {
		m.detectionInterval = interval
	}
	m.ctx, m.cancel = context.WithCancel(ctx)
	m.stopping = false
	m.detectionWorkers.Add(1)
	m.mu.Unlock()

	// Initial detection
	_ = m.detectAndUpdate()

	// Start periodic detection
	go func() {
		defer m.detectionWorkers.Done()
		m.detectionLoop()
	}()
}

// Stop stops the location detection loop
func (m *Manager) Stop() {
	m.mu.Lock()
	m.stopping = true
	if m.cancel != nil {
		m.cancel()
	}
	m.mu.Unlock()
	m.detectionWorkers.Wait()
	m.callbackWorkers.Wait()
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
			_ = m.detectAndUpdate()
		}
	}
}

// detectAndUpdate detects location and updates if changed.
func (m *Manager) detectAndUpdate() error {
	release, err := m.admissionGate.Enter()
	if err != nil {
		return err
	}
	defer release()
	m.mu.RLock()
	stopping := m.stopping
	m.mu.RUnlock()
	if stopping {
		return context.Canceled
	}
	detectedValue, method := m.detectLocation()
	if detectedValue == "" {
		return nil
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
		logger.Debug("Location changed", logger.Fields{"previous_location": previousLocation, "zone_name": zoneName, "method": method})
	}
	return nil
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
			switch detectorName {
			case "manual":
				method = DetectionMethodManual
			case "wifi-darwin", "wifi-linux", "wifi-windows", "mock-wifi":
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

// SetManualLocation sets a manual location override.
func (m *Manager) SetManualLocation(location string) error {
	release, err := m.admissionGate.Enter()
	if err != nil {
		return err
	}
	defer release()
	m.manualDetector.SetLocation(location)
	return m.detectAndUpdate()
}

// ClearManualLocation clears the manual location override.
func (m *Manager) ClearManualLocation() error {
	release, err := m.admissionGate.Enter()
	if err != nil {
		return err
	}
	defer release()
	m.manualDetector.ClearLocation()
	return m.detectAndUpdate()
}

// OnLocationChange registers a callback for location change events
func (m *Manager) OnLocationChange(callback func(LocationChangeEvent)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.eventCallbacks = append(m.eventCallbacks, callback)
}

// emitLocationChange emits a location change event to all registered callbacks
func (m *Manager) emitLocationChange(event LocationChangeEvent) {
	type admittedCallback struct {
		callback func(LocationChangeEvent)
		release  func()
	}
	m.mu.Lock()
	if m.stopping {
		m.mu.Unlock()
		return
	}
	admitted := make([]admittedCallback, 0, len(m.eventCallbacks))
	for _, callback := range m.eventCallbacks {
		release, err := m.admissionGate.Enter()
		if err != nil {
			continue
		}
		m.callbackWorkers.Add(1)
		admitted = append(admitted, admittedCallback{callback: callback, release: release})
	}
	m.mu.Unlock()
	for _, item := range admitted {
		go func(item admittedCallback) {
			defer m.callbackWorkers.Done()
			defer item.release()
			item.callback(event)
		}(item)
	}
}

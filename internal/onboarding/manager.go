package onboarding

import (
	"encoding/json"
	"errors"
	"os"
	"sync"
	"time"

	"github.com/johnjallday/ori-agent/internal/device"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/version"
)

var (
	// ErrInvalidDeviceType is returned when an invalid device type is provided
	ErrInvalidDeviceType = errors.New("invalid device type")
)

// Manager handles onboarding state persistence and logic
type Manager struct {
	mu        sync.RWMutex
	statePath string
	state     *types.AppState
}

// NewManager creates a new onboarding manager
// It loads existing state or creates a new one if none exists
func NewManager(statePath string) *Manager {
	m := &Manager{
		statePath: statePath,
		state: &types.AppState{
			Version: version.Version,
			Onboarding: types.OnboardingState{
				Completed:      false,
				CurrentStep:    0,
				StepsCompleted: []string{},
			},
		},
	}

	// Try to load existing state
	_ = m.load()

	return m
}

// IsOnboardingComplete returns true if onboarding has been completed or skipped
func (m *Manager) IsOnboardingComplete() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state.Onboarding.Completed || !m.state.Onboarding.SkippedAt.IsZero()
}

// GetCurrentStep returns the current onboarding step (0-indexed)
func (m *Manager) GetCurrentStep() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state.Onboarding.CurrentStep
}

// GetState returns a copy of the current onboarding state
func (m *Manager) GetState() types.OnboardingState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state.Onboarding
}

// CompleteStep marks a step as completed and advances to the next step
func (m *Manager) CompleteStep(stepName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Add to completed steps if not already there
	found := false
	for _, s := range m.state.Onboarding.StepsCompleted {
		if s == stepName {
			found = true
			break
		}
	}
	if !found {
		m.state.Onboarding.StepsCompleted = append(m.state.Onboarding.StepsCompleted, stepName)
	}

	// Advance current step
	m.state.Onboarding.CurrentStep++

	return m.saveUnlocked()
}

// SetCurrentStep sets the current onboarding step
func (m *Manager) SetCurrentStep(step int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.state.Onboarding.CurrentStep = step
	return m.saveUnlocked()
}

// SkipOnboarding marks onboarding as skipped
func (m *Manager) SkipOnboarding() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.state.Onboarding.SkippedAt = time.Now()
	return m.saveUnlocked()
}

// CompleteOnboarding marks onboarding as completed
func (m *Manager) CompleteOnboarding() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.state.Onboarding.Completed = true
	m.state.Onboarding.CompletedAt = time.Now()
	return m.saveUnlocked()
}

// ResetOnboarding resets the onboarding state (useful for testing or re-onboarding)
func (m *Manager) ResetOnboarding() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.state.Onboarding = types.OnboardingState{
		Completed:      false,
		CurrentStep:    0,
		StepsCompleted: []string{},
	}
	return m.saveUnlocked()
}

// load reads the state from disk
func (m *Manager) load() error {
	data, err := os.ReadFile(m.statePath)
	if err != nil {
		if os.IsNotExist(err) {
			// File doesn't exist yet, this is fine for first run
			return nil
		}
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	return json.Unmarshal(data, m.state)
}

// saveUnlocked writes the state to disk (caller must hold lock)
func (m *Manager) saveUnlocked() error {
	data, err := json.MarshalIndent(m.state, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(m.statePath, data, 0o644)
}

// Save writes the current state to disk (with locking)
func (m *Manager) Save() error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.saveUnlocked()
}

// DetectAndStoreDevice automatically detects device information and stores it
func (m *Manager) DetectAndStoreDevice() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Skip if device has already been detected
	if m.state.Device.Detected {
		return nil
	}

	// Perform detection
	m.state.Device = device.Detect()

	return m.saveUnlocked()
}

// GetDeviceInfo returns the current device information
func (m *Manager) GetDeviceInfo() types.DeviceInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state.Device
}

// SetDeviceType allows user to manually set the device type
func (m *Manager) SetDeviceType(deviceType string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !device.ValidateDeviceType(deviceType) {
		return ErrInvalidDeviceType
	}

	m.state.Device.Type = deviceType
	m.state.Device.UserSet = true
	m.state.Device.Detected = true

	return m.saveUnlocked()
}

// IsDeviceDetected returns true if device detection has been completed
func (m *Manager) IsDeviceDetected() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state.Device.Detected
}

// GetTheme returns the current theme ("light" or "dark")
func (m *Manager) GetTheme() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.state.Theme == "" {
		return "light" // default to light
	}
	return m.state.Theme
}

// SetTheme sets the theme preference ("light" or "dark")
func (m *Manager) SetTheme(theme string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Validate theme value
	if theme != "light" && theme != "dark" {
		return errors.New("theme must be 'light' or 'dark'")
	}

	m.state.Theme = theme
	return m.saveUnlocked()
}

// GetMenuBarAutoStart returns whether auto-start on login is enabled
func (m *Manager) GetMenuBarAutoStart() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.state.MenuBar == nil {
		return false
	}
	return m.state.MenuBar.AutoStartOnLogin
}

// SetMenuBarAutoStart sets the auto-start on login preference
func (m *Manager) SetMenuBarAutoStart(enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Initialize MenuBar settings if nil
	if m.state.MenuBar == nil {
		m.state.MenuBar = &types.MenuBarSettings{}
	}

	m.state.MenuBar.AutoStartOnLogin = enabled
	return m.saveUnlocked()
}

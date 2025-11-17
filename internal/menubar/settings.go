package menubar

import (
	"github.com/johnjallday/ori-agent/internal/onboarding"
)

// SettingsManager manages menu bar app settings via the AppState
type SettingsManager struct {
	onboardingMgr *onboarding.Manager
}

// NewSettingsManager creates a new settings manager
func NewSettingsManager(onboardingMgr *onboarding.Manager) *SettingsManager {
	return &SettingsManager{
		onboardingMgr: onboardingMgr,
	}
}

// GetAutoStartEnabled returns whether auto-start on login is enabled
func (m *SettingsManager) GetAutoStartEnabled() bool {
	return m.onboardingMgr.GetMenuBarAutoStart()
}

// SetAutoStartEnabled sets the auto-start on login preference
func (m *SettingsManager) SetAutoStartEnabled(enabled bool) error {
	return m.onboardingMgr.SetMenuBarAutoStart(enabled)
}

// GetPort returns the configured server port (defaults to 8765)
func (m *SettingsManager) GetPort() int {
	return m.onboardingMgr.GetMenuBarPort()
}

// SetPort sets the server port preference
func (m *SettingsManager) SetPort(port int) error {
	return m.onboardingMgr.SetMenuBarPort(port)
}

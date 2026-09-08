package menubar

import (
	"github.com/johnjallday/ori-agent/internal/onboarding"
	"github.com/johnjallday/ori-agent/internal/resetstate"
)

// SettingsManager manages menu bar app settings via the AppState
type SettingsManager struct {
	onboardingMgr *onboarding.Manager
	admissionGate *resetstate.WorkGate
}

// NewSettingsManager creates a new settings manager
func NewSettingsManager(onboardingMgr *onboarding.Manager) *SettingsManager {
	return NewSettingsManagerWithAdmission(onboardingMgr, nil)
}

// NewSettingsManagerWithAdmission shares the host's process-lifetime gate, not
// the replaceable server's lifetime. A nil gate is an unsupported reset host.
func NewSettingsManagerWithAdmission(onboardingMgr *onboarding.Manager, gate *resetstate.WorkGate) *SettingsManager {
	return &SettingsManager{onboardingMgr: onboardingMgr, admissionGate: gate}
}

// WithMutation admits a whole menu action, including any native prompt or
// LaunchAgent effect before saving settings. Guarding the final save alone
// would be too late. Nested settings setters retain the same admission.
func (m *SettingsManager) WithMutation(fn func() error) error {
	release, err := m.admissionGate.Enter()
	if err != nil {
		return err
	}
	defer release()
	return fn()
}

// GetAutoStartEnabled returns whether auto-start on login is enabled
func (m *SettingsManager) GetAutoStartEnabled() bool {
	return m.onboardingMgr.GetMenuBarAutoStart()
}

// SetAutoStartEnabled sets the auto-start on login preference
func (m *SettingsManager) SetAutoStartEnabled(enabled bool) error {
	return m.WithMutation(func() error { return m.onboardingMgr.SetMenuBarAutoStart(enabled) })
}

// GetPort returns the configured server port (defaults to 8765)
func (m *SettingsManager) GetPort() int {
	return m.onboardingMgr.GetMenuBarPort()
}

// SetPort sets the server port preference
func (m *SettingsManager) SetPort(port int) error {
	return m.WithMutation(func() error { return m.onboardingMgr.SetMenuBarPort(port) })
}

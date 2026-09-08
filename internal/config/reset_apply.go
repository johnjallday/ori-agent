package config

import "reflect"

// ResetPreferencesOptions names the retained settings fields that are owned by
// categories outside Settings. Vault roots are retained whenever vault backing
// files are retained. Workspace roots are retained unless app registrations are
// part of the same reviewed reset.
type ResetPreferencesOptions struct {
	PreserveWorkspaceRoot bool
	PreserveVaultRoot     bool
}

// ResetPreferences replaces global preferences with canonical defaults while
// retaining only explicitly requested cross-category roots. It rolls memory
// back when persistence fails so callers cannot observe unverified success.
func (m *Manager) ResetPreferences(options ResetPreferencesOptions) error {
	m.mu.Lock()
	previous := m.settings
	reset := defaultSettings()
	if options.PreserveWorkspaceRoot {
		reset.WorkspaceRoot = previous.WorkspaceRoot
		reset.WorkspaceRootConfirmed = previous.WorkspaceRootConfirmed
	}
	if options.PreserveVaultRoot {
		reset.VaultRoot = previous.VaultRoot
	}
	m.settings = reset
	m.mu.Unlock()

	if err := m.Save(); err != nil {
		m.mu.Lock()
		m.settings = previous
		m.mu.Unlock()
		return err
	}
	return nil
}

// ResetPreferencesVerified reports whether memory matches canonical defaults
// plus the explicitly retained cross-category roots. It does not consult
// environment variables or external authentication.
func (m *Manager) ResetPreferencesVerified(options ResetPreferencesOptions) bool {
	m.mu.RLock()
	actual := m.settings
	m.mu.RUnlock()
	expected := defaultSettings()
	if options.PreserveWorkspaceRoot {
		expected.WorkspaceRoot = actual.WorkspaceRoot
		expected.WorkspaceRootConfirmed = actual.WorkspaceRootConfirmed
	}
	if options.PreserveVaultRoot {
		expected.VaultRoot = actual.VaultRoot
	}
	// Save validates and canonicalizes collection and path fields. Apply the
	// same pure normalization to expected defaults so nil/empty collection
	// representation cannot make a successful durable reset unverifiable.
	canonical := &Manager{settings: expected}
	if err := canonical.validate(); err != nil {
		return false
	}
	return reflect.DeepEqual(actual, canonical.settings)
}

// ClearWorkspaceRegistration removes automatic workspace-root adoption consent
// without changing unrelated preferences or retained vault/template roots.
func (m *Manager) ClearWorkspaceRegistration() error {
	m.mu.Lock()
	previousRoot := m.settings.WorkspaceRoot
	previousConfirmed := m.settings.WorkspaceRootConfirmed
	m.settings.WorkspaceRoot = ""
	m.settings.WorkspaceRootConfirmed = false
	m.mu.Unlock()

	if err := m.Save(); err != nil {
		m.mu.Lock()
		m.settings.WorkspaceRoot = previousRoot
		m.settings.WorkspaceRootConfirmed = previousConfirmed
		m.mu.Unlock()
		return err
	}
	return nil
}

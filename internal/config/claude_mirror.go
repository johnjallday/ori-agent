package config

import "encoding/json"

// ClaudeStatuslineMirror avoids retrieving unrelated settings or vault secrets.
func (m *Manager) ClaudeStatuslineMirror() (bool, json.RawMessage) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.settings.ClaudeStatuslineMirrorInstalled, append(json.RawMessage(nil), m.settings.ClaudeStatuslineMirrorPrevious...)
}

// SetClaudeStatuslineMirror updates only the mirror fields. The installer owns
// serialization of install/remove operations and calls Save before changing
// Claude's settings, so the restoration record is durable first.
func (m *Manager) SetClaudeStatuslineMirror(installed bool, previous json.RawMessage) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.settings.ClaudeStatuslineMirrorInstalled = installed
	m.settings.ClaudeStatuslineMirrorPrevious = append(json.RawMessage(nil), previous...)
}

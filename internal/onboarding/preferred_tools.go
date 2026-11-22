package onboarding

import (
	"strings"

	"github.com/johnjallday/ori-agent/internal/types"
)

// AddPreferredTools merges into the latest profile under the manager's write
// lock. No personalization completion, inferred-at stamp or saved-app observer.
func (m *Manager) AddPreferredTools(names []string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	previous := m.state.UserProfile
	profile := copyInferredProfile(previous)
	if profile == nil {
		profile = &types.InferredProfile{}
	}
	seen := map[string]bool{}
	for _, name := range profile.PreferredTools {
		seen[strings.ToLower(strings.TrimSpace(name))] = true
	}
	changed := false
	for _, name := range names {
		name = strings.TrimSpace(name)
		key := strings.ToLower(name)
		if key != "" && !seen[key] {
			seen[key] = true
			profile.PreferredTools = append(profile.PreferredTools, name)
			changed = true
		}
	}
	if changed {
		m.state.UserProfile = profile
		if err := m.saveUnlocked(); err != nil {
			m.state.UserProfile = previous
			return nil, err
		}
	}
	return append([]string{}, profile.PreferredTools...), nil
}

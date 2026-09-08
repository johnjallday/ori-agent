package onboarding

import (
	"errors"
	"reflect"
	"time"

	"github.com/johnjallday/ori-agent/internal/types"
)

// ResetFirstRunVerified replaces only the app-state owner's document with its
// canonical first-run value. Other reset categories own config, agents,
// relational records and transient state.
func (m *Manager) ResetFirstRunVerified() error {
	m.mu.Lock()
	previous := m.state
	m.state = newManagerDefaults(m.statePath).state
	if err := m.saveUnlocked(); err != nil {
		m.state = previous
		m.mu.Unlock()
		return err
	}
	m.mu.Unlock()

	verified, err := OpenForReset(m.statePath)
	if err != nil {
		return err
	}
	if !verified.IsCanonicalFirstRun() {
		return errors.New("first-run app state did not verify canonical")
	}
	return nil
}

// IsCanonicalFirstRun compares every app-state field except the creation time
// attached to canonical zero-progress state.
func (m *Manager) IsCanonicalFirstRun() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return isCanonicalFirstRun(m.state, m.statePath)
}

func isCanonicalFirstRun(state *types.AppState, statePath string) bool {
	if state == nil {
		return false
	}
	candidate := *state
	expected := *newManagerDefaults(statePath).state
	if candidate.AssistantProgress != nil {
		progress := *candidate.AssistantProgress
		progress.UpdatedAt = time.Time{}
		candidate.AssistantProgress = &progress
	}
	if expected.AssistantProgress != nil {
		progress := *expected.AssistantProgress
		progress.UpdatedAt = time.Time{}
		expected.AssistantProgress = &progress
	}
	return reflect.DeepEqual(&candidate, &expected)
}

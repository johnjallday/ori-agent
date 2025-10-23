package onboarding

import (
	"encoding/json"
	"os"
	"sync"
	"time"

	"github.com/johnjallday/dolphin-agent/internal/types"
	"github.com/johnjallday/dolphin-agent/internal/version"
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

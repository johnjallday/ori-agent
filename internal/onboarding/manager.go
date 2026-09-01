package onboarding

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/johnjallday/ori-agent/internal/device"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/version"
)

var (
	// ErrInvalidDeviceType is returned when an invalid device type is provided
	ErrInvalidDeviceType = errors.New("invalid device type")
)

const DefaultAssistantName = "Ori"

// Manager handles onboarding state persistence and logic
type Manager struct {
	mu        sync.RWMutex
	statePath string
	state     *types.AppState
	userStore userprofile.UserStore
}

// NewManager loads existing state or initializes a canonical onboarding state.
// Personal-assistant onboarding is the only supported first-run path; older
// state files are upgraded in memory and persisted by the next write.
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
			AssistantProgress: types.NewAssistantProgress(),
			AssistantName:     DefaultAssistantName,
		},
	}

	// A missing or unreadable file falls back to canonical defaults so
	// onboarding remains recoverable.
	if err := m.load(); err != nil {
		logger.Verbosef("Warning: failed to load onboarding state from %s: %v", statePath, err)
	}

	m.mu.Lock()
	m.ensureStateDefaultsUnlocked()
	m.mu.Unlock()

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
		StepsSkipped:   []string{},
	}
	return m.saveUnlocked()
}

// SkipStep marks a step as skipped and advances to the next step
func (m *Manager) SkipStep(stepName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Add to skipped steps if not already there
	found := false
	for _, s := range m.state.Onboarding.StepsSkipped {
		if s == stepName {
			found = true
			break
		}
	}
	if !found {
		m.state.Onboarding.StepsSkipped = append(m.state.Onboarding.StepsSkipped, stepName)
	}

	// Advance current step
	m.state.Onboarding.CurrentStep++

	return m.saveUnlocked()
}

// GetSkippedSteps returns a copy of the skipped steps slice
func (m *Manager) GetSkippedSteps() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.state.Onboarding.StepsSkipped == nil {
		return []string{}
	}

	// Return a copy to prevent external modification
	result := make([]string, len(m.state.Onboarding.StepsSkipped))
	copy(result, m.state.Onboarding.StepsSkipped)
	return result
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

	if err := json.Unmarshal(data, m.state); err != nil {
		return err
	}
	m.ensureStateDefaultsUnlocked()
	return nil
}

// saveUnlocked writes the state to disk (caller must hold lock)
func (m *Manager) saveUnlocked() error {
	m.ensureStateDefaultsUnlocked()

	data, err := json.MarshalIndent(m.state, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(m.statePath, data, 0o600)
}

// Save writes the current state to disk (with locking)
func (m *Manager) Save() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.saveUnlocked()
}

// GetAssistantProgress returns a copy of assistant progression data.
func (m *Manager) GetAssistantProgress() types.AssistantProgress {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.state.AssistantProgress == nil {
		return *types.NewAssistantProgress()
	}

	progress := *m.state.AssistantProgress
	progress.EnsureDefaults()
	return progress
}

// SetAssistantProgress stores assistant progression data.
func (m *Manager) SetAssistantProgress(progress *types.AssistantProgress) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if progress == nil {
		m.state.AssistantProgress = types.NewAssistantProgress()
		return m.saveUnlocked()
	}

	cp := *progress
	cp.EnsureDefaults()
	m.state.AssistantProgress = &cp
	return m.saveUnlocked()
}

func (m *Manager) ensureStateDefaultsUnlocked() {
	if m.state == nil {
		m.state = &types.AppState{}
	}
	if m.state.AssistantProgress == nil {
		m.state.AssistantProgress = types.NewAssistantProgress()
	} else {
		m.state.AssistantProgress.EnsureDefaults()
	}
	if m.state.AssistantName == "" {
		m.state.AssistantName = DefaultAssistantName
	}
}

// GetProgression returns a copy of the onboarding quest-log progression state.
// It satisfies the progression package's state-store contract, keeping
// app_state.json's single serialized writer.
func (m *Manager) GetProgression() types.ProgressionState {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.state.Progression == nil {
		return types.ProgressionState{}
	}
	return cloneProgression(*m.state.Progression)
}

// SetProgression persists the onboarding quest-log progression state.
func (m *Manager) SetProgression(p types.ProgressionState) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cp := cloneProgression(p)
	m.state.Progression = &cp
	return m.saveUnlocked()
}

// cloneProgression deep-copies the completion and skip maps so callers can't
// mutate persisted state through a shared reference.
func cloneProgression(p types.ProgressionState) types.ProgressionState {
	if p.CompletedQuests != nil {
		completed := make(map[string]time.Time, len(p.CompletedQuests))
		for id, at := range p.CompletedQuests {
			completed[id] = at
		}
		p.CompletedQuests = completed
	}
	if p.SkippedQuests != nil {
		skipped := make(map[string]time.Time, len(p.SkippedQuests))
		for id, at := range p.SkippedQuests {
			skipped[id] = at
		}
		p.SkippedQuests = skipped
	}
	return p
}

func (m *Manager) SetUserStore(store userprofile.UserStore) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.userStore = store
}

func (m *Manager) userProfileStore() userprofile.UserStore {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.userStore
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

// RedetectDevice forces a fresh detection of device hardware
func (m *Manager) RedetectDevice() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Preserve user-set device type if applicable
	preservedType := ""
	if m.state.Device.UserSet {
		preservedType = m.state.Device.Type
	}

	// Perform fresh detection
	m.state.Device = device.Detect()

	// Restore user-set type if it was set
	if preservedType != "" {
		m.state.Device.Type = preservedType
		m.state.Device.UserSet = true
	}

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

// GetMenuBarPort returns the configured server port (defaults to 8765)
func (m *Manager) GetMenuBarPort() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.state.MenuBar == nil || m.state.MenuBar.Port == 0 {
		return 8765 // default port
	}
	return m.state.MenuBar.Port
}

// SetMenuBarPort sets the server port preference
func (m *Manager) SetMenuBarPort(port int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Initialize MenuBar settings if nil
	if m.state.MenuBar == nil {
		m.state.MenuBar = &types.MenuBarSettings{}
	}

	m.state.MenuBar.Port = port
	return m.saveUnlocked()
}

// GetNotesOpenBehavior returns the user's preferred way to open a note:
// "modal" (default), "page" (navigate to the focused note page), or
// "page-new-tab".
func (m *Manager) GetNotesOpenBehavior() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.state.NotesOpenBehavior == "" {
		return "modal"
	}
	return m.state.NotesOpenBehavior
}

// SetNotesOpenBehavior persists the user's preferred note-opening behavior.
// Accepts "modal", "page", or "page-new-tab".
func (m *Manager) SetNotesOpenBehavior(behavior string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	switch behavior {
	case "modal", "page", "page-new-tab":
		// valid
	default:
		return errors.New("notes_open_behavior must be 'modal', 'page', or 'page-new-tab'")
	}

	m.state.NotesOpenBehavior = behavior
	return m.saveUnlocked()
}

// GetUserProfile returns the stored user profile (may be nil)
func (m *Manager) GetUserProfile() *types.InferredProfile {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state.UserProfile
}

// SetUserProfile stores the user's inferred profile
func (m *Manager) SetUserProfile(profile *types.InferredProfile) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if profile != nil && profile.InferredAt.IsZero() {
		profile.InferredAt = time.Now()
	}
	m.state.UserProfile = profile
	return m.saveUnlocked()
}

// GetNames returns persisted display names for onboarding.
func (m *Manager) GetNames() (userName string, assistantName string) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.state.UserName, m.getAssistantNameLocked()
}

// SetNames stores display names for the user and assistant.
func (m *Manager) SetNames(userName, assistantName string) error {
	m.mu.Lock()

	m.state.UserName = userName
	m.state.AssistantName = strings.TrimSpace(assistantName)
	if m.state.AssistantName == "" {
		m.state.AssistantName = DefaultAssistantName
	}
	store := m.userStore
	err := m.saveUnlocked()
	m.mu.Unlock()
	if err != nil {
		return err
	}
	return updateLocalProfile(context.Background(), store, func(profile *userprofile.UserProfile) {
		profile.DisplayName = strings.TrimSpace(userName)
	})
}

func (m *Manager) GetTimezone() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return strings.TrimSpace(m.state.Timezone)
}

func (m *Manager) SetTimezone(timezone string) error {
	timezone = strings.TrimSpace(timezone)
	m.mu.Lock()
	m.state.Timezone = timezone
	store := m.userStore
	err := m.saveUnlocked()
	m.mu.Unlock()
	if err != nil {
		return err
	}
	return updateLocalProfile(context.Background(), store, func(profile *userprofile.UserProfile) {
		profile.Timezone = timezone
	})
}

func (m *Manager) SeedLocalUserProfile(ctx context.Context) error {
	store := m.userProfileStore()
	if store == nil {
		return nil
	}
	current, err := store.Get(ctx, userprofile.LocalUserID)
	if err != nil && !errors.Is(err, userprofile.ErrNotFound) {
		return err
	}
	if current != nil && !current.IsEmpty() {
		return nil
	}

	m.mu.RLock()
	displayName := strings.TrimSpace(m.state.UserName)
	timezone := strings.TrimSpace(m.state.Timezone)
	var roleCategory string
	var specializations []string
	if m.state.UserProfile != nil {
		roleCategory = strings.TrimSpace(m.state.UserProfile.PrimaryCategory)
		specializations = append([]string(nil), m.state.UserProfile.Specializations...)
	}
	m.mu.RUnlock()

	return store.Upsert(ctx, &userprofile.UserProfile{
		ID:              userprofile.LocalUserID,
		DisplayName:     displayName,
		Timezone:        timezone,
		RoleCategory:    roleCategory,
		Specializations: specializations,
	})
}

func (m *Manager) getAssistantNameLocked() string {
	name := strings.TrimSpace(m.state.AssistantName)
	if name == "" {
		return DefaultAssistantName
	}
	return name
}

func updateLocalProfile(ctx context.Context, store userprofile.UserStore, mutate func(*userprofile.UserProfile)) error {
	if store == nil {
		return nil
	}
	profile, err := store.Get(ctx, userprofile.LocalUserID)
	if errors.Is(err, userprofile.ErrNotFound) {
		profile = &userprofile.UserProfile{ID: userprofile.LocalUserID}
	} else if err != nil {
		return err
	}
	mutate(profile)
	return store.Upsert(ctx, profile)
}

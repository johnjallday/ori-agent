package filejanitor

import (
	"errors"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacecapability"
)

// AutomationReview is the effective, path-free consent binding used by
// assistant-led setup. It is derived fresh from canonical settings and the
// workspace's immutable automation-recipe snapshot.
type AutomationReview struct {
	RootID                 string
	SettingsUpdatedAt      time.Time
	ContentMode            ContentMode
	DailyScanLocalTime     string
	Timezone               string
	TimezoneConfigured     bool
	WatchEvents            []string
	WatchDebounceSecond    int
	SettlingSecond         int
	AutomationApproved     bool
	Paused                 bool
	LastAssistedAutomation *AssistedAutomationReceipt
}

// AssistedAutomationOperation is the path-free coordinator authority stamped
// into File Janitor settings before assisted monitoring is activated.
type AssistedAutomationOperation struct {
	RunID             string
	OperationID       string
	ReviewRevision    string
	AuthorityRevision string
}

func normalizeAssistedAutomationOperation(operation AssistedAutomationOperation) (AssistedAutomationOperation, error) {
	operation.RunID = strings.TrimSpace(operation.RunID)
	operation.OperationID = strings.TrimSpace(operation.OperationID)
	operation.ReviewRevision = strings.TrimSpace(operation.ReviewRevision)
	operation.AuthorityRevision = strings.TrimSpace(operation.AuthorityRevision)
	if operation.RunID == "" || operation.OperationID == "" || operation.ReviewRevision == "" || operation.AuthorityRevision == "" {
		return AssistedAutomationOperation{}, ErrInvalidSettings
	}
	return operation, nil
}

func automationReceiptMatches(receipt *AssistedAutomationReceipt, operation AssistedAutomationOperation) bool {
	return receipt != nil && receipt.RunID == operation.RunID && receipt.OperationID == operation.OperationID &&
		receipt.ReviewRevision == operation.ReviewRevision && receipt.AuthorityRevision == operation.AuthorityRevision
}

func automationReceipt(operation AssistedAutomationOperation, state AssistedAutomationState, now time.Time) *AssistedAutomationReceipt {
	return &AssistedAutomationReceipt{
		RunID: operation.RunID, OperationID: operation.OperationID,
		ReviewRevision: operation.ReviewRevision, AuthorityRevision: operation.AuthorityRevision,
		State: state, UpdatedAt: now,
	}
}

func (s *Service) ReviewAutomation(workspaceID string) (AutomationReview, error) {
	settings, err := s.requireConfigured(workspaceID)
	if err != nil {
		return AutomationReview{}, err
	}
	if strings.TrimSpace(settings.RootID) == "" {
		return AutomationReview{}, ErrInvalidSettings
	}
	recipe := s.automationRecipe(workspaceID)
	events := []string{"create", "rename"}
	debounce := int(DefaultWatchDebounce / time.Second)
	settling := int(SettleInterval / time.Second)
	if recipe.Watch != nil {
		if len(recipe.Watch.Events) > 0 {
			events = append([]string(nil), recipe.Watch.Events...)
		}
		if recipe.Watch.DebounceSeconds > 0 {
			debounce = recipe.Watch.DebounceSeconds
		}
	}
	dailyTime := settings.DailyScanLocalTime
	if recipe.DailyScan != nil && strings.TrimSpace(dailyTime) == "" {
		dailyTime = recipe.DailyScan.LocalTime
	}
	if strings.TrimSpace(dailyTime) == "" {
		dailyTime = DefaultDailyScanLocalTime
	}
	timezone := strings.TrimSpace(settings.Timezone)
	if timezone == "" {
		timezone = time.Now().Location().String()
		if timezone == "" {
			timezone = "Local"
		}
	}
	var assisted *AssistedAutomationReceipt
	if settings.LastAssistedAutomation != nil {
		receipt := *settings.LastAssistedAutomation
		assisted = &receipt
	}
	return AutomationReview{
		RootID: settings.RootID, SettingsUpdatedAt: settings.UpdatedAt,
		ContentMode: settings.ContentMode, DailyScanLocalTime: dailyTime, Timezone: timezone,
		TimezoneConfigured: strings.TrimSpace(settings.Timezone) != "",
		WatchEvents:        events, WatchDebounceSecond: debounce, SettlingSecond: settling,
		AutomationApproved: !settings.AutomationApprovedAt.IsZero(), Paused: settings.Paused,
		LastAssistedAutomation: assisted,
	}, nil
}

// RecordAutomationApprovalPaused records the separate monitoring consent while
// keeping unattended work stopped. The caller activates and health-checks the
// watcher immediately afterwards; a crash between these steps therefore leaves
// approval visible but cannot start hidden background work.
// ApplyReviewedAutomationTimezone persists an otherwise-unset timezone only as
// part of the user's monitoring approval. It never overwrites a timezone or
// schedule changed in another tab after the review was shown.
func (s *Service) ApplyReviewedAutomationTimezone(workspaceID, rootID string, privacy ContentMode, dailyTime, timezone string) error {
	timezone = strings.TrimSpace(timezone)
	if timezone == "" {
		return setupErr(CodeFolderChanged, "Choose a timezone before starting the daily scan.", RepairRetry, nil)
	}
	if _, err := time.LoadLocation(timezone); err != nil {
		return setupErr(CodeFolderChanged, "The reviewed timezone is no longer available. Review the schedule again.", RepairRetry, err)
	}
	_, err := s.store.UpdateSettings(workspaceID, func(settings *JanitorSettings) error {
		if settings.RootID != strings.TrimSpace(rootID) || settings.ContentMode != privacy ||
			settings.DailyScanLocalTime != strings.TrimSpace(dailyTime) {
			return setupErr(CodeFolderChanged, "The File Janitor folder, privacy, or schedule changed. Review monitoring again.", RepairRetry, nil)
		}
		if current := strings.TrimSpace(settings.Timezone); current != "" && current != timezone {
			return setupErr(CodeFolderChanged, "The File Janitor timezone changed. Review monitoring again.", RepairRetry, nil)
		}
		settings.Timezone = timezone
		settings.LastAssistedAutomation = nil
		return nil
	})
	return err
}

func validateReviewedAutomation(settings *JanitorSettings, rootID string, privacy ContentMode, dailyTime, timezone string) error {
	if settings == nil || settings.RootID != strings.TrimSpace(rootID) || settings.ContentMode != privacy ||
		settings.DailyScanLocalTime != strings.TrimSpace(dailyTime) || strings.TrimSpace(settings.Timezone) != strings.TrimSpace(timezone) {
		return setupErr(CodeFolderChanged, "The File Janitor folder, privacy, or schedule changed. Review monitoring again.", RepairRetry, nil)
	}
	return nil
}

// RecordReviewedAutomationApprovalPaused atomically binds consent to the exact
// reviewed root/privacy/schedule and keeps unattended work off. The operation
// marker is the proof that a later automatic retry is continuing this consent,
// rather than treating a manual pause as coordinator authority.
func (s *Service) RecordReviewedAutomationApprovalPaused(workspaceID, rootID string, privacy ContentMode, dailyTime, timezone string, expectedApproved, expectedPaused bool, operation AssistedAutomationOperation) (Status, error) {
	operation, err := normalizeAssistedAutomationOperation(operation)
	if err != nil {
		return Status{}, err
	}
	timezone = strings.TrimSpace(timezone)
	if timezone == "" {
		return Status{}, setupErr(CodeFolderChanged, "Choose a timezone before starting the daily scan.", RepairRetry, nil)
	}
	if _, err := time.LoadLocation(timezone); err != nil {
		return Status{}, setupErr(CodeFolderChanged, "The reviewed timezone is no longer available. Review the schedule again.", RepairRetry, err)
	}
	if _, err = s.store.UpdateSettings(workspaceID, func(settings *JanitorSettings) error {
		if settings.RootID != strings.TrimSpace(rootID) || settings.ContentMode != privacy ||
			settings.DailyScanLocalTime != strings.TrimSpace(dailyTime) {
			return setupErr(CodeFolderChanged, "The File Janitor folder, privacy, or schedule changed. Review monitoring again.", RepairRetry, nil)
		}
		if current := strings.TrimSpace(settings.Timezone); current != "" && current != timezone {
			return setupErr(CodeFolderChanged, "The File Janitor timezone changed. Review monitoring again.", RepairRetry, nil)
		}
		if (!settings.AutomationApprovedAt.IsZero()) != expectedApproved || settings.Paused != expectedPaused {
			return setupErr(CodeFolderChanged, "The File Janitor monitoring state changed. Review it again.", RepairRetry, nil)
		}
		settings.Timezone = timezone
		if settings.AutomationApprovedAt.IsZero() {
			settings.AutomationApprovedAt = s.clock()
		}
		settings.Paused = true
		settings.LastAssistedAutomation = automationReceipt(operation, AssistedAutomationApprovalPaused, s.clock())
		return nil
	}); err != nil {
		return Status{}, err
	}
	return s.Status(workspaceID)
}

// ActivateReviewedAutomation revalidates the accepted authority immediately
// before unpausing; a concurrent root/privacy/schedule or operation-marker
// change fails closed.
func (s *Service) ActivateReviewedAutomation(workspaceID, rootID string, privacy ContentMode, dailyTime, timezone string, operation AssistedAutomationOperation) (Status, error) {
	operation, err := normalizeAssistedAutomationOperation(operation)
	if err != nil {
		return Status{}, err
	}
	if _, err = s.store.UpdateSettings(workspaceID, func(settings *JanitorSettings) error {
		if err := validateReviewedAutomation(settings, rootID, privacy, dailyTime, timezone); err != nil {
			return err
		}
		if settings.AutomationApprovedAt.IsZero() || !automationReceiptMatches(settings.LastAssistedAutomation, operation) ||
			settings.LastAssistedAutomation.State != AssistedAutomationApprovalPaused {
			return setupErr(CodePrivacyReviewRequired, "Review monitoring before starting it.", RepairRetry, nil)
		}
		settings.Paused = false
		settings.LastAssistedAutomation = automationReceipt(operation, AssistedAutomationActive, s.clock())
		return nil
	}); err != nil {
		return Status{}, err
	}
	return s.Status(workspaceID)
}

// PauseReviewedAutomation rolls back only while the same reviewed authority
// and operation marker are still current, so recovery never pauses a manually
// changed or newly configured root from another tab.
func (s *Service) PauseReviewedAutomation(workspaceID, rootID string, privacy ContentMode, dailyTime, timezone string, operation AssistedAutomationOperation) error {
	operation, err := normalizeAssistedAutomationOperation(operation)
	if err != nil {
		return err
	}
	_, err = s.store.UpdateSettings(workspaceID, func(settings *JanitorSettings) error {
		if err := validateReviewedAutomation(settings, rootID, privacy, dailyTime, timezone); err != nil {
			return err
		}
		if !automationReceiptMatches(settings.LastAssistedAutomation, operation) ||
			(settings.LastAssistedAutomation.State != AssistedAutomationApprovalPaused && settings.LastAssistedAutomation.State != AssistedAutomationActive) {
			return setupErr(CodeFolderChanged, "The File Janitor monitoring state changed. Review it again.", RepairRetry, nil)
		}
		settings.Paused = true
		settings.LastAssistedAutomation = automationReceipt(operation, AssistedAutomationFailedPaused, s.clock())
		return nil
	})
	return err
}

func (s *Service) RecordAutomationApprovalPaused(workspaceID string) (Status, error) {
	if _, err := s.store.UpdateSettings(workspaceID, func(settings *JanitorSettings) error {
		if !settings.IsSetUp() {
			return setupErr(CodeNotConfigured, "Choose a folder before starting monitoring.", RepairChooseFolder, nil)
		}
		if settings.ContentMode != ContentModeMetadataOnly {
			return setupErr(CodePrivacyReviewRequired, "Review privacy settings before starting assisted monitoring.", RepairRetry, nil)
		}
		if settings.AutomationApprovedAt.IsZero() {
			settings.AutomationApprovedAt = s.clock()
		}
		settings.Paused = true
		settings.LastAssistedAutomation = nil
		return nil
	}); err != nil {
		return Status{}, err
	}
	return s.Status(workspaceID)
}

func (s *Service) automationRecipe(workspaceID string) workspace.AutomationRecipe {
	ws, err := s.readWorkspace(workspaceID)
	if err != nil || ws == nil {
		return workspace.AutomationRecipe{}
	}
	for _, key := range []string{
		DirectoryRequirementKey,
		workspacecapability.FileJanitorDefinition().Setup.DirectoryRequirementKey,
	} {
		if recipe, ok := ws.TemplateAutomationRecipeFor(key); ok {
			return recipe
		}
	}
	return workspace.AutomationRecipe{}
}

// RequireHealthyAutomation returns a bounded error when approval is saved but
// the watcher or scheduler did not become healthy.
func RequireHealthyAutomation(status Status) error {
	for _, component := range []ReadinessComponent{ComponentWatcher, ComponentScheduler} {
		check, ok := findCheck(status.Readiness, component)
		if !ok || check.Status != ComponentOK {
			return errors.New("file janitor monitoring is not healthy")
		}
	}
	return nil
}

package filejanitor

import "time"

// SetupMode is a human decision, never inferred from a paused flag or a model.
type SetupMode string

const (
	SetupModeManual     SetupMode = "manual"
	SetupModeMonitoring SetupMode = "monitoring"
)

// SetupDecision is root-scoped consent. Monitoring's existing approval timestamp
// remains its authority; this record distinguishes an explicit manual choice
// from unfinished setup. It carries neither a path nor permission for file moves.
type SetupDecision struct {
	Mode      SetupMode `json:"mode"`
	RootID    string    `json:"root_id"`
	DecidedAt time.Time `json:"decided_at"`
}

func (d SetupDecision) validFor(settings JanitorSettings) bool {
	return (d.Mode == SetupModeManual || d.Mode == SetupModeMonitoring) &&
		d.RootID != "" && d.RootID == settings.RootID && !d.DecidedAt.IsZero() && settings.IsSetUp() &&
		((d.Mode == SetupModeManual && settings.AutomationApprovedAt.IsZero()) ||
			(d.Mode == SetupModeMonitoring && !settings.AutomationApprovedAt.IsZero()))
}

// ManualScansOnly is deliberately stricter than Paused: no legacy missing field
// or unknown decision can finish setup, and a prior monitoring approval is never
// silently reinterpreted as a new manual choice.
func (s JanitorSettings) ManualScansOnly() bool {
	return s.SetupDecision != nil && s.SetupDecision.Mode == SetupModeManual && s.SetupDecision.validFor(s)
}

// RecordManualSetupDecision commits a reviewed choice for a newly granted root.
// The caller must provide the exact settings revision it reviewed. This method
// never grants access, scans, changes privacy/schedule, or replaces prior approval.
// Replays are observed through canonical state by the preparation caller, rather
// than treating a stale timestamp as fresh authorization.
func (s *Service) RecordManualSetupDecision(workspaceID, rootID string, reviewedAt time.Time) (Status, error) {
	return s.recordManualSetupDecision(workspaceID, rootID, reviewedAt, "")
}

func (s *Service) recordManualSetupDecision(workspaceID, rootID string, reviewedAt time.Time, preparationID string) (Status, error) {
	_, err := s.store.UpdateSettings(workspaceID, func(settings *JanitorSettings) error {
		if err := s.validatePreparationBoundary(*settings, preparationID); err != nil {
			return err
		}
		if !settings.IsSetUp() || rootID == "" || settings.RootID != rootID ||
			reviewedAt.IsZero() || !settings.UpdatedAt.Equal(reviewedAt) || !settings.AccessRevokedAt.IsZero() {
			return setupErr(CodeFolderChanged, "Folder setup changed. Review the current folder before continuing.", RepairRetry, nil)
		}
		if settings.ContentMode != ContentModeMetadataOnly {
			return setupErr(CodePrivacyReviewRequired, "Review the current privacy settings before preparing a metadata-only review.", RepairRetry, nil)
		}
		if !settings.Paused || !settings.AutomationApprovedAt.IsZero() || settings.LastAssistedAutomation != nil {
			return setupErr(CodeFolderChanged, "Monitoring has already been configured. Keep its current settings or review a change separately.", RepairRetry, nil)
		}
		if preparationID == "" {
			invalidateSetupPreparation(settings)
		}
		settings.SetupDecision = &SetupDecision{Mode: SetupModeManual, RootID: rootID, DecidedAt: s.clock()}
		return nil
	})
	if err != nil {
		return Status{}, err
	}
	return s.Status(workspaceID)
}

func recordMonitoringDecision(settings *JanitorSettings) {
	settings.SetupDecision = nil
	if settings.IsSetUp() && settings.RootID != "" && !settings.AutomationApprovedAt.IsZero() {
		settings.SetupDecision = &SetupDecision{
			Mode: SetupModeMonitoring, RootID: settings.RootID, DecidedAt: settings.AutomationApprovedAt,
		}
	}
}

func refuseUnreviewedManualActivation(settings JanitorSettings, paused bool) error {
	if !paused && settings.ManualScansOnly() {
		return setupErr(CodePrivacyReviewRequired, "Review and approve monitoring before starting automatic scans.", RepairRetry, nil)
	}
	return nil
}

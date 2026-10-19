package filejanitor

import (
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// PreparationAutomation is the existing watcher/daily lifecycle, not another
// scheduler. Manual preparation does not require it.
type PreparationAutomation interface {
	EnsureWatcher(string) error
	WatcherRecord(string) (string, bool, error)
	MarkCaughtUp(string, string)
}

type PreparationRequest struct {
	WorkspaceID        string
	RootID             string
	Monitoring         bool
	DailyScanLocalTime string
	Timezone           string
	WasApproved        bool
	WasPaused          bool
	Operation          AssistedAutomationOperation
	ScanOperationID    string
	BeforeScan         func() error
	// Ephemeral proof of a newly persisted dispatch, never reconstructed on replay.
	FreshScanAttempt func() bool
}

type PreparationResult struct {
	Status               Status
	Scan                 ScanOperationReceipt
	WatcherID            string
	MonitoringApplied    bool
	MonitoringRetryBound bool
	ScanAttempted        bool
	ScanConfirmed        bool
}

// PrepareMetadataReview is the shared canonical seam for assistant-led and
// attached-folder setup. It records separately reviewed monitoring only when
// selected, requires actual health, and runs/replays one metadata-only scan.
// It never grants a folder or creates a workspace/agent. Callers own admission,
// human review, lifecycle revalidation and a stable scan-operation identity.
func (s *Service) PrepareMetadataReview(request PreparationRequest, automation PreparationAutomation) (result PreparationResult, err error) {
	status, err := s.Status(request.WorkspaceID)
	if err != nil {
		return result, err
	}
	if err := requirePreparation(status.Settings, request.Operation.PreparationID); err != nil {
		return result, err
	}
	settings := status.Settings
	if !settings.IsSetUp() || settings.RootID != request.RootID || settings.ContentMode != ContentModeMetadataOnly ||
		settings.DailyScanLocalTime != request.DailyScanLocalTime ||
		(settings.Timezone != "" && settings.Timezone != request.Timezone) || strings.TrimSpace(request.ScanOperationID) == "" {
		return result, setupErr(CodeFolderChanged, "The folder, privacy, or schedule changed. Review setup again.", RepairRetry, nil)
	}
	if !request.Monitoring && !settings.ManualScansOnly() {
		return result, setupErr(CodePrivacyReviewRequired, "Confirm manual scans before preparing the first review.", RepairRetry, nil)
	}
	// A lost final response replays the exact receipt without reapplying earlier
	// permissions or restarting automation. Historical results do not scan again.
	if receipt, found, err := s.ScanOperationReceipt(request.WorkspaceID, request.ScanOperationID); err != nil {
		return result, err
	} else if found {
		if receipt.RootID != request.RootID || receipt.PrivacyMode != ContentModeMetadataOnly {
			return result, ErrScanOperationConflict
		}
		result.Status, result.Scan, result.ScanConfirmed = status, receipt, true
		result.MonitoringApplied = request.Monitoring && RequireHealthyAutomation(status) == nil
		if result.MonitoringApplied && automation != nil {
			result.WatcherID, _, err = automation.WatcherRecord(request.WorkspaceID)
		}
		return result, err
	}
	preserveExisting := request.WasApproved && !request.WasPaused && RequireHealthyAutomation(status) == nil
	rollback := func() {
		if !request.Monitoring || preserveExisting {
			return
		}
		if err := s.PauseReviewedAutomation(request.WorkspaceID, request.RootID, ContentModeMetadataOnly,
			request.DailyScanLocalTime, request.Timezone, request.Operation); err == nil {
			if automation != nil {
				_ = automation.EnsureWatcher(request.WorkspaceID)
			}
			result.MonitoringRetryBound = true
			result.MonitoringApplied = false
		}
	}
	if request.Monitoring {
		if automation == nil {
			return result, fmt.Errorf("monitoring is unavailable")
		}
		if !preserveExisting {
			if _, err := s.RecordReviewedAutomationApprovalPaused(request.WorkspaceID, request.RootID, ContentModeMetadataOnly,
				request.DailyScanLocalTime, request.Timezone, request.WasApproved, request.WasPaused, request.Operation); err != nil {
				return result, err
			}
			if _, err := s.ActivateReviewedAutomation(request.WorkspaceID, request.RootID, ContentModeMetadataOnly,
				request.DailyScanLocalTime, request.Timezone, request.Operation); err != nil {
				rollback()
				return result, err
			}
		}
		if err := automation.EnsureWatcher(request.WorkspaceID); err != nil {
			rollback()
			return result, err
		}
		status, err = s.Status(request.WorkspaceID)
		if err != nil {
			rollback()
			return result, err
		}
		if err := requirePreparation(status.Settings, request.Operation.PreparationID); err != nil {
			return result, err
		}
		if status.Settings.RootID != request.RootID || status.Settings.ContentMode != ContentModeMetadataOnly ||
			status.Settings.DailyScanLocalTime != request.DailyScanLocalTime || status.Settings.Timezone != request.Timezone ||
			status.Settings.AutomationApprovedAt.IsZero() {
			rollback()
			return result, setupErr(CodeFolderChanged, "File Janitor settings changed while preparing review.", RepairRetry, nil)
		}
		if err := RequireHealthyAutomation(status); err != nil {
			rollback()
			return result, err
		}
		var enabled bool
		result.WatcherID, enabled, err = automation.WatcherRecord(request.WorkspaceID)
		if err != nil || !enabled || result.WatcherID == "" {
			rollback()
			return result, fmt.Errorf("folder watching is not registered")
		}
		result.MonitoringApplied = true
	}
	result.Status = status
	if request.BeforeScan != nil {
		if err := request.BeforeScan(); err != nil {
			rollback()
			return result, err
		}
	}
	result.ScanAttempted = true
	result.Scan, err = s.ScanNowOperation(request.WorkspaceID, ScanSourceAssistantSetup, ScanOperationRequest{
		OperationID: request.ScanOperationID, RootID: request.RootID, PrivacyMode: ContentModeMetadataOnly,
		PreparationID:           request.Operation.PreparationID,
		FreshPreparationAttempt: request.FreshScanAttempt != nil && request.FreshScanAttempt(),
	})
	if err != nil {
		rollback()
		return result, err
	}
	result.ScanConfirmed = true
	if request.Monitoring && automation != nil {
		automation.MarkCaughtUp(request.WorkspaceID, workspace.LocalDateKey(status.Settings.Timezone, result.Scan.CompletedAt))
	}
	return result, nil
}

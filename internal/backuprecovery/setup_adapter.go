package backuprecovery

import (
	"context"

	"github.com/johnjallday/ori-agent/internal/setupwizard"
	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacecapability"
)

// SetupConnectionState is a bounded stored-only projection. It is not supplied
// by a browser and cannot carry paths, credentials or provider diagnostics.
type SetupConnectionState uint8

const (
	SetupUnavailable SetupConnectionState = iota
	SetupNotConfigured
	SetupReviewRequired
	SetupConnected
)

// SetupReader must check live ownership/install/receipt visibility against stored
// facts. It must never probe a destination or decrypt a backup password. The
// service implementation is wired only after the provider/admission gates pass.
type SetupReader interface {
	ReadBackupSetup(context.Context, string) (SetupConnectionState, error)
}

// SetupAdapter offers only fixed, stored-only readiness and summary steps.
type SetupAdapter struct{ reader SetupReader }

// NewSetupAdapter with a nil reader reports unavailable, never ready.
func NewSetupAdapter(reader SetupReader) *SetupAdapter { return &SetupAdapter{reader: reader} }
func (*SetupAdapter) ID() string                       { return workspacecapability.BackupRecoverySetupAdapterID }

func (a *SetupAdapter) Evaluate(ctx context.Context, req setupwizard.StepRequest) (setupwizard.StepReadiness, error) {
	if err := ctx.Err(); err != nil {
		return setupwizard.StepReadiness{}, err
	}
	if !backupSetupRequest(req) {
		return setupwizard.StepReadiness{Blocked: true, Summary: "This backup setup step is unsupported.", ErrorCategory: setupwizard.ErrorCategoryUnsupported}, nil
	}
	unavailable := setupwizard.StepReadiness{Blocked: true, Summary: "Backup & Recovery is unavailable in this runtime.", ErrorCategory: setupwizard.ErrorCategoryUnavailable}
	if a == nil || a.reader == nil {
		return unavailable, nil
	}
	state, err := a.reader.ReadBackupSetup(ctx, req.WorkspaceID)
	if err != nil {
		return unavailable, nil
	}
	switch state {
	case SetupNotConfigured:
		return setupwizard.StepReadiness{Summary: "Open the Backup & Recovery console to review external setup and connect metadata.", ErrorCategory: setupwizard.ErrorCategoryNotConfigured}, nil
	case SetupReviewRequired:
		return setupwizard.StepReadiness{Blocked: true, Summary: "Review local metadata access in the Backup & Recovery console. Monitoring requires separate approval.", ErrorCategory: setupwizard.ErrorCategoryPermissionRequired}, nil
	case SetupConnected:
		return setupwizard.StepReadiness{Ready: true, Summary: "A metadata connection has been observed. This does not prove backup success, complete coverage or recovery."}, nil
	default:
		return unavailable, nil
	}
}

// Confirm deliberately performs the same stored read as Evaluate. It cannot
// activate, refresh, schedule, select paths or accept a caller's readiness claim.
func (a *SetupAdapter) Confirm(ctx context.Context, req setupwizard.StepRequest, action setupwizard.StepAction) (setupwizard.StepReadiness, error) {
	if action.Type != setupwizard.ActionConfirm || action.Option != "" {
		return setupwizard.StepReadiness{}, setupwizard.ErrInvalidAction
	}
	return a.Evaluate(ctx, req)
}

func backupSetupRequest(req setupwizard.StepRequest) bool {
	if !localID(req.WorkspaceID) || req.Step.Adapter != workspacecapability.BackupRecoverySetupAdapterID || req.Step.RequirementKey != "" ||
		req.Directory != nil || req.Automation != nil || req.Intake != nil || req.Capability != nil || req.RuntimeRequirement != nil || req.Plugin != "" || req.PluginSource != "" || req.SelectedOption != "" {
		return false
	}
	return req.Step.ID == "backup-connection" && req.Step.Kind == workspace.SetupStepKindReadiness || req.Step.ID == "backup-summary" && req.Step.Kind == workspace.SetupStepKindSummary
}

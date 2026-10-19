package filejanitor

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// SetupPreparation is the one accepted, path-free preparation authority for a
// workspace. Stage completion is NOT stored here: folder/decision/scan receipts
// remain their canonical owners. Any later manual change drops this authority.
type SetupPreparation struct {
	ID                 string           `json:"id"`
	OwnerUserID        string           `json:"owner_user_id"`
	ReferenceID        string           `json:"reference_id"`
	ReviewRevision     string           `json:"review_revision"`
	BindingRevision    string           `json:"binding_revision"`
	CapabilityRevision string           `json:"capability_revision"`
	AccessRevision     string           `json:"access_revision,omitempty"`
	Monitoring         bool             `json:"monitoring"`
	Terms              SetupReviewTerms `json:"terms"`
	AcceptedAt         time.Time        `json:"accepted_at"`
	// Refusal-only retention fence. Canonical scan state remains the result;
	// losing that receipt must not make an old confirmation scan again.
	ScanReceiptDigest string `json:"scan_receipt_digest,omitempty"`
	// Dispatch intent is a refusal fence after interruption, not proof of a result.
	ScanDispatchedAt time.Time `json:"scan_dispatched_at,omitempty"`
}

func (p SetupPreparation) valid() bool {
	_, err := uuid.Parse(p.ID)
	return err == nil && strings.TrimSpace(p.OwnerUserID) != "" && len(p.OwnerUserID) <= 128 &&
		p.ReferenceID != "" && len(p.ReferenceID) <= 128 && len(p.ReviewRevision) == 64 &&
		len(p.BindingRevision) == 64 && len(p.CapabilityRevision) == 64 && !p.AcceptedAt.IsZero() &&
		p.Terms.PrivacyMode == ContentModeMetadataOnly && p.Terms.FilingRootName == DefaultFilingRootName &&
		p.Terms.ExcludesFiled && len(p.Terms.WatchEvents) <= 8 && len(p.Terms.Timezone) <= 128 &&
		(p.ScanReceiptDigest == "" || len(p.ScanReceiptDigest) == 64) &&
		(p.AccessRevision == "" || len(p.AccessRevision) == 64)
}

func requirePreparation(settings JanitorSettings, id string) error {
	if id == "" {
		return nil // Existing assistant-led operations keep their own authority.
	}
	if settings.SetupPreparation == nil || settings.SetupPreparation.ID != id || !settings.AccessRevokedAt.IsZero() {
		return setupErr(CodeFolderChanged, "Folder setup changed. Review the current settings before continuing.", RepairRetry, nil)
	}
	return nil
}

type LinkedPreparationRequest struct {
	Context        context.Context
	WorkspaceID    string
	OwnerUserID    string
	ReferenceID    string
	Monitoring     bool
	ReviewRevision string
}

type SetupPreparationResult struct {
	WorkspaceID string                `json:"workspace_id"`
	OperationID string                `json:"operation_id,omitempty"`
	Stage       string                `json:"stage"`
	Result      *ScanOperationReceipt `json:"result,omitempty"`
	Status      *Status               `json:"status,omitempty"`
}

// PrepareLinkedSetup accepts one exact owner-reviewed candidate, persists its
// authority before consequences, then uses the canonical grant and preparation
// seam. Repeated requests observe those same receipts. There is no timer or
// startup continuation and no dependency on an Assistant relationship.
func (s *Service) PrepareLinkedSetup(request LinkedPreparationRequest, automation PreparationAutomation) (result SetupPreparationResult, err error) {
	result = SetupPreparationResult{WorkspaceID: request.WorkspaceID, Stage: "review"}
	s.prepareMu.Lock()
	defer s.prepareMu.Unlock()
	if err := preparationContextError(request.Context); err != nil {
		return result, err
	}

	settings, err := s.store.LoadSettings(request.WorkspaceID)
	if err != nil {
		return result, err
	}
	preparation := settings.SetupPreparation
	if preparation == nil {
		review, err := s.ReviewLinkedSetup(request.WorkspaceID, request.OwnerUserID, request.ReferenceID, request.Monitoring)
		if err != nil {
			return result, err
		}
		if review.State != "review" || review.Revision != request.ReviewRevision || request.ReviewRevision == "" {
			return result, setupErr(CodeFolderChanged, "This folder review changed. Review the current setup before confirming.", RepairRetry, nil)
		}
		preparation = &SetupPreparation{
			ID: uuid.NewString(), OwnerUserID: request.OwnerUserID, ReferenceID: review.SelectedReference,
			ReviewRevision: review.Revision, BindingRevision: review.bindingRevision, CapabilityRevision: review.capabilityRevision, AccessRevision: review.accessRevision,
			Monitoring: review.Monitoring, Terms: review.Terms, AcceptedAt: s.clock(),
		}
		if _, err := s.store.UpdateSettings(request.WorkspaceID, func(current *JanitorSettings) error {
			digest, err := setupDigest(*current)
			if err != nil {
				return err
			}
			if digest != review.settingsDigest || current.SetupPreparation != nil {
				return setupErr(CodeFolderChanged, "Folder setup changed before confirmation. Review it again.", RepairRetry, nil)
			}
			current.SetupPreparation = preparation
			return nil
		}); err != nil {
			return result, err
		}
	}
	if preparation.OwnerUserID != request.OwnerUserID || preparation.ReviewRevision != request.ReviewRevision ||
		(request.ReferenceID != "" && preparation.ReferenceID != request.ReferenceID) || preparation.Monitoring != request.Monitoring {
		return result, setupErr(CodeFolderChanged, "Another folder setup was accepted. Open the current workspace settings.", RepairRetry, nil)
	}
	review, settings, err := s.validateLinkedPreparation(request.WorkspaceID, *preparation)
	if err != nil {
		return result, err
	}
	result.OperationID = preparation.ID
	result.Stage = "folder"
	if err := preparationContextError(request.Context); err != nil {
		return result, err
	}
	folderOperation := preparation.ID + ":folder"
	if settings.LastFolderGrant == nil || settings.LastFolderGrant.OperationID != folderOperation {
		if settings.IsSetUp() {
			return result, setupErr(CodeFolderChanged, "An existing folder configuration cannot be replaced by this review.", RepairRetry, nil)
		}
		paused := true
		_, err := s.ConfirmSetup(SetupRequest{
			WorkspaceID: request.WorkspaceID, Path: review.rootPath, Paused: &paused,
			DailyScanLocalTime: preparation.Terms.DailyScanLocalTime, Timezone: preparation.Terms.Timezone,
			ExpectedRootIdentity: review.rootIdentity, DirectoryReferenceID: preparation.ReferenceID,
			Operation: &FolderGrantOperation{RunID: preparation.ID, OperationID: folderOperation, PreparationID: preparation.ID},
		})
		if err != nil {
			return result, err
		}
	}
	_, settings, err = s.validateLinkedPreparation(request.WorkspaceID, *preparation)
	if err != nil {
		return result, err
	}
	result.Stage = "decision"
	if err := preparationContextError(request.Context); err != nil {
		return result, err
	}
	if !preparation.Monitoring && !settings.ManualScansOnly() {
		if _, err := s.recordManualSetupDecision(request.WorkspaceID, settings.RootID, settings.UpdatedAt, preparation.ID); err != nil {
			return result, err
		}
	}
	_, settings, err = s.validateLinkedPreparation(request.WorkspaceID, *preparation)
	if err != nil {
		return result, err
	}
	result.Stage = "prepare"
	if err := preparationContextError(request.Context); err != nil {
		return result, err
	}
	if _, _, err := s.preparedScanReceipt(request.WorkspaceID, *preparation); err != nil {
		return result, err
	}
	freshScanAttempt := false
	prepared, err := s.PrepareMetadataReview(PreparationRequest{
		WorkspaceID: request.WorkspaceID, RootID: settings.RootID, Monitoring: preparation.Monitoring,
		DailyScanLocalTime: preparation.Terms.DailyScanLocalTime, Timezone: preparation.Terms.Timezone,
		WasApproved: !settings.AutomationApprovedAt.IsZero(), WasPaused: settings.Paused,
		Operation: AssistedAutomationOperation{
			RunID: preparation.ID, OperationID: preparation.ID + ":monitoring", PreparationID: preparation.ID,
			ReviewRevision: preparation.ReviewRevision, AuthorityRevision: preparation.BindingRevision,
		},
		ScanOperationID:  preparation.ID + ":scan",
		FreshScanAttempt: func() bool { return freshScanAttempt },
		BeforeScan: func() error {
			if err := preparationContextError(request.Context); err != nil {
				return err
			}
			_, err := s.store.UpdateSettings(request.WorkspaceID, func(current *JanitorSettings) error {
				if err := s.validatePreparationBoundary(*current, preparation.ID); err != nil {
					return err
				}
				if !current.SetupPreparation.ScanDispatchedAt.IsZero() {
					return initialReceiptUnavailable()
				}
				current.SetupPreparation.ScanDispatchedAt = s.clock().UTC()
				return nil
			})
			freshScanAttempt = err == nil
			return err
		},
	}, automation)
	if err != nil {
		switch {
		case prepared.ScanConfirmed:
			result.Stage, result.Result, result.Status = "result", &prepared.Scan, &prepared.Status
		case prepared.ScanAttempted:
			result.Stage = "scan"
		case preparation.Monitoring:
			result.Stage = "monitoring"
		default:
			result.Stage = "readiness"
		}
		return result, err
	}
	result.Stage, result.Result, result.Status = "result", &prepared.Scan, &prepared.Status
	digest, err := setupDigest(prepared.Scan)
	if err != nil {
		return result, err
	}
	if preparation.ScanReceiptDigest == digest {
		return result, nil
	}
	acknowledged, err := s.store.UpdateSettings(request.WorkspaceID, func(current *JanitorSettings) error {
		if err := s.validatePreparationBoundary(*current, preparation.ID); err != nil {
			return err
		}
		if prior := current.SetupPreparation.ScanReceiptDigest; prior != "" && prior != digest {
			return initialReceiptUnavailable()
		}
		current.SetupPreparation.ScanReceiptDigest = digest
		return nil
	})
	if err != nil {
		return result, err
	}
	result.Status.Settings = acknowledged
	return result, nil
}

// PreparationFailureSummary exposes stage-specific repair without forwarding
// raw filesystem/provider causes or pretending the workspace must be recreated.
func PreparationFailureSummary(stage string) (code, message string) {
	switch stage {
	case "monitoring":
		return "monitoring_incomplete", "Folder access was saved, but watching or daily catch-up could not start. No initial scan ran. Retry this preparation or stop here and keep the saved setup."
	case "scan":
		return "initial_scan_incomplete", "Folder access and the chosen setup were saved, but the initial scan result could not be confirmed. Open the existing Review and choose Scan now explicitly if another attempt is needed. Do not create another workspace."
	case "result":
		return "result_observation_incomplete", "The first scan has a saved result, but Ori could not confirm every remaining detail. Reload this workspace to observe that result or open current settings. Do not create again."
	default:
		return "setup_incomplete", "Preparation stopped. Keep this workspace and retry its current step or open current settings; do not create again."
	}
}

func invalidateSetupPreparation(settings *JanitorSettings) {
	if p := settings.SetupPreparation; p != nil {
		if pending := settings.PendingFolderGrant; pending != nil && pending.RunID == p.ID && pending.OperationID == p.ID+":folder" {
			settings.PendingFolderGrant = nil
		}
	}
	settings.SetupPreparation = nil
}

// StopLinkedPreparation invalidates only this owner's exact accepted operation.
// It waits for an already-started stage, pauses monitoring, and releases its
// uncompleted grant claim. Saved access/resources/results are never deleted.
func (s *Service) StopLinkedPreparation(workspaceID, owner, operationID string) (Status, error) {
	s.prepareMu.Lock()
	defer s.prepareMu.Unlock()
	s.claimMu.Lock()
	defer s.claimMu.Unlock()
	ws, err := s.readWorkspace(workspaceID)
	if err != nil || ws == nil || !linkedSetupOwned(ws, owner) {
		return Status{}, setupErr(CodeWorkspaceMissing, "This workspace is unavailable.", "", nil)
	}
	primary, err := s.workspaces.Get(workspaceID)
	if err != nil || primary == nil || !linkedSetupOwned(primary, owner) {
		return Status{}, setupErr(CodeWorkspaceMissing, "This workspace is unavailable.", "", nil)
	}
	if _, err := s.store.UpdateSettings(workspaceID, func(settings *JanitorSettings) error {
		p := settings.SetupPreparation
		if p == nil || p.ID != operationID || p.OwnerUserID != owner {
			return setupErr(CodeFolderChanged, "Preparation changed. Open the workspace's current settings.", RepairRetry, nil)
		}
		settings.Paused = true
		invalidateSetupPreparation(settings)
		settings.LastAssistedAutomation = nil
		return nil
	}); err != nil {
		return Status{}, err
	}
	return s.Status(workspaceID)
}

func preparationContextError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

// validatePreparationWorkspace runs inside the canonical workspace update when
// granting access, so an ownership/removal change cannot be missed between the
// review and the resource write. Resource associations themselves are excluded
// from the immutable install identity.
func validatePreparationWorkspace(ws *workspace.Workspace, preparation *SetupPreparation, root string) error {
	if preparation == nil {
		return nil
	}
	if ws == nil || !linkedSetupOwned(ws, preparation.OwnerUserID) ||
		(ws.Status != "" && ws.Status != workspace.StatusActive) || strings.EqualFold(ws.Kind, "group") {
		return setupErr(CodeWorkspaceMissing, "This workspace is no longer available for the accepted setup.", "", nil)
	}
	install, found := ws.GetInstalledCapability(workspace.CapabilityFileJanitor)
	install.OwnedResources = nil
	digest, err := setupDigest(install)
	if err != nil || !found || digest != preparation.CapabilityRevision {
		return setupErr(CodeFolderChanged, "File Janitor was removed or changed. Review its current setup.", RepairRetry, nil)
	}
	if !preparationAccessMatches(ws, preparation, root) {
		return setupErr(CodeFolderChanged, "Workspace folder permissions changed. Stop this preparation and review current access.", RepairRetry, nil)
	}
	return nil
}

func (s *Service) validatePreparationBoundary(settings JanitorSettings, id string) error {
	if err := requirePreparation(settings, id); err != nil {
		return err
	}
	if id == "" {
		return nil
	}
	_, _, err := s.validateLinkedPreparation(settings.WorkspaceID, *settings.SetupPreparation)
	return err
}

func (s *Service) validateLinkedPreparation(workspaceID string, preparation SetupPreparation) (LinkedSetupReview, JanitorSettings, error) {
	review, err := s.reviewLinkedSetup(workspaceID, preparation.OwnerUserID, preparation.ReferenceID, preparation.Monitoring, true)
	if err != nil {
		return review, JanitorSettings{}, err
	}
	settings, err := s.store.LoadSettings(workspaceID)
	if err != nil {
		return review, settings, err
	}
	if err := requirePreparation(settings, preparation.ID); err != nil {
		return review, settings, err
	}
	for _, get := range []func(string) (*workspace.Workspace, error){s.readWorkspace, s.workspaces.Get} {
		ws, err := get(workspaceID)
		if err != nil || ws == nil || !preparationAccessMatches(ws, &preparation, review.rootPath) {
			return review, settings, setupErr(CodeFolderChanged, "Workspace folder permissions changed. Stop this preparation and review current access.", RepairRetry, nil)
		}
		if !settings.IsSetUp() && hasJanitorAccessResources(ws) {
			pending := settings.PendingFolderGrant
			if pending == nil || pending.RunID != preparation.ID || pending.OperationID != preparation.ID+":folder" {
				return review, settings, setupErr(CodeFolderChanged, "The pending folder grant cannot be confirmed. Stop it and review current access.", RepairRetry, nil)
			}
		}
	}
	if settings.IsSetUp() {
		receipt := settings.LastFolderGrant
		if settings.RootPath != review.rootPath || receipt == nil || receipt.RunID != preparation.ID || receipt.OperationID != preparation.ID+":folder" || receipt.RootID != settings.RootID || receipt.DirectoryReference != preparation.ReferenceID {
			return review, settings, setupErr(CodeFolderChanged, "The saved folder grant changed. Open current settings before continuing.", RepairRetry, nil)
		}
	}
	if review.State != "review" || review.bindingRevision != preparation.BindingRevision || settings.ContentMode != ContentModeMetadataOnly {
		return review, settings, setupErr(CodeFolderChanged, "The folder, access, or monitoring terms changed. Review the current setup.", RepairRetry, nil)
	}
	return review, settings, nil
}

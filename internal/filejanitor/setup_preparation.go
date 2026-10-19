package filejanitor

import (
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
	Monitoring         bool             `json:"monitoring"`
	Terms              SetupReviewTerms `json:"terms"`
	AcceptedAt         time.Time        `json:"accepted_at"`
}

func (p SetupPreparation) valid() bool {
	_, err := uuid.Parse(p.ID)
	return err == nil && strings.TrimSpace(p.OwnerUserID) != "" && len(p.OwnerUserID) <= 128 &&
		p.ReferenceID != "" && len(p.ReferenceID) <= 128 && len(p.ReviewRevision) == 64 &&
		len(p.BindingRevision) == 64 && len(p.CapabilityRevision) == 64 && !p.AcceptedAt.IsZero() &&
		p.Terms.PrivacyMode == ContentModeMetadataOnly && p.Terms.FilingRootName == DefaultFilingRootName &&
		p.Terms.ExcludesFiled && len(p.Terms.WatchEvents) <= 8 && len(p.Terms.Timezone) <= 128
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
			ReviewRevision: review.Revision, BindingRevision: review.bindingRevision, CapabilityRevision: review.capabilityRevision,
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
	prepared, err := s.PrepareMetadataReview(PreparationRequest{
		WorkspaceID: request.WorkspaceID, RootID: settings.RootID, Monitoring: preparation.Monitoring,
		DailyScanLocalTime: preparation.Terms.DailyScanLocalTime, Timezone: preparation.Terms.Timezone,
		WasApproved: !settings.AutomationApprovedAt.IsZero(), WasPaused: settings.Paused,
		Operation: AssistedAutomationOperation{
			RunID: preparation.ID, OperationID: preparation.ID + ":monitoring", PreparationID: preparation.ID,
			ReviewRevision: preparation.ReviewRevision, AuthorityRevision: preparation.BindingRevision,
		},
		ScanOperationID: preparation.ID + ":scan",
	}, automation)
	if err != nil {
		return result, err
	}
	result.Stage, result.Result, result.Status = "result", &prepared.Scan, &prepared.Status
	return result, nil
}

// validatePreparationWorkspace runs inside the canonical workspace update when
// granting access, so an ownership/removal change cannot be missed between the
// review and the resource write. Resource associations themselves are excluded
// from the immutable install identity.
func validatePreparationWorkspace(ws *workspace.Workspace, preparation *SetupPreparation) error {
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
	if review.State != "review" || review.bindingRevision != preparation.BindingRevision || settings.ContentMode != ContentModeMetadataOnly {
		return review, settings, setupErr(CodeFolderChanged, "The folder, access, or monitoring terms changed. Review the current setup.", RepairRetry, nil)
	}
	return review, settings, nil
}

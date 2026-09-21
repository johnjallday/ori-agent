package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/filejanitor"

	"github.com/johnjallday/ori-agent/internal/assistantsetup"
	"github.com/johnjallday/ori-agent/internal/progression"
	"github.com/johnjallday/ori-agent/internal/sessionhttp"
	"github.com/johnjallday/ori-agent/internal/setupwizard"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type assistantSetupSessionAdapter struct {
	sessions   *sessionhttp.Handler
	folders    *workspace.FileStore
	janitor    *filejanitor.Service
	automation *filejanitor.Automation
	wizard     interface {
		Status(ctx context.Context, workspaceID string) (setupwizard.Status, error)
	}
	coordinator assistantsetup.Store
	profiles    userprofile.UserStore
}

func (a assistantSetupSessionAdapter) ReviewFileJanitorPlan(ctx context.Context, _ string) (assistantsetup.TeamPlan, error) {
	if a.sessions == nil {
		return assistantsetup.TeamPlan{}, assistantsetup.ErrUnavailable
	}
	plan, err := a.sessions.ReviewFileJanitorCreation(ctx)
	if err != nil {
		return assistantsetup.TeamPlan{}, mapAssistantSetupSessionError(err)
	}
	roles := make([]assistantsetup.TeamRole, 0, len(plan.Roles))
	for _, role := range plan.Roles {
		roles = append(roles, assistantsetup.TeamRole{
			RoleID: role.RoleID, Name: role.Name, Action: role.Action,
			Provider: role.Provider, Model: role.Model, ModelConfigured: role.ModelConfigured,
			ConfigDigest: role.ConfigDigest, Warning: role.Warning,
		})
	}
	return assistantsetup.TeamPlan{
		BlueprintID: plan.BlueprintID, BlueprintVersion: plan.BlueprintVersion,
		BlueprintDigest: plan.BlueprintDigest, PlanRevision: plan.PlanRevision, Roles: roles,
	}, nil
}

func (a assistantSetupSessionAdapter) PrepareFileJanitor(ctx context.Context, request assistantsetup.PrepareRequest) (assistantsetup.WorkspaceResult, error) {
	if a.sessions == nil || a.folders == nil {
		return assistantsetup.WorkspaceResult{}, assistantsetup.ErrUnavailable
	}
	if request.Mode == assistantsetup.TargetAdopt {
		return a.observeAdoptedFileJanitor(request)
	}
	roles := make([]sessionhttp.ReviewedTemplateRole, 0, len(request.Plan.Roles))
	for _, role := range request.Plan.Roles {
		roles = append(roles, sessionhttp.ReviewedTemplateRole{
			RoleID: role.RoleID, Name: role.Name, Action: role.Action,
			Provider: role.Provider, Model: role.Model, ModelConfigured: role.ModelConfigured,
			ConfigDigest: role.ConfigDigest, Warning: role.Warning,
		})
	}
	plan := sessionhttp.ReviewedTemplateCreationPlan{
		BlueprintID: request.Plan.BlueprintID, BlueprintVersion: request.Plan.BlueprintVersion,
		BlueprintDigest: request.Plan.BlueprintDigest, PlanRevision: request.Plan.PlanRevision, Roles: roles,
	}
	configDigest := ""
	if len(roles) == 1 {
		configDigest = roles[0].ConfigDigest
	}
	result, err := a.sessions.CreateReviewedFileJanitor(ctx, sessionhttp.ReviewedTemplateCreationRequest{
		Name: assistantsetup.WorkspaceName, Plan: plan,
		Descriptor: sessionhttp.AssistantSetupCreationDescriptor{
			OwnerUserID: request.OwnerUserID, RunID: request.RunID, OperationID: request.OperationID,
			ReviewDigest: request.ReviewDigest, WorkspaceID: request.WorkspaceID,
			ProfileProvenanceID: request.ProfileProvenanceID, ConfigDigest: configDigest,
		},
	})
	if err != nil {
		return assistantsetup.WorkspaceResult{}, mapAssistantSetupSessionError(err)
	}
	return assistantsetup.WorkspaceResult{
		WorkspaceID: result.WorkspaceID, AgentInstanceID: result.AgentInstanceID,
		ProfileProvenanceID: result.ProfileProvenanceID, ProfileStoreOrigin: result.ProfileStoreOrigin,
		ProfileCreated: result.ProfileCreated, ConfigurationDigest: result.ConfigurationDigest,
	}, nil
}

func (a assistantSetupSessionAdapter) observeAdoptedFileJanitor(request assistantsetup.PrepareRequest) (assistantsetup.WorkspaceResult, error) {
	if request.ExpectedTarget == nil || !request.ExpectedTarget.Supported ||
		request.ExpectedTarget.WorkspaceID != request.WorkspaceID {
		return assistantsetup.WorkspaceResult{}, assistantsetup.ErrUnsupportedTarget
	}
	candidate, err := a.folders.Get(request.WorkspaceID)
	if err != nil || candidate == nil || !candidate.HasInstalledCapability(workspace.CapabilityFileJanitor) {
		return assistantsetup.WorkspaceResult{}, assistantsetup.ErrNoLongerAvailable
	}
	entryID := ""
	for _, instance := range candidate.AgentInstances {
		if instance.EntryPoint {
			entryID = strings.TrimSpace(instance.ID)
			break
		}
	}
	if entryID == "" {
		return assistantsetup.WorkspaceResult{}, assistantsetup.ErrUnsupportedTarget
	}
	return assistantsetup.WorkspaceResult{
		WorkspaceID: candidate.ID, AgentInstanceID: entryID,
		ProfileCreated: false, ConfigurationDigest: request.ReviewDigest,
	}, nil
}

func (a assistantSetupSessionAdapter) Facts(ctx context.Context, ownerUserID, runID, workspaceID string) (assistantsetup.FileJanitorFacts, error) {
	if a.janitor == nil {
		return assistantsetup.FileJanitorFacts{}, assistantsetup.ErrUnavailable
	}
	status, err := a.janitor.Status(workspaceID)
	if err != nil {
		return assistantsetup.FileJanitorFacts{}, assistantsetup.ErrUnavailable
	}
	folderReady := true
	for _, component := range []filejanitor.ReadinessComponent{
		filejanitor.ComponentDirectoryAccess, filejanitor.ComponentDestination,
		filejanitor.ComponentMCPBinding, filejanitor.ComponentPersistence,
	} {
		matched := false
		for _, check := range status.Readiness.Checks {
			if check.Component == component {
				matched = check.Status == filejanitor.ComponentOK
				break
			}
		}
		folderReady = folderReady && matched
	}
	facts := assistantsetup.FileJanitorFacts{
		FolderReady: folderReady, Readiness: string(status.Readiness.State), Paused: status.Settings.Paused,
		MonitoringApproved: !status.Settings.AutomationApprovedAt.IsZero(),
		MonitoringActive:   !status.Settings.Paused && status.Readiness.State == filejanitor.ReadinessReady,
		PrivacyMode:        string(status.Settings.ContentMode), RootGenerationID: status.Settings.RootID,
		DirectoryReference: status.Settings.DirectoryReferenceID,
	}
	if a.coordinator != nil {
		operations, listErr := a.coordinator.ListOperations(ctx, ownerUserID, runID)
		if listErr != nil {
			return assistantsetup.FileJanitorFacts{}, assistantsetup.ErrUnavailable
		}
		for _, operation := range operations {
			if operation.Kind != assistantsetup.OperationInitialScan {
				continue
			}
			receipt, found, receiptErr := a.janitor.ScanOperationReceipt(workspaceID, operation.ID)
			if receiptErr != nil {
				return assistantsetup.FileJanitorFacts{}, assistantsetup.ErrUnavailable
			}
			if found && receipt.RootID == status.Settings.RootID && receipt.PrivacyMode == filejanitor.ContentModeMetadataOnly {
				facts.FirstOutcome = receipt.Outcome
				facts.FirstBatchID = receipt.BatchID
				facts.FirstEligible = receipt.EligibleCount
				facts.FirstIneligible = receipt.IneligibleCount
				facts.FirstCompletedAt = receipt.CompletedAt
			}
			break
		}
	}
	return facts, nil
}

func (a assistantSetupSessionAdapter) ReviewMonitoring(ctx context.Context, ownerUserID, workspaceID string) (assistantsetup.MonitoringFacts, error) {
	if a.janitor == nil {
		return assistantsetup.MonitoringFacts{}, assistantsetup.ErrUnavailable
	}
	review, err := a.janitor.ReviewAutomation(workspaceID)
	if err != nil {
		return assistantsetup.MonitoringFacts{}, mapAssistantSetupJanitorError(err)
	}
	if !review.TimezoneConfigured && a.profiles != nil {
		if profile, profileErr := a.profiles.Get(ctx, ownerUserID); profileErr == nil && profile != nil {
			if timezone := strings.TrimSpace(profile.Timezone); timezone != "" {
				if _, timezoneErr := time.LoadLocation(timezone); timezoneErr == nil {
					review.Timezone = timezone
				}
			}
		}
	}
	status, statusErr := a.janitor.Status(workspaceID)
	if statusErr != nil {
		return assistantsetup.MonitoringFacts{}, assistantsetup.ErrUnavailable
	}
	monitoringActive := !status.Settings.Paused && filejanitor.RequireHealthyAutomation(status) == nil
	authorityParts := []string{
		review.RootID, string(review.ContentMode), review.DailyScanLocalTime, review.Timezone,
		strings.Join(review.WatchEvents, ","), fmt.Sprintf("%d", review.WatchDebounceSecond),
		fmt.Sprintf("%d", review.SettlingSecond),
	}
	authorityHash := sha256.Sum256([]byte(strings.Join(authorityParts, "\x00")))
	settingsHash := sha256.Sum256([]byte(strings.Join(append(authorityParts,
		fmt.Sprintf("%t", review.Paused), fmt.Sprintf("%t", review.AutomationApproved), fmt.Sprintf("%t", monitoringActive)), "\x00")))
	facts := assistantsetup.MonitoringFacts{
		RootGenerationID: review.RootID, SettingsRevision: hex.EncodeToString(settingsHash[:]),
		AuthorityRevision: hex.EncodeToString(authorityHash[:]),
		PrivacyMode:       string(review.ContentMode), WatchEvents: append([]string(nil), review.WatchEvents...),
		DebounceSeconds: review.WatchDebounceSecond, SettlingSeconds: review.SettlingSecond,
		DailyScanLocalTime: review.DailyScanLocalTime, Timezone: review.Timezone, ExcludesFiled: true,
		WasPaused: review.Paused, WasApproved: review.AutomationApproved, WasMonitoringActive: monitoringActive,
	}
	if receipt := review.LastAssistedAutomation; receipt != nil {
		facts.RetryRunID = receipt.RunID
		facts.RetryOperationID = receipt.OperationID
		facts.RetryReviewRevision = receipt.ReviewRevision
		facts.RetryAuthorityRevision = receipt.AuthorityRevision
		facts.RetryState = string(receipt.State)
	}
	return facts, nil
}

func (a assistantSetupSessionAdapter) PrepareFirstReview(ctx context.Context, request assistantsetup.PrepareReviewRequest) (assistantsetup.PrepareReviewResult, error) {
	var result assistantsetup.PrepareReviewResult
	if a.janitor == nil || a.automation == nil {
		return result, assistantsetup.ErrUnavailable
	}
	fresh, err := a.ReviewMonitoring(ctx, request.OwnerUserID, request.WorkspaceID)
	if err != nil {
		return result, err
	}
	if fresh.RootGenerationID != request.Review.RootGenerationID || fresh.SettingsRevision != request.Review.SettingsRevision {
		return result, assistantsetup.ErrFolderChanged
	}
	if fresh.PrivacyMode != string(filejanitor.ContentModeMetadataOnly) || fresh.PrivacyMode != request.Review.PrivacyMode {
		return result, assistantsetup.ErrPrivacyReview
	}
	privacy := filejanitor.ContentMode(request.Review.PrivacyMode)
	preserveExistingMonitoring := request.Review.WasApproved && request.Review.WasMonitoringActive
	operation := filejanitor.AssistedAutomationOperation{
		RunID: request.RunID, OperationID: request.MonitoringOperation,
		ReviewRevision: request.Review.Revision, AuthorityRevision: request.Review.AuthorityRevision,
	}
	rollback := func() bool {
		if preserveExistingMonitoring {
			return false
		}
		if err := a.janitor.PauseReviewedAutomation(
			request.WorkspaceID, request.Review.RootGenerationID, privacy,
			request.Review.DailyScanLocalTime, request.Review.Timezone, operation,
		); err == nil {
			_ = a.automation.EnsureWatcher(request.WorkspaceID)
			return true
		}
		return false
	}
	if !preserveExistingMonitoring {
		if _, err := a.janitor.RecordReviewedAutomationApprovalPaused(
			request.WorkspaceID, request.Review.RootGenerationID, privacy,
			request.Review.DailyScanLocalTime, request.Review.Timezone,
			request.Review.WasApproved, request.Review.WasPaused, operation,
		); err != nil {
			return result, mapAssistantSetupJanitorError(err)
		}
		if _, err := a.janitor.ActivateReviewedAutomation(
			request.WorkspaceID, request.Review.RootGenerationID, privacy,
			request.Review.DailyScanLocalTime, request.Review.Timezone, operation,
		); err != nil {
			result.MonitoringRetryBound = rollback()
			return result, mapAssistantSetupJanitorError(err)
		}
	}
	if err := a.automation.EnsureWatcher(request.WorkspaceID); err != nil {
		result.MonitoringRetryBound = rollback()
		return result, err
	}
	status, err := a.janitor.Status(request.WorkspaceID)
	if err != nil || status.Settings.RootID != request.Review.RootGenerationID ||
		status.Settings.ContentMode != privacy || status.Settings.DailyScanLocalTime != request.Review.DailyScanLocalTime ||
		status.Settings.Timezone != request.Review.Timezone || status.Settings.AutomationApprovedAt.IsZero() ||
		filejanitor.RequireHealthyAutomation(status) != nil {
		result.MonitoringRetryBound = rollback()
		if err == nil && (status.Settings.RootID != request.Review.RootGenerationID || status.Settings.ContentMode != privacy ||
			status.Settings.DailyScanLocalTime != request.Review.DailyScanLocalTime || status.Settings.Timezone != request.Review.Timezone) {
			return result, assistantsetup.ErrFolderChanged
		}
		return result, assistantsetup.ErrUnavailable
	}
	watcherID, enabled, err := a.automation.WatcherRecord(request.WorkspaceID)
	if err != nil || !enabled || strings.TrimSpace(watcherID) == "" {
		result.MonitoringRetryBound = rollback()
		return result, assistantsetup.ErrUnavailable
	}
	result.MonitoringApplied = true
	result.WatcherID = watcherID
	result.Facts = assistantsetup.FileJanitorFacts{
		Readiness: string(status.Readiness.State), Paused: status.Settings.Paused,
		MonitoringApproved: !status.Settings.AutomationApprovedAt.IsZero(), MonitoringActive: true,
		PrivacyMode: string(status.Settings.ContentMode), RootGenerationID: status.Settings.RootID,
		DirectoryReference: status.Settings.DirectoryReferenceID,
	}
	if a.wizard != nil {
		if _, err := a.wizard.Status(ctx, request.WorkspaceID); err != nil {
			result.MonitoringRetryBound = rollback()
			result.MonitoringApplied = false
			return result, err
		}
	}
	result.ScanAttempted = true
	receipt, err := a.janitor.ScanNowOperation(request.WorkspaceID, filejanitor.ScanSourceAssistantSetup, filejanitor.ScanOperationRequest{
		OperationID: request.ScanOperation, RootID: request.Review.RootGenerationID,
		PrivacyMode: filejanitor.ContentModeMetadataOnly,
	})
	if err != nil {
		result.MonitoringRetryBound = rollback()
		result.MonitoringApplied = false
		return result, mapAssistantSetupJanitorError(err)
	}
	a.automation.MarkCaughtUp(request.WorkspaceID, workspace.LocalDateKey(status.Settings.Timezone, receipt.CompletedAt))
	result.ScanOutcome = receipt.Outcome
	result.BatchID = receipt.BatchID
	result.EligibleCount = receipt.EligibleCount
	result.IneligibleCount = receipt.IneligibleCount
	result.CompletedAt = receipt.CompletedAt
	result.Facts.FirstOutcome = receipt.Outcome
	result.Facts.FirstBatchID = receipt.BatchID
	result.Facts.FirstEligible = receipt.EligibleCount
	result.Facts.FirstIneligible = receipt.IneligibleCount
	result.Facts.FirstCompletedAt = receipt.CompletedAt
	return result, nil
}

func mapAssistantSetupJanitorError(err error) error {
	if err == nil {
		return nil
	}
	var setupError *filejanitor.SetupError
	if errors.As(err, &setupError) {
		switch setupError.Code {
		case filejanitor.CodePrivacyReviewRequired:
			return assistantsetup.ErrPrivacyReview
		case filejanitor.CodeFolderChanged, filejanitor.CodeFolderConflict:
			return assistantsetup.ErrFolderChanged
		}
	}
	if errors.Is(err, filejanitor.ErrScanOperationConflict) {
		return assistantsetup.ErrFolderChanged
	}
	return err
}

func mapAssistantSetupSessionError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, store.ErrAgentRootUnavailable) {
		return assistantsetup.ErrAgentRootUnavailable
	}
	if errors.Is(err, assistantsetup.ErrNoLongerAvailable) || errors.Is(err, assistantsetup.ErrUnsupportedTarget) {
		return err
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "root") && strings.Contains(message, "unavailable"):
		return assistantsetup.ErrAgentRootUnavailable
	case strings.Contains(message, "plan changed"), strings.Contains(message, "team plan"):
		return assistantsetup.ErrTeamConflict
	default:
		return err
	}
}

type progressionFileJanitorRecommendations struct{ engine *progression.Engine }

func (d progressionFileJanitorRecommendations) FileJanitorRecommendationDeferred(context.Context, string) (bool, error) {
	if d.engine == nil {
		return false, assistantsetup.ErrUnavailable
	}
	for _, mission := range d.engine.Status().Missions {
		if mission.ID == progression.TidyDownloadsQuestID {
			return mission.Status == progression.StatusSkipped, nil
		}
	}
	return false, assistantsetup.ErrUnavailable
}

func (d progressionFileJanitorRecommendations) DeferFileJanitorRecommendation(context.Context, string) error {
	if d.engine == nil {
		return assistantsetup.ErrUnavailable
	}
	return d.engine.Skip(progression.TidyDownloadsQuestID)
}

func (b *ServerBuilder) wireAssistantSetup() {
	if b == nil || b.sessionStore == nil || b.personalAssistantService == nil ||
		b.personalAssistantHandler == nil || b.sessionHandler == nil || b.workspaceFileStore == nil {
		return
	}
	store := assistantsetup.NewSQLiteStore(b.sessionStore.DB())
	resolver := assistantsetup.NewCanonicalCandidateResolver(b.workspaceFileStore)
	adapter := assistantSetupSessionAdapter{
		sessions: b.sessionHandler, folders: b.workspaceFileStore,
		janitor: b.fileJanitorService, automation: b.fileJanitorAutomation,
		wizard: b.setupWizardService, coordinator: store, profiles: b.userStore,
	}
	service := assistantsetup.NewService(store, b.personalAssistantService, resolver, adapter, adapter)
	service.SetAdmissionGate(b.resetWork)
	service.SetProgressor(adapter)
	if b.progressionEngine != nil {
		service.SetRecommendationService(progressionFileJanitorRecommendations{engine: b.progressionEngine})
	}
	b.assistantSetupStore = store
	b.assistantSetupService = service
	b.assistantSetupRetries = assistantsetup.NewRetryRunner(service, 5*time.Second)
	b.personalAssistantHandler.SetAssistantSetupService(service)
	if b.fileJanitorHandler != nil {
		b.fileJanitorHandler.SetAssistantSetupCoordinator(service)
		b.fileJanitorHandler.SetPathSelectionResolver(b.pathSelectionStore)
	}
}

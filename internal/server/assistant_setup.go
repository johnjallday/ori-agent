package server

import (
	"context"
	"errors"
	"strings"

	"github.com/johnjallday/ori-agent/internal/assistantsetup"
	"github.com/johnjallday/ori-agent/internal/progression"
	"github.com/johnjallday/ori-agent/internal/sessionhttp"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type assistantSetupSessionAdapter struct {
	sessions *sessionhttp.Handler
	folders  *workspace.FileStore
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
		Name: "File Janitor", Plan: plan,
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
	adapter := assistantSetupSessionAdapter{sessions: b.sessionHandler, folders: b.workspaceFileStore}
	service := assistantsetup.NewService(store, b.personalAssistantService, resolver, adapter, adapter)
	service.SetAdmissionGate(b.resetWork)
	if b.progressionEngine != nil {
		service.SetRecommendationService(progressionFileJanitorRecommendations{engine: b.progressionEngine})
	}
	b.assistantSetupStore = store
	b.assistantSetupService = service
	b.personalAssistantHandler.SetAssistantSetupService(service)
}

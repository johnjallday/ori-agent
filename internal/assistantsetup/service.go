package assistantsetup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/resetstate"
)

const (
	metadataDisclosure   = "File Janitor classifies files from names, types, sizes, and dates only. It does not read file contents or send them to a model."
	folderDisclosure     = "You will choose one folder next. Granting it allows Ori to list immediate-child metadata and create or reuse Filed inside that folder; it does not start monitoring."
	monitoringDisclosure = "Monitoring is a separate decision. If approved, Ori groups create and rename activity for five minutes, proposes only settled files, and runs a daily catch-up at 09:00 local time."
	fileReviewDisclosure = "Setup only prepares proposals. Every move or send-to-Trash still requires your separate approval in File Janitor review."
)

type RelationshipReader interface {
	Get(ctx context.Context, userID string) (*personalassistant.Projection, error)
}

type PlanSource interface {
	ReviewFileJanitorPlan(ctx context.Context, ownerUserID string) (TeamPlan, error)
}

type PrepareRequest struct {
	OwnerUserID         string
	RunID               string
	OperationID         string
	ReviewDigest        string
	WorkspaceID         string
	ProfileProvenanceID string
	Mode                TargetMode
	Plan                TeamPlan
	ExpectedTarget      *Target
}

type WorkspacePreparer interface {
	PrepareFileJanitor(ctx context.Context, request PrepareRequest) (WorkspaceResult, error)
}

type FileJanitorProgressor interface {
	Facts(ctx context.Context, ownerUserID, runID, workspaceID string) (FileJanitorFacts, error)
	ReviewMonitoring(ctx context.Context, ownerUserID, workspaceID string) (MonitoringFacts, error)
	PrepareFirstReview(ctx context.Context, request PrepareReviewRequest) (PrepareReviewResult, error)
}

type RecommendationService interface {
	FileJanitorRecommendationDeferred(ctx context.Context, ownerUserID string) (bool, error)
	DeferFileJanitorRecommendation(ctx context.Context, ownerUserID string) error
}

type Service struct {
	store           Store
	relationships   RelationshipReader
	resolver        CandidateResolver
	plans           PlanSource
	preparer        WorkspacePreparer
	recommendations RecommendationService
	progressor      FileJanitorProgressor
	folderIntents   *folderIntentStore
	gate            *resetstate.WorkGate
	observer        WorkspaceObserver

	preparationMu sync.Mutex
	preparations  map[string]chan struct{}
}

// lockPreparation serializes Accept and Resume for one owner. An owner has at
// most one active run, so this is the single-flight guard for workspace
// preparation: the preparer is entered at most once concurrently per run, and a
// second tab waits (bounded by its own request) and then replays from what the
// first attempt durably recorded. The lock is in-process state for mutual
// exclusion and for reporting a live attempt; the database remains the record.
func (s *Service) lockPreparation(ctx context.Context, ownerUserID string) (func(), error) {
	s.preparationMu.Lock()
	if s.preparations == nil {
		s.preparations = map[string]chan struct{}{}
	}
	slot, ok := s.preparations[ownerUserID]
	if !ok {
		slot = make(chan struct{}, 1)
		s.preparations[ownerUserID] = slot
	}
	s.preparationMu.Unlock()
	select {
	case slot <- struct{}{}:
		return func() { <-slot }, nil
	case <-ctx.Done():
		return nil, ErrUnavailable
	}
}

// preparing reports whether an Accept or Resume for this owner is executing in
// this process right now.
func (s *Service) preparing(ownerUserID string) bool {
	s.preparationMu.Lock()
	slot := s.preparations[ownerUserID]
	s.preparationMu.Unlock()
	return len(slot) > 0
}

func NewService(store Store, relationships RelationshipReader, resolver CandidateResolver, plans PlanSource, preparer WorkspacePreparer) *Service {
	return &Service{store: store, relationships: relationships, resolver: resolver, plans: plans, preparer: preparer, folderIntents: newFolderIntentStore()}
}

func (s *Service) SetAdmissionGate(gate *resetstate.WorkGate) {
	if s != nil {
		s.gate = gate
	}
}

func (s *Service) SetProgressor(progressor FileJanitorProgressor) {
	if s != nil {
		s.progressor = progressor
	}
}

func (s *Service) SetRecommendationService(recommendations RecommendationService) {
	if s != nil {
		s.recommendations = recommendations
	}
}

func (s *Service) Get(ctx context.Context, ownerUserID, selectedWorkspaceID string) (*Projection, error) {
	if s == nil || s.store == nil {
		return nil, ErrUnavailable
	}
	active, activeErr := s.store.FindActiveRun(ctx, ownerUserID, CapabilityID)
	if activeErr == nil && s.reviewableAgain(ctx, active) {
		active, activeErr = nil, ErrNotFound
	}
	relationship, err := s.relationship(ctx, ownerUserID)
	if err != nil {
		if activeErr == nil && active.Status != RunInvalidated &&
			(errors.Is(err, ErrAssistantNotReady) || errors.Is(err, ErrAssistantRepair)) {
			_, _ = s.store.InvalidateRun(ctx, ownerUserID, active.ID, active.Revision, "assistant_identity_no_longer_available")
		}
		return nil, err
	}
	projection := &Projection{
		SchemaVersion: SchemaVersion,
		CapabilityID:  CapabilityID,
		Relationship:  relationship,
		Actions:       []Action{},
	}
	if activeErr == nil {
		if active.Status != RunInvalidated && active.CurrentStep != StepWorkspace && s.resolver != nil {
			target, targetErr := s.resolver.ResolveOne(ctx, ownerUserID, active.TargetWorkspaceID)
			if errors.Is(targetErr, ErrNotFound) || (targetErr == nil && target != nil && !target.Supported) {
				invalidated, invalidateErr := s.store.InvalidateRun(ctx, ownerUserID, active.ID, active.Revision, "target_no_longer_available")
				if invalidateErr != nil {
					return nil, invalidateErr
				}
				active = invalidated
			} else if targetErr != nil {
				return nil, ErrUnavailable
			}
		}
		return s.projectRun(ctx, projection, active)
	}
	if !errors.Is(activeErr, ErrNotFound) {
		return nil, ErrUnavailable
	}
	if s.resolver == nil {
		return nil, ErrUnavailable
	}
	targets, err := s.resolver.Resolve(ctx, ownerUserID)
	if err != nil {
		return nil, ErrUnavailable
	}
	for i := range targets {
		if !targets[i].Supported || s.progressor == nil {
			continue
		}
		facts, factsErr := s.progressor.Facts(ctx, ownerUserID, "", targets[i].WorkspaceID)
		if factsErr != nil {
			targets[i].Supported = false
			targets[i].Reason = "status_unavailable"
			continue
		}
		targets[i].Readiness = facts.Readiness
		targets[i].Paused = facts.Paused
		targets[i].PrivacyMode = facts.PrivacyMode
		targets[i].MonitoringApproved = facts.MonitoringApproved
		targets[i].MonitoringActive = facts.MonitoringActive
		if facts.PrivacyMode != "" && facts.PrivacyMode != "metadata_only" {
			targets[i].Supported = false
			targets[i].Reason = "privacy_customized"
		}
	}
	var readyTarget *Target
	for i := range targets {
		if targets[i].Supported && targets[i].Readiness == "ready" && targets[i].MonitoringApproved &&
			(len(targets) == 1 || targets[i].WorkspaceID == strings.TrimSpace(selectedWorkspaceID)) {
			candidate := targets[i]
			readyTarget = &candidate
			break
		}
	}
	if readyTarget != nil {
		target := *readyTarget
		projection.Target = &target
		projection.ViewState = "current_status"
		if target.Paused {
			projection.StatusMessage = "This File Janitor workspace is set up and paused. Opening it will not resume monitoring or start a scan."
		} else {
			projection.StatusMessage = "This File Janitor workspace is already set up. Opening it will not reprovision it or start a scan."
		}
		projection.Health = &Health{
			Fresh: true, Readiness: target.Readiness, Paused: target.Paused,
			MonitoringApproved: target.MonitoringApproved, MonitoringActive: target.MonitoringActive,
			PrivacyMode: target.PrivacyMode,
		}
		projection.Actions = []Action{{ID: "open_workspace", Label: "Open current status", Enabled: target.Route != "", Route: target.Route}}
		return projection, nil
	}
	proposal, err := s.buildProposal(ctx, ownerUserID, relationship, targets, selectedWorkspaceID)
	if err != nil {
		return nil, err
	}
	projection.Proposal = proposal
	if s.recommendations != nil {
		deferred, deferredErr := s.recommendations.FileJanitorRecommendationDeferred(ctx, ownerUserID)
		if deferredErr != nil {
			return nil, ErrUnavailable
		}
		if deferred {
			projection.ViewState = "recommendation_deferred"
			projection.StatusMessage = "File Janitor setup is saved for later. You can revisit it from your assistant at any time."
			projection.Actions = []Action{{ID: "accept", Label: "Set up now", Enabled: proposal.Mode != TargetChoose}, {ID: "manual", Label: "Customize manually", Enabled: true, Route: proposalManualRoute(proposal)}}
			return projection, nil
		}
	}
	switch proposal.Mode {
	case TargetChoose:
		projection.ViewState = "decision_required"
		projection.StatusMessage = "Choose which File Janitor workspace your assistant should continue."
		projection.Actions = []Action{{ID: "choose_target", Label: "Choose workspace", Enabled: true}, {ID: "manual", Label: "Review workspaces manually", Enabled: true, Route: "/workspaces"}}
	case TargetAdopt:
		target := proposal.Targets[0]
		projection.Target = &target
		if !target.Supported {
			projection.ViewState = "manual_required"
			projection.StatusMessage = "This File Janitor workspace needs a manual review before assisted setup can continue."
			projection.Actions = []Action{{ID: "manual", Label: "Review setup", Enabled: target.Route != "", Route: target.Route}}
		} else {
			projection.ViewState = "proposed"
			projection.StatusMessage = "Your assistant can continue the existing File Janitor workspace without changing it just by opening this review."
			projection.Actions = proposalActions(target.Route)
		}
	default:
		projection.ViewState = "proposed"
		projection.StatusMessage = "Review the exact workspace and File Curator setup before anything is created."
		projection.Actions = proposalActions("/workspaces/new?template=file-janitor")
	}
	return projection, nil
}

func proposalManualRoute(proposal *Proposal) string {
	if proposal == nil {
		return ""
	}
	if proposal.Mode == TargetAdopt && len(proposal.Targets) == 1 {
		return proposal.Targets[0].Route
	}
	if proposal.Mode == TargetChoose {
		return "/workspaces"
	}
	return "/workspaces/new?template=file-janitor"
}

func proposalActions(manualRoute string) []Action {
	return []Action{
		{ID: "accept", Label: "Set up for me", Enabled: true},
		{ID: "defer_recommendation", Label: "Not now", Enabled: true},
		{ID: "manual", Label: "Customize manually", Enabled: manualRoute != "", Route: manualRoute},
	}
}

func (s *Service) relationship(ctx context.Context, ownerUserID string) (Relationship, error) {
	if s == nil || s.relationships == nil || strings.TrimSpace(ownerUserID) == "" {
		return Relationship{}, ErrUnavailable
	}
	state, err := s.relationships.Get(ctx, ownerUserID)
	if err != nil || state == nil {
		return Relationship{}, ErrUnavailable
	}
	result := Relationship{AssistantID: state.AssistantID, DisplayName: state.DisplayName, State: string(state.State)}
	switch state.State {
	case personalassistant.APIStateNeedsHQ, personalassistant.APIStateProvisioningHQ, personalassistant.APIStateActive:
		result.Eligible = true
		result.Proactive = true
	case personalassistant.APIStatePaused:
		result.Eligible = true
		result.Proactive = false
	case personalassistant.APIStateRepairNeeded:
		return result, ErrAssistantRepair
	case personalassistant.APIStateNeedsHire, personalassistant.APIStateHiring:
		return result, ErrAssistantNotReady
	default:
		return result, ErrUnavailable
	}
	if strings.TrimSpace(result.AssistantID) == "" {
		return result, ErrAssistantRepair
	}
	return result, nil
}

func (s *Service) buildProposal(ctx context.Context, ownerUserID string, relationship Relationship, targets []Target, selectedWorkspaceID string) (*Proposal, error) {
	proposal := &Proposal{
		MetadataDisclosure: metadataDisclosure, FolderDisclosure: folderDisclosure,
		MonitoringDisclosure: monitoringDisclosure, FileReviewDisclosure: fileReviewDisclosure,
		DefaultSchedule: "09:00", Timezone: "local time", NoAutomaticTasks: true, NoFileActions: true,
		Targets: append([]Target(nil), targets...), Team: []TeamRole{},
	}
	selectedWorkspaceID = strings.TrimSpace(selectedWorkspaceID)
	switch len(targets) {
	case 0:
		if selectedWorkspaceID != "" {
			return nil, ErrNotFound
		}
		if s.plans == nil {
			return nil, ErrUnavailable
		}
		plan, err := s.plans.ReviewFileJanitorPlan(ctx, ownerUserID)
		if err != nil {
			return nil, err
		}
		if err := validateTeamPlan(plan); err != nil {
			return nil, err
		}
		proposal.Mode = TargetCreate
		proposal.BlueprintID = plan.BlueprintID
		proposal.BlueprintVersion = plan.BlueprintVersion
		proposal.BlueprintDigest = plan.BlueprintDigest
		proposal.TeamPlanRevision = plan.PlanRevision
		proposal.Team = append([]TeamRole(nil), plan.Roles...)
	case 1:
		if selectedWorkspaceID != "" && selectedWorkspaceID != targets[0].WorkspaceID {
			return nil, ErrNotFound
		}
		proposal.Mode = TargetAdopt
		proposal.SelectedWorkspaceID = targets[0].WorkspaceID
		proposal.Targets = []Target{targets[0]}
		if targets[0].Reason == "privacy_customized" {
			proposal.MetadataDisclosure = "This workspace has a customized privacy mode. Your assistant will not inspect or reset it; review the existing File Janitor settings manually."
			proposal.MonitoringDisclosure = "Existing monitoring and schedule settings are preserved until you review them in the workspace."
		}
	default:
		if selectedWorkspaceID == "" {
			proposal.Mode = TargetChoose
		} else {
			matched := false
			for _, target := range targets {
				if target.WorkspaceID == selectedWorkspaceID {
					proposal.Mode = TargetAdopt
					proposal.SelectedWorkspaceID = target.WorkspaceID
					proposal.Targets = []Target{target}
					matched = true
					break
				}
			}
			if !matched {
				return nil, ErrNotFound
			}
		}
	}
	proposal.Revision = proposalRevision(ownerUserID, relationship.AssistantID, *proposal)
	if proposal.Revision == "" {
		return nil, ErrUnavailable
	}
	return proposal, nil
}

func validateTeamPlan(plan TeamPlan) error {
	if plan.BlueprintID != BlueprintID || plan.BlueprintVersion < 1 ||
		strings.TrimSpace(plan.BlueprintDigest) == "" || strings.TrimSpace(plan.PlanRevision) == "" || len(plan.Roles) != 1 {
		return ErrTeamConflict
	}
	role := plan.Roles[0]
	if strings.TrimSpace(role.RoleID) == "" || strings.TrimSpace(role.Name) == "" || strings.TrimSpace(role.ConfigDigest) == "" ||
		(role.Action != "create" && role.Action != "reuse") {
		return ErrTeamConflict
	}
	return nil
}

func proposalRevision(ownerUserID, assistantID string, proposal Proposal) string {
	canonical := struct {
		Owner, Assistant string
		Mode             TargetMode
		BlueprintID      string
		BlueprintVersion int
		BlueprintDigest  string
		PlanRevision     string
		Team             []TeamRole
		Targets          []Target
		Selected         string
		Metadata         string
		Folder           string
		Monitoring       string
		FileReview       string
		Schedule         string
		Timezone         string
	}{ownerUserID, assistantID, proposal.Mode, proposal.BlueprintID, proposal.BlueprintVersion,
		proposal.BlueprintDigest, proposal.TeamPlanRevision, proposal.Team, proposal.Targets,
		proposal.SelectedWorkspaceID, proposal.MetadataDisclosure, proposal.FolderDisclosure,
		proposal.MonitoringDisclosure, proposal.FileReviewDisclosure, proposal.DefaultSchedule, proposal.Timezone}
	data, err := json.Marshal(canonical)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func (s *Service) Accept(ctx context.Context, ownerUserID, proposalRevisionValue, selectedWorkspaceID string) (*Projection, bool, error) {
	if s == nil || s.store == nil || s.preparer == nil {
		return nil, false, ErrUnavailable
	}
	release, err := s.gate.Enter()
	if err != nil {
		return nil, false, err
	}
	defer release()
	unlock, err := s.lockPreparation(ctx, ownerUserID)
	if err != nil {
		return nil, false, err
	}
	defer unlock()

	// A lost response or second tab reuses the accepted claim before allocating
	// any new identifier. The proposal must still name the exact accepted review.
	// A run stopped by a changed plan before any write no longer blocks review.
	active, activeErr := s.store.FindActiveRun(ctx, ownerUserID, CapabilityID)
	if activeErr == nil && s.reviewableAgain(ctx, active) {
		active, activeErr = nil, ErrNotFound
	}
	if activeErr == nil {
		if active.ProposalRevision != strings.TrimSpace(proposalRevisionValue) {
			return nil, false, ErrConflict
		}
		if selected := strings.TrimSpace(selectedWorkspaceID); selected != "" && selected != active.TargetWorkspaceID {
			return nil, false, ErrNotFound
		}
		projection, resumeErr := s.resumeWorkspaceClaim(ctx, ownerUserID, active)
		return projection, false, resumeErr
	} else if !errors.Is(activeErr, ErrNotFound) {
		return nil, false, ErrUnavailable
	}

	fresh, err := s.Get(ctx, ownerUserID, selectedWorkspaceID)
	if err != nil {
		return nil, false, err
	}
	if fresh.Proposal == nil || fresh.Proposal.Mode == TargetChoose {
		return fresh, false, ErrAmbiguousTarget
	}
	proposal := fresh.Proposal
	if proposal.Revision != strings.TrimSpace(proposalRevisionValue) {
		return fresh, false, ErrStaleProposal
	}
	if proposal.Mode == TargetAdopt && (len(proposal.Targets) != 1 || !proposal.Targets[0].Supported) {
		return fresh, false, ErrUnsupportedTarget
	}

	workspaceID := proposal.SelectedWorkspaceID
	profileProvenanceID := ""
	plan := TeamPlan{}
	var role TeamRole
	if proposal.Mode == TargetCreate {
		workspaceID = uuid.NewString()
		profileProvenanceID = uuid.NewString()
		role = proposal.Team[0]
		plan = TeamPlan{BlueprintID: proposal.BlueprintID, BlueprintVersion: proposal.BlueprintVersion,
			BlueprintDigest: proposal.BlueprintDigest, PlanRevision: proposal.TeamPlanRevision,
			Roles: append([]TeamRole(nil), proposal.Team...)}
	}
	operationID := uuid.NewString()
	acceptance := Acceptance{
		OwnerUserID: ownerUserID, AssistantID: fresh.Relationship.AssistantID,
		ProposalRevision: proposal.Revision, BlueprintID: proposal.BlueprintID,
		BlueprintVersion: proposal.BlueprintVersion, BlueprintDigest: proposal.BlueprintDigest,
		TeamPlanRevision: proposal.TeamPlanRevision, TeamRole: role, TargetMode: proposal.Mode,
		TargetWorkspaceID: workspaceID, WorkspaceOperationID: operationID,
		ProfileProvenanceID: profileProvenanceID, ReviewDigest: proposal.Revision,
	}
	run, _, created, err := s.store.Accept(ctx, acceptance)
	if err != nil {
		return nil, false, err
	}
	projection, err := s.prepareClaim(ctx, run, plan, proposalTarget(proposal))
	return projection, created, err
}

func proposalTarget(proposal *Proposal) *Target {
	if proposal != nil && len(proposal.Targets) == 1 {
		target := proposal.Targets[0]
		return &target
	}
	return nil
}

func (s *Service) resumeWorkspaceClaim(ctx context.Context, owner string, run *Run) (*Projection, error) {
	if run.CurrentStep != StepWorkspace {
		base, err := s.baseProjection(ctx, owner)
		if err != nil {
			return nil, err
		}
		return s.projectRun(ctx, base, run)
	}
	plan := TeamPlan{BlueprintID: run.BlueprintID, BlueprintVersion: run.BlueprintVersion,
		BlueprintDigest: run.BlueprintDigest, PlanRevision: run.TeamPlanRevision}
	if run.TeamRole.RoleID != "" {
		plan.Roles = []TeamRole{run.TeamRole}
	}
	var target *Target
	if run.TargetMode == TargetAdopt {
		resolved, err := s.resolver.ResolveOne(ctx, owner, run.TargetWorkspaceID)
		if err != nil {
			return nil, ErrNoLongerAvailable
		}
		target = resolved
	}
	return s.prepareClaim(ctx, run, plan, target)
}

func (s *Service) prepareClaim(ctx context.Context, run *Run, plan TeamPlan, target *Target) (*Projection, error) {
	operations, err := s.store.ListOperations(ctx, run.OwnerUserID, run.ID)
	if err != nil {
		return nil, ErrUnavailable
	}
	var operation *Operation
	for index := range operations {
		if operations[index].Kind == OperationWorkspace {
			operation = &operations[index]
			break
		}
	}
	if operation == nil {
		return nil, ErrReconcileRequired
	}
	if operation.Status == OperationSucceeded {
		base, baseErr := s.baseProjection(ctx, run.OwnerUserID)
		if baseErr != nil {
			return nil, baseErr
		}
		return s.projectRun(ctx, base, run)
	}
	// The caller holds lockPreparation for this owner, so no other attempt at
	// this run is executing in this process.
	if startErr := s.store.StartWorkspace(ctx, run.OwnerUserID, run.ID, operation.ID); startErr != nil {
		return nil, ErrUnavailable
	}
	request := PrepareRequest{
		OwnerUserID: run.OwnerUserID, RunID: run.ID, OperationID: operation.ID,
		ReviewDigest: operation.ReviewDigest, WorkspaceID: run.TargetWorkspaceID,
		ProfileProvenanceID: operation.ProfileProvenanceID, Mode: run.TargetMode,
		Plan: plan, ExpectedTarget: target,
	}
	result, prepareErr := s.preparer.PrepareFileJanitor(ctx, request)
	if prepareErr != nil {
		return s.settleFailedPreparation(ctx, run, operation, request, prepareErr)
	}
	updated, err := s.store.CompleteWorkspace(ctx, run.OwnerUserID, run.ID, operation.ID, result)
	if err != nil {
		return nil, err
	}
	if updated.TargetMode == TargetAdopt && s.progressor != nil {
		facts, factsErr := s.progressor.Facts(ctx, updated.OwnerUserID, updated.ID, updated.TargetWorkspaceID)
		if factsErr == nil && facts.FolderReady && strings.TrimSpace(facts.RootGenerationID) != "" && strings.TrimSpace(facts.DirectoryReference) != "" {
			updated, err = s.store.AdoptCurrentFolder(ctx, updated.OwnerUserID, updated.ID, updated.Revision, FolderGrantResult{
				RootGenerationID: facts.RootGenerationID, DirectoryReference: facts.DirectoryReference, Adopted: true,
			})
			if err != nil {
				return nil, err
			}
		}
	}
	base, err := s.baseProjection(ctx, run.OwnerUserID)
	if err != nil {
		return nil, err
	}
	return s.projectRun(ctx, base, updated)
}

// DeferRecommendation delegates durable mission deferral without creating an
// assistant-setup run. The reviewed proposal must still be current.
func (s *Service) DeferRecommendation(ctx context.Context, ownerUserID, proposalRevisionValue string) (*Projection, error) {
	if s == nil || s.recommendations == nil {
		return nil, ErrUnavailable
	}
	fresh, err := s.Get(ctx, ownerUserID, "")
	if err != nil {
		return nil, err
	}
	if fresh.Run != nil || fresh.Proposal == nil || fresh.Proposal.Revision != strings.TrimSpace(proposalRevisionValue) {
		return fresh, ErrStaleProposal
	}
	if err := s.recommendations.DeferFileJanitorRecommendation(ctx, ownerUserID); err != nil {
		return nil, ErrUnavailable
	}
	fresh.ViewState = "recommendation_deferred"
	fresh.StatusMessage = "File Janitor setup is saved for later. You can revisit it from your assistant at any time."
	fresh.Actions = []Action{{ID: "accept", Label: "Set up now", Enabled: true}, {ID: "manual", Label: "Customize manually", Enabled: true, Route: "/workspaces/new?template=file-janitor"}}
	return fresh, nil
}

func (s *Service) DeferRun(ctx context.Context, ownerUserID, runID string, ifVersion int64) (*Projection, error) {
	if s == nil || s.store == nil {
		return nil, ErrUnavailable
	}
	run, err := s.store.DeferRun(ctx, ownerUserID, runID, ifVersion)
	if err != nil {
		return nil, err
	}
	base, err := s.baseProjection(ctx, ownerUserID)
	if err != nil {
		return nil, err
	}
	return s.projectRun(ctx, base, run)
}

func (s *Service) ResumeRun(ctx context.Context, ownerUserID, runID string, ifVersion int64) (*Projection, error) {
	if s == nil || s.store == nil {
		return nil, ErrUnavailable
	}
	release, err := s.gate.Enter()
	if err != nil {
		return nil, err
	}
	defer release()
	unlock, err := s.lockPreparation(ctx, ownerUserID)
	if err != nil {
		return nil, err
	}
	defer unlock()
	// An active run stopped at the workspace step (interrupted, or failed with a
	// retryable code) continues on its own claim. Holding the preparation lock
	// proves no other attempt is executing in this process, and preparation is
	// observation-first by the reserved IDs, so nothing is created twice.
	current, err := s.store.GetRun(ctx, ownerUserID, runID)
	if err != nil {
		return nil, err
	}
	if current.Status == RunActive && current.CurrentStep == StepWorkspace {
		if current.Revision != ifVersion {
			return nil, ErrStaleRun
		}
		operation, opErr := s.workspaceOperation(ctx, current)
		if opErr != nil {
			return nil, opErr
		}
		if !workspaceResumable(operation) {
			return nil, ErrInvalidAction
		}
		return s.resumeWorkspaceClaim(ctx, ownerUserID, current)
	}
	run, err := s.store.ResumeRun(ctx, ownerUserID, runID, ifVersion)
	if err != nil {
		return nil, err
	}
	return s.resumeWorkspaceClaim(ctx, ownerUserID, run)
}

func (s *Service) workspaceOperation(ctx context.Context, run *Run) (*Operation, error) {
	operations, err := s.store.ListOperations(ctx, run.OwnerUserID, run.ID)
	if err != nil {
		return nil, ErrUnavailable
	}
	return findWorkspaceOperation(operations), nil
}

func findWorkspaceOperation(operations []Operation) *Operation {
	for index := range operations {
		if operations[index].Kind == OperationWorkspace {
			return &operations[index]
		}
	}
	return nil
}

// workspaceResumable reports a workspace claim that may continue: never
// started, started by a process that stopped, or refused before any write for
// a reason that can clear on its own.
func workspaceResumable(operation *Operation) bool {
	if operation == nil {
		return false
	}
	switch operation.Status {
	case OperationClaimed, OperationRunning:
		return true
	case OperationFailed:
		return retryableWorkspaceCode(operation.SafeErrorCode)
	}
	return false
}

func (s *Service) baseProjection(ctx context.Context, owner string) (*Projection, error) {
	relationship, err := s.relationship(ctx, owner)
	if err != nil {
		return nil, err
	}
	return &Projection{SchemaVersion: SchemaVersion, CapabilityID: CapabilityID, Relationship: relationship, Actions: []Action{}}, nil
}

// projectRun projects one run and attaches its server-derived milestones. The
// milestones are derived from the run as finally projected, after any
// reconciliation inside projectRunState.
//
// A preparation is live when this owner's lock is held before or after the
// durable read. Sampling both sides means an attempt that finishes during the
// read is never mistaken for an interrupted one: a finished attempt leaves its
// operation succeeded, failed, or unresolved.
func (s *Service) projectRun(ctx context.Context, projection *Projection, run *Run) (*Projection, error) {
	return s.projectRunAfter(ctx, projection, run, false)
}

// projectRunAfter projects a run; attemptEnded tells it that the caller holds
// the preparation lock but its own attempt has already finished, so nothing
// is live even though the lock is held.
func (s *Service) projectRunAfter(ctx context.Context, projection *Projection, run *Run, attemptEnded bool) (*Projection, error) {
	liveBefore := !attemptEnded && s.preparing(run.OwnerUserID)
	projected, err := s.projectRunState(ctx, projection, run)
	if err != nil {
		return nil, err
	}
	live := liveBefore || (!attemptEnded && s.preparing(run.OwnerUserID))
	operation := findWorkspaceOperation(projected.Operations)
	if err := s.attachMilestones(ctx, projected, operation, live); err != nil {
		return nil, err
	}
	applyWorkspaceStop(projected, operation, live)
	return projected, nil
}

func (s *Service) attachMilestones(ctx context.Context, projection *Projection, operation *Operation, live bool) error {
	run := projection.Run
	if run == nil {
		return nil
	}
	resources, err := s.store.ListResources(ctx, run.OwnerUserID, run.ID)
	if err != nil {
		return ErrUnavailable
	}
	input := milestoneInput{Run: run, Operation: operation, Resources: resources, InFlight: live && workspaceUnsettled(operation)}
	if projection.Target != nil {
		input.WorkspaceName = projection.Target.Name
	}
	projection.Milestones = deriveMilestones(input)
	return nil
}

func (s *Service) projectRunState(ctx context.Context, projection *Projection, run *Run) (*Projection, error) {
	operations, err := s.store.ListOperations(ctx, run.OwnerUserID, run.ID)
	if err != nil {
		return nil, ErrUnavailable
	}
	projection.Run = run
	projection.Operations = operations
	if run.Status == RunInvalidated {
		projection.ViewState = "no_longer_available"
		projection.StatusMessage = "This setup stopped because its selected workspace or capability is no longer available. Ori will not recreate it or restore access."
		projection.Actions = []Action{{ID: "manual", Label: "Review workspaces", Enabled: true, Route: "/workspaces"}}
		return projection, nil
	}
	if s.resolver != nil {
		target, targetErr := s.resolver.ResolveOne(ctx, run.OwnerUserID, run.TargetWorkspaceID)
		if targetErr == nil && target.Supported {
			projection.Target = target
		} else if targetErr != nil && !errors.Is(targetErr, ErrNotFound) {
			return nil, ErrUnavailable
		}
	}
	var facts FileJanitorFacts
	needsFacts := run.CurrentStep == StepMonitoring || run.CurrentStep == StepInitialScan || run.CurrentStep == StepResult || run.Status == RunFirstResult
	if needsFacts || (run.Status == RunActive && run.CurrentStep == StepFolder && s.progressor != nil) {
		if s.progressor == nil {
			return nil, ErrUnavailable
		}
		var factsErr error
		facts, factsErr = s.progressor.Facts(ctx, run.OwnerUserID, run.ID, run.TargetWorkspaceID)
		if factsErr != nil {
			return nil, factsErr
		}
		projection.Health = &Health{
			Fresh: true, Readiness: facts.Readiness, Paused: facts.Paused,
			MonitoringApproved: facts.MonitoringApproved, MonitoringActive: facts.MonitoringActive,
			PrivacyMode: facts.PrivacyMode, RootGenerationID: facts.RootGenerationID,
			DirectoryReference: facts.DirectoryReference,
		}
		if run.Status == RunActive && run.CurrentStep == StepMonitoring && facts.MonitoringActive &&
			(facts.FirstOutcome == "batch" || facts.FirstOutcome == "no_eligible") && !facts.FirstCompletedAt.IsZero() {
			var monitoringOperation, scanOperation *Operation
			for operationIndex := range operations {
				operation := &operations[operationIndex]
				switch operation.Kind {
				case OperationMonitoring:
					monitoringOperation = operation
				case OperationInitialScan:
					scanOperation = operation
				}
			}
			if monitoringOperation != nil && scanOperation != nil && monitoringOperation.ReviewDigest == scanOperation.ReviewDigest {
				reconciled, reconcileErr := s.store.CompletePrepareReview(ctx, PrepareReviewRequest{
					OwnerUserID: run.OwnerUserID, RunID: run.ID, WorkspaceID: run.TargetWorkspaceID,
					RunRevision: run.Revision, MonitoringOperation: monitoringOperation.ID, ScanOperation: scanOperation.ID,
					Review: MonitoringReview{Revision: monitoringOperation.ReviewDigest, RootGenerationID: facts.RootGenerationID, PrivacyMode: facts.PrivacyMode},
				}, PrepareReviewResult{
					Facts: facts, MonitoringApplied: true, ScanAttempted: true, ScanOutcome: facts.FirstOutcome,
					BatchID: facts.FirstBatchID, EligibleCount: facts.FirstEligible, IneligibleCount: facts.FirstIneligible,
					CompletedAt: facts.FirstCompletedAt,
				})
				if reconcileErr != nil {
					return nil, reconcileErr
				}
				run = reconciled
				projection.Run = reconciled
				operations, err = s.store.ListOperations(ctx, run.OwnerUserID, run.ID)
				if err != nil {
					return nil, ErrUnavailable
				}
				projection.Operations = operations
			}
		}
		if run.Status == RunActive && run.CurrentStep == StepFolder && facts.FolderReady &&
			strings.TrimSpace(facts.RootGenerationID) != "" && strings.TrimSpace(facts.DirectoryReference) != "" {
			adopted, completeErr := s.store.AdoptCurrentFolder(ctx, run.OwnerUserID, run.ID, run.Revision, FolderGrantResult{
				RootGenerationID: facts.RootGenerationID, DirectoryReference: facts.DirectoryReference, Adopted: true,
			})
			if completeErr != nil {
				return nil, completeErr
			}
			run = adopted
			projection.Run = adopted
			operations, err = s.store.ListOperations(ctx, run.OwnerUserID, run.ID)
			if err != nil {
				return nil, ErrUnavailable
			}
			projection.Operations = operations
		}
		if run.Status == RunActive && run.CurrentStep == StepMonitoring && facts.FolderReady &&
			strings.TrimSpace(facts.RootGenerationID) != "" && strings.TrimSpace(facts.DirectoryReference) != "" {
			reconciled, changed, reconcileErr := s.store.ReconcileAdoptedFolder(ctx, run.OwnerUserID, run.ID, run.Revision, FolderGrantResult{
				RootGenerationID: facts.RootGenerationID, DirectoryReference: facts.DirectoryReference, Adopted: true,
			})
			if reconcileErr != nil {
				return nil, reconcileErr
			}
			if changed {
				run = reconciled
				projection.Run = reconciled
				operations, err = s.store.ListOperations(ctx, run.OwnerUserID, run.ID)
				if err != nil {
					return nil, ErrUnavailable
				}
				projection.Operations = operations
			}
		}
	}
	if run.Status == RunDeferred {
		projection.ViewState = "saved_for_later"
		projection.StatusMessage = "Setup is saved for later. Existing access or monitoring was not changed."
		projection.Actions = []Action{{ID: "resume", Label: "Resume setup", Enabled: true}, {ID: "manual", Label: "Continue manually", Enabled: projection.Target != nil, Route: targetRoute(projection.Target)}}
		if facts.MonitoringActive {
			projection.StatusMessage += " Monitoring is still active; pause it separately in File Janitor if you want it stopped."
			projection.Actions = append(projection.Actions, Action{ID: "pause_monitoring", Label: "Pause monitoring", Enabled: projection.Target != nil, Route: targetTabRoute(projection.Target, "settings")})
		}
		return projection, nil
	}
	switch {
	case run.Status == RunReconcileRequired:
		projection.ViewState = "needs_attention"
		projection.StatusMessage = "Ori saved the setup claim but cannot prove the workspace result. Review it before retrying."
		projection.Actions = []Action{{ID: "manual", Label: "Review manually", Enabled: projection.Target != nil, Route: targetRoute(projection.Target)}}
	case run.Status == RunFirstResult || run.CurrentStep == StepResult:
		if (facts.FirstOutcome != "batch" && facts.FirstOutcome != "no_eligible") || facts.FirstCompletedAt.IsZero() ||
			(facts.FirstOutcome == "batch" && strings.TrimSpace(facts.FirstBatchID) == "") {
			projection.ViewState = "needs_attention"
			projection.StatusMessage = "Ori cannot verify the saved first-scan result. Open File Janitor to review its current state."
			projection.Actions = []Action{{ID: "manual", Label: "Review File Janitor", Enabled: projection.Target != nil, Route: targetRoute(projection.Target)}}
			break
		}
		projection.FirstResult = &FirstResult{
			Outcome: facts.FirstOutcome, BatchID: facts.FirstBatchID,
			EligibleCount: facts.FirstEligible, IneligibleCount: facts.FirstIneligible,
			CompletedAt: facts.FirstCompletedAt,
		}
		if facts.FirstOutcome == "batch" {
			projection.ViewState = "first_review_ready"
			projection.StatusMessage = "Your first metadata-only filing proposals are ready to review. Nothing has moved."
			projection.FirstResult.ReviewRoute = reviewBatchRoute(projection.Target, facts.FirstBatchID)
			projection.Actions = []Action{{ID: "review_batch", Label: "Review proposed filing", Enabled: projection.Target != nil, Route: projection.FirstResult.ReviewRoute}, {ID: "open_workspace", Label: "Open workspace", Enabled: projection.Target != nil, Route: targetRoute(projection.Target)}}
		} else {
			projection.ViewState = "no_new_files"
			projection.StatusMessage = "The first metadata-only scan completed. There are no new eligible files to review, and nothing moved."
			projection.Actions = []Action{{ID: "open_workspace", Label: "Open workspace", Enabled: projection.Target != nil, Route: targetRoute(projection.Target)}}
		}
		projection.Actions = append(projection.Actions,
			Action{ID: "manage_access", Label: "Manage access", Enabled: projection.Target != nil, Route: targetTabRoute(projection.Target, "settings")},
			Action{ID: "history", Label: "History", Enabled: projection.Target != nil, Route: targetTabRoute(projection.Target, "history")},
		)
		if facts.Paused {
			projection.StatusMessage += " Monitoring is currently paused."
		} else if !facts.MonitoringActive {
			projection.StatusMessage += " Monitoring needs attention."
		} else {
			projection.Actions = append(projection.Actions, Action{ID: "pause_monitoring", Label: "Pause monitoring", Enabled: projection.Target != nil, Route: targetTabRoute(projection.Target, "settings")})
		}
	case run.CurrentStep == StepFolder:
		projection.ViewState = "needs_permission"
		projection.StatusMessage = "Workspace prepared. Choose the folder to tidy when you are ready."
		projection.Actions = []Action{{ID: "choose_folder", Label: "Choose folder", Enabled: true}, {ID: "finish_later", Label: "Finish later", Enabled: true}, {ID: "manual_takeover", Label: "Continue manually", Enabled: projection.Target != nil}}
	case run.CurrentStep == StepMonitoring:
		if !facts.FolderReady || strings.TrimSpace(facts.RootGenerationID) == "" || strings.TrimSpace(facts.DirectoryReference) == "" {
			projection.ViewState = "needs_attention"
			projection.StatusMessage = "File Janitor folder access needs attention. Review or relink it manually; Ori will not restore access on its own."
			projection.Actions = []Action{{ID: "manual_takeover", Label: "Manage access manually", Enabled: projection.Target != nil}}
			break
		}
		review, reviewErr := s.monitoringReview(ctx, run.OwnerUserID, run)
		if reviewErr != nil {
			return nil, reviewErr
		}
		projection.Monitoring = review
		if review.PrivacyMode != "metadata_only" {
			projection.ViewState = "manual_required"
			projection.StatusMessage = "This workspace uses a custom privacy mode. Review it manually before assisted monitoring starts."
			projection.Actions = []Action{{ID: "manual_takeover", Label: "Review settings manually", Enabled: projection.Target != nil}}
			break
		}
		projection.ViewState = "needs_decision"
		projection.StatusMessage = "Folder access is saved and monitoring is still off. Review the watcher, settling delay, and daily scan before starting them."
		label := "Start monitoring and prepare review"
		if facts.MonitoringApproved && facts.MonitoringActive {
			projection.StatusMessage = "Existing monitoring is active. Review the current watcher, settling delay, privacy, and daily scan before preparing the first review."
			label = "Prepare first review"
		} else if facts.MonitoringApproved && facts.Paused {
			projection.StatusMessage = "Existing monitoring approval is saved, but monitoring is paused. Review the current settings before starting it for the first review."
		}
		switch run.FailedStep {
		case StepMonitoring:
			projection.ViewState = "needs_attention"
			projection.StatusMessage = "Monitoring could not be started and remains off. Review the unchanged settings and try again."
			label = "Try starting monitoring again"
		case StepInitialScan:
			projection.ViewState = "needs_attention"
			if facts.MonitoringActive {
				projection.StatusMessage = "The first metadata-only scan did not complete. Existing monitoring remains active; review the settings and try again."
			} else {
				projection.StatusMessage = "The first metadata-only scan did not complete. Monitoring was paused safely; review the settings and try again."
			}
			label = "Try preparing the review again"
		}
		projection.Actions = []Action{{ID: "prepare_review", Label: label, Enabled: true}, {ID: "finish_later", Label: "Finish later", Enabled: true}, {ID: "manual_takeover", Label: "Continue manually", Enabled: projection.Target != nil}}
	default:
		projection.ViewState = "setting_up"
		projection.StatusMessage = "Preparing the reviewed File Janitor workspace."
		projection.Actions = []Action{{ID: "finish_later", Label: "Finish later", Enabled: true}}
	}
	return projection, nil
}

func reviewBatchRoute(target *Target, batchID string) string {
	route := targetRoute(target)
	if route == "" || strings.TrimSpace(batchID) == "" {
		return route
	}
	separator := "?"
	if strings.Contains(route, "?") {
		separator = "&"
	}
	return route + separator + "batch_id=" + strings.TrimSpace(batchID)
}

func targetTabRoute(target *Target, tab string) string {
	route := targetRoute(target)
	if route == "" || strings.TrimSpace(tab) == "" {
		return route
	}
	separator := "?"
	if strings.Contains(route, "?") {
		separator = "&"
	}
	return route + separator + "tab=" + strings.TrimSpace(tab)
}

func targetRoute(target *Target) string {
	if target == nil {
		return ""
	}
	return target.Route
}

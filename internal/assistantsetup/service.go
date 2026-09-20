package assistantsetup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/resetstate"
)

const (
	metadataDisclosure   = "File Janitor classifies files from names, types, sizes, and dates only. It does not read file contents or send them to a model."
	folderDisclosure     = "You will choose one folder next. Granting it allows Ori to list immediate-child metadata and create or reuse Filed inside that folder; it does not start monitoring."
	monitoringDisclosure = "Monitoring is a separate decision. If approved, Ori watches create and rename events after a five-minute settling delay and runs a daily catch-up at 09:00 local time."
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
	gate            *resetstate.WorkGate
}

func NewService(store Store, relationships RelationshipReader, resolver CandidateResolver, plans PlanSource, preparer WorkspacePreparer) *Service {
	return &Service{store: store, relationships: relationships, resolver: resolver, plans: plans, preparer: preparer}
}

func (s *Service) SetAdmissionGate(gate *resetstate.WorkGate) {
	if s != nil {
		s.gate = gate
	}
}

func (s *Service) SetRecommendationService(recommendations RecommendationService) {
	if s != nil {
		s.recommendations = recommendations
	}
}

func (s *Service) Get(ctx context.Context, ownerUserID, selectedWorkspaceID string) (*Projection, error) {
	relationship, err := s.relationship(ctx, ownerUserID)
	if err != nil {
		return nil, err
	}
	projection := &Projection{
		SchemaVersion: SchemaVersion,
		CapabilityID:  CapabilityID,
		Relationship:  relationship,
		Actions:       []Action{},
	}
	if s.store == nil {
		return nil, ErrUnavailable
	}
	active, activeErr := s.store.FindActiveRun(ctx, ownerUserID, CapabilityID)
	if activeErr == nil {
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

	// A lost response or second tab reuses the accepted claim before allocating
	// any new identifier. The proposal must still name the exact accepted review.
	if active, activeErr := s.store.FindActiveRun(ctx, ownerUserID, CapabilityID); activeErr == nil {
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
	result, prepareErr := s.preparer.PrepareFileJanitor(ctx, PrepareRequest{
		OwnerUserID: run.OwnerUserID, RunID: run.ID, OperationID: operation.ID,
		ReviewDigest: operation.ReviewDigest, WorkspaceID: run.TargetWorkspaceID,
		ProfileProvenanceID: operation.ProfileProvenanceID, Mode: run.TargetMode,
		Plan: plan, ExpectedTarget: target,
	})
	if prepareErr != nil {
		_, _ = s.store.MarkWorkspaceUnresolved(ctx, run.OwnerUserID, run.ID, operation.ID, "workspace_outcome_unresolved")
		return nil, fmt.Errorf("%w: %v", ErrReconcileRequired, prepareErr)
	}
	updated, err := s.store.CompleteWorkspace(ctx, run.OwnerUserID, run.ID, operation.ID, result)
	if err != nil {
		return nil, err
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
	projection, err := s.projectRun(ctx, base, run)
	if err == nil {
		projection.ViewState = "saved_for_later"
		projection.StatusMessage = "Setup is saved for later. Existing access or monitoring was not changed."
		projection.Actions = []Action{{ID: "resume", Label: "Resume setup", Enabled: true}, {ID: "manual", Label: "Set up manually", Enabled: projection.Target != nil, Route: targetRoute(projection.Target)}}
	}
	return projection, err
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
	run, err := s.store.ResumeRun(ctx, ownerUserID, runID, ifVersion)
	if err != nil {
		return nil, err
	}
	return s.resumeWorkspaceClaim(ctx, ownerUserID, run)
}

func (s *Service) baseProjection(ctx context.Context, owner string) (*Projection, error) {
	relationship, err := s.relationship(ctx, owner)
	if err != nil {
		return nil, err
	}
	return &Projection{SchemaVersion: SchemaVersion, CapabilityID: CapabilityID, Relationship: relationship, Actions: []Action{}}, nil
}

func (s *Service) projectRun(ctx context.Context, projection *Projection, run *Run) (*Projection, error) {
	operations, err := s.store.ListOperations(ctx, run.OwnerUserID, run.ID)
	if err != nil {
		return nil, ErrUnavailable
	}
	projection.Run = run
	projection.Operations = operations
	if s.resolver != nil {
		if target, targetErr := s.resolver.ResolveOne(ctx, run.OwnerUserID, run.TargetWorkspaceID); targetErr == nil {
			projection.Target = target
		}
	}
	switch {
	case run.Status == RunReconcileRequired:
		projection.ViewState = "needs_attention"
		projection.StatusMessage = "Ori saved the setup claim but cannot prove the workspace result. Review it before retrying."
		projection.Actions = []Action{{ID: "manual", Label: "Review manually", Enabled: projection.Target != nil, Route: targetRoute(projection.Target)}}
	case run.CurrentStep == StepFolder:
		projection.ViewState = "needs_permission"
		projection.StatusMessage = "Workspace prepared. Choose the folder to tidy when you are ready."
		projection.Actions = []Action{{ID: "choose_folder", Label: "Choose folder", Enabled: true}, {ID: "finish_later", Label: "Finish later", Enabled: true}, {ID: "manual", Label: "Set up manually", Enabled: projection.Target != nil, Route: targetRoute(projection.Target)}}
	default:
		projection.ViewState = "setting_up"
		projection.StatusMessage = "Preparing the reviewed File Janitor workspace."
		projection.Actions = []Action{{ID: "finish_later", Label: "Finish later", Enabled: true}}
	}
	return projection, nil
}

func targetRoute(target *Target) string {
	if target == nil {
		return ""
	}
	return target.Route
}

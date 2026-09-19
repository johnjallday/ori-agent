// Package grouprequirements owns template-controlled workspace placement.
// Template/plugin declarations are inert inputs; this package alone resolves a
// current owner, exact Assistant Program Home, reviewed choice, and mutations.
package grouprequirements

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

const (
	ReviewTTL = 15 * time.Minute

	CompositionGrouped    = workspace.GroupRequirementCompositionGrouped
	CompositionStandalone = workspace.GroupRequirementCompositionStandalone
)

type State string

const (
	StateLegacy                     State = "legacy"
	StateReadyGrouped               State = "ready_grouped"
	StateReadyStandalone            State = "ready_standalone"
	StateChoiceRequired             State = "choice_required"
	StateHomeCreationReviewRequired State = "home_creation_review_required"
	StateHomeRequired               State = "home_required"
	StateSourceUnavailable          State = "source_unavailable"
	StateTargetAmbiguous            State = "target_ambiguous"
	StateContractInvalid            State = "contract_invalid"
)

type Action string

const (
	ActionChooseGrouped    Action = "choose_grouped"
	ActionChooseStandalone Action = "choose_standalone"
	ActionReviewCreateHome Action = "review_create_home"
	ActionOpenGuidedSetup  Action = "open_guided_setup"
	// ActionOpenGroupRoles routes to the exact canonical Home's role roster.
	// It is inert navigation projected only after that Home is resolved.
	ActionOpenGroupRoles Action = "open_group_roles"
	ActionCustomize      Action = "customize_template"
	ActionChangeTemplate Action = "change_template"
	ActionManagePlugins  Action = "manage_plugins"
	ActionRetry          Action = "retry"
)

type OperationKind string

const (
	// OperationPrepareHome is the separately confirmed prerequisite for grouped
	// project creation. It may create or reuse only the canonical Home; it never
	// reserves, creates, moves, or links a project workspace.
	OperationPrepareHome             OperationKind = "prepare_home"
	OperationCreateWorkspace         OperationKind = "create_workspace"
	OperationCreateProject           OperationKind = "create_project"
	OperationConnectProject          OperationKind = "connect_project"
	OperationDeleteRequiredWorkspace OperationKind = "delete_required_workspace"
)

type Input struct {
	OwnerUserID       string
	OperationKind     OperationKind
	Template          projecttemplates.Template
	Composition       string
	CreateHome        bool
	RequestedParentID string
	TargetWorkspaceID string
	InputDigest       string
	// HomeName is a reviewed user-chosen display name for a Home created by
	// OperationPrepareHome. It never renames an existing Home.
	HomeName string
	// GroupTemplateID and GroupTemplateRevision bind a Home-only operation to
	// one derived Group Template selection. When set, first creation records
	// inert Group Template provenance in the same write as the Home.
	GroupTemplateID       string
	GroupTemplateRevision string
}

type Evaluation struct {
	State               State                          `json:"state"`
	Policy              projecttemplates.GroupPolicy   `json:"policy,omitempty"`
	SelectedComposition string                         `json:"selected_composition,omitempty"`
	HomeWorkspaceID     string                         `json:"home_workspace_id,omitempty"`
	HomeName            string                         `json:"home_name,omitempty"`
	HomeWillBeCreated   bool                           `json:"home_will_be_created,omitempty"`
	Summary             string                         `json:"summary"`
	Detail              string                         `json:"detail,omitempty"`
	Actions             []Action                       `json:"actions,omitempty"`
	EffectiveTemplate   projecttemplates.Template      `json:"-"`
	ProgramKey          *workspace.AssistantProgramKey `json:"-"`
	DefinitionDigest    string                         `json:"-"`
}

type Review struct {
	Evaluation
	Token        string    `json:"review_token,omitempty"`
	ReviewDigest string    `json:"review_digest,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitempty"`
}

type OperationStatus string

const (
	OperationClaimed           OperationStatus = "claimed"
	OperationHomeReady         OperationStatus = "home_ready"
	OperationChildReady        OperationStatus = "child_ready"
	OperationLinkReady         OperationStatus = "link_ready"
	OperationSucceeded         OperationStatus = "succeeded"
	OperationReconcileRequired OperationStatus = "reconcile_required"
)

type Receipt struct {
	Token                   string                         `json:"token"`
	OwnerUserID             string                         `json:"owner_user_id"`
	OperationKind           OperationKind                  `json:"operation_kind"`
	InputDigest             string                         `json:"input_digest"`
	TemplateID              string                         `json:"template_id"`
	TemplateRevision        string                         `json:"template_revision,omitempty"`
	VariantID               string                         `json:"variant_id,omitempty"`
	VariantRevision         string                         `json:"variant_revision,omitempty"`
	DefinitionDigest        string                         `json:"definition_digest"`
	Policy                  projecttemplates.GroupPolicy   `json:"policy"`
	Composition             string                         `json:"composition"`
	ProgramKey              *workspace.AssistantProgramKey `json:"program_key,omitempty"`
	HomeWorkspaceID         string                         `json:"home_workspace_id,omitempty"`
	CreateHome              bool                           `json:"create_home,omitempty"`
	DefaultHomeName         string                         `json:"default_home_name,omitempty"`
	RequestedParentID       string                         `json:"requested_parent_id,omitempty"`
	TargetWorkspaceID       string                         `json:"target_workspace_id,omitempty"`
	GroupTemplateID         string                         `json:"group_template_id,omitempty"`
	GroupTemplateRev        string                         `json:"group_template_revision,omitempty"`
	DeleteSessions          bool                           `json:"delete_sessions,omitempty"`
	TrashWorkspace          bool                           `json:"trash_workspace,omitempty"`
	ContractOperationDigest string                         `json:"contract_operation_digest,omitempty"`
	ContractSnapshotDigest  string                         `json:"contract_snapshot_digest,omitempty"`
	ReviewDigest            string                         `json:"review_digest"`
	CreatedAt               time.Time                      `json:"created_at"`
	ExpiresAt               time.Time                      `json:"expires_at"`
	ConsumedAt              *time.Time                     `json:"consumed_at,omitempty"`
}

type Operation struct {
	OwnerUserID      string          `json:"owner_user_id"`
	OperationKind    OperationKind   `json:"operation_kind"`
	IdempotencyKey   string          `json:"idempotency_key"`
	InputDigest      string          `json:"input_digest"`
	OperationDigest  string          `json:"operation_digest"`
	ReviewDigest     string          `json:"review_digest"`
	Status           OperationStatus `json:"status"`
	ChildWorkspaceID string          `json:"child_workspace_id"`
	HomeWorkspaceID  string          `json:"home_workspace_id,omitempty"`
	ProjectLinkID    string          `json:"project_link_id,omitempty"`
	AppliedAt        time.Time       `json:"applied_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
}

type ReceiptStore interface {
	SaveReceipt(context.Context, Receipt) error
	GetReceipt(context.Context, string) (Receipt, error)
	ConsumeReceipt(context.Context, string, time.Time) error
	GetOperation(context.Context, string, OperationKind, string) (Operation, error)
	SaveOperation(context.Context, Operation) error
}

var (
	ErrReviewRequired       = errors.New("group requirement review is required")
	ErrReviewStale          = errors.New("group requirement review is stale")
	ErrOperationConflict    = errors.New("group requirement operation conflicts with an earlier request")
	ErrLifecycleConflict    = errors.New("group requirement lifecycle state conflicts with this operation")
	ErrLifecycleNotRequired = errors.New("workspace has no Required group requirement to review")
	ErrUnavailable          = errors.New("group requirement owner is unavailable")
	ErrNotFound             = errors.New("group requirement receipt was not found")
)

type Service struct {
	workspaces workspace.Store
	receipts   ReceiptStore
	now        func() time.Time
	mu         sync.Mutex
}

func NewService(workspaces workspace.Store, receipts ReceiptStore) *Service {
	return &Service{workspaces: workspaces, receipts: receipts, now: func() time.Time { return time.Now().UTC() }}
}

func (s *Service) Evaluate(input Input) Evaluation {
	template := input.Template
	if template.GroupRequirement == nil {
		return Evaluation{State: StateLegacy, Summary: "This template uses its existing placement behavior.", EffectiveTemplate: template}
	}
	if s == nil || s.workspaces == nil || s.receipts == nil || strings.TrimSpace(input.OwnerUserID) == "" {
		return unavailable(StateSourceUnavailable, "Template placement ownership is unavailable.", ActionRetry)
	}
	if template.HasInvalidGroupRequirement() || template.HasInvalidStandaloneComposition() || !validOperationKind(input.OperationKind) {
		return unavailable(StateContractInvalid, "This template's group requirement cannot be applied.", ActionCustomize, ActionChangeTemplate)
	}
	var targetWorkspace *workspace.Workspace
	if targetID := strings.TrimSpace(input.TargetWorkspaceID); targetID != "" {
		target, err := s.workspaces.Get(targetID)
		if err != nil || target == nil || target.OwnerUserID != strings.TrimSpace(input.OwnerUserID) {
			return unavailable(StateSourceUnavailable, "The target workspace owner cannot be verified.", ActionRetry)
		}
		targetWorkspace = target
	}
	requirement := template.GroupRequirement
	evaluation := Evaluation{Policy: requirement.Policy, EffectiveTemplate: template, DefinitionDigest: definitionDigest(template)}
	switch requirement.Policy {
	case projecttemplates.GroupPolicyNone:
		standalone, err := standaloneTemplate(template)
		if err != nil {
			return unavailable(StateContractInvalid, "This template has no valid standalone composition.", ActionCustomize, ActionChangeTemplate)
		}
		if targetWorkspace != nil && targetWorkspace.GetAssistantProjectLink() != nil {
			return unavailable(StateContractInvalid, "A linked workspace needs the reviewed disconnect or reconnect flow.", ActionOpenGuidedSetup)
		}
		evaluation.State = StateReadyStandalone
		evaluation.SelectedComposition = CompositionStandalone
		evaluation.Summary = "The project will be created as an ordinary standalone workspace."
		evaluation.EffectiveTemplate = standalone
		return evaluation
	case projecttemplates.GroupPolicyRecommended:
		switch strings.TrimSpace(input.Composition) {
		case "":
			return Evaluation{State: StateChoiceRequired, Policy: requirement.Policy, Summary: "Choose grouped or standalone placement.", Actions: []Action{ActionChooseGrouped, ActionChooseStandalone}, DefinitionDigest: evaluation.DefinitionDigest}
		case CompositionStandalone:
			standalone, err := standaloneTemplate(template)
			if err != nil {
				return unavailable(StateContractInvalid, "This template has no valid standalone composition.", ActionCustomize, ActionChangeTemplate)
			}
			if targetWorkspace != nil && targetWorkspace.GetAssistantProjectLink() != nil {
				return unavailable(StateContractInvalid, "A linked workspace needs the reviewed disconnect or reconnect flow.", ActionOpenGuidedSetup)
			}
			evaluation.State = StateReadyStandalone
			evaluation.SelectedComposition = CompositionStandalone
			evaluation.Summary = "The project will be created without an Assistant Program Home."
			evaluation.EffectiveTemplate = standalone
			return evaluation
		case CompositionGrouped:
			// Continue through exact target resolution below.
		default:
			return unavailable(StateContractInvalid, "The selected composition is not supported.", ActionChooseGrouped, ActionChooseStandalone)
		}
	case projecttemplates.GroupPolicyRequired:
		if choice := strings.TrimSpace(input.Composition); choice != "" && choice != CompositionGrouped {
			return unavailable(StateContractInvalid, "This template requires grouped placement.", ActionCustomize, ActionChangeTemplate)
		}
	default:
		return unavailable(StateContractInvalid, "This template's group policy is unsupported.", ActionCustomize, ActionChangeTemplate)
	}

	key, err := programKey(input.OwnerUserID, template)
	if err != nil {
		return unavailable(StateSourceUnavailable, "The exact group owner cannot be verified.", ActionManagePlugins, ActionChangeTemplate)
	}
	evaluation.SelectedComposition = CompositionGrouped
	evaluation.ProgramKey = &key
	if targetWorkspace != nil {
		if link := targetWorkspace.GetAssistantProjectLink(); link != nil && link.Key.Normalize() != key {
			return unavailable(StateTargetAmbiguous, "The workspace is linked to a different canonical group.", ActionOpenGuidedSetup)
		}
	}
	programs := workspace.NewAssistantProgramStore(s.workspaces)
	home, findErr := programs.FindStation(key)
	if findErr == nil && home != nil {
		if !compatibleHome(home, key, template.AssistantProgram) {
			return unavailable(StateTargetAmbiguous, "The canonical group state conflicts with this template.", ActionOpenGuidedSetup, ActionRetry)
		}
		if requested := strings.TrimSpace(input.RequestedParentID); requested != "" && requested != home.ID {
			return unavailable(StateContractInvalid, "The requested parent is not the template's canonical group.", ActionRetry)
		}
		evaluation.State = StateReadyGrouped
		evaluation.HomeWorkspaceID = home.ID
		evaluation.HomeName = home.Name
		evaluation.Summary = "The project will be created in the existing canonical group."
		return evaluation
	}
	if findErr != nil && !errors.Is(findErr, workspace.ErrAssistantStationNotFound) {
		return unavailable(StateTargetAmbiguous, "The canonical group could not be resolved uniquely.", ActionOpenGuidedSetup, ActionRetry)
	}
	if strings.TrimSpace(input.RequestedParentID) != "" {
		return unavailable(StateContractInvalid, "An arbitrary parent cannot satisfy this template's group requirement.", ActionRetry)
	}
	if requirement.MissingHome == projecttemplates.MissingHomeExistingOnly {
		return unavailable(StateHomeRequired, "The required canonical group does not exist.", ActionOpenGuidedSetup, ActionChangeTemplate)
	}
	if requirement.MissingHome != projecttemplates.MissingHomeOfferCreate {
		return unavailable(StateContractInvalid, "This template's missing-group behavior is unsupported.", ActionCustomize, ActionChangeTemplate)
	}
	// A project operation may never create its own prerequisite Home. Keeping
	// this refusal in the owner—not only in the browser—ensures direct HTTP,
	// chat, and orchestration callers cannot collapse the two confirmations.
	if input.OperationKind != OperationPrepareHome || !input.CreateHome {
		return Evaluation{State: StateHomeCreationReviewRequired, Policy: requirement.Policy, SelectedComposition: CompositionGrouped,
			HomeName: requirement.DefaultHomeName, HomeWillBeCreated: true,
			Summary: "Create the canonical group before creating the project workspace.", Actions: []Action{ActionReviewCreateHome, ActionOpenGuidedSetup},
			EffectiveTemplate: template, ProgramKey: &key, DefinitionDigest: evaluation.DefinitionDigest}
	}
	evaluation.State = StateReadyGrouped
	evaluation.HomeName = requirement.DefaultHomeName
	if name := strings.TrimSpace(input.HomeName); name != "" {
		if projecttemplates.ValidateHomeDisplayName(name) != nil {
			return unavailable(StateContractInvalid, "The group name is not valid.", ActionRetry)
		}
		evaluation.HomeName = name
	}
	evaluation.HomeWillBeCreated = true
	evaluation.Summary = "Only the canonical group will be created by this action."
	return evaluation
}

func (s *Service) Review(ctx context.Context, input Input) (Review, error) {
	evaluation := s.Evaluate(input)
	if evaluation.State == StateLegacy {
		return Review{Evaluation: evaluation}, nil
	}
	if evaluation.State != StateReadyGrouped && evaluation.State != StateReadyStandalone {
		return Review{Evaluation: evaluation}, nil
	}
	if !validDigest(input.InputDigest) {
		return Review{}, fmt.Errorf("%w: input digest is invalid", ErrReviewRequired)
	}
	now := s.now()
	receipt := Receipt{
		Token: uuid.NewString(), OwnerUserID: strings.TrimSpace(input.OwnerUserID), OperationKind: input.OperationKind,
		InputDigest: strings.ToLower(input.InputDigest), TemplateID: input.Template.ID, TemplateRevision: input.Template.Revision,
		DefinitionDigest: evaluation.DefinitionDigest, Policy: evaluation.Policy, Composition: evaluation.SelectedComposition,
		HomeWorkspaceID: evaluation.HomeWorkspaceID, CreateHome: evaluation.HomeWillBeCreated, DefaultHomeName: evaluation.HomeName,
		RequestedParentID: strings.TrimSpace(input.RequestedParentID), TargetWorkspaceID: strings.TrimSpace(input.TargetWorkspaceID),
		GroupTemplateID: strings.TrimSpace(input.GroupTemplateID), GroupTemplateRev: strings.TrimSpace(input.GroupTemplateRevision),
		CreatedAt: now, ExpiresAt: now.Add(ReviewTTL),
	}
	if (receipt.GroupTemplateID != "" || receipt.GroupTemplateRev != "") &&
		(input.OperationKind != OperationPrepareHome || receipt.GroupTemplateID == "" || receipt.GroupTemplateRev == "") {
		return Review{}, fmt.Errorf("%w: a group template selection binds only a complete Home-only review", ErrReviewRequired)
	}
	if input.Template.TemplateVariant != nil {
		receipt.VariantID = input.Template.TemplateVariant.VariantID
		receipt.VariantRevision = input.Template.VariantRevision
	}
	if evaluation.ProgramKey != nil {
		key := evaluation.ProgramKey.Normalize()
		receipt.ProgramKey = &key
	}
	receipt.ReviewDigest = receiptDigest(receipt)
	if err := s.receipts.SaveReceipt(ctx, receipt); err != nil {
		return Review{}, ErrUnavailable
	}
	return Review{Evaluation: evaluation, Token: receipt.Token, ReviewDigest: receipt.ReviewDigest, ExpiresAt: receipt.ExpiresAt}, nil
}

type Claim struct {
	Operation         Operation
	EffectiveTemplate projecttemplates.Template
	Snapshot          *workspace.GroupRequirementSnapshot
	HomeCreated       bool
	Replayed          bool
}

// DeletionInput binds a lifecycle review to the exact workspace and deletion
// consequence the user saw. TrashWorkspace is decided by the host from its
// current platform/store support; it is never supplied by a browser as policy.
type DeletionInput struct {
	OwnerUserID    string
	WorkspaceID    string
	DeleteSessions bool
	TrashWorkspace bool
}

// DeletionReview is an inert, expiring authorization preview. The original
// Required creation contract remains intact so restoring a trashed workspace
// cannot silently turn it into an ordinary workspace.
type DeletionReview struct {
	Token          string    `json:"review_token"`
	ExpiresAt      time.Time `json:"expires_at"`
	WorkspaceID    string    `json:"workspace_id"`
	State          string    `json:"state"`
	Summary        string    `json:"summary"`
	Impact         []string  `json:"impact"`
	DeleteSessions bool      `json:"delete_sessions"`
	Trashes        bool      `json:"trashes"`
}

// DeletionClaim is the narrow proof the folder store accepts for one reviewed
// Required-contract removal. It carries the immutable creation-operation digest
// rather than a browser-provided bypass flag.
type DeletionClaim struct {
	WorkspaceID             string
	ContractOperationDigest string
	DeleteSessions          bool
	TrashWorkspace          bool
}

// ReviewDeletion issues a durable, expiring receipt only after canonical state
// proves the Required project is already detached from all live Assistant
// Program topology. It changes no workspace, project, Home, task, or file.
func (s *Service) ReviewDeletion(ctx context.Context, input DeletionInput) (DeletionReview, error) {
	candidate, snapshot, err := s.requiredDeletionCandidate(input)
	if err != nil {
		return DeletionReview{}, err
	}
	inputDigest, err := deletionInputDigest(input)
	if err != nil {
		return DeletionReview{}, ErrReviewRequired
	}
	contractSnapshotDigest, err := DigestInput(snapshot)
	if err != nil {
		return DeletionReview{}, ErrReviewRequired
	}
	now := s.now()
	receipt := Receipt{
		Token: uuid.NewString(), OwnerUserID: strings.TrimSpace(input.OwnerUserID),
		OperationKind: OperationDeleteRequiredWorkspace, InputDigest: inputDigest,
		TemplateID: snapshot.TemplateID, TemplateRevision: snapshot.TemplateRevision,
		DefinitionDigest: snapshot.DefinitionDigest, Policy: projecttemplates.GroupPolicyRequired,
		Composition: CompositionGrouped, TargetWorkspaceID: candidate.ID,
		DeleteSessions: input.DeleteSessions, TrashWorkspace: input.TrashWorkspace,
		ContractOperationDigest: snapshot.OperationDigest, ContractSnapshotDigest: contractSnapshotDigest,
		CreatedAt: now, ExpiresAt: now.Add(ReviewTTL),
	}
	receipt.ReviewDigest = receiptDigest(receipt)
	if err := s.receipts.SaveReceipt(ctx, receipt); err != nil {
		return DeletionReview{}, ErrUnavailable
	}
	summary := fmt.Sprintf("Review deletion of %q and its recorded Required template contract.", candidate.Name)
	consequence := "The workspace will be removed using Ori's permanent deletion path."
	if input.TrashWorkspace {
		consequence = "The workspace will move to the system Trash and can be restored with its Required contract still recorded."
	}
	impact := []string{
		consequence,
		"The recorded Assistant Program Home will not be created, removed, or reconnected by this deletion.",
		"External project folders remain protected by Ori's existing workspace deletion boundary.",
	}
	if input.DeleteSessions {
		impact = append(impact, "Sessions belonging to this workspace will also be deleted.")
	}
	return DeletionReview{
		Token: receipt.Token, ExpiresAt: receipt.ExpiresAt, WorkspaceID: candidate.ID,
		State: "deletion_review_ready", Summary: summary, Impact: impact,
		DeleteSessions: input.DeleteSessions, Trashes: input.TrashWorkspace,
	}, nil
}

// ClaimDeletion consumes one exact review after re-reading canonical state. A
// failed delete requires a fresh review; a consumed token can never become a
// reusable generic deletion capability.
func (s *Service) ClaimDeletion(ctx context.Context, input DeletionInput, token string) (DeletionClaim, error) {
	if s == nil || s.workspaces == nil || s.receipts == nil {
		return DeletionClaim{}, ErrUnavailable
	}
	token = strings.TrimSpace(token)
	if token == "" || len(token) > 160 {
		return DeletionClaim{}, ErrReviewRequired
	}
	inputDigest, err := deletionInputDigest(input)
	if err != nil {
		return DeletionClaim{}, ErrReviewRequired
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	receipt, err := s.receipts.GetReceipt(ctx, token)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return DeletionClaim{}, ErrReviewRequired
		}
		return DeletionClaim{}, ErrUnavailable
	}
	if receipt.OwnerUserID != strings.TrimSpace(input.OwnerUserID) ||
		receipt.OperationKind != OperationDeleteRequiredWorkspace || receipt.InputDigest != inputDigest ||
		receipt.TargetWorkspaceID != strings.TrimSpace(input.WorkspaceID) ||
		receipt.DeleteSessions != input.DeleteSessions || receipt.TrashWorkspace != input.TrashWorkspace {
		return DeletionClaim{}, ErrReviewStale
	}
	if receipt.ConsumedAt != nil || !s.now().Before(receipt.ExpiresAt) {
		return DeletionClaim{}, ErrReviewStale
	}
	_, snapshot, err := s.requiredDeletionCandidate(input)
	if err != nil {
		return DeletionClaim{}, ErrReviewStale
	}
	contractSnapshotDigest, digestErr := DigestInput(snapshot)
	if digestErr != nil || contractSnapshotDigest != receipt.ContractSnapshotDigest ||
		snapshot.TemplateID != receipt.TemplateID || snapshot.TemplateRevision != receipt.TemplateRevision ||
		snapshot.DefinitionDigest != receipt.DefinitionDigest || snapshot.OperationDigest != receipt.ContractOperationDigest {
		return DeletionClaim{}, ErrReviewStale
	}
	if err := s.receipts.ConsumeReceipt(ctx, receipt.Token, s.now()); err != nil {
		if errors.Is(err, ErrReviewStale) || errors.Is(err, ErrNotFound) {
			return DeletionClaim{}, ErrReviewStale
		}
		return DeletionClaim{}, ErrUnavailable
	}
	return DeletionClaim{
		WorkspaceID: receipt.TargetWorkspaceID, ContractOperationDigest: receipt.ContractOperationDigest,
		DeleteSessions: receipt.DeleteSessions, TrashWorkspace: receipt.TrashWorkspace,
	}, nil
}

func (s *Service) requiredDeletionCandidate(input DeletionInput) (*workspace.Workspace, *workspace.GroupRequirementSnapshot, error) {
	if s == nil || s.workspaces == nil || s.receipts == nil || strings.TrimSpace(input.OwnerUserID) == "" || strings.TrimSpace(input.WorkspaceID) == "" {
		return nil, nil, ErrUnavailable
	}
	candidate, err := canonicalGroupRequirementWorkspace(s.workspaces, strings.TrimSpace(input.WorkspaceID))
	if err != nil || candidate == nil || candidate.OwnerUserID != strings.TrimSpace(input.OwnerUserID) ||
		candidate.Status == workspace.StatusTrashed || candidate.Status == workspace.StatusMissing {
		return nil, nil, ErrUnavailable
	}
	provenance := candidate.GetTemplateProvenance()
	if provenance == nil || provenance.GroupRequirement == nil ||
		!strings.EqualFold(strings.TrimSpace(provenance.GroupRequirement.Policy), "required") {
		return nil, nil, ErrLifecycleNotRequired
	}
	snapshot := provenance.GroupRequirement
	if !snapshot.StructurallyValid() {
		return nil, nil, ErrLifecycleConflict
	}
	if candidate.GetAssistantProjectLink() != nil || candidate.GetAssistantProgramState() != nil {
		return nil, nil, ErrLifecycleConflict
	}
	if home, homeErr := s.workspaces.Get(snapshot.HomeWorkspaceID); homeErr == nil && home != nil {
		if state := home.GetAssistantProgramState(); state != nil {
			for _, projectID := range state.LinkedProjectIDs {
				if projectID == candidate.ID {
					return nil, nil, ErrLifecycleConflict
				}
			}
		}
	}
	return candidate, snapshot, nil
}

func canonicalGroupRequirementWorkspace(store workspace.Store, id string) (*workspace.Workspace, error) {
	if reader, ok := store.(interface {
		GetFolderWorkspace(string) (*workspace.Workspace, error)
	}); ok {
		return reader.GetFolderWorkspace(id)
	}
	return store.Get(id)
}

func deletionInputDigest(input DeletionInput) (string, error) {
	return DigestInput(struct {
		OwnerUserID    string `json:"owner_user_id"`
		WorkspaceID    string `json:"workspace_id"`
		DeleteSessions bool   `json:"delete_sessions"`
		TrashWorkspace bool   `json:"trash_workspace"`
	}{
		OwnerUserID: strings.TrimSpace(input.OwnerUserID), WorkspaceID: strings.TrimSpace(input.WorkspaceID),
		DeleteSessions: input.DeleteSessions, TrashWorkspace: input.TrashWorkspace,
	})
}

func (s *Service) Claim(ctx context.Context, input Input, token, idempotencyKey string) (Claim, error) {
	if s == nil || s.workspaces == nil || s.receipts == nil {
		return Claim{}, ErrUnavailable
	}
	token, idempotencyKey = strings.TrimSpace(token), strings.TrimSpace(idempotencyKey)
	if token == "" || idempotencyKey == "" || len(idempotencyKey) > 200 {
		return Claim{}, ErrReviewRequired
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	receipt, err := s.receipts.GetReceipt(ctx, token)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Claim{}, ErrReviewRequired
		}
		return Claim{}, ErrUnavailable
	}
	if receipt.OwnerUserID != strings.TrimSpace(input.OwnerUserID) || receipt.OperationKind != input.OperationKind ||
		receipt.InputDigest != strings.ToLower(strings.TrimSpace(input.InputDigest)) {
		return Claim{}, ErrReviewStale
	}
	expired := !s.now().Before(receipt.ExpiresAt)
	// A committed Home-only operation stays replayable after its review expires:
	// a lost response must never force a second confirmation or another create.
	// Project-bearing operations keep refusing expired reviews outright.
	if expired && receipt.OperationKind != OperationPrepareHome {
		return Claim{}, ErrReviewStale
	}
	if existing, getErr := s.receipts.GetOperation(ctx, receipt.OwnerUserID, receipt.OperationKind, idempotencyKey); getErr == nil {
		if existing.InputDigest != receipt.InputDigest || existing.ReviewDigest != receipt.ReviewDigest {
			return Claim{}, ErrOperationConflict
		}
		current := s.Evaluate(input)
		if err := receiptMatches(receipt, input, current); err != nil {
			return Claim{}, err
		}
		var snapshot *workspace.GroupRequirementSnapshot
		if receipt.OperationKind != OperationPrepareHome {
			snapshot = snapshotFor(receipt, existing, input.Template)
		}
		return Claim{Operation: existing, EffectiveTemplate: current.EffectiveTemplate, Snapshot: snapshot, Replayed: true}, nil
	} else if !errors.Is(getErr, ErrNotFound) {
		return Claim{}, ErrUnavailable
	}
	if expired || receipt.ConsumedAt != nil {
		return Claim{}, ErrReviewStale
	}

	current := s.Evaluate(input)
	if err := receiptMatches(receipt, input, current); err != nil {
		return Claim{}, err
	}
	operation := Operation{
		OwnerUserID: receipt.OwnerUserID, OperationKind: receipt.OperationKind, IdempotencyKey: idempotencyKey,
		InputDigest: receipt.InputDigest, ReviewDigest: receipt.ReviewDigest, Status: OperationClaimed,
		ChildWorkspaceID: receipt.TargetWorkspaceID,
		AppliedAt:        s.now(), UpdatedAt: s.now(),
	}
	if operation.ChildWorkspaceID == "" && receipt.OperationKind != OperationPrepareHome {
		operation.ChildWorkspaceID = deterministicChildID(receipt.OwnerUserID, receipt.OperationKind, idempotencyKey)
	}
	homeCreated := false
	if receipt.Composition == CompositionGrouped {
		key := receipt.ProgramKey.Normalize()
		homeID := receipt.HomeWorkspaceID
		if receipt.CreateHome {
			var home *workspace.Workspace
			var ensureErr error
			programs := workspace.NewAssistantProgramStore(s.workspaces)
			if receipt.GroupTemplateID != "" {
				home, homeCreated, ensureErr = programs.EnsureNamedStationWithProvenance(key, input.Template.AssistantProgram, receipt.DefaultHomeName, groupTemplateProvenance(receipt, key, input.Template))
			} else {
				home, homeCreated, ensureErr = programs.EnsureNamedStation(key, input.Template.AssistantProgram, receipt.DefaultHomeName)
			}
			if ensureErr != nil || home == nil {
				return Claim{}, ErrUnavailable
			}
			homeID = home.ID
		}
		if homeID == "" {
			return Claim{}, ErrReviewStale
		}
		operation.HomeWorkspaceID = homeID
		if receipt.OperationKind != OperationPrepareHome {
			operation.ProjectLinkID = workspace.AssistantProjectLinkID(homeID, operation.ChildWorkspaceID)
		}
		operation.Status = OperationHomeReady
	}
	operation.OperationDigest = operationDigest(operation)
	if err := s.receipts.SaveOperation(ctx, operation); err != nil {
		return Claim{}, ErrUnavailable
	}
	consumedAt := s.now()
	if err := s.receipts.ConsumeReceipt(ctx, receipt.Token, consumedAt); err != nil {
		return Claim{}, ErrUnavailable
	}
	var snapshot *workspace.GroupRequirementSnapshot
	if receipt.OperationKind != OperationPrepareHome {
		snapshot = snapshotFor(receipt, operation, input.Template)
	}
	return Claim{Operation: operation, EffectiveTemplate: current.EffectiveTemplate, Snapshot: snapshot, HomeCreated: homeCreated}, nil
}

// CommitReviewed applies a review boundary owned by another durable host
// workflow (currently setup journeys). The caller supplies only digests and a
// deterministic child ID; this method re-evaluates canonical owner/Home state
// and returns the same snapshot shape as the native receipt path.
func (s *Service) CommitReviewed(input Input, childWorkspaceID, reviewDigest, operationDigest string) (projecttemplates.Template, string, *workspace.GroupRequirementSnapshot, error) {
	if s == nil || s.workspaces == nil || strings.TrimSpace(childWorkspaceID) == "" ||
		!validDigest(reviewDigest) || !validDigest(operationDigest) {
		return projecttemplates.Template{}, "", nil, ErrUnavailable
	}
	evaluation := s.Evaluate(input)
	if evaluation.State == StateLegacy {
		return input.Template, "", nil, nil
	}
	if evaluation.State != StateReadyGrouped && evaluation.State != StateReadyStandalone {
		return projecttemplates.Template{}, "", nil, ErrReviewStale
	}
	// Setup journeys prepare and acknowledge their Home in a distinct action.
	// CommitReviewed must therefore observe an existing destination and may not
	// silently restore the old create-Home-and-project behavior.
	if evaluation.SelectedComposition == CompositionGrouped && evaluation.HomeWillBeCreated {
		return projecttemplates.Template{}, "", nil, ErrReviewStale
	}
	homeID := evaluation.HomeWorkspaceID
	receipt := Receipt{
		OwnerUserID: strings.TrimSpace(input.OwnerUserID), OperationKind: input.OperationKind,
		InputDigest: input.InputDigest, TemplateID: input.Template.ID, TemplateRevision: input.Template.Revision,
		DefinitionDigest: evaluation.DefinitionDigest, Policy: evaluation.Policy, Composition: evaluation.SelectedComposition,
		HomeWorkspaceID: evaluation.HomeWorkspaceID, CreateHome: evaluation.HomeWillBeCreated,
		DefaultHomeName: evaluation.HomeName, ReviewDigest: reviewDigest,
	}
	if input.Template.TemplateVariant != nil {
		receipt.VariantID = input.Template.TemplateVariant.VariantID
		receipt.VariantRevision = input.Template.VariantRevision
	}
	if evaluation.ProgramKey != nil {
		key := evaluation.ProgramKey.Normalize()
		receipt.ProgramKey = &key
	}
	operation := Operation{
		OwnerUserID: receipt.OwnerUserID, OperationKind: input.OperationKind, InputDigest: input.InputDigest,
		OperationDigest: operationDigest, ReviewDigest: reviewDigest, ChildWorkspaceID: childWorkspaceID,
		HomeWorkspaceID: homeID, AppliedAt: s.now(), UpdatedAt: s.now(),
	}
	if homeID != "" {
		operation.ProjectLinkID = workspace.AssistantProjectLinkID(homeID, childWorkspaceID)
	}
	return evaluation.EffectiveTemplate, homeID, snapshotFor(receipt, operation, input.Template), nil
}

func (s *Service) Mark(ctx context.Context, operation Operation, status OperationStatus) error {
	if s == nil || s.receipts == nil {
		return ErrUnavailable
	}
	switch status {
	case OperationChildReady, OperationLinkReady, OperationSucceeded, OperationReconcileRequired:
	default:
		return ErrUnavailable
	}
	operation.Status = status
	operation.UpdatedAt = s.now()
	return s.receipts.SaveOperation(ctx, operation)
}

func receiptMatches(receipt Receipt, input Input, current Evaluation) error {
	template := input.Template
	if current.State != StateReadyGrouped && current.State != StateReadyStandalone {
		return ErrReviewStale
	}
	if receipt.GroupTemplateID != strings.TrimSpace(input.GroupTemplateID) || receipt.GroupTemplateRev != strings.TrimSpace(input.GroupTemplateRevision) {
		return ErrReviewStale
	}
	if receipt.TemplateID != template.ID || receipt.TemplateRevision != template.Revision ||
		receipt.DefinitionDigest != current.DefinitionDigest || receipt.Policy != current.Policy ||
		receipt.Composition != current.SelectedComposition {
		return ErrReviewStale
	}
	variantID := ""
	if template.TemplateVariant != nil {
		variantID = template.TemplateVariant.VariantID
	}
	if receipt.VariantID != variantID || receipt.VariantRevision != template.VariantRevision {
		return ErrReviewStale
	}
	if receipt.ProgramKey != nil {
		if current.ProgramKey == nil || receipt.ProgramKey.Normalize() != current.ProgramKey.Normalize() {
			return ErrReviewStale
		}
		if receipt.HomeWorkspaceID != "" && current.HomeWorkspaceID != receipt.HomeWorkspaceID {
			return ErrReviewStale
		}
		if receipt.CreateHome && current.HomeWorkspaceID != "" {
			// A concurrently-created Home with the exact stable key may satisfy a
			// reviewed create intent. Its mutable display name need not match.
			return nil
		}
	}
	return nil
}

func snapshotFor(receipt Receipt, operation Operation, template projecttemplates.Template) *workspace.GroupRequirementSnapshot {
	snapshot := &workspace.GroupRequirementSnapshot{
		SchemaVersion: workspace.GroupRequirementSnapshotSchemaVersion, Policy: string(receipt.Policy),
		SelectedComposition: receipt.Composition, TemplateID: receipt.TemplateID, TemplateRevision: receipt.TemplateRevision,
		DefinitionDigest: receipt.DefinitionDigest, VariantID: receipt.VariantID, VariantRevision: receipt.VariantRevision,
		ReviewDigest: receipt.ReviewDigest, OperationDigest: operation.OperationDigest, AppliedAt: operation.AppliedAt,
	}
	if template.TemplateVariant != nil {
		owner := workspace.PluginTemplateOwner{
			PluginID: template.TemplateVariant.Source.PluginID, PluginVersion: template.TemplateVariant.Source.PluginVersion,
			BlueprintID: template.TemplateVariant.Source.BlueprintID, BlueprintVersion: template.TemplateVariant.Source.BlueprintVersion,
		}
		snapshot.SourcePlugin = &owner
		snapshot.SourceDigest = template.TemplateVariant.Source.DefinitionDigest
	}
	if receipt.Composition == CompositionStandalone && template.StandaloneComposition != nil {
		for _, role := range template.StandaloneComposition.ProjectRoles {
			snapshot.StandaloneRoles = append(snapshot.StandaloneRoles, workspace.GroupRequirementRoleSource{RoleID: role.RoleID})
		}
	}
	if receipt.ProgramKey != nil {
		key := receipt.ProgramKey.Normalize()
		snapshot.ProgramKey = &key
		snapshot.HomeWorkspaceID = operation.HomeWorkspaceID
		snapshot.ProjectLinkID = operation.ProjectLinkID
	}
	return snapshot
}

// groupTemplateProvenance derives inert creation provenance only from the
// consumed-once receipt, the server-resolved key, and the trusted template.
func groupTemplateProvenance(receipt Receipt, key workspace.AssistantProgramKey, template projecttemplates.Template) *workspace.AssistantGroupTemplateProvenance {
	provenance := &workspace.AssistantGroupTemplateProvenance{
		SchemaVersion:   workspace.AssistantGroupTemplateProvenanceSchemaVersion,
		GroupTemplateID: receipt.GroupTemplateID, GroupTemplateRevision: receipt.GroupTemplateRev,
		SourceKind: string(projecttemplates.GroupTemplateSourceUserTemplate), TemplateID: receipt.TemplateID,
		TemplateRevision: receipt.TemplateRevision, VariantID: receipt.VariantID, VariantRevision: receipt.VariantRevision,
		HomeDigest: projecttemplates.GroupTemplateHomeDigest(template.AssistantProgram), ReviewDigest: receipt.ReviewDigest,
	}
	if key.Normalize().PluginID == "" {
		return provenance
	}
	provenance.SourceKind = string(projecttemplates.GroupTemplateSourcePlugin)
	switch {
	case template.PluginOwner != nil:
		owner := template.PluginOwner.Clone()
		provenance.PluginOwner = &owner
	case template.TemplateVariant != nil:
		source := template.TemplateVariant.Source
		provenance.PluginOwner = &workspace.PluginTemplateOwner{
			PluginID: source.PluginID, PluginVersion: source.PluginVersion, BlueprintID: source.BlueprintID, BlueprintVersion: source.BlueprintVersion,
		}
	}
	return provenance
}

func programKey(ownerUserID string, template projecttemplates.Template) (workspace.AssistantProgramKey, error) {
	if template.AssistantProgram == nil || template.GroupRequirement == nil ||
		template.GroupRequirement.AssistantProgramID != strings.ToLower(strings.TrimSpace(template.AssistantProgram.ID)) {
		return workspace.AssistantProgramKey{}, errors.New("program is unavailable")
	}
	key := workspace.AssistantProgramKey{OwnerUserID: strings.TrimSpace(ownerUserID), ProgramID: template.AssistantProgram.ID}
	switch {
	case template.PluginOwner != nil:
		key.PluginID = template.PluginOwner.PluginID
	case template.TemplateVariant != nil:
		key.PluginID = template.TemplateVariant.Source.PluginID
	case template.UserSetupQuest != nil:
		key.TemplateID = template.ID
		key.AttachmentID = template.UserSetupQuest.AttachmentID
	default:
		return workspace.AssistantProgramKey{}, errors.New("program owner is unavailable")
	}
	key = key.Normalize()
	if !key.Valid() {
		return workspace.AssistantProgramKey{}, errors.New("program owner is unavailable")
	}
	return key, nil
}

func compatibleHome(home *workspace.Workspace, key workspace.AssistantProgramKey, declaration *workspace.AssistantProgramDeclaration) bool {
	if home == nil || home.Kind != "group" || home.Status == workspace.StatusTrashed || home.Status == workspace.StatusMissing || declaration == nil {
		return false
	}
	state := home.GetAssistantProgramState()
	return state != nil && state.SchemaVersion == workspace.AssistantProgramStateSchemaVersion &&
		state.Key.Normalize() == key.Normalize() && state.Declaration != nil &&
		state.Declaration.SchemaVersion == declaration.SchemaVersion && state.Declaration.ID == declaration.ID
}

func standaloneTemplate(template projecttemplates.Template) (projecttemplates.Template, error) {
	if template.AssistantProgram == nil {
		result := template
		result.SetupQuestID = ""
		result.UserSetupQuest = nil
		result.UserSetupQuestError = ""
		result.UserSetupQuestRevision = ""
		return result, nil
	}
	return projecttemplates.StandaloneTemplate(template)
}

func unavailable(state State, summary string, actions ...Action) Evaluation {
	if len(actions) > 4 {
		actions = actions[:4]
	}
	return Evaluation{State: state, Summary: summary, Actions: actions}
}

func definitionDigest(template projecttemplates.Template) string {
	skeletonDigest := ""
	if template.TemplateVariant != nil {
		skeletonDigest = template.TemplateVariant.Source.DefinitionDigest
	}
	return projecttemplates.TemplateDefinitionDigest(template, skeletonDigest)
}

func receiptDigest(receipt Receipt) string {
	copy := receipt
	copy.Token = ""
	copy.ReviewDigest = ""
	copy.ConsumedAt = nil
	return digestJSON(copy)
}

func operationDigest(operation Operation) string {
	copy := operation
	copy.OperationDigest = ""
	copy.Status = ""
	copy.UpdatedAt = time.Time{}
	return digestJSON(copy)
}

func digestJSON(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func DigestInput(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func deterministicChildID(owner string, kind OperationKind, key string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(strings.TrimSpace(owner)+"\x00"+string(kind)+"\x00"+key)).String()
}

func validOperationKind(kind OperationKind) bool {
	return kind == OperationPrepareHome || kind == OperationCreateWorkspace || kind == OperationCreateProject || kind == OperationConnectProject
}

func validDigest(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

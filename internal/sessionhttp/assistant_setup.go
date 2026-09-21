package sessionhttp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/store"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// ReviewedTemplateRole is a bounded projection of the current effective role
// plan. It contains no prompt; ConfigDigest binds the complete effective
// definition that the strict creator will re-resolve at commit.
type ReviewedTemplateRole struct {
	RoleID          string
	Name            string
	Action          string
	Provider        string
	Model           string
	ModelConfigured bool
	ConfigDigest    string
	Warning         string
}

type ReviewedTemplateCreationPlan struct {
	BlueprintID      string
	BlueprintVersion int
	BlueprintDigest  string
	PlanRevision     string
	Roles            []ReviewedTemplateRole
}

type AssistantSetupCreationDescriptor struct {
	OwnerUserID         string
	RunID               string
	OperationID         string
	ReviewDigest        string
	WorkspaceID         string
	ProfileProvenanceID string
	ConfigDigest        string
}

type ReviewedTemplateCreationRequest struct {
	Name       string
	Plan       ReviewedTemplateCreationPlan
	Descriptor AssistantSetupCreationDescriptor
}

type ReviewedTemplateCreationResult struct {
	WorkspaceID         string
	AgentInstanceID     string
	ProfileProvenanceID string
	ProfileStoreOrigin  string
	ProfileCreated      bool
	ConfigurationDigest string
}

// Typed refusals the reviewed creator returns before it writes anything. The
// assistant setup coordinator relies on them to call a refusal Failed rather
// than unproven, so they must never be returned after a write.
var (
	// ErrReviewedPlanChanged: the current effective team plan differs from the
	// one the user reviewed.
	ErrReviewedPlanChanged = errors.New("file janitor team plan changed")
	// ErrReviewedPlanUnsupported: the current built-in no longer resolves into a
	// supported one-role reviewed plan.
	ErrReviewedPlanUnsupported = errors.New("file janitor team plan is unavailable or unsupported")
)

// ReviewedFileJanitorObservation is read-only evidence of what one creation
// claim left behind. It is keyed by the reserved workspace ID and this
// operation's exact markers; a same-name agent without them is unrelated.
type ReviewedFileJanitorObservation struct {
	WorkspacePresent    bool
	WorkspaceProven     bool
	AgentInstanceID     string
	ProfileCreated      bool
	ProfileProvenanceID string
	ProfileStoreOrigin  string
}

type assistantSetupCreationContextKey struct{}

func withAssistantSetupCreation(ctx context.Context, descriptor AssistantSetupCreationDescriptor) context.Context {
	return context.WithValue(ctx, assistantSetupCreationContextKey{}, descriptor)
}

func assistantSetupCreationFromContext(ctx context.Context) (AssistantSetupCreationDescriptor, bool) {
	if ctx == nil {
		return AssistantSetupCreationDescriptor{}, false
	}
	descriptor, ok := ctx.Value(assistantSetupCreationContextKey{}).(AssistantSetupCreationDescriptor)
	if !ok || strings.TrimSpace(descriptor.RunID) == "" || strings.TrimSpace(descriptor.OperationID) == "" {
		return AssistantSetupCreationDescriptor{}, false
	}
	return descriptor, true
}

// ReviewFileJanitorCreation resolves the current built-in and builds the same
// effective team plan the strict workspace creator validates. It performs no
// writes and creates no workspace, agent, task, grant, watcher, or scan.
func (h *Handler) ReviewFileJanitorCreation(_ context.Context) (ReviewedTemplateCreationPlan, error) {
	if h == nil || h.agentStore == nil {
		return ReviewedTemplateCreationPlan{}, errors.New("template agent storage is unavailable")
	}
	tpl, err := h.resolveProjectTemplate("file-janitor", "")
	if err != nil {
		return ReviewedTemplateCreationPlan{}, fmt.Errorf("resolve File Janitor blueprint: %w", err)
	}
	if !tpl.Builtin || tpl.ID != "file-janitor" || tpl.BuiltinVersion < 1 || len(tpl.Agents) != 1 {
		return ReviewedTemplateCreationPlan{}, errors.New("file janitor blueprint is unavailable or unsupported")
	}
	plan := h.buildTemplateAgentPlan(tpl)
	if plan.Revision == "" || len(plan.Agents) != 1 {
		return ReviewedTemplateCreationPlan{}, ErrReviewedPlanUnsupported
	}
	roleIDs := projecttemplates.AgentRoleIDs(tpl.Agents)
	item := plan.Agents[0]
	if item.Action != "create" && item.Action != "reuse" {
		return ReviewedTemplateCreationPlan{}, fmt.Errorf("%w: unsupported role action", ErrReviewedPlanUnsupported)
	}
	if item.Action == "create" {
		if statusReader, ok := h.agentStore.(interface{ RootStatus() store.AgentRootStatus }); ok && !statusReader.RootStatus().Available {
			return ReviewedTemplateCreationPlan{}, store.ErrAgentRootUnavailable
		}
	} else if originReader, ok := h.agentStore.(interface {
		AgentOrigin(string) (store.AgentOrigin, bool)
	}); ok {
		origin, found := originReader.AgentOrigin(item.Name)
		if !found || origin.Source != store.SourceRoster {
			return ReviewedTemplateCreationPlan{}, fmt.Errorf("agent %q is not an owned root profile", item.Name)
		}
	}
	blueprintDigest, err := digestJSON(tpl)
	if err != nil {
		return ReviewedTemplateCreationPlan{}, err
	}
	configDigest, err := digestTemplateAgent(tpl.Agents[0], item)
	if err != nil {
		return ReviewedTemplateCreationPlan{}, err
	}
	return ReviewedTemplateCreationPlan{
		BlueprintID: tpl.ID, BlueprintVersion: tpl.BuiltinVersion,
		BlueprintDigest: blueprintDigest, PlanRevision: plan.Revision,
		Roles: []ReviewedTemplateRole{{
			RoleID: roleIDs[0], Name: item.Name, Action: item.Action,
			Provider: item.Provider, Model: item.Model,
			ModelConfigured: strings.TrimSpace(item.Model) != "", ConfigDigest: configDigest,
			Warning: item.Warning,
		}},
	}, nil
}

func digestJSON(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func digestTemplateAgent(spec projecttemplates.AgentSpec, item templateAgentPlanItem) (string, error) {
	return digestJSON(struct {
		Spec                                          projecttemplates.AgentSpec
		Provider, Model, ReasoningEffort, ModelSource string
	}{spec, item.Provider, item.Model, item.ReasoningEffort, item.ModelSource})
}

// CreateReviewedFileJanitor re-resolves and compares the exact server plan,
// then enters the normal production creation pipeline through strict
// team_intent/role_staffing. The operation descriptor is server-only context;
// POST /api/workspaces cannot supply it.
func (h *Handler) CreateReviewedFileJanitor(ctx context.Context, request ReviewedTemplateCreationRequest) (ReviewedTemplateCreationResult, error) {
	if err := validateAssistantSetupCreationRequest(request); err != nil {
		return ReviewedTemplateCreationResult{}, err
	}
	// Reconcile the exact preallocated ID before rebuilding a plan. A successful
	// create necessarily changes its role action from create to reuse, so the
	// immutable operation/profile/workspace markers — not a newly derived name
	// match — are the replay proof.
	if h.workspaceTaskStore != nil {
		if existing, _ := h.workspaceTaskStore.Get(request.Descriptor.WorkspaceID); existing != nil {
			return h.observeReviewedFileJanitorCreation(request)
		}
	}
	fresh, err := h.ReviewFileJanitorCreation(ctx)
	if err != nil {
		return ReviewedTemplateCreationResult{}, err
	}
	if !reviewedTemplatePlansEqual(fresh, request.Plan) {
		return ReviewedTemplateCreationResult{}, ErrReviewedPlanChanged
	}
	role := fresh.Roles[0]
	mode := roleStaffingModeCreate
	if role.Action == "reuse" {
		mode = roleStaffingModeAssign
	}
	intent, err := json.Marshal(workspaceTeamIntent{
		Version: workspaceTeamIntentVersion, Mode: workspaceTeamModeStaffed, PlanRevision: fresh.PlanRevision,
	})
	if err != nil {
		return ReviewedTemplateCreationResult{}, err
	}
	body, err := json.Marshal(createWorkspaceRequest{
		Name: request.Name, TemplateID: fresh.BlueprintID, TeamIntent: intent,
		teamIntentPresent: true, roleStaffingPresent: true,
		RoleStaffing: []roleStaffingInput{{RoleID: role.RoleID, Mode: mode, Name: role.Name}},
	})
	if err != nil {
		return ReviewedTemplateCreationResult{}, err
	}
	requestCtx := withAssistantSetupCreation(ctx, request.Descriptor)
	httpRequest, err := http.NewRequestWithContext(requestCtx, http.MethodPost, "/api/workspaces", bytes.NewReader(body))
	if err != nil {
		return ReviewedTemplateCreationResult{}, err
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	h.HandleWorkspaces(recorder, httpRequest)
	if recorder.Code != http.StatusCreated {
		return ReviewedTemplateCreationResult{}, fmt.Errorf("reviewed workspace creation failed (%d)", recorder.Code)
	}
	return h.observeReviewedFileJanitorCreation(request)
}

func validateAssistantSetupCreationRequest(request ReviewedTemplateCreationRequest) error {
	d := request.Descriptor
	if strings.TrimSpace(request.Name) == "" || strings.TrimSpace(d.OwnerUserID) == "" ||
		strings.TrimSpace(d.RunID) == "" || strings.TrimSpace(d.OperationID) == "" ||
		strings.TrimSpace(d.ReviewDigest) == "" || strings.TrimSpace(d.ProfileProvenanceID) == "" ||
		strings.TrimSpace(d.ConfigDigest) == "" {
		return errors.New("assistant setup creation descriptor is incomplete")
	}
	if _, err := uuid.Parse(d.WorkspaceID); err != nil {
		return errors.New("assistant setup workspace id is invalid")
	}
	if request.Plan.BlueprintID != "file-janitor" || len(request.Plan.Roles) != 1 {
		return errors.New("assistant setup plan is invalid")
	}
	return nil
}

func reviewedTemplatePlansEqual(left, right ReviewedTemplateCreationPlan) bool {
	if left.BlueprintID != right.BlueprintID || left.BlueprintVersion != right.BlueprintVersion ||
		left.BlueprintDigest != right.BlueprintDigest || left.PlanRevision != right.PlanRevision ||
		len(left.Roles) != len(right.Roles) {
		return false
	}
	for index := range left.Roles {
		if left.Roles[index] != right.Roles[index] {
			return false
		}
	}
	return true
}

func (h *Handler) readReviewedWorkspace(id string) (*agentworkspace.Workspace, error) {
	if folders, ok := h.workspaceTaskStore.(interface {
		GetFolderWorkspace(string) (*agentworkspace.Workspace, error)
	}); ok {
		return folders.GetFolderWorkspace(id)
	}
	return h.workspaceTaskStore.Get(id)
}

// workspaceMatchesClaim reports a workspace that carries this exact
// operation's assistant-setup provenance and the File Janitor capability.
func workspaceMatchesClaim(ws *agentworkspace.Workspace, descriptor AssistantSetupCreationDescriptor) bool {
	if ws == nil {
		return false
	}
	provenance := ws.GetTemplateProvenance()
	return provenance != nil && provenance.AssistantSetup != nil &&
		provenance.AssistantSetup.RunID == descriptor.RunID &&
		provenance.AssistantSetup.OperationID == descriptor.OperationID &&
		provenance.AssistantSetup.ReviewDigest == descriptor.ReviewDigest &&
		ws.HasInstalledCapability(agentworkspace.CapabilityFileJanitor)
}

func attachedRoleInstance(ws *agentworkspace.Workspace, roleID string) string {
	for _, instance := range ws.AgentInstances {
		if instance.RoleID == roleID {
			return instance.ID
		}
	}
	return ""
}

// profileMatchesClaim reports a profile stamped by this exact operation with
// the reviewed configuration. Name equality alone proves nothing.
func (h *Handler) profileMatchesClaim(role ReviewedTemplateRole, descriptor AssistantSetupCreationDescriptor) (*agent.Agent, bool) {
	profile, found := h.agentStore.GetAgent(role.Name)
	if !found || profile == nil || profile.AssistantSetup == nil ||
		profile.AssistantSetup.ID != descriptor.ProfileProvenanceID ||
		profile.AssistantSetup.OperationID != descriptor.OperationID ||
		profile.AssistantSetup.ConfigDigest != role.ConfigDigest {
		return nil, false
	}
	return profile, true
}

func (h *Handler) observeReviewedFileJanitorCreation(request ReviewedTemplateCreationRequest) (ReviewedTemplateCreationResult, error) {
	if h == nil || h.workspaceTaskStore == nil || h.agentStore == nil {
		return ReviewedTemplateCreationResult{}, errors.New("reviewed workspace storage is unavailable")
	}
	ws, err := h.readReviewedWorkspace(request.Descriptor.WorkspaceID)
	if err != nil || ws == nil {
		return ReviewedTemplateCreationResult{}, errors.New("reviewed workspace result is unavailable")
	}
	if !workspaceMatchesClaim(ws, request.Descriptor) {
		return ReviewedTemplateCreationResult{}, errors.New("reviewed workspace provenance is incomplete")
	}
	role := request.Plan.Roles[0]
	instanceID := attachedRoleInstance(ws, role.RoleID)
	if instanceID == "" {
		return ReviewedTemplateCreationResult{}, errors.New("reviewed workspace agent attachment is unavailable")
	}
	result := ReviewedTemplateCreationResult{
		WorkspaceID: request.Descriptor.WorkspaceID, AgentInstanceID: instanceID,
		ProfileCreated: role.Action == "create", ConfigurationDigest: role.ConfigDigest,
	}
	if role.Action == "create" {
		profile, matched := h.profileMatchesClaim(role, request.Descriptor)
		if !matched {
			return ReviewedTemplateCreationResult{}, errors.New("reviewed agent provenance is incomplete")
		}
		result.ProfileProvenanceID = profile.AssistantSetup.ID
		result.ProfileStoreOrigin = store.SourceRoster
	}
	return result, nil
}

// ObserveReviewedFileJanitor reports, without writing, what one creation claim
// left behind: whether anything sits at the reserved workspace ID, whether it
// is this operation's complete workspace, the reviewed role's attached
// instance, and a profile carrying this operation's markers. A workspace that
// is listed but unreadable is an error, never "absent".
func (h *Handler) ObserveReviewedFileJanitor(request ReviewedTemplateCreationRequest) (ReviewedFileJanitorObservation, error) {
	var observation ReviewedFileJanitorObservation
	if h == nil || h.workspaceTaskStore == nil || h.agentStore == nil {
		return observation, errors.New("reviewed workspace storage is unavailable")
	}
	if err := validateAssistantSetupCreationRequest(request); err != nil {
		return observation, err
	}
	role := request.Plan.Roles[0]
	if role.Action == "create" {
		if profile, matched := h.profileMatchesClaim(role, request.Descriptor); matched {
			observation.ProfileCreated = true
			observation.ProfileProvenanceID = profile.AssistantSetup.ID
			observation.ProfileStoreOrigin = store.SourceRoster
		}
	}
	ids, err := h.workspaceTaskStore.List()
	if err != nil {
		return ReviewedFileJanitorObservation{}, fmt.Errorf("list workspaces: %w", err)
	}
	if !slices.Contains(ids, request.Descriptor.WorkspaceID) {
		return observation, nil
	}
	observation.WorkspacePresent = true
	ws, err := h.readReviewedWorkspace(request.Descriptor.WorkspaceID)
	if err != nil || ws == nil {
		return ReviewedFileJanitorObservation{}, errors.New("reviewed workspace is present but unreadable")
	}
	if workspaceMatchesClaim(ws, request.Descriptor) {
		observation.WorkspaceProven = true
		observation.AgentInstanceID = attachedRoleInstance(ws, role.RoleID)
	}
	return observation, nil
}

func assistantSetupAgentProvenance(ctx context.Context, name string) *agent.AssistantSetupProvenance {
	descriptor, ok := assistantSetupCreationFromContext(ctx)
	if !ok {
		return nil
	}
	_ = name // The marker is bound by the reviewed config digest, not a display name.
	return &agent.AssistantSetupProvenance{
		ID: descriptor.ProfileProvenanceID, RunID: descriptor.RunID,
		OperationID: descriptor.OperationID, ReviewDigest: descriptor.ReviewDigest,
		ConfigDigest: descriptor.ConfigDigest,
	}
}

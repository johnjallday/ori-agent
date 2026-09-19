package sessionhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/grouprequirements"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/personalhq"
	"github.com/johnjallday/ori-agent/internal/platform"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/types"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacecapability"
	"github.com/johnjallday/ori-agent/internal/workspacesettings"
)

var errParentWorkspaceMustBeGroup = errors.New("parent workspace must be a group")

// Sentinel errors renameWorkspace wraps so callers can map a non-conflict
// failure to the same 500 body handleWorkspaceRename has always returned,
// without the helper writing the HTTP response itself.
var (
	errWorkspaceRenameSQLite   = errors.New("failed to rename workspace")
	errWorkspaceRenameFolder   = errors.New("failed to rename workspace folder")
	errWorkspaceRenameRollback = errors.New("failed to rollback workspace rename")
)

// workspaceRenameConflictError signals a slug conflict encountered while
// renaming a workspace, whether the conflict surfaced at the SQLite layer
// (ErrWorkspaceSlugConflict) or the folder-store layer (FolderSlugConflictError).
// Both render the same 409 body, built from globalWorkspaceSlugConflict.
type workspaceRenameConflictError struct {
	targetSlug string
	parentDir  string
}

func (e *workspaceRenameConflictError) Error() string {
	return fmt.Sprintf("workspace slug %q is already in use", e.targetSlug)
}

// workspaceSharedDataPrimaryDirectoryIDKey mirrors projecttemplates.PrimaryDirectoryIDKey
// so this package and the workspace_create_project chat tool agree on the
// SharedData key used to record a workspace's primary linked directory.
const workspaceSharedDataPrimaryDirectoryIDKey = projecttemplates.PrimaryDirectoryIDKey

// workspaceTrashSharedDataKey is the SharedData key under which trash metadata
// ({original_path, trashed_path, deleted_at}) is stored while a workspace is
// trashed, so its folder can be moved back on restore. Shared with the
// workspace package, whose SyncStore.Trash writes the same record for a
// reviewed Home removal.
const workspaceTrashSharedDataKey = agentworkspace.TrashSharedDataKey

// HandleWorkspaces routes requests to /api/workspaces (also supports legacy /api/folders).
func (h *Handler) HandleWorkspaces(w http.ResponseWriter, r *http.Request) {
	// Normalize path for both /api/folders and /api/workspaces
	path := r.URL.Path
	path = strings.TrimPrefix(path, "/api/folders")
	path = strings.TrimPrefix(path, "/api/workspaces")
	path = strings.TrimPrefix(path, "/")

	// Import routes must be handled before generic workspace-id routing.
	switch path {
	case "import":
		h.handleWorkspaceImport(w, r)
		return
	case "import/check":
		h.handleWorkspaceImportCheck(w, r)
		return
	case "import/duplicate-action":
		h.handleWorkspaceImportDuplicateAction(w, r)
		return
	case "sync-status":
		h.handleWorkspaceSyncStatus(w, r)
		return
	case "sync":
		h.handleWorkspaceSync(w, r)
		return
	case "rescan":
		h.handleWorkspaceRescan(w, r)
		return
	case "template-agent-plan":
		h.handleTemplateAgentPlan(w, r)
		return
	case "template-agent-create":
		h.handleTemplateAgentCreate(w, r)
		return
	}

	// Handle sub-paths like {id}/agents, {id}/layout
	if path != "" && strings.Contains(path, "/") {
		parts := strings.SplitN(path, "/", 3)
		id := parts[0]
		subPath := parts[1]

		switch subPath {
		case "settings":
			h.handleWorkspaceSettings(w, r, id)
			return
		case "planning-policy":
			h.handleWorkspacePlanningPolicy(w, r, id)
			return
		case "agents":
			h.handleWorkspaceAgents(w, r, id, parts)
			return
		case "layout":
			h.handleWorkspaceLayout(w, r, id)
			return
		case "board":
			h.handleWorkspaceBoard(w, r, id)
			return
		case "project":
			h.handleWorkspaceProject(w, r, id)
			return
		case "rename":
			h.handleWorkspaceRename(w, r, id)
			return
		case "restore":
			h.restoreWorkspace(w, r, id)
			return
		case "template-setup":
			if len(parts) == 3 && parts[2] == "start" {
				h.handleTemplateSetupStart(w, r, id)
				return
			}
		}
	}

	if path != "" && !strings.Contains(path, "/") {
		// This is a request for a specific workspace
		h.handleWorkspace(w, r, path)
		return
	}

	// Handle collection-level requests
	switch r.Method {
	case http.MethodGet:
		h.listWorkspaces(w, r)
	case http.MethodPost:
		h.createWorkspace(w, r)
	default:
		_ = orihttp.RespondMethodNotAllowed(w)
	}
}

// handleWorkspace handles requests for a specific workspace.
func (h *Handler) handleWorkspace(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodGet:
		h.getWorkspace(w, r, id)
	case http.MethodPut:
		h.updateWorkspace(w, r, id)
	case http.MethodPatch:
		h.updateWorkspace(w, r, id)
	case http.MethodDelete:
		h.deleteWorkspace(w, r, id)
	default:
		_ = orihttp.RespondMethodNotAllowed(w)
	}
}

type workspaceBootstrapRequest struct {
	Goal         string `json:"goal,omitempty"`
	Systems      string `json:"systems,omitempty"`
	Capabilities string `json:"capabilities,omitempty"`
	Context      string `json:"context,omitempty"`
}

func normalizeWorkspaceBootstrap(input *workspaceBootstrapRequest) map[string]any {
	if input == nil {
		return nil
	}

	goal := strings.TrimSpace(input.Goal)
	systems := strings.TrimSpace(input.Systems)
	capabilities := strings.TrimSpace(input.Capabilities)
	contextValue := strings.TrimSpace(input.Context)
	if goal == "" && systems == "" && capabilities == "" && contextValue == "" {
		return nil
	}

	systemsList := splitWorkspaceBootstrapValues(systems)
	if systemsList == nil {
		systemsList = []string{}
	}

	return map[string]any{
		"version":      1,
		"goal":         goal,
		"systems":      systems,
		"capabilities": capabilities,
		"systems_list": systemsList,
		"context":      contextValue,
		"captured_at":  time.Now().UTC().Format(time.RFC3339),
	}
}

func splitWorkspaceBootstrapValues(raw string) []string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}

	parts := strings.FieldsFunc(trimmed, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r'
	})

	values := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		value := strings.TrimSpace(part)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		values = append(values, value)
	}
	return values
}

// createWorkspaceRequest is the JSON body of POST /api/workspaces.
type createWorkspaceRequest struct {
	Name            string `json:"name"`
	Kind            string `json:"kind,omitempty"`
	WorkspacePreset string `json:"workspace_preset,omitempty"`
	Description     string `json:"description,omitempty"`
	ParentID        string `json:"parent_id,omitempty"`
	OrderIndex      *int   `json:"order_index,omitempty"`
	Color           string `json:"color,omitempty"`
	ProjectPath     string `json:"project_path,omitempty"`
	FolderSlug      string `json:"folder_slug,omitempty"`
	Location        string `json:"location,omitempty"`         // Optional custom directory for workspace folder (overrides default root)
	EntryAgentName  string `json:"entry_agent_name,omitempty"` // Optional existing agent name; otherwise a workspace manager is created automatically
	// ExistingAgentNames is an optional ordered roster of saved definitions to
	// attach while the workspace is created. A nil slice preserves the legacy
	// entry-agent-only behavior; a present (including empty) slice opts into the
	// additive template-plus-existing composition contract.
	ExistingAgentNames   []string                   `json:"existing_agent_names,omitempty"`
	WorkspaceBootstrap   *workspaceBootstrapRequest `json:"workspace_bootstrap,omitempty"`
	TemplateID           string                     `json:"template_id,omitempty"`   // Optional project template from the library
	TemplatePath         string                     `json:"template_path,omitempty"` // Optional arbitrary folder used as a project template. NOT restricted to the templates library: resolveProjectTemplate/LoadFolder will stat and copy from any path the caller supplies. Acceptable for this admin-facing, local-first, single-user app; do not expose this endpoint to untrusted callers without adding a path allowlist.
	ProjectName          string                     `json:"project_name,omitempty"`  // Project name for template instantiation (defaults to the workspace name)
	Tags                 []string                   `json:"tags,omitempty"`          // Optional initial tags; merged with template tags
	CreateTemplateAgents *bool                      `json:"create_template_agents,omitempty"`
	// GroupRoster opts a Group into the separate reviewed roster contract. It is
	// intentionally not team_intent: project role staffing remains invalid for
	// groups and cannot be smuggled in through this UI path.
	GroupRoster            bool                    `json:"group_roster,omitempty"`
	TemplateAgentOverrides []templateAgentOverride `json:"template_agent_overrides,omitempty"`
	TemplateAgentReview    *templateAgentReview    `json:"template_agent_review,omitempty"`
	// TeamIntent is raw so absence (legacy), explicit null, malformed objects,
	// and unknown fields remain distinguishable at the strict creation gate.
	TeamIntent        json.RawMessage `json:"team_intent,omitempty"`
	teamIntentPresent bool
	// RoleStaffing carries the user's per-role choices: which of the
	// blueprint's declared roles to fill, and how. A role the user left empty
	// is simply absent, so an empty (but present) slice means "create this
	// workspace with nobody in it" — a supported outcome.
	//
	// Its ABSENCE is what preserves every pre-vacancy caller: nil means the
	// endpoint behaves exactly as it always has, auto-staffing required roles
	// and seeding template agents (FR57). The vacancy default is a property of
	// the wizard, not of this API, which is why CreateFromTemplate and the
	// Personal HQ setup coordinator are unaffected.
	RoleStaffing []roleStaffingInput `json:"role_staffing,omitempty"`
	// JSON presence is tracked separately because strict intent requires an
	// explicit array; nil remains meaningful to legacy callers.
	roleStaffingPresent bool
	roleStaffingNull    bool
	Blank               bool `json:"blank,omitempty"` // The Blank blueprint: seed the synthetic single-agent roster (no template, no project)
	// GroupRequirementReview makes this request an inert review. A policy-aware
	// commit must replay the same request with the returned token and one caller
	// idempotency key; neither field is accepted as a trusted destination.
	GroupRequirementReview bool   `json:"group_requirement_review,omitempty"`
	GroupComposition       string `json:"group_composition,omitempty"`
	CreateRequiredHome     bool   `json:"create_required_home,omitempty"`
	GroupReviewToken       string `json:"group_review_token,omitempty"`
	IdempotencyKey         string `json:"idempotency_key,omitempty"`
	// ProjectConnection attaches a project folder the user already has, chosen
	// with the native folder picker, instead of scaffolding a new project.
	ProjectConnection *createWorkspaceProjectConnection `json:"project_connection,omitempty"`
	// BlueprintInputs carries the values the user chose for the blueprint's
	// declared inputs, keyed by field id. Values stay raw so the one validator
	// in projecttemplates decides what each field type accepts — this layer
	// never interprets a number or an option value itself.
	BlueprintInputs map[string]json.RawMessage `json:"blueprint_inputs,omitempty"`
}

func (req *createWorkspaceRequest) UnmarshalJSON(data []byte) error {
	type requestAlias createWorkspaceRequest
	var decoded requestAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*req = createWorkspaceRequest(decoded)
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	teamRaw, teamPresent := fields["team_intent"]
	req.teamIntentPresent = teamPresent
	if teamPresent {
		req.TeamIntent = append(json.RawMessage(nil), teamRaw...)
	}
	raw, present := fields["role_staffing"]
	req.roleStaffingPresent = present
	req.roleStaffingNull = present && bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
	return nil
}

// roleStaffingInput is one filled role in a create request.
type roleStaffingInput struct {
	RoleID string `json:"role_id"`
	// Mode is "create" (mint a new agent from the role's spec) or "assign"
	// (attach one the user already has). It mirrors the staffing adapter's
	// modes so there is one vocabulary end to end.
	Mode     string `json:"mode"`
	Name     string `json:"name"`
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	// SystemPrompt is the user's edit to what the blueprint proposed. Absent
	// means "use the blueprint's", which is what every caller that does not
	// show a form sends. Accepting it is what stops the Create form's System
	// Prompt field from being decorative — it used to be collected and
	// silently dropped.
	SystemPrompt string `json:"system_prompt,omitempty"`
	// ReasoningEffort is the Create form's reasoning level. It is kept only
	// when the created agent's provider/model accepts it (Codex, Claude Code).
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
}

const (
	roleStaffingModeCreate = "create"
	roleStaffingModeAssign = "assign"
)

// normalizeRoleStaffing validates the per-role choices and returns them keyed
// by role id. A malformed entry is rejected rather than skipped: silently
// dropping one would create a workspace missing a role the user asked for and
// report success.
func normalizeRoleStaffing(items []roleStaffingInput) (map[string]roleStaffingInput, error) {
	out := make(map[string]roleStaffingInput, len(items))
	seenNames := make(map[string]string, len(items))
	for _, item := range items {
		item.RoleID = strings.ToLower(strings.TrimSpace(item.RoleID))
		item.Mode = strings.ToLower(strings.TrimSpace(item.Mode))
		item.Name = strings.TrimSpace(item.Name)
		item.Provider = strings.ToLower(strings.TrimSpace(item.Provider))
		item.Model = strings.TrimSpace(item.Model)
		item.SystemPrompt = strings.TrimSpace(item.SystemPrompt)
		requestedEffort := strings.TrimSpace(item.ReasoningEffort)
		item.ReasoningEffort = types.NormalizeReasoningEffort(requestedEffort)
		if requestedEffort != "" && item.ReasoningEffort == "" {
			return nil, fmt.Errorf("reasoning_effort for %q must be one of [low medium high xhigh max]", item.RoleID)
		}
		if item.Mode == "" {
			item.Mode = roleStaffingModeCreate
		}
		if item.RoleID == "" || item.Name == "" {
			return nil, fmt.Errorf("each role_staffing entry needs a role_id and a name")
		}
		if item.Mode != roleStaffingModeCreate && item.Mode != roleStaffingModeAssign {
			return nil, fmt.Errorf("role_staffing mode for %q must be create or assign", item.RoleID)
		}
		// An assigned agent keeps its own definition entirely — accepting any of
		// these would imply this request could rewrite an agent the user owns.
		if item.Mode == roleStaffingModeAssign &&
			(item.Provider != "" || item.Model != "" || item.SystemPrompt != "" || item.ReasoningEffort != "") {
			return nil, fmt.Errorf("assigning %q cannot change its setup; edit it on the Agents page", item.Name)
		}
		if _, duplicate := out[item.RoleID]; duplicate {
			return nil, fmt.Errorf("role %q is staffed twice", item.RoleID)
		}
		// One agent fills at most one role per workspace: two roles naming the
		// same agent would resolve to a single attachment and silently drop a
		// role the user believes they filled.
		nameKey := strings.ToLower(item.Name)
		if other, clash := seenNames[nameKey]; clash {
			return nil, fmt.Errorf("agent %q cannot fill both %q and %q", item.Name, other, item.RoleID)
		}
		seenNames[nameKey] = item.RoleID
		out[item.RoleID] = item
	}
	return out, nil
}

// CreateFromTemplate creates a normal (non-group) workspace from a built-in
// template by ID and returns its new workspace ID. It reuses the exact
// production POST /api/workspaces path in-process (entry-agent selection,
// tool binding, scaffold provisioning, starter-task seeding, template
// provenance) rather than duplicating any of that logic, so callers outside
// the HTTP layer — such as the Personal HQ setup coordinator — get identical
// behavior to a user picking the template from the library (PRD FR128).
func (h *Handler) CreateFromTemplate(ctx context.Context, name, templateID string) (string, error) {
	return h.createFromTemplate(ctx, name, templateID)
}

func (h *Handler) createFromTemplate(ctx context.Context, name, templateID string) (string, error) {
	body, err := json.Marshal(createWorkspaceRequest{Name: name, TemplateID: templateID})
	if err != nil {
		return "", fmt.Errorf("failed to encode workspace creation request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "/api/workspaces", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("failed to build workspace creation request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.HandleWorkspaces(rec, req)

	if rec.Code != http.StatusCreated {
		return "", fmt.Errorf("workspace creation failed (%d): %s", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	var resp struct {
		Folder struct {
			ID string `json:"id"`
		} `json:"folder"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		return "", fmt.Errorf("failed to parse workspace creation response: %w", err)
	}
	if resp.Folder.ID == "" {
		return "", errors.New("workspace creation response missing an id")
	}
	return resp.Folder.ID, nil
}

// createWorkspace handles POST /api/workspaces. The flow is staged:
// validate → build record → select entry agent → persist → provision folder
// and apply template → respond. Each stage is a helper below.
func (h *Handler) createWorkspace(w http.ResponseWriter, r *http.Request) {
	var req createWorkspaceRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	if envelopeErr := validateWorkspaceTeamIntentEnvelope(&req); envelopeErr != nil {
		respondWorkspaceTeamReadinessError(w, envelopeErr)
		return
	}

	if req.Name == "" {
		_ = orihttp.RespondBadRequest(w, "name is required")
		return
	}

	requestedTags, err := agentworkspace.ValidateWorkspaceTags(req.Tags)
	if err != nil {
		_ = orihttp.RespondBadRequest(w, err.Error())
		return
	}

	kind, err := parseWorkspaceKind(req.Kind)
	if err != nil {
		_ = orihttp.RespondBadRequest(w, err.Error())
		return
	}
	if req.GroupRoster && kind != session.WorkspaceKindGroup {
		_ = orihttp.RespondBadRequest(w, "group_roster is available only when kind is group")
		return
	}
	if kind == session.WorkspaceKindGroup && req.GroupRoster {
		if !createTemplateAgentsEnabled(req) {
			_ = orihttp.RespondBadRequest(w, "group_roster requires create_template_agents to be true")
			return
		}
		if req.TemplateAgentReview == nil {
			_ = orihttp.RespondBadRequest(w, "group_roster requires a reviewed Group Manager")
			return
		}
		if req.teamIntentPresent || req.roleStaffingPresent {
			_ = orihttp.RespondBadRequest(w, "group_roster cannot use project team_intent or role_staffing")
			return
		}
		if strings.TrimSpace(req.EntryAgentName) != "" {
			_ = orihttp.RespondBadRequest(w, "group_roster chooses its Group Manager; use template_agent_overrides to customize it")
			return
		}
	} else if kind == session.WorkspaceKindGroup && req.TemplateAgentReview != nil {
		_ = orihttp.RespondBadRequest(w, "template_agent_review for a group requires group_roster")
		return
	}
	if err := h.requireGroupParent(r.Context(), req.ParentID); err != nil {
		handleWorkspaceParentError(w, err)
		return
	}
	// Refused before anything is created: the SQLite record is written first
	// and a folder failure is otherwise treated as non-fatal.
	if strings.TrimSpace(req.ParentID) == "" && h.isWorkspaceRootLocation(req.Location) &&
		agentworkspace.IsReservedTopLevelSlug(firstNonEmptyString(req.FolderSlug, req.Name)) {
		_ = orihttp.RespondBadRequest(w, agentworkspace.ReservedWorkspaceSlugMessage)
		return
	}

	wantsProject := strings.TrimSpace(req.TemplateID) != "" || strings.TrimSpace(req.TemplatePath) != ""
	if strings.TrimSpace(req.TemplateID) != "" && strings.TrimSpace(req.TemplatePath) != "" {
		_ = orihttp.RespondBadRequest(w, "specify either template_id or template_path, not both")
		return
	}
	if wantsProject && kind == session.WorkspaceKindGroup {
		_ = orihttp.RespondBadRequest(w, "group workspaces cannot be created from a project template")
		return
	}

	var resolvedTemplate projecttemplates.Template
	templateResolved := false
	var templateResolveErr error
	if wantsProject {
		resolvedTemplate, templateResolveErr = h.resolveProjectTemplate(req.TemplateID, req.TemplatePath)
		templateResolved = templateResolveErr == nil
	}
	if req.GroupRequirementReview && !templateResolved {
		if templateResolveErr != nil {
			h.respondWorkspaceProjectError(w, templateResolveErr)
		} else {
			_ = orihttp.RespondBadRequest(w, "a template is required for placement review")
		}
		return
	}
	if wantsProject && !templateResolved && (strings.HasPrefix(strings.TrimSpace(req.TemplateID), "plugin:") ||
		errors.Is(templateResolveErr, projecttemplates.ErrTemplateVariantSource) ||
		errors.Is(templateResolveErr, projecttemplates.ErrTemplateVariantSourceChanged) ||
		errors.Is(templateResolveErr, projecttemplates.ErrTemplateVariantOwner)) {
		h.respondWorkspaceProjectError(w, templateResolveErr)
		return
	}
	if templateResolved {
		if options, ok := personalAssistantCreationOptions(r.Context()); ok {
			if strings.TrimSpace(req.TemplateID) != personalhq.PersonalHQTemplateID || strings.TrimSpace(req.TemplatePath) != "" {
				_ = orihttp.RespondBadRequest(w, "personal assistant creation options require the built-in personal-ops template")
				return
			}
			resolvedTemplate, err = applyPersonalAssistantTemplateOptions(resolvedTemplate, options)
			if err != nil {
				_ = orihttp.RespondBadRequest(w, err.Error())
				return
			}
		}

		// Re-derive blueprint readiness before issuing a placement receipt. The
		// receipt must never make an unavailable source appear safe to create.
		readiness := h.revalidateBlueprintReadiness(resolvedTemplate)
		if blueprintCreationBlocked(resolvedTemplate, readiness) {
			respondBlueprintReadinessConflict(w, resolvedTemplate, readiness)
			return
		}
	}

	// An attached project is validated here, before any Home claim, agent,
	// workspace, or folder exists, so a refusal leaves nothing behind.
	attachPlan, attachErr := h.planCreateWorkspaceAttach(req, kind, resolvedTemplate, templateResolved)
	if attachErr != nil {
		respondCreateWorkspaceAttachError(w, attachErr)
		return
	}

	// The client's values are never trusted: they are re-validated against the
	// blueprint's own declaration here, before anything is created, so a
	// refusal leaves nothing behind.
	blueprintInputs, blueprintInputsErr := resolveCreateWorkspaceInputs(req, kind, resolvedTemplate, templateResolved)
	if blueprintInputsErr != nil {
		_ = orihttp.RespondBadRequest(w, blueprintInputsErr.Error())
		return
	}

	// A versioned wizard request must prove its reviewed team before any group
	// claim or creation side effect. Blank joins the same synthetic template
	// machinery only on this strict path; legacy Blank behavior remains below.
	teamTemplate := resolvedTemplate
	teamTemplateResolved := templateResolved
	if req.teamIntentPresent && req.Blank && !wantsProject {
		teamTemplate = blankWorkspaceTemplate()
		teamTemplateResolved = true
	}
	if req.teamIntentPresent && teamTemplateResolved && strings.TrimSpace(req.GroupComposition) == string(grouprequirements.CompositionStandalone) {
		standalone, standaloneErr := projecttemplates.StandaloneTemplate(teamTemplate)
		if standaloneErr == nil {
			teamTemplate = standalone
		}
	}
	teamIntent, teamErr := h.validateWorkspaceTeamReadiness(
		r.Context(), &req, teamTemplate, resolvedTemplate, teamTemplateResolved, string(kind),
	)
	if teamErr != nil {
		if !respondWorkspaceTeamReadinessError(w, teamErr) {
			_ = orihttp.RespondBadRequest(w, teamErr.Error())
		}
		return
	}
	if teamIntent != nil && req.Blank && !wantsProject {
		resolvedTemplate = teamTemplate
		templateResolved = true
	}
	if kind == session.WorkspaceKindGroup && !req.GroupRoster {
		_ = orihttp.RespondBadRequest(w, "groups require a reviewed group_roster")
		return
	}

	var groupPlan *createWorkspaceGroupPlan
	if templateResolved {
		var handled bool
		resolvedTemplate, groupPlan, handled = h.prepareCreateWorkspaceGroupRequirement(r.Context(), w, req, resolvedTemplate)
		if handled {
			return
		}
		if h.respondCreateWorkspaceGroupReplay(r.Context(), w, groupPlan) {
			return
		}
	}
	rawResolvedTemplate := resolvedTemplate
	if templateResolved && createTemplateAgentsEnabled(req) && len(req.TemplateAgentOverrides) > 0 {
		var err error
		resolvedTemplate, err = applyTemplateAgentOverrides(resolvedTemplate, req.TemplateAgentOverrides)
		if err != nil {
			if !respondTemplateAgentOverrideValidationError(w, err) {
				_ = orihttp.RespondBadRequest(w, err.Error())
			}
			return
		}
	}

	var strictTemplate, strictFreshTemplate *projecttemplates.Template
	if req.TemplateAgentReview != nil {
		if !createTemplateAgentsEnabled(req) {
			_ = orihttp.RespondBadRequest(w, "template_agent_review cannot be used when the blueprint team is excluded")
			return
		}
		var rawReviewTemplate, effectiveReviewTemplate projecttemplates.Template
		switch {
		case templateResolved && !hasManagedAssistantTeam(rawResolvedTemplate):
			rawReviewTemplate = rawResolvedTemplate
			effectiveReviewTemplate = resolvedTemplate
		case req.Blank && kind != session.WorkspaceKindGroup:
			rawReviewTemplate = blankWorkspaceTemplate()
			var applyErr error
			effectiveReviewTemplate, applyErr = applyTemplateAgentOverrides(rawReviewTemplate, req.TemplateAgentOverrides)
			if applyErr != nil {
				if !respondTemplateAgentOverrideValidationError(w, applyErr) {
					_ = orihttp.RespondBadRequest(w, applyErr.Error())
				}
				return
			}
		case kind == session.WorkspaceKindGroup && req.GroupRoster:
			rawReviewTemplate = groupRosterTemplate(req.Name)
			var applyErr error
			effectiveReviewTemplate, applyErr = applyTemplateAgentOverrides(rawReviewTemplate, req.TemplateAgentOverrides)
			if applyErr != nil {
				if !respondTemplateAgentOverrideValidationError(w, applyErr) {
					_ = orihttp.RespondBadRequest(w, applyErr.Error())
				}
				return
			}
		default:
			_ = orihttp.RespondBadRequest(w, "template_agent_review requires an ordinary, Blank, or reviewed Group roster")
			return
		}
		if err := h.validateTemplateAgentReview(req.TemplateAgentReview, rawReviewTemplate, effectiveReviewTemplate); err != nil {
			respondTemplateAgentReviewError(w, err)
			return
		}
		strictTemplate = &effectiveReviewTemplate
		strictFreshTemplate = &rawReviewTemplate
	}

	composition, err := h.validateCreateWorkspaceAgentComposition(req)
	if err != nil {
		_ = orihttp.RespondBadRequest(w, err.Error())
		return
	}
	if composition.usesExistingAgentRoster {
		req.ExistingAgentNames = composition.existingAgentNames
		req.EntryAgentName = composition.entryAgentName
	}

	// Validate the COMPLETE resulting roster before anything is persisted (FR70).
	// validateTemplateAgentOverrideNames only compares blueprint specs with each
	// other, so a customized copy renamed onto a selected saved agent would
	// otherwise reach seeding and silently collapse two intended members into one.
	if templateResolved && createTemplateAgentsEnabled(req) {
		if err := validateRosterNameCollisions(resolvedTemplate, req.TemplateAgentOverrides, composition.existingAgentNames); err != nil {
			_ = orihttp.RespondBadRequest(w, err.Error())
			return
		}
	} else if req.GroupRoster && strictFreshTemplate != nil {
		if err := validateRosterNameCollisions(*strictFreshTemplate, req.TemplateAgentOverrides, composition.existingAgentNames); err != nil {
			_ = orihttp.RespondBadRequest(w, err.Error())
			return
		}
	}

	ws := buildCreateWorkspace(req, kind, requestedTags, resolvedTemplate, templateResolved)
	if groupPlan != nil {
		ws.ID = groupPlan.claim.Operation.ChildWorkspaceID
		ws.OwnerUserID = groupPlan.claim.Operation.OwnerUserID
		if groupPlan.claim.Snapshot.SelectedComposition == grouprequirements.CompositionGrouped {
			// Create directly at the reviewed canonical destination. The client
			// parent is never used as a temporary placement or membership claim.
			ws.ParentID = groupPlan.claim.Operation.HomeWorkspaceID
		}
	}

	seed, ok := h.selectCreateWorkspaceEntryAgent(w, ws, req, kind, resolvedTemplate, templateResolved, strictTemplate, strictFreshTemplate)
	if !ok {
		return
	}
	if options, pafCreation := personalAssistantCreationOptions(r.Context()); pafCreation {
		markPersonalAssistantPresentation(ws, options)
	}

	if err := h.store.CreateWorkspace(r.Context(), ws); err != nil {
		logger.Error("Failed to create workspace", logger.Fields{"error": err})
		// The roster was seeded before this point, so without cleanup the user
		// is left with agents in Your Agents for a workspace that does not
		// exist (FR71).
		h.rollbackSeededAgents(seed)
		if errors.Is(err, session.ErrWorkspaceSlugConflict) {
			parentDir := ""
			if h.workspaceStore != nil {
				parentDir = h.workspaceStore.BasePath()
			}
			writeWorkspaceCreateSlugConflict(w, req.Name, h.globalWorkspaceSlugConflict(r.Context(), ws.FolderSlug, "", parentDir))
			return
		}
		_ = orihttp.RespondInternalError(w, "Failed to create workspace")
		return
	}

	prov, responded := h.provisionCreateWorkspaceFolder(r.Context(), w, req, ws, createTemplateContext{
		wantsProject: wantsProject,
		template:     resolvedTemplate,
		resolved:     templateResolved,
		resolveErr:   templateResolveErr,
		attach:       attachPlan,
		inputs:       blueprintInputs,
	}, seed)
	if responded {
		return
	}
	if prov.attachErr != nil {
		h.rollbackFailedAttachCreate(r.Context(), ws.ID, seed)
		respondCreateWorkspaceAttachError(w, prov.attachErr)
		return
	}

	// A new group-policy contract becomes canonical before any template task,
	// capability, plugin/tool binding, or Home-dependent staffing can run. A
	// grouped commit must also observe its exact parent and reciprocal link.
	if err := h.finalizeCreateWorkspaceGroupRequirement(r.Context(), ws.ID, groupPlan, resolvedTemplate); err != nil {
		_ = h.rollbackIncompleteGroupWorkspace(r.Context(), ws.ID, groupPlan.claim.Operation.OperationDigest, string(groupPlan.claim.Operation.Status), seed)
		_ = orihttp.RespondJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": "Workspace placement is incomplete; retry the same reviewed operation.",
			"group_requirement": map[string]any{
				"state": "operation_incomplete", "reason": "operation_incomplete",
				"summary": "Ori could not observe every required placement consequence.",
				"actions": []grouprequirements.Action{grouprequirements.ActionRetry},
			},
		})
		return
	}

	// Bind per-agent tools only after the policy snapshot and, where required,
	// reciprocal group membership are durable. Declarations alone grant nothing.
	prov.agentToolWarnings = h.bindSeededAgentTools(ws.ID, seed.Created)

	if ws != nil {
		h.allowlistLocallyCreatedWorkspace(ws.ID)
	}

	if ws != nil {
		createdTemplateID := ""
		if templateResolved {
			createdTemplateID = resolvedTemplate.ID
		}
		h.publishWorkspaceCreated(ws.ID, ws.Name, createdTemplateID, string(ws.Kind))
	}

	agentSeedWarnings := append(seed.Warnings, prov.agentToolWarnings...)

	// Seed the template's starter tasks server-side, after the skeleton is
	// instantiated and the roster is attached, so they are assigned to a real
	// entry agent and the setup task only ever adjusts files that already
	// exist. Best-effort: a failure logs and never fails creation.
	seededStarterTasks := 0
	if templateResolved && kind != session.WorkspaceKindGroup {
		starterTemplate := resolvedTemplate
		if attachPlan != nil {
			// An attached project gets only the tasks its blueprint allows for
			// existing projects; a task about the scaffolded file would ask the
			// user about a file Ori never created.
			starterTemplate.StarterTasks = starterTasksForAttachedProject(resolvedTemplate)
		}
		seededStarterTasks = h.seedTemplateStarterTasksLogged(ws.ID, starterTemplate)
	}

	// Must run after starter-task seeding above — see
	// persistCreateWorkspaceTemplateProvenance's doc comment for why.
	assistantStationID := ""
	var teamCompletion *workspaceTeamCompletion
	var groupSnapshot *agentworkspace.GroupRequirementSnapshot
	if groupPlan != nil {
		groupSnapshot = groupPlan.claim.Snapshot
	}
	if capabilityWarning := h.persistCreateWorkspaceTemplateProvenance(ws.ID, resolvedTemplate, templateResolved, groupSnapshot); capabilityWarning != "" {
		if prov.projectWarning == "" {
			prov.projectWarning = capabilityWarning
		} else {
			prov.projectWarning += "; " + capabilityWarning
		}
	}
	if templateResolved && hasManagedAssistantTeam(resolvedTemplate) && h.workspaceTaskStore != nil {
		if groupPlan != nil && groupPlan.claim.Snapshot.SelectedComposition == grouprequirements.CompositionGrouped {
			// The mandatory path already established and observed this exact link
			// before tasks/tools/capabilities. It is never downgraded to a warning.
			assistantStationID = groupPlan.claim.Operation.HomeWorkspaceID
		} else {
			// Legacy absence preserves the historical best-effort activation path.
			station, _, err := agentworkspace.NewAssistantProgramStore(h.workspaceTaskStore).EnsureProjectStation(ws.ID)
			if err != nil {
				warning := "assistant home could not be linked; use Activate from the workspace after resolving storage"
				if prov.projectWarning == "" {
					prov.projectWarning = warning
				} else {
					prov.projectWarning += "; " + warning
				}
				logger.Warn("Failed to link assistant station", logger.Fields{"workspace_id": ws.ID, "error": err})
			} else {
				assistantStationID = station.ID
			}
		}
		// An assistant-program blueprint can only be staffed once its station
		// link exists, which is why this runs here rather than in the
		// entry-agent stage. Exactly the roles the user filled are committed;
		// the roles they left empty stay empty.
		if assistantStationID != "" && len(req.RoleStaffing) > 0 {
			if warning := h.staffAssistantRoles(r.Context(), ws.ID, req.RoleStaffing); warning != "" {
				if prov.projectWarning == "" {
					prov.projectWarning = warning
				} else {
					prov.projectWarning += "; " + warning
				}
			}
		}
		if teamIntent != nil {
			teamCompletion = h.observeAssistantTeamCompletion(ws.ID, ws.FolderSlug, req.RoleStaffing)
		}
	}

	// Completeness/ordering backstop: when the workspace was created with an
	// entry agent, claim any tasks that already exist on the folder workspace
	// (e.g. import-style seeds). Template starter tasks are assigned at seed
	// time above, so this is a no-op for them.
	if seed.EntrySet {
		h.claimUnassignedTasksForEntryAgentLogged(ws.ID)
	}

	if err := h.completeCreateWorkspaceGroupRequirement(r.Context(), groupPlan); err != nil {
		_ = orihttp.RespondJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": "Workspace placement completed but its operation receipt needs reconciliation.",
			"group_requirement": map[string]any{
				"state": "operation_incomplete", "reason": "operation_incomplete",
				"summary": "Retry the same reviewed operation; Ori will not create a duplicate workspace.",
				"actions": []grouprequirements.Action{grouprequirements.ActionRetry},
			},
		})
		return
	}

	logger.Info("Workspace created", logger.Fields{"id": ws.ID, "name": req.Name, "folder_slug": ws.FolderSlug, "kind": ws.Kind})

	response := map[string]any{
		"success": true,
		"folder":  ws,
	}
	if assistantStationID != "" {
		response["assistant_station_id"] = assistantStationID
	}
	if teamCompletion != nil {
		response["team_completion"] = teamCompletion
	}
	if groupSnapshot != nil {
		response["group_requirement"] = groupSnapshot
	}
	if prov.projectWarning != "" {
		response["project_warning"] = prov.projectWarning
	}
	if len(agentSeedWarnings) > 0 {
		response["agent_warnings"] = agentSeedWarnings
	}
	if len(seed.ReuseNotices) > 0 {
		response["agent_reuse_notices"] = seed.ReuseNotices
	}
	if seededStarterTasks > 0 {
		response["seeded_starter_tasks"] = seededStarterTasks
	}
	_ = orihttp.RespondCreated(w, response)
}

// allowlistLocallyCreatedWorkspace records that this data directory owns a
// workspace it just created, so its agent snapshots are restored (and not
// wiped) on subsequent startups, mirroring the import flow. It applies to every
// local creation path, including a program Home a reviewed operation created.
// Best-effort; a failure only affects later agent hydration, not the creation.
func (h *Handler) allowlistLocallyCreatedWorkspace(workspaceID string) {
	if h == nil || h.workspaceAllowlist == nil || strings.TrimSpace(workspaceID) == "" {
		return
	}
	if err := h.workspaceAllowlist.Add(workspaceID); err != nil {
		logger.Warn("Failed to allowlist created workspace", logger.Fields{"id": workspaceID, "error": err.Error()})
	}
}

// publishWorkspaceCreated emits a workspace.created event after a workspace is
// persisted. Consumed by the onboarding progression detector. No-op when the
// event bus is not configured.
//
// template_id is always present: the blueprint the workspace came from, or ""
// for a blank one. Its presence is what marks an event from this creator, and
// the starter missions' "Start a project workspace" branch reads it to tell a
// project apart from Personal HQ and the starter blueprints
// (tasks/prd-starter-missions.md FR16). kind separates a group from a
// workspace.
func (h *Handler) publishWorkspaceCreated(workspaceID, name, templateID, kind string) {
	if h == nil || h.eventBus == nil {
		return
	}
	h.eventBus.Publish(agentworkspace.Event{
		Type:        agentworkspace.EventWorkspaceCreated,
		WorkspaceID: workspaceID,
		Source:      "api",
		Data: map[string]any{
			"name":        name,
			"template_id": strings.TrimSpace(templateID),
			"kind":        kind,
		},
	})
}

// buildCreateWorkspace constructs the workspace record from a validated
// create request. Pure construction: no stores are touched.
func buildCreateWorkspace(req createWorkspaceRequest, kind session.WorkspaceKind, tags []string, tmpl projecttemplates.Template, templateResolved bool) *session.Workspace {
	ws := &session.Workspace{
		Name:        req.Name,
		Kind:        kind,
		Description: req.Description,
		ParentID:    req.ParentID,
		Color:       req.Color,
		FolderSlug:  agentworkspace.Slugify(req.Name),
		ProjectPath: req.ProjectPath,
		Tags:        tags,
	}
	if templateResolved {
		ws.Tags = agentworkspace.MergeWorkspaceTags(ws.Tags, tmpl.Tags)
	}
	if requestedSlug := strings.TrimSpace(req.FolderSlug); requestedSlug != "" {
		ws.FolderSlug = agentworkspace.Slugify(requestedSlug)
	}
	if req.OrderIndex != nil {
		ws.OrderIndex = *req.OrderIndex
	}
	if kind != session.WorkspaceKindGroup {
		// Behavior profile: the request value wins; when absent, fall back to the
		// selected template's behavior_profile so a template_id carries its
		// behavior even when the caller (e.g. an API client) omits the preset.
		preset := strings.TrimSpace(req.WorkspacePreset)
		if preset == "" && templateResolved {
			preset = tmpl.BehaviorProfile
		}
		ws.SharedData = workspacesettings.Store(ws.SharedData, workspacesettings.ProfileDefaults(preset))
	}
	if bootstrapData := normalizeWorkspaceBootstrap(req.WorkspaceBootstrap); bootstrapData != nil {
		if ws.SharedData == nil {
			ws.SharedData = make(map[string]any)
		}
		ws.SharedData["workspace_bootstrap"] = bootstrapData
	}
	return ws
}

// selectCreateWorkspaceEntryAgent applies the create-time entry-agent policy.
// Legacy callers that omit existing_agent_names keep their historical explicit
// entry-agent behavior. Callers that include it compose a template/Blank roster
// first, then attach the validated existing definitions, and only then apply an
// explicit primary. This keeps creation atomic and never requires best-effort
// post-create attachment requests.
// Returns ok=false when an error response has already been written.
func (h *Handler) selectCreateWorkspaceEntryAgent(w http.ResponseWriter, ws *session.Workspace, req createWorkspaceRequest, kind session.WorkspaceKind, tmpl projecttemplates.Template, templateResolved bool, strictTemplate, strictFreshTemplate *projecttemplates.Template) (seedAgentsResult, bool) {
	var seed seedAgentsResult
	usesExistingAgentRoster := req.ExistingAgentNames != nil

	// The vacancy model replaces automatic staffing entirely: exactly the roles
	// the user filled are staffed, and NOTHING else runs — no whole-roster
	// seeding, and no fallback "<Name> Manager" when every role was left empty
	// (FR59). A workspace with no agent is a supported outcome, and inventing
	// one to avoid it is the behavior this feature exists to remove.
	//
	// An assistant-program blueprint staffs after creation instead: its station
	// link does not exist until the workspace is persisted.
	if req.RoleStaffing != nil {
		if templateResolved && !hasManagedAssistantTeam(tmpl) && tmpl.HasAgents() {
			staffing, err := normalizeRoleStaffing(req.RoleStaffing)
			if err != nil {
				_ = orihttp.RespondBadRequest(w, err.Error())
				return seed, false
			}
			seed, err = h.seedRoleStaffedAgents(ws, tmpl, staffing)
			if err != nil {
				cleanupErrors := h.respondRoleStaffingError(seed, err)
				response := map[string]any{
					"error":    err.Error(),
					"conflict": map[string]any{"type": "role_staffing"},
				}
				if len(cleanupErrors) > 0 {
					response["cleanup_errors"] = cleanupErrors
				}
				_ = orihttp.RespondJSON(w, http.StatusConflict, response)
				return seedAgentsResult{}, false
			}
		}
		for _, name := range req.ExistingAgentNames {
			attachWorkspaceSpecialist(ws, name)
		}
		if !seed.EntrySet && len(req.ExistingAgentNames) > 0 {
			entryName := req.ExistingAgentNames[0]
			if req.EntryAgentName != "" {
				entryName = req.EntryAgentName
			}
			setWorkspaceEntryAgent(ws, entryName)
			seed.EntrySet = true
		}
		return seed, true
	}

	if strictTemplate != nil && req.TemplateAgentReview != nil {
		freshTemplate := tmpl
		if strictFreshTemplate != nil {
			freshTemplate = *strictFreshTemplate
		}
		var err error
		seed, err = h.seedTemplateAgentsStrict(ws, *strictTemplate, freshTemplate, *req.TemplateAgentReview)
		if err != nil {
			h.respondStrictTemplateAgentSeedError(w, seed, err)
			return seed, false
		}
		for _, name := range req.ExistingAgentNames {
			attachWorkspaceSpecialist(ws, name)
		}
		if req.EntryAgentName != "" {
			setWorkspaceEntryAgent(ws, req.EntryAgentName)
			seed.EntrySet = true
		}
		return seed, true
	}

	if usesExistingAgentRoster {
		switch {
		case templateResolved && createTemplateAgentsEnabled(req) && !hasManagedAssistantTeam(tmpl):
			if tmpl.HasAgents() {
				seed = h.seedTemplateAgents(ws, tmpl)
			}
			if !seed.EntrySet {
				if agentName := h.autoCreateManagerEntryAgent(ws); agentName != "" {
					setWorkspaceEntryAgent(ws, agentName)
					seed.EntrySet = true
				}
			}
		case req.Blank && kind != session.WorkspaceKindGroup && createTemplateAgentsEnabled(req):
			blankTpl, err := applyTemplateAgentOverrides(blankWorkspaceTemplate(), req.TemplateAgentOverrides)
			if err != nil {
				_ = orihttp.RespondBadRequest(w, err.Error())
				return seed, false
			}
			seed = h.seedTemplateAgents(ws, blankTpl)
		}

		for _, name := range req.ExistingAgentNames {
			attachWorkspaceSpecialist(ws, name)
		}

		if req.EntryAgentName != "" {
			setWorkspaceEntryAgent(ws, req.EntryAgentName)
			seed.EntrySet = true
		} else if !seed.EntrySet && len(req.ExistingAgentNames) > 0 {
			setWorkspaceEntryAgent(ws, req.ExistingAgentNames[0])
			seed.EntrySet = true
		}
		return seed, true
	}

	switch {
	case req.EntryAgentName != "":
		entryAgentName, err := h.validateWorkspaceEntryAgent(req.EntryAgentName)
		if err != nil {
			logger.Error("Failed to validate workspace entry agent", logger.Fields{"name": req.Name, "error": err})
			_ = orihttp.RespondBadRequest(w, err.Error())
			return seed, false
		}
		if entryAgentName != "" {
			setWorkspaceEntryAgent(ws, entryAgentName)
			seed.EntrySet = true
		}
	case templateResolved && createTemplateAgentsEnabled(req) && !hasManagedAssistantTeam(tmpl):
		// The template declares an agent roster: seed it (first = entry agent,
		// rest = specialists). Every template-created workspace must end up with
		// an entry agent to own its seeded starter tasks, so a roster-less
		// (legacy) template — or a roster whose seeding failed — falls back to
		// an auto-created "<Name> Manager". An explicit create_template_agents:
		// false opt-out skips this case entirely: the caller chose to pick
		// agents post-create, and the claim-on-agent-add sweep hands the seeded
		// tasks over when the first agent joins.
		if tmpl.HasAgents() {
			seed = h.seedTemplateAgents(ws, tmpl)
		}
		if !seed.EntrySet {
			if agentName := h.autoCreateManagerEntryAgent(ws); agentName != "" {
				setWorkspaceEntryAgent(ws, agentName)
				seed.EntrySet = true
			}
		}
	case req.Blank && kind != session.WorkspaceKindGroup && createTemplateAgentsEnabled(req):
		// Blank blueprint: seed the synthetic single-agent roster (a reusable
		// "Workspace Manager") so a blank workspace is chat-ready, honoring the
		// review panel's edits (overrides) and its Create toggle. A seed failure
		// surfaces a warning and leaves the workspace agent-less — the legacy
		// no-entry-agent fallback the detail page already handles.
		blankTpl, err := applyTemplateAgentOverrides(blankWorkspaceTemplate(), req.TemplateAgentOverrides)
		if err != nil {
			_ = orihttp.RespondBadRequest(w, err.Error())
			return seed, false
		}
		seed = h.seedTemplateAgents(ws, blankTpl)
	}
	return seed, true
}

func createTemplateAgentsEnabled(req createWorkspaceRequest) bool {
	return req.CreateTemplateAgents == nil || *req.CreateTemplateAgents
}

type createWorkspaceAgentComposition struct {
	usesExistingAgentRoster bool
	existingAgentNames      []string
	entryAgentName          string
}

// validateCreateWorkspaceAgentComposition canonicalizes saved-agent selections
// before any workspace or template agent is persisted. A nil slice is a legacy
// request and intentionally takes the old entry-agent selection path.
func (h *Handler) validateCreateWorkspaceAgentComposition(req createWorkspaceRequest) (createWorkspaceAgentComposition, error) {
	composition := createWorkspaceAgentComposition{
		usesExistingAgentRoster: req.ExistingAgentNames != nil,
	}
	if !composition.usesExistingAgentRoster {
		return composition, nil
	}

	composition.existingAgentNames = make([]string, 0, len(req.ExistingAgentNames))
	seen := make(map[string]string, len(req.ExistingAgentNames))
	for index, requestedName := range req.ExistingAgentNames {
		requestedName = strings.TrimSpace(requestedName)
		if requestedName == "" {
			return composition, fmt.Errorf("existing_agent_names[%d] cannot be empty", index)
		}
		canonicalName, err := h.validateAttachableWorkspaceAgent(requestedName)
		if err != nil {
			return composition, err
		}
		key := strings.ToLower(strings.TrimSpace(canonicalName))
		if original, alreadySelected := seen[key]; alreadySelected {
			return composition, fmt.Errorf("existing agent %q duplicates %q", requestedName, original)
		}
		seen[key] = canonicalName
		composition.existingAgentNames = append(composition.existingAgentNames, canonicalName)
	}

	if requestedPrimary := strings.TrimSpace(req.EntryAgentName); requestedPrimary != "" {
		canonicalPrimary, err := h.validateAttachableWorkspaceAgent(requestedPrimary)
		if err != nil {
			return composition, fmt.Errorf("entry agent %q does not exist or cannot be attached", requestedPrimary)
		}
		if _, selected := seen[strings.ToLower(strings.TrimSpace(canonicalPrimary))]; !selected {
			return composition, fmt.Errorf("entry_agent_name %q must also be selected in existing_agent_names", requestedPrimary)
		}
		composition.entryAgentName = canonicalPrimary
	}

	return composition, nil
}

// createTemplateContext bundles the template-resolution results that folder
// provisioning and template application need.
type createTemplateContext struct {
	wantsProject bool
	template     projecttemplates.Template
	resolved     bool
	resolveErr   error
	// attach, when set, references an existing project folder instead of
	// scaffolding one.
	attach *createWorkspaceAttachPlan
	// inputs, when set, are the validated values for the blueprint's declared
	// inputs: written into the scaffold and recorded on the workspace.
	inputs *createWorkspaceInputs
}

// createProvisionOutcome carries folder-provisioning results back to
// createWorkspace's response assembly.
type createProvisionOutcome struct {
	projectWarning    string
	agentToolWarnings []string
	// attachErr is set when a requested existing project was not attached.
	// Unlike projectWarning it is fatal: the caller rolls the create back.
	attachErr error
}

// provisionCreateWorkspaceFolder creates the on-disk workspace folder,
// scaffolds content, binds seeded agent tools, and applies the project
// template. Folder problems are non-fatal by design (the SQLite record
// already exists); the one exception is a folder-slug conflict, which rolls
// the workspace back and writes the conflict response itself — signalled by
// responded=true, in which case the caller must return immediately.
func (h *Handler) provisionCreateWorkspaceFolder(ctx context.Context, w http.ResponseWriter, req createWorkspaceRequest, ws *session.Workspace, tc createTemplateContext, seed seedAgentsResult) (out createProvisionOutcome, responded bool) {
	if tc.wantsProject {
		// Default for every path below that does not reach (or does not
		// succeed in) template application: missing store, folder-creation
		// failure, and folder-path failure all leave the workspace without a
		// usable folder. applyCreateWorkspaceTemplate overwrites this with a
		// specific message or clears it on success.
		out.projectWarning = "workspace was created, but the project template was not applied: workspace folder unavailable"
	}
	if tc.attach != nil {
		// Every path below that does not record the attach leaves this set.
		out.attachErr = errAttachWorkspaceFolderUnavailable
	}
	if h.workspaceStore == nil {
		return out, false
	}

	folderWS := &agentworkspace.Workspace{
		ID:             ws.ID,
		Name:           ws.Name,
		Kind:           string(ws.Kind),
		Description:    ws.Description,
		FolderSlug:     ws.FolderSlug,
		OwnerUserID:    ws.OwnerUserID,
		ProjectPath:    ws.ProjectPath,
		Tags:           append([]string(nil), ws.Tags...),
		ParentID:       ws.ParentID,
		AgentInstances: toWorkspaceAgentInstances(ws.AgentInstances),
		SharedData:     ws.SharedData,
		Status:         agentworkspace.StatusActive,
		CreatedAt:      ws.CreatedAt,
		UpdatedAt:      ws.UpdatedAt,
	}

	targetLocation := strings.TrimSpace(req.Location)
	if targetLocation == "" && h.workspaceRootResolver != nil {
		targetLocation = strings.TrimSpace(h.workspaceRootResolver())
	}

	var folderErr error
	switch {
	case targetLocation != "" && !workspacePathsEqual(targetLocation, h.workspaceStore.BasePath()):
		// Custom location or updated default root outside the original file store base.
		folderErr = h.workspaceStore.SaveAt(folderWS, targetLocation)
	default:
		// Default location inside the file store base path.
		folderErr = h.workspaceStore.Save(folderWS)
	}

	if folderErr != nil {
		if errors.Is(folderErr, agentworkspace.ErrReservedWorkspaceSlug) {
			if delErr := h.store.DeleteWorkspace(ctx, ws.ID); delErr != nil {
				logger.Error("Failed to rollback workspace after reserved folder name", logger.Fields{"id": ws.ID, "error": delErr})
			}
			h.rollbackSeededAgents(seed)
			_ = orihttp.RespondBadRequest(w, agentworkspace.ReservedWorkspaceSlugMessage)
			return out, true
		}
		var slugConflict *agentworkspace.FolderSlugConflictError
		if errors.As(folderErr, &slugConflict) {
			if delErr := h.store.DeleteWorkspace(ctx, ws.ID); delErr != nil {
				logger.Error("Failed to rollback workspace after slug conflict", logger.Fields{"id": ws.ID, "error": delErr})
				_ = orihttp.RespondInternalError(w, "Failed to rollback workspace after folder conflict")
				return out, true
			}
			// The workspace record is gone, so the agents seeded for it must go
			// too — otherwise retrying under a different name would find them
			// already present and silently reuse them (FR72).
			h.rollbackSeededAgents(seed)
			writeWorkspaceCreateSlugConflict(w, req.Name, h.globalWorkspaceSlugConflict(ctx, ws.FolderSlug, "", slugConflict.ParentDir))
			return out, true
		}
		logger.Warn("Failed to create workspace folder on disk", logger.Fields{"id": ws.ID, "error": folderErr})
		// Non-fatal: SQLite creation succeeded, folder is supplementary
		return out, false
	}

	folderPath, err := h.workspaceStore.GetFolderPath(ws.ID)
	if err != nil {
		return out, false
	}

	// Install the template's dashboard, if it ships one. Deliberately before
	// the group branch and outside the project-scaffold path: a dashboard is
	// workspace-level, so it applies to groups too, and to metadata-only
	// templates that create no project folder at all.
	h.installTemplateDashboard(ws.ID, folderPath, tc)

	if ws.Kind == session.WorkspaceKindGroup {
		// Groups physically nest members under sub-workspaces/, so
		// their linked folder and MCP roots are scoped to the group's
		// own files/ and notes/ — never the folder root — keeping
		// member content hidden from group agents.
		if dirs, mkErr := ensureGroupContentDirs(folderPath); mkErr != nil {
			logger.Warn("Failed to create group content directories", logger.Fields{"id": ws.ID, "error": mkErr})
		} else {
			h.provisionWorkspaceScaffolding(ctx, ws, folderWS, dirs.files, dirs.mcpRoots())
		}
		logger.Info("Group folder created on disk", logger.Fields{"id": ws.ID, "path": folderPath})
		return out, false
	}

	h.provisionWorkspaceScaffolding(ctx, ws, folderWS, folderPath, []string{folderPath})
	logger.Info("Workspace folder created on disk", logger.Fields{"id": ws.ID, "path": folderPath})

	if tc.wantsProject {
		out.projectWarning, out.attachErr = h.applyCreateWorkspaceTemplate(ctx, req, ws, folderWS, tc)
	}
	return out, false
}

// installTemplateDashboard copies the template's custom dashboard into the new
// workspace folder, so a workspace created from, say, Email Ops opens with that
// template's dashboard already attached.
//
// Best-effort by design: a workspace is perfectly usable without a dashboard,
// so a copy failure is logged and never fails creation or surfaces a warning
// the user cannot act on. The dashboard is re-read from disk on every page
// load, so a user who fixes or adds the files later needs no repair step.
func (h *Handler) installTemplateDashboard(workspaceID, folderPath string, tc createTemplateContext) {
	if h == nil || !tc.resolved || !tc.template.HasDashboard || strings.TrimSpace(folderPath) == "" {
		return
	}
	installed, err := projecttemplates.InstallDashboard(tc.template.Path, folderPath)
	switch {
	case err != nil:
		logger.Warn("Template dashboard was not installed", logger.Fields{
			"id": workspaceID, "template": tc.template.ID, "error": err.Error(),
		})
	case installed:
		logger.Info("Template dashboard installed", logger.Fields{
			"id": workspaceID, "template": tc.template.ID,
		})
	}
}

// applyCreateWorkspaceTemplate applies the selected project template to a
// freshly-provisioned (non-group) workspace folder: direct skeleton
// instantiation plus the template's default tool bindings. A legacy
// `onboarding` block in the manifest is ignored (the intake engine was
// replaced by setup starter tasks). Scaffolding is never fatal — a failure is
// reported via the returned warning. An attached existing project replaces
// scaffolding and is the exception: its failure is returned as attachErr.
func (h *Handler) applyCreateWorkspaceTemplate(ctx context.Context, req createWorkspaceRequest, ws *session.Workspace, folderWS *agentworkspace.Workspace, tc createTemplateContext) (projectWarning string, attachErr error) {
	switch {
	case tc.attach != nil:
		// The user's folder is referenced in place; nothing is copied into it.
		if err := h.recordCreateWorkspaceAttachedProject(ctx, ws, folderWS, tc.attach); err != nil {
			return "", err
		}
	case tc.resolved && !tc.template.HasSkeleton:
		// Metadata-only template (no files): there is no project to
		// scaffold by design. Its behavior/tools/starter-tasks still
		// apply; skip instantiation without surfacing a warning.
		logger.Info("Metadata-only template: skipping project scaffold", logger.Fields{"id": ws.ID, "template": tc.template.ID})
	default:
		// Non-fatal by design: a failed instantiation must not fail
		// workspace creation. The warning is surfaced to the user.
		result, err := h.instantiateWorkspaceProject(ctx, ws, folderWS, req.TemplateID, req.TemplatePath, req.ProjectName, instantiateWithInputs(tc.inputs))
		if err != nil {
			if tc.resolveErr != nil {
				err = tc.resolveErr
			}
			projectWarning = fmt.Sprintf("workspace was created, but the project template was not applied: %v", err)
			logger.Warn("Project template instantiation failed", logger.Fields{"id": ws.ID, "error": err})
		} else if result.ProjectWarning != "" {
			projectWarning = result.ProjectWarning
			logger.Warn("Project entry was not persisted", logger.Fields{"id": ws.ID, "warning": result.ProjectWarning})
		}
	}

	// Bind the template's declared default tools (skills / MCP /
	// plugins) onto the workspace, independent of onboarding. Tools
	// not present on the machine are skipped and noted; never fatal.
	if tc.resolved && !tc.template.Tools.IsEmpty() && h.applyTemplateTools != nil {
		// The applier binds through the read store and persists itself.
		applied, missing := h.applyTemplateTools(ws.ID, tc.template.Tools)
		if len(applied) > 0 {
			logger.Info("Applied template default tools", logger.Fields{"id": ws.ID, "applied": applied})
		}
		if len(missing) > 0 {
			logger.Info("Template default tools not found (skipped)", logger.Fields{"id": ws.ID, "missing": missing})
			warn := fmt.Sprintf("some template tools were not found and were skipped: %s", strings.Join(missing, ", "))
			if projectWarning == "" {
				projectWarning = warn
			} else {
				projectWarning = projectWarning + "; " + warn
			}
		}
	}

	return projectWarning, nil
}

// persistCreateWorkspaceTemplateProvenance records the template a workspace
// was created from onto its portable workspace.json. Runtime providers identify
// origin from this rather than scanning filenames or task prose. Best-effort: a
// failure never fails creation.
//
// Must run after starter-task seeding, not from inside
// applyCreateWorkspaceTemplate. h.workspaceTaskStore is a SyncStore whose
// primary is the SQLite-backed session store; session.Workspace has no
// TemplateProvenance column, so every Update on this store round-trips
// through a conversion that silently drops it before re-saving to disk. Any
// later Update on the same workspace id — starter-task seeding runs right
// after template application — would clobber a provenance write made here
// earlier. Doing it last avoids that.
func (h *Handler) persistCreateWorkspaceTemplateProvenance(wsID string, tmpl projecttemplates.Template, resolved bool, snapshots ...*agentworkspace.GroupRequirementSnapshot) string {
	if !resolved || strings.TrimSpace(tmpl.ID) == "" || h.workspaceTaskStore == nil {
		return ""
	}
	var snapshot *agentworkspace.GroupRequirementSnapshot
	if len(snapshots) > 0 {
		snapshot = snapshots[0]
	}
	// Built-ins always record provenance. A user template records it too when it
	// declares a setup/runtime/program/group contract: without provenance those
	// reviewed requirements would silently disappear after creation.
	if !tmpl.Builtin && tmpl.PluginOwner == nil && !tmpl.HasSetupWizard() && !tmpl.HasRuntimeRequirements() && !hasManagedAssistantTeam(tmpl) && snapshot == nil {
		return ""
	}
	prov := newTemplateProvenance(tmpl, snapshot)
	// Provenance and the blueprint's declared capability installs are written in
	// ONE update, so a workspace can never end up recorded as coming from the
	// File Janitor blueprint while lacking the capability that blueprint exists
	// to install (FR-32, FR-34).
	//
	// This runs after the folder-provisioning writes rather than on the struct
	// they share: creation threads one workspace value through several
	// whole-record workspace.json writes, and anything set on it earlier is
	// overwritten by the last one. See
	// tasks/trace-installed-capabilities-persistence.md (H5).
	installedAt := time.Now()
	if len(tmpl.Capabilities) > 0 && h.templateCapabilityService == nil {
		return "workspace was created, but blueprint capabilities are unavailable; retry setup after restoring the provider"
	}
	var registry *workspacecapability.Registry
	if h.templateCapabilityService != nil {
		registry = h.templateCapabilityService.Registry()
	}
	if err := h.workspaceTaskStore.Update(wsID, func(w *agentworkspace.Workspace) error {
		w.SetTemplateProvenance(prov)
		for _, capability := range tmpl.Capabilities {
			if registry == nil {
				return fmt.Errorf("capability registry is unavailable")
			}
			def, known := registry.Definition(capability.ID)
			if !known {
				return fmt.Errorf("capability %q is unavailable", capability.ID)
			}
			source := capability.Source
			if strings.TrimSpace(source) == "" {
				source = agentworkspace.InstallSourceBlueprint
			}
			record := agentworkspace.InstalledCapability{
				ID: def.ID, Version: def.Version, InstalledAt: installedAt, Source: source,
			}
			if def.Owner != nil {
				owner := def.Owner.Clone()
				record.Owner = &owner
			}
			if _, err := w.AddInstalledCapability(record); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		logger.Warn("Failed to persist template provenance and capabilities", logger.Fields{"id": wsID, "template": tmpl.ID, "error": err})
		return "workspace was created, but blueprint setup is incomplete; retry setup after restoring the capability provider"
	}
	for _, capability := range tmpl.Capabilities {
		if _, err := h.templateCapabilityService.Install(workspacecapability.InstallRequest{
			WorkspaceID: wsID, CapabilityID: capability.ID, Source: capability.Source,
		}); err != nil {
			logger.Warn("Blueprint capability components were not reconciled", logger.Fields{
				"id": wsID, "template": tmpl.ID, "capability": capability.ID, "error": err,
			})
			return "workspace was created, but blueprint setup is incomplete; retry setup from the capability catalog"
		}
	}
	return ""
}

func hasManagedAssistantTeam(tmpl projecttemplates.Template) bool {
	return tmpl.HasAssistantProgram() || tmpl.HasAssistantProject()
}

func newTemplateProvenance(tmpl projecttemplates.Template, snapshot *agentworkspace.GroupRequirementSnapshot) *agentworkspace.TemplateProvenance {
	version := tmpl.BuiltinVersion
	assistantProgram := tmpl.AssistantProgram
	var projectRoles []agentworkspace.AssistantProgramRoleSpec
	if tmpl.AssistantProject != nil {
		assistantProgram = tmpl.ResolvedAssistantHome
		projectRoles = tmpl.AssistantProject.ProgramRoles()
	}
	if tmpl.PluginOwner != nil {
		version = tmpl.PluginOwner.BlueprintVersion
	} else if snapshot != nil && snapshot.SourcePlugin != nil {
		version = snapshot.SourcePlugin.BlueprintVersion
	}
	provenance := &agentworkspace.TemplateProvenance{
		TemplateID: tmpl.ID, TemplateName: tmpl.Name, Builtin: tmpl.Builtin, Version: version,
		AppliedAt: time.Now(), PluginOwner: tmpl.PluginOwner,
		// Setup requirements stay unresolved: this snapshot chooses no path,
		// registers no watcher, enables no schedule, and grants no capability.
		DirectoryRequirements:  tmpl.DirectoryRequirements,
		AutomationRecipes:      tmpl.AutomationRecipes,
		CapabilityRequirements: tmpl.CapabilityRequirements,
		Plugins:                tmpl.Tools.Plugins, PluginSources: tmpl.Tools.PluginSources,
		RuntimeRequirements: tmpl.RuntimeRequirements, SetupWizard: tmpl.SetupWizard,
		AssistantProgram: assistantProgram, AssistantProjectRoles: projectRoles, GroupRequirement: snapshot,
	}
	if tmpl.UserSetupQuest != nil && tmpl.UserSetupQuest.Declaration != nil {
		provenance.UserTemplateOwner = &agentworkspace.UserTemplateOwner{
			TemplateID: tmpl.ID, AttachmentID: tmpl.UserSetupQuest.AttachmentID,
			QuestID:          tmpl.UserSetupQuest.Declaration.ID,
			DefinitionDigest: projecttemplates.UserSetupQuestDefinitionDigest(tmpl.UserSetupQuest),
			ExecutionDigest:  projecttemplates.UserSetupQuestExecutionDigest(tmpl),
		}
	}
	return provenance
}

// isWorkspaceRootLocation reports whether a create's location puts the new
// folder directly in the workspace root: no location, or the root itself.
func (h *Handler) isWorkspaceRootLocation(location string) bool {
	location = strings.TrimSpace(location)
	if location == "" {
		return true
	}
	if h.workspaceRootResolver != nil && workspacePathsEqual(location, h.workspaceRootResolver()) {
		return true
	}
	return h.workspaceStore != nil && workspacePathsEqual(location, h.workspaceStore.BasePath())
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func workspacePathsEqual(a, b string) bool {
	if strings.TrimSpace(a) == "" || strings.TrimSpace(b) == "" {
		return false
	}

	absA, err := filepath.Abs(strings.TrimSpace(a))
	if err != nil {
		absA = strings.TrimSpace(a)
	}
	absB, err := filepath.Abs(strings.TrimSpace(b))
	if err != nil {
		absB = strings.TrimSpace(b)
	}

	if runtime.GOOS == "windows" {
		return strings.EqualFold(filepath.Clean(absA), filepath.Clean(absB))
	}

	return filepath.Clean(absA) == filepath.Clean(absB)
}

func parseWorkspaceKind(value string) (session.WorkspaceKind, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return session.WorkspaceKindWorkspace, nil
	}

	switch session.WorkspaceKind(trimmed) {
	case session.WorkspaceKindWorkspace:
		return session.WorkspaceKindWorkspace, nil
	case session.WorkspaceKindGroup:
		return session.WorkspaceKindGroup, nil
	default:
		return "", fmt.Errorf("invalid workspace kind %q", trimmed)
	}
}

// pruneHiddenWorkspaces removes workspaces that have been moved to the trash
// or whose folder is missing from disk, recursing into children so hidden
// sub-workspaces don't leak into the tree.
func pruneHiddenWorkspaces(workspaces []session.Workspace) []session.Workspace {
	if len(workspaces) == 0 {
		return workspaces
	}

	filtered := make([]session.Workspace, 0, len(workspaces))
	for _, ws := range workspaces {
		if ws.Status == session.WorkspaceStatusTrashed || ws.Status == session.WorkspaceStatusMissing {
			continue
		}
		ws.Children = pruneHiddenWorkspaces(ws.Children)
		filtered = append(filtered, ws)
	}
	return filtered
}

// requireWorkspace validates that workspaceID, when provided, refers to an
// existing workspace of any kind (groups hold sessions, notes, and direct work
// just like concrete workspaces).
func (h *Handler) requireWorkspace(ctx context.Context, workspaceID string) (*session.Workspace, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return nil, nil
	}

	return h.store.GetWorkspace(ctx, workspaceID)
}

// getWorkspace handles GET /api/workspaces/{id}.
func (h *Handler) getWorkspace(w http.ResponseWriter, r *http.Request, id string) {
	workspace, err := h.store.GetWorkspace(r.Context(), id)
	if err == session.ErrWorkspaceNotFound {
		_ = orihttp.RespondNotFound(w, "Workspace not found")
		return
	}
	if err != nil {
		logger.Error("Failed to get workspace", logger.Fields{"id": id, "error": err})
		_ = orihttp.RespondInternalError(w, "Failed to get workspace")
		return
	}
	workspace = h.hydrateWorkspaceMetadataFromFileStore(workspace)

	orihttp.WriteJSON(w, h.buildWorkspaceDetailResponse(workspace))
}

// updateWorkspace handles PUT/PATCH /api/workspaces/{id}.
func (h *Handler) updateWorkspace(w http.ResponseWriter, r *http.Request, id string) {
	workspace, err := h.store.GetWorkspace(r.Context(), id)
	if err == session.ErrWorkspaceNotFound {
		_ = orihttp.RespondNotFound(w, "Workspace not found")
		return
	}
	if err != nil {
		_ = orihttp.RespondInternalError(w, "Failed to get workspace")
		return
	}

	var req struct {
		Name               *string                    `json:"name,omitempty"`
		Description        *string                    `json:"description,omitempty"`
		ParentID           *string                    `json:"parent_id,omitempty"`
		OrderIndex         *int                       `json:"order_index,omitempty"`
		Color              *string                    `json:"color,omitempty"`
		ProjectPath        *string                    `json:"project_path,omitempty"`
		Tags               *[]string                  `json:"tags,omitempty"`
		PrimaryDirectoryID *string                    `json:"primary_directory_id,omitempty"`
		WorkspaceBootstrap *workspaceBootstrapRequest `json:"workspace_bootstrap,omitempty"`
	}

	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	// Reject a blank name up front, before any other field is applied, so a
	// 400 here never leaves a partial mutation - matching /rename's
	// "name is required" behavior for the same field.
	var trimmedName string
	if req.Name != nil {
		trimmedName = strings.TrimSpace(*req.Name)
		if trimmedName == "" {
			_ = orihttp.RespondBadRequest(w, "name is required")
			return
		}
	}

	h.hydrateWorkspaceMetadataInto(workspace)

	// Apply partial updates. The "name" field is handled last, via the same
	// renameWorkspace helper POST .../rename uses: it moves the backing
	// folder and keeps SQLite's FolderSlug in sync with it, closing the gap
	// that used to rewrite FolderSlug here without ever touching disk.
	if req.Description != nil {
		workspace.Description = *req.Description
	}
	if req.Description != nil || req.WorkspaceBootstrap != nil {
		bootstrapData := mergeWorkspaceBootstrapForUpdate(
			workspace.SharedData,
			workspace.Description,
			req.Description != nil,
			req.WorkspaceBootstrap,
		)
		if bootstrapData != nil {
			if workspace.SharedData == nil {
				workspace.SharedData = make(map[string]any)
			}
			workspace.SharedData["workspace_bootstrap"] = bootstrapData
		} else if workspace.SharedData != nil {
			delete(workspace.SharedData, "workspace_bootstrap")
		}
	}
	if req.ProjectPath != nil {
		workspace.ProjectPath = *req.ProjectPath
	}
	if req.Tags != nil {
		tags, err := agentworkspace.ValidateWorkspaceTags(*req.Tags)
		if err != nil {
			_ = orihttp.RespondBadRequest(w, err.Error())
			return
		}
		workspace.Tags = tags
	}
	if req.PrimaryDirectoryID != nil {
		setWorkspacePrimaryDirectoryID(workspace, *req.PrimaryDirectoryID)
	}
	if req.ParentID != nil {
		newParentID := strings.TrimSpace(*req.ParentID)
		if newParentID != workspace.ParentID {
			// Self-parent guard.
			if newParentID == workspace.ID {
				_ = orihttp.RespondBadRequest(w, "Workspace cannot be its own parent")
				return
			}
			// Cycle guard: cannot move under one of our own descendants.
			if newParentID != "" {
				descendants, err := h.store.GetSubworkspaceIDs(r.Context(), workspace.ID)
				if err != nil {
					logger.Error("Failed to load workspace descendants", logger.Fields{"id": id, "error": err})
					_ = orihttp.RespondInternalError(w, "Failed to update workspace")
					return
				}
				for _, descendantID := range descendants {
					if descendantID == newParentID {
						_ = orihttp.RespondBadRequest(w, "Workspace cannot be moved under its descendant")
						return
					}
				}
			}
			// Destination must be a group; an empty parent moves to the root (ungroup).
			if err := h.requireGroupParent(r.Context(), newParentID); err != nil {
				handleWorkspaceParentError(w, err)
				return
			}
			// Eligibility: only managed workspaces can be grouped. A workspace
			// linked to an external folder can't be physically nested (req 23).
			if newParentID != "" && isFolderImportedWorkspace(*workspace) {
				_ = orihttp.RespondBadRequest(w, "This workspace is linked to an external folder and can't be grouped. Rebind it into the managed workspaces root first.")
				return
			}
			// Active-work hard block: never move a workspace (or, for a group,
			// any workspace nested inside it) while it has in-flight work (req 12).
			if blocker, err := h.firstActiveWorkBlocker(r.Context(), workspace.ID); err != nil {
				logger.Error("Failed to check workspace active work", logger.Fields{"id": id, "error": err})
				_ = orihttp.RespondInternalError(w, "Failed to update workspace")
				return
			} else if blocker != "" {
				_ = orihttp.RespondConflict(w, fmt.Sprintf("Stop the running task in %q before grouping this workspace.", blocker))
				return
			}
			// Physically move the folder tree when a folder store is available;
			// disk location is the source of truth for grouping. Falls back to a
			// metadata-only parent change when no folder store is configured.
			if h.workspaceStore != nil {
				moved, err := h.workspaceStore.MoveWorkspaceFolder(workspace.ID, newParentID)
				if err != nil {
					handleWorkspaceMoveError(w, err)
					return
				}
				h.applyMoveReferenceUpdates(r.Context(), workspace, moved)
				workspace.ParentID = newParentID
				// Persist the move immediately: the folder already changed on
				// disk, so SQLite's parent_id must agree with it right away.
				// Without this, a later rename failure in this same request
				// would roll back only the name/slug fields, leaving SQLite
				// pointing at the pre-move parent while disk already moved.
				if err := h.store.UpdateWorkspace(r.Context(), workspace); err != nil {
					logger.Error("Failed to persist workspace parent move", logger.Fields{"id": id, "error": err})
					_ = orihttp.RespondInternalError(w, "Failed to update workspace")
					return
				}
			} else {
				workspace.ParentID = newParentID
			}
		}
	}
	if req.OrderIndex != nil {
		workspace.OrderIndex = *req.OrderIndex
	}
	if req.Color != nil {
		workspace.Color = *req.Color
	}

	if req.Name != nil {
		if err := h.renameWorkspace(r.Context(), workspace, trimmedName, ""); err != nil {
			h.writeWorkspaceRenameError(w, r.Context(), trimmedName, id, err)
			return
		}
		// renameWorkspace already synced workspace.json (unconditionally, even
		// when the slug didn't change), so no further sync is needed here.
	} else {
		if err := h.store.UpdateWorkspace(r.Context(), workspace); err != nil {
			logger.Error("Failed to update workspace", logger.Fields{"id": id, "error": err})
			_ = orihttp.RespondInternalError(w, "Failed to update workspace")
			return
		}
		if req.ParentID == nil {
			if err := h.syncWorkspacePortableStateToFileStore(workspace); err != nil {
				logger.Warn("Failed to sync workspace.json after workspace update", logger.Fields{"id": id, "error": err})
			}
		} else if req.Tags != nil {
			if err := h.syncWorkspaceTagsToFileStore(workspace); err != nil {
				logger.Warn("Failed to sync workspace tags after workspace update", logger.Fields{"id": id, "error": err})
			}
		}
	}

	logger.Info("Workspace updated", logger.Fields{"id": id})

	hydrated := h.hydrateWorkspaceMetadataFromFileStore(workspace)
	orihttp.WriteJSON(w, map[string]any{
		"success": true,
		"folder":  hydrated,
	})
}

// deleteWorkspace handles DELETE /api/workspaces/{id}.
// Query params:
//   - delete_sessions=true: also delete all sessions belonging to this workspace.
//     If false or absent, sessions are unlinked (workspace_id set to NULL).
//   - confirm=true: required to proceed with deletion (if absent, returns session count for confirmation).
func (h *Handler) deleteWorkspace(w http.ResponseWriter, r *http.Request, id string) {
	ctx := r.Context()

	// Check workspace exists
	ws, err := h.store.GetWorkspace(ctx, id)
	if err == session.ErrWorkspaceNotFound {
		_ = orihttp.RespondNotFound(w, "Workspace not found")
		return
	}
	if err != nil {
		logger.Error("Failed to get workspace", logger.Fields{"id": id, "error": err})
		_ = orihttp.RespondInternalError(w, "Failed to delete workspace")
		return
	}

	// Review assistant topology before confirmations or any session, folder, or
	// group mutation. Folder-only guards cannot see DB-only or divergent Homes.
	if !h.allowWorkspaceRemoval(w, r, ws) {
		return
	}

	// If confirm is not set, return session count for UI confirmation prompt
	if r.URL.Query().Get("confirm") != "true" {
		sessionCount := ws.SessionCount
		orihttp.WriteJSON(w, map[string]any{
			"workspace_id":     id,
			"name":             ws.Name,
			"session_count":    sessionCount,
			"confirm_required": true,
			"message":          fmt.Sprintf("Workspace %q has %d sessions. Delete the workspace?", ws.Name, sessionCount),
		})
		return
	}

	deleteSessions := r.URL.Query().Get("delete_sessions") == "true"
	if h.workspaceStore != nil {
		if canonical, canonicalErr := h.workspaceStore.Get(id); canonicalErr == nil && canonical != nil {
			if canonical.GetAssistantProgramState() != nil || canonical.GetAssistantProjectLink() != nil {
				_ = orihttp.RespondJSON(w, http.StatusConflict, map[string]any{
					"error": "Assistant Program membership must be reviewed before deleting this workspace.",
					"group_requirement": map[string]any{
						"state": "group_requirement_unfulfilled", "actions": []string{"open_guided_setup"},
					},
				})
				return
			}
			if agentworkspace.HasRequiredGroupRequirement(canonical) {
				status := agentworkspace.EvaluateGroupRequirementLifecycle(canonical, h.workspaceStore.Get)
				_ = orihttp.RespondJSON(w, http.StatusConflict, map[string]any{
					"error":             "This Required template contract needs an explicit lifecycle review before deletion.",
					"group_requirement": status,
				})
				return
			}
		}
	}

	// Groups physically contain their members, so deletion has its own two-mode
	// flow (delete contents vs un-nest members to the root, then remove the
	// empty group). Handle it separately from regular workspaces.
	if ws.Kind == session.WorkspaceKindGroup {
		h.deleteGroup(w, r, ws, deleteSessions)
		return
	}

	// Soft delete (default): move the folder-backed workspace to the system
	// trash and mark it trashed so it can be restored from the hub. Explicit
	// delete_sessions=true requests, and platforms without system-trash
	// support fall through to a permanent delete below.
	if ws.Kind != session.WorkspaceKindGroup && !deleteSessions && h.workspaceStore != nil && platform.TrashSupported() {
		if _, ferr := h.workspaceStore.Get(id); ferr == nil {
			if err := h.trashWorkspace(ctx, ws); err != nil {
				logger.Error("Failed to move workspace to trash", logger.Fields{"id": id, "error": err})
				_ = orihttp.RespondInternalError(w, "Failed to move workspace to trash")
				return
			}
			logger.Info("Workspace moved to trash", logger.Fields{"id": id})
			orihttp.WriteJSON(w, map[string]any{"success": true, "id": id, "trashed": true})
			return
		}
	}

	// Capture the entry agent name before deletion so it can be cleaned up.
	entryAgentName := ""
	if h.workspaceStore != nil && ws.Kind != session.WorkspaceKindGroup {
		if folderWS, ferr := h.workspaceStore.Get(id); ferr == nil && folderWS != nil {
			entryAgentName = strings.TrimSpace(folderWS.EntryAgentName())
		}
	}

	// Handle session cleanup
	if deleteSessions {
		if err := h.store.DeleteSessionsByWorkspace(ctx, id); err != nil {
			logger.Error("Failed to delete sessions for workspace", logger.Fields{"id": id, "error": err})
			_ = orihttp.RespondInternalError(w, "Failed to delete workspace sessions")
			return
		}
	} else {
		if err := h.store.UnlinkSessionsFromWorkspace(ctx, id); err != nil {
			logger.Error("Failed to unlink sessions from workspace", logger.Fields{"id": id, "error": err})
			_ = orihttp.RespondInternalError(w, "Failed to unlink workspace sessions")
			return
		}
	}

	// Delete the workspace
	if err := h.store.DeleteWorkspace(ctx, id); err != nil {
		logger.Error("Failed to delete workspace", logger.Fields{"id": id, "error": err})
		_ = orihttp.RespondInternalError(w, "Failed to delete workspace")
		return
	}

	// Also delete from folder-based store if available
	if h.workspaceStore != nil && ws.Kind != session.WorkspaceKindGroup {
		if err := h.workspaceStore.Delete(id); err != nil {
			logger.Warn("Failed to delete workspace folder", logger.Fields{"id": id, "error": err})
			// Non-fatal: SQLite deletion succeeded
		}
	}

	// Delete the workspace's entry agent so it no longer lingers in the agent
	// store after its parent workspace is gone. Non-fatal on failure.
	if entryAgentName != "" && h.agentStore != nil {
		if _, exists := h.agentStore.GetAgent(entryAgentName); exists {
			if err := h.agentStore.DeleteAgent(entryAgentName); err != nil {
				logger.Warn("Failed to delete workspace entry agent", logger.Fields{
					"workspace_id": id,
					"agent":        entryAgentName,
					"error":        err,
				})
			} else {
				logger.Info("Deleted workspace entry agent", logger.Fields{
					"workspace_id": id,
					"agent":        entryAgentName,
				})

				// Purge any sessions that still reference the now-deleted entry
				// agent so the UI cannot restore stale state that resolves to a
				// 404 on /api/agents. Mirrors the DELETE /api/agents path.
				// Non-fatal on failure.
				if n, perr := h.store.DeleteSessionsByAgent(ctx, entryAgentName); perr != nil {
					logger.Warn("Failed to purge sessions for deleted entry agent", logger.Fields{
						"workspace_id": id,
						"agent":        entryAgentName,
						"error":        perr,
					})
				} else if n > 0 {
					logger.Info("Purged sessions for deleted entry agent", logger.Fields{
						"workspace_id": id,
						"agent":        entryAgentName,
						"count":        n,
					})
				}
			}
		}
	}

	logger.Info("Workspace deleted", logger.Fields{"id": id, "delete_sessions": deleteSessions})

	orihttp.RespondNoContent(w)
}

// listWorkspaces handles GET /api/workspaces.
func (h *Handler) listWorkspaces(w http.ResponseWriter, r *http.Request) {
	tree := r.URL.Query().Get("tree") == "true"

	if tree {
		workspaces, err := h.store.GetWorkspaceTree(r.Context())
		if err != nil {
			// Don't log context canceled - it's normal when client disconnects
			if errors.Is(err, context.Canceled) {
				return
			}
			logger.Error("Failed to get workspace tree", logger.Fields{"error": err})
			_ = orihttp.RespondInternalError(w, "Failed to get workspaces")
			return
		}
		workspaces = pruneHiddenWorkspaces(workspaces)
		workspaces = h.hydrateWorkspaceListFromFileStore(workspaces)

		orihttp.WriteJSON(w, map[string]any{
			"folders":    workspaces,
			"workspaces": workspaces,
		})
		return
	}

	workspaces, err := h.store.ListWorkspaces(r.Context())
	if err != nil {
		// Don't log context canceled - it's normal when client disconnects
		if errors.Is(err, context.Canceled) {
			return
		}
		logger.Error("Failed to list workspaces", logger.Fields{"error": err})
		_ = orihttp.RespondInternalError(w, "Failed to list workspaces")
		return
	}

	workspaces = pruneHiddenWorkspaces(workspaces)
	workspaces = h.hydrateWorkspaceListFromFileStore(workspaces)

	orihttp.WriteJSON(w, map[string]any{
		"folders":    workspaces,
		"workspaces": workspaces,
	})
}

func (h *Handler) hydrateWorkspaceListFromFileStore(workspaces []session.Workspace) []session.Workspace {
	if len(workspaces) == 0 {
		return workspaces
	}

	hydrated := make([]session.Workspace, len(workspaces))
	for i := range workspaces {
		hydrated[i] = workspaces[i]
		hydrated[i].Children = h.hydrateWorkspaceListFromFileStore(workspaces[i].Children)
		h.hydrateWorkspaceMetadataInto(&hydrated[i])
	}

	return hydrated
}

func (h *Handler) hydrateWorkspaceMetadataFromFileStore(workspace *session.Workspace) *session.Workspace {
	if workspace == nil {
		return nil
	}

	copy := *workspace
	h.hydrateWorkspaceMetadataInto(&copy)
	return &copy
}

func (h *Handler) hydrateWorkspaceMetadataInto(workspace *session.Workspace) {
	if h == nil || h.workspaceStore == nil || workspace == nil {
		return
	}

	diskWorkspace, err := h.workspaceStore.Get(workspace.ID)
	if err != nil || diskWorkspace == nil {
		return
	}

	fallback := session.ConvertAgentWorkspace(diskWorkspace)
	if fallback == nil {
		return
	}

	if strings.TrimSpace(workspace.FolderSlug) == "" {
		workspace.FolderSlug = fallback.FolderSlug
	}
	// project_path has no SQLite column: workspace.json is its canonical
	// store, so reads always hydrate it from disk.
	if strings.TrimSpace(workspace.ProjectPath) == "" {
		workspace.ProjectPath = fallback.ProjectPath
	}
	// designation has no SQLite column either: workspace.json (via the
	// personalhq sync) is canonical, so reads always hydrate it from disk.
	if strings.TrimSpace(string(workspace.Designation)) == "" {
		workspace.Designation = fallback.Designation
	}
	// len()==0 rather than nil: SQLite deserializes the '[]' column default to
	// an empty non-nil slice, which must not shadow tags that live only in
	// workspace.json (e.g. a workspace imported from another machine).
	if len(workspace.Tags) == 0 {
		workspace.Tags = append([]string(nil), fallback.Tags...)
	}
	if workspace.SharedData == nil && fallback.SharedData != nil {
		workspace.SharedData = fallback.SharedData
	}
	mergeWorkspaceJSONField(&workspace.DirectoryReferencesJSON, fallback.DirectoryReferencesJSON)
	mergeWorkspaceJSONField(&workspace.MCPBindingsJSON, fallback.MCPBindingsJSON)
	mergeWorkspaceJSONField(&workspace.AgentMCPAccessJSON, fallback.AgentMCPAccessJSON)
	mergeWorkspaceJSONField(&workspace.SkillBindingsJSON, fallback.SkillBindingsJSON)
	mergeWorkspaceJSONField(&workspace.AgentSkillAccessJSON, fallback.AgentSkillAccessJSON)
	// installed_capabilities has a SQLite column, so most reads already carry
	// it. Hydration still matters for a row written before the column existed
	// and for a workspace folder imported from another machine: workspace.json
	// remains canonical, and a SQLite row that says nothing must not be
	// reported to the UI as "File Janitor is not installed".
	mergeWorkspaceJSONField(&workspace.InstalledCapabilitiesJSON, fallback.InstalledCapabilitiesJSON)

	// Map-view summary fields (agent roster, task/tool/skill counts, ops mode,
	// active flag) are always recomputed from the disk workspace rather than
	// filled-if-empty like the fields above — they're display-only derived
	// state, not data that could already be populated from another source.
	mapFields := agentworkspace.ComputeMapSummaryFields(diskWorkspace)
	workspace.EntryAgentName = mapFields.EntryAgentName
	workspace.Agents = mapFields.AgentNames
	workspace.AgentCount = mapFields.AgentCount
	workspace.OpenTaskCount = mapFields.OpenTaskCount
	workspace.BacklogCount = mapFields.BacklogCount
	workspace.NeedsAttentionCount = mapFields.NeedsAttentionCount
	workspace.MCPCount = mapFields.MCPCount
	workspace.SkillCount = mapFields.SkillCount
	workspace.OpsMode = mapFields.OpsMode
	workspace.Active = mapFields.Active
	// Template type and provider are derived from the Home's own program state,
	// never from its editable name, so rename and source removal keep them.
	workspace.GroupTemplate = groupTemplateSummary(diskWorkspace)

	// Blueprint identity is inert provenance from the canonical workspace
	// record. Expose only the stable ID and built-in ownership bit needed to
	// choose curated artwork; never infer identity from names or live activity.
	workspace.BlueprintID = ""
	workspace.BlueprintBuiltin = false
	if provenance := diskWorkspace.GetTemplateProvenance(); provenance != nil {
		workspace.BlueprintID = provenance.TemplateID
		workspace.BlueprintBuiltin = provenance.Builtin
	}
}

func mergeWorkspaceJSONField(target *json.RawMessage, fallback json.RawMessage) {
	if target == nil || len(*target) > 0 || len(fallback) == 0 {
		return
	}
	*target = append(json.RawMessage(nil), fallback...)
}

func workspacePrimaryDirectoryID(workspace *session.Workspace) string {
	if workspace == nil || workspace.SharedData == nil {
		return ""
	}

	raw, ok := workspace.SharedData[workspaceSharedDataPrimaryDirectoryIDKey]
	if !ok {
		return ""
	}

	value, _ := raw.(string)
	return strings.TrimSpace(value)
}

func setWorkspacePrimaryDirectoryID(workspace *session.Workspace, directoryID string) {
	if workspace == nil {
		return
	}

	trimmed := strings.TrimSpace(directoryID)
	if workspace.SharedData == nil {
		workspace.SharedData = make(map[string]any)
	}

	if trimmed == "" {
		delete(workspace.SharedData, workspaceSharedDataPrimaryDirectoryIDKey)
		return
	}

	workspace.SharedData[workspaceSharedDataPrimaryDirectoryIDKey] = trimmed
}

// renameWorkspace is the shared rename mechanism behind both POST
// .../rename and the generic update's "name" field. It computes the target
// slug, updates SQLite (rolling back the in-memory name/slug on failure),
// and - when the workspace is folder-tracked - renames the backing folder,
// rewrites path-keyed references for every moved descendant, and syncs
// workspace.json. On every return path SQLite and disk agree: a folder-level
// failure rolls SQLite back to the pre-rename name and slug before returning.
//
// ws is mutated in place (Name, FolderSlug, UpdatedAt) so a successful
// caller can respond with the same record. Errors are typed so a caller can
// build its own response: a *workspaceRenameConflictError means "409, folder
// or slug is already taken"; anything else is a 500, distinguished (if the
// caller cares) via errors.Is against errWorkspaceRenameSQLite/
// errWorkspaceRenameFolder/errWorkspaceRenameRollback.
func (h *Handler) renameWorkspace(ctx context.Context, ws *session.Workspace, name, requestedSlug string) error {
	oldName := ws.Name
	oldFolderSlug := ws.FolderSlug

	targetSlug := ""
	if trimmedSlug := strings.TrimSpace(requestedSlug); trimmedSlug != "" {
		targetSlug = agentworkspace.Slugify(trimmedSlug)
	}
	if targetSlug == "" {
		targetSlug = agentworkspace.Slugify(name)
	}
	if strings.TrimSpace(ws.ParentID) == "" && targetSlug != oldFolderSlug && agentworkspace.IsReservedTopLevelSlug(targetSlug) {
		return agentworkspace.ErrReservedWorkspaceSlug
	}

	ws.Name = name
	ws.FolderSlug = targetSlug
	ws.UpdatedAt = time.Now()

	if err := h.store.UpdateWorkspace(ctx, ws); err != nil {
		ws.Name = oldName
		ws.FolderSlug = oldFolderSlug
		if errors.Is(err, session.ErrWorkspaceSlugConflict) {
			parentDir := ""
			if h.workspaceStore != nil {
				if path, pathErr := h.workspaceStore.GetFolderPath(ws.ID); pathErr == nil {
					parentDir = filepath.Dir(path)
				}
			}
			return &workspaceRenameConflictError{targetSlug: targetSlug, parentDir: parentDir}
		}
		return fmt.Errorf("%w: %v", errWorkspaceRenameSQLite, err)
	}

	// Rename the backing folder when this workspace is tracked by the folder
	// store. This includes groups (a group is a folder that may physically
	// contain members); RenameWithSlug rewrites nested members' paths. DB-only
	// workspaces have no folder to rename and are skipped.
	folderTracked := false
	if h.workspaceStore != nil {
		if existing, getErr := h.workspaceStore.Get(ws.ID); getErr == nil && existing != nil {
			folderTracked = true
		}
	}
	if !folderTracked {
		return nil
	}

	moved, err := h.workspaceStore.RenameWithSlug(ws.ID, name, targetSlug)
	if err != nil {
		ws.Name = oldName
		ws.FolderSlug = oldFolderSlug
		ws.UpdatedAt = time.Now()
		if rollbackErr := h.store.UpdateWorkspace(ctx, ws); rollbackErr != nil {
			logger.Error("Failed to rollback workspace rename after folder rename error", logger.Fields{"id": ws.ID, "error": rollbackErr})
			return fmt.Errorf("%w: %v", errWorkspaceRenameRollback, rollbackErr)
		}

		var slugConflict *agentworkspace.FolderSlugConflictError
		if errors.As(err, &slugConflict) {
			return &workspaceRenameConflictError{targetSlug: targetSlug, parentDir: slugConflict.ParentDir}
		}

		logger.Error("Failed to rename workspace folder", logger.Fields{"id": ws.ID, "error": err})
		return fmt.Errorf("%w: %v", errWorkspaceRenameFolder, err)
	}

	// The folder (and any nested members) changed paths: rewrite path-keyed
	// references (directory references, MCP roots, project_path) and persist
	// them.
	if len(moved) > 0 {
		h.applyMoveReferenceUpdates(ctx, ws, moved)
		if err := h.store.UpdateWorkspace(ctx, ws); err != nil {
			logger.Warn("Failed to persist renamed workspace references", logger.Fields{"id": ws.ID, "error": err})
		}
	}
	// Sync workspace.json even when the slug did not change: RenameWithSlug
	// returns no moved paths for a display-name-only change, but the folder
	// still needs the new display name written into it.
	if err := h.syncWorkspacePortableStateToFileStore(ws); err != nil {
		logger.Warn("Failed to sync workspace.json after rename", logger.Fields{"id": ws.ID, "error": err})
	}

	return nil
}

// writeWorkspaceRenameError maps a renameWorkspace error to the HTTP response
// handleWorkspaceRename has always returned, so a rename through the generic
// update fails exactly the same way.
func (h *Handler) writeWorkspaceRenameError(w http.ResponseWriter, ctx context.Context, requestedName, excludeID string, err error) {
	var conflict *workspaceRenameConflictError
	if errors.As(err, &conflict) {
		writeWorkspaceCreateSlugConflict(w, requestedName, h.globalWorkspaceSlugConflict(ctx, conflict.targetSlug, excludeID, conflict.parentDir))
		return
	}
	switch {
	case errors.Is(err, agentworkspace.ErrReservedWorkspaceSlug):
		_ = orihttp.RespondBadRequest(w, agentworkspace.ReservedWorkspaceSlugMessage)
	case errors.Is(err, errWorkspaceRenameRollback):
		_ = orihttp.RespondInternalError(w, "Failed to rollback workspace rename")
	case errors.Is(err, errWorkspaceRenameFolder):
		_ = orihttp.RespondInternalError(w, "Failed to rename workspace folder")
	default:
		_ = orihttp.RespondInternalError(w, "Failed to rename workspace")
	}
}

// handleWorkspaceRename handles POST /api/workspaces/{id}/rename.
func (h *Handler) handleWorkspaceRename(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		_ = orihttp.RespondMethodNotAllowed(w)
		return
	}

	var req struct {
		Name       string `json:"name"`
		FolderSlug string `json:"folder_slug,omitempty"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	if req.Name == "" {
		_ = orihttp.RespondBadRequest(w, "name is required")
		return
	}

	ctx := r.Context()

	// Update in session store
	ws, err := h.store.GetWorkspace(ctx, id)
	if err == session.ErrWorkspaceNotFound {
		_ = orihttp.RespondNotFound(w, "Workspace not found")
		return
	}
	if err != nil {
		_ = orihttp.RespondInternalError(w, "Failed to get workspace")
		return
	}

	if err := h.renameWorkspace(ctx, ws, req.Name, req.FolderSlug); err != nil {
		h.writeWorkspaceRenameError(w, ctx, req.Name, id, err)
		return
	}

	logger.Info("Workspace renamed", logger.Fields{"id": id, "new_name": req.Name})

	orihttp.WriteJSON(w, map[string]any{
		"success": true,
		"folder":  ws,
	})
}

// =============================================================================
// Workspace Folder Import
// =============================================================================

type createWorkspaceImportRequest struct {
	Name               string                     `json:"name,omitempty"`
	FolderSlug         string                     `json:"folder_slug,omitempty"`
	WorkspacePreset    string                     `json:"workspace_preset,omitempty"`
	Description        string                     `json:"description,omitempty"`
	ParentID           string                     `json:"parent_id,omitempty"`
	OrderIndex         *int                       `json:"order_index,omitempty"`
	Color              string                     `json:"color,omitempty"`
	Path               string                     `json:"path"`
	AllowDuplicate     bool                       `json:"allow_duplicate,omitempty"`
	EntryPoint         string                     `json:"entry_point,omitempty"`
	EntryAgentName     string                     `json:"entry_agent_name,omitempty"`
	WorkspaceBootstrap *workspaceBootstrapRequest `json:"workspace_bootstrap,omitempty"`
	// ProjectConnection is decoded only so it can be refused: Import Folder
	// adopts a whole folder and never attaches a blueprint's project file.
	ProjectConnection json.RawMessage `json:"project_connection,omitempty"`
	// BlueprintInputs is decoded for the same reason: Import Folder scaffolds
	// nothing, so there is no file for a blueprint's values to be written into.
	BlueprintInputs json.RawMessage `json:"blueprint_inputs,omitempty"`
}

type workspaceImportDuplicate struct {
	Found         bool   `json:"found"`
	WorkspaceID   string `json:"workspace_id,omitempty"`
	WorkspaceSlug string `json:"workspace_slug,omitempty"`
	WorkspaceName string `json:"workspace_name,omitempty"`
	DirectoryID   string `json:"directory_id,omitempty"`
	Path          string `json:"path,omitempty"`
}

type workspaceCreateConflict struct {
	Type          string `json:"type"`
	RequestedSlug string `json:"requested_slug,omitempty"`
	SuggestedSlug string `json:"suggested_slug,omitempty"`
	Location      string `json:"location,omitempty"`
}

type workspaceSyncLocateRequest struct {
	ID   string `json:"id"`
	Path string `json:"path"`
}

// workspaceDirectoryReference is the session-side JSON shape for a workspace
// directory reference. It is an alias of agentworkspace.DirectoryReference —
// the two are field-identical — so the import and folder-sync paths share a
// single reference-rebase core (see workspace_reference_rebase.go).
type workspaceDirectoryReference = agentworkspace.DirectoryReference

type workspaceImportItem struct {
	Workspace  *agentworkspace.Workspace
	SourcePath string
}

// =============================================================================
// Workspace Agent Management
// =============================================================================

// =============================================================================
// Workspace Layout Management
// =============================================================================

// workspaceReconcileStats summarizes the outcome of a disk reconcile pass.
type workspaceReconcileStats struct {
	// Imported counts disk workspaces newly created in the session store.
	Imported int
	// Reparented counts session workspaces whose parent changed to match disk.
	Reparented int
	// Orphaned counts session workspaces marked missing because their folder
	// is gone from disk (or was recreated as a different workspace).
	Orphaned int
	// Restored counts previously-missing workspaces whose folder reappeared.
	Restored int
}

// workspaceRescanCooldown is the minimum interval between background-initiated
// disk reconciles (page loads); explicit rescans are exempt.
const workspaceRescanCooldown = 30 * time.Second

func workspaceBindingHasRoot(config map[string]any, path string) bool {
	if len(config) == 0 || strings.TrimSpace(path) == "" {
		return false
	}

	rawRoots, ok := config["roots"]
	if !ok || rawRoots == nil {
		return false
	}

	switch roots := rawRoots.(type) {
	case []string:
		for _, root := range roots {
			if cleanWorkspaceSyncPath(root) == path {
				return true
			}
		}
	case []any:
		for _, root := range roots {
			if cleanWorkspaceSyncPath(fmt.Sprint(root)) == path {
				return true
			}
		}
	}

	return false
}

func decodeWorkspaceMCPBindings(raw json.RawMessage) ([]agentworkspace.MCPBinding, error) {
	if len(raw) == 0 {
		return []agentworkspace.MCPBinding{}, nil
	}
	var bindings []agentworkspace.MCPBinding
	if err := json.Unmarshal(raw, &bindings); err != nil {
		return nil, err
	}
	return bindings, nil
}

func buildFileStoreWorkspace(workspace *session.Workspace) (*agentworkspace.Workspace, error) {
	if workspace == nil {
		return nil, fmt.Errorf("workspace is required")
	}

	folderWS := &agentworkspace.Workspace{
		ID:             workspace.ID,
		Name:           workspace.Name,
		Kind:           string(workspace.Kind),
		Description:    workspace.Description,
		FolderSlug:     workspace.FolderSlug,
		ProjectPath:    workspace.ProjectPath,
		Tags:           append([]string(nil), workspace.Tags...),
		ParentID:       workspace.ParentID,
		AgentInstances: toWorkspaceAgentInstances(workspace.AgentInstances),
		SharedData:     workspace.SharedData,
		Status:         agentworkspace.WorkspaceStatus(workspace.Status),
		CreatedAt:      workspace.CreatedAt,
		UpdatedAt:      workspace.UpdatedAt,
	}

	if folderWS.Status == "" {
		folderWS.Status = agentworkspace.StatusActive
	}

	if workspace.Layout != nil {
		layoutData, err := json.Marshal(workspace.Layout)
		if err != nil {
			return nil, fmt.Errorf("failed to encode workspace layout: %w", err)
		}
		var layout agentworkspace.CanvasLayout
		if err := json.Unmarshal(layoutData, &layout); err != nil {
			return nil, fmt.Errorf("failed to decode workspace layout: %w", err)
		}
		folderWS.Layout = &layout
	}

	if err := decodeSessionWorkspaceJSONField(workspace.MessagesJSON, &folderWS.Messages); err != nil {
		return nil, fmt.Errorf("failed to decode workspace messages: %w", err)
	}
	if err := decodeSessionWorkspaceJSONField(workspace.TasksJSON, &folderWS.Tasks); err != nil {
		return nil, fmt.Errorf("failed to decode workspace tasks: %w", err)
	}
	if err := decodeSessionWorkspaceJSONField(workspace.AttachmentsJSON, &folderWS.Attachments); err != nil {
		return nil, fmt.Errorf("failed to decode workspace attachments: %w", err)
	}
	if err := decodeSessionWorkspaceJSONField(workspace.ScheduledTasksJSON, &folderWS.ScheduledTasks); err != nil {
		return nil, fmt.Errorf("failed to decode workspace schedules: %w", err)
	}
	if err := decodeSessionWorkspaceJSONField(workspace.StoreNodesJSON, &folderWS.StoreNodes); err != nil {
		return nil, fmt.Errorf("failed to decode workspace store nodes: %w", err)
	}
	if err := decodeSessionWorkspaceJSONField(workspace.WorkflowsJSON, &folderWS.Workflows); err != nil {
		return nil, fmt.Errorf("failed to decode workspace workflows: %w", err)
	}
	if err := decodeSessionWorkspaceJSONField(workspace.DirectoryReferencesJSON, &folderWS.DirectoryReferences); err != nil {
		return nil, fmt.Errorf("failed to decode workspace directory references: %w", err)
	}
	if err := decodeSessionWorkspaceJSONField(workspace.MCPBindingsJSON, &folderWS.MCPBindings); err != nil {
		return nil, fmt.Errorf("failed to decode workspace MCP bindings: %w", err)
	}
	if err := decodeSessionWorkspaceJSONField(workspace.AgentMCPAccessJSON, &folderWS.AgentMCPAccess); err != nil {
		return nil, fmt.Errorf("failed to decode workspace agent MCP access: %w", err)
	}
	if err := decodeSessionWorkspaceJSONField(workspace.SkillBindingsJSON, &folderWS.SkillBindings); err != nil {
		return nil, fmt.Errorf("failed to decode workspace skill bindings: %w", err)
	}
	if err := decodeSessionWorkspaceJSONField(workspace.AgentSkillAccessJSON, &folderWS.AgentSkillAccess); err != nil {
		return nil, fmt.Errorf("failed to decode workspace agent skill access: %w", err)
	}
	if err := decodeSessionWorkspaceJSONField(workspace.InstalledCapabilitiesJSON, &folderWS.InstalledCapabilities); err != nil {
		return nil, fmt.Errorf("failed to decode workspace installed capabilities: %w", err)
	}

	assistantState, err := decodeWorkspaceAssistantState(workspace.AssistantProgramJSON)
	if err != nil {
		return nil, err
	}
	folderWS.AssistantProgramState = assistantState.State
	folderWS.AssistantProjectLink = assistantState.Link

	return folderWS, nil
}

func decodeSessionWorkspaceJSONField(raw json.RawMessage, target any) error {
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, target)
}

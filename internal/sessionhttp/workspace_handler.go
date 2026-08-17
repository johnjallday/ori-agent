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

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/platform"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/session"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacecapability"
	"github.com/johnjallday/ori-agent/internal/workspacesettings"
)

var errParentWorkspaceMustBeGroup = errors.New("parent workspace must be a group")

// workspaceSharedDataPrimaryDirectoryIDKey mirrors projecttemplates.PrimaryDirectoryIDKey
// so this package and the workspace_create_project chat tool agree on the
// SharedData key used to record a workspace's primary linked directory.
const workspaceSharedDataPrimaryDirectoryIDKey = projecttemplates.PrimaryDirectoryIDKey

// workspaceTrashSharedDataKey is the SharedData key under which trash metadata
// ({original_path, trashed_path, deleted_at}) is stored while a workspace is
// trashed, so its folder can be moved back on restore.
const workspaceTrashSharedDataKey = "_trash"

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
		case "reaper-setup":
			if len(parts) == 3 && parts[2] == "repair" {
				h.handleReaperRepair(w, r, id)
				return
			}
			h.handleReaperReadiness(w, r, id)
			return
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
	ExistingAgentNames     []string                   `json:"existing_agent_names,omitempty"`
	WorkspaceBootstrap     *workspaceBootstrapRequest `json:"workspace_bootstrap,omitempty"`
	TemplateID             string                     `json:"template_id,omitempty"`   // Optional project template from the library
	TemplatePath           string                     `json:"template_path,omitempty"` // Optional arbitrary folder used as a project template. NOT restricted to the templates library: resolveProjectTemplate/LoadFolder will stat and copy from any path the caller supplies. Acceptable for this admin-facing, local-first, single-user app; do not expose this endpoint to untrusted callers without adding a path allowlist.
	ProjectName            string                     `json:"project_name,omitempty"`  // Project name for template instantiation (defaults to the workspace name)
	Tags                   []string                   `json:"tags,omitempty"`          // Optional initial tags; merged with template tags
	CreateTemplateAgents   *bool                      `json:"create_template_agents,omitempty"`
	TemplateAgentOverrides []templateAgentOverride    `json:"template_agent_overrides,omitempty"`
	Blank                  bool                       `json:"blank,omitempty"` // The Blank blueprint: seed the synthetic single-agent roster (no template, no project)
}

// CreateFromTemplate creates a normal (non-group) workspace from a built-in
// template by ID and returns its new workspace ID. It reuses the exact
// production POST /api/workspaces path in-process (entry-agent selection,
// tool binding, scaffold provisioning, starter-task seeding, template
// provenance) rather than duplicating any of that logic, so callers outside
// the HTTP layer — such as the Personal HQ setup coordinator — get identical
// behavior to a user picking the template from the library (PRD FR128).
func (h *Handler) CreateFromTemplate(ctx context.Context, name, templateID string) (string, error) {
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
	if err := h.requireGroupParent(r.Context(), req.ParentID); err != nil {
		handleWorkspaceParentError(w, err)
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
	if templateResolved && createTemplateAgentsEnabled(req) && len(req.TemplateAgentOverrides) > 0 {
		var err error
		resolvedTemplate, err = applyTemplateAgentOverrides(resolvedTemplate, req.TemplateAgentOverrides)
		if err != nil {
			_ = orihttp.RespondBadRequest(w, err.Error())
			return
		}
	}

	// Unusable-runtime-contract gate: a template whose runtime_requirements block
	// cannot be honored is refused before workspace files, agents, tasks,
	// plugins, or permissions are changed. Treating an invalid declaration as
	// absent would silently turn a requirement-bearing mode into an ungated one.
	if templateResolved && resolvedTemplate.HasInvalidRuntimeRequirements() {
		_ = orihttp.RespondJSON(w, http.StatusConflict, map[string]any{
			"error":                      fmt.Sprintf("This blueprint's runtime requirements are unusable, so no workspace was created. Fix its template.json: %s", resolvedTemplate.RuntimeRequirementsError),
			"runtime_requirements_error": resolvedTemplate.RuntimeRequirementsError,
		})
		return
	}

	// Unusable-setup-wizard gate: a template whose `setup_wizard` block could not
	// be understood is refused outright rather than created without its setup.
	// The author declared steps that grant folder access, connect accounts, or
	// change permissions; creating the workspace anyway would leave the user with
	// a blueprint that silently does none of them. Reported before the plugin gate
	// because a manifest Ori cannot read is the more fundamental problem.
	if templateResolved && resolvedTemplate.HasInvalidSetupWizard() {
		_ = orihttp.RespondJSON(w, http.StatusConflict, map[string]any{
			"error":              fmt.Sprintf("This blueprint's setup wizard is unusable, so no workspace was created. Fix its template.json: %s", resolvedTemplate.SetupWizardError),
			"setup_wizard_error": resolvedTemplate.SetupWizardError,
		})
		return
	}

	// Required-plugin gate: a template that declares plugins requires them
	// installed and enabled before creation, so the workspace is never created
	// missing its required tools. Reject with a structured 409 naming what to
	// install/enable; the create modal surfaces this and offers the install/enable
	// actions.
	//
	// A blueprint with a Setup Wizard is exempt, and deliberately so. Its wizard
	// asks how the workspace should work — and for Reaper Song one of the two
	// supported answers needs no plugin at all. Blocking creation on a plugin
	// would force everyone through an install to reach a question whose answer
	// might be "I don't need that", and there is no way to ask before the
	// workspace exists.
	if templateResolved && !resolvedTemplate.HasSetupWizard() {
		if missing, disabled := h.unsatisfiedRequiredPlugins(resolvedTemplate.Tools); len(missing)+len(disabled) > 0 {
			_ = orihttp.RespondJSON(w, http.StatusConflict, map[string]any{
				"error":            "required plugins are not ready",
				"missing_plugins":  missing,
				"disabled_plugins": disabled,
			})
			return
		}
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
	}

	ws := buildCreateWorkspace(req, kind, requestedTags, resolvedTemplate, templateResolved)

	seed, ok := h.selectCreateWorkspaceEntryAgent(w, ws, req, kind, resolvedTemplate, templateResolved)
	if !ok {
		return
	}

	if err := h.store.CreateWorkspace(r.Context(), ws); err != nil {
		logger.Error("Failed to create workspace", logger.Fields{"error": err})
		// The roster was seeded before this point, so without cleanup the user
		// is left with agents in Your Agents for a workspace that does not
		// exist (FR71).
		h.rollbackSeededAgents(seed)
		_ = orihttp.RespondInternalError(w, "Failed to create workspace")
		return
	}

	prov, responded := h.provisionCreateWorkspaceFolder(r.Context(), w, req, ws, createTemplateContext{
		wantsProject: wantsProject,
		template:     resolvedTemplate,
		resolved:     templateResolved,
		resolveErr:   templateResolveErr,
	}, seed)
	if responded {
		return
	}

	// Local creation implies this data directory owns the workspace: allowlist it
	// so its agent snapshots are restored (and not wiped) on subsequent startups,
	// mirroring the import flow. Best-effort; a failure only affects later agent
	// hydration, not this creation.
	if h.workspaceAllowlist != nil && ws != nil {
		if err := h.workspaceAllowlist.Add(ws.ID); err != nil {
			logger.Warn("Failed to allowlist created workspace", logger.Fields{"id": ws.ID, "error": err.Error()})
		}
	}

	if ws != nil {
		h.publishWorkspaceCreated(ws.ID, ws.Name)
	}

	agentSeedWarnings := append(seed.Warnings, prov.agentToolWarnings...)

	// Seed the template's starter tasks server-side, after the skeleton is
	// instantiated and the roster is attached, so they are assigned to a real
	// entry agent and the setup task only ever adjusts files that already
	// exist. Best-effort: a failure logs and never fails creation.
	seededStarterTasks := 0
	if templateResolved && kind != session.WorkspaceKindGroup {
		seededStarterTasks = h.seedTemplateStarterTasksLogged(ws.ID, resolvedTemplate)
	}

	// Must run after starter-task seeding above — see
	// persistCreateWorkspaceTemplateProvenance's doc comment for why.
	h.persistCreateWorkspaceTemplateProvenance(ws.ID, resolvedTemplate, templateResolved)

	// Completeness/ordering backstop: when the workspace was created with an
	// entry agent, claim any tasks that already exist on the folder workspace
	// (e.g. import-style seeds). Template starter tasks are assigned at seed
	// time above, so this is a no-op for them.
	if seed.EntrySet {
		h.claimUnassignedTasksForEntryAgentLogged(ws.ID)
	}

	logger.Info("Workspace created", logger.Fields{"id": ws.ID, "name": req.Name, "folder_slug": ws.FolderSlug, "kind": ws.Kind})

	response := map[string]any{
		"success": true,
		"folder":  ws,
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

// publishWorkspaceCreated emits a workspace.created event after a workspace is
// persisted. Consumed by the onboarding progression detector (Tier 2 "create
// your first workspace"). No-op when the event bus is not configured.
func (h *Handler) publishWorkspaceCreated(workspaceID, name string) {
	if h == nil || h.eventBus == nil {
		return
	}
	h.eventBus.Publish(agentworkspace.Event{
		Type:        agentworkspace.EventWorkspaceCreated,
		WorkspaceID: workspaceID,
		Source:      "api",
		Data:        map[string]any{"name": name},
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
func (h *Handler) selectCreateWorkspaceEntryAgent(w http.ResponseWriter, ws *session.Workspace, req createWorkspaceRequest, kind session.WorkspaceKind, tmpl projecttemplates.Template, templateResolved bool) (seedAgentsResult, bool) {
	var seed seedAgentsResult
	usesExistingAgentRoster := req.ExistingAgentNames != nil

	if usesExistingAgentRoster {
		switch {
		case templateResolved && createTemplateAgentsEnabled(req):
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
		case kind == session.WorkspaceKindGroup:
			if agentName := h.autoCreateManagerEntryAgent(ws); agentName != "" {
				setWorkspaceEntryAgent(ws, agentName)
				seed.EntrySet = true
			}
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
	case templateResolved && createTemplateAgentsEnabled(req):
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
	case kind == session.WorkspaceKindGroup:
		if agentName := h.autoCreateManagerEntryAgent(ws); agentName != "" {
			setWorkspaceEntryAgent(ws, agentName)
			seed.EntrySet = true
		}
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
}

// createProvisionOutcome carries folder-provisioning results back to
// createWorkspace's response assembly.
type createProvisionOutcome struct {
	projectWarning    string
	agentToolWarnings []string
}

// provisionCreateWorkspaceFolder creates the on-disk workspace folder,
// scaffolds content, binds seeded agent tools, and applies the project
// template. Folder problems are non-fatal by design (the SQLite record
// already exists); the one exception is a folder-slug conflict, which rolls
// the workspace back and writes the conflict response itself — signalled by
// responded=true, in which case the caller must return immediately.
func (h *Handler) provisionCreateWorkspaceFolder(ctx context.Context, w http.ResponseWriter, req createWorkspaceRequest, ws *session.Workspace, tc createTemplateContext, seed seedAgentsResult) (out createProvisionOutcome, responded bool) {
	seededAgents := seed.Created
	if tc.wantsProject {
		// Default for every path below that does not reach (or does not
		// succeed in) template application: missing store, folder-creation
		// failure, and folder-path failure all leave the workspace without a
		// usable folder. applyCreateWorkspaceTemplate overwrites this with a
		// specific message or clears it on success.
		out.projectWarning = "workspace was created, but the project template was not applied: workspace folder unavailable"
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
			writeWorkspaceCreateSlugConflict(w, req.Name, slugConflict)
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

	// Bind per-agent tools for any seeded template agents now that the
	// workspace is persisted (skills enable on the agent; MCP binds on
	// the workspace). Apply-if-present and non-fatal.
	out.agentToolWarnings = h.bindSeededAgentTools(ws.ID, seededAgents)

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
		out.projectWarning = h.applyCreateWorkspaceTemplate(ctx, req, ws, folderWS, tc)
	}
	return out, false
}

// applyCreateWorkspaceTemplate applies the selected project template to a
// freshly-provisioned (non-group) workspace folder: direct skeleton
// instantiation plus the template's default tool bindings. A legacy
// `onboarding` block in the manifest is ignored (the intake engine was
// replaced by setup starter tasks). Never fatal — a failure is reported via
// the returned warning.
func (h *Handler) applyCreateWorkspaceTemplate(ctx context.Context, req createWorkspaceRequest, ws *session.Workspace, folderWS *agentworkspace.Workspace, tc createTemplateContext) (projectWarning string) {
	switch {
	case tc.resolved && !tc.template.HasSkeleton:
		// Metadata-only template (no files): there is no project to
		// scaffold by design. Its behavior/tools/starter-tasks still
		// apply; skip instantiation without surfacing a warning.
		logger.Info("Metadata-only template: skipping project scaffold", logger.Fields{"id": ws.ID, "template": tc.template.ID})
	default:
		// Non-fatal by design: a failed instantiation must not fail
		// workspace creation. The warning is surfaced to the user.
		result, err := h.instantiateWorkspaceProject(ctx, ws, folderWS, req.TemplateID, req.TemplatePath, req.ProjectName)
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

	return projectWarning
}

// persistCreateWorkspaceTemplateProvenance records the built-in template a
// workspace was created from onto its portable workspace.json (features like
// REAPER readiness and repair identify origin from this rather than scanning
// filenames or task prose). Best-effort: a failure never fails creation.
//
// Must run after starter-task seeding, not from inside
// applyCreateWorkspaceTemplate. h.workspaceTaskStore is a SyncStore whose
// primary is the SQLite-backed session store; session.Workspace has no
// TemplateProvenance column, so every Update on this store round-trips
// through a conversion that silently drops it before re-saving to disk. Any
// later Update on the same workspace id — starter-task seeding runs right
// after template application — would clobber a provenance write made here
// earlier. Doing it last avoids that.
func (h *Handler) persistCreateWorkspaceTemplateProvenance(wsID string, tmpl projecttemplates.Template, resolved bool) {
	if !resolved || strings.TrimSpace(tmpl.ID) == "" || h.workspaceTaskStore == nil {
		return
	}
	// Built-ins always record provenance. A user template records it too when it
	// declares a setup wizard or runtime contract: those snapshots *are* the
	// workspace's setup contract, so without provenance the blueprint's declared
	// requirements would silently disappear after creation.
	if !tmpl.Builtin && !tmpl.HasSetupWizard() && !tmpl.HasRuntimeRequirements() {
		return
	}
	prov := &agentworkspace.TemplateProvenance{
		TemplateID:   tmpl.ID,
		TemplateName: tmpl.Name,
		Builtin:      tmpl.Builtin,
		Version:      tmpl.BuiltinVersion,
		AppliedAt:    time.Now(),
		// Setup requirements are recorded unresolved on purpose: creation states
		// which folder the template will ask for and what automation it wants
		// afterwards, but selects no path, expands no "~", registers no watcher,
		// and enables no schedule. Guided setup does all of that, only after the
		// user confirms a folder.
		DirectoryRequirements: tmpl.DirectoryRequirements,
		AutomationRecipes:     tmpl.AutomationRecipes,
		// The capability and plugin declarations travel with the wizard because
		// its steps reference them by key. Snapshotting them keeps setup and
		// repair readable from the workspace alone, instead of re-reading a
		// template the user may have since edited, replaced, or deleted.
		CapabilityRequirements: tmpl.CapabilityRequirements,
		Plugins:                tmpl.Tools.Plugins,
		PluginSources:          tmpl.Tools.PluginSources,
		RuntimeRequirements:    tmpl.RuntimeRequirements,
		SetupWizard:            tmpl.SetupWizard,
	}
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
	// The version recorded is the one THIS build compiles for that capability,
	// read from the registry rather than assumed, so a record can never claim a
	// version that does not exist (FR-13). A capability the registry does not
	// know about was already dropped at manifest load.
	registry, registryErr := workspacecapability.NewBuiltinRegistry()
	if err := h.workspaceTaskStore.Update(wsID, func(w *agentworkspace.Workspace) error {
		w.SetTemplateProvenance(prov)
		for _, capability := range tmpl.Capabilities {
			if registryErr != nil {
				break
			}
			def, known := registry.Definition(capability.ID)
			if !known {
				logger.Warn("Blueprint declares a capability this build does not provide", logger.Fields{
					"id": wsID, "template": tmpl.ID, "capability": capability.ID,
				})
				continue
			}
			source := capability.Source
			if strings.TrimSpace(source) == "" {
				source = agentworkspace.InstallSourceBlueprint
			}
			if _, err := w.AddInstalledCapability(agentworkspace.InstalledCapability{
				ID:          def.ID,
				Version:     def.Version,
				InstalledAt: installedAt,
				Source:      source,
			}); err != nil {
				// One malformed declaration must not cost the workspace its
				// provenance, which is written in this same update.
				logger.Warn("Blueprint capability not installed", logger.Fields{
					"id": wsID, "template": tmpl.ID, "capability": capability.ID, "error": err,
				})
			}
		}
		return nil
	}); err != nil {
		logger.Warn("Failed to persist template provenance", logger.Fields{"id": wsID, "template": tmpl.ID, "error": err})
	}
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

	h.hydrateWorkspaceMetadataInto(workspace)

	// Apply partial updates
	if req.Name != nil {
		workspace.Name = *req.Name
		workspace.FolderSlug = agentworkspace.Slugify(*req.Name)
	}
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
			}
			workspace.ParentID = newParentID
		}
	}
	if req.OrderIndex != nil {
		workspace.OrderIndex = *req.OrderIndex
	}
	if req.Color != nil {
		workspace.Color = *req.Color
	}

	if err := h.store.UpdateWorkspace(r.Context(), workspace); err != nil {
		logger.Error("Failed to update workspace", logger.Fields{"id": id, "error": err})
		_ = orihttp.RespondInternalError(w, "Failed to update workspace")
		return
	}
	if req.Name == nil && req.ParentID == nil {
		if err := h.syncWorkspacePortableStateToFileStore(workspace); err != nil {
			logger.Warn("Failed to sync workspace.json after workspace update", logger.Fields{"id": id, "error": err})
		}
	} else if req.Tags != nil {
		if err := h.syncWorkspaceTagsToFileStore(workspace); err != nil {
			logger.Warn("Failed to sync workspace tags after workspace update", logger.Fields{"id": id, "error": err})
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

	oldName := ws.Name
	oldFolderSlug := ws.FolderSlug
	targetSlug := ""
	if requestedSlug := strings.TrimSpace(req.FolderSlug); requestedSlug != "" {
		targetSlug = agentworkspace.Slugify(requestedSlug)
	}
	if targetSlug == "" {
		targetSlug = agentworkspace.Slugify(req.Name)
	}

	ws.Name = req.Name
	ws.FolderSlug = targetSlug
	ws.UpdatedAt = time.Now()

	if err := h.store.UpdateWorkspace(ctx, ws); err != nil {
		_ = orihttp.RespondInternalError(w, "Failed to rename workspace")
		return
	}

	// Rename the backing folder when this workspace is tracked by the folder
	// store. This now includes groups (a group is a folder that may physically
	// contain members); RenameWithSlug rewrites nested members' paths. DB-only
	// workspaces have no folder to rename and are skipped.
	folderTracked := false
	if h.workspaceStore != nil {
		if existing, getErr := h.workspaceStore.Get(id); getErr == nil && existing != nil {
			folderTracked = true
		}
	}
	if folderTracked {
		moved, err := h.workspaceStore.RenameWithSlug(id, req.Name, targetSlug)
		if err != nil {
			ws.Name = oldName
			ws.FolderSlug = oldFolderSlug
			ws.UpdatedAt = time.Now()
			if rollbackErr := h.store.UpdateWorkspace(ctx, ws); rollbackErr != nil {
				logger.Error("Failed to rollback workspace rename after folder rename error", logger.Fields{"id": id, "error": rollbackErr})
				_ = orihttp.RespondInternalError(w, "Failed to rollback workspace rename")
				return
			}

			var slugConflict *agentworkspace.FolderSlugConflictError
			if errors.As(err, &slugConflict) {
				writeWorkspaceCreateSlugConflict(w, req.Name, slugConflict)
				return
			}

			logger.Error("Failed to rename workspace folder", logger.Fields{"id": id, "error": err})
			_ = orihttp.RespondInternalError(w, "Failed to rename workspace folder")
			return
		}

		// The folder (and any nested members) changed paths: rewrite
		// path-keyed references (directory references, MCP roots,
		// project_path) and persist them.
		if len(moved) > 0 {
			h.applyMoveReferenceUpdates(ctx, ws, moved)
			if err := h.store.UpdateWorkspace(ctx, ws); err != nil {
				logger.Warn("Failed to persist renamed workspace references", logger.Fields{"id": id, "error": err})
			}
			if err := h.syncWorkspacePortableStateToFileStore(ws); err != nil {
				logger.Warn("Failed to sync workspace.json after rename", logger.Fields{"id": id, "error": err})
			}
		}
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
}

type workspaceImportDuplicate struct {
	Found         bool   `json:"found"`
	WorkspaceID   string `json:"workspace_id,omitempty"`
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

	return folderWS, nil
}

func decodeSessionWorkspaceJSONField(raw json.RawMessage, target any) error {
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, target)
}

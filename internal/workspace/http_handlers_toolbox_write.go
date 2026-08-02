package workspace

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
)

// Server-owned Toolbox creation, versioning, archiving, and deletion
// (PRD FR-8–FR-9, FR-18–FR-23, FR-37–FR-42, task 2.2).
//
// Two rules shape every handler here.
//
// First, ONE request per user action. The pre-Toolbox editor issued a request
// per capability toggle, so a half-completed edit left a half-changed agent and
// there was nothing to roll back to. A Toolbox version is saved whole or not at
// all (§9.5).
//
// Second, the write happens inside store.Update against a freshly loaded
// workspace, and an optional expected_workspace_version rejects a stale write
// rather than overwriting whatever changed underneath it (FR-23). Callers that
// omit the field get last-write-wins, which is right for a first save and wrong
// for anything that started from a preview.

type toolboxSkillPayload struct {
	CapabilityID      string `json:"capability_id,omitempty"`
	DisplayName       string `json:"display_name,omitempty"`
	Source            string `json:"source"`
	BindingID         string `json:"binding_id,omitempty"`
	OwnerCapabilityID string `json:"owner_capability_id,omitempty"`
	Required          bool   `json:"required,omitempty"`
}

type toolboxMCPPayload struct {
	BindingID         string   `json:"binding_id"`
	AllowedTools      []string `json:"allowed_tools"`
	OwnerCapabilityID string   `json:"owner_capability_id,omitempty"`
	Required          bool     `json:"required,omitempty"`
}

func (p toolboxSkillPayload) toRef() ToolboxSkillRef {
	return ToolboxSkillRef{
		CapabilityID:      p.CapabilityID,
		DisplayName:       p.DisplayName,
		Source:            p.Source,
		BindingID:         p.BindingID,
		OwnerCapabilityID: p.OwnerCapabilityID,
		Required:          p.Required,
	}
}

func (p toolboxMCPPayload) toRef() ToolboxMCPRef {
	ref := ToolboxMCPRef{
		BindingID:         p.BindingID,
		OwnerCapabilityID: p.OwnerCapabilityID,
		Required:          p.Required,
	}
	// A client that sends no list at all is asking for legacy all-tools
	// semantics, which FR-13 forbids for anything a user creates. Normalizing
	// nil to an empty slice would silently turn that into "expose nothing",
	// hiding the mistake — so nil is preserved and Validate rejects it.
	if p.AllowedTools != nil {
		ref.AllowedTools = append([]string(nil), p.AllowedTools...)
	}
	return ref
}

func toolboxRefsFromPayload(skills []toolboxSkillPayload, bindings []toolboxMCPPayload) ([]ToolboxSkillRef, []ToolboxMCPRef) {
	skillRefs := make([]ToolboxSkillRef, 0, len(skills))
	for _, payload := range skills {
		skillRefs = append(skillRefs, payload.toRef())
	}
	bindingRefs := make([]ToolboxMCPRef, 0, len(bindings))
	for _, payload := range bindings {
		bindingRefs = append(bindingRefs, payload.toRef())
	}
	return skillRefs, bindingRefs
}

// toolboxWriteError maps a domain error onto the right status code. Validation
// problems are the user's to fix (400); a stale version is a conflict the
// client must re-read and retry (409).
func toolboxWriteError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrToolboxNotFound), errors.Is(err, ErrToolboxVersionNotFound):
		orihttp.NotFound(w, err.Error())
	case errors.Is(err, errStaleWorkspaceVersion):
		orihttp.Conflict(w, err.Error())
	default:
		orihttp.BadRequest(w, err.Error())
	}
}

var errStaleWorkspaceVersion = errors.New("the workspace changed while you were editing; reload and try again")

// requireWorkspaceVersion enforces the caller's expected workspace version.
// Zero means "no expectation" — a first save from a fresh editor.
func requireWorkspaceVersion(ws *Workspace, expected int64) error {
	if expected == 0 || ws.Version == expected {
		return nil
	}
	return fmt.Errorf("%w (expected version %d, found %d)", errStaleWorkspaceVersion, expected, ws.Version)
}

// CreateToolboxHandler handles POST /api/workspaces/{workspaceID}/toolboxes
//
// One endpoint covers all three creation paths (FR-38, FR-39, FR-40) because
// they differ only in where the initial contents come from, and splitting them
// would put the "did this change the source Toolbox?" guarantee in three places
// instead of one. None of them changes the current assignment or the source.
func (h *HTTPHandler) CreateToolboxHandler(w http.ResponseWriter, r *http.Request) {
	workspaceID := r.PathValue("workspaceID")
	if workspaceID == "" {
		orihttp.BadRequest(w, "workspace ID is required")
		return
	}

	var req struct {
		Name        string `json:"name"`
		Description string `json:"description,omitempty"`
		Icon        string `json:"icon,omitempty"`
		Color       string `json:"color,omitempty"`
		// From selects the starting contents:
		//   "empty"     — a non-core empty selection (FR-39)
		//   "current"   — the agent instance's current effective selection (FR-38)
		//   "duplicate" — a copy of SourceToolboxID at SourceVersion (FR-40)
		//   "explicit"  — exactly the skills/mcp_bindings in this request
		From              string `json:"from,omitempty"`
		AgentInstanceID   string `json:"agent_instance_id,omitempty"`
		SourceToolboxID   string `json:"source_toolbox_id,omitempty"`
		SourceVersion     int64  `json:"source_version,omitempty"`
		ExpectedWorkspace int64  `json:"expected_workspace_version,omitempty"`

		Skills      []toolboxSkillPayload `json:"skills,omitempty"`
		MCPBindings []toolboxMCPPayload   `json:"mcp_bindings,omitempty"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	var created *ToolboxDefinition
	err := h.store.Update(workspaceID, func(ws *Workspace) error {
		if err := requireWorkspaceVersion(ws, req.ExpectedWorkspace); err != nil {
			return err
		}

		skills, bindings, err := resolveNewToolboxContents(ws, req.From, req.AgentInstanceID, req.SourceToolboxID, req.SourceVersion, req.Skills, req.MCPBindings)
		if err != nil {
			return err
		}

		definition, err := ws.CreateToolbox(ToolboxDefinition{
			ID:          "tbx-" + uuid.New().String(),
			Name:        req.Name,
			Description: req.Description,
			Icon:        req.Icon,
			Color:       req.Color,
			// A brand-new Toolbox is a draft until it is edited or used, so the
			// picker can tell an untouched recipe from a working one.
			Status:      ToolboxStatusDraft,
			Skills:      skills,
			MCPBindings: bindings,
			Provenance:  ToolboxProvenanceUser,
			Actor:       toolboxActor(r),
		})
		if err != nil {
			return err
		}
		created = definition
		return nil
	})
	if err != nil {
		toolboxWriteError(w, err)
		return
	}

	h.publishToolboxEvent(workspaceID, "toolbox_created", map[string]any{"toolbox": created})
	writeToolboxJSON(w, http.StatusCreated, map[string]any{
		"message":   "Toolbox created",
		"toolbox":   created,
		"workspace": workspaceID,
	})
}

// resolveNewToolboxContents computes the initial contents for each creation
// path. Reading from the workspace rather than trusting the client is what
// keeps "save the current selection" honest: the browser's idea of the current
// selection may be a page-load old.
func resolveNewToolboxContents(
	ws *Workspace,
	from, agentInstanceID, sourceToolboxID string,
	sourceVersion int64,
	skillPayload []toolboxSkillPayload,
	bindingPayload []toolboxMCPPayload,
) ([]ToolboxSkillRef, []ToolboxMCPRef, error) {
	switch strings.ToLower(strings.TrimSpace(from)) {
	case "", "empty":
		// FR-39: an empty non-core selection is a legitimate starting point,
		// not an error. Core capabilities remain available regardless.
		return nil, nil, nil

	case "current":
		instanceID := strings.TrimSpace(agentInstanceID)
		if instanceID == "" {
			return nil, nil, fmt.Errorf("agent_instance_id is required to save the current selection")
		}
		_, recipe, ok, err := ws.ResolveAssignedToolbox(instanceID)
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			return nil, nil, fmt.Errorf("agent instance %s has no current toolbox to save", instanceID)
		}
		return recipe.Skills, recipe.MCPBindings, nil

	case "duplicate":
		sourceID := strings.TrimSpace(sourceToolboxID)
		if sourceID == "" {
			return nil, nil, fmt.Errorf("source_toolbox_id is required to duplicate a toolbox")
		}
		source, exists := ws.GetToolbox(sourceID)
		if !exists {
			return nil, nil, fmt.Errorf("%w: %s", ErrToolboxNotFound, sourceID)
		}
		version := sourceVersion
		if version == 0 {
			version = source.Version
		}
		// Duplicating reads the source and never writes it, so an archived
		// Toolbox can still be duplicated into a working one — which is exactly
		// how a user recovers a retired recipe (FR-40).
		recipe, err := source.ResolveVersion(version)
		if err != nil {
			return nil, nil, err
		}
		return recipe.Skills, recipe.MCPBindings, nil

	case "explicit":
		skills, bindings := toolboxRefsFromPayload(skillPayload, bindingPayload)
		return skills, bindings, nil

	default:
		return nil, nil, fmt.Errorf("unknown creation source %q", from)
	}
}

// UpdateToolboxHandler handles PUT/PATCH
// /api/workspaces/{workspaceID}/toolboxes/{toolboxID} — metadata only.
//
// Renaming and recoloring do NOT create a version: a run snapshot reproduces
// capabilities, and no capability changed. Versioning a rename would fill the
// audit history with entries that grant nothing (FR-41).
func (h *HTTPHandler) UpdateToolboxHandler(w http.ResponseWriter, r *http.Request) {
	workspaceID := r.PathValue("workspaceID")
	toolboxID := r.PathValue("toolboxID")
	if workspaceID == "" || toolboxID == "" {
		orihttp.BadRequest(w, "workspace ID and toolbox ID are required")
		return
	}

	var req struct {
		Name              *string `json:"name,omitempty"`
		Description       *string `json:"description,omitempty"`
		Icon              *string `json:"icon,omitempty"`
		Color             *string `json:"color,omitempty"`
		ExpectedWorkspace int64   `json:"expected_workspace_version,omitempty"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	var updated *ToolboxDefinition
	err := h.store.Update(workspaceID, func(ws *Workspace) error {
		if err := requireWorkspaceVersion(ws, req.ExpectedWorkspace); err != nil {
			return err
		}
		current, exists := ws.GetToolbox(toolboxID)
		if !exists {
			return fmt.Errorf("%w: %s", ErrToolboxNotFound, toolboxID)
		}

		name := current.Name
		if req.Name != nil {
			name = *req.Name
		}
		description := current.Description
		if req.Description != nil {
			description = *req.Description
		}
		icon := current.Icon
		if req.Icon != nil {
			icon = *req.Icon
		}
		color := current.Color
		if req.Color != nil {
			color = *req.Color
		}

		result, err := ws.UpdateToolboxMetadata(toolboxID, name, description, icon, color)
		if err != nil {
			return err
		}
		updated = result
		return nil
	})
	if err != nil {
		toolboxWriteError(w, err)
		return
	}

	h.publishToolboxEvent(workspaceID, "toolbox_updated", map[string]any{"toolbox": updated})
	writeToolboxJSON(w, http.StatusOK, map[string]any{
		"message":   "Toolbox updated",
		"toolbox":   updated,
		"workspace": workspaceID,
	})
}

// CreateToolboxVersionHandler handles POST
// /api/workspaces/{workspaceID}/toolboxes/{toolboxID}/versions
//
// Every content edit lands here, and every one produces a new version while
// leaving the previous one immutable and resolvable (FR-18, FR-19, task 2.10).
func (h *HTTPHandler) CreateToolboxVersionHandler(w http.ResponseWriter, r *http.Request) {
	workspaceID := r.PathValue("workspaceID")
	toolboxID := r.PathValue("toolboxID")
	if workspaceID == "" || toolboxID == "" {
		orihttp.BadRequest(w, "workspace ID and toolbox ID are required")
		return
	}

	var req struct {
		Skills      []toolboxSkillPayload `json:"skills"`
		MCPBindings []toolboxMCPPayload   `json:"mcp_bindings"`
		// ExpectedVersion rejects an edit built from a version that has since
		// been superseded, so two editors cannot silently overwrite each other.
		ExpectedVersion   int64 `json:"expected_version,omitempty"`
		ExpectedWorkspace int64 `json:"expected_workspace_version,omitempty"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	var updated *ToolboxDefinition
	err := h.store.Update(workspaceID, func(ws *Workspace) error {
		if err := requireWorkspaceVersion(ws, req.ExpectedWorkspace); err != nil {
			return err
		}
		current, exists := ws.GetToolbox(toolboxID)
		if !exists {
			return fmt.Errorf("%w: %s", ErrToolboxNotFound, toolboxID)
		}
		if req.ExpectedVersion != 0 && current.Version != req.ExpectedVersion {
			return fmt.Errorf("%w: this toolbox is now at version %d, not %d",
				errStaleWorkspaceVersion, current.Version, req.ExpectedVersion)
		}

		skills, bindings := toolboxRefsFromPayload(req.Skills, req.MCPBindings)
		result, err := ws.SaveToolboxVersion(toolboxID, skills, bindings, ToolboxProvenanceUser, toolboxActor(r))
		if err != nil {
			return err
		}
		updated = result
		return nil
	})
	if err != nil {
		toolboxWriteError(w, err)
		return
	}

	h.publishToolboxEvent(workspaceID, "toolbox_version_created", map[string]any{
		"toolbox": updated,
		"version": updated.Version,
	})
	writeToolboxJSON(w, http.StatusCreated, map[string]any{
		"message":   "Toolbox saved",
		"toolbox":   updated,
		"version":   updated.Version,
		"workspace": workspaceID,
	})
}

// SetToolboxStatusHandler handles POST
// /api/workspaces/{workspaceID}/toolboxes/{toolboxID}/status — archive and
// reactivate (FR-20, FR-41).
func (h *HTTPHandler) SetToolboxStatusHandler(w http.ResponseWriter, r *http.Request) {
	workspaceID := r.PathValue("workspaceID")
	toolboxID := r.PathValue("toolboxID")
	if workspaceID == "" || toolboxID == "" {
		orihttp.BadRequest(w, "workspace ID and toolbox ID are required")
		return
	}

	var req struct {
		Status            string `json:"status"`
		ExpectedWorkspace int64  `json:"expected_workspace_version,omitempty"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	status := NormalizeToolboxStatus(req.Status)

	var updated *ToolboxDefinition
	var references []ToolboxReference
	err := h.store.Update(workspaceID, func(ws *Workspace) error {
		if err := requireWorkspaceVersion(ws, req.ExpectedWorkspace); err != nil {
			return err
		}
		// Archiving a Toolbox an agent is actively using would leave that agent
		// pinned to something it can no longer re-select. The references are
		// reported so the user can move the agent first (FR-20, FR-21).
		if status == ToolboxStatusArchived {
			if refs := ws.ToolboxReferences(toolboxID); len(refs) > 0 {
				references = refs
				return fmt.Errorf("this toolbox is still in use by %d agent instance(s); switch them to another toolbox first", len(refs))
			}
		}
		result, err := ws.SetToolboxStatus(toolboxID, status)
		if err != nil {
			return err
		}
		updated = result
		return nil
	})
	if err != nil {
		if len(references) > 0 {
			writeToolboxJSON(w, http.StatusConflict, map[string]any{
				"message":    err.Error(),
				"references": references,
				"workspace":  workspaceID,
			})
			return
		}
		toolboxWriteError(w, err)
		return
	}

	h.publishToolboxEvent(workspaceID, "toolbox_status_changed", map[string]any{"toolbox": updated})
	writeToolboxJSON(w, http.StatusOK, map[string]any{
		"message":   "Toolbox " + status,
		"toolbox":   updated,
		"workspace": workspaceID,
	})
}

// DeleteToolboxHandler handles DELETE
// /api/workspaces/{workspaceID}/toolboxes/{toolboxID}
//
// Deletion is guarded and the guard EXPLAINS itself: a refusal lists exactly
// what still references the Toolbox, so the user has something to act on rather
// than a flat "in use" (FR-21).
func (h *HTTPHandler) DeleteToolboxHandler(w http.ResponseWriter, r *http.Request) {
	workspaceID := r.PathValue("workspaceID")
	toolboxID := r.PathValue("toolboxID")
	if workspaceID == "" || toolboxID == "" {
		orihttp.BadRequest(w, "workspace ID and toolbox ID are required")
		return
	}

	var references []ToolboxReference
	err := h.store.Update(workspaceID, func(ws *Workspace) error {
		if _, exists := ws.GetToolbox(toolboxID); !exists {
			return fmt.Errorf("%w: %s", ErrToolboxNotFound, toolboxID)
		}
		if refs := ws.ToolboxReferences(toolboxID); len(refs) > 0 {
			references = refs
			return fmt.Errorf("this toolbox is still in use and cannot be deleted")
		}
		return ws.DeleteToolbox(toolboxID)
	})
	if err != nil {
		if len(references) > 0 {
			writeToolboxJSON(w, http.StatusConflict, map[string]any{
				"message":    err.Error(),
				"references": references,
				"workspace":  workspaceID,
			})
			return
		}
		toolboxWriteError(w, err)
		return
	}

	h.publishToolboxEvent(workspaceID, "toolbox_deleted", map[string]any{"toolbox_id": toolboxID})
	writeToolboxJSON(w, http.StatusOK, map[string]any{
		"message":    "Toolbox deleted",
		"toolbox_id": toolboxID,
		"workspace":  workspaceID,
	})
}

// CompareToolboxVersionsHandler handles GET
// /api/workspaces/{workspaceID}/toolboxes/{toolboxID}/compare?from=&to=
//
// Comparing across two DIFFERENT toolboxes is supported through
// &to_toolbox_id=, because "what would switching cost me?" is the same question
// as "what changed between my versions?" (FR-51, FR-52).
func (h *HTTPHandler) CompareToolboxVersionsHandler(w http.ResponseWriter, r *http.Request) {
	workspaceID := r.PathValue("workspaceID")
	toolboxID := r.PathValue("toolboxID")
	if workspaceID == "" || toolboxID == "" {
		orihttp.BadRequest(w, "workspace ID and toolbox ID are required")
		return
	}

	ws, err := h.store.Get(workspaceID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace not found: %v", err))
		return
	}

	source, exists := ws.GetToolbox(toolboxID)
	if !exists {
		orihttp.NotFound(w, fmt.Sprintf("Toolbox %s not found", toolboxID))
		return
	}

	target := source
	if other := strings.TrimSpace(r.URL.Query().Get("to_toolbox_id")); other != "" && !strings.EqualFold(other, toolboxID) {
		found, ok := ws.GetToolbox(other)
		if !ok {
			orihttp.NotFound(w, fmt.Sprintf("Toolbox %s not found", other))
			return
		}
		target = found
	}

	fromVersion, err := parseVersionParam(r.URL.Query().Get("from"), earliestToolboxVersion(*source))
	if err != nil {
		orihttp.BadRequest(w, err.Error())
		return
	}
	toVersion, err := parseVersionParam(r.URL.Query().Get("to"), target.Version)
	if err != nil {
		orihttp.BadRequest(w, err.Error())
		return
	}

	fromRecipe, err := source.ResolveVersion(fromVersion)
	if err != nil {
		orihttp.NotFound(w, err.Error())
		return
	}
	toRecipe, err := target.ResolveVersion(toVersion)
	if err != nil {
		orihttp.NotFound(w, err.Error())
		return
	}

	diff := CompareToolboxRecipes(fromRecipe, toRecipe)
	writeToolboxJSON(w, http.StatusOK, map[string]any{
		"diff":               diff,
		"identical":          diff.Identical(),
		"expands_operations": diff.ExpandsOperations(),
		"from": map[string]any{
			"toolbox_id": source.ID,
			"name":       source.Name,
			"version":    fromVersion,
		},
		"to": map[string]any{
			"toolbox_id": target.ID,
			"name":       target.Name,
			"version":    toVersion,
		},
		"workspace":         workspaceID,
		"workspace_version": ws.Version,
	})
}

func earliestToolboxVersion(definition ToolboxDefinition) int64 {
	if len(definition.History) > 0 {
		return definition.History[0].Version
	}
	return definition.Version
}

func parseVersionParam(raw string, fallback int64) (int64, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return fallback, nil
	}
	version, err := strconv.ParseInt(trimmed, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid version %q", raw)
	}
	return version, nil
}

// toolboxActor identifies who performed a write, for the audit trail (FR-160).
// Ori is single-user today, so this records the surface rather than an identity
// it cannot actually verify — a fabricated user ID would be worse than an
// honest "api".
func toolboxActor(r *http.Request) string {
	if actor := strings.TrimSpace(r.Header.Get("X-Ori-Actor")); actor != "" {
		return actor
	}
	return "api"
}

func (h *HTTPHandler) publishToolboxEvent(workspaceID, action string, data map[string]any) {
	if h == nil || h.eventBus == nil {
		return
	}
	payload := map[string]any{"action": action}
	for key, value := range data {
		payload[key] = value
	}
	h.eventBus.Publish(Event{
		Type:        EventWorkspaceUpdated,
		WorkspaceID: workspaceID,
		Source:      "api",
		Data:        payload,
	})
}

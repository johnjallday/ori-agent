package workspace

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
)

// Read APIs for named workspace Toolboxes (PRD FR-1, FR-5, FR-15–FR-17,
// FR-24–FR-29, task 1.14).
//
// V1 group 1 exposes only the truthful READ surface: what Toolboxes exist, what
// one version grants, and which Toolbox each stable agent instance is currently
// pinned to. Creation, versioning, preview, and safe switching arrive with the
// Workshop UI and the atomic use operation; adding a write endpoint here ahead
// of the server-owned revalidation those need would create exactly the
// browser-driven multi-request mutation §9.5 rules out.

// ToolboxSummary is one Toolbox as the list surface presents it.
type ToolboxSummary struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Icon        string `json:"icon,omitempty"`
	Color       string `json:"color,omitempty"`
	Version     int64  `json:"version"`
	Status      string `json:"status"`
	Provenance  string `json:"provenance,omitempty"`
	// SkillCount counts the deduplicated space-consuming skills; MCP operations
	// are reported separately because they never consume a skill space (FR-58).
	SkillCount int `json:"skill_count"`
	// MCPBindingCount and OperationCount describe the exposed operation
	// surface. OperationCount is -1 for a Toolbox that still defers to a
	// binding's own tool policy, since the real number is unknown until the
	// user pins an explicit subset (see ToolboxMCPRef.InheritsBindingTools).
	MCPBindingCount int `json:"mcp_binding_count"`
	OperationCount  int `json:"operation_count"`
	// AssignedInstanceIDs lists the stable agent instances currently pinned to
	// this Toolbox. Instance IDs, never names (FR-16).
	AssignedInstanceIDs []string `json:"assigned_instance_ids,omitempty"`
}

func summarizeToolbox(ws *Workspace, definition ToolboxDefinition) ToolboxSummary {
	summary := ToolboxSummary{
		ID:              definition.ID,
		Name:            definition.Name,
		Description:     definition.Description,
		Icon:            definition.Icon,
		Color:           definition.Color,
		Version:         definition.Version,
		Status:          NormalizeToolboxStatus(definition.Status),
		Provenance:      definition.Provenance,
		SkillCount:      definition.SkillSpacesUsed(),
		MCPBindingCount: len(definition.MCPBindings),
	}
	operations := 0
	for _, ref := range definition.MCPBindings {
		if ref.NeedsExplicitTools() {
			operations = -1
			break
		}
		operations += len(ref.AllowedTools)
	}
	summary.OperationCount = operations
	summary.AssignedInstanceIDs = instancesAssignedToToolbox(ws, definition.ID)
	return summary
}

// ListToolboxes handles GET /api/workspaces/{workspaceID}/toolboxes
func (h *HTTPHandler) ListToolboxes(w http.ResponseWriter, r *http.Request) {
	workspaceID := r.PathValue("workspaceID")
	if workspaceID == "" {
		orihttp.BadRequest(w, "workspace ID is required")
		return
	}

	ws, err := h.store.Get(workspaceID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace not found: %v", err))
		return
	}

	definitions := ws.GetToolboxes()
	summaries := make([]ToolboxSummary, 0, len(definitions))
	for _, definition := range definitions {
		summaries = append(summaries, summarizeToolbox(ws, definition))
	}

	writeToolboxJSON(w, http.StatusOK, map[string]any{
		"toolboxes":         summaries,
		"count":             len(summaries),
		"workspace":         workspaceID,
		"workspace_version": ws.Version,
		"migrated":          ws.ToolboxMigration.Migrated(),
	})
}

// GetToolboxByID handles GET /api/workspaces/{workspaceID}/toolboxes/{toolboxID}
//
// An optional ?version= selects a historical version, which must stay
// resolvable after the Toolbox is edited or archived so a run snapshot's
// version can always be explained (FR-19).
func (h *HTTPHandler) GetToolboxByID(w http.ResponseWriter, r *http.Request) {
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

	definition, exists := ws.GetToolbox(toolboxID)
	if !exists {
		orihttp.NotFound(w, fmt.Sprintf("Toolbox %s not found", toolboxID))
		return
	}

	recipe := definition.CurrentRecipe()
	if raw := strings.TrimSpace(r.URL.Query().Get("version")); raw != "" {
		version, parseErr := strconv.ParseInt(raw, 10, 64)
		if parseErr != nil {
			orihttp.BadRequest(w, fmt.Sprintf("invalid version %q", raw))
			return
		}
		resolved, resolveErr := definition.ResolveVersion(version)
		if resolveErr != nil {
			orihttp.NotFound(w, resolveErr.Error())
			return
		}
		recipe = resolved
	}

	availableVersions := make([]int64, 0, len(definition.History)+1)
	for _, historical := range definition.History {
		availableVersions = append(availableVersions, historical.Version)
	}
	availableVersions = append(availableVersions, definition.Version)

	writeToolboxJSON(w, http.StatusOK, map[string]any{
		"toolbox":            definition,
		"recipe":             recipe,
		"summary":            summarizeToolbox(ws, *definition),
		"available_versions": availableVersions,
		"references":         ws.ToolboxReferences(definition.ID),
		"workspace":          workspaceID,
		"workspace_version":  ws.Version,
	})
}

// AgentToolboxView is the current Toolbox position of one stable agent
// instance: which Toolbox, which version, and where each capability came from.
type AgentToolboxView struct {
	AgentInstanceID string `json:"agent_instance_id"`
	AgentName       string `json:"agent_name"`
	NodeID          string `json:"node_id,omitempty"`
	InstanceNumber  int    `json:"instance_number,omitempty"`
	// Assigned is false for an instance that has not been migrated yet. Such an
	// instance still resolves through the legacy implicit merge, and saying so
	// is more useful than presenting an empty Toolbox as if it were a choice.
	Assigned       bool   `json:"assigned"`
	ToolboxID      string `json:"toolbox_id,omitempty"`
	ToolboxName    string `json:"toolbox_name,omitempty"`
	ToolboxVersion int64  `json:"toolbox_version,omitempty"`
	ToolboxStatus  string `json:"toolbox_status,omitempty"`
	// CoreOnly reports a Toolbox that activates nothing beyond Ori's mandatory
	// runtime abilities — a truthful state the Workshop must show as such
	// rather than as an error (FR-39, FR-46).
	CoreOnly bool `json:"core_only"`

	Skills      []AgentToolboxSkillView `json:"skills,omitempty"`
	MCPBindings []AgentToolboxMCPView   `json:"mcp_bindings,omitempty"`

	SkillSpacesUsed int `json:"skill_spaces_used"`
	// AppliedAt / Provenance / Actor answer "who put this here and when"
	// (FR-160).
	AppliedAt  string `json:"applied_at,omitempty"`
	Provenance string `json:"provenance,omitempty"`
	Actor      string `json:"actor,omitempty"`
}

// AgentToolboxSkillView is one active skill with its provenance resolved
// against the current Workshop (FR-5).
type AgentToolboxSkillView struct {
	CapabilityID string `json:"capability_id"`
	DisplayName  string `json:"display_name"`
	Source       string `json:"source"`
	BindingID    string `json:"binding_id,omitempty"`
	// OwnerCapabilityID names the installed Workspace Capability that supplied
	// the binding, when one did. Provenance only — it never implies the
	// capability is active or should be installed (FR-32).
	OwnerCapabilityID string `json:"owner_capability_id,omitempty"`
	Required          bool   `json:"required"`
	// Available reports whether the referenced source resolves right now. A
	// false value is `Missing capability`, not an error (FR-14).
	Available bool `json:"available"`
}

// AgentToolboxMCPView is one active MCP binding with its exposed operations.
type AgentToolboxMCPView struct {
	BindingID         string   `json:"binding_id"`
	ServerName        string   `json:"server_name,omitempty"`
	Alias             string   `json:"alias,omitempty"`
	AllowedTools      []string `json:"allowed_tools,omitempty"`
	OwnerCapabilityID string   `json:"owner_capability_id,omitempty"`
	Required          bool     `json:"required"`
	Available         bool     `json:"available"`
	// NeedsExplicitTools marks a migrated entry still deferring to the
	// binding's own tool policy, which the Workshop offers to make explicit
	// (FR-13, FR-47).
	NeedsExplicitTools bool `json:"needs_explicit_tools,omitempty"`
}

// GetAgentToolbox handles
// GET /api/workspaces/{workspaceID}/agent-toolboxes/{agentInstanceID}
func (h *HTTPHandler) GetAgentToolbox(w http.ResponseWriter, r *http.Request) {
	workspaceID := r.PathValue("workspaceID")
	agentInstanceID := r.PathValue("agentInstanceID")
	if workspaceID == "" || agentInstanceID == "" {
		orihttp.BadRequest(w, "workspace ID and agent instance ID are required")
		return
	}

	ws, err := h.store.Get(workspaceID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace not found: %v", err))
		return
	}

	view, found := buildAgentToolboxView(ws, agentInstanceID)
	if !found {
		orihttp.NotFound(w, fmt.Sprintf("agent instance %s is not attached to this workspace", agentInstanceID))
		return
	}

	writeToolboxJSON(w, http.StatusOK, map[string]any{
		"agent_toolbox":     view,
		"workspace":         workspaceID,
		"workspace_version": ws.Version,
	})
}

// ListAgentToolboxes handles GET /api/workspaces/{workspaceID}/agent-toolboxes
func (h *HTTPHandler) ListAgentToolboxes(w http.ResponseWriter, r *http.Request) {
	workspaceID := r.PathValue("workspaceID")
	if workspaceID == "" {
		orihttp.BadRequest(w, "workspace ID is required")
		return
	}

	ws, err := h.store.Get(workspaceID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace not found: %v", err))
		return
	}

	instances := ws.GetAgentInstances()
	views := make([]AgentToolboxView, 0, len(instances))
	for _, instance := range instances {
		if view, ok := buildAgentToolboxView(ws, instance.ID); ok {
			views = append(views, view)
		}
	}

	writeToolboxJSON(w, http.StatusOK, map[string]any{
		"agent_toolboxes":   views,
		"count":             len(views),
		"workspace":         workspaceID,
		"workspace_version": ws.Version,
		"migrated":          ws.ToolboxMigration.Migrated(),
	})
}

func buildAgentToolboxView(ws *Workspace, agentInstanceID string) (AgentToolboxView, bool) {
	var instance *AgentInstance
	for _, candidate := range ws.GetAgentInstances() {
		if strings.EqualFold(strings.TrimSpace(candidate.ID), strings.TrimSpace(agentInstanceID)) {
			found := candidate
			instance = &found
			break
		}
	}
	if instance == nil {
		return AgentToolboxView{}, false
	}

	view := AgentToolboxView{
		AgentInstanceID: instance.ID,
		AgentName:       instance.Name,
		NodeID:          instance.NodeID,
		InstanceNumber:  instance.InstanceNumber,
	}

	definition, recipe, assigned, err := ws.ResolveAssignedToolbox(instance.ID)
	if err != nil || !assigned {
		if err != nil {
			logger.Warn("Agent toolbox view could not resolve the pinned assignment", logger.Fields{
				"workspace_id":      ws.ID,
				"agent_instance_id": instance.ID,
				"error":             err.Error(),
			})
		}
		return view, true
	}

	view.Assigned = true
	view.ToolboxID = definition.ID
	view.ToolboxName = definition.Name
	view.ToolboxVersion = recipe.Version
	view.ToolboxStatus = NormalizeToolboxStatus(definition.Status)
	view.SkillSpacesUsed = recipe.SkillSpacesUsed()
	if assignmentRecord, ok := ws.GetToolboxAssignment(instance.ID); ok {
		view.AppliedAt = assignmentRecord.AppliedAt.Format("2006-01-02T15:04:05Z07:00")
		view.Provenance = assignmentRecord.Provenance
		view.Actor = assignmentRecord.Actor
	}

	skillBindings := make(map[string]SkillBinding)
	for _, binding := range ws.GetSkillBindings() {
		skillBindings[strings.ToLower(strings.TrimSpace(binding.ID))] = binding
	}
	for _, ref := range recipe.Skills {
		entry := AgentToolboxSkillView{
			CapabilityID:      ref.CapabilityID,
			DisplayName:       ref.DisplayName,
			Source:            NormalizeToolboxSource(ref.Source),
			BindingID:         ref.BindingID,
			OwnerCapabilityID: ref.OwnerCapabilityID,
			Required:          ref.Required,
		}
		switch entry.Source {
		case ToolboxSourceWorkspaceProvided:
			binding, exists := skillBindings[strings.ToLower(strings.TrimSpace(ref.BindingID))]
			entry.Available = exists && binding.Enabled
		default:
			// Agent-learned availability depends on the skill manager, which
			// this read surface does not reach. Reporting it as available here
			// and letting preview do the real check is honest: this view
			// answers "what is selected", not "is it ready" (FR-73).
			entry.Available = true
		}
		view.Skills = append(view.Skills, entry)
	}

	mcpBindings := make(map[string]MCPBinding)
	for _, binding := range ws.GetMCPBindings() {
		mcpBindings[strings.ToLower(strings.TrimSpace(binding.ID))] = binding
	}
	for _, ref := range recipe.MCPBindings {
		binding, exists := mcpBindings[strings.ToLower(strings.TrimSpace(ref.BindingID))]
		entry := AgentToolboxMCPView{
			BindingID:          ref.BindingID,
			AllowedTools:       ref.AllowedTools,
			OwnerCapabilityID:  ref.OwnerCapabilityID,
			Required:           ref.Required,
			Available:          exists && binding.Enabled,
			NeedsExplicitTools: ref.NeedsExplicitTools(),
		}
		if exists {
			entry.ServerName = binding.ServerName
			entry.Alias = binding.Alias
			if ref.NeedsExplicitTools() && !binding.AllowsAllTools() {
				entry.AllowedTools = append([]string(nil), binding.AllowedTools...)
			}
		}
		view.MCPBindings = append(view.MCPBindings, entry)
	}

	view.CoreOnly = len(view.Skills) == 0 && len(view.MCPBindings) == 0
	return view, true
}

func writeToolboxJSON(w http.ResponseWriter, status int, payload map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		logger.Error("Failed to encode toolbox response", logger.Fields{"error": err})
	}
}

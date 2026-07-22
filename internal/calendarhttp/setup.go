package calendarhttp

import (
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/johnjallday/ori-agent/internal/calendar"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// --- response shapes -------------------------------------------------------

type requirementView struct {
	Key                string   `json:"key"`
	RequiredOperations []string `json:"required_operations"`
	OptionalOperations []string `json:"optional_operations"`
}

type bindingView struct {
	ID           string   `json:"id"`
	ServerName   string   `json:"server_name"`
	HasMapping   bool     `json:"has_mapping"`
	MappingValid bool     `json:"mapping_valid"`
	AllowedTools []string `json:"allowed_tools"`
	MappedOps    []string `json:"mapped_operations"`
}

type settingsView struct {
	SelectedCalendarIDs []string `json:"selected_calendar_ids"`
	DisplayTimeZone     string   `json:"display_time_zone"`
	ContextWorkspaceIDs []string `json:"context_workspace_ids"`
	Validated           bool     `json:"validated"`
}

type existingConnectorView struct {
	Name      string `json:"name"`
	Transport string `json:"transport,omitempty"`
	Remote    bool   `json:"remote"`
}

type workspaceRefView struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type setupStateResponse struct {
	Applicable                 bool                       `json:"applicable"`
	WorkspaceID                string                     `json:"workspace_id,omitempty"`
	State                      calendar.SetupState        `json:"state,omitempty"`
	States                     []calendar.SetupState      `json:"states,omitempty"`
	Requirement                *requirementView           `json:"requirement,omitempty"`
	Binding                    *bindingView               `json:"binding,omitempty"`
	Connector                  *connectorStatus           `json:"connector,omitempty"`
	Settings                   settingsView               `json:"settings"`
	Presets                    []calendar.ConnectorPreset `json:"presets,omitempty"`
	PresetAdded                map[string]bool            `json:"preset_added,omitempty"`
	ExistingConnectors         []existingConnectorView    `json:"existing_connectors,omitempty"`
	ContextWorkspaceCandidates []workspaceRefView         `json:"context_workspace_candidates,omitempty"`
}

// --- endpoints -------------------------------------------------------------

// Setup handles GET /api/calendar-ops/setup?workspace_id=ID and returns the
// full setup-state view. A non-Calendar-Ops (or unknown) workspace responds
// applicable:false so the frontend module can stay dormant.
func (h *Handler) Setup(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodGet) {
		return
	}
	workspaceID := strings.TrimSpace(r.URL.Query().Get("workspace_id"))
	if workspaceID == "" {
		orihttp.BadRequest(w, "workspace_id is required")
		return
	}
	ws, ok := h.loadCalendarOpsWorkspace(w, workspaceID)
	if !ok {
		return
	}
	if ws == nil {
		_ = orihttp.RespondSuccess(w, setupStateResponse{Applicable: false})
		return
	}
	userID, err := h.currentUserID(r.Context())
	if err != nil {
		orihttp.InternalError(w, "Failed to resolve current user: "+err.Error())
		return
	}
	_ = orihttp.RespondSuccess(w, h.buildStateResponse(r.Context(), ws, userID))
}

// connectorRequest selects or adds the connector to set up.
type connectorRequest struct {
	WorkspaceID string `json:"workspace_id"`
	PresetID    string `json:"preset_id,omitempty"`
	ServerName  string `json:"server_name,omitempty"`
}

// Connector handles POST /api/calendar-ops/setup/connector: it (optionally)
// adds a shipped connector preset to the global MCP registry, then creates or
// repoints the workspace's calendar binding at the chosen server with an empty
// (placeholder) calendar mapping and an empty read allowlist. This makes the
// setup state advance to auth_required so the user can authenticate through the
// existing MCP connect flow before mappings are confirmed.
func (h *Handler) Connector(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	var req connectorRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	ws, ok := h.loadCalendarOpsWorkspace(w, strings.TrimSpace(req.WorkspaceID))
	if !ok {
		return
	}
	if ws == nil {
		orihttp.BadRequest(w, "workspace is not a Calendar Ops workspace")
		return
	}

	serverName := strings.TrimSpace(req.ServerName)
	if presetID := strings.TrimSpace(req.PresetID); presetID != "" {
		preset, found := calendar.FindPreset(presetID)
		if !found {
			orihttp.BadRequest(w, "unknown connector preset: "+presetID)
			return
		}
		if err := h.ensurePresetServer(preset); err != nil {
			orihttp.InternalError(w, "Failed to add connector preset: "+err.Error())
			return
		}
		serverName = preset.ServerName
	}
	if serverName == "" {
		orihttp.BadRequest(w, "server_name or preset_id is required")
		return
	}

	binding, _ := findCalendarBinding(ws)
	if binding == nil {
		binding = &agentworkspace.MCPBinding{
			ID:         uuid.New().String(),
			ServerName: serverName,
			Enabled:    true,
			// Empty (non-nil) allowlist: no tools exposed until the mapping is
			// confirmed and saved, at which point the read-only allowlist is set.
			AllowedTools: []string{},
			// The calendar_ops Config marker (not CapabilityMappings, which is
			// empty until a mapping is confirmed and would be normalized away)
			// is what lets findCalendarBinding locate this binding through
			// every setup stage -- see its doc comment.
			Config: calendar.WriteBindingSettings(nil, calendar.BindingSettings{}),
		}
	} else {
		binding.ServerName = serverName
		binding.Enabled = true
	}
	if err := ws.UpsertMCPBinding(*binding); err != nil {
		orihttp.BadRequest(w, err.Error())
		return
	}
	if err := h.folders.Save(ws); err != nil {
		orihttp.InternalError(w, "Failed to save workspace: "+err.Error())
		return
	}
	userID, err := h.currentUserID(r.Context())
	if err != nil {
		orihttp.InternalError(w, "Failed to resolve current user: "+err.Error())
		return
	}
	_ = orihttp.RespondSuccess(w, h.buildStateResponse(r.Context(), ws, userID))
}

// suggestMappingsRequest asks for guided suggestions from a server's discovered
// tools. When server_name is given the handler discovers the tools; a caller
// may also pass explicit discovered tools (stateless).
type suggestMappingsRequest struct {
	ServerName      string           `json:"server_name,omitempty"`
	DiscoveredTools []discoveredTool `json:"discovered_tools,omitempty"`
}

type discoveredTool struct {
	Name                  string   `json:"name"`
	Description           string   `json:"description,omitempty"`
	InputSchemaProperties []string `json:"input_schema_properties,omitempty"`
}

type suggestMappingsResponse struct {
	Tools       []discoveredTool               `json:"tools"`
	Suggestions []calendar.OperationSuggestion `json:"suggestions"`
}

// SuggestMappings handles POST /api/calendar-ops/setup/suggest-mappings and
// returns guided, unconfirmed operation-mapping suggestions (FR12). It never
// activates a mapping — the user confirms in the advanced editor.
func (h *Handler) SuggestMappings(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	var req suggestMappingsRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	tools := req.DiscoveredTools
	if len(tools) == 0 && strings.TrimSpace(req.ServerName) != "" {
		discovered, err := h.discoverTools(req.ServerName)
		if err != nil {
			orihttp.BadRequest(w, err.Error())
			return
		}
		tools = discovered
	}
	converted := make([]calendar.DiscoveredTool, 0, len(tools))
	for _, t := range tools {
		converted = append(converted, calendar.DiscoveredTool{
			Name:                  t.Name,
			Description:           t.Description,
			InputSchemaProperties: t.InputSchemaProperties,
		})
	}
	_ = orihttp.RespondSuccess(w, suggestMappingsResponse{
		Tools:       tools,
		Suggestions: calendar.SuggestMappings(converted),
	})
}

// validateRequest carries the mapping to test against the live connector.
type validateRequest struct {
	WorkspaceID string                           `json:"workspace_id"`
	Mapping     agentworkspace.CapabilityMapping `json:"mapping"`
}

type validateResponse struct {
	MappingValid      bool                                 `json:"mapping_valid"`
	MappingError      string                               `json:"mapping_error,omitempty"`
	ValidationResults []calendar.OperationValidationResult `json:"validation_results,omitempty"`
	AllSucceeded      bool                                 `json:"all_succeeded"`
	Calendars         []calendar.Calendar                  `json:"calendars,omitempty"`
	CalendarsError    string                               `json:"calendars_error,omitempty"`
}

// Validate handles POST /api/calendar-ops/setup/validate: it structurally
// validates the proposed mapping, then runs the required read operations
// against the live connector and lists the connector's calendars so the user
// can choose which are visible (FR15). It performs no writes.
func (h *Handler) Validate(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	var req validateRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	ws, ok := h.loadCalendarOpsWorkspace(w, strings.TrimSpace(req.WorkspaceID))
	if !ok {
		return
	}
	if ws == nil {
		orihttp.BadRequest(w, "workspace is not a Calendar Ops workspace")
		return
	}
	binding, _ := findCalendarBinding(ws)
	if binding == nil {
		orihttp.BadRequest(w, "no calendar connector is selected yet")
		return
	}

	mapping := agentworkspace.NormalizeCapabilityMappings([]agentworkspace.CapabilityMapping{req.Mapping})
	resp := validateResponse{}
	if len(mapping) == 0 {
		resp.MappingError = "mapping has no calendar operations"
		_ = orihttp.RespondSuccess(w, resp)
		return
	}
	normalized := mapping[0]
	if err := calendar.ValidateMapping(normalized); err != nil {
		resp.MappingError = err.Error()
		_ = orihttp.RespondSuccess(w, resp)
		return
	}
	resp.MappingValid = true

	call := h.toolCallerFor(binding.ServerName)
	start, end := calendar.DefaultValidationTimeRange(nowUTC())
	listEventsArgs := buildListEventsProbeArgs(normalized, start, end)
	resp.ValidationResults = calendar.ValidateConnection(r.Context(), normalized, call, listEventsArgs)
	resp.AllSucceeded = calendar.AllSucceeded(resp.ValidationResults)

	if resp.AllSucceeded {
		cals, err := calendar.ListCalendars(r.Context(), normalized, call)
		if err != nil {
			resp.CalendarsError = err.Error()
		} else {
			resp.Calendars = cals
		}
	}
	_ = orihttp.RespondSuccess(w, resp)
}

// saveRequest is the final setup submission.
type saveRequest struct {
	WorkspaceID         string                           `json:"workspace_id"`
	Mapping             agentworkspace.CapabilityMapping `json:"mapping"`
	SelectedCalendarIDs []string                         `json:"selected_calendar_ids,omitempty"`
	DisplayTimeZone     string                           `json:"display_time_zone,omitempty"`
	ContextWorkspaceIDs []string                         `json:"context_workspace_ids,omitempty"`
}

// Save handles POST /api/calendar-ops/setup/save: it persists the confirmed
// mapping, the read-only tool allowlist, the calendar/timezone/context
// settings, and grants the calendar binding (read-only) to only the Scheduler
// and Meeting Prep agents. Context workspace ids are filtered to the current
// user's own workspaces (FR44 / 3.6).
func (h *Handler) Save(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	var req saveRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	userID, err := h.currentUserID(r.Context())
	if err != nil {
		orihttp.InternalError(w, "Failed to resolve current user: "+err.Error())
		return
	}
	ws, ok := h.loadCalendarOpsWorkspace(w, strings.TrimSpace(req.WorkspaceID))
	if !ok {
		return
	}
	if ws == nil {
		orihttp.BadRequest(w, "workspace is not a Calendar Ops workspace")
		return
	}

	if err := h.applySave(r.Context(), ws, userID, req); err != nil {
		orihttp.BadRequest(w, err.Error())
		return
	}
	if err := h.folders.Save(ws); err != nil {
		orihttp.InternalError(w, "Failed to save workspace: "+err.Error())
		return
	}
	_ = orihttp.RespondSuccess(w, h.buildStateResponse(r.Context(), ws, userID))
}

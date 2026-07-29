package calendarhttp

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/calendar"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/mcp"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// loadCalendarOpsWorkspace loads a workspace and reports whether it applies to
// Calendar Ops setup. ok=false means an HTTP error has already been written
// and the caller must return immediately. ok=true with a nil workspace means
// the load succeeded but the workspace is not (or is no longer) a Calendar
// Ops workspace -- callers decide how to report that (Setup responds
// applicable:false; the mutating endpoints respond 400).
func (h *Handler) loadCalendarOpsWorkspace(w http.ResponseWriter, workspaceID string) (*agentworkspace.Workspace, bool) {
	if workspaceID == "" {
		orihttp.BadRequest(w, "workspace_id is required")
		return nil, false
	}
	if h.folders == nil {
		orihttp.InternalError(w, "workspace storage is unavailable")
		return nil, false
	}
	ws, err := h.folders.GetFolderWorkspace(workspaceID)
	if err != nil {
		orihttp.NotFound(w, "workspace not found: "+err.Error())
		return nil, false
	}
	if ws == nil || !ws.IsFromTemplate(CalendarOpsTemplateID) {
		return nil, true
	}
	return ws, true
}

// findCalendarBinding returns the workspace's calendar connector binding, if
// any. The returned binding is a value copy (via GetMCPBindings' deep copy)
// safe for the caller to mutate and re-upsert.
//
// A binding is identified two ways because it must be findable at every
// setup stage, including before any operation is mapped:
//   - by a confirmed "calendar" capability mapping (post-save state), or
//   - by carrying the calendar_ops Config marker (Connector sets this on the
//     placeholder binding it creates before any mapping exists).
//
// The second check exists because workspace.NormalizeCapabilityMappings
// (run on every UpsertMCPBinding) drops a CapabilityMapping with zero
// operations -- an empty placeholder mapping would vanish on the very next
// save, making the binding unfindable. Config isn't touched by that
// normalization, so it survives as a stable marker across the whole flow.
func findCalendarBinding(ws *agentworkspace.Workspace) (*agentworkspace.MCPBinding, bool) {
	for _, b := range ws.GetMCPBindings() {
		if _, ok := b.FindCapabilityMapping(calendar.CapabilityKey); ok {
			binding := b
			return &binding, true
		}
		if _, marked := b.Config[calendar.BindingConfigKey]; marked {
			binding := b
			return &binding, true
		}
	}
	return nil, false
}

// derivedSetup is the part of the setup picture that does not depend on the
// requesting user: the workspace's binding, the connector's runtime status, and
// the state those reduce to.
type derivedSetup struct {
	state        calendar.SetupState
	binding      *agentworkspace.MCPBinding
	connector    *connectorStatus
	settings     calendar.BindingSettings
	mappingValid bool
}

// deriveSetupState computes the workspace's Calendar Ops setup state.
//
// It is factored out of buildStateResponse so the setup card, the Setup Wizard
// adapter, and anything else asking "where is this workspace up to?" get the
// same answer from the same code. A second derivation would be a second state
// machine, and the two would disagree the first time one of them was updated.
func (h *Handler) deriveSetupState(ws *agentworkspace.Workspace) derivedSetup {
	binding, hasBinding := findCalendarBinding(ws)
	in := calendar.SetupStateInput{HasBinding: hasBinding}
	out := derivedSetup{}
	if hasBinding {
		out.binding = binding
		out.settings = calendar.ReadBindingSettings(binding.Config)

		status := h.connectorStatusFn(binding.ServerName)
		out.connector = &status
		in.ConnectorPresent = status.Present
		in.Connected = status.Connected
		in.AuthRequired = status.AuthRequired
		in.Degraded = status.Degraded

		if mapping, ok := binding.FindCapabilityMapping(calendar.CapabilityKey); ok {
			out.mappingValid = calendar.ValidateMapping(mapping) == nil
		}
		in.MappingValid = out.mappingValid
		in.Validated = out.settings.Validated
	}
	out.state = calendar.DeriveSetupState(in)
	return out
}

// buildStateResponse assembles the full setup-state view: the derived
// SetupState, the current binding/connector/settings, the shipped presets
// (and whether each is already configured), existing MCP servers the user
// could point Calendar Ops at instead, and the current user's eligible
// meeting-prep context workspaces.
func (h *Handler) buildStateResponse(ctx context.Context, ws *agentworkspace.Workspace, userID string) setupStateResponse {
	resp := setupStateResponse{
		Applicable:  true,
		WorkspaceID: ws.ID,
		States:      calendar.AllSetupStates(),
		Requirement: &requirementView{
			Key:                calendar.CapabilityKey,
			RequiredOperations: calendar.RequiredOperations(),
			OptionalOperations: calendar.OptionalOperations(),
		},
		Presets: calendar.ShippedPresets(),
	}

	presetAdded := make(map[string]bool, len(resp.Presets))
	for _, p := range resp.Presets {
		presetAdded[p.ID] = h.serverConfigured(p.ServerName)
	}
	resp.PresetAdded = presetAdded
	resp.ExistingConnectors = h.existingConnectors()

	derived := h.deriveSetupState(ws)
	if derived.binding != nil {
		resp.Connector = derived.connector
		resp.Binding = &bindingView{
			ID:           derived.binding.ID,
			ServerName:   derived.binding.ServerName,
			HasMapping:   len(derived.binding.CapabilityMappings) > 0,
			MappingValid: derived.mappingValid,
			AllowedTools: derived.binding.AllowedTools,
			MappedOps:    mappedOperationNames(derived.binding),
		}
	}
	settings := derived.settings
	resp.State = derived.state
	resp.Settings = settingsView{
		SelectedCalendarIDs: settings.SelectedCalendarIDs,
		DisplayTimeZone:     settings.DisplayTimeZone,
		ContextWorkspaceIDs: settings.ContextWorkspaceIDs,
		Validated:           settings.Validated,
	}
	resp.ContextWorkspaceCandidates = h.contextWorkspaceCandidates(ctx, userID, ws.ID)
	return resp
}

func mappedOperationNames(binding *agentworkspace.MCPBinding) []string {
	mapping, ok := binding.FindCapabilityMapping(calendar.CapabilityKey)
	if !ok {
		return nil
	}
	names := make([]string, 0, len(mapping.Operations))
	for name := range mapping.Operations {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (h *Handler) serverConfigured(serverName string) bool {
	if h.config == nil {
		return false
	}
	_, err := h.config.GetServer(serverName)
	return err == nil
}

// existingConnectors lists every globally configured MCP server as a
// candidate the user could point Calendar Ops at instead of adding a preset.
// "Compatible" isn't pre-filtered here -- the user picks, then the guided
// mapping/validation step (Suggest/Validate) is what actually determines
// whether a chosen connector satisfies the calendar contract.
func (h *Handler) existingConnectors() []existingConnectorView {
	if h.config == nil {
		return nil
	}
	cfg, err := h.config.LoadGlobalConfig()
	if err != nil {
		return nil
	}
	out := make([]existingConnectorView, 0, len(cfg.Servers))
	for _, s := range cfg.Servers {
		out = append(out, existingConnectorView{
			Name:      s.Name,
			Transport: s.Transport,
			Remote:    mcp.IsRemoteTransport(s),
		})
	}
	return out
}

// contextWorkspaceCandidates returns the active, non-group workspaces owned
// by userID, excluding the Calendar Ops workspace itself, as the selectable
// pool for meeting-prep context (FR44). Ownership is enforced here, not just
// in the UI: Save's applySave re-filters against this same set, so a
// forged/stale client request can't grant prep access to a workspace the
// current user doesn't own.
func (h *Handler) contextWorkspaceCandidates(ctx context.Context, userID, excludeWorkspaceID string) []workspaceRefView {
	if h.lister == nil {
		return nil
	}
	all, err := h.lister.ListWorkspaces(ctx)
	if err != nil {
		return nil
	}
	wantOwner := strings.TrimSpace(userID)
	if wantOwner == "" {
		wantOwner = userprofile.LocalUserID
	}

	out := make([]workspaceRefView, 0, len(all))
	for _, w := range all {
		if w.ID == excludeWorkspaceID || w.IsGroup() {
			continue
		}
		if w.Status != "" && w.Status != session.WorkspaceStatusActive {
			continue
		}
		owner := strings.TrimSpace(w.OwnerUserID)
		if owner == "" {
			owner = userprofile.LocalUserID
		}
		if !strings.EqualFold(owner, wantOwner) {
			continue
		}
		out = append(out, workspaceRefView{ID: w.ID, Name: w.Name})
	}
	return out
}

// ensurePresetServer registers a shipped connector preset in the global MCP
// registry/config if it isn't already configured. Idempotent: re-adding an
// already-configured preset is a no-op, so repeated Connector calls (a user
// revisiting setup) never error.
func (h *Handler) ensurePresetServer(preset calendar.ConnectorPreset) error {
	if h.config == nil || h.registry == nil {
		return fmt.Errorf("mcp registry is unavailable")
	}
	if _, err := h.config.GetServer(preset.ServerName); err == nil {
		return nil
	}
	cfg := mcp.ServerConfig{
		Name:      preset.ServerName,
		Transport: preset.Transport,
		URL:       preset.URL,
		Enabled:   true,
	}
	if err := h.config.AddServer(cfg); err != nil {
		return err
	}
	return h.registry.AddServer(cfg)
}

// discoverTools lists a server's tools for the guided-suggestion step,
// lazily starting it if it's stopped/errored -- mirroring the existing
// mcphttp.GetServerToolsHandler lazy-start pattern.
func (h *Handler) discoverTools(serverName string) ([]discoveredTool, error) {
	if h.registry == nil {
		return nil, fmt.Errorf("mcp registry is unavailable")
	}
	server, err := h.registry.GetServer(serverName)
	if err != nil {
		return nil, err
	}
	switch server.GetStatus() {
	case mcp.StatusStopped, mcp.StatusError:
		if server.GetStatus() == mcp.StatusError {
			_ = h.registry.StopServer(serverName)
		}
		if err := h.registry.StartServer(serverName); err != nil {
			return nil, fmt.Errorf("failed to start connector: %w", err)
		}
	}

	tools := server.GetTools()
	out := make([]discoveredTool, 0, len(tools))
	for _, t := range tools {
		out = append(out, discoveredTool{
			Name:                  t.Name,
			Description:           t.Description,
			InputSchemaProperties: inputSchemaPropertyNames(t),
		})
	}
	return out, nil
}

// inputSchemaPropertyNames extracts a discovered tool's declared JSON Schema
// input property names. Tools discovered from a remote server always decode
// InputSchema as map[string]any (per the SDK's Tool.InputSchema doc), so
// this only needs to handle that shape.
func inputSchemaPropertyNames(t mcp.Tool) []string {
	schema, ok := t.InputSchema.(map[string]any)
	if !ok {
		return nil
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		return nil
	}
	names := make([]string, 0, len(props))
	for name := range props {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// buildListEventsProbeArgs builds the (best-effort) bounded arguments for the
// list_events connection-test probe. list_events is a read operation, so the
// calendar contract doesn't require argument pointers for it (see
// internal/calendar's operationContracts) -- an advanced mapping may still
// declare start_time/end_time argument pointers, in which case this bounds
// the probe; otherwise the probe call carries no arguments and relies on the
// connector's own default result size.
func buildListEventsProbeArgs(mapping agentworkspace.CapabilityMapping, start, end string) map[string]any {
	op, ok := mapping.Operation(calendar.OpListEvents)
	if !ok {
		return nil
	}
	args, err := calendar.BuildArguments(map[string]any{"start_time": start, "end_time": end}, op)
	if err != nil {
		return nil
	}
	return args
}

func nowUTC() time.Time { return time.Now().UTC() }

// applySave validates and normalizes the final setup submission, then
// persists it: the confirmed mapping, the derived read-only tool allowlist,
// calendar/timezone/context settings, and the roster access grant (FR27,
// 3.6). Context workspace ids are re-filtered against the current user's own
// active workspaces server-side, not merely trusted from the request.
func (h *Handler) applySave(ctx context.Context, ws *agentworkspace.Workspace, userID string, req saveRequest) error {
	binding, ok := findCalendarBinding(ws)
	if !ok {
		return fmt.Errorf("no calendar connector is selected yet")
	}

	mappings := agentworkspace.NormalizeCapabilityMappings([]agentworkspace.CapabilityMapping{req.Mapping})
	if len(mappings) == 0 {
		return fmt.Errorf("mapping has no calendar operations")
	}
	mapping := mappings[0]
	if err := calendar.ValidateMapping(mapping); err != nil {
		return fmt.Errorf("invalid mapping: %w", err)
	}

	settings := calendar.BindingSettings{
		SelectedCalendarIDs: req.SelectedCalendarIDs,
		DisplayTimeZone:     req.DisplayTimeZone,
		ContextWorkspaceIDs: h.filterOwnedActiveWorkspaceIDs(ctx, userID, ws.ID, req.ContextWorkspaceIDs),
		Validated:           true,
	}.Normalize()

	binding.CapabilityMappings = []agentworkspace.CapabilityMapping{mapping}
	binding.AllowedTools = calendar.ReadOnlyAllowedTools(mapping)
	// DefaultSideEffect=read matches the allowlist above (only read tools are
	// ever agent-callable); ToolOverrides gives the autonomy gate exact
	// per-tool attribution, classifying the mapped write tools as external
	// even though they're never exposed to agents (FR28, defense in depth).
	// Setting a valid default also satisfies MissionBindingsReady so Calendar
	// Ops workspaces don't hit the one-time "classify this binding" prompt.
	binding.DefaultSideEffect = agentworkspace.SideEffectRead
	binding.ToolOverrides = calendar.ToolSideEffectOverrides(mapping)
	binding.Config = calendar.WriteBindingSettings(binding.Config, settings)
	binding.Enabled = true

	if err := ws.UpsertMCPBinding(*binding); err != nil {
		return err
	}
	return h.grantCalendarBindingToRoster(ws, binding.ID)
}

// filterOwnedActiveWorkspaceIDs keeps only the requested ids that are also in
// the current user's context-workspace candidate set.
func (h *Handler) filterOwnedActiveWorkspaceIDs(ctx context.Context, userID, excludeWorkspaceID string, requested []string) []string {
	if len(requested) == 0 {
		return nil
	}
	candidates := h.contextWorkspaceCandidates(ctx, userID, excludeWorkspaceID)
	allowed := make(map[string]struct{}, len(candidates))
	for _, c := range candidates {
		allowed[c.ID] = struct{}{}
	}
	out := make([]string, 0, len(requested))
	for _, id := range requested {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := allowed[id]; ok {
			out = append(out, id)
		}
	}
	return out
}

// calendarOpsRosterAgent reports whether name is one of the Calendar Ops
// roster agents entitled to the calendar binding.
func calendarOpsRosterAgent(name string) bool {
	return slices.Contains(calendarOpsAgentNames, name)
}

// grantCalendarBindingToRoster ensures the calendar binding is usable only by
// the Scheduler and Meeting Prep agent instances (FR27): roster agents with
// an existing (restrictive) access entry get the binding added to it;
// non-roster agents are given (or already have) an access entry that
// excludes it. An agent instance with no access entry defaults to "all
// bindings allowed" (see AgentMCPAccess), so a roster agent with no entry
// already has access and needs no write.
func (h *Handler) grantCalendarBindingToRoster(ws *agentworkspace.Workspace, bindingID string) error {
	for _, agent := range ws.AgentInstances {
		access, hasAccess := ws.GetAgentMCPAccess(agent.ID)

		if calendarOpsRosterAgent(agent.Name) {
			if !hasAccess || slices.Contains(access.EnabledBindingIDs, bindingID) {
				continue
			}
			updated := append(append([]string{}, access.EnabledBindingIDs...), bindingID)
			if err := ws.SetAgentMCPAccess(agentworkspace.AgentMCPAccess{AgentInstanceID: agent.ID, EnabledBindingIDs: updated}); err != nil {
				return err
			}
			continue
		}

		if !hasAccess {
			// No entry yet defaults to "all bindings" -- write an explicit
			// allowlist of every OTHER currently-enabled binding so this
			// agent's existing access is preserved minus the new calendar one.
			var allowed []string
			for _, b := range ws.GetMCPBindings() {
				if b.ID != bindingID && b.Enabled {
					allowed = append(allowed, b.ID)
				}
			}
			if err := ws.SetAgentMCPAccess(agentworkspace.AgentMCPAccess{AgentInstanceID: agent.ID, EnabledBindingIDs: allowed}); err != nil {
				return err
			}
			continue
		}
		if slices.Contains(access.EnabledBindingIDs, bindingID) {
			filtered := make([]string, 0, len(access.EnabledBindingIDs))
			for _, id := range access.EnabledBindingIDs {
				if id != bindingID {
					filtered = append(filtered, id)
				}
			}
			if err := ws.SetAgentMCPAccess(agentworkspace.AgentMCPAccess{AgentInstanceID: agent.ID, EnabledBindingIDs: filtered}); err != nil {
				return err
			}
		}
	}
	return nil
}

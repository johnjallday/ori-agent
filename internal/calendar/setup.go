package calendar

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// BindingConfigKey is the key under an MCPBinding.Config map where Calendar Ops
// stores its per-workspace connector settings (selected calendars, display
// timezone, permitted meeting-prep context workspaces). Storing settings inside
// the binding's existing Config map reuses the workspace layer's established
// deep-copy plumbing (cloneInterfaceMap) rather than introducing a new
// persistence path.
const BindingConfigKey = "calendar_ops"

// BindingSettings are the Calendar Ops choices persisted on the workspace's
// calendar MCP binding once setup completes. It deliberately holds no event
// data — Calendar reads stay live (FR34); only these small selections persist.
type BindingSettings struct {
	// SelectedCalendarIDs are the connector calendar ids the user chose to make
	// visible. An empty slice means "not yet chosen".
	SelectedCalendarIDs []string `json:"selected_calendar_ids,omitempty"`
	// DisplayTimeZone is the IANA timezone the agenda renders in.
	DisplayTimeZone string `json:"display_time_zone,omitempty"`
	// ContextWorkspaceIDs are the user-owned Ori workspaces Meeting Prep may
	// read as context. Enforced against current ownership at prep time (group 6).
	ContextWorkspaceIDs []string `json:"context_workspace_ids,omitempty"`
	// Validated records that the required read operations passed a connection
	// test at least once for the current mapping. It gates the "ready" setup
	// state; a later mapping change should clear it.
	Validated bool `json:"validated,omitempty"`
}

// Normalize trims/de-duplicates the id slices and trims the timezone,
// preserving a stable order so persistence round-trips deterministically.
func (s BindingSettings) Normalize() BindingSettings {
	return BindingSettings{
		SelectedCalendarIDs: dedupeStrings(s.SelectedCalendarIDs),
		DisplayTimeZone:     strings.TrimSpace(s.DisplayTimeZone),
		ContextWorkspaceIDs: dedupeStrings(s.ContextWorkspaceIDs),
		Validated:           s.Validated,
	}
}

// ReadBindingSettings extracts Calendar Ops settings from an MCPBinding.Config
// map. A binding with no calendar_ops entry (or a malformed one) yields a
// zero-value BindingSettings, never an error — a partially-configured workspace
// must still resolve a setup state.
func ReadBindingSettings(config map[string]any) BindingSettings {
	var out BindingSettings
	if len(config) == 0 {
		return out
	}
	raw, ok := config[BindingConfigKey].(map[string]any)
	if !ok {
		return out
	}
	out.SelectedCalendarIDs = stringSliceFromAny(raw["selected_calendar_ids"])
	out.DisplayTimeZone = stringFromAny(raw["display_time_zone"])
	out.ContextWorkspaceIDs = stringSliceFromAny(raw["context_workspace_ids"])
	out.Validated = boolFromAny(raw["validated"])
	return out.Normalize()
}

// WriteBindingSettings returns a copy of config with the Calendar Ops settings
// stored under BindingConfigKey. A nil input yields a fresh map. The values are
// plain JSON-friendly types so the workspace layer's JSON-based deep copy
// round-trips them.
func WriteBindingSettings(config map[string]any, settings BindingSettings) map[string]any {
	out := make(map[string]any, len(config)+1)
	for k, v := range config {
		out[k] = v
	}
	settings = settings.Normalize()
	entry := map[string]any{}
	if len(settings.SelectedCalendarIDs) > 0 {
		entry["selected_calendar_ids"] = toAnySlice(settings.SelectedCalendarIDs)
	}
	if settings.DisplayTimeZone != "" {
		entry["display_time_zone"] = settings.DisplayTimeZone
	}
	if len(settings.ContextWorkspaceIDs) > 0 {
		entry["context_workspace_ids"] = toAnySlice(settings.ContextWorkspaceIDs)
	}
	if settings.Validated {
		entry["validated"] = true
	}
	out[BindingConfigKey] = entry
	return out
}

// ReadOnlyAllowedTools returns the connector tool names that may be granted to
// Calendar Ops agents: every mapped *read* operation's tool, never a write
// (create_event/update_event/connect_account). The result is sorted and
// de-duplicated so it persists deterministically and so the allowlist a binding
// carries is stable across saves. This is the read-only allowlist Calendar Ops
// persists (FR27); runtime enforcement of AllowedTools is added in a later
// group.
func ReadOnlyAllowedTools(mapping workspace.CapabilityMapping) []string {
	seen := make(map[string]struct{})
	var out []string
	for name, op := range mapping.Operations {
		contract, known := operationContracts[strings.ToLower(strings.TrimSpace(name))]
		if !known || contract.IsWrite {
			continue
		}
		tool := strings.TrimSpace(op.Tool)
		if tool == "" {
			continue
		}
		key := strings.ToLower(tool)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, tool)
	}
	sort.Strings(out)
	return out
}

// IsReadOperation reports whether a semantic calendar operation is a read (safe
// to grant to agents) rather than a write. Unknown names are treated as
// non-read so an unrecognized operation never widens the allowlist.
func IsReadOperation(operation string) bool {
	contract, known := operationContracts[strings.ToLower(strings.TrimSpace(operation))]
	return known && !contract.IsWrite
}

// SetupState is a stable, UI-facing Calendar Ops connector setup state. The
// values are contract strings shared with the frontend; do not rename them.
type SetupState string

const (
	// SetupConnectorMissing: no calendar connector is bound to the workspace
	// (or the bound connector no longer exists). The user must choose or add one.
	SetupConnectorMissing SetupState = "connector_missing"
	// SetupAuthRequired: a connector is bound but not authenticated/connected
	// (first-time auth, revoked token, or reconnect needed).
	SetupAuthRequired SetupState = "auth_required"
	// SetupMappingRequired: connected, but the semantic operation mapping is
	// missing or invalid.
	SetupMappingRequired SetupState = "mapping_required"
	// SetupValidationFailed: mapping present but the connection test has not
	// passed for it yet.
	SetupValidationFailed SetupState = "validation_failed"
	// SetupReady: connected, mapped, validated — Calendar Ops is usable.
	SetupReady SetupState = "ready"
	// SetupDegraded: previously set up and validated, but the connector is
	// currently erroring/unreachable. Distinct from auth_required so the UI can
	// tell "finish setup" apart from "temporary connector trouble".
	SetupDegraded SetupState = "degraded"
)

// AllSetupStates lists every setup state, required only, in a stable order.
func AllSetupStates() []SetupState {
	return []SetupState{
		SetupConnectorMissing,
		SetupAuthRequired,
		SetupMappingRequired,
		SetupValidationFailed,
		SetupReady,
		SetupDegraded,
	}
}

// SetupStateInput is the deterministic input to DeriveSetupState. The caller
// (the setup HTTP handler) computes these booleans from the workspace binding,
// the MCP server's runtime status, and mapping validation.
type SetupStateInput struct {
	// HasBinding is true when the workspace has an MCP binding carrying a
	// "calendar" capability mapping.
	HasBinding bool
	// ConnectorPresent is true when the bound MCP server still exists in the
	// registry/config.
	ConnectorPresent bool
	// Connected is true when the bound server is running (authenticated).
	Connected bool
	// AuthRequired is true when the server reports it needs (re)authentication.
	AuthRequired bool
	// MappingValid is true when the binding's calendar mapping passes
	// ValidateMapping.
	MappingValid bool
	// Validated is true when the persisted BindingSettings.Validated flag is set
	// (a connection test passed for the current mapping).
	Validated bool
	// Degraded is true when the bound server is in an error/unreachable state.
	Degraded bool
}

// DeriveSetupState reduces the input to a single stable setup state. Precedence:
// a missing connector dominates everything; an otherwise-complete connector that
// is currently erroring is degraded (not "unauthenticated"); then authentication,
// then mapping, then validation gate readiness in order.
func DeriveSetupState(in SetupStateInput) SetupState {
	if !in.HasBinding || !in.ConnectorPresent {
		return SetupConnectorMissing
	}
	if in.Degraded && in.MappingValid && in.Validated {
		return SetupDegraded
	}
	if in.AuthRequired || !in.Connected {
		return SetupAuthRequired
	}
	if !in.MappingValid {
		return SetupMappingRequired
	}
	if !in.Validated {
		return SetupValidationFailed
	}
	return SetupReady
}

// ListCalendars invokes the mapped list_calendars operation through call and
// deterministically assembles the connector's calendars into canonical
// Calendar values. It is the setup step that lets the user pick visible
// calendars. Like ValidateConnection it depends only on the ToolCaller seam, so
// it is testable with a fake connector. A calendar item with no resolvable id
// is skipped rather than surfaced as a blank entry.
func ListCalendars(ctx context.Context, mapping workspace.CapabilityMapping, call ToolCaller) ([]Calendar, error) {
	op, ok := mapping.Operation(OpListCalendars)
	if !ok {
		return nil, fmt.Errorf("%q is not mapped", OpListCalendars)
	}
	if call == nil {
		return nil, fmt.Errorf("no connector call available")
	}
	raw, err := call(ctx, op.Tool, map[string]any{})
	if err != nil {
		return nil, fmt.Errorf("connector call failed: %w", err)
	}
	items, err := Collection(raw, op)
	if err != nil {
		return nil, err
	}
	out := make([]Calendar, 0, len(items))
	for _, item := range items {
		cal := ApplyCalendar(item, op)
		if cal.ID == "" {
			continue
		}
		out = append(out, cal)
	}
	return out, nil
}

func dedupeStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, dup := seen[v]; dup {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func toAnySlice(in []string) []any {
	out := make([]any, len(in))
	for i, v := range in {
		out[i] = v
	}
	return out
}

func stringSliceFromAny(v any) []string {
	list, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, entry := range list {
		if s, ok := entry.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

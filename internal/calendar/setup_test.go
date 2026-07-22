package calendar

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestDeriveSetupState_Transitions(t *testing.T) {
	base := SetupStateInput{
		HasBinding:       true,
		ConnectorPresent: true,
		Connected:        true,
		MappingValid:     true,
		Validated:        true,
	}
	cases := []struct {
		name string
		in   SetupStateInput
		want SetupState
	}{
		{
			name: "no binding is connector_missing",
			in:   SetupStateInput{},
			want: SetupConnectorMissing,
		},
		{
			name: "binding but connector gone is connector_missing",
			in:   SetupStateInput{HasBinding: true, ConnectorPresent: false, Connected: true, MappingValid: true, Validated: true},
			want: SetupConnectorMissing,
		},
		{
			name: "present but unauthenticated is auth_required",
			in:   SetupStateInput{HasBinding: true, ConnectorPresent: true, Connected: false},
			want: SetupAuthRequired,
		},
		{
			name: "explicit auth-required flag wins over connected",
			in:   SetupStateInput{HasBinding: true, ConnectorPresent: true, Connected: true, AuthRequired: true},
			want: SetupAuthRequired,
		},
		{
			name: "connected but no valid mapping is mapping_required",
			in:   SetupStateInput{HasBinding: true, ConnectorPresent: true, Connected: true, MappingValid: false},
			want: SetupMappingRequired,
		},
		{
			name: "mapped but not validated is validation_failed",
			in:   SetupStateInput{HasBinding: true, ConnectorPresent: true, Connected: true, MappingValid: true, Validated: false},
			want: SetupValidationFailed,
		},
		{
			name: "all good is ready",
			in:   base,
			want: SetupReady,
		},
		{
			name: "validated connector currently erroring is degraded",
			in:   SetupStateInput{HasBinding: true, ConnectorPresent: true, Connected: true, MappingValid: true, Validated: true, Degraded: true},
			want: SetupDegraded,
		},
		{
			name: "erroring but never validated stays auth_required not degraded",
			in:   SetupStateInput{HasBinding: true, ConnectorPresent: true, Connected: false, MappingValid: true, Validated: false, Degraded: true},
			want: SetupAuthRequired,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DeriveSetupState(tc.in); got != tc.want {
				t.Fatalf("DeriveSetupState(%+v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestBindingSettings_RoundTrip(t *testing.T) {
	settings := BindingSettings{
		SelectedCalendarIDs: []string{"primary", "team@example.com", "primary"}, // dup collapses
		DisplayTimeZone:     " America/New_York ",
		ContextWorkspaceIDs: []string{"ws-1", "ws-2"},
		Validated:           true,
	}
	config := WriteBindingSettings(nil, settings)
	got := ReadBindingSettings(config)

	wantCals := []string{"primary", "team@example.com"}
	if !reflect.DeepEqual(got.SelectedCalendarIDs, wantCals) {
		t.Fatalf("SelectedCalendarIDs = %v, want %v", got.SelectedCalendarIDs, wantCals)
	}
	if got.DisplayTimeZone != "America/New_York" {
		t.Fatalf("DisplayTimeZone = %q, want trimmed 'America/New_York'", got.DisplayTimeZone)
	}
	if !reflect.DeepEqual(got.ContextWorkspaceIDs, []string{"ws-1", "ws-2"}) {
		t.Fatalf("ContextWorkspaceIDs = %v", got.ContextWorkspaceIDs)
	}
	if !got.Validated {
		t.Fatal("Validated should round-trip true")
	}
}

func TestWriteBindingSettings_PreservesOtherConfigKeys(t *testing.T) {
	config := map[string]any{"unrelated": "keep-me"}
	out := WriteBindingSettings(config, BindingSettings{DisplayTimeZone: "UTC"})
	if out["unrelated"] != "keep-me" {
		t.Fatalf("WriteBindingSettings dropped an unrelated config key: %+v", out)
	}
	// Original map must not be mutated (deep-copy discipline).
	if _, exists := config[BindingConfigKey]; exists {
		t.Fatal("WriteBindingSettings mutated the input config map")
	}
}

func TestReadBindingSettings_MalformedIsZeroValue(t *testing.T) {
	// A calendar_ops entry of the wrong type must not panic or error.
	got := ReadBindingSettings(map[string]any{BindingConfigKey: "not-an-object"})
	if len(got.SelectedCalendarIDs) != 0 || got.DisplayTimeZone != "" || got.Validated {
		t.Fatalf("malformed settings should read as zero value, got %+v", got)
	}
}

func TestReadOnlyAllowedTools_ExcludesWrites(t *testing.T) {
	// googleShapedMapping maps list_calendars (calendars_list), list_events
	// (events_list) and create_event (events_insert, a write). The write tool
	// must never appear in the allowlist.
	got := ReadOnlyAllowedTools(googleShapedMapping())
	want := []string{"calendars_list", "events_list"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ReadOnlyAllowedTools = %v, want %v (writes excluded, sorted)", got, want)
	}
	for _, tool := range got {
		if tool == "events_insert" {
			t.Fatal("events_insert (create_event) must never be in the read allowlist")
		}
	}
}

func TestReadOnlyAllowedTools_IncludesMappedOptionalReads(t *testing.T) {
	mapping := workspace.CapabilityMapping{
		Capability: CapabilityKey,
		Operations: map[string]workspace.OperationMapping{
			OpListCalendars:  {Tool: "cal_list"},
			OpListEvents:     {Tool: "evt_list"},
			OpFreeBusy:       {Tool: "avail"},
			OpUpdateEvent:    {Tool: "evt_patch"},    // write -- excluded
			OpConnectAccount: {Tool: "acct_connect"}, // write -- excluded
		},
	}
	got := ReadOnlyAllowedTools(mapping)
	want := []string{"avail", "cal_list", "evt_list"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ReadOnlyAllowedTools = %v, want %v", got, want)
	}
}

func TestIsReadOperation(t *testing.T) {
	reads := []string{OpListCalendars, OpListEvents, OpGetEvent, OpFreeBusy, OpSuggestTime, OpListAccounts}
	for _, op := range reads {
		if !IsReadOperation(op) {
			t.Fatalf("%q should be a read operation", op)
		}
	}
	writes := []string{OpCreateEvent, OpUpdateEvent, OpConnectAccount}
	for _, op := range writes {
		if IsReadOperation(op) {
			t.Fatalf("%q must not be classified as a read operation", op)
		}
	}
	if IsReadOperation("nonsense") {
		t.Fatal("an unknown operation must not be treated as read")
	}
}

func TestToolSideEffectOverrides_ClassifiesReadsAndWrites(t *testing.T) {
	got := ToolSideEffectOverrides(googleShapedMapping())
	want := map[string]workspace.SideEffect{
		"calendars_list": workspace.SideEffectRead,
		"events_list":    workspace.SideEffectRead,
		"events_insert":  workspace.SideEffectExternal,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ToolSideEffectOverrides = %v, want %v", got, want)
	}
}

func TestToolSideEffectOverrides_UnmappedYieldsNil(t *testing.T) {
	mapping := workspace.CapabilityMapping{Capability: CapabilityKey}
	if got := ToolSideEffectOverrides(mapping); got != nil {
		t.Fatalf("ToolSideEffectOverrides(empty mapping) = %v, want nil", got)
	}
}

func TestListCalendars_GoogleShaped(t *testing.T) {
	call := func(ctx context.Context, tool string, args map[string]any) (any, error) {
		if tool != "calendars_list" {
			return nil, fmt.Errorf("unexpected tool %q", tool)
		}
		return map[string]any{"items": []any{
			map[string]any{"id": "primary", "summary": "me@example.com", "primary": true, "timeZone": "America/New_York"},
			map[string]any{"id": "team@example.com", "summary": "Team"},
			map[string]any{"summary": "no id -- skipped"},
		}}, nil
	}
	cals, err := ListCalendars(context.Background(), googleShapedMapping(), call)
	if err != nil {
		t.Fatalf("ListCalendars: %v", err)
	}
	if len(cals) != 2 {
		t.Fatalf("expected 2 calendars (id-less item skipped), got %d: %+v", len(cals), cals)
	}
	if cals[0].ID != "primary" || cals[0].Name != "me@example.com" || !cals[0].Primary {
		t.Fatalf("first calendar mapped wrong: %+v", cals[0])
	}
	if cals[0].TimeZone != "America/New_York" {
		t.Fatalf("timezone not mapped: %+v", cals[0])
	}
}

func TestListCalendars_AlternateShapedConnector(t *testing.T) {
	// A differently named/shaped connector proves the semantic contract is not
	// Google-specific: different tool name, different collection pointer, and a
	// flat field layout.
	mapping := workspace.CapabilityMapping{
		Capability: CapabilityKey,
		Operations: map[string]workspace.OperationMapping{
			OpListCalendars: {
				Tool:             "get_calendars",
				ResultCollection: "/data/calendars",
				Fields:           map[string]string{"id": "/uid", "name": "/label"},
			},
		},
	}
	call := func(ctx context.Context, tool string, args map[string]any) (any, error) {
		if tool != "get_calendars" {
			return nil, fmt.Errorf("unexpected tool %q", tool)
		}
		return map[string]any{"data": map[string]any{"calendars": []any{
			map[string]any{"uid": "cal-a", "label": "Work"},
			map[string]any{"uid": "cal-b", "label": "Home"},
		}}}, nil
	}
	cals, err := ListCalendars(context.Background(), mapping, call)
	if err != nil {
		t.Fatalf("ListCalendars: %v", err)
	}
	if len(cals) != 2 || cals[1].ID != "cal-b" || cals[1].Name != "Home" {
		t.Fatalf("alternate connector mapped wrong: %+v", cals)
	}
}

func TestListCalendars_UnmappedIsError(t *testing.T) {
	mapping := workspace.CapabilityMapping{Capability: CapabilityKey}
	if _, err := ListCalendars(context.Background(), mapping, func(context.Context, string, map[string]any) (any, error) { return nil, nil }); err == nil {
		t.Fatal("expected an error when list_calendars is not mapped")
	}
}

func TestListCalendars_FreeTextIsError(t *testing.T) {
	call := func(ctx context.Context, tool string, args map[string]any) (any, error) {
		return "here are your calendars", nil
	}
	if _, err := ListCalendars(context.Background(), googleShapedMapping(), call); err == nil {
		t.Fatal("a free-text connector response must be an error, never reinterpreted")
	}
}

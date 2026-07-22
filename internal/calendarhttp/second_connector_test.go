package calendarhttp

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/johnjallday/ori-agent/internal/calendar"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// alternateConnectorMapping is a full, real-shaped calendar mapping for a
// connector that looks nothing like Google Calendar: different tool names
// (get_calendars/get_events/insert_event vs calendars_list/events_list/
// events_insert), a different result envelope (/data/calendars, /results vs
// /items), different field names (calId/label/eventId/name vs id/summary),
// and a nested time shape (when.begins/when.ends vs start.dateTime/
// end.dateTime). Mirrors the shape already exercised at the domain layer by
// TestApplyEvent_AlternateShaped/TestValidateMapping_AlternateShapedSucceeds/
// TestListCalendars_AlternateShapedConnector -- this file drives the SAME
// shape through the full HTTP route pipeline (task 8.1).
func alternateConnectorMapping() agentworkspace.CapabilityMapping {
	return agentworkspace.CapabilityMapping{
		Capability: calendar.CapabilityKey,
		Operations: map[string]agentworkspace.OperationMapping{
			calendar.OpListCalendars: {
				Tool:             "get_calendars",
				ResultCollection: "/data/calendars",
				Fields:           map[string]string{"id": "/calId", "name": "/label"},
			},
			calendar.OpListEvents: {
				Tool:             "get_events",
				ResultCollection: "/results",
				Fields: map[string]string{
					"id": "/eventId", "title": "/name",
					"start_time": "/when/begins", "end_time": "/when/ends",
				},
				Arguments: map[string]string{
					"calendar_id": "/calId", "start_time": "/rangeStart", "end_time": "/rangeEnd",
				},
			},
			calendar.OpCreateEvent: {
				Tool: "insert_event",
				Fields: map[string]string{
					"id": "/eventId", "title": "/name",
					"start_time": "/when/begins", "end_time": "/when/ends",
				},
				Arguments: map[string]string{
					"calendar_id": "/calId", "title": "/name",
					"start_time": "/when/begins", "end_time": "/when/ends",
				},
			},
		},
	}
}

// TestSecondConnectorShape_FullFlowThroughDiscoveryMappingValidationAgendaAndMutation
// proves the Calendar Ops pipeline is shape-agnostic end to end (task 8.1),
// not merely correct for Google Calendar's specific field/tool names: it
// drives a second, differently-shaped fixture through discovery (guided
// suggestions), validation, save, an agenda read, and the full mutation
// preview/confirm boundary.
func TestSecondConnectorShape_FullFlowThroughDiscoveryMappingValidationAgendaAndMutation(t *testing.T) {
	ws := newCalendarOpsWorkspace("ws-alt")
	ws.OwnerUserID = "local"
	binding := agentworkspace.MCPBinding{
		ID:           "alt-binding",
		ServerName:   "alt-calendar",
		Enabled:      true,
		AllowedTools: []string{},
		Config:       calendar.WriteBindingSettings(nil, calendar.BindingSettings{}),
	}
	if err := ws.UpsertMCPBinding(binding); err != nil {
		t.Fatalf("UpsertMCPBinding: %v", err)
	}

	store := newFakeFolderStore()
	store.workspaces[ws.ID] = ws
	h := NewHandler(store, &fakeWorkspaceLister{}, nil, nil, fakeUserProvider{id: "local"})
	h.WithConnectorStatusFn(func(string) connectorStatus { return readyStatus })
	rec := &recordingToolCaller{}
	h.WithToolCallerFactory(func(string) calendar.ToolCaller { return rec.asToolCaller() })

	// --- 1. Discovery + guided mapping: SuggestMappings must recognize
	// this connector's tools by name/description synonym matching, proving
	// the guided step isn't hardcoded to Google's tool names. Fields for
	// reads are always a deliberate manual step (this package's contract,
	// see suggest.go) -- only create_event's Arguments are prefillable
	// here, from the tool's declared input schema.
	suggestW := doJSONRequest(t, h.SuggestMappings, http.MethodPost, "/api/calendar-ops/setup/suggest-mappings", suggestMappingsRequest{
		DiscoveredTools: []discoveredTool{
			{Name: "get_calendars", Description: "Get the user's calendars"},
			{Name: "get_events", Description: "Get events for a calendar in a range"},
			{
				Name: "insert_event", Description: "Insert a new event",
				InputSchemaProperties: []string{"calId", "name", "when"},
			},
		},
	})
	if suggestW.Code != http.StatusOK {
		t.Fatalf("SuggestMappings status = %d, body=%s", suggestW.Code, suggestW.Body.String())
	}
	suggestResp := decodeSuccess[suggestMappingsResponse](t, suggestW)
	for _, op := range []string{calendar.OpListCalendars, calendar.OpListEvents, calendar.OpCreateEvent} {
		if _, ok := calendar.SuggestionForOperation(suggestResp.Suggestions, op); !ok {
			t.Fatalf("expected a guided suggestion for %q from this connector's tool names, got %+v", op, suggestResp.Suggestions)
		}
	}

	// --- 2. Validate the confirmed mapping against the live (fake) connector.
	mapping := alternateConnectorMapping()
	rec.resultFn = func(tool string, args map[string]any) (any, error) {
		switch tool {
		case "get_calendars":
			return map[string]any{"data": map[string]any{"calendars": []any{
				map[string]any{"calId": "primary", "label": "Personal"},
			}}}, nil
		case "get_events":
			return map[string]any{"results": []any{
				map[string]any{
					"eventId": "e-1", "name": "Kickoff",
					"when": map[string]any{"begins": "2026-07-21T09:00:00Z", "ends": "2026-07-21T09:30:00Z"},
				},
			}}, nil
		case "insert_event":
			return map[string]any{
				"eventId": "e-new", "name": "Planning Sync",
				"when": map[string]any{"begins": "2026-07-21T13:00:00Z", "ends": "2026-07-21T13:30:00Z"},
			}, nil
		default:
			return nil, fmt.Errorf("unexpected tool %q", tool)
		}
	}
	validateW := doJSONRequest(t, h.Validate, http.MethodPost, "/api/calendar-ops/setup/validate", validateRequest{
		WorkspaceID: ws.ID, Mapping: mapping,
	})
	if validateW.Code != http.StatusOK {
		t.Fatalf("Validate status = %d, body=%s", validateW.Code, validateW.Body.String())
	}
	validateResp := decodeSuccess[validateResponse](t, validateW)
	if !validateResp.MappingValid {
		t.Fatalf("expected mapping_valid=true for a well-formed differently-shaped mapping, got error=%q", validateResp.MappingError)
	}

	// --- 3. Save: persists the mapping, read-only allowlist, and settings.
	saveW := doJSONRequest(t, h.Save, http.MethodPost, "/api/calendar-ops/setup/save", saveRequest{
		WorkspaceID:         ws.ID,
		Mapping:             mapping,
		SelectedCalendarIDs: []string{"primary"},
	})
	if saveW.Code != http.StatusOK {
		t.Fatalf("Save status = %d, body=%s", saveW.Code, saveW.Body.String())
	}
	saveResp := decodeSuccess[setupStateResponse](t, saveW)
	if saveResp.State != calendar.SetupReady {
		t.Fatalf("expected state=ready after save, got %q", saveResp.State)
	}

	// --- 4. Agenda read through the normal gateway route.
	eventsW := doJSONRequest(t, h.Events, http.MethodGet,
		"/api/calendar-ops/events?workspace_id="+ws.ID+"&start=2026-07-21T00:00:00Z&end=2026-07-22T00:00:00Z", nil)
	if eventsW.Code != http.StatusOK {
		t.Fatalf("Events status = %d, body=%s", eventsW.Code, eventsW.Body.String())
	}
	eventsResp := decodeSuccess[eventsResponse](t, eventsW)
	if len(eventsResp.Events) != 1 || eventsResp.Events[0].ID != "e-1" || eventsResp.Events[0].Title != "Kickoff" {
		t.Fatalf("expected one sanitized, correctly-mapped event, got %+v", eventsResp.Events)
	}
	if eventsResp.Events[0].StartTime != "2026-07-21T09:00:00Z" {
		t.Fatalf("expected the nested when.begins pointer to resolve, got start_time=%q", eventsResp.Events[0].StartTime)
	}

	// --- 5. Mutation preview + confirm through this shape's create_event.
	req := mutationRequest{
		WorkspaceID: ws.ID, Operation: calendar.OpCreateEvent, CalendarID: "primary",
		Title: "Planning Sync", StartTime: "2026-07-21T13:00:00Z", EndTime: "2026-07-21T13:30:00Z",
	}
	preCallCount := rec.callCount()
	previewW := doJSONRequest(t, h.Preview, http.MethodPost, "/api/calendar-ops/mutations/preview", req)
	if previewW.Code != http.StatusOK {
		t.Fatalf("Preview status = %d, body=%s", previewW.Code, previewW.Body.String())
	}
	if rec.callCount() != preCallCount {
		t.Fatal("Preview must perform zero connector calls")
	}
	preview := decodeSuccess[mutationPreviewResponse](t, previewW)
	if preview.ConfirmationID == "" {
		t.Fatal("expected a non-empty confirmation_id")
	}

	req.ConfirmationID = preview.ConfirmationID
	confirmW := doJSONRequest(t, h.Confirm, http.MethodPost, "/api/calendar-ops/mutations/confirm", req)
	if confirmW.Code != http.StatusOK {
		t.Fatalf("Confirm status = %d, body=%s", confirmW.Code, confirmW.Body.String())
	}
	if got := rec.callCount(); got != preCallCount+1 {
		t.Fatalf("expected exactly one connector call for Confirm, connector call count = %d", got)
	}
}

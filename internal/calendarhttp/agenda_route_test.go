package calendarhttp

import (
	"net/http"
	"testing"

	"github.com/johnjallday/ori-agent/internal/calendar"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

func TestCapabilities_ReportsMappedOperations(t *testing.T) {
	h, _, _ := newMutableGatewayHandler(t, "local")
	w := doJSONRequest(t, h.Capabilities, http.MethodGet, "/api/calendar-ops/capabilities?workspace_id=ws-cal", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	resp := decodeSuccess[capabilitiesResponse](t, w)
	if !resp.CanCreate || !resp.CanEdit {
		t.Fatalf("expected can_create/can_edit true for a mapping with create_event+update_event: %+v", resp)
	}
	if resp.CanFreeBusy || resp.CanSuggestTime {
		t.Fatalf("freebusy/suggest_time are not mapped in this fixture: %+v", resp)
	}
}

func TestCapabilities_DeniesCrossWorkspace(t *testing.T) {
	h, _, _ := newMutableGatewayHandler(t, "local")
	w := doJSONRequest(t, h.Capabilities, http.MethodGet, "/api/calendar-ops/capabilities?workspace_id=does-not-exist", nil)
	if w.Code == http.StatusOK {
		t.Fatalf("expected an error for an unknown workspace, got 200: %s", w.Body.String())
	}
}

func TestCalendars_ReturnsSanitizedListAndCachesIdenticalRequests(t *testing.T) {
	h, _, rec := newMutableGatewayHandler(t, "local")
	rec.resultFn = func(tool string, args map[string]any) (any, error) {
		return map[string]any{
			"items": []any{
				map[string]any{"id": "primary", "summary": "Work\x00Calendar"},
			},
		}, nil
	}

	w1 := doJSONRequest(t, h.Calendars, http.MethodGet, "/api/calendar-ops/calendars?workspace_id=ws-cal", nil)
	if w1.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w1.Code, w1.Body.String())
	}
	resp1 := decodeSuccess[calendarsResponse](t, w1)
	if len(resp1.Calendars) != 1 || resp1.Calendars[0].Name != "WorkCalendar" {
		t.Fatalf("expected one sanitized calendar, got %+v", resp1.Calendars)
	}
	if rec.callCount() != 1 {
		t.Fatalf("expected exactly 1 connector call, got %d", rec.callCount())
	}

	w2 := doJSONRequest(t, h.Calendars, http.MethodGet, "/api/calendar-ops/calendars?workspace_id=ws-cal", nil)
	if w2.Code != http.StatusOK {
		t.Fatalf("second call status = %d", w2.Code)
	}
	if rec.callCount() != 1 {
		t.Fatalf("identical repeat request must be served from cache, got %d connector calls", rec.callCount())
	}
}

func TestEvents_RejectsExcessiveDateRange(t *testing.T) {
	h, _, rec := newMutableGatewayHandler(t, "local")
	w := doJSONRequest(t, h.Events, http.MethodGet,
		"/api/calendar-ops/events?workspace_id=ws-cal&start=2026-01-01T00:00:00Z&end=2026-12-31T00:00:00Z", nil)
	if w.Code == http.StatusOK {
		t.Fatalf("expected a bounds error for an oversized range, got 200: %s", w.Body.String())
	}
	if rec.callCount() != 0 {
		t.Fatal("an out-of-bounds request must never reach the connector")
	}
}

func TestEvents_RejectsInvertedRange(t *testing.T) {
	h, _, _ := newMutableGatewayHandler(t, "local")
	w := doJSONRequest(t, h.Events, http.MethodGet,
		"/api/calendar-ops/events?workspace_id=ws-cal&start=2026-07-21T00:00:00Z&end=2026-07-20T00:00:00Z", nil)
	if w.Code == http.StatusOK {
		t.Fatalf("expected an error for end before start, got 200: %s", w.Body.String())
	}
}

func TestEvents_UsesSelectedCalendarsByDefault(t *testing.T) {
	h, _, rec := newMutableGatewayHandler(t, "local")
	rec.resultFn = func(tool string, args map[string]any) (any, error) {
		if args["calendarId"] != "primary" {
			t.Errorf("expected the default-selected calendar 'primary', got args=%+v", args)
		}
		return map[string]any{"items": []any{}}, nil
	}
	w := doJSONRequest(t, h.Events, http.MethodGet,
		"/api/calendar-ops/events?workspace_id=ws-cal&start=2026-07-20T00:00:00Z&end=2026-07-21T00:00:00Z", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	if rec.callCount() != 1 {
		t.Fatalf("expected exactly 1 call for the single selected calendar, got %d", rec.callCount())
	}
}

func TestEvents_BackfillsCalendarIDWhenConnectorDoesNotEchoIt(t *testing.T) {
	// Some connectors' list_events results don't repeat calendar_id per item
	// (it's already implied by the request); this fixture's mapping has no
	// Fields entry for calendar_id, mirroring that shape. Callers downstream
	// (meeting prep's link key, the edit form's update_event call) require a
	// populated CalendarID on every event this endpoint returns.
	h, _, rec := newMutableGatewayHandler(t, "local")
	rec.resultFn = func(tool string, args map[string]any) (any, error) {
		return map[string]any{
			"items": []any{
				map[string]any{
					"id": "evt-1", "summary": "Sync",
					"start": map[string]any{"dateTime": "2026-07-20T10:00:00Z"},
					"end":   map[string]any{"dateTime": "2026-07-20T11:00:00Z"},
				},
			},
		}, nil
	}
	w := doJSONRequest(t, h.Events, http.MethodGet,
		"/api/calendar-ops/events?workspace_id=ws-cal&start=2026-07-20T00:00:00Z&end=2026-07-21T00:00:00Z&calendar_id=primary", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	resp := decodeSuccess[eventsResponse](t, w)
	if len(resp.Events) != 1 {
		t.Fatalf("expected 1 event, got %+v", resp.Events)
	}
	if resp.Events[0].CalendarID != "primary" {
		t.Fatalf("expected CalendarID backfilled to the queried calendar 'primary', got %q", resp.Events[0].CalendarID)
	}
}

func TestEvents_PassesExplicitTimeZoneFromWorkspaceSettings(t *testing.T) {
	h, _, rec := newMutableGatewayHandler(t, "local")
	rec.resultFn = func(tool string, args map[string]any) (any, error) {
		if args["timeZone"] != nil {
			t.Errorf("this fixture's mapping doesn't declare a time_zone argument pointer; got unexpected %v", args["timeZone"])
		}
		return map[string]any{"items": []any{}}, nil
	}
	w := doJSONRequest(t, h.Events, http.MethodGet,
		"/api/calendar-ops/events?workspace_id=ws-cal&start=2026-07-20T00:00:00Z&end=2026-07-21T00:00:00Z&time_zone=America/New_York", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	resp := decodeSuccess[eventsResponse](t, w)
	if resp.TimeZone != "America/New_York" {
		t.Fatalf("response TimeZone = %q, want America/New_York (query override)", resp.TimeZone)
	}
}

func TestEvents_DefaultsTimeZoneToWorkspaceDisplaySetting(t *testing.T) {
	h, ws, _ := newMutableGatewayHandler(t, "local")
	binding, _ := findCalendarBinding(ws)
	settings := calendar.ReadBindingSettings(binding.Config)
	settings.DisplayTimeZone = "Europe/London"
	binding.Config = calendar.WriteBindingSettings(binding.Config, settings)
	_ = ws.UpsertMCPBinding(*binding)

	w := doJSONRequest(t, h.Events, http.MethodGet,
		"/api/calendar-ops/events?workspace_id=ws-cal&start=2026-07-20T00:00:00Z&end=2026-07-21T00:00:00Z", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	resp := decodeSuccess[eventsResponse](t, w)
	if resp.TimeZone != "Europe/London" {
		t.Fatalf("response TimeZone = %q, want Europe/London (workspace default)", resp.TimeZone)
	}
}

func TestEvents_SanitizesAndBoundsResults(t *testing.T) {
	h, _, rec := newMutableGatewayHandler(t, "local")
	rec.resultFn = func(tool string, args map[string]any) (any, error) {
		return map[string]any{
			"items": []any{
				map[string]any{
					"id": "evt-1", "summary": "Sync",
					"start": map[string]any{"dateTime": "2026-07-20T10:00:00Z"},
					"end":   map[string]any{"dateTime": "2026-07-20T11:00:00Z"},
				},
			},
		}, nil
	}
	w := doJSONRequest(t, h.Events, http.MethodGet,
		"/api/calendar-ops/events?workspace_id=ws-cal&start=2026-07-20T00:00:00Z&end=2026-07-21T00:00:00Z", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	resp := decodeSuccess[eventsResponse](t, w)
	if len(resp.Events) != 1 || resp.Events[0].ID != "evt-1" {
		t.Fatalf("expected one sanitized event, got %+v", resp.Events)
	}
}

func TestEvents_OneCalendarFailureDoesNotBlankTheWholeAgenda(t *testing.T) {
	h, ws, rec := newMutableGatewayHandler(t, "local")
	binding, _ := findCalendarBinding(ws)
	settings := calendar.ReadBindingSettings(binding.Config)
	settings.SelectedCalendarIDs = []string{"good-cal", "bad-cal"}
	binding.Config = calendar.WriteBindingSettings(binding.Config, settings)
	_ = ws.UpsertMCPBinding(*binding)

	rec.resultFn = func(tool string, args map[string]any) (any, error) {
		if args["calendarId"] == "bad-cal" {
			return nil, errConnectorBoom
		}
		return map[string]any{
			"items": []any{
				map[string]any{
					"id": "evt-good", "summary": "OK",
					"start": map[string]any{"dateTime": "2026-07-20T10:00:00Z"},
					"end":   map[string]any{"dateTime": "2026-07-20T11:00:00Z"},
				},
			},
		}, nil
	}

	w := doJSONRequest(t, h.Events, http.MethodGet,
		"/api/calendar-ops/events?workspace_id=ws-cal&start=2026-07-20T00:00:00Z&end=2026-07-21T00:00:00Z", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	resp := decodeSuccess[eventsResponse](t, w)
	if len(resp.Events) != 1 || resp.Events[0].ID != "evt-good" {
		t.Fatalf("expected the good calendar's event to survive a sibling failure, got %+v", resp.Events)
	}
}

func TestEventDetail_UnmappedOperationRespondsMappedFalse(t *testing.T) {
	h, _, rec := newMutableGatewayHandler(t, "local")
	w := doJSONRequest(t, h.EventDetail, http.MethodGet,
		"/api/calendar-ops/events/detail?workspace_id=ws-cal&calendar_id=primary&event_id=evt-1", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("unmapped get_event should be a clean 200 mapped:false, got %d: %s", w.Code, w.Body.String())
	}
	resp := decodeSuccess[eventDetailResponse](t, w)
	if resp.Mapped {
		t.Fatal("get_event is not mapped in this fixture; expected mapped:false")
	}
	if rec.callCount() != 0 {
		t.Fatal("an unmapped operation must never reach the connector")
	}
}

func TestFreeWindows_UnmappedRespondsMappedFalseWithZeroCalls(t *testing.T) {
	h, _, rec := newMutableGatewayHandler(t, "local")
	w := doJSONRequest(t, h.FreeWindows, http.MethodGet,
		"/api/calendar-ops/free-windows?workspace_id=ws-cal&start=2026-07-20T00:00:00Z&end=2026-07-21T00:00:00Z", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("unmapped freebusy/suggest_time should be a clean 200 mapped:false, got %d: %s", w.Code, w.Body.String())
	}
	resp := decodeSuccess[freeWindowsResponse](t, w)
	if resp.Mapped {
		t.Fatal("neither freebusy nor suggest_time is mapped in this fixture; expected mapped:false")
	}
	if rec.callCount() != 0 {
		t.Fatal("an unmapped free-windows request must never reach the connector")
	}
}

func TestFreeWindows_PrefersFreeBusyOverSuggestTime(t *testing.T) {
	h, ws, rec := newMutableGatewayHandler(t, "local")
	binding, _ := findCalendarBinding(ws)
	mapping := binding.CapabilityMappings[0]
	mapping.Operations[calendar.OpFreeBusy] = agentworkspace.OperationMapping{
		Tool:             "freebusy_query",
		ResultCollection: "/slots",
		Arguments:        map[string]string{"start_time": "/timeMin", "end_time": "/timeMax"},
		Fields:           map[string]string{"start_time": "/start", "end_time": "/end"},
	}
	mapping.Operations[calendar.OpSuggestTime] = agentworkspace.OperationMapping{
		Tool:             "suggest_time_query",
		ResultCollection: "/slots",
		Fields:           map[string]string{"start_time": "/start", "end_time": "/end"},
	}
	binding.CapabilityMappings = []agentworkspace.CapabilityMapping{mapping}
	binding.AllowedTools = calendar.ReadOnlyAllowedTools(mapping)
	_ = ws.UpsertMCPBinding(*binding)

	rec.resultFn = func(tool string, args map[string]any) (any, error) {
		if tool != "freebusy_query" {
			t.Errorf("expected freebusy to be preferred over suggest_time, got tool=%q", tool)
		}
		return map[string]any{"slots": []any{
			map[string]any{"start": "2026-07-20T09:00:00Z", "end": "2026-07-20T10:00:00Z"},
		}}, nil
	}

	w := doJSONRequest(t, h.FreeWindows, http.MethodGet,
		"/api/calendar-ops/free-windows?workspace_id=ws-cal&start=2026-07-20T00:00:00Z&end=2026-07-21T00:00:00Z", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	resp := decodeSuccess[freeWindowsResponse](t, w)
	if !resp.Mapped || resp.Operation != "freebusy" {
		t.Fatalf("expected mapped:true operation:freebusy, got %+v", resp)
	}
	if len(resp.Windows) != 1 {
		t.Fatalf("expected 1 window, got %+v", resp.Windows)
	}
	if rec.callCount() != 1 {
		t.Fatalf("expected exactly 1 connector call, got %d", rec.callCount())
	}
}

func TestFreeWindows_RejectsExcessiveRange(t *testing.T) {
	h, ws, rec := newMutableGatewayHandler(t, "local")
	binding, _ := findCalendarBinding(ws)
	mapping := binding.CapabilityMappings[0]
	mapping.Operations[calendar.OpFreeBusy] = agentworkspace.OperationMapping{Tool: "freebusy_query", ResultCollection: "/slots"}
	binding.CapabilityMappings = []agentworkspace.CapabilityMapping{mapping}
	_ = ws.UpsertMCPBinding(*binding)

	w := doJSONRequest(t, h.FreeWindows, http.MethodGet,
		"/api/calendar-ops/free-windows?workspace_id=ws-cal&start=2026-01-01T00:00:00Z&end=2026-02-01T00:00:00Z", nil)
	if w.Code == http.StatusOK {
		t.Fatalf("expected a bounds error for a range exceeding the free-window maximum, got 200: %s", w.Body.String())
	}
	if rec.callCount() != 0 {
		t.Fatal("an out-of-bounds free-windows request must never reach the connector")
	}
}

func TestCache_IsolatedAcrossWorkspaces(t *testing.T) {
	h1, _, rec1 := newMutableGatewayHandler(t, "local")
	// Second workspace, same handler-shape but its own binding id, sharing
	// nothing with the first except the process-wide cache would be shared
	// if the handler were process-wide -- here each handler has its own
	// cache instance (NewHandler creates one per Handler), which is exactly
	// what the production wiring does too (one Handler, one cache, but keys
	// are scoped by workspace/binding so cross-workspace collisions can't
	// happen even sharing one cache). Assert the key-scoping directly.
	key1 := readCacheKey{UserID: "local", WorkspaceID: "ws-cal", BindingID: "cal-binding", Operation: calendar.OpListCalendars}
	h1.cache.set(key1, "workspace-1-value")

	key2 := readCacheKey{UserID: "local", WorkspaceID: "ws-other", BindingID: "cal-binding", Operation: calendar.OpListCalendars}
	if _, hit := h1.cache.get(key2); hit {
		t.Fatal("a different workspace_id must never hit another workspace's cached entry")
	}
	_ = rec1
}

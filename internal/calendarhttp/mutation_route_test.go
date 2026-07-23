package calendarhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/johnjallday/ori-agent/internal/calendar"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// mutableMappingForTest returns a Google-shaped mapping with list_calendars,
// list_events, and create_event all mapped (create_event -> events_insert),
// so Preview/Confirm tests can exercise the write path.
func mutableMappingForTest() agentworkspace.CapabilityMapping {
	m := googleShapedMappingForTest()
	// googleShapedMappingForTest's list_events has no Arguments (it's built
	// for the setup/validate flow, which never calls BuildArguments); the
	// gateway's Events route always resolves calendar_id/start_time/end_time
	// through BuildArguments, so route-level tests need a mapping that
	// actually declares where those go.
	listEvents := m.Operations[calendar.OpListEvents]
	listEvents.Arguments = map[string]string{
		"calendar_id": "/calendarId",
		"start_time":  "/timeMin",
		"end_time":    "/timeMax",
	}
	m.Operations[calendar.OpListEvents] = listEvents

	m.Operations[calendar.OpCreateEvent] = agentworkspace.OperationMapping{
		Tool: "events_insert",
		Arguments: map[string]string{
			"calendar_id": "/calendarId",
			"title":       "/summary",
			"start_time":  "/start/dateTime",
			"end_time":    "/end/dateTime",
			"time_zone":   "/start/timeZone",
			"location":    "/location",
			"description": "/description",
			"attendees":   "/attendees",
		},
	}
	m.Operations[calendar.OpUpdateEvent] = agentworkspace.OperationMapping{
		Tool: "events_patch",
		Arguments: map[string]string{
			"id":          "/eventId",
			"calendar_id": "/calendarId",
			"title":       "/summary",
		},
	}
	return m
}

// recordingToolCaller counts invocations and records the tool/args of each
// call, so tests can assert exactly how many (and which) external tool calls
// a route made -- the core of "preview performs zero writes" / "confirm
// exactly once".
type recordingToolCaller struct {
	mu    sync.Mutex
	calls []recordedCall
	// resultFn lets a test control what a call returns; nil means succeed
	// with an empty object.
	resultFn func(tool string, args map[string]any) (any, error)
}

type recordedCall struct {
	Tool string
	Args map[string]any
}

func (r *recordingToolCaller) asToolCaller() calendar.ToolCaller {
	return func(_ context.Context, tool string, args map[string]any) (any, error) {
		r.mu.Lock()
		r.calls = append(r.calls, recordedCall{Tool: tool, Args: args})
		r.mu.Unlock()
		if r.resultFn != nil {
			return r.resultFn(tool, args)
		}
		return map[string]any{"id": "connector-event-id"}, nil
	}
}

func (r *recordingToolCaller) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func newMutableGatewayHandler(t *testing.T, userID string) (*Handler, *agentworkspace.Workspace, *recordingToolCaller) {
	t.Helper()
	ws := newCalendarOpsWorkspace("ws-cal")
	ws.OwnerUserID = userID
	mapping := mutableMappingForTest()
	binding := agentworkspace.MCPBinding{
		ID:                 "cal-binding",
		ServerName:         "google-calendar",
		Enabled:            true,
		CapabilityMappings: []agentworkspace.CapabilityMapping{mapping},
		AllowedTools:       calendar.ReadOnlyAllowedTools(mapping),
		Config:             calendar.WriteBindingSettings(nil, calendar.BindingSettings{SelectedCalendarIDs: []string{"primary"}, Validated: true}),
	}
	if err := ws.UpsertMCPBinding(binding); err != nil {
		t.Fatalf("UpsertMCPBinding: %v", err)
	}

	store := newFakeFolderStore()
	store.workspaces[ws.ID] = ws
	h := NewHandler(store, &fakeWorkspaceLister{}, nil, nil, fakeUserProvider{id: userID})
	h.WithConnectorStatusFn(func(string) connectorStatus { return readyStatus })

	rec := &recordingToolCaller{}
	h.WithToolCallerFactory(func(string) calendar.ToolCaller { return rec.asToolCaller() })

	return h, ws, rec
}

func doJSONRequest(t *testing.T, handler http.HandlerFunc, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reqBody *bytes.Buffer
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request body: %v", err)
		}
		reqBody = bytes.NewBuffer(b)
	} else {
		reqBody = bytes.NewBuffer(nil)
	}
	req := httptest.NewRequest(method, path, reqBody)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler(w, req)
	return w
}

func decodeSuccess[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var data T
	if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
		t.Fatalf("decode response %s: %v", w.Body.String(), err)
	}
	return data
}

func TestPreview_PerformsZeroToolCalls(t *testing.T) {
	h, _, rec := newMutableGatewayHandler(t, "local")

	w := doJSONRequest(t, h.Preview, http.MethodPost, "/api/calendar-ops/mutations/preview", mutationRequest{
		WorkspaceID: "ws-cal", Operation: "create_event", CalendarID: "primary",
		Title: "Team Sync", StartTime: "2026-07-20T10:00:00Z", EndTime: "2026-07-20T11:00:00Z",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("Preview status = %d, body=%s", w.Code, w.Body.String())
	}
	if got := rec.callCount(); got != 0 {
		t.Fatalf("Preview must perform zero MCP calls, got %d: %+v", got, rec.calls)
	}
	resp := decodeSuccess[mutationPreviewResponse](t, w)
	if resp.ConfirmationID == "" {
		t.Fatal("expected a non-empty confirmation_id")
	}
}

func TestPreview_RejectsInvalidPayloadWithZeroCalls(t *testing.T) {
	h, _, rec := newMutableGatewayHandler(t, "local")
	w := doJSONRequest(t, h.Preview, http.MethodPost, "/api/calendar-ops/mutations/preview", mutationRequest{
		WorkspaceID: "ws-cal", Operation: "delete_event", CalendarID: "primary", Title: "x",
		StartTime: "2026-07-20T10:00:00Z", EndTime: "2026-07-20T11:00:00Z",
	})
	if w.Code == http.StatusOK {
		t.Fatalf("expected a non-200 for an unsupported operation, got 200: %s", w.Body.String())
	}
	if rec.callCount() != 0 {
		t.Fatal("an invalid preview must never reach the connector")
	}
}

func TestPreview_UnmappedOperationRejected(t *testing.T) {
	h, ws, rec := newMutableGatewayHandler(t, "local")
	// Repoint at a mapping with no create_event.
	binding, _ := findCalendarBinding(ws)
	binding.CapabilityMappings = []agentworkspace.CapabilityMapping{googleShapedMappingForTest()}
	binding.AllowedTools = calendar.ReadOnlyAllowedTools(binding.CapabilityMappings[0])
	_ = ws.UpsertMCPBinding(*binding)

	w := doJSONRequest(t, h.Preview, http.MethodPost, "/api/calendar-ops/mutations/preview", mutationRequest{
		WorkspaceID: "ws-cal", Operation: "create_event", CalendarID: "primary", Title: "x",
		StartTime: "2026-07-20T10:00:00Z", EndTime: "2026-07-20T11:00:00Z",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for an unmapped operation, got %d: %s", w.Code, w.Body.String())
	}
	if rec.callCount() != 0 {
		t.Fatal("must not call the connector for an unmapped operation")
	}
}

func TestConfirm_InvokesExactlyOneExternalCall(t *testing.T) {
	h, _, rec := newMutableGatewayHandler(t, "local")

	req := mutationRequest{
		WorkspaceID: "ws-cal", Operation: "create_event", CalendarID: "primary",
		Title: "Team Sync", StartTime: "2026-07-20T10:00:00Z", EndTime: "2026-07-20T11:00:00Z",
	}
	previewW := doJSONRequest(t, h.Preview, http.MethodPost, "/api/calendar-ops/mutations/preview", req)
	preview := decodeSuccess[mutationPreviewResponse](t, previewW)

	req.ConfirmationID = preview.ConfirmationID
	confirmW := doJSONRequest(t, h.Confirm, http.MethodPost, "/api/calendar-ops/mutations/confirm", req)
	if confirmW.Code != http.StatusOK {
		t.Fatalf("Confirm status = %d, body=%s", confirmW.Code, confirmW.Body.String())
	}
	result := decodeSuccess[mutationConfirmResponse](t, confirmW)
	if !result.Success {
		t.Fatalf("expected success, got %+v", result)
	}
	if got := rec.callCount(); got != 1 {
		t.Fatalf("Confirm must invoke exactly one external tool call, got %d: %+v", got, rec.calls)
	}
	if rec.calls[0].Tool != "events_insert" {
		t.Errorf("expected the mapped create_event tool events_insert, got %q", rec.calls[0].Tool)
	}
}

func TestConfirm_ReplayIsRejectedAndDoesNotCallAgain(t *testing.T) {
	h, _, rec := newMutableGatewayHandler(t, "local")
	req := mutationRequest{
		WorkspaceID: "ws-cal", Operation: "create_event", CalendarID: "primary",
		Title: "Team Sync", StartTime: "2026-07-20T10:00:00Z", EndTime: "2026-07-20T11:00:00Z",
	}
	previewW := doJSONRequest(t, h.Preview, http.MethodPost, "/api/calendar-ops/mutations/preview", req)
	preview := decodeSuccess[mutationPreviewResponse](t, previewW)
	req.ConfirmationID = preview.ConfirmationID

	first := doJSONRequest(t, h.Confirm, http.MethodPost, "/api/calendar-ops/mutations/confirm", req)
	if first.Code != http.StatusOK {
		t.Fatalf("first confirm should succeed: %s", first.Body.String())
	}
	second := doJSONRequest(t, h.Confirm, http.MethodPost, "/api/calendar-ops/mutations/confirm", req)
	if second.Code == http.StatusOK {
		t.Fatalf("a replayed confirm must be rejected, got 200: %s", second.Body.String())
	}
	if got := rec.callCount(); got != 1 {
		t.Fatalf("replay must not invoke a second external call, got %d calls", got)
	}
}

func TestConfirm_PayloadTamperRejectedWithNoCall(t *testing.T) {
	h, _, rec := newMutableGatewayHandler(t, "local")
	req := mutationRequest{
		WorkspaceID: "ws-cal", Operation: "create_event", CalendarID: "primary",
		Title: "Team Sync", StartTime: "2026-07-20T10:00:00Z", EndTime: "2026-07-20T11:00:00Z",
	}
	previewW := doJSONRequest(t, h.Preview, http.MethodPost, "/api/calendar-ops/mutations/preview", req)
	preview := decodeSuccess[mutationPreviewResponse](t, previewW)

	req.ConfirmationID = preview.ConfirmationID
	req.Title = "A Different Title" // tamper after preview

	w := doJSONRequest(t, h.Confirm, http.MethodPost, "/api/calendar-ops/mutations/confirm", req)
	if w.Code == http.StatusOK {
		t.Fatalf("a tampered payload must be rejected, got 200: %s", w.Body.String())
	}
	if rec.callCount() != 0 {
		t.Fatal("a rejected tamper attempt must never reach the connector")
	}
}

func TestConfirm_WrongUserRejected(t *testing.T) {
	h, _, rec := newMutableGatewayHandler(t, "local")
	req := mutationRequest{
		WorkspaceID: "ws-cal", Operation: "create_event", CalendarID: "primary",
		Title: "Team Sync", StartTime: "2026-07-20T10:00:00Z", EndTime: "2026-07-20T11:00:00Z",
	}
	previewW := doJSONRequest(t, h.Preview, http.MethodPost, "/api/calendar-ops/mutations/preview", req)
	preview := decodeSuccess[mutationPreviewResponse](t, previewW)
	req.ConfirmationID = preview.ConfirmationID

	// A second handler instance sharing the same in-memory store but acting
	// as a different current user must not be able to consume the first
	// user's confirmation.
	h.provider = fakeUserProvider{id: "mallory"}
	w := doJSONRequest(t, h.Confirm, http.MethodPost, "/api/calendar-ops/mutations/confirm", req)
	if w.Code == http.StatusOK {
		t.Fatalf("a different user must not be able to confirm, got 200: %s", w.Body.String())
	}
	if rec.callCount() != 0 {
		t.Fatal("a wrong-user confirm must never reach the connector")
	}
}

func TestConfirm_WrongWorkspaceRejected(t *testing.T) {
	h, ws, rec := newMutableGatewayHandler(t, "local")
	other := newCalendarOpsWorkspace("ws-other")
	other.OwnerUserID = "local"
	storeWithBoth := h.folders.(*fakeFolderStore)
	storeWithBoth.workspaces["ws-other"] = other
	_ = ws

	req := mutationRequest{
		WorkspaceID: "ws-cal", Operation: "create_event", CalendarID: "primary",
		Title: "Team Sync", StartTime: "2026-07-20T10:00:00Z", EndTime: "2026-07-20T11:00:00Z",
	}
	previewW := doJSONRequest(t, h.Preview, http.MethodPost, "/api/calendar-ops/mutations/preview", req)
	preview := decodeSuccess[mutationPreviewResponse](t, previewW)

	req.WorkspaceID = "ws-other" // switch workspace at confirm time
	req.ConfirmationID = preview.ConfirmationID
	w := doJSONRequest(t, h.Confirm, http.MethodPost, "/api/calendar-ops/mutations/confirm", req)
	if w.Code == http.StatusOK {
		t.Fatalf("confirming against a different workspace must be rejected, got 200: %s", w.Body.String())
	}
	if rec.callCount() != 0 {
		t.Fatal("a wrong-workspace confirm must never reach the connector")
	}
}

func TestConfirm_ReturnsCreatedEventWhenMappingDeclaresFields(t *testing.T) {
	h, ws, rec := newMutableGatewayHandler(t, "local")
	binding, _ := findCalendarBinding(ws)
	mapping := binding.CapabilityMappings[0]
	createOp := mapping.Operations[calendar.OpCreateEvent]
	createOp.Fields = map[string]string{
		"id": "/id", "title": "/summary",
		"start_time": "/start/dateTime", "end_time": "/end/dateTime",
	}
	mapping.Operations[calendar.OpCreateEvent] = createOp
	binding.CapabilityMappings = []agentworkspace.CapabilityMapping{mapping}
	_ = ws.UpsertMCPBinding(*binding)

	rec.resultFn = func(tool string, args map[string]any) (any, error) {
		return map[string]any{
			"id": "connector-evt-42", "summary": "Team Sync",
			"start": map[string]any{"dateTime": "2026-07-20T10:00:00Z"},
			"end":   map[string]any{"dateTime": "2026-07-20T11:00:00Z"},
		}, nil
	}

	req := mutationRequest{
		WorkspaceID: "ws-cal", Operation: "create_event", CalendarID: "primary",
		Title: "Team Sync", StartTime: "2026-07-20T10:00:00Z", EndTime: "2026-07-20T11:00:00Z",
	}
	previewW := doJSONRequest(t, h.Preview, http.MethodPost, "/api/calendar-ops/mutations/preview", req)
	preview := decodeSuccess[mutationPreviewResponse](t, previewW)
	req.ConfirmationID = preview.ConfirmationID

	w := doJSONRequest(t, h.Confirm, http.MethodPost, "/api/calendar-ops/mutations/confirm", req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	result := decodeSuccess[mutationConfirmResponse](t, w)
	if !result.Success {
		t.Fatalf("expected success, got %+v", result)
	}
	if result.EventID != "connector-evt-42" {
		t.Fatalf("EventID = %q, want connector-evt-42", result.EventID)
	}
	if result.Event == nil || result.Event.Title != "Team Sync" {
		t.Fatalf("expected the returned event to be populated, got %+v", result.Event)
	}
	_ = rec
}

func TestConfirm_ConnectorFailureReportsFailureNotSuccess(t *testing.T) {
	h, _, rec := newMutableGatewayHandler(t, "local")
	rec.resultFn = func(tool string, args map[string]any) (any, error) {
		return nil, errConnectorBoom
	}

	req := mutationRequest{
		WorkspaceID: "ws-cal", Operation: "create_event", CalendarID: "primary",
		Title: "Team Sync", StartTime: "2026-07-20T10:00:00Z", EndTime: "2026-07-20T11:00:00Z",
	}
	previewW := doJSONRequest(t, h.Preview, http.MethodPost, "/api/calendar-ops/mutations/preview", req)
	preview := decodeSuccess[mutationPreviewResponse](t, previewW)
	req.ConfirmationID = preview.ConfirmationID

	w := doJSONRequest(t, h.Confirm, http.MethodPost, "/api/calendar-ops/mutations/confirm", req)
	if w.Code != http.StatusOK {
		t.Fatalf("a connector failure is still a 200 envelope with success:false, got %d: %s", w.Code, w.Body.String())
	}
	result := decodeSuccess[mutationConfirmResponse](t, w)
	if result.Success {
		t.Fatal("a connector failure must never be reported as success:true")
	}
	if result.Error == "" {
		t.Fatal("expected an error message describing the connector failure")
	}
}

func TestConfirm_InvalidatesReadCacheForTheBinding(t *testing.T) {
	h, _, rec := newMutableGatewayHandler(t, "local")
	key := readCacheKey{UserID: "local", WorkspaceID: "ws-cal", BindingID: "cal-binding", Operation: calendar.OpListCalendars}
	h.cache.set(key, "stale-cached-value")

	req := mutationRequest{
		WorkspaceID: "ws-cal", Operation: "create_event", CalendarID: "primary",
		Title: "Team Sync", StartTime: "2026-07-20T10:00:00Z", EndTime: "2026-07-20T11:00:00Z",
	}
	previewW := doJSONRequest(t, h.Preview, http.MethodPost, "/api/calendar-ops/mutations/preview", req)
	preview := decodeSuccess[mutationPreviewResponse](t, previewW)
	req.ConfirmationID = preview.ConfirmationID

	if w := doJSONRequest(t, h.Confirm, http.MethodPost, "/api/calendar-ops/mutations/confirm", req); w.Code != http.StatusOK {
		t.Fatalf("confirm should succeed: %s", w.Body.String())
	}
	if _, hit := h.cache.get(key); hit {
		t.Fatal("a successful confirm must invalidate the binding's cached reads")
	}
	_ = rec
}

var errConnectorBoom = errors.New("connector exploded")

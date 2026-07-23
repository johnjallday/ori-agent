package calendarhttp

import (
	"net/http"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/calendar"
	"github.com/johnjallday/ori-agent/internal/session"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// newPortalTestHandler mirrors newMutableGatewayHandler but also populates
// the lister, since PortalSummary resolves its workspace via ActiveWorkspace
// (FR49) rather than a workspace_id query param.
func newPortalTestHandler(t *testing.T, userID string) (*Handler, *agentworkspace.Workspace, *recordingToolCaller) {
	t.Helper()
	h, ws, rec := newMutableGatewayHandler(t, userID)
	h.lister = &fakeWorkspaceLister{workspaces: []session.Workspace{
		{ID: ws.ID, Name: ws.Name, OwnerUserID: userID, Status: session.WorkspaceStatusActive},
	}}
	return h, ws, rec
}

func TestPortalSummary_NoWorkspaceReportsHasWorkspaceFalse(t *testing.T) {
	h := newActiveWorkspaceTestHandler(nil, nil)
	w := doJSONRequest(t, h.PortalSummary, http.MethodGet, "/api/calendar-ops/home-portal-summary", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	resp := decodeSuccess[portalSummaryResponse](t, w)
	if resp.HasWorkspace {
		t.Fatalf("expected has_workspace=false, got %+v", resp)
	}
}

func TestPortalSummary_UnfinishedSetupReportsSetupState(t *testing.T) {
	ws := newCalendarOpsWorkspace("ws-cal")
	ws.OwnerUserID = "local"
	h := newActiveWorkspaceTestHandler(
		map[string]*agentworkspace.Workspace{"ws-cal": ws},
		[]session.Workspace{{ID: "ws-cal", Name: "Calendar Ops", OwnerUserID: "local", Status: session.WorkspaceStatusActive}},
	)

	w := doJSONRequest(t, h.PortalSummary, http.MethodGet, "/api/calendar-ops/home-portal-summary", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	resp := decodeSuccess[portalSummaryResponse](t, w)
	if !resp.HasWorkspace || resp.WorkspaceID != "ws-cal" {
		t.Fatalf("expected has_workspace=true workspace_id=ws-cal, got %+v", resp)
	}
	if resp.State != calendar.SetupConnectorMissing {
		t.Fatalf("expected state=connector_missing, got %q", resp.State)
	}
}

func TestPortalSummary_ReadyReportsEventsConflictsAndNextMeeting(t *testing.T) {
	h, _, rec := newPortalTestHandler(t, "local")
	now := time.Now().UTC()
	rec.resultFn = func(tool string, args map[string]any) (any, error) {
		return map[string]any{
			"items": []any{
				map[string]any{
					"id": "evt-1", "summary": "A",
					"start": map[string]any{"dateTime": now.Add(1 * time.Hour).Format(time.RFC3339)},
					"end":   map[string]any{"dateTime": now.Add(2 * time.Hour).Format(time.RFC3339)},
				},
				map[string]any{
					"id": "evt-2", "summary": "B",
					"start": map[string]any{"dateTime": now.Add(90 * time.Minute).Format(time.RFC3339)},
					"end":   map[string]any{"dateTime": now.Add(150 * time.Minute).Format(time.RFC3339)},
				},
			},
		}, nil
	}

	w := doJSONRequest(t, h.PortalSummary, http.MethodGet, "/api/calendar-ops/home-portal-summary", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	resp := decodeSuccess[portalSummaryResponse](t, w)
	if resp.State != calendar.SetupReady {
		t.Fatalf("expected state=ready, got %q", resp.State)
	}
	if resp.EventCount != 2 {
		t.Fatalf("expected event_count=2, got %d", resp.EventCount)
	}
	if resp.ConflictCount != 2 {
		t.Fatalf("expected conflict_count=2 (both overlap), got %d", resp.ConflictCount)
	}
	if resp.NextMeeting == nil || resp.NextMeeting.ID != "evt-1" {
		t.Fatalf("expected next_meeting=evt-1 (earliest upcoming), got %+v", resp.NextMeeting)
	}
	if resp.DataGap {
		t.Fatal("expected data_gap=false when every calendar succeeded")
	}
}

func TestPortalSummary_ConnectorFailureIsolatesDataGapWithoutBlankingSuccessfulCalendars(t *testing.T) {
	h, ws, rec := newPortalTestHandler(t, "local")
	binding, _ := findCalendarBinding(ws)
	settings := calendar.ReadBindingSettings(binding.Config)
	settings.SelectedCalendarIDs = []string{"good-cal", "slow-cal"}
	binding.Config = calendar.WriteBindingSettings(binding.Config, settings)
	if err := ws.UpsertMCPBinding(*binding); err != nil {
		t.Fatalf("UpsertMCPBinding: %v", err)
	}

	now := time.Now().UTC()
	rec.resultFn = func(tool string, args map[string]any) (any, error) {
		if args["calendarId"] == "slow-cal" {
			return nil, errConnectorBoom
		}
		return map[string]any{
			"items": []any{
				map[string]any{
					"id": "evt-good", "summary": "OK",
					"start": map[string]any{"dateTime": now.Add(1 * time.Hour).Format(time.RFC3339)},
					"end":   map[string]any{"dateTime": now.Add(2 * time.Hour).Format(time.RFC3339)},
				},
			},
		}, nil
	}

	w := doJSONRequest(t, h.PortalSummary, http.MethodGet, "/api/calendar-ops/home-portal-summary", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("a connector failure must never break the portal response, got %d: %s", w.Code, w.Body.String())
	}
	resp := decodeSuccess[portalSummaryResponse](t, w)
	if !resp.DataGap {
		t.Fatal("expected data_gap=true when one calendar failed")
	}
	if resp.EventCount != 1 || resp.NextMeeting == nil || resp.NextMeeting.ID != "evt-good" {
		t.Fatalf("expected the successful calendar's event to still show, got %+v", resp)
	}
}

func TestPortalSummary_NoCalendarsSelectedIsAnEmptyStateNotAGap(t *testing.T) {
	h, ws, rec := newPortalTestHandler(t, "local")
	binding, _ := findCalendarBinding(ws)
	settings := calendar.ReadBindingSettings(binding.Config)
	settings.SelectedCalendarIDs = nil
	binding.Config = calendar.WriteBindingSettings(binding.Config, settings)
	if err := ws.UpsertMCPBinding(*binding); err != nil {
		t.Fatalf("UpsertMCPBinding: %v", err)
	}

	w := doJSONRequest(t, h.PortalSummary, http.MethodGet, "/api/calendar-ops/home-portal-summary", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	resp := decodeSuccess[portalSummaryResponse](t, w)
	if resp.DataGap {
		t.Fatal("expected data_gap=false when zero calendars are selected (a legitimate empty state)")
	}
	if resp.EventCount != 0 {
		t.Fatalf("expected event_count=0, got %d", resp.EventCount)
	}
	if rec.callCount() != 0 {
		t.Fatalf("expected zero connector calls with no calendars selected, got %d", rec.callCount())
	}
}

func TestTodayWindow_UsesConfiguredTimezoneAndFallsBackToUTC(t *testing.T) {
	now, err := time.Parse(time.RFC3339, "2026-07-20T23:30:00Z")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	start, end := todayWindow("", now)
	if start.UTC().Format("2006-01-02") != "2026-07-20" || end.Sub(start) != 24*time.Hour {
		t.Fatalf("UTC window = [%v,%v), want a 24h window starting 2026-07-20", start, end)
	}

	// 23:30 UTC is already 2026-07-21 in a positive-offset zone -- the window
	// must follow the configured zone's calendar day, not UTC's.
	start2, end2 := todayWindow("Asia/Tokyo", now)
	loc, _ := time.LoadLocation("Asia/Tokyo")
	if start2.In(loc).Format("2006-01-02") != "2026-07-21" || end2.Sub(start2) != 24*time.Hour {
		t.Fatalf("Tokyo window = [%v,%v), want a 24h window starting 2026-07-21 in Asia/Tokyo", start2, end2)
	}

	start3, _ := todayWindow("not-a-real-zone", now)
	if start3.Location() != time.UTC {
		t.Fatalf("expected an invalid timezone to fall back to UTC, got %v", start3.Location())
	}
}

// The Home/Personal HQ Calendar Ops portal (FR49-51, task 7.2/7.3): a single
// bounded read, resolved for the current user via ActiveWorkspace (FR49),
// that never widens into a full agenda scan and never blocks its caller on a
// slow/broken connector -- a per-calendar failure degrades to data_gap
// rather than an error response.
package calendarhttp

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/calendar"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

type portalSummaryResponse struct {
	HasWorkspace  bool                `json:"has_workspace"`
	WorkspaceID   string              `json:"workspace_id,omitempty"`
	WorkspaceSlug string              `json:"workspace_slug,omitempty"`
	State         calendar.SetupState `json:"state,omitempty"`
	NextMeeting   *calendar.Event     `json:"next_meeting,omitempty"`
	EventCount    int                 `json:"event_count"`
	ConflictCount int                 `json:"conflict_count"`
	DataGap       bool                `json:"data_gap,omitempty"`
}

// PortalSummary handles GET /api/calendar-ops/home-portal-summary. Unlike
// every other route in this package, it takes no workspace_id -- FR49's
// shared resolver finds the current user's Calendar Ops workspace, if any.
func (h *Handler) PortalSummary(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodGet) {
		return
	}
	ctx := r.Context()
	userID, err := h.currentUserID(ctx)
	if err != nil {
		orihttp.InternalError(w, "failed to resolve current user: "+err.Error())
		return
	}

	ws, ok := h.ActiveWorkspace(ctx, userID)
	if !ok {
		_ = orihttp.RespondSuccess(w, portalSummaryResponse{HasWorkspace: false})
		return
	}
	resp := portalSummaryResponse{HasWorkspace: true, WorkspaceID: ws.ID, WorkspaceSlug: ws.FolderSlug}

	gw, gerr := h.resolveGateway(ctx, ws.ID)
	if gerr != nil {
		if gerr.status == http.StatusConflict || gerr.status == http.StatusServiceUnavailable {
			resp.State = calendar.SetupState(gerr.code)
		} else {
			// Ownership/not-found only happens on an ActiveWorkspace/
			// resolveGateway race (workspace deleted/reassigned between the
			// two reads); treat it as a transient gap rather than surfacing
			// an internal error code typed as a SetupState.
			resp.DataGap = true
		}
		_ = orihttp.RespondSuccess(w, resp)
		return
	}
	resp.State = calendar.SetupReady

	events, dataGap := h.loadTodayEvents(ctx, gw)
	resp.DataGap = dataGap
	resp.EventCount = len(events)
	resp.ConflictCount = calendar.CountConflicts(events)
	resp.NextMeeting = calendar.NextMeeting(events, nowUTC())
	_ = orihttp.RespondSuccess(w, resp)
}

// loadTodayEvents fetches today's events (in the workspace's display
// timezone, else UTC) across the user's selected calendars, isolating
// per-calendar failures exactly like Events() does: one bad calendar must
// never blank the whole summary. dataGap reports whether any calendar
// failed, so the caller can show partial data with an honest caveat rather
// than either hiding it or presenting it as complete.
func (h *Handler) loadTodayEvents(ctx context.Context, gw *gatewayContext) (events []calendar.Event, dataGap bool) {
	op, mapped := gw.Mapping.Operation(calendar.OpListEvents)
	if !mapped {
		return nil, true
	}
	settings := calendar.ReadBindingSettings(gw.Binding.Config)
	calendarIDs := settings.SelectedCalendarIDs
	if len(calendarIDs) > maxAgendaCalendars {
		calendarIDs = calendarIDs[:maxAgendaCalendars]
	}
	if len(calendarIDs) == 0 {
		// No calendars selected is a legitimate empty state, not a gap.
		return nil, false
	}

	start, end := todayWindow(settings.DisplayTimeZone, nowUTC())
	startStr, endStr := start.Format(time.RFC3339), end.Format(time.RFC3339)

	events = make([]calendar.Event, 0, maxAgendaEvents)
	failed := 0
	for _, calID := range calendarIDs {
		calID = strings.TrimSpace(calID)
		if calID == "" {
			continue
		}
		input := map[string]any{
			"calendar_id": calID,
			"start_time":  startStr,
			"end_time":    endStr,
		}
		if settings.DisplayTimeZone != "" {
			input["time_zone"] = settings.DisplayTimeZone
		}
		args, err := calendar.BuildArguments(input, op)
		if err != nil {
			failed++
			continue
		}
		raw, cached := h.cachedCall(ctx, gw, calendar.OpListEvents, args, func(ctx context.Context, call calendar.ToolCaller, op agentworkspace.OperationMapping) (any, error) {
			return listEventsRaw(ctx, call, op, args, calID)
		})
		if cached.err != nil {
			failed++
			continue
		}
		calEvents, _ := raw.([]calendar.Event)
		events = append(events, calEvents...)
	}
	return events, failed > 0
}

// todayWindow returns [start,end) for "today" in tz (UTC if empty/invalid).
func todayWindow(tz string, now time.Time) (time.Time, time.Time) {
	loc := time.UTC
	if tz != "" {
		if l, err := time.LoadLocation(tz); err == nil {
			loc = l
		}
	}
	local := now.In(loc)
	start := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	return start, start.Add(24 * time.Hour)
}

// Normalized read routes: capabilities, calendars, bounded agenda reads, and
// event detail (task 4.2). Every route resolves through resolveGateway and
// returns only calendar.Sanitize*-passed canonical data -- never a raw
// connector response, and never a connector credential.
package calendarhttp

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/calendar"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// Bounds applied to agenda reads (task 4.2: "clamp date ranges/result
// counts"). maxAgendaRangeDays is generous enough for the day/week navigation
// FR35 specifies while keeping a single request's connector-side cost and
// response size bounded.
const (
	maxAgendaRangeDays = 40
	maxAgendaEvents    = 500
	maxAgendaCalendars = 25
)

// --- capabilities --------------------------------------------------------

type capabilitiesResponse struct {
	WorkspaceID         string   `json:"workspace_id"`
	MappedOperations    []string `json:"mapped_operations"`
	SelectedCalendarIDs []string `json:"selected_calendar_ids,omitempty"`
	DisplayTimeZone     string   `json:"display_time_zone,omitempty"`
	CanCreate           bool     `json:"can_create"`
	CanEdit             bool     `json:"can_edit"`
	CanFreeBusy         bool     `json:"can_freebusy"`
	CanSuggestTime      bool     `json:"can_suggest_time"`
}

// Capabilities handles GET /api/calendar-ops/capabilities?workspace_id=ID. It
// tells the frontend which optional operations are mapped so create/edit/
// freebusy/suggest-time controls only appear when they're actually usable
// (FR40) -- v1 never shows delete/RSVP/recurring-series controls because
// those operations don't exist in the calendar contract at all.
func (h *Handler) Capabilities(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodGet) {
		return
	}
	workspaceID := strings.TrimSpace(r.URL.Query().Get("workspace_id"))
	gw, gerr := h.resolveGateway(r.Context(), workspaceID)
	if gerr != nil {
		writeGatewayError(w, gerr)
		return
	}
	settings := calendar.ReadBindingSettings(gw.Binding.Config)
	_, canCreate := gw.Mapping.Operation(calendar.OpCreateEvent)
	_, canEdit := gw.Mapping.Operation(calendar.OpUpdateEvent)
	_, canFreeBusy := gw.Mapping.Operation(calendar.OpFreeBusy)
	_, canSuggest := gw.Mapping.Operation(calendar.OpSuggestTime)

	names := make([]string, 0, len(gw.Mapping.Operations))
	for name := range gw.Mapping.Operations {
		names = append(names, name)
	}

	_ = orihttp.RespondSuccess(w, capabilitiesResponse{
		WorkspaceID:         gw.Workspace.ID,
		MappedOperations:    names,
		SelectedCalendarIDs: settings.SelectedCalendarIDs,
		DisplayTimeZone:     settings.DisplayTimeZone,
		CanCreate:           canCreate,
		CanEdit:             canEdit,
		CanFreeBusy:         canFreeBusy,
		CanSuggestTime:      canSuggest,
	})
}

// --- calendars -------------------------------------------------------------

type calendarsResponse struct {
	Calendars           []calendar.Calendar `json:"calendars"`
	SelectedCalendarIDs []string            `json:"selected_calendar_ids,omitempty"`
}

// Calendars handles GET /api/calendar-ops/calendars?workspace_id=ID: the
// connector's full calendar list plus which ones the user selected as
// visible during setup.
func (h *Handler) Calendars(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodGet) {
		return
	}
	workspaceID := strings.TrimSpace(r.URL.Query().Get("workspace_id"))
	gw, gerr := h.resolveGateway(r.Context(), workspaceID)
	if gerr != nil {
		writeGatewayError(w, gerr)
		return
	}

	raw, cached := h.cachedCall(r.Context(), gw, calendar.OpListCalendars, nil, func(ctx context.Context, call calendar.ToolCaller, op agentworkspace.OperationMapping) (any, error) {
		return listCalendarsRaw(ctx, call, op)
	})
	if cached.err != nil {
		orihttp.InternalError(w, "failed to list calendars: "+cached.err.Error())
		return
	}
	cals, _ := raw.([]calendar.Calendar)

	settings := calendar.ReadBindingSettings(gw.Binding.Config)
	_ = orihttp.RespondSuccess(w, calendarsResponse{Calendars: cals, SelectedCalendarIDs: settings.SelectedCalendarIDs})
}

func listCalendarsRaw(ctx context.Context, call calendar.ToolCaller, op agentworkspace.OperationMapping) ([]calendar.Calendar, error) {
	raw, err := call(ctx, op.Tool, map[string]any{})
	if err != nil {
		return nil, err
	}
	items, err := calendar.Collection(raw, op)
	if err != nil {
		return nil, err
	}
	out := make([]calendar.Calendar, 0, len(items))
	for _, item := range items {
		cal := calendar.SanitizeCalendar(calendar.ApplyCalendar(item, op))
		if cal.ID == "" {
			continue
		}
		out = append(out, cal)
	}
	return out, nil
}

// --- agenda (bounded event reads) ------------------------------------------

type eventsResponse struct {
	Events    []calendar.Event `json:"events"`
	StartTime string           `json:"start_time"`
	EndTime   string           `json:"end_time"`
	TimeZone  string           `json:"time_zone,omitempty"`
}

// Events handles GET /api/calendar-ops/events, a bounded agenda read across
// one or more calendars (task 4.2). Query params: workspace_id (required),
// start/end (RFC3339, required), calendar_id (repeatable; defaults to the
// user's selected calendars from setup), time_zone (optional; defaults to the
// workspace's configured display timezone and is passed through explicitly
// to any mapping that references it -- start/end remain unambiguous RFC3339
// instants regardless).
func (h *Handler) Events(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodGet) {
		return
	}
	q := r.URL.Query()
	workspaceID := strings.TrimSpace(q.Get("workspace_id"))
	gw, gerr := h.resolveGateway(r.Context(), workspaceID)
	if gerr != nil {
		writeGatewayError(w, gerr)
		return
	}

	op, mapped := gw.Mapping.Operation(calendar.OpListEvents)
	if !mapped {
		orihttp.BadRequest(w, "list_events is not mapped for this connector")
		return
	}

	start, end, boundsErr := parseAgendaRange(q.Get("start"), q.Get("end"))
	if boundsErr != nil {
		orihttp.BadRequest(w, boundsErr.Error())
		return
	}

	calendarIDs := q["calendar_id"]
	if len(calendarIDs) == 0 {
		calendarIDs = calendar.ReadBindingSettings(gw.Binding.Config).SelectedCalendarIDs
	}
	if len(calendarIDs) == 0 {
		orihttp.BadRequest(w, "no calendars selected; choose visible calendars in Calendar Ops setup or pass calendar_id")
		return
	}
	if len(calendarIDs) > maxAgendaCalendars {
		calendarIDs = calendarIDs[:maxAgendaCalendars]
	}

	// The display timezone is passed through explicitly (query override, else
	// the workspace's configured DisplayTimeZone) so a connector whose
	// mapping references a time_zone argument formats/interprets the range in
	// the zone the user actually sees, rather than an implicit server
	// default. start/end are already unambiguous instants (RFC3339, always
	// offset-bearing) regardless of this value.
	timeZone := strings.TrimSpace(q.Get("time_zone"))
	if timeZone == "" {
		timeZone = calendar.ReadBindingSettings(gw.Binding.Config).DisplayTimeZone
	}

	startStr, endStr := start.Format(time.RFC3339), end.Format(time.RFC3339)
	events := make([]calendar.Event, 0, maxAgendaEvents)
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
		if timeZone != "" {
			input["time_zone"] = timeZone
		}
		args, err := calendar.BuildArguments(input, op)
		if err != nil {
			continue
		}
		raw, cached := h.cachedCall(r.Context(), gw, calendar.OpListEvents, args, func(ctx context.Context, call calendar.ToolCaller, op agentworkspace.OperationMapping) (any, error) {
			return listEventsRaw(ctx, call, op, args, calID)
		})
		if cached.err != nil {
			// One calendar failing (e.g. connector-side per-calendar
			// permission issue) must not blank the whole agenda; skip it.
			continue
		}
		calEvents, _ := raw.([]calendar.Event)
		events = append(events, calEvents...)
		if len(events) >= maxAgendaEvents {
			events = events[:maxAgendaEvents]
			break
		}
	}

	_ = orihttp.RespondSuccess(w, eventsResponse{Events: events, StartTime: startStr, EndTime: endStr, TimeZone: timeZone})
}

// listEventsRaw fetches events for calendarID and applies the mapping.
// calendarID backfills Event.CalendarID whenever the connector's list_events
// mapping doesn't resolve it per-item (a real, observed connector shape: the
// calendar being queried is already implied by the request, so some
// connectors simply don't echo it back on every result row) -- callers
// downstream (meeting prep's link key, the update_event mapping argument)
// require a populated CalendarID on every event this package hands out.
func listEventsRaw(ctx context.Context, call calendar.ToolCaller, op agentworkspace.OperationMapping, args map[string]any, calendarID string) ([]calendar.Event, error) {
	raw, err := call(ctx, op.Tool, args)
	if err != nil {
		return nil, err
	}
	items, err := calendar.Collection(raw, op)
	if err != nil {
		return nil, err
	}
	out := make([]calendar.Event, 0, len(items))
	for _, item := range items {
		evt := calendar.SanitizeEvent(calendar.ApplyEvent(item, op))
		if evt.ID == "" || evt.StartTime == "" || evt.EndTime == "" {
			continue
		}
		if evt.CalendarID == "" {
			evt.CalendarID = calendarID
		}
		out = append(out, evt)
		if len(out) >= maxAgendaEvents {
			break
		}
	}
	return out, nil
}

// parseAgendaRange validates and clamps a requested [start,end) range: both
// bounds must be RFC3339, end must be after start, and the span must not
// exceed maxAgendaRangeDays.
func parseAgendaRange(startRaw, endRaw string) (time.Time, time.Time, error) {
	start, err := time.Parse(time.RFC3339, strings.TrimSpace(startRaw))
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("start is required and must be an RFC3339 timestamp")
	}
	end, err := time.Parse(time.RFC3339, strings.TrimSpace(endRaw))
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("end is required and must be an RFC3339 timestamp")
	}
	if !end.After(start) {
		return time.Time{}, time.Time{}, fmt.Errorf("end must be after start")
	}
	if end.Sub(start) > maxAgendaRangeDays*24*time.Hour {
		return time.Time{}, time.Time{}, fmt.Errorf("requested range exceeds the %d-day maximum", maxAgendaRangeDays)
	}
	return start, end, nil
}

// --- event detail ------------------------------------------------------

type eventDetailResponse struct {
	Event   calendar.Event `json:"event"`
	Mapped  bool           `json:"mapped"`
	Message string         `json:"message,omitempty"`
}

// EventDetail handles GET /api/calendar-ops/events/detail?workspace_id=&
// calendar_id=&event_id=. get_event is optional in the calendar contract; if
// unmapped this responds mapped:false with a 200 (not an error) so the
// frontend falls back to the agenda item it already has instead of treating
// an optional capability's absence as a failure.
func (h *Handler) EventDetail(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodGet) {
		return
	}
	q := r.URL.Query()
	workspaceID := strings.TrimSpace(q.Get("workspace_id"))
	gw, gerr := h.resolveGateway(r.Context(), workspaceID)
	if gerr != nil {
		writeGatewayError(w, gerr)
		return
	}

	op, mapped := gw.Mapping.Operation(calendar.OpGetEvent)
	if !mapped {
		_ = orihttp.RespondSuccess(w, eventDetailResponse{Mapped: false, Message: "get_event is not mapped for this connector"})
		return
	}

	calendarID := strings.TrimSpace(q.Get("calendar_id"))
	eventID := strings.TrimSpace(q.Get("event_id"))
	if calendarID == "" || eventID == "" {
		orihttp.BadRequest(w, "calendar_id and event_id are required")
		return
	}

	args, err := calendar.BuildArguments(map[string]any{"calendar_id": calendarID, "id": eventID}, op)
	if err != nil {
		orihttp.InternalError(w, "failed to build connector arguments: "+err.Error())
		return
	}

	raw, cached := h.cachedCall(r.Context(), gw, calendar.OpGetEvent, args, func(ctx context.Context, call calendar.ToolCaller, op agentworkspace.OperationMapping) (any, error) {
		result, err := call(ctx, op.Tool, args)
		if err != nil {
			return nil, err
		}
		return calendar.SanitizeEvent(calendar.ApplyEvent(result, op)), nil
	})
	if cached.err != nil {
		orihttp.InternalError(w, "failed to load event: "+cached.err.Error())
		return
	}
	evt, _ := raw.(calendar.Event)
	_ = orihttp.RespondSuccess(w, eventDetailResponse{Event: evt, Mapped: true})
}

// --- free windows ------------------------------------------------------

// maxFreeWindowRangeDays bounds a free-window request tighter than the
// general agenda range: freebusy/suggest_time queries are typically scoped to
// "find a slot this week," and connectors are more likely to reject/degrade
// on a wide multi-week freebusy scan.
const maxFreeWindowRangeDays = 14

type freeWindowsResponse struct {
	Mapped    bool                `json:"mapped"`
	Operation string              `json:"operation,omitempty"` // "freebusy" | "suggest_time"
	Windows   []calendar.TimeSlot `json:"windows,omitempty"`
	StartTime string              `json:"start_time"`
	EndTime   string              `json:"end_time"`
}

// FreeWindows handles GET /api/calendar-ops/free-windows. When the connector
// maps freebusy or suggest_time (freebusy preferred), it invokes that
// operation and returns provider-confirmed windows. When neither is mapped,
// it responds mapped:false with zero MCP calls -- FR39 requires the frontend
// derive gaps client-side from the already-loaded agenda range in that case,
// and label them "event-derived" rather than implying provider-confirmed
// availability, which this endpoint deliberately does not fabricate.
func (h *Handler) FreeWindows(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodGet) {
		return
	}
	q := r.URL.Query()
	workspaceID := strings.TrimSpace(q.Get("workspace_id"))
	gw, gerr := h.resolveGateway(r.Context(), workspaceID)
	if gerr != nil {
		writeGatewayError(w, gerr)
		return
	}

	operation := calendar.OpFreeBusy
	op, mapped := gw.Mapping.Operation(operation)
	if !mapped {
		operation = calendar.OpSuggestTime
		op, mapped = gw.Mapping.Operation(operation)
	}
	if !mapped {
		_ = orihttp.RespondSuccess(w, freeWindowsResponse{Mapped: false})
		return
	}

	startT, endT, boundsErr := parseFreeWindowRange(q.Get("start"), q.Get("end"))
	if boundsErr != nil {
		orihttp.BadRequest(w, boundsErr.Error())
		return
	}
	startStr, endStr := startT.Format(time.RFC3339), endT.Format(time.RFC3339)

	args, err := calendar.BuildArguments(map[string]any{"start_time": startStr, "end_time": endStr}, op)
	if err != nil {
		orihttp.InternalError(w, "failed to build connector arguments: "+err.Error())
		return
	}

	raw, cached := h.cachedCall(r.Context(), gw, operation, args, func(ctx context.Context, call calendar.ToolCaller, op agentworkspace.OperationMapping) (any, error) {
		result, err := call(ctx, op.Tool, args)
		if err != nil {
			return nil, err
		}
		items, err := calendar.Collection(result, op)
		if err != nil {
			return nil, err
		}
		windows := make([]calendar.TimeSlot, 0, len(items))
		for _, item := range items {
			slot := calendar.SanitizeTimeSlot(calendar.ApplyTimeSlot(item, op))
			if slot.StartTime == "" || slot.EndTime == "" {
				continue
			}
			windows = append(windows, slot)
		}
		return windows, nil
	})
	if cached.err != nil {
		orihttp.InternalError(w, "failed to load free windows: "+cached.err.Error())
		return
	}
	windows, _ := raw.([]calendar.TimeSlot)
	_ = orihttp.RespondSuccess(w, freeWindowsResponse{
		Mapped:    true,
		Operation: operation,
		Windows:   windows,
		StartTime: startStr,
		EndTime:   endStr,
	})
}

func parseFreeWindowRange(startRaw, endRaw string) (time.Time, time.Time, error) {
	start, end, err := parseAgendaRange(startRaw, endRaw)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	if end.Sub(start) > maxFreeWindowRangeDays*24*time.Hour {
		return time.Time{}, time.Time{}, fmt.Errorf("requested range exceeds the %d-day free-window maximum", maxFreeWindowRangeDays)
	}
	return start, end, nil
}

// --- shared cached-call plumbing -------------------------------------------

type cachedCallResult struct{ err error }

// cachedCall wraps a read operation with the gateway's short-TTL cache
// (task 4.4): a cache hit skips the connector entirely; a miss invokes fn,
// and only a successful result is stored (errors are never cached, so the
// next request always retries against the connector).
func (h *Handler) cachedCall(
	ctx context.Context,
	gw *gatewayContext,
	operation string,
	args map[string]any,
	fn func(ctx context.Context, call calendar.ToolCaller, op agentworkspace.OperationMapping) (any, error),
) (any, cachedCallResult) {
	op, ok := gw.Mapping.Operation(operation)
	if !ok {
		return nil, cachedCallResult{err: fmt.Errorf("%q is not mapped", operation)}
	}

	key := readCacheKey{
		UserID:      gw.UserID,
		WorkspaceID: gw.Workspace.ID,
		BindingID:   gw.Binding.ID,
		Operation:   operation,
		ArgsHash:    readCacheArgsHash(args),
	}
	if cached, hit := h.cache.get(key); hit {
		return cached, cachedCallResult{}
	}

	call := h.toolCallerFor(gw.Binding.ServerName)
	value, err := fn(ctx, call, op)
	if err != nil {
		return nil, cachedCallResult{err: err}
	}
	h.cache.set(key, value)
	return value, cachedCallResult{}
}

package calendarhttp

import (
	"fmt"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/calendar"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// mutationAttendee is the wire shape of one attendee on a create/update
// mutation request.
type mutationAttendee struct {
	Email       string `json:"email"`
	DisplayName string `json:"display_name,omitempty"`
}

// mutationRequest is the shared wire shape for both Preview and Confirm.
// Confirm additionally carries ConfirmationID; every other field must be
// byte-for-byte what was previewed, or the payload hash check in Confirm
// rejects it (FR31).
type mutationRequest struct {
	WorkspaceID    string             `json:"workspace_id"`
	ConfirmationID string             `json:"confirmation_id,omitempty"` // Confirm only
	Operation      string             `json:"operation"`                 // "create_event" | "update_event"
	CalendarID     string             `json:"calendar_id"`
	EventID        string             `json:"event_id,omitempty"` // required for update_event
	Title          string             `json:"title"`
	StartTime      string             `json:"start_time"` // RFC3339
	EndTime        string             `json:"end_time"`   // RFC3339
	TimeZone       string             `json:"time_zone,omitempty"`
	Location       string             `json:"location,omitempty"`
	Description    string             `json:"description,omitempty"`
	Attendees      []mutationAttendee `json:"attendees,omitempty"`
}

// normalizedMutationPayload is the validated, canonicalized form of a
// mutationRequest -- trimmed strings, RFC3339-normalized times, deduplicated
// attendees in a stable order -- used both to render the preview and, via
// hashMutationPayload, to bind a confirmation to exactly these field values.
// Field order here is the JSON field order used for hashing; do not reorder
// without treating it as a breaking change to already-issued confirmations
// (harmless in practice since confirmations are short-lived, but keep it
// deliberate).
type normalizedMutationPayload struct {
	Operation   string             `json:"operation"`
	CalendarID  string             `json:"calendar_id"`
	EventID     string             `json:"event_id,omitempty"`
	Title       string             `json:"title"`
	StartTime   string             `json:"start_time"`
	EndTime     string             `json:"end_time"`
	TimeZone    string             `json:"time_zone,omitempty"`
	Location    string             `json:"location,omitempty"`
	Description string             `json:"description,omitempty"`
	Attendees   []mutationAttendee `json:"attendees,omitempty"`
}

// validateAndNormalizeMutation applies task 4.6's rejection rules and returns
// a canonical payload on success. It never touches the network -- this is
// pure validation so Preview can run it with zero MCP calls.
func validateAndNormalizeMutation(req mutationRequest) (normalizedMutationPayload, []string) {
	var errs []string
	out := normalizedMutationPayload{}

	op := strings.ToLower(strings.TrimSpace(req.Operation))
	switch op {
	case calendar.OpCreateEvent, calendar.OpUpdateEvent:
		out.Operation = op
	case "":
		errs = append(errs, "operation is required")
	default:
		// Deliberately rejects everything else by name, including
		// delete_event/rsvp/recurring-series style operations: v1 defines no
		// such operation, so any value other than the two writes above is
		// unsupported.
		errs = append(errs, fmt.Sprintf("unsupported mutation operation %q", req.Operation))
	}

	out.CalendarID = strings.TrimSpace(req.CalendarID)
	if out.CalendarID == "" {
		errs = append(errs, "calendar_id is required")
	}

	out.EventID = strings.TrimSpace(req.EventID)
	if op == calendar.OpUpdateEvent && out.EventID == "" {
		errs = append(errs, "event_id is required to update an event")
	}
	if op == calendar.OpCreateEvent && out.EventID != "" {
		errs = append(errs, "event_id must not be set when creating an event")
	}

	out.Title = strings.TrimSpace(req.Title)
	if out.Title == "" {
		errs = append(errs, "title is required")
	}

	start, startErr := parseMutationTime(req.StartTime)
	end, endErr := parseMutationTime(req.EndTime)
	if startErr != nil {
		errs = append(errs, "start_time: "+startErr.Error())
	}
	if endErr != nil {
		errs = append(errs, "end_time: "+endErr.Error())
	}
	if startErr == nil && endErr == nil {
		if !end.After(start) {
			errs = append(errs, "end_time must be strictly after start_time")
		} else {
			out.StartTime = start.Format(time.RFC3339)
			out.EndTime = end.Format(time.RFC3339)
		}
	}

	out.TimeZone = strings.TrimSpace(req.TimeZone)
	if out.TimeZone != "" {
		if _, err := time.LoadLocation(out.TimeZone); err != nil {
			errs = append(errs, fmt.Sprintf("time_zone %q is not a recognized IANA zone", out.TimeZone))
		}
	}

	out.Location = strings.TrimSpace(req.Location)
	out.Description = strings.TrimSpace(req.Description)

	attendees, attendeeErrs := normalizeMutationAttendees(req.Attendees)
	out.Attendees = attendees
	errs = append(errs, attendeeErrs...)

	return out, errs
}

func parseMutationTime(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, fmt.Errorf("is required")
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("must be an RFC3339 timestamp")
	}
	return t, nil
}

// normalizeMutationAttendees requires every attendee to carry an explicit
// display (a name or a syntactically valid email) so the preview never shows
// an anonymous/blank invitee (task 4.6). Attendees are sorted by email so
// payload hashing is insensitive to submission order.
func normalizeMutationAttendees(in []mutationAttendee) ([]mutationAttendee, []string) {
	if len(in) == 0 {
		return nil, nil
	}
	var errs []string
	seen := make(map[string]struct{}, len(in))
	out := make([]mutationAttendee, 0, len(in))
	for i, a := range in {
		email := strings.TrimSpace(a.Email)
		name := strings.TrimSpace(a.DisplayName)
		if email == "" && name == "" {
			errs = append(errs, fmt.Sprintf("attendee %d must have an email or a display name", i+1))
			continue
		}
		if email != "" {
			if _, err := mail.ParseAddress(email); err != nil {
				errs = append(errs, fmt.Sprintf("attendee %d email %q is not valid", i+1, email))
				continue
			}
			key := strings.ToLower(email)
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
		}
		out = append(out, mutationAttendee{Email: email, DisplayName: name})
	}
	sortMutationAttendees(out)
	if len(out) == 0 {
		return nil, errs
	}
	return out, errs
}

func sortMutationAttendees(a []mutationAttendee) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0; j-- {
			ki := mutationAttendeeSortKey(a[j])
			kj := mutationAttendeeSortKey(a[j-1])
			if ki >= kj {
				break
			}
			a[j], a[j-1] = a[j-1], a[j]
		}
	}
}

func mutationAttendeeSortKey(a mutationAttendee) string {
	if a.Email != "" {
		return strings.ToLower(a.Email)
	}
	return strings.ToLower(a.DisplayName)
}

// --- HTTP routes -------------------------------------------------------

type mutationPreviewResponse struct {
	ConfirmationID    string             `json:"confirmation_id"`
	Operation         string             `json:"operation"`
	CalendarID        string             `json:"calendar_id"`
	EventID           string             `json:"event_id,omitempty"`
	Title             string             `json:"title"`
	StartTime         string             `json:"start_time"`
	EndTime           string             `json:"end_time"`
	TimeZone          string             `json:"time_zone,omitempty"`
	Location          string             `json:"location,omitempty"`
	Description       string             `json:"description,omitempty"`
	Attendees         []mutationAttendee `json:"attendees,omitempty"`
	NotifiesAttendees bool               `json:"notifies_attendees"`
	ExpiresAt         time.Time          `json:"expires_at"`
}

// Preview handles POST /api/calendar-ops/mutations/preview. It performs
// ownership/readiness resolution and structural validation only -- zero MCP
// calls, and in particular zero writes (FR29, FR32) -- then mints a
// short-lived, single-use confirmation bound to the exact normalized payload.
func (h *Handler) Preview(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	var req mutationRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	gw, gerr := h.resolveGateway(r.Context(), strings.TrimSpace(req.WorkspaceID))
	if gerr != nil {
		writeGatewayError(w, gerr)
		return
	}

	payload, errs := validateAndNormalizeMutation(req)
	if len(errs) > 0 {
		orihttp.ValidationError(w, "mutation preview is invalid", errs)
		return
	}

	if _, mapped := gw.Mapping.Operation(payload.Operation); !mapped {
		orihttp.BadRequest(w, fmt.Sprintf("the %q operation is not mapped for this connector", payload.Operation))
		return
	}

	hash := hashMutationPayload(payload)
	c := h.confirmations.create(gw.UserID, gw.Workspace.ID, gw.Binding.ID, payload.Operation, hash)

	_ = orihttp.RespondSuccess(w, mutationPreviewResponse{
		ConfirmationID:    c.ID,
		Operation:         payload.Operation,
		CalendarID:        payload.CalendarID,
		EventID:           payload.EventID,
		Title:             payload.Title,
		StartTime:         payload.StartTime,
		EndTime:           payload.EndTime,
		TimeZone:          payload.TimeZone,
		Location:          payload.Location,
		Description:       payload.Description,
		Attendees:         payload.Attendees,
		NotifiesAttendees: len(payload.Attendees) > 0,
		ExpiresAt:         c.ExpiresAt,
	})
}

type mutationConfirmResponse struct {
	Success bool   `json:"success"`
	EventID string `json:"event_id,omitempty"`
	Error   string `json:"error,omitempty"`
}

// Confirm handles POST /api/calendar-ops/mutations/confirm. It re-resolves
// ownership/binding readiness (never trusts the state from Preview time),
// re-validates and re-normalizes the resent payload, atomically consumes the
// confirmation (rechecking hash/expiry/ownership), and only then invokes
// exactly one external MCP tool call. A connector failure is reported as
// success:false, never as a completed change (FR33); either way the affected
// binding's read cache is invalidated so the next agenda read is fresh.
func (h *Handler) Confirm(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	var req mutationRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	confirmationID := strings.TrimSpace(req.ConfirmationID)
	if confirmationID == "" {
		orihttp.BadRequest(w, "confirmation_id is required")
		return
	}

	gw, gerr := h.resolveGateway(r.Context(), strings.TrimSpace(req.WorkspaceID))
	if gerr != nil {
		writeGatewayError(w, gerr)
		return
	}

	payload, errs := validateAndNormalizeMutation(req)
	if len(errs) > 0 {
		orihttp.ValidationError(w, "mutation payload is invalid", errs)
		return
	}

	op, mapped := gw.Mapping.Operation(payload.Operation)
	if !mapped {
		orihttp.BadRequest(w, fmt.Sprintf("the %q operation is not mapped for this connector", payload.Operation))
		return
	}

	hash := hashMutationPayload(payload)
	c, err := h.confirmations.consume(confirmationID, gw.UserID, gw.Workspace.ID, gw.Binding.ID, payload.Operation, hash)
	if err != nil {
		orihttp.Conflict(w, err.Error())
		return
	}
	_ = c // validated; the invocation below uses gw/payload/op directly.

	args, err := buildMutationArguments(payload, op)
	if err != nil {
		orihttp.InternalError(w, "failed to build connector arguments: "+err.Error())
		return
	}

	call := h.toolCallerFor(gw.Binding.ServerName)
	_, callErr := call(r.Context(), op.Tool, args)
	h.cache.invalidateBinding(gw.Binding.ID)
	if callErr != nil {
		_ = orihttp.RespondSuccess(w, mutationConfirmResponse{Success: false, Error: callErr.Error()})
		return
	}

	_ = orihttp.RespondSuccess(w, mutationConfirmResponse{Success: true, EventID: payload.EventID})
}

// buildMutationArguments maps a normalized mutation payload onto the mapped
// operation's argument JSON Pointers via calendar.BuildArguments, the same
// deterministic mapping engine used everywhere else. Attendees are passed
// through as plain maps so a mapping's attendee argument pointer resolves
// against a JSON-shaped list.
func buildMutationArguments(payload normalizedMutationPayload, op agentworkspace.OperationMapping) (map[string]any, error) {
	input := map[string]any{
		"calendar_id": payload.CalendarID,
		"title":       payload.Title,
		"start_time":  payload.StartTime,
		"end_time":    payload.EndTime,
	}
	if payload.EventID != "" {
		input["id"] = payload.EventID
	}
	if payload.TimeZone != "" {
		input["time_zone"] = payload.TimeZone
	}
	if payload.Location != "" {
		input["location"] = payload.Location
	}
	if payload.Description != "" {
		input["description"] = payload.Description
	}
	if len(payload.Attendees) > 0 {
		attendees := make([]any, 0, len(payload.Attendees))
		for _, a := range payload.Attendees {
			attendees = append(attendees, map[string]any{
				"email":        a.Email,
				"display_name": a.DisplayName,
			})
		}
		input["attendees"] = attendees
	}
	return calendar.BuildArguments(input, op)
}

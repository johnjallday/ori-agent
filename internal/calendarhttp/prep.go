// On-demand meeting preparation (PRD FR42-48, task 6.0): "Prepare me" starts
// an asynchronous task assigned to the Meeting Prep agent, grounded strictly
// in the normalized event and notes from only the Ori workspaces the user
// explicitly permitted as context during Calendar Ops setup. The agent saves
// its brief as a normal Calendar Ops note (tagged meeting-prep) and this
// package keeps a durable link (internal/meetingprep) from the event to that
// note so a rerun updates the same note instead of creating duplicates.
package calendarhttp

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/calendar"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/meetingprep"
	"github.com/johnjallday/ori-agent/internal/session"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// meetingPrepAgentName is the fixed roster agent every prep task is assigned
// to (matches the shipped calendar-ops template's specialist).
const meetingPrepAgentName = "Meeting Prep"

// noteIDSentinel is the exact line the prep task prompt requires the agent to
// end its response with, carrying the id of the note it saved via
// workspace_save_note. This is a deliberately simple, low-risk way to read a
// structured result back out of free-text task output; there is no separate
// signal (event bus, structured-output contract) wired for this yet -- see
// prep.go's package doc.
const noteIDSentinel = "MEETING_PREP_NOTE_ID:"

// maxPrepContextWorkspaces / maxPrepNotesPerWorkspace / maxPrepNoteChars keep
// the permitted-context note material bounded (task 6.3: "load bounded note
// summaries/content"), independent of how many workspaces/notes exist.
const (
	maxPrepContextWorkspaces = 8
	maxPrepNotesPerWorkspace = 12
	maxPrepNoteChars         = 4000
)

// NoteStore is the note CRUD subset meeting prep needs: creating/reading the
// permitted-context notes and reading/tagging the note the agent saves.
type NoteStore interface {
	CreateNote(ctx context.Context, note *session.WorkspaceNote) error
	GetNote(ctx context.Context, id string) (*session.WorkspaceNote, error)
	UpdateNote(ctx context.Context, note *session.WorkspaceNote) error
	ListNotesByWorkspace(ctx context.Context, workspaceID string) ([]session.WorkspaceNoteListItem, error)
}

// MeetingPrepStore is the durable event-to-note link store subset the prep
// flow needs (see internal/meetingprep; *meetingprep.SQLiteStore satisfies
// this).
type MeetingPrepStore interface {
	GetByKey(ctx context.Context, key meetingprep.Key) (*meetingprep.Link, error)
	StartRun(ctx context.Context, key meetingprep.Key, taskID string) (*meetingprep.Link, bool, error)
	MarkReady(ctx context.Context, id, noteID, fingerprint string) error
	MarkFailed(ctx context.Context, id, reason string) error
}

// TaskExecutor runs a workspace task to completion. Satisfied by
// *workspace.Orchestrator. Only available from Phase 22 of server startup
// (after wireCalendarOpsSetup itself runs at Phase 18), so it is wired in
// separately via SetTaskExecutor rather than through NewHandler.
type TaskExecutor interface {
	ExecuteTask(ctx context.Context, workspaceID string, task agentworkspace.Task) error
}

// SetNotes wires the note store (tests use a fake).
func (h *Handler) SetNotes(notes NoteStore) *Handler {
	h.notes = notes
	return h
}

// SetMeetingPreps wires the meeting-prep link store (tests use a fake).
func (h *Handler) SetMeetingPreps(preps MeetingPrepStore) *Handler {
	h.meetingPreps = preps
	return h
}

// SetTaskExecutor wires the task executor (tests use a fake).
func (h *Handler) SetTaskExecutor(exec TaskExecutor) *Handler {
	h.taskExecutor = exec
	return h
}

// --- request/response shapes -------------------------------------------

type prepareEventRequest struct {
	WorkspaceID string         `json:"workspace_id"`
	Event       calendar.Event `json:"event"`
}

type prepareEventResponse struct {
	Status         meetingprep.Status `json:"status"`
	AlreadyRunning bool               `json:"already_running"`
	NoteID         string             `json:"note_id,omitempty"`
}

// Prepare handles POST /api/calendar-ops/events/prepare. It validates the
// event is preparable, starts (or dedupes into) a run, and dispatches the
// Meeting Prep task asynchronously -- the HTTP response never waits for the
// task to finish.
func (h *Handler) Prepare(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	var req prepareEventRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	gw, gerr := h.resolveGateway(r.Context(), strings.TrimSpace(req.WorkspaceID))
	if gerr != nil {
		writeGatewayError(w, gerr)
		return
	}
	if h.notes == nil || h.meetingPreps == nil || h.taskExecutor == nil {
		orihttp.ServiceUnavailable(w, "meeting preparation is not available")
		return
	}

	evt := calendar.SanitizeEvent(req.Event)
	if err := validatePreparableEvent(evt); err != nil {
		orihttp.BadRequest(w, err.Error())
		return
	}
	if !gw.Workspace.HasAgent(meetingPrepAgentName) {
		orihttp.BadRequest(w, "this workspace has no Meeting Prep agent")
		return
	}

	key := meetingprep.Key{
		WorkspaceID: gw.Workspace.ID,
		BindingID:   gw.Binding.ID,
		CalendarID:  strings.TrimSpace(evt.CalendarID),
		EventID:     evt.ID,
	}
	if key.CalendarID == "" {
		// A connector may not echo calendar_id back on every event; fall back
		// to the request's own scoping so the key still exists.
		key.CalendarID = "default"
	}

	taskID := ""
	link, already, err := h.meetingPreps.StartRun(r.Context(), key, taskID)
	if err != nil {
		orihttp.InternalError(w, "failed to start meeting prep: "+err.Error())
		return
	}
	if already {
		_ = orihttp.RespondSuccess(w, prepareEventResponse{Status: link.Status, AlreadyRunning: true, NoteID: link.NoteID})
		return
	}

	userID := gw.UserID
	settings := calendar.ReadBindingSettings(gw.Binding.Config)
	permittedWorkspaceIDs := h.filterOwnedActiveWorkspaceIDs(r.Context(), userID, gw.Workspace.ID, settings.ContextWorkspaceIDs)

	// A rerun updates the prior note in place; but if it was deleted out from
	// under the link (task 6.5: "a missing/deleted note is safely recreated
	// and relinked"), verify it still exists before telling the agent to
	// update it -- otherwise fall through to creating a fresh one.
	priorNoteID := ""
	if link.NoteID != "" {
		if _, err := h.notes.GetNote(r.Context(), link.NoteID); err == nil {
			priorNoteID = link.NoteID
		}
	}

	task := buildPrepTask(gw.Workspace.ID, evt, priorNoteID, h.loadPermittedContext(r.Context(), permittedWorkspaceIDs))
	if aerr := gw.Workspace.ApplyTaskAssignment(&task, agentworkspace.TaskAssignment{
		AgentName:  meetingPrepAgentName,
		Mode:       agentworkspace.TaskAssignmentModeManual,
		AssignedBy: agentworkspace.TaskAssignedByManual,
		Reason:     "Meeting Prep: Prepare me",
	}); aerr != nil {
		_ = h.meetingPreps.MarkFailed(r.Context(), link.ID, aerr.Error())
		orihttp.InternalError(w, "failed to assign meeting prep task: "+aerr.Error())
		return
	}
	task.Status = agentworkspace.TaskStatusAssigned
	if err := gw.Workspace.AddTask(task); err != nil {
		_ = h.meetingPreps.MarkFailed(r.Context(), link.ID, err.Error())
		orihttp.InternalError(w, "failed to create meeting prep task: "+err.Error())
		return
	}
	dispatchedTask := gw.Workspace.Tasks[len(gw.Workspace.Tasks)-1]
	if err := h.folders.Save(gw.Workspace); err != nil {
		_ = h.meetingPreps.MarkFailed(r.Context(), link.ID, err.Error())
		orihttp.InternalError(w, "failed to save meeting prep task: "+err.Error())
		return
	}

	// Deliberately context.Background(), not r.Context(): the whole point of
	// "asynchronous" (task 6.2) is that this run must survive the HTTP
	// response, which cancels the request context the instant this handler
	// returns.
	go h.runPrepTask(context.Background(), gw.Workspace.ID, dispatchedTask, link.ID, evt) // #nosec G118 -- request-scoped context would cancel the prep run the instant this handler returns; see comment above

	_ = orihttp.RespondSuccess(w, prepareEventResponse{Status: meetingprep.StatusPending, AlreadyRunning: false})
}

type prepStatusResponse struct {
	Linked  bool               `json:"linked"`
	Status  meetingprep.Status `json:"status,omitempty"`
	NoteID  string             `json:"note_id,omitempty"`
	Error   string             `json:"error,omitempty"`
	IsStale bool               `json:"is_stale,omitempty"`
}

// PrepStatus handles GET /api/calendar-ops/events/prep-status?workspace_id=&
// calendar_id=&event_id=[&title=&start_time=&end_time=&location=&description=].
// It reports whether a meeting-prep link exists for this event and, if the
// current normalized event fields are supplied, whether the linked note may
// be stale relative to the live event (its fingerprint no longer matches) --
// task 6.6's "prep status and note-link fields" for the event detail drawer.
// This is independent of whether get_event is mapped: the frontend already
// has the event from the agenda read, so this never touches the connector.
func (h *Handler) PrepStatus(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodGet) {
		return
	}
	q := r.URL.Query()
	gw, gerr := h.resolveGateway(r.Context(), strings.TrimSpace(q.Get("workspace_id")))
	if gerr != nil {
		writeGatewayError(w, gerr)
		return
	}
	if h.meetingPreps == nil {
		_ = orihttp.RespondSuccess(w, prepStatusResponse{Linked: false})
		return
	}

	calendarID := strings.TrimSpace(q.Get("calendar_id"))
	eventID := strings.TrimSpace(q.Get("event_id"))
	if calendarID == "" || eventID == "" {
		orihttp.BadRequest(w, "calendar_id and event_id are required")
		return
	}
	key := meetingprep.Key{WorkspaceID: gw.Workspace.ID, BindingID: gw.Binding.ID, CalendarID: calendarID, EventID: eventID}

	link, err := h.meetingPreps.GetByKey(r.Context(), key)
	if err != nil {
		_ = orihttp.RespondSuccess(w, prepStatusResponse{Linked: false})
		return
	}

	resp := prepStatusResponse{Linked: true, Status: link.Status, NoteID: link.NoteID, Error: link.Error}
	if link.Status == meetingprep.StatusReady && q.Get("title") != "" {
		current := meetingprep.Fingerprint(meetingprep.FingerprintInput{
			Title: q.Get("title"), StartTime: q.Get("start_time"), EndTime: q.Get("end_time"),
			Location: q.Get("location"), Description: q.Get("description"),
		})
		resp.IsStale = current != link.EventFingerprint
	}
	_ = orihttp.RespondSuccess(w, resp)
}

// validatePreparableEvent enforces task 6.2's "stable id, title, and usable
// time" gate server-side -- the frontend only shows the Prepare-me action
// under the same condition, but this handler never trusts that.
func validatePreparableEvent(evt calendar.Event) error {
	if strings.TrimSpace(evt.ID) == "" {
		return fmt.Errorf("event has no stable id")
	}
	if strings.TrimSpace(evt.Title) == "" {
		return fmt.Errorf("event has no title")
	}
	start, err := time.Parse(time.RFC3339, evt.StartTime)
	if err != nil {
		return fmt.Errorf("event has no usable start time")
	}
	end, err := time.Parse(time.RFC3339, evt.EndTime)
	if err != nil {
		return fmt.Errorf("event has no usable end time")
	}
	if !end.After(start) {
		return fmt.Errorf("event end time must be after its start time")
	}
	return nil
}

// contextNote is one bounded, permission-checked note handed to the prep
// task as grounding material.
type contextNote struct {
	WorkspaceID string
	Name        string
	Content     string
}

// loadPermittedContext reads bounded note content from exactly the
// workspaces the caller has already re-verified as owned/active/permitted
// (task 6.3). It performs no ownership check itself -- that already happened
// in Prepare via filterOwnedActiveWorkspaceIDs -- and it never reads Email
// Ops, external documents, transcripts, or any workspace outside this list.
func (h *Handler) loadPermittedContext(ctx context.Context, permittedWorkspaceIDs []string) []contextNote {
	if h.notes == nil {
		return nil
	}
	ids := permittedWorkspaceIDs
	if len(ids) > maxPrepContextWorkspaces {
		ids = ids[:maxPrepContextWorkspaces]
	}
	var out []contextNote
	for _, wsID := range ids {
		items, err := h.notes.ListNotesByWorkspace(ctx, wsID)
		if err != nil {
			continue
		}
		if len(items) > maxPrepNotesPerWorkspace {
			items = items[:maxPrepNotesPerWorkspace]
		}
		for _, item := range items {
			note, err := h.notes.GetNote(ctx, item.ID)
			if err != nil || note == nil {
				continue
			}
			content := note.Content
			if len(content) > maxPrepNoteChars {
				content = content[:maxPrepNoteChars]
			}
			out = append(out, contextNote{WorkspaceID: wsID, Name: note.Name, Content: content})
		}
	}
	return out
}

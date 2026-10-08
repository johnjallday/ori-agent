package agenthttp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/johnjallday/ori-agent/internal/assistantcontext"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// ErrAssistantSourceNotFound is a record that does not exist. A reader reports
// it the same way as a record that belongs to another workspace.
var ErrAssistantSourceNotFound = errors.New("assistant source not found")

// AssistantNote is one canonical workspace note, narrowed to what a read needs.
type AssistantNote struct {
	ID          string
	WorkspaceID string
	Name        string
	Content     string
	UpdatedAt   time.Time
}

// AssistantNoteSummary is a note's identity without its content.
type AssistantNoteSummary struct {
	ID          string
	WorkspaceID string
	Name        string
	UpdatedAt   time.Time
}

// AssistantNoteReader is the canonical note store narrowed to two reads. It has
// no create, update, delete, tag or search operation, so the panel's model can
// never save or change a note through it.
type AssistantNoteReader interface {
	Note(ctx context.Context, id string) (AssistantNote, error)
	Notes(ctx context.Context, workspaceID string) ([]AssistantNoteSummary, error)
}

const (
	readerNotes = "assistant_workspace_notes"
	readerNote  = "assistant_workspace_note"
	readerTasks = "assistant_workspace_tasks"
	readerTask  = "assistant_workspace_task"

	readerListLimit    = 50
	readerTaskTextCap  = 8000
	readerTaskErrorCap = 2000
)

// readerDefinitions are the brokered readers for the turn's pinned workspace.
// They are advertised only when that workspace exists and the provider can run
// Ori's tools; every one is a read.
func (r *panelToolRegistry) readerDefinitions() []llm.Tool {
	if r.turn == nil || r.turn.projection.Subject == nil {
		return nil
	}
	none := map[string]any{"type": "object", "properties": map[string]any{}}
	tools := []llm.Tool{
		{Name: readerTasks, Description: "List this workspace's tasks with their recorded state, assignee and update time. Titles only; this does not read a task's details. Read-only.", Parameters: map[string]any{"type": "object", "properties": map[string]any{
			"state": map[string]any{"type": "string", "description": "Optional recorded state filter: backlog, ready, in_progress, review, done or cancelled."},
		}}},
		{Name: readerTask, Description: "Read one task's current details: description, recorded state, assignee, result and error. Use the task_id from the list or the overview. Read-only; it never starts, assigns or changes a task.", Parameters: map[string]any{"type": "object", "required": []string{"task_id"}, "properties": map[string]any{
			"task_id": map[string]any{"type": "string", "description": "The exact task_id."},
		}}},
	}
	if r.handler.Notes != nil {
		tools = append(tools,
			llm.Tool{Name: readerNotes, Description: "List this workspace's notes by title and update time. A title is not the note's content. Read-only.", Parameters: none},
			llm.Tool{Name: readerNote, Description: "Read the current content of one note in this workspace. Give note_id from the list, or an exact title when only one note has it. Long notes are read in parts: pass the returned next_offset to continue. Read-only; it never saves or edits a note.", Parameters: map[string]any{"type": "object", "properties": map[string]any{
				"note_id": map[string]any{"type": "string", "description": "The exact note_id."},
				"title":   map[string]any{"type": "string", "description": "An exact note title, when note_id is not known."},
				"offset":  map[string]any{"type": "integer", "description": "Character position to continue from; use next_offset from the previous part."},
			}}})
	}
	return tools
}

func isReader(name string) bool {
	return name == readerNotes || name == readerNote || name == readerTasks || name == readerTask
}

// A reader result that is not a delivered source says so in the same shape.
func readerStatus(status assistantcontext.Availability, reason string, extra map[string]any) map[string]any {
	result := map[string]any{"status": status, "content_read": false}
	if reason != "" {
		result["reason"] = reason
	}
	for key, value := range extra {
		result[key] = value
	}
	return result
}

// readerText withholds secret-like lines before source text reaches a provider.
// The line count is unchanged, so the text still reads in order.
func readerText(text string) (string, int) {
	kept, _, _, _, withheld := readerChunk(text, 0, utf8.RuneCountInString(text))
	return kept, withheld
}

func readerVersion(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:6])
}

// readerHref is an in-app page for a record of the pinned workspace. It is
// built from canonical identifiers only; without a publishable slug there is
// no link rather than a guessed one.
func readerHref(subject *assistantcontext.WorkspaceRef, segment, id string) string {
	if subject == nil || subject.Slug == "" || strings.TrimSpace(id) == "" {
		return ""
	}
	return "/workspaces/" + url.PathEscape(subject.Slug) + "/" + segment + "/" + url.PathEscape(id)
}

func readerTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

// executeReader runs one brokered reader against the turn's pinned workspace.
// The caller has already re-checked the user, the relationship and both pinned
// workspaces for this call. The model may name a record; it cannot name a
// workspace, so nothing here can move the read to another one.
func (r *panelToolRegistry) executeReader(ctx context.Context, name string, args map[string]any) (map[string]any, error) {
	subject := r.turn.projection.Subject
	if subject == nil || r.handler.WorkspaceContext == nil || r.handler.WorkspaceContext.Source == nil {
		return readerStatus(assistantcontext.Unavailable, "no_workspace_in_scope", nil), nil
	}
	if r.ledger == nil {
		return nil, errors.New("evidence ledger unavailable")
	}
	switch name {
	case readerNotes:
		return r.listNotes(ctx, subject)
	case readerNote:
		return r.readNote(ctx, subject, args)
	case readerTasks:
		return r.listTasks(subject, homeToolString(args, "state"))
	case readerTask:
		return r.readTask(subject, homeToolString(args, "task_id"))
	}
	return nil, errors.New("unknown reader")
}

func (r *panelToolRegistry) subjectNotes(ctx context.Context, subject *assistantcontext.WorkspaceRef) ([]AssistantNoteSummary, bool) {
	if r.handler.Notes == nil {
		return nil, false
	}
	listed, err := r.handler.Notes.Notes(ctx, subject.ID)
	if err != nil {
		return nil, false
	}
	owned := make([]AssistantNoteSummary, 0, len(listed))
	for _, note := range listed {
		if note.WorkspaceID == subject.ID && workspaceReferenceToken.MatchString(note.ID) {
			owned = append(owned, note)
		}
	}
	sort.SliceStable(owned, func(i, j int) bool { return owned[i].UpdatedAt.After(owned[j].UpdatedAt) })
	return owned, true
}

func noteListing(notes []AssistantNoteSummary) []map[string]any {
	rows := make([]map[string]any, 0, min(len(notes), readerListLimit))
	for _, note := range notes {
		if len(rows) == readerListLimit {
			break
		}
		rows = append(rows, map[string]any{"note_id": note.ID, "title": workspaceContextText(note.Name, 160), "updated_at": readerTime(note.UpdatedAt)})
	}
	return rows
}

func (r *panelToolRegistry) listNotes(ctx context.Context, subject *assistantcontext.WorkspaceRef) (map[string]any, error) {
	notes, ok := r.subjectNotes(ctx, subject)
	if !ok {
		// A failed listing is not an empty workspace.
		return readerStatus(assistantcontext.Unavailable, "note_listing_failed", map[string]any{"workspace": subject.Name}), nil
	}
	if len(notes) == 0 {
		return readerStatus(assistantcontext.Empty, "", map[string]any{"workspace": subject.Name, "total": 0, "notes": []any{}}), nil
	}
	return readerStatus(assistantcontext.Available, "", map[string]any{
		"workspace": subject.Name, "total": len(notes), "truncated": len(notes) > readerListLimit, "notes": noteListing(notes),
		"next_step": "Read one with " + readerNote + ". A title is not the note's content.",
	}), nil
}

func (r *panelToolRegistry) readNote(ctx context.Context, subject *assistantcontext.WorkspaceRef, args map[string]any) (map[string]any, error) {
	if r.handler.Notes == nil {
		return readerStatus(assistantcontext.Unsupported, "note_reader_not_connected", nil), nil
	}
	id, title := homeToolString(args, "note_id"), homeToolString(args, "title")
	if id == "" && title == "" {
		return readerStatus(assistantcontext.Unavailable, "note_id_or_title_required", nil), nil
	}
	if id == "" {
		notes, ok := r.subjectNotes(ctx, subject)
		if !ok {
			return readerStatus(assistantcontext.Unavailable, "note_listing_failed", nil), nil
		}
		var matches []AssistantNoteSummary
		for _, note := range notes {
			if strings.EqualFold(strings.TrimSpace(note.Name), title) {
				matches = append(matches, note)
			}
		}
		switch len(matches) {
		case 0:
			return readerStatus(assistantcontext.Unavailable, "note_not_found_in_this_workspace", nil), nil
		case 1:
			id = matches[0].ID
		default:
			// Never pick one of several notes with the same title.
			return readerStatus(assistantcontext.Unavailable, "several_notes_share_this_title", map[string]any{
				"matches": noteListing(matches), "next_step": "Ask the user which note they mean, or read one by note_id.",
			}), nil
		}
	}
	if !workspaceReferenceToken.MatchString(id) {
		return readerStatus(assistantcontext.Unavailable, "note_not_found_in_this_workspace", nil), nil
	}
	note, err := r.handler.Notes.Note(ctx, id)
	// A note in another workspace is reported exactly like one that does not
	// exist, so an ID cannot be used to find out about another workspace.
	if errors.Is(err, ErrAssistantSourceNotFound) || (err == nil && (note.WorkspaceID != subject.ID || note.ID != id)) {
		return readerStatus(assistantcontext.Unavailable, "note_not_found_in_this_workspace", nil), nil
	}
	if err != nil {
		return readerStatus(assistantcontext.Unavailable, "note_read_failed", nil), nil
	}
	version := readerVersion(note.Content, readerTime(note.UpdatedAt))
	offset := 0
	if value, ok := args["offset"].(float64); ok && value > 0 {
		offset = int(value)
	}
	if prior, read := r.ledger.prior(assistantcontext.SourceNote, subject.ID, note.ID); read && offset > 0 && prior.Version != version {
		return readerStatus(assistantcontext.Partial, "note_changed_since_first_part", map[string]any{
			"note_id": note.ID, "next_step": "The note changed while it was being read. Read it again from the start.",
		}), nil
	}
	const continueNote = "This is part of the note. Continue with next_offset, or say that only this part was read."
	label := workspaceContextText(note.Name, 160)
	result := map[string]any{
		"status": assistantcontext.Available, "content_read": true,
		"workspace": subject.Name, "note_id": note.ID, "title": label, "updated_at": readerTime(note.UpdatedAt),
	}
	// The whole result is charged, not only the note's text.
	envelope := contentEnvelope(result, utf8.RuneCountInString(note.Content), continueNote)
	chunk, start, end, total, size, withheld := fitReaderChunk(note.Content, offset, assistantcontext.FileChunkLimit, r.ledger.remaining()-envelope)
	if (end == start && start < total) || !r.ledger.charge(size+envelope) {
		return readerStatus(assistantcontext.Partial, "evidence_budget_exhausted", map[string]any{
			"note_id": note.ID, "next_step": "This turn's reading budget is used up. Answer from what was read and say what was not.",
		}), nil
	}
	source := r.ledger.record(assistantcontext.SourceRef{
		Kind: assistantcontext.SourceNote, WorkspaceID: subject.ID, Workspace: subject.Name, ID: note.ID, Label: label,
		Version: version, UpdatedAt: note.UpdatedAt, Start: start, End: end, Total: total, Href: readerHref(subject, "notes", note.ID),
	})
	result["source"], result["cite_as"], result["read_at"] = source.Key, "["+source.Key+"]", readerTime(source.ReadAt)
	// Coverage is what this turn has read of the note so far, not this part alone.
	result["coverage"], result["start"], result["end"], result["total"], result["content"] = source.Coverage, start, end, total, chunk
	if end < total {
		result["next_offset"] = end
		result["next_step"] = continueNote
	}
	if withheld > 0 {
		result["withheld_lines"] = withheld
	}
	return result, nil
}

// subjectWorkspace reads the pinned workspace fresh for one reader call.
func (r *panelToolRegistry) subjectWorkspace(subject *assistantcontext.WorkspaceRef) (*workspace.Workspace, bool) {
	ws, err := r.handler.WorkspaceContext.Source.Get(subject.ID)
	if err != nil || !workspaceReadable(ws, r.turn.userID) {
		return nil, false
	}
	return ws, true
}

func taskUpdated(task workspace.Task) time.Time {
	if task.UpdatedAt.IsZero() {
		return task.CreatedAt
	}
	return task.UpdatedAt
}

func taskRow(task *workspace.Task, ws *workspace.Workspace) map[string]any {
	ticket := workspace.NewTicket(task, ws.ID, ws.Name, ws.FolderSlug)
	row := map[string]any{
		"task_id": task.ID, "title": workspaceContextText(ticket.Title, 240),
		"state": string(ticket.State), "state_label": ticket.StateLabel, "updated_at": readerTime(taskUpdated(*task)),
	}
	if ticket.DisplayNumber != "" {
		row["number"] = ticket.DisplayNumber
	}
	if assignee := workspaceContextText(ticket.Assignee, 80); assignee != "" {
		row["assignee"] = assignee
	}
	return row
}

func (r *panelToolRegistry) listTasks(subject *assistantcontext.WorkspaceRef, state string) (map[string]any, error) {
	ws, ok := r.subjectWorkspace(subject)
	if !ok {
		return readerStatus(assistantcontext.Unavailable, "workspace_unavailable", nil), nil
	}
	rows := []map[string]any{}
	total := 0
	for i := range ws.Tasks {
		task := &ws.Tasks[i]
		if !workspaceReferenceToken.MatchString(task.ID) || (task.WorkspaceID != "" && task.WorkspaceID != ws.ID) {
			continue
		}
		if state != "" && string(task.CanonicalState()) != state {
			continue
		}
		total++
		if len(rows) < readerListLimit {
			rows = append(rows, taskRow(task, ws))
		}
	}
	if total == 0 {
		return readerStatus(assistantcontext.Empty, "", map[string]any{"workspace": subject.Name, "total": 0, "tasks": []any{}}), nil
	}
	return readerStatus(assistantcontext.Available, "", map[string]any{
		"workspace": subject.Name, "total": total, "truncated": total > len(rows), "tasks": rows,
		"next_step": "Read one with " + readerTask + " before describing its details, result or error.",
	}), nil
}

// bounded cuts one recorded field and reports whether anything was left out.
func bounded(text string, limit int) (string, bool) {
	text = strings.TrimSpace(text)
	chunk, _, end, total := evidenceChunk(text, 0, limit)
	return chunk, end < total
}

func (r *panelToolRegistry) readTask(subject *assistantcontext.WorkspaceRef, id string) (map[string]any, error) {
	ws, ok := r.subjectWorkspace(subject)
	if !ok {
		return readerStatus(assistantcontext.Unavailable, "workspace_unavailable", nil), nil
	}
	var task *workspace.Task
	for i := range ws.Tasks {
		if ws.Tasks[i].ID == id && workspaceReferenceToken.MatchString(id) && (ws.Tasks[i].WorkspaceID == "" || ws.Tasks[i].WorkspaceID == ws.ID) {
			task = &ws.Tasks[i]
			break
		}
	}
	if task == nil {
		return readerStatus(assistantcontext.Unavailable, "task_not_found_in_this_workspace", nil), nil
	}
	details, withheld := readerText(task.Details)
	outcome, withheldOutcome := readerText(task.Result)
	failure, withheldFailure := readerText(task.Error)
	withheld += withheldOutcome + withheldFailure
	details, cutDetails := bounded(details, readerTaskTextCap)
	outcome, cutOutcome := bounded(outcome, readerTaskTextCap)
	failure, cutFailure := bounded(failure, readerTaskErrorCap)
	result := taskRow(task, ws)
	result["status"], result["content_read"], result["workspace"] = assistantcontext.Available, true, subject.Name
	result["run_status"], result["priority"], result["created_at"] = string(task.Status), task.Priority, readerTime(task.CreatedAt)
	// Recorded only when present: an absent result is not an empty one.
	if details != "" {
		result["description"] = details
	}
	if outcome != "" {
		result["recorded_result"] = outcome
	}
	if failure != "" {
		result["recorded_error"] = failure
	}
	if task.StartedAt != nil {
		result["started_at"] = readerTime(*task.StartedAt)
	}
	if task.CompletedAt != nil {
		result["completed_at"] = readerTime(*task.CompletedAt)
	}
	var subtasks []map[string]any
	for i := range ws.Tasks {
		other := &ws.Tasks[i]
		if other.ID == task.ParentTaskID && task.ParentTaskID != "" {
			result["parent"] = taskRow(other, ws)
		}
		if other.ParentTaskID == task.ID && len(subtasks) < 10 {
			subtasks = append(subtasks, taskRow(other, ws))
		}
	}
	if len(subtasks) > 0 {
		result["subtasks"] = subtasks
	}
	coverage := assistantcontext.CoverageFull
	if cutDetails || cutOutcome || cutFailure {
		coverage = assistantcontext.CoveragePartial
		result["truncated"] = true
	}
	if withheld > 0 {
		result["withheld_lines"] = withheld
	}
	result["note"] = "State, result and error are recorded facts. A priority or next step you suggest is your advice, not recorded state. Reading a task does not start or change it."
	// Measured with the fields that are filled in once the read is recorded, so
	// the whole result is charged.
	result["source"], result["cite_as"], result["coverage"], result["read_at"] = "S999", "[S999]", assistantcontext.CoveragePartial, readerTime(time.Now())
	encoded, err := homeToolJSON(result)
	if err != nil {
		return nil, errors.New("source unavailable")
	}
	size := evidenceSize(encoded)
	if !r.ledger.charge(size) {
		return readerStatus(assistantcontext.Partial, "evidence_budget_exhausted", map[string]any{"task_id": task.ID}), nil
	}
	ticket := workspace.NewTicket(task, ws.ID, ws.Name, ws.FolderSlug)
	label := workspaceContextText(ticket.Title, 160)
	if ticket.DisplayNumber != "" {
		label = ticket.DisplayNumber + " " + label
	}
	source := r.ledger.record(assistantcontext.SourceRef{
		Kind: assistantcontext.SourceTask, WorkspaceID: subject.ID, Workspace: subject.Name, ID: task.ID, Label: label,
		Version:   readerVersion(string(task.Status), string(ticket.State), task.Details, task.Result, task.Error, readerTime(taskUpdated(*task))),
		UpdatedAt: taskUpdated(*task), Total: size, End: size, Coverage: coverage, Href: readerHref(subject, "task", task.ID),
	})
	result["source"], result["cite_as"], result["coverage"], result["read_at"] = source.Key, "["+source.Key+"]", source.Coverage, readerTime(source.ReadAt)
	return result, nil
}

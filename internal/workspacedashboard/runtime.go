package workspacedashboard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacesurface"
)

// ErrOperationUnknown is returned for an operation id the dashboard vocabulary
// does not contain. It never falls through to another operation and never
// returns partial data: a dashboard must not be able to reach anything by
// guessing at a name.
var ErrOperationUnknown = errors.New("workspace dashboard operation is not available")

// WorkspaceReader resolves a workspace record by id. *workspace.SyncStore and
// *workspace.FileStore both satisfy it.
type WorkspaceReader interface {
	Get(workspaceID string) (*workspace.Workspace, error)
}

// NoteReader lists a workspace's notes. Satisfied by
// session.WorkspaceTaskContextAdapter, which already returns a narrow summary
// rather than note bodies.
type NoteReader interface {
	ListNotesByWorkspace(ctx context.Context, workspaceID string) ([]workspace.TaskPromptNoteSummary, error)
}

// SessionReader lists a workspace's recent sessions and their total count.
// Satisfied by session.WorkspaceTaskContextAdapter.
type SessionReader interface {
	ListSessionsByWorkspace(ctx context.Context, workspaceID string, limit int) ([]workspace.TaskPromptSessionSummary, int, error)
}

// Runtime serves a dashboard's read-only data operations. It implements
// workspacesurface.Runtime like any other, so dashboard operations dispatch
// through the existing broker with no parallel plumbing and no exemption from
// input validation, output bounds, timeouts, or the policy class.
//
// Every operation is scoped to the workspace in the trusted WorkspaceContext,
// which the browser cannot construct or override.
type Runtime struct {
	workspaces WorkspaceReader
	notes      NoteReader
	sessions   SessionReader
}

// NewRuntime creates the read-only dashboard runtime. Any dependency may be nil;
// the operations that need it then report empty rather than failing the whole
// dashboard, since a dashboard reading one thing should not go dark because an
// unrelated store is unavailable.
func NewRuntime(workspaces WorkspaceReader, notes NoteReader, sessions SessionReader) *Runtime {
	return &Runtime{workspaces: workspaces, notes: notes, sessions: sessions}
}

// Status reports the dashboard's health. A dashboard that resolved at all has
// its files in place; unlike a plugin service there is no process to be up or
// down, so there is nothing further to check.
func (r *Runtime) Status(context.Context, workspacesurface.WorkspaceContext) (workspacesurface.StationStatus, error) {
	return workspacesurface.StationStatus{
		State: workspacesurface.StationReady, Value: "Ready",
		Description: "This workspace's dashboard is ready.",
	}, nil
}

// listInput is the shared shape of every list operation's input. There is
// deliberately no workspace field: the workspace comes from the trusted
// WorkspaceContext, and the input schema's additionalProperties:false rejects
// any attempt to supply one (FR17).
type listInput struct {
	Limit  int    `json:"limit"`
	Offset int    `json:"offset"`
	Status string `json:"status"`
}

func (in listInput) bounds(total int) (limit, offset int) {
	limit = in.Limit
	if limit <= 0 {
		limit = defaultListLimit
	}
	if limit > maxListLimit {
		limit = maxListLimit
	}
	offset = in.Offset
	if offset < 0 {
		offset = 0
	}
	if offset > total {
		offset = total
	}
	return limit, offset
}

// Invoke dispatches one declared read-only operation.
func (r *Runtime) Invoke(ctx context.Context, invocation workspacesurface.Invocation) (workspacesurface.Result, error) {
	// The workspace is taken from the trusted context and nowhere else. A
	// workspace id in the operation input is not consulted, not merged, and not
	// compared — there is simply no path from input to workspace selection.
	workspaceID := strings.TrimSpace(invocation.Workspace.WorkspaceID)
	if workspaceID == "" {
		return workspacesurface.Result{}, errors.New("workspace dashboard operation has no workspace context")
	}

	var input listInput
	if len(invocation.Input) > 0 {
		if err := json.Unmarshal(invocation.Input, &input); err != nil {
			return workspacesurface.Result{}, fmt.Errorf("workspace dashboard operation input is invalid: %w", err)
		}
	}

	// Reject unknown ids before doing any work, so an undeclared operation can
	// never touch a store (FR20).
	if !isDeclaredOperation(invocation.Operation) {
		return workspacesurface.Result{}, fmt.Errorf("%w: %q", ErrOperationUnknown, invocation.Operation)
	}

	// Fit the response to the budget. Clipping individual fields is not enough:
	// a hundred entries each just under the field limit still overruns the
	// bridge's message cap, and an oversized bridge message is dropped by the
	// sender rather than reported — the dashboard would just see nothing. So the
	// encoded size is the thing actually checked, and the page shrinks until it
	// fits. has_more follows from the smaller page, so the dashboard can still
	// walk the whole list.
	input.Limit, _ = input.bounds(0)
	for {
		output, err := r.dispatch(ctx, invocation, workspaceID, input)
		if err != nil {
			return workspacesurface.Result{}, err
		}
		encoded, err := json.Marshal(output)
		if err != nil {
			return workspacesurface.Result{}, fmt.Errorf("workspace dashboard result could not be encoded: %w", err)
		}
		if len(encoded) <= maxOutputBytes || input.Limit <= 1 {
			if len(encoded) > maxOutputBytes {
				// One single entry is already too large to send. Report that
				// rather than emitting a message the bridge will silently drop.
				return workspacesurface.Result{}, fmt.Errorf(
					"workspace dashboard result is too large to send (%d bytes)", len(encoded))
			}
			return workspacesurface.Result{Output: encoded}, nil
		}
		input.Limit /= 2
	}
}

func (r *Runtime) dispatch(ctx context.Context, invocation workspacesurface.Invocation, workspaceID string, input listInput) (any, error) {
	switch invocation.Operation {
	case OpWorkspaceSummary:
		return r.summary(ctx, workspaceID)
	case OpTasksList:
		return r.tasks(workspaceID, input)
	case OpNotesList:
		return r.notesList(ctx, workspaceID, input)
	case OpAgentsList:
		return r.agents(workspaceID, input)
	case OpSessionsList:
		return r.sessionsList(ctx, workspaceID, input)
	case OpFilesList:
		return r.files(invocation.Workspace, input)
	default:
		return nil, fmt.Errorf("%w: %q", ErrOperationUnknown, invocation.Operation)
	}
}

func isDeclaredOperation(id string) bool {
	return slices.Contains(operationIDs(), id)
}

// ---------------------------------------------------------------------------
// Response builders. Every field a dashboard can ever see is named here; see
// the secret boundary note in operations.go for what is deliberately absent.
// ---------------------------------------------------------------------------

type summaryResponse struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Kind        string         `json:"kind,omitempty"`
	Designation string         `json:"designation,omitempty"`
	Description string         `json:"description,omitempty"`
	Tags        []string       `json:"tags,omitempty"`
	Counts      map[string]int `json:"counts"`
}

func (r *Runtime) summary(ctx context.Context, workspaceID string) (summaryResponse, error) {
	ws, err := r.workspace(workspaceID)
	if err != nil {
		return summaryResponse{}, err
	}
	open := 0
	for _, task := range ws.Tasks {
		if isOpenTask(string(task.Status)) {
			open++
		}
	}
	noteCount := 0
	if r.notes != nil {
		if notes, err := r.notes.ListNotesByWorkspace(ctx, workspaceID); err == nil {
			noteCount = len(notes)
		}
	}
	sessionCount := 0
	if r.sessions != nil {
		if _, total, err := r.sessions.ListSessionsByWorkspace(ctx, workspaceID, 1); err == nil {
			sessionCount = total
		}
	}
	tags := make([]string, 0, len(ws.Tags))
	for _, tag := range ws.Tags {
		tags = append(tags, clip(tag))
	}
	return summaryResponse{
		ID:   ws.ID,
		Name: clip(ws.Name),
		Kind: clip(ws.Kind),
		// Designation is a display label the workspace already exposes; it is
		// read from SharedData rather than assumed present.
		Designation: clip(sharedString(ws.SharedData, "designation")),
		Description: clip(ws.Description),
		Tags:        tags,
		Counts: map[string]int{
			"tasks":      len(ws.Tasks),
			"open_tasks": open,
			"agents":     len(ws.AgentInstances),
			"notes":      noteCount,
			"sessions":   sessionCount,
		},
	}, nil
}

type taskEntry struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Status   string `json:"status"`
	Priority int    `json:"priority"`
	Assignee string `json:"assignee,omitempty"`
}

type tasksResponse struct {
	Tasks   []taskEntry `json:"tasks"`
	Total   int         `json:"total"`
	Limit   int         `json:"limit"`
	Offset  int         `json:"offset"`
	HasMore bool        `json:"has_more"`
}

func (r *Runtime) tasks(workspaceID string, input listInput) (tasksResponse, error) {
	ws, err := r.workspace(workspaceID)
	if err != nil {
		return tasksResponse{}, err
	}
	filter := strings.ToLower(strings.TrimSpace(input.Status))
	matching := make([]workspace.Task, 0, len(ws.Tasks))
	for _, task := range ws.Tasks {
		status := strings.ToLower(string(task.Status))
		switch filter {
		case "":
			// no filter
		case "open":
			if !isOpenTask(status) {
				continue
			}
		default:
			if status != filter {
				continue
			}
		}
		matching = append(matching, task)
	}

	limit, offset := input.bounds(len(matching))
	page := matching[offset:min(offset+limit, len(matching))]
	entries := make([]taskEntry, 0, len(page))
	for _, task := range page {
		entries = append(entries, taskEntry{
			ID: task.ID, Title: clip(task.Description), Status: string(task.Status),
			Priority: task.Priority, Assignee: clip(task.To),
		})
	}
	return tasksResponse{
		Tasks: entries, Total: len(matching), Limit: limit, Offset: offset,
		HasMore: offset+len(entries) < len(matching),
	}, nil
}

type noteEntry struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type notesResponse struct {
	Notes   []noteEntry `json:"notes"`
	Total   int         `json:"total"`
	Limit   int         `json:"limit"`
	Offset  int         `json:"offset"`
	HasMore bool        `json:"has_more"`
}

func (r *Runtime) notesList(ctx context.Context, workspaceID string, input listInput) (notesResponse, error) {
	if r.notes == nil {
		return notesResponse{Notes: []noteEntry{}}, nil
	}
	notes, err := r.notes.ListNotesByWorkspace(ctx, workspaceID)
	if err != nil {
		return notesResponse{}, fmt.Errorf("workspace dashboard could not list notes: %w", err)
	}
	limit, offset := input.bounds(len(notes))
	page := notes[offset:min(offset+limit, len(notes))]
	entries := make([]noteEntry, 0, len(page))
	for _, note := range page {
		// Name only. The summary also carries Preview — a body excerpt — which
		// is dropped here rather than passed through to the frame.
		entries = append(entries, noteEntry{ID: note.ID, Name: clip(note.Name)})
	}
	return notesResponse{
		Notes: entries, Total: len(notes), Limit: limit, Offset: offset,
		HasMore: offset+len(entries) < len(notes),
	}, nil
}

type agentEntry struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Role           string `json:"role,omitempty"`
	InstanceNumber int    `json:"instance_number,omitempty"`
	EntryPoint     bool   `json:"entry_point,omitempty"`
}

type agentsResponse struct {
	Agents  []agentEntry `json:"agents"`
	Total   int          `json:"total"`
	Limit   int          `json:"limit"`
	Offset  int          `json:"offset"`
	HasMore bool         `json:"has_more"`
}

func (r *Runtime) agents(workspaceID string, input listInput) (agentsResponse, error) {
	ws, err := r.workspace(workspaceID)
	if err != nil {
		return agentsResponse{}, err
	}
	limit, offset := input.bounds(len(ws.AgentInstances))
	page := ws.AgentInstances[offset:min(offset+limit, len(ws.AgentInstances))]
	entries := make([]agentEntry, 0, len(page))
	for _, agent := range page {
		// Identity and role only. CustomInstructions — the per-workspace prompt
		// refinement — and Description are not gathered (FR18).
		entries = append(entries, agentEntry{
			ID: agent.ID, Name: clip(agent.Name), Role: clip(agent.Role),
			InstanceNumber: agent.InstanceNumber, EntryPoint: agent.EntryPoint,
		})
	}
	return agentsResponse{
		Agents: entries, Total: len(ws.AgentInstances), Limit: limit, Offset: offset,
		HasMore: offset+len(entries) < len(ws.AgentInstances),
	}, nil
}

type sessionEntry struct {
	Title     string `json:"title"`
	AgentName string `json:"agent_name,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

type sessionsResponse struct {
	Sessions []sessionEntry `json:"sessions"`
	Total    int            `json:"total"`
	Limit    int            `json:"limit"`
	Offset   int            `json:"offset"`
	HasMore  bool           `json:"has_more"`
}

func (r *Runtime) sessionsList(ctx context.Context, workspaceID string, input listInput) (sessionsResponse, error) {
	if r.sessions == nil {
		return sessionsResponse{Sessions: []sessionEntry{}}, nil
	}
	limit, offset := input.bounds(0)
	// The store pages from the top, so ask for enough to cover the requested
	// window and slice it here.
	sessions, total, err := r.sessions.ListSessionsByWorkspace(ctx, workspaceID, offset+limit)
	if err != nil {
		return sessionsResponse{}, fmt.Errorf("workspace dashboard could not list sessions: %w", err)
	}
	if offset > len(sessions) {
		offset = len(sessions)
	}
	page := sessions[offset:min(offset+limit, len(sessions))]
	entries := make([]sessionEntry, 0, len(page))
	for _, item := range page {
		// Title, agent, and timestamp. Transcripts are never gathered.
		entry := sessionEntry{Title: clip(item.Title), AgentName: clip(item.AgentName)}
		if !item.UpdatedAt.IsZero() {
			entry.UpdatedAt = item.UpdatedAt.UTC().Format(time.RFC3339)
		}
		entries = append(entries, entry)
	}
	return sessionsResponse{
		Sessions: entries, Total: total, Limit: limit, Offset: offset,
		HasMore: offset+len(entries) < total,
	}, nil
}

type fileEntry struct {
	Name       string `json:"name"`
	Size       int64  `json:"size"`
	IsDir      bool   `json:"is_dir,omitempty"`
	ModifiedAt string `json:"modified_at,omitempty"`
}

type filesResponse struct {
	Files   []fileEntry `json:"files"`
	Total   int         `json:"total"`
	Limit   int         `json:"limit"`
	Offset  int         `json:"offset"`
	HasMore bool        `json:"has_more"`
}

// files lists the workspace's files directory. Names, sizes, and timestamps
// only — no file is ever opened, so no file's contents can reach the frame.
func (r *Runtime) files(workspaceContext workspacesurface.WorkspaceContext, input listInput) (filesResponse, error) {
	root := strings.TrimSpace(workspaceContext.WorkspaceRoot)
	if root == "" || !filepath.IsAbs(root) {
		return filesResponse{Files: []fileEntry{}}, nil
	}
	// The path is the host-resolved workspace root joined with a package
	// constant. No browser-supplied string contributes a path segment.
	entries, err := os.ReadDir(filepath.Join(filepath.Clean(root), workspace.FilesDir))
	if err != nil {
		// A workspace with no files directory has no files, which is not a
		// failure worth blanking the dashboard over.
		return filesResponse{Files: []fileEntry{}}, nil
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	limit, offset := input.bounds(len(entries))
	page := entries[offset:min(offset+limit, len(entries))]
	files := make([]fileEntry, 0, len(page))
	for _, item := range page {
		entry := fileEntry{Name: clip(item.Name()), IsDir: item.IsDir()}
		if info, err := item.Info(); err == nil {
			entry.Size = info.Size()
			entry.ModifiedAt = info.ModTime().UTC().Format(time.RFC3339)
		}
		files = append(files, entry)
	}
	return filesResponse{
		Files: files, Total: len(entries), Limit: limit, Offset: offset,
		HasMore: offset+len(files) < len(entries),
	}, nil
}

// ---------------------------------------------------------------------------

func (r *Runtime) workspace(workspaceID string) (*workspace.Workspace, error) {
	if r == nil || r.workspaces == nil {
		return nil, errors.New("workspace dashboard data is unavailable")
	}
	ws, err := r.workspaces.Get(workspaceID)
	if err != nil || ws == nil {
		return nil, fmt.Errorf("workspace dashboard could not read its workspace: %w", err)
	}
	return ws, nil
}

// isOpenTask treats anything not finished as open. Statuses are compared
// lowercase so a stored variant cannot slip past.
func isOpenTask(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed", "complete", "done", "failed", "cancelled", "canceled", "timeout":
		return false
	default:
		return true
	}
}

func sharedString(shared map[string]any, key string) string {
	if shared == nil {
		return ""
	}
	value, _ := shared[key].(string)
	return value
}

// clip bounds one free-text field so a single pathological record cannot
// consume the whole response budget, truncating on a rune boundary.
func clip(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= maxTextBytes {
		return value
	}
	clipped := value[:maxTextBytes]
	for len(clipped) > 0 && !utf8.ValidString(clipped) {
		clipped = clipped[:len(clipped)-1]
	}
	return clipped
}

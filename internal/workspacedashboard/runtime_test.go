package workspacedashboard

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacesurface"
)

type stubWorkspaces map[string]*workspace.Workspace

func (s stubWorkspaces) Get(workspaceID string) (*workspace.Workspace, error) {
	ws, ok := s[workspaceID]
	if !ok {
		return nil, errors.New("workspace not found")
	}
	return ws, nil
}

type stubNotes map[string][]workspace.TaskPromptNoteSummary

func (s stubNotes) ListNotesByWorkspace(_ context.Context, workspaceID string) ([]workspace.TaskPromptNoteSummary, error) {
	return s[workspaceID], nil
}

type stubSessions struct {
	byWorkspace map[string][]workspace.TaskPromptSessionSummary
	lastLimit   int
}

func (s *stubSessions) ListSessionsByWorkspace(_ context.Context, workspaceID string, limit int) ([]workspace.TaskPromptSessionSummary, int, error) {
	s.lastLimit = limit
	all := s.byWorkspace[workspaceID]
	if limit > 0 && limit < len(all) {
		return all[:limit], len(all), nil
	}
	return all, len(all), nil
}

func testWorkspace() *workspace.Workspace {
	return &workspace.Workspace{
		ID: "ws-1", Name: "Marketing Site", Kind: "workspace",
		Description: "The public site", Tags: []string{"web", "public"},
		SharedData: map[string]any{"designation": "outpost"},
		AgentInstances: []workspace.AgentInstance{
			{
				ID: "agent-1", Name: "writer", Role: "Copywriter", InstanceNumber: 1,
				EntryPoint: true,
				// Never exposed (FR18).
				CustomInstructions: "SECRET-PROMPT never reveal the launch date",
				Description:        "SECRET-DESCRIPTION",
			},
			{ID: "agent-2", Name: "reviewer", Role: "Reviewer", InstanceNumber: 1},
		},
		Tasks: []workspace.Task{
			{
				ID: "task-1", Description: "Ship the landing page", Status: "pending",
				Priority: 2, To: "writer",
				// None of these may reach the frame (FR18).
				Details:          "SECRET-DETAILS api key sk-live-1234",
				Result:           "SECRET-RESULT",
				Error:            "SECRET-ERROR",
				Context:          map[string]any{"token": "SECRET-CONTEXT"},
				StructuredResult: map[string]any{"password": "SECRET-STRUCTURED"},
			},
			{ID: "task-2", Description: "Fix the footer", Status: "completed", Priority: 1},
			{ID: "task-3", Description: "Audit copy", Status: "in_progress", Priority: 3},
		},
	}
}

func testRuntime(t *testing.T) (*Runtime, *stubSessions) {
	t.Helper()
	sessions := &stubSessions{byWorkspace: map[string][]workspace.TaskPromptSessionSummary{
		"ws-1": {
			{Title: "Kickoff", AgentName: "writer", UpdatedAt: time.Unix(1_700_000_000, 0)},
			{Title: "Review", AgentName: "reviewer", UpdatedAt: time.Unix(1_700_000_100, 0)},
		},
	}}
	notes := stubNotes{"ws-1": {
		{ID: "note-1", Name: "Brand voice", Preview: "SECRET-PREVIEW body excerpt"},
		{ID: "note-2", Name: "Launch plan", Preview: "SECRET-PREVIEW body excerpt"},
	}}
	return NewRuntime(stubWorkspaces{"ws-1": testWorkspace()}, notes, sessions), sessions
}

func invoke(t *testing.T, runtime *Runtime, operation string, input string, target any) {
	t.Helper()
	raw := json.RawMessage(input)
	if input == "" {
		raw = nil
	}
	result, err := runtime.Invoke(t.Context(), workspacesurface.Invocation{
		Workspace: workspacesurface.WorkspaceContext{WorkspaceID: "ws-1"},
		Operation: operation, Input: raw,
	})
	if err != nil {
		t.Fatalf("Invoke(%s) error = %v", operation, err)
	}
	if len(result.Output) > maxOutputBytes {
		t.Fatalf("Invoke(%s) returned %d bytes, over the %d cap", operation, len(result.Output), maxOutputBytes)
	}
	if err := json.Unmarshal(result.Output, target); err != nil {
		t.Fatalf("Invoke(%s) output is not valid JSON for the target: %v", operation, err)
	}
}

func TestSummaryReportsIdentityAndCounts(t *testing.T) {
	runtime, _ := testRuntime(t)
	var summary summaryResponse
	invoke(t, runtime, OpWorkspaceSummary, "{}", &summary)

	if summary.ID != "ws-1" || summary.Name != "Marketing Site" || summary.Kind != "workspace" {
		t.Fatalf("summary = %+v", summary)
	}
	if summary.Designation != "outpost" {
		t.Fatalf("designation = %q", summary.Designation)
	}
	want := map[string]int{"tasks": 3, "open_tasks": 2, "agents": 2, "notes": 2, "sessions": 2}
	for key, value := range want {
		if summary.Counts[key] != value {
			t.Fatalf("counts[%s] = %d, want %d (counts=%v)", key, summary.Counts[key], value, summary.Counts)
		}
	}
}

func TestTasksListFiltersPaginatesAndOmitsFreeText(t *testing.T) {
	runtime, _ := testRuntime(t)

	var all tasksResponse
	invoke(t, runtime, OpTasksList, "{}", &all)
	if all.Total != 3 || len(all.Tasks) != 3 || all.HasMore {
		t.Fatalf("unfiltered tasks = %+v", all)
	}

	var open tasksResponse
	invoke(t, runtime, OpTasksList, `{"status":"open"}`, &open)
	if open.Total != 2 {
		t.Fatalf("open tasks = %+v", open)
	}
	for _, task := range open.Tasks {
		if task.Status == "completed" {
			t.Fatalf("a completed task matched status=open: %+v", task)
		}
	}

	var exact tasksResponse
	invoke(t, runtime, OpTasksList, `{"status":"completed"}`, &exact)
	if exact.Total != 1 || exact.Tasks[0].ID != "task-2" {
		t.Fatalf("completed tasks = %+v", exact)
	}

	var page tasksResponse
	invoke(t, runtime, OpTasksList, `{"limit":1,"offset":1}`, &page)
	if len(page.Tasks) != 1 || page.Total != 3 || page.Offset != 1 || !page.HasMore {
		t.Fatalf("paged tasks = %+v", page)
	}
	if page.Tasks[0].ID != "task-2" {
		t.Fatalf("offset did not advance: %+v", page.Tasks[0])
	}
}

func TestAgentsListOmitsPromptText(t *testing.T) {
	runtime, _ := testRuntime(t)
	var agents agentsResponse
	invoke(t, runtime, OpAgentsList, "{}", &agents)

	if agents.Total != 2 || len(agents.Agents) != 2 {
		t.Fatalf("agents = %+v", agents)
	}
	if agents.Agents[0].Name != "writer" || agents.Agents[0].Role != "Copywriter" || !agents.Agents[0].EntryPoint {
		t.Fatalf("agent = %+v", agents.Agents[0])
	}
}

func TestNotesAndSessionsReturnTitlesOnly(t *testing.T) {
	runtime, sessions := testRuntime(t)

	var notes notesResponse
	invoke(t, runtime, OpNotesList, "{}", &notes)
	if notes.Total != 2 || notes.Notes[0].Name != "Brand voice" {
		t.Fatalf("notes = %+v", notes)
	}

	var sessionList sessionsResponse
	invoke(t, runtime, OpSessionsList, `{"limit":1}`, &sessionList)
	if len(sessionList.Sessions) != 1 || sessionList.Total != 2 || !sessionList.HasMore {
		t.Fatalf("sessions = %+v", sessionList)
	}
	if sessionList.Sessions[0].Title != "Kickoff" || sessionList.Sessions[0].AgentName != "writer" {
		t.Fatalf("session = %+v", sessionList.Sessions[0])
	}
	if sessionList.Sessions[0].UpdatedAt == "" {
		t.Fatal("session has no timestamp")
	}
	if sessions.lastLimit != 1 {
		t.Fatalf("session store was asked for %d rows, want the requested window", sessions.lastLimit)
	}
}

// FR18, as a single sweep: no response from any operation may contain a value
// that only exists in a secret-bearing field.
func TestNoOperationLeaksSecretBearingFields(t *testing.T) {
	runtime, _ := testRuntime(t)
	forbidden := []string{
		"SECRET-PROMPT", "SECRET-DESCRIPTION", "SECRET-DETAILS", "SECRET-RESULT",
		"SECRET-ERROR", "SECRET-CONTEXT", "SECRET-STRUCTURED", "SECRET-PREVIEW",
		"sk-live-1234", "custom_instructions", "details", "preview",
	}
	for _, operation := range operationIDs() {
		result, err := runtime.Invoke(t.Context(), workspacesurface.Invocation{
			Workspace: workspacesurface.WorkspaceContext{WorkspaceID: "ws-1"},
			Operation: operation, Input: json.RawMessage(`{}`),
		})
		if err != nil {
			t.Fatalf("Invoke(%s) error = %v", operation, err)
		}
		body := string(result.Output)
		for _, needle := range forbidden {
			if strings.Contains(body, needle) {
				t.Fatalf("%s leaked %q: %s", operation, needle, body)
			}
		}
	}
}

// FR17: the workspace comes from the trusted context. Input cannot select one.
func TestOperationInputCannotChooseTheWorkspace(t *testing.T) {
	other := testWorkspace()
	other.ID = "ws-2"
	other.Name = "Someone Else's Workspace"
	runtime := NewRuntime(stubWorkspaces{"ws-1": testWorkspace(), "ws-2": other}, nil, nil)

	// A forged workspace id in the input is ignored: the schema rejects unknown
	// fields at the broker, and the runtime never reads one regardless.
	result, err := runtime.Invoke(t.Context(), workspacesurface.Invocation{
		Workspace: workspacesurface.WorkspaceContext{WorkspaceID: "ws-1"},
		Operation: OpWorkspaceSummary,
		Input:     json.RawMessage(`{"workspace_id":"ws-2","workspace":"ws-2","id":"ws-2"}`),
	})
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	var summary summaryResponse
	if err := json.Unmarshal(result.Output, &summary); err != nil {
		t.Fatal(err)
	}
	if summary.ID != "ws-1" || strings.Contains(summary.Name, "Someone Else") {
		t.Fatalf("a forged workspace id was honored: %+v", summary)
	}
}

func TestInvokeRequiresATrustedWorkspaceContext(t *testing.T) {
	runtime, _ := testRuntime(t)
	if _, err := runtime.Invoke(t.Context(), workspacesurface.Invocation{
		Operation: OpWorkspaceSummary, Input: json.RawMessage(`{}`),
	}); err == nil {
		t.Fatal("an operation ran with no workspace context")
	}
}

// FR20: an unknown id is rejected outright — no fallthrough, no partial data.
func TestInvokeRejectsUnknownOperations(t *testing.T) {
	runtime, _ := testRuntime(t)
	for _, operation := range []string{"vault.read", "workspace.summary.extra", "", "WORKSPACE.SUMMARY"} {
		result, err := runtime.Invoke(t.Context(), workspacesurface.Invocation{
			Workspace: workspacesurface.WorkspaceContext{WorkspaceID: "ws-1"},
			Operation: operation, Input: json.RawMessage(`{}`),
		})
		if err == nil {
			t.Fatalf("the runtime answered undeclared operation %q", operation)
		}
		if !errors.Is(err, ErrOperationUnknown) {
			t.Fatalf("operation %q error = %v, want ErrOperationUnknown", operation, err)
		}
		if len(result.Output) != 0 {
			t.Fatalf("operation %q returned partial data: %s", operation, result.Output)
		}
	}
}

// The bridge silently drops a message over 64 KiB, so an unbounded list would
// not fail loudly — it would simply never arrive.
func TestListsStayWithinTheBridgeBudget(t *testing.T) {
	big := &workspace.Workspace{ID: "ws-1", Name: "Huge"}
	longTitle := strings.Repeat("very long task title ", 200)
	for i := 0; i < 5000; i++ {
		big.Tasks = append(big.Tasks, workspace.Task{
			ID: "task-" + strconv.Itoa(i), Description: longTitle, Status: "pending",
		})
	}
	runtime := NewRuntime(stubWorkspaces{"ws-1": big}, nil, nil)

	result, err := runtime.Invoke(t.Context(), workspacesurface.Invocation{
		Workspace: workspacesurface.WorkspaceContext{WorkspaceID: "ws-1"},
		Operation: OpTasksList, Input: json.RawMessage(`{"limit":100}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Output) > maxOutputBytes {
		t.Fatalf("response is %d bytes, over the %d budget", len(result.Output), maxOutputBytes)
	}
	var tasks tasksResponse
	if err := json.Unmarshal(result.Output, &tasks); err != nil {
		t.Fatal(err)
	}
	// The page shrinks below the requested limit to fit the budget — that is the
	// intended behavior, not a failure. What must hold is that some entries came
	// back and has_more is set, so the dashboard can walk the rest.
	if len(tasks.Tasks) == 0 || len(tasks.Tasks) > maxListLimit {
		t.Fatalf("returned %d entries", len(tasks.Tasks))
	}
	if tasks.Total != 5000 || !tasks.HasMore {
		t.Fatalf("paging = total %d, has_more %v", tasks.Total, tasks.HasMore)
	}
	for _, task := range tasks.Tasks {
		if len(task.Title) > maxTextBytes {
			t.Fatalf("a title survived at %d bytes", len(task.Title))
		}
	}
}

// An over-large limit is clamped rather than honored.
func TestListLimitIsClamped(t *testing.T) {
	runtime, _ := testRuntime(t)
	var tasks tasksResponse
	invoke(t, runtime, OpTasksList, `{"limit":100000,"offset":-5}`, &tasks)
	if tasks.Limit != maxListLimit || tasks.Offset != 0 {
		t.Fatalf("bounds = limit %d, offset %d", tasks.Limit, tasks.Offset)
	}
}

func TestFilesListReportsMetadataOnly(t *testing.T) {
	root := t.TempDir()
	filesDir := filepath.Join(root, workspace.FilesDir)
	if err := os.MkdirAll(filesDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filesDir, "brief.md"), []byte("SECRET-FILE-BODY"), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime(stubWorkspaces{"ws-1": testWorkspace()}, nil, nil)

	result, err := runtime.Invoke(t.Context(), workspacesurface.Invocation{
		Workspace: workspacesurface.WorkspaceContext{WorkspaceID: "ws-1", WorkspaceRoot: root},
		Operation: OpFilesList, Input: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(result.Output), "SECRET-FILE-BODY") {
		t.Fatalf("file contents reached the response: %s", result.Output)
	}
	var files filesResponse
	if err := json.Unmarshal(result.Output, &files); err != nil {
		t.Fatal(err)
	}
	if files.Total != 1 || files.Files[0].Name != "brief.md" || files.Files[0].Size == 0 {
		t.Fatalf("files = %+v", files)
	}
}

// A workspace with no files directory is not a failure; the dashboard's other
// panels must keep working.
func TestFilesListToleratesAMissingDirectory(t *testing.T) {
	runtime := NewRuntime(stubWorkspaces{"ws-1": testWorkspace()}, nil, nil)
	var files filesResponse
	result, err := runtime.Invoke(t.Context(), workspacesurface.Invocation{
		Workspace: workspacesurface.WorkspaceContext{WorkspaceID: "ws-1", WorkspaceRoot: t.TempDir()},
		Operation: OpFilesList, Input: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(result.Output, &files); err != nil {
		t.Fatal(err)
	}
	if len(files.Files) != 0 {
		t.Fatalf("files = %+v", files)
	}
}

// Operations whose store is unavailable report empty rather than failing, so
// one missing store cannot blank a dashboard that reads several things.
func TestOperationsDegradeIndependently(t *testing.T) {
	runtime := NewRuntime(stubWorkspaces{"ws-1": testWorkspace()}, nil, nil)
	var notes notesResponse
	invoke(t, runtime, OpNotesList, "{}", &notes)
	if len(notes.Notes) != 0 {
		t.Fatalf("notes = %+v", notes)
	}
	var summary summaryResponse
	invoke(t, runtime, OpWorkspaceSummary, "{}", &summary)
	if summary.Counts["tasks"] != 3 {
		t.Fatalf("summary lost task counts when the note store was absent: %+v", summary)
	}
}

// Every declared operation must validate against its own schemas through the
// same helpers the broker uses, or the broker would reject its own runtime.
func TestDeclaredOperationsValidateTheirOwnOutput(t *testing.T) {
	runtime, _ := testRuntime(t)
	declared := operations()
	for _, id := range operationIDs() {
		operation := declared[id]
		if err := workspacesurface.ValidateOperationInput(operation, json.RawMessage(`{}`)); err != nil {
			t.Fatalf("operation %q rejects an empty input: %v", id, err)
		}
		result, err := runtime.Invoke(t.Context(), workspacesurface.Invocation{
			Workspace: workspacesurface.WorkspaceContext{WorkspaceID: "ws-1"},
			Operation: id, Input: json.RawMessage(`{}`),
		})
		if err != nil {
			t.Fatalf("Invoke(%s) error = %v", id, err)
		}
		if err := workspacesurface.ValidateOperationOutput(operation, result.Output); err != nil {
			t.Fatalf("operation %q output fails its own schema: %v\n%s", id, err, result.Output)
		}
	}
}

package orchestrationhttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestUpcomingScheduledTasksHandler_Empty(t *testing.T) {
	store := workspace.NewInMemoryStore()
	handler := &TaskHandler{workspaceStore: store}

	req := httptest.NewRequest(http.MethodGet, "/api/orchestration/scheduled-tasks/upcoming", nil)
	rec := httptest.NewRecorder()
	handler.UpcomingScheduledTasksHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Upcoming []map[string]any `json:"upcoming"`
		Count    int              `json:"count"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Count != 0 || len(resp.Upcoming) != 0 {
		t.Fatalf("expected empty result, got count=%d items=%d", resp.Count, len(resp.Upcoming))
	}
}

func TestUpcomingScheduledTasksHandler_AggregatesAndSorts(t *testing.T) {
	store := workspace.NewInMemoryStore()

	now := time.Now().UTC()
	ws1 := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Workspace One"})
	ws1.ID = "ws-1"
	ws1.ScheduledTasks = []workspace.ScheduledTask{
		// later than ws2's task — should rank second
		{ID: "st-1a", Name: "Task A1", To: "Agent-A", NextRun: ptrTime(now.Add(2 * time.Hour)), Enabled: true},
		// disabled — must be excluded
		{ID: "st-1b", Name: "Disabled", To: "Agent-A", NextRun: ptrTime(now.Add(30 * time.Minute)), Enabled: false},
		// nil NextRun — must be excluded
		{ID: "st-1c", Name: "Pending", To: "Agent-A", NextRun: nil, Enabled: true},
	}
	if err := store.Save(ws1); err != nil {
		t.Fatalf("save ws1: %v", err)
	}

	ws2 := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Workspace Two"})
	ws2.ID = "ws-2"
	ws2.ScheduledTasks = []workspace.ScheduledTask{
		// earliest — should rank first
		{ID: "st-2a", Name: "Task B1", To: "Agent-B", NextRun: ptrTime(now.Add(1 * time.Hour)), Enabled: true},
		// latest — should rank third (before limit truncates)
		{ID: "st-2b", Name: "Task B2", To: "Agent-B", NextRun: ptrTime(now.Add(3 * time.Hour)), Enabled: true},
	}
	if err := store.Save(ws2); err != nil {
		t.Fatalf("save ws2: %v", err)
	}

	handler := &TaskHandler{workspaceStore: store}
	req := httptest.NewRequest(http.MethodGet, "/api/orchestration/scheduled-tasks/upcoming", nil)
	rec := httptest.NewRecorder()
	handler.UpcomingScheduledTasksHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Upcoming []struct {
			TaskID        string    `json:"task_id"`
			TaskName      string    `json:"task_name"`
			WorkspaceID   string    `json:"workspace_id"`
			WorkspaceName string    `json:"workspace_name"`
			AgentName     string    `json:"agent_name"`
			NextRun       time.Time `json:"next_run"`
		} `json:"upcoming"`
		Count int `json:"count"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if resp.Count != 3 {
		t.Fatalf("expected 3 upcoming (disabled + nil filtered out), got %d", resp.Count)
	}
	if got := []string{resp.Upcoming[0].TaskID, resp.Upcoming[1].TaskID, resp.Upcoming[2].TaskID}; !equalStrings(got, []string{"st-2a", "st-1a", "st-2b"}) {
		t.Fatalf("unexpected sort order: %v", got)
	}
	if resp.Upcoming[0].WorkspaceName != "Workspace Two" || resp.Upcoming[1].WorkspaceName != "Workspace One" {
		t.Fatalf("workspace name not joined into rows: %#v", resp.Upcoming)
	}
	if resp.Upcoming[0].AgentName != "Agent-B" {
		t.Fatalf("agent name not joined: got %q", resp.Upcoming[0].AgentName)
	}
}

func TestUpcomingScheduledTasksHandler_LimitHonored(t *testing.T) {
	store := workspace.NewInMemoryStore()
	now := time.Now().UTC()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "WS"})
	ws.ID = "ws-limit"
	for i := 0; i < 10; i++ {
		ws.ScheduledTasks = append(ws.ScheduledTasks, workspace.ScheduledTask{
			ID:      "st-" + strconvI(i),
			Name:    "Task " + strconvI(i),
			To:      "Agent",
			NextRun: ptrTime(now.Add(time.Duration(i) * time.Minute)),
			Enabled: true,
		})
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("save: %v", err)
	}

	handler := &TaskHandler{workspaceStore: store}
	req := httptest.NewRequest(http.MethodGet, "/api/orchestration/scheduled-tasks/upcoming?limit=3", nil)
	rec := httptest.NewRecorder()
	handler.UpcomingScheduledTasksHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp struct {
		Count int `json:"count"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Count != 3 {
		t.Fatalf("expected count=3 honoring limit, got %d", resp.Count)
	}
}

func TestUpcomingScheduledTasksHandler_MethodNotAllowed(t *testing.T) {
	store := workspace.NewInMemoryStore()
	handler := &TaskHandler{workspaceStore: store}

	req := httptest.NewRequest(http.MethodPost, "/api/orchestration/scheduled-tasks/upcoming", nil)
	rec := httptest.NewRecorder()
	handler.UpcomingScheduledTasksHandler(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}

// --- helpers ---

func ptrTime(t time.Time) *time.Time { return &t }

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func strconvI(i int) string {
	const digits = "0123456789"
	if i == 0 {
		return "0"
	}
	out := ""
	for i > 0 {
		out = string(digits[i%10]) + out
		i /= 10
	}
	return out
}

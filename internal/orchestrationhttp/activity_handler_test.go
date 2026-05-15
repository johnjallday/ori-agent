package orchestrationhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// stubSessionStore implements the orchestrationhttp.SessionStore interface
// for tests. Returns the notes/sessions configured per workspace.
type stubSessionStore struct {
	notesByWorkspace map[string][]WorkspaceNoteItem
}

func (s *stubSessionStore) ListSessionsByWorkspace(ctx context.Context, workspaceID string) ([]SessionListItem, error) {
	return nil, nil
}

func (s *stubSessionStore) ListNotesByWorkspace(ctx context.Context, workspaceID string) ([]WorkspaceNoteItem, error) {
	return s.notesByWorkspace[workspaceID], nil
}

func TestRecentActivityHandler_Empty(t *testing.T) {
	store := workspace.NewInMemoryStore()
	h := &Handler{
		workspaceStore: store,
		sessionStore:   &stubSessionStore{},
		eventBus:       workspace.NewEventBus(10, 10),
	}

	req := httptest.NewRequest(http.MethodGet, "/api/activity/recent", nil)
	rec := httptest.NewRecorder()
	h.RecentActivityHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Events []activityRow `json:"events"`
		Count  int           `json:"count"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Count != 0 || len(resp.Events) != 0 {
		t.Fatalf("expected empty, got count=%d items=%d", resp.Count, len(resp.Events))
	}
}

func TestRecentActivityHandler_AggregatesSources(t *testing.T) {
	store := workspace.NewInMemoryStore()
	now := time.Now().UTC()

	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "WS"})
	ws.ID = "ws-1"

	// A completed task (timestamp = oldest)
	completedAt := now.Add(-3 * time.Hour)
	ws.Tasks = []workspace.Task{
		{ID: "t-1", Description: "Implement feature X", CompletedAt: &completedAt},
		// An incomplete task — must be excluded
		{ID: "t-2", Description: "Pending work", CompletedAt: nil},
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("save: %v", err)
	}

	// A note update (timestamp = middle)
	noteUpdated := now.Add(-2 * time.Hour)
	sessionStore := &stubSessionStore{
		notesByWorkspace: map[string][]WorkspaceNoteItem{
			"ws-1": {{ID: "n-1", Name: "Brand Kit", UpdatedAt: noteUpdated}},
		},
	}

	// A scheduled-task fire event (timestamp = most recent)
	bus := workspace.NewEventBus(10, 10)
	bus.Publish(workspace.Event{
		Type:        workspace.EventScheduledTaskTriggered,
		WorkspaceID: "ws-1",
		Timestamp:   now.Add(-1 * time.Hour),
		Data:        map[string]interface{}{"task_name": "Monday digest"},
	})

	h := &Handler{workspaceStore: store, sessionStore: sessionStore, eventBus: bus}

	req := httptest.NewRequest(http.MethodGet, "/api/activity/recent?limit=20", nil)
	rec := httptest.NewRecorder()
	h.RecentActivityHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Events []activityRow `json:"events"`
		Count  int           `json:"count"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Count != 3 {
		t.Fatalf("expected 3 events (task + note + fire), got %d: %#v", resp.Count, resp.Events)
	}
	// Sorted timestamp desc → fire first, then note, then task
	wantKinds := []string{
		ActivityKindScheduledFired,
		ActivityKindNoteEdited,
		ActivityKindTaskCompleted,
	}
	for i, want := range wantKinds {
		if resp.Events[i].Kind != want {
			t.Fatalf("event[%d] kind = %q; want %q", i, resp.Events[i].Kind, want)
		}
	}
	if resp.Events[0].Description != "Scheduled task fired: Monday digest" {
		t.Fatalf("fire description missing task_name: %q", resp.Events[0].Description)
	}
	if resp.Events[1].Description != "Note edited: Brand Kit" {
		t.Fatalf("note description: %q", resp.Events[1].Description)
	}
	if resp.Events[2].Description != "Task completed: Implement feature X" {
		t.Fatalf("task description: %q", resp.Events[2].Description)
	}
	// Workspace name propagated
	for _, ev := range resp.Events {
		if ev.WorkspaceName != "WS" {
			t.Fatalf("workspace name not set on %s event", ev.Kind)
		}
	}
}

func TestRecentActivityHandler_LimitHonored(t *testing.T) {
	store := workspace.NewInMemoryStore()
	now := time.Now().UTC()

	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "WS"})
	ws.ID = "ws-1"
	for i := 0; i < 10; i++ {
		ts := now.Add(-time.Duration(i) * time.Hour)
		ws.Tasks = append(ws.Tasks, workspace.Task{
			ID:          "t-" + strconvI(i),
			Description: "Task " + strconvI(i),
			CompletedAt: &ts,
		})
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("save: %v", err)
	}

	h := &Handler{
		workspaceStore: store,
		sessionStore:   &stubSessionStore{},
		eventBus:       workspace.NewEventBus(10, 10),
	}

	req := httptest.NewRequest(http.MethodGet, "/api/activity/recent?limit=4", nil)
	rec := httptest.NewRecorder()
	h.RecentActivityHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp struct {
		Count int `json:"count"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Count != 4 {
		t.Fatalf("expected count=4 (limit honored), got %d", resp.Count)
	}
}

func TestRecentActivityHandler_MethodNotAllowed(t *testing.T) {
	h := &Handler{
		workspaceStore: workspace.NewInMemoryStore(),
		sessionStore:   &stubSessionStore{},
		eventBus:       workspace.NewEventBus(10, 10),
	}
	req := httptest.NewRequest(http.MethodPost, "/api/activity/recent", nil)
	rec := httptest.NewRecorder()
	h.RecentActivityHandler(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}

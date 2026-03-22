package chathttp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type mockWorkspaceSnapshotSessionStore struct {
	notes       []session.WorkspaceNoteListItem
	notesErr    error
	sessions    []session.SessionListItem
	sessionsErr error
	lastFilter  *session.SessionFilter
	lastOpts    *session.ListOptions
}

func (m *mockWorkspaceSnapshotSessionStore) ListNotesByWorkspace(_ context.Context, _ string) ([]session.WorkspaceNoteListItem, error) {
	if m.notesErr != nil {
		return nil, m.notesErr
	}
	return append([]session.WorkspaceNoteListItem(nil), m.notes...), nil
}

func (m *mockWorkspaceSnapshotSessionStore) ListSessions(_ context.Context, filter *session.SessionFilter, opts *session.ListOptions) (*session.ListResult, error) {
	m.lastFilter = filter
	m.lastOpts = opts
	if m.sessionsErr != nil {
		return nil, m.sessionsErr
	}
	return &session.ListResult{
		Sessions: append([]session.SessionListItem(nil), m.sessions...),
		Total:    len(m.sessions),
	}, nil
}

type errWorkspaceSnapshotStore struct {
	err error
}

func (s errWorkspaceSnapshotStore) Get(string) (*workspace.Workspace, error) {
	return nil, s.err
}

func TestBuildWorkspaceSnapshotPrompt_PopulatedWorkspace(t *testing.T) {
	wsStore := workspace.NewInMemoryStore()
	now := time.Date(2026, time.March, 8, 10, 0, 0, 0, time.UTC)
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{
		Name:        "Alpha Workspace",
		Description: "Workspace for release planning and execution",
		Agents:      []string{"Ori", "Reviewer"},
	})
	ws.Status = workspace.StatusActive
	ws.UpdatedAt = now
	ws.Tasks = []workspace.Task{
		{Description: "Prepare release brief", To: "Ori", Priority: 1, Status: workspace.TaskStatusPending},
		{Description: "Review launch risks", To: "Reviewer", Priority: 2, Status: workspace.TaskStatusInProgress},
		{Description: "Archive old notes", To: "Ori", Priority: 3, Status: workspace.TaskStatusCompleted},
	}
	ws.Attachments = []workspace.Attachment{
		{
			Title: "Internal note",
			Body:  "super secret file body",
			Type:  workspace.AttachmentTypeDoc,
		},
		{
			Title: "Spec",
			Type:  workspace.AttachmentTypeDoc,
			File: &workspace.AttachmentFileMeta{
				Name: "spec.md",
				Mime: "text/markdown",
			},
		},
	}
	ws.DirectoryReferences = []workspace.DirectoryReference{
		{Name: "repo", Path: "/tmp/repo"},
	}
	if err := wsStore.Save(ws); err != nil {
		t.Fatalf("failed to save workspace: %v", err)
	}

	sessionStore := &mockWorkspaceSnapshotSessionStore{
		notes: []session.WorkspaceNoteListItem{
			{Name: "Launch Plan", Preview: "Launch checklist and milestones"},
		},
		sessions: []session.SessionListItem{
			{Title: "Sprint sync", AgentName: "Ori", UpdatedAt: now},
		},
	}

	prompt := buildWorkspaceSnapshotPrompt(context.Background(), normalizedChatRouteContext{
		Surface:     "workspace_detail",
		PagePath:    "/workspaces/" + ws.ID,
		WorkspaceID: ws.ID,
	}, wsStore, sessionStore)

	for _, want := range []string{
		"# Workspace Snapshot",
		`- Name: "Alpha Workspace"`,
		`- Description: "Workspace for release planning and execution"`,
		`- Agents (2): Ori, Reviewer`,
		"Counts: total=3, pending=1, in_progress=1, completed=1",
		`Open task: [pending] "Prepare release brief" -> "Ori"`,
		`Note: "Launch Plan" - "Launch checklist and milestones"`,
		`File: "spec.md" (type="doc", mime="text/markdown")`,
		`Directory: "repo" path="/tmp/repo"`,
		`Session: "Sprint sync" agent="Ori" updated_at="2026-03-08T10:00:00Z"`,
		"Treat this snapshot as current workspace state.",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected prompt to contain %q, got %q", want, prompt)
		}
	}

	if strings.Contains(prompt, "super secret file body") {
		t.Fatalf("expected file body to be excluded from snapshot, got %q", prompt)
	}
	if sessionStore.lastFilter == nil || sessionStore.lastFilter.FolderID == nil || *sessionStore.lastFilter.FolderID != ws.ID {
		t.Fatalf("expected session filter to target workspace %q", ws.ID)
	}
	if sessionStore.lastOpts == nil || sessionStore.lastOpts.Sort != session.SortByUpdatedDesc {
		t.Fatalf("expected session list to use updated-desc sort")
	}
}

func TestBuildWorkspaceSnapshotPrompt_EmptyWorkspace(t *testing.T) {
	wsStore := workspace.NewInMemoryStore()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Empty"})
	if err := wsStore.Save(ws); err != nil {
		t.Fatalf("failed to save workspace: %v", err)
	}

	prompt := buildWorkspaceSnapshotPrompt(context.Background(), normalizedChatRouteContext{
		Surface:     "workspace_detail",
		PagePath:    "/workspaces/" + ws.ID,
		WorkspaceID: ws.ID,
	}, wsStore, nil)

	if !strings.Contains(prompt, "Counts: total=0") {
		t.Fatalf("expected empty task counts, got %q", prompt)
	}
	if strings.Contains(prompt, "Notes (") || strings.Contains(prompt, "Files (") || strings.Contains(prompt, "Recent sessions (") {
		t.Fatalf("expected empty sections to be omitted, got %q", prompt)
	}
}

func TestBuildWorkspaceSnapshotPrompt_NonToolProviderOmitsToolCallInstructions(t *testing.T) {
	wsStore := workspace.NewInMemoryStore()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Codex Workspace"})
	if err := wsStore.Save(ws); err != nil {
		t.Fatalf("failed to save workspace: %v", err)
	}

	prompt := buildWorkspaceSnapshotPromptForToolCapability(context.Background(), normalizedChatRouteContext{
		Surface:     "workspace_detail",
		PagePath:    "/workspaces/" + ws.ID,
		WorkspaceID: ws.ID,
	}, wsStore, nil, false)

	if !strings.Contains(prompt, "This route includes workspace snapshot context only.") {
		t.Fatalf("expected non-tool provider warning, got %q", prompt)
	}
	if strings.Contains(prompt, "workspace_save_note") {
		t.Fatalf("expected tool list to be omitted for non-tool providers, got %q", prompt)
	}
	if strings.Contains(prompt, "tool_call") {
		t.Fatalf("expected tool-call instructions to be omitted for non-tool providers, got %q", prompt)
	}
}

func TestBuildWorkspaceRuntimeSystemPrompt_RouteOnlyWhenWorkspaceMissing(t *testing.T) {
	routeCtx := normalizedChatRouteContext{
		Surface:     "workspace_detail",
		PagePath:    "/workspaces/ws-missing",
		WorkspaceID: "ws-missing",
		Origin:      "ask_ori",
	}

	got := buildWorkspaceRuntimeSystemPrompt(context.Background(), routeCtx, errWorkspaceSnapshotStore{err: errors.New("missing")}, nil)
	want := buildRouteContextSystemPrompt(routeCtx)
	if got != want {
		t.Fatalf("expected route-only prompt when workspace is missing, got %q want %q", got, want)
	}
}

func TestBuildWorkspaceRuntimeSystemPrompt_RouteOnlyWithoutWorkspaceContext(t *testing.T) {
	routeCtx := normalizedChatRouteContext{
		Surface:  "dashboard",
		PagePath: "/dashboard",
		Origin:   "chat",
	}

	got := buildWorkspaceRuntimeSystemPrompt(context.Background(), routeCtx, nil, nil)
	want := buildRouteContextSystemPrompt(routeCtx)
	if got != want {
		t.Fatalf("expected route-only prompt, got %q want %q", got, want)
	}
}

func TestBuildWorkspaceSnapshotPrompt_TruncatesAndCapsItems(t *testing.T) {
	wsStore := workspace.NewInMemoryStore()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{
		Name:   strings.Repeat("A", workspaceSnapshotTextLimit+20),
		Agents: []string{"Ori"},
	})

	for i := 1; i <= 6; i++ {
		ws.Tasks = append(ws.Tasks, workspace.Task{
			Description: "Task " + string(rune('0'+i)),
			To:          "Ori",
			Priority:    i,
			Status:      workspace.TaskStatusPending,
		})
		ws.Attachments = append(ws.Attachments, workspace.Attachment{
			Title: "File " + string(rune('0'+i)),
			Type:  workspace.AttachmentTypeOther,
			File: &workspace.AttachmentFileMeta{
				Name: "file-" + string(rune('0'+i)) + ".txt",
				Mime: "text/plain",
			},
		})
		ws.DirectoryReferences = append(ws.DirectoryReferences, workspace.DirectoryReference{
			Name: "Dir " + string(rune('0'+i)),
			Path: "/tmp/dir-" + string(rune('0'+i)),
		})
	}

	if err := wsStore.Save(ws); err != nil {
		t.Fatalf("failed to save workspace: %v", err)
	}

	sessionStore := &mockWorkspaceSnapshotSessionStore{}
	for i := 1; i <= 6; i++ {
		sessionStore.notes = append(sessionStore.notes, session.WorkspaceNoteListItem{
			Name:    "Note " + string(rune('0'+i)),
			Preview: strings.Repeat("B", workspaceSnapshotPreviewLimit+10),
		})
		sessionStore.sessions = append(sessionStore.sessions, session.SessionListItem{
			Title:     "Session " + string(rune('0'+i)),
			AgentName: "Ori",
			UpdatedAt: time.Date(2026, time.March, i, 9, 0, 0, 0, time.UTC),
		})
	}

	prompt := buildWorkspaceSnapshotPrompt(context.Background(), normalizedChatRouteContext{
		Surface:     "workspace_canvas",
		PagePath:    "/workspaces/" + ws.ID + "/canvas",
		WorkspaceID: ws.ID,
	}, wsStore, sessionStore)

	if !strings.Contains(prompt, strings.Repeat("A", workspaceSnapshotTextLimit-3)+"...") {
		t.Fatalf("expected workspace name truncation, got %q", prompt)
	}
	for _, unexpected := range []string{"Task 6", "Note 6", "file-6.txt", `Directory: "Dir 6"`, `Session: "Session 6"`} {
		if strings.Contains(prompt, unexpected) {
			t.Fatalf("expected capped snapshot to omit %q, got %q", unexpected, prompt)
		}
	}
	if !strings.Contains(prompt, strings.Repeat("B", workspaceSnapshotPreviewLimit-3)+"...") {
		t.Fatalf("expected note preview truncation, got %q", prompt)
	}
}

func TestSetWorkspaceStore_SetsHandlerAndKeepsWorkspaceCommandsWorking(t *testing.T) {
	st := newPreflightStore("Ori", &agent.Agent{})
	h := NewHandler(st, nil)
	wsStore := workspace.NewInMemoryStore()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{
		Name:   "Command Workspace",
		Agents: []string{"Ori"},
	})
	if err := wsStore.Save(ws); err != nil {
		t.Fatalf("failed to save workspace: %v", err)
	}

	h.SetWorkspaceStore(wsStore)
	if h.workspaceStore != wsStore {
		t.Fatalf("expected handler workspace store to be set")
	}
	if h.commandHandler.workspaceStore != wsStore {
		t.Fatalf("expected command handler workspace store to be set")
	}

	runtimePrompt := h.buildRuntimeSystemPrompt(context.Background(), normalizedChatRouteContext{
		Surface:     "workspace_detail",
		PagePath:    "/workspaces/" + ws.ID,
		WorkspaceID: ws.ID,
	})
	if !strings.Contains(runtimePrompt, "Command Workspace") {
		t.Fatalf("expected runtime prompt to include workspace name, got %q", runtimePrompt)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/chat", nil)
	rec := httptest.NewRecorder()
	h.commandHandler.HandleWorkspace(rec, req, "")

	if body := rec.Body.String(); !strings.Contains(body, "Command Workspace") {
		t.Fatalf("expected /workspace command response to include workspace name, got %q", body)
	}
}

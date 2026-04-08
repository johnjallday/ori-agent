package workspace

import (
	"context"
	"strings"
	"testing"
	"time"
)

type mockTaskPromptContextStore struct {
	notes        []TaskPromptNoteSummary
	notesErr     error
	sessions     []TaskPromptSessionSummary
	sessionErr   error
	sessionCount int
}

func (m *mockTaskPromptContextStore) ListNotesByWorkspace(_ context.Context, _ string) ([]TaskPromptNoteSummary, error) {
	if m.notesErr != nil {
		return nil, m.notesErr
	}
	return append([]TaskPromptNoteSummary(nil), m.notes...), nil
}

func (m *mockTaskPromptContextStore) ListSessionsByWorkspace(_ context.Context, _ string, limit int) ([]TaskPromptSessionSummary, int, error) {
	if m.sessionErr != nil {
		return nil, 0, m.sessionErr
	}

	items := append([]TaskPromptSessionSummary(nil), m.sessions...)
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}

	count := m.sessionCount
	if count == 0 {
		count = len(m.sessions)
	}
	return items, count, nil
}

func TestBuildTaskPrompt_IncludesWorkspaceSnapshot(t *testing.T) {
	wsStore := NewInMemoryStore()
	now := time.Date(2026, time.March, 8, 10, 0, 0, 0, time.UTC)

	ws := NewWorkspace(CreateWorkspaceParams{
		Name:        "amr",
		Description: "test",
		Agents:      []string{"Ori"},
	})
	ws.ID = "workspace-1"
	ws.UpdatedAt = now
	ws.Tasks = []Task{
		{ID: "task-complete", Description: "check weather", To: "Ori", Priority: 2, Status: TaskStatusCompleted},
		{ID: "task-open", Description: "summarize workspace", To: "Ori", Priority: 1, Status: TaskStatusPending},
	}
	ws.Attachments = []Attachment{
		{
			ID:    "att-1",
			Title: "plan.md",
			Body:  "sensitive body text",
			Type:  AttachmentTypeDoc,
			File: &AttachmentFileMeta{
				Name: "plan.md",
				Mime: "text/markdown",
			},
		},
	}
	ws.DirectoryReferences = []DirectoryReference{
		{Name: "amr", Path: "/Users/jjdev/Projects/amr"},
	}
	if err := wsStore.Save(ws); err != nil {
		t.Fatalf("failed to save workspace: %v", err)
	}

	handler := &LLMTaskHandler{
		workspaceStore: wsStore,
		contextStore: &mockTaskPromptContextStore{
			notes: []TaskPromptNoteSummary{
				{Name: "Workspace note", Preview: "Summary of active work"},
			},
			sessions: []TaskPromptSessionSummary{
				{Title: "Daily sync", AgentName: "Ori", UpdatedAt: now},
			},
			sessionCount: 1,
		},
	}

	prompt := handler.buildTaskPrompt(context.Background(), Task{
		ID:          "task-open",
		WorkspaceID: ws.ID,
		From:        "jj",
		To:          "Ori",
		Description: "summarize workspace",
		Priority:    1,
	}, nil)

	for _, want := range []string{
		"## Workspace Snapshot",
		`- Workspace Name: "amr"`,
		`- Workspace Description: "test"`,
		`- Agents (1): Ori`,
		"Counts: total=2, pending=1, completed=1",
		`Open task: [pending] "summarize workspace" -> "Ori" (priority 1)`,
		`File: "plan.md" (type="doc", mime="text/markdown")`,
		`Directory: "amr" path="/Users/jjdev/Projects/amr"`,
		`Note: "Workspace note" - "Summary of active work"`,
		`Session: "Daily sync" agent="Ori" updated_at="2026-03-08T10:00:00Z"`,
		"Do not replace it with repository or worktree assumptions",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected prompt to contain %q, got %q", want, prompt)
		}
	}

	if strings.Contains(prompt, "sensitive body text") {
		t.Fatalf("expected workspace file body to be excluded from snapshot, got %q", prompt)
	}
}

func TestBuildTaskPrompt_WithoutWorkspaceSnapshotWhenWorkspaceUnavailable(t *testing.T) {
	handler := &LLMTaskHandler{}

	prompt := handler.buildTaskPrompt(context.Background(), Task{
		ID:          "task-1",
		From:        "jj",
		Description: "summarize workspace",
		Priority:    1,
	}, nil)

	if strings.Contains(prompt, "## Workspace Snapshot") {
		t.Fatalf("expected prompt to omit workspace snapshot, got %q", prompt)
	}
	if !strings.Contains(prompt, "## Task Description") {
		t.Fatalf("expected task description to remain in prompt, got %q", prompt)
	}
}

func TestBuildTaskPrompt_IncludesTaskDetails(t *testing.T) {
	handler := &LLMTaskHandler{}

	prompt := handler.buildTaskPrompt(context.Background(), Task{
		ID:          "task-2",
		From:        "jj",
		Description: "plan the trip",
		Details:     "Original request:\nplan a trip in Lisbon\n\nPlanning intake:\n- Travel dates: 5/11 arrival, 5/14 departure",
		Priority:    1,
	}, nil)

	for _, want := range []string{
		"## Task Description",
		"plan the trip",
		"## Task Details",
		"Original request:",
		"Planning intake:",
		"5/11 arrival",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected prompt to contain %q, got %q", want, prompt)
		}
	}
}

func TestBuildTaskSystemPrompt_DisambiguatesWorkspaceFromRepository(t *testing.T) {
	handler := &LLMTaskHandler{}
	prompt := handler.buildTaskSystemPrompt()

	for _, want := range []string{
		"workspace as the collaborative workspace data provided in the prompt",
		"not the server's current working directory, git checkout, or repository state",
		"Use the workspace snapshot in the prompt as the source of truth",
		"must verify the answer with filesystem tools before responding",
		"Do not answer filesystem listing tasks from the workspace snapshot",
		"return the list directly instead of asking whether the user wants to see it",
		"inspect that exact target after locating it instead of stopping at the parent directory",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected system prompt to contain %q, got %q", want, prompt)
		}
	}
}

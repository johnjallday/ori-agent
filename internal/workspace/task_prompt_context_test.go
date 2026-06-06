package workspace

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/toolapi"
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
		agentStore: &resolverAgentStoreStub{
			agents: map[string]*agent.Agent{
				"Ori": {},
			},
		},
		workspaceStore: wsStore,
		contextStore: &mockTaskPromptContextStore{
			notes: []TaskPromptNoteSummary{
				{ID: "note-1", Name: "Workspace note", Preview: "Summary of active work"},
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
	})

	for _, want := range []string{
		"## Workspace Snapshot",
		`- Workspace Name: "amr"`,
		`- Workspace Description: "test"`,
		`- Agents (1): Ori`,
		"Counts: total=2, pending=1, completed=1",
		`Open task: [pending] "summarize workspace" -> "Ori" (priority 1)`,
		`File: "plan.md" (type="doc", mime="text/markdown")`,
		`Directory: "amr" path="/Users/jjdev/Projects/amr"`,
		`Note: id="note-1" name="Workspace note" preview="Summary of active work"`,
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

func TestPrepareTaskContext_DescribesInjectedSummarizedAndOnDemandSurfaces(t *testing.T) {
	wsStore := NewInMemoryStore()
	ws := NewWorkspace(CreateWorkspaceParams{
		Name:   "amr",
		Agents: []string{"Ori"},
	})
	ws.ID = "workspace-1"
	ws.Attachments = []Attachment{
		{
			ID:    "att-1",
			Title: "plan.md",
			Type:  AttachmentTypeDoc,
			File: &AttachmentFileMeta{
				Name: "plan.md",
				Mime: "text/markdown",
			},
		},
	}
	ws.DirectoryReferences = []DirectoryReference{{Name: "repo", Path: "/tmp/repo"}}
	if err := wsStore.Save(ws); err != nil {
		t.Fatalf("save workspace: %v", err)
	}

	handler := &LLMTaskHandler{
		agentStore: &resolverAgentStoreStub{
			agents: map[string]*agent.Agent{
				"Ori": {},
			},
		},
		workspaceStore: wsStore,
		contextStore: &mockTaskPromptContextStore{
			notes: []TaskPromptNoteSummary{{ID: "note-1", Name: "Note", Preview: "Preview"}},
			sessions: []TaskPromptSessionSummary{
				{Title: "Sync", AgentName: "Ori", UpdatedAt: time.Now()},
			},
			sessionCount: 1,
		},
	}
	handler.workspaceToolsFn = func(string, string) []toolapi.Tool {
		return []toolapi.Tool{taskHandlerToolStub{name: "workspace_notes"}}
	}

	prepared, err := handler.PrepareTaskContext(context.Background(), "Ori", Task{
		ID:          "task-1",
		WorkspaceID: ws.ID,
		Description: "summarize workspace",
	})
	if err != nil {
		t.Fatalf("PrepareTaskContext: %v", err)
	}
	if prepared.Strategy != "task_default" {
		t.Fatalf("Strategy = %q, want task_default", prepared.Strategy)
	}
	if !containsPreparedContextItem(prepared.Items, "workspace_snapshot", "injected") {
		t.Fatalf("Items = %+v, want injected workspace snapshot", prepared.Items)
	}
	if !containsPreparedContextItem(prepared.Items, "workspace_notes", "summarized") {
		t.Fatalf("Items = %+v, want summarized notes", prepared.Items)
	}
	if !containsPreparedContextItem(prepared.Items, "workspace_tools", "on_demand") {
		t.Fatalf("Items = %+v, want on-demand tools", prepared.Items)
	}
	if len(prepared.AvailableTools) != 1 || prepared.AvailableTools[0] != "workspace_notes" {
		t.Fatalf("AvailableTools = %+v, want workspace_notes", prepared.AvailableTools)
	}
}

func TestBuildTaskPrompt_WithoutWorkspaceSnapshotWhenWorkspaceUnavailable(t *testing.T) {
	handler := &LLMTaskHandler{}

	prompt := handler.buildTaskPrompt(context.Background(), Task{
		ID:          "task-1",
		From:        "jj",
		Description: "summarize workspace",
		Priority:    1,
	})

	if strings.Contains(prompt, "## Workspace Snapshot") {
		t.Fatalf("expected prompt to omit workspace snapshot, got %q", prompt)
	}
	if !strings.Contains(prompt, "## Task Description") {
		t.Fatalf("expected task description to remain in prompt, got %q", prompt)
	}
}

func TestBuildTaskPrompt_FreshPublicInfoOmitsUnrelatedPriorTaskSummaries(t *testing.T) {
	wsStore := NewInMemoryStore()
	ws := NewWorkspace(CreateWorkspaceParams{
		Name:   "pollen",
		Agents: []string{"Ori"},
	})
	ws.ID = "workspace-pollen"
	ws.Tasks = []Task{
		{
			ID:          "old-pollen",
			Description: "check pollen count in NYC",
			To:          "Ori",
			Status:      TaskStatusWaitingForChoice,
			Context: map[string]any{
				"human_loop": map[string]any{
					"reason": "AccuWeather was blocked by robots.txt.",
				},
			},
		},
		{
			ID:          "new-pollen",
			Description: "check today's pollen count in NYC",
			To:          "Ori",
			Status:      TaskStatusInProgress,
		},
		{
			ID:          "explicit-input",
			Description: "source preference note",
			To:          "Ori",
			Status:      TaskStatusPending,
		},
	}
	if err := wsStore.Save(ws); err != nil {
		t.Fatalf("failed to save workspace: %v", err)
	}

	handler := &LLMTaskHandler{workspaceStore: wsStore}
	prompt := handler.buildTaskPrompt(context.Background(), Task{
		ID:           "new-pollen",
		WorkspaceID:  ws.ID,
		From:         "jj",
		To:           "Ori",
		Description:  "check today's pollen count in NYC",
		InputTaskIDs: []string{"explicit-input"},
	})

	if strings.Contains(prompt, `Open task: [waiting_for_choice] "check pollen count in NYC"`) {
		t.Fatalf("expected unrelated old pollen task to be omitted, got %q", prompt)
	}
	if !strings.Contains(prompt, `Open task: [in_progress] "check today's pollen count in NYC"`) {
		t.Fatalf("expected current task to remain in prompt, got %q", prompt)
	}
	if !strings.Contains(prompt, `Open task: [pending] "source preference note"`) {
		t.Fatalf("expected explicit input task to remain in prompt, got %q", prompt)
	}
	if !strings.Contains(prompt, "unrelated prior task summaries are omitted") {
		t.Fatalf("expected public-info omission note, got %q", prompt)
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
	})

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

func TestBuildTaskPrompt_IncludesReferenceURLGuidance(t *testing.T) {
	handler := &LLMTaskHandler{}

	prompt := handler.buildTaskPrompt(context.Background(), Task{
		ID:           "task-1",
		From:         "jj",
		Description:  "implement from spec",
		ReferenceURL: "https://example.com/spec",
		Priority:     1,
	})

	for _, want := range []string{
		"## Reference URL",
		"https://example.com/spec",
		"Treat this URL as authoritative source material",
		"Inspect it with available fetch, browser, or web tools",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected prompt to contain %q, got %q", want, prompt)
		}
	}
}

func TestPrepareTaskContext_IncludesReferenceURLSurface(t *testing.T) {
	handler := &LLMTaskHandler{
		agentStore: &resolverAgentStoreStub{
			agents: map[string]*agent.Agent{"Ori": {}},
		},
	}

	prepared, err := handler.PrepareTaskContext(context.Background(), "Ori", Task{
		ID:           "task-1",
		WorkspaceID:  "workspace-1",
		Description:  "implement from spec",
		ReferenceURL: "https://example.com/spec",
	})
	if err != nil {
		t.Fatalf("PrepareTaskContext: %v", err)
	}
	if !containsPreparedContextItem(prepared.Items, "reference_url", "on_demand") {
		t.Fatalf("Items = %+v, want reference_url on-demand context", prepared.Items)
	}
}

func TestBuildTaskSystemPrompt_DisambiguatesWorkspaceFromRepository(t *testing.T) {
	handler := &LLMTaskHandler{}
	prompt := handler.buildTaskSystemPrompt()

	for _, want := range []string{
		"workspace as the collaborative workspace data provided in the prompt",
		"not the server's current working directory, git checkout, or repository state",
		"Use the workspace snapshot in the prompt as the source of truth",
		"must call the workspace_notes tool with the note's id to read the full content",
		"must verify the answer with filesystem tools before responding",
		"Do not answer filesystem listing tasks from the workspace snapshot",
		"return the list directly instead of asking whether the user wants to see it",
		"inspect that exact target after locating it instead of stopping at the parent directory",
		"use web_search first when available",
		"verify that fetched pages match the requested city, region, or ZIP",
		"If search results are empty, broaden the query",
		"Do not return raw Tool Results as the final answer",
		"Do not answer those tasks from prior blocked attempts or workspace task-status summaries",
		"Include source names or URLs and visible dates when available",
		"When a task includes a Reference URL",
		"state that limitation instead of fabricating URL contents",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected system prompt to contain %q, got %q", want, prompt)
		}
	}
}

func containsPreparedContextItem(items []TaskPreparedContextItem, kind, access string) bool {
	for _, item := range items {
		if item.Kind == kind && item.Access == access {
			return true
		}
	}
	return false
}

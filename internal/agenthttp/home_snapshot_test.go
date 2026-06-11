package agenthttp

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// --- shared test stubs (reused across home_* tests) ---

type stubSessionsReader struct {
	sessions []HomeSessionSummary
	err      error
}

func (s stubSessionsReader) RecentSessions(_ context.Context, limit int) ([]HomeSessionSummary, error) {
	if s.err != nil {
		return nil, s.err
	}
	if limit > 0 && limit < len(s.sessions) {
		return s.sessions[:limit], nil
	}
	return s.sessions, nil
}

type stubUsageReader struct {
	summary HomeUsageSummary
	ok      bool
}

func (u stubUsageReader) UsageSummary() (HomeUsageSummary, bool) { return u.summary, u.ok }

type stubAgentsReader struct {
	roster []HomeAgentSummary
	ok     bool
}

func (a stubAgentsReader) AgentRoster() ([]HomeAgentSummary, bool) { return a.roster, a.ok }

func fixedNow() time.Time {
	// A Wednesday so "this week" (Mon-based) has prior days inside the window.
	return time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
}

func makeTestWorkspace(t *testing.T, store workspace.Store, id, name string, tasks []workspace.Task) {
	t.Helper()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: name})
	ws.ID = id
	for _, task := range tasks {
		if err := ws.AddTask(task); err != nil {
			t.Fatalf("add task: %v", err)
		}
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("save workspace: %v", err)
	}
}

func TestBuildHomeSnapshot_Empty(t *testing.T) {
	store := workspace.NewInMemoryStore()
	snap := BuildHomeSnapshot(context.Background(), HomeSnapshotSources{
		Workspaces: store,
		Now:        fixedNow,
	}, HomeWindowThisWeek)

	if snap.Meta.WorkspaceCount != 0 {
		t.Errorf("WorkspaceCount = %d, want 0", snap.Meta.WorkspaceCount)
	}
	if len(snap.Tasks) != 0 {
		t.Errorf("Tasks = %d, want 0", len(snap.Tasks))
	}
	// nil Sessions and Usage sources degrade their sections, not the whole snapshot.
	if !containsString(snap.Meta.Degraded, "sessions") || !containsString(snap.Meta.Degraded, "usage") {
		t.Errorf("expected degraded sessions+usage, got %v", snap.Meta.Degraded)
	}
}

func TestBuildHomeSnapshot_WithData(t *testing.T) {
	store := workspace.NewInMemoryStore()
	now := fixedNow()
	makeTestWorkspace(t, store, "ws-1", "Alpha", []workspace.Task{
		{Description: "recent task", Status: workspace.TaskStatusInProgress, Priority: 2, CreatedAt: now.Add(-2 * time.Hour)},
	})
	makeTestWorkspace(t, store, "ws-2", "Beta", []workspace.Task{
		{Description: "old done task", Status: workspace.TaskStatusCompleted, Priority: 1, CreatedAt: now.AddDate(0, -3, 0), CompletedAt: timePtr(now.AddDate(0, -3, 0))},
	})

	snap := BuildHomeSnapshot(context.Background(), HomeSnapshotSources{
		Workspaces:    store,
		Opportunities: workspace.NewOpportunityStore(store),
		Sessions:      stubSessionsReader{sessions: []HomeSessionSummary{{ID: "s1", Title: "Chat", AgentName: "Ori", MessageCount: 4, UpdatedAt: now}}},
		Usage:         stubUsageReader{summary: HomeUsageSummary{TodayCost: 0.12, TodayTokens: 1000, MonthCost: 3.4, MonthTokens: 50000, Currency: "USD"}, ok: true},
		Agents:        stubAgentsReader{roster: []HomeAgentSummary{{Name: "Ori", Type: "tool-calling", Role: "orchestrator", Model: "gpt-5", Provider: "openai"}}, ok: true},
		Now:           fixedNow,
	}, HomeWindowThisWeek)

	if snap.Meta.WorkspaceCount != 2 {
		t.Errorf("WorkspaceCount = %d, want 2", snap.Meta.WorkspaceCount)
	}
	if snap.Meta.AgentCount != 1 || len(snap.Agents) != 1 || snap.Agents[0].Name != "Ori" {
		t.Errorf("expected 1 agent 'Ori', got count=%d agents=%+v", snap.Meta.AgentCount, snap.Agents)
	}
	// Only the in-window task should appear; the 3-month-old completed task is out.
	if snap.Meta.TaskCount != 1 || len(snap.Tasks) != 1 || snap.Tasks[0].Description != "recent task" {
		t.Errorf("expected 1 windowed task 'recent task', got count=%d tasks=%+v", snap.Meta.TaskCount, snap.Tasks)
	}
	if snap.Meta.SessionCount != 1 {
		t.Errorf("SessionCount = %d, want 1", snap.Meta.SessionCount)
	}
	if snap.Usage == nil || snap.Usage.TodayTokens != 1000 {
		t.Errorf("usage not populated: %+v", snap.Usage)
	}
	if len(snap.Meta.Degraded) != 0 {
		t.Errorf("expected no degraded sections, got %v", snap.Meta.Degraded)
	}
	// Prompt text mentions the window and is non-empty.
	if txt := snap.PromptText(); !strings.Contains(txt, "Home Snapshot") || !strings.Contains(txt, "Alpha") {
		t.Errorf("prompt text missing expected content: %q", txt)
	}
}

func TestBuildHomeSnapshot_DegradedUsage(t *testing.T) {
	store := workspace.NewInMemoryStore()
	snap := BuildHomeSnapshot(context.Background(), HomeSnapshotSources{
		Workspaces: store,
		Sessions:   stubSessionsReader{},
		Usage:      stubUsageReader{ok: false},
		Now:        fixedNow,
	}, HomeWindowToday)
	if snap.Usage != nil {
		t.Error("expected nil usage when reader reports unavailable")
	}
	if !containsString(snap.Meta.Degraded, "usage") {
		t.Errorf("expected degraded usage, got %v", snap.Meta.Degraded)
	}
}

func TestBuildHomeSnapshot_SessionsErrorDegradesSection(t *testing.T) {
	store := workspace.NewInMemoryStore()
	snap := BuildHomeSnapshot(context.Background(), HomeSnapshotSources{
		Workspaces: store,
		Sessions:   stubSessionsReader{err: errors.New("boom")},
		Now:        fixedNow,
	}, HomeWindowThisWeek)
	if !containsString(snap.Meta.Degraded, "sessions") {
		t.Errorf("expected degraded sessions on error, got %v", snap.Meta.Degraded)
	}
}

func TestNormalizeHomeDateWindow(t *testing.T) {
	if got := NormalizeHomeDateWindow("this week", HomeWindowToday); got != HomeWindowThisWeek {
		t.Errorf("explicit 'this week' = %q, want this_week", got)
	}
	if got := NormalizeHomeDateWindow("", HomeWindowToday); got != HomeWindowToday {
		t.Errorf("empty falls back to default today, got %q", got)
	}
	if got := DefaultHomeDateWindowForPrompt("summarize my task activity"); got != HomeWindowThisWeek {
		t.Errorf("recap default = %q, want this_week", got)
	}
	if got := DefaultHomeDateWindowForPrompt("what's running right now"); got != HomeWindowToday {
		t.Errorf("status default = %q, want today", got)
	}
}

func timePtr(t time.Time) *time.Time { return &t }

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

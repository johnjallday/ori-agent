package chathttp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/dailybrief"
	"github.com/johnjallday/ori-agent/internal/personalhq"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/toolapi"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type hqToolSessionSource struct {
	byWorkspace    map[string][]session.SessionListItem
	errByWorkspace map[string]error
}

func (s *hqToolSessionSource) ListSessions(_ context.Context, filter *session.SessionFilter, opts *session.ListOptions) ([]session.SessionListItem, error) {
	if filter == nil || filter.FolderID == nil {
		return nil, nil
	}
	workspaceID := *filter.FolderID
	if err := s.errByWorkspace[workspaceID]; err != nil {
		return nil, err
	}
	items := append([]session.SessionListItem(nil), s.byWorkspace[workspaceID]...)
	if opts != nil && opts.Limit > 0 && len(items) > opts.Limit {
		items = items[:opts.Limit]
	}
	return items, nil
}

func hqToolWorkspace(id, name string) *workspace.Workspace {
	return &workspace.Workspace{
		ID:          id,
		Name:        name,
		Kind:        "workspace",
		Status:      workspace.StatusActive,
		OwnerUserID: "local",
		AgentInstances: []workspace.AgentInstance{
			{Name: "Chief", EntryPoint: true},
			{Name: "Specialist"},
		},
	}
}

func hqToolDeps(store workspace.Store, sessions *hqToolSessionSource, isHQ func(context.Context, string) (bool, error)) HQVisibilityDeps {
	return HQVisibilityDeps{
		SnapshotSources: func() dailybrief.SnapshotSources {
			return dailybrief.SnapshotSources{
				Workspaces:    store,
				Opportunities: workspace.NewOpportunityStore(store),
				Sessions:      sessions,
			}
		},
		IsDesignatedHQ: isHQ,
		FolderPath: func(workspaceID string) string {
			return "/workspace/" + workspaceID
		},
		UserID: "local",
	}
}

func findWorkspaceTool(t *testing.T, provider *WorkspaceToolProvider, name string) interface {
	Call(context.Context, string) (string, error)
} {
	t.Helper()
	for _, tool := range provider.Tools() {
		if tool.Definition().Name == name {
			return tool
		}
	}
	t.Fatalf("workspace tool %q was not registered", name)
	return nil
}

func serializedWorkspaceToolDefinitions(t *testing.T, provider *WorkspaceToolProvider) string {
	t.Helper()
	definitions := make([]toolapi.ToolDefinition, 0)
	for _, tool := range provider.Tools() {
		definitions = append(definitions, tool.Definition())
	}
	raw, err := json.Marshal(definitions)
	if err != nil {
		t.Fatalf("marshal tool definitions: %v", err)
	}
	return string(raw)
}

func TestHQOverviewTool_GatingMatrix(t *testing.T) {
	store := workspace.NewInMemoryStore()
	if err := store.Save(hqToolWorkspace("hq-1", "HQ")); err != nil {
		t.Fatalf("save workspace: %v", err)
	}
	sessions := &hqToolSessionSource{}

	cases := []struct {
		name     string
		isHQ     bool
		agent    string
		wantTool bool
	}{
		{name: "HQ coordinator", isHQ: true, agent: "Chief", wantTool: true},
		{name: "HQ specialist", isHQ: true, agent: "Specialist", wantTool: false},
		{name: "non-HQ coordinator", isHQ: false, agent: "Chief", wantTool: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			provider := NewWorkspaceToolProvider(nil, store, "hq-1", hqToolDeps(store, sessions, func(context.Context, string) (bool, error) {
				return tc.isHQ, nil
			}))
			provider.SetExecutingAgent(tc.agent)
			if got := toolNames(provider)["hq_overview"]; got != tc.wantTool {
				t.Fatalf("hq_overview registered = %v, want %v", got, tc.wantTool)
			}
		})
	}
}

func TestHQOverviewTool_RegistersWhenDesignationExistsOnlyInFolderStore(t *testing.T) {
	store := workspace.NewInMemoryStore()
	if err := store.Save(hqToolWorkspace("hq-folder-only", "HQ")); err != nil {
		t.Fatalf("save primary workspace: %v", err)
	}
	folderStore, err := workspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	if err := folderStore.Save(&workspace.Workspace{
		ID:          "hq-folder-only",
		Name:        "HQ",
		FolderSlug:  "hq",
		Designation: "personal_hq",
	}); err != nil {
		t.Fatalf("save folder workspace: %v", err)
	}
	service := personalhq.NewService(nil, nil)
	service.SetDesignationReader(folderStore)

	provider := NewWorkspaceToolProvider(nil, store, "hq-folder-only", hqToolDeps(store, &hqToolSessionSource{}, func(ctx context.Context, workspaceID string) (bool, error) {
		return service.IsWorkspaceDesignatedPersonalHQ(ctx, "local", workspaceID)
	}))
	provider.SetExecutingAgent("Chief")
	if !toolNames(provider)["hq_overview"] {
		t.Fatal("hq_overview should be registered from the folder-store-only designation")
	}
}

func TestHQOverviewTool_ReturnsBoundedMetadataOnlyProjection(t *testing.T) {
	store := workspace.NewInMemoryStore()
	now := time.Date(2026, 7, 18, 15, 0, 0, 0, time.UTC)
	hq := hqToolWorkspace("hq-1", "Personal HQ")
	hq.Tasks = make([]workspace.Task, 0, 6)
	for i := 0; i < 6; i++ {
		hq.Tasks = append(hq.Tasks, workspace.Task{
			ID:          "task-" + string(rune('a'+i)),
			WorkspaceID: "hq-1",
			Description: "Open task " + string(rune('a'+i)),
			Details:     "task details must never be exposed",
			To:          "Specialist",
			Status:      workspace.TaskStatusPending,
			CreatedAt:   now.Add(-time.Duration(i) * time.Minute),
		})
	}
	hq.ScheduledTasks = []workspace.ScheduledTask{
		{Name: "Broken schedule", FailureCount: 2, LastError: "permission denied", UpdatedAt: now.Add(-2 * time.Minute)},
		{Name: "Error-only schedule", LastError: "network unavailable", UpdatedAt: now.Add(-3 * time.Minute)},
		{Name: "Healthy schedule", UpdatedAt: now.Add(-4 * time.Minute)},
	}
	hq.Opportunities = []workspace.Opportunity{{ID: "low", WorkspaceID: "hq-1", Title: "Low finding", Priority: "low", Status: workspace.OpportunityNew, UpdatedAt: now.Add(-6 * time.Minute)}}
	for i := 0; i < 6; i++ {
		hq.Opportunities = append(hq.Opportunities, workspace.Opportunity{
			ID:          "high-" + string(rune('a'+i)),
			WorkspaceID: "hq-1",
			Title:       "High finding " + string(rune('a'+i)),
			Priority:    "high",
			Status:      workspace.OpportunityNew,
			UpdatedAt:   now.Add(-time.Duration(5+i) * time.Minute),
		})
	}
	if err := store.Save(hq); err != nil {
		t.Fatalf("save HQ: %v", err)
	}

	sessions := &hqToolSessionSource{byWorkspace: map[string][]session.SessionListItem{
		"hq-1": {
			{ID: "older", FolderID: "hq-1", Title: "Older session", Preview: "transcript content must never be exposed", UpdatedAt: now.Add(-20 * time.Minute)},
			{ID: "latest", FolderID: "hq-1", Title: "Latest session", Preview: "more private session content", UpdatedAt: now.Add(-time.Minute)},
		},
	}}
	provider := NewWorkspaceToolProvider(nil, store, "hq-1", hqToolDeps(store, sessions, func(context.Context, string) (bool, error) {
		return true, nil
	}))
	provider.SetExecutingAgent("Chief")

	raw, err := findWorkspaceTool(t, provider, "hq_overview").Call(context.Background(), `{}`)
	if err != nil {
		t.Fatalf("hq_overview call: %v", err)
	}
	if strings.Contains(raw, "task details must never be exposed") || strings.Contains(raw, "transcript content must never be exposed") || strings.Contains(raw, "more private session content") {
		t.Fatalf("hq_overview leaked non-projection content: %s", raw)
	}

	var response hqOverviewResponse
	if err := json.Unmarshal([]byte(raw), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response.Workspaces) != 1 {
		t.Fatalf("workspaces = %#v", response.Workspaces)
	}
	entry := response.Workspaces[0]
	if entry.FolderPath != "/workspace/hq-1" || entry.AgentCount != 2 || entry.OpenTaskCount != 6 || entry.OpenOpportunityCount != 7 || entry.FailingScheduledTaskCount != 2 {
		t.Fatalf("unexpected overview metadata: %#v", entry)
	}
	if len(entry.OpenTasks) != hqOverviewHighlightLimit || len(entry.OpenOpportunities) != hqOverviewHighlightLimit || len(entry.FailingScheduledTasks) != 2 {
		t.Fatalf("unexpected highlight limits: %#v", entry)
	}
	if entry.LatestSession == nil || entry.LatestSession.Title != "Latest session" || entry.MostRecentActivityAt != now.Format(time.RFC3339) {
		t.Fatalf("unexpected recent activity/session: %#v", entry)
	}
}

func TestHQOverviewTool_PropagatesSnapshotGaps(t *testing.T) {
	store := workspace.NewInMemoryStore()
	if err := store.Save(hqToolWorkspace("hq-1", "HQ")); err != nil {
		t.Fatalf("save workspace: %v", err)
	}
	sessions := &hqToolSessionSource{errByWorkspace: map[string]error{"hq-1": errors.New("session read failed")}}
	provider := NewWorkspaceToolProvider(nil, store, "hq-1", hqToolDeps(store, sessions, func(context.Context, string) (bool, error) {
		return true, nil
	}))
	provider.SetExecutingAgent("Chief")

	raw, err := findWorkspaceTool(t, provider, "hq_overview").Call(context.Background(), `{}`)
	if err != nil {
		t.Fatalf("hq_overview call: %v", err)
	}
	var response hqOverviewResponse
	if err := json.Unmarshal([]byte(raw), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response.Gaps) != 1 || !strings.Contains(response.Gaps[0], "recent sessions for workspace HQ are unavailable") {
		t.Fatalf("gaps = %#v", response.Gaps)
	}
}

func TestHQOverviewTool_NonHQToolSurfaceIsUnchanged(t *testing.T) {
	store := workspace.NewInMemoryStore()
	if err := store.Save(hqToolWorkspace("workspace-1", "Ordinary workspace")); err != nil {
		t.Fatalf("save workspace: %v", err)
	}

	before := NewWorkspaceToolProvider(nil, store, "workspace-1")
	before.SetExecutingAgent("Chief")
	after := NewWorkspaceToolProvider(nil, store, "workspace-1", hqToolDeps(store, &hqToolSessionSource{}, func(context.Context, string) (bool, error) {
		return false, nil
	}))
	after.SetExecutingAgent("Chief")

	if got, want := serializedWorkspaceToolDefinitions(t, after), serializedWorkspaceToolDefinitions(t, before); got != want {
		t.Fatalf("non-HQ tool definitions changed: before=%s after=%s", want, got)
	}
	beforeTools := toolNames(before)
	afterTools := toolNames(after)
	if len(beforeTools) != len(afterTools) {
		t.Fatalf("non-HQ tool count changed: before=%v after=%v", beforeTools, afterTools)
	}
	for name := range beforeTools {
		if !afterTools[name] {
			t.Fatalf("non-HQ tool surface lost %q: before=%v after=%v", name, beforeTools, afterTools)
		}
	}
	if afterTools["hq_overview"] {
		t.Fatal("non-HQ workspace must not receive hq_overview")
	}
}

package dailybrief

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type fakeOpportunitySource struct {
	byWorkspace map[string][]workspace.Opportunity
	err         error
}

func (f *fakeOpportunitySource) List(workspaceID string) ([]workspace.Opportunity, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.byWorkspace[workspaceID], nil
}

type fakeSessionSource struct {
	byWorkspace map[string][]session.SessionListItem
	err         error
}

func (f *fakeSessionSource) ListSessions(ctx context.Context, filter *session.SessionFilter, opts *session.ListOptions) ([]session.SessionListItem, error) {
	if f.err != nil {
		return nil, f.err
	}
	if filter == nil || filter.FolderID == nil {
		return nil, nil
	}
	items := f.byWorkspace[*filter.FolderID]
	if opts != nil && opts.Limit > 0 && len(items) > opts.Limit {
		items = items[:opts.Limit]
	}
	return items, nil
}

// slimListingWorkspaceSource models the SQLite workspace list path, which
// returns enough metadata for navigation but omits the heavier orchestration
// payloads. Get still returns the complete record.
type slimListingWorkspaceSource struct {
	full map[string]*workspace.Workspace
}

func (s slimListingWorkspaceSource) Get(id string) (*workspace.Workspace, error) {
	ws, ok := s.full[id]
	if !ok {
		return nil, errors.New("workspace not found")
	}
	return ws, nil
}

func (s slimListingWorkspaceSource) ListActive() ([]*workspace.Workspace, error) {
	items := make([]*workspace.Workspace, 0, len(s.full))
	for _, full := range s.full {
		if full == nil || full.Status != workspace.StatusActive {
			continue
		}
		items = append(items, &workspace.Workspace{
			ID:          full.ID,
			Name:        full.Name,
			Kind:        full.Kind,
			Status:      full.Status,
			OwnerUserID: full.OwnerUserID,
			CreatedAt:   full.CreatedAt,
		})
	}
	return items, nil
}

func newTestWorkspace(id, name, kind string, status workspace.WorkspaceStatus, owner string) *workspace.Workspace {
	return &workspace.Workspace{
		ID: id, Name: name, Kind: kind, Status: status, OwnerUserID: owner,
		CreatedAt: time.Now(),
	}
}

func TestBuildSnapshot_ExcludesGroupsAndInactiveWorkspaces(t *testing.T) {
	store := workspace.NewInMemoryStore()
	_ = store.Save(newTestWorkspace("ws-active", "Active", "workspace", workspace.StatusActive, "local"))
	_ = store.Save(newTestWorkspace("ws-group", "Group", "group", workspace.StatusActive, "local"))
	_ = store.Save(newTestWorkspace("ws-trashed", "Trashed", "workspace", workspace.StatusTrashed, "local"))

	snap := BuildSnapshot(context.Background(), SnapshotSources{Workspaces: store}, Config{Scope: ScopeAll}, "local", time.Now())
	if len(snap.Workspaces) != 1 || snap.Workspaces[0].WorkspaceID != "ws-active" {
		t.Fatalf("expected only the active, non-group workspace, got %+v", snap.Workspaces)
	}
}

func TestBuildSnapshot_ExcludesWorkspacesOwnedByAnotherUser(t *testing.T) {
	store := workspace.NewInMemoryStore()
	_ = store.Save(newTestWorkspace("ws-mine", "Mine", "workspace", workspace.StatusActive, "local"))
	_ = store.Save(newTestWorkspace("ws-other", "Other", "workspace", workspace.StatusActive, "someone-else"))

	snap := BuildSnapshot(context.Background(), SnapshotSources{Workspaces: store}, Config{Scope: ScopeAll}, "local", time.Now())
	if len(snap.Workspaces) != 1 || snap.Workspaces[0].WorkspaceID != "ws-mine" {
		t.Fatalf("expected only the user's own workspace, got %+v", snap.Workspaces)
	}
}

func TestBuildSnapshot_SelectedScopeOnlyIncludesChosenWorkspaces(t *testing.T) {
	store := workspace.NewInMemoryStore()
	_ = store.Save(newTestWorkspace("ws-1", "One", "workspace", workspace.StatusActive, "local"))
	_ = store.Save(newTestWorkspace("ws-2", "Two", "workspace", workspace.StatusActive, "local"))
	_ = store.Save(newTestWorkspace("ws-3", "Three", "workspace", workspace.StatusActive, "local"))

	cfg := Config{Scope: ScopeSelected, SelectedWorkspaceIDs: []string{"ws-1", "ws-3"}}
	snap := BuildSnapshot(context.Background(), SnapshotSources{Workspaces: store}, cfg, "local", time.Now())
	if len(snap.Workspaces) != 2 {
		t.Fatalf("expected 2 selected workspaces, got %d: %+v", len(snap.Workspaces), snap.Workspaces)
	}
	ids := map[string]bool{}
	for _, ws := range snap.Workspaces {
		ids[ws.WorkspaceID] = true
	}
	if !ids["ws-1"] || !ids["ws-3"] || ids["ws-2"] {
		t.Fatalf("expected exactly ws-1 and ws-3, got %+v", ids)
	}
}

// TestBuildSnapshot_SelectedScopeNamesGapForMissingWorkspace covers PRD
// FR86: a selected-but-missing workspace must be named as a gap, not
// silently dropped.
func TestBuildSnapshot_SelectedScopeNamesGapForMissingWorkspace(t *testing.T) {
	store := workspace.NewInMemoryStore()
	cfg := Config{Scope: ScopeSelected, SelectedWorkspaceIDs: []string{"does-not-exist"}}
	snap := BuildSnapshot(context.Background(), SnapshotSources{Workspaces: store}, cfg, "local", time.Now())
	if len(snap.Workspaces) != 0 {
		t.Fatalf("expected no workspaces, got %+v", snap.Workspaces)
	}
	if len(snap.Gaps) != 1 {
		t.Fatalf("expected exactly one named gap, got %v", snap.Gaps)
	}
}

// TestBuildSnapshot_FutureWorkspaceInclusion covers PRD FR110/task 6.2:
// scope=all with IncludeFutureWorkspaces=false must freeze to workspaces
// that existed at the config's last save, excluding ones created after.
func TestBuildSnapshot_FutureWorkspaceInclusion(t *testing.T) {
	store := workspace.NewInMemoryStore()
	configSavedAt := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)

	old := newTestWorkspace("ws-old", "Old", "workspace", workspace.StatusActive, "local")
	old.CreatedAt = configSavedAt.Add(-24 * time.Hour)
	_ = store.Save(old)

	newer := newTestWorkspace("ws-new", "New", "workspace", workspace.StatusActive, "local")
	newer.CreatedAt = configSavedAt.Add(24 * time.Hour)
	_ = store.Save(newer)

	cfgNoFuture := Config{Scope: ScopeAll, IncludeFutureWorkspaces: false, UpdatedAt: configSavedAt}
	snap := BuildSnapshot(context.Background(), SnapshotSources{Workspaces: store}, cfgNoFuture, "local", time.Now())
	if len(snap.Workspaces) != 1 || snap.Workspaces[0].WorkspaceID != "ws-old" {
		t.Fatalf("expected only the pre-existing workspace when future inclusion is off, got %+v", snap.Workspaces)
	}

	cfgWithFuture := Config{Scope: ScopeAll, IncludeFutureWorkspaces: true, UpdatedAt: configSavedAt}
	snap = BuildSnapshot(context.Background(), SnapshotSources{Workspaces: store}, cfgWithFuture, "local", time.Now())
	if len(snap.Workspaces) != 2 {
		t.Fatalf("expected both workspaces when future inclusion is on, got %+v", snap.Workspaces)
	}
}

func TestBuildAllScopeSnapshot_IncludesCurrentEligibleWorkspacesRegardlessOfSavedConfigTime(t *testing.T) {
	store := workspace.NewInMemoryStore()
	now := time.Date(2026, 7, 18, 15, 0, 0, 0, time.UTC)

	hq := newTestWorkspace("ws-hq", "Personal HQ", "workspace", workspace.StatusActive, "local")
	hq.CreatedAt = now.Add(-48 * time.Hour)
	if err := store.Save(hq); err != nil {
		t.Fatalf("save HQ workspace: %v", err)
	}

	newWorkspace := newTestWorkspace("ws-new", "Created Later", "workspace", workspace.StatusActive, "local")
	newWorkspace.CreatedAt = now.Add(-time.Hour)
	if err := store.Save(newWorkspace); err != nil {
		t.Fatalf("save later workspace: %v", err)
	}

	group := newTestWorkspace("ws-group", "Group", "group", workspace.StatusActive, "local")
	if err := store.Save(group); err != nil {
		t.Fatalf("save group: %v", err)
	}
	inactive := newTestWorkspace("ws-inactive", "Inactive", "workspace", workspace.StatusTrashed, "local")
	if err := store.Save(inactive); err != nil {
		t.Fatalf("save inactive: %v", err)
	}
	otherOwner := newTestWorkspace("ws-other", "Someone Else", "workspace", workspace.StatusActive, "other-user")
	if err := store.Save(otherOwner); err != nil {
		t.Fatalf("save other-owner workspace: %v", err)
	}

	snap := BuildAllScopeSnapshot(context.Background(), SnapshotSources{Workspaces: store}, "local", now)
	ids := map[string]bool{}
	for _, ws := range snap.Workspaces {
		ids[ws.WorkspaceID] = true
	}
	if !ids["ws-hq"] || !ids["ws-new"] || len(ids) != 2 {
		t.Fatalf("all-scope snapshot should include only the HQ and later eligible workspace, got %#v", ids)
	}
}

func TestBuildAllScopeSnapshot_HydratesAllScopeWorkspacePayloads(t *testing.T) {
	now := time.Date(2026, 7, 18, 15, 0, 0, 0, time.UTC)
	full := newTestWorkspace("ws-1", "Attention", "workspace", workspace.StatusActive, "local")
	full.Tasks = []workspace.Task{{
		ID:          "task-failed",
		WorkspaceID: "ws-1",
		Description: "Resolve the release blocker",
		Status:      workspace.TaskStatusFailed,
		CreatedAt:   now.Add(-time.Hour),
	}}
	full.ScheduledTasks = []workspace.ScheduledTask{{
		ID:           "nightly-sync",
		WorkspaceID:  "ws-1",
		Name:         "Nightly sync",
		FailureCount: 2,
		LastError:    "remote unavailable",
		UpdatedAt:    now.Add(-30 * time.Minute),
	}}

	snap := BuildAllScopeSnapshot(context.Background(), SnapshotSources{
		Workspaces: slimListingWorkspaceSource{full: map[string]*workspace.Workspace{"ws-1": full}},
	}, "local", now)
	if len(snap.Workspaces) != 1 {
		t.Fatalf("expected one workspace, got %+v", snap.Workspaces)
	}
	got := snap.Workspaces[0]
	if len(got.OpenTasks) != 1 || got.OpenTasks[0].Ref.EntityID != "task-failed" {
		t.Fatalf("expected hydrated failed task, got %+v", got.OpenTasks)
	}
	if len(got.ScheduledTasks) != 1 || got.ScheduledTasks[0].Ref.EntityID != "nightly-sync" {
		t.Fatalf("expected hydrated scheduled task, got %+v", got.ScheduledTasks)
	}
}

func TestBuildSnapshot_DegradesWithGapWhenWorkspaceSourceMissing(t *testing.T) {
	snap := BuildSnapshot(context.Background(), SnapshotSources{}, Config{Scope: ScopeAll}, "local", time.Now())
	if len(snap.Gaps) != 1 {
		t.Fatalf("expected a named gap when workspace source is unavailable, got %v", snap.Gaps)
	}
}

func TestBuildSnapshot_TruncatesTasksOpportunitiesAndSessions(t *testing.T) {
	store := workspace.NewInMemoryStore()
	ws := newTestWorkspace("ws-1", "One", "workspace", workspace.StatusActive, "local")
	for i := 0; i < maxTasksPerWorkspace+10; i++ {
		ws.Tasks = append(ws.Tasks, workspace.Task{
			ID: "task-" + string(rune('a'+i%26)) + string(rune(i)), WorkspaceID: "ws-1",
			Description: "task", Status: workspace.TaskStatusPending, CreatedAt: time.Now().Add(-time.Duration(i) * time.Minute),
		})
	}
	_ = store.Save(ws)

	opps := make([]workspace.Opportunity, 0, maxOpportunitiesPerWorkspace+5)
	for i := 0; i < maxOpportunitiesPerWorkspace+5; i++ {
		opps = append(opps, workspace.Opportunity{ID: "opp-" + string(rune(i)), WorkspaceID: "ws-1", Title: "opp", Priority: "high", Status: workspace.OpportunityNew})
	}
	oppSource := &fakeOpportunitySource{byWorkspace: map[string][]workspace.Opportunity{"ws-1": opps}}

	sessions := make([]session.SessionListItem, 0, maxSessionsPerWorkspace+5)
	for i := 0; i < maxSessionsPerWorkspace+5; i++ {
		sessions = append(sessions, session.SessionListItem{ID: "sess-" + string(rune(i)), Title: "s"})
	}
	sessSource := &fakeSessionSource{byWorkspace: map[string][]session.SessionListItem{"ws-1": sessions}}

	snap := BuildSnapshot(context.Background(), SnapshotSources{Workspaces: store, Opportunities: oppSource, Sessions: sessSource}, Config{Scope: ScopeAll}, "local", time.Now())
	if len(snap.Workspaces) != 1 {
		t.Fatalf("expected one workspace, got %d", len(snap.Workspaces))
	}
	wsSnap := snap.Workspaces[0]
	if len(wsSnap.OpenTasks) != maxTasksPerWorkspace {
		t.Errorf("expected tasks capped at %d, got %d", maxTasksPerWorkspace, len(wsSnap.OpenTasks))
	}
	if len(wsSnap.Opportunities) != maxOpportunitiesPerWorkspace {
		t.Errorf("expected opportunities capped at %d, got %d", maxOpportunitiesPerWorkspace, len(wsSnap.Opportunities))
	}
}

func TestBuildSnapshot_NamesGapsForFailingOpportunityAndSessionSources(t *testing.T) {
	store := workspace.NewInMemoryStore()
	_ = store.Save(newTestWorkspace("ws-1", "One", "workspace", workspace.StatusActive, "local"))

	oppSource := &fakeOpportunitySource{err: errors.New("boom")}
	sessSource := &fakeSessionSource{err: errors.New("boom")}

	snap := BuildSnapshot(context.Background(), SnapshotSources{Workspaces: store, Opportunities: oppSource, Sessions: sessSource}, Config{Scope: ScopeAll}, "local", time.Now())
	if len(snap.Gaps) != 2 {
		t.Fatalf("expected 2 named gaps (opportunities + sessions), got %v", snap.Gaps)
	}
}

func TestBuildSnapshot_OnlyIncludesOpenOpportunities(t *testing.T) {
	store := workspace.NewInMemoryStore()
	_ = store.Save(newTestWorkspace("ws-1", "One", "workspace", workspace.StatusActive, "local"))
	oppSource := &fakeOpportunitySource{byWorkspace: map[string][]workspace.Opportunity{
		"ws-1": {
			{ID: "open-1", WorkspaceID: "ws-1", Title: "open", Status: workspace.OpportunityNew, Priority: "high"},
			{ID: "resolved-1", WorkspaceID: "ws-1", Title: "resolved", Status: workspace.OpportunityResolved, Priority: "high"},
			{ID: "dismissed-1", WorkspaceID: "ws-1", Title: "dismissed", Status: workspace.OpportunityDismissed, Priority: "high"},
		},
	}}
	snap := BuildSnapshot(context.Background(), SnapshotSources{Workspaces: store, Opportunities: oppSource}, Config{Scope: ScopeAll}, "local", time.Now())
	if len(snap.Workspaces[0].Opportunities) != 1 || snap.Workspaces[0].Opportunities[0].Ref.EntityID != "open-1" {
		t.Fatalf("expected only the open opportunity, got %+v", snap.Workspaces[0].Opportunities)
	}
}

func TestSnapshot_AllRefsCollectsEveryEntity(t *testing.T) {
	snap := Snapshot{Workspaces: []WorkspaceSnapshot{
		{
			WorkspaceID:    "ws-1",
			OpenTasks:      []TaskSnapshot{{Ref: SourceRef{WorkspaceID: "ws-1", EntityType: "task", EntityID: "t1"}}},
			Opportunities:  []OpportunitySnapshot{{Ref: SourceRef{WorkspaceID: "ws-1", EntityType: "opportunity", EntityID: "o1"}}},
			ScheduledTasks: []ScheduledTaskSnapshot{{Ref: SourceRef{WorkspaceID: "ws-1", EntityType: "scheduled_task", EntityID: "st1"}}},
			RecentSessions: []SessionSnapshot{{Ref: SourceRef{WorkspaceID: "ws-1", EntityType: "session", EntityID: "s1"}}},
		},
	}}
	refs := snap.AllRefs()
	if len(refs) != 4 {
		t.Fatalf("expected 4 refs, got %d: %+v", len(refs), refs)
	}
}

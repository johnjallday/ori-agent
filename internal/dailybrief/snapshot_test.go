package dailybrief

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/followup"
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

type fakeFollowUpSource struct {
	items             []*followup.FollowUp
	err               error
	itemsByWorkspace  map[string][]*followup.FollowUp
	errorsByWorkspace map[string]error
	filter            followup.Filter
	filters           []followup.Filter
	calls             int
}

func (f *fakeFollowUpSource) List(_ context.Context, filter followup.Filter) ([]*followup.FollowUp, error) {
	f.calls++
	f.filter = filter
	f.filters = append(f.filters, filter)
	if err := f.errorsByWorkspace[filter.WorkspaceID]; err != nil {
		return nil, err
	}
	if f.itemsByWorkspace != nil {
		return f.itemsByWorkspace[filter.WorkspaceID], nil
	}
	return f.items, f.err
}

// slimListingWorkspaceSource models the SQLite workspace list path, which
// returns enough metadata for navigation but omits the heavier orchestration
// payloads. Get still returns the complete record.
type slimListingWorkspaceSource struct {
	full      map[string]*workspace.Workspace
	listed    []*workspace.Workspace
	listErr   error
	getErrors map[string]error
	getCalls  map[string]int
}

func (s *slimListingWorkspaceSource) Get(id string) (*workspace.Workspace, error) {
	if s.getCalls != nil {
		s.getCalls[id]++
	}
	if err := s.getErrors[id]; err != nil {
		return nil, err
	}
	ws, ok := s.full[id]
	if !ok {
		return nil, errors.New("workspace not found")
	}
	return ws, nil
}

func (s *slimListingWorkspaceSource) ListActive() ([]*workspace.Workspace, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	if s.listed != nil {
		return s.listed, nil
	}
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

func TestResolveWorkspaceScope_HydratesDeduplicatesAndOrdersSelectedWorkspaces(t *testing.T) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	first := newTestWorkspace("ws-b", "Beta", "workspace", workspace.StatusActive, "local")
	first.FolderSlug = "beta"
	first.Tasks = []workspace.Task{{ID: "task-b", WorkspaceID: first.ID}}
	second := newTestWorkspace("ws-a", "Alpha", "workspace", workspace.StatusActive, "local")
	second.FolderSlug = "alpha"
	second.CreatedAt = now.Add(time.Hour) // Selected scope ignores the all-scope cutoff.
	source := &slimListingWorkspaceSource{
		full:     map[string]*workspace.Workspace{"ws-a": second, "ws-b": first},
		getCalls: map[string]int{},
	}

	got := ResolveWorkspaceScope(source, Config{
		Scope: ScopeSelected, SelectedWorkspaceIDs: []string{" ws-b ", "ws-a", "ws-b"},
		IncludeFutureWorkspaces: false, UpdatedAt: now,
	}, "local")
	if len(got.Workspaces) != 2 || got.Workspaces[0].ID != "ws-a" || got.Workspaces[1].ID != "ws-b" {
		t.Fatalf("resolved workspaces = %+v, want deterministic ws-a/ws-b", got.Workspaces)
	}
	if got.Workspaces[1].Workspace == nil || len(got.Workspaces[1].Workspace.Tasks) != 1 || got.Workspaces[1].Slug != "beta" {
		t.Fatalf("selected workspace was not hydrated with stable identity: %+v", got.Workspaces[1])
	}
	if source.getCalls["ws-a"] != 1 || source.getCalls["ws-b"] != 1 {
		t.Fatalf("selected IDs were not deduplicated before hydration: calls=%v", source.getCalls)
	}
	if len(got.Gaps) != 0 {
		t.Fatalf("healthy selected scope gaps = %v", got.Gaps)
	}
}

func TestResolveWorkspaceScope_AllScopeHydratesBeforeEligibilityAndHonorsSavedCutoff(t *testing.T) {
	cutoff := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	eligible := newTestWorkspace("ws-eligible", "Eligible", "workspace", workspace.StatusActive, "local")
	eligible.FolderSlug = "eligible"
	eligible.CreatedAt = cutoff.Add(-time.Hour)
	eligible.Tasks = []workspace.Task{{ID: "hydrated-task", WorkspaceID: eligible.ID}}
	future := newTestWorkspace("ws-future", "Future", "workspace", workspace.StatusActive, "local")
	future.FolderSlug = "future"
	future.CreatedAt = cutoff.Add(time.Hour)
	foreign := newTestWorkspace("ws-foreign", "Foreign", "workspace", workspace.StatusActive, "other")
	foreign.FolderSlug = "foreign"
	inactive := newTestWorkspace("ws-inactive", "Inactive", "workspace", workspace.StatusTrashed, "local")
	inactive.FolderSlug = "inactive"
	group := newTestWorkspace("ws-group", "Group", "group", workspace.StatusActive, "local")
	group.FolderSlug = "group"
	source := &slimListingWorkspaceSource{
		full: map[string]*workspace.Workspace{
			eligible.ID: eligible, future.ID: future, foreign.ID: foreign,
			inactive.ID: inactive, group.ID: group,
		},
		listed: []*workspace.Workspace{
			{ID: future.ID, Name: future.Name, Status: workspace.StatusActive},
			{ID: eligible.ID, Name: eligible.Name, Status: workspace.StatusActive},
			{ID: group.ID, Name: group.Name, Status: workspace.StatusActive},
			{ID: foreign.ID, Name: foreign.Name, Status: workspace.StatusActive},
			{ID: eligible.ID, Name: "duplicate", Status: workspace.StatusActive},
			{ID: inactive.ID, Name: inactive.Name, Status: workspace.StatusActive},
		},
		getCalls: map[string]int{},
	}

	got := ResolveWorkspaceScope(source, Config{
		Scope: ScopeAll, IncludeFutureWorkspaces: false, UpdatedAt: cutoff,
	}, "local")
	if len(got.Workspaces) != 1 || got.Workspaces[0].ID != eligible.ID || len(got.Workspaces[0].Workspace.Tasks) != 1 {
		t.Fatalf("all scope = %+v, want one hydrated pre-cutoff owned workspace", got.Workspaces)
	}
	if source.getCalls[eligible.ID] != 1 {
		t.Fatalf("duplicate all-scope identity hydrated %d times, want once", source.getCalls[eligible.ID])
	}
}

func TestResolveWorkspaceScope_SourceFailuresAreNamedBoundedAndNotHealthyEmpty(t *testing.T) {
	t.Run("missing source", func(t *testing.T) {
		got := ResolveWorkspaceScope(nil, Config{Scope: ScopeAll}, "local")
		if len(got.Workspaces) != 0 || len(got.Gaps) != 1 || got.Gaps[0] != "workspace data is unavailable" {
			t.Fatalf("result = %+v", got)
		}
	})
	t.Run("list failure", func(t *testing.T) {
		got := ResolveWorkspaceScope(&slimListingWorkspaceSource{listErr: errors.New("private details")}, Config{Scope: ScopeAll}, "local")
		if len(got.Workspaces) != 0 || len(got.Gaps) != 1 || got.Gaps[0] != "workspace list is unavailable" {
			t.Fatalf("result = %+v", got)
		}
	})
	t.Run("selected get failures", func(t *testing.T) {
		ids := make([]string, 0, maxWorkspaceScopeGaps+10)
		for i := 0; i < maxWorkspaceScopeGaps+10; i++ {
			ids = append(ids, fmt.Sprintf("missing-%02d", i))
		}
		got := ResolveWorkspaceScope(&slimListingWorkspaceSource{full: map[string]*workspace.Workspace{}}, Config{
			Scope: ScopeSelected, SelectedWorkspaceIDs: ids,
		}, "local")
		if len(got.Workspaces) != 0 || len(got.Gaps) != maxWorkspaceScopeGaps {
			t.Fatalf("result workspaces=%v gaps=%v", got.Workspaces, got.Gaps)
		}
		if got.Gaps[maxWorkspaceScopeGaps-1] != "additional workspace sources are unavailable" {
			t.Fatalf("bounded gap summary = %q", got.Gaps[maxWorkspaceScopeGaps-1])
		}
	})
	t.Run("all hydration failure", func(t *testing.T) {
		source := &slimListingWorkspaceSource{
			full:      map[string]*workspace.Workspace{},
			listed:    []*workspace.Workspace{{ID: "ws-broken", Name: "Broken source", Status: workspace.StatusActive}},
			getErrors: map[string]error{"ws-broken": errors.New("private details")},
		}
		got := ResolveWorkspaceScope(source, Config{Scope: ScopeAll}, "local")
		if len(got.Workspaces) != 0 || len(got.Gaps) != 1 || got.Gaps[0] != "workspace Broken source is unavailable" {
			t.Fatalf("result = %+v", got)
		}
	})
}

func TestResolveFollowUpOwnerScope_IncludesHQOnceAndOnlyScopedEmailOpsOwners(t *testing.T) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	hq := newTestWorkspace("hq", "Personal HQ", "workspace", workspace.StatusActive, "local")
	hq.FolderSlug = "personal-hq"
	emailOps := newTestWorkspace("email-ops", "Email Ops", "workspace", workspace.StatusActive, "local")
	emailOps.FolderSlug = "email-ops"
	emailOps.SetTemplateProvenance(&workspace.TemplateProvenance{TemplateID: workspace.EmailOpsTemplateID, Builtin: true})
	unrelated := newTestWorkspace("project", "Client Project", "workspace", workspace.StatusActive, "local")
	unrelated.FolderSlug = "client-project"
	futureEmailOps := newTestWorkspace("future-email-ops", "Future Email Ops", "workspace", workspace.StatusActive, "local")
	futureEmailOps.FolderSlug = "future-email-ops"
	futureEmailOps.CreatedAt = now.Add(time.Hour)
	futureEmailOps.SetTemplateProvenance(&workspace.TemplateProvenance{TemplateID: workspace.EmailOpsTemplateID, Builtin: true})
	source := &slimListingWorkspaceSource{full: map[string]*workspace.Workspace{
		hq.ID: hq, emailOps.ID: emailOps, unrelated.ID: unrelated, futureEmailOps.ID: futureEmailOps,
	}}

	scope := ResolveWorkspaceScope(source, Config{
		Scope: ScopeSelected, SelectedWorkspaceIDs: []string{emailOps.ID, unrelated.ID, emailOps.ID},
	}, "local")
	got := ResolveFollowUpOwnerScope(source, scope, hq.ID, "local")
	if len(got.Owners) != 2 || got.Owners[0].WorkspaceID != emailOps.ID || got.Owners[1].WorkspaceID != hq.ID {
		t.Fatalf("owners = %+v, want exactly Email Ops and designated HQ", got.Owners)
	}
	for _, owner := range got.Owners {
		if owner.WorkspaceSlug == "" || owner.Name == "" {
			t.Fatalf("owner lacks safe grounding identity: %+v", owner)
		}
	}
	if len(got.Gaps) != 0 {
		t.Fatalf("healthy owner scope gaps = %v", got.Gaps)
	}
}

func TestResolveFollowUpOwnerScope_FailsClosedWithoutLeakingExcludedSources(t *testing.T) {
	cutoff := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	makeWorkspace := func(id, name, owner string, status workspace.WorkspaceStatus) *workspace.Workspace {
		ws := newTestWorkspace(id, name, "workspace", status, owner)
		ws.FolderSlug = id
		ws.CreatedAt = cutoff.Add(-time.Hour)
		ws.SetTemplateProvenance(&workspace.TemplateProvenance{TemplateID: workspace.EmailOpsTemplateID, Builtin: true})
		return ws
	}
	hq := newTestWorkspace("hq", "Personal HQ", "workspace", workspace.StatusActive, "local")
	hq.FolderSlug = "personal-hq"
	valid := makeWorkspace("email-valid", "Valid Email Ops", "local", workspace.StatusActive)
	foreign := makeWorkspace("email-foreign", "Foreign Email Ops", "other", workspace.StatusActive)
	inactive := makeWorkspace("email-inactive", "Inactive Email Ops", "local", workspace.StatusTrashed)
	malformedSlug := makeWorkspace("email-malformed", "Malformed Email Ops", "local", workspace.StatusActive)
	malformedSlug.FolderSlug = "../email"
	future := makeWorkspace("email-future", "Future Email Ops", "local", workspace.StatusActive)
	future.CreatedAt = cutoff.Add(time.Hour)
	unrelated := newTestWorkspace("project", "Project", "workspace", workspace.StatusActive, "local")
	unrelated.FolderSlug = "project"
	source := &slimListingWorkspaceSource{full: map[string]*workspace.Workspace{
		hq.ID: hq, valid.ID: valid, foreign.ID: foreign, inactive.ID: inactive,
		malformedSlug.ID: malformedSlug, future.ID: future, unrelated.ID: unrelated,
	}}

	selected := ResolveWorkspaceScope(source, Config{Scope: ScopeSelected, SelectedWorkspaceIDs: []string{
		valid.ID, foreign.ID, inactive.ID, malformedSlug.ID, unrelated.ID, "missing",
	}}, "local")
	got := ResolveFollowUpOwnerScope(source, selected, hq.ID, "local")
	if len(got.Owners) != 2 || got.Owners[0].WorkspaceID != valid.ID || got.Owners[1].WorkspaceID != hq.ID {
		t.Fatalf("selected owners leaked an invalid source: %+v", got.Owners)
	}
	if len(got.Gaps) != 4 {
		t.Fatalf("explicit invalid sources were not named as gaps: %v", got.Gaps)
	}

	all := ResolveWorkspaceScope(source, Config{
		Scope: ScopeAll, IncludeFutureWorkspaces: false, UpdatedAt: cutoff,
	}, "local")
	got = ResolveFollowUpOwnerScope(source, all, hq.ID, "local")
	if len(got.Owners) != 2 || got.Owners[0].WorkspaceID != valid.ID || got.Owners[1].WorkspaceID != hq.ID {
		t.Fatalf("all-scope owners leaked foreign/inactive/future/malformed/unrelated sources: %+v", got.Owners)
	}
	if len(got.Gaps) != 0 {
		t.Fatalf("excluded all-scope sources should not be presented as selected failures: %v", got.Gaps)
	}
}

func TestResolveFollowUpOwnerScope_RejectsMalformedAndGroupIdentitiesAndDeduplicatesHQ(t *testing.T) {
	hq := newTestWorkspace("hq", "Personal HQ", "workspace", workspace.StatusActive, "local")
	hq.FolderSlug = "personal-hq"
	// Even if a malformed installation marks HQ with Email Ops provenance, the
	// stable workspace identity may authorize it only once as the designated HQ.
	hq.SetTemplateProvenance(&workspace.TemplateProvenance{TemplateID: workspace.EmailOpsTemplateID, Builtin: true})
	group := newTestWorkspace("email-group", "Email Group", "group", workspace.StatusActive, "local")
	group.FolderSlug = "email-group"
	group.SetTemplateProvenance(&workspace.TemplateProvenance{TemplateID: workspace.EmailOpsTemplateID, Builtin: true})
	mismatched := newTestWorkspace("different-id", "Mismatched", "workspace", workspace.StatusActive, "local")
	mismatched.FolderSlug = "mismatched"
	mismatched.SetTemplateProvenance(&workspace.TemplateProvenance{TemplateID: workspace.EmailOpsTemplateID, Builtin: true})
	source := &slimListingWorkspaceSource{full: map[string]*workspace.Workspace{
		hq.ID: hq, group.ID: group, "configured-id": mismatched,
	}}

	scope := ResolveWorkspaceScope(source, Config{Scope: ScopeSelected, SelectedWorkspaceIDs: []string{
		hq.ID, hq.ID, group.ID, "configured-id",
	}}, "local")
	got := ResolveFollowUpOwnerScope(source, scope, hq.ID, "local")
	if len(got.Owners) != 1 || got.Owners[0].WorkspaceID != hq.ID {
		t.Fatalf("owner identity was duplicated or malformed source leaked: %+v", got.Owners)
	}
	if len(got.Gaps) != 2 {
		t.Fatalf("group and mismatched selected identities must be named gaps: %v", got.Gaps)
	}
}

func TestResolveFollowUpOwnerScope_ListFailureKeepsValidatedHQAndNamesDegradation(t *testing.T) {
	hq := newTestWorkspace("hq", "Personal HQ", "workspace", workspace.StatusActive, "local")
	hq.FolderSlug = "personal-hq"
	source := &slimListingWorkspaceSource{
		full: map[string]*workspace.Workspace{hq.ID: hq}, listErr: errors.New("private details"),
	}
	scope := ResolveWorkspaceScope(source, Config{Scope: ScopeAll}, "local")
	got := ResolveFollowUpOwnerScope(source, scope, hq.ID, "local")
	if len(got.Owners) != 1 || got.Owners[0].WorkspaceID != hq.ID {
		t.Fatalf("healthy HQ was erased by specialist discovery failure: %+v", got.Owners)
	}
	if len(got.Gaps) != 1 || got.Gaps[0] != "workspace list is unavailable" {
		t.Fatalf("list failure degradation = %v", got.Gaps)
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
		Workspaces: &slimListingWorkspaceSource{full: map[string]*workspace.Workspace{"ws-1": full}},
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

func TestBuildSnapshot_FollowUpsAreHQScopedBoundedAndDueFirst(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	items := []*followup.FollowUp{
		{ID: "healthy", UserID: "local", WorkspaceID: "hq", Category: followup.CategoryIOwe, Direction: followup.DirectionOutbound, Title: "Healthy", Status: followup.StatusActive, UpdatedAt: now},
		{ID: "stale", UserID: "local", WorkspaceID: "hq", Category: followup.CategoryWaitingOn, Direction: followup.DirectionInbound, Title: "Stale", Status: followup.StatusActive, UpdatedAt: now.Add(-8 * 24 * time.Hour)},
		{ID: "candidate", UserID: "local", WorkspaceID: "hq", Title: "Candidate", Status: followup.StatusCandidate, UpdatedAt: now},
		{ID: "closed", UserID: "local", WorkspaceID: "hq", Title: "Closed", Status: followup.StatusCompleted, UpdatedAt: now},
		{ID: "other-user", UserID: "other", WorkspaceID: "hq", Title: "Other", Status: followup.StatusActive, UpdatedAt: now},
		{ID: "other-hq", UserID: "local", WorkspaceID: "other", Title: "Other HQ", Status: followup.StatusActive, UpdatedAt: now},
	}
	for i := 0; i < maxFollowUpsPerBrief; i++ {
		due := now.Add(time.Duration(i+1) * time.Hour)
		items = append(items, &followup.FollowUp{
			ID: "due-" + string(rune('a'+i)), UserID: "local", WorkspaceID: "hq",
			Category: followup.CategoryIOwe, Direction: followup.DirectionOutbound,
			Title: "Due", Status: followup.StatusReopened, DueAt: &due, UpdatedAt: now.Add(-time.Duration(i) * time.Minute),
		})
	}
	source := &fakeFollowUpSource{items: items}
	store := workspace.NewInMemoryStore()
	hq := newTestWorkspace("hq", "Personal HQ", "workspace", workspace.StatusActive, "local")
	hq.FolderSlug = "personal-hq"
	_ = store.Save(hq)
	snap := BuildSnapshot(context.Background(), SnapshotSources{
		Workspaces: store, FollowUps: source,
	}, Config{Scope: ScopeAll, WorkspaceID: "hq"}, "local", now)
	if source.calls != 1 || source.filter.UserID != "local" || source.filter.WorkspaceID != "hq" || len(source.filter.Statuses) != 2 {
		t.Fatalf("follow-up filter = %#v calls=%d", source.filter, source.calls)
	}
	if len(snap.FollowUps) != maxFollowUpsPerBrief {
		t.Fatalf("follow-ups = %d, want cap %d", len(snap.FollowUps), maxFollowUpsPerBrief)
	}
	if snap.FollowUps[0].Ref.EntityID != "stale" || !snap.FollowUps[0].Stale || snap.FollowUps[0].Ref.EntityType != "follow_up" {
		t.Fatalf("due/stale ordering = %#v", snap.FollowUps)
	}
	for _, item := range snap.FollowUps {
		if item.Ref.EntityID == "candidate" || item.Ref.EntityID == "closed" || item.Ref.EntityID == "other-user" || item.Ref.EntityID == "other-hq" {
			t.Fatalf("unauthorized or inactive follow-up included: %#v", item)
		}
	}
	if _, ok := snap.AllRefs()[snap.FollowUps[0].Ref.Key()]; !ok {
		t.Fatal("follow-up ref missing from allowlist")
	}
}

func TestBuildSnapshot_AggregatesAuthorizedOwnersWithoutChangingCanonicalRows(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	store := workspace.NewInMemoryStore()
	hq := newTestWorkspace("hq", "Personal HQ", "workspace", workspace.StatusActive, "local")
	hq.FolderSlug = "personal-hq"
	emailOps := newTestWorkspace("email", "Email Ops", "workspace", workspace.StatusActive, "local")
	emailOps.FolderSlug = "email-ops"
	emailOps.SetTemplateProvenance(&workspace.TemplateProvenance{TemplateID: workspace.EmailOpsTemplateID, Builtin: true})
	project := newTestWorkspace("project", "Project", "workspace", workspace.StatusActive, "local")
	project.FolderSlug = "project"
	for _, ws := range []*workspace.Workspace{hq, emailOps, project} {
		_ = store.Save(ws)
	}

	hqRow := &followup.FollowUp{
		ID: "same-id", UserID: "local", WorkspaceID: hq.ID, Title: "HQ commitment",
		Status: followup.StatusReopened, Source: followup.SourceRef{Type: "journal", ID: "entry-1"},
		Provenance: followup.ProvenanceExplicit, UpdatedAt: now.Add(-2 * time.Hour),
	}
	emailRow := &followup.FollowUp{
		ID: "same-id", UserID: "local", WorkspaceID: emailOps.ID, Title: "Waiting for agreement",
		Category: followup.CategoryNeedsDecision, Status: followup.StatusActive,
		Source:     followup.SourceRef{Type: "email_thread", ID: "thread-1", AccountID: "account-1"},
		Provenance: followup.ProvenanceManual, UpdatedAt: now.Add(-time.Hour),
	}
	closed := &followup.FollowUp{ID: "closed", UserID: "local", WorkspaceID: emailOps.ID, Status: followup.StatusCompleted}
	snoozed := &followup.FollowUp{ID: "snoozed", UserID: "local", WorkspaceID: emailOps.ID, Status: followup.StatusSnoozed}
	candidate := &followup.FollowUp{ID: "candidate", UserID: "local", WorkspaceID: emailOps.ID, Status: followup.StatusCandidate}
	foreign := &followup.FollowUp{ID: "foreign", UserID: "other", WorkspaceID: emailOps.ID, Status: followup.StatusActive}
	wrongOwner := &followup.FollowUp{ID: "wrong-owner", UserID: "local", WorkspaceID: project.ID, Status: followup.StatusActive}
	blankOwner := &followup.FollowUp{ID: "blank-owner", UserID: "local", WorkspaceID: "", Status: followup.StatusActive}
	rows := []*followup.FollowUp{hqRow, emailRow, closed, snoozed, candidate, foreign, wrongOwner, blankOwner}
	before := make([]followup.FollowUp, len(rows))
	for i, row := range rows {
		before[i] = *row
	}
	source := &fakeFollowUpSource{itemsByWorkspace: map[string][]*followup.FollowUp{
		hq.ID:       {hqRow, blankOwner},
		emailOps.ID: {emailRow, emailRow, closed, snoozed, candidate, foreign, wrongOwner},
		"orphan":    {{ID: "orphan", UserID: "local", WorkspaceID: "orphan", Status: followup.StatusActive}},
	}}
	snap := BuildSnapshot(context.Background(), SnapshotSources{
		Workspaces: store, FollowUps: source,
		Opportunities: &fakeOpportunitySource{}, Sessions: &fakeSessionSource{},
	}, Config{
		Scope: ScopeSelected, SelectedWorkspaceIDs: []string{emailOps.ID, project.ID}, WorkspaceID: hq.ID,
	}, "local", now)

	if source.calls != 2 {
		t.Fatalf("follow-up reads = %d, want one per HQ/Email Ops owner", source.calls)
	}
	if len(snap.FollowUps) != 2 {
		t.Fatalf("follow-ups = %+v, want both same-ID records under distinct owners", snap.FollowUps)
	}
	byKey := make(map[string]FollowUpSnapshot)
	for _, item := range snap.FollowUps {
		byKey[item.Ref.Key()] = item
	}
	if got := byKey["follow_up:"+hq.ID+":same-id"]; got.Ref.WorkspaceSlug != "personal-hq" || got.OwnerName != "Personal HQ" {
		t.Fatalf("HQ grounding = %+v", got)
	}
	if got := byKey["follow_up:"+emailOps.ID+":same-id"]; got.Ref.WorkspaceSlug != "email-ops" || got.OwnerName != "Email Ops" {
		t.Fatalf("Email Ops grounding = %+v", got)
	}
	for i, row := range rows {
		if !reflect.DeepEqual(before[i], *row) {
			t.Fatalf("snapshot evaluation mutated canonical row %q: before=%+v after=%+v", row.ID, before[i], *row)
		}
	}
	for _, filter := range source.filters {
		if filter.WorkspaceID == project.ID || filter.WorkspaceID == "" || filter.WorkspaceID == "orphan" {
			t.Fatalf("unauthorized owner was queried: %+v", filter)
		}
	}
}

func TestBuildSnapshot_MixedOwnerFollowUpsUseOneDeterministicGlobalCap(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	store := workspace.NewInMemoryStore()
	hq := newTestWorkspace("hq", "Personal HQ", "workspace", workspace.StatusActive, "local")
	hq.FolderSlug = "personal-hq"
	emailOps := newTestWorkspace("email", "Email Ops", "workspace", workspace.StatusActive, "local")
	emailOps.FolderSlug = "email-ops"
	emailOps.SetTemplateProvenance(&workspace.TemplateProvenance{TemplateID: workspace.EmailOpsTemplateID, Builtin: true})
	_ = store.Save(hq)
	_ = store.Save(emailOps)
	source := &fakeFollowUpSource{itemsByWorkspace: map[string][]*followup.FollowUp{}}
	for _, ownerID := range []string{hq.ID, emailOps.ID} {
		for i := 5; i >= 0; i-- { // Deliberately reverse each source's order.
			source.itemsByWorkspace[ownerID] = append(source.itemsByWorkspace[ownerID], &followup.FollowUp{
				ID: fmt.Sprintf("item-%02d", i), UserID: "local", WorkspaceID: ownerID,
				Title: "Commitment", Status: followup.StatusActive, UpdatedAt: now,
			})
		}
	}
	snap := BuildSnapshot(context.Background(), SnapshotSources{
		Workspaces: store, FollowUps: source,
		Opportunities: &fakeOpportunitySource{}, Sessions: &fakeSessionSource{},
	}, Config{Scope: ScopeAll, IncludeFutureWorkspaces: true, WorkspaceID: hq.ID}, "local", now)
	if len(snap.FollowUps) != maxFollowUpsPerBrief {
		t.Fatalf("mixed-owner cap = %d, want %d", len(snap.FollowUps), maxFollowUpsPerBrief)
	}
	wantKeys := []string{
		"follow_up:email:item-00", "follow_up:email:item-01", "follow_up:email:item-02",
		"follow_up:email:item-03", "follow_up:email:item-04", "follow_up:email:item-05",
		"follow_up:hq:item-00", "follow_up:hq:item-01", "follow_up:hq:item-02", "follow_up:hq:item-03",
	}
	for i, want := range wantKeys {
		if got := snap.FollowUps[i].Ref.Key(); got != want {
			t.Fatalf("position %d = %q, want %q; all=%+v", i, got, want, snap.FollowUps)
		}
	}
}

func TestBuildSnapshot_EmailOpsReadFailureRetainsHQAndNamesOwnerGap(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	store := workspace.NewInMemoryStore()
	hq := newTestWorkspace("hq", "Personal HQ", "workspace", workspace.StatusActive, "local")
	hq.FolderSlug = "personal-hq"
	emailOps := newTestWorkspace("email", "Email Ops", "workspace", workspace.StatusActive, "local")
	emailOps.FolderSlug = "email-ops"
	emailOps.SetTemplateProvenance(&workspace.TemplateProvenance{TemplateID: workspace.EmailOpsTemplateID, Builtin: true})
	_ = store.Save(hq)
	_ = store.Save(emailOps)
	source := &fakeFollowUpSource{
		itemsByWorkspace: map[string][]*followup.FollowUp{hq.ID: {{
			ID: "hq-item", UserID: "local", WorkspaceID: hq.ID, Title: "Healthy HQ item",
			Status: followup.StatusActive, UpdatedAt: now,
		}}},
		errorsByWorkspace: map[string]error{emailOps.ID: errors.New("private details")},
	}
	snap := BuildSnapshot(context.Background(), SnapshotSources{
		Workspaces: store, FollowUps: source,
		Opportunities: &fakeOpportunitySource{}, Sessions: &fakeSessionSource{},
	}, Config{Scope: ScopeSelected, SelectedWorkspaceIDs: []string{emailOps.ID}, WorkspaceID: hq.ID}, "local", now)
	if len(snap.FollowUps) != 1 || snap.FollowUps[0].Ref.WorkspaceID != hq.ID {
		t.Fatalf("healthy HQ result erased by Email Ops failure: %+v", snap.FollowUps)
	}
	if len(snap.Gaps) != 1 || snap.Gaps[0] != "follow-ups for Email Ops could not be read" {
		t.Fatalf("owner-specific degradation = %v", snap.Gaps)
	}
}

func TestBuildSnapshot_FollowUpNotConfiguredEmptyAndFailureAreDistinct(t *testing.T) {
	store := workspace.NewInMemoryStore()
	hq := newTestWorkspace("hq", "Personal HQ", "workspace", workspace.StatusActive, "local")
	hq.FolderSlug = "personal-hq"
	_ = store.Save(hq)
	source := &fakeFollowUpSource{}
	sources := SnapshotSources{
		Workspaces: store, FollowUps: source,
		Opportunities: &fakeOpportunitySource{}, Sessions: &fakeSessionSource{},
	}
	notConfigured := BuildSnapshot(context.Background(), sources, Config{Scope: ScopeAll}, "local", time.Now())
	if source.calls != 0 || len(notConfigured.Gaps) != 0 {
		t.Fatalf("not configured calls=%d gaps=%v", source.calls, notConfigured.Gaps)
	}
	healthy := BuildSnapshot(context.Background(), sources, Config{Scope: ScopeAll, WorkspaceID: "hq"}, "local", time.Now())
	if source.calls != 1 || len(healthy.FollowUps) != 0 || len(healthy.Gaps) != 0 {
		t.Fatalf("healthy empty calls=%d followups=%v gaps=%v", source.calls, healthy.FollowUps, healthy.Gaps)
	}
	source.err = errors.New("database unavailable")
	failed := BuildSnapshot(context.Background(), sources, Config{Scope: ScopeAll, WorkspaceID: "hq"}, "local", time.Now())
	if len(failed.Gaps) != 1 || failed.Gaps[0] != "Personal HQ follow-ups could not be read" {
		t.Fatalf("failed gaps=%v", failed.Gaps)
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
	}, FollowUps: []FollowUpSnapshot{{Ref: SourceRef{WorkspaceID: "hq", EntityType: "follow_up", EntityID: "f1"}}}}
	refs := snap.AllRefs()
	if len(refs) != 5 {
		t.Fatalf("expected 5 refs, got %d: %+v", len(refs), refs)
	}
}

package dailybrief

import (
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

type summaryWorkspaceSource struct {
	*slimListingWorkspaceSource
	summaries    []workspace.WorkspaceSummary
	summaryErr   error
	summaryCalls int
	legacyCalls  int
}

func (s *summaryWorkspaceSource) ListActiveSummaries() ([]workspace.WorkspaceSummary, error) {
	s.summaryCalls++
	return s.summaries, s.summaryErr
}

func (s *summaryWorkspaceSource) ListActive() ([]*workspace.Workspace, error) {
	s.legacyCalls++
	return nil, errors.New("legacy workspace listing must not be used")
}

func TestResolveWorkspaceScope_PrefersSummariesAndHydratesCandidates(t *testing.T) {
	full := newTestWorkspace("ws-a", "Current name", "workspace", workspace.StatusActive, "local")
	full.Tasks = []workspace.Task{{ID: "task-a"}}
	full.FolderSlug = "current-name"
	source := &summaryWorkspaceSource{
		slimListingWorkspaceSource: &slimListingWorkspaceSource{
			full: map[string]*workspace.Workspace{full.ID: full}, getCalls: map[string]int{},
		},
		// Summary metadata can be stale; only hydrated records decide access,
		// status, payload, and the display/navigation fields.
		summaries: []workspace.WorkspaceSummary{
			{ID: full.ID, Name: "Stale name", Status: workspace.StatusTrashed, OwnerUserID: "foreign"},
			{ID: full.ID, Name: "Duplicate"},
		},
	}
	got := ResolveWorkspaceScope(source, Config{Scope: ScopeAll, IncludeFutureWorkspaces: true}, "local")
	if !got.Complete || len(got.Workspaces) != 1 {
		t.Fatalf("summary candidates were not hydrated: %+v", got)
	}
	if got.Workspaces[0].Name != full.Name || got.Workspaces[0].Slug != full.FolderSlug || len(got.Workspaces[0].Workspace.Tasks) != 1 {
		t.Fatalf("summary replaced the authoritative workspace: %+v", got.Workspaces[0])
	}
	if source.summaryCalls != 1 || source.legacyCalls != 0 || source.getCalls[full.ID] != 1 {
		t.Fatalf("listing or hydration calls = summaries:%d legacy:%d get:%v", source.summaryCalls, source.legacyCalls, source.getCalls)
	}
}

func TestResolveWorkspaceScope_SummaryFailureDoesNotFallBackOrExposeDetails(t *testing.T) {
	source := &summaryWorkspaceSource{
		slimListingWorkspaceSource: &slimListingWorkspaceSource{},
		summaryErr:                 errors.New("private backing store details"),
	}
	got := ResolveWorkspaceScope(source, Config{Scope: ScopeAll}, "local")
	if got.Complete || len(got.Gaps) != 1 || got.Gaps[0] != "workspace list is unavailable" || source.summaryCalls != 1 || source.legacyCalls != 0 {
		t.Fatalf("summary failure was hidden or leaked: %+v, summary:%d legacy:%d", got, source.summaryCalls, source.legacyCalls)
	}
}

func TestResolveWorkspaceScope_SelectedIDsSkipBothListingAPIs(t *testing.T) {
	full := newTestWorkspace("ws-a", "Selected", "workspace", workspace.StatusActive, "local")
	source := &summaryWorkspaceSource{
		slimListingWorkspaceSource: &slimListingWorkspaceSource{full: map[string]*workspace.Workspace{full.ID: full}},
		summaryErr:                 errors.New("listing unavailable"),
	}
	got := ResolveWorkspaceScope(source, Config{Scope: ScopeSelected, SelectedWorkspaceIDs: []string{full.ID}}, "local")
	if !got.Complete || len(got.Workspaces) != 1 || source.summaryCalls != 0 || source.legacyCalls != 0 {
		t.Fatalf("selected scope unnecessarily listed workspaces: %+v", got)
	}
}

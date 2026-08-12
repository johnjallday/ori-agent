package agents

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/config"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/github"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/herdr"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/model"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/worktree"
)

type fakeIssues struct {
	issue github.Issue
	err   error
	calls int
}

func (f *fakeIssues) GetIssue(_ context.Context, _ int) (github.Issue, error) {
	f.calls++
	if f.err != nil {
		return github.Issue{}, f.err
	}
	return f.issue, nil
}

func readyIssue(number int, title string, route IssueRoute) github.Issue {
	return github.Issue{
		Number:    number,
		Title:     title,
		Body:      "issue body for #" + strconv.Itoa(number),
		URL:       "https://github.com/johnjallday/ori-agent/issues/" + strconv.Itoa(number),
		State:     "open",
		Labels:    []string{"backlog", "size:" + string(route)},
		FetchedAt: time.Date(2026, time.August, 12, 12, 0, 0, 0, time.UTC),
	}
}

func newPlanningService(client *fakeHerdr, store *memoryStore, devPath string, issues IssueFetcher) *Service {
	cfg := config.Default()
	cfg.Bootstrap.TimeoutSeconds = 3
	now := time.Date(2026, time.August, 12, 12, 0, 0, 0, time.UTC)
	return &Service{
		Config:       cfg,
		RepositoryID: "repo-123456",
		GitCommonDir: "/tmp/common.git",
		Client:       client,
		Store:        store,
		Inspector:    fakeInspector{worktree: worktree.GitWorktree{Path: devPath, Branch: "dev", CommonDir: "/tmp/common.git", SourcePath: "/tmp/source-checkout"}},
		Issues:       issues,
		Now:          func() time.Time { return now },
	}
}

func TestDeriveSlug(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		number int
		title  string
		want   string
	}{
		{"simple", 342, "Ready issue codex planning", "342-ready-issue-codex-planning"},
		{"punctuation and repeats", 12, "Fix: the  --weird,,punctuation!!", "12-fix-the-weird-punctuation"},
		{"uppercase", 7, "UPPER CASE Title", "7-upper-case-title"},
		{"leading dash like content", 9, "-- leading dashes --", "9-leading-dashes"},
		{"unicode only falls back", 5, "日本語のタイトル", "5-issue"},
		{"empty title falls back", 3, "", "3-issue"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := DeriveSlug(tc.number, tc.title)
			if got != tc.want {
				t.Fatalf("DeriveSlug(%d, %q) = %q, want %q", tc.number, tc.title, got, tc.want)
			}
			if !planningValidSlugForTest(got) {
				t.Fatalf("DeriveSlug(%d, %q) = %q is not a canonical slug", tc.number, tc.title, got)
			}
		})
	}

	long := strings.Repeat("very long title words ", 20)
	got := DeriveSlug(999999, long)
	if len(got) > 80 {
		t.Fatalf("DeriveSlug produced a slug longer than 80 characters: %d", len(got))
	}
	if !strings.HasPrefix(got, "999999-") {
		t.Fatalf("DeriveSlug(999999, ...) = %q, want number-first prefix", got)
	}
}

// planningValidSlugForTest avoids importing the planning package's ValidSlug
// twice under two names in this file; it mirrors the exact pattern.
func planningValidSlugForTest(slug string) bool {
	if slug == "" || len(slug) > 80 {
		return false
	}
	for index, r := range slug {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '-' && index > 0:
		default:
			return false
		}
	}
	return true
}

func TestIssueIsReadyMatchesDevopsShLabelsAreReady(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		labels []string
		want   bool
	}{
		{"approved dominates even with backlog", []string{"backlog", "approved"}, false},
		{"feature-proposal alone is ready", []string{"feature-proposal"}, true},
		{"backlog not bundled is ready", []string{"backlog"}, true},
		{"backlog bundled is not ready", []string{"backlog", "bundled"}, false},
		{"neither backlog nor proposal", []string{"needs-decision"}, false},
		{"empty labels", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := issueIsReady(tc.labels); got != tc.want {
				t.Fatalf("issueIsReady(%v) = %v, want %v", tc.labels, got, tc.want)
			}
		})
	}
}

func TestIssueRouteCountsSizeLabels(t *testing.T) {
	t.Parallel()
	if route, count := issueRoute([]string{"backlog"}); count != 0 || route != "" {
		t.Fatalf("no size label = %q, %d", route, count)
	}
	if route, count := issueRoute([]string{"size:quick"}); count != 1 || route != RouteQuick {
		t.Fatalf("size:quick = %q, %d", route, count)
	}
	if route, count := issueRoute([]string{"size:planned", "size:prd"}); count != 2 {
		t.Fatalf("duplicate size labels = %q, %d, want count 2", route, count)
	}
}

func TestBuildIssuePlanRoutesBySizeAndTouchesNoFiles(t *testing.T) {
	t.Parallel()
	for _, route := range []IssueRoute{RouteQuick, RoutePlanned, RoutePRD} {
		t.Run(string(route), func(t *testing.T) {
			t.Parallel()
			devPath := t.TempDir()
			issues := &fakeIssues{issue: readyIssue(342, "Ready issue codex planning", route)}
			client := newFakeHerdr(devPath)
			service := newPlanningService(client, newMemoryStore(), devPath, issues)

			plan, err := service.BuildIssuePlan(context.Background(), IssuePlanRequest{IssueNumber: 342, DevWorktreePath: devPath})
			if err != nil {
				t.Fatalf("BuildIssuePlan() error = %v", err)
			}
			if issues.calls != 1 {
				t.Fatalf("GetIssue called %d times, want exactly 1", issues.calls)
			}
			if plan.Route != route || plan.Slug != "342-ready-issue-codex-planning" {
				t.Fatalf("plan = %#v", plan)
			}
			if !plan.Startable() || plan.ArtifactState != IssueArtifactNone {
				t.Fatalf("fresh plan artifact state = %q, startable=%v", plan.ArtifactState, plan.Startable())
			}
			if (plan.PRDPath != "") != (route == RoutePRD) {
				t.Fatalf("PRDPath = %q for route %q", plan.PRDPath, route)
			}
			entries, _ := os.ReadDir(filepath.Join(devPath, "tasks"))
			if len(entries) != 0 {
				t.Fatalf("BuildIssuePlan must not write files; found %d entries", len(entries))
			}
			if client.tabCreateCalls != 0 || client.startCalls != 0 {
				t.Fatalf("BuildIssuePlan must not contact Herdr beyond the focused-workspace hint; tabs=%d starts=%d", client.tabCreateCalls, client.startCalls)
			}
		})
	}
}

func TestBuildIssuePlanRejectsIneligibleIssuesWithoutMutation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		issue github.Issue
	}{
		{"closed", func() github.Issue { i := readyIssue(1, "closed one", RouteQuick); i.State = "closed"; return i }()},
		{"not ready: approved", func() github.Issue {
			i := readyIssue(2, "approved one", RouteQuick)
			i.Labels = []string{"backlog", "approved", "size:quick"}
			return i
		}()},
		{"not ready: bundled", func() github.Issue {
			i := readyIssue(3, "bundled one", RouteQuick)
			i.Labels = []string{"backlog", "bundled", "size:quick"}
			return i
		}()},
		{"missing size label", func() github.Issue {
			i := readyIssue(4, "no size", RouteQuick)
			i.Labels = []string{"backlog"}
			return i
		}()},
		{"duplicate size labels", func() github.Issue {
			i := readyIssue(5, "two sizes", RouteQuick)
			i.Labels = []string{"backlog", "size:quick", "size:prd"}
			return i
		}()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			devPath := t.TempDir()
			issues := &fakeIssues{issue: tc.issue}
			client := newFakeHerdr(devPath)
			service := newPlanningService(client, newMemoryStore(), devPath, issues)

			_, err := service.BuildIssuePlan(context.Background(), IssuePlanRequest{IssueNumber: tc.issue.Number, DevWorktreePath: devPath})
			var stage *model.StageError
			if !errors.As(err, &stage) || stage.Code != model.ErrIssueIneligible {
				t.Fatalf("BuildIssuePlan() error = %v, want ErrIssueIneligible", err)
			}
			entries, _ := os.ReadDir(filepath.Join(devPath, "tasks"))
			if len(entries) != 0 {
				t.Fatalf("an ineligible Issue must never write files")
			}
		})
	}
}

func TestBuildIssuePlanValidatesExactDevWorktreeBeforeFetchingIssue(t *testing.T) {
	t.Parallel()
	devPath := t.TempDir()
	issues := &fakeIssues{issue: readyIssue(342, "title", RouteQuick)}
	client := newFakeHerdr(devPath)
	service := newPlanningService(client, newMemoryStore(), devPath, issues)
	service.Inspector = fakeInspector{err: errors.New("not a linked dev worktree")}

	_, err := service.BuildIssuePlan(context.Background(), IssuePlanRequest{IssueNumber: 342, DevWorktreePath: devPath})
	var stage *model.StageError
	if !errors.As(err, &stage) || stage.Code != model.ErrWorktreeInvalid {
		t.Fatalf("BuildIssuePlan() error = %v, want ErrWorktreeInvalid", err)
	}
	if issues.calls != 0 {
		t.Fatalf("GetIssue was called %d times before worktree validation failed; want 0", issues.calls)
	}
}

func TestExecuteIssuePlanWritesFilesAndIsIdempotentOnRerun(t *testing.T) {
	t.Parallel()
	devPath := t.TempDir()
	issues := &fakeIssues{issue: readyIssue(342, "Ready issue codex planning", RoutePlanned)}
	client := newFakeHerdr(devPath)
	store := newMemoryStore()
	service := newPlanningService(client, store, devPath, issues)

	plan, err := service.BuildIssuePlan(context.Background(), IssuePlanRequest{IssueNumber: 342, DevWorktreePath: devPath})
	if err != nil {
		t.Fatalf("BuildIssuePlan() error = %v", err)
	}
	result, err := service.ExecuteIssuePlan(context.Background(), plan)
	if err != nil {
		t.Fatalf("ExecuteIssuePlan() error = %v", err)
	}
	if !result.SnapshotWritten || !result.StarterWritten || result.Degraded {
		t.Fatalf("first execute result = %#v", result)
	}
	if !result.PromptDelivered || client.tabCreateCalls != 1 || client.startCalls != 1 || client.promptCalls != 1 {
		t.Fatalf("first execute did not place/start/prompt exactly once: %#v tabs=%d starts=%d prompts=%d", result, client.tabCreateCalls, client.startCalls, client.promptCalls)
	}
	snapshot, err := os.ReadFile(plan.SnapshotPath)
	if err != nil || !strings.Contains(string(snapshot), "ori-devflow: issue-snapshot; issue=342") {
		t.Fatalf("snapshot content = %q, err=%v", snapshot, err)
	}
	starter, err := os.ReadFile(plan.TaskListPath)
	if err != nil || !strings.Contains(string(starter), PlanningStarterMarker) {
		t.Fatalf("starter content = %q, err=%v", starter, err)
	}

	// Re-run BuildIssuePlan: the snapshot and starter both already exist, so a
	// re-plan resumes rather than overwriting them.
	second, err := service.BuildIssuePlan(context.Background(), IssuePlanRequest{IssueNumber: 342, DevWorktreePath: devPath})
	if err != nil {
		t.Fatalf("second BuildIssuePlan() error = %v", err)
	}
	if second.ArtifactState != IssueArtifactResume {
		t.Fatalf("second plan artifact state = %q, want resume", second.ArtifactState)
	}

	secondResult, err := service.ExecuteIssuePlan(context.Background(), second)
	if err != nil {
		t.Fatalf("second ExecuteIssuePlan() error = %v", err)
	}
	if secondResult.SnapshotWritten || secondResult.StarterWritten {
		t.Fatalf("rerun must never rewrite existing planning files: %#v", secondResult)
	}
	if !secondResult.PromptSkipped || client.tabCreateCalls != 1 || client.startCalls != 1 || client.promptCalls != 1 {
		t.Fatalf("rerun must reuse the exact tab/planner/prompt, not duplicate them: tabs=%d starts=%d prompts=%d", client.tabCreateCalls, client.startCalls, client.promptCalls)
	}

	state, _ := store.Load()
	if len(state.Features) != 0 {
		t.Fatalf("a planning session must never appear in BridgeState.Features: %#v", state.Features)
	}
	session, ok := state.PlanningSessions["repo-123456:342"]
	if !ok || session.Stage != model.PlanningPrompted || session.Slug != "342-ready-issue-codex-planning" {
		t.Fatalf("planning session record = %#v, ok=%v", session, ok)
	}
}

func TestExecuteIssuePlanNeverOverwritesARealTaskList(t *testing.T) {
	t.Parallel()
	devPath := t.TempDir()
	tasksDir := filepath.Join(devPath, "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	realList := "# Tasks: 342-ready-issue-codex-planning\n\n- [ ] 1.1 Real work already planned by hand.\n"
	if err := os.WriteFile(filepath.Join(tasksDir, "tasks-342-ready-issue-codex-planning.md"), []byte(realList), 0o644); err != nil {
		t.Fatal(err)
	}
	issues := &fakeIssues{issue: readyIssue(342, "Ready issue codex planning", RoutePlanned)}
	client := newFakeHerdr(devPath)
	service := newPlanningService(client, newMemoryStore(), devPath, issues)

	plan, err := service.BuildIssuePlan(context.Background(), IssuePlanRequest{IssueNumber: 342, DevWorktreePath: devPath})
	if err != nil {
		t.Fatalf("BuildIssuePlan() error = %v", err)
	}
	if plan.ArtifactState != IssueArtifactComplete || plan.Startable() {
		t.Fatalf("plan with a real task list already present = %#v", plan)
	}
	after, err := os.ReadFile(filepath.Join(tasksDir, "tasks-342-ready-issue-codex-planning.md"))
	if err != nil || string(after) != realList {
		t.Fatalf("the real task list must be left untouched: %q, err=%v", after, err)
	}
}

func TestExecuteIssuePlanDoesNotOverwriteAnExistingPRD(t *testing.T) {
	t.Parallel()
	devPath := t.TempDir()
	tasksDir := filepath.Join(devPath, "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	realPRD := "# PRD: 342-ready-issue-codex-planning\n\nAlready written by hand.\n"
	if err := os.WriteFile(filepath.Join(tasksDir, "prd-342-ready-issue-codex-planning.md"), []byte(realPRD), 0o644); err != nil {
		t.Fatal(err)
	}
	issues := &fakeIssues{issue: readyIssue(342, "Ready issue codex planning", RoutePRD)}
	client := newFakeHerdr(devPath)
	service := newPlanningService(client, newMemoryStore(), devPath, issues)

	plan, err := service.BuildIssuePlan(context.Background(), IssuePlanRequest{IssueNumber: 342, DevWorktreePath: devPath})
	if err != nil {
		t.Fatalf("BuildIssuePlan() error = %v", err)
	}
	if plan.ArtifactState != IssueArtifactPRDExists || !plan.ExistingPRD || !plan.Startable() {
		t.Fatalf("plan with existing PRD = %#v", plan)
	}
	if strings.Contains(plan.starterContent, "Write `tasks/prd-") {
		t.Fatalf("starter for an existing PRD must not ask Codex to write the PRD again: %s", plan.starterContent)
	}
	after, err := os.ReadFile(filepath.Join(tasksDir, "prd-342-ready-issue-codex-planning.md"))
	if err != nil || string(after) != realPRD {
		t.Fatalf("the existing PRD must be left untouched: %q, err=%v", after, err)
	}
}

func TestResolveIssueSlugReusesExistingIdentityAfterRename(t *testing.T) {
	t.Parallel()
	devPath := t.TempDir()
	tasksDir := filepath.Join(devPath, "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	oldSlug := "342-old-title"
	snapshot := "# Issue #342: old title\n\n<!-- ori-devflow: issue-snapshot; issue=342 -->\n\nresumed\n"
	if err := os.WriteFile(filepath.Join(tasksDir, "issue-"+oldSlug+".md"), []byte(snapshot), 0o644); err != nil {
		t.Fatal(err)
	}
	issues := &fakeIssues{issue: readyIssue(342, "Brand New Renamed Title", RoutePlanned)}
	client := newFakeHerdr(devPath)
	service := newPlanningService(client, newMemoryStore(), devPath, issues)

	plan, err := service.BuildIssuePlan(context.Background(), IssuePlanRequest{IssueNumber: 342, DevWorktreePath: devPath})
	if err != nil {
		t.Fatalf("BuildIssuePlan() error = %v", err)
	}
	if plan.Slug != oldSlug {
		t.Fatalf("slug = %q, want the reused pre-rename slug %q", plan.Slug, oldSlug)
	}
}

func TestResolveIssueSlugFailsClosedOnAmbiguousIdentity(t *testing.T) {
	t.Parallel()
	devPath := t.TempDir()
	tasksDir := filepath.Join(devPath, "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tasksDir, "issue-342-first-slug.md"), []byte("# Issue #342: a\n\n<!-- ori-devflow: issue-snapshot; issue=342 -->\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tasksDir, "prd-342-second-slug.md"), []byte("# PRD\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	issues := &fakeIssues{issue: readyIssue(342, "some title", RoutePRD)}
	client := newFakeHerdr(devPath)
	service := newPlanningService(client, newMemoryStore(), devPath, issues)

	_, err := service.BuildIssuePlan(context.Background(), IssuePlanRequest{IssueNumber: 342, DevWorktreePath: devPath})
	if err == nil || !strings.Contains(err.Error(), "more than one candidate feature slug") {
		t.Fatalf("BuildIssuePlan() error = %v, want an ambiguous-identity failure", err)
	}
}

func TestSnapshotCollisionWithADifferentIssueIsNeverAdopted(t *testing.T) {
	t.Parallel()
	devPath := t.TempDir()
	tasksDir := filepath.Join(devPath, "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	slug := "342-ready-issue-codex-planning"
	foreign := "# Issue #999: unrelated\n\n<!-- ori-devflow: issue-snapshot; issue=999 -->\n"
	if err := os.WriteFile(filepath.Join(tasksDir, "issue-"+slug+".md"), []byte(foreign), 0o644); err != nil {
		t.Fatal(err)
	}
	issues := &fakeIssues{issue: readyIssue(342, "Ready issue codex planning", RoutePlanned)}
	client := newFakeHerdr(devPath)
	service := newPlanningService(client, newMemoryStore(), devPath, issues)

	_, err := service.BuildIssuePlan(context.Background(), IssuePlanRequest{IssueNumber: 342, DevWorktreePath: devPath})
	if err == nil || !strings.Contains(err.Error(), "does not describe Issue #342") {
		t.Fatalf("BuildIssuePlan() error = %v, want a snapshot-collision failure", err)
	}
	after, readErr := os.ReadFile(filepath.Join(tasksDir, "issue-"+slug+".md"))
	if readErr != nil || string(after) != foreign {
		t.Fatalf("the conflicting snapshot must be left untouched: %q, err=%v", after, readErr)
	}
}

func TestSameDevWorktreeConcurrentPlannersGetDistinctTabsAndNames(t *testing.T) {
	t.Parallel()
	devPath := t.TempDir()
	store := newMemoryStore()
	client := newFakeHerdr(devPath)

	first := newPlanningService(client, store, devPath, &fakeIssues{issue: readyIssue(100, "first issue", RouteQuick)})
	second := newPlanningService(client, store, devPath, &fakeIssues{issue: readyIssue(200, "second issue", RouteQuick)})

	planOne, err := first.BuildIssuePlan(context.Background(), IssuePlanRequest{IssueNumber: 100, DevWorktreePath: devPath})
	if err != nil {
		t.Fatalf("BuildIssuePlan(100) error = %v", err)
	}
	resultOne, err := first.ExecuteIssuePlan(context.Background(), planOne)
	if err != nil {
		t.Fatalf("ExecuteIssuePlan(100) error = %v", err)
	}
	planTwo, err := second.BuildIssuePlan(context.Background(), IssuePlanRequest{IssueNumber: 200, DevWorktreePath: devPath})
	if err != nil {
		t.Fatalf("BuildIssuePlan(200) error = %v", err)
	}
	resultTwo, err := second.ExecuteIssuePlan(context.Background(), planTwo)
	if err != nil {
		t.Fatalf("ExecuteIssuePlan(200) error = %v", err)
	}

	if resultOne.TabID == resultTwo.TabID {
		t.Fatalf("two Issue planners in the same dev worktree shared a tab: %q", resultOne.TabID)
	}
	if resultOne.Planner.Name == resultTwo.Planner.Name {
		t.Fatalf("two Issue planners shared one agent name: %q", resultOne.Planner.Name)
	}
	if client.tabCreateCalls != 2 {
		t.Fatalf("tabCreateCalls = %d, want 2 distinct tabs", client.tabCreateCalls)
	}

	state, _ := store.Load()
	if len(state.PlanningSessions) != 2 {
		t.Fatalf("planning sessions = %d, want 2", len(state.PlanningSessions))
	}
}

// TestExecuteIssuePlanRecreatesTheTabWhenTheRecordedOneIsClosed proves partial
// -stage recovery: a user closing the planner's tab (state saved, tab gone)
// must not fail the next wt plan invocation. It resumes by placing a fresh
// tab and adopting/starting the planner again, exactly mirroring feature
// handoff's already-proven recovery contract in service_test.go.
func TestExecuteIssuePlanRecreatesTheTabWhenTheRecordedOneIsClosed(t *testing.T) {
	t.Parallel()
	devPath := t.TempDir()
	issues := &fakeIssues{issue: readyIssue(342, "Ready issue codex planning", RoutePlanned)}
	client := newFakeHerdr(devPath)
	store := newMemoryStore()
	service := newPlanningService(client, store, devPath, issues)

	plan, err := service.BuildIssuePlan(context.Background(), IssuePlanRequest{IssueNumber: 342, DevWorktreePath: devPath})
	if err != nil {
		t.Fatalf("BuildIssuePlan() error = %v", err)
	}
	if _, err := service.ExecuteIssuePlan(context.Background(), plan); err != nil {
		t.Fatalf("first ExecuteIssuePlan() error = %v", err)
	}

	// Close the tab and forget the planner, the way a user closing a tab does.
	delete(client.tabs, "w1:t1")
	delete(client.panes, "w1:p1")
	client.agents = nil
	client.byName = make(map[string]herdr.AgentInfo)
	state, _ := store.Load()
	session := state.PlanningSessions["repo-123456:342"]
	session.Planner = model.RoleAgent{}
	state.PlanningSessions["repo-123456:342"] = session
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}

	second, err := service.BuildIssuePlan(context.Background(), IssuePlanRequest{IssueNumber: 342, DevWorktreePath: devPath})
	if err != nil {
		t.Fatalf("second BuildIssuePlan() error = %v", err)
	}
	recovered, err := service.ExecuteIssuePlan(context.Background(), second)
	if err != nil {
		t.Fatalf("ExecuteIssuePlan() after the tab was closed = %v", err)
	}
	if client.tabCreateCalls != 2 || recovered.TabID != "w1:t2" {
		t.Fatalf("recovery = %d creates, tab %q; want a fresh tab", client.tabCreateCalls, recovered.TabID)
	}
	after, _ := store.Load()
	if after.PlanningSessions["repo-123456:342"].TabID != "w1:t2" {
		t.Fatalf("state kept the closed tab: %#v", after.PlanningSessions["repo-123456:342"])
	}
	if !recovered.PromptDelivered && !recovered.PromptSkipped {
		t.Fatalf("recovery neither delivered nor explicitly skipped a prompt: %#v", recovered)
	}
}

// TestPlanningSessionCannotSeedOrOverrideFeatureHandoffKind proves AR18/AR31:
// a planning session's fixed "codex" kind must never leak into an ordinary
// feature handoff for a same-named feature. Feature handoff never reads
// PlanningSessions at all, so this is true by construction; the test pins
// that down as an explicit regression rather than an implicit property.
func TestPlanningSessionCannotSeedOrOverrideFeatureHandoffKind(t *testing.T) {
	t.Parallel()
	devPath := t.TempDir()
	client := newFakeHerdr(devPath)
	store := newMemoryStore()

	planner := newPlanningService(client, store, devPath, &fakeIssues{issue: readyIssue(342, "shared name", RoutePlanned)})
	plan, err := planner.BuildIssuePlan(context.Background(), IssuePlanRequest{IssueNumber: 342, DevWorktreePath: devPath})
	if err != nil {
		t.Fatalf("BuildIssuePlan() error = %v", err)
	}
	if _, err := planner.ExecuteIssuePlan(context.Background(), plan); err != nil {
		t.Fatalf("ExecuteIssuePlan() error = %v", err)
	}
	state, _ := store.Load()
	if state.PlanningSessions["repo-123456:342"].Planner.Kind != "codex" {
		t.Fatalf("fixture is invalid: expected a recorded codex planner, got %#v", state.PlanningSessions["repo-123456:342"])
	}

	// A feature happens to share the same slug the planning session derived.
	// Its own handoff must still resolve to the configured default kind
	// (claude), never the planning session's codex.
	feature := newService(client, store, devPath)
	result, err := feature.Handoff(context.Background(), HandoffRequest{FeatureName: plan.Slug, WorktreePath: devPath, Branch: "feature/bridge"})
	if err != nil {
		t.Fatalf("Handoff() error = %v", err)
	}
	if result.Primary.Kind != "claude" {
		t.Fatalf("feature handoff kind = %q, want claude (the planning session's codex kind must not leak)", result.Primary.Kind)
	}
	after, _ := store.Load()
	featureRecord := after.Features["repo-123456:"+plan.Slug]
	if featureRecord.Handoff.PrimaryKind != "claude" {
		t.Fatalf("stored feature handoff kind = %q, want claude", featureRecord.Handoff.PrimaryKind)
	}
}

func TestPlanningBootstrapPromptNamesArtifactsForbidsImplementationAndOmitsIssueBody(t *testing.T) {
	t.Parallel()
	plan := IssuePlan{
		IssueNumber:     342,
		Slug:            "342-ready-issue-codex-planning",
		Route:           RoutePlanned,
		DevWorktreePath: "/tmp/ori-agent-dev",
		SnapshotPath:    "/tmp/ori-agent-dev/tasks/issue-342-ready-issue-codex-planning.md",
		TaskListPath:    "/tmp/ori-agent-dev/tasks/tasks-342-ready-issue-codex-planning.md",
	}
	prompt := PlanningBootstrapPrompt(plan)
	for _, want := range []string{
		"AGENTS.md",
		plan.SnapshotPath,
		plan.TaskListPath,
		"size:planned",
		"Do not implement",
		"wt start 342-ready-issue-codex-planning",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("PlanningBootstrapPrompt missing %q: %s", want, prompt)
		}
	}
	if strings.Contains(prompt, "issue body") {
		t.Fatalf("PlanningBootstrapPrompt must never embed Issue content, only reference its snapshot path: %s", prompt)
	}
}

func TestRenderIssueSnapshotPreservesHostileContentVerbatimButStripsControlBytes(t *testing.T) {
	t.Parallel()
	hostile := "line one\nline two with `backticks` and $(rm -rf /) and \"quotes\" and --leading-dash\n\ttabbed\nline\x1b[31mred\x1b[0mtext\x00null"
	issue := github.Issue{
		Number:    342,
		Title:     "hostile title",
		Body:      hostile,
		URL:       "https://example.invalid/342",
		State:     "open",
		Labels:    []string{"backlog", "size:quick"},
		FetchedAt: time.Now(),
	}
	rendered := RenderIssueSnapshot(issue)
	for _, want := range []string{"`backticks`", "$(rm -rf /)", "\"quotes\"", "--leading-dash", "\ttabbed"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered snapshot dropped inert content %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "\x1b[31m") || strings.Contains(rendered, "\x00") {
		t.Fatalf("rendered snapshot must strip raw ANSI/control bytes:\n%q", rendered)
	}
}

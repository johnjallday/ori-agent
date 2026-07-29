package overview

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/github"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/herdr"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/model"
)

// This file is the hermetic reproduction of the reported `wt herd status`
// gaps. One fixture carries every shape the roster has to survive at once,
// because the reported failures only appear together: a repository-level agent
// in the primary checkout, a managed feature worktree with several agents, an
// unmanaged worktree, a saved feature whose worktree is gone, an occupied pane
// running nothing, and a legacy bridge record pointing at a Herdr workspace
// that was deleted months ago.
//
// Nothing here touches the real repository, Git, GitHub, or Herdr: the Git
// listing is an injected runner, and the agent, workspace, and bridge facts are
// deterministic fixtures.

const (
	scenarioPrimarySlugLike  = "ori-agent-dev"
	scenarioManagedFeature   = "managed-feature"
	scenarioUnmanagedFeature = "unmanaged-feature"
	scenarioMissingFeature   = "missing-feature"
	scenarioStaleFeature     = "stale-workspace-feature"
	// scenarioDeletedWorkspace is the historical workspace ID that aborted a
	// metadata refresh in the field; Herdr no longer knows it.
	scenarioDeletedWorkspace = "w12"
)

// herdScenario is one repository observed from every surface at once. Its
// layout mirrors the real one: the source checkout sits on `main` and the
// baseline `dev` checkout is a linked worktree beside the feature worktrees.
type herdScenario struct {
	// Root is the temporary directory holding every checkout.
	Root string
	// Source is the repository's normal checkout, on main.
	Source string
	// Primary is the baseline `dev` checkout. Agents run here even though it is
	// not a feature worktree.
	Primary string
	// Managed is the feature worktree with saved bridge roles.
	Managed string
	// Unmanaged is a feature worktree whose agent no bridge role claims.
	Unmanaged string
	// Removed is a saved feature path that no longer exists on disk.
	Removed string
	// Stale is the legacy workspace-backed feature path that no longer exists.
	Stale string

	agents *fakeAgents
	bridge *fakeBridge
	remote *fakeRemote
	git    func(context.Context, string, ...string) (string, error)
}

func newHerdScenario(t *testing.T) *herdScenario {
	t.Helper()
	root := t.TempDir()
	scenario := &herdScenario{
		Root:      root,
		Source:    filepath.Join(root, "ori-agent"),
		Primary:   filepath.Join(root, "worktrees", scenarioPrimarySlugLike),
		Managed:   filepath.Join(root, "worktrees", scenarioManagedFeature),
		Unmanaged: filepath.Join(root, "worktrees", scenarioUnmanagedFeature),
		Removed:   filepath.Join(root, "worktrees", scenarioMissingFeature),
		Stale:     filepath.Join(root, "worktrees", scenarioStaleFeature),
	}

	// The source checkout owns a .git directory, which is how the inventory
	// tells it from a linked worktree.
	mkdirAll(t, filepath.Join(scenario.Source, ".git"))
	mkdirAll(t, filepath.Join(scenario.Primary, "tasks"))
	writeFixtureFile(t, filepath.Join(scenario.Primary, ".git"), "gitdir: "+filepath.Join(scenario.Source, ".git")+"\n")
	for _, slug := range []string{
		scenarioManagedFeature, scenarioUnmanagedFeature, scenarioMissingFeature, scenarioStaleFeature,
	} {
		writeFixtureFile(t, filepath.Join(scenario.Primary, "tasks", "prd-"+slug+".md"), "# PRD: "+slug+"\n")
		writeFixtureFile(t, filepath.Join(scenario.Primary, "tasks", "tasks-"+slug+".md"), scenarioPlan(slug))
	}

	for _, checkout := range []string{scenario.Managed, scenario.Unmanaged} {
		mkdirAll(t, filepath.Join(checkout, "tasks"))
		// A linked worktree carries a .git file, never a directory.
		writeFixtureFile(t, filepath.Join(checkout, ".git"), "gitdir: "+filepath.Join(scenario.Source, ".git")+"\n")
		slug := filepath.Base(checkout)
		writeFixtureFile(t, filepath.Join(checkout, "tasks", "prd-"+slug+".md"), "# PRD: "+slug+"\n")
		writeFixtureFile(t, filepath.Join(checkout, "tasks", "tasks-"+slug+".md"), scenarioPlan(slug))
	}

	listing := strings.Join([]string{
		"worktree " + scenario.Source,
		"HEAD 0000000000000000000000000000000000000000",
		"branch refs/heads/main",
		"",
		"worktree " + scenario.Primary,
		"HEAD 1111111111111111111111111111111111111111",
		"branch refs/heads/dev",
		"",
		"worktree " + scenario.Managed,
		"HEAD 2222222222222222222222222222222222222222",
		"branch refs/heads/feature/" + scenarioManagedFeature,
		"",
		"worktree " + scenario.Unmanaged,
		"HEAD 3333333333333333333333333333333333333333",
		"branch refs/heads/feature/" + scenarioUnmanagedFeature,
		"",
	}, "\n")
	scenario.git = func(_ context.Context, _ string, args ...string) (string, error) {
		if len(args) >= 2 && args[0] == "worktree" && args[1] == "list" {
			return listing, nil
		}
		return "", nil
	}

	scenario.agents = &fakeAgents{
		live:       scenario.liveAgents(),
		workspaces: scenario.workspaces(),
	}
	scenario.bridge = &fakeBridge{state: scenario.bridgeState()}
	scenario.remote = &fakeRemote{result: github.Result{ObservedAt: observed}}
	return scenario
}

// scenarioPlan is a task list with implementation work outstanding, so every
// feature in the fixture has a next actionable subtask to report.
func scenarioPlan(slug string) string {
	return "# Tasks: " + slug + "\n\n" +
		"- [x] 1.0 Groundwork\n" +
		"  - [x] 1.1 Land the groundwork\n" +
		"- [ ] 2.0 Implementation\n" +
		"  - [ ] 2.1 Continue " + slug + "\n" +
		"  - [ ] 2.2 Demo: drive the new surface\n"
}

// liveAgents is what `herdr agent list` reports for this repository. Two of
// them run in the primary checkout, which is exactly the population the
// feature-first roster drops today.
func (s *herdScenario) liveAgents() []herdr.AgentInfo {
	return []herdr.AgentInfo{
		{
			WorkspaceID: "w-dev", PaneID: "w-dev:p1", TerminalID: "term-dev-1", TabID: "w-dev:t1",
			Name: "ori-dev-claude", Agent: "claude", AgentStatus: model.AgentWorking,
			Cwd: s.Primary, AgentSession: session("sess-dev-claude"),
		},
		{
			// A second repository-level agent, in the source checkout on main
			// rather than the baseline worktree.
			WorkspaceID: "w-main", PaneID: "w-main:p1", TerminalID: "term-main-1", TabID: "w-main:t1",
			Name: "ori-main-codex", Agent: "codex", AgentStatus: model.AgentIdle,
			Cwd: s.Source, AgentSession: &model.NativeSession{Source: "codex", Agent: "codex", Kind: "session", Value: "sess-main-codex"},
		},
		{
			WorkspaceID: "w-managed", PaneID: "w-managed:p1", TerminalID: "term-managed-1", TabID: "w-managed:t1",
			Name: "ori-managed-builder", Agent: "claude", AgentStatus: model.AgentIdle,
			Cwd: s.Managed, AgentSession: session("sess-managed-builder"),
		},
		{
			WorkspaceID: "w-managed", PaneID: "w-managed:p2", TerminalID: "term-managed-2", TabID: "w-managed:t1",
			Name: "ori-managed-reviewer", Agent: "claude", AgentStatus: model.AgentWorking,
			Cwd: s.Managed, AgentSession: session("sess-managed-reviewer"),
		},
		{
			// An occupied pane running no agent. It is occupancy evidence and
			// must never be presented as an agent.
			WorkspaceID: "w-managed", PaneID: "w-managed:p3", TerminalID: "term-managed-3", TabID: "w-managed:t1",
			Cwd: s.Managed,
		},
		{
			WorkspaceID: "w-unmanaged", PaneID: "w-unmanaged:p1", TerminalID: "term-unmanaged-1", TabID: "w-unmanaged:t1",
			Name: "ori-unmanaged-claude", Agent: "claude", AgentStatus: model.AgentIdle,
			Cwd: s.Unmanaged, AgentSession: session("sess-unmanaged"),
		},
		{
			// A pane in a workspace that hosts a tab per feature. The workspace
			// binding cannot say which checkout this pane is in, so the agent
			// belongs to the repository but cannot be placed.
			WorkspaceID: "w-shared", PaneID: "w-shared:p1", TerminalID: "term-shared-1", TabID: "w-shared:t2",
			Name: "ori-shared-claude", Agent: "claude", AgentStatus: model.AgentIdle,
			AgentSession: session("sess-shared"),
		},
	}
}

// workspaces deliberately omits the deleted workspace: Herdr forgets a closed
// workspace, while the bridge's saved record for it survives on disk.
func (s *herdScenario) workspaces() []herdr.WorkspaceInfo {
	return []herdr.WorkspaceInfo{
		{
			WorkspaceID: "w-dev", Cwd: s.Primary, Label: scenarioPrimarySlugLike, TabCount: 1,
			Worktree: &herdr.WorktreeBinding{CheckoutPath: s.Primary, RepoRoot: s.Source, RepoKey: "repo", IsLinkedWorktree: true},
		},
		{
			WorkspaceID: "w-main", Cwd: s.Source, Label: "ori-agent", TabCount: 1,
			Worktree: &herdr.WorktreeBinding{CheckoutPath: s.Source, RepoRoot: s.Source, RepoKey: "repo"},
		},
		{
			WorkspaceID: "w-managed", Cwd: s.Managed, Label: scenarioManagedFeature, TabCount: 1,
			Worktree: &herdr.WorktreeBinding{CheckoutPath: s.Managed, RepoRoot: s.Source, RepoKey: "repo", IsLinkedWorktree: true},
		},
		{
			WorkspaceID: "w-unmanaged", Cwd: s.Unmanaged, Label: scenarioUnmanagedFeature, TabCount: 1,
			Worktree: &herdr.WorktreeBinding{CheckoutPath: s.Unmanaged, RepoRoot: s.Source, RepoKey: "repo", IsLinkedWorktree: true},
		},
		{
			// A workspace holding several feature tabs. Its worktree binding
			// still names the repository, but no longer identifies which
			// checkout any one of its panes is in.
			WorkspaceID: "w-shared", Cwd: s.Managed, Label: "ori", TabCount: 3,
			Worktree: &herdr.WorktreeBinding{CheckoutPath: s.Managed, RepoRoot: s.Source, RepoKey: "repo", IsLinkedWorktree: true},
		},
	}
}

// bridgeState is what the bridge saved on disk, including two records whose
// worktrees and workspaces are long gone.
func (s *herdScenario) bridgeState() model.BridgeState {
	state := model.NewBridgeState()

	builder := savedRole("builder", "w-managed", "w-managed:p1", "term-managed-1", "ori-managed-builder",
		func(role *model.RoleAgent) { role.NativeSession = *session("sess-managed-builder") })
	reviewer := savedRole("reviewer", "w-managed", "w-managed:p2", "term-managed-2", "ori-managed-reviewer",
		func(role *model.RoleAgent) { role.NativeSession = *session("sess-managed-reviewer") })
	state.Features["repo:"+scenarioManagedFeature] = model.FeatureState{
		Feature:     model.Feature{RepositoryID: "repo", Name: scenarioManagedFeature, Branch: "feature/" + scenarioManagedFeature, Path: s.Managed},
		WorkspaceID: "w-managed",
		TabID:       "w-managed:t1",
		Handoff:     model.HandoffState{Stage: model.HandoffReady, RootPaneID: "w-managed:p1"},
		Agents:      map[string]model.RoleAgent{"builder": builder, "reviewer": reviewer},
		UpdatedAt:   observed,
	}

	// A saved feature whose worktree was removed without `wt done`: the record
	// is history, not a live agent, and must stay visible as such.
	state.Features["repo:"+scenarioMissingFeature] = model.FeatureState{
		Feature:     model.Feature{RepositoryID: "repo", Name: scenarioMissingFeature, Branch: "feature/" + scenarioMissingFeature, Path: s.Removed},
		WorkspaceID: "w-gone",
		TabID:       "w-gone:t1",
		Handoff:     model.HandoffState{Stage: model.HandoffReady, RootPaneID: "w-gone:p1"},
		Agents: map[string]model.RoleAgent{
			"builder": savedRole("builder", "w-gone", "w-gone:p1", "term-gone-1", "ori-missing-builder"),
		},
		UpdatedAt: observed,
	}

	// The record that broke metadata publication in the field: a legacy
	// workspace-backed feature, no tab, no agent rows, and a workspace ID Herdr
	// no longer recognizes.
	state.Features["repo:"+scenarioStaleFeature] = model.FeatureState{
		Feature:     model.Feature{RepositoryID: "repo", Name: scenarioStaleFeature, Branch: "feature/" + scenarioStaleFeature, Path: s.Stale},
		WorkspaceID: scenarioDeletedWorkspace,
		Agents:      map[string]model.RoleAgent{},
		UpdatedAt:   observed,
	}
	return state
}

// service builds the collector every surface shares, pointed at the fixture.
func (s *herdScenario) service(t *testing.T) *Service {
	t.Helper()
	return NewService(Config{
		RepoRoot: s.Primary,
		Baseline: "dev",
		Git:      s.git,
		Remote:   s.remote,
		Agents:   s.agents,
		Bridge:   s.bridge,
		Now:      func() time.Time { return observed },
	})
}

func (s *herdScenario) snapshot(t *testing.T) Snapshot {
	t.Helper()
	snapshot, err := s.service(t).Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	return snapshot
}

// scenarioAgentRows is every agent the snapshot reports, so a test can ask "is
// this agent visible at all?" without knowing which feature, if any, claimed
// it. It reads the flat roster precisely because the feature-first grouping is
// where agents used to disappear.
func scenarioAgentRows(snapshot Snapshot) []Agent { return snapshot.Agents }

func scenarioAgentByPane(snapshot Snapshot, pane string) (Agent, bool) {
	for _, row := range scenarioAgentRows(snapshot) {
		if row.Live.Pane == pane || row.Saved.Pane == pane {
			return row, true
		}
	}
	return Agent{}, false
}

func mkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeFixtureFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestScenarioShowsAgentsRunningInThePrimaryCheckout is the reported gap:
// `herdr agent list` shows the agents working in `ori-agent-dev`, and the
// feature-first roster shows none of them, because the primary checkout is not
// a feature worktree. FR2, FR3.
func TestScenarioShowsAgentsRunningInThePrimaryCheckout(t *testing.T) {
	snapshot := newHerdScenario(t).snapshot(t)

	for _, pane := range []string{"w-dev:p1", "w-main:p1"} {
		if _, found := scenarioAgentByPane(snapshot, pane); !found {
			t.Fatalf("agent in pane %q is missing from the roster; rows = %+v", pane, scenarioAgentRows(snapshot))
		}
	}
}

// TestScenarioPlacesRepositoryAgentsWithoutAdoptingThem checks the other half
// of FR3 and FR4: an agent in a non-feature checkout is visible, is attributed
// to that checkout by canonical path, and is never presented as managed or as
// belonging to a feature.
func TestScenarioPlacesRepositoryAgentsWithoutAdoptingThem(t *testing.T) {
	scenario := newHerdScenario(t)
	snapshot := scenario.snapshot(t)

	for pane, checkout := range map[string]string{"w-dev:p1": scenario.Primary, "w-main:p1": scenario.Source} {
		row, found := scenarioAgentByPane(snapshot, pane)
		if !found {
			t.Fatalf("agent in pane %q is missing from the roster", pane)
		}
		if row.Scope != AgentScopeRepository {
			t.Fatalf("agent %q scope = %q, want repository", pane, row.Scope)
		}
		if row.Feature != "" || row.Managed || row.Role != "" {
			t.Fatalf("a repository-level agent was adopted into a feature: %+v", row)
		}
		if !samePathValue(row.MatchedPath, checkout) {
			t.Fatalf("agent %q matched path = %q, want the %q checkout", pane, row.MatchedPath, checkout)
		}
		if !row.StatusAvailability.OK() {
			t.Fatalf("agent %q status availability = %q, want an observed status", pane, row.StatusAvailability)
		}
	}
	if _, ok := findingFor(snapshot.Findings, FindingAgentUnscoped); !ok {
		t.Fatalf("findings = %+v, want agent_unscoped for the repository-level agents", snapshot.Findings)
	}
}

// TestScenarioKeepsAnUnplaceableAgentVisibleAsUnknown covers the pane that
// reports no working directory in a workspace holding several feature tabs.
// Herdr can see it, so the roster must too — as unplaced, never guessed into a
// sibling feature's worktree. FR4, FR6.
func TestScenarioKeepsAnUnplaceableAgentVisibleAsUnknown(t *testing.T) {
	scenario := newHerdScenario(t)
	snapshot := scenario.snapshot(t)

	row, found := scenarioAgentByPane(snapshot, "w-shared:p1")
	if !found {
		t.Fatalf("the unplaceable agent is missing from the roster: %+v", snapshot.Agents)
	}
	if row.Scope != AgentScopeUnknown {
		t.Fatalf("scope = %q, want unknown for an agent with no resolvable directory", row.Scope)
	}
	if row.Feature != "" || row.MatchedPath != "" {
		t.Fatalf("an unplaceable agent was attributed to a checkout: %+v", row)
	}
	if row.Status != AgentIdle || !row.StatusAvailability.OK() {
		t.Fatalf("status = %q/%q, want the observed live status", row.Status, row.StatusAvailability)
	}
	// It must not inflate any checkout's occupancy either.
	managed, ok := scenarioCheckout(snapshot, scenario.Managed)
	if !ok || managed.Agents != 2 || managed.Occupancy != 3 {
		t.Fatalf("an unplaceable agent changed a checkout's occupancy: %+v", managed)
	}
}

// TestScenarioListsEveryCheckoutWithItsOccupancy checks that the union covers
// the whole repository, not only the checkouts that implement a feature. FR2.
func TestScenarioListsEveryCheckoutWithItsOccupancy(t *testing.T) {
	scenario := newHerdScenario(t)
	snapshot := scenario.snapshot(t)

	byPath := map[string]Checkout{}
	for _, checkout := range snapshot.Checkouts {
		byPath[checkout.Path] = checkout
	}
	if len(byPath) != 4 {
		t.Fatalf("checkouts = %+v, want the source, baseline, and two feature worktrees", snapshot.Checkouts)
	}
	source, ok := scenarioCheckout(snapshot, scenario.Source)
	if !ok || !source.Source || !source.Baseline || source.Feature != "" {
		t.Fatalf("source checkout = %+v, want the main source checkout with no feature", source)
	}
	baseline, ok := scenarioCheckout(snapshot, scenario.Primary)
	if !ok || !baseline.Baseline || baseline.Source || baseline.Feature != "" {
		t.Fatalf("baseline checkout = %+v, want the linked dev checkout with no feature", baseline)
	}
	if baseline.Agents != 1 || baseline.Occupancy != 1 {
		t.Fatalf("baseline occupancy = %+v, want the one agent open in dev", baseline)
	}
	managed, ok := scenarioCheckout(snapshot, scenario.Managed)
	if !ok || managed.Feature != scenarioManagedFeature {
		t.Fatalf("managed checkout = %+v, want the feature worktree", managed)
	}
	// Three panes are open in the managed worktree; two of them run an agent.
	if managed.Occupancy != 3 || managed.Agents != 2 {
		t.Fatalf("managed occupancy = %d, agents = %d, want 3 and 2", managed.Occupancy, managed.Agents)
	}
}

// TestScenarioRosterAndFeatureRowsAgree proves the flat roster is the same
// evidence regrouped: every feature-scoped agent appears in both views with the
// same identity, so no surface can show an agent another surface hides. FR1.
func TestScenarioRosterAndFeatureRowsAgree(t *testing.T) {
	snapshot := newHerdScenario(t).snapshot(t)

	rostered := map[string]Agent{}
	for _, row := range snapshot.Agents {
		rostered[row.Feature+"/"+row.Role+"/"+row.Live.Pane+row.Saved.Pane] = row
	}
	for _, feature := range snapshot.Features {
		for _, agent := range feature.Agents {
			key := agent.Feature + "/" + agent.Role + "/" + agent.Live.Pane + agent.Saved.Pane
			found, ok := rostered[key]
			if !ok {
				t.Fatalf("feature agent %+v is missing from the roster", agent)
			}
			if found.Status != agent.Status || found.Binding != agent.Binding || found.Scope != AgentScopeFeature {
				t.Fatalf("roster row %+v disagrees with feature row %+v", found, agent)
			}
		}
	}
}

func scenarioCheckout(snapshot Snapshot, path string) (Checkout, bool) {
	for _, checkout := range snapshot.Checkouts {
		if samePathValue(checkout.Path, path) {
			return checkout, true
		}
	}
	return Checkout{}, false
}

// samePathValue compares a rendered path against a fixture path. Temporary
// directories resolve through /private on macOS, so the raw strings differ
// while naming the same directory.
func samePathValue(left, right string) bool {
	resolvedLeft, err := filepath.EvalSymlinks(left)
	if err != nil {
		resolvedLeft = left
	}
	resolvedRight, err := filepath.EvalSymlinks(right)
	if err != nil {
		resolvedRight = right
	}
	return filepath.Clean(resolvedLeft) == filepath.Clean(resolvedRight)
}

// TestScenarioKeepsSeveralAgentsInOneFeatureDistinct guards FR7: two agents in
// one worktree are two rows, never one collapsed feature status.
func TestScenarioKeepsSeveralAgentsInOneFeatureDistinct(t *testing.T) {
	snapshot := newHerdScenario(t).snapshot(t)

	row, ok := snapshot.Feature(scenarioManagedFeature)
	if !ok {
		t.Fatalf("the managed feature is missing: %+v", snapshot.Features)
	}
	roles := map[string]Agent{}
	for _, agent := range row.Agents {
		roles[agent.Role] = agent
	}
	if len(roles) != 2 || roles["builder"].Binding != BindingExact || roles["reviewer"].Binding != BindingExact {
		t.Fatalf("managed agents = %+v, want an exact builder and reviewer", row.Agents)
	}
	if roles["builder"].Status != AgentIdle || roles["reviewer"].Status != AgentWorking {
		t.Fatalf("agent statuses were collapsed: %+v", row.Agents)
	}
}

// TestScenarioShowsUnmanagedAgentsWithoutAdoptingThem guards FR4: an agent with
// no bridge role stays visible and stays unmanaged.
func TestScenarioShowsUnmanagedAgentsWithoutAdoptingThem(t *testing.T) {
	snapshot := newHerdScenario(t).snapshot(t)

	row, ok := snapshot.Feature(scenarioUnmanagedFeature)
	if !ok {
		t.Fatalf("the unmanaged feature is missing: %+v", snapshot.Features)
	}
	if len(row.Agents) != 1 {
		t.Fatalf("unmanaged agents = %+v, want exactly the discovered agent", row.Agents)
	}
	if row.Agents[0].Managed || row.Agents[0].Role != "" {
		t.Fatalf("an unclaimed agent was adopted into a role: %+v", row.Agents[0])
	}
	if _, ok := findingFor(row.Findings, FindingAgentUnmanaged); !ok {
		t.Fatalf("findings = %+v, want agent_unmanaged", row.Findings)
	}
}

// TestScenarioKeepsAMissingManagedAgentVisible guards FR5 and FR6: a saved
// agent whose worktree and workspace are gone is reported as missing, not
// silently dropped and not rendered as a real zero.
func TestScenarioKeepsAMissingManagedAgentVisible(t *testing.T) {
	snapshot := newHerdScenario(t).snapshot(t)

	row, ok := snapshot.Feature(scenarioMissingFeature)
	if !ok {
		t.Fatalf("the missing feature has no row: %+v", snapshot.Features)
	}
	if len(row.Agents) != 1 || row.Agents[0].Binding != BindingMissing {
		t.Fatalf("missing-feature agents = %+v, want one missing binding", row.Agents)
	}
	if row.Agents[0].Status != AgentMissing {
		t.Fatalf("status = %q, want an explicit missing status", row.Agents[0].Status)
	}
	if _, ok := findingFor(row.Findings, FindingAgentMissing); !ok {
		t.Fatalf("findings = %+v, want agent_missing", row.Findings)
	}
}

// TestScenarioCountsAnAgentlessPaneAsOccupancyOnly guards FR8.
func TestScenarioCountsAnAgentlessPaneAsOccupancyOnly(t *testing.T) {
	snapshot := newHerdScenario(t).snapshot(t)

	row, ok := snapshot.Feature(scenarioManagedFeature)
	if !ok {
		t.Fatalf("the managed feature is missing: %+v", snapshot.Features)
	}
	if row.Occupancy != 3 {
		t.Fatalf("occupancy = %d, want 3 panes including the one running nothing", row.Occupancy)
	}
	if _, found := scenarioAgentByPane(snapshot, "w-managed:p3"); found {
		t.Fatal("a pane with no agent was presented as an agent")
	}
}

// TestScenarioRetainsTheDeletedWorkspaceRecordAsHistory guards FR13: the legacy
// record for a deleted workspace stays as diagnostic evidence rather than being
// quietly removed to hide the drift.
func TestScenarioRetainsTheDeletedWorkspaceRecordAsHistory(t *testing.T) {
	snapshot := newHerdScenario(t).snapshot(t)

	row, ok := snapshot.Feature(scenarioStaleFeature)
	if !ok {
		t.Fatalf("the stale bridge record has no row: %+v", snapshot.Features)
	}
	if len(row.Agents) != 0 {
		t.Fatalf("stale record agents = %+v, want none: no agent was ever observed", row.Agents)
	}
	if _, ok := findingFor(row.Findings, FindingBindingPathStale); !ok {
		t.Fatalf("findings = %+v, want binding_path_stale for the removed worktree", row.Findings)
	}
	if row.Git.WorktreePath != "" {
		t.Fatalf("worktree path = %q, want empty for a removed checkout", row.Git.WorktreePath)
	}
}

// TestScenarioHumanStatusShowsEveryOpenAgent is the rendered half of the
// roster: `wt herd status` must name every agent an operator has open,
// including the ones no feature accounts for, with a plain-language reason it
// can or cannot be run overnight. FR5, FR8, FR15.
func TestScenarioHumanStatusShowsEveryOpenAgent(t *testing.T) {
	scenario := newHerdScenario(t)
	snapshot := scenario.snapshot(t)

	var expanded strings.Builder
	if err := RenderExpanded(&expanded, snapshot, RenderOptions{NoColor: true}); err != nil {
		t.Fatalf("RenderExpanded: %v", err)
	}
	output := expanded.String()

	for _, want := range []string{
		// The managed feature's two agents, by role.
		"agent builder", "agent reviewer",
		// The section that did not exist while the roster was feature-first.
		"Agents outside a feature",
		// The repository-level agents, by the stable name Herdr reports.
		"ori-dev-claude", "ori-main-codex",
		// The agent that could not be placed at all.
		"ori-shared-claude", "no working directory reported",
		// An explicit eligibility answer for each.
		"overnight: not eligible", "overnight: unverified",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expanded status did not mention %q:\n%s", want, output)
		}
	}
	if strings.ContainsRune(output, '\x1b') {
		t.Fatalf("no-color output contained escape sequences:\n%q", output)
	}

	var compact strings.Builder
	if err := RenderCompact(&compact, snapshot, RenderOptions{NoColor: true}); err != nil {
		t.Fatalf("RenderCompact: %v", err)
	}
	if !strings.Contains(compact.String(), "agent(s) outside a feature") {
		t.Fatalf("compact status hid the agents outside a feature:\n%s", compact.String())
	}
}

// TestScenarioStatusNeverPrintsPromptsOrSecrets keeps the roster to identities
// and states. Prompt bodies, terminal content, and environment values are not
// collected, and this test is what keeps them out as fields are added. FR118.
func TestScenarioStatusNeverPrintsPromptsOrSecrets(t *testing.T) {
	scenario := newHerdScenario(t)
	// Plant secrets in the places a careless renderer could reach for: a saved
	// prompt on a schedule, and an agent name carrying a token-shaped value.
	state := scenario.bridge.state
	managed := state.Features["repo:"+scenarioManagedFeature]
	managed.Schedules = map[string]model.Schedule{
		"sch-1": {
			ID: "sch-1", State: model.SchedulePending, DueAt: observed.Add(time.Hour),
			Prompt: "continue implementing; the API key is sk-not-a-real-secret",
		},
	}
	state.Features["repo:"+scenarioManagedFeature] = managed
	scenario.bridge.state = state

	snapshot := scenario.snapshot(t)
	var out strings.Builder
	if err := RenderExpanded(&out, snapshot, RenderOptions{NoColor: true}); err != nil {
		t.Fatalf("RenderExpanded: %v", err)
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, secret := range []string{"sk-not-a-real-secret", "the API key is", "continue implementing"} {
		if strings.Contains(out.String(), secret) {
			t.Fatalf("human status leaked %q:\n%s", secret, out.String())
		}
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("JSON status leaked %q:\n%s", secret, encoded)
		}
	}
}

// TestScenarioHerdrOutageKeepsSavedRecordsWithoutInventingAgents covers FR6 and
// FR14: during an outage the saved bridge roles stay visible, clearly labelled
// as records rather than observations, and no repository-level agent is
// invented from evidence nobody could read.
func TestScenarioHerdrOutageKeepsSavedRecordsWithoutInventingAgents(t *testing.T) {
	scenario := newHerdScenario(t)
	scenario.agents.err = errors.New("herdr socket unavailable")
	snapshot := scenario.snapshot(t)

	if _, found := scenarioAgentByPane(snapshot, "w-dev:p1"); found {
		t.Fatal("a repository-level agent was reported while Herdr was unavailable")
	}
	saved, found := scenarioAgentByPane(snapshot, "w-managed:p1")
	if !found {
		t.Fatalf("the saved builder record disappeared during an outage: %+v", snapshot.Agents)
	}
	if saved.StatusAvailability.OK() || saved.Status != AgentUnknown {
		t.Fatalf("a saved record was presented as a live observation: %+v", saved)
	}
	if saved.Binding != BindingUnavailable {
		t.Fatalf("binding = %q, want unavailable while Herdr could not be consulted", saved.Binding)
	}
	if saved.Eligibility.State != EligibilityIneligible {
		t.Fatalf("eligibility = %+v, want ineligible while the live state is unknown", saved.Eligibility)
	}
	if _, ok := findingFor(snapshot.Findings, FindingHerdrUnavailable); !ok {
		t.Fatalf("findings = %+v, want herdr_unavailable", snapshot.Findings)
	}
	for _, checkout := range snapshot.Checkouts {
		if checkout.Agents != 0 || checkout.Occupancy != 0 {
			t.Fatalf("occupancy was invented during an outage: %+v", checkout)
		}
	}
}

// TestScenarioEverySurfaceShowsEveryAgent is the parity proof for FR1: the
// compact table, the expanded herd view, and the JSON contract are three
// renderings of one snapshot, so an agent cannot be visible on one and missing
// from another.
func TestScenarioEverySurfaceShowsEveryAgent(t *testing.T) {
	snapshot := newHerdScenario(t).snapshot(t)

	var compact, expanded strings.Builder
	options := RenderOptions{NoColor: true}
	if err := RenderCompact(&compact, snapshot, options); err != nil {
		t.Fatalf("RenderCompact: %v", err)
	}
	if err := RenderExpanded(&expanded, snapshot, options); err != nil {
		t.Fatalf("RenderExpanded: %v", err)
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	if len(snapshot.Agents) == 0 {
		t.Fatal("the fixture produced no agents at all")
	}
	for _, agent := range snapshot.Agents {
		identity := agent.Live.Session
		if identity == "" {
			identity = agent.Saved.Pane
		}
		if !strings.Contains(string(encoded), identity) {
			t.Fatalf("JSON hid agent %q:\n%s", identity, encoded)
		}
		// The expanded view names a managed agent by its bridge role and every
		// other agent by the stable name Herdr reports; either way the agent
		// must be findable on the surface.
		token := agentName(agent)
		if agent.Role != "" {
			token = agent.Role
		}
		if !strings.Contains(expanded.String(), token) {
			t.Fatalf("the expanded surface hid agent %q:\n%s", token, expanded.String())
		}
		if agent.Scope != AgentScopeFeature && !strings.Contains(compact.String(), "outside a feature") {
			t.Fatalf("the compact surface hid the agents outside a feature:\n%s", compact.String())
		}
	}
}

// TestScenarioAgentRowsCarryTheIdentityFieldsStatusPromises covers FR5: each
// row states its name, kind, session, live state, workspace/pane, worktree,
// feature, and binding grade when those are known.
func TestScenarioAgentRowsCarryTheIdentityFieldsStatusPromises(t *testing.T) {
	scenario := newHerdScenario(t)
	snapshot := scenario.snapshot(t)

	agent, found := scenarioAgentByPane(snapshot, "w-managed:p2")
	if !found {
		t.Fatalf("the reviewer is missing: %+v", snapshot.Agents)
	}
	if agent.Feature != scenarioManagedFeature || agent.Role != "reviewer" || !agent.Managed {
		t.Fatalf("identity = %+v, want the managed reviewer role", agent)
	}
	if agent.Kind != claudeKind || agent.Live.Session != "sess-managed-reviewer" {
		t.Fatalf("kind/session = %q/%q, want a claude agent with its native session", agent.Kind, agent.Live.Session)
	}
	if agent.Live.Workspace != "w-managed" || agent.Live.Terminal != "term-managed-2" {
		t.Fatalf("live coordinates = %+v, want the observed workspace and terminal", agent.Live)
	}
	if !samePathValue(agent.MatchedPath, scenario.Managed) {
		t.Fatalf("matched path = %q, want the feature worktree", agent.MatchedPath)
	}
	if agent.Status != AgentWorking || agent.Binding != BindingExact {
		t.Fatalf("state = %q/%q, want a working agent with an exact binding", agent.Status, agent.Binding)
	}
	// The saved record and the live observation stay separate values.
	if agent.Saved.Pane != agent.Live.Pane || agent.SavedAt.IsZero() {
		t.Fatalf("saved record = %+v at %v, want the bridge's own copy", agent.Saved, agent.SavedAt)
	}
}

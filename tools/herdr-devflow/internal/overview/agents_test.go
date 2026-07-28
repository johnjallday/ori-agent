package overview

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/herdr"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/model"
)

// fakeAgents is a deterministic AgentCollector. It also records every method
// invocation so tests can prove collection never mutates Herdr.
type fakeAgents struct {
	// mu guards calls: the service may collect concurrently, so the fake has
	// to be as safe as the thing it stands in for.
	mu         sync.Mutex
	live       []herdr.AgentInfo
	workspaces []herdr.WorkspaceInfo
	err        error
	calls      []string
}

func (f *fakeAgents) AgentListInfo(context.Context) ([]herdr.AgentInfo, error) {
	f.mu.Lock()
	f.calls = append(f.calls, "AgentListInfo")
	f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	return f.live, nil
}

func (f *fakeAgents) WorkspaceListInfo(context.Context) ([]herdr.WorkspaceInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "WorkspaceListInfo")
	if f.err != nil {
		return nil, f.err
	}
	return f.workspaces, nil
}

func (f *fakeAgents) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

type fakeBridge struct {
	state model.BridgeState
	err   error
}

func (f *fakeBridge) Load() (model.BridgeState, error) { return f.state, f.err }

func session(value string) *model.NativeSession {
	return &model.NativeSession{Source: "claude", Agent: "claude", Kind: "session", Value: value}
}

func liveAgent(workspace, pane, terminal, name string, mutate ...func(*herdr.AgentInfo)) herdr.AgentInfo {
	agent := herdr.AgentInfo{
		WorkspaceID: workspace,
		PaneID:      pane,
		TerminalID:  terminal,
		Name:        name,
		Agent:       "claude",
		AgentStatus: model.AgentIdle,
		// Agents are resolved by working directory, so a fixture without one
		// would be invisible for the same reason a hand-made workspace was.
		Cwd: "/w/x",
	}
	for _, apply := range mutate {
		apply(&agent)
	}
	return agent
}

func savedRole(role, workspace, pane, terminal, name string, mutate ...func(*model.RoleAgent)) model.RoleAgent {
	saved := model.RoleAgent{
		Role: role, Name: name, Kind: "claude",
		WorkspaceID: workspace, PaneID: pane, TerminalID: terminal,
		Status: model.AgentIdle, UpdatedAt: observed,
	}
	for _, apply := range mutate {
		apply(&saved)
	}
	return saved
}

func bridgeWith(slug string, agents map[string]model.RoleAgent, mutate ...func(*model.FeatureState)) *fakeBridge {
	state := model.FeatureState{
		Feature:     model.Feature{RepositoryID: "repo", Name: slug, Branch: "feature/" + slug},
		WorkspaceID: "ws-1",
		Agents:      agents,
		UpdatedAt:   observed,
	}
	for _, apply := range mutate {
		apply(&state)
	}
	return &fakeBridge{state: model.BridgeState{Version: 1, Features: map[string]model.FeatureState{slug: state}}}
}

func attach(t *testing.T, slug string, agents *fakeAgents, bridge *fakeBridge) (Feature, []Finding) {
	t.Helper()
	// Point every fixture agent at this feature's worktree unless the test
	// deliberately placed it elsewhere.
	for index := range agents.live {
		if agents.live[index].Cwd == "/w/x" {
			agents.live[index].Cwd = "/w/" + slug
		}
	}
	evidence := CollectAgents(context.Background(), agents, bridge, observed)
	row := feature(slug, withWorktree("/w/"+slug))
	findings := AttachAgents(&row, evidence)
	return row, findings
}

func TestAttachAgentsExactNativeSessionBinding(t *testing.T) {
	live := liveAgent("ws-1", "pane-1", "term-1", "ori-builder", func(a *herdr.AgentInfo) {
		a.AgentSession = session("sess-abc")
		a.AgentStatus = model.AgentWorking
	})
	bridge := bridgeWith("x", map[string]model.RoleAgent{
		"builder": savedRole("builder", "ws-1", "pane-1", "term-1", "ori-builder", func(s *model.RoleAgent) {
			s.NativeSession = *session("sess-abc")
		}),
	})

	row, findings := attach(t, "x", &fakeAgents{live: []herdr.AgentInfo{live}}, bridge)
	if len(row.Agents) != 1 {
		t.Fatalf("agents = %d, want 1", len(row.Agents))
	}
	agent := row.Agents[0]
	if agent.Binding != BindingExact {
		t.Fatalf("binding = %q, want exact", agent.Binding)
	}
	if agent.Status != AgentWorking || !agent.StatusAvailability.OK() {
		t.Fatalf("status = %q/%q, want an observed working status", agent.Status, agent.StatusAvailability)
	}
	if len(findings) != 0 {
		t.Fatalf("findings = %v, want none for a healthy binding", findings)
	}
}

func TestAttachAgentsKeepsSavedAndLiveIdentitySeparate(t *testing.T) {
	live := liveAgent("ws-1", "pane-2", "term-1", "renamed-agent")
	bridge := bridgeWith("x", map[string]model.RoleAgent{
		"builder": savedRole("builder", "ws-1", "pane-1", "term-1", "original-agent"),
	})

	row, _ := attach(t, "x", &fakeAgents{live: []herdr.AgentInfo{live}}, bridge)
	agent := row.Agents[0]
	if agent.Saved.Pane != "pane-1" || agent.Saved.Session != "" {
		t.Fatalf("saved identity was altered: %+v", agent.Saved)
	}
	if agent.Live.Pane != "pane-2" {
		t.Fatalf("live identity = %+v, want the observed pane", agent.Live)
	}
	if agent.Saved.Pane == agent.Live.Pane {
		t.Fatal("saved and live identities were collapsed into one value")
	}
}

// TestAttachAgentsReproducesTheDownloadsJanitorDrift locks in the drift this
// feature was written to explain: a saved builder whose live pane no longer
// carries the recorded name.
func TestAttachAgentsReproducesTheDownloadsJanitorDrift(t *testing.T) {
	live := liveAgent("ws-1", "pane-1", "term-1", "", func(a *herdr.AgentInfo) {
		a.AgentStatus = model.AgentIdle
	})
	bridge := bridgeWith("downloads-janitor", map[string]model.RoleAgent{
		"builder": savedRole("builder", "ws-1", "pane-1", "term-1", "ori-git-b68-downloa-build-cda01d"),
	})

	row, findings := attach(t, "downloads-janitor", &fakeAgents{live: []herdr.AgentInfo{live}}, bridge)
	agent := row.Agents[0]
	if agent.Binding != BindingPossibleDrift {
		t.Fatalf("binding = %q, want possible_drift", agent.Binding)
	}
	if agent.Status != AgentIdle {
		t.Fatalf("status = %q, want the live idle status still observed", agent.Status)
	}
	// The explanation must be field by field, not a bare "missing".
	if !strings.Contains(agent.BindingDetail, "name:") {
		t.Fatalf("binding detail = %q, want a field-by-field explanation", agent.BindingDetail)
	}
	if !strings.Contains(agent.BindingDetail, "ori-git-b68-downloa-build-cda01d") {
		t.Fatalf("binding detail = %q, want the saved name quoted", agent.BindingDetail)
	}
	finding, ok := findingFor(findings, FindingAgentDrift)
	if !ok || finding.Role != "builder" {
		t.Fatalf("finding = %+v, ok=%v, want a role-scoped drift warning", finding, ok)
	}
}

func TestAttachAgentsAmbiguousMatchPreservesEveryCandidate(t *testing.T) {
	first := liveAgent("ws-1", "pane-1", "term-9", "candidate-a")
	second := liveAgent("ws-1", "pane-9", "term-1", "candidate-b")
	bridge := bridgeWith("x", map[string]model.RoleAgent{
		"builder": savedRole("builder", "ws-1", "pane-1", "term-1", "gone"),
	})

	row, findings := attach(t, "x", &fakeAgents{live: []herdr.AgentInfo{first, second}}, bridge)
	agent := row.Agents[0]
	if agent.Binding != BindingAmbiguous {
		t.Fatalf("binding = %q, want ambiguous", agent.Binding)
	}
	if len(agent.BindingCandidates) != 2 {
		t.Fatalf("candidates = %d, want both plausible rows preserved", len(agent.BindingCandidates))
	}
	if agent.StatusAvailability.OK() {
		t.Fatal("an ambiguous binding reported a live status as if one agent had been chosen")
	}
	if finding, ok := findingFor(findings, FindingAgentAmbiguous); !ok || finding.Severity != SeverityError {
		t.Fatalf("finding = %+v, ok=%v, want an ambiguity error", finding, ok)
	}
}

func TestAttachAgentsTrulyMissingAgent(t *testing.T) {
	bridge := bridgeWith("x", map[string]model.RoleAgent{
		"builder": savedRole("builder", "ws-1", "pane-1", "term-1", "gone"),
	})

	elsewhere := liveAgent("ws-other", "pane-5", "term-5", "elsewhere")
	elsewhere.Cwd = "/w/some-other-feature"
	row, findings := attach(t, "x", &fakeAgents{live: []herdr.AgentInfo{elsewhere}}, bridge)
	agent := row.Agents[0]
	if agent.Binding != BindingMissing || agent.Status != AgentMissing {
		t.Fatalf("agent = %+v, want a missing binding and status", agent)
	}
	if _, ok := findingFor(findings, FindingAgentMissing); !ok {
		t.Fatalf("findings = %v, want agent_missing", findings)
	}
}

func TestAttachAgentsDiscoversUnmanagedAgentsWithoutAdoptingThem(t *testing.T) {
	managed := liveAgent("ws-1", "pane-1", "term-1", "ori-builder")
	codex := liveAgent("ws-1", "pane-7", "term-7", "codex-scratch", func(a *herdr.AgentInfo) {
		a.Agent = "codex"
		a.AgentStatus = model.AgentWorking
	})
	bridge := bridgeWith("x", map[string]model.RoleAgent{
		"builder": savedRole("builder", "ws-1", "pane-1", "term-1", "ori-builder"),
	})

	row, findings := attach(t, "x", &fakeAgents{live: []herdr.AgentInfo{managed, codex}}, bridge)
	if len(row.Agents) != 2 {
		t.Fatalf("agents = %d, want the managed role and the unmanaged pane", len(row.Agents))
	}
	var unmanaged *Agent
	for index := range row.Agents {
		if !row.Agents[index].Managed {
			unmanaged = &row.Agents[index]
		}
	}
	if unmanaged == nil {
		t.Fatal("the unmanaged Codex pane was not surfaced")
	}
	if unmanaged.Role != "" {
		t.Fatalf("an unmanaged agent was given the role %q", unmanaged.Role)
	}
	if unmanaged.Kind != "codex" {
		t.Fatalf("kind = %q, want codex", unmanaged.Kind)
	}
	if _, ok := findingFor(findings, FindingAgentUnmanaged); !ok {
		t.Fatalf("findings = %v, want agent_unmanaged", findings)
	}
}

func TestAttachAgentsWorktreeWithNoAgentIsStated(t *testing.T) {
	// Silence here would look identical to a healthy, quietly-working agent.
	row, findings := attach(t, "lonely", &fakeAgents{}, &fakeBridge{})
	if len(row.Agents) != 0 {
		t.Fatalf("agents = %v, want none", row.Agents)
	}
	finding, ok := findingFor(findings, FindingNoAgent)
	if !ok {
		t.Fatalf("findings = %v, want no_agent", findings)
	}
	if finding.Severity != SeverityInfo {
		t.Fatalf("severity = %q, want info", finding.Severity)
	}
}

func TestAttachAgentsHerdrOutageNeverPromotesSavedValues(t *testing.T) {
	bridge := bridgeWith("x", map[string]model.RoleAgent{
		"builder": savedRole("builder", "ws-1", "pane-1", "term-1", "ori-builder", func(s *model.RoleAgent) {
			s.Status = model.AgentWorking
		}),
	})
	agents := &fakeAgents{err: errors.New("herdr socket closed")}

	evidence := CollectAgents(context.Background(), agents, bridge, observed)
	if evidence.Availability != AvailabilityUnavailable {
		t.Fatalf("availability = %q, want unavailable", evidence.Availability)
	}

	row := feature("x", withWorktree("/w/x"))
	AttachAgents(&row, evidence)
	if len(row.Agents) != 1 {
		t.Fatalf("agents = %d, want the saved role still listed", len(row.Agents))
	}
	agent := row.Agents[0]
	// The saved record said "working". Presenting that as observed during an
	// outage is exactly the lie this separation exists to prevent.
	if agent.Status == AgentWorking {
		t.Fatal("a saved status was promoted to a live observation during an outage")
	}
	if agent.StatusAvailability.OK() {
		t.Fatalf("status availability = %q, want unavailable", agent.StatusAvailability)
	}
	if agent.Binding != BindingUnavailable {
		t.Fatalf("binding = %q, want unavailable", agent.Binding)
	}
	if agent.Saved.Pane != "pane-1" {
		t.Fatalf("the bridge record was dropped: %+v", agent.Saved)
	}
}

func TestAttachAgentsSchedulesAreSummarizedWithoutPromptText(t *testing.T) {
	bridge := bridgeWith("x", map[string]model.RoleAgent{}, func(state *model.FeatureState) {
		state.Schedules = map[string]model.Schedule{
			"sched-1": {ID: "sched-1", State: model.ScheduleFailed, DueAt: observed},
		}
	})

	row, findings := attach(t, "x", &fakeAgents{}, bridge)
	if len(row.Schedules) != 1 {
		t.Fatalf("schedules = %d, want 1", len(row.Schedules))
	}
	if strings.Contains(row.Schedules[0].Summary, "prompt") {
		t.Fatalf("schedule summary leaked prompt content: %q", row.Schedules[0].Summary)
	}
	if _, ok := findingFor(findings, FindingScheduleFailed); !ok {
		t.Fatalf("findings = %v, want a failed-schedule warning", findings)
	}
}

func TestCollectAgentsNeverMutatesHerdr(t *testing.T) {
	// Collection is diagnostic. If this list ever grows beyond a read, the
	// board has started changing the thing it is supposed to observe.
	agents := &fakeAgents{live: []herdr.AgentInfo{liveAgent("ws-1", "pane-1", "term-1", "a")}}
	evidence := CollectAgents(context.Background(), agents, &fakeBridge{}, observed)

	row := feature("x", withWorktree("/w/x"))
	AttachAgents(&row, evidence)

	// Two read-only listings per collection — agents and workspaces — and
	// nothing else. The count is bounded and per-collection, never per agent.
	if agents.callCount() != 2 {
		t.Fatalf("herdr calls = %d, want the two read-only listings", agents.callCount())
	}
}

func TestBridgeSlugsRejectsNonCanonicalNames(t *testing.T) {
	state := model.BridgeState{Features: map[string]model.FeatureState{
		"good": {Feature: model.Feature{Name: "good-slug"}},
		"bad":  {Feature: model.Feature{Name: "Not A Slug"}},
	}}
	slugs := BridgeSlugs(state)
	if len(slugs) != 1 || slugs[0] != "good-slug" {
		t.Fatalf("slugs = %v, want only the canonical name", slugs)
	}
}

func TestNormalizeStatusNeverReadsBlankAsIdle(t *testing.T) {
	if got := normalizeStatus(""); got != model.AgentUnknown {
		t.Fatalf("normalizeStatus(\"\") = %q, want unknown", got)
	}
	if got := normalizeStatus(model.AgentStatus("something-new")); got != model.AgentUnknown {
		t.Fatalf("an unrecognized status = %q, want unknown", got)
	}
	if got := normalizeStatus(model.AgentBlocked); got != model.AgentBlocked {
		t.Fatalf("a known status was altered: %q", got)
	}
}

func TestCollectAgentsWithoutACollectorIsUnavailableNotEmpty(t *testing.T) {
	evidence := CollectAgents(context.Background(), nil, &fakeBridge{}, time.Now())
	if evidence.Availability != AvailabilityUnavailable {
		t.Fatalf("availability = %q, want unavailable", evidence.Availability)
	}
	if evidence.Detail == "" {
		t.Fatal("an unavailable Herdr carried no explanation")
	}
}

func TestMetadataStalenessIsTimestampBased(t *testing.T) {
	published := observed
	changed := observed.Add(time.Hour)

	bridge := bridgeWith("x", map[string]model.RoleAgent{}, func(state *model.FeatureState) {
		state.UpdatedAt = published
	})
	evidence := CollectAgents(context.Background(), &fakeAgents{}, bridge, observed)

	row := feature("x", withWorktree("/w/x"))
	row.Plan.TaskListModTime = changed
	findings := AttachAgents(&row, evidence)

	finding, ok := findingFor(findings, FindingMetadataStale)
	if !ok {
		t.Fatalf("findings = %v, want metadata_stale", findings)
	}
	if finding.Severity != SeverityInfo {
		t.Fatalf("severity = %q, want info", finding.Severity)
	}
	if !strings.Contains(finding.Detail, "last published") {
		t.Fatalf("detail = %q, want both timestamps stated", finding.Detail)
	}
}

func TestMetadataStalenessSilentWhenMetadataIsCurrent(t *testing.T) {
	bridge := bridgeWith("x", map[string]model.RoleAgent{}, func(state *model.FeatureState) {
		state.UpdatedAt = observed.Add(time.Hour)
	})
	evidence := CollectAgents(context.Background(), &fakeAgents{}, bridge, observed)

	row := feature("x", withWorktree("/w/x"))
	row.Plan.TaskListModTime = observed
	if _, ok := findingFor(AttachAgents(&row, evidence), FindingMetadataStale); ok {
		t.Fatal("metadata published after the last plan change was reported stale")
	}
}

func TestMetadataStalenessRespectsAnOptOut(t *testing.T) {
	disabled := false
	bridge := bridgeWith("x", map[string]model.RoleAgent{}, func(state *model.FeatureState) {
		state.UpdatedAt = observed
		state.MetadataEnabled = &disabled
	})
	evidence := CollectAgents(context.Background(), &fakeAgents{}, bridge, observed)

	row := feature("x", withWorktree("/w/x"))
	row.Plan.TaskListModTime = observed.Add(time.Hour)
	if _, ok := findingFor(AttachAgents(&row, evidence), FindingMetadataStale); ok {
		t.Fatal("a feature that opted out of metadata was reported stale")
	}
}

// TestAttachAgentsFindsAgentInAnUnboundWorkspace is the case that motivated
// this change. On 2026-07-26 a Claude agent worked for hours in a feature's
// worktree from a workspace nobody had bound, and the bridge could not see it
// because discovery was scoped to the saved workspace ID.
func TestAttachAgentsFindsAgentInAnUnboundWorkspace(t *testing.T) {
	hidden := liveAgent("wF", "wF:p1", "term-1", "hand-opened")
	hidden.Cwd = "/w/wt-herd-feature-overview"

	// The bridge's saved record points at a workspace that no longer exists.
	bridge := bridgeWith("wt-herd-feature-overview", map[string]model.RoleAgent{
		"builder": savedRole("builder", "wK", "wK:p1", "term-gone", "ori-builder"),
	})
	evidence := CollectAgents(context.Background(), &fakeAgents{live: []herdr.AgentInfo{hidden}}, bridge, observed)

	row := feature("wt-herd-feature-overview", withWorktree("/w/wt-herd-feature-overview"))
	findings := AttachAgents(&row, evidence)

	var unmanaged *Agent
	for index := range row.Agents {
		if !row.Agents[index].Managed {
			unmanaged = &row.Agents[index]
		}
	}
	if unmanaged == nil {
		t.Fatalf("the agent in the unbound workspace stayed invisible: %+v", row.Agents)
	}
	if unmanaged.Live.Workspace != "wF" {
		t.Fatalf("live workspace = %q, want wF", unmanaged.Live.Workspace)
	}
	if _, ok := findingFor(findings, FindingAgentUnmanaged); !ok {
		t.Fatalf("findings = %v, want agent_unmanaged", findings)
	}
	// It must be reported, never claimed.
	if unmanaged.Role != "" || unmanaged.Managed {
		t.Fatalf("a discovered agent was adopted: %+v", unmanaged)
	}
}

func TestAttachAgentsIgnoresAgentsInSiblingWorktrees(t *testing.T) {
	// The prefix-collision case, end to end.
	sibling := liveAgent("wZ", "wZ:p1", "term-9", "other")
	sibling.Cwd = "/w/feature-abc"

	evidence := CollectAgents(context.Background(), &fakeAgents{live: []herdr.AgentInfo{sibling}}, &fakeBridge{}, observed)
	row := feature("feature-a", withWorktree("/w/feature-a"))
	AttachAgents(&row, evidence)

	if len(row.Agents) != 0 {
		t.Fatalf("an agent in a sibling worktree was attributed: %+v", row.Agents)
	}
}

func TestAttachAgentsFallsBackToTheWorkspaceBinding(t *testing.T) {
	// A pane with no usable cwd still resolves through its workspace's
	// recorded checkout path.
	agent := liveAgent("wE", "wE:p1", "term-1", "builder")
	agent.Cwd = ""
	agent.ForegroundCwd = ""

	evidence := CollectAgents(context.Background(), &fakeAgents{
		live: []herdr.AgentInfo{agent},
		workspaces: []herdr.WorkspaceInfo{{
			WorkspaceID: "wE",
			Worktree:    &herdr.WorktreeBinding{CheckoutPath: "/w/bound", IsLinkedWorktree: true},
		}},
	}, &fakeBridge{}, observed)

	row := feature("bound", withWorktree("/w/bound"))
	AttachAgents(&row, evidence)
	if len(row.Agents) != 1 {
		t.Fatalf("the workspace-binding fallback did not resolve: %+v", row.Agents)
	}
}

func TestAttachAgentsNeverResolvesThroughWorkspaceLabels(t *testing.T) {
	// Labels are user-editable and observed to drift: two workspaces have been
	// seen sharing one label while pointing at different checkouts.
	mislabelled := liveAgent("wJ", "wJ:p1", "term-1", "codex")
	mislabelled.Cwd = "/w/somewhere-else"

	evidence := CollectAgents(context.Background(), &fakeAgents{
		live: []herdr.AgentInfo{mislabelled},
		workspaces: []herdr.WorkspaceInfo{{
			WorkspaceID: "wJ",
			Label:       "target-feature", // matches the slug exactly
		}},
	}, &fakeBridge{}, observed)

	row := feature("target-feature", withWorktree("/w/target-feature"))
	AttachAgents(&row, evidence)
	if len(row.Agents) != 0 {
		t.Fatalf("an agent was matched by workspace label: %+v", row.Agents)
	}
}

func TestAttachAgentsReportsAStaleBindingPath(t *testing.T) {
	// The wt-herd-feature-overview case after its worktree was removed: the
	// bridge still holds a binding for a directory that no longer exists.
	bridge := bridgeWith("gone", map[string]model.RoleAgent{}, func(state *model.FeatureState) {
		state.Feature.Path = "/w/gone"
	})
	evidence := CollectAgents(context.Background(), &fakeAgents{}, bridge, observed)

	row := feature("gone") // no worktree
	findings := AttachAgents(&row, evidence)

	finding, ok := findingFor(findings, FindingBindingPathStale)
	if !ok {
		t.Fatalf("findings = %v, want binding_path_stale", findings)
	}
	if finding.Severity != SeverityInfo {
		t.Fatalf("severity = %q, want info — a stale path is drift, not an error", finding.Severity)
	}
	if !strings.Contains(finding.Detail, "/w/gone") {
		t.Fatalf("detail = %q, want the recorded path named", finding.Detail)
	}
}

func TestAttachAgentsListsEveryAgentInTheWorktree(t *testing.T) {
	// A builder and a test-watcher on one feature is a normal setup, not an
	// anomaly: both must appear with independent statuses.
	builder := liveAgent("wA", "wA:p1", "term-1", "ori-builder")
	builder.Cwd = "/w/busy"
	watcher := liveAgent("wA", "wA:p2", "term-2", "watcher")
	watcher.Cwd = "/w/busy"
	watcher.AgentStatus = model.AgentWorking

	bridge := bridgeWith("busy", map[string]model.RoleAgent{
		"builder": savedRole("builder", "wA", "wA:p1", "term-1", "ori-builder"),
	})
	evidence := CollectAgents(context.Background(), &fakeAgents{live: []herdr.AgentInfo{builder, watcher}}, bridge, observed)

	row := feature("busy", withWorktree("/w/busy"))
	AttachAgents(&row, evidence)

	if len(row.Agents) != 2 {
		t.Fatalf("agents = %d, want the managed builder and the unmanaged watcher", len(row.Agents))
	}
	statuses := map[string]AgentStatus{}
	for _, agent := range row.Agents {
		key := agent.Role
		if key == "" {
			key = "unmanaged"
		}
		statuses[key] = agent.Status
	}
	if statuses["builder"] != AgentIdle || statuses["unmanaged"] != AgentWorking {
		t.Fatalf("statuses = %v, want independent per-agent statuses", statuses)
	}
	if row.Occupancy != 2 {
		t.Fatalf("occupancy = %d, want 2", row.Occupancy)
	}
}

func TestAttachAgentsCountsAgentlessPanesAsOccupancy(t *testing.T) {
	// A pane with no agent is occupancy, not an agent row. The distinction
	// decides whether a worktree is safe to remove.
	empty := liveAgent("wA", "wA:p2", "term-2", "")
	empty.Agent = ""
	empty.Cwd = "/w/quiet"

	evidence := CollectAgents(context.Background(), &fakeAgents{live: []herdr.AgentInfo{empty}}, &fakeBridge{}, observed)
	row := feature("quiet", withWorktree("/w/quiet"))
	AttachAgents(&row, evidence)

	if len(row.Agents) != 0 {
		t.Fatalf("agents = %+v, want none — an agentless pane is not an agent", row.Agents)
	}
	if row.Occupancy != 1 {
		t.Fatalf("occupancy = %d, want the agentless pane counted", row.Occupancy)
	}
}

func TestAttachAgentsRecordsTheMatchedWorktree(t *testing.T) {
	agent := liveAgent("wA", "wA:p1", "term-1", "solo")
	agent.Cwd = "/w/traced/internal/overview"

	evidence := CollectAgents(context.Background(), &fakeAgents{live: []herdr.AgentInfo{agent}}, &fakeBridge{}, observed)
	row := feature("traced", withWorktree("/w/traced"))
	AttachAgents(&row, evidence)

	if len(row.Agents) != 1 {
		t.Fatalf("agents = %d, want the agent in a subdirectory matched", len(row.Agents))
	}
	if row.Agents[0].MatchedPath == "" {
		t.Fatal("the attribution recorded no evidence of which worktree matched")
	}
}

// A workspace that hosts one tab still identifies that tab's checkout, so the
// binding remains a useful fallback for a pane with no cwd. A workspace hosting
// a tab per feature identifies none of them: it keeps the binding of whichever
// worktree opened it, and using that would attribute an agent to a sibling
// feature's branch. Reporting nothing is the honest answer there.
func TestWorkspaceBindingFallbackOnlyAppliesToSingleTabWorkspaces(t *testing.T) {
	agent := herdr.AgentInfo{Name: "ori-agent", WorkspaceID: "w1"}
	single := []herdr.WorkspaceInfo{{
		WorkspaceID: "w1", TabCount: 1,
		Worktree: &herdr.WorktreeBinding{CheckoutPath: "/repo/worktrees/alpha"},
	}}
	if got := agentWorktree(agent, single); got != "/repo/worktrees/alpha" {
		t.Fatalf("single-tab workspace fallback = %q, want the bound checkout", got)
	}

	shared := []herdr.WorkspaceInfo{{
		WorkspaceID: "w1", TabCount: 3,
		Worktree: &herdr.WorktreeBinding{CheckoutPath: "/repo/worktrees/alpha"},
	}}
	if got := agentWorktree(agent, shared); got != "" {
		t.Fatalf("shared workspace fallback = %q, want no guess", got)
	}

	// A pane that does report a directory is unaffected either way: its own cwd
	// is first-hand evidence and always wins.
	located := agent
	located.Cwd = "/repo/worktrees/beta"
	if got := agentWorktree(located, shared); got != "/repo/worktrees/beta" {
		t.Fatalf("pane cwd = %q, want it to win over the workspace binding", got)
	}
}

// A handoff that degraded is non-fatal by design, and its reason is printed at
// the time — but a warning in a terminal that has scrolled away is not a state
// anyone can look up later. A worktree with a bridge record and no Herdr
// placement must be visible where the feature is inspected, carrying the
// command that finishes the job.
func TestDegradedHandoffIsVisibleInTheOverview(t *testing.T) {
	bridge := model.NewBridgeState()
	bridge.Features["repo:degraded"] = model.FeatureState{
		Feature: model.Feature{RepositoryID: "repo", Name: "degraded", Branch: "feature/degraded", Path: "/repo/worktrees/degraded"},
		// No WorkspaceID and no TabID: Herdr was never reached.
	}
	feature := Feature{Slug: "degraded", Git: GitState{WorktreePath: "/repo/worktrees/degraded"}}
	evidence := AgentEvidence{Availability: AvailabilityAvailable, Bridge: bridge}

	findings := AttachAgents(&feature, evidence)
	found, ok := findingFor(findings, FindingHandoffIncomplete)
	if !ok {
		t.Fatalf("findings = %v, want handoff_incomplete", findings)
	}
	if found.Severity != SeverityWarning {
		t.Fatalf("severity = %q, want warning: the feature has no agent and nobody was told twice", found.Severity)
	}
	if !strings.Contains(found.Detail, "wt herd retry") {
		t.Fatalf("detail = %q, want the command that finishes the handoff", found.Detail)
	}

	// A feature that did get placed must not raise it, or the finding would be
	// noise on every healthy row.
	bridge.Features["repo:placed"] = model.FeatureState{
		Feature:     model.Feature{RepositoryID: "repo", Name: "placed", Path: "/repo/worktrees/placed"},
		WorkspaceID: "w1", TabID: "w1:t2",
	}
	placed := Feature{Slug: "placed", Git: GitState{WorktreePath: "/repo/worktrees/placed"}}
	if _, ok := findingFor(AttachAgents(&placed, AgentEvidence{Availability: AvailabilityAvailable, Bridge: bridge}), FindingHandoffIncomplete); ok {
		t.Fatal("a placed feature raised handoff_incomplete")
	}
}

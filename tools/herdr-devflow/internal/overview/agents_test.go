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
	mu    sync.Mutex
	live  []herdr.AgentInfo
	err   error
	calls []string
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

	row, findings := attach(t, "x", &fakeAgents{live: []herdr.AgentInfo{
		liveAgent("ws-other", "pane-5", "term-5", "elsewhere"),
	}}, bridge)
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

	if agents.callCount() != 1 {
		t.Fatalf("herdr calls = %d, want exactly one read-only listing", agents.callCount())
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

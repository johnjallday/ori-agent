package status

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/herdr"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/model"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/tasklist"
)

type testStore struct{ state model.BridgeState }

func (s testStore) Load() (model.BridgeState, error) { return s.state, nil }

type fakeHerdr struct {
	mu             sync.Mutex
	agents         []herdr.AgentInfo
	listErr        error
	workspaceCalls []metadataCall
	paneCalls      []metadataCall
	viewParams     map[string]any
	clearedSource  string
}

type metadataCall struct {
	id     string
	source string
	tokens map[string]string
}

func copyTokens(value map[string]string) map[string]string {
	result := make(map[string]string, len(value))
	for key, item := range value {
		result[key] = item
	}
	return result
}

func (f *fakeHerdr) AgentListInfo(context.Context) ([]herdr.AgentInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]herdr.AgentInfo(nil), f.agents...), f.listErr
}

func (f *fakeHerdr) ReportWorkspaceMetadata(_ context.Context, id, source string, tokens map[string]string) (json.RawMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.workspaceCalls = append(f.workspaceCalls, metadataCall{id: id, source: source, tokens: copyTokens(tokens)})
	return json.RawMessage(`{"type":"workspace_metadata"}`), nil
}

func (f *fakeHerdr) ReportPaneMetadata(_ context.Context, id, source string, tokens map[string]string) (json.RawMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paneCalls = append(f.paneCalls, metadataCall{id: id, source: source, tokens: copyTokens(tokens)})
	return json.RawMessage(`{"type":"pane_metadata"}`), nil
}

func (f *fakeHerdr) SetAgentView(_ context.Context, params map[string]any) (json.RawMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.viewParams = params
	return json.RawMessage(`{"type":"agent_view","active":true}`), nil
}

func (f *fakeHerdr) ClearAgentView(_ context.Context, source string) (json.RawMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.clearedSource = source
	return json.RawMessage(`{"type":"agent_view","active":false}`), nil
}

type fakeGit struct{ states map[string]GitState }

func (f fakeGit) Inspect(_ context.Context, path string) (GitState, error) {
	if state, ok := f.states[path]; ok {
		return state, nil
	}
	return GitState{}, errors.New("missing git fixture")
}

func TestSnapshotKeepsObservedStateSeparateFromTaskAndGitHints(t *testing.T) {
	now := time.Date(2026, time.July, 23, 16, 0, 0, 0, time.UTC)
	state, agents, paths := fixtureState(t, now)
	client := &fakeHerdr{agents: agents}
	service := &Service{Store: testStore{state: state}, Client: client, Git: fakeGit{states: map[string]GitState{paths["alpha"]: {Dirty: true, Ahead: 2}, paths["beta"]: {Dirty: false}}}, Now: func() time.Time { return now }}

	snapshot, err := service.Snapshot(context.Background(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Stale || len(snapshot.Features) != 2 || len(snapshot.Rows) != 2 {
		t.Fatalf("Snapshot() = %#v", snapshot)
	}
	if snapshot.Rows[0].Feature != "beta" || snapshot.Rows[0].ObservedStatus != model.AgentBlocked {
		t.Fatalf("attention sort = %#v", snapshot.Rows)
	}
	alpha := snapshot.Rows[1]
	if alpha.ObservedStatus != model.AgentWorking || alpha.Task.Completed != 1 || alpha.Task.Total != 2 || alpha.Task.Next != "1.1 Continue alpha" || !alpha.Git.Dirty || alpha.NextSchedule == nil || alpha.NextSchedule.ID != "sch-alpha" {
		t.Fatalf("alpha row = %#v", alpha)
	}
	if strings.Contains(alpha.Task.Next, "working") {
		t.Fatalf("derived next task was presented as observed activity: %#v", alpha)
	}
}

func TestSnapshotMarksHerdrUnavailableRowsStaleRatherThanInventingState(t *testing.T) {
	now := time.Date(2026, time.July, 23, 16, 0, 0, 0, time.UTC)
	state, _, paths := fixtureState(t, now)
	client := &fakeHerdr{listErr: &model.StageError{Code: model.ErrHerdrUnavailable, Message: "socket unavailable"}}
	service := &Service{Store: testStore{state: state}, Client: client, Git: fakeGit{states: map[string]GitState{paths["alpha"]: {}, paths["beta"]: {}}}, Now: func() time.Time { return now }}

	snapshot, err := service.Snapshot(context.Background(), Options{})
	if err != nil || !snapshot.Stale || len(snapshot.Rows) != 2 {
		t.Fatalf("Snapshot() = %#v, %v", snapshot, err)
	}
	for _, row := range snapshot.Rows {
		if !row.Stale || row.ObservedStatus != model.AgentUnknown || row.StatusDetail != "socket unavailable" {
			t.Fatalf("stale row = %#v", row)
		}
	}
}

func TestSnapshotKeepsAFailedScheduleVisibleWhenNothingIsPending(t *testing.T) {
	now := time.Date(2026, time.July, 23, 16, 0, 0, 0, time.UTC)
	state, agents, paths := fixtureState(t, now)
	alpha := state.Features["repo:alpha"]
	alpha.Schedules = map[string]model.Schedule{
		"sch-delivered": {ID: "sch-delivered", DueAt: now.Add(-2 * time.Hour), State: model.ScheduleDelivered, UpdatedAt: now.Add(-time.Hour)},
		"sch-failed":    {ID: "sch-failed", DueAt: now.Add(-time.Hour), State: model.ScheduleFailed, UpdatedAt: now.Add(-30 * time.Minute)},
	}
	state.Features["repo:alpha"] = alpha
	service := &Service{Store: testStore{state: state}, Client: &fakeHerdr{agents: agents}, Git: fakeGit{states: map[string]GitState{paths["alpha"]: {}, paths["beta"]: {}}}, Now: func() time.Time { return now }}
	snapshot, err := service.Snapshot(context.Background(), Options{FeatureName: "alpha"})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Rows) != 1 || snapshot.Rows[0].NextSchedule == nil || snapshot.Rows[0].NextSchedule.ID != "sch-failed" || snapshot.Rows[0].NextSchedule.State != model.ScheduleFailed {
		t.Fatalf("failed schedule visibility = %#v", snapshot.Rows)
	}
}

func TestMetadataAndViewStaySourceScoped(t *testing.T) {
	now := time.Date(2026, time.July, 23, 16, 0, 0, 0, time.UTC)
	state, agents, paths := fixtureState(t, now)
	client := &fakeHerdr{agents: agents}
	service := &Service{Store: testStore{state: state}, Client: client, SourceID: "ori.devflow", ViewSource: "plugin:ori.devflow", Git: fakeGit{states: map[string]GitState{paths["alpha"]: {}, paths["beta"]: {}}}, Now: func() time.Time { return now }}
	snapshot, err := service.Snapshot(context.Background(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.RehydrateMetadata(context.Background(), snapshot); err != nil {
		t.Fatal(err)
	}
	if len(client.workspaceCalls) != 2 || len(client.paneCalls) != 2 {
		t.Fatalf("metadata calls workspaces=%#v panes=%#v", client.workspaceCalls, client.paneCalls)
	}
	if client.paneCalls[0].tokens["next_task"] == "" || client.paneCalls[0].source != "ori.devflow" {
		t.Fatalf("pane metadata = %#v", client.paneCalls[0])
	}
	if err := service.ApplyManagedView(context.Background()); err != nil {
		t.Fatal(err)
	}
	if client.viewParams["source"] != "plugin:ori.devflow" || client.viewParams["filter"] != nil {
		t.Fatalf("view params = %#v", client.viewParams)
	}
	if err := service.ClearManagedView(context.Background()); err != nil || client.clearedSource != "plugin:ori.devflow" {
		t.Fatalf("clear view = %q, %v", client.clearedSource, err)
	}
}

func TestMetadataUsesThePersistedSourceForEachManagedFeature(t *testing.T) {
	now := time.Date(2026, time.July, 23, 16, 0, 0, 0, time.UTC)
	state, agents, paths := fixtureState(t, now)
	alpha := state.Features["repo:alpha"]
	alpha.SourceID = "team.alpha"
	state.Features["repo:alpha"] = alpha
	beta := state.Features["repo:beta"]
	beta.SourceID = "team.beta"
	state.Features["repo:beta"] = beta
	client := &fakeHerdr{agents: agents}
	service := &Service{Store: testStore{state: state}, Client: client, SourceID: "ori.devflow", Git: fakeGit{states: map[string]GitState{paths["alpha"]: {}, paths["beta"]: {}}}, Now: func() time.Time { return now }}
	snapshot, err := service.Snapshot(context.Background(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.RehydrateMetadata(context.Background(), snapshot); err != nil {
		t.Fatal(err)
	}
	sources := make(map[string]string)
	for _, call := range client.workspaceCalls {
		sources[call.id] = call.source
	}
	if sources["w1"] != "team.alpha" || sources["w2"] != "team.beta" {
		t.Fatalf("workspace metadata sources = %#v", sources)
	}
}

func TestMetadataRespectsPerFeatureOptOut(t *testing.T) {
	now := time.Date(2026, time.July, 23, 16, 0, 0, 0, time.UTC)
	state, agents, paths := fixtureState(t, now)
	disabled := false
	alpha := state.Features["repo:alpha"]
	alpha.MetadataEnabled = &disabled
	state.Features["repo:alpha"] = alpha
	client := &fakeHerdr{agents: agents}
	service := &Service{Store: testStore{state: state}, Client: client, Git: fakeGit{states: map[string]GitState{paths["alpha"]: {}, paths["beta"]: {}}}, Now: func() time.Time { return now }}
	snapshot, err := service.Snapshot(context.Background(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.RehydrateMetadata(context.Background(), snapshot); err != nil {
		t.Fatal(err)
	}
	if len(client.workspaceCalls) != 1 || client.workspaceCalls[0].id != "w2" || len(client.paneCalls) != 1 || client.paneCalls[0].id != "w2:p1" {
		t.Fatalf("metadata calls with alpha disabled: workspace=%#v pane=%#v", client.workspaceCalls, client.paneCalls)
	}
}

func TestMetadataSkipsAClosedWorkspaceWhoseSavedAgentsAreMissing(t *testing.T) {
	client := &fakeHerdr{}
	live := herdr.AgentInfo{PaneID: "w2:p1"}
	service := &Service{Client: client, SourceID: "ori.devflow"}
	snapshot := Snapshot{
		Features: []FeatureSnapshot{
			{Feature: model.Feature{Name: "closed"}, WorkspaceID: "w1", MetadataEnabled: true},
			{Feature: model.Feature{Name: "live"}, WorkspaceID: "w2", MetadataEnabled: true},
		},
		Rows: []AgentRow{
			{WorkspaceID: "w1", Missing: true},
			{WorkspaceID: "w2", Live: &live},
		},
	}
	if err := service.RehydrateMetadata(context.Background(), snapshot); err != nil {
		t.Fatal(err)
	}
	if len(client.workspaceCalls) != 1 || client.workspaceCalls[0].id != "w2" || len(client.paneCalls) != 1 || client.paneCalls[0].id != "w2:p1" {
		t.Fatalf("metadata calls should ignore the closed workspace: workspace=%#v pane=%#v", client.workspaceCalls, client.paneCalls)
	}
}

func TestWorkspaceTokensClearAResolvedContinuation(t *testing.T) {
	tokens := workspaceTokens(FeatureSnapshot{Feature: model.Feature{RepositoryID: "repo", Name: "bridge", Branch: "feature/bridge"}, Task: tasklist.Progress{Exists: true, Total: 1, Completed: 1, Next: "All checklist items are marked complete"}})
	if value, found := tokens["next_schedule"]; !found || value != "" {
		t.Fatalf("resolved next_schedule token = %q, present=%v", value, found)
	}
}

func TestRenderHumanHonorsColorAndSanitizesTaskText(t *testing.T) {
	snapshot := Snapshot{GeneratedAt: time.Now(), Rows: []AgentRow{{Feature: "bridge", Role: "builder", AgentName: "ori-bridge-builder", Kind: "claude", ObservedStatus: model.AgentBlocked, Task: tasklist.Progress{Exists: true, Total: 2, Completed: 1, Next: "1.1 bad\x1b[31m task\ntext"}, Git: GitState{Dirty: true}}}}
	plain := RenderHuman(snapshot, RenderOptions{Color: false})
	if strings.ContainsRune(plain, '\x1b') || !strings.Contains(plain, "blocked") || !strings.Contains(plain, "bad [31m task text") {
		t.Fatalf("plain render = %q", plain)
	}
	t.Logf("deterministic status fixture:\n%s", plain)
	colored := RenderHuman(snapshot, RenderOptions{Color: true})
	if !strings.Contains(colored, "\x1b[31m! blocked\x1b[0m") {
		t.Fatalf("colored render = %q", colored)
	}
}

func TestWatchUsesEventRefreshAndPollingFallback(t *testing.T) {
	now := time.Now().UTC()
	state, agents, paths := fixtureState(t, now)
	client := &fakeHerdr{agents: agents}
	stream := &testStream{events: make(chan error, 1)}
	service := &Service{Store: testStore{state: state}, Client: client, Git: fakeGit{states: map[string]GitState{paths["alpha"]: {}, paths["beta"]: {}}}, Subscribe: func(context.Context, []map[string]any) (EventStream, error) { return stream, nil }, WatchPollInterval: 5 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	emitted := make(chan Snapshot, 3)
	done := make(chan error, 1)
	go func() { done <- service.Watch(ctx, Options{}, func(snapshot Snapshot) { emitted <- snapshot }) }()
	<-emitted // Initial snapshot.
	stream.events <- nil
	<-emitted // Event-triggered refresh.
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("event Watch() error = %v", err)
	}

	fallback := &Service{Store: testStore{state: state}, Client: client, Git: fakeGit{states: map[string]GitState{paths["alpha"]: {}, paths["beta"]: {}}}, Subscribe: func(context.Context, []map[string]any) (EventStream, error) {
		return nil, errors.New("socket unavailable")
	}, WatchPollInterval: 5 * time.Millisecond}
	ctx, cancel = context.WithCancel(context.Background())
	emitted = make(chan Snapshot, 3)
	done = make(chan error, 1)
	go func() { done <- fallback.Watch(ctx, Options{}, func(snapshot Snapshot) { emitted <- snapshot }) }()
	<-emitted // Initial.
	<-emitted // Polling fallback.
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("fallback Watch() error = %v", err)
	}
}

func TestOverlappingStatusHooksKeepMetadataAndViewsSourceScoped(t *testing.T) {
	now := time.Now().UTC()
	state, agents, paths := fixtureState(t, now)
	client := &fakeHerdr{agents: agents}
	service := &Service{Store: testStore{state: state}, Client: client, SourceID: "ori.devflow", ViewSource: "plugin:ori.devflow", Git: fakeGit{states: map[string]GitState{paths["alpha"]: {}, paths["beta"]: {}}}, Now: func() time.Time { return now }}
	snapshot, err := service.Snapshot(context.Background(), Options{})
	if err != nil {
		t.Fatal(err)
	}

	const hooks = 8
	errors := make(chan error, hooks)
	var group sync.WaitGroup
	for index := 0; index < hooks; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			if err := service.RehydrateMetadata(context.Background(), snapshot); err != nil {
				errors <- err
				return
			}
			if err := service.ApplyManagedView(context.Background()); err != nil {
				errors <- err
			}
		}()
	}
	group.Wait()
	close(errors)
	for err := range errors {
		t.Fatalf("overlapping status hook failed: %v", err)
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	if len(client.workspaceCalls) != hooks*2 || len(client.paneCalls) != hooks*2 || client.viewParams["source"] != "plugin:ori.devflow" {
		t.Fatalf("overlapping hook calls = workspaces:%d panes:%d view:%#v", len(client.workspaceCalls), len(client.paneCalls), client.viewParams)
	}
}

type testStream struct {
	events chan error
	closed bool
	mu     sync.Mutex
}

func (s *testStream) Next() (json.RawMessage, error) {
	err, ok := <-s.events
	if !ok {
		return nil, io.EOF
	}
	if err != nil {
		return nil, err
	}
	return json.RawMessage(`{"type":"pane.updated"}`), nil
}

func (s *testStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.closed = true
		close(s.events)
	}
	return nil
}

func fixtureState(t *testing.T, now time.Time) (model.BridgeState, []herdr.AgentInfo, map[string]string) {
	t.Helper()
	paths := map[string]string{"alpha": filepath.Join(t.TempDir(), "alpha"), "beta": filepath.Join(t.TempDir(), "beta")}
	for name, path := range paths {
		if err := os.MkdirAll(filepath.Join(path, "tasks"), 0700); err != nil {
			t.Fatal(err)
		}
		contents := "- [x] 1.0 Parent\n- [ ] 1.1 Continue " + name + "\n"
		if err := os.WriteFile(filepath.Join(path, "tasks", "tasks-"+name+".md"), []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	state := model.NewBridgeState()
	alphaNative := model.NativeSession{Source: "herdr:claude", Agent: "claude", Kind: "id", Value: "alpha-session"}
	betaNative := model.NativeSession{Source: "herdr:codex", Agent: "codex", Kind: "id", Value: "beta-session"}
	alpha := model.Feature{RepositoryID: "repo", Name: "alpha", Branch: "feature/alpha", Path: paths["alpha"]}
	beta := model.Feature{RepositoryID: "repo", Name: "beta", Branch: "feature/beta", Path: paths["beta"]}
	alphaAgent := model.RoleAgent{Role: "builder", Name: "ori-alpha-builder", Kind: "claude", WorkspaceID: "w1", PaneID: "w1:p1", TerminalID: "term-1", NativeSession: alphaNative, Status: model.AgentIdle, UpdatedAt: now.Add(-time.Minute)}
	betaAgent := model.RoleAgent{Role: "tester", Name: "ori-beta-tester", Kind: "codex", WorkspaceID: "w2", PaneID: "w2:p1", TerminalID: "term-2", NativeSession: betaNative, Status: model.AgentIdle, UpdatedAt: now.Add(-2 * time.Minute)}
	state.Features["repo:alpha"] = model.FeatureState{Feature: alpha, WorkspaceID: "w1", Agents: map[string]model.RoleAgent{"builder": alphaAgent}, Schedules: map[string]model.Schedule{"sch-alpha": {ID: "sch-alpha", DueAt: now.Add(time.Hour), RetryUntil: now.Add(2 * time.Hour), State: model.SchedulePending}}}
	state.Features["repo:beta"] = model.FeatureState{Feature: beta, WorkspaceID: "w2", Agents: map[string]model.RoleAgent{"tester": betaAgent}}
	return state, []herdr.AgentInfo{
		{Name: alphaAgent.Name, Agent: "claude", WorkspaceID: "w1", PaneID: "w1:p9", TerminalID: "term-9", AgentStatus: model.AgentWorking, AgentSession: &alphaNative},
		{Name: betaAgent.Name, Agent: "codex", WorkspaceID: "w2", PaneID: "w2:p1", TerminalID: "term-2", AgentStatus: model.AgentBlocked, AgentSession: &betaNative},
	}, paths
}

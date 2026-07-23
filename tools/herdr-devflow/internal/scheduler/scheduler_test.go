package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/herdr"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/model"
)

type memoryStore struct {
	operation chan struct{}
	mu        sync.Mutex
	state     model.BridgeState
	saves     []model.BridgeState
	saveCalls int
	failSave  int
}

func newMemoryStore() *memoryStore {
	operation := make(chan struct{}, 1)
	operation <- struct{}{}
	return &memoryStore{operation: operation, state: model.NewBridgeState()}
}

func (s *memoryStore) Lock(ctx context.Context) (func(), error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.operation:
		return func() { s.operation <- struct{}{} }, nil
	}
}

func (s *memoryStore) Load() (model.BridgeState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return copyState(s.state), nil
}

func (s *memoryStore) Save(value model.BridgeState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.saveCalls++
	if s.failSave == s.saveCalls {
		return errors.New("simulated atomic state write failure")
	}
	s.state = copyState(value)
	s.saves = append(s.saves, copyState(value))
	return nil
}

func (s *memoryStore) seed(feature model.Feature, agent model.RoleAgent, schedules ...model.Schedule) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := model.NewBridgeState()
	featureState := model.FeatureState{Feature: feature, WorkspaceID: agent.WorkspaceID, Agents: map[string]model.RoleAgent{agent.Role: agent}, Schedules: make(map[string]model.Schedule)}
	for _, schedule := range schedules {
		featureState.Schedules[schedule.ID] = schedule
	}
	state.Features[featureKey(feature)] = featureState
	s.state = state
}

func copyState(value model.BridgeState) model.BridgeState {
	encoded, _ := json.Marshal(value)
	var result model.BridgeState
	_ = json.Unmarshal(encoded, &result)
	if result.Features == nil {
		result.Features = make(map[string]model.FeatureState)
	}
	return result
}

type fakeHerdr struct {
	mu           sync.Mutex
	agents       map[string]herdr.AgentInfo
	listError    error
	getError     error
	promptError  error
	promptResult *herdr.AgentInfo
	promptCalls  int
	promptTarget string
	promptText   string
	onPrompt     func()
}

func newFakeHerdr(agent herdr.AgentInfo) *fakeHerdr {
	return &fakeHerdr{agents: map[string]herdr.AgentInfo{agent.Name: agent}}
}

func (f *fakeHerdr) AgentGetInfo(_ context.Context, target string) (herdr.AgentInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getError != nil {
		return herdr.AgentInfo{}, f.getError
	}
	agent, ok := f.agents[target]
	if !ok {
		return herdr.AgentInfo{}, &model.StageError{Stage: "agent get", Code: model.ErrAgentMissing, Message: "agent not found"}
	}
	return agent, nil
}

func (f *fakeHerdr) AgentListInfo(_ context.Context) ([]herdr.AgentInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listError != nil {
		return nil, f.listError
	}
	result := make([]herdr.AgentInfo, 0, len(f.agents))
	for _, agent := range f.agents {
		result = append(result, agent)
	}
	return result, nil
}

func (f *fakeHerdr) AgentPromptInfo(_ context.Context, target, text string, _ time.Duration) (herdr.AgentInfo, error) {
	f.mu.Lock()
	f.promptCalls++
	f.promptTarget = target
	f.promptText = text
	onPrompt := f.onPrompt
	err := f.promptError
	agent := f.agents[target]
	f.mu.Unlock()
	if onPrompt != nil {
		onPrompt()
	}
	if err != nil {
		return herdr.AgentInfo{}, err
	}
	if f.promptResult != nil {
		return *f.promptResult, nil
	}
	return agent, nil
}

func TestParseDueAtAcceptsOneTimeAbsoluteOrLocalTimestamps(t *testing.T) {
	now := time.Date(2026, time.July, 23, 12, 0, 0, 0, time.UTC)
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}

	local, zone, err := ParseDueAt("2026-07-24 09:30", now, location)
	if err != nil {
		t.Fatalf("ParseDueAt(local) error = %v", err)
	}
	if want := time.Date(2026, time.July, 24, 13, 30, 0, 0, time.UTC); !local.Equal(want) || zone != "America/New_York" {
		t.Fatalf("ParseDueAt(local) = %s, %q; want %s, America/New_York", local, zone, want)
	}

	abs, absoluteZone, err := ParseDueAt("2026-07-24T09:30:00-04:00", now, location)
	if err != nil || !abs.Equal(local) || absoluteZone == "" {
		t.Fatalf("ParseDueAt(RFC3339) = %s, %q, %v; want %s and a timezone", abs, absoluteZone, err, local)
	}
	if _, _, err := ParseDueAt("every weekday at 09:30", now, location); err == nil {
		t.Fatal("ParseDueAt accepted a recurring expression")
	}
	if _, _, err := ParseDueAt("2026-07-23 07:30", now, location); err == nil {
		t.Fatal("ParseDueAt accepted a past timestamp")
	}
	if _, _, err := ParseDueAt("2026-03-08 02:30", time.Date(2026, time.March, 1, 0, 0, 0, 0, time.UTC), location); err == nil {
		t.Fatal("ParseDueAt accepted a nonexistent spring-forward local timestamp")
	}
}

func TestCreatePersistsFeatureScopedOneTimeSchedule(t *testing.T) {
	now := testNow()
	feature, agent, _ := testIdentity()
	store := newMemoryStore()
	store.seed(feature, agent)
	service := &Service{Store: store, Now: func() time.Time { return now }, NewID: func() (string, error) { return "sch-create", nil }}

	schedule, err := service.Create(context.Background(), CreateRequest{
		Feature: feature, Agent: agent, DueAt: now.Add(time.Hour), Timezone: "America/New_York", Prompt: "Continue safely.", RetryWindow: 15 * time.Minute,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if schedule.ID != "sch-create" || schedule.State != model.SchedulePending || !schedule.RetryUntil.Equal(now.Add(time.Hour+15*time.Minute)) {
		t.Fatalf("Create() = %#v", schedule)
	}
	state, _ := store.Load()
	got := state.Features[featureKey(feature)].Schedules[schedule.ID]
	if got.Prompt != "Continue safely." || got.AgentName != agent.Name || got.NativeSession != agent.NativeSession {
		t.Fatalf("persisted schedule = %#v", got)
	}
}

func TestDispatcherDeliversStubAgentAtOneMinuteDueTime(t *testing.T) {
	current := testNow()
	feature, agent, live := testIdentity()
	store := newMemoryStore()
	store.seed(feature, agent)
	client := newFakeHerdr(live)
	service := &Service{
		Store:  store,
		Client: client,
		Now: func() time.Time {
			return current
		},
		NewID: func() (string, error) { return "sch-one-minute", nil },
	}
	record, err := service.Create(context.Background(), CreateRequest{Feature: feature, Agent: agent, DueAt: current.Add(time.Minute), Timezone: "America/New_York", Prompt: "Continue safely.", RetryWindow: 15 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if results, dispatchErr := service.DispatchDue(context.Background()); dispatchErr != nil || len(results) != 0 || client.promptCalls != 0 {
		t.Fatalf("early DispatchDue() = %#v, %v, prompts=%d", results, dispatchErr, client.promptCalls)
	}
	current = record.DueAt
	results, err := service.DispatchDue(context.Background())
	if err != nil || len(results) != 1 || results[0].Outcome != model.ScheduleDelivered || client.promptCalls != 1 {
		t.Fatalf("due DispatchDue() = %#v, %v, prompts=%d", results, err, client.promptCalls)
	}
}

func TestDispatchDueDeliversOnceAfterPersistingDeliveringBoundary(t *testing.T) {
	now := testNow()
	feature, agent, live := testIdentity()
	schedule := dueSchedule(now, feature, agent)
	store := newMemoryStore()
	store.seed(feature, agent, schedule)
	client := newFakeHerdr(live)
	service := &Service{Store: store, Client: client, Now: func() time.Time { return now }}

	results, err := service.DispatchDue(context.Background())
	if err != nil {
		t.Fatalf("DispatchDue() error = %v", err)
	}
	if len(results) != 1 || results[0].Outcome != model.ScheduleDelivered || client.promptCalls != 1 {
		t.Fatalf("DispatchDue() = %#v, promptCalls=%d", results, client.promptCalls)
	}
	if len(store.saves) < 2 {
		t.Fatalf("save count = %d, want delivering plus final result", len(store.saves))
	}
	first := store.saves[len(store.saves)-2].Features[featureKey(feature)].Schedules[schedule.ID]
	if first.State != model.ScheduleDelivering {
		t.Fatalf("state before prompt = %q, want delivering", first.State)
	}
	second, err := service.DispatchDue(context.Background())
	if err != nil || len(second) != 0 || client.promptCalls != 1 {
		t.Fatalf("second DispatchDue() = %#v, %v, promptCalls=%d", second, err, client.promptCalls)
	}
}

func TestDispatchDueDoesNotSubmitWhenDeliveringBoundaryCannotPersist(t *testing.T) {
	now := testNow()
	feature, agent, live := testIdentity()
	schedule := dueSchedule(now, feature, agent)
	store := newMemoryStore()
	store.seed(feature, agent, schedule)
	store.failSave = 1
	client := newFakeHerdr(live)
	service := &Service{Store: store, Client: client, Now: func() time.Time { return now }}

	if _, err := service.DispatchDue(context.Background()); err == nil {
		t.Fatal("DispatchDue() succeeded despite failing to persist delivering")
	}
	if client.promptCalls != 0 {
		t.Fatalf("prompt calls = %d, want none before a durable delivering transition", client.promptCalls)
	}
}

func TestDispatchDueWaitsForBusyOrMissingAgentThenFailsAfterRetryWindow(t *testing.T) {
	now := testNow()
	feature, agent, live := testIdentity()
	live.AgentStatus = model.AgentWorking
	schedule := dueSchedule(now, feature, agent)
	store := newMemoryStore()
	store.seed(feature, agent, schedule)
	client := newFakeHerdr(live)
	service := &Service{Store: store, Client: client, Now: func() time.Time { return now }}

	results, err := service.DispatchDue(context.Background())
	if err != nil || len(results) != 1 || results[0].Outcome != model.ScheduleWaiting || client.promptCalls != 0 {
		t.Fatalf("busy DispatchDue() = %#v, %v, prompts=%d", results, err, client.promptCalls)
	}
	service.Now = func() time.Time { return schedule.RetryUntil.Add(time.Second) }
	results, err = service.DispatchDue(context.Background())
	if err != nil || len(results) != 1 || results[0].Outcome != model.ScheduleFailed || client.promptCalls != 0 {
		t.Fatalf("expired DispatchDue() = %#v, %v, prompts=%d", results, err, client.promptCalls)
	}
}

func TestDispatchDueDeliversWhenBusyAgentBecomesIdleWithinRetryWindow(t *testing.T) {
	now := testNow()
	feature, agent, live := testIdentity()
	live.AgentStatus = model.AgentBlocked
	schedule := dueSchedule(now, feature, agent)
	store := newMemoryStore()
	store.seed(feature, agent, schedule)
	client := newFakeHerdr(live)
	service := &Service{Store: store, Client: client, Now: func() time.Time { return now }}

	first, err := service.DispatchDue(context.Background())
	if err != nil || len(first) != 1 || first[0].Outcome != model.ScheduleWaiting {
		t.Fatalf("blocked DispatchDue() = %#v, %v", first, err)
	}
	live.AgentStatus = model.AgentIdle
	client.mu.Lock()
	client.agents[live.Name] = live
	client.mu.Unlock()
	service.Now = func() time.Time { return now.Add(time.Minute) }
	second, err := service.DispatchDue(context.Background())
	if err != nil || len(second) != 1 || second[0].Outcome != model.ScheduleDelivered || client.promptCalls != 1 {
		t.Fatalf("idle DispatchDue() = %#v, %v, prompts=%d", second, err, client.promptCalls)
	}
}

func TestDispatchDueMarksLateOrUnreachableSchedulesFailedAfterRetryWindow(t *testing.T) {
	now := testNow()
	feature, agent, live := testIdentity()
	late := dueSchedule(now, feature, agent)
	late.DueAt = now.Add(-16 * time.Minute)
	late.RetryUntil = late.DueAt.Add(15 * time.Minute)
	store := newMemoryStore()
	store.seed(feature, agent, late)
	client := newFakeHerdr(live)
	service := &Service{Store: store, Client: client, Now: func() time.Time { return now }}

	results, err := service.DispatchDue(context.Background())
	if err != nil || len(results) != 1 || results[0].Outcome != model.ScheduleFailed || client.promptCalls != 0 {
		t.Fatalf("late DispatchDue() = %#v, %v, prompts=%d", results, err, client.promptCalls)
	}

	missing := dueSchedule(now, feature, agent)
	missing.ID = "sch-missing"
	store = newMemoryStore()
	store.seed(feature, agent, missing)
	client = newFakeHerdr(live)
	client.listError = errors.New("Herdr unavailable")
	service = &Service{Store: store, Client: client, Now: func() time.Time { return now }}
	results, err = service.DispatchDue(context.Background())
	if err != nil || len(results) != 1 || results[0].Outcome != model.ScheduleWaiting {
		t.Fatalf("unreachable DispatchDue() = %#v, %v", results, err)
	}
	service.Now = func() time.Time { return missing.RetryUntil.Add(time.Second) }
	results, err = service.DispatchDue(context.Background())
	if err != nil || len(results) != 1 || results[0].Outcome != model.ScheduleFailed || client.promptCalls != 0 {
		t.Fatalf("expired unreachable DispatchDue() = %#v, %v, prompts=%d", results, err, client.promptCalls)
	}
}

func TestDispatchDueTreatsUnconfirmedPromptAsUncertainAndNeverRetriesIt(t *testing.T) {
	now := testNow()
	feature, agent, live := testIdentity()
	schedule := dueSchedule(now, feature, agent)
	store := newMemoryStore()
	store.seed(feature, agent, schedule)
	client := newFakeHerdr(live)
	client.promptError = errors.New("connection closed after request")
	service := &Service{Store: store, Client: client, Now: func() time.Time { return now }}

	results, err := service.DispatchDue(context.Background())
	if err != nil || len(results) != 1 || results[0].Outcome != model.ScheduleUncertain || client.promptCalls != 1 {
		t.Fatalf("DispatchDue() = %#v, %v, prompts=%d", results, err, client.promptCalls)
	}
	client.promptError = nil
	results, err = service.DispatchDue(context.Background())
	if err != nil || len(results) != 0 || client.promptCalls != 1 {
		t.Fatalf("retry of uncertain = %#v, %v, prompts=%d", results, err, client.promptCalls)
	}
}

func TestDispatchDueTreatsIncompleteAcknowledgementAsUncertain(t *testing.T) {
	now := testNow()
	feature, agent, live := testIdentity()
	schedule := dueSchedule(now, feature, agent)
	store := newMemoryStore()
	store.seed(feature, agent, schedule)
	client := newFakeHerdr(live)
	client.promptResult = &herdr.AgentInfo{Name: live.Name}
	service := &Service{Store: store, Client: client, Now: func() time.Time { return now }}

	results, err := service.DispatchDue(context.Background())
	if err != nil || len(results) != 1 || results[0].Outcome != model.ScheduleUncertain || client.promptCalls != 1 {
		t.Fatalf("DispatchDue() = %#v, %v, prompts=%d", results, err, client.promptCalls)
	}
}

func TestDispatchDueRetriesOnlyDefiniteMissingAgentFailure(t *testing.T) {
	now := testNow()
	feature, agent, live := testIdentity()
	schedule := dueSchedule(now, feature, agent)
	store := newMemoryStore()
	store.seed(feature, agent, schedule)
	client := newFakeHerdr(live)
	client.promptError = &model.StageError{Stage: "agent prompt", Code: model.ErrAgentMissing, Message: "agent disappeared"}
	service := &Service{Store: store, Client: client, Now: func() time.Time { return now }}

	results, err := service.DispatchDue(context.Background())
	if err != nil || len(results) != 1 || results[0].Outcome != model.ScheduleWaiting {
		t.Fatalf("definite missing DispatchDue() = %#v, %v", results, err)
	}
	client.promptError = nil
	results, err = service.DispatchDue(context.Background())
	if err != nil || len(results) != 1 || results[0].Outcome != model.ScheduleDelivered || client.promptCalls != 2 {
		t.Fatalf("retry DispatchDue() = %#v, %v, prompts=%d", results, err, client.promptCalls)
	}
}

func TestDispatchDueMarksInterruptedDeliveryUncertain(t *testing.T) {
	now := testNow()
	feature, agent, live := testIdentity()
	schedule := dueSchedule(now, feature, agent)
	schedule.State = model.ScheduleDelivering
	schedule.Attempts = 1
	store := newMemoryStore()
	store.seed(feature, agent, schedule)
	client := newFakeHerdr(live)
	service := &Service{Store: store, Client: client, Now: func() time.Time { return now }}

	results, err := service.DispatchDue(context.Background())
	if err != nil || len(results) != 1 || results[0].Outcome != model.ScheduleUncertain || client.promptCalls != 0 {
		t.Fatalf("DispatchDue() = %#v, %v, prompts=%d", results, err, client.promptCalls)
	}
}

func TestDispatchDueRestoresNativeSessionTargetWithoutStartingAnotherAgent(t *testing.T) {
	now := testNow()
	feature, agent, live := testIdentity()
	schedule := dueSchedule(now, feature, agent)
	deleteName := live.Name
	live.Name = "ori-repo-bridge-builder-restored"
	live.PaneID = "w9:p7"
	live.TerminalID = "term-restored"
	store := newMemoryStore()
	store.seed(feature, agent, schedule)
	client := newFakeHerdr(live)
	service := &Service{Store: store, Client: client, Now: func() time.Time { return now }}

	results, err := service.DispatchDue(context.Background())
	if err != nil || len(results) != 1 || results[0].Outcome != model.ScheduleDelivered || client.promptTarget != live.Name {
		t.Fatalf("restored DispatchDue() = %#v, %v, target=%q", results, err, client.promptTarget)
	}
	if client.promptTarget == deleteName {
		t.Fatal("dispatcher prompted the stale target instead of native session restoration")
	}
}

func TestDispatchDueSerializesOverlappingDispatchersWithoutDuplicatePrompt(t *testing.T) {
	now := testNow()
	feature, agent, live := testIdentity()
	schedule := dueSchedule(now, feature, agent)
	store := newMemoryStore()
	store.seed(feature, agent, schedule)
	client := newFakeHerdr(live)
	started := make(chan struct{})
	release := make(chan struct{})
	client.onPrompt = func() {
		state, err := store.Load()
		if err != nil {
			t.Errorf("Load() during prompt = %v", err)
		}
		if got := state.Features[featureKey(feature)].Schedules[schedule.ID].State; got != model.ScheduleDelivering {
			t.Errorf("state during prompt = %q, want delivering", got)
		}
		close(started)
		<-release
	}
	service := &Service{Store: store, Client: client, Now: func() time.Time { return now }}
	first := make(chan error, 1)
	second := make(chan error, 1)
	go func() {
		_, err := service.DispatchDue(context.Background())
		first <- err
	}()
	<-started
	go func() {
		_, err := service.DispatchDue(context.Background())
		second <- err
	}()
	select {
	case err := <-second:
		t.Fatalf("overlapping dispatcher returned before delivery completed: %v", err)
	case <-time.After(75 * time.Millisecond):
		// It is blocked behind the durable dispatch lock as intended.
	}
	close(release)
	if err := <-first; err != nil {
		t.Fatalf("first DispatchDue() error = %v", err)
	}
	if err := <-second; err != nil {
		t.Fatalf("second DispatchDue() error = %v", err)
	}
	if client.promptCalls != 1 {
		t.Fatalf("prompt calls = %d, want exactly one", client.promptCalls)
	}
}

func TestCancelOnlyAllowsUndeliveredSchedule(t *testing.T) {
	now := testNow()
	feature, agent, _ := testIdentity()
	schedule := dueSchedule(now, feature, agent)
	store := newMemoryStore()
	store.seed(feature, agent, schedule)
	service := &Service{Store: store, Now: func() time.Time { return now }}

	canceled, err := service.Cancel(context.Background(), ScheduleRef{RepositoryID: feature.RepositoryID, FeatureName: feature.Name}, schedule.ID)
	if err != nil || canceled.State != model.ScheduleCanceled {
		t.Fatalf("Cancel() = %#v, %v", canceled, err)
	}
	if _, err := service.Cancel(context.Background(), ScheduleRef{RepositoryID: feature.RepositoryID, FeatureName: feature.Name}, schedule.ID); err == nil {
		t.Fatal("Cancel() allowed an already canceled schedule")
	}
}

func testNow() time.Time { return time.Date(2026, time.July, 23, 16, 0, 0, 0, time.UTC) }

func testIdentity() (model.Feature, model.RoleAgent, herdr.AgentInfo) {
	feature := model.Feature{RepositoryID: "repo-123", Name: "bridge", Branch: "feature/bridge", Path: "/tmp/bridge"}
	native := model.NativeSession{Source: "herdr:claude", Agent: "claude", Kind: "id", Value: "native-123"}
	agent := model.RoleAgent{Role: "builder", Name: "ori-repo-bridge-builder", Kind: "claude", WorkspaceID: "w1", PaneID: "w1:p2", TerminalID: "term-2", NativeSession: native, Status: model.AgentIdle}
	live := herdr.AgentInfo{Name: agent.Name, Agent: agent.Kind, WorkspaceID: agent.WorkspaceID, PaneID: agent.PaneID, TerminalID: agent.TerminalID, AgentSession: &native, AgentStatus: model.AgentIdle}
	return feature, agent, live
}

func dueSchedule(now time.Time, feature model.Feature, agent model.RoleAgent) model.Schedule {
	return model.Schedule{ID: "sch-due", FeaturePath: feature.Path, Role: agent.Role, AgentName: agent.Name, AgentKind: agent.Kind, WorkspaceID: agent.WorkspaceID, PaneID: agent.PaneID, TerminalID: agent.TerminalID, NativeSession: agent.NativeSession, DueAt: now.Add(-time.Minute), RetryUntil: now.Add(15 * time.Minute), Timezone: "America/New_York", Prompt: "Continue the requested task safely.", State: model.SchedulePending, CreatedAt: now.Add(-2 * time.Minute), UpdatedAt: now.Add(-2 * time.Minute)}
}

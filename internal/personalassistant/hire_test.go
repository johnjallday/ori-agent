package personalassistant

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/johnjallday/ori-agent/internal/dailybrief"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/personalhq"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/userprofile"
)

type failOnceStateStore struct {
	Store
	failWhen func(*State) bool
	failed   bool
}

func (s *failOnceStateStore) UpdateState(ctx context.Context, state *State, expectedVersion int64) (*State, error) {
	if !s.failed && s.failWhen != nil && s.failWhen(state) {
		s.failed = true
		return nil, errors.New("injected state save failure")
	}
	return s.Store.UpdateState(ctx, state, expectedVersion)
}

type fakeAssistantCreator struct {
	mu     sync.Mutex
	calls  int
	result *personalhq.AssistantWorkspaceResult
	err    error
	seen   personalhq.AssistantCreationOptions
}

func (f *fakeAssistantCreator) CreatePersonalAssistantHQ(_ context.Context, _ string, options personalhq.AssistantCreationOptions) (*personalhq.AssistantWorkspaceResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.seen = options
	if f.result == nil {
		f.result = &personalhq.AssistantWorkspaceResult{
			WorkspaceID: "hq-1", EntryAgentInstanceID: "instance-1",
			GlobalAgentProfileName: options.DisplayName,
		}
	}
	return f.result, f.err
}

type fakeHireHQ struct {
	status          *personalhq.Status
	statusErr       error
	designateErr    error
	onboardingErr   error
	designateCalls  int
	onboardingCalls int
}

func (f *fakeHireHQ) Status(context.Context, string) (*personalhq.Status, error) {
	if f.status == nil {
		return &personalhq.Status{}, f.statusErr
	}
	return f.status, f.statusErr
}
func (f *fakeHireHQ) Designate(_ context.Context, userID, workspaceID string) (*personalhq.Status, error) {
	f.designateCalls++
	if f.designateErr != nil {
		return nil, f.designateErr
	}
	f.status = &personalhq.Status{
		UserID: userID, WorkspaceID: workspaceID, Valid: true,
		Workspace: &session.Workspace{ID: workspaceID, OwnerUserID: userID},
	}
	return f.status, nil
}
func (f *fakeHireHQ) SetOnboardingState(context.Context, string, userprofile.HQOnboardingState) (*personalhq.Status, error) {
	f.onboardingCalls++
	return f.status, f.onboardingErr
}

type fakeHireBriefs struct {
	config      *dailybrief.Config
	getErr      error
	updateErr   error
	updateCalls int
}

func (f *fakeHireBriefs) GetConfig(context.Context, string) (*dailybrief.Config, error) {
	return cloneBriefConfig(f.config), f.getErr
}
func (f *fakeHireBriefs) UpdateConfig(_ context.Context, config dailybrief.Config) (*dailybrief.Config, error) {
	f.updateCalls++
	if f.updateErr != nil {
		return nil, f.updateErr
	}
	config.ConfigRevision++
	f.config = cloneBriefConfig(&config)
	f.getErr = nil
	return cloneBriefConfig(f.config), nil
}

func validHireRequest() HireRequest {
	return HireRequest{
		RequestID: "hire-request-1", IfVersion: 0, DisplayName: DefaultAssistantName,
		Appearance: &types.AgentAppearance{
			Mode: types.AppearanceModeGenerated, Generated: &types.GeneratedAppearance{Color: "#225588"},
		},
		Mandate:    "Help me keep this week's commitments visible.",
		FocusAreas: []string{"plan my day", "keep projects moving"},
		Timezone:   "America/New_York", ScheduleDays: []string{"mon", "tue", "wed", "thu", "fri"},
		ScheduleTime: "08:00", NotifyOnReady: false,
	}
}

func TestHireCoordinator_CreatesDurableIdentityHQBriefAndOnboardingState(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestStore(t)
	creator := &fakeAssistantCreator{}
	hq := &fakeHireHQ{}
	briefs := &fakeHireBriefs{getErr: dailybrief.ErrConfigNotFound}
	coordinator := NewHireCoordinator(store, creator, hq, briefs)

	result, err := coordinator.Hire(ctx, "local", validHireRequest())
	if err != nil {
		t.Fatalf("Hire: %v", err)
	}
	if result.State.Status != StatusActive || result.State.AssistantID == "" ||
		result.State.HQWorkspaceID != "hq-1" || result.State.HQEntryAgentInstanceID != "instance-1" ||
		result.State.GlobalAgentProfileName != DefaultAssistantName || result.State.HiredAt == nil {
		t.Fatalf("result state = %#v", result.State)
	}
	if result.State.AssistantID == result.State.DisplayName {
		t.Fatal("stable assistant identity depends on mutable display name")
	}
	if result.BriefConfig == nil || result.BriefConfig.Timezone != "America/New_York" ||
		result.BriefConfig.ScheduleTime != "08:00" || result.BriefConfig.NotifyOnReady {
		t.Fatalf("brief config = %#v", result.BriefConfig)
	}
	if creator.seen.AssistantID != result.State.AssistantID || creator.seen.RequestID != "hire-request-1" ||
		creator.seen.Role != types.RoleOrchestrator || creator.seen.SystemPromptFragment != PersonalAssistantPromptFragment {
		t.Fatalf("creation options = %#v", creator.seen)
	}
	if hq.designateCalls != 1 || hq.onboardingCalls != 1 || briefs.updateCalls != 1 {
		t.Fatalf("designation=%d onboarding=%d brief updates=%d", hq.designateCalls, hq.onboardingCalls, briefs.updateCalls)
	}
	persisted, err := store.GetState(ctx, "local")
	if err != nil || persisted.Status != StatusActive || persisted.HirePayloadHash == "" || persisted.RepairStep != RepairNone {
		t.Fatalf("persisted = %#v, %v", persisted, err)
	}
}

func TestHireCoordinator_CustomAgreementAndAppearanceNeedNoModelDependency(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestStore(t)
	creator := &fakeAssistantCreator{}
	briefs := &fakeHireBriefs{getErr: dailybrief.ErrConfigNotFound}
	coordinator := NewHireCoordinator(store, creator, &fakeHireHQ{}, briefs)
	request := validHireRequest()
	request.DisplayName = "Atlas"
	request.Mandate = "Protect focused work and flag commitments at risk."
	request.FocusAreas = []string{"prepare_for_meetings"}
	request.Appearance = &types.AgentAppearance{
		Mode:      types.AppearanceModeGenerated,
		Generated: &types.GeneratedAppearance{Color: "#ABCDEF"},
	}
	result, err := coordinator.Hire(ctx, "local", request)
	if err != nil {
		t.Fatalf("Hire without any model dependency: %v", err)
	}
	if result.State.DisplayName != "Atlas" || result.State.Mandate != request.Mandate ||
		len(result.State.FocusAreas) != 1 || result.State.FocusAreas[0] != FocusPrepareForMeetings ||
		result.State.Appearance.GeneratedColor() != "#abcdef" {
		t.Fatalf("custom relationship = %#v", result.State)
	}
	if creator.seen.DisplayName != "Atlas" || creator.seen.Appearance.GeneratedColor() != "#abcdef" {
		t.Fatalf("custom creation options = %#v", creator.seen)
	}
}

func TestHireCoordinator_ReplayReturnsSameActiveRecordsWithoutDuplicates(t *testing.T) {
	ctx := context.Background()
	var events []EventType
	originalEmitter := emitPersonalAssistantEvent
	emitPersonalAssistantEvent = func(event EventType, _ logger.Fields) { events = append(events, event) }
	t.Cleanup(func() { emitPersonalAssistantEvent = originalEmitter })
	store, _ := newTestStore(t)
	creator := &fakeAssistantCreator{}
	hq := &fakeHireHQ{}
	briefs := &fakeHireBriefs{getErr: dailybrief.ErrConfigNotFound}
	coordinator := NewHireCoordinator(store, creator, hq, briefs)
	request := validHireRequest()
	first, err := coordinator.Hire(ctx, "local", request)
	if err != nil {
		t.Fatalf("first Hire: %v", err)
	}
	second, err := coordinator.Hire(ctx, "local", request)
	if err != nil {
		t.Fatalf("replay Hire: %v", err)
	}
	if first.State.AssistantID != second.State.AssistantID || first.State.HQWorkspaceID != second.State.HQWorkspaceID || !second.Resumed {
		t.Fatalf("first=%#v second=%#v", first.State, second.State)
	}
	if creator.calls != 1 || hq.designateCalls != 1 || briefs.updateCalls != 1 {
		t.Fatalf("replay caused side effects: creator=%d designate=%d brief=%d", creator.calls, hq.designateCalls, briefs.updateCalls)
	}
	if len(events) != 2 || events[0] != EventHireStarted || events[1] != EventHireCompleted {
		t.Fatalf("idempotent replay emitted duplicate/misleading lifecycle events: %v", events)
	}
}

func TestHireCoordinator_ConcurrentDoubleSubmitCreatesOneRelationship(t *testing.T) {
	ctx := context.Background()
	store, db := newTestStore(t)
	creator := &fakeAssistantCreator{}
	hq := &fakeHireHQ{}
	briefs := &fakeHireBriefs{getErr: dailybrief.ErrConfigNotFound}
	coordinator := NewHireCoordinator(store, creator, hq, briefs)
	request := validHireRequest()

	const submits = 8
	results := make(chan *HireResult, submits)
	errorsSeen := make(chan error, submits)
	var wait sync.WaitGroup
	for range submits {
		wait.Add(1)
		go func() {
			defer wait.Done()
			result, err := coordinator.Hire(ctx, "local", request)
			results <- result
			errorsSeen <- err
		}()
	}
	wait.Wait()
	close(results)
	close(errorsSeen)
	for err := range errorsSeen {
		if err != nil {
			t.Fatalf("concurrent Hire: %v", err)
		}
	}
	assistantID := ""
	for result := range results {
		if result == nil || result.State == nil || result.State.Status != StatusActive {
			t.Fatalf("concurrent result = %#v", result)
		}
		if assistantID == "" {
			assistantID = result.State.AssistantID
		} else if result.State.AssistantID != assistantID {
			t.Fatalf("assistant ids differ: %q and %q", assistantID, result.State.AssistantID)
		}
	}
	var relationships int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM personal_assistant_state`).Scan(&relationships); err != nil || relationships != 1 {
		t.Fatalf("relationship count = %d, %v", relationships, err)
	}
	if creator.calls != 1 || hq.designateCalls != 1 || briefs.updateCalls != 1 {
		t.Fatalf("side effects creator=%d designation=%d brief=%d", creator.calls, hq.designateCalls, briefs.updateCalls)
	}
}

func TestHireCoordinator_ValidatesBeforePersistenceOrCreation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*HireRequest)
	}{
		{"empty name", func(r *HireRequest) { r.DisplayName = "" }},
		{"reserved Ori", func(r *HireRequest) { r.DisplayName = "Ori" }},
		{"unknown focus", func(r *HireRequest) { r.FocusAreas = []string{"telepathy"} }},
		{"invalid timezone", func(r *HireRequest) { r.Timezone = "Mars/Olympus" }},
		{"secret mandate", func(r *HireRequest) { r.Mandate = "Use sk-abcdefghijklmnopqrstuvwxyz" }},
		{"no agreement", func(r *HireRequest) { r.Mandate = ""; r.FocusAreas = nil }},
		{"malformed appearance", func(r *HireRequest) { r.Appearance.Mode = types.AppearanceMode("hologram") }},
		{"unknown character", func(r *HireRequest) {
			r.Appearance = &types.AgentAppearance{
				Mode:      types.AppearanceModeCharacter,
				Character: &types.CharacterAppearance{CatalogID: "not-in-catalog"},
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store, _ := newTestStore(t)
			creator := &fakeAssistantCreator{}
			coordinator := NewHireCoordinator(
				store, creator, &fakeHireHQ{}, &fakeHireBriefs{getErr: dailybrief.ErrConfigNotFound},
			)
			request := validHireRequest()
			test.mutate(&request)
			if _, err := coordinator.Hire(context.Background(), "local", request); !errors.Is(err, ErrValidation) {
				t.Fatalf("Hire error = %v, want validation", err)
			}
			if creator.calls != 0 {
				t.Fatal("invalid request reached workspace creation")
			}
			if _, err := store.GetState(context.Background(), "local"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("invalid request persisted state: %v", err)
			}
		})
	}
}

func TestHireCoordinator_PartialFailuresReplayToExactlyOneActiveRelationship(t *testing.T) {
	tests := []struct {
		name           string
		configure      func(*failOnceStateStore, *fakeHireHQ, *fakeHireBriefs)
		clearFailure   func(*fakeHireHQ, *fakeHireBriefs)
		wantStep       RepairStep
		wantBriefSaves int
	}{
		{
			name: "state save after hq agent creation",
			configure: func(store *failOnceStateStore, _ *fakeHireHQ, _ *fakeHireBriefs) {
				store.failWhen = func(state *State) bool { return state.Status == StatusHiring && state.HQWorkspaceID != "" }
			},
			wantStep: RepairFinalization, wantBriefSaves: 1,
		},
		{
			name: "designation",
			configure: func(_ *failOnceStateStore, hq *fakeHireHQ, _ *fakeHireBriefs) {
				hq.designateErr = errors.New("injected designation failure")
			},
			clearFailure: func(hq *fakeHireHQ, _ *fakeHireBriefs) { hq.designateErr = nil },
			wantStep:     RepairDesignation, wantBriefSaves: 1,
		},
		{
			name: "daily brief config save",
			configure: func(_ *failOnceStateStore, _ *fakeHireHQ, briefs *fakeHireBriefs) {
				briefs.updateErr = errors.New("injected config save failure")
			},
			clearFailure: func(_ *fakeHireHQ, briefs *fakeHireBriefs) { briefs.updateErr = nil },
			wantStep:     RepairDailyBriefConfig, wantBriefSaves: 2,
		},
		{
			name: "final relationship state save",
			configure: func(store *failOnceStateStore, _ *fakeHireHQ, _ *fakeHireBriefs) {
				store.failWhen = func(state *State) bool { return state.Status == StatusActive }
			},
			wantStep: RepairFinalization, wantBriefSaves: 1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			sqliteStore, db := newTestStore(t)
			store := &failOnceStateStore{Store: sqliteStore}
			creator := &fakeAssistantCreator{}
			hq := &fakeHireHQ{}
			briefs := &fakeHireBriefs{getErr: dailybrief.ErrConfigNotFound}
			test.configure(store, hq, briefs)
			coordinator := NewHireCoordinator(store, creator, hq, briefs)

			request := validHireRequest()
			_, firstErr := coordinator.Hire(ctx, "local", request)
			var partial *PartialHireError
			if !errors.As(firstErr, &partial) || partial.Step != test.wantStep || partial.State == nil {
				t.Fatalf("first error = %#v, want partial step %q", firstErr, test.wantStep)
			}
			if test.clearFailure != nil {
				test.clearFailure(hq, briefs)
			}
			result, err := coordinator.Hire(ctx, "local", request)
			if err != nil {
				t.Fatalf("retry Hire: %v", err)
			}
			if result.State.Status != StatusActive || result.State.RepairStep != RepairNone ||
				result.State.AssistantID != partial.State.AssistantID || result.State.HQWorkspaceID != "hq-1" {
				t.Fatalf("retry state = %#v; partial = %#v", result.State, partial.State)
			}
			var relationships int
			if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM personal_assistant_state WHERE user_id = 'local'`).Scan(&relationships); err != nil || relationships != 1 {
				t.Fatalf("relationship count = %d, %v", relationships, err)
			}
			if creator.calls != 2 {
				t.Fatalf("creator calls = %d; want two idempotent attempts", creator.calls)
			}
			if briefs.updateCalls != test.wantBriefSaves {
				t.Fatalf("brief saves = %d; want %d", briefs.updateCalls, test.wantBriefSaves)
			}
		})
	}
}

func TestHireCoordinator_RestartResumeUsesDurableOriginalRhythm(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestStore(t)
	creator := &fakeAssistantCreator{}
	hq := &fakeHireHQ{}
	briefs := &fakeHireBriefs{
		getErr: dailybrief.ErrConfigNotFound, updateErr: errors.New("injected config failure"),
	}
	request := validHireRequest()
	firstCoordinator := NewHireCoordinator(store, creator, hq, briefs)
	if _, err := firstCoordinator.Hire(ctx, "local", request); err == nil {
		t.Fatal("expected the first config save to fail")
	}

	// Simulate a process/browser restart: only the durable request ID survives in
	// the submitted body, while editable rhythm controls have fallen back to
	// defaults that do not match the original operation.
	briefs.updateErr = nil
	retry := request
	retry.ScheduleDays = []string{"sat"}
	retry.ScheduleTime = "19:30"
	retry.NotifyOnReady = true
	secondCoordinator := NewHireCoordinator(store, creator, hq, briefs)
	result, err := secondCoordinator.Hire(ctx, "local", retry)
	if err != nil {
		t.Fatalf("restart retry: %v", err)
	}
	if result.State.Status != StatusActive || result.BriefConfig.ScheduleTime != "08:00" ||
		result.BriefConfig.NotifyOnReady || len(result.BriefConfig.ScheduleDays) != 5 {
		t.Fatalf("restart result = %#v, config=%#v", result.State, result.BriefConfig)
	}
	if persisted, err := store.GetState(ctx, "local"); err != nil || persisted.HirePayloadJSON != "" {
		t.Fatalf("completed operation retained provisional payload: %#v, %v", persisted, err)
	}
}

func TestHireCoordinator_RejectsSecondAssistantAndChangedReplayPayload(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestStore(t)
	coordinator := NewHireCoordinator(
		store, &fakeAssistantCreator{}, &fakeHireHQ{}, &fakeHireBriefs{getErr: dailybrief.ErrConfigNotFound},
	)
	request := validHireRequest()
	if _, err := coordinator.Hire(ctx, "local", request); err != nil {
		t.Fatalf("first Hire: %v", err)
	}
	second := request
	second.RequestID = "hire-request-2"
	if _, err := coordinator.Hire(ctx, "local", second); !errors.Is(err, ErrConflict) {
		t.Fatalf("second assistant error = %v, want conflict", err)
	}
	changed := request
	changed.Mandate = "A different agreement"
	if _, err := coordinator.Hire(ctx, "local", changed); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed replay error = %v, want conflict", err)
	}
}

func TestHireCoordinator_RejectsMissingDependenciesBeforeReadingOrWriting(t *testing.T) {
	coordinator := NewHireCoordinator(nil, nil, nil, nil)
	if _, err := coordinator.Hire(context.Background(), "local", validHireRequest()); err == nil {
		t.Fatal("Hire succeeded without configured dependencies")
	}
}

package personalassistant

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
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

// fakeProfileCreator stands in for the session handler's profile-only creation
// seam. It records what the hire asked for so tests can assert that a hire
// creates a profile and nothing else.
type fakeProfileCreator struct {
	mu     sync.Mutex
	calls  int
	seen   personalhq.AssistantCreationOptions
	result *personalhq.AssistantProfileResult
	err    error
}

func (f *fakeProfileCreator) CreatePersonalAssistantProfile(_ context.Context, options personalhq.AssistantCreationOptions) (*personalhq.AssistantProfileResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.seen = options
	if f.err != nil {
		return nil, f.err
	}
	if f.result != nil {
		return f.result, nil
	}
	// Real ownership is by durable marker, so a replay resolves the same name.
	return &personalhq.AssistantProfileResult{
		GlobalAgentProfileName: options.DisplayName, Reused: f.calls > 1,
	}, nil
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
	}
}

// hireFixture builds the current hire: a store, a profile creator, and the
// legacy HQ dependencies that a fresh hire must never reach.
type hireFixture struct {
	store       Store
	profiles    *fakeProfileCreator
	creator     *fakeAssistantCreator
	hq          *fakeHireHQ
	briefs      *fakeHireBriefs
	coordinator *HireCoordinator
}

func newHireFixture(t *testing.T, store Store) *hireFixture {
	t.Helper()
	fixture := &hireFixture{
		store:    store,
		profiles: &fakeProfileCreator{},
		creator:  &fakeAssistantCreator{},
		hq:       &fakeHireHQ{},
		briefs:   &fakeHireBriefs{getErr: dailybrief.ErrConfigNotFound},
	}
	fixture.coordinator = NewHireCoordinator(
		store, fixture.profiles, fixture.creator, fixture.hq, fixture.briefs)
	return fixture
}

// assertNoHQSideEffects pins the central promise of the amendment: a hire
// creates no workspace, designation, Daily Brief config, or onboarding change.
func (f *hireFixture) assertNoHQSideEffects(t *testing.T) {
	t.Helper()
	if f.creator.calls != 0 {
		t.Fatalf("hire created a workspace (%d creator calls)", f.creator.calls)
	}
	if f.hq.designateCalls != 0 {
		t.Fatalf("hire designated Personal HQ (%d calls)", f.hq.designateCalls)
	}
	if f.hq.onboardingCalls != 0 {
		t.Fatalf("hire completed HQ onboarding (%d calls)", f.hq.onboardingCalls)
	}
	if f.briefs.updateCalls != 0 {
		t.Fatalf("hire wrote a Daily Brief configuration (%d calls)", f.briefs.updateCalls)
	}
}

func TestHireCoordinator_CreatesProfileAndRelationshipWithoutHQ(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestStore(t)
	fixture := newHireFixture(t, store)

	result, err := fixture.coordinator.Hire(ctx, "local", validHireRequest())
	if err != nil {
		t.Fatalf("Hire: %v", err)
	}
	if result.State.Status != StatusAwaitingHQ || result.State.AssistantID == "" ||
		result.State.GlobalAgentProfileName != DefaultAssistantName || result.State.HiredAt == nil {
		t.Fatalf("result state = %#v", result.State)
	}
	if result.State.HQWorkspaceID != "" || result.State.HQEntryAgentInstanceID != "" {
		t.Fatalf("hire created HQ identifiers: %#v", result.State)
	}
	if result.BriefConfig != nil {
		t.Fatalf("hire promised a Daily Brief config: %#v", result.BriefConfig)
	}
	if result.State.AssistantID == result.State.DisplayName {
		t.Fatal("stable assistant identity depends on mutable display name")
	}

	// The profile must be created from the canonical entry specification.
	if fixture.profiles.calls != 1 ||
		fixture.profiles.seen.AssistantID != result.State.AssistantID ||
		fixture.profiles.seen.RequestID != "hire-request-1" ||
		fixture.profiles.seen.Role != types.RoleOrchestrator ||
		fixture.profiles.seen.SystemPromptFragment != PersonalAssistantPromptFragment {
		t.Fatalf("profile creation options = %#v (calls=%d)", fixture.profiles.seen, fixture.profiles.calls)
	}
	fixture.assertNoHQSideEffects(t)

	persisted, err := store.GetState(ctx, "local")
	if err != nil || persisted.Status != StatusAwaitingHQ || persisted.HirePayloadHash == "" ||
		persisted.RepairStep != RepairNone {
		t.Fatalf("persisted = %#v, %v", persisted, err)
	}
	if persisted.HirePayloadJSON != "" {
		t.Fatal("completed hire retained its provisional payload")
	}
	if err := persisted.ValidateStateInvariants(); err != nil {
		t.Fatalf("persisted hire violates the state invariants: %v", err)
	}
}

func TestHireCoordinator_IgnoresStaleDailyBriefRhythmFields(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestStore(t)
	fixture := newHireFixture(t, store)

	// A stale client may still send the rhythm the hire step used to collect.
	// It must be dropped, not validated and not stored — the Map's HQ form owns
	// the schedule now.
	request := validHireRequest()
	request.Timezone = "Mars/Olympus"
	request.ScheduleDays = []string{"notaday"}
	request.ScheduleTime = "99:99"
	request.NotifyOnReady = true

	result, err := fixture.coordinator.Hire(ctx, "local", request)
	if err != nil {
		t.Fatalf("Hire with stale rhythm fields: %v", err)
	}
	if result.State.Status != StatusAwaitingHQ {
		t.Fatalf("state = %#v", result.State)
	}
	fixture.assertNoHQSideEffects(t)
}

func TestHireCoordinator_CustomAgreementAndAppearanceNeedNoModelDependency(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestStore(t)
	fixture := newHireFixture(t, store)
	request := validHireRequest()
	request.DisplayName = "Atlas"
	request.Mandate = "Protect focused work and flag commitments at risk."
	request.FocusAreas = []string{"prepare_for_meetings"}
	request.Appearance = &types.AgentAppearance{
		Mode:      types.AppearanceModeGenerated,
		Generated: &types.GeneratedAppearance{Color: "#ABCDEF"},
	}
	result, err := fixture.coordinator.Hire(ctx, "local", request)
	if err != nil {
		t.Fatalf("Hire without any model dependency: %v", err)
	}
	if result.State.DisplayName != "Atlas" || result.State.Mandate != request.Mandate ||
		len(result.State.FocusAreas) != 1 || result.State.FocusAreas[0] != FocusPrepareForMeetings ||
		result.State.Appearance.GeneratedColor() != "#abcdef" {
		t.Fatalf("custom relationship = %#v", result.State)
	}
	if fixture.profiles.seen.DisplayName != "Atlas" ||
		fixture.profiles.seen.Appearance.GeneratedColor() != "#abcdef" {
		t.Fatalf("custom creation options = %#v", fixture.profiles.seen)
	}
}

func TestHireCoordinator_ReplayReturnsSameProfileWithoutDuplicates(t *testing.T) {
	ctx := context.Background()
	var events []EventType
	originalEmitter := emitPersonalAssistantEvent
	emitPersonalAssistantEvent = func(event EventType, _ logger.Fields) { events = append(events, event) }
	t.Cleanup(func() { emitPersonalAssistantEvent = originalEmitter })

	store, _ := newTestStore(t)
	fixture := newHireFixture(t, store)
	request := validHireRequest()
	first, err := fixture.coordinator.Hire(ctx, "local", request)
	if err != nil {
		t.Fatalf("first Hire: %v", err)
	}
	second, err := fixture.coordinator.Hire(ctx, "local", request)
	if err != nil {
		t.Fatalf("replay Hire: %v", err)
	}
	if first.State.AssistantID != second.State.AssistantID ||
		first.State.GlobalAgentProfileName != second.State.GlobalAgentProfileName || !second.Resumed {
		t.Fatalf("first=%#v second=%#v", first.State, second.State)
	}
	if fixture.profiles.calls != 1 {
		t.Fatalf("replay re-entered profile creation %d times", fixture.profiles.calls)
	}
	fixture.assertNoHQSideEffects(t)
	if len(events) != 2 || events[0] != EventHireStarted || events[1] != EventHireCompleted {
		t.Fatalf("idempotent replay emitted duplicate/misleading lifecycle events: %v", events)
	}
}

func TestHireCoordinator_ConcurrentDoubleSubmitCreatesOneRelationship(t *testing.T) {
	ctx := context.Background()
	store, db := newTestStore(t)
	fixture := newHireFixture(t, store)
	request := validHireRequest()

	const submits = 8
	results := make(chan *HireResult, submits)
	errorsSeen := make(chan error, submits)
	var wait sync.WaitGroup
	for range submits {
		wait.Add(1)
		go func() {
			defer wait.Done()
			result, err := fixture.coordinator.Hire(ctx, "local", request)
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
		if result == nil || result.State == nil || result.State.Status != StatusAwaitingHQ {
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
	if fixture.profiles.calls != 1 {
		t.Fatalf("concurrent submits created %d profiles", fixture.profiles.calls)
	}
	fixture.assertNoHQSideEffects(t)
}

func TestHireCoordinator_ValidatesBeforePersistenceOrCreation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*HireRequest)
	}{
		{"empty name", func(r *HireRequest) { r.DisplayName = "" }},
		{"reserved Ori", func(r *HireRequest) { r.DisplayName = "Ori" }},
		{"unknown focus", func(r *HireRequest) { r.FocusAreas = []string{"telepathy"} }},
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
			fixture := newHireFixture(t, store)
			request := validHireRequest()
			test.mutate(&request)
			if _, err := fixture.coordinator.Hire(context.Background(), "local", request); !errors.Is(err, ErrValidation) {
				t.Fatalf("Hire error = %v, want validation", err)
			}
			if fixture.profiles.calls != 0 {
				t.Fatal("invalid request reached profile creation")
			}
			fixture.assertNoHQSideEffects(t)
			if _, err := store.GetState(context.Background(), "local"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("invalid request persisted state: %v", err)
			}
		})
	}
}

func TestHireCoordinator_ProfileCreationFailureStaysRetryable(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestStore(t)
	fixture := newHireFixture(t, store)
	fixture.profiles.err = errors.New("injected profile creation failure")

	request := validHireRequest()
	if _, err := fixture.coordinator.Hire(ctx, "local", request); err == nil {
		t.Fatal("expected the profile creation to fail")
	}
	persisted, err := store.GetState(ctx, "local")
	if err != nil || persisted.Status != StatusHiring || persisted.GlobalAgentProfileName != "" {
		t.Fatalf("failed hire left %#v, %v", persisted, err)
	}

	fixture.profiles.err = nil
	result, err := fixture.coordinator.Hire(ctx, "local", request)
	if err != nil {
		t.Fatalf("retry Hire: %v", err)
	}
	if result.State.Status != StatusAwaitingHQ || result.State.AssistantID != persisted.AssistantID {
		t.Fatalf("retry produced a different identity: %#v", result.State)
	}
	fixture.assertNoHQSideEffects(t)
}

func TestHireCoordinator_ProfileCreatedButFinalizationFailedReportsBoundedPartial(t *testing.T) {
	ctx := context.Background()
	sqliteStore, db := newTestStore(t)
	store := &failOnceStateStore{
		Store:    sqliteStore,
		failWhen: func(state *State) bool { return state.Status == StatusAwaitingHQ },
	}
	fixture := newHireFixture(t, store)
	request := validHireRequest()

	_, firstErr := fixture.coordinator.Hire(ctx, "local", request)
	var partial *PartialHireError
	if !errors.As(firstErr, &partial) || partial.Step != RepairProfileCreation || partial.State == nil {
		t.Fatalf("first error = %#v, want a bounded profile-creation partial", firstErr)
	}
	// A bounded partial names a safe step and never carries provider text.
	if strings.Contains(string(partial.Step), " ") {
		t.Fatalf("repair step is not a closed code: %q", partial.Step)
	}

	result, err := fixture.coordinator.Hire(ctx, "local", request)
	if err != nil {
		t.Fatalf("retry Hire: %v", err)
	}
	if result.State.Status != StatusAwaitingHQ || result.State.AssistantID != partial.State.AssistantID {
		t.Fatalf("retry state = %#v; partial = %#v", result.State, partial.State)
	}
	// The retry re-enters the creator, which resolves the profile it already owns
	// by durable marker rather than creating a replacement.
	if fixture.profiles.calls != 2 {
		t.Fatalf("profile creation calls = %d; want two idempotent attempts", fixture.profiles.calls)
	}
	if result.State.GlobalAgentProfileName != DefaultAssistantName {
		t.Fatalf("retry bound a different profile: %q", result.State.GlobalAgentProfileName)
	}
	var relationships int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM personal_assistant_state`).Scan(&relationships); err != nil || relationships != 1 {
		t.Fatalf("relationship count = %d, %v", relationships, err)
	}
	fixture.assertNoHQSideEffects(t)
}

func TestHireCoordinator_NameCollisionLeavesNoRelationshipConsequence(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestStore(t)
	fixture := newHireFixture(t, store)
	fixture.profiles.err = personalhq.ErrAssistantNameConflict

	_, err := fixture.coordinator.Hire(ctx, "local", validHireRequest())
	if !errors.Is(err, personalhq.ErrAssistantNameConflict) {
		t.Fatalf("Hire error = %v; want a name conflict", err)
	}
	persisted, getErr := store.GetState(ctx, "local")
	if getErr != nil {
		t.Fatalf("GetState: %v", getErr)
	}
	// The operation stays claimed and retryable, but no profile was adopted.
	if persisted.Status != StatusHiring || persisted.GlobalAgentProfileName != "" {
		t.Fatalf("collision left %#v", persisted)
	}
	fixture.assertNoHQSideEffects(t)
}

func TestHireCoordinator_RejectsSecondAssistantAndChangedReplayPayload(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestStore(t)
	fixture := newHireFixture(t, store)
	request := validHireRequest()
	if _, err := fixture.coordinator.Hire(ctx, "local", request); err != nil {
		t.Fatalf("first Hire: %v", err)
	}
	second := request
	second.RequestID = "hire-request-2"
	if _, err := fixture.coordinator.Hire(ctx, "local", second); !errors.Is(err, ErrConflict) {
		t.Fatalf("second assistant error = %v, want conflict", err)
	}
	changed := request
	changed.Mandate = "A different agreement"
	if _, err := fixture.coordinator.Hire(ctx, "local", changed); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed replay error = %v, want conflict", err)
	}
	if fixture.profiles.calls != 1 {
		t.Fatalf("a rejected request reached profile creation (%d calls)", fixture.profiles.calls)
	}
}

func TestHireCoordinator_RejectsMissingDependenciesBeforeReadingOrWriting(t *testing.T) {
	coordinator := NewHireCoordinator(nil, nil, nil, nil, nil)
	if _, err := coordinator.Hire(context.Background(), "local", validHireRequest()); err == nil {
		t.Fatal("Hire succeeded without configured dependencies")
	}
}

// --- Pre-amendment compatibility -------------------------------------------
//
// An installation upgraded mid-hire still carries an operation whose consequence
// included Personal HQ. Those must be finished through their original path,
// selected by the stored payload version rather than guessed from the state.

// seedLegacyAutoHQOperation persists a hiring relationship carrying a
// pre-amendment hire payload: no version field, and the Daily Brief rhythm the
// hire step used to collect.
func seedLegacyAutoHQOperation(t *testing.T, store Store, withWorkspace bool) *State {
	t.Helper()
	const userID = "local"
	legacy := map[string]any{
		"request_id":   "hire-request-1",
		"display_name": DefaultAssistantName,
		"appearance": map[string]any{
			"mode": "generated", "generated": map[string]any{"color": "#225588"},
		},
		"mandate":         "Help me keep this week's commitments visible.",
		"focus_areas":     []string{"plan_my_day"},
		"timezone":        "America/New_York",
		"schedule_days":   []string{"mon", "tue", "wed", "thu", "fri"},
		"schedule_time":   "08:00",
		"notify_on_ready": false,
	}
	payload, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("marshal legacy payload: %v", err)
	}

	state := NewState(userID)
	state.Status = StatusHiring
	state.DisplayName = DefaultAssistantName
	state.Mandate = "Help me keep this week's commitments visible."
	state.FocusAreas = []FocusArea{FocusPlanMyDay}
	state.LastHireRequestID = "hire-request-1"
	state.HirePayloadJSON = string(payload)
	state.HirePayloadHash = PayloadHash(payload)
	if withWorkspace {
		// The old path already created and recorded a workspace.
		state.HQWorkspaceID = "hq-1"
		state.HQEntryAgentInstanceID = "instance-1"
		state.GlobalAgentProfileName = DefaultAssistantName
	}
	created, err := store.CreateState(context.Background(), state)
	if err != nil {
		t.Fatalf("seed legacy operation: %v", err)
	}
	return created
}

func TestHireCoordinator_ResumesPreAmendmentOperationWithARecordedWorkspace(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestStore(t)
	seeded := seedLegacyAutoHQOperation(t, store, true)
	fixture := newHireFixture(t, store)

	// The client has been upgraded, so it submits a modern profile-only request.
	// The stored operation still decides the path.
	result, err := fixture.coordinator.Hire(ctx, "local", validHireRequest())
	if err != nil {
		t.Fatalf("resume legacy operation: %v", err)
	}
	if result.State.Status != StatusActive {
		t.Fatalf("legacy operation did not finish: %#v", result.State)
	}
	if result.State.HQWorkspaceID != "hq-1" || result.State.HQEntryAgentInstanceID != "instance-1" {
		t.Fatalf("legacy workspace was abandoned or replaced: %#v", result.State)
	}
	if result.State.AssistantID != seeded.AssistantID {
		t.Fatal("legacy resume forked the assistant identity")
	}
	// Its original rhythm is honored, from the durable stored payload.
	if result.BriefConfig == nil || result.BriefConfig.Timezone != "America/New_York" ||
		result.BriefConfig.ScheduleTime != "08:00" {
		t.Fatalf("legacy brief config = %#v", result.BriefConfig)
	}
	if fixture.creator.calls != 1 || fixture.hq.designateCalls != 1 || fixture.briefs.updateCalls != 1 {
		t.Fatalf("legacy resume side effects: creator=%d designate=%d brief=%d",
			fixture.creator.calls, fixture.hq.designateCalls, fixture.briefs.updateCalls)
	}
	// The profile-only path must not also run: that would be a second profile.
	if fixture.profiles.calls != 0 {
		t.Fatalf("legacy resume also entered the profile-only path (%d calls)", fixture.profiles.calls)
	}
}

func TestHireCoordinator_PreAmendmentOperationWithNoDurableResultCannotAdoptByName(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestStore(t)
	seedLegacyAutoHQOperation(t, store, false)
	fixture := newHireFixture(t, store)
	// The old operation recorded no workspace. Its creator is the only thing that
	// can resolve one, and it does so by assistant/request provenance metadata —
	// never by display name.
	fixture.creator.result = &personalhq.AssistantWorkspaceResult{
		WorkspaceID: "hq-owned", EntryAgentInstanceID: "instance-owned",
		GlobalAgentProfileName: DefaultAssistantName,
	}

	result, err := fixture.coordinator.Hire(ctx, "local", validHireRequest())
	if err != nil {
		t.Fatalf("resume legacy operation: %v", err)
	}
	if fixture.creator.seen.AssistantID != result.State.AssistantID ||
		fixture.creator.seen.RequestID != "hire-request-1" {
		t.Fatalf("legacy resume did not scope creation to its own operation: %#v", fixture.creator.seen)
	}
	if result.State.HQWorkspaceID != "hq-owned" {
		t.Fatalf("legacy resume adopted an unrelated workspace: %#v", result.State)
	}
	if fixture.profiles.calls != 0 {
		t.Fatalf("legacy resume also created a standalone profile (%d calls)", fixture.profiles.calls)
	}
}

func TestHireCoordinator_PreAmendmentPartialFailuresReplayToOneActiveRelationship(t *testing.T) {
	tests := []struct {
		name           string
		configure      func(*failOnceStateStore, *fakeHireHQ, *fakeHireBriefs)
		clearFailure   func(*fakeHireHQ, *fakeHireBriefs)
		wantStep       RepairStep
		wantBriefSaves int
	}{
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
			seedLegacyAutoHQOperation(t, store, true)
			fixture := newHireFixture(t, store)
			test.configure(store, fixture.hq, fixture.briefs)

			request := validHireRequest()
			_, firstErr := fixture.coordinator.Hire(ctx, "local", request)
			var partial *PartialHireError
			if !errors.As(firstErr, &partial) || partial.Step != test.wantStep || partial.State == nil {
				t.Fatalf("first error = %#v, want partial step %q", firstErr, test.wantStep)
			}
			if test.clearFailure != nil {
				test.clearFailure(fixture.hq, fixture.briefs)
			}
			result, err := fixture.coordinator.Hire(ctx, "local", request)
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
			if fixture.briefs.updateCalls != test.wantBriefSaves {
				t.Fatalf("brief saves = %d; want %d", fixture.briefs.updateCalls, test.wantBriefSaves)
			}
		})
	}
}

func TestHireCoordinator_PreAmendmentRestartUsesDurableOriginalRhythm(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestStore(t)
	seedLegacyAutoHQOperation(t, store, true)
	fixture := newHireFixture(t, store)
	fixture.briefs.updateErr = errors.New("injected config failure")

	if _, err := fixture.coordinator.Hire(ctx, "local", validHireRequest()); err == nil {
		t.Fatal("expected the first config save to fail")
	}

	// Simulate a process/browser restart: the submitted body no longer carries
	// the rhythm at all, because the modern hire step does not collect one.
	fixture.briefs.updateErr = nil
	result, err := fixture.coordinator.Hire(ctx, "local", validHireRequest())
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

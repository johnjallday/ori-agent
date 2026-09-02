package personalassistant

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/johnjallday/ori-agent/internal/dailybrief"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/personalhq"
)

// hqFixture is a hired assistant that has not built HQ yet, wired to fakes for
// every canonical service the coordinator sequences.
type hqFixture struct {
	store       Store
	creator     *fakeAssistantCreator
	hq          *fakeHireHQ
	briefs      *fakeHireBriefs
	profiles    *fakeProfileReader
	coordinator *HQSetupCoordinator
	assistantID string
	// version is the seeded relationship's actual state version. CreateState
	// always writes version 1 regardless of what the fixture asked for, so tests
	// must read it rather than assume.
	version int64
}

// request returns a valid submission carrying the seeded relationship's current
// version, which is what a real client would have just read.
func (f *hqFixture) request() HQSetupRequest {
	out := validHQSetupRequest()
	out.IfVersion = f.version
	return out
}

func newHQFixture(t *testing.T, store Store) *hqFixture {
	t.Helper()
	seeded, err := store.CreateState(context.Background(), awaitingHQTestState("local", "assistant-a"))
	if err != nil {
		t.Fatalf("seed awaiting_hq relationship: %v", err)
	}
	fixture := &hqFixture{
		store:       store,
		creator:     &fakeAssistantCreator{},
		hq:          &fakeHireHQ{},
		briefs:      &fakeHireBriefs{getErr: dailybrief.ErrConfigNotFound},
		profiles:    ownedProfileReader("Atlas", "assistant-a"),
		assistantID: seeded.AssistantID,
		version:     seeded.StateVersion,
	}
	fixture.coordinator = NewHQSetupCoordinator(
		store, fixture.creator, fixture.hq, fixture.briefs, fixture.profiles)
	return fixture
}

func validHQSetupRequest() HQSetupRequest {
	return HQSetupRequest{
		RequestID: "hq-request-1", IfVersion: 1,
		HQName: "Command Post", Timezone: "America/New_York",
		ScheduleDays: []string{"mon", "wed", "fri"}, ScheduleTime: "07:30",
		Scope: "all", NotifyOnReady: true,
	}
}

func TestHQSetup_BuildsOneCanonicalHQAroundTheHiredAssistant(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestStore(t)
	fixture := newHQFixture(t, store)

	result, err := fixture.coordinator.Setup(ctx, "local", fixture.request())
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if result.State.Status != StatusActive || result.State.HQWorkspaceID != "hq-1" ||
		result.State.HQEntryAgentInstanceID != "instance-1" {
		t.Fatalf("result state = %#v", result.State)
	}
	// The already-hired identity is what the workspace is built around.
	if result.State.AssistantID != fixture.assistantID ||
		result.State.GlobalAgentProfileName != "Atlas" {
		t.Fatalf("setup forked the hired identity: %#v", result.State)
	}
	if fixture.creator.seen.AssistantID != fixture.assistantID ||
		fixture.creator.seen.RequestID != "hq-request-1" ||
		fixture.creator.seen.DisplayName != "Atlas" {
		t.Fatalf("creation options = %#v", fixture.creator.seen)
	}
	// One of each canonical consequence.
	if fixture.creator.calls != 1 || fixture.hq.designateCalls != 1 ||
		fixture.briefs.updateCalls != 1 || fixture.hq.onboardingCalls != 1 {
		t.Fatalf("creator=%d designate=%d brief=%d onboarding=%d",
			fixture.creator.calls, fixture.hq.designateCalls,
			fixture.briefs.updateCalls, fixture.hq.onboardingCalls)
	}
	// The rhythm the user confirmed here, written against the real workspace.
	if result.BriefConfig == nil || result.BriefConfig.WorkspaceID != "hq-1" ||
		result.BriefConfig.Timezone != "America/New_York" ||
		result.BriefConfig.ScheduleTime != "07:30" || !result.BriefConfig.NotifyOnReady ||
		len(result.BriefConfig.ScheduleDays) != 3 {
		t.Fatalf("brief config = %#v", result.BriefConfig)
	}

	persisted, err := store.GetState(ctx, "local")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if err := persisted.ValidateStateInvariants(); err != nil {
		t.Fatalf("activated state violates invariants: %v", err)
	}
	// The receipt survives; the duplicate schedule does not.
	if persisted.LastHQRequestID != "hq-request-1" || persisted.HQPayloadHash == "" {
		t.Fatalf("activation dropped the replay receipt: %#v", persisted)
	}
	if persisted.HQPayloadJSON != "" {
		t.Fatal("activation kept a duplicate of the Daily Brief schedule")
	}
}

func TestHQSetup_ClaimsTheOperationBeforeCreatingAnything(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestStore(t)
	fixture := newHQFixture(t, store)
	// Fail at the creator, so the only writes are the claim.
	fixture.creator.err = errors.New("injected workspace creation failure")

	_, err := fixture.coordinator.Setup(ctx, "local", fixture.request())
	var partial *PartialHQSetupError
	if !errors.As(err, &partial) || partial.Step != RepairHQCreation {
		t.Fatalf("error = %#v; want a bounded hq_creation partial", err)
	}
	persisted, getErr := store.GetState(ctx, "local")
	if getErr != nil {
		t.Fatalf("GetState: %v", getErr)
	}
	// The claim is durable, so a restart resumes this request rather than
	// inviting a second create.
	if persisted.Status != StatusProvisioningHQ || persisted.LastHQRequestID != "hq-request-1" ||
		persisted.HQPayloadJSON == "" {
		t.Fatalf("claim was not persisted before creation: %#v", persisted)
	}
	if persisted.HQWorkspaceID != "" {
		t.Fatal("a failed creation recorded a workspace")
	}
	if fixture.hq.designateCalls != 0 || fixture.briefs.updateCalls != 0 {
		t.Fatal("a failed creation still designated or wrote a brief config")
	}
}

func TestHQSetup_FaultAtEveryBoundaryResumesToOneHQ(t *testing.T) {
	tests := []struct {
		name             string
		inject           func(*hqFixture, *failOnceStateStore)
		clear            func(*hqFixture)
		wantStep         RepairStep
		wantCreators     int
		wantDesignations int
	}{
		{
			name: "workspace creation",
			inject: func(f *hqFixture, _ *failOnceStateStore) {
				f.creator.err = errors.New("injected creation failure")
			},
			clear:    func(f *hqFixture) { f.creator.err = nil },
			wantStep: RepairHQCreation, wantCreators: 2, wantDesignations: 1,
		},
		{
			name: "state checkpoint after creation",
			inject: func(_ *hqFixture, store *failOnceStateStore) {
				store.failWhen = func(state *State) bool { return state.HQWorkspaceID != "" }
			},
			wantStep: RepairHQCreation, wantCreators: 2, wantDesignations: 1,
		},
		{
			// The first designation attempt fails, so the retry attempts it once
			// more. Two attempts, one successful designation.
			name: "designation",
			inject: func(f *hqFixture, _ *failOnceStateStore) {
				f.hq.designateErr = errors.New("injected designation failure")
			},
			clear:    func(f *hqFixture) { f.hq.designateErr = nil },
			wantStep: RepairDesignation, wantCreators: 1, wantDesignations: 2,
		},
		{
			name: "daily brief config",
			inject: func(f *hqFixture, _ *failOnceStateStore) {
				f.briefs.updateErr = errors.New("injected config failure")
			},
			clear:    func(f *hqFixture) { f.briefs.updateErr = nil },
			wantStep: RepairDailyBriefConfig, wantCreators: 1, wantDesignations: 1,
		},
		{
			name: "hq onboarding state",
			inject: func(f *hqFixture, _ *failOnceStateStore) {
				f.hq.onboardingErr = errors.New("injected onboarding failure")
			},
			clear:    func(f *hqFixture) { f.hq.onboardingErr = nil },
			wantStep: RepairFinalization, wantCreators: 1, wantDesignations: 1,
		},
		{
			name: "active finalization",
			inject: func(_ *hqFixture, store *failOnceStateStore) {
				store.failWhen = func(state *State) bool { return state.Status == StatusActive }
			},
			wantStep: RepairFinalization, wantCreators: 1, wantDesignations: 1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			sqliteStore, db := newTestStore(t)
			store := &failOnceStateStore{Store: sqliteStore}
			fixture := newHQFixture(t, store)
			test.inject(fixture, store)

			request := validHQSetupRequest()
			_, firstErr := fixture.coordinator.Setup(ctx, "local", request)
			var partial *PartialHQSetupError
			if !errors.As(firstErr, &partial) || partial.Step != test.wantStep {
				t.Fatalf("first error = %#v; want partial step %q", firstErr, test.wantStep)
			}
			// A resumable partial must not tell the user their setup is broken.
			if partial.State != nil && partial.State.Status == StatusRepairNeeded {
				t.Fatal("a resumable partial was recorded as repair_needed")
			}
			// Bounded: a closed step code, never provider text.
			if strings.ContainsAny(string(partial.Step), " :/") {
				t.Fatalf("repair step is not a closed code: %q", partial.Step)
			}

			if test.clear != nil {
				test.clear(fixture)
			}
			// The retry carries no version: the stored claim is authoritative.
			retry := request
			retry.IfVersion = 0
			result, err := fixture.coordinator.Setup(ctx, "local", retry)
			if err != nil {
				t.Fatalf("retry Setup: %v", err)
			}
			if result.State.Status != StatusActive || result.State.HQWorkspaceID != "hq-1" ||
				result.State.RepairStep != RepairNone {
				t.Fatalf("retry state = %#v", result.State)
			}

			// Exactly one of everything.
			var relationships int
			if err := db.QueryRowContext(ctx,
				`SELECT COUNT(*) FROM personal_assistant_state`).Scan(&relationships); err != nil ||
				relationships != 1 {
				t.Fatalf("relationship count = %d, %v", relationships, err)
			}
			if fixture.creator.calls != test.wantCreators {
				t.Fatalf("creator calls = %d; want %d", fixture.creator.calls, test.wantCreators)
			}
			// Designation is attempted again only when its own attempt failed;
			// once it has succeeded, ensureDesignation sees a matching valid HQ
			// and does not re-designate.
			if fixture.hq.designateCalls != test.wantDesignations {
				t.Fatalf("designate calls = %d; want %d",
					fixture.hq.designateCalls, test.wantDesignations)
			}
		})
	}
}

func TestHQSetup_ReplayReturnsTheSameCanonicalResult(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestStore(t)
	fixture := newHQFixture(t, store)
	request := validHQSetupRequest()

	first, err := fixture.coordinator.Setup(ctx, "local", request)
	if err != nil {
		t.Fatalf("first Setup: %v", err)
	}
	replay := request
	replay.IfVersion = 0
	second, err := fixture.coordinator.Setup(ctx, "local", replay)
	if err != nil {
		t.Fatalf("replay Setup: %v", err)
	}
	if second.State.HQWorkspaceID != first.State.HQWorkspaceID ||
		second.State.HQEntryAgentInstanceID != first.State.HQEntryAgentInstanceID ||
		!second.Resumed {
		t.Fatalf("replay = %#v; want the same records marked resumed", second.State)
	}
	if fixture.creator.calls != 1 || fixture.hq.designateCalls != 1 || fixture.briefs.updateCalls != 1 {
		t.Fatalf("replay caused side effects: creator=%d designate=%d brief=%d",
			fixture.creator.calls, fixture.hq.designateCalls, fixture.briefs.updateCalls)
	}
}

func TestHQSetup_RejectsChangedPayloadAndStaleVersion(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestStore(t)
	fixture := newHQFixture(t, store)
	// Claim the operation but stop before it completes.
	fixture.creator.err = errors.New("injected creation failure")
	if _, err := fixture.coordinator.Setup(ctx, "local", fixture.request()); err == nil {
		t.Fatal("expected the first attempt to fail")
	}
	fixture.creator.err = nil

	// Same request ID, different rhythm: the user confirmed one thing.
	changed := validHQSetupRequest()
	changed.ScheduleTime = "23:00"
	changed.IfVersion = 0
	if _, err := fixture.coordinator.Setup(ctx, "local", changed); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed payload error = %v; want conflict", err)
	}

	// A second, different confirmed submit while one is in flight.
	competing := validHQSetupRequest()
	competing.RequestID = "hq-request-2"
	if _, err := fixture.coordinator.Setup(ctx, "local", competing); !errors.Is(err, ErrConflict) {
		t.Fatalf("competing request error = %v; want conflict", err)
	}
	if fixture.creator.calls != 1 {
		t.Fatalf("a rejected request reached the creator (%d calls)", fixture.creator.calls)
	}
}

func TestHQSetup_StaleVersionOnAFreshClaimIsRejected(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestStore(t)
	fixture := newHQFixture(t, store)
	request := fixture.request()
	request.IfVersion = fixture.version + 1 // the client read an older state

	if _, err := fixture.coordinator.Setup(ctx, "local", request); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale version error = %v; want conflict", err)
	}
	if fixture.creator.calls != 0 {
		t.Fatal("a stale-version request reached the creator")
	}
}

func TestHQSetup_ResumeWritesTheDurableConfirmedRhythm(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestStore(t)
	fixture := newHQFixture(t, store)
	fixture.briefs.updateErr = errors.New("injected config failure")
	if _, err := fixture.coordinator.Setup(ctx, "local", fixture.request()); err == nil {
		t.Fatal("expected the first config save to fail")
	}

	// The retry resends the same request. The rhythm written is the one read back
	// from the durable claim, so the coordinator never depends on the client
	// still holding a correct copy of what the user confirmed.
	fixture.briefs.updateErr = nil
	retry := fixture.request()
	retry.IfVersion = 0
	result, err := fixture.coordinator.Setup(ctx, "local", retry)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if result.BriefConfig.ScheduleTime != "07:30" || !result.BriefConfig.NotifyOnReady ||
		result.BriefConfig.Timezone != "America/New_York" {
		t.Fatalf("resume wrote the wrong rhythm: %#v", result.BriefConfig)
	}
	if fixture.creator.calls != 1 {
		t.Fatalf("resume created a second workspace (%d creator calls)", fixture.creator.calls)
	}
}

func TestHQSetup_ResumeWithADifferentRhythmIsAConflictNotASilentChange(t *testing.T) {
	// A browser that lost its form state must not be able to quietly replace the
	// rhythm the user actually confirmed. The contract is explicit: a changed
	// payload under the same request ID is a conflict. The client's honest
	// options are to resend what it confirmed, or start a new request.
	ctx := context.Background()
	store, _ := newTestStore(t)
	fixture := newHQFixture(t, store)
	fixture.briefs.updateErr = errors.New("injected config failure")
	if _, err := fixture.coordinator.Setup(ctx, "local", fixture.request()); err == nil {
		t.Fatal("expected the first config save to fail")
	}
	fixture.briefs.updateErr = nil

	// Defaults rather than the confirmed rhythm: a different payload.
	lost := HQSetupRequest{RequestID: "hq-request-1", IfVersion: 0}
	if _, err := fixture.coordinator.Setup(ctx, "local", lost); !errors.Is(err, ErrConflict) {
		t.Fatalf("error = %v; want conflict", err)
	}
	// The confirmed rhythm is still intact and still resumable.
	persisted, err := store.GetState(ctx, "local")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if persisted.Status != StatusProvisioningHQ || persisted.HQPayloadJSON == "" {
		t.Fatalf("a rejected resume damaged the claim: %#v", persisted)
	}
	stored, decodeErr := decodeStoredHQRequest(persisted)
	if decodeErr != nil {
		t.Fatalf("stored payload no longer decodes: %v", decodeErr)
	}
	if stored.ScheduleTime != "07:30" || !stored.NotifyOnReady {
		t.Fatalf("stored rhythm changed: %#v", stored)
	}
}

func TestHQSetup_ConcurrentSubmitsProduceOneHQ(t *testing.T) {
	ctx := context.Background()
	store, db := newTestStore(t)
	fixture := newHQFixture(t, store)
	request := validHQSetupRequest()

	const submits = 8
	var wait sync.WaitGroup
	errs := make([]error, submits)
	for i := range submits {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			_, errs[index] = fixture.coordinator.Setup(ctx, "local", request)
		}(i)
	}
	wait.Wait()

	// Every submit either succeeds or loses the version check; none may create a
	// second workspace.
	for _, err := range errs {
		if err != nil && !errors.Is(err, ErrConflict) {
			t.Fatalf("unexpected concurrent error: %v", err)
		}
	}
	if fixture.creator.calls != 1 {
		t.Fatalf("concurrent submits created %d workspaces", fixture.creator.calls)
	}
	if fixture.hq.designateCalls != 1 || fixture.briefs.updateCalls != 1 {
		t.Fatalf("designate=%d brief=%d", fixture.hq.designateCalls, fixture.briefs.updateCalls)
	}
	var relationships int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM personal_assistant_state`).Scan(&relationships); err != nil ||
		relationships != 1 {
		t.Fatalf("relationship count = %d, %v", relationships, err)
	}
	final, err := store.GetState(ctx, "local")
	if err != nil || final.Status != StatusActive {
		t.Fatalf("final state = %#v, %v", final, err)
	}
}

func TestHQSetup_RefusesWithoutAProvableOwnedProfile(t *testing.T) {
	tests := []struct {
		name     string
		profiles ProfileReader
		mutate   func(*State)
		wantErr  error
	}{
		{
			name:     "profile vanished",
			profiles: &fakeProfileReader{profiles: map[string]ProfileProvenance{}},
			wantErr:  ErrRepairNeeded,
		},
		{
			name: "profile owned by another relationship",
			profiles: &fakeProfileReader{profiles: map[string]ProfileProvenance{
				"Atlas": {Name: "Atlas", AssistantID: "assistant-b"},
			}},
			wantErr: ErrConflict,
		},
		{
			name: "unmarked same-named profile",
			profiles: &fakeProfileReader{profiles: map[string]ProfileProvenance{
				"Atlas": {Name: "Atlas"},
			}},
			wantErr: ErrConflict,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			store, _ := newTestStore(t)
			fixture := newHQFixture(t, store)
			fixture.coordinator = NewHQSetupCoordinator(
				store, fixture.creator, fixture.hq, fixture.briefs, test.profiles)

			_, err := fixture.coordinator.Setup(ctx, "local", fixture.request())
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("error = %v; want %v", err, test.wantErr)
			}
			if fixture.creator.calls != 0 {
				t.Fatal("an unverifiable profile still reached workspace creation")
			}
			persisted, getErr := store.GetState(ctx, "local")
			if getErr != nil {
				t.Fatalf("GetState: %v", getErr)
			}
			if persisted.Status != StatusAwaitingHQ {
				t.Fatalf("a refused request changed the relationship: %#v", persisted)
			}
		})
	}
}

func TestHQSetup_RefusesRelationshipsThatCannotBuildHQ(t *testing.T) {
	for _, status := range []RelationshipStatus{
		StatusNotHired, StatusHiring, StatusRepairNeeded,
	} {
		t.Run(string(status), func(t *testing.T) {
			ctx := context.Background()
			store, _ := newTestStore(t)
			state := awaitingHQTestState("local", "assistant-a")
			state.Status = status
			if status == StatusNotHired {
				state.GlobalAgentProfileName = ""
				state.HiredAt = nil
			}
			if _, err := store.CreateState(ctx, state); err != nil {
				t.Fatalf("seed %s: %v", status, err)
			}
			creator := &fakeAssistantCreator{}
			coordinator := NewHQSetupCoordinator(
				store, creator, &fakeHireHQ{},
				&fakeHireBriefs{getErr: dailybrief.ErrConfigNotFound},
				ownedProfileReader("Atlas", "assistant-a"))

			if _, err := coordinator.Setup(ctx, "local", validHQSetupRequest()); !errors.Is(err, ErrConflict) {
				t.Fatalf("%s error = %v; want conflict", status, err)
			}
			if creator.calls != 0 {
				t.Fatalf("%s reached workspace creation", status)
			}
		})
	}
}

func TestHQSetup_RefusesWhenNoRelationshipExists(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestStore(t)
	creator := &fakeAssistantCreator{}
	coordinator := NewHQSetupCoordinator(
		store, creator, &fakeHireHQ{},
		&fakeHireBriefs{getErr: dailybrief.ErrConfigNotFound},
		ownedProfileReader("Atlas", "assistant-a"))

	if _, err := coordinator.Setup(ctx, "local", validHQSetupRequest()); !errors.Is(err, ErrConflict) {
		t.Fatalf("error = %v; want conflict", err)
	}
	if creator.calls != 0 {
		t.Fatal("an unhired user reached workspace creation")
	}
}

func TestHQSetup_RefusesAForeignAlreadyDesignatedHQ(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestStore(t)
	fixture := newHQFixture(t, store)
	// Another workspace is already this user's Personal HQ.
	fixture.hq.status = &personalhq.Status{UserID: "local", WorkspaceID: "someone-elses", Valid: true}

	_, err := fixture.coordinator.Setup(ctx, "local", fixture.request())
	var partial *PartialHQSetupError
	if !errors.As(err, &partial) || partial.Step != RepairDesignation {
		t.Fatalf("error = %#v; want a designation partial", err)
	}
	if fixture.hq.designateCalls != 0 {
		t.Fatal("setup re-designated over an existing Personal HQ")
	}
}

func TestHQSetup_ValidatesBeforeAnyConsequence(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*HQSetupRequest)
	}{
		{"missing request id", func(r *HQSetupRequest) { r.RequestID = "" }},
		{"negative version", func(r *HQSetupRequest) { r.IfVersion = -1 }},
		{"invalid timezone", func(r *HQSetupRequest) { r.Timezone = "Mars/Olympus" }},
		{"invalid schedule day", func(r *HQSetupRequest) { r.ScheduleDays = []string{"someday"} }},
		{"invalid scope", func(r *HQSetupRequest) { r.Scope = "everything" }},
		{"html hq name", func(r *HQSetupRequest) { r.HQName = "<b>HQ</b>" }},
		{"secret-like hq name", func(r *HQSetupRequest) {
			r.HQName = "sk-abcdefghijklmnopqrstuvwxyz"
		}},
		{"overlong hq name", func(r *HQSetupRequest) {
			r.HQName = strings.Repeat("x", MaxHQNameLen+1)
		}},
		{"too many selected workspaces", func(r *HQSetupRequest) {
			r.Scope = "selected"
			ids := make([]string, MaxHQSelectedWorkspaces+1)
			for i := range ids {
				ids[i] = "ws-" + strings.Repeat("a", 3)
			}
			r.SelectedIDs = ids
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			store, _ := newTestStore(t)
			fixture := newHQFixture(t, store)
			request := validHQSetupRequest()
			test.mutate(&request)

			if _, err := fixture.coordinator.Setup(ctx, "local", request); !errors.Is(err, ErrValidation) {
				t.Fatalf("error = %v; want validation", err)
			}
			if fixture.creator.calls != 0 || fixture.hq.designateCalls != 0 ||
				fixture.briefs.updateCalls != 0 {
				t.Fatal("an invalid request reached a canonical consequence")
			}
			persisted, getErr := store.GetState(ctx, "local")
			if getErr != nil {
				t.Fatalf("GetState: %v", getErr)
			}
			if persisted.Status != StatusAwaitingHQ || persisted.LastHQRequestID != "" {
				t.Fatalf("an invalid request claimed an operation: %#v", persisted)
			}
		})
	}
}

func TestHQSetup_NormalizationIsStableAcrossEquivalentSubmissions(t *testing.T) {
	// Two submissions that mean the same thing must hash the same, or a harmless
	// client difference would read as "the payload changed".
	base := validHQSetupRequest()
	base.ScheduleDays = []string{"mon", "wed", "fri"}
	noisy := validHQSetupRequest()
	noisy.ScheduleDays = []string{"MON", " wed ", "fri", "mon"}
	// A selection is meaningless under all-workspace scope and must be dropped.
	noisy.SelectedIDs = []string{"ws-a", "ws-b"}

	left, err := normalizeHQSetupRequest(base)
	if err != nil {
		t.Fatalf("normalize base: %v", err)
	}
	right, err := normalizeHQSetupRequest(noisy)
	if err != nil {
		t.Fatalf("normalize noisy: %v", err)
	}
	if left.Hash != right.Hash {
		t.Fatalf("equivalent submissions hashed differently:\n%#v\n%#v", left, right)
	}

	// A real difference must change the hash.
	different := validHQSetupRequest()
	different.ScheduleTime = "09:00"
	other, err := normalizeHQSetupRequest(different)
	if err != nil {
		t.Fatalf("normalize different: %v", err)
	}
	if other.Hash == left.Hash {
		t.Fatal("a changed rhythm produced the same hash")
	}
}

func TestHQSetup_DefaultsMatchTheFormsVisibleDefaults(t *testing.T) {
	normalized, err := normalizeHQSetupRequest(HQSetupRequest{RequestID: "hq-1"})
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if normalized.HQName != DefaultHQName {
		t.Fatalf("default name = %q", normalized.HQName)
	}
	if normalized.ScheduleTime != "08:00" {
		t.Fatalf("default time = %q", normalized.ScheduleTime)
	}
	if strings.Join(normalized.ScheduleDays, ",") != "mon,tue,wed,thu,fri" {
		t.Fatalf("default days = %v", normalized.ScheduleDays)
	}
	if normalized.NotifyOnReady {
		t.Fatal("notifications must default to off")
	}
	if normalized.Scope != "all" {
		t.Fatalf("default scope = %q", normalized.Scope)
	}
}

func TestHQSetup_RejectsMissingDependencies(t *testing.T) {
	coordinator := NewHQSetupCoordinator(nil, nil, nil, nil, nil)
	if _, err := coordinator.Setup(context.Background(), "local", validHQSetupRequest()); err == nil {
		t.Fatal("Setup succeeded without configured dependencies")
	}
}

func TestHQSetup_EmitsBoundedLifecycleEventsOnly(t *testing.T) {
	ctx := context.Background()
	var events []EventType
	var fields []logger.Fields
	original := emitPersonalAssistantEvent
	emitPersonalAssistantEvent = func(event EventType, f logger.Fields) {
		events = append(events, event)
		fields = append(fields, f)
	}
	t.Cleanup(func() { emitPersonalAssistantEvent = original })

	store, _ := newTestStore(t)
	fixture := newHQFixture(t, store)
	if _, err := fixture.coordinator.Setup(ctx, "local", fixture.request()); err != nil {
		t.Fatalf("Setup: %v", err)
	}

	if len(events) != 2 || events[0] != EventHQSetupStarted || events[1] != EventHQActivated {
		t.Fatalf("events = %v", events)
	}
	// Nothing a user typed may appear: not the HQ name, not the schedule.
	for _, f := range fields {
		for key, value := range f {
			rendered := strings.TrimSpace(fmt.Sprint(value))
			for _, leak := range []string{"Command Post", "07:30", "America/New_York", "Atlas"} {
				if strings.Contains(rendered, leak) {
					t.Fatalf("event field %s=%q leaked %q", key, rendered, leak)
				}
			}
		}
	}
}

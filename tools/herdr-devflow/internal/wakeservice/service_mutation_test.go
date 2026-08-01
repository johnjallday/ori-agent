package wakeservice

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/wakeprotocol"
)

type fakePowerScheduler struct {
	events        []PowerEvent
	calls         []string
	scheduleError error
	cancelError   error
}

func (f *fakePowerScheduler) Events(context.Context) ([]PowerEvent, error) {
	f.calls = append(f.calls, "list")
	return append([]PowerEvent(nil), f.events...), nil
}

func (f *fakePowerScheduler) Schedule(_ context.Context, wakeAt time.Time) error {
	f.calls = append(f.calls, "schedule:"+wakeAt.UTC().Format(time.RFC3339))
	if f.scheduleError != nil {
		return f.scheduleError
	}
	f.events = append(f.events, PowerEvent{Type: "wake", At: wakeAt.UTC(), Owner: PMSetOwner})
	return nil
}

func (f *fakePowerScheduler) Cancel(_ context.Context, wakeAt time.Time) error {
	f.calls = append(f.calls, "cancel:"+wakeAt.UTC().Format(time.RFC3339))
	if f.cancelError != nil {
		return f.cancelError
	}
	kept := f.events[:0]
	for _, event := range f.events {
		if event.Owner == PMSetOwner && event.At.Equal(wakeAt) {
			continue
		}
		kept = append(kept, event)
	}
	f.events = kept
	return nil
}

func TestSingleCandidateSchedulesVerifiesAndCancelsExactly(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	power := &fakePowerScheduler{}
	service := newMutationService(t, now, power)
	candidate := wakeprotocol.Candidate{
		ID:        "continuation-1",
		Source:    wakeprotocol.SourceContinuation,
		Purpose:   wakeprotocol.PurposeContinuation,
		WakeAt:    now.Add(2*time.Hour + 500*time.Millisecond),
		ExpiresAt: now.Add(3 * time.Hour),
		Reason:    "continue saved agent",
	}
	register := service.Handle(context.Background(), os.Getuid(), mutationRequest(
		"register-1", "idem-register-1", wakeprotocol.OperationRegisterOrReplace, &candidate, nil,
	))
	if register.Result != wakeprotocol.ResultSuccess || register.State == nil ||
		register.State.Programmed == nil {
		t.Fatalf("register response = %+v", register)
	}
	expected := candidate.WakeAt.Truncate(time.Second)
	if !register.State.Programmed.WakeAt.Equal(expected) ||
		register.State.Programmed.Owner != PMSetOwner ||
		register.State.Programmed.EventType != PMSetEventType {
		t.Fatalf("programmed = %+v, want exact fixed event", register.State.Programmed)
	}

	target := wakeprotocol.Target{ID: candidate.ID, Source: candidate.Source, Purpose: candidate.Purpose}
	verify := service.Handle(context.Background(), os.Getuid(), mutationRequest(
		"verify-1", "", wakeprotocol.OperationVerify, nil, &target,
	))
	if verify.Result != wakeprotocol.ResultSuccess || verify.Verification == nil ||
		!verify.Verification.Matched || !verify.Verification.ProgrammedWakeAt.Equal(expected) {
		t.Fatalf("verify response = %+v", verify)
	}

	cancel := service.Handle(context.Background(), os.Getuid(), mutationRequest(
		"cancel-1", "idem-cancel-1", wakeprotocol.OperationCancel, nil, &target,
	))
	if cancel.Result != wakeprotocol.ResultSuccess || cancel.State == nil ||
		cancel.State.Programmed != nil || len(cancel.State.Candidates) != 0 {
		t.Fatalf("cancel response = %+v", cancel)
	}
	wantCalls := []string{
		"list", "list", "schedule:" + expected.Format(time.RFC3339), "list",
		"list",
		"list", "list", "cancel:" + expected.Format(time.RFC3339), "list",
	}
	if !reflect.DeepEqual(power.calls, wantCalls) {
		t.Fatalf("power calls = %#v, want %#v", power.calls, wantCalls)
	}
}

func TestSameTimeForeignEventIsPreservedByExactCancellation(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	wakeAt := now.Add(2 * time.Hour)
	foreign := PowerEvent{Type: "wake", At: wakeAt, Owner: "com.example.backup"}
	power := &fakePowerScheduler{events: []PowerEvent{foreign}}
	service := newMutationService(t, now, power)
	candidate := wakeprotocol.Candidate{
		ID:        "continuation-foreign",
		Source:    wakeprotocol.SourceContinuation,
		Purpose:   wakeprotocol.PurposeContinuation,
		WakeAt:    wakeAt,
		ExpiresAt: wakeAt.Add(time.Hour),
	}
	register := service.Handle(context.Background(), os.Getuid(), mutationRequest(
		"register-foreign", "idem-register-foreign", wakeprotocol.OperationRegisterOrReplace, &candidate, nil,
	))
	if register.Result != wakeprotocol.ResultSuccess {
		t.Fatalf("register response = %+v", register)
	}
	target := wakeprotocol.Target{ID: candidate.ID, Source: candidate.Source, Purpose: candidate.Purpose}
	cancel := service.Handle(context.Background(), os.Getuid(), mutationRequest(
		"cancel-foreign", "idem-cancel-foreign", wakeprotocol.OperationCancel, nil, &target,
	))
	if cancel.Result != wakeprotocol.ResultSuccess {
		t.Fatalf("cancel response = %+v", cancel)
	}
	if len(power.events) != 1 || power.events[0] != foreign {
		t.Fatalf("events after exact cancellation = %+v, want foreign event %+v", power.events, foreign)
	}
}

func TestUntrackedOrConflictingOwnedEventsRefuseWithoutMutation(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	candidate := wakeprotocol.Candidate{
		ID:        "continuation-conflict",
		Source:    wakeprotocol.SourceContinuation,
		Purpose:   wakeprotocol.PurposeContinuation,
		WakeAt:    now.Add(2 * time.Hour),
		ExpiresAt: now.Add(3 * time.Hour),
	}
	tests := []struct {
		name   string
		events []PowerEvent
	}{
		{
			name: "one untracked event",
			events: []PowerEvent{{
				Type: "wake", At: candidate.WakeAt, Owner: PMSetOwner,
			}},
		},
		{
			name: "multiple owned events",
			events: []PowerEvent{
				{Type: "wake", At: candidate.WakeAt, Owner: PMSetOwner},
				{Type: "wake", At: candidate.WakeAt.Add(time.Minute), Owner: PMSetOwner},
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			power := &fakePowerScheduler{events: append([]PowerEvent(nil), test.events...)}
			service := newMutationService(t, now, power)
			response := service.Handle(context.Background(), os.Getuid(), mutationRequest(
				"register-conflict", "idem-register-conflict", wakeprotocol.OperationRegisterOrReplace, &candidate, nil,
			))
			if response.Result != wakeprotocol.ResultRefusal || response.Code != wakeprotocol.CodeConflict {
				t.Fatalf("response = %+v", response)
			}
			if !reflect.DeepEqual(power.events, test.events) {
				t.Fatalf("host events changed: got %+v want %+v", power.events, test.events)
			}
			for _, call := range power.calls {
				if call != "list" {
					t.Fatalf("mutation issued after conflict: %s", call)
				}
			}
		})
	}
}

func TestMutationIdempotencyReplaysAndRejectsConflictingReuse(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	power := &fakePowerScheduler{}
	service := newMutationService(t, now, power)
	candidate := wakeprotocol.Candidate{
		ID: "idem-candidate", Source: wakeprotocol.SourceContinuation,
		Purpose: wakeprotocol.PurposeContinuation, WakeAt: now.Add(time.Hour),
		ExpiresAt: now.Add(2 * time.Hour),
	}
	request := mutationRequest(
		"first", "same-key", wakeprotocol.OperationRegisterOrReplace, &candidate, nil,
	)
	first := service.Handle(context.Background(), os.Getuid(), request)
	request.RequestID = "retry"
	retry := service.Handle(context.Background(), os.Getuid(), request)
	if first.Result != wakeprotocol.ResultSuccess || retry.Result != wakeprotocol.ResultSuccess {
		t.Fatalf("first = %+v retry = %+v", first, retry)
	}
	scheduleCalls := 0
	for _, call := range power.calls {
		if len(call) >= len("schedule:") && call[:len("schedule:")] == "schedule:" {
			scheduleCalls++
		}
	}
	if scheduleCalls != 1 {
		t.Fatalf("schedule calls = %d, want 1: %v", scheduleCalls, power.calls)
	}
	changed := candidate
	changed.WakeAt = changed.WakeAt.Add(time.Minute)
	changed.ExpiresAt = changed.ExpiresAt.Add(time.Minute)
	conflict := service.Handle(context.Background(), os.Getuid(), mutationRequest(
		"conflict", "same-key", wakeprotocol.OperationRegisterOrReplace, &changed, nil,
	))
	if conflict.Result != wakeprotocol.ResultRefusal || conflict.Code != wakeprotocol.CodeConflict {
		t.Fatalf("conflict response = %+v", conflict)
	}
}

func TestCandidateArbitrationReplacesEarlierAndRecomputesAfterCancelAndExpiry(t *testing.T) {
	t.Parallel()
	current := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	power := &fakePowerScheduler{}
	service, err := New(Config{
		BuildVersion: "test", StateDir: filepath.Join(t.TempDir(), "state"), AllowedUID: os.Getuid(),
		Now: func() time.Time { return current }, Power: power, RequireRoot: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	later := wakeprotocol.Candidate{ID: "later", Source: wakeprotocol.SourceContinuation, Purpose: wakeprotocol.PurposeContinuation, WakeAt: current.Add(3 * time.Hour), ExpiresAt: current.Add(4 * time.Hour)}
	earlier := wakeprotocol.Candidate{ID: "earlier", Source: wakeprotocol.SourceOvernight, Purpose: wakeprotocol.PurposeClaudeReset, WakeAt: current.Add(2 * time.Hour), ExpiresAt: current.Add(2*time.Hour + 30*time.Minute)}
	for _, candidate := range []wakeprotocol.Candidate{later, earlier} {
		response := service.Handle(context.Background(), os.Getuid(), mutationRequest(
			"register-"+candidate.ID, "idem-"+candidate.ID, wakeprotocol.OperationRegisterOrReplace, &candidate, nil,
		))
		if response.Result != wakeprotocol.ResultSuccess || response.State == nil || response.State.Programmed == nil {
			t.Fatalf("register %s response=%+v", candidate.ID, response)
		}
	}
	if got := power.events; len(got) != 1 || !got[0].At.Equal(earlier.WakeAt) {
		t.Fatalf("earlier candidate was not selected: %#v", got)
	}
	earlierTarget := wakeprotocol.Target{ID: earlier.ID, Source: earlier.Source, Purpose: earlier.Purpose}
	response := service.Handle(context.Background(), os.Getuid(), mutationRequest(
		"cancel-earlier", "idem-cancel-earlier", wakeprotocol.OperationCancel, nil, &earlierTarget,
	))
	if response.Result != wakeprotocol.ResultSuccess || response.State == nil || response.State.Programmed == nil || !response.State.Programmed.WakeAt.Equal(later.WakeAt) {
		t.Fatalf("cancel did not restore later candidate: %+v", response)
	}
	earlier = wakeprotocol.Candidate{ID: "expires-first", Source: wakeprotocol.SourceContinuation, Purpose: wakeprotocol.PurposeContinuation, WakeAt: current.Add(time.Hour), ExpiresAt: current.Add(90 * time.Minute)}
	response = service.Handle(context.Background(), os.Getuid(), mutationRequest(
		"register-expiring", "idem-expiring", wakeprotocol.OperationRegisterOrReplace, &earlier, nil,
	))
	if response.Result != wakeprotocol.ResultSuccess {
		t.Fatalf("register expiring candidate=%+v", response)
	}
	current = current.Add(2 * time.Hour)
	laterTarget := wakeprotocol.Target{ID: later.ID, Source: later.Source, Purpose: later.Purpose}
	response = service.Handle(context.Background(), os.Getuid(), mutationRequest(
		"verify-later-after-expiry", "", wakeprotocol.OperationVerify, nil, &laterTarget,
	))
	if response.Result != wakeprotocol.ResultSuccess || response.State == nil || response.State.Programmed == nil || !response.State.Programmed.WakeAt.Equal(later.WakeAt) {
		t.Fatalf("expiry did not recompute the later winner: %+v", response)
	}
	for _, call := range power.calls {
		if strings.Contains(call, "cancelall") {
			t.Fatalf("unsafe broad cancellation was attempted: %s", call)
		}
	}
}

func TestScheduleFailureReturnsUncertainAndRetainsRecoveryIntent(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	power := &fakePowerScheduler{scheduleError: fmt.Errorf("lost response")}
	service := newMutationService(t, now, power)
	candidate := wakeprotocol.Candidate{
		ID: "uncertain", Source: wakeprotocol.SourceContinuation,
		Purpose: wakeprotocol.PurposeContinuation, WakeAt: now.Add(time.Hour),
		ExpiresAt: now.Add(2 * time.Hour),
	}
	response := service.Handle(context.Background(), os.Getuid(), mutationRequest(
		"uncertain", "idem-uncertain", wakeprotocol.OperationRegisterOrReplace, &candidate, nil,
	))
	if response.Result != wakeprotocol.ResultUncertain || response.Code != wakeprotocol.CodeUncertain {
		t.Fatalf("response = %+v", response)
	}
	state, err := service.store.load()
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Candidates) != 1 || state.Intent == nil || state.Intent.Desired == nil {
		t.Fatalf("recovery state = %+v", state)
	}
}

func TestRestartReconcilesPersistedCandidateAndAuditExcludesReason(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	stateDir := filepath.Join(t.TempDir(), "state")
	power := &fakePowerScheduler{}
	config := Config{
		BuildVersion: "test", StateDir: stateDir, AllowedUID: os.Getuid(),
		Now: func() time.Time { return now }, Power: power, RequireRoot: false,
	}
	first, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	candidate := wakeprotocol.Candidate{
		ID: "restart-candidate", Source: wakeprotocol.SourceContinuation,
		Purpose: wakeprotocol.PurposeContinuation, WakeAt: now.Add(time.Hour),
		ExpiresAt: now.Add(2 * time.Hour), Reason: "secret prompt and repository content must never reach audit",
	}
	response := first.Handle(context.Background(), os.Getuid(), mutationRequest(
		"restart-register", "restart-idempotency", wakeprotocol.OperationRegisterOrReplace, &candidate, nil,
	))
	if response.Result != wakeprotocol.ResultSuccess {
		t.Fatalf("register response = %+v", response)
	}
	power.events = nil // Simulate a daemon crash after the host event was lost.
	restarted, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.reconcileStartup(context.Background()); err != nil {
		t.Fatalf("restart reconciliation failed: %v", err)
	}
	if len(power.events) != 1 || power.events[0].Owner != PMSetOwner || !power.events[0].At.Equal(candidate.WakeAt) {
		t.Fatalf("restart did not recreate exact owned event: %#v", power.events)
	}
	payload, err := os.ReadFile(filepath.Join(stateDir, AuditFile))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), candidate.Reason) || !strings.Contains(string(payload), `"uid":`) || !strings.Contains(string(payload), `"candidate_id":"restart-candidate"`) {
		t.Fatalf("bounded wake audit is missing identity or leaked a reason: %s", payload)
	}
}

func newMutationService(
	t *testing.T,
	now time.Time,
	power PowerScheduler,
) *Service {
	t.Helper()
	service, err := New(Config{
		BuildVersion: "test",
		StateDir:     filepath.Join(t.TempDir(), "state"),
		AllowedUID:   os.Getuid(),
		Now:          func() time.Time { return now },
		Power:        power,
		RequireRoot:  false,
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func mutationRequest(
	requestID string,
	idempotencyKey string,
	operation wakeprotocol.Operation,
	candidate *wakeprotocol.Candidate,
	target *wakeprotocol.Target,
) wakeprotocol.Request {
	return wakeprotocol.Request{
		ProtocolVersion: wakeprotocol.Version,
		RequestID:       requestID,
		HelperBuild:     "test",
		Operation:       operation,
		IdempotencyKey:  idempotencyKey,
		Candidate:       candidate,
		Target:          target,
	}
}

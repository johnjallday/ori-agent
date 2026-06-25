package templateonboarding

import (
	"errors"
	"testing"
	"time"
)

func TestNewSessionSnapshotsSpec(t *testing.T) {
	spec := testSpec()
	session, err := NewSession("ws-1", spec, StatusCollecting)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	spec.Fields[0].Label = "Mutated"
	spec.Completion.Inputs["bpm"] = "200"

	if got := session.Spec.Fields[0].Label; got != "BPM" {
		t.Fatalf("session spec label mutated with source spec: %q", got)
	}
	if got := session.Spec.Completion.Inputs["bpm"]; got != "${fields.bpm}" {
		t.Fatalf("session completion input mutated with source spec: %q", got)
	}
}

func TestSessionHappyPathTransitions(t *testing.T) {
	session := mustSession(t, StatusPendingEntryAgent)

	if changed, err := session.AttachEntryAgent(); err != nil || !changed {
		t.Fatalf("AttachEntryAgent changed=%v err=%v", changed, err)
	}
	if session.Status != StatusCollecting {
		t.Fatalf("status=%q, want collecting", session.Status)
	}
	if changed, err := session.MarkReadyToComplete(); err != nil || !changed {
		t.Fatalf("MarkReadyToComplete changed=%v err=%v", changed, err)
	}
	if changed, err := session.StartCompletion(); err != nil || !changed {
		t.Fatalf("StartCompletion changed=%v err=%v", changed, err)
	}
	if changed, err := session.StartCompletion(); err != nil || changed {
		t.Fatalf("duplicate StartCompletion changed=%v err=%v, want no-op", changed, err)
	}
	if changed, err := session.MarkSucceeded(&ActionResult{RunID: "run-1", Result: "created"}); err != nil || !changed {
		t.Fatalf("MarkSucceeded changed=%v err=%v", changed, err)
	}
	if session.Status != StatusSucceeded || session.ActionResult.RunID != "run-1" {
		t.Fatalf("success state not persisted: %+v", session)
	}
	if _, err := session.Transition(StatusCollecting); !errors.Is(err, ErrTerminalSession) {
		t.Fatalf("transition out of success err=%v, want ErrTerminalSession", err)
	}
}

func TestSessionFailureRetryPath(t *testing.T) {
	session := mustSession(t, StatusCollecting)
	if _, err := session.MarkReadyToComplete(); err != nil {
		t.Fatalf("MarkReadyToComplete: %v", err)
	}
	if _, err := session.StartCompletion(); err != nil {
		t.Fatalf("StartCompletion: %v", err)
	}
	if changed, err := session.MarkFailed("tool failed"); err != nil || !changed {
		t.Fatalf("MarkFailed changed=%v err=%v", changed, err)
	}
	if session.Status != StatusFailed || session.ActionError != "tool failed" {
		t.Fatalf("failure state not persisted: %+v", session)
	}
	if changed, err := session.Retry(); err != nil || !changed {
		t.Fatalf("Retry changed=%v err=%v", changed, err)
	}
	if session.Status != StatusRunning || session.ActionError != "" {
		t.Fatalf("retry did not reset running state: %+v", session)
	}
}

func TestSessionInvalidCompletionStates(t *testing.T) {
	session := mustSession(t, StatusPendingEntryAgent)
	if _, err := session.StartCompletion(); !errors.Is(err, ErrCompletionNotReady) {
		t.Fatalf("StartCompletion from pending err=%v, want ErrCompletionNotReady", err)
	}
	if _, err := session.Retry(); !errors.Is(err, ErrCompletionNotReady) {
		t.Fatalf("Retry from pending err=%v, want ErrCompletionNotReady", err)
	}
	if _, err := session.MarkFailed("nope"); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("MarkFailed from pending err=%v, want ErrInvalidTransition", err)
	}
}

func TestSessionCancelAndRunningGuards(t *testing.T) {
	session := mustSession(t, StatusCollecting)
	if changed, err := session.Cancel(); err != nil || !changed {
		t.Fatalf("Cancel changed=%v err=%v", changed, err)
	}
	if session.Status != StatusCancelled {
		t.Fatalf("status=%q, want cancelled", session.Status)
	}
	if changed, err := session.Cancel(); err != nil || changed {
		t.Fatalf("duplicate Cancel changed=%v err=%v, want no-op", changed, err)
	}
	if _, err := session.MergeValues(map[string]any{"bpm": 120}); !errors.Is(err, ErrTerminalSession) {
		t.Fatalf("MergeValues after cancel err=%v, want ErrTerminalSession", err)
	}

	running := mustSession(t, StatusCollecting)
	if _, err := running.MarkReadyToComplete(); err != nil {
		t.Fatal(err)
	}
	if _, err := running.StartCompletion(); err != nil {
		t.Fatal(err)
	}
	if _, err := running.Cancel(); !errors.Is(err, ErrSessionRunning) {
		t.Fatalf("Cancel while running err=%v, want ErrSessionRunning", err)
	}
	if _, err := running.MergeValues(map[string]any{"bpm": 120}); !errors.Is(err, ErrSessionRunning) {
		t.Fatalf("MergeValues while running err=%v, want ErrSessionRunning", err)
	}
}

func TestSessionBlockedTransitions(t *testing.T) {
	session := mustSession(t, StatusCollecting)
	if changed, err := session.Block("missing REAPER skill"); err != nil || !changed {
		t.Fatalf("Block changed=%v err=%v", changed, err)
	}
	if session.Status != StatusBlocked || len(session.Blockers) != 1 {
		t.Fatalf("blocked state not persisted: %+v", session)
	}
	if changed, err := session.Transition(StatusReadyToComplete); err != nil || !changed {
		t.Fatalf("unblock to ready changed=%v err=%v", changed, err)
	}
	if len(session.Blockers) != 0 || session.ActionError != "" {
		t.Fatalf("unblock did not clear blockers/error: %+v", session)
	}
}

func TestSessionMergeValues(t *testing.T) {
	session := mustSession(t, StatusCollecting)
	before := session.UpdatedAt
	if changed, err := session.MergeValues(map[string]any{"bpm": 120, "name": "Song"}); err != nil || !changed {
		t.Fatalf("MergeValues changed=%v err=%v", changed, err)
	}
	if session.Values["bpm"].(float64) != 120 || session.Values["name"] != "Song" {
		t.Fatalf("values not merged/cloned as JSON: %#v", session.Values)
	}
	if !session.UpdatedAt.After(before) {
		t.Fatalf("UpdatedAt did not advance: before=%v after=%v", before, session.UpdatedAt)
	}
	if changed, err := session.MergeValues(map[string]any{"bpm": 120.0, "name": "Song"}); err != nil || changed {
		t.Fatalf("same MergeValues changed=%v err=%v, want no-op", changed, err)
	}
}

func mustSession(t *testing.T, status Status) *Session {
	t.Helper()
	restore := freezeTime(t)
	defer restore()
	session, err := NewSession("ws-1", testSpec(), status)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	restore()
	return session
}

func testSpec() *OnboardingSpec {
	return &OnboardingSpec{
		Version: DefaultVersion,
		Fields: []Field{
			{ID: "bpm", Label: "BPM", Type: FieldNumber, Default: float64(120)},
			{ID: "song_name", Label: "Song name", Type: FieldString, Required: true},
		},
		Completion: CompletionAction{
			Type:                ActionTask,
			Instructions:        "Create the project",
			Inputs:              map[string]string{"bpm": "${fields.bpm}", "name": "${fields.song_name}"},
			InstantiateSkeleton: true,
		},
	}
}

func freezeTime(t *testing.T) func() {
	t.Helper()
	original := nowFunc
	current := time.Date(2026, 6, 25, 10, 0, 0, 0, time.UTC)
	nowFunc = func() time.Time {
		current = current.Add(time.Second)
		return current
	}
	return func() { nowFunc = original }
}

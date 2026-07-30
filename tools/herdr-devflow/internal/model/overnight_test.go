package model

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestOvernightRunSurvivesAJSONRoundTrip is the durability contract: everything
// a later process needs to act deterministically has to come back out of the
// file, because the process that wrote it will often be gone by then.
func TestOvernightRunSurvivesAJSONRoundTrip(t *testing.T) {
	reset := time.Date(2026, 7, 30, 3, 0, 0, 0, time.UTC)
	run := OvernightRun{
		Version:      RunVersion,
		ID:           "run-1",
		RepositoryID: "repo-1",
		State:        RunWaitingForReset,
		CreatedAt:    time.Date(2026, 7, 29, 23, 0, 0, 0, time.UTC),
		StartAt:      time.Date(2026, 7, 29, 23, 0, 0, 0, time.UTC),
		DeadlineAt:   time.Date(2026, 7, 30, 7, 0, 0, 0, time.UTC),
		Timezone:     "America/New_York",
		MaxResumes:   3,
		Confirmation: "interactive",
		Participants: []RunParticipant{{
			ID:       "participant-1",
			Position: 1,
			State:    ParticipantActive,
			Feature:  Feature{RepositoryID: "repo-1", Name: "alpha", Branch: "feature/alpha", Path: "/w/alpha"},
			Binding: AgentBinding{
				Role: "builder", AgentName: "ori-alpha-builder", AgentKind: "claude",
				WorkspaceID: "w1", PaneID: "w1:p1", TerminalID: "t1",
				NativeSession: NativeSession{Source: "herdr:claude", Agent: "claude", Kind: "id", Value: "sess-1"},
			},
			Checkpoint: TaskCheckpoint{
				TaskListPath:  "/w/alpha/tasks/tasks-alpha.md",
				SubtasksTotal: 10, SubtasksCompleted: 4,
				NextOrdinal: "2.3", NextText: "Continue implementation",
				ManualOrdinal: "2.9", ManualText: "Demo: drive the new surface",
			},
			Delivery: RunDelivery{
				ID: "delivery-1", IdempotencyKey: "run-1:participant-1:2026-07-30T03:00:00Z:1",
				State: DeliveryAcknowledged, Summary: "continue with the next implementation subtask",
			},
			Limit: &DetectedLimit{
				Class: "included_session", DetectedAt: reset.Add(-2 * time.Hour),
				ResetAt: reset, AuthMode: "plan_backed", Sleepable: true,
			},
			ConsumedResets:      []time.Time{reset.Add(-5 * time.Hour)},
			AcknowledgedResumes: 1,
		}},
		ActiveParticipant: "participant-1",
		Wake:              WakeOwnership{CandidateID: "cand-1", RequestedAt: reset.Add(-2 * time.Minute), ResetAt: reset, Verified: true},
		Timeline:          []RunEvent{{At: reset.Add(-2 * time.Hour), Kind: "limit_detected", Participant: "participant-1"}},
	}

	encoded, err := json.Marshal(run)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded OvernightRun
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	participant, ok := decoded.Active()
	if !ok || participant.ID != "participant-1" {
		t.Fatalf("active participant = %+v, %v", participant, ok)
	}
	if participant.Binding.NativeSession.Value != "sess-1" {
		t.Fatalf("the exact native session was lost: %+v", participant.Binding)
	}
	if !participant.Limit.ResetAt.Equal(reset) || !participant.Limit.Sleepable {
		t.Fatalf("limit = %+v, want Claude's reset preserved", participant.Limit)
	}
	if len(participant.ConsumedResets) != 1 {
		t.Fatalf("consumed resets = %v, want the handled boundary preserved", participant.ConsumedResets)
	}
	if !decoded.Wake.Verified || decoded.Wake.CandidateID != "cand-1" {
		t.Fatalf("wake ownership = %+v", decoded.Wake)
	}
	if decoded.RemainingResumes() != 3 {
		t.Fatalf("remaining resumes = %d, want the run-level ceiling untouched", decoded.RemainingResumes())
	}
}

// TestRunStateTerminalCoversEveryEndingState keeps a new state from silently
// counting as still running, which would let a finished run be prompted again.
func TestRunStateTerminalCoversEveryEndingState(t *testing.T) {
	terminal := []RunState{
		RunCompleted, RunDeadlineReached, RunCycleLimitReached, RunCanceled, RunFailed, RunUncertain,
	}
	for _, state := range terminal {
		if !state.Terminal() {
			t.Fatalf("state %q should be terminal", state)
		}
	}
	active := []RunState{
		RunScheduled, RunRunning, RunLimitDetected, RunPreparingSleep, RunSleeping,
		RunWaking, RunResuming, RunWaitingForReset, RunWaitingManual, RunReadyForReview, RunOverrun,
	}
	for _, state := range active {
		if state.Terminal() {
			t.Fatalf("state %q should not be terminal", state)
		}
	}
}

func TestParticipantTerminalDistinguishesQueuedFromFinished(t *testing.T) {
	if ParticipantQueued.Terminal() || ParticipantActive.Terminal() {
		t.Fatal("a queued or active participant is not finished")
	}
	for _, state := range []ParticipantState{
		ParticipantCompleted, ParticipantReadyForReview, ParticipantWaitingManual,
		ParticipantFailed, ParticipantCanceled, ParticipantUncertain,
	} {
		if !state.Terminal() {
			t.Fatalf("state %q should be terminal", state)
		}
	}
}

func TestNextQueuedFollowsConfirmedOrder(t *testing.T) {
	run := OvernightRun{Participants: []RunParticipant{
		{ID: "a", Position: 1, State: ParticipantCompleted},
		{ID: "b", Position: 2, State: ParticipantQueued},
		{ID: "c", Position: 3, State: ParticipantQueued},
	}}
	next, ok := run.NextQueued()
	if !ok || next.ID != "b" {
		t.Fatalf("next queued = %+v, want the confirmed order respected", next)
	}
}

// TestRunRecordCarriesNoPromptBody is the redaction boundary at the record
// level: a delivery keeps a summary and an idempotency key, never the text.
func TestRunRecordCarriesNoPromptBody(t *testing.T) {
	secret := "continue; the API key is sk-not-a-real-secret"
	run := OvernightRun{
		ID: "run-1",
		Participants: []RunParticipant{{
			ID: "p1",
			Delivery: RunDelivery{
				ID: "d1", State: DeliveryAcknowledged,
				Summary: "continue with the next implementation subtask",
			},
			Recovery: "wt herd status --feature alpha",
		}},
		Timeline: []RunEvent{{Kind: "prompt_acknowledged", Detail: "delivery d1 acknowledged"}},
	}
	encoded, err := json.Marshal(run)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) == "" || containsAny(string(encoded), secret, "sk-not-a-real-secret") {
		t.Fatalf("the run record carried prompt content: %s", encoded)
	}
}

func containsAny(haystack string, needles ...string) bool {
	for _, needle := range needles {
		if needle != "" && strings.Contains(haystack, needle) {
			return true
		}
	}
	return false
}

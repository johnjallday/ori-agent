package overnight

import (
	"context"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/claudeusage"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/herdr"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/model"
)

// This file is the whole night, one fake cycle at a time: work, limit, sleep,
// wake, continue — three times — and then the fourth limit that must not be
// slept for. Nothing here sleeps a Mac or programs a wake.

// cycle drives one complete limit → sleep → wake → continue sequence and
// returns the run afterwards.
func (h *harness) cycle(t *testing.T, wake *fakeWake, power *fakePower, reset time.Time) model.OvernightRun {
	t.Helper()
	sleptBefore := power.slept

	// Claude reports the limit; the supervisor freezes the queue.
	h.usage.signal = claudeusage.Signal{
		Class: claudeusage.LimitIncludedSession, AuthMode: claudeusage.AuthPlanBacked,
		ResetAt: reset, Sleepable: true,
	}
	run := h.tick(t)
	if run.State != model.RunLimitDetected {
		t.Fatalf("state = %q, want limit_detected", run.State)
	}

	run, err := h.supervisor.PrepareAndSleep(context.Background(), h.run.ID, sleepConfig())
	if err != nil {
		t.Fatalf("PrepareAndSleep: %v", err)
	}
	if run.State != model.RunSleeping {
		t.Fatalf("state = %q, want sleeping (%v)", run.State, run.Timeline[len(run.Timeline)-1].Detail)
	}
	if power.slept != sleptBefore+1 {
		t.Fatalf("slept = %d, want exactly one more sleep", power.slept)
	}
	h.run = run

	// The Mac wakes at the reset.
	h.clock = reset.Add(time.Minute)
	run, err = h.supervisor.Resume(context.Background(), h.run.ID)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if run.State != model.RunResuming {
		t.Fatalf("state = %q, want resuming", run.State)
	}
	h.run = run

	// The allowance is back, so the same exact session is continued once.
	h.usage.signal = claudeusage.Signal{Class: claudeusage.LimitNone}
	h.agents.agents = []herdr.AgentInfo{liveAgent(uint64(10+power.slept), model.AgentIdle)}
	h.prompter.acknowledgement = liveAgent(uint64(11+power.slept), model.AgentWorking)
	return h.tick(t)
}

// TestThreeResumesThenTheCeilingStops is the PRD's worked example: an initial
// window, three acknowledged resumes, and a fourth limit that ends the run
// instead of sleeping again.
func TestThreeResumesThenTheCeilingStops(t *testing.T) {
	h, wake, power, _ := limitedHarness(t)
	// Undo the limit the harness pre-loaded so the run starts clean.
	h.usage.signal = claudeusage.Signal{Class: claudeusage.LimitNone}
	saved, err := h.supervisor.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	run := saved.Runs[h.run.ID]
	run.State = model.RunRunning
	run.Participants[0].Limit = nil
	saved.Runs[h.run.ID] = run
	if err := h.supervisor.Store.Save(saved); err != nil {
		t.Fatal(err)
	}
	h.run = run

	// Three full cycles, each against a strictly newer Claude-reported reset.
	for cycle := 1; cycle <= 3; cycle++ {
		reset := h.clock.Add(90 * time.Minute)
		after := h.cycle(t, wake, power, reset)
		if after.AcknowledgedResumes != cycle {
			t.Fatalf("after cycle %d the run reports %d resumes, want %d",
				cycle, after.AcknowledgedResumes, cycle)
		}
		if after.Participants[0].AcknowledgedResumes != cycle {
			t.Fatalf("participant resumes = %d, want %d", after.Participants[0].AcknowledgedResumes, cycle)
		}
		if len(after.Participants[0].ConsumedResets) != cycle {
			t.Fatalf("consumed resets = %v, want %d recorded boundaries",
				after.Participants[0].ConsumedResets, cycle)
		}
		// The limited agent kept the queue head the whole way through, and the
		// second participant was never touched.
		if after.ActiveParticipant != after.Participants[0].ID {
			t.Fatalf("active = %q, want the same participant across the cycle", after.ActiveParticipant)
		}
		if after.Participants[1].State != model.ParticipantQueued {
			t.Fatalf("the queued participant = %q after cycle %d", after.Participants[1].State, cycle)
		}
		h.run = after
	}
	if power.slept != 3 {
		t.Fatalf("slept = %d, want one sleep per cycle", power.slept)
	}

	// The fourth limit arrives with a perfectly good reset — and is refused,
	// because the configured ceiling is three.
	fourth := h.clock.Add(90 * time.Minute)
	h.usage.signal = claudeusage.Signal{
		Class: claudeusage.LimitIncludedSession, AuthMode: claudeusage.AuthPlanBacked,
		ResetAt: fourth, Sleepable: true,
	}
	h.tick(t)
	final, err := h.supervisor.PrepareAndSleep(context.Background(), h.run.ID, sleepConfig())
	if err != nil {
		t.Fatalf("PrepareAndSleep: %v", err)
	}
	if power.slept != 3 {
		t.Fatalf("slept = %d, want the fourth cycle refused", power.slept)
	}
	if final.State != model.RunCycleLimitReached || final.TerminalReason != model.ReasonCycleLimitReached {
		t.Fatalf("run = %+v, want cycle_limit_reached", final)
	}
	if len(wake.registered) != 3 {
		t.Fatalf("wake requests = %d, want one per accepted cycle", len(wake.registered))
	}
}

// TestSchedulingAWakeDoesNotConsumeAResume is FR93: only an acknowledged
// continuation costs a cycle.
func TestSchedulingAWakeDoesNotConsumeAResume(t *testing.T) {
	h, _, _, reset := limitedHarness(t)
	run, err := h.supervisor.PrepareAndSleep(context.Background(), h.run.ID, sleepConfig())
	if err != nil {
		t.Fatal(err)
	}
	if run.AcknowledgedResumes != 0 {
		t.Fatalf("resumes = %d after scheduling a wake, want 0", run.AcknowledgedResumes)
	}

	// Waking early does not consume one either.
	h.clock = reset.Add(-time.Hour)
	run, err = h.supervisor.Resume(context.Background(), h.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if run.AcknowledgedResumes != 0 {
		t.Fatalf("resumes = %d after an early wake, want 0", run.AcknowledgedResumes)
	}
}

// TestAnUnacknowledgedResumeDoesNotConsumeACycle is the other half of FR93: a
// prompt nobody confirmed cannot spend the budget.
func TestAnUnacknowledgedResumeDoesNotConsumeACycle(t *testing.T) {
	h, _, _, reset := limitedHarness(t)
	if _, err := h.supervisor.PrepareAndSleep(context.Background(), h.run.ID, sleepConfig()); err != nil {
		t.Fatal(err)
	}
	h.clock = reset.Add(time.Minute)
	if _, err := h.supervisor.Resume(context.Background(), h.run.ID); err != nil {
		t.Fatal(err)
	}

	// Herdr acknowledges a different agent, so the delivery is uncertain.
	h.usage.signal = claudeusage.Signal{Class: claudeusage.LimitNone}
	h.agents.agents = []herdr.AgentInfo{liveAgent(9, model.AgentIdle)}
	h.prompter.acknowledgement = liveAgent(10, model.AgentWorking, func(a *herdr.AgentInfo) { a.PaneID = "w-other:p1" })
	run := h.tick(t)

	if run.Participants[0].Delivery.State != model.DeliveryUncertain {
		t.Fatalf("delivery = %+v, want uncertain", run.Participants[0].Delivery)
	}
	if run.AcknowledgedResumes != 0 || run.Participants[0].AcknowledgedResumes != 0 {
		t.Fatalf("resumes = %d/%d, want an uncertain delivery to cost nothing",
			run.AcknowledgedResumes, run.Participants[0].AcknowledgedResumes)
	}
	if len(run.Participants[0].ConsumedResets) != 0 {
		t.Fatalf("consumed resets = %v, want the boundary unrecorded", run.Participants[0].ConsumedResets)
	}
}

// TestAStaleResetNeverStartsAnotherCycle is the loop guard across a restart:
// the same reset arriving again is old news.
func TestAStaleResetNeverStartsAnotherCycle(t *testing.T) {
	h, wake, power, reset := limitedHarness(t)
	h.cycle(t, wake, power, reset)

	// Claude reports the very same reset again, as a restarted adapter might.
	h.usage.signal = claudeusage.Signal{
		Class: claudeusage.LimitIncludedSession, AuthMode: claudeusage.AuthPlanBacked,
		ResetAt: reset, Sleepable: true,
	}
	run := h.tick(t)
	// The fake classifier applies the same strictly-newer rule the real adapter
	// does, so this arrives as an unsleepable limit and stops the participant.
	if run.State == model.RunLimitDetected {
		t.Fatal("a repeated reset re-entered the sleep sequence")
	}
	if power.slept != 1 {
		t.Fatalf("slept = %d, want no second sleep for the same boundary", power.slept)
	}
}

// TestTheCeilingIsConfigurable covers FR92's bounded override.
func TestTheCeilingIsConfigurable(t *testing.T) {
	h, wake, power, _ := limitedHarness(t)
	saved, err := h.supervisor.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	run := saved.Runs[h.run.ID]
	run.MaxResumes = 1
	run.State = model.RunRunning
	run.Participants[0].Limit = nil
	saved.Runs[h.run.ID] = run
	if err := h.supervisor.Store.Save(saved); err != nil {
		t.Fatal(err)
	}
	h.run = run
	h.usage.signal = claudeusage.Signal{Class: claudeusage.LimitNone}

	h.cycle(t, wake, power, h.clock.Add(30*time.Minute))

	// One resume was configured and one was used, so the next limit ends it.
	h.usage.signal = claudeusage.Signal{
		Class: claudeusage.LimitIncludedSession, AuthMode: claudeusage.AuthPlanBacked,
		ResetAt: h.clock.Add(30 * time.Minute), Sleepable: true,
	}
	h.tick(t)
	final, err := h.supervisor.PrepareAndSleep(context.Background(), h.run.ID, sleepConfig())
	if err != nil {
		t.Fatal(err)
	}
	if final.State != model.RunCycleLimitReached {
		t.Fatalf("state = %q, want cycle_limit_reached with a ceiling of one", final.State)
	}
	if power.slept != 1 {
		t.Fatalf("slept = %d, want exactly the one configured cycle", power.slept)
	}
}

// TestATerminalRunWithdrawsItsOwnWake keeps the Mac from waking for a run that
// has finished.
func TestATerminalRunWithdrawsItsOwnWake(t *testing.T) {
	h, wake, _, _ := limitedHarness(t)
	if _, err := h.supervisor.PrepareAndSleep(context.Background(), h.run.ID, sleepConfig()); err != nil {
		t.Fatal(err)
	}

	// The deadline arrives while the machine is asleep.
	h.clock = h.run.DeadlineAt.Add(time.Minute)
	run, err := h.supervisor.Resume(context.Background(), h.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if run.State != model.RunDeadlineReached {
		t.Fatalf("state = %q, want deadline_reached", run.State)
	}
	// Reaching a terminal state withdraws the wake it owned, and only that one.
	if len(wake.canceled) == 0 {
		t.Fatal("a finished run left its wake candidate registered")
	}
	for _, canceled := range wake.canceled {
		if canceled != h.run.ID {
			t.Fatalf("withdrew %q, which is not this run's candidate", canceled)
		}
	}
}

package overnight

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/claudeusage"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/herdr"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/model"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/systempower"
)

// No test in this file sleeps a Mac or changes a real wake schedule. The wake
// coordinator and the power service are both fakes, and the assertions are
// mostly about what was *not* called.

type fakeWake struct {
	registered []time.Time
	canceled   []string
	programmed time.Time
	// verifyErr simulates the owner never programming the wake, which is the
	// failure the whole sequence is built around.
	verifyErr    error
	registerErr  error
	verifyCalled int
}

func (f *fakeWake) Register(_ string, wakeAt time.Time, _ string) error {
	if f.registerErr != nil {
		return f.registerErr
	}
	f.registered = append(f.registered, wakeAt)
	return nil
}

func (f *fakeWake) Verify(_ context.Context, _ string, wakeAt time.Time) (time.Time, error) {
	f.verifyCalled++
	if f.verifyErr != nil {
		return time.Time{}, f.verifyErr
	}
	if f.programmed.IsZero() {
		return wakeAt, nil
	}
	return f.programmed, nil
}

func (f *fakeWake) Cancel(runID string) error {
	f.canceled = append(f.canceled, runID)
	return nil
}

type fakePower struct {
	supported       bool
	source          systempower.Source
	slept           int
	sleepErr        error
	assertionID     string
	assertionActive bool
	assertionErr    error
	releaseErr      error
	released        bool
}

func (f *fakePower) SupportsSleep() bool                            { return f.supported }
func (f *fakePower) PowerSource(context.Context) systempower.Source { return f.source }
func (f *fakePower) Sleep(context.Context) error {
	if f.sleepErr != nil {
		return f.sleepErr
	}
	f.slept++
	return nil
}

func (f *fakePower) AcquireIdleSleepAssertion(_ context.Context, runID string) (string, error) {
	if f.assertionErr != nil {
		return "", f.assertionErr
	}
	if f.assertionID == "" {
		f.assertionID = runID + "-assertion"
	}
	f.assertionActive = true
	return f.assertionID, nil
}
func (f *fakePower) IdleSleepAssertionActive(_ context.Context, id string) bool {
	return f.assertionActive && id == f.assertionID
}
func (f *fakePower) ReleaseIdleSleepAssertion(_ context.Context, id string) error {
	if id != f.assertionID {
		return errors.New("unknown assertion")
	}
	if f.releaseErr != nil {
		return f.releaseErr
	}
	f.assertionActive = false
	f.released = true
	return nil
}

// limitedHarness is a run whose active participant has just hit a verified
// included-session limit.
func limitedHarness(t *testing.T) (*harness, *fakeWake, *fakePower, time.Time) {
	t.Helper()
	h := newHarness(t, "alpha", "beta")
	reset := h.clock.Add(2 * time.Hour)
	h.usage.signal = claudeusage.Signal{
		Class: claudeusage.LimitIncludedSession, AuthMode: claudeusage.AuthPlanBacked,
		ResetAt: reset, Sleepable: true,
	}
	h.tick(t)
	if h.run.State != model.RunLimitDetected {
		t.Fatalf("state = %q, want limit_detected", h.run.State)
	}

	wake := &fakeWake{}
	power := &fakePower{supported: true, source: systempower.SourceAC}
	h.supervisor.Wake = wake
	h.supervisor.Power = power
	return h, wake, power, reset
}

func sleepConfig() SleepConfig {
	return SleepConfig{WakeLead: 2 * time.Minute, ApprovalGranted: true}
}

// TestTheSleepSequenceVerifiesTheWakeBeforeSleeping is the ordering property
// the whole feature rests on.
func TestTheSleepSequenceVerifiesTheWakeBeforeSleeping(t *testing.T) {
	h, wake, power, reset := limitedHarness(t)

	run, err := h.supervisor.PrepareAndSleep(context.Background(), h.run.ID, sleepConfig())
	if err != nil {
		t.Fatalf("PrepareAndSleep: %v", err)
	}
	if run.State != model.RunSleeping {
		t.Fatalf("state = %q, want sleeping", run.State)
	}
	if len(wake.registered) != 1 {
		t.Fatalf("registered = %v, want exactly one wake request", wake.registered)
	}
	// The wake is a lead time before Claude's reset, never after it.
	if !wake.registered[0].Equal(reset.Add(-2 * time.Minute)) {
		t.Fatalf("wake at %v, want the lead time before the reset %v", wake.registered[0], reset)
	}
	if wake.verifyCalled != 1 {
		t.Fatalf("verify calls = %d, want the wake verified once", wake.verifyCalled)
	}
	if power.slept != 1 {
		t.Fatalf("slept = %d, want exactly one sleep", power.slept)
	}
	if !run.Wake.Verified || run.Wake.VerifiedAt.IsZero() {
		t.Fatalf("wake ownership = %+v, want the verification recorded", run.Wake)
	}

	// The verification is durable, so a process that restarts can tell that a
	// wake exists without asking again.
	reloaded, err := h.service.Get(h.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.Wake.Verified || reloaded.State != model.RunSleeping {
		t.Fatalf("reloaded = %+v, want the sleep durable", reloaded)
	}
}

func TestStayAwakeVerifiesRunOwnedAssertionWithoutWakeOrSleep(t *testing.T) {
	h, wake, power, _ := limitedHarness(t)
	updated, err := h.supervisor.updateRun(context.Background(), h.run.ID, func(run *model.OvernightRun, _ time.Time) {
		run.WakeMode = model.WakeModeStayAwake
	})
	if err != nil {
		t.Fatal(err)
	}
	h.run = updated
	run, err := h.supervisor.EnsureStayAwake(context.Background(), h.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if run.State != model.RunWaitingForReset || run.Assertion.ID == "" || run.Assertion.VerifiedAt.IsZero() || power.slept != 0 || len(wake.registered) != 0 {
		t.Fatalf("stay-awake run=%+v slept=%d registered=%v", run, power.slept, wake.registered)
	}
	if _, err := h.supervisor.ReleaseStayAwake(context.Background(), run.ID); err != nil || !power.released {
		t.Fatalf("release stay-awake assertion err=%v released=%v", err, power.released)
	}
}

func TestStayAwakeReleaseFailureRemainsDurablyUncertain(t *testing.T) {
	h, _, power, _ := limitedHarness(t)
	updated, err := h.supervisor.updateRun(context.Background(), h.run.ID, func(run *model.OvernightRun, _ time.Time) {
		run.WakeMode = model.WakeModeStayAwake
	})
	if err != nil {
		t.Fatal(err)
	}
	h.run = updated
	run, err := h.supervisor.EnsureStayAwake(context.Background(), h.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	power.releaseErr = errors.New("assertion process still present")
	released, err := h.supervisor.ReleaseStayAwake(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !released.Assertion.Uncertain || !released.Assertion.ReleasedAt.IsZero() {
		t.Fatalf("release result = %+v, want durable unresolved assertion", released.Assertion)
	}
}

// TestAnUnverifiedWakeNeverSleeps is the failure this design exists to prevent:
// a Mac asleep with no wake programmed.
func TestAnUnverifiedWakeNeverSleeps(t *testing.T) {
	h, wake, power, _ := limitedHarness(t)
	wake.verifyErr = errors.New("not programmed")

	run, err := h.supervisor.PrepareAndSleep(context.Background(), h.run.ID, sleepConfig())
	if err != nil {
		t.Fatalf("PrepareAndSleep: %v", err)
	}
	if power.slept != 0 {
		t.Fatal("the Mac was put to sleep without a verified wake")
	}
	if run.State != model.RunWaitingForReset {
		t.Fatalf("state = %q, want waiting_for_reset", run.State)
	}
	if run.Wake.Verified {
		t.Fatalf("wake = %+v, want it recorded as unverified", run.Wake)
	}
	// The candidate is withdrawn rather than left behind to wake the machine
	// for a run that never slept.
	if len(wake.canceled) != 1 {
		t.Fatalf("canceled = %v, want the unusable candidate withdrawn", wake.canceled)
	}
	if run.Wake.Detail == "" {
		t.Fatal("the refusal was not explained")
	}
}

func TestACoordinatorOutageNeverSleeps(t *testing.T) {
	h, wake, power, _ := limitedHarness(t)
	wake.registerErr = errors.New("coordinator unavailable")

	run, err := h.supervisor.PrepareAndSleep(context.Background(), h.run.ID, sleepConfig())
	if err != nil {
		t.Fatalf("PrepareAndSleep: %v", err)
	}
	if power.slept != 0 {
		t.Fatal("the Mac slept despite an unavailable wake coordinator")
	}
	if run.State != model.RunWaitingForReset {
		t.Fatalf("state = %q, want waiting_for_reset", run.State)
	}
}

// TestEveryGateRefusesIndependently walks the preconditions one at a time.
func TestEveryGateRefusesIndependently(t *testing.T) {
	cases := []struct {
		name     string
		mutate   func(*harness, *fakePower, *SleepConfig)
		contains string
	}{
		{
			name:     "on battery",
			mutate:   func(_ *harness, p *fakePower, _ *SleepConfig) { p.source = systempower.SourceBattery },
			contains: "battery",
		},
		{
			name:     "power source unknown",
			mutate:   func(_ *harness, p *fakePower, _ *SleepConfig) { p.source = systempower.SourceUnknown },
			contains: "unknown",
		},
		{
			name:     "not macOS",
			mutate:   func(_ *harness, p *fakePower, _ *SleepConfig) { p.supported = false },
			contains: "macOS only",
		},
		{
			name:     "no wake approval",
			mutate:   func(_ *harness, _ *fakePower, c *SleepConfig) { c.ApprovalGranted = false },
			contains: "not been authorized",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			h, wake, power, _ := limitedHarness(t)
			config := sleepConfig()
			testCase.mutate(h, power, &config)

			run, err := h.supervisor.PrepareAndSleep(context.Background(), h.run.ID, config)
			if err != nil {
				t.Fatalf("PrepareAndSleep: %v", err)
			}
			if power.slept != 0 {
				t.Fatalf("the Mac slept with %s", testCase.name)
			}
			if len(wake.registered) != 0 {
				t.Fatalf("a wake was requested despite %s", testCase.name)
			}
			if run.State != model.RunWaitingForReset {
				t.Fatalf("state = %q, want waiting_for_reset", run.State)
			}
			if !strings.Contains(strings.ToLower(run.Timeline[len(run.Timeline)-1].Detail), strings.ToLower(testCase.contains)) {
				t.Fatalf("timeline = %q, want it to mention %q",
					run.Timeline[len(run.Timeline)-1].Detail, testCase.contains)
			}
		})
	}
}

// TestAResetAfterTheDeadlineIsNeverSleptFor covers FR97.
func TestAResetAfterTheDeadlineIsNeverSleptFor(t *testing.T) {
	h, wake, power, _ := limitedHarness(t)
	// Push the recorded reset past the morning deadline.
	saved, err := h.supervisor.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	run := saved.Runs[h.run.ID]
	run.Participants[0].Limit.ResetAt = run.DeadlineAt.Add(time.Hour)
	saved.Runs[h.run.ID] = run
	if err := h.supervisor.Store.Save(saved); err != nil {
		t.Fatal(err)
	}

	updated, err := h.supervisor.PrepareAndSleep(context.Background(), h.run.ID, sleepConfig())
	if err != nil {
		t.Fatalf("PrepareAndSleep: %v", err)
	}
	if power.slept != 0 || len(wake.registered) != 0 {
		t.Fatal("a reset after the deadline produced a wake or a sleep")
	}
	// A reset the run can never reach is the end of unattended execution, not
	// something to keep waiting for.
	if updated.State != model.RunDeadlineReached || updated.TerminalReason != model.ReasonDeadlineReached {
		t.Fatalf("run = %+v, want deadline_reached", updated)
	}
}

// TestAnAlreadyHandledResetIsNeverSleptForAgain is the loop guard.
func TestAnAlreadyHandledResetIsNeverSleptForAgain(t *testing.T) {
	h, wake, power, reset := limitedHarness(t)
	saved, err := h.supervisor.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	run := saved.Runs[h.run.ID]
	run.Participants[0].ConsumedResets = []time.Time{reset}
	saved.Runs[h.run.ID] = run
	if err := h.supervisor.Store.Save(saved); err != nil {
		t.Fatal(err)
	}

	updated, err := h.supervisor.PrepareAndSleep(context.Background(), h.run.ID, sleepConfig())
	if err != nil {
		t.Fatalf("PrepareAndSleep: %v", err)
	}
	if power.slept != 0 || len(wake.registered) != 0 {
		t.Fatal("a reset boundary that was already handled produced another sleep")
	}
	if !strings.Contains(updated.Timeline[len(updated.Timeline)-1].Detail, "already handled") {
		t.Fatalf("timeline = %q", updated.Timeline[len(updated.Timeline)-1].Detail)
	}
}

// TestSleepFailureLeavesTheRunAwakeAndUnprompted covers the case where macOS
// itself refuses.
func TestSleepFailureLeavesTheRunAwakeAndUnprompted(t *testing.T) {
	h, _, power, _ := limitedHarness(t)
	power.sleepErr = errors.New("refused")

	run, err := h.supervisor.PrepareAndSleep(context.Background(), h.run.ID, sleepConfig())
	if err != nil {
		t.Fatalf("PrepareAndSleep: %v", err)
	}
	if run.State != model.RunWaitingForReset {
		t.Fatalf("state = %q, want waiting_for_reset", run.State)
	}
	// And the queued participant is still queued, unprompted.
	if run.Participants[1].State != model.ParticipantQueued {
		t.Fatalf("the second participant = %q, want it untouched", run.Participants[1].State)
	}
	if len(h.prompter.prompts) != 0 {
		t.Fatalf("prompts = %v, want none while waiting for a reset", h.prompter.prompts)
	}
}

// TestWakingEarlyWaitsRatherThanPrompting is FR89 and success metric fourteen.
func TestWakingEarlyWaitsRatherThanPrompting(t *testing.T) {
	h, _, _, reset := limitedHarness(t)
	if _, err := h.supervisor.PrepareAndSleep(context.Background(), h.run.ID, sleepConfig()); err != nil {
		t.Fatal(err)
	}

	// The user opens the lid an hour before the reset.
	h.clock = reset.Add(-time.Hour)
	run, err := h.supervisor.Resume(context.Background(), h.run.ID)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if run.State != model.RunWaitingForReset {
		t.Fatalf("state = %q, want waiting_for_reset after an early wake", run.State)
	}

	// And no tick prompts anyone while it waits.
	for range 5 {
		h.clock = h.clock.Add(time.Minute)
		h.tick(t)
	}
	if len(h.prompter.prompts) != 0 {
		t.Fatalf("prompts = %v, want none before the reset", h.prompter.prompts)
	}
}

// TestWakingAfterTheResetResumes is the other half: at the reset, work may
// continue.
func TestWakingAfterTheResetResumes(t *testing.T) {
	h, _, _, reset := limitedHarness(t)
	if _, err := h.supervisor.PrepareAndSleep(context.Background(), h.run.ID, sleepConfig()); err != nil {
		t.Fatal(err)
	}

	h.clock = reset.Add(time.Minute)
	run, err := h.supervisor.Resume(context.Background(), h.run.ID)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if run.State != model.RunResuming {
		t.Fatalf("state = %q, want resuming", run.State)
	}
	if run.ActiveParticipant != run.Participants[0].ID {
		t.Fatalf("active = %q, want the limited participant still at the head", run.ActiveParticipant)
	}
}

// TestWakingAfterTheDeadlineNeverPrompts covers FR101's second half.
func TestWakingAfterTheDeadlineNeverPrompts(t *testing.T) {
	h, _, _, _ := limitedHarness(t)
	if _, err := h.supervisor.PrepareAndSleep(context.Background(), h.run.ID, sleepConfig()); err != nil {
		t.Fatal(err)
	}

	h.clock = h.run.DeadlineAt.Add(time.Minute)
	run, err := h.supervisor.Resume(context.Background(), h.run.ID)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if run.State != model.RunDeadlineReached || run.TerminalReason != model.ReasonDeadlineReached {
		t.Fatalf("run = %+v, want deadline_reached", run)
	}
	if len(h.prompter.prompts) != 0 {
		t.Fatalf("prompts = %v, want none after a late wake", h.prompter.prompts)
	}
}

// TestOneCompleteCycle walks limit → wake → sleep → wake → continue, which is
// what group five exists to deliver.
func TestOneCompleteCycle(t *testing.T) {
	h, wake, power, reset := limitedHarness(t)

	// Sleep.
	run, err := h.supervisor.PrepareAndSleep(context.Background(), h.run.ID, sleepConfig())
	if err != nil {
		t.Fatalf("PrepareAndSleep: %v", err)
	}
	if run.State != model.RunSleeping || power.slept != 1 || len(wake.registered) != 1 {
		t.Fatalf("run = %+v, slept = %d, wakes = %v", run.State, power.slept, wake.registered)
	}

	// Wake at the reset.
	h.clock = reset.Add(time.Minute)
	run, err = h.supervisor.Resume(context.Background(), h.run.ID)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if run.State != model.RunResuming {
		t.Fatalf("state = %q, want resuming", run.State)
	}

	// The allowance has returned, so the next tick continues the same session.
	h.usage.signal = claudeusage.Signal{Class: claudeusage.LimitNone}
	h.agents.agents = []herdr.AgentInfo{liveAgent(4, model.AgentIdle)}
	run = h.tick(t)

	if len(h.prompter.prompts) != 1 {
		t.Fatalf("prompts = %v, want exactly one continuation after the reset", h.prompter.prompts)
	}
	if h.prompter.targets[0] != "w-alpha:p1" {
		t.Fatalf("target = %q, want the exact saved pane", h.prompter.targets[0])
	}
	if run.Participants[0].Delivery.State != model.DeliveryAcknowledged {
		t.Fatalf("delivery = %+v, want acknowledged", run.Participants[0].Delivery)
	}
	// The second participant never received anything across the whole cycle.
	if run.Participants[1].State != model.ParticipantQueued {
		t.Fatalf("the queued participant = %q, want it untouched", run.Participants[1].State)
	}
}

// TestEvaluateSleepIsPureSoStatusCanAskWithoutActing keeps the decision
// inspectable from anywhere.
func TestEvaluateSleepIsPureSoStatusCanAskWithoutActing(t *testing.T) {
	h, _, power, reset := limitedHarness(t)
	run, err := h.service.Get(h.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	participant, _ := run.Active()

	decision := EvaluateSleep(run, participant, power, sleepConfig(), h.clock)
	if !decision.Sleep || decision.Reason != "" {
		t.Fatalf("decision = %+v, want a sleepable verdict", decision)
	}
	if !decision.WakeAt.Equal(reset.Add(-2 * time.Minute)) {
		t.Fatalf("wake = %v, want the lead before the reset", decision.WakeAt)
	}
	if power.slept != 0 {
		t.Fatal("evaluating the decision slept the Mac")
	}
	if len(decision.Gates) == 0 {
		t.Fatal("the decision carried no gates to explain itself")
	}
}

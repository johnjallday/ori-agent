package overnight

import (
	"context"
	"fmt"
	"time"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/model"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/systempower"
)

// This file is the sleep sequence: the part that turns a recognized Claude
// limit into a sleeping Mac, and back again.
//
// It is written as a series of gates that each have to pass, in order, with the
// run persisted before every step that has an effect outside this process. The
// ordering is the safety property. Registering a wake before persisting the run
// would leave a machine waking up for a run it has no record of; sleeping
// before verifying the wake would leave it asleep until someone opened the lid.
//
// Nothing here retries. Every failure leaves the run awake in a state that says
// what happened, because a Mac that stayed awake is a mild disappointment and a
// Mac that slept without a wake is someone's morning.

// WakeCoordinator is the helper's side of Ori's shared wake coordinator.
type WakeCoordinator interface {
	// Register asks Ori to program a wake. It is a request, not a schedule.
	Register(runID string, wakeAt time.Time, detail string) error
	// Verify waits for evidence that the wake exists, returning what was
	// actually programmed.
	Verify(ctx context.Context, runID string, wakeAt time.Time) (time.Time, error)
	// Cancel withdraws this run's candidate and nothing else.
	Cancel(runID string) error
}

// PowerService answers power questions and performs sleep.
type PowerService interface {
	SupportsSleep() bool
	PowerSource(ctx context.Context) systempower.Source
	Sleep(ctx context.Context) error
}

// SleepConfig is what the sleep sequence needs beyond the run itself.
type SleepConfig struct {
	// WakeLead is how far before the reset the machine should wake, so macOS,
	// the network, Ori, and Herdr are ready. It never permits an early prompt.
	WakeLead time.Duration
	// ApprovalGranted records that the user has authorized Ori to program wake
	// events. Without it nothing sleeps.
	ApprovalGranted bool
}

// SleepGate is one precondition and its outcome.
type SleepGate struct {
	Name   string
	Passed bool
	Detail string
}

// SleepDecision is the whole answer: whether to sleep, and every gate behind it.
type SleepDecision struct {
	Sleep bool
	Gates []SleepGate
	// WakeAt is the instant the machine should wake, once the gates allow it.
	WakeAt time.Time
	// Reason names the first gate that refused.
	Reason string
}

// EvaluateSleep checks every precondition without performing any of them.
//
// It is separate from the sequence that acts so status, doctor, and the run
// itself can all ask "would this sleep, and if not why not" without a single
// side effect.
func EvaluateSleep(run model.OvernightRun, participant model.RunParticipant,
	power PowerService, config SleepConfig, now time.Time,
) SleepDecision {
	decision := SleepDecision{}
	add := func(name string, passed bool, detail string) {
		decision.Gates = append(decision.Gates, SleepGate{Name: name, Passed: passed, Detail: detail})
		if !passed && decision.Reason == "" {
			decision.Reason = detail
		}
	}

	limit := participant.Limit
	hasLimit := limit != nil && limit.Sleepable && !limit.ResetAt.IsZero()
	add("verified included-session limit", hasLimit,
		"no verified included-session limit with a trustworthy reset is recorded")

	platformOK := power != nil && power.SupportsSleep()
	add("macOS", platformOK, "system sleep is supported on macOS only")

	add("wake approval", config.ApprovalGranted,
		"Ori has not been authorized to program macOS wake events")

	source := systempower.SourceUnknown
	if power != nil {
		source = power.PowerSource(context.Background())
	}
	add("external power", source.External(),
		"this Mac is on "+source.Label()+"; an Overnight Run sleeps only on external power")

	add("exact Claude session", participant.Binding.NativeSession.Value != "",
		"this participant has no exact native Claude session to return to")

	if hasLimit {
		add("reset is in the future", limit.ResetAt.After(now),
			"the reported reset time has already passed")
		add("reset is before the deadline", limit.ResetAt.Before(run.DeadlineAt),
			"the reported reset falls at or after the morning deadline")
		newer := true
		for _, consumed := range participant.ConsumedResets {
			if !limit.ResetAt.After(consumed) {
				newer = false
				break
			}
		}
		add("reset is newer than the last handled one", newer,
			"this reset boundary was already handled")
	}

	add("resume budget remains", run.RemainingResumes() > 0,
		fmt.Sprintf("all %d configured resumes have been used", run.MaxResumes))

	decision.Sleep = decision.Reason == ""
	if decision.Sleep {
		decision.WakeAt = limit.ResetAt.Add(-config.WakeLead)
		if !decision.WakeAt.After(now) {
			// The reset is closer than the lead time. Waking "now" is not a
			// wake; there is no point sleeping at all.
			decision.Sleep = false
			decision.Reason = "the reset is too close to sleep before it"
			decision.Gates = append(decision.Gates, SleepGate{
				Name: "time to sleep before the reset", Passed: false, Detail: decision.Reason,
			})
		}
	}
	return decision
}

// PrepareAndSleep runs the sleep sequence for a run that has detected a limit.
//
// The order is the contract: persist, register, verify, persist again, and only
// then sleep. Each step's failure leaves the run awake and visibly waiting.
func (s *Supervisor) PrepareAndSleep(ctx context.Context, runID string, config SleepConfig) (model.OvernightRun, error) {
	release, err := s.Store.Lock(ctx)
	if err != nil {
		return model.OvernightRun{}, fmt.Errorf("lock local state: %w", err)
	}
	saved, err := s.Store.Load()
	if err != nil {
		release()
		return model.OvernightRun{}, fmt.Errorf("read local state: %w", err)
	}
	run, ok := saved.Runs[runID]
	if !ok {
		release()
		return model.OvernightRun{}, ErrNotFound
	}
	if run.State != model.RunLimitDetected && run.State != model.RunPreparingSleep {
		release()
		return run, nil
	}
	participant, ok := run.Active()
	if !ok {
		release()
		return run, nil
	}

	now := s.now()
	decision := EvaluateSleep(run, participant, s.Power, config, now)
	if !decision.Sleep {
		// Awake, waiting, and explicit about why. Queued participants stay
		// unprompted: the included allowance is shared, so there is nothing
		// useful for anyone else to do either.
		run.State = model.RunWaitingForReset
		run.Timeline = append(run.Timeline, model.RunEvent{
			At: now, Kind: "sleep_refused", Participant: participant.ID, Detail: decision.Reason,
		})
		run.UpdatedAt = now
		saved.Runs[runID] = run
		err := s.Store.Save(saved)
		release()
		return run, err
	}

	// Persist the intention before anything outside this process happens, so a
	// crash between here and the sleep is recoverable rather than mysterious.
	run.State = model.RunPreparingSleep
	run.Wake = model.WakeOwnership{
		RequestedAt: decision.WakeAt,
		ResetAt:     participant.Limit.ResetAt,
		CandidateID: runID,
	}
	run.Timeline = append(run.Timeline, model.RunEvent{
		At: now, Kind: "preparing_sleep", Participant: participant.ID,
		Detail: "wake requested for " + decision.WakeAt.UTC().Format(time.RFC3339),
	})
	run.UpdatedAt = now
	saved.Runs[runID] = run
	if err := s.Store.Save(saved); err != nil {
		release()
		return model.OvernightRun{}, fmt.Errorf("persist the sleep intention: %w", err)
	}
	// The lock is released before waiting on the coordinator: verification can
	// take a minute, and holding the shared lock that long would block every
	// other bridge command on this machine.
	release()

	if s.Wake == nil {
		return s.recordSleepFailure(ctx, runID, "no wake coordinator is configured, so no wake could be requested")
	}
	if err := s.Wake.Register(runID, decision.WakeAt, "Claude included-session reset"); err != nil {
		return s.recordSleepFailure(ctx, runID, "Ori's wake coordinator did not accept the wake request")
	}
	programmed, err := s.Wake.Verify(ctx, runID, decision.WakeAt)
	if err != nil {
		// Registration is a request; only the owner's own record proves a wake
		// exists. Without it, this Mac does not sleep.
		_ = s.Wake.Cancel(runID)
		return s.recordSleepFailure(ctx, runID, "the requested wake could not be verified as programmed")
	}

	if err := s.recordVerifiedWake(ctx, runID, programmed); err != nil {
		return model.OvernightRun{}, err
	}
	if err := s.Power.Sleep(ctx); err != nil {
		return s.recordSleepFailure(ctx, runID, "macOS did not accept the sleep request")
	}
	return s.recordSlept(ctx, runID)
}

// recordSleepFailure leaves the run awake and says why.
func (s *Supervisor) recordSleepFailure(ctx context.Context, runID, detail string) (model.OvernightRun, error) {
	return s.updateRun(ctx, runID, func(run *model.OvernightRun, now time.Time) {
		run.State = model.RunWaitingForReset
		run.Wake.Verified = false
		run.Wake.Detail = detail
		run.Timeline = append(run.Timeline, model.RunEvent{At: now, Kind: "sleep_failed", Detail: detail})
	})
}

// recordVerifiedWake stores the owner's evidence before the machine sleeps.
func (s *Supervisor) recordVerifiedWake(ctx context.Context, runID string, programmed time.Time) error {
	_, err := s.updateRun(ctx, runID, func(run *model.OvernightRun, now time.Time) {
		run.Wake.Verified = true
		run.Wake.VerifiedAt = now
		run.Wake.RegisteredAt = now
		run.Wake.RequestedAt = programmed
		run.Wake.Detail = ""
		run.Timeline = append(run.Timeline, model.RunEvent{
			At: now, Kind: "wake_verified",
			Detail: "Ori programmed a wake at " + programmed.UTC().Format(time.RFC3339),
		})
	})
	return err
}

func (s *Supervisor) recordSlept(ctx context.Context, runID string) (model.OvernightRun, error) {
	return s.updateRun(ctx, runID, func(run *model.OvernightRun, now time.Time) {
		run.State = model.RunSleeping
		run.Timeline = append(run.Timeline, model.RunEvent{
			At: now, Kind: "slept", Detail: "the machine was asked to sleep until the verified wake",
		})
	})
}

// updateRun applies one change under the shared lock.
func (s *Supervisor) updateRun(ctx context.Context, runID string,
	apply func(*model.OvernightRun, time.Time),
) (model.OvernightRun, error) {
	release, err := s.Store.Lock(ctx)
	if err != nil {
		return model.OvernightRun{}, fmt.Errorf("lock local state: %w", err)
	}
	defer release()

	saved, err := s.Store.Load()
	if err != nil {
		return model.OvernightRun{}, fmt.Errorf("read local state: %w", err)
	}
	run, ok := saved.Runs[runID]
	if !ok {
		return model.OvernightRun{}, ErrNotFound
	}
	now := s.now()
	apply(&run, now)
	run.UpdatedAt = now
	saved.Runs[runID] = run
	if err := s.Store.Save(saved); err != nil {
		return model.OvernightRun{}, fmt.Errorf("persist the run: %w", err)
	}
	return run, nil
}

// Resume is what runs after the machine wakes.
//
// The fact that the Mac is awake proves nothing: it may have been opened by
// hand, woken for a different Ori wake, or woken by something else entirely.
// Durable state, not wakefulness, decides whether anyone may be prompted.
func (s *Supervisor) Resume(ctx context.Context, runID string) (model.OvernightRun, error) {
	release, err := s.Store.Lock(ctx)
	if err != nil {
		return model.OvernightRun{}, fmt.Errorf("lock local state: %w", err)
	}
	defer release()

	saved, err := s.Store.Load()
	if err != nil {
		return model.OvernightRun{}, fmt.Errorf("read local state: %w", err)
	}
	run, ok := saved.Runs[runID]
	if !ok {
		return model.OvernightRun{}, ErrNotFound
	}
	if run.State != model.RunSleeping && run.State != model.RunWaking && run.State != model.RunWaitingForReset {
		return run, nil
	}
	participant, ok := run.Active()
	if !ok {
		return run, nil
	}

	now := s.now()
	changed := false
	switch {
	case participant.Limit == nil || participant.Limit.ResetAt.IsZero():
		run.State = model.RunWaitingManual
		run.Timeline = append(run.Timeline, model.RunEvent{
			At: now, Kind: "resume_refused", Participant: participant.ID,
			Detail: "no reset time is recorded, so there is nothing to resume against",
		})
		changed = true
	case now.Before(participant.Limit.ResetAt):
		// Awake early. The reset has not happened, so the allowance has not
		// returned, and prompting now would simply hit the limit again.
		if run.State != model.RunWaitingForReset {
			run.State = model.RunWaitingForReset
			run.Timeline = append(run.Timeline, model.RunEvent{
				At: now, Kind: "early_wake", Participant: participant.ID,
				Detail: "awake before the reset; waiting rather than prompting",
			})
			changed = true
		}
	case !now.Before(run.DeadlineAt):
		// The wake arrived after the morning deadline. Nothing is prompted and
		// the exact session is not even restored.
		run.State = model.RunDeadlineReached
		run.TerminalReason = model.ReasonDeadlineReached
		run.Timeline = append(run.Timeline, model.RunEvent{
			At: now, Kind: "late_wake", Participant: participant.ID,
			Detail: "woke at or after the morning deadline; no continuation was sent",
		})
		changed = true
	default:
		// At or after the reset and still before the deadline: this is the one
		// case where work may continue. The continuation itself goes through
		// the ordinary supervisor path, which revalidates the exact session
		// before it prompts anything.
		run.State = model.RunResuming
		run.Timeline = append(run.Timeline, model.RunEvent{
			At: now, Kind: "resuming", Participant: participant.ID,
			Detail: "the reported reset has passed; revalidating the exact session",
		})
		changed = true
	}

	if !changed {
		return run, nil
	}
	run.UpdatedAt = now
	saved.Runs[runID] = run
	if err := s.Store.Save(saved); err != nil {
		return model.OvernightRun{}, fmt.Errorf("persist the resume: %w", err)
	}
	return run, nil
}

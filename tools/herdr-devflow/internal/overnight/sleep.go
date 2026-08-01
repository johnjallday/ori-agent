package overnight

import (
	"context"
	"fmt"
	"time"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/model"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/systempower"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/wakeclient"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/wakeprotocol"
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

// WakeCoordinator is the unprivileged side of the standalone Herdr wake
// service. It has no Ori runtime dependency or system-sleep capability.
type WakeCoordinator interface {
	// Register asks the standalone daemon to program a wake. It is a request,
	// not proof that the matching event exists.
	Register(runID string, wakeAt time.Time, detail string) error
	// Verify waits for evidence that the wake exists, returning what was
	// actually programmed.
	Verify(ctx context.Context, runID string, wakeAt time.Time) (time.Time, error)
	// Cancel withdraws this run's candidate and nothing else.
	Cancel(runID string) error
}

// EvidenceWakeCoordinator is implemented by production standalone clients.
// The legacy-shaped methods remain for focused state-machine fakes, while the
// production path persists protocol/build/read-back evidence before sleep.
type EvidenceWakeCoordinator interface {
	WakeCoordinator
	RegisterCandidate(context.Context, string, time.Time, string) (wakeclient.Evidence, error)
	VerifyCandidate(context.Context, string, time.Time) (wakeclient.Evidence, error)
	CancelCandidate(context.Context, string) (wakeclient.Evidence, error)
}

// PowerService answers power questions and performs sleep.
type PowerService interface {
	SupportsSleep() bool
	PowerSource(ctx context.Context) systempower.Source
	Sleep(ctx context.Context) error
}

// AssertionService is the unprivileged user-level capability used only by an
// explicitly confirmed stay-awake run. The root daemon never implements it.
type AssertionService interface {
	AcquireIdleSleepAssertion(context.Context, string) (string, error)
	IdleSleepAssertionActive(context.Context, string) bool
	ReleaseIdleSleepAssertion(context.Context, string) error
}

// SleepConfig is what the sleep sequence needs beyond the run itself.
type SleepConfig struct {
	// WakeLead is how far before the reset the machine should wake, so macOS,
	// the network, Ori, and Herdr are ready. It never permits an early prompt.
	WakeLead time.Duration
	// ApprovalGranted records that the standalone service handshake authorizes
	// events. Without it nothing sleeps.
	ApprovalGranted bool
	// StayAwake is the user-confirmed mode that explicitly refuses all
	// deliberate sleep/reset-wake actions for this run.
	StayAwake bool
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
		"the standalone Herdr wake service has not been authorized and verified as healthy")
	add("sleep-enabled mode", !config.StayAwake,
		"this Overnight Run was confirmed with --stay-awake, so it will wait without sleeping or scheduling a reset wake")

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
		// Two of these refusals are not "wait and see" — they are the end of
		// unattended execution, and saying so is the difference between a run
		// that finished and one that is still hoping.
		switch {
		case run.RemainingResumes() <= 0:
			run.State = model.RunCycleLimitReached
			run.TerminalReason = model.ReasonCycleLimitReached
			run.ActiveParticipant = ""
		case participant.Limit != nil && !participant.Limit.ResetAt.IsZero() &&
			!participant.Limit.ResetAt.Before(run.DeadlineAt):
			run.State = model.RunDeadlineReached
			run.TerminalReason = model.ReasonDeadlineReached
			run.ActiveParticipant = ""
		default:
			// Awake, waiting, and explicit about why. Queued participants stay
			// unprompted: the included allowance is shared, so there is
			// nothing useful for anyone else to do either.
			run.State = model.RunWaitingForReset
		}
		run.Timeline = append(run.Timeline, model.RunEvent{
			At: now, Kind: "sleep_refused", Participant: participant.ID, Detail: decision.Reason,
		})
		if run.State.Terminal() {
			s.withdrawWake(&run, now)
		}
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
		Source:      string(wakeprotocol.SourceOvernight),
		Purpose:     string(wakeprotocol.PurposeClaudeReset),
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
	// The lock is released before waiting on the standalone daemon: verification can
	// take a minute, and holding the shared lock that long would block every
	// other bridge command on this machine.
	release()

	if s.Wake == nil {
		return s.recordSleepFailure(ctx, runID, "no standalone wake service client is configured, so no wake could be requested")
	}
	var evidence wakeclient.Evidence
	var programmed time.Time
	var verifyErr error
	if wake, ok := s.Wake.(EvidenceWakeCoordinator); ok {
		evidence, verifyErr = wake.RegisterCandidate(ctx, runID, decision.WakeAt, "Claude included-session reset")
		if verifyErr == nil {
			evidence, verifyErr = wake.VerifyCandidate(ctx, runID, decision.WakeAt)
			programmed = evidence.ProgrammedAt
		}
	} else {
		verifyErr = s.Wake.Register(runID, decision.WakeAt, "Claude included-session reset")
		if verifyErr == nil {
			programmed, verifyErr = s.Wake.Verify(ctx, runID, decision.WakeAt)
		}
	}
	if verifyErr != nil {
		// Registration is a request; only the owner's own record proves a wake
		// exists. Without it, this Mac does not sleep.
		_ = s.Wake.Cancel(runID)
		return s.recordSleepFailure(ctx, runID, "the requested standalone wake could not be verified as programmed")
	}

	if err := s.recordVerifiedWake(ctx, runID, programmed, evidence); err != nil {
		return model.OvernightRun{}, err
	}
	if err := s.Power.Sleep(ctx); err != nil {
		return s.recordSleepFailure(ctx, runID, "macOS did not accept the sleep request")
	}
	return s.recordSlept(ctx, runID)
}

// EnsureStayAwake acquires and directly verifies the run-owned user-level
// assertion. An unavailable, lost, or unverifiable assertion leaves the run
// awake and visibly waiting; it never falls back to a reset wake or sleep.
func (s *Supervisor) EnsureStayAwake(ctx context.Context, runID string) (model.OvernightRun, error) {
	assertions, ok := s.Power.(AssertionService)
	if !ok {
		return s.recordSleepFailure(ctx, runID, "stay-awake mode is unavailable on this platform")
	}
	run, err := s.updateRun(ctx, runID, func(run *model.OvernightRun, now time.Time) {
		if run.Assertion.ID == "" {
			run.Assertion.ID = run.ID
			run.Assertion.Detail = "idle-sleep assertion requested"
		}
	})
	if err != nil || run.State.Terminal() {
		return run, err
	}
	if run.Assertion.ID != "" && assertions.IdleSleepAssertionActive(ctx, run.Assertion.ID) {
		return s.updateRun(ctx, runID, func(run *model.OvernightRun, now time.Time) {
			run.Assertion.VerifiedAt = now
			run.Assertion.Uncertain = false
			run.Assertion.Detail = "run-owned idle-sleep assertion remains verified"
			run.State = model.RunWaitingForReset
		})
	}
	assertionID, acquireErr := assertions.AcquireIdleSleepAssertion(ctx, run.ID)
	if acquireErr != nil || assertionID == "" || !assertions.IdleSleepAssertionActive(ctx, assertionID) {
		detail := "stay-awake assertion could not be acquired and verified"
		if acquireErr != nil {
			detail = detail + "; " + acquireErr.Error()
		}
		return s.recordSleepFailure(ctx, runID, detail)
	}
	return s.updateRun(ctx, runID, func(run *model.OvernightRun, now time.Time) {
		run.Assertion.ID = assertionID
		run.Assertion.AcquiredAt = now
		run.Assertion.VerifiedAt = now
		run.Assertion.Uncertain = false
		run.Assertion.Detail = "run-owned idle-sleep assertion verified"
		run.State = model.RunWaitingForReset
		run.Timeline = append(run.Timeline, model.RunEvent{At: now, Kind: "stay_awake_verified", Detail: run.Assertion.Detail})
	})
}

// ReleaseStayAwake releases only this run's assertion. A lost assertion is
// retained as uncertainty for recovery rather than claimed released.
func (s *Supervisor) ReleaseStayAwake(ctx context.Context, runID string) (model.OvernightRun, error) {
	run, err := s.updateRun(ctx, runID, func(run *model.OvernightRun, _ time.Time) {})
	if err != nil || run.Assertion.ID == "" || !run.Assertion.ReleasedAt.IsZero() {
		return run, err
	}
	assertions, ok := s.Power.(AssertionService)
	if !ok || assertions.ReleaseIdleSleepAssertion(ctx, run.Assertion.ID) != nil {
		return s.updateRun(ctx, runID, func(run *model.OvernightRun, _ time.Time) {
			run.Assertion.Uncertain = true
			run.Assertion.Detail = "run-owned idle-sleep assertion release was not proven"
		})
	}
	return s.updateRun(ctx, runID, func(run *model.OvernightRun, now time.Time) {
		run.Assertion.ReleasedAt = now
		run.Assertion.Uncertain = false
		run.Assertion.Detail = "run-owned idle-sleep assertion released"
	})
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
func (s *Supervisor) recordVerifiedWake(ctx context.Context, runID string, programmed time.Time, evidence wakeclient.Evidence) error {
	_, err := s.updateRun(ctx, runID, func(run *model.OvernightRun, now time.Time) {
		run.Wake.Verified = true
		run.Wake.VerifiedAt = now
		run.Wake.RegisteredAt = now
		run.Wake.RequestedAt = programmed
		run.Wake.ProgrammedAt = programmed
		run.Wake.ProtocolVersion = evidence.ProtocolVersion
		run.Wake.DaemonBuild = evidence.DaemonBuild
		run.Wake.HelperBuild = evidence.HelperBuild
		run.Wake.Detail = ""
		run.Timeline = append(run.Timeline, model.RunEvent{
			At: now, Kind: "wake_verified",
			Detail: "standalone Herdr wake service verified a wake at " + programmed.UTC().Format(time.RFC3339),
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
// hand, woken for a different system event, or woken by something else entirely.
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
		s.withdrawWake(&run, now)
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
		run.ActiveParticipant = ""
		// The wake that brought the machine back has served its purpose, and
		// this run will not use another.
		s.withdrawWake(&run, now)
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

// withdrawWake removes this run's wake candidate when the run will never use
// it. It is scoped to the run's own candidate: another subsystem's wake is not
// this run's to cancel, and a cancellation that cannot be confirmed is recorded
// as uncertain rather than claimed.
func (s *Supervisor) withdrawWake(run *model.OvernightRun, now time.Time) {
	if run.Wake.CandidateID == "" || run.Wake.Canceled {
		return
	}
	wake := s.Wake
	if run.Wake.Purpose == "overnight_scheduled_start" && s.StartWake != nil {
		wake = s.StartWake
	}
	if wake == nil {
		run.Wake.Uncertain = true
		run.Wake.Detail = "no standalone wake client was available to withdraw this run's candidate"
		return
	}
	var cancelErr error
	if evidenceWake, ok := wake.(EvidenceWakeCoordinator); ok {
		_, cancelErr = evidenceWake.CancelCandidate(context.Background(), run.Wake.CandidateID)
	} else {
		cancelErr = wake.Cancel(run.Wake.CandidateID)
	}
	if cancelErr != nil {
		run.Wake.Uncertain = true
		run.Wake.Detail = "this run's wake candidate could not be confirmed withdrawn"
		return
	}
	run.Wake.Canceled = true
	run.Wake.Uncertain = false
	run.Wake.Detail = ""
	run.Timeline = append(run.Timeline, model.RunEvent{
		At: now, Kind: "wake_withdrawn", Detail: "this run's wake candidate was withdrawn",
	})
}

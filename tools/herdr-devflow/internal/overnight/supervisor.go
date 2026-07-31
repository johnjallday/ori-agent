package overnight

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/claudeusage"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/herdr"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/model"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/tasklist"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/worktree"
)

// The supervisor is the part that actually touches an agent, so it is written
// to do as little as possible and to refuse rather than guess.
//
// One participant is active at a time. It is prompted only when Herdr says its
// exact saved session is idle, its worktree and plan still look the way they
// did at confirmation, and the next thing in that plan is work an unattended
// agent may do. Every prompt is written down before it is sent, and an
// acknowledgement that does not name the expected agent leaves the delivery
// uncertain and stops — a duplicate `continue` can duplicate implementation
// work, which is worse than stopping early.

// AgentObserver reports what Herdr can currently see.
type AgentObserver interface {
	AgentListInfo(ctx context.Context) ([]herdr.AgentInfo, error)
}

// Prompter submits one prompt to an exact target and returns Herdr's
// structured acknowledgement.
type Prompter interface {
	AgentPromptInfo(ctx context.Context, target, text string, timeout time.Duration) (herdr.AgentInfo, error)
}

// LimitClassifier answers what a Claude session has run out of.
type LimitClassifier interface {
	Classify(sessionID string, now, lastHandledReset time.Time) claudeusage.Signal
}

// PlanProofValidator is optional for historical records. New app-created runs
// carry a proof; legacy records stay readable and are never silently upgraded.
type PlanProofValidator interface {
	ValidatePlanProof(model.PlanProof, string, time.Time) error
}

// PlanReader reads one task list.
type PlanReader func(path string) tasklist.Plan

// Supervisor advances one Overnight Run.
type Supervisor struct {
	Store Store
	// Agents observes live agent state; nil leaves every participant
	// unobservable, which stops the run rather than guessing at it.
	Agents AgentObserver
	// Prompt delivers continuations. Nil means observation only.
	Prompt Prompter
	// ReadPlan reads a participant's task list; nil uses the real reader.
	ReadPlan PlanReader
	// Usage classifies Claude limits; nil means no limit can be recognized,
	// so nothing ever sleeps.
	Usage LimitClassifier
	// Wake is the reset-purpose standalone wake client. Nil means no
	// wake can be requested, so nothing sleeps.
	Wake WakeCoordinator
	// StartWake is the separately scoped scheduled-start wake client.
	StartWake WakeCoordinator
	// Power answers power questions and performs sleep. Nil means the same.
	Power PowerService
	// Git inspects a participant's worktree for the commit and cleanliness
	// facts the morning report shows. Nil leaves those unknown rather than
	// reporting a clean tree nobody looked at.
	Git worktree.Runner
	// Baseline is the integration branch divergence is counted against.
	Baseline string
	// Now supplies the clock.
	Now func() time.Time
	// PromptTimeout bounds one submission.
	PromptTimeout time.Duration
}

func (s *Supervisor) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Supervisor) readPlan(path string) tasklist.Plan {
	if s.ReadPlan != nil {
		return s.ReadPlan(path)
	}
	return tasklist.ReadPlan(path)
}

func (s *Supervisor) promptTimeout() time.Duration {
	if s.PromptTimeout > 0 {
		return s.PromptTimeout
	}
	return 30 * time.Second
}

// ContinuePrompt is the whole instruction an unattended continuation carries.
// It is deliberately short: the agent already has its plan, and a supervisor
// that re-explained the work would be inventing instructions nobody approved.
const ContinuePrompt = "continue"

// Tick advances one run by one step.
//
// It is safe to call at any time and from any process: it takes the shared
// lock, re-reads durable state, and makes at most one decision. Calling it
// repeatedly without anything changing does nothing, which is what stops a
// polling interval from becoming a prompt generator.
func (s *Supervisor) Tick(ctx context.Context, runID string) (model.OvernightRun, error) {
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
	if run.State.Terminal() {
		return run, nil
	}

	now := s.now()
	changed := s.advance(ctx, &run, now)
	if !changed {
		return run, nil
	}
	run.UpdatedAt = now
	saved.Runs[runID] = run
	if err := s.Store.Save(saved); err != nil {
		return model.OvernightRun{}, fmt.Errorf("persist the run: %w", err)
	}
	return run, nil
}

// advance makes at most one decision and reports whether anything changed.
func (s *Supervisor) advance(ctx context.Context, run *model.OvernightRun, now time.Time) bool {
	if now.Before(run.StartAt) {
		// A confirmed run that has not started yet is not a run that is idle;
		// it is a run that is waiting, and waiting is not a decision.
		return false
	}
	if run.Wake.Purpose == "overnight_scheduled_start" && !run.Wake.Canceled {
		s.withdrawWake(run, now)
		if run.Wake.Uncertain {
			run.State = model.RunWaitingManual
			run.Uncertainty = "the served pre-start wake was not confirmed withdrawn; run wt herd wake doctor"
			run.Timeline = append(run.Timeline, model.RunEvent{At: now, Kind: "pre_start_wake_uncertain", Detail: run.Uncertainty})
			return true
		}
		run.Timeline = append(run.Timeline, model.RunEvent{At: now, Kind: "pre_start_wake_withdrawn", Detail: "verified scheduled-start wake was withdrawn before work began"})
	}
	// The deadline is absolute for new work. An agent already working is
	// observed to a stop rather than interrupted.
	if !now.Before(run.DeadlineAt) {
		return s.finishAtDeadline(ctx, run, now)
	}
	// While the run is waiting out a limit, nothing may be prompted — the
	// included allowance is shared, so the queue is frozen, not redirected.
	if frozen(run.State) {
		return false
	}

	participant, ok := s.activeParticipant(run, now)
	if !ok {
		return s.finishRun(run, now, model.RunCompleted, model.ReasonQueueComplete, "every participant reached a terminal outcome")
	}
	if participant.PlanProof.SessionID != "" {
		if validator, ok := s.Usage.(PlanProofValidator); ok {
			if err := validator.ValidatePlanProof(participant.PlanProof, participant.Binding.NativeSession.Value, now); err != nil {
				return s.stopParticipant(run, participant, now, model.ParticipantWaitingManual, model.ReasonBlocked,
					err.Error(), "wt herd overnight show "+run.ID)
			}
		}
	}

	live, observation := s.observe(ctx, *participant)
	switch observation.kind {
	case observationUnavailable:
		// Herdr could not be consulted. That is a reason to do nothing, not a
		// reason to assume an agent is idle.
		return false
	case observationDrifted:
		return s.stopParticipant(run, participant, now, model.ParticipantWaitingManual, model.ReasonBlocked,
			observation.detail, "wt herd rebind --feature "+participant.Feature.Name)
	}

	if signal, stop := s.classifyLimit(run, participant, now); stop {
		return true
	} else if signal {
		return true
	}

	if live.AgentStatus == model.AgentWorking {
		// Observation only. An agent mid-turn is making progress, and the one
		// thing that would certainly not help is another prompt.
		return s.recordObservation(participant, live, now)
	}

	return s.considerContinuation(ctx, run, participant, live, now)
}

// frozen reports whether the run is inside a sleep/wake cycle, where no
// participant may be prompted.
func frozen(state model.RunState) bool {
	switch state {
	case model.RunLimitDetected, model.RunPreparingSleep, model.RunSleeping,
		model.RunWaking, model.RunWaitingForReset:
		// Resuming is deliberately absent: it is the one state inside a cycle
		// where the queue head may be prompted again.
		return true
	default:
		return false
	}
}

// activeParticipant returns the participant the supervisor may act on,
// promoting the queue head when nothing is active yet.
func (s *Supervisor) activeParticipant(run *model.OvernightRun, now time.Time) (*model.RunParticipant, bool) {
	for index := range run.Participants {
		if run.Participants[index].State == model.ParticipantActive {
			run.ActiveParticipant = run.Participants[index].ID
			return &run.Participants[index], true
		}
	}
	for index := range run.Participants {
		if run.Participants[index].State != model.ParticipantQueued {
			continue
		}
		run.Participants[index].State = model.ParticipantActive
		run.Participants[index].UpdatedAt = now
		run.ActiveParticipant = run.Participants[index].ID
		run.State = model.RunRunning
		run.Timeline = append(run.Timeline, model.RunEvent{
			At: now, Kind: "participant_activated", Participant: run.Participants[index].ID,
			Detail: run.Participants[index].Feature.Name + " became the queue head",
		})
		return &run.Participants[index], true
	}
	return nil, false
}

type observationKind int

const (
	observationLive observationKind = iota
	observationUnavailable
	observationDrifted
)

type observation struct {
	kind   observationKind
	detail string
}

// observe finds the exact live agent for a participant.
//
// Matching is by native session first, and every other coordinate is checked
// against the record. A pane that was renamed or replaced is drift, and drift
// stops the participant: prompting "whatever is in that pane now" is how an
// unattended run would eventually talk to the wrong conversation.
func (s *Supervisor) observe(ctx context.Context, participant model.RunParticipant) (herdr.AgentInfo, observation) {
	if s.Agents == nil {
		return herdr.AgentInfo{}, observation{kind: observationUnavailable, detail: "no agent observer is configured"}
	}
	agents, err := s.Agents.AgentListInfo(ctx)
	if err != nil {
		return herdr.AgentInfo{}, observation{kind: observationUnavailable, detail: "Herdr could not be consulted"}
	}

	var matches []herdr.AgentInfo
	for _, agent := range agents {
		if agent.AgentSession == nil || agent.AgentSession.Value == "" {
			continue
		}
		if agent.AgentSession.Value == participant.Binding.NativeSession.Value {
			matches = append(matches, agent)
		}
	}
	switch len(matches) {
	case 0:
		return herdr.AgentInfo{}, observation{
			kind:   observationDrifted,
			detail: "the exact Claude session this participant was bound to is no longer running",
		}
	case 1:
	default:
		return herdr.AgentInfo{}, observation{
			kind:   observationDrifted,
			detail: "several live agents report this participant's native session",
		}
	}

	live := matches[0]
	if live.WorkspaceID != participant.Binding.WorkspaceID || live.PaneID != participant.Binding.PaneID {
		return herdr.AgentInfo{}, observation{
			kind:   observationDrifted,
			detail: "this participant's agent moved to a different workspace or pane after it was confirmed",
		}
	}
	if !worktree.Contains(participant.Feature.Path, agentDirectory(live)) {
		return herdr.AgentInfo{}, observation{
			kind:   observationDrifted,
			detail: "this participant's agent is no longer working in its confirmed worktree",
		}
	}
	return live, observation{kind: observationLive}
}

func agentDirectory(agent herdr.AgentInfo) string {
	if agent.Cwd != "" {
		return agent.Cwd
	}
	return agent.ForegroundCwd
}

// classifyLimit asks the usage adapter what this session has run out of and
// reports (limitRecognized, participantStopped).
func (s *Supervisor) classifyLimit(run *model.OvernightRun, participant *model.RunParticipant, now time.Time) (bool, bool) {
	if s.Usage == nil {
		return false, false
	}
	signal := s.Usage.Classify(participant.Binding.NativeSession.Value, now, lastConsumedReset(*participant))
	switch signal.Class {
	case claudeusage.LimitNone:
		return false, false
	case claudeusage.LimitIncludedSession:
		if !signal.Sleepable {
			// A limit that cannot be waited out safely is a stop, not a sleep.
			participant.Limit = limitRecord(signal)
			return false, s.stopParticipant(run, participant, now, model.ParticipantWaitingManual, model.ReasonBlocked,
				signal.Reason, "wt herd overnight show "+run.ID)
		}
		// The whole run enters the sleep sequence and the queue freezes. The
		// participant keeps the head: the allowance is shared, so promoting
		// anyone else would just consume the same exhausted window.
		participant.Limit = limitRecord(signal)
		participant.UpdatedAt = now
		run.State = model.RunLimitDetected
		run.Timeline = append(run.Timeline, model.RunEvent{
			At: now, Kind: "limit_detected", Participant: participant.ID,
			Detail: "included five-hour session limit; Claude reports a reset at " + signal.ResetAt.UTC().Format(time.RFC3339),
		})
		return true, false
	default:
		participant.Limit = limitRecord(signal)
		return false, s.stopParticipant(run, participant, now, model.ParticipantWaitingManual, model.ReasonBlocked,
			signal.Reason, "wt herd overnight show "+run.ID)
	}
}

func limitRecord(signal claudeusage.Signal) *model.DetectedLimit {
	return &model.DetectedLimit{
		Class:      string(signal.Class),
		DetectedAt: signal.DetectedAt,
		ResetAt:    signal.ResetAt,
		AuthMode:   string(signal.AuthMode),
		Sleepable:  signal.Sleepable,
		Detail:     signal.Reason,
	}
}

func lastConsumedReset(participant model.RunParticipant) time.Time {
	var latest time.Time
	for _, reset := range participant.ConsumedResets {
		if reset.After(latest) {
			latest = reset
		}
	}
	return latest
}

// recordObservation stores what Herdr reported without acting on it.
func (s *Supervisor) recordObservation(participant *model.RunParticipant, live herdr.AgentInfo, now time.Time) bool {
	state := string(live.AgentStatus)
	if participant.LastObservedState == state && participant.LastObservedSeq == live.StateChangeSeq {
		return false
	}
	participant.LastObservedState = state
	participant.LastObservedSeq = live.StateChangeSeq
	participant.UpdatedAt = now
	return true
}

// considerContinuation decides whether a settled agent may be prompted.
func (s *Supervisor) considerContinuation(ctx context.Context, run *model.OvernightRun,
	participant *model.RunParticipant, live herdr.AgentInfo, now time.Time,
) bool {
	plan := s.readPlan(participant.Checkpoint.TaskListPath)
	switch plan.State {
	case tasklist.PlanAvailable:
	case tasklist.PlanAbsent:
		return s.stopParticipant(run, participant, now, model.ParticipantWaitingManual, model.ReasonBlocked,
			"this participant's task list no longer exists", "restore "+participant.Checkpoint.TaskListPath)
	default:
		return s.stopParticipant(run, participant, now, model.ParticipantWaitingManual, model.ReasonBlocked,
			"this participant's task list could not be understood", "check "+participant.Checkpoint.TaskListPath)
	}
	// Progress is read from the file, never written to it: the agent alone
	// ticks its own checklist, and a supervisor that did so would be reporting
	// work nobody had done.
	updateCheckpoint(participant, plan, now)
	s.recordValidation(participant, now)

	if next, ok := plan.SafeNext(); ok {
		return s.deliverContinuation(ctx, run, participant, live, next, now)
	}
	if manual, ok := plan.FirstManual(); ok {
		return s.stopParticipant(run, participant, now, model.ParticipantReadyForReview, model.ReasonManualCheckpoint,
			"reached "+manual.Ordinal+" "+manual.Text+", which needs a person",
			"review "+participant.Feature.Name+", then continue it yourself")
	}
	return s.stopParticipant(run, participant, now, model.ParticipantCompleted, model.ReasonQueueComplete,
		"every item an unattended agent may do is complete",
		"review "+participant.Feature.Name+" and take it through delivery")
}

// recordValidation captures the Git facts the morning report promises.
//
// It is read-only and best-effort: an unreadable worktree leaves the previous
// summary in place rather than replacing it with zeroes, because "clean" and
// "nobody looked" are different claims and only one of them is safe to print.
func (s *Supervisor) recordValidation(participant *model.RunParticipant, now time.Time) {
	if s.Git == nil || participant.Feature.Path == "" {
		return
	}
	baseline := s.Baseline
	if baseline == "" {
		baseline = "dev"
	}
	facts := worktree.InspectFacts(context.Background(), s.Git, participant.Feature.Path, baseline)
	summary := participant.Validation
	if facts.DirtyAvailability == worktree.FactAvailable {
		summary.Dirty = facts.Dirty
	}
	if facts.DivergenceAvailability == worktree.FactAvailable {
		// Commits made overnight are the growth in how far ahead the branch is
		// of its baseline, which is the only count available without reading
		// the log.
		if facts.Ahead > summary.Ahead {
			summary.Commits += facts.Ahead - summary.Ahead
		}
		summary.Ahead, summary.Behind = facts.Ahead, facts.Behind
	}
	summary.ObservedAt = now
	participant.Validation = summary
}

// updateCheckpoint refreshes the recorded plan position from the file.
func updateCheckpoint(participant *model.RunParticipant, plan tasklist.Plan, now time.Time) {
	participant.Checkpoint.SubtasksTotal = plan.SubtasksTotal
	participant.Checkpoint.SubtasksCompleted = plan.SubtasksCompleted
	participant.Checkpoint.ImplementationComplete = plan.ImplementationBoundaryComplete()
	participant.Checkpoint.ObservedAt = now
	if next, ok := plan.SafeNext(); ok {
		participant.Checkpoint.NextOrdinal, participant.Checkpoint.NextText = next.Ordinal, next.Text
	} else {
		participant.Checkpoint.NextOrdinal, participant.Checkpoint.NextText = "", ""
	}
	if manual, ok := plan.FirstManual(); ok {
		participant.Checkpoint.ManualOrdinal, participant.Checkpoint.ManualText = manual.Ordinal, manual.Text
	} else {
		participant.Checkpoint.ManualOrdinal, participant.Checkpoint.ManualText = "", ""
	}
}

// deliverContinuation sends at most one `continue`, once.
func (s *Supervisor) deliverContinuation(ctx context.Context, run *model.OvernightRun,
	participant *model.RunParticipant, live herdr.AgentInfo, next tasklist.Item, now time.Time,
) bool {
	if participant.Delivery.State == model.DeliveryUncertain {
		// An unresolved delivery is never retried automatically. Whether that
		// prompt arrived is exactly what nobody knows.
		return false
	}
	key := idempotencyKey(*run, *participant)
	if participant.Delivery.IdempotencyKey == key && participant.Delivery.State == model.DeliveryAcknowledged {
		// This continuation was already delivered for this boundary. A new one
		// needs new evidence — a state transition or checklist movement — not
		// another turn of the polling loop.
		if !s.settledSincePrompt(*participant, live) {
			return false
		}
	}
	if s.Prompt == nil {
		return false
	}

	delivery := model.RunDelivery{
		ID:             fmt.Sprintf("%s-d%d", participant.ID, participant.AcknowledgedResumes+1),
		IdempotencyKey: key,
		State:          model.DeliveryDelivering,
		Summary:        "continue at " + next.Ordinal,
		Expected:       participant.Binding,
		AttemptedAt:    now,
	}
	participant.Delivery = delivery
	participant.UpdatedAt = now
	// The delivering record is written before submission on purpose: finding it
	// after a crash means the prompt may have arrived, and that has to be
	// discoverable rather than reconstructed.
	if err := s.persistDelivery(run, *participant); err != nil {
		participant.Delivery.State = model.DeliveryUncertain
		participant.Delivery.Detail = "the delivery record could not be written before submission"
		return true
	}

	acknowledgement, err := s.Prompt.AgentPromptInfo(ctx, participant.Binding.PaneID, ContinuePrompt, s.promptTimeout())
	settled := s.now()
	participant.Delivery.SettledAt = settled
	switch {
	case err != nil:
		participant.Delivery.State = model.DeliveryFailed
		participant.Delivery.Detail = "Herdr did not accept the continuation"
		run.Timeline = append(run.Timeline, model.RunEvent{
			At: settled, Kind: "prompt_failed", Participant: participant.ID, Detail: "submission was rejected",
		})
	case !acknowledges(participant.Binding, acknowledgement):
		// Herdr accepted something, but not demonstrably this agent. That is
		// the uncertain case, and it stops rather than repeating.
		participant.Delivery.State = model.DeliveryUncertain
		participant.Delivery.Detail = "the acknowledgement did not name the expected agent, pane, and session"
		run.Timeline = append(run.Timeline, model.RunEvent{
			At: settled, Kind: "prompt_uncertain", Participant: participant.ID,
			Detail: "acknowledgement did not match the expected binding",
		})
	default:
		participant.Delivery.State = model.DeliveryAcknowledged
		participant.LastObservedSeq = acknowledgement.StateChangeSeq
		participant.LastObservedState = string(acknowledgement.AgentStatus)
		run.Timeline = append(run.Timeline, model.RunEvent{
			At: settled, Kind: "prompt_acknowledged", Participant: participant.ID,
			Detail: "continuation " + participant.Delivery.ID + " acknowledged",
		})
		s.consumeResume(run, participant, settled)
	}
	participant.UpdatedAt = settled
	return true
}

// consumeResume counts a post-reset continuation, and only that.
//
// A cycle is consumed by an acknowledged continuation reaching the exact
// session — never by scheduling a wake, waking early, or a delivery whose
// acknowledgement nobody could confirm. Counting any of those would let a run
// exhaust its budget without doing any work.
func (s *Supervisor) consumeResume(run *model.OvernightRun, participant *model.RunParticipant, now time.Time) {
	if run.State != model.RunResuming {
		// An ordinary continuation inside the current window. The initial
		// execution window is cycle zero and costs nothing.
		return
	}
	participant.AcknowledgedResumes++
	run.AcknowledgedResumes++
	if participant.Limit != nil && !participant.Limit.ResetAt.IsZero() {
		// Record the boundary as handled so a repeat of the same reset can
		// never start another sleep/wake cycle.
		participant.ConsumedResets = append(participant.ConsumedResets, participant.Limit.ResetAt)
		participant.Limit = nil
	}
	// The wake this cycle owned has fired and is spent.
	run.Wake = model.WakeOwnership{}
	run.State = model.RunRunning
	run.Timeline = append(run.Timeline, model.RunEvent{
		At: now, Kind: "resume_acknowledged", Participant: participant.ID,
		Detail: fmt.Sprintf("resume %d of %d reached the exact session",
			run.AcknowledgedResumes, run.MaxResumes),
	})
}

// persistDelivery writes the in-flight delivery before the prompt is sent.
func (s *Supervisor) persistDelivery(run *model.OvernightRun, participant model.RunParticipant) error {
	saved, err := s.Store.Load()
	if err != nil {
		return err
	}
	stored, ok := saved.Runs[run.ID]
	if !ok {
		return ErrNotFound
	}
	for index := range stored.Participants {
		if stored.Participants[index].ID == participant.ID {
			stored.Participants[index].Delivery = participant.Delivery
			stored.Participants[index].UpdatedAt = participant.UpdatedAt
		}
	}
	stored.State = run.State
	stored.ActiveParticipant = run.ActiveParticipant
	saved.Runs[run.ID] = stored
	return s.Store.Save(saved)
}

// settledSincePrompt reports whether the agent has moved on since the last
// acknowledged continuation. Without new evidence, a repeated tick is not a
// reason to prompt again.
func (s *Supervisor) settledSincePrompt(participant model.RunParticipant, live herdr.AgentInfo) bool {
	if live.StateChangeSeq == 0 && participant.LastObservedSeq == 0 {
		// Herdr reported no sequence at all, so no transition can be proven.
		return false
	}
	return live.StateChangeSeq > participant.LastObservedSeq
}

// idempotencyKey binds one continuation to a run, participant, reset boundary,
// and cycle. Replaying a tick after a crash reproduces the same key, which is
// what makes a duplicate prompt recognizable rather than plausible.
func idempotencyKey(run model.OvernightRun, participant model.RunParticipant) string {
	boundary := "initial"
	if reset := lastConsumedReset(participant); !reset.IsZero() {
		boundary = reset.UTC().Format(time.RFC3339)
	}
	return strings.Join([]string{
		run.ID, participant.ID, boundary, fmt.Sprintf("%d", participant.AcknowledgedResumes),
	}, ":")
}

// acknowledges reports whether Herdr's response names the exact agent the
// prompt was aimed at.
func acknowledges(expected model.AgentBinding, actual herdr.AgentInfo) bool {
	if actual.PaneID != expected.PaneID || actual.WorkspaceID != expected.WorkspaceID {
		return false
	}
	if expected.NativeSession.Value == "" {
		return false
	}
	return actual.AgentSession != nil && actual.AgentSession.Value == expected.NativeSession.Value
}

// stopParticipant records a terminal outcome and hands the queue on.
func (s *Supervisor) stopParticipant(run *model.OvernightRun, participant *model.RunParticipant, now time.Time,
	state model.ParticipantState, outcome model.TerminalReason, detail, recovery string,
) bool {
	participant.State = state
	participant.Outcome = outcome
	participant.Recovery = recovery
	participant.UpdatedAt = now
	run.ActiveParticipant = ""
	run.Timeline = append(run.Timeline, model.RunEvent{
		At: now, Kind: "participant_stopped", Participant: participant.ID,
		Detail: participant.Feature.Name + ": " + detail,
	})

	// The next queued participant becomes active on the following tick, which
	// keeps activation and stopping separate decisions in the record.
	if _, ok := run.NextQueued(); !ok {
		s.finishRun(run, now, terminalStateFor(run), terminalReasonFor(run), "no queued participant remains")
	}
	return true
}

// terminalStateFor picks the run state that describes how its participants
// ended. A run whose agents all stopped for review did not "complete".
func terminalStateFor(run *model.OvernightRun) model.RunState {
	uncertain, review, blocked := false, false, false
	for _, participant := range run.Participants {
		switch participant.State {
		case model.ParticipantUncertain:
			uncertain = true
		case model.ParticipantReadyForReview:
			review = true
		case model.ParticipantWaitingManual, model.ParticipantFailed:
			blocked = true
		}
	}
	switch {
	case uncertain:
		return model.RunUncertain
	case review:
		return model.RunReadyForReview
	case blocked:
		return model.RunWaitingManual
	default:
		return model.RunCompleted
	}
}

func terminalReasonFor(run *model.OvernightRun) model.TerminalReason {
	switch terminalStateFor(run) {
	case model.RunUncertain:
		return model.ReasonUncertain
	case model.RunReadyForReview:
		return model.ReasonManualCheckpoint
	case model.RunWaitingManual:
		return model.ReasonBlocked
	default:
		return model.ReasonQueueComplete
	}
}

// finishAtDeadline applies the absolute morning boundary.
func (s *Supervisor) finishAtDeadline(ctx context.Context, run *model.OvernightRun, now time.Time) bool {
	participant, ok := run.Active()
	if ok {
		live, observed := s.observe(ctx, participant)
		if observed.kind == observationLive && live.AgentStatus == model.AgentWorking {
			// Work already in flight is allowed to settle. Interrupting it
			// would discard exactly the progress the run existed to make.
			if run.State == model.RunOverrun {
				return false
			}
			run.State = model.RunOverrun
			run.Timeline = append(run.Timeline, model.RunEvent{
				At: now, Kind: "overrun", Participant: participant.ID,
				Detail: "the deadline passed while this agent was working; it will not be prompted again",
			})
			return true
		}
	}
	return s.finishRun(run, now, model.RunDeadlineReached, model.ReasonDeadlineReached,
		"the morning deadline passed")
}

// finishRun records a terminal run state once.
func (s *Supervisor) finishRun(run *model.OvernightRun, now time.Time,
	state model.RunState, reason model.TerminalReason, detail string,
) bool {
	if run.State == state {
		return false
	}
	run.State = state
	run.TerminalReason = reason
	run.ActiveParticipant = ""
	// A finished run must not leave the Mac waking up for work nobody will do.
	s.withdrawWake(run, now)
	run.Timeline = append(run.Timeline, model.RunEvent{At: now, Kind: "run_finished", Detail: detail})
	run.UpdatedAt = now
	report := BuildReport(*run, now)
	run.Report = &report
	return true
}

// ErrNoActiveRun means no run is available to supervise.
var ErrNoActiveRun = errors.New("no active Overnight Run")

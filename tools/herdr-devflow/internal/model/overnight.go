package model

import "time"

// This file defines the durable record of an Overnight Run.
//
// A run is not a schedule. A schedule is one prompt at one time; a run watches
// an ordered queue of Claude agents across hours, may put the Mac to sleep, and
// has to survive a crash, a restart, and a wake without prompting twice or
// losing which agent it was in the middle of. That needs its own state machine
// and its own record, not extra fields on a Schedule.
//
// Everything here is written before the action it describes and re-read after
// it, so recovery never has to assume an effect happened. Prompt bodies,
// terminal content, and account details are deliberately absent: the run stores
// what it must be able to prove, and a summary of everything else.

// RunVersion is the schema version of one persisted Overnight Run.
const RunVersion = 2

// WakeMode states how an Overnight Run keeps the Mac available. Sleep is the
// historical/default behavior; stay-awake deliberately never registers a
// reset wake or asks macOS to sleep.
type WakeMode string

const (
	WakeModeSleep     WakeMode = "sleep"
	WakeModeStayAwake WakeMode = "stay_awake"
)

// RunState is the lifecycle position of a whole run.
type RunState string

const (
	// RunScheduled is a confirmed run whose start time has not arrived.
	RunScheduled RunState = "scheduled"
	// RunRunning means the queue head is being supervised.
	RunRunning RunState = "running"
	// RunLimitDetected means a verified included-session limit was recorded and
	// the sleep sequence has been entered.
	RunLimitDetected RunState = "limit_detected"
	// RunPreparingSleep means every pre-sleep gate is being checked and the
	// wake is being registered and verified.
	RunPreparingSleep RunState = "preparing_sleep"
	// RunSleeping means sleep was requested after every gate passed.
	RunSleeping RunState = "sleeping"
	// RunWaking means the Mac came back and durable state is being reloaded.
	RunWaking RunState = "waking"
	// RunResuming means the exact session is being revalidated before one
	// post-reset continuation.
	RunResuming RunState = "resuming"
	// RunWaitingForReset is the awake degraded state: a limit is known, but
	// something stopped the sleep, so the run waits without prompting anyone.
	RunWaitingForReset RunState = "waiting_for_reset"
	// RunWaitingManual means a decision only a person can make is outstanding.
	RunWaitingManual RunState = "waiting_manual"
	// RunReadyForReview means work reached a manual delivery checkpoint.
	RunReadyForReview RunState = "ready_for_review"
	// RunOverrun means the deadline passed while an agent was still working;
	// it is observed to a safe stop, never interrupted.
	RunOverrun RunState = "overrun"
	// RunCompleted means every participant reached a terminal outcome.
	RunCompleted RunState = "completed"
	// RunDeadlineReached ended unattended execution at the morning deadline.
	RunDeadlineReached RunState = "deadline_reached"
	// RunCycleLimitReached ended it at the configured resume ceiling.
	RunCycleLimitReached RunState = "cycle_limit_reached"
	// RunCanceled was stopped by the user.
	RunCanceled RunState = "canceled"
	// RunFailed ended on an error the run could not classify as anything safer.
	RunFailed RunState = "failed"
	// RunUncertain means an effect could not be proven either way — a prompt
	// whose acknowledgement never arrived, a wake that cannot be confirmed
	// canceled. It is a terminal state that asks for a person, not a retry.
	RunUncertain RunState = "uncertain"
)

// Terminal reports whether a run has finished and will take no further action.
func (s RunState) Terminal() bool {
	switch s {
	case RunCompleted, RunDeadlineReached, RunCycleLimitReached, RunCanceled, RunFailed, RunUncertain:
		return true
	default:
		return false
	}
}

// Label is the operator-facing name of a run state.
func (s RunState) Label() string {
	switch s {
	case RunWaitingForReset:
		return "waiting for reset"
	case RunWaitingManual:
		return "waiting on a decision"
	case RunReadyForReview:
		return "ready for review"
	case RunDeadlineReached:
		return "deadline reached"
	case RunCycleLimitReached:
		return "resume limit reached"
	case RunLimitDetected:
		return "limit detected"
	case RunPreparingSleep:
		return "preparing to sleep"
	default:
		return string(s)
	}
}

// ParticipantState is one enrolled agent's position in the run. Exactly one
// non-terminal participant may be active; every other one is queued and must
// not receive an unattended prompt.
type ParticipantState string

const (
	ParticipantQueued         ParticipantState = "queued"
	ParticipantActive         ParticipantState = "active"
	ParticipantCompleted      ParticipantState = "completed"
	ParticipantReadyForReview ParticipantState = "ready_for_review"
	ParticipantWaitingManual  ParticipantState = "waiting_manual"
	ParticipantFailed         ParticipantState = "failed"
	ParticipantCanceled       ParticipantState = "canceled"
	ParticipantUncertain      ParticipantState = "uncertain"
)

// Terminal reports whether a participant has finished.
func (s ParticipantState) Terminal() bool {
	switch s {
	case ParticipantCompleted, ParticipantReadyForReview, ParticipantWaitingManual,
		ParticipantFailed, ParticipantCanceled, ParticipantUncertain:
		return true
	default:
		return false
	}
}

// TerminalReason is why unattended execution stopped. It is recorded even for
// a successful run, because "finished the work" and "ran out of night" need to
// be distinguishable in the morning.
type TerminalReason string

const (
	ReasonNone              TerminalReason = ""
	ReasonQueueComplete     TerminalReason = "queue_complete"
	ReasonDeadlineReached   TerminalReason = "deadline_reached"
	ReasonCycleLimitReached TerminalReason = "cycle_limit_reached"
	ReasonCanceled          TerminalReason = "canceled"
	ReasonManualCheckpoint  TerminalReason = "manual_checkpoint"
	ReasonBlocked           TerminalReason = "blocked"
	ReasonSleepUnavailable  TerminalReason = "sleep_unavailable"
	ReasonUncertain         TerminalReason = "uncertain"
	ReasonFailed            TerminalReason = "failed"
)

// TaskCheckpoint is the task-list position a participant was at. It is a
// snapshot for reporting and for detecting drift, never an authority: the file
// in the worktree is the plan, and the agent alone ticks it off.
type TaskCheckpoint struct {
	// TaskListPath is the canonical plan inside the participant's worktree.
	TaskListPath string `json:"task_list_path,omitempty"`
	// ModTime is when that file last changed, which is how a stale checkpoint
	// is recognized without re-reading the whole plan.
	ModTime time.Time `json:"mod_time,omitzero"`
	// SubtasksTotal and SubtasksCompleted are implementation counts only.
	SubtasksTotal     int `json:"subtasks_total"`
	SubtasksCompleted int `json:"subtasks_completed"`
	// NextOrdinal and NextText name the next safe implementation subtask.
	NextOrdinal string `json:"next_ordinal,omitempty"`
	NextText    string `json:"next_text,omitempty"`
	// ManualOrdinal and ManualText name the first unresolved checkpoint that
	// only a person may cross.
	ManualOrdinal string `json:"manual_ordinal,omitempty"`
	ManualText    string `json:"manual_text,omitempty"`
	// ImplementationComplete means only delivery work remains.
	ImplementationComplete bool `json:"implementation_complete"`
	// ObservedAt is when the plan was read.
	ObservedAt time.Time `json:"observed_at,omitzero"`
}

// DeliveryState is the at-most-once boundary for one unattended prompt.
type DeliveryState string

const (
	// DeliveryPending means a delivery was recorded but not yet attempted.
	DeliveryPending DeliveryState = "pending"
	// DeliveryDelivering is written before submission. Finding this state after
	// a crash means the prompt may or may not have arrived.
	DeliveryDelivering DeliveryState = "delivering"
	// DeliveryAcknowledged means Herdr confirmed the prompt reached the exact
	// expected agent, pane, terminal, and native session.
	DeliveryAcknowledged DeliveryState = "acknowledged"
	// DeliveryUncertain means the acknowledgement was missing or did not match.
	// It never retries: a duplicate `continue` can duplicate implementation work.
	DeliveryUncertain DeliveryState = "uncertain"
	// DeliveryFailed means submission was rejected outright, which is safe.
	DeliveryFailed DeliveryState = "failed"
)

// RunDelivery records one unattended prompt attempt. The prompt body lives in
// protected state and never appears here; only a bounded summary does.
type RunDelivery struct {
	// ID is the durable delivery identity written before submission.
	ID string `json:"id"`
	// IdempotencyKey binds the attempt to a run, participant, reset boundary,
	// and cycle, so replay after a crash cannot produce a second prompt.
	IdempotencyKey string        `json:"idempotency_key"`
	State          DeliveryState `json:"state"`
	// Summary describes the prompt without quoting it.
	Summary string `json:"summary,omitempty"`
	// Expected identifies who the acknowledgement had to come from.
	Expected AgentBinding `json:"expected,omitzero"`
	// AttemptedAt and SettledAt bracket the attempt.
	AttemptedAt time.Time `json:"attempted_at,omitzero"`
	SettledAt   time.Time `json:"settled_at,omitzero"`
	// Detail explains an uncertain or failed delivery in operator language.
	Detail string `json:"detail,omitempty"`
}

// AgentBinding is the exact identity an unattended action is aimed at. Every
// field is compared before acting: an Overnight Run that prompted "the agent in
// that pane" would eventually prompt a different conversation.
type AgentBinding struct {
	Role          string        `json:"role,omitempty"`
	AgentName     string        `json:"agent_name,omitempty"`
	AgentKind     string        `json:"agent_kind,omitempty"`
	WorkspaceID   string        `json:"workspace_id,omitempty"`
	TabID         string        `json:"tab_id,omitempty"`
	PaneID        string        `json:"pane_id,omitempty"`
	TerminalID    string        `json:"terminal_id,omitempty"`
	NativeSession NativeSession `json:"native_session,omitzero"`
}

// Empty reports whether no binding field was populated.
func (b AgentBinding) Empty() bool {
	return b.Role == "" && b.AgentName == "" && b.WorkspaceID == "" &&
		b.PaneID == "" && b.TerminalID == "" && b.NativeSession.Value == ""
}

// DetectedLimit is one verified usage limit and the reset Claude reported for
// it. The reset is always Claude's own absolute timestamp.
type DetectedLimit struct {
	// Class names which exhaustion this was; only an included-session limit may
	// drive a sleep cycle.
	Class string `json:"class"`
	// DetectedAt is when the limit was observed.
	DetectedAt time.Time `json:"detected_at"`
	// ResetAt is Claude's reported reset, never a computed one.
	ResetAt time.Time `json:"reset_at,omitzero"`
	// AuthMode records the billing posture proved alongside it.
	AuthMode string `json:"auth_mode,omitempty"`
	// Sleepable records whether this limit satisfied every sleep precondition.
	Sleepable bool `json:"sleepable"`
	// Detail explains a limit that was recognized but refused.
	Detail string `json:"detail,omitempty"`
}

// WakeOwnership records the wake candidate this run owns.
//
// The standalone daemon arbitrates multiple Herdr wake candidates, so a run
// must retain exact source/purpose evidence before it can withdraw anything.
type WakeOwnership struct {
	// CandidateID is the stable daemon candidate identity.
	CandidateID string `json:"candidate_id,omitempty"`
	Source      string `json:"source,omitempty"`
	Purpose     string `json:"purpose,omitempty"`
	// RequestedAt is the wake time this run asked for, normally a small lead
	// before the reset.
	RequestedAt time.Time `json:"requested_at,omitzero"`
	// ResetAt is the reset the wake exists to serve.
	ResetAt time.Time `json:"reset_at,omitzero"`
	// Verified is true only when the coordinator independently confirmed the
	// wake is programmed. Sleep requires this, never the registration alone.
	Verified bool `json:"verified"`
	// RegisteredAt and VerifiedAt record when each step succeeded.
	RegisteredAt    time.Time `json:"registered_at,omitzero"`
	ProgrammedAt    time.Time `json:"programmed_at,omitzero"`
	VerifiedAt      time.Time `json:"verified_at,omitzero"`
	ProtocolVersion int       `json:"protocol_version,omitempty"`
	DaemonBuild     string    `json:"daemon_build,omitempty"`
	HelperBuild     string    `json:"helper_build,omitempty"`
	// Canceled records that this run's candidate was withdrawn.
	Canceled bool `json:"canceled"`
	// Uncertain means cancellation could not be confirmed, which must be
	// reported rather than assumed either way.
	Uncertain bool   `json:"uncertain"`
	Detail    string `json:"detail,omitempty"`
}

// PlanProof is the positive confirmation-time evidence that one exact native
// Claude session uses included Claude.ai capacity. It is deliberately distinct
// from fresh five-hour-window/reset evidence, which can become stale later.
type PlanProof struct {
	FormatVersion int       `json:"format_version,omitempty"`
	SessionID     string    `json:"session_id,omitempty"`
	ClaudeVersion string    `json:"claude_version,omitempty"`
	ObservedAt    time.Time `json:"observed_at,omitzero"`
	ExpiresAt     time.Time `json:"expires_at,omitzero"`
	PlanBacked    bool      `json:"plan_backed"`
}

// StayAwakeAssertion records the user-level idle-sleep assertion that belongs
// to one `--stay-awake` run. It is never created by the root wake daemon.
type StayAwakeAssertion struct {
	ID         string    `json:"id,omitempty"`
	AcquiredAt time.Time `json:"acquired_at,omitzero"`
	VerifiedAt time.Time `json:"verified_at,omitzero"`
	ReleasedAt time.Time `json:"released_at,omitzero"`
	Uncertain  bool      `json:"uncertain"`
	Detail     string    `json:"detail,omitempty"`
}

// ValidationSummary is what a participant's own task list required and what was
// last observed about it. It records outcomes, never command output.
type ValidationSummary struct {
	// Commits counts milestone commits observed during the run.
	Commits int `json:"commits"`
	// LastCommitSubject is a bounded summary of the newest one.
	LastCommitSubject string `json:"last_commit_subject,omitempty"`
	// Dirty, Ahead, and Behind are the participant's Git state at last look.
	Dirty  bool `json:"dirty"`
	Ahead  int  `json:"ahead"`
	Behind int  `json:"behind"`
	// ObservedAt is when that was read.
	ObservedAt time.Time `json:"observed_at,omitzero"`
}

// RunParticipant is one enrolled Claude agent and everything needed to act on
// it deterministically much later, on a machine that has since slept.
type RunParticipant struct {
	// ID is stable for the life of the run.
	ID string `json:"id"`
	// Position is the confirmed 1-based queue order the user chose.
	Position int              `json:"position"`
	State    ParticipantState `json:"state"`
	// Feature is the exact worktree this participant works in.
	Feature Feature `json:"feature"`
	// Binding is the exact agent, pane, terminal, and native session approved
	// at confirmation.
	Binding AgentBinding `json:"binding"`
	// PlanProof belongs only to this run's exact approved native session.
	PlanProof PlanProof `json:"plan_proof,omitzero"`
	// Checkpoint is the task-list position last observed.
	Checkpoint TaskCheckpoint `json:"checkpoint,omitzero"`
	// StartingCompleted is how many subtasks were already done when the user
	// confirmed the run. It never changes, which is what makes "what moved"
	// answerable in the morning.
	StartingCompleted int `json:"starting_completed"`
	// Delivery is the most recent unattended prompt attempt.
	Delivery RunDelivery `json:"delivery,omitzero"`
	// Limit is the most recent verified limit for this participant.
	Limit *DetectedLimit `json:"limit,omitempty"`
	// ConsumedResets are the reset boundaries already handled. A later signal
	// must be strictly newer than all of them, which is what stops a stale
	// reset from creating a wake/sleep loop after a restart.
	ConsumedResets []time.Time `json:"consumed_resets,omitempty"`
	// AcknowledgedResumes counts post-reset continuations that were durably
	// acknowledged. Scheduling a wake or waking early never increments it.
	AcknowledgedResumes int `json:"acknowledged_resumes"`
	// LastObservedState and LastObservedSeq are Herdr's authoritative live
	// values at the last observation, used to require a real transition before
	// prompting again.
	LastObservedState string `json:"last_observed_state,omitempty"`
	LastObservedSeq   uint64 `json:"last_observed_seq,omitempty"`
	// Validation is the commit and Git summary for the morning report.
	Validation ValidationSummary `json:"validation,omitzero"`
	// Outcome is why this participant stopped.
	Outcome TerminalReason `json:"outcome,omitempty"`
	// Recovery is the exact command a person should run next.
	Recovery  string    `json:"recovery,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitzero"`
}

// Active reports whether this participant is the one the supervisor may prompt.
func (p RunParticipant) Active() bool { return p.State == ParticipantActive }

// RunEvent is one durable, redacted entry in a run's timeline.
type RunEvent struct {
	At time.Time `json:"at"`
	// Kind is a stable machine-readable event name.
	Kind string `json:"kind"`
	// Participant scopes the event when it belongs to one.
	Participant string `json:"participant,omitempty"`
	// Detail is a bounded, operator-safe sentence. It never carries prompt
	// bodies, terminal content, environment values, or credentials.
	Detail string `json:"detail,omitempty"`
}

// MorningReport is the durable summary a terminal run leaves behind.
type MorningReport struct {
	GeneratedAt time.Time `json:"generated_at"`
	// StartedAt, DeadlineAt, and FinishedAt bracket the run.
	StartedAt  time.Time `json:"started_at,omitzero"`
	DeadlineAt time.Time `json:"deadline_at,omitzero"`
	FinishedAt time.Time `json:"finished_at,omitzero"`
	// Reason is why unattended execution ended.
	Reason TerminalReason `json:"reason,omitempty"`
	// MaxResumes and AcknowledgedResumes are the configured ceiling and what
	// was actually consumed.
	MaxResumes          int `json:"max_resumes"`
	AcknowledgedResumes int `json:"acknowledged_resumes"`
	// Participants summarizes each enrolled agent in confirmed queue order.
	Participants []ReportParticipant `json:"participants,omitempty"`
	// DeclinedCreditOffers counts every paid continuation that was refused.
	DeclinedCreditOffers int `json:"declined_credit_offers"`
	// Uncertainties and NextActions are what a person has to deal with.
	Uncertainties []string `json:"uncertainties,omitempty"`
	NextActions   []string `json:"next_actions,omitempty"`
}

// ReportParticipant is one agent's line in the morning report.
type ReportParticipant struct {
	Feature string `json:"feature"`
	Role    string `json:"role,omitempty"`
	// Position is the confirmed queue order.
	Position int              `json:"position"`
	State    ParticipantState `json:"state"`
	Outcome  TerminalReason   `json:"outcome,omitempty"`
	// SubtasksBefore and SubtasksAfter show what moved.
	SubtasksBefore int `json:"subtasks_before"`
	SubtasksAfter  int `json:"subtasks_after"`
	SubtasksTotal  int `json:"subtasks_total"`
	// ManualCheckpoint names the boundary that stopped unattended work.
	ManualCheckpoint string `json:"manual_checkpoint,omitempty"`
	// Validation is the Git and commit summary at the end.
	Validation ValidationSummary `json:"validation,omitzero"`
	// Recovery is the next safe command for this participant.
	Recovery string `json:"recovery,omitempty"`
}

// OvernightRun is the durable record of one confirmed run.
type OvernightRun struct {
	// Version is the record schema version.
	Version int `json:"version"`
	// ID is immutable and is how every later command refers to this run.
	ID string `json:"id"`
	// RepositoryID scopes the run to one repository.
	RepositoryID string   `json:"repository_id"`
	State        RunState `json:"state"`
	// CreatedAt is when the user confirmed it.
	CreatedAt time.Time `json:"created_at"`
	// StartAt is when supervision may begin, and DeadlineAt is the absolute
	// no-new-prompt, no-new-wake boundary.
	StartAt    time.Time `json:"start_at"`
	DeadlineAt time.Time `json:"deadline_at"`
	// Timezone is the IANA zone the user chose. Absolute times above are
	// authoritative; this is what display and cross-midnight arithmetic use.
	Timezone string `json:"timezone"`
	// MaxResumes is the configured ceiling on acknowledged post-reset
	// continuations, and AcknowledgedResumes what the run has consumed.
	MaxResumes          int `json:"max_resumes"`
	AcknowledgedResumes int `json:"acknowledged_resumes"`
	// Participants are the confirmed queue, in order.
	Participants []RunParticipant `json:"participants"`
	// ActiveParticipant is the participant ID at the queue head, empty before
	// the run starts and after it ends.
	ActiveParticipant string `json:"active_participant,omitempty"`
	// Wake is the wake candidate this run owns, if any.
	Wake WakeOwnership `json:"wake,omitzero"`
	// WakeMode is fixed when the run is confirmed; it is never inferred later
	// from daemon availability or a transient power reading.
	WakeMode  WakeMode           `json:"wake_mode,omitempty"`
	Assertion StayAwakeAssertion `json:"assertion,omitzero"`
	// Confirmation records how the user approved this run, so a run can never
	// exist without evidence somebody agreed to it.
	Confirmation string `json:"confirmation"`
	// TerminalReason is why the run ended.
	TerminalReason TerminalReason `json:"terminal_reason,omitempty"`
	// Uncertainty is preserved rather than resolved when an effect could not be
	// proven either way.
	Uncertainty string `json:"uncertainty,omitempty"`
	// Timeline is the redacted audit trail.
	Timeline []RunEvent `json:"timeline,omitempty"`
	// Report is the morning report, present once the run is terminal.
	Report    *MorningReport `json:"report,omitempty"`
	UpdatedAt time.Time      `json:"updated_at"`
}

// Participant returns one participant by ID.
func (r OvernightRun) Participant(id string) (RunParticipant, bool) {
	for _, participant := range r.Participants {
		if participant.ID == id {
			return participant, true
		}
	}
	return RunParticipant{}, false
}

// Active returns the participant the supervisor may act on.
func (r OvernightRun) Active() (RunParticipant, bool) {
	if r.ActiveParticipant == "" {
		return RunParticipant{}, false
	}
	return r.Participant(r.ActiveParticipant)
}

// NextQueued returns the first queued participant in confirmed order.
func (r OvernightRun) NextQueued() (RunParticipant, bool) {
	for _, participant := range r.Participants {
		if participant.State == ParticipantQueued {
			return participant, true
		}
	}
	return RunParticipant{}, false
}

// RemainingResumes reports how many acknowledged post-reset continuations the
// configured ceiling still allows.
func (r OvernightRun) RemainingResumes() int {
	remaining := r.MaxResumes - r.AcknowledgedResumes
	if remaining < 0 {
		return 0
	}
	return remaining
}

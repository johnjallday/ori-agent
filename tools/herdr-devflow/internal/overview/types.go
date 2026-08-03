// Package overview defines the read-only, feature-first snapshot used by
// `wt status`, its JSON contract, and the Herdr board.
//
// Everything here is diagnostic. A snapshot records what was observed, when it
// was observed, and how confident that observation is. It never repairs drift,
// never writes planning files, Git, GitHub, bridge, or Herdr state, and never
// promotes a saved bridge record to a live observation. Bridge persistence
// types stay in internal/model; this package owns only the read model.
package overview

import (
	"encoding/json"
	"time"
)

// SchemaVersion identifies the JSON contract emitted from this snapshot.
// Consumers must tolerate additive fields within one version.
//
// Version 3 removed every backlog field: the per-feature `backlog` object, the
// `backlog` source, the `backlog_drift` finding code, and the `dropped` phase.
// A removal is not additive, so it gets a version rather than being slipped
// into the existing one — a consumer reading `.features[].backlog` should be
// told the shape changed, not handed a silently missing key.
const SchemaVersion = 3

// Availability separates "absent", "not understood", and "could not look" from
// a real zero value. Renderers and JSON consumers must never collapse these.
type Availability string

const (
	// AvailabilityAvailable means the value was observed and is trustworthy.
	AvailabilityAvailable Availability = "available"
	// AvailabilityAbsent means the source was read and the thing is genuinely
	// not there (no PRD file, no PR, no agent).
	AvailabilityAbsent Availability = "absent"
	// AvailabilityMalformed means the source existed but could not be parsed.
	AvailabilityMalformed Availability = "malformed"
	// AvailabilityUnavailable means the source could not be consulted at all
	// (GitHub unauthenticated, Herdr down, Git command failed).
	AvailabilityUnavailable Availability = "unavailable"
	// AvailabilityStale means a previously successful observation is being
	// reused past its refresh interval and may no longer be true.
	AvailabilityStale Availability = "stale"
	// AvailabilityUnknown means no determination could be made. It is the zero
	// value so an uninitialized field never reads as trustworthy.
	AvailabilityUnknown Availability = ""
)

// OK reports whether a value backed by this availability may be presented as a
// current fact without qualification.
func (a Availability) OK() bool { return a == AvailabilityAvailable }

// Label is the full textual form used in human output. Presentation never
// relies on color alone to convey availability.
func (a Availability) Label() string {
	switch a {
	case AvailabilityAvailable:
		return "available"
	case AvailabilityAbsent:
		return "absent"
	case AvailabilityMalformed:
		return "malformed"
	case AvailabilityUnavailable:
		return "unavailable"
	case AvailabilityStale:
		return "stale"
	default:
		return "unknown"
	}
}

// MarshalJSON emits the unknown zero value as an explicit "unknown" string.
// An empty string in the payload would read to a consumer as a missing field
// rather than as a deliberate "no determination was made".
func (a Availability) MarshalJSON() ([]byte, error) {
	if a == AvailabilityUnknown {
		return []byte(`"unknown"`), nil
	}
	return json.Marshal(string(a))
}

// UnmarshalJSON accepts the explicit "unknown" spelling and the empty string.
func (a *Availability) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw == "unknown" {
		raw = string(AvailabilityUnknown)
	}
	*a = Availability(raw)
	return nil
}

// SourceKind names one evidence producer. Every derived value records which
// kinds it came from so a reader can tell planning intent from observed truth.
type SourceKind string

const (
	SourcePlanning SourceKind = "planning"
	SourceWorktree SourceKind = "worktree"
	SourceGit      SourceKind = "git"
	SourceGitHub   SourceKind = "github"
	SourceBridge   SourceKind = "bridge"
	SourceHerdr    SourceKind = "herdr"
)

// Source records one evidence producer's outcome for a single collection. A
// source that failed still appears, with its availability and a sanitized
// detail, so degraded snapshots stay explainable.
type Source struct {
	Kind SourceKind `json:"kind"`
	// Availability is the outcome of consulting this source.
	Availability Availability `json:"availability"`
	// ObservedAt is when this source was consulted. Zero when never consulted.
	ObservedAt time.Time `json:"observed_at,omitzero"`
	// Detail is a sanitized, operator-safe explanation. It never carries
	// tokens, environment values, raw command output, or prompt text.
	Detail string `json:"detail,omitempty"`
	// Required marks a source whose failure makes the whole snapshot
	// incomplete. A fresh GitHub query is required for a complete snapshot.
	Required bool `json:"required"`
}

// Fresh reports whether this source was observed within window of now.
func (s Source) Fresh(now time.Time, window time.Duration) bool {
	if !s.Availability.OK() || s.ObservedAt.IsZero() {
		return false
	}
	return !s.ObservedAt.Add(window).Before(now)
}

// Phase is the deterministic lifecycle position of one feature. Precedence is
// evidence-strength ordered, not chronological: delivered remote evidence
// outranks stale local planning state.
type Phase string

const (
	// PhaseUnknown means no evidence was strong enough to place the feature.
	PhaseUnknown Phase = "unknown"
	// PhasePlanning means planning artifacts exist but no feature worktree does.
	PhasePlanning Phase = "planning"
	// PhaseReady means a complete plan exists and work may start.
	PhaseReady Phase = "ready"
	// PhaseImplementing means a feature worktree or branch exists locally.
	PhaseImplementing Phase = "implementing"
	// PhaseReview means an exact open or draft PR targets the baseline branch.
	PhaseReview Phase = "review"
	// PhaseMergedCleanup means the PR merged but worktree, branch, or archive
	// evidence is still outstanding.
	PhaseMergedCleanup Phase = "merged_cleanup"
	// PhaseShipped means the pull request merged and no local cleanup remains.
	PhaseShipped Phase = "shipped"
)

// There is deliberately no dropped phase. The only thing that ever reported one
// was a hand-maintained backlog file, and nothing replaced it: an Issue can be
// closed for reasons that have nothing to do with abandoning work, so inferring
// "dropped" from a closed Issue would put words in somebody's mouth. If a
// dropped state is wanted later it needs a source that actually means it.

// Terminal reports whether the phase is history rather than active work.
// Terminal features stay visible but sort last and group separately.
func (p Phase) Terminal() bool { return p == PhaseShipped }

// Label is the full textual phase name for human output.
func (p Phase) Label() string {
	switch p {
	case PhasePlanning:
		return "Planning"
	case PhaseReady:
		return "Ready"
	case PhaseImplementing:
		return "Implementing"
	case PhaseReview:
		return "Review"
	case PhaseMergedCleanup:
		return "Merged (cleanup)"
	case PhaseShipped:
		return "Shipped"
	default:
		return "Unknown"
	}
}

// order ranks phases for deterministic sorting of active work.
func (p Phase) order() int {
	switch p {
	case PhaseMergedCleanup:
		return 0
	case PhaseReview:
		return 1
	case PhaseImplementing:
		return 2
	case PhaseReady:
		return 3
	case PhasePlanning:
		return 4
	case PhaseUnknown:
		return 5
	case PhaseShipped:
		return 6
	default:
		return 7
	}
}

// PhaseState carries a phase together with why it was chosen and whether the
// remote evidence needed to confirm it was actually available.
type PhaseState struct {
	Phase Phase `json:"phase"`
	// Confirmed is false when a required source was unavailable, so the phase
	// is the best local guess rather than a settled fact.
	Confirmed bool `json:"confirmed"`
	// Reason is a short, stable, sanitized explanation of the precedence rule
	// that selected this phase.
	Reason string `json:"reason,omitempty"`
	// Sources lists the evidence kinds that contributed to the decision.
	Sources []SourceKind `json:"sources,omitempty"`
}

// Severity ranks findings. Attention grouping and sorting use this ordering.
type Severity string

const (
	SeverityInfo    Severity = "info"
	SeverityWarning Severity = "warning"
	SeverityError   Severity = "error"
)

func (s Severity) order() int {
	switch s {
	case SeverityError:
		return 0
	case SeverityWarning:
		return 1
	case SeverityInfo:
		return 2
	default:
		return 3
	}
}

// Label is the full textual severity for human output.
func (s Severity) Label() string {
	switch s {
	case SeverityError:
		return "error"
	case SeverityWarning:
		return "warning"
	case SeverityInfo:
		return "info"
	default:
		return "info"
	}
}

// FindingCode is a stable machine-readable drift or gap identifier. Codes are
// part of the JSON contract: rename only with a schema version change.
type FindingCode string

const (
	FindingPRDMissing            FindingCode = "prd_missing"
	FindingTaskListMissing       FindingCode = "task_list_missing"
	FindingPlanMalformed         FindingCode = "plan_malformed"
	FindingNameMismatch          FindingCode = "name_mismatch"
	FindingWorktreeWithoutPlan   FindingCode = "worktree_without_plan"
	FindingArchiveStale          FindingCode = "archive_stale"
	FindingCleanupOutstanding    FindingCode = "cleanup_outstanding"
	FindingBindingPathStale      FindingCode = "binding_path_stale"
	FindingWorktreePathCollision FindingCode = "worktree_path_collision"
	FindingArchiveMissing        FindingCode = "archive_missing"
	FindingBranchBehindBase      FindingCode = "branch_behind_base"
	FindingBaselineStale         FindingCode = "baseline_stale"
	FindingWorktreeDirty         FindingCode = "worktree_dirty"
	FindingIdentityAmbiguous     FindingCode = "identity_ambiguous"
	FindingGitUnavailable        FindingCode = "git_unavailable"
	FindingGitHubUnavailable     FindingCode = "github_unavailable"
	FindingPRAmbiguous           FindingCode = "pr_ambiguous"
	FindingPRUnexpectedBase      FindingCode = "pr_unexpected_base"
	FindingPRClosedUnmerged      FindingCode = "pr_closed_unmerged"
	FindingChecksFailing         FindingCode = "checks_failing"
	FindingAgentMissing          FindingCode = "agent_missing"
	FindingAgentAmbiguous        FindingCode = "agent_ambiguous"
	FindingAgentDrift            FindingCode = "agent_possible_drift"
	FindingAgentUnmanaged        FindingCode = "agent_unmanaged"
	FindingAgentUnscoped         FindingCode = "agent_unscoped"
	FindingNoAgent               FindingCode = "no_agent"
	FindingHandoffIncomplete     FindingCode = "handoff_incomplete"
	FindingScheduleFailed        FindingCode = "schedule_failed"
	FindingMetadataStale         FindingCode = "metadata_stale"
	FindingHerdrUnavailable      FindingCode = "herdr_unavailable"
)

// Finding is one observed gap or drift. Findings are reported, never repaired,
// and never cleared by a later collector.
type Finding struct {
	Code     FindingCode `json:"code"`
	Severity Severity    `json:"severity"`
	// Feature is the slug the finding belongs to; empty for repository-scoped
	// findings such as an unavailable GitHub.
	Feature string `json:"feature,omitempty"`
	// Role scopes the finding to one managed agent role when applicable.
	Role string `json:"role,omitempty"`
	// Message is a sanitized, complete sentence safe for terminals and JSON.
	Message string `json:"message"`
	// Detail optionally explains the comparison field by field, for example
	// which saved identity field diverged from the live one.
	Detail string `json:"detail,omitempty"`
	// Source names the evidence producer that raised the finding.
	Source SourceKind `json:"source,omitempty"`
}

// PlanProgress is the hierarchy-aware view of one task list. Counts are only
// meaningful when Availability is available; a malformed or missing plan must
// not present as 0/0.
type PlanProgress struct {
	Availability Availability `json:"availability"`
	// MilestonesTotal and MilestonesCompleted count `<N>.0` parent tasks.
	MilestonesTotal     int `json:"milestones_total"`
	MilestonesCompleted int `json:"milestones_completed"`
	// SubtasksTotal and SubtasksCompleted count `<N>.<M>` actionable subtasks.
	SubtasksTotal     int `json:"subtasks_total"`
	SubtasksCompleted int `json:"subtasks_completed"`
	// ActiveMilestone is the first incomplete parent containing incomplete
	// subtasks, falling back to the first incomplete parent.
	ActiveMilestone PlanItem `json:"active_milestone,omitzero"`
	// NextActionable is the first incomplete numbered subtask inside the
	// active milestone. It is never a delivery checkpoint claim of progress.
	NextActionable PlanItem `json:"next_actionable,omitzero"`
	// DeliveryCheckpoints are the remaining validation, demo, commit, PR,
	// merge, and `wt done` items, reported separately from implementation.
	DeliveryCheckpoints []PlanItem `json:"delivery_checkpoints,omitempty"`
	// DeliveryCheckpointsRemaining counts every outstanding checkpoint, so a
	// capped list never understates the delivery work left.
	DeliveryCheckpointsRemaining int `json:"delivery_checkpoints_remaining"`
	// ImplementationComplete is true when every non-checkpoint subtask is
	// checked, so remaining work is delivery only.
	ImplementationComplete bool `json:"implementation_complete"`
	// ParseIssue is a sanitized reason the plan could not be fully understood.
	ParseIssue string `json:"parse_issue,omitempty"`
}

// PlanItem is one checklist entry with its ordinal preserved.
type PlanItem struct {
	// Ordinal is the literal numbering, for example "5.1" or "5.0".
	Ordinal string `json:"ordinal,omitempty"`
	// Text is the bounded display text with control characters removed.
	Text string `json:"text,omitempty"`
	// Completed reflects the checkbox state.
	Completed bool `json:"completed"`
	// Checkpoint marks a delivery checkpoint rather than implementation work.
	Checkpoint bool `json:"checkpoint"`
	// Line is the 1-based source line, useful for jumping to the item.
	Line int `json:"line,omitempty"`
}

// Empty reports whether this item was never populated.
func (i PlanItem) Empty() bool { return i.Ordinal == "" && i.Text == "" }

// PlanCopy names which physical copy of a planning artifact was authoritative.
type PlanCopy string

const (
	// PlanCopyNone means no copy was found.
	PlanCopyNone PlanCopy = "none"
	// PlanCopyActive is the copy inside the live feature worktree, which is
	// authoritative whenever that worktree exists.
	PlanCopyActive PlanCopy = "active_worktree"
	// PlanCopyDev is the copy in the dev worktree: planning input before
	// `wt start`, archived history after `wt done`.
	PlanCopyDev PlanCopy = "dev_worktree"
)

// Plan is the planning evidence for one feature.
type Plan struct {
	// Copy names which physical copy supplied the progress below.
	Copy PlanCopy `json:"copy"`
	// PRDPath and TaskListPath are canonical absolute paths, or empty.
	PRDPath      string `json:"prd_path,omitempty"`
	TaskListPath string `json:"task_list_path,omitempty"`
	// PRDAvailability and TaskListAvailability distinguish absent from
	// malformed from unreadable.
	PRDAvailability      Availability `json:"prd_availability"`
	TaskListAvailability Availability `json:"task_list_availability"`
	// Title is the PRD's declared title when one could be read.
	Title string `json:"title,omitempty"`
	// Progress is the hierarchy-aware checklist state.
	Progress PlanProgress `json:"progress"`
	// ObservedAt is when these files were read.
	ObservedAt time.Time `json:"observed_at,omitzero"`
	// TaskListModTime is when the authoritative task list last changed. It is
	// what makes displayed metadata verifiable as current or stale.
	TaskListModTime time.Time `json:"task_list_mod_time,omitzero"`
}

// GitState is the local Git evidence for one feature's checkout and branch.
// Individual facts degrade independently: a failed ahead/behind computation
// must not discard a successfully read branch.
type GitState struct {
	Availability Availability `json:"availability"`
	// WorktreePath is the canonical linked-worktree root, or empty when the
	// feature has no checkout.
	WorktreePath string `json:"worktree_path,omitempty"`
	// Branch is the checked-out branch name without refs/heads/.
	Branch string `json:"branch,omitempty"`
	// HeadSHA is the resolved commit of the branch tip.
	HeadSHA string `json:"head_sha,omitempty"`
	// Dirty reports uncommitted changes; DirtyAvailability qualifies it.
	Dirty             bool         `json:"dirty"`
	DirtyAvailability Availability `json:"dirty_availability"`
	// Ahead and Behind are counted against the local baseline branch.
	Ahead                  int          `json:"ahead"`
	Behind                 int          `json:"behind"`
	DivergenceAvailability Availability `json:"divergence_availability"`
	// BaselineStale reports that local dev may lag origin/dev, which makes the
	// divergence counts above less meaningful.
	BaselineStale bool `json:"baseline_stale"`
	// Detail is a sanitized reason for any degraded field above.
	Detail     string    `json:"detail,omitempty"`
	ObservedAt time.Time `json:"observed_at,omitzero"`
}

// CheckState aggregates a PR's required checks.
type CheckState string

const (
	ChecksNone        CheckState = "none"
	ChecksPassing     CheckState = "passing"
	ChecksPending     CheckState = "pending"
	ChecksFailing     CheckState = "failing"
	ChecksUnavailable CheckState = "unavailable"
)

// Label is the full textual check state for human output.
func (c CheckState) Label() string {
	if c == "" {
		return "unavailable"
	}
	return string(c)
}

// PullRequest is the exact remote delivery evidence for one feature. Only PRs
// whose head branch matches the feature exactly and whose base is the expected
// baseline are treated as this feature's delivery.
type PullRequest struct {
	Number int    `json:"number"`
	URL    string `json:"url,omitempty"`
	Head   string `json:"head,omitempty"`
	Base   string `json:"base,omitempty"`
	Draft  bool   `json:"draft"`
	// State is the raw remote state: open, closed, or merged.
	State  string `json:"state,omitempty"`
	Merged bool   `json:"merged"`
	// Checks aggregates required checks for the head commit.
	Checks CheckState `json:"checks"`
	// UpdatedAt and MergedAt are remote timestamps, zero when absent.
	UpdatedAt time.Time `json:"updated_at,omitzero"`
	MergedAt  time.Time `json:"merged_at,omitzero"`
}

// Remote is the GitHub evidence for one feature, including the ambiguity case
// where more than one exact open PR was found.
type Remote struct {
	Availability Availability `json:"availability"`
	// PullRequest is the single selected delivery PR, when unambiguous.
	PullRequest *PullRequest `json:"pull_request,omitempty"`
	// Candidates preserves every matching PR when selection was ambiguous.
	Candidates []PullRequest `json:"candidates,omitempty"`
	// Detail is a sanitized explanation of an unavailable or ambiguous result.
	Detail     string    `json:"detail,omitempty"`
	ObservedAt time.Time `json:"observed_at,omitzero"`
}

// BindingHealth grades how confidently a saved bridge role maps to a live
// Herdr agent. Classification never rewrites the saved identity.
type BindingHealth string

const (
	BindingExact         BindingHealth = "exact"
	BindingPossibleDrift BindingHealth = "possible_drift"
	BindingAmbiguous     BindingHealth = "ambiguous"
	BindingMissing       BindingHealth = "missing"
	BindingUnavailable   BindingHealth = "unavailable"
)

// Label is the full textual binding health for human output.
func (b BindingHealth) Label() string {
	switch b {
	case BindingExact:
		return "exact"
	case BindingPossibleDrift:
		return "possible drift"
	case BindingAmbiguous:
		return "ambiguous"
	case BindingMissing:
		return "missing"
	default:
		return "unavailable"
	}
}

// Identity is one agent identity as recorded by the bridge or observed live.
// Saved and live identities are always kept in separate fields so a saved
// record can never be presented as a live observation.
type Identity struct {
	Workspace string `json:"workspace,omitempty"`
	Pane      string `json:"pane,omitempty"`
	Terminal  string `json:"terminal,omitempty"`
	// Name is the stable Herdr agent name. It is what `herdr agent list` shows
	// and what an operator recognizes, so it is the label every surface prefers
	// over an opaque session value.
	Name    string `json:"name,omitempty"`
	Session string `json:"session,omitempty"`
	Kind    string `json:"kind,omitempty"`
	Source  string `json:"source,omitempty"`
}

// Empty reports whether no identity field was populated.
func (i Identity) Empty() bool {
	return i.Workspace == "" && i.Pane == "" && i.Terminal == "" &&
		i.Name == "" && i.Session == "" && i.Kind == "" && i.Source == ""
}

// Schedule is one Herdr schedule relevant to a feature or role. Prompt text is
// deliberately excluded; only bounded summaries are carried.
type Schedule struct {
	ID string `json:"id,omitempty"`
	// State is the schedule lifecycle: pending, failed, or delivered.
	State string `json:"state,omitempty"`
	// Summary is a bounded, sanitized description without prompt content.
	Summary string    `json:"summary,omitempty"`
	DueAt   time.Time `json:"due_at,omitzero"`
}

// AgentScope names which kind of checkout an agent's work resolved into. It is
// the difference between "this agent is implementing a feature" and "this agent
// is open in the dev checkout", and only a feature-scoped agent can ever be a
// candidate for unattended control.
type AgentScope string

const (
	// AgentScopeFeature means the agent resolved into a feature worktree, or is
	// a saved bridge role belonging to one.
	AgentScopeFeature AgentScope = "feature"
	// AgentScopeRepository means the agent resolved into a checkout of this
	// repository that implements no feature: the source checkout, or a baseline
	// dev/main worktree. These agents are real work and must stay visible.
	AgentScopeRepository AgentScope = "repository"
	// AgentScopeUnknown is the zero value: no working directory could be
	// resolved, so the agent was not placed in any checkout.
	AgentScopeUnknown AgentScope = ""
)

// Label is the full textual scope for human output.
func (s AgentScope) Label() string {
	switch s {
	case AgentScopeFeature:
		return "feature"
	case AgentScopeRepository:
		return "repository"
	default:
		return "unknown"
	}
}

// MarshalJSON emits the zero value as an explicit "unknown", so a consumer can
// tell "not placed" from a missing field.
func (s AgentScope) MarshalJSON() ([]byte, error) {
	if s == AgentScopeUnknown {
		return []byte(`"unknown"`), nil
	}
	return json.Marshal(string(s))
}

// UnmarshalJSON accepts the explicit "unknown" spelling and the empty string.
func (s *AgentScope) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw == "unknown" {
		raw = string(AgentScopeUnknown)
	}
	*s = AgentScope(raw)
	return nil
}

// Agent is one row of the roster: a managed bridge role, an unmanaged live
// agent discovered in a feature worktree, or an agent working in a checkout
// that implements no feature. None of them is ever adopted automatically.
type Agent struct {
	// Feature is the slug this agent belongs to, empty for a repository-scoped
	// agent. Emptiness is meaningful, so the field is always emitted.
	Feature string `json:"feature"`
	// Scope records which kind of checkout the agent was placed in.
	Scope AgentScope `json:"scope"`
	// Role is the bridge role name; empty for unmanaged agents.
	Role string `json:"role,omitempty"`
	// Managed distinguishes a saved bridge role from a discovered live agent.
	Managed bool `json:"managed"`
	// Kind is the agent executable kind, for example claude or codex.
	Kind string `json:"kind,omitempty"`
	// Saved is the bridge record; SavedAt is when the bridge wrote it.
	Saved   Identity  `json:"saved,omitzero"`
	SavedAt time.Time `json:"saved_at,omitzero"`
	// Live is the identity observed from Herdr this collection.
	Live Identity `json:"live,omitzero"`
	// Status is the observed semantic status. It is Herdr's value, never a
	// value derived from checklist progress.
	Status AgentStatus `json:"status"`
	// StatusAvailability qualifies Status; unavailable during a Herdr outage.
	StatusAvailability Availability `json:"status_availability"`
	// Binding grades the saved-to-live mapping.
	Binding BindingHealth `json:"binding"`
	// BindingDetail explains a possible_drift field by field.
	BindingDetail string `json:"binding_detail,omitempty"`
	// BindingCandidates preserves every plausible row when ambiguous.
	BindingCandidates []Identity `json:"binding_candidates,omitempty"`
	// Schedules are the role's unresolved, failed, or recently delivered
	// one-time schedules.
	Schedules []Schedule `json:"schedules,omitempty"`
	// LastActivityAt is Herdr's authoritative event/activity timestamp. It is
	// zero when Herdr reported none, which is distinct from "no activity".
	LastActivityAt time.Time `json:"last_activity_at,omitzero"`
	// MatchedPath is the canonical checkout this agent's working directory
	// resolved into. It is the evidence for the attribution, and it is empty
	// when the agent could not be placed.
	MatchedPath string `json:"matched_path,omitempty"`
	// Eligibility answers whether an Overnight Run may control this agent, and
	// why not when it may not.
	Eligibility Eligibility `json:"eligibility"`
	// Run is this agent's membership in an Overnight Run. It is absent until a
	// run enrolls the agent, which is distinct from a run that enrolled it and
	// has not started.
	Run *RunMembership `json:"run,omitempty"`
}

// RunMembership is one agent's place in an Overnight Run. Only one participant
// per run is active; every other enrolled participant is queued and must not
// receive an unattended prompt.
type RunMembership struct {
	// RunID is the immutable identity of the run that enrolled this agent.
	RunID string `json:"run_id"`
	// State is the participant's state within the run.
	State string `json:"state,omitempty"`
	// QueuePosition is 1-based; zero means the position is unknown.
	QueuePosition int `json:"queue_position,omitempty"`
	// Active marks the single participant the supervisor may prompt.
	Active bool `json:"active"`
}

// Feature is one row of the feature-first overview: the union of planning,
// worktree, Git, GitHub, bridge, and Herdr evidence for one slug.
type Feature struct {
	// Slug is the exact normalized feature identifier used to join sources.
	Slug string `json:"slug"`
	// Title is the human name, preferring the PRD title.
	Title string `json:"title,omitempty"`
	// Phase is the derived lifecycle position and its confirmation state.
	Phase PhaseState `json:"phase"`
	// Plan, Git, and Remote carry each evidence family.
	Plan   Plan     `json:"plan"`
	Git    GitState `json:"git"`
	Remote Remote   `json:"remote"`
	// Agents are the managed roles and unmanaged live agents for this feature.
	// A feature has zero or more; it is never collapsed to a single agent.
	Agents []Agent `json:"agents,omitempty"`
	// Occupancy counts panes resolving into this feature's worktree, including
	// panes running no agent. A worktree can be occupied without an agent, and
	// that distinction decides whether cleanup is safe.
	Occupancy int `json:"occupancy"`
	// Schedules are feature-level schedules not attributable to one role.
	Schedules []Schedule `json:"schedules,omitempty"`
	// Findings are this feature's gaps and drift, most severe first.
	Findings []Finding `json:"findings,omitempty"`
	// Sources records which evidence kinds contributed to this row.
	Sources []SourceKind `json:"sources,omitempty"`
}

// AgentStatus mirrors the bridge's semantic status values without
// importing bridge persistence types into the read model. The service maps
// model.AgentStatus onto this at the boundary.
type AgentStatus string

const (
	AgentIdle    AgentStatus = "idle"
	AgentWorking AgentStatus = "working"
	AgentBlocked AgentStatus = "blocked"
	AgentDone    AgentStatus = "done"
	AgentUnknown AgentStatus = "unknown"
	AgentMissing AgentStatus = "missing"
)

// Attention returns the highest severity among this feature's findings and
// whether any finding exists at all.
func (f Feature) Attention() (Severity, bool) {
	best := Severity("")
	found := false
	for _, finding := range f.Findings {
		if !found || finding.Severity.order() < best.order() {
			best = finding.Severity
			found = true
		}
	}
	return best, found
}

// Checkout is one working copy of this repository: the source checkout, a
// baseline dev/main checkout, or a feature worktree.
//
// It exists so an agent that implements no feature still has an honest home in
// the snapshot. Before this row existed, the roster was feature-first all the
// way down, and an agent working in `ori-agent-dev` was simply absent — Herdr
// could see it and Ori could not.
type Checkout struct {
	// Path is the canonical checkout root.
	Path string `json:"path"`
	// Branch is the checked-out branch without refs/heads/, empty if detached.
	Branch string `json:"branch,omitempty"`
	// Feature is the exact slug this checkout implements, empty for baseline
	// and source checkouts.
	Feature string `json:"feature,omitempty"`
	// Baseline marks an integration checkout: dev, main, master, or develop.
	Baseline bool `json:"baseline"`
	// Source marks the repository's normal checkout, which owns the .git
	// directory every linked worktree points at.
	Source bool `json:"source"`
	// Detached marks a checkout with no branch.
	Detached bool `json:"detached"`
	// Occupancy counts panes resolving into this checkout, agent-bearing or
	// not: a worktree can be occupied without an agent running in it.
	Occupancy int `json:"occupancy"`
	// Agents counts the live agents observed in this checkout.
	Agents int `json:"agents"`
}

// Repository identifies the checkout the snapshot describes.
type Repository struct {
	// ID is the stable local identity derived from the Git common directory.
	ID string `json:"id"`
	// Root is the canonical source checkout path.
	Root string `json:"root"`
	// GitCommonDir is the shared directory every linked worktree points at.
	GitCommonDir string `json:"git_common_dir,omitempty"`
	// Baseline is the integration branch features target, normally dev.
	Baseline string `json:"baseline"`
	// BaselineStale reports that the local baseline may lag its remote.
	BaselineStale bool `json:"baseline_stale"`
}

// Snapshot is one complete read-only observation of the repository. It is the
// single value every feature surface renders: compact `wt status`, `--json`,
// and the expanded Herdr board.
type Snapshot struct {
	// SchemaVersion is the JSON contract version of this payload.
	SchemaVersion int `json:"schema_version"`
	// GeneratedAt is when collection finished.
	GeneratedAt time.Time `json:"generated_at"`
	// GitHubCheckedAt is when the required remote query last succeeded. Zero
	// means a fresh remote query has never succeeded in this process.
	GitHubCheckedAt time.Time `json:"github_checked_at,omitzero"`
	// Repository identifies the checkout described here.
	Repository Repository `json:"repository"`
	// Complete is true only when every required source was freshly observed.
	// A local-only snapshot is never complete.
	Complete bool `json:"complete"`
	// Stale is true when any presented value is reused past its refresh
	// interval, which happens during watch after a remote failure.
	Stale bool `json:"stale"`
	// Features are the feature rows, sorted by the shared deterministic order.
	Features []Feature `json:"features"`
	// Checkouts are every working copy of this repository, feature and
	// non-feature alike, with their pane occupancy.
	Checkouts []Checkout `json:"checkouts,omitempty"`
	// Agents is the complete agent roster: every managed, unmanaged, and
	// repository-level agent in one deterministic order. Feature rows carry the
	// same values grouped for the feature-first display; this flat list is what
	// a roster, a selector, and an Overnight Run consume, because it is the only
	// view in which an agent cannot be hidden by lacking a feature.
	Agents []Agent `json:"agents,omitempty"`
	// Sources records every evidence producer's outcome for this collection.
	Sources []Source `json:"sources"`
	// Findings are repository-scoped gaps not attributable to one feature.
	Findings []Finding `json:"findings,omitempty"`
}

// Source returns the recorded outcome for one evidence producer.
func (s Snapshot) Source(kind SourceKind) (Source, bool) {
	for _, source := range s.Sources {
		if source.Kind == kind {
			return source, true
		}
	}
	return Source{}, false
}

// Feature returns the row for an exact slug.
func (s Snapshot) Feature(slug string) (Feature, bool) {
	for _, feature := range s.Features {
		if feature.Slug == slug {
			return feature, true
		}
	}
	return Feature{}, false
}

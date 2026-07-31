// Package overnight builds, persists, and inspects Overnight Runs.
//
// A run is a promise to control someone's Claude agents while they are asleep,
// and possibly to put their Mac to sleep. Everything in this package is
// therefore built around one rule: nothing happens that the user did not
// explicitly approve, and nothing is approved that could not be described to
// them first.
//
// This file covers the part before anything is written: resolving the exact
// agents named, ordering them into a queue, working out the absolute start and
// deadline, and collecting every reason the run should not start. It produces a
// Plan — a proposal, not a run — which the caller renders for confirmation.
package overnight

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/config"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/model"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/overview"
)

// Request is what the user asked for, before any of it is resolved.
type Request struct {
	// Selections are the agents to enrol, in the order the user gave them.
	// Each is a feature slug, optionally narrowed to one role as "slug:role".
	Selections []string
	// Start is "now", an HH:MM local time, or empty for the configured default.
	Start string
	// Deadline is an HH:MM local time, or empty for the configured default.
	Deadline string
	// Timezone is an IANA zone, or empty for the configured or machine default.
	Timezone string
	// MaxResumes overrides the configured ceiling when positive.
	MaxResumes int
	// StayAwake keeps the run awake rather than allowing reset sleep cycles.
	StayAwake bool
}

// PlannedParticipant is one resolved agent in the proposed queue.
type PlannedParticipant struct {
	// Position is 1-based and is exactly the order the user typed.
	Position  int
	Feature   model.Feature
	Binding   model.AgentBinding
	PlanProof model.PlanProof
	// Checkpoint is where this feature's plan currently stands.
	Checkpoint model.TaskCheckpoint
	// Working records that the agent is already busy. One working participant
	// is fine if it is the queue head; two is a refusal.
	Working bool
}

// ExcludedAgent is an agent that was visible but cannot be enrolled, kept so
// the confirmation screen can say why rather than silently omitting it.
type ExcludedAgent struct {
	Feature string
	Role    string
	Kind    string
	Scope   string
	Reason  string
}

// Plan is a proposed run. It has been resolved and checked but not persisted,
// and building one changes nothing.
type Plan struct {
	RepositoryID string
	Participants []PlannedParticipant
	Excluded     []ExcludedAgent
	StartAt      time.Time
	DeadlineAt   time.Time
	Timezone     string
	MaxResumes   int
	WakeMode     model.WakeMode
	// Warnings are things the user should know before confirming but that do
	// not prevent the run.
	Warnings []string
	// Conflicts prevent the run entirely. A plan with any conflict must never
	// be created.
	Conflicts []string
}

// Startable reports whether this plan may be confirmed.
func (p Plan) Startable() bool { return len(p.Conflicts) == 0 && len(p.Participants) > 0 }

// BuildPlan resolves a request against the shared snapshot and the bridge's
// saved records.
//
// It reads only: no agent is prompted, no worktree is created, no binding is
// repaired, and no Claude session is contacted.
func BuildPlan(snapshot overview.Snapshot, saved model.BridgeState, request Request, cfg config.OvernightConfig, now time.Time) (Plan, error) {
	plan := Plan{RepositoryID: snapshot.Repository.ID, WakeMode: model.WakeModeSleep}
	if request.StayAwake {
		plan.WakeMode = model.WakeModeStayAwake
	}

	location, err := resolveLocation(request.Timezone, cfg.Timezone)
	if err != nil {
		return Plan{}, err
	}
	plan.Timezone = location.String()

	plan.StartAt, err = resolveStart(request.Start, cfg.StartTime, now, location)
	if err != nil {
		return Plan{}, err
	}
	plan.DeadlineAt, err = resolveDeadline(request.Deadline, cfg.Deadline, plan.StartAt, location)
	if err != nil {
		return Plan{}, err
	}
	plan.MaxResumes, err = resolveMaxResumes(request.MaxResumes, cfg.MaxResumes)
	if err != nil {
		return Plan{}, err
	}

	if len(request.Selections) == 0 {
		return Plan{}, errors.New("an Overnight Run requires at least one explicitly selected Claude agent")
	}
	plan.Excluded = excludedAgents(snapshot, request.Selections)

	claimed := map[string]string{}
	for index, selection := range request.Selections {
		participant, err := resolveSelection(snapshot, selection, index+1)
		if err != nil {
			return Plan{}, err
		}
		// One autonomous agent per worktree. Two agents editing one checkout
		// without coordination is a conflict nothing downstream can untangle.
		if previous, taken := claimed[participant.Feature.Path]; taken {
			return Plan{}, fmt.Errorf("%s and %s are in the same worktree; an Overnight Run controls at most one agent per worktree",
				previous, selection)
		}
		claimed[participant.Feature.Path] = selection
		plan.Participants = append(plan.Participants, participant)
	}

	plan.Conflicts = append(plan.Conflicts, workingConflicts(plan.Participants)...)
	plan.Conflicts = append(plan.Conflicts, scheduleConflicts(plan.Participants, saved)...)
	plan.Conflicts = append(plan.Conflicts, activeRunConflicts(saved, snapshot.Repository.ID)...)
	plan.Warnings = append(plan.Warnings, planWarnings(plan)...)
	return plan, nil
}

// resolveSelection turns one "slug" or "slug:role" into an exact participant.
func resolveSelection(snapshot overview.Snapshot, selection string, position int) (PlannedParticipant, error) {
	slug, role, _ := strings.Cut(strings.TrimSpace(selection), ":")
	if slug == "" {
		return PlannedParticipant{}, fmt.Errorf("%q is not a feature to select", selection)
	}
	feature, ok := snapshot.Feature(slug)
	if !ok {
		return PlannedParticipant{}, fmt.Errorf("no feature named %q was found", slug)
	}

	var candidates []overview.Agent
	for _, agent := range snapshot.Agents {
		if agent.Feature != slug {
			continue
		}
		if role != "" && agent.Role != role {
			continue
		}
		candidates = append(candidates, agent)
	}
	switch len(candidates) {
	case 0:
		if role != "" {
			return PlannedParticipant{}, fmt.Errorf("feature %q has no agent in role %q", slug, role)
		}
		return PlannedParticipant{}, fmt.Errorf("feature %q has no agent to enrol", slug)
	case 1:
	default:
		roles := make([]string, 0, len(candidates))
		for _, candidate := range candidates {
			roles = append(roles, candidate.Role)
		}
		sort.Strings(roles)
		return PlannedParticipant{}, fmt.Errorf("feature %q has several agents (%s); select one as %s:<role>",
			slug, strings.Join(roles, ", "), slug)
	}

	agent := candidates[0]
	if agent.Eligibility.State != overview.EligibilityEligible {
		reason := agent.Eligibility.Reason
		if reason == "" {
			reason = "this agent is not eligible for unattended execution"
		}
		return PlannedParticipant{}, fmt.Errorf("%s cannot be enrolled: %s", selection, reason)
	}

	return PlannedParticipant{
		Position: position,
		Feature: model.Feature{
			RepositoryID: snapshot.Repository.ID,
			Name:         feature.Slug,
			Branch:       feature.Git.Branch,
			Path:         feature.Git.WorktreePath,
		},
		Binding: model.AgentBinding{
			Role:        agent.Role,
			AgentName:   agent.Live.Name,
			AgentKind:   agent.Kind,
			WorkspaceID: agent.Live.Workspace,
			PaneID:      agent.Live.Pane,
			TerminalID:  agent.Live.Terminal,
			NativeSession: model.NativeSession{
				Source: agent.Saved.Source,
				Agent:  agent.Saved.Kind,
				Kind:   "id",
				Value:  agent.Saved.Session,
			},
		},
		Checkpoint: checkpointFrom(feature),
		Working:    agent.Status == overview.AgentWorking,
	}, nil
}

func checkpointFrom(feature overview.Feature) model.TaskCheckpoint {
	progress := feature.Plan.Progress
	checkpoint := model.TaskCheckpoint{
		TaskListPath:           feature.Plan.TaskListPath,
		ModTime:                feature.Plan.TaskListModTime,
		SubtasksTotal:          progress.SubtasksTotal,
		SubtasksCompleted:      progress.SubtasksCompleted,
		NextOrdinal:            progress.NextActionable.Ordinal,
		NextText:               progress.NextActionable.Text,
		ImplementationComplete: progress.ImplementationComplete,
		ObservedAt:             feature.Plan.ObservedAt,
	}
	for _, item := range progress.DeliveryCheckpoints {
		if item.Completed {
			continue
		}
		checkpoint.ManualOrdinal = item.Ordinal
		checkpoint.ManualText = item.Text
		break
	}
	return checkpoint
}

// excludedAgents lists the agents the user can see but did not, or could not,
// select. The confirmation screen shows them so an absent agent is never a
// silent omission.
func excludedAgents(snapshot overview.Snapshot, selections []string) []ExcludedAgent {
	selected := map[string]struct{}{}
	for _, selection := range selections {
		slug, _, _ := strings.Cut(strings.TrimSpace(selection), ":")
		selected[slug] = struct{}{}
	}
	var excluded []ExcludedAgent
	for _, agent := range snapshot.Agents {
		if _, chosen := selected[agent.Feature]; chosen && agent.Eligibility.State == overview.EligibilityEligible {
			continue
		}
		reason := agent.Eligibility.Reason
		if reason == "" && agent.Eligibility.State == overview.EligibilityEligible {
			reason = "eligible, but not selected"
		}
		excluded = append(excluded, ExcludedAgent{
			Feature: agent.Feature,
			Role:    agent.Role,
			Kind:    agent.Kind,
			Scope:   string(agent.Scope),
			Reason:  reason,
		})
	}
	return excluded
}

// workingConflicts enforces the one-working-agent rule. A queue whose head is
// already working is normal — it is the agent the user just left running. Two
// working participants means the supervisor cannot tell which one it is meant
// to be watching.
func workingConflicts(participants []PlannedParticipant) []string {
	var working []string
	for _, participant := range participants {
		if participant.Working {
			working = append(working, participant.Feature.Name)
		}
	}
	if len(working) <= 1 {
		return nil
	}
	return []string{fmt.Sprintf(
		"%d selected agents are already working (%s); leave only the queue head working before starting",
		len(working), strings.Join(working, ", "))}
}

// scheduleConflicts refuses a participant that already has an unresolved
// one-time continuation. Two plans aiming a prompt at one session is exactly
// the duplicate this feature must never produce.
func scheduleConflicts(participants []PlannedParticipant, saved model.BridgeState) []string {
	var conflicts []string
	for _, participant := range participants {
		for _, feature := range saved.Features {
			if feature.Feature.Name != participant.Feature.Name {
				continue
			}
			for _, schedule := range feature.Schedules {
				if !schedule.State.IsUnresolved() {
					continue
				}
				if schedule.Role != "" && participant.Binding.Role != "" && schedule.Role != participant.Binding.Role {
					continue
				}
				conflicts = append(conflicts, fmt.Sprintf(
					"%s has an unresolved continuation (%s) due %s; deliver or cancel it first with wt herd schedule cancel %s",
					participant.Feature.Name, schedule.ID, schedule.DueAt.Format(time.RFC3339), schedule.ID))
			}
		}
	}
	return conflicts
}

// activeRunConflicts refuses a second run in one repository. Two supervisors
// would each believe they owned the queue and the system wake.
func activeRunConflicts(saved model.BridgeState, repositoryID string) []string {
	var conflicts []string
	ids := make([]string, 0, len(saved.Runs))
	for id := range saved.Runs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		run := saved.Runs[id]
		if run.RepositoryID != repositoryID || run.State.Terminal() {
			continue
		}
		conflicts = append(conflicts, fmt.Sprintf(
			"Overnight Run %s is still active (%s); cancel it first with wt herd overnight cancel %s",
			run.ID, run.State.Label(), run.ID))
	}
	return conflicts
}

// planWarnings collects what the user should see before confirming.
func planWarnings(plan Plan) []string {
	var warnings []string
	for _, participant := range plan.Participants {
		switch {
		case participant.Checkpoint.TaskListPath == "":
			warnings = append(warnings, participant.Feature.Name+" has no readable task list, so it has no next safe task")
		case participant.Checkpoint.ImplementationComplete:
			warnings = append(warnings, participant.Feature.Name+" has no implementation work left; only delivery checkpoints remain")
		case participant.Checkpoint.NextOrdinal == "":
			warnings = append(warnings, participant.Feature.Name+" has no next implementation subtask to continue")
		}
		if participant.Checkpoint.ManualOrdinal != "" &&
			participant.Checkpoint.NextOrdinal == "" {
			warnings = append(warnings, participant.Feature.Name+" stops immediately at "+participant.Checkpoint.ManualOrdinal)
		}
	}
	if window := plan.DeadlineAt.Sub(plan.StartAt); window < time.Hour {
		warnings = append(warnings, fmt.Sprintf("the run window is only %s long", window.Round(time.Minute)))
	}
	return warnings
}

// resolveLocation picks the IANA zone every displayed time is interpreted in.
func resolveLocation(requested, configured string) (*time.Location, error) {
	for _, candidate := range []string{requested, configured} {
		if strings.TrimSpace(candidate) == "" {
			continue
		}
		location, err := time.LoadLocation(candidate)
		if err != nil {
			return nil, fmt.Errorf("%q is not an IANA time zone such as America/New_York", candidate)
		}
		return location, nil
	}
	return time.Local, nil
}

// resolveStart turns "now" or HH:MM into an absolute instant.
func resolveStart(requested, configured string, now time.Time, location *time.Location) (time.Time, error) {
	raw := strings.TrimSpace(requested)
	if raw == "" {
		raw = strings.TrimSpace(configured)
	}
	if raw == "" || strings.EqualFold(raw, "now") {
		return now.UTC(), nil
	}
	clock, err := config.ParseClockTime(raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("start time %q must be \"now\" or a 24-hour time such as 23:00", raw)
	}
	return nextOccurrence(clock, now, location, false), nil
}

// resolveDeadline turns HH:MM into the first occurrence strictly after the
// start. That is what makes a 23:00 start with an 07:00 deadline mean the next
// morning rather than sixteen hours ago.
func resolveDeadline(requested, configured string, startAt time.Time, location *time.Location) (time.Time, error) {
	raw := strings.TrimSpace(requested)
	if raw == "" {
		raw = strings.TrimSpace(configured)
	}
	clock, err := config.ParseClockTime(raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("deadline %q must be a 24-hour time such as 07:00", raw)
	}
	deadline := nextOccurrence(clock, startAt, location, true)
	if !deadline.After(startAt) {
		return time.Time{}, fmt.Errorf("deadline %q is not after the start time", raw)
	}
	return deadline, nil
}

// nextOccurrence finds the next wall-clock time in location at or after from.
//
// It builds the instant from the local calendar date rather than adding hours,
// so a run scheduled across a daylight-saving change still lands on the time
// the user typed.
func nextOccurrence(clock config.ClockTime, from time.Time, location *time.Location, strict bool) time.Time {
	local := from.In(location)
	candidate := time.Date(local.Year(), local.Month(), local.Day(), clock.Hour, clock.Minute, 0, 0, location)
	if candidate.Before(local) || (strict && !candidate.After(local)) {
		next := local.AddDate(0, 0, 1)
		candidate = time.Date(next.Year(), next.Month(), next.Day(), clock.Hour, clock.Minute, 0, 0, location)
	}
	return candidate.UTC()
}

// resolveMaxResumes bounds the reset-resume ceiling.
func resolveMaxResumes(requested, configured int) (int, error) {
	value := requested
	if value == 0 {
		value = configured
	}
	if value == 0 {
		value = config.DefaultMaxResumes
	}
	if value < 1 || value > config.MaxAllowedResumes {
		return 0, fmt.Errorf("maximum resumes must be between 1 and %d", config.MaxAllowedResumes)
	}
	return value, nil
}

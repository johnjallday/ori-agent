package overnight

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/config"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/model"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/overview"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/state"
)

// Every test here runs against fixed values: a fixed clock, a fixed snapshot,
// and a temporary state directory. Nothing contacts Herdr or Claude, nothing
// prompts an agent, and nothing schedules a wake — this whole group is about
// what happens strictly before any of that is allowed to.

var (
	// evening is a fixed 22:00 in New York, so cross-midnight arithmetic has a
	// stable answer.
	newYork, _ = time.LoadLocation("America/New_York")
	evening    = time.Date(2026, 7, 29, 22, 0, 0, 0, newYork).UTC()
)

func defaults() config.OvernightConfig { return config.Default().Overnight }

// snapshotWith builds a snapshot carrying the given agents and their features.
func snapshotWith(agents ...overview.Agent) overview.Snapshot {
	snapshot := overview.Snapshot{
		SchemaVersion: overview.SchemaVersion,
		Repository:    overview.Repository{ID: "repo-1", Baseline: "dev"},
		Agents:        agents,
	}
	seen := map[string]bool{}
	for _, agent := range agents {
		if agent.Feature == "" || seen[agent.Feature] {
			continue
		}
		seen[agent.Feature] = true
		snapshot.Features = append(snapshot.Features, overview.Feature{
			Slug: agent.Feature,
			Git: overview.GitState{
				Availability: overview.AvailabilityAvailable,
				WorktreePath: "/w/" + agent.Feature,
				Branch:       "feature/" + agent.Feature,
			},
			Plan: overview.Plan{
				Copy:                 overview.PlanCopyActive,
				TaskListPath:         "/w/" + agent.Feature + "/tasks/tasks-" + agent.Feature + ".md",
				TaskListAvailability: overview.AvailabilityAvailable,
				Progress: overview.PlanProgress{
					Availability:      overview.AvailabilityAvailable,
					SubtasksTotal:     10,
					SubtasksCompleted: 4,
					NextActionable:    overview.PlanItem{Ordinal: "2.3", Text: "Continue implementation"},
					DeliveryCheckpoints: []overview.PlanItem{
						{Ordinal: "2.9", Text: "Demo: drive the new surface", Checkpoint: true},
					},
					DeliveryCheckpointsRemaining: 1,
				},
			},
		})
	}
	return snapshot
}

// eligible is a Claude agent the adapter has approved.
func eligible(feature, role string, mutate ...func(*overview.Agent)) overview.Agent {
	agent := overview.Agent{
		Feature: feature,
		Scope:   overview.AgentScopeFeature,
		Role:    role,
		Managed: true,
		Kind:    "claude",
		Saved: overview.Identity{
			Workspace: "w-" + feature, Pane: "w-" + feature + ":p1", Terminal: "t-" + feature,
			Name: "ori-" + feature + "-" + role, Session: "sess-" + feature, Kind: "claude", Source: "herdr:claude",
		},
		Live: overview.Identity{
			Workspace: "w-" + feature, Pane: "w-" + feature + ":p1", Terminal: "t-" + feature,
			Name: "ori-" + feature + "-" + role, Session: "sess-" + feature, Kind: "claude",
		},
		Status:             overview.AgentIdle,
		StatusAvailability: overview.AvailabilityAvailable,
		Binding:            overview.BindingExact,
		MatchedPath:        "/w/" + feature,
		Eligibility:        overview.Eligibility{State: overview.EligibilityEligible},
	}
	for _, apply := range mutate {
		apply(&agent)
	}
	return agent
}

func TestBuildPlanPreservesTheOrderTheUserTyped(t *testing.T) {
	snapshot := snapshotWith(eligible("alpha", "builder"), eligible("beta", "builder"), eligible("gamma", "builder"))
	plan, err := BuildPlan(snapshot, model.NewBridgeState(),
		Request{Selections: []string{"gamma", "alpha", "beta"}}, defaults(), evening)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	want := []string{"gamma", "alpha", "beta"}
	if len(plan.Participants) != len(want) {
		t.Fatalf("participants = %d, want %d", len(plan.Participants), len(want))
	}
	for index, name := range want {
		if plan.Participants[index].Feature.Name != name || plan.Participants[index].Position != index+1 {
			t.Fatalf("participant %d = %+v, want %q at position %d", index, plan.Participants[index], name, index+1)
		}
	}
	if !plan.Startable() {
		t.Fatalf("plan is not startable: %v", plan.Conflicts)
	}
}

func TestBuildPlanSnapshotsTheExactBindingAndCheckpoint(t *testing.T) {
	snapshot := snapshotWith(eligible("alpha", "builder"))
	plan, err := BuildPlan(snapshot, model.NewBridgeState(), Request{Selections: []string{"alpha"}}, defaults(), evening)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	participant := plan.Participants[0]
	if participant.Binding.NativeSession.Value != "sess-alpha" {
		t.Fatalf("binding = %+v, want the exact saved native session", participant.Binding)
	}
	if participant.Binding.PaneID != "w-alpha:p1" || participant.Binding.Role != "builder" {
		t.Fatalf("binding = %+v, want the approved pane and role", participant.Binding)
	}
	if participant.Feature.Path != "/w/alpha" || participant.Feature.Branch != "feature/alpha" {
		t.Fatalf("feature = %+v, want the canonical worktree", participant.Feature)
	}
	if participant.Checkpoint.NextOrdinal != "2.3" || participant.Checkpoint.ManualOrdinal != "2.9" {
		t.Fatalf("checkpoint = %+v, want the next safe task and the first manual stop", participant.Checkpoint)
	}
}

func TestBuildPlanRefusesAnythingItCannotControlSafely(t *testing.T) {
	cases := []struct {
		name       string
		agents     []overview.Agent
		selections []string
		contains   string
	}{
		{
			name: "an agent the Claude adapter refused",
			agents: []overview.Agent{eligible("alpha", "builder", func(a *overview.Agent) {
				a.Eligibility = overview.Eligibility{State: overview.EligibilityIneligible, Reason: "no usage record exists"}
			})},
			selections: []string{"alpha"},
			contains:   "no usage record exists",
		},
		{
			name: "an agent whose readiness was never checked",
			agents: []overview.Agent{eligible("alpha", "builder", func(a *overview.Agent) {
				a.Eligibility = overview.Eligibility{State: overview.EligibilityUnverified, Reason: "readiness has not been checked"}
			})},
			selections: []string{"alpha"},
			contains:   "readiness has not been checked",
		},
		{
			name:       "a feature that does not exist",
			agents:     []overview.Agent{eligible("alpha", "builder")},
			selections: []string{"missing"},
			contains:   "no feature named",
		},
		{
			name:       "a feature with several agents and no role given",
			agents:     []overview.Agent{eligible("alpha", "builder"), eligible("alpha", "reviewer")},
			selections: []string{"alpha"},
			contains:   "select one as alpha:<role>",
		},
		{
			name:       "no selection at all",
			agents:     []overview.Agent{eligible("alpha", "builder")},
			selections: nil,
			contains:   "at least one explicitly selected",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := BuildPlan(snapshotWith(testCase.agents...), model.NewBridgeState(),
				Request{Selections: testCase.selections}, defaults(), evening)
			if err == nil {
				t.Fatal("an unsafe selection was accepted")
			}
			if !strings.Contains(err.Error(), testCase.contains) {
				t.Fatalf("error = %v, want it to mention %q", err, testCase.contains)
			}
		})
	}
}

// TestBuildPlanRefusesTwoAgentsInOneWorktree guards the rule that stops two
// autonomous agents editing one checkout.
func TestBuildPlanRefusesTwoAgentsInOneWorktree(t *testing.T) {
	snapshot := snapshotWith(eligible("alpha", "builder"), eligible("alpha", "reviewer"))
	_, err := BuildPlan(snapshot, model.NewBridgeState(),
		Request{Selections: []string{"alpha:builder", "alpha:reviewer"}}, defaults(), evening)
	if err == nil || !strings.Contains(err.Error(), "same worktree") {
		t.Fatalf("error = %v, want a refusal naming the shared worktree", err)
	}
}

// TestBuildPlanRefusesASecondWorkingParticipant is the queue-head rule: one
// agent already working is the one you left running; two means the supervisor
// cannot tell which it is watching.
func TestBuildPlanRefusesASecondWorkingParticipant(t *testing.T) {
	working := func(a *overview.Agent) { a.Status = overview.AgentWorking }
	snapshot := snapshotWith(eligible("alpha", "builder", working), eligible("beta", "builder", working))
	plan, err := BuildPlan(snapshot, model.NewBridgeState(),
		Request{Selections: []string{"alpha", "beta"}}, defaults(), evening)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if plan.Startable() {
		t.Fatal("a plan with two working agents was startable")
	}
	if !strings.Contains(strings.Join(plan.Conflicts, " "), "already working") {
		t.Fatalf("conflicts = %v, want the working agents named", plan.Conflicts)
	}

	// One working agent at the head is fine.
	single := snapshotWith(eligible("alpha", "builder", working), eligible("beta", "builder"))
	plan, err = BuildPlan(single, model.NewBridgeState(),
		Request{Selections: []string{"alpha", "beta"}}, defaults(), evening)
	if err != nil || !plan.Startable() {
		t.Fatalf("a single working queue head was refused: %v %v", err, plan.Conflicts)
	}
}

// TestBuildPlanRefusesAParticipantWithAnUnresolvedContinuation stops two plans
// aiming a prompt at one session.
func TestBuildPlanRefusesAParticipantWithAnUnresolvedContinuation(t *testing.T) {
	saved := model.NewBridgeState()
	saved.Features["repo-1:alpha"] = model.FeatureState{
		Feature: model.Feature{RepositoryID: "repo-1", Name: "alpha"},
		Schedules: map[string]model.Schedule{
			"sch-1": {ID: "sch-1", Role: "builder", State: model.SchedulePending, DueAt: evening.Add(time.Hour)},
		},
	}
	plan, err := BuildPlan(snapshotWith(eligible("alpha", "builder")), saved,
		Request{Selections: []string{"alpha"}}, defaults(), evening)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if plan.Startable() {
		t.Fatal("a participant with an unresolved continuation was startable")
	}
	if !strings.Contains(strings.Join(plan.Conflicts, " "), "unresolved continuation") {
		t.Fatalf("conflicts = %v", plan.Conflicts)
	}
}

func TestBuildPlanRefusesASecondActiveRun(t *testing.T) {
	saved := model.NewBridgeState()
	saved.Runs["ovr-1"] = model.OvernightRun{ID: "ovr-1", RepositoryID: "repo-1", State: model.RunRunning}
	plan, err := BuildPlan(snapshotWith(eligible("alpha", "builder")), saved,
		Request{Selections: []string{"alpha"}}, defaults(), evening)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if plan.Startable() || !strings.Contains(strings.Join(plan.Conflicts, " "), "still active") {
		t.Fatalf("conflicts = %v, want the active run named", plan.Conflicts)
	}

	// A terminal run is history and blocks nothing.
	saved.Runs["ovr-1"] = model.OvernightRun{ID: "ovr-1", RepositoryID: "repo-1", State: model.RunCompleted}
	plan, err = BuildPlan(snapshotWith(eligible("alpha", "builder")), saved,
		Request{Selections: []string{"alpha"}}, defaults(), evening)
	if err != nil || !plan.Startable() {
		t.Fatalf("a finished run blocked a new one: %v %v", err, plan.Conflicts)
	}
}

// TestPlanTimesCrossMidnightInTheChosenZone is the deadline arithmetic the
// whole run depends on.
func TestPlanTimesCrossMidnightInTheChosenZone(t *testing.T) {
	plan, err := BuildPlan(snapshotWith(eligible("alpha", "builder")), model.NewBridgeState(), Request{
		Selections: []string{"alpha"}, Start: "23:00", Deadline: "07:00", Timezone: "America/New_York",
	}, defaults(), evening)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	start := plan.StartAt.In(newYork)
	deadline := plan.DeadlineAt.In(newYork)
	if start.Hour() != 23 || start.Day() != 29 {
		t.Fatalf("start = %v, want 23:00 on the 29th", start)
	}
	if deadline.Hour() != 7 || deadline.Day() != 30 {
		t.Fatalf("deadline = %v, want 07:00 the next morning", deadline)
	}
	if !plan.DeadlineAt.After(plan.StartAt) {
		t.Fatalf("deadline %v is not after start %v", plan.DeadlineAt, plan.StartAt)
	}
}

func TestPlanTimesAcceptAnImmediateStart(t *testing.T) {
	plan, err := BuildPlan(snapshotWith(eligible("alpha", "builder")), model.NewBridgeState(), Request{
		Selections: []string{"alpha"}, Start: "now", Deadline: "07:00", Timezone: "America/New_York",
	}, defaults(), evening)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if !plan.StartAt.Equal(evening.UTC()) {
		t.Fatalf("start = %v, want the current instant", plan.StartAt)
	}
}

func TestPlanTimesRejectUnusableValues(t *testing.T) {
	cases := []struct {
		name     string
		request  Request
		contains string
	}{
		{"a start that is not a time", Request{Selections: []string{"alpha"}, Start: "tonight"}, "start time"},
		{"a deadline that is not a time", Request{Selections: []string{"alpha"}, Deadline: "morning"}, "deadline"},
		{"an unknown time zone", Request{Selections: []string{"alpha"}, Timezone: "Mars/Olympus"}, "IANA time zone"},
		{"a resume ceiling beyond the bound", Request{Selections: []string{"alpha"}, MaxResumes: 99}, "maximum resumes"},
		{"a negative resume ceiling", Request{Selections: []string{"alpha"}, MaxResumes: -1}, "maximum resumes"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := BuildPlan(snapshotWith(eligible("alpha", "builder")), model.NewBridgeState(),
				testCase.request, defaults(), evening)
			if err == nil || !strings.Contains(err.Error(), testCase.contains) {
				t.Fatalf("error = %v, want it to mention %q", err, testCase.contains)
			}
		})
	}
}

// TestExcludedAgentsExplainThemselves keeps an agent that was left out visible
// with its reason, rather than silently missing from the summary.
func TestExcludedAgentsExplainThemselves(t *testing.T) {
	codex := eligible("beta", "builder", func(a *overview.Agent) {
		a.Kind, a.Live.Kind = "codex", "codex"
		a.Eligibility = overview.Eligibility{State: overview.EligibilityIneligible, Reason: "Overnight Runs control Claude agents only."}
	})
	plan, err := BuildPlan(snapshotWith(eligible("alpha", "builder"), codex), model.NewBridgeState(),
		Request{Selections: []string{"alpha"}}, defaults(), evening)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if len(plan.Excluded) != 1 || plan.Excluded[0].Feature != "beta" {
		t.Fatalf("excluded = %+v, want the Codex agent listed", plan.Excluded)
	}
	if !strings.Contains(plan.Excluded[0].Reason, "Claude agents only") {
		t.Fatalf("excluded reason = %q", plan.Excluded[0].Reason)
	}
}

// TestConfirmationStatesEveryConsequence is the screen the user reads at
// midnight. Each of these lines is a promise the run has to keep.
func TestConfirmationStatesEveryConsequence(t *testing.T) {
	snapshot := snapshotWith(eligible("alpha", "builder"), eligible("beta", "builder"))
	plan, err := BuildPlan(snapshot, model.NewBridgeState(), Request{
		Selections: []string{"alpha", "beta"}, Start: "23:00", Deadline: "07:00", Timezone: "America/New_York",
	}, defaults(), evening)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	var out strings.Builder
	if err := RenderConfirmation(&out, plan); err != nil {
		t.Fatalf("RenderConfirmation: %v", err)
	}
	rendered := out.String()
	for _, want := range []string{
		"[1] alpha", "[2] beta",
		"sess-alpha",
		"2.3 Continue implementation",
		"2.9 Demo",
		"one agent at a time",
		"07:00",
		"America/New_York",
		"3 acknowledged post-reset continuations",
		"never a calculated five hours",
		"never accepts or spends usage or API credits",
		"Stops before:",
		"put THIS MAC to sleep",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("the confirmation did not state %q:\n%s", want, rendered)
		}
	}
}

func newService(t *testing.T) *Service {
	t.Helper()
	return &Service{
		Store: state.New(t.TempDir()),
		Now:   func() time.Time { return evening },
		NewID: func() string { return "ovr-test-1" },
	}
}

// TestCreateRequiresAnExplicitConfirmation is the gate: no confirmation, no run.
func TestCreateRequiresAnExplicitConfirmation(t *testing.T) {
	service := newService(t)
	plan, err := BuildPlan(snapshotWith(eligible("alpha", "builder")), model.NewBridgeState(),
		Request{Selections: []string{"alpha"}}, defaults(), evening)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if _, err := service.Create(context.Background(), plan, ""); err == nil {
		t.Fatal("a run was created without a confirmation")
	}
	runs, err := service.List("")
	if err != nil || len(runs) != 0 {
		t.Fatalf("runs = %v, %v; want nothing persisted", runs, err)
	}
}

func TestCreatePersistsAScheduledRunThatHasPromptedNothing(t *testing.T) {
	service := newService(t)
	plan, err := BuildPlan(snapshotWith(eligible("alpha", "builder"), eligible("beta", "builder")),
		model.NewBridgeState(), Request{Selections: []string{"alpha", "beta"}}, defaults(), evening)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	run, err := service.Create(context.Background(), plan, ConfirmationInteractive)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if run.State != model.RunScheduled {
		t.Fatalf("state = %q, want scheduled: creation prompts nothing", run.State)
	}
	if run.ActiveParticipant != "" {
		t.Fatalf("active participant = %q, want none before the run starts", run.ActiveParticipant)
	}
	for _, participant := range run.Participants {
		if participant.State != model.ParticipantQueued {
			t.Fatalf("participant %s = %q, want queued", participant.ID, participant.State)
		}
		if participant.Delivery.State != "" {
			t.Fatalf("participant %s already has a delivery: %+v", participant.ID, participant.Delivery)
		}
	}
	if run.Wake.CandidateID != "" {
		t.Fatalf("a wake was registered at creation: %+v", run.Wake)
	}
	if run.Confirmation != ConfirmationInteractive {
		t.Fatalf("confirmation = %q", run.Confirmation)
	}

	// It survives a reload, which is the only thing that matters later.
	reloaded, err := service.Get(run.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(reloaded.Participants) != 2 || reloaded.Participants[1].Feature.Name != "beta" {
		t.Fatalf("reloaded = %+v, want the confirmed queue in order", reloaded.Participants)
	}
}

func TestCreateRefusesASecondActiveRunEvenIfThePlanWasStale(t *testing.T) {
	service := newService(t)
	plan, err := BuildPlan(snapshotWith(eligible("alpha", "builder")), model.NewBridgeState(),
		Request{Selections: []string{"alpha"}}, defaults(), evening)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if _, err := service.Create(context.Background(), plan, ConfirmationFlag); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	// The plan was built before the first run existed, exactly as a second
	// terminal would have it.
	service.NewID = func() string { return "ovr-test-2" }
	if _, err := service.Create(context.Background(), plan, ConfirmationFlag); err == nil {
		t.Fatal("a second active run was created")
	}
}

func TestCancelStopsTheRunWithoutTouchingAgents(t *testing.T) {
	service := newService(t)
	plan, err := BuildPlan(snapshotWith(eligible("alpha", "builder")), model.NewBridgeState(),
		Request{Selections: []string{"alpha"}}, defaults(), evening)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	created, err := service.Create(context.Background(), plan, ConfirmationFlag)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	canceled, err := service.Cancel(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if canceled.State != model.RunCanceled || canceled.TerminalReason != model.ReasonCanceled {
		t.Fatalf("canceled run = %+v", canceled)
	}
	if canceled.Participants[0].State != model.ParticipantCanceled {
		t.Fatalf("participant = %q, want canceled", canceled.Participants[0].State)
	}
	// The feature record, worktree, and agent binding are untouched: cancel
	// stops future prompts, it does not clean anything up.
	if canceled.Participants[0].Binding.NativeSession.Value != "sess-alpha" {
		t.Fatalf("cancel altered a binding: %+v", canceled.Participants[0].Binding)
	}
}

// TestCancelPreservesUncertaintyAboutAnInFlightPrompt is the honesty rule: a
// prompt that may already have arrived is never recorded as not delivered.
func TestCancelPreservesUncertaintyAboutAnInFlightPrompt(t *testing.T) {
	service := newService(t)
	plan, err := BuildPlan(snapshotWith(eligible("alpha", "builder")), model.NewBridgeState(),
		Request{Selections: []string{"alpha"}}, defaults(), evening)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	created, err := service.Create(context.Background(), plan, ConfirmationFlag)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	saved, err := service.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	run := saved.Runs[created.ID]
	run.State = model.RunRunning
	run.Participants[0].State = model.ParticipantActive
	run.Participants[0].Delivery = model.RunDelivery{ID: "d1", State: model.DeliveryDelivering}
	run.Wake = model.WakeOwnership{CandidateID: "cand-1", RequestedAt: evening.Add(time.Hour)}
	saved.Runs[created.ID] = run
	if err := service.Store.Save(saved); err != nil {
		t.Fatal(err)
	}

	canceled, err := service.Cancel(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if canceled.State != model.RunUncertain {
		t.Fatalf("state = %q, want uncertain while a prompt was in flight", canceled.State)
	}
	if canceled.Participants[0].State != model.ParticipantUncertain {
		t.Fatalf("participant = %q, want uncertain", canceled.Participants[0].State)
	}
	if canceled.Uncertainty == "" || canceled.Participants[0].Recovery == "" {
		t.Fatalf("cancellation lost its uncertainty record: %+v", canceled)
	}
	if canceled.Wake.Canceled {
		t.Fatal("cancel claimed a wake candidate was withdrawn without withdrawing it")
	}
	if !canceled.Wake.Uncertain {
		t.Fatalf("wake = %+v, want the unwithdrawn candidate flagged", canceled.Wake)
	}
}

func TestCancelIsIdempotentAndFindsNothingTwice(t *testing.T) {
	service := newService(t)
	if _, err := service.Cancel(context.Background(), "ovr-missing"); err == nil {
		t.Fatal("cancelling an unknown run succeeded")
	}
	plan, _ := BuildPlan(snapshotWith(eligible("alpha", "builder")), model.NewBridgeState(),
		Request{Selections: []string{"alpha"}}, defaults(), evening)
	created, err := service.Create(context.Background(), plan, ConfirmationFlag)
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.Cancel(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Cancel(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("cancelling twice failed: %v", err)
	}
	if second.State != first.State || len(second.Timeline) != len(first.Timeline) {
		t.Fatalf("a second cancel changed the run: %+v", second)
	}
}

// TestDeadlinesLandOnTheWallClockAcrossADaylightSavingChange is why the
// deadline is built from a calendar date rather than by adding hours: on the
// night the clocks change, "07:00" still means seven in the morning.
func TestDeadlinesLandOnTheWallClockAcrossADaylightSavingChange(t *testing.T) {
	// 2026-11-01 is when US clocks go back; the night is 25 hours long.
	fallBack := time.Date(2026, 11, 1, 0, 30, 0, 0, newYork).UTC()
	plan, err := BuildPlan(snapshotWith(eligible("alpha", "builder")), model.NewBridgeState(), Request{
		Selections: []string{"alpha"}, Start: "00:30", Deadline: "07:00", Timezone: "America/New_York",
	}, defaults(), fallBack)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	deadline := plan.DeadlineAt.In(newYork)
	if deadline.Hour() != 7 || deadline.Minute() != 0 || deadline.Day() != 1 {
		t.Fatalf("deadline = %v, want 07:00 on the same long night", deadline)
	}
	// Seven wall-clock hours, but seven and a half real ones.
	if elapsed := plan.DeadlineAt.Sub(plan.StartAt); elapsed != 7*time.Hour+30*time.Minute {
		t.Fatalf("elapsed = %v, want the extra hour the clock change adds", elapsed)
	}

	// And the spring-forward night is an hour shorter.
	springForward := time.Date(2026, 3, 8, 0, 30, 0, 0, newYork).UTC()
	plan, err = BuildPlan(snapshotWith(eligible("alpha", "builder")), model.NewBridgeState(), Request{
		Selections: []string{"alpha"}, Start: "00:30", Deadline: "07:00", Timezone: "America/New_York",
	}, defaults(), springForward)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if deadline := plan.DeadlineAt.In(newYork); deadline.Hour() != 7 {
		t.Fatalf("deadline = %v, want 07:00", deadline)
	}
	if elapsed := plan.DeadlineAt.Sub(plan.StartAt); elapsed != 5*time.Hour+30*time.Minute {
		t.Fatalf("elapsed = %v, want the hour the clock change removes", elapsed)
	}
}

// TestTimesArePersistedAbsolutelySoADisplayZoneCannotMoveThem keeps a stored
// deadline meaning one instant regardless of how it is later shown.
func TestTimesArePersistedAbsolutelySoADisplayZoneCannotMoveThem(t *testing.T) {
	plan, err := BuildPlan(snapshotWith(eligible("alpha", "builder")), model.NewBridgeState(), Request{
		Selections: []string{"alpha"}, Start: "23:00", Deadline: "07:00", Timezone: "America/New_York",
	}, defaults(), evening)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	service := newService(t)
	run, err := service.Create(context.Background(), plan, ConfirmationFlag)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	reloaded, err := service.Get(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.DeadlineAt.Equal(plan.DeadlineAt) {
		t.Fatalf("deadline = %v, want the exact instant preserved (%v)", reloaded.DeadlineAt, plan.DeadlineAt)
	}
	if reloaded.Timezone != "America/New_York" {
		t.Fatalf("timezone = %q, want the display zone recorded alongside", reloaded.Timezone)
	}
	// Displayed in another zone it is a different clock time and the same moment.
	tokyo, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		t.Skip("tzdata unavailable")
	}
	if !reloaded.DeadlineAt.In(tokyo).Equal(reloaded.DeadlineAt.In(newYork)) {
		t.Fatal("the stored deadline is not one absolute instant")
	}
}

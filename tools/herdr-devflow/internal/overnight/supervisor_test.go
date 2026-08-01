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
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/overview"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/tasklist"
)

// Nothing in this file talks to Herdr or Claude. The agent list, the prompt
// endpoint, the task list, and the usage classifier are all values, and the
// clock is a variable — which is the only way to assert that ten unchanged
// ticks produce exactly zero prompts.

type fakeAgents struct {
	agents []herdr.AgentInfo
	err    error
	calls  int
}

func (f *fakeAgents) AgentListInfo(context.Context) ([]herdr.AgentInfo, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.agents, nil
}

type fakePrompter struct {
	prompts []string
	targets []string
	// acknowledgement is what Herdr reports back; the zero value acknowledges
	// nothing, which must read as uncertain.
	acknowledgement herdr.AgentInfo
	err             error
}

func (f *fakePrompter) AgentPromptInfo(_ context.Context, target, text string, _ time.Duration) (herdr.AgentInfo, error) {
	f.targets = append(f.targets, target)
	f.prompts = append(f.prompts, text)
	if f.err != nil {
		return herdr.AgentInfo{}, f.err
	}
	return f.acknowledgement, nil
}

type fakeUsage struct{ signal claudeusage.Signal }

func (f fakeUsage) Classify(sessionID string, now, lastHandled time.Time) claudeusage.Signal {
	signal := f.signal
	if signal.Class == "" {
		signal.Class = claudeusage.LimitNone
	}
	signal.SessionID = sessionID
	if signal.DetectedAt.IsZero() {
		signal.DetectedAt = now
	}
	// A reset that is not strictly newer than the handled boundary is never
	// sleepable, exactly as the real adapter decides it.
	if signal.Sleepable && !lastHandled.IsZero() && !signal.ResetAt.After(lastHandled) {
		signal.Sleepable = false
		signal.Reason = "this reset boundary was already handled"
	}
	return signal
}

const supervisorPlan = `
- [ ] 1.0 Build it
  - [x] 1.1 Land the groundwork
  - [ ] 1.2 Continue the implementation
  - [ ] 1.3 Demo: drive the new surface
`

// harness is one run with one participant, plus the fakes it is observed
// through.
type harness struct {
	service    *Service
	supervisor *Supervisor
	agents     *fakeAgents
	prompter   *fakePrompter
	usage      *fakeUsage
	plan       string
	run        model.OvernightRun
	clock      time.Time
}

func liveAgent(seq uint64, status model.AgentStatus, mutate ...func(*herdr.AgentInfo)) herdr.AgentInfo {
	agent := herdr.AgentInfo{
		WorkspaceID: "w-alpha", PaneID: "w-alpha:p1", TerminalID: "t-alpha",
		Name: "ori-alpha-builder", Agent: "claude", AgentStatus: status,
		Cwd: "/w/alpha", StateChangeSeq: seq,
		AgentSession: &model.NativeSession{Source: "herdr:claude", Agent: "claude", Kind: "id", Value: "sess-alpha"},
	}
	for _, apply := range mutate {
		apply(&agent)
	}
	return agent
}

func newHarness(t *testing.T, features ...string) *harness {
	t.Helper()
	if len(features) == 0 {
		features = []string{"alpha"}
	}
	service := newService(t)
	agents := make([]overview.Agent, 0, len(features))
	selections := make([]string, 0, len(features))
	for _, feature := range features {
		agents = append(agents, eligible(feature, "builder"))
		selections = append(selections, feature)
	}
	plan, err := BuildPlan(snapshotWith(agents...), model.NewBridgeState(),
		Request{Selections: selections}, defaults(), evening)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	run, err := service.Create(context.Background(), plan, ConfirmationFlag)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	harness := &harness{
		service:  service,
		agents:   &fakeAgents{agents: []herdr.AgentInfo{liveAgent(1, model.AgentIdle)}},
		prompter: &fakePrompter{acknowledgement: liveAgent(2, model.AgentWorking)},
		usage:    &fakeUsage{},
		plan:     supervisorPlan,
		run:      run,
		clock:    evening.Add(time.Minute),
	}
	harness.supervisor = &Supervisor{
		Store:    service.Store,
		Agents:   harness.agents,
		Prompt:   harness.prompter,
		Usage:    harness.usage,
		ReadPlan: func(string) tasklist.Plan { return tasklist.ParsePlan(harness.plan) },
		Now:      func() time.Time { return harness.clock },
	}
	return harness
}

func (h *harness) tick(t *testing.T) model.OvernightRun {
	t.Helper()
	run, err := h.supervisor.Tick(context.Background(), h.run.ID)
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	h.run = run
	return run
}

func (h *harness) participant(t *testing.T, index int) model.RunParticipant {
	t.Helper()
	if index >= len(h.run.Participants) {
		t.Fatalf("participant %d does not exist", index)
	}
	return h.run.Participants[index]
}

// TestTickBeforeTheStartTimeDoesNothing keeps a scheduled run scheduled.
func TestTickBeforeTheStartTimeDoesNothing(t *testing.T) {
	h := newHarness(t)
	h.clock = h.run.StartAt.Add(-time.Hour)
	run := h.tick(t)

	if run.State != model.RunScheduled || run.ActiveParticipant != "" {
		t.Fatalf("run = %+v, want it untouched before its start time", run)
	}
	if len(h.prompter.prompts) != 0 {
		t.Fatalf("prompts = %v, want none before the start time", h.prompter.prompts)
	}
}

func TestValidPlanProofDoesNotTurnAStaleWindowIntoAStop(t *testing.T) {
	h := newHarness(t, "alpha")
	participant := &h.run.Participants[0]
	participant.PlanProof = model.PlanProof{FormatVersion: 1, SessionID: participant.Binding.NativeSession.Value, PlanBacked: true, ExpiresAt: h.clock.Add(8 * time.Hour)}
	h.usage.signal = claudeusage.Signal{Class: claudeusage.LimitUnknown, Reason: "The newest Claude usage record for this session is too old to describe the current window."}
	recognized, stopped := h.supervisor.classifyLimit(&h.run, participant, h.clock)
	if recognized || stopped || participant.Limit != nil {
		t.Fatalf("stale window became a reset or stop: recognized=%v stopped=%v participant=%+v", recognized, stopped, participant)
	}
}

// TestFirstTickActivatesTheQueueHeadAndPromptsItOnce is the ordinary path.
func TestFirstTickActivatesTheQueueHeadAndPromptsItOnce(t *testing.T) {
	h := newHarness(t, "alpha", "beta")
	run := h.tick(t)

	if run.State != model.RunRunning {
		t.Fatalf("state = %q, want running", run.State)
	}
	if run.ActiveParticipant != run.Participants[0].ID {
		t.Fatalf("active = %q, want the queue head", run.ActiveParticipant)
	}
	if h.participant(t, 1).State != model.ParticipantQueued {
		t.Fatalf("the second participant = %q, want it left queued", h.participant(t, 1).State)
	}
	if len(h.prompter.prompts) != 1 || h.prompter.prompts[0] != ContinuePrompt {
		t.Fatalf("prompts = %v, want exactly one continue", h.prompter.prompts)
	}
	if h.prompter.targets[0] != "w-alpha:p1" {
		t.Fatalf("prompt target = %q, want the exact confirmed pane", h.prompter.targets[0])
	}
	delivery := h.participant(t, 0).Delivery
	if delivery.State != model.DeliveryAcknowledged {
		t.Fatalf("delivery = %+v, want an acknowledged continuation", delivery)
	}
	if delivery.IdempotencyKey == "" || strings.Contains(delivery.Summary, "the API key") {
		t.Fatalf("delivery = %+v, want a key and a bounded summary", delivery)
	}
}

// TestRepeatedTicksWithNothingNewProduceNoSecondPrompt is success metric seven:
// ten unchanged polls, one prompt.
func TestRepeatedTicksWithNothingNewProduceNoSecondPrompt(t *testing.T) {
	h := newHarness(t)
	h.tick(t)
	if len(h.prompter.prompts) != 1 {
		t.Fatalf("prompts after the first tick = %v", h.prompter.prompts)
	}
	// The agent is now working, then settles back to idle at the same
	// sequence Herdr reported when it acknowledged: nothing new happened.
	h.agents.agents = []herdr.AgentInfo{liveAgent(2, model.AgentIdle)}
	for range 10 {
		h.clock = h.clock.Add(time.Minute)
		h.tick(t)
	}
	if len(h.prompter.prompts) != 1 {
		t.Fatalf("prompts = %d, want exactly one across ten unchanged ticks", len(h.prompter.prompts))
	}
}

// TestAWorkingAgentIsObservedNotPrompted covers FR39.
func TestAWorkingAgentIsObservedNotPrompted(t *testing.T) {
	h := newHarness(t)
	h.agents.agents = []herdr.AgentInfo{liveAgent(1, model.AgentWorking)}
	run := h.tick(t)

	if len(h.prompter.prompts) != 0 {
		t.Fatalf("prompts = %v, want none for a working agent", h.prompter.prompts)
	}
	if run.Participants[0].State != model.ParticipantActive {
		t.Fatalf("participant = %q, want it left active and observed", run.Participants[0].State)
	}
	if run.Participants[0].LastObservedState != string(model.AgentWorking) {
		t.Fatalf("observation = %q, want the working state recorded", run.Participants[0].LastObservedState)
	}
}

// TestAnUnacknowledgedPromptBecomesUncertainAndStops is the duplicate-work
// guard: a prompt Herdr did not confirm is never sent again automatically.
func TestAnUnacknowledgedPromptBecomesUncertainAndStops(t *testing.T) {
	h := newHarness(t)
	// Herdr acknowledges a different pane than the one that was prompted.
	h.prompter.acknowledgement = liveAgent(2, model.AgentWorking, func(a *herdr.AgentInfo) { a.PaneID = "w-other:p9" })
	run := h.tick(t)

	delivery := run.Participants[0].Delivery
	if delivery.State != model.DeliveryUncertain {
		t.Fatalf("delivery = %+v, want uncertain", delivery)
	}
	// Every later tick leaves it alone.
	for range 5 {
		h.clock = h.clock.Add(time.Minute)
		h.agents.agents = []herdr.AgentInfo{liveAgent(9, model.AgentIdle)}
		h.tick(t)
	}
	if len(h.prompter.prompts) != 1 {
		t.Fatalf("prompts = %d, want the uncertain delivery never retried", len(h.prompter.prompts))
	}
}

func TestAFailedSubmissionIsRecordedWithoutRetrying(t *testing.T) {
	h := newHarness(t)
	h.prompter.err = errors.New("herdr refused")
	run := h.tick(t)

	if run.Participants[0].Delivery.State != model.DeliveryFailed {
		t.Fatalf("delivery = %+v, want failed", run.Participants[0].Delivery)
	}
	if strings.Contains(run.Participants[0].Delivery.Detail, "herdr refused") {
		t.Fatalf("the raw Herdr error reached the record: %q", run.Participants[0].Delivery.Detail)
	}
}

// TestAManualCheckpointStopsTheParticipantAndHandsTheQueueOn covers FR57 and
// the queue handoff in FR102.
func TestAManualCheckpointStopsTheParticipantAndHandsTheQueueOn(t *testing.T) {
	h := newHarness(t, "alpha", "beta")
	h.plan = `
- [ ] 1.0 Build it
  - [x] 1.1 Land the groundwork
  - [ ] 1.2 Demo: drive the new surface
`
	run := h.tick(t)

	if len(h.prompter.prompts) != 0 {
		t.Fatalf("prompts = %v, want none when a demo comes next", h.prompter.prompts)
	}
	if run.Participants[0].State != model.ParticipantReadyForReview {
		t.Fatalf("participant = %q, want ready_for_review", run.Participants[0].State)
	}
	if run.Participants[0].Outcome != model.ReasonManualCheckpoint || run.Participants[0].Recovery == "" {
		t.Fatalf("participant = %+v, want the manual outcome and a recovery", run.Participants[0])
	}
	if !strings.Contains(strings.ToLower(run.Timeline[len(run.Timeline)-1].Detail), "demo") {
		t.Fatalf("timeline = %+v, want the checkpoint named", run.Timeline)
	}

	// The next tick activates the queued participant.
	h.clock = h.clock.Add(time.Minute)
	h.agents.agents = []herdr.AgentInfo{
		liveAgent(1, model.AgentIdle),
		liveAgent(1, model.AgentIdle, func(a *herdr.AgentInfo) {
			a.WorkspaceID, a.PaneID, a.Cwd = "w-beta", "w-beta:p1", "/w/beta"
			a.AgentSession = &model.NativeSession{Source: "herdr:claude", Agent: "claude", Kind: "id", Value: "sess-beta"}
		}),
	}
	run = h.tick(t)
	// The second participant got its turn. It stops at the same demo, because
	// this fixture gives both participants the same plan — what matters is
	// that the queue moved on rather than stalling on the first one.
	if run.Participants[1].State == model.ParticipantQueued {
		t.Fatalf("the second participant = %q, want the queue to have moved on", run.Participants[1].State)
	}
	if len(h.prompter.prompts) != 0 {
		t.Fatalf("prompts = %v, want none: both participants stop at a demo", h.prompter.prompts)
	}
	if run.State != model.RunReadyForReview {
		t.Fatalf("run = %q, want ready_for_review once every participant stopped for review", run.State)
	}
}

// TestCompletedImplementationCompletesTheParticipant covers FR60: a
// participant is complete when the work an unattended agent may do is done.
func TestCompletedImplementationCompletesTheParticipant(t *testing.T) {
	h := newHarness(t)
	h.plan = `
- [ ] 1.0 Build it
  - [x] 1.1 Land the groundwork
  - [x] 1.2 Continue the implementation
  - [x] 1.3 Validate the slice
  - [x] 1.4 Commit: "feat: land it"
`
	run := h.tick(t)

	if run.Participants[0].State != model.ParticipantCompleted {
		t.Fatalf("participant = %q, want completed", run.Participants[0].State)
	}
	if run.State != model.RunCompleted {
		t.Fatalf("run = %q, want completed once the queue emptied", run.State)
	}
	if len(h.prompter.prompts) != 0 {
		t.Fatalf("prompts = %v, want none for finished work", h.prompter.prompts)
	}
}

// TestBindingDriftStopsTheParticipantRatherThanPromptingAnyway is why the
// exact native session is recorded at confirmation.
func TestBindingDriftStopsTheParticipantRatherThanPromptingAnyway(t *testing.T) {
	cases := []struct {
		name  string
		agent herdr.AgentInfo
	}{
		{"the session disappeared", liveAgent(1, model.AgentIdle, func(a *herdr.AgentInfo) {
			a.AgentSession = &model.NativeSession{Value: "sess-someone-else"}
		})},
		{"the agent moved to another pane", liveAgent(1, model.AgentIdle, func(a *herdr.AgentInfo) {
			a.PaneID = "w-alpha:p7"
		})},
		{"the agent left its worktree", liveAgent(1, model.AgentIdle, func(a *herdr.AgentInfo) {
			a.Cwd = "/somewhere/else"
		})},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			h := newHarness(t)
			h.agents.agents = []herdr.AgentInfo{testCase.agent}
			run := h.tick(t)

			if len(h.prompter.prompts) != 0 {
				t.Fatalf("prompts = %v, want none after drift", h.prompter.prompts)
			}
			if run.Participants[0].State != model.ParticipantWaitingManual {
				t.Fatalf("participant = %q, want waiting_manual", run.Participants[0].State)
			}
			if run.Participants[0].Recovery == "" {
				t.Fatalf("participant = %+v, want a recovery command", run.Participants[0])
			}
		})
	}
}

// TestAmbiguousSessionsStopRatherThanChoosing covers the case where two agents
// claim the same session.
func TestAmbiguousSessionsStopRatherThanChoosing(t *testing.T) {
	h := newHarness(t)
	h.agents.agents = []herdr.AgentInfo{liveAgent(1, model.AgentIdle), liveAgent(1, model.AgentIdle)}
	run := h.tick(t)

	if len(h.prompter.prompts) != 0 {
		t.Fatalf("prompts = %v, want none when the session is ambiguous", h.prompter.prompts)
	}
	if run.Participants[0].State != model.ParticipantWaitingManual {
		t.Fatalf("participant = %q, want waiting_manual", run.Participants[0].State)
	}
}

// TestHerdrOutageDoesNothingAtAll is the difference between "cannot see" and
// "sees an idle agent".
func TestHerdrOutageDoesNothingAtAll(t *testing.T) {
	h := newHarness(t)
	h.agents.err = errors.New("socket unavailable")
	run := h.tick(t)

	if len(h.prompter.prompts) != 0 {
		t.Fatalf("prompts = %v, want none during an outage", h.prompter.prompts)
	}
	if run.Participants[0].State != model.ParticipantActive {
		t.Fatalf("participant = %q, want it left active and unchanged", run.Participants[0].State)
	}
}

// TestAMissingOrMalformedPlanStopsTheParticipant covers FR46.
func TestAMissingOrMalformedPlanStopsTheParticipant(t *testing.T) {
	for _, testCase := range []struct{ name, contents string }{
		{"a plan with no checklist at all", "just prose, no items"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			h := newHarness(t)
			h.plan = testCase.contents
			run := h.tick(t)
			if run.Participants[0].State != model.ParticipantWaitingManual {
				t.Fatalf("participant = %q, want waiting_manual", run.Participants[0].State)
			}
			if len(h.prompter.prompts) != 0 {
				t.Fatalf("prompts = %v, want none for an unreadable plan", h.prompter.prompts)
			}
		})
	}
}

// TestAVerifiedLimitFreezesTheWholeQueue is FR50 and FR69: the limited agent
// keeps the head, and nobody else is prompted in the meantime.
func TestAVerifiedLimitFreezesTheWholeQueue(t *testing.T) {
	h := newHarness(t, "alpha", "beta")
	reset := h.clock.Add(2 * time.Hour)
	h.usage.signal = claudeusage.Signal{
		Class: claudeusage.LimitIncludedSession, AuthMode: claudeusage.AuthPlanBacked,
		ResetAt: reset, Sleepable: true,
	}
	run := h.tick(t)

	if run.State != model.RunLimitDetected {
		t.Fatalf("state = %q, want limit_detected", run.State)
	}
	if run.ActiveParticipant != run.Participants[0].ID {
		t.Fatalf("active = %q, want the limited participant to keep the head", run.ActiveParticipant)
	}
	if run.Participants[1].State != model.ParticipantQueued {
		t.Fatalf("the second participant = %q, want it still queued", run.Participants[1].State)
	}
	if run.Participants[0].Limit == nil || !run.Participants[0].Limit.ResetAt.Equal(reset) {
		t.Fatalf("limit = %+v, want Claude's reset recorded", run.Participants[0].Limit)
	}
	if len(h.prompter.prompts) != 0 {
		t.Fatalf("prompts = %v, want none once a limit is detected", h.prompter.prompts)
	}

	// While frozen, further ticks prompt nobody.
	for range 5 {
		h.clock = h.clock.Add(time.Minute)
		h.tick(t)
	}
	if len(h.prompter.prompts) != 0 {
		t.Fatalf("prompts = %v, want the queue frozen", h.prompter.prompts)
	}
}

// TestANonSessionLimitStopsTheParticipantWithoutSleeping covers FR48: weekly
// caps, context exhaustion, and billing errors are not waited out.
func TestANonSessionLimitStopsTheParticipantWithoutSleeping(t *testing.T) {
	for _, class := range []claudeusage.LimitClass{
		claudeusage.LimitWeekly, claudeusage.LimitContext, claudeusage.LimitUnknown,
	} {
		t.Run(string(class), func(t *testing.T) {
			h := newHarness(t, "alpha", "beta")
			h.usage.signal = claudeusage.Signal{Class: class, Reason: "not a five-hour session limit"}
			run := h.tick(t)

			if run.State == model.RunLimitDetected {
				t.Fatalf("a %s limit entered the sleep sequence", class)
			}
			if run.Participants[0].State != model.ParticipantWaitingManual {
				t.Fatalf("participant = %q, want waiting_manual", run.Participants[0].State)
			}
			if len(h.prompter.prompts) != 0 {
				t.Fatalf("prompts = %v", h.prompter.prompts)
			}
		})
	}
}

// TestAnUnsleepableSessionLimitStopsRatherThanFreezing covers the case where
// the class is right but the evidence is not good enough to wait it out.
func TestAnUnsleepableSessionLimitStopsRatherThanFreezing(t *testing.T) {
	h := newHarness(t)
	h.usage.signal = claudeusage.Signal{
		Class: claudeusage.LimitIncludedSession, Sleepable: false,
		Reason: "Claude reported no reset time for the exhausted window",
	}
	run := h.tick(t)

	if run.State == model.RunLimitDetected {
		t.Fatal("an unsleepable limit entered the sleep sequence")
	}
	if run.Participants[0].State != model.ParticipantWaitingManual {
		t.Fatalf("participant = %q, want waiting_manual", run.Participants[0].State)
	}
}

// TestTheDeadlineStopsNewWorkButNeverInterrupts covers FR95 and FR96.
func TestTheDeadlineStopsNewWorkButNeverInterrupts(t *testing.T) {
	h := newHarness(t)
	// The run is under way with its agent mid-turn when the deadline arrives.
	h.agents.agents = []herdr.AgentInfo{liveAgent(1, model.AgentWorking)}
	h.tick(t)

	h.clock = h.run.DeadlineAt.Add(time.Minute)
	run := h.tick(t)

	if run.State != model.RunOverrun {
		t.Fatalf("state = %q, want overrun while an agent is still working", run.State)
	}
	if len(h.prompter.prompts) != 0 {
		t.Fatalf("prompts = %v, want none at the deadline", h.prompter.prompts)
	}

	// Once it settles, the run ends.
	h.clock = h.clock.Add(time.Minute)
	h.agents.agents = []herdr.AgentInfo{liveAgent(2, model.AgentIdle)}
	run = h.tick(t)
	if run.State != model.RunDeadlineReached || run.TerminalReason != model.ReasonDeadlineReached {
		t.Fatalf("run = %+v, want deadline_reached", run)
	}
	if len(h.prompter.prompts) != 0 {
		t.Fatalf("prompts = %v, want none after the deadline", h.prompter.prompts)
	}
}

// TestATerminalRunIsNeverAdvanced keeps a finished run finished.
func TestATerminalRunIsNeverAdvanced(t *testing.T) {
	h := newHarness(t)
	if _, err := h.service.Cancel(context.Background(), h.run.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	run := h.tick(t)
	if run.State != model.RunCanceled {
		t.Fatalf("state = %q, want the cancellation preserved", run.State)
	}
	if len(h.prompter.prompts) != 0 {
		t.Fatalf("prompts = %v, want none for a canceled run", h.prompter.prompts)
	}
}

// TestTheSupervisorNeverWritesToTheTaskList is FR45. The plan is read; the
// agent alone ticks it off.
func TestTheSupervisorNeverWritesToTheTaskList(t *testing.T) {
	h := newHarness(t)
	reads := 0
	h.supervisor.ReadPlan = func(string) tasklist.Plan {
		reads++
		return tasklist.ParsePlan(h.plan)
	}
	before := h.plan
	h.tick(t)
	if reads == 0 {
		t.Fatal("the plan was never read")
	}
	if h.plan != before {
		t.Fatal("the supervisor modified the task list")
	}
}

// TestCheckpointProgressIsRefreshedFromTheFile proves the recorded position
// follows the file rather than the supervisor's own expectations.
func TestCheckpointProgressIsRefreshedFromTheFile(t *testing.T) {
	h := newHarness(t)
	run := h.tick(t)
	if run.Participants[0].Checkpoint.NextOrdinal != "1.2" {
		t.Fatalf("checkpoint = %+v, want the next safe subtask", run.Participants[0].Checkpoint)
	}
	if run.Participants[0].Checkpoint.ManualOrdinal != "1.3" {
		t.Fatalf("checkpoint = %+v, want the first manual stop", run.Participants[0].Checkpoint)
	}

	// The agent finishes 1.2 and the record follows.
	h.plan = `
- [ ] 1.0 Build it
  - [x] 1.1 Land the groundwork
  - [x] 1.2 Continue the implementation
  - [ ] 1.3 Demo: drive the new surface
`
	h.clock = h.clock.Add(time.Minute)
	h.agents.agents = []herdr.AgentInfo{liveAgent(5, model.AgentIdle)}
	run = h.tick(t)
	if run.Participants[0].Checkpoint.SubtasksCompleted != 2 {
		t.Fatalf("checkpoint = %+v, want the file's progress", run.Participants[0].Checkpoint)
	}
	if run.Participants[0].State != model.ParticipantReadyForReview {
		t.Fatalf("participant = %q, want it stopped at the demo", run.Participants[0].State)
	}
}

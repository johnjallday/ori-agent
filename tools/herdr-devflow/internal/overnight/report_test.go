package overnight

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/claudeusage"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/herdr"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/model"
)

// TestTheReportSaysWhatMovedAndWhatToDo is what somebody reads over coffee.
func TestTheReportSaysWhatMovedAndWhatToDo(t *testing.T) {
	h, wake, power, reset := limitedHarness(t)
	h.cycle(t, wake, power, reset)

	// The agent finishes its implementation work, leaving only the demo.
	h.plan = `
- [ ] 1.0 Build it
  - [x] 1.1 Land the groundwork
  - [x] 1.2 Continue the implementation
  - [ ] 1.3 Demo: drive the new surface
`
	h.usage.signal = claudeusage.Signal{Class: claudeusage.LimitNone}
	h.clock = h.clock.Add(time.Minute)
	h.agents.agents = []herdr.AgentInfo{liveAgent(30, model.AgentIdle)}
	run := h.tick(t)

	if run.Participants[0].State != model.ParticipantReadyForReview {
		t.Fatalf("participant = %q, want ready_for_review", run.Participants[0].State)
	}
	report := BuildReport(run, h.clock)
	var out strings.Builder
	if err := RenderReport(&out, run, report); err != nil {
		t.Fatalf("RenderReport: %v", err)
	}
	rendered := out.String()

	for _, want := range []string{
		"[1] alpha",
		"ready for review",
		"subtasks complete",
		"stopped at: 1.3 Demo",
		"1 of 3 acknowledged",
		"included plan capacity only",
		"Limits and sleeps:",
		"Next:",
		"review and continue at 1.3 Demo",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("the report did not mention %q:\n%s", want, rendered)
		}
	}
	// It never overstates what happened.
	for _, forbidden := range []string{"shipped", "merged", "Merged"} {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("the report claimed %q:\n%s", forbidden, rendered)
		}
	}
}

// TestTheReportShowsWhatMovedOvernight is the before-and-after the confirmed
// checkpoint exists for.
func TestTheReportShowsWhatMovedOvernight(t *testing.T) {
	h := newHarness(t)
	run, err := h.service.Get(h.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Participants[0].StartingCompleted != run.Participants[0].Checkpoint.SubtasksCompleted {
		t.Fatalf("starting progress = %d, want the confirmed checkpoint",
			run.Participants[0].StartingCompleted)
	}

	// Two more subtasks get done overnight.
	saved, err := h.supervisor.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	stored := saved.Runs[run.ID]
	stored.Participants[0].Checkpoint.SubtasksCompleted = stored.Participants[0].StartingCompleted + 2
	stored.Participants[0].Checkpoint.SubtasksTotal = 10
	saved.Runs[run.ID] = stored
	if err := h.supervisor.Store.Save(saved); err != nil {
		t.Fatal(err)
	}

	report := BuildReport(stored, h.clock)
	var out strings.Builder
	if err := RenderReport(&out, stored, report); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "(+2 overnight)") {
		t.Fatalf("the report did not show what moved:\n%s", out.String())
	}
}

// TestTheReportListsUncertaintiesRatherThanResolvingThem is the honesty rule.
func TestTheReportListsUncertaintiesRatherThanResolvingThem(t *testing.T) {
	h := newHarness(t)
	saved, err := h.supervisor.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	run := saved.Runs[h.run.ID]
	run.Participants[0].Delivery = model.RunDelivery{ID: "d1", State: model.DeliveryUncertain}
	run.Wake = model.WakeOwnership{CandidateID: h.run.ID, Uncertain: true,
		Detail: "this run's wake candidate could not be confirmed withdrawn"}
	saved.Runs[h.run.ID] = run
	if err := h.supervisor.Store.Save(saved); err != nil {
		t.Fatal(err)
	}

	report := BuildReport(run, h.clock)
	if len(report.Uncertainties) < 2 {
		t.Fatalf("uncertainties = %v, want both the delivery and the wake", report.Uncertainties)
	}
	var out strings.Builder
	if err := RenderReport(&out, run, report); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Uncertain — please check:") {
		t.Fatalf("the report hid its uncertainties:\n%s", out.String())
	}
}

// TestTheReportIsDurableAndCarriesNoPromptText covers the record and the
// redaction boundary together.
func TestTheReportIsDurableAndCarriesNoPromptText(t *testing.T) {
	h := newHarness(t)
	h.plan = `
- [ ] 1.0 Build it
  - [x] 1.1 Land the groundwork
`
	run := h.tick(t)
	if run.State != model.RunCompleted {
		t.Fatalf("state = %q, want completed", run.State)
	}
	if run.Report == nil {
		t.Fatal("a finished run left no durable report")
	}

	reloaded, err := h.service.Get(h.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Report == nil || reloaded.Report.Reason != run.TerminalReason {
		t.Fatalf("reloaded report = %+v, want it durable", reloaded.Report)
	}

	encoded, err := json.Marshal(ReportPayload(reloaded, *reloaded.Report))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"continue;", "API key", "sk-"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("the report payload leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestTheReportNamesTheEndingPlainly(t *testing.T) {
	cases := map[model.TerminalReason]string{
		model.ReasonQueueComplete:     "completed its implementation boundary",
		model.ReasonDeadlineReached:   "stopped at the morning deadline",
		model.ReasonCycleLimitReached: "stopped at the resume ceiling",
		model.ReasonCanceled:          "was canceled",
		model.ReasonUncertain:         "ended with something uncertain",
	}
	for reason, want := range cases {
		if got := endingLabel(reason, model.RunCompleted); got != want {
			t.Fatalf("endingLabel(%q) = %q, want %q", reason, got, want)
		}
	}
}

// TestAProvisionalReportIsBuildableMidRun so `report` works before morning.
func TestAProvisionalReportIsBuildableMidRun(t *testing.T) {
	h := newHarness(t)
	h.tick(t)
	run, err := h.service.Get(h.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Report != nil {
		t.Fatal("an unfinished run already has a final report")
	}
	report := BuildReport(run, h.clock)
	if len(report.Participants) != 1 || report.FinishedAt.IsZero() == false {
		t.Fatalf("provisional report = %+v, want no finish time", report)
	}
	_ = context.Background()
}

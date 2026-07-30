package overnight

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/model"
)

// The morning report is the one thing a person definitely reads. Everything
// else in this feature is a safeguard against something going wrong overnight;
// this is what they get when they sit down with coffee.
//
// It is written to answer four questions in order: what happened, what moved,
// what stopped and why, and what to do now. It never says "shipped" or
// "finished" — an Overnight Run completes an implementation boundary, and
// calling that anything grander would be the run flattering itself.

// BuildReport assembles the durable summary for a terminal run.
//
// It reads only what the run already recorded. Nothing is recomputed from live
// state: by morning the agents may have been closed, and a report that changed
// depending on when it was read would be worthless as a record.
func BuildReport(run model.OvernightRun, now time.Time) model.MorningReport {
	report := model.MorningReport{
		GeneratedAt:         now.UTC(),
		StartedAt:           run.StartAt,
		DeadlineAt:          run.DeadlineAt,
		Reason:              run.TerminalReason,
		MaxResumes:          run.MaxResumes,
		AcknowledgedResumes: run.AcknowledgedResumes,
	}
	if run.State.Terminal() {
		report.FinishedAt = run.UpdatedAt
	}

	for _, participant := range run.Participants {
		entry := model.ReportParticipant{
			Feature:          participant.Feature.Name,
			Role:             participant.Binding.Role,
			Position:         participant.Position,
			State:            participant.State,
			Outcome:          participant.Outcome,
			SubtasksBefore:   participant.StartingCompleted,
			SubtasksAfter:    participant.Checkpoint.SubtasksCompleted,
			SubtasksTotal:    participant.Checkpoint.SubtasksTotal,
			ManualCheckpoint: manualLabel(participant.Checkpoint),
			Validation:       participant.Validation,
			Recovery:         participant.Recovery,
		}
		report.Participants = append(report.Participants, entry)
	}

	report.Uncertainties = uncertainties(run)
	report.NextActions = nextActions(run)
	report.DeclinedCreditOffers = countEvents(run, "credit_offer_declined")
	return report
}

func manualLabel(checkpoint model.TaskCheckpoint) string {
	if checkpoint.ManualOrdinal == "" {
		return ""
	}
	return checkpoint.ManualOrdinal + " " + checkpoint.ManualText
}

func countEvents(run model.OvernightRun, kind string) int {
	count := 0
	for _, event := range run.Timeline {
		if event.Kind == kind {
			count++
		}
	}
	return count
}

// uncertainties collects everything nobody could establish either way. They are
// listed separately from failures because they need a person to look, not a
// person to fix.
func uncertainties(run model.OvernightRun) []string {
	var found []string
	if run.Uncertainty != "" {
		found = append(found, run.Uncertainty)
	}
	if run.Wake.Uncertain {
		detail := run.Wake.Detail
		if detail == "" {
			detail = "this run's wake candidate could not be confirmed withdrawn"
		}
		found = append(found, detail)
	}
	for _, participant := range run.Participants {
		switch participant.Delivery.State {
		case model.DeliveryDelivering:
			found = append(found, participant.Feature.Name+
				": a continuation was in flight when the run ended; it may or may not have arrived")
		case model.DeliveryUncertain:
			found = append(found, participant.Feature.Name+
				": a continuation was submitted but not confirmed; check before prompting it again")
		}
	}
	return found
}

// nextActions is the shortest honest list of what to do now.
func nextActions(run model.OvernightRun) []string {
	var actions []string
	for _, participant := range run.Participants {
		switch participant.State {
		case model.ParticipantReadyForReview:
			label := manualLabel(participant.Checkpoint)
			if label == "" {
				label = "its next manual checkpoint"
			}
			actions = append(actions, participant.Feature.Name+": review and continue at "+label)
		case model.ParticipantWaitingManual, model.ParticipantFailed, model.ParticipantUncertain:
			recovery := participant.Recovery
			if recovery == "" {
				recovery = "inspect it with wt herd status --feature " + participant.Feature.Name
			}
			actions = append(actions, participant.Feature.Name+": "+recovery)
		case model.ParticipantCompleted:
			actions = append(actions, participant.Feature.Name+
				": implementation is complete; take it through its delivery checkpoints")
		}
	}
	if run.TerminalReason == model.ReasonCycleLimitReached {
		actions = append(actions, "the resume ceiling was reached; start another run if more time is wanted")
	}
	if run.TerminalReason == model.ReasonDeadlineReached {
		actions = append(actions, "the morning deadline ended unattended work; nothing was interrupted")
	}
	return actions
}

// RenderReport writes the morning report for a person.
func RenderReport(out io.Writer, run model.OvernightRun, report model.MorningReport) error {
	location := loadLocation(run.Timezone)
	write := func(format string, args ...any) error {
		_, err := fmt.Fprintf(out, format+"\n", args...)
		return err
	}

	if err := write("Overnight Run %s — %s", run.ID, endingLabel(report.Reason, run.State)); err != nil {
		return err
	}
	if err := write("  ran:      %s → %s", formatLocal(report.StartedAt, location),
		formatLocal(report.FinishedAt, location)); err != nil {
		return err
	}
	if err := write("  deadline: %s", formatLocal(report.DeadlineAt, location)); err != nil {
		return err
	}
	if err := write("  resumes:  %d of %d acknowledged", report.AcknowledgedResumes, report.MaxResumes); err != nil {
		return err
	}
	if err := write("  billing:  included plan capacity only; %d credit offer(s) declined",
		report.DeclinedCreditOffers); err != nil {
		return err
	}

	if err := write("\nAgents, in the order you confirmed:"); err != nil {
		return err
	}
	for _, participant := range report.Participants {
		name := participant.Feature
		if participant.Role != "" {
			name += " / " + participant.Role
		}
		if err := write("  [%d] %-30s %s", participant.Position, truncate(name, 30),
			participantEnding(participant)); err != nil {
			return err
		}
		if participant.SubtasksTotal > 0 {
			moved := participant.SubtasksAfter - participant.SubtasksBefore
			if err := write("      progress: %d of %d subtasks complete (+%d overnight)",
				participant.SubtasksAfter, participant.SubtasksTotal, moved); err != nil {
				return err
			}
		}
		if participant.ManualCheckpoint != "" {
			if err := write("      stopped at: %s", truncate(participant.ManualCheckpoint, 96)); err != nil {
				return err
			}
		}
		if participant.Validation.Commits > 0 {
			if err := write("      commits:  %d (%s)", participant.Validation.Commits,
				truncate(participant.Validation.LastCommitSubject, 64)); err != nil {
				return err
			}
		}
		if participant.Validation.Dirty || participant.Validation.Ahead > 0 || participant.Validation.Behind > 0 {
			if err := write("      git:      %s", gitSummary(participant.Validation)); err != nil {
				return err
			}
		}
	}

	if events := cycleEvents(run); len(events) > 0 {
		if err := write("\nLimits and sleeps:"); err != nil {
			return err
		}
		for _, event := range events {
			if err := write("  %s  %s", formatLocal(event.At, location), truncate(event.Detail, 96)); err != nil {
				return err
			}
		}
	}

	if len(report.Uncertainties) > 0 {
		if err := write("\nUncertain — please check:"); err != nil {
			return err
		}
		for _, item := range report.Uncertainties {
			if err := write("  - %s", truncate(item, 120)); err != nil {
				return err
			}
		}
	}

	if err := write("\nNext:"); err != nil {
		return err
	}
	if len(report.NextActions) == 0 {
		return write("  nothing outstanding")
	}
	for _, action := range report.NextActions {
		if err := write("  - %s", truncate(action, 120)); err != nil {
			return err
		}
	}
	return nil
}

// endingLabel describes how the run ended in words a person recognizes, and
// deliberately never says "shipped".
func endingLabel(reason model.TerminalReason, state model.RunState) string {
	switch reason {
	case model.ReasonQueueComplete:
		return "completed its implementation boundary"
	case model.ReasonDeadlineReached:
		return "stopped at the morning deadline"
	case model.ReasonCycleLimitReached:
		return "stopped at the resume ceiling"
	case model.ReasonManualCheckpoint:
		return "stopped at a manual checkpoint"
	case model.ReasonCanceled:
		return "was canceled"
	case model.ReasonBlocked:
		return "stopped on a blocker"
	case model.ReasonUncertain:
		return "ended with something uncertain"
	default:
		return state.Label()
	}
}

func participantEnding(participant model.ReportParticipant) string {
	switch participant.State {
	case model.ParticipantCompleted:
		return "implementation complete"
	case model.ParticipantReadyForReview:
		return "ready for review"
	case model.ParticipantWaitingManual:
		return "needs a decision"
	case model.ParticipantQueued:
		return "never started"
	case model.ParticipantUncertain:
		return "uncertain"
	default:
		return string(participant.State)
	}
}

func gitSummary(validation model.ValidationSummary) string {
	parts := []string{}
	if validation.Dirty {
		parts = append(parts, "uncommitted changes")
	}
	if validation.Ahead > 0 {
		parts = append(parts, fmt.Sprintf("%d ahead", validation.Ahead))
	}
	if validation.Behind > 0 {
		parts = append(parts, fmt.Sprintf("%d behind", validation.Behind))
	}
	if len(parts) == 0 {
		return "clean"
	}
	return strings.Join(parts, ", ")
}

// cycleEvents are the timeline entries a person cares about in the morning:
// what ran out, what slept, and what woke.
func cycleEvents(run model.OvernightRun) []model.RunEvent {
	var events []model.RunEvent
	for _, event := range run.Timeline {
		switch event.Kind {
		case "limit_detected", "preparing_sleep", "wake_verified", "slept",
			"early_wake", "late_wake", "resuming", "resume_acknowledged",
			"sleep_refused", "sleep_failed", "wake_withdrawn":
			events = append(events, event)
		}
	}
	return events
}

// ReportPayload is the JSON contract for a morning report.
func ReportPayload(run model.OvernightRun, report model.MorningReport) map[string]any {
	participants := make([]map[string]any, 0, len(report.Participants))
	for _, participant := range report.Participants {
		participants = append(participants, map[string]any{
			"position":           participant.Position,
			"feature":            participant.Feature,
			"role":               participant.Role,
			"state":              string(participant.State),
			"outcome":            string(participant.Outcome),
			"subtasks_before":    participant.SubtasksBefore,
			"subtasks_completed": participant.SubtasksAfter,
			"subtasks_total":     participant.SubtasksTotal,
			"manual_checkpoint":  participant.ManualCheckpoint,
			"commits":            participant.Validation.Commits,
			"dirty":              participant.Validation.Dirty,
			"ahead":              participant.Validation.Ahead,
			"behind":             participant.Validation.Behind,
			"recovery":           participant.Recovery,
		})
	}
	return map[string]any{
		"run_id":                 run.ID,
		"reason":                 string(report.Reason),
		"ending":                 endingLabel(report.Reason, run.State),
		"started_at":             report.StartedAt,
		"deadline_at":            report.DeadlineAt,
		"finished_at":            report.FinishedAt,
		"max_resumes":            report.MaxResumes,
		"acknowledged_resumes":   report.AcknowledgedResumes,
		"declined_credit_offers": report.DeclinedCreditOffers,
		"participants":           participants,
		"uncertainties":          report.Uncertainties,
		"next_actions":           report.NextActions,
	}
}

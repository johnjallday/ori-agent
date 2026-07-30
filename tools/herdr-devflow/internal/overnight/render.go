package overnight

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/model"
)

// This file writes the two things a person actually reads: the confirmation
// summary before a run exists, and the status views after it does.
//
// The confirmation screen is not a formality. It is the only moment where the
// consequences — one agent at a time, implementation work only, included plan
// capacity only, and a Mac that will go to sleep — are stated before anything
// can act on them. Nothing here is abbreviated away, and the two prominent
// warnings are printed whether or not the terminal has color.

// RenderConfirmation writes the full summary the user must approve.
func RenderConfirmation(out io.Writer, plan Plan) error {
	write := func(format string, args ...any) error {
		_, err := fmt.Fprintf(out, format+"\n", args...)
		return err
	}
	location := plan.location()

	if err := write("Overnight Run — review before confirming"); err != nil {
		return err
	}
	if err := write("\nQueue (one agent at a time, in this order):"); err != nil {
		return err
	}
	for _, participant := range plan.Participants {
		if err := renderPlannedParticipant(write, participant); err != nil {
			return err
		}
	}

	if len(plan.Excluded) > 0 {
		if err := write("\nNot enrolled:"); err != nil {
			return err
		}
		for _, excluded := range plan.Excluded {
			name := excluded.Feature
			if name == "" {
				name = "(" + excluded.Scope + " agent)"
			}
			if excluded.Role != "" {
				name += "/" + excluded.Role
			}
			if err := write("  %-34s %s", truncate(name, 34), excluded.Reason); err != nil {
				return err
			}
		}
	}

	if err := write("\nStart:         %s", formatLocal(plan.StartAt, location)); err != nil {
		return err
	}
	if err := write("Deadline:      %s (absolute; no new prompt or wake at or after it)", formatLocal(plan.DeadlineAt, location)); err != nil {
		return err
	}
	if err := write("Timezone:      %s", plan.Timezone); err != nil {
		return err
	}
	if err := write("Max resumes:   %d acknowledged post-reset continuations (currently 0)", plan.MaxResumes); err != nil {
		return err
	}
	if err := write("Queue head:    %s; the next agent starts only when it completes or stops for a non-limit reason", plan.headName()); err != nil {
		return err
	}
	if err := write("Reset rule:    each cycle uses the reset time Claude reports; never a calculated five hours"); err != nil {
		return err
	}
	if err := write("Billing:       included plan capacity only. This run never accepts or spends usage or API credits."); err != nil {
		return err
	}
	if err := write("Boundary:      implementation, tests, validation, and milestone commits"); err != nil {
		return err
	}
	if err := write("Stops before:  Demo, approval, credentials, PR, merge, deploy, wt done"); err != nil {
		return err
	}
	if err := write("Wake:          scheduled through Ori's shared macOS wake coordinator and verified before sleeping"); err != nil {
		return err
	}

	if err := write("\n!! A verified included-session limit will put THIS MAC to sleep."); err != nil {
		return err
	}
	if err := write("!! Every other process on it is suspended by macOS until the reset wake."); err != nil {
		return err
	}
	if err := write("!! Unsaved work in other applications is your responsibility."); err != nil {
		return err
	}

	for _, warning := range plan.Warnings {
		if err := write("\nWarning: %s", warning); err != nil {
			return err
		}
	}
	for _, conflict := range plan.Conflicts {
		if err := write("\nBlocked: %s", conflict); err != nil {
			return err
		}
	}
	return nil
}

func renderPlannedParticipant(write func(string, ...any) error, participant PlannedParticipant) error {
	label := fmt.Sprintf("  [%d] %s", participant.Position, participant.Feature.Name)
	if participant.Binding.Role != "" {
		label += " / " + participant.Binding.Role
	}
	state := "idle"
	if participant.Working {
		state = "working"
	}
	if err := write("%s (%s, %s)", label, participant.Binding.AgentKind, state); err != nil {
		return err
	}
	if err := write("      worktree: %s", participant.Feature.Path); err != nil {
		return err
	}
	if err := write("      session:  %s", identityOrUnknown(participant.Binding.NativeSession.Value)); err != nil {
		return err
	}
	checkpoint := participant.Checkpoint
	progress := "no readable task list"
	if checkpoint.TaskListPath != "" {
		progress = fmt.Sprintf("%d/%d subtasks", checkpoint.SubtasksCompleted, checkpoint.SubtasksTotal)
	}
	if err := write("      progress: %s", progress); err != nil {
		return err
	}
	next := "none — nothing safe to continue"
	if checkpoint.NextOrdinal != "" {
		next = checkpoint.NextOrdinal + " " + truncate(checkpoint.NextText, 72)
	} else if checkpoint.ImplementationComplete {
		next = "implementation complete; only delivery checkpoints remain"
	}
	if err := write("      next:     %s", next); err != nil {
		return err
	}
	manual := "none found"
	if checkpoint.ManualOrdinal != "" {
		manual = checkpoint.ManualOrdinal + " " + truncate(checkpoint.ManualText, 72)
	}
	return write("      stops at: %s", manual)
}

// RenderRunList writes one line per run.
func RenderRunList(out io.Writer, runs []model.OvernightRun) error {
	if len(runs) == 0 {
		_, err := fmt.Fprintln(out, "No Overnight Runs have been created for this repository.")
		return err
	}
	for _, run := range runs {
		active := "—"
		if participant, ok := run.Active(); ok {
			active = participant.Feature.Name
		}
		if _, err := fmt.Fprintf(out, "%s  %-20s  %d participant(s)  head %s  deadline %s\n",
			run.ID, run.State.Label(), len(run.Participants), active,
			formatLocal(run.DeadlineAt, loadLocation(run.Timezone))); err != nil {
			return err
		}
	}
	return nil
}

// RenderRun writes one run in full: configuration, queue, cycle accounting,
// wake state, and what to do next. Prompt bodies and terminal output are never
// part of it.
func RenderRun(out io.Writer, run model.OvernightRun) error {
	write := func(format string, args ...any) error {
		_, err := fmt.Fprintf(out, format+"\n", args...)
		return err
	}
	location := loadLocation(run.Timezone)

	if err := write("Overnight Run %s — %s", run.ID, run.State.Label()); err != nil {
		return err
	}
	if err := write("  started:  %s", formatLocal(run.StartAt, location)); err != nil {
		return err
	}
	if err := write("  deadline: %s", formatLocal(run.DeadlineAt, location)); err != nil {
		return err
	}
	if err := write("  resumes:  %d of %d acknowledged", run.AcknowledgedResumes, run.MaxResumes); err != nil {
		return err
	}
	if err := write("  approved: %s", run.Confirmation); err != nil {
		return err
	}
	if run.Wake.CandidateID != "" {
		state := "registered"
		switch {
		case run.Wake.Canceled:
			state = "withdrawn"
		case run.Wake.Uncertain:
			state = "uncertain"
		case run.Wake.Verified:
			state = "verified"
		}
		if err := write("  wake:     %s at %s (%s)", state, formatLocal(run.Wake.RequestedAt, location), run.Wake.CandidateID); err != nil {
			return err
		}
	}
	if run.TerminalReason != "" {
		if err := write("  ended:    %s", run.TerminalReason); err != nil {
			return err
		}
	}
	if run.Uncertainty != "" {
		if err := write("  UNCERTAIN: %s", run.Uncertainty); err != nil {
			return err
		}
	}

	if err := write("\nQueue:"); err != nil {
		return err
	}
	for _, participant := range run.Participants {
		if err := renderRunParticipant(write, participant, location); err != nil {
			return err
		}
	}
	if len(run.Timeline) == 0 {
		return nil
	}
	if err := write("\nTimeline:"); err != nil {
		return err
	}
	for _, event := range run.Timeline {
		line := "  " + formatLocal(event.At, location) + "  " + event.Kind
		if event.Detail != "" {
			line += " — " + truncate(event.Detail, 96)
		}
		if err := write("%s", line); err != nil {
			return err
		}
	}
	return nil
}

func renderRunParticipant(write func(string, ...any) error, participant model.RunParticipant, location *time.Location) error {
	label := fmt.Sprintf("  [%d] %-28s %s", participant.Position,
		truncate(participant.Feature.Name, 28), string(participant.State))
	if err := write("%s", label); err != nil {
		return err
	}
	if participant.Checkpoint.TaskListPath != "" {
		if err := write("      progress: %d/%d subtasks",
			participant.Checkpoint.SubtasksCompleted, participant.Checkpoint.SubtasksTotal); err != nil {
			return err
		}
	}
	if participant.Limit != nil {
		detail := participant.Limit.Class
		if !participant.Limit.ResetAt.IsZero() {
			detail += ", resets " + formatLocal(participant.Limit.ResetAt, location)
		}
		if !participant.Limit.Sleepable && participant.Limit.Detail != "" {
			detail += " — " + truncate(participant.Limit.Detail, 96)
		}
		if err := write("      limit:    %s", detail); err != nil {
			return err
		}
	}
	if participant.AcknowledgedResumes > 0 {
		if err := write("      resumes:  %d acknowledged", participant.AcknowledgedResumes); err != nil {
			return err
		}
	}
	if participant.Delivery.State != "" {
		// The summary is bounded and never the prompt itself.
		if err := write("      prompt:   %s (%s)", participant.Delivery.State,
			truncate(participant.Delivery.Summary, 64)); err != nil {
			return err
		}
	}
	if participant.Recovery != "" {
		return write("      next:     %s", truncate(participant.Recovery, 96))
	}
	return nil
}

// location resolves a plan's display zone, falling back to UTC rather than
// guessing a different one.
func (p Plan) location() *time.Location { return loadLocation(p.Timezone) }

func (p Plan) headName() string {
	if len(p.Participants) == 0 {
		return "—"
	}
	return p.Participants[0].Feature.Name
}

func loadLocation(name string) *time.Location {
	if name == "" {
		return time.UTC
	}
	location, err := time.LoadLocation(name)
	if err != nil {
		return time.UTC
	}
	return location
}

func formatLocal(value time.Time, location *time.Location) string {
	if value.IsZero() {
		return "—"
	}
	return value.In(location).Format("2006-01-02 15:04 MST")
}

func identityOrUnknown(value string) string {
	if strings.TrimSpace(value) == "" {
		return "unknown"
	}
	return value
}

func truncate(value string, limit int) string {
	runes := []rune(value)
	if limit <= 0 || len(runes) <= limit {
		return value
	}
	return string(runes[:limit-1]) + "…"
}

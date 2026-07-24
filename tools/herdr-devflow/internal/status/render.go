package status

import (
	"bytes"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/model"
)

type RenderOptions struct {
	Color bool
	Now   time.Time
}

// RenderHuman produces a compact terminal board. Colors reinforce the text
// status labels and are optional so logs and screen readers retain meaning.
func RenderHuman(snapshot Snapshot, options RenderOptions) string {
	if options.Now.IsZero() {
		options.Now = snapshot.GeneratedAt
	}
	var output bytes.Buffer
	if snapshot.Stale {
		fmt.Fprintf(&output, "Ori Devflow status: STALE — %s\n", sanitize(snapshot.Detail, 120))
	} else {
		fmt.Fprintf(&output, "Ori Devflow status: %d managed agent(s)\n", len(snapshot.Rows))
	}
	writer := tabwriter.NewWriter(&output, 0, 2, 2, ' ', 0)
	fmt.Fprintln(writer, "FEATURE\tROLE\tAGENT\tKIND\tSTATUS\tTASK\tGIT\tLAST ACTIVITY\tNEXT INCOMPLETE\tSCHEDULE")
	for _, row := range snapshot.Rows {
		observed := decorateStatus(row, options.Color)
		task := row.Task.Label()
		git := gitLabel(row.Git)
		activity := activityLabel(row.LastActivityAt, options.Now)
		next := sanitize(row.Task.Next, 52)
		schedule := scheduleLabel(row.NextSchedule, options.Now)
		fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			sanitize(row.Feature, 24),
			sanitize(row.Role, 16),
			sanitize(row.AgentName, 30),
			sanitize(row.Kind, 12),
			observed,
			sanitize(task, 12),
			git,
			activity,
			next,
			schedule,
		)
	}
	_ = writer.Flush()
	return output.String()
}

func decorateStatus(row AgentRow, color bool) string {
	status := string(row.ObservedStatus)
	if row.Missing {
		status = "missing"
	}
	label := statusSymbol(status) + " " + status
	if row.Stale {
		label += " (stale)"
	}
	if !color {
		return label
	}
	code := "\x1b[36m" // idle/default cyan
	switch status {
	case "blocked", "missing":
		code = "\x1b[31m"
	case "unknown":
		code = "\x1b[33m"
	case "working":
		code = "\x1b[35m"
	case "done":
		code = "\x1b[32m"
	}
	return code + label + "\x1b[0m"
}

func statusSymbol(status string) string {
	switch status {
	case "blocked", "missing":
		return "!"
	case "unknown":
		return "?"
	case "working":
		return ">"
	case "done":
		return "+"
	default:
		return "-"
	}
}

func gitLabel(state GitState) string {
	if state.Stale {
		return "unknown"
	}
	if state.Dirty {
		if state.Ahead > 0 {
			return fmt.Sprintf("dirty +%d", state.Ahead)
		}
		return "dirty"
	}
	if state.Ahead > 0 {
		return fmt.Sprintf("clean +%d", state.Ahead)
	}
	return "clean"
}

func scheduleLabel(schedule *ScheduleSummary, now time.Time) string {
	if schedule == nil {
		return "—"
	}
	when := schedule.DueAt.Format("Jan 02 15:04")
	if !now.IsZero() && schedule.DueAt.Before(now) && schedule.State.IsUnresolved() {
		when = "due " + when
	}
	return scheduleSymbol(schedule.State) + " " + sanitize(schedule.ID, 16) + " " + string(schedule.State) + " " + when
}

func scheduleSymbol(state model.ScheduleState) string {
	switch state {
	case model.ScheduleFailed, model.ScheduleUncertain:
		return "!"
	case model.ScheduleDelivered:
		return "+"
	case model.ScheduleWaiting, model.ScheduleDelivering:
		return ">"
	default:
		return "@"
	}
}

func activityLabel(value, now time.Time) string {
	if value.IsZero() {
		return "—"
	}
	if now.IsZero() || value.After(now) {
		return value.Format("Jan 02 15:04")
	}
	age := now.Sub(value)
	switch {
	case age < time.Minute:
		return "now"
	case age < time.Hour:
		return fmt.Sprintf("%dm ago", int(age.Minutes()))
	case age < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(age.Hours()))
	default:
		return value.Format("Jan 02 15:04")
	}
}

func sanitize(value string, limit int) string {
	var builder strings.Builder
	space := false
	for _, runeValue := range value {
		if runeValue < 32 || runeValue == 127 {
			space = true
			continue
		}
		if unicodeSpace(runeValue) {
			space = true
			continue
		}
		if space && builder.Len() > 0 {
			builder.WriteByte(' ')
		}
		space = false
		builder.WriteRune(runeValue)
		if limit > 0 && len([]rune(builder.String())) >= limit {
			break
		}
	}
	result := strings.TrimSpace(builder.String())
	if limit > 0 && len([]rune(value)) > len([]rune(result)) && len([]rune(result)) >= limit {
		return strings.TrimSpace(string([]rune(result)[:limit])) + "…"
	}
	return result
}

func unicodeSpace(value rune) bool {
	return value == ' ' || value == '\t' || value == '\n' || value == '\r'
}

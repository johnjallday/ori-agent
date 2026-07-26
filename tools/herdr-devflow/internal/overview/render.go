package overview

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"
)

// RenderOptions controls human output. Colour is never the only carrier of
// meaning: every phase, availability, and severity also prints as full text.
type RenderOptions struct {
	// NoColor suppresses styling entirely. It is set by --no-color and by the
	// NO_COLOR environment variable.
	NoColor bool
}

const (
	// placeholderUnknown marks a column whose collector has not run.
	placeholderUnknown = "—"
	// maxTitleColumn bounds the feature column.
	maxTitleColumn = 34
)

// RenderCompact writes the feature-first overview table: one row per feature,
// grouped attention first and terminal history last.
//
// Columns that a later slice fills in are rendered as explicit placeholders
// rather than being omitted, so the table's shape does not change as
// collectors are added and a reader can see what is not yet known.
func RenderCompact(out io.Writer, snapshot Snapshot, options RenderOptions) error {
	if len(snapshot.Features) == 0 {
		return renderEmpty(out, snapshot)
	}

	writer := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(writer, "FEATURE\tPHASE\tPLAN\tGIT\tREMOTE\tAGENT\tATTENTION"); err != nil {
		return err
	}

	for _, feature := range snapshot.Features {
		// Terminal history is grouped last by the shared sort and named by the
		// PHASE column. A blank separator row is deliberately not emitted:
		// tabwriter would pad it into a line of trailing whitespace, and
		// splitting the table into two blocks would let the two halves align
		// to different column widths.
		row := strings.Join([]string{
			truncate(feature.Slug, maxTitleColumn),
			phaseCell(feature.Phase),
			planCell(feature.Plan),
			gitCell(feature.Git),
			remoteCell(feature.Remote),
			agentCell(feature),
			attentionCell(feature),
		}, "\t")
		if _, err := fmt.Fprintln(writer, row); err != nil {
			return err
		}
	}
	if err := writer.Flush(); err != nil {
		return err
	}
	return renderFooter(out, snapshot)
}

func renderEmpty(out io.Writer, snapshot Snapshot) error {
	if _, err := fmt.Fprintln(out, "No features were found in this repository."); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(out, "Looked for planning artifacts, BACKLOG.md entries, and feature worktrees."); err != nil {
		return err
	}
	return renderFooter(out, snapshot)
}

// renderFooter states the snapshot's completeness and every repository-scoped
// finding. An incomplete snapshot must say so; silence would read as health.
func renderFooter(out io.Writer, snapshot Snapshot) error {
	if _, err := fmt.Fprintln(out); err != nil {
		return err
	}
	status := "complete"
	if !snapshot.Complete {
		status = "INCOMPLETE"
	}
	if snapshot.Stale {
		status += " (stale)"
	}
	line := "Snapshot: " + status + " · generated " + snapshot.GeneratedAt.Format("2006-01-02 15:04:05")
	if _, err := fmt.Fprintln(out, line); err != nil {
		return err
	}

	for _, source := range snapshot.Sources {
		if source.Availability.OK() {
			continue
		}
		detail := source.Detail
		if detail == "" {
			detail = source.Availability.Label()
		}
		marker := "  - "
		if source.Required {
			marker = "  ! "
		}
		if _, err := fmt.Fprintln(out, marker+string(source.Kind)+": "+detail); err != nil {
			return err
		}
	}
	for _, finding := range snapshot.Findings {
		if _, err := fmt.Fprintln(out, "  ["+finding.Severity.Label()+"] "+finding.Message); err != nil {
			return err
		}
	}
	return nil
}

func phaseCell(state PhaseState) string {
	label := state.Phase.Label()
	if !state.Confirmed {
		// An unconfirmed phase is a local guess. Saying so is the difference
		// between a diagnostic and a lie.
		label += " (unconfirmed)"
	}
	return label
}

func planCell(plan Plan) string {
	switch {
	case plan.Copy == PlanCopyNone:
		return "no plan"
	case plan.PRDAvailability == AvailabilityAbsent && plan.TaskListAvailability != AvailabilityAbsent:
		return "no PRD"
	case plan.TaskListAvailability == AvailabilityAbsent && plan.PRDAvailability != AvailabilityAbsent:
		return "no task list"
	case plan.PRDAvailability == AvailabilityMalformed || plan.TaskListAvailability == AvailabilityMalformed:
		return "malformed"
	case plan.Progress.Availability.OK():
		return progressCell(plan.Progress)
	default:
		// Hierarchical progress arrives with the plan-parsing slice.
		return placeholderUnknown
	}
}

func progressCell(progress PlanProgress) string {
	cell := strconv.Itoa(progress.MilestonesCompleted) + "/" + strconv.Itoa(progress.MilestonesTotal) + " milestones"
	if progress.SubtasksTotal > 0 {
		cell += " · " + strconv.Itoa(progress.SubtasksCompleted) + "/" + strconv.Itoa(progress.SubtasksTotal) + " subtasks"
	}
	if progress.NextActionable.Ordinal != "" {
		cell += " · next " + progress.NextActionable.Ordinal
	}
	return cell
}

func gitCell(git GitState) string {
	switch git.Availability {
	case AvailabilityAbsent:
		return "no worktree"
	case AvailabilityUnavailable:
		return "unavailable"
	case AvailabilityUnknown:
		return placeholderUnknown
	}
	parts := []string{}
	if git.DivergenceAvailability.OK() {
		parts = append(parts, "+"+strconv.Itoa(git.Ahead)+"/-"+strconv.Itoa(git.Behind))
	} else {
		parts = append(parts, "divergence unavailable")
	}
	switch {
	case !git.DirtyAvailability.OK():
		parts = append(parts, "state unavailable")
	case git.Dirty:
		parts = append(parts, "dirty")
	default:
		parts = append(parts, "clean")
	}
	return strings.Join(parts, " ")
}

func remoteCell(remote Remote) string {
	switch remote.Availability {
	case AvailabilityUnavailable:
		return "unavailable"
	case AvailabilityAbsent:
		return "no PR"
	case AvailabilityStale:
		return "stale"
	case AvailabilityUnknown:
		return placeholderUnknown
	}
	if remote.PullRequest == nil {
		return "no PR"
	}
	cell := "#" + strconv.Itoa(remote.PullRequest.Number)
	if remote.PullRequest.Draft {
		cell += " draft"
	}
	return cell + " " + remote.PullRequest.Checks.Label()
}

func agentCell(feature Feature) string {
	if len(feature.Agents) == 0 {
		return placeholderUnknown
	}
	statuses := make([]string, 0, len(feature.Agents))
	for _, agent := range feature.Agents {
		if !agent.StatusAvailability.OK() {
			statuses = append(statuses, "unavailable")
			continue
		}
		statuses = append(statuses, string(agent.Status))
	}
	return strings.Join(statuses, ", ")
}

// attentionCell shows the highest-severity finding for a feature, with a count
// when more than one exists, so nothing is hidden behind a single line.
func attentionCell(feature Feature) string {
	severity, has := feature.Attention()
	if !has {
		return "ok"
	}
	cell := severity.Label() + ": " + string(feature.Findings[0].Code)
	if extra := len(feature.Findings) - 1; extra > 0 {
		cell += " (+" + strconv.Itoa(extra) + ")"
	}
	return cell
}

func truncate(value string, limit int) string {
	runes := []rune(value)
	if limit <= 0 || len(runes) <= limit {
		return value
	}
	return string(runes[:limit-1]) + "…"
}

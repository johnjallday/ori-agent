package personalassistant

import (
	"strings"

	"github.com/johnjallday/ori-agent/internal/softwareassessment"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// A hire from the Agents page starts a first look on its new workspace: a goal
// run with origin workspace.MissionOriginHireFirstLook, whose run record keeps
// a short summary ending in a "Next:" line and one sentence on why that task
// comes first. The report reads them back from the record, never from the
// browser.

// HireLookRun is the hire's first-look run as its workspace recorded it once
// the run finished; nil while it runs, or when the hire has no run.
func HireLookRun(ws *workspace.Workspace, runID string) *workspace.MissionRunRecord {
	runID = strings.TrimSpace(runID)
	if ws == nil || runID == "" {
		return nil
	}
	for i := range ws.MissionRuns {
		if ws.MissionRuns[i].RunID == runID {
			return &ws.MissionRuns[i]
		}
	}
	return nil
}

// HireLookSuggestionOf is what a finished first look suggests doing first: its
// "Next:" line, read with the same parser as a folder's first look, and the
// look's one sentence on why — or, when it gave none, the why of the Backlog
// suggestion it names. A suggestion the user already turned down in this
// workspace is not offered again. nil when there is none.
func HireLookSuggestionOf(ws *workspace.Workspace, run *workspace.MissionRunRecord) *FirstLookSuggestion {
	if ws == nil || run == nil || !run.Succeeded {
		return nil
	}
	text := softwareassessment.NextTask(run.Summary)
	if text == "" {
		return nil
	}
	if _, dismissed := ws.DismissedSuggestionKeys()[workspace.NormalizeSuggestionTitle(text)]; dismissed {
		return nil
	}
	why := strings.TrimSpace(run.NextWhy)
	if why == "" {
		if ticket := HireLookSuggestionTicket(ws, run, text); ticket != nil {
			if reason, ok := ticket.Context[workspace.MissionSuggestionContextKey].(map[string]any); ok {
				why, _ = reason["why"].(string)
			}
		}
	}
	return &FirstLookSuggestion{Text: text, Evidence: truncateRunes(strings.Join(strings.Fields(why), " "), firstLookEvidenceMax)}
}

// HireLookSuggestionTicket is the Backlog suggestion the look wrote for its
// next task, when it wrote one under that title.
func HireLookSuggestionTicket(ws *workspace.Workspace, run *workspace.MissionRunRecord, text string) *workspace.Task {
	if ws == nil || run == nil || run.RunID == "" {
		return nil
	}
	key := workspace.NormalizeSuggestionTitle(text)
	for i := range ws.Tasks {
		task := &ws.Tasks[i]
		if task.SourceType == workspace.TicketSourceGoalSuggestion && task.SourceID == run.RunID &&
			workspace.NormalizeSuggestionTitle(task.Description) == key {
			return task
		}
	}
	return nil
}

// HireLookSummary is what the look found, without its "Next:" line.
func HireLookSummary(run *workspace.MissionRunRecord) string {
	if run == nil {
		return ""
	}
	var kept []string
	for _, line := range strings.Split(run.Summary, "\n") {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(strings.TrimLeft(trimmed, "*_>#- "))
		if trimmed == "" || strings.HasPrefix(lower, "next:") || strings.HasPrefix(lower, "next task:") {
			continue
		}
		kept = append(kept, trimmed)
	}
	return truncateRunes(strings.Join(kept, " "), 400)
}

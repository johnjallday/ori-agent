package personalassistant

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/dailybrief"
	"github.com/johnjallday/ori-agent/internal/softwareassessment"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// A hired agent's first look reports back to the assistant (folder-hire-brief
// FR16-FR21): once it finishes, the assistant shows what it found and the one
// task it suggests first under Needs you, with Start this and Not now. What the
// user did with that report is kept on the look's own task.
const (
	// FirstLookSuggestionTaskKey on the look names the task Start this made.
	FirstLookSuggestionTaskKey = "first_look_suggestion_task_id"
	// FirstLookReportDismissedKey on the look is when Not now was pressed.
	FirstLookReportDismissedKey = "first_look_report_dismissed_at"
	// FirstLookOriginTaskKey on a started suggestion names the look it came from.
	FirstLookOriginTaskKey = "first_look_task_id"
)

// Today item kinds for a hired agent's report and the task it started.
const (
	TodayKindFirstLookReport     = "folder_first_look"
	TodayKindFirstLookSuggestion = "first_look_suggestion"
)

// The actions a first look's report offers in place.
const (
	FirstLookActionStartThis = "start_this"
	FirstLookActionNotNow    = "not_now"
)

// Errors of the report's two actions.
var (
	ErrFirstLookNotFound     = errors.New("personal assistant: no such first look")
	ErrFirstLookNotFinished  = errors.New("personal assistant: the first look has not finished")
	ErrFirstLookNoSuggestion = errors.New("personal assistant: the first look suggested no task")
	ErrFirstLookActedOn      = errors.New("personal assistant: the first look's suggestion was already started or put aside")
)

// FirstLookSuggestion is the one task a finished first look suggests first, and
// the one line of evidence it gave for it. Both are text the model wrote; they
// are shown as text and re-read from the look's result whenever they are used.
type FirstLookSuggestion struct {
	Text     string `json:"text"`
	Evidence string `json:"evidence,omitempty"`
}

const firstLookEvidenceMax = 200

// FirstLookSuggestionOf reads what a finished first look suggests first: its
// closing "Next:" (or "Next task:") line, with the assessment's reason for it
// when it gave one. A suggestion the user already turned down in this
// workspace is not offered again. nil when there is none.
func FirstLookSuggestionOf(ws *workspace.Workspace, task *workspace.Task) *FirstLookSuggestion {
	if task == nil || FolderFirstTaskStateOf(task) != FolderFirstTaskFinished {
		return nil
	}
	text := softwareassessment.NextTask(task.Result)
	if text == "" {
		return nil
	}
	if _, dismissed := ws.DismissedSuggestionKeys()[workspace.NormalizeSuggestionTitle(text)]; dismissed {
		return nil
	}
	return &FirstLookSuggestion{Text: text, Evidence: firstLookEvidence(task.Result, text)}
}

// firstLookEvidence is the assessment's one sentence on why its suggested task
// is a good first step: the text under "Suggested first task" other than the
// task itself and the closing line. A generic look has no such section.
func firstLookEvidence(result, suggestion string) string {
	lines := strings.Split(strings.ReplaceAll(result, "\r\n", "\n"), "\n")
	in := false
	var parts []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			if in {
				break
			}
			in = strings.EqualFold(strings.TrimSpace(strings.TrimLeft(trimmed, "#")), "Suggested first task")
			continue
		}
		if in && trimmed == "---" {
			// Ori's own note on what the report was based on follows; it is
			// not the agent's reason.
			break
		}
		lower := strings.ToLower(strings.TrimLeft(trimmed, "*_>- "))
		if !in || trimmed == "" || strings.HasPrefix(lower, "next task:") || strings.HasPrefix(lower, "next:") ||
			strings.HasPrefix(lower, "what this is based on") {
			continue
		}
		parts = append(parts, trimmed)
	}
	// The task is named again in the section, often with code marks the
	// closing line dropped; only the reason around it is evidence.
	plain := func(text string) string { return strings.Join(strings.Fields(strings.ReplaceAll(text, "`", "")), " ") }
	evidence := strings.TrimSpace(strings.ReplaceAll(plain(strings.Join(parts, " ")), plain(suggestion), ""))
	evidence = strings.Trim(strings.TrimSpace(evidence), "*_ ")
	if evidence == "" || strings.EqualFold(evidence, strings.TrimSuffix(plain(suggestion), ".")) {
		return ""
	}
	return truncateRunes(evidence, firstLookEvidenceMax)
}

// firstLookSummary is at most two sentences of what the look found, from the
// first paragraph of its report (headings skipped).
func firstLookSummary(task *workspace.Task) string {
	excerpt := strings.TrimSuffix(FolderFirstTaskRecipeExcerpt(task.Result), "…")
	sentences := 0
	for i, r := range excerpt {
		if r == '.' || r == '!' || r == '?' {
			next := excerpt[i+1:]
			if next == "" || strings.HasPrefix(next, " ") {
				sentences++
				if sentences == 2 {
					return strings.TrimSpace(excerpt[:i+1])
				}
			}
		}
	}
	return strings.TrimSpace(excerpt)
}

// firstLookReportItems is a hired agent's first look as Today shows it once it
// has finished: the report under Needs you until the user acts on it; after
// Start this, the task it started under Working on and then Done; after Not
// now, nothing. handled is false while the look has not finished, so the
// caller keeps its "First look at" item under Working on.
func firstLookReportItems(offer FolderOffer, ws *workspace.Workspace, route string, now time.Time) (needs, working, done []TodayItem, handled bool) {
	look := FindFolderFirstTask(ws)
	if look == nil || offer.Outcome == nil || offer.Outcome.FirstLook == nil || FolderFirstTaskStateAt(look, now) != FolderFirstTaskFinished {
		return nil, nil, nil, false
	}
	agent := truncateRunes(strings.TrimSpace(look.To), 100)
	if started, _ := look.Context[FirstLookSuggestionTaskKey].(string); started != "" {
		task := findTask(ws, started)
		if task == nil {
			return nil, nil, nil, true
		}
		item := TodayItem{ID: "first-look-suggestion-" + task.ID, Kind: TodayKindFirstLookSuggestion,
			Title: truncateRunes(task.Description, 200), Attribution: agent, Workspace: truncateRunes(ws.Name, 120),
			Route: route + "/task/" + url.PathEscape(task.ID),
			Ref:   dailybrief.SourceRef{WorkspaceID: ws.ID, EntityType: "task", EntityID: task.ID}}
		switch task.CanonicalState() {
		case workspace.TicketStateReview, workspace.TicketStateDone:
			at := taskSourceTime(*task)
			if at.After(now) || now.Sub(at) > todayFolderReceiptWindow {
				return nil, nil, nil, true
			}
			item.Detail, item.State, item.SourceAt, item.Ref.Timestamp = FolderFirstTaskExcerpt(task.Result), string(task.CanonicalState()), at, at
			return nil, nil, []TodayItem{item}, true
		case workspace.TicketStateCancelled:
			return nil, nil, nil, true
		default:
			item.Detail = "Started from its first look"
			if task.NeedsAttention() {
				item.State = "needs_attention"
			}
			return nil, []TodayItem{item}, nil, true
		}
	}
	if _, dismissed := look.Context[FirstLookReportDismissedKey]; dismissed {
		return nil, nil, nil, true
	}
	at := taskSourceTime(*look)
	if at.After(now) || now.Sub(at) > todayFolderReceiptWindow {
		return nil, nil, nil, true
	}
	item := TodayItem{ID: "first-look-report-" + look.ID, Kind: TodayKindFirstLookReport,
		Title: "Finished its first look", Detail: truncateRunes(firstLookSummary(look), 400),
		Attribution: agent, Workspace: truncateRunes(ws.Name, 120), State: "finished",
		Route: route + "/task/" + url.PathEscape(look.ID), ReportRoute: route + "/task/" + url.PathEscape(look.ID),
		SourceAt: at, Actions: []string{FirstLookActionNotNow},
		Ref: dailybrief.SourceRef{WorkspaceID: ws.ID, EntityType: "task", EntityID: look.ID, Timestamp: at}}
	if suggestion := FirstLookSuggestionOf(ws, look); suggestion != nil {
		item.Suggestion = suggestion
		item.Actions = []string{FirstLookActionStartThis, FirstLookActionNotNow}
	}
	if parent := offer.Outcome.Parent; parent != nil {
		item.Home = truncateRunes(strings.TrimSpace(parent.Name), 120)
	}
	return []TodayItem{item}, nil, nil, true
}

func findTask(ws *workspace.Workspace, id string) *workspace.Task {
	for i := range ws.Tasks {
		if ws.Tasks[i].ID == id {
			return &ws.Tasks[i]
		}
	}
	return nil
}

// FirstLookStarted is the task Start this made and started.
type FirstLookStarted struct {
	TaskID      string `json:"task_id"`
	WorkspaceID string `json:"workspace_id"`
	Route       string `json:"route"`
}

// FirstLookActions are Start this and Not now on a hired agent's report. The
// look is found among the user's own folder setups, never from a browser's
// word, and the suggestion is read again from the look's own result.
type FirstLookActions struct {
	Digest     *FolderDigestService
	Workspaces workspace.Store
	// Start starts a Ready task through the manual-execution path (the same
	// one Start first look uses).
	Start func(workspaceID, taskID string) error
	Now   func() time.Time
}

func (a *FirstLookActions) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

// hiredLook finds the user's hired first look by its task id: a resolved
// project setup that hired someone, whose workspace's first look it is.
func (a *FirstLookActions) hiredLook(ctx context.Context, userID, lookTaskID string) (*workspace.Workspace, *workspace.Task, error) {
	lookTaskID = strings.TrimSpace(lookTaskID)
	if a == nil || a.Digest == nil || a.Digest.store == nil || a.Workspaces == nil || lookTaskID == "" {
		return nil, nil, ErrFirstLookNotFound
	}
	doc, err := a.Digest.store.Read(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	for _, offer := range doc.Offers {
		if offer.Status != FolderOfferResolved || offer.Outcome == nil || offer.Outcome.FirstLook == nil || offer.Outcome.WorkspaceID == "" {
			continue
		}
		ws, err := a.Workspaces.Get(offer.Outcome.WorkspaceID)
		if err != nil || ws == nil {
			continue
		}
		if look := FindFolderFirstTask(ws); look != nil && look.ID == lookTaskID {
			return ws, look, nil
		}
	}
	return nil, nil, ErrFirstLookNotFound
}

// StartSuggestion is Start this: the look's suggested task is created in its
// workspace, assigned to the agent that made the look, made Ready and started
// at once. The click is the confirmation. The task runs under the workspace's
// own autonomy (Watch unless the user chose otherwise): it reads and reports.
// A second click finds the task already started and starts nothing.
func (a *FirstLookActions) StartSuggestion(ctx context.Context, userID, lookTaskID string) (FirstLookStarted, error) {
	ws, look, err := a.hiredLook(ctx, userID, lookTaskID)
	if err != nil {
		return FirstLookStarted{}, err
	}
	if FolderFirstTaskStateOf(look) != FolderFirstTaskFinished {
		return FirstLookStarted{}, ErrFirstLookNotFinished
	}
	route := "/workspaces/" + url.PathEscape(ws.FolderSlug)
	if started, _ := look.Context[FirstLookSuggestionTaskKey].(string); started != "" {
		return FirstLookStarted{TaskID: started, WorkspaceID: ws.ID, Route: route + "/task/" + url.PathEscape(started)}, ErrFirstLookActedOn
	}
	if _, dismissed := look.Context[FirstLookReportDismissedKey]; dismissed {
		return FirstLookStarted{}, ErrFirstLookActedOn
	}
	suggestion := FirstLookSuggestionOf(ws, look)
	if suggestion == nil {
		return FirstLookStarted{}, ErrFirstLookNoSuggestion
	}
	if a.Start == nil {
		return FirstLookStarted{}, ErrFolderOutcomeUnavailable
	}
	taskID := uuid.NewString()
	now := a.now()
	err = workspace.CanonicalUpdate(a.Workspaces, ws.ID, func(current *workspace.Workspace) error {
		origin := findTask(current, look.ID)
		if origin == nil {
			return ErrFirstLookNotFound
		}
		if started, _ := origin.Context[FirstLookSuggestionTaskKey].(string); started != "" {
			taskID = started
			return ErrFirstLookActedOn
		}
		task := workspace.Task{
			ID: taskID, WorkspaceID: current.ID, Description: suggestion.Text,
			Details:    "Suggested by " + strings.TrimSpace(look.To) + "'s first look. Read and report; change nothing.",
			Priority:   workspace.TicketPriorityDefault,
			Status:     workspace.TaskStatusPending,
			SourceType: workspace.TicketSourceFirstLookSuggestion, SourceID: look.ID,
			From: "Ori", CreatedAt: now,
			Context: map[string]any{
				FirstLookOriginTaskKey:            look.ID,
				workspace.TaskContextAutonomyGate: workspace.TaskAutonomyGateWorkspace,
			},
		}
		if err := current.ApplyTaskAssignment(&task, workspace.TaskAssignment{
			AgentName: strings.TrimSpace(look.To), Mode: workspace.TaskAssignmentModeManual,
			AssignedBy: workspace.TaskAssignedByManual, Reason: "Suggested by its first look",
		}); err != nil {
			return err
		}
		if err := current.AddTask(task); err != nil {
			return err
		}
		origin.Context[FirstLookSuggestionTaskKey] = taskID
		return nil
	})
	if errors.Is(err, ErrFirstLookActedOn) {
		return FirstLookStarted{TaskID: taskID, WorkspaceID: ws.ID, Route: route + "/task/" + url.PathEscape(taskID)}, err
	}
	if err != nil {
		return FirstLookStarted{}, err
	}
	started := FirstLookStarted{TaskID: taskID, WorkspaceID: ws.ID, Route: route + "/task/" + url.PathEscape(taskID)}
	if err := a.Start(ws.ID, taskID); err != nil {
		// The task stays Ready and assigned in its workspace, where it can be
		// started by hand; the report has done its part.
		return started, err
	}
	return started, nil
}

// Dismiss is Not now: the report leaves Needs you and Today, the suggestion's
// title goes into the workspace's dismissed suggestions so later goal runs do
// not suggest it again, and the report stays readable from the workspace.
func (a *FirstLookActions) Dismiss(ctx context.Context, userID, lookTaskID string) error {
	ws, look, err := a.hiredLook(ctx, userID, lookTaskID)
	if err != nil {
		return err
	}
	if started, _ := look.Context[FirstLookSuggestionTaskKey].(string); started != "" {
		return ErrFirstLookActedOn
	}
	suggestion := FirstLookSuggestionOf(ws, look)
	now := a.now()
	return workspace.CanonicalUpdate(a.Workspaces, ws.ID, func(current *workspace.Workspace) error {
		origin := findTask(current, look.ID)
		if origin == nil {
			return ErrFirstLookNotFound
		}
		if origin.Context == nil {
			origin.Context = map[string]any{}
		}
		if _, done := origin.Context[FirstLookReportDismissedKey]; done {
			return nil
		}
		origin.Context[FirstLookReportDismissedKey] = now.UTC().Format(time.RFC3339)
		if suggestion != nil {
			current.RememberDismissedSuggestion(suggestion.Text, "Not now on its first look", look.ID, now)
		}
		return nil
	})
}

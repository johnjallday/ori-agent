package personalassistant

import (
	"context"
	"net/url"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/dailybrief"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Today kinds for a hire from the Agents page (agents-hire-with-a-brief).
const (
	// TodayKindHireFirstLook is the look while it runs, under Working on.
	TodayKindHireFirstLook = "hire_first_look"
	// TodayKindHireLookReport is the finished look's report, or the look
	// that did not finish, under Needs you.
	TodayKindHireLookReport = "hire_first_look_report"
)

// HireLookActionRetry is Retry on a look that did not finish.
const HireLookActionRetry = "retry"

// HireRecordReader is what Today reads of the user's hires.
type HireRecordReader interface {
	List(ctx context.Context, userID string) ([]HireRecord, error)
}

// SetHireRecordReader adds the user's hires to Today: the first look under
// Working on, its report under Needs you, and the task started from it.
// running reports whether a workspace has a goal run in flight; it may be
// nil. Startup wiring only.
func (s *TodayService) SetHireRecordReader(reader HireRecordReader, running func(workspaceID string) bool) {
	if s != nil {
		s.hires = reader
		s.hireLookRunning = running
	}
}

// hireItems are the hires' Today items. A source that cannot be read adds
// nothing and is named once in the footer.
func (s *TodayService) hireItems(ctx context.Context, userID string, now time.Time, out *TodayProjection) (needs, working, done []TodayItem) {
	if s == nil || s.hires == nil || s.workspaces == nil {
		return nil, nil, nil
	}
	records, err := s.hires.List(ctx, userID)
	if err != nil {
		logger.Warn("personal assistant today: hires unavailable", logger.Fields{"error": err.Error()})
		out.UnavailableSources = appendSource(out.UnavailableSources, "hires")
		return nil, nil, nil
	}
	for _, record := range records {
		ws, err := s.workspaces.Get(record.WorkspaceID)
		if err != nil || ws == nil || ws.ID != record.WorkspaceID || !todaySafeSlug.MatchString(ws.FolderSlug) {
			continue
		}
		n, w, d := hireTodayItems(record, ws, s.hireLookRunning, now)
		needs, working, done = append(needs, n...), append(working, w...), append(done, d...)
	}
	return needs, working, done
}

// hireTodayItems is one hire as Today shows it: its look under Working on
// while it runs; its report under Needs you once it finished, until the user
// acts on it or a week passes; a look that did not finish under Needs you
// with Retry; and, after Start this, the task it started under Working on and
// then Done.
func hireTodayItems(record HireRecord, ws *workspace.Workspace, running func(string) bool, now time.Time) (needs, working, done []TodayItem) {
	route := "/workspaces/" + url.PathEscape(ws.FolderSlug)
	agent := truncateRunes(strings.TrimSpace(record.AgentName), 100)
	place := truncateRunes(ws.Name, 120)
	ref := dailybrief.SourceRef{WorkspaceID: ws.ID, EntityType: "hire", EntityID: record.ID}
	status, failure, run := HireLookStatusOf(record, ws, running, now)
	switch status {
	case HireLookWaiting, HireLookRunning:
		detail := "Reading the brief"
		if status == HireLookWaiting {
			detail = "Starts when you open the workspace"
		}
		return nil, []TodayItem{{ID: "hire-look-" + record.ID, Kind: TodayKindHireFirstLook,
			Title: "First look: " + agent + " on " + place, Detail: detail, State: "working",
			Attribution: agent, Workspace: place, Route: route, SourceAt: record.CreatedAt, Ref: ref}}, nil
	case HireLookFailed:
		at := record.FinishedAt
		if run != nil {
			at = run.FinishedAt
		}
		if at.IsZero() {
			at = record.UpdatedAt
		}
		if at.After(now) || now.Sub(at) > todayFolderReceiptWindow {
			return nil, nil, nil
		}
		ref.Timestamp = at
		return []TodayItem{{ID: "hire-report-" + record.ID, Kind: TodayKindHireLookReport,
			Title: "First look did not finish", Detail: truncateRunes(failure, 400), State: "failed",
			Attribution: agent, Workspace: place, Route: route, SourceAt: at,
			Actions: []string{HireLookActionRetry}, Ref: ref}}, nil, nil
	case HireLookReported:
		at := run.FinishedAt
		if at.After(now) || now.Sub(at) > todayFolderReceiptWindow {
			return nil, nil, nil
		}
		ref.Timestamp = at
		item := TodayItem{ID: "hire-report-" + record.ID, Kind: TodayKindHireLookReport,
			Title: "Finished its first look", Detail: HireLookSummary(run), State: "finished",
			Attribution: agent, Workspace: place, Route: route, ReportRoute: route + "?panel=backlog",
			SourceAt: at, Actions: []string{FirstLookActionNotNow}, Ref: ref}
		if suggestion := HireLookSuggestionOf(ws, run); suggestion != nil {
			item.Suggestion = suggestion
			item.Actions = []string{FirstLookActionStartThis, FirstLookActionNotNow}
		}
		return []TodayItem{item}, nil, nil
	case HireLookStarted:
		task := findTask(ws, record.TaskID)
		if task == nil {
			return nil, nil, nil
		}
		item := TodayItem{ID: "hire-suggestion-" + task.ID, Kind: TodayKindFirstLookSuggestion,
			Title: truncateRunes(task.Description, 200), Attribution: agent, Workspace: place,
			Route: route + "/task/" + url.PathEscape(task.ID),
			Ref:   dailybrief.SourceRef{WorkspaceID: ws.ID, EntityType: "task", EntityID: task.ID}}
		switch task.CanonicalState() {
		case workspace.TicketStateReview, workspace.TicketStateDone:
			at := taskSourceTime(*task)
			if at.After(now) || now.Sub(at) > todayFolderReceiptWindow {
				return nil, nil, nil
			}
			item.Detail, item.State, item.SourceAt, item.Ref.Timestamp = FolderFirstTaskExcerpt(task.Result), string(task.CanonicalState()), at, at
			return nil, nil, []TodayItem{item}
		case workspace.TicketStateCancelled:
			return nil, nil, nil
		default:
			item.Detail = "Started from its first look"
			if task.NeedsAttention() {
				item.State = "needs_attention"
			}
			return nil, []TodayItem{item}, nil
		}
	}
	return nil, nil, nil
}

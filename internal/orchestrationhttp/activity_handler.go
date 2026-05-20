package orchestrationhttp

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// ActivityKind enumerates the surfaces the home dashboard's Recent Activity
// section can show. Keep stable — client renderers branch on these strings.
const (
	ActivityKindNoteEdited      = "note_edited"
	ActivityKindTaskCompleted   = "task_completed"
	ActivityKindScheduledFired  = "scheduled_task_fired"
	ActivityKindScheduledFailed = "scheduled_task_failed"
)

type activityRow struct {
	Kind          string    `json:"kind"`
	Description   string    `json:"description"`
	WorkspaceID   string    `json:"workspace_id"`
	WorkspaceName string    `json:"workspace_name"`
	TargetID      string    `json:"target_id,omitempty"`
	Timestamp     time.Time `json:"timestamp"`
}

// RecentActivityHandler returns a unified, time-sorted-desc activity feed
// derived from existing data sources: note `updated_at`, task `completed_at`,
// and the in-memory event-bus history filtered to scheduled-task fires.
//
// Powers the home dashboard's Recent Activity section. GET only.
//
// Known v1 limitation: scheduled-task fires older than the in-memory event
// buffer (default 1000 events) are not visible after a server restart.
func (h *Handler) RecentActivityHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		orihttp.MethodNotAllowed(w)
		return
	}

	limit := 5
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}

	ctx := r.Context()
	ids, err := h.workspaceStore.List()
	if err != nil {
		orihttp.RespondErrorWithErr(w, http.StatusInternalServerError, "Failed to list workspaces", err)
		return
	}

	wsNameByID := make(map[string]string, len(ids))
	rows := make([]activityRow, 0)

	for _, id := range ids {
		ws, err := h.workspaceStore.Get(id)
		if err != nil {
			logger.Warn("recent-activity: failed to load workspace", logger.Fields{"workspace_id": id, "err": err})
			continue
		}
		wsNameByID[ws.ID] = ws.Name

		// Task completions
		for _, task := range ws.Tasks {
			if task.CompletedAt == nil {
				continue
			}
			rows = append(rows, activityRow{
				Kind:          ActivityKindTaskCompleted,
				Description:   "Task completed: " + safeFirstLine(task.Description),
				WorkspaceID:   ws.ID,
				WorkspaceName: ws.Name,
				TargetID:      task.ID,
				Timestamp:     *task.CompletedAt,
			})
		}

		// Note edits — pulled from the session note store if available.
		if h.sessionStore != nil {
			notes, err := h.sessionStore.ListNotesByWorkspace(ctx, ws.ID)
			if err != nil {
				logger.Warn("recent-activity: failed to list notes", logger.Fields{"workspace_id": ws.ID, "err": err})
			} else {
				for _, note := range notes {
					rows = append(rows, activityRow{
						Kind:          ActivityKindNoteEdited,
						Description:   "Note edited: " + note.Name,
						WorkspaceID:   ws.ID,
						WorkspaceName: ws.Name,
						TargetID:      note.ID,
						Timestamp:     note.UpdatedAt,
					})
				}
			}
		}
	}

	// Scheduled-task fires from the event bus history. The bus is in-memory
	// and bounded — older fires after a server restart simply won't appear.
	if h.eventBus != nil {
		// GetHistory uses `len(events) < limit` as its loop bound, so we
		// need a positive limit even when we want "everything available".
		// 1000 matches DefaultEventBus's historySize — enough headroom for
		// the dashboard's later time-sort + truncate.
		history := h.eventBus.GetHistory(func(ev workspace.Event) bool {
			return ev.Type == workspace.EventScheduledTaskTriggered ||
				ev.Type == workspace.EventScheduledTaskFailed
		}, 1000)
		for _, ev := range history {
			kind := ActivityKindScheduledFired
			desc := "Scheduled task fired"
			if ev.Type == workspace.EventScheduledTaskFailed {
				kind = ActivityKindScheduledFailed
				desc = "Scheduled task failed"
			}
			if name, ok := ev.Data["task_name"].(string); ok && name != "" {
				desc += ": " + name
			}
			rows = append(rows, activityRow{
				Kind:          kind,
				Description:   desc,
				WorkspaceID:   ev.WorkspaceID,
				WorkspaceName: wsNameByID[ev.WorkspaceID],
				Timestamp:     ev.Timestamp,
			})
		}
	}

	sort.Slice(rows, func(i, j int) bool {
		return rows[i].Timestamp.After(rows[j].Timestamp)
	})
	if len(rows) > limit {
		rows = rows[:limit]
	}

	orihttp.WriteJSON(w, map[string]any{
		"events": rows,
		"count":  len(rows),
	})
}

// safeFirstLine returns the first line of s, trimmed, truncated to 80 chars.
// Keeps task descriptions readable when they're multi-line prompts.
func safeFirstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 80 {
		s = s[:79] + "…"
	}
	if s == "" {
		s = "(untitled)"
	}
	return s
}

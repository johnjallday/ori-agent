package personalhqhttp

import (
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/dailybrief"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

const watchtowerWorkspaceIDQuery = "workspace_id"

// WatchtowerItem is one bounded cross-workspace item that needs attention.
// EntityID and WorkspaceID are sufficient for the UI to route to its owning
// workspace without exposing the entity's full content.
type WatchtowerItem struct {
	WorkspaceID   string `json:"workspace_id"`
	WorkspaceSlug string `json:"workspace_slug"`
	WorkspaceName string `json:"workspace_name"`
	ItemType      string `json:"item_type"`
	EntityID      string `json:"entity_id"`
	Title         string `json:"title"`
	Description   string `json:"description"`
	Severity      string `json:"severity"`
	Timestamp     string `json:"timestamp"`

	severityRank int
	timestamp    time.Time
}

// WatchtowerResponse is the read-only attention queue returned to the HQ Map.
// Gaps are always included so an unreadable source is never mistaken for a
// quiet workspace.
type WatchtowerResponse struct {
	Items []WatchtowerItem `json:"items"`
	Gaps  []string         `json:"gaps"`
}

// SetWatchtowerSources wires the lazy Daily Brief projection source for the
// Watchtower endpoint. Server setup provides a closure because the handler is
// built before workspace storage is available.
func (h *Handler) SetWatchtowerSources(factory func() dailybrief.SnapshotSources) {
	if h == nil {
		return
	}
	h.watchtowerSources = factory
}

// Watchtower handles GET /api/personal-hq/watchtower?workspace_id=<hq-id>.
// It is intentionally read-only and returns data only when the calling
// workspace is the user's folder-hydrated Personal HQ.
func (h *Handler) Watchtower(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodGet) {
		return
	}
	if h == nil || h.service == nil || h.watchtowerSources == nil {
		orihttp.ServiceUnavailable(w, "personal hq Watchtower is unavailable")
		return
	}
	userID, ok := h.resolveUser(w, r)
	if !ok {
		return
	}
	workspaceID := strings.TrimSpace(r.URL.Query().Get(watchtowerWorkspaceIDQuery))
	if workspaceID == "" {
		orihttp.Forbidden(w, "Watchtower is available only in the designated Personal HQ")
		return
	}
	isHQ, err := h.service.IsWorkspaceDesignatedPersonalHQ(r.Context(), userID, workspaceID)
	if err != nil || !isHQ {
		orihttp.Forbidden(w, "Watchtower is available only in the designated Personal HQ")
		return
	}

	snapshot := dailybrief.BuildAllScopeSnapshot(r.Context(), h.watchtowerSources(), userID, time.Now())
	orihttp.Success(w, buildWatchtowerResponse(snapshot))
}

func buildWatchtowerResponse(snapshot dailybrief.Snapshot) WatchtowerResponse {
	response := WatchtowerResponse{
		Items: make([]WatchtowerItem, 0),
		Gaps:  append([]string(nil), snapshot.Gaps...),
	}
	if response.Gaps == nil {
		response.Gaps = []string{}
	}

	for _, workspaceSnapshot := range snapshot.Workspaces {
		for _, task := range workspaceSnapshot.OpenTasks {
			if !isWatchtowerTaskStatus(task.Status) {
				continue
			}
			severityRank := watchtowerRankWaitingForChoice
			if task.Status == string(workspace.TaskStatusFailed) || task.Status == string(workspace.TaskStatusTimeout) {
				severityRank = watchtowerRankUrgent
			}
			response.Items = append(response.Items, newWatchtowerItem(
				workspaceSnapshot.WorkspaceID,
				workspaceSnapshot.WorkspaceSlug,
				workspaceSnapshot.Name,
				"task",
				task.Ref.EntityID,
				task.Description,
				task.Description,
				task.Status,
				task.Ref.Timestamp,
				severityRank,
			))
		}

		for _, scheduled := range workspaceSnapshot.ScheduledTasks {
			if scheduled.FailureCount <= 0 && strings.TrimSpace(scheduled.LastError) == "" {
				continue
			}
			response.Items = append(response.Items, newWatchtowerItem(
				workspaceSnapshot.WorkspaceID,
				workspaceSnapshot.WorkspaceSlug,
				workspaceSnapshot.Name,
				"scheduled_task",
				scheduled.Ref.EntityID,
				scheduled.Name,
				scheduled.LastError,
				"scheduled_failure",
				scheduled.Ref.Timestamp,
				watchtowerRankScheduledFailure,
			))
		}

		for _, opportunity := range workspaceSnapshot.Opportunities {
			severityRank, ok := watchtowerOpportunityRank(opportunity.Priority)
			if !ok {
				continue
			}
			response.Items = append(response.Items, newWatchtowerItem(
				workspaceSnapshot.WorkspaceID,
				workspaceSnapshot.WorkspaceSlug,
				workspaceSnapshot.Name,
				"opportunity",
				opportunity.Ref.EntityID,
				opportunity.Title,
				"",
				strings.ToLower(strings.TrimSpace(opportunity.Priority)),
				opportunity.Ref.Timestamp,
				severityRank,
			))
		}
	}

	sort.Slice(response.Items, func(i, j int) bool {
		left, right := response.Items[i], response.Items[j]
		if left.severityRank != right.severityRank {
			return left.severityRank < right.severityRank
		}
		if !left.timestamp.Equal(right.timestamp) {
			return left.timestamp.After(right.timestamp)
		}
		if left.WorkspaceID != right.WorkspaceID {
			return left.WorkspaceID < right.WorkspaceID
		}
		if left.ItemType != right.ItemType {
			return left.ItemType < right.ItemType
		}
		return left.EntityID < right.EntityID
	})
	for index := range response.Items {
		response.Items[index].severityRank = 0
		response.Items[index].timestamp = time.Time{}
	}
	return response
}

const (
	watchtowerRankUrgent = iota
	watchtowerRankWaitingForChoice
	watchtowerRankScheduledFailure
	watchtowerRankHighOpportunity
)

func newWatchtowerItem(workspaceID, workspaceSlug, workspaceName, itemType, entityID, title, description, severity string, timestamp time.Time, rank int) WatchtowerItem {
	item := WatchtowerItem{
		WorkspaceID:   workspaceID,
		WorkspaceSlug: workspaceSlug,
		WorkspaceName: workspaceName,
		ItemType:      itemType,
		EntityID:      entityID,
		Title:         title,
		Description:   description,
		Severity:      severity,
		severityRank:  rank,
		timestamp:     timestamp,
	}
	if !timestamp.IsZero() {
		item.Timestamp = timestamp.Format(time.RFC3339)
	}
	return item
}

func isWatchtowerTaskStatus(status string) bool {
	switch workspace.TaskStatus(status) {
	case workspace.TaskStatusWaitingForChoice, workspace.TaskStatusFailed, workspace.TaskStatusTimeout:
		return true
	default:
		return false
	}
}

func watchtowerOpportunityRank(priority string) (int, bool) {
	switch strings.ToLower(strings.TrimSpace(priority)) {
	case "critical":
		return watchtowerRankUrgent, true
	case "high":
		return watchtowerRankHighOpportunity, true
	default:
		return 0, false
	}
}

package chathttp

import (
	"context"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/dailybrief"
	"github.com/johnjallday/ori-agent/internal/toolapi"
	"github.com/johnjallday/ori-agent/internal/userprofile"
)

const hqOverviewHighlightLimit = 5

// hqOverviewEnabled keeps the overview structurally unavailable everywhere
// except the designated Personal HQ coordinator. This mirrors the existing
// delegate_task coordinator gate: a specialist cannot acquire the tool merely
// by being in the HQ workspace.
func (p *WorkspaceToolProvider) hqOverviewEnabled() bool {
	if p == nil || p.hqVisibility.SnapshotSources == nil || p.hqVisibility.IsDesignatedHQ == nil {
		return false
	}
	isHQ, err := p.hqVisibility.IsDesignatedHQ(context.Background(), p.workspaceID)
	if err != nil || !isHQ {
		return false
	}
	return p.isWorkspaceCoordinator()
}

type hqOverviewResponse struct {
	Workspaces []hqWorkspaceOverview `json:"workspaces"`
	Gaps       []string              `json:"gaps"`
}

type hqWorkspaceOverview struct {
	ID                        string                     `json:"id"`
	Name                      string                     `json:"name"`
	FolderPath                string                     `json:"folder_path"`
	AgentCount                int                        `json:"agent_count"`
	OpenTaskCount             int                        `json:"open_task_count"`
	OpenOpportunityCount      int                        `json:"open_opportunity_count"`
	FailingScheduledTaskCount int                        `json:"failing_scheduled_task_count"`
	MostRecentActivityAt      string                     `json:"most_recent_activity_at"`
	OpenTasks                 []hqTaskHighlight          `json:"open_tasks"`
	FailingScheduledTasks     []hqScheduledTaskHighlight `json:"failing_scheduled_tasks"`
	OpenOpportunities         []hqOpportunityHighlight   `json:"open_opportunities"`
	LatestSession             *hqSessionHighlight        `json:"latest_session,omitempty"`
}

type hqTaskHighlight struct {
	Description   string `json:"description"`
	Status        string `json:"status"`
	AssignedAgent string `json:"assigned_agent"`
}

type hqScheduledTaskHighlight struct {
	Name         string `json:"name"`
	FailureCount int    `json:"failure_count"`
	LastError    string `json:"last_error"`
}

type hqOpportunityHighlight struct {
	Title    string `json:"title"`
	Priority string `json:"priority"`
}

type hqSessionHighlight struct {
	Title     string `json:"title"`
	UpdatedAt string `json:"updated_at"`
}

// hqOverviewTool returns the bounded Daily Brief projection for every eligible
// workspace. It intentionally contains no note bodies, session previews or
// transcripts, file contents, or agent settings; detailed reads remain the
// user-controlled linked-folder path.
func (p *WorkspaceToolProvider) hqOverviewTool() toolapi.Tool {
	return &nativeUtilityTool{
		definition: toolapi.ToolDefinition{
			Name:        "hq_overview",
			Description: "Read a bounded, cross-workspace overview for the Personal HQ. Returns workspace metadata, folder paths for linked-folder deep-dives, open work highlights, schedule failures, recent opportunities, and session recency. This tool is read-only.",
			Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		call: func(ctx context.Context, _ string) (string, error) {
			sources := p.hqVisibility.SnapshotSources()
			userID := strings.TrimSpace(p.hqVisibility.UserID)
			if userID == "" {
				userID = userprofile.LocalUserID
			}
			snapshot := dailybrief.BuildAllScopeSnapshot(ctx, sources, userID, time.Now())
			return marshalToolResponse(buildHQOverview(snapshot, p.hqWorkspaceFolderPath))
		},
	}
}

func buildHQOverview(snapshot dailybrief.Snapshot, folderPath func(string) string) hqOverviewResponse {
	response := hqOverviewResponse{
		Workspaces: make([]hqWorkspaceOverview, 0, len(snapshot.Workspaces)),
		Gaps:       append([]string(nil), snapshot.Gaps...),
	}
	if response.Gaps == nil {
		response.Gaps = []string{}
	}

	for _, workspaceSnapshot := range snapshot.Workspaces {
		entry := hqWorkspaceOverview{
			ID:                    workspaceSnapshot.WorkspaceID,
			Name:                  workspaceSnapshot.Name,
			AgentCount:            workspaceSnapshot.AgentCount,
			OpenTaskCount:         len(workspaceSnapshot.OpenTasks),
			OpenOpportunityCount:  len(workspaceSnapshot.Opportunities),
			OpenTasks:             []hqTaskHighlight{},
			FailingScheduledTasks: []hqScheduledTaskHighlight{},
			OpenOpportunities:     []hqOpportunityHighlight{},
		}
		if folderPath != nil {
			entry.FolderPath = strings.TrimSpace(folderPath(workspaceSnapshot.WorkspaceID))
		}

		latestActivity := time.Time{}
		for _, task := range workspaceSnapshot.OpenTasks {
			latestActivity = laterTime(latestActivity, task.Ref.Timestamp)
			if len(entry.OpenTasks) < hqOverviewHighlightLimit {
				entry.OpenTasks = append(entry.OpenTasks, hqTaskHighlight{
					Description:   task.Description,
					Status:        task.Status,
					AssignedAgent: task.AssignedAgent,
				})
			}
		}

		for _, scheduled := range workspaceSnapshot.ScheduledTasks {
			latestActivity = laterTime(latestActivity, scheduled.Ref.Timestamp)
			if scheduled.FailureCount > 0 || strings.TrimSpace(scheduled.LastError) != "" {
				entry.FailingScheduledTaskCount++
				entry.FailingScheduledTasks = append(entry.FailingScheduledTasks, hqScheduledTaskHighlight{
					Name:         scheduled.Name,
					FailureCount: scheduled.FailureCount,
					LastError:    scheduled.LastError,
				})
			}
		}

		for _, opportunity := range workspaceSnapshot.Opportunities {
			latestActivity = laterTime(latestActivity, opportunity.Ref.Timestamp)
			if isHighPriorityOpportunity(opportunity.Priority) && len(entry.OpenOpportunities) < hqOverviewHighlightLimit {
				entry.OpenOpportunities = append(entry.OpenOpportunities, hqOpportunityHighlight{
					Title:    opportunity.Title,
					Priority: opportunity.Priority,
				})
			}
		}

		latestSessionAt := time.Time{}
		for _, session := range workspaceSnapshot.RecentSessions {
			latestActivity = laterTime(latestActivity, session.UpdatedAt)
			if entry.LatestSession == nil || session.UpdatedAt.After(latestSessionAt) {
				entry.LatestSession = &hqSessionHighlight{
					Title:     session.Title,
					UpdatedAt: session.UpdatedAt.Format(time.RFC3339),
				}
				latestSessionAt = session.UpdatedAt
			}
		}

		if !latestActivity.IsZero() {
			entry.MostRecentActivityAt = latestActivity.Format(time.RFC3339)
		}
		response.Workspaces = append(response.Workspaces, entry)
	}
	return response
}

func isHighPriorityOpportunity(priority string) bool {
	switch strings.ToLower(strings.TrimSpace(priority)) {
	case "critical", "high":
		return true
	default:
		return false
	}
}

func laterTime(current, candidate time.Time) time.Time {
	if candidate.After(current) {
		return candidate
	}
	return current
}

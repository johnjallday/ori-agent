package server

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/dailybrief"
	"github.com/johnjallday/ori-agent/internal/filejanitor"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// currentBriefReader is the part of dailybrief.Service the backfill reads.
type currentBriefReader interface {
	GetCurrent(ctx context.Context, workspaceID string) (*dailybrief.Revision, error)
}

// briefRevisionExists reports whether HQ already has a Daily Brief revision,
// the backfill evidence for Mission 04. No HQ means no brief.
func briefRevisionExists(briefs currentBriefReader, hqWorkspaceID string) bool {
	if briefs == nil || strings.TrimSpace(hqWorkspaceID) == "" {
		return false
	}
	revision, err := briefs.GetCurrent(context.Background(), hqWorkspaceID)
	return err == nil && revision != nil
}

// janitorActionSource is the part of filejanitor.Service Today reads.
type janitorActionSource interface {
	ListActions(workspaceID string) ([]filejanitor.FileAction, error)
	Status(workspaceID string) (filejanitor.Status, error)
}

// todayJanitorResults adapts File Janitor's action journal to Today's Results
// line (tasks/prd-starter-missions.md FR36-FR38): for each File Janitor
// workspace the user owns, the applied, not-undone actions of the last day.
type todayJanitorResults struct {
	workspaces starterWorkspaceSource
	janitor    janitorActionSource
	now        func() time.Time
}

// JanitorResults implements personalassistant.JanitorResultReader. One
// workspace's unreadable journal is logged and skipped; it never hides the
// others.
func (r todayJanitorResults) JanitorResults(_ context.Context, userID string) ([]personalassistant.JanitorResult, error) {
	if r.workspaces == nil || r.janitor == nil {
		return nil, nil
	}
	now := time.Now()
	if r.now != nil {
		now = r.now()
	}
	since := now.Add(-24 * time.Hour)

	var results []personalassistant.JanitorResult
	for id, lean := range r.workspaces.CachedWorkspaces() {
		if lean == nil || lean.GetStatus() != workspace.StatusActive || !isFileJanitorWorkspace(lean) ||
			!ownedBy(lean, userID) {
			continue
		}
		actions, err := r.janitor.ListActions(id)
		if err != nil {
			logger.Warn("today: File Janitor history unreadable", logger.Fields{"workspace_id": id, "error": err.Error()})
			continue
		}
		result, recent := summarizeJanitorActions(actions, since)
		if !recent {
			continue
		}
		status, err := r.janitor.Status(id)
		if err != nil || strings.TrimSpace(status.Settings.RootPath) == "" {
			continue
		}
		result.WorkspaceID = id
		result.WorkspaceName = lean.Name
		result.Slug = lean.FolderSlug
		result.FolderName = filepath.Base(filepath.Clean(status.Settings.RootPath))
		results = append(results, result)
	}
	return results, nil
}

// summarizeJanitorActions counts the applied, not-undone moves and trashes
// completed at or after since, and names the newest. recent is false when there
// are none.
func summarizeJanitorActions(actions []filejanitor.FileAction, since time.Time) (result personalassistant.JanitorResult, recent bool) {
	for _, action := range actions {
		if action.Result != filejanitor.ResultApplied || !action.UndoneAt.IsZero() {
			continue
		}
		at := action.CompletedAt
		if at.IsZero() {
			at = action.ApprovedAt
		}
		if at.Before(since) {
			continue
		}
		switch action.Operation {
		case filejanitor.OperationMove:
			result.Moved++
		case filejanitor.OperationTrash:
			result.Trashed++
		default:
			continue
		}
		if !recent || at.After(result.NewestAt) {
			result.NewestAt, result.NewestActionID = at, action.ID
		}
		recent = true
	}
	return result, recent
}

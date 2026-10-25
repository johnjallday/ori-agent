package personalassistant

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/dailybrief"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// ErrProtectionWorkspaceNotFound hides whether a workspace exists for another owner.
var ErrProtectionWorkspaceNotFound = errors.New("workspace not found")

// WorkspaceProtection is one workspace's read-only Protection fact (FR11),
// from the same stored coverage read and saved scope as Today's line. It is
// state, not an offer: no decline hides it. nil means no adopted folder.
func (s *TodayService) WorkspaceProtection(ctx context.Context, userID, workspaceID string) (*dailybrief.ProtectionFact, error) {
	if s == nil || s.backupCoverage == nil {
		return nil, ErrRepairNeeded
	}
	scope, ws, err := s.ProtectionScope(ctx, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	if s.now != nil {
		now = s.now().UTC()
	}
	fact, ok := dailybrief.WorkspaceProtection(ctx, s.backupCoverage, strings.TrimSpace(userID), scope, ws, backupChooserRoute, now)
	if !ok {
		return nil, nil
	}
	return &fact, nil
}

// ProtectionScope shares the existing saved owner/scope policy with narrowly
// scoped host projections. It does not enumerate adopted folders or probe roots.
func (s *TodayService) ProtectionScope(ctx context.Context, userID, workspaceID string) (dailybrief.WorkspaceScopeResult, *workspace.Workspace, error) {
	if s == nil || s.followUpWorkspaces == nil {
		return dailybrief.WorkspaceScopeResult{}, nil, ErrRepairNeeded
	}
	userID, workspaceID = strings.TrimSpace(userID), strings.TrimSpace(workspaceID)
	ws, err := s.followUpWorkspaces.Get(workspaceID)
	if err != nil || ws == nil || workspaceID == "" || ws.ID != workspaceID {
		return dailybrief.WorkspaceScopeResult{}, nil, ErrProtectionWorkspaceNotFound
	}
	if owner := strings.TrimSpace(ws.OwnerUserID); owner != "" && !strings.EqualFold(owner, userID) {
		return dailybrief.WorkspaceScopeResult{}, nil, ErrProtectionWorkspaceNotFound
	}
	// The saved Daily Brief scope when there is one; before a brief is set up,
	// every workspace the user owns, as a fresh hire's default scope is.
	cfg := dailybrief.Config{Scope: dailybrief.ScopeAll, IncludeFutureWorkspaces: true}
	if s.relationship != nil {
		if relationship, err := s.relationship.Get(ctx, userID); err == nil && relationship != nil && relationship.DailyBrief != nil {
			cfg = todayFollowUpConfig(relationship)
		}
	}
	scope := dailybrief.ResolveWorkspaceScope(s.followUpWorkspaces, cfg, userID).Including(s.followUpWorkspaces, ws)
	return scope, ws, nil
}

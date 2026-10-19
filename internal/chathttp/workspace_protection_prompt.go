package chathttp

import (
	"context"
	"strings"

	"github.com/johnjallday/ori-agent/internal/dailybrief"
	"github.com/johnjallday/ori-agent/internal/userprofile"
)

// workspaceProtectionReader is the stored-only Protection fact of a workspace,
// the same read the Command view shows. It offers no check, route or action.
type workspaceProtectionReader interface {
	WorkspaceProtection(ctx context.Context, userID, workspaceID string) (*dailybrief.ProtectionFact, error)
}

// SetWorkspaceProtectionReader is composition-only.
func (h *Handler) SetWorkspaceProtectionReader(reader workspaceProtectionReader) {
	if h != nil {
		h.workspaceProtection = reader
	}
}

// buildWorkspaceProtectionPrompt gives the workspace's entry agent the
// compiled Protection fact (folder protection FR12): a host-owned, timestamped
// stored-evidence line it may state. No tool, route or capability comes with
// it, other agents do not receive it, and a workspace without an adopted
// folder gets nothing.
func (h *Handler) buildWorkspaceProtectionPrompt(ctx context.Context, routeCtx normalizedChatRouteContext) string {
	if h == nil || h.workspaceProtection == nil || h.workspaceStore == nil || !shouldAttachWorkspaceSnapshot(routeCtx) {
		return ""
	}
	ws, err := h.workspaceStore.Get(routeCtx.WorkspaceID)
	if err != nil || ws == nil {
		return ""
	}
	entry := strings.TrimSpace(ws.EntryAgentName())
	if entry == "" || !strings.EqualFold(entry, strings.TrimSpace(routeCtx.AgentName)) {
		return ""
	}
	userID := userprofile.LocalUserID
	if h.userProvider != nil {
		resolved, err := h.userProvider.CurrentUserID(ctx)
		if err != nil || strings.TrimSpace(resolved) == "" {
			return ""
		}
		userID = strings.TrimSpace(resolved)
	}
	fact, err := h.workspaceProtection.WorkspaceProtection(ctx, userID, ws.ID)
	if err != nil || fact == nil {
		return ""
	}
	return "## Backup Protection\n" +
		"Host-compiled stored evidence. You cannot check, change or set up backups from here.\n" +
		dailybrief.ProtectionPromptLine(*fact)
}

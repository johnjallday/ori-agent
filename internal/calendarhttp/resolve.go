// Shared Calendar Ops workspace resolution (FR49): the single rule Home, the
// Personal HQ portal, and Home calendar-intent routing all use to find "the"
// Calendar Ops workspace for the current user, instead of each reimplementing
// the scan.
package calendarhttp

import (
	"context"
	"strings"

	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// calendarOpsEntryAgentName is calendarOpsAgentNames[0] -- the Scheduler is
// the calendar-ops template's entry/orchestrator agent (see
// starter/calendar-ops/template.json). Kept as its own constant since Go
// can't index a package-level slice in a const declaration; the two must
// stay in sync.
const calendarOpsEntryAgentName = "Scheduler"

// ActiveWorkspace resolves the user's active, owned Calendar Ops workspace:
// the one whose template provenance matches CalendarOpsTemplateID. When
// several qualify (duplicate creations), the most recently updated one wins.
// ok=false means no qualifying workspace exists yet.
func (h *Handler) ActiveWorkspace(ctx context.Context, userID string) (*agentworkspace.Workspace, bool) {
	if h == nil || h.lister == nil || h.folders == nil {
		return nil, false
	}
	all, err := h.lister.ListWorkspaces(ctx)
	if err != nil {
		return nil, false
	}
	wantOwner := strings.TrimSpace(userID)
	if wantOwner == "" {
		wantOwner = userprofile.LocalUserID
	}

	var best *agentworkspace.Workspace
	for _, w := range all {
		if w.IsGroup() {
			continue
		}
		if w.Status != "" && w.Status != session.WorkspaceStatusActive {
			continue
		}
		owner := strings.TrimSpace(w.OwnerUserID)
		if owner == "" {
			owner = userprofile.LocalUserID
		}
		if !strings.EqualFold(owner, wantOwner) {
			continue
		}
		full, err := h.folders.GetFolderWorkspace(w.ID)
		if err != nil || full == nil || !full.IsFromTemplate(CalendarOpsTemplateID) {
			continue
		}
		if best == nil || full.UpdatedAt.After(best.UpdatedAt) {
			best = full
		}
	}
	if best == nil {
		return nil, false
	}
	return best, true
}

// PreferredCalendarAgent reports the current user's Calendar Ops entry agent
// name when their active Calendar Ops workspace has an effective, ready
// calendar binding (FR53): Home calendar-intent routing uses this to prefer
// the Scheduler over generic agent scoring for personal-calendar prompts.
// ok=false means "no preference" -- the caller falls back to its existing
// generic matching unchanged, whether because the user has no Calendar Ops
// workspace yet or its setup/connector isn't ready.
func (h *Handler) PreferredCalendarAgent(ctx context.Context) (string, bool) {
	if h == nil {
		return "", false
	}
	userID, err := h.currentUserID(ctx)
	if err != nil {
		return "", false
	}
	ws, ok := h.ActiveWorkspace(ctx, userID)
	if !ok {
		return "", false
	}
	if _, gerr := h.resolveGateway(ctx, ws.ID); gerr != nil {
		return "", false
	}
	return calendarOpsEntryAgentName, true
}

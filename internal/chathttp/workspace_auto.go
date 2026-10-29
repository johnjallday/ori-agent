package chathttp

import (
	"fmt"
	"sort"
	"strings"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func routeNeedsWorkspace(mode UtilityRouteMode) bool {
	switch mode {
	case UtilityRouteWorkspace, UtilityRouteScratch, UtilityRouteSpecial:
		return true
	default:
		return false
	}
}

// workspaceForRoute returns the workspace a routed chat request runs in: the
// one the route names, else the agent's most recently updated active
// workspace, else nil. Chat never creates a workspace; that is the job of the
// assistant's reviewed setup and the workspace creation surfaces.
func (h *Handler) workspaceForRoute(agentName string, decision UtilityRouteDecision, routeCtx normalizedChatRouteContext) (*workspace.Workspace, error) {
	if h == nil || h.commandHandler == nil || h.commandHandler.workspaceStore == nil {
		return nil, nil
	}
	if strings.TrimSpace(agentName) == "" || !routeNeedsWorkspace(decision.Mode) {
		return nil, nil
	}

	if workspaceID := strings.TrimSpace(routeCtx.WorkspaceID); workspaceID != "" {
		ws, err := h.commandHandler.workspaceStore.Get(workspaceID)
		if err != nil {
			return nil, fmt.Errorf("get route workspace %q: %w", workspaceID, err)
		}
		if ws == nil {
			return nil, fmt.Errorf("route workspace %q was not found", workspaceID)
		}
		return ws, nil
	}

	active, err := h.commandHandler.workspaceStore.ListActive()
	if err != nil {
		return nil, fmt.Errorf("list active workspaces: %w", err)
	}

	for _, ws := range newestWorkspacesFirst(active) {
		if ws != nil && ws.HasAgent(agentName) {
			return ws, nil
		}
	}
	return nil, nil
}

func applyWorkspaceRouteContext(routeCtx normalizedChatRouteContext, ws *workspace.Workspace) normalizedChatRouteContext {
	if ws == nil {
		return routeCtx
	}

	updated := routeCtx
	updated.WorkspaceID = strings.TrimSpace(ws.ID)
	updated.WorkspaceSlug = strings.TrimSpace(ws.FolderSlug)
	if updated.WorkspaceID == "" {
		return routeCtx
	}

	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(updated.PagePath)), "/workspaces/") {
		if updated.WorkspaceSlug != "" {
			updated.PagePath = "/workspaces/" + updated.WorkspaceSlug
		}
	}
	if !isWorkspaceChatSurface(updated.Surface) {
		updated.Surface = "workspace_chat"
	}
	if strings.TrimSpace(updated.Origin) == "" {
		updated.Origin = "chat_route_workspace"
	}
	return updated
}

func isWorkspaceChatSurface(surface string) bool {
	switch strings.TrimSpace(surface) {
	case "workspace_detail", "workspace_canvas", "workspace_chat":
		return true
	default:
		return false
	}
}

func newestWorkspacesFirst(items []*workspace.Workspace) []*workspace.Workspace {
	out := make([]*workspace.Workspace, 0, len(items))
	out = append(out, items...)
	sort.Slice(out, func(i, j int) bool {
		if out[i] == nil || out[j] == nil {
			return out[i] != nil
		}
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	return out
}

func enrichPromptWithWorkspaceContext(prompt string, ws *workspace.Workspace, mode UtilityRouteMode) string {
	if ws == nil {
		return prompt
	}

	return fmt.Sprintf(
		"Reusing existing workspace context.\nWorkspace context:\n- workspace_id: %s\n- workspace_name: %s\n- route_mode: %s\n\nUser request:\n%s",
		ws.ID,
		ws.Name,
		mode,
		prompt,
	)
}

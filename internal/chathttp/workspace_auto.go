package chathttp

import (
	"fmt"
	"sort"
	"strings"

	"github.com/johnjallday/ori-agent/internal/logger"
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

// ensureWorkspaceForRoute returns an active workspace for the agent, creating one when needed.
func (h *Handler) ensureWorkspaceForRoute(agentName, request string, decision UtilityRouteDecision, routeCtx normalizedChatRouteContext) (*workspace.Workspace, bool, error) {
	if h == nil || h.commandHandler == nil || h.commandHandler.workspaceStore == nil {
		return nil, false, nil
	}
	if strings.TrimSpace(agentName) == "" || !routeNeedsWorkspace(decision.Mode) {
		return nil, false, nil
	}

	if workspaceID := strings.TrimSpace(routeCtx.WorkspaceID); workspaceID != "" {
		ws, err := h.commandHandler.workspaceStore.Get(workspaceID)
		if err != nil {
			return nil, false, fmt.Errorf("get route workspace %q: %w", workspaceID, err)
		}
		if ws == nil {
			return nil, false, fmt.Errorf("route workspace %q was not found", workspaceID)
		}
		return ws, false, nil
	}

	active, err := h.commandHandler.workspaceStore.ListActive()
	if err != nil {
		return nil, false, fmt.Errorf("list active workspaces: %w", err)
	}

	for _, ws := range newestWorkspacesFirst(active) {
		if ws != nil && ws.HasAgent(agentName) {
			return ws, false, nil
		}
	}

	name := buildAutoWorkspaceName(agentName, decision.Mode, request)
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{
		Name:        name,
		Description: fmt.Sprintf("Auto-created from chat (%s)", decision.Mode),
		Agents:      []string{agentName},
		InitialData: map[string]any{
			"source":          "chat_auto_workspace",
			"initial_request": strings.TrimSpace(request),
			"route_mode":      string(decision.Mode),
		},
	})

	if err := h.commandHandler.workspaceStore.Save(ws); err != nil {
		return nil, false, fmt.Errorf("save auto-created workspace: %w", err)
	}

	logger.Info("Auto-created workspace for chat route", logger.Fields{
		"workspace_id": ws.ID,
		"name":         ws.Name,
		"agent":        agentName,
		"route_mode":   decision.Mode,
	})

	return ws, true, nil
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
		updated.Origin = "chat_auto_workspace"
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

func buildAutoWorkspaceName(agentName string, mode UtilityRouteMode, request string) string {
	base := strings.TrimSpace(agentName)
	if base == "" {
		base = "agent"
	}
	snippet := strings.TrimSpace(request)
	if len(snippet) > 42 {
		snippet = strings.TrimSpace(snippet[:42])
	}
	snippet = strings.ReplaceAll(snippet, "\n", " ")
	if snippet == "" {
		snippet = "task"
	}
	return fmt.Sprintf("%s-%s-%s", base, mode, snippet)
}

func enrichPromptWithWorkspaceContext(prompt string, ws *workspace.Workspace, mode UtilityRouteMode, created bool) string {
	if ws == nil {
		return prompt
	}

	action := "Reusing existing workspace context."
	if created {
		action = "A workspace was auto-created for this request."
	}

	return fmt.Sprintf(
		"%s\nWorkspace context:\n- workspace_id: %s\n- workspace_name: %s\n- route_mode: %s\n\nUser request:\n%s",
		action,
		ws.ID,
		ws.Name,
		mode,
		prompt,
	)
}

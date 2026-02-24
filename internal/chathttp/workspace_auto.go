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
func (h *Handler) ensureWorkspaceForRoute(agentName, request string, decision UtilityRouteDecision) (*workspace.Workspace, bool, error) {
	if h == nil || h.commandHandler == nil || h.commandHandler.workspaceStore == nil {
		return nil, false, nil
	}
	if strings.TrimSpace(agentName) == "" || !routeNeedsWorkspace(decision.Mode) {
		return nil, false, nil
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
		"%s\nWorkspace context:\n- studio_id: %s\n- workspace_name: %s\n- route_mode: %s\n\nUser request:\n%s",
		action,
		ws.ID,
		ws.Name,
		mode,
		prompt,
	)
}

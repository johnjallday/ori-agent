package sessionhttp

import (
	"fmt"
	"strings"
)

func workspaceBootstrapMap(sharedData map[string]any) map[string]any {
	if sharedData == nil {
		return nil
	}

	raw, ok := sharedData["workspace_bootstrap"]
	if !ok || raw == nil {
		return nil
	}

	bootstrap, _ := raw.(map[string]any)
	return bootstrap
}

func workspaceBootstrapStringValue(sharedData map[string]any, key string) string {
	bootstrap := workspaceBootstrapMap(sharedData)
	if bootstrap == nil {
		return ""
	}

	value, ok := bootstrap[key]
	if !ok || value == nil {
		return ""
	}

	return strings.TrimSpace(fmt.Sprint(value))
}

// mergeWorkspaceBootstrapForUpdate applies a workspace update to the legacy
// workspace_bootstrap map. The description is no longer the goal: the goal
// lives in Workspace.Mission, so a description edit leaves the stored
// bootstrap goal alone, and an update that omits a goal keeps the stored one.
func mergeWorkspaceBootstrapForUpdate(sharedData map[string]any, input *workspaceBootstrapRequest) map[string]any {
	if input == nil {
		return workspaceBootstrapMap(sharedData)
	}
	goal := strings.TrimSpace(input.Goal)
	if goal == "" {
		goal = workspaceBootstrapStringValue(sharedData, "goal")
	}
	return normalizeWorkspaceBootstrap(&workspaceBootstrapRequest{
		Goal:         goal,
		Systems:      strings.TrimSpace(input.Systems),
		Capabilities: strings.TrimSpace(input.Capabilities),
		Context:      strings.TrimSpace(input.Context),
	})
}

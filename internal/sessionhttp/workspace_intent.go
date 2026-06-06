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

func mergeWorkspaceBootstrapForUpdate(sharedData map[string]any, description string, descriptionTouched bool, input *workspaceBootstrapRequest) map[string]any {
	if input != nil {
		goal := strings.TrimSpace(input.Goal)
		if descriptionTouched {
			goal = strings.TrimSpace(description)
		}
		if goal == "" {
			goal = strings.TrimSpace(description)
		}
		return normalizeWorkspaceBootstrap(&workspaceBootstrapRequest{
			Goal:         goal,
			Systems:      strings.TrimSpace(input.Systems),
			Capabilities: strings.TrimSpace(input.Capabilities),
			Context:      strings.TrimSpace(input.Context),
		})
	}

	if !descriptionTouched {
		return workspaceBootstrapMap(sharedData)
	}

	return normalizeWorkspaceBootstrap(&workspaceBootstrapRequest{
		Goal:         strings.TrimSpace(description),
		Systems:      workspaceBootstrapStringValue(sharedData, "systems"),
		Capabilities: workspaceBootstrapStringValue(sharedData, "capabilities"),
		Context:      workspaceBootstrapStringValue(sharedData, "context"),
	})
}

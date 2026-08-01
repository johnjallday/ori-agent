package chathttp

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	dependencyResolutionSurfaceModal     = "modal"
	dependencyResolutionSurfaceSetupFlow = "setup_flow"

	dependencyActionTypeOpenURL              = "open_url"
	dependencyActionTypeEnableWorkspaceMCP   = "workspace_enable_mcp_binding"
	dependencyActionTypeSuppressWorkspaceAsk = "suppress_dependency_prompt"

	dependencyTypeMCPMissing         = "mcp_missing"
	dependencyTypeWorkspaceMCP       = "workspace_mcp_binding"
	dependencyTypeWorkspaceRequired  = "workspace_required"
	dependencyTypeSkillMissing       = "skill_missing"
	dependencyTypeSkillDisabled      = "skill_disabled"
	dependencyTypeSkillTrustRequired = "skill_trust_required"
	dependencyTypeToolPermission     = "tool_permission"
)

var dependencyResolutionMCPTargetPattern = regexp.MustCompile(`mcp__([a-z0-9._-]+)`)

type dependencyResolution struct {
	Version            int                        `json:"version"`
	Title              string                     `json:"title"`
	Summary            string                     `json:"summary,omitempty"`
	ReasonCode         string                     `json:"reason_code,omitempty"`
	RecommendedSurface string                     `json:"recommended_surface,omitempty"`
	RetryContext       *dependencyRetryContext    `json:"retry_context,omitempty"`
	Steps              []dependencyResolutionStep `json:"steps,omitempty"`
}

type dependencyRetryContext struct {
	Supported bool   `json:"supported"`
	Strategy  string `json:"strategy,omitempty"`
}

type dependencyResolutionStep struct {
	ID           string                       `json:"id,omitempty"`
	Type         string                       `json:"type"`
	DisplayName  string                       `json:"display_name,omitempty"`
	Summary      string                       `json:"summary,omitempty"`
	RiskLevel    string                       `json:"risk_level,omitempty"`
	Suppressible bool                         `json:"suppressible,omitempty"`
	Actions      []dependencyResolutionAction `json:"actions,omitempty"`
}

type dependencyResolutionAction struct {
	Type           string `json:"type"`
	Label          string `json:"label"`
	Description    string `json:"description,omitempty"`
	Variant        string `json:"variant,omitempty"`
	URL            string `json:"url,omitempty"`
	WorkspaceID    string `json:"workspace_id,omitempty"`
	ServerName     string `json:"server_name,omitempty"`
	SkillName      string `json:"skill_name,omitempty"`
	DependencyType string `json:"dependency_type,omitempty"`
	PreferenceKey  string `json:"preference_key,omitempty"`
	AutoRetry      bool   `json:"auto_retry,omitempty"`
}

func attachDependencyResolution(payload map[string]any, resolution *dependencyResolution) map[string]any {
	if payload == nil {
		payload = make(map[string]any)
	}
	if resolution == nil {
		return payload
	}
	payload["dependency_resolution"] = resolution
	return payload
}

func buildDefaultRetryContext(supported bool) *dependencyRetryContext {
	return &dependencyRetryContext{
		Supported: supported,
		Strategy:  "repeat_request",
	}
}

func normalizeDependencyActionLabel(label, fallback string) string {
	if trimmed := strings.TrimSpace(label); trimmed != "" {
		return trimmed
	}
	return fallback
}

func buildSkillDependencyResolution(name string, dependencyType string) *dependencyResolution {
	trimmedName := strings.TrimSpace(name)
	if trimmedName == "" {
		return nil
	}

	title := "Skill action required"
	summary := "Review this skill before retrying."
	switch dependencyType {
	case dependencyTypeSkillMissing:
		title = fmt.Sprintf("Skill \"%s\" is not available", trimmedName)
		summary = "Open the Skills page to install or enable the missing skill."
	case dependencyTypeSkillDisabled:
		title = fmt.Sprintf("Skill \"%s\" is disabled", trimmedName)
		summary = "Open the Skills page to enable this skill, then retry."
	case dependencyTypeSkillTrustRequired:
		title = fmt.Sprintf("Skill \"%s\" requires trust", trimmedName)
		summary = "This skill includes privileged scripts and must be trusted explicitly before it can run."
	}

	return &dependencyResolution{
		Version:            1,
		Title:              title,
		Summary:            summary,
		ReasonCode:         dependencyType,
		RecommendedSurface: dependencyResolutionSurfaceSetupFlow,
		RetryContext:       buildDefaultRetryContext(false),
		Steps: []dependencyResolutionStep{
			{
				ID:          "skill-resolution",
				Type:        dependencyType,
				DisplayName: trimmedName,
				Summary:     summary,
				RiskLevel:   skillDependencyRiskLevel(dependencyType),
				Actions: []dependencyResolutionAction{
					{
						Type:        dependencyActionTypeOpenURL,
						Label:       "Open Skills",
						Description: "Review available skills and make the required change.",
						Variant:     "primary",
						URL:         "/skills",
						SkillName:   trimmedName,
					},
				},
			},
		},
	}
}

func skillDependencyRiskLevel(dependencyType string) string {
	switch dependencyType {
	case dependencyTypeSkillTrustRequired:
		return "high"
	default:
		return "low"
	}
}

func buildSkillRequiredMCPActions(workspaceID, serverName, preferenceKey string) []dependencyResolutionAction {
	if strings.TrimSpace(workspaceID) == "" {
		return []dependencyResolutionAction{
			{
				Type:        dependencyActionTypeOpenURL,
				Label:       "Open Workspace Map",
				Description: "Choose a workspace first, then enable the required connector.",
				Variant:     "primary",
				URL:         "/",
				ServerName:  strings.TrimSpace(serverName),
			},
			{
				Type:        dependencyActionTypeOpenURL,
				Label:       "Open MCP Settings",
				Description: "Review or change the MCP connector setup manually.",
				Variant:     "secondary",
				URL:         "/mcp",
				ServerName:  strings.TrimSpace(serverName),
			},
		}
	}

	actions := []dependencyResolutionAction{
		{
			Type:           dependencyActionTypeEnableWorkspaceMCP,
			Label:          "Enable in workspace",
			Description:    fmt.Sprintf("Attach %s to the current workspace and retry automatically.", strings.TrimSpace(serverName)),
			Variant:        "primary",
			WorkspaceID:    strings.TrimSpace(workspaceID),
			ServerName:     strings.TrimSpace(serverName),
			DependencyType: dependencyTypeWorkspaceMCP,
			AutoRetry:      strings.TrimSpace(workspaceID) != "",
		},
		{
			Type:        dependencyActionTypeOpenURL,
			Label:       "Open MCP Settings",
			Description: "Review or change the MCP connector setup manually.",
			Variant:     "secondary",
			URL:         "/mcp",
			WorkspaceID: strings.TrimSpace(workspaceID),
			ServerName:  strings.TrimSpace(serverName),
		},
	}

	if strings.TrimSpace(workspaceID) != "" {
		actions = append(actions, dependencyResolutionAction{
			Type:           dependencyActionTypeSuppressWorkspaceAsk,
			Label:          "Don't ask again in this workspace",
			Description:    "Suppress this low-risk prompt for the current workspace.",
			Variant:        "secondary",
			WorkspaceID:    strings.TrimSpace(workspaceID),
			ServerName:     strings.TrimSpace(serverName),
			DependencyType: dependencyTypeWorkspaceMCP,
			PreferenceKey:  strings.TrimSpace(preferenceKey),
		})
	}

	return actions
}

func inferDependencyResolutionFromText(responseText string, routeCtx normalizedChatRouteContext, providerName string) *dependencyResolution {
	if resolution := inferExternalProviderPermissionResolution(responseText, routeCtx, providerName); resolution != nil {
		return resolution
	}
	return nil
}

func inferExternalProviderPermissionResolution(responseText string, routeCtx normalizedChatRouteContext, providerName string) *dependencyResolution {
	trimmed := strings.TrimSpace(responseText)
	if trimmed == "" {
		return nil
	}

	lower := strings.ToLower(trimmed)
	if !looksLikeExternalProviderPermissionDenial(lower) {
		return nil
	}

	displayName, serverName := inferPermissionDependencyTarget(trimmed)
	title := "Tool permission required"
	summary := "The current external agent blocked a required tool under its permission mode. Review agent permissions, then retry."
	if displayName != "" {
		title = fmt.Sprintf("%s permission required", displayName)
		summary = fmt.Sprintf("The current external agent blocked %s under its permission mode. Review agent permissions, then retry.", displayName)
	}

	actions := []dependencyResolutionAction{
		{
			Type:        dependencyActionTypeOpenURL,
			Label:       "Open Agents",
			Description: "Review external agent permissions and allow the required tool or MCP, then retry this request.",
			Variant:     "primary",
			URL:         "/agents",
		},
	}
	if strings.TrimSpace(serverName) != "" {
		actions = append(actions, dependencyResolutionAction{
			Type:        dependencyActionTypeOpenURL,
			Label:       "Open MCP Settings",
			Description: "Confirm the required MCP connector is installed and available.",
			Variant:     "secondary",
			URL:         "/mcp",
			WorkspaceID: strings.TrimSpace(routeCtx.WorkspaceID),
			ServerName:  strings.TrimSpace(serverName),
		})
	}

	if providerLabel := externalProviderLabel(providerName); providerLabel != "" {
		summary = strings.Replace(summary, "external agent", providerLabel, 1)
	}

	return &dependencyResolution{
		Version:            1,
		Title:              title,
		Summary:            summary,
		ReasonCode:         "provider_permission_denied",
		RecommendedSurface: dependencyResolutionSurfaceModal,
		RetryContext:       buildDefaultRetryContext(false),
		Steps: []dependencyResolutionStep{
			{
				ID:          "external-tool-permission",
				Type:        dependencyTypeToolPermission,
				DisplayName: normalizeDependencyActionLabel(displayName, "Required tool"),
				Summary:     summary,
				RiskLevel:   "medium",
				Actions:     actions,
			},
		},
	}
}

func looksLikeExternalProviderPermissionDenial(lower string) bool {
	switch {
	case strings.Contains(lower, "denied permission"),
		strings.Contains(lower, "permission mode"),
		strings.Contains(lower, "permission settings"),
		strings.Contains(lower, "permissions settings"),
		strings.Contains(lower, "isn't enabled in the current permission mode"),
		strings.Contains(lower, "is not enabled in the current permission mode"),
		strings.Contains(lower, "enable the mcp__"),
		strings.Contains(lower, "enable the `mcp__"),
		strings.Contains(lower, "enable the 'mcp__"),
		strings.Contains(lower, "enable the \"mcp__"):
		return strings.Contains(lower, "tool") || strings.Contains(lower, "mcp") || strings.Contains(lower, "reaper")
	default:
		return false
	}
}

func inferPermissionDependencyTarget(responseText string) (displayName, serverName string) {
	lower := strings.ToLower(responseText)

	if strings.Contains(lower, "mcp__ori-reaper") || strings.Contains(lower, "ori-reaper") || strings.Contains(lower, "reaper") {
		return "REAPER MCP tool", "ori-reaper"
	}

	if matches := dependencyResolutionMCPTargetPattern.FindStringSubmatch(lower); len(matches) == 2 {
		serverName = strings.TrimSpace(matches[1])
		displayName = humanizeDependencyServerName(serverName)
		if displayName != "" {
			displayName += " MCP tool"
		}
		return strings.TrimSpace(displayName), serverName
	}

	if strings.Contains(lower, "mcp tool") {
		return "MCP tool", ""
	}
	if strings.Contains(lower, "tool") {
		return "Required tool", ""
	}
	return "", ""
}

func humanizeDependencyServerName(serverName string) string {
	cleaned := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(serverName, "mcp__"), "ori-"))
	if cleaned == "" {
		return ""
	}
	if strings.EqualFold(cleaned, "reaper") {
		return "REAPER"
	}
	parts := strings.FieldsFunc(cleaned, func(r rune) bool {
		return r == '-' || r == '_' || r == '.'
	})
	for idx, part := range parts {
		if part == "" {
			continue
		}
		if len(part) <= 3 {
			parts[idx] = strings.ToUpper(part)
			continue
		}
		parts[idx] = strings.ToUpper(part[:1]) + part[1:]
	}
	return strings.Join(parts, " ")
}

func externalProviderLabel(providerName string) string {
	switch strings.ToLower(strings.TrimSpace(providerName)) {
	case "claude_code":
		return "Claude Code"
	case "codex":
		return "Codex"
	default:
		return ""
	}
}

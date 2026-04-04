package chathttp

import (
	"fmt"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

type mcpAutoRequirement struct {
	label            string
	phrases          []string
	candidateServers []string
}

type mcpPreflightResult struct {
	requirementLabel     string
	serverName           string
	userMessage          string
	dependencyResolution *dependencyResolution
}

var mcpAutoRequirements = []mcpAutoRequirement{
	{
		label: "browser automation",
		phrases: []string{
			"open website",
			"browse to",
			"click on",
			"fill out form",
			"browser automation",
			"automate browser",
		},
		candidateServers: []string{"playwright", "browserbase", "puppeteer"},
	},
	{
		label: "web research",
		phrases: []string{
			"search the web",
			"web search",
			"search online",
			"on web",
			"look up",
			"lookup",
			"internet search",
			"latest news",
		},
		candidateServers: []string{"brave-search", "web-search", "search"},
	},
}

func (h *Handler) maybeAutoEnableMCPForPrompt(
	agentName string,
	ag *resolvedChatAgent,
	prompt string,
	routeCtx normalizedChatRouteContext,
) (*mcpPreflightResult, *resolvedChatAgent) {
	if h == nil || ag == nil || h.store == nil || h.mcpConfigManager == nil || h.mcpRegistry == nil {
		return nil, nil
	}
	if !isSystemAssistantForPreflight(agentName) {
		return nil, nil
	}

	requirement := detectMCPAutoRequirement(prompt)
	if requirement == nil {
		return nil, nil
	}
	if hasAnyMCPServer(ag.MCPServers, requirement.candidateServers) {
		return &mcpPreflightResult{
			requirementLabel: requirement.label,
		}, nil
	}

	workspaceID := strings.TrimSpace(routeCtx.WorkspaceID)

	serverName := h.selectAvailableMCPServer(requirement.candidateServers)
	if serverName == "" {
		if workspaceID != "" && h.isDependencyPromptSuppressed(workspaceID, dependencyTypeMCPMissing, firstCandidateServer(requirement)) {
			return &mcpPreflightResult{
				requirementLabel: requirement.label,
				userMessage:      buildMissingMCPMessage(requirement),
			}, nil
		}
		return &mcpPreflightResult{
			requirementLabel:     requirement.label,
			userMessage:          buildMissingMCPMessage(requirement),
			dependencyResolution: buildMissingMCPDependencyResolution(requirement, workspaceID),
		}, nil
	}
	if workspaceID != "" && h.isDependencyPromptSuppressed(workspaceID, dependencyTypeWorkspaceMCP, serverName) {
		return &mcpPreflightResult{
			requirementLabel: requirement.label,
			serverName:       serverName,
			userMessage:      buildWorkspaceEnableMCPMessage(requirement, serverName),
		}, nil
	}

	if workspaceID == "" || h.workspaceStore == nil {
		return &mcpPreflightResult{
			requirementLabel:     requirement.label,
			serverName:           serverName,
			userMessage:          buildWorkspaceRequiredMCPMessage(requirement),
			dependencyResolution: buildWorkspaceRequiredDependencyResolution(requirement, serverName),
		}, nil
	}

	return &mcpPreflightResult{
		requirementLabel: requirement.label,
		serverName:       serverName,
		userMessage:      buildWorkspaceEnableMCPMessage(requirement, serverName),
		dependencyResolution: buildWorkspaceEnableMCPDependencyResolution(
			requirement,
			workspaceID,
			serverName,
		),
	}, nil
}

func isSystemAssistantForPreflight(agentName string) bool {
	normalized := strings.ToLower(strings.TrimSpace(agentName))
	return normalized == "ori" || normalized == "__assistant__"
}

func detectMCPAutoRequirement(prompt string) *mcpAutoRequirement {
	text := normalizeMCPPreflightPrompt(prompt)
	if text == "" {
		return nil
	}

	for i := range mcpAutoRequirements {
		req := &mcpAutoRequirements[i]
		for _, phrase := range req.phrases {
			if strings.Contains(text, phrase) {
				return req
			}
		}
	}

	if looksLikeBrowserAutomationPrompt(text) {
		return findMCPAutoRequirementByLabel("browser automation")
	}
	if looksLikeWebResearchPrompt(text) {
		return findMCPAutoRequirementByLabel("web research")
	}

	return nil
}

func normalizeMCPPreflightPrompt(prompt string) string {
	return strings.ToLower(strings.TrimSpace(prompt))
}

func looksLikeWebResearchPrompt(text string) bool {
	if text == "" {
		return false
	}
	hasWebContext := containsAnyPhrase(text, []string{"web", "online", "internet"})
	if !hasWebContext {
		return false
	}
	return containsAnyPhrase(text, []string{"search", "look up", "lookup", "find", "check", "latest", "news"})
}

func looksLikeBrowserAutomationPrompt(text string) bool {
	if text == "" {
		return false
	}
	hasBrowserContext := containsAnyPhrase(text, []string{"browser", "website", "web page", "webpage", "site"}) || containsLikelyWebTarget(text)
	if !hasBrowserContext {
		return false
	}
	return containsAnyPhrase(text, []string{"open", "click", "fill", "type", "submit", "navigate", "scroll", "visit", "go to"})
}

func containsAnyPhrase(text string, phrases []string) bool {
	for _, phrase := range phrases {
		candidate := strings.TrimSpace(phrase)
		if candidate == "" {
			continue
		}
		if strings.Contains(text, candidate) {
			return true
		}
	}
	return false
}

func containsLikelyWebTarget(text string) bool {
	if text == "" {
		return false
	}

	if strings.Contains(text, "http://") || strings.Contains(text, "https://") || strings.Contains(text, "www.") {
		return true
	}

	tokens := strings.Fields(text)
	for _, token := range tokens {
		candidate := strings.TrimSpace(strings.Trim(token, " \t\r\n.,!?;:\"'()[]{}"))
		if candidate == "" {
			continue
		}
		if !strings.Contains(candidate, ".") {
			continue
		}
		// Skip obvious email-like targets.
		if strings.Contains(candidate, "@") {
			continue
		}
		labels := strings.Split(candidate, ".")
		if len(labels) < 2 {
			continue
		}
		valid := true
		for _, label := range labels {
			label = strings.TrimSpace(label)
			if label == "" {
				valid = false
				break
			}
			for _, r := range label {
				if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
					valid = false
					break
				}
			}
			if !valid {
				break
			}
		}
		if valid && len(labels[len(labels)-1]) >= 2 {
			return true
		}
	}

	return false
}

func hasAnyMCPServer(enabledServers, candidates []string) bool {
	candidateSet := normalizeLogicalMCPServerSet(candidates)
	for _, enabled := range enabledServers {
		normalizedEnabled := normalizeLogicalMCPServerName(enabled)
		if candidateSet[normalizedEnabled] {
			return true
		}
	}
	return false
}

func (h *Handler) attachWorkspaceMCPBindingForPrompt(agentName, workspaceID, serverName string) (*resolvedChatAgent, error) {
	if h == nil || h.workspaceStore == nil {
		return nil, fmt.Errorf("workspace store is not configured")
	}

	ws, err := h.workspaceStore.Get(workspaceID)
	if err != nil {
		return nil, fmt.Errorf("load workspace: %w", err)
	}

	binding := findWorkspaceMCPBindingByServerName(ws.GetMCPBindings(), serverName)
	if binding == nil {
		binding = &workspace.WorkspaceMCPBinding{
			ID:         autoWorkspaceMCPBindingID(serverName),
			ServerName: serverName,
			Alias:      autoWorkspaceMCPBindingAlias(serverName),
			Enabled:    true,
			CreatedAt:  time.Now(),
			UpdatedAt:  time.Now(),
		}
	} else {
		binding.Enabled = true
	}

	if err := ws.UpsertMCPBinding(*binding); err != nil {
		return nil, fmt.Errorf("upsert workspace MCP binding: %w", err)
	}

	if instance, ok := ws.FindAgentInstance(agentName, ""); ok {
		if access, exists := ws.GetAgentMCPAccess(instance.ID); exists {
			alreadyEnabled := false
			normalizedNewID := strings.ToLower(strings.TrimSpace(binding.ID))
			for _, id := range access.EnabledBindingIDs {
				if strings.ToLower(strings.TrimSpace(id)) == normalizedNewID {
					alreadyEnabled = true
					break
				}
			}
			if !alreadyEnabled {
				access.EnabledBindingIDs = append(access.EnabledBindingIDs, binding.ID)
				if err := ws.SetAgentMCPAccess(*access); err != nil {
					return nil, fmt.Errorf("update agent MCP access: %w", err)
				}
			}
		}
	}

	if err := h.workspaceStore.Save(ws); err != nil {
		return nil, fmt.Errorf("save workspace: %w", err)
	}

	if h.runtimeResolver == nil {
		return nil, nil
	}

	resolved, err := h.runtimeResolver.ResolveAgentForWorkspace(agentName, workspaceID, "")
	if err != nil {
		return nil, fmt.Errorf("resolve workspace runtime: %w", err)
	}
	if resolved == nil {
		return nil, nil
	}
	return &resolvedChatAgent{
		Agent:      resolved.Agent,
		MCPServers: append([]string{}, resolved.MCPServers...),
	}, nil
}

func findWorkspaceMCPBindingByServerName(bindings []workspace.WorkspaceMCPBinding, serverName string) *workspace.WorkspaceMCPBinding {
	target := strings.ToLower(strings.TrimSpace(serverName))
	for i := range bindings {
		if strings.ToLower(strings.TrimSpace(bindings[i].ServerName)) == target {
			copy := bindings[i]
			return &copy
		}
	}
	return nil
}

func autoWorkspaceMCPBindingID(serverName string) string {
	return "auto-" + sanitizeWorkspaceMCPToken(serverName)
}

func autoWorkspaceMCPBindingAlias(serverName string) string {
	return sanitizeWorkspaceMCPToken(serverName)
}

func sanitizeWorkspaceMCPToken(value string) string {
	trimmed := strings.ToLower(strings.TrimSpace(value))
	if trimmed == "" {
		return "mcp"
	}
	var b strings.Builder
	for _, r := range trimmed {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
		case r == '-' || r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return strings.Trim(strings.ReplaceAll(b.String(), "--", "-"), "-")
}

func buildWorkspaceRequiredMCPMessage(requirement *mcpAutoRequirement) string {
	if requirement == nil {
		return "This request needs an MCP binding in a workspace."
	}
	return fmt.Sprintf("This request needs %s tools, but they require a workspace. Please open a workspace first, then try again.", requirement.label)
}

func buildWorkspaceEnableMCPMessage(requirement *mcpAutoRequirement, serverName string) string {
	if requirement == nil {
		return fmt.Sprintf("This request needs the %s MCP connector enabled in the current workspace before I can continue.", strings.TrimSpace(serverName))
	}
	return fmt.Sprintf("This request needs %s tools. Enable the %s MCP connector for this workspace to continue.", requirement.label, strings.TrimSpace(serverName))
}

func buildWorkspaceAttachMCPFailureMessage(serverName string) string {
	trimmed := strings.TrimSpace(serverName)
	if trimmed == "" {
		return "I couldn't attach the required MCP binding to the active workspace."
	}
	return fmt.Sprintf("I couldn't attach the %s MCP binding to the active workspace.", trimmed)
}

func findMCPAutoRequirementByLabel(label string) *mcpAutoRequirement {
	target := strings.ToLower(strings.TrimSpace(label))
	for i := range mcpAutoRequirements {
		req := &mcpAutoRequirements[i]
		if strings.ToLower(strings.TrimSpace(req.label)) == target {
			return req
		}
	}
	return nil
}

func (h *Handler) selectAvailableMCPServer(candidates []string) string {
	if h == nil || h.mcpConfigManager == nil {
		return ""
	}

	for _, serverName := range candidates {
		candidate := strings.TrimSpace(serverName)
		if candidate == "" {
			continue
		}
		if _, err := h.mcpConfigManager.GetServer(candidate); err == nil {
			return candidate
		}
	}

	return ""
}

func buildMissingMCPMessage(requirement *mcpAutoRequirement) string {
	if requirement == nil {
		return "I cannot run this tool request because no suitable MCP server is configured."
	}
	switch strings.ToLower(strings.TrimSpace(requirement.label)) {
	case "web research":
		return "I cannot run a real web search yet because no web MCP server is configured. Add one in MCP settings (recommended: brave-search with BRAVE_API_KEY), then ask again."
	case "browser automation":
		return "I cannot run browser automation yet because no browser MCP server is configured. Add one in MCP settings (for example: playwright), then ask again."
	default:
		return "I cannot run this tool request because no suitable MCP server is configured."
	}
}

func (h *Handler) isDependencyPromptSuppressed(workspaceID, dependencyType, target string) bool {
	if h == nil || h.workspaceStore == nil {
		return false
	}
	ws, err := h.workspaceStore.Get(strings.TrimSpace(workspaceID))
	if err != nil || ws == nil {
		return false
	}
	preferenceKey := workspace.DependencyPreferenceKey(dependencyType, target)
	pref, ok := ws.GetDependencyPreference(preferenceKey)
	if !ok {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(pref.Value), "suppressed")
}

func buildMissingMCPDependencyResolution(requirement *mcpAutoRequirement, workspaceID string) *dependencyResolution {
	if requirement == nil {
		return nil
	}

	title := "Required MCP connector is not configured"
	summary := "This request needs a compatible MCP connector before it can continue."
	switch strings.ToLower(strings.TrimSpace(requirement.label)) {
	case "web research":
		title = "Web search MCP is not configured"
		summary = "Set up a web-search MCP connector, then retry this request."
	case "browser automation":
		title = "Browser automation MCP is not configured"
		summary = "Set up a browser automation MCP connector, then retry this request."
	}

	actions := []dependencyResolutionAction{
		{
			Type:        dependencyActionTypeOpenURL,
			Label:       "Open MCP Settings",
			Description: "Configure or install the required MCP connector.",
			Variant:     "primary",
			URL:         "/mcp",
		},
	}

	if strings.TrimSpace(workspaceID) != "" {
		serverName := firstCandidateServer(requirement)
		preferenceKey := workspace.DependencyPreferenceKey(dependencyTypeMCPMissing, serverName)
		actions = append(actions, dependencyResolutionAction{
			Type:           dependencyActionTypeSuppressWorkspaceAsk,
			Label:          "Don't ask again in this workspace",
			Description:    "Suppress this low-risk prompt for the current workspace.",
			Variant:        "secondary",
			WorkspaceID:    workspaceID,
			ServerName:     serverName,
			DependencyType: dependencyTypeMCPMissing,
			PreferenceKey:  preferenceKey,
		})
	}

	return &dependencyResolution{
		Version:            1,
		Title:              title,
		Summary:            summary,
		ReasonCode:         "mcp_missing",
		RecommendedSurface: dependencyResolutionSurfaceSetupFlow,
		RetryContext:       buildDefaultRetryContext(false),
		Steps: []dependencyResolutionStep{
			{
				ID:           "missing-mcp",
				Type:         dependencyTypeMCPMissing,
				DisplayName:  strings.TrimSpace(requirement.label),
				Summary:      summary,
				RiskLevel:    "low",
				Suppressible: strings.TrimSpace(workspaceID) != "",
				Actions:      actions,
			},
		},
	}
}

func buildWorkspaceRequiredDependencyResolution(requirement *mcpAutoRequirement, serverName string) *dependencyResolution {
	summary := "Open or create a workspace before enabling the connector and retrying."
	return &dependencyResolution{
		Version:            1,
		Title:              "Workspace required",
		Summary:            summary,
		ReasonCode:         "workspace_required",
		RecommendedSurface: dependencyResolutionSurfaceSetupFlow,
		RetryContext:       buildDefaultRetryContext(false),
		Steps: []dependencyResolutionStep{
			{
				ID:          "workspace-required",
				Type:        dependencyTypeWorkspaceRequired,
				DisplayName: strings.TrimSpace(requirement.label),
				Summary:     summary,
				RiskLevel:   "low",
				Actions: []dependencyResolutionAction{
					{
						Type:        dependencyActionTypeOpenURL,
						Label:       "Open Workspaces",
						Description: "Choose a workspace, then retry this request.",
						Variant:     "primary",
						URL:         "/workspaces",
						ServerName:  serverName,
					},
				},
			},
		},
	}
}

func buildWorkspaceEnableMCPDependencyResolution(requirement *mcpAutoRequirement, workspaceID, serverName string) *dependencyResolution {
	serverName = strings.TrimSpace(serverName)
	summary := fmt.Sprintf("Enable the %s MCP connector in this workspace to continue automatically.", serverName)
	preferenceKey := workspace.DependencyPreferenceKey(dependencyTypeWorkspaceMCP, serverName)

	return &dependencyResolution{
		Version:            1,
		Title:              "Enable MCP connector for this workspace",
		Summary:            summary,
		ReasonCode:         "workspace_mcp_binding_missing",
		RecommendedSurface: dependencyResolutionSurfaceModal,
		RetryContext:       buildDefaultRetryContext(true),
		Steps: []dependencyResolutionStep{
			{
				ID:           "enable-workspace-mcp",
				Type:         dependencyTypeWorkspaceMCP,
				DisplayName:  serverName,
				Summary:      summary,
				RiskLevel:    "low",
				Suppressible: true,
				Actions: []dependencyResolutionAction{
					{
						Type:           dependencyActionTypeEnableWorkspaceMCP,
						Label:          normalizeDependencyActionLabel("Enable in workspace", "Enable in workspace"),
						Description:    fmt.Sprintf("Attach %s to workspace %s and retry automatically.", serverName, workspaceID),
						Variant:        "primary",
						WorkspaceID:    workspaceID,
						ServerName:     serverName,
						DependencyType: dependencyTypeWorkspaceMCP,
						AutoRetry:      true,
					},
					{
						Type:           dependencyActionTypeSuppressWorkspaceAsk,
						Label:          "Don't ask again in this workspace",
						Description:    "Suppress this low-risk prompt for the current workspace.",
						Variant:        "secondary",
						WorkspaceID:    workspaceID,
						ServerName:     serverName,
						DependencyType: dependencyTypeWorkspaceMCP,
						PreferenceKey:  preferenceKey,
					},
					{
						Type:        dependencyActionTypeOpenURL,
						Label:       "Open MCP Settings",
						Description: "Review or change the connector setup manually.",
						Variant:     "secondary",
						URL:         "/mcp",
						WorkspaceID: workspaceID,
						ServerName:  serverName,
					},
				},
			},
		},
	}
}

func firstCandidateServer(requirement *mcpAutoRequirement) string {
	if requirement == nil || len(requirement.candidateServers) == 0 {
		return ""
	}
	return strings.TrimSpace(requirement.candidateServers[0])
}

package chathttp

import (
	"fmt"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type mcpAutoRequirement struct {
	label            string
	phrases          []string
	candidateServers []string
}

type mcpPreflightResult struct {
	requirementLabel string
	serverName       string
	userMessage      string
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

	serverName := h.selectAvailableMCPServer(requirement.candidateServers)
	if serverName == "" {
		return &mcpPreflightResult{
			requirementLabel: requirement.label,
			userMessage:      buildMissingMCPMessage(requirement),
		}, nil
	}

	workspaceID := strings.TrimSpace(routeCtx.WorkspaceID)
	if workspaceID == "" || h.workspaceStore == nil {
		return &mcpPreflightResult{
			requirementLabel: requirement.label,
			serverName:       serverName,
			userMessage:      buildWorkspaceRequiredMCPMessage(requirement),
		}, nil
	}

	updatedAgent, err := h.attachWorkspaceMCPBindingForPrompt(agentName, workspaceID, serverName)
	if err != nil {
		logger.Warn("Failed to auto-attach workspace MCP binding for prompt", logger.Fields{
			"agent":        agentName,
			"workspace_id": workspaceID,
			"server":       serverName,
			"error":        err,
		})
		return &mcpPreflightResult{
			requirementLabel: requirement.label,
			serverName:       serverName,
			userMessage:      buildWorkspaceAttachMCPFailureMessage(serverName),
		}, nil
	}

	logger.Info("Auto-attached workspace MCP binding for assistant prompt", logger.Fields{
		"agent":        agentName,
		"workspace_id": workspaceID,
		"server":       serverName,
		"requirement":  requirement.label,
	})
	return &mcpPreflightResult{
		requirementLabel: requirement.label,
		serverName:       serverName,
	}, updatedAgent
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

func appendMCPServerIfMissing(servers *[]string, serverName string) bool {
	if servers == nil {
		return false
	}
	for _, existing := range *servers {
		if strings.EqualFold(strings.TrimSpace(existing), strings.TrimSpace(serverName)) {
			return false
		}
	}
	*servers = append(*servers, serverName)
	return true
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
			access.EnabledBindingIDs = append(access.EnabledBindingIDs, binding.ID)
			if err := ws.SetAgentMCPAccess(*access); err != nil {
				return nil, fmt.Errorf("update agent MCP access: %w", err)
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
	return fmt.Sprintf("This request needs %s tools, but MCP bindings are workspace-scoped now. Open it from a workspace and try again.", requirement.label)
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

func isMCPServerAlreadyRunningError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "already running")
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

func buildEnableMCPFailureMessage(serverName string) string {
	name := strings.TrimSpace(serverName)
	if name == "" {
		return "I found a required MCP server but failed to enable it. Check MCP settings and try again."
	}
	return "I found MCP server \"" + name + "\" but failed to enable it. Check MCP settings and try again."
}

func buildStartMCPFailureMessage(serverName string) string {
	name := strings.TrimSpace(serverName)
	if name == "" {
		return "I enabled the required MCP server, but it failed to start. Check MCP settings and runtime dependencies, then try again."
	}
	return "I enabled MCP server \"" + name + "\", but it failed to start. Check MCP settings and runtime dependencies, then try again."
}

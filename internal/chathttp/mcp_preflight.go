package chathttp

import (
	"strings"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/logger"
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

func (h *Handler) maybeAutoEnableMCPForPrompt(agentName string, ag *agent.Agent, prompt string) *mcpPreflightResult {
	if h == nil || ag == nil || h.store == nil || h.mcpConfigManager == nil || h.mcpRegistry == nil {
		return nil
	}
	if !isSystemAssistantForPreflight(agentName) {
		return nil
	}

	requirement := detectMCPAutoRequirement(prompt)
	if requirement == nil {
		return nil
	}
	if hasAnyMCPServer(ag.MCPServers, requirement.candidateServers) {
		return &mcpPreflightResult{
			requirementLabel: requirement.label,
		}
	}

	serverName := h.selectAvailableMCPServer(requirement.candidateServers)
	if serverName == "" {
		return &mcpPreflightResult{
			requirementLabel: requirement.label,
			userMessage:      buildMissingMCPMessage(requirement),
		}
	}

	if err := h.mcpConfigManager.EnableServerForAgent(agentName, serverName); err != nil {
		logger.Warn("Failed to auto-enable MCP server for agent", logger.Fields{
			"agent":  agentName,
			"server": serverName,
			"err":    err,
		})
		return &mcpPreflightResult{
			requirementLabel: requirement.label,
			serverName:       serverName,
			userMessage:      buildEnableMCPFailureMessage(serverName),
		}
	}

	if appendMCPServerIfMissing(&ag.MCPServers, serverName) {
		if err := h.store.SetAgent(agentName, ag); err != nil {
			logger.Warn("Failed to persist auto-enabled MCP server for agent", logger.Fields{
				"agent":  agentName,
				"server": serverName,
				"err":    err,
			})
		}
	}

	if err := h.mcpRegistry.StartServer(serverName); err != nil && !isMCPServerAlreadyRunningError(err) {
		logger.Warn("Auto-enabled MCP server but failed to start it", logger.Fields{
			"agent":  agentName,
			"server": serverName,
			"err":    err,
		})
		return &mcpPreflightResult{
			requirementLabel: requirement.label,
			serverName:       serverName,
			userMessage:      buildStartMCPFailureMessage(serverName),
		}
	}

	logger.Info("Auto-enabled MCP server for assistant prompt", logger.Fields{
		"agent":       agentName,
		"server":      serverName,
		"requirement": requirement.label,
	})
	return &mcpPreflightResult{
		requirementLabel: requirement.label,
		serverName:       serverName,
	}
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
	for _, enabled := range enabledServers {
		for _, candidate := range candidates {
			if strings.EqualFold(strings.TrimSpace(enabled), strings.TrimSpace(candidate)) {
				return true
			}
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

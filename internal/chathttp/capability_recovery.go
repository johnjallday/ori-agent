package chathttp

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/openai/openai-go/v3"

	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/skills"
	"github.com/johnjallday/ori-agent/internal/types"
)

const capabilityRecoverySearchTimeout = 6 * time.Second

var (
	capabilityRecoveryANSIPattern         = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)
	capabilityRecoveryPackagePattern      = regexp.MustCompile(`^([A-Za-z0-9._-]+/[A-Za-z0-9._-]+@[A-Za-z0-9._-]+)\b`)
	capabilityRecoveryMarketplaceSearchFn = searchCapabilityRecoveryMarketplaceSkills
)

type capabilityRecoveryIntent struct {
	Kind         string
	Recipient    string
	MarketplaceQ string
}

type capabilityRecoverySuggestion struct {
	Type        string `json:"type"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Command     string `json:"command,omitempty"`
	URL         string `json:"url,omitempty"`
}

type capabilityRecoveryMarketplaceSkill struct {
	Package string `json:"package"`
	URL     string `json:"url,omitempty"`
}

type capabilityRecoverySnapshot struct {
	HasDirectCommunicationTool bool
	ProviderSupportsTools      bool
	HasBrowserTool             bool
	OpenAppAvailable           bool
	ActiveMCPServers           []string
	InstalledSkills            []skills.Skill
	MarketplaceSkills          []capabilityRecoveryMarketplaceSkill
}

func (h *Handler) maybeHandleCapabilityRecovery(
	w http.ResponseWriter,
	baseCtx context.Context,
	ag *resolvedChatAgent,
	agentName string,
	userMessage string,
	sessionID string,
	tools []llm.Tool,
	plannerDecision *types.PlannerDecision,
) bool {
	if h == nil || ag == nil || ag.Agent == nil {
		return false
	}

	intent, ok := detectCapabilityRecoveryIntent(userMessage)
	if !ok {
		return false
	}

	snapshot := h.buildCapabilityRecoverySnapshot(baseCtx, ag, agentName, tools, intent)
	if snapshot.HasDirectCommunicationTool && snapshot.ProviderSupportsTools {
		return false
	}

	responseText, suggestions := buildCapabilityRecoveryResponse(intent, snapshot)
	if strings.TrimSpace(responseText) == "" {
		return false
	}

	ag.Messages = append(ag.Messages, openai.UserMessage(userMessage))
	ag.Messages = append(ag.Messages, openai.AssistantMessage(responseText))
	if err := h.persistAgent(agentName, ag.Agent); err != nil {
		logger.Warn("Failed to persist agent after capability recovery", logger.Fields{"agent": agentName, "error": err})
	}

	h.storeMessageInSession(baseCtx, sessionID, "user", userMessage)
	h.storeMessageInSession(baseCtx, sessionID, "assistant", responseText)

	reason := "no matching communication capability was available"
	if !snapshot.ProviderSupportsTools && snapshot.HasDirectCommunicationTool {
		reason = "current provider path does not support calling available tools"
	}

	receipt := buildActionReceipt(
		"capability_recovery",
		"Generated capability recovery guidance",
		reason,
		"",
		userMessage,
		responseText,
		0,
		true,
		"",
	)

	payload := map[string]any{
		"response":             responseText,
		"capability_recovery":  true,
		"recovery_intent":      intent.Kind,
		"recovery_recipient":   intent.Recipient,
		"recovery_suggestions": suggestions,
	}

	writeJSONResponse(w, attachPlannerDecision(attachActionReceipts(attachRouteMetadata(payload, chatRouteMetadata{
		Mode:   routeModeAssistantChat,
		Reason: "capability recovery guidance",
	}), []ActionReceipt{receipt}), plannerDecision))
	return true
}

func (h *Handler) buildCapabilityRecoverySnapshot(
	ctx context.Context,
	ag *resolvedChatAgent,
	agentName string,
	tools []llm.Tool,
	intent capabilityRecoveryIntent,
) capabilityRecoverySnapshot {
	snapshot := capabilityRecoverySnapshot{
		HasDirectCommunicationTool: hasDirectCommunicationTool(tools),
		ProviderSupportsTools:      agentProviderSupportsTools(ag),
		HasBrowserTool:             hasBrowserLikeTool(tools),
		OpenAppAvailable:           true,
		ActiveMCPServers:           append([]string{}, ag.MCPServers...),
	}

	if h.skillsManager != nil {
		skillsList, err := h.skillsManager.ListSkills(agentName)
		if err == nil {
			snapshot.InstalledSkills = filterCapabilityRecoverySkills(skillsList)
		}
	}

	if len(snapshot.InstalledSkills) == 0 {
		searchCtx, cancel := context.WithTimeout(ctx, capabilityRecoverySearchTimeout)
		defer cancel()
		snapshot.MarketplaceSkills, _ = capabilityRecoveryMarketplaceSearchFn(searchCtx, intent.MarketplaceQ)
	}

	return snapshot
}

func detectCapabilityRecoveryIntent(query string) (capabilityRecoveryIntent, bool) {
	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		return capabilityRecoveryIntent{}, false
	}
	if isPlanningFormSubmissionPrompt(trimmed) {
		return capabilityRecoveryIntent{}, false
	}
	lower := strings.ToLower(trimmed)

	if containsAnyPhrase(lower, []string{"open my inbox", "check my inbox", "show my inbox", "open inbox"}) {
		return capabilityRecoveryIntent{
			Kind:         "open_inbox",
			MarketplaceQ: "email gmail outlook inbox",
		}, true
	}

	if containsAnyPhrase(lower, []string{"send", "email", "mail", "message", "text", "sms", "reply"}) {
		if containsAnyPhrase(lower, []string{"send ", "email ", "mail ", "message ", "text ", "sms ", "reply "}) ||
			containsAnyPhrase(lower, []string{"send this", "send that", "send this note", "email this", "message this", "reply to"}) {
			intent := capabilityRecoveryIntent{
				Kind:         "send_communication",
				Recipient:    extractCommunicationRecipient(trimmed),
				MarketplaceQ: "email messaging gmail outlook smtp imap",
			}
			return intent, true
		}
	}

	return capabilityRecoveryIntent{}, false
}

func extractCommunicationRecipient(query string) string {
	lower := strings.ToLower(query)
	idx := strings.Index(lower, " to ")
	if idx < 0 {
		return ""
	}
	recipient := strings.TrimSpace(query[idx+4:])
	for _, stop := range []string{" about ", " saying ", " with ", " that ", ",", ".", "!", "?"} {
		if cut := strings.Index(strings.ToLower(recipient), stop); cut >= 0 {
			recipient = recipient[:cut]
			break
		}
	}
	return strings.TrimSpace(strings.Trim(recipient, `"'`))
}

func hasDirectCommunicationTool(tools []llm.Tool) bool {
	for _, tool := range tools {
		name := strings.ToLower(strings.TrimSpace(tool.Name))
		if name == "" {
			continue
		}
		if strings.Contains(name, "email") ||
			strings.Contains(name, "gmail") ||
			strings.Contains(name, "outlook") ||
			strings.Contains(name, "mail") ||
			strings.Contains(name, "smtp") ||
			strings.Contains(name, "imap") ||
			strings.Contains(name, "message") ||
			strings.Contains(name, "sms") ||
			strings.Contains(name, "twilio") ||
			strings.Contains(name, "slack") ||
			strings.Contains(name, "discord") {
			return true
		}
	}
	return false
}

func hasBrowserLikeTool(tools []llm.Tool) bool {
	for _, tool := range tools {
		name := strings.ToLower(strings.TrimSpace(tool.Name))
		switch name {
		case "browser", "browser_navigate", "navigate":
			return true
		}
	}
	return false
}

func agentProviderSupportsTools(ag *resolvedChatAgent) bool {
	if ag == nil || ag.Agent == nil {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(ag.Settings.Provider), "claude_code") {
		return false
	}
	return !isCodexProviderOrModel(ag.Settings.Provider, ag.Settings.Model)
}

func filterCapabilityRecoverySkills(all []skills.Skill) []skills.Skill {
	filtered := make([]skills.Skill, 0, 3)
	for _, skill := range all {
		if len(filtered) >= 3 {
			break
		}
		text := strings.ToLower(strings.TrimSpace(skill.Name + " " + skill.Description))
		if text == "" {
			continue
		}
		if strings.Contains(text, "email") ||
			strings.Contains(text, "gmail") ||
			strings.Contains(text, "outlook") ||
			strings.Contains(text, "mail") ||
			strings.Contains(text, "smtp") ||
			strings.Contains(text, "imap") ||
			strings.Contains(text, "message") ||
			strings.Contains(text, "messaging") {
			filtered = append(filtered, skill)
		}
	}
	return filtered
}

func buildCapabilityRecoveryResponse(
	intent capabilityRecoveryIntent,
	snapshot capabilityRecoverySnapshot,
) (string, []capabilityRecoverySuggestion) {
	suggestions := make([]capabilityRecoverySuggestion, 0, 8)

	var intro strings.Builder
	intro.WriteString("I can't ")
	switch intent.Kind {
	case "open_inbox":
		intro.WriteString("open your inbox directly from this Assistant session")
	default:
		intro.WriteString("send that directly from this Assistant session")
	}
	if recipient := strings.TrimSpace(intent.Recipient); recipient != "" {
		intro.WriteString(" for ")
		intro.WriteString(`"`)
		intro.WriteString(recipient)
		intro.WriteString(`"`)
	}
	if !snapshot.ProviderSupportsTools && snapshot.HasDirectCommunicationTool {
		intro.WriteString(" because this provider path can't call tools.")
	} else {
		intro.WriteString(" because there isn't a messaging/email tool configured here.")
	}

	lines := []string{intro.String(), "", "Closest ways to recover this:"}

	if snapshot.OpenAppAvailable {
		suggestions = append(suggestions,
			capabilityRecoverySuggestion{
				Type:        "nearby_action",
				Label:       "Open Mail app",
				Command:     "/openapp Mail",
				Description: "Launch the local Mail app so you can send or review the message there.",
			},
			capabilityRecoverySuggestion{
				Type:        "nearby_action",
				Label:       "Open Messages app",
				Command:     "/openapp Messages",
				Description: "Launch the local Messages app for message-based follow-up.",
			},
		)
	}

	if snapshot.HasBrowserTool {
		suggestions = append(suggestions,
			capabilityRecoverySuggestion{
				Type:        "nearby_action",
				Label:       "Open Gmail",
				Command:     "open gmail.com",
				Description: "Use the browser tool path to open Gmail.",
			},
			capabilityRecoverySuggestion{
				Type:        "nearby_action",
				Label:       "Open Outlook",
				Command:     "open outlook.com",
				Description: "Use the browser tool path to open Outlook Web.",
			},
		)
	} else {
		suggestions = append(suggestions, capabilityRecoverySuggestion{
			Type:        "mcp_hint",
			Label:       "Attach browser MCP",
			Description: "Attach a browser MCP such as Playwright or Browserbase if you want web inbox or compose flows.",
		})
	}

	for _, skill := range snapshot.InstalledSkills {
		description := strings.TrimSpace(skill.Description)
		if description == "" {
			description = "Relevant installed skill."
		}
		suggestions = append(suggestions, capabilityRecoverySuggestion{
			Type:        "installed_skill",
			Label:       skill.Name,
			Description: description,
			Command:     "/skills",
		})
	}

	for _, item := range snapshot.MarketplaceSkills {
		description := "Install with `npx skills add " + item.Package + "`."
		if item.URL != "" {
			description += " " + item.URL
		}
		suggestions = append(suggestions, capabilityRecoverySuggestion{
			Type:        "marketplace_skill",
			Label:       item.Package,
			Description: description,
			URL:         item.URL,
		})
	}

	suggestions = append(suggestions, capabilityRecoverySuggestion{
		Type:        "custom_capability",
		Label:       "Create custom skill or MCP",
		Description: "If this is recurring, create a dedicated messaging/email skill or MCP instead of copying text manually.",
		Command:     "npx skills init email-assistant",
	})

	for _, suggestion := range suggestions {
		switch {
		case strings.TrimSpace(suggestion.Command) != "":
			lines = append(lines, "- "+suggestion.Label+": "+suggestion.Description+" Command: `"+suggestion.Command+"`.")
		case strings.TrimSpace(suggestion.URL) != "":
			lines = append(lines, "- "+suggestion.Label+": "+suggestion.Description)
		default:
			lines = append(lines, "- "+suggestion.Label+": "+suggestion.Description)
		}
	}

	lines = append(lines, "", "Tell me which recovery path you want, and I’ll take the next safe step.")
	return strings.Join(lines, "\n"), suggestions
}

func searchCapabilityRecoveryMarketplaceSkills(ctx context.Context, query string) ([]capabilityRecoveryMarketplaceSkill, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}

	// Validate query contains only safe characters to prevent misuse of npx --yes
	for _, r := range query {
		if !isCapabilityRecoverySafeQueryRune(r) {
			return nil, fmt.Errorf("invalid character in marketplace query: %q", string(r))
		}
	}

	output, err := runCapabilityRecoverySkillsCLI(ctx, "find", query)
	results := parseCapabilityRecoverySkillsFind(output, 3)
	if err != nil && len(results) == 0 {
		return nil, err
	}
	return results, nil
}

func runCapabilityRecoverySkillsCLI(ctx context.Context, args ...string) (string, error) {
	commandArgs := append([]string{"--yes", "skills"}, args...)
	cmd := exec.CommandContext(ctx, "npx", commandArgs...)
	cmd.Env = append(os.Environ(), "CI=1", "NO_COLOR=1", "FORCE_COLOR=0")
	outputBytes, err := cmd.CombinedOutput()
	output := string(outputBytes)
	if err != nil {
		if output == "" {
			output = err.Error()
		}
		return output, fmt.Errorf("skills command failed: %w", err)
	}
	return output, nil
}

// isCapabilityRecoverySafeQueryRune returns true for characters safe to pass to npx skills find.
func isCapabilityRecoverySafeQueryRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == ' ' || r == '-' || r == '_' || r == '.'
}

func parseCapabilityRecoverySkillsFind(output string, limit int) []capabilityRecoveryMarketplaceSkill {
	cleaned := strings.TrimSpace(capabilityRecoveryANSIPattern.ReplaceAllString(output, ""))
	if cleaned == "" {
		return nil
	}
	if limit <= 0 {
		limit = 3
	}

	lines := strings.Split(cleaned, "\n")
	results := make([]capabilityRecoveryMarketplaceSkill, 0, limit)
	indexByPackage := make(map[string]int)
	currentIndex := -1

	for _, raw := range lines {
		line := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(raw), "│└├─•·"))
		if line == "" {
			continue
		}

		if match := capabilityRecoveryPackagePattern.FindStringSubmatch(line); len(match) > 1 {
			pkg := strings.TrimSpace(match[1])
			resultIndex, exists := indexByPackage[pkg]
			if !exists {
				if len(results) >= limit {
					currentIndex = -1
					continue
				}
				results = append(results, capabilityRecoveryMarketplaceSkill{Package: pkg})
				resultIndex = len(results) - 1
				indexByPackage[pkg] = resultIndex
			}
			currentIndex = resultIndex
			continue
		}

		if currentIndex < 0 || currentIndex >= len(results) {
			continue
		}
		if idx := strings.Index(line, "https://skills.sh/"); idx >= 0 {
			results[currentIndex].URL = strings.TrimSpace(line[idx:])
		}
	}

	return results
}

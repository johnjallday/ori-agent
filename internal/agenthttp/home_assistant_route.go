package agenthttp

import (
	"net/http"
	"strings"
	"unicode"

	"github.com/johnjallday/ori-agent/internal/agent"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
)

type HomeAssistantRouteHandler struct {
	State store.Store
}

func NewHomeAssistantRouteHandler(state store.Store) *HomeAssistantRouteHandler {
	return &HomeAssistantRouteHandler{State: state}
}

type HomeAssistantRouteRequest struct {
	Prompt string `json:"prompt"`
}

type HomeAssistantRouteResponse struct {
	Intent             string   `json:"intent"`
	IntentLabel        string   `json:"intent_label"`
	MatchedAgent       string   `json:"matched_agent,omitempty"`
	Score              int      `json:"score"`
	RequiresCreation   bool     `json:"requires_creation"`
	Reasons            []string `json:"reasons,omitempty"`
	SuggestedAgentName string   `json:"suggested_agent_name"`
	SuggestedAgentType string   `json:"suggested_agent_type"`
}

type homeAssistantIntent struct {
	Key              string
	Label            string
	Keywords         []string
	PreferredPlugins []string
	PreferredTypes   []string
	DefaultType      string
	SuggestedName    string
	MinScore         int
}

type routedAgentMatch struct {
	Name    string
	Agent   *agent.Agent
	Score   int
	Reasons []string
}

var (
	homeAssistantTravelIntent = homeAssistantIntent{
		Key:              "travel_planning",
		Label:            "travel planning",
		Keywords:         []string{"trip", "travel", "itinerary", "vacation", "los angeles", "la", "weekend", "hotel", "flight"},
		PreferredPlugins: []string{"web", "weather", "maps", "search", "travel"},
		PreferredTypes:   []string{"research", "general", "tool-calling"},
		DefaultType:      "research",
		SuggestedName:    "Travel Planner",
		MinScore:         4,
	}
	homeAssistantEmailIntent = homeAssistantIntent{
		Key:              "email_check",
		Label:            "email triage",
		Keywords:         []string{"email", "inbox", "mail", "gmail", "outlook", "unread", "reply", "messages"},
		PreferredPlugins: []string{"email", "gmail", "outlook", "imap"},
		PreferredTypes:   []string{"tool-calling", "general"},
		DefaultType:      "tool-calling",
		SuggestedName:    "Email Assistant",
		MinScore:         4,
	}
	homeAssistantAppLaunchIntent = homeAssistantIntent{
		Key:              "app_launch",
		Label:            "app launch",
		Keywords:         []string{"open", "launch", "start", "run", "application", "app", "obsidian", "reaper", "finder"},
		PreferredPlugins: []string{"shell", "executor", "desktop", "automation", "os-shell", "command"},
		PreferredTypes:   []string{"tool-calling", "general"},
		DefaultType:      "tool-calling",
		SuggestedName:    "Desktop Launcher",
		MinScore:         4,
	}
	homeAssistantDefaultIntent = homeAssistantIntent{
		Key:              "general_task",
		Label:            "general task",
		Keywords:         []string{},
		PreferredPlugins: []string{},
		PreferredTypes:   []string{"general", "tool-calling", "research"},
		DefaultType:      "general",
		SuggestedName:    "Task Assistant",
		MinScore:         3,
	}
	homeAssistantSpecificIntents = []homeAssistantIntent{
		homeAssistantTravelIntent,
		homeAssistantEmailIntent,
	}
	homeAssistantCommonTokens = map[string]bool{
		"a": true, "an": true, "and": true, "are": true, "can": true, "do": true, "for": true, "help": true,
		"i": true, "in": true, "is": true, "it": true, "my": true, "of": true, "on": true, "open": true,
		"or": true, "please": true, "task": true, "that": true, "the": true, "this": true, "to": true,
		"want": true, "with": true, "you": true,
	}
)

// RouteHandler classifies a home assistant prompt and finds the best existing agent.
// POST /api/home-assistant/route
func (h *HomeAssistantRouteHandler) RouteHandler(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}

	var req HomeAssistantRouteRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		orihttp.BadRequest(w, "prompt is required")
		return
	}

	intent := detectHomeAssistantIntent(prompt)
	match := h.findBestMatch(prompt, intent)

	resp := HomeAssistantRouteResponse{
		Intent:             intent.Key,
		IntentLabel:        intent.Label,
		RequiresCreation:   true,
		SuggestedAgentName: intent.SuggestedName,
		SuggestedAgentType: intent.DefaultType,
	}

	if match != nil {
		resp.MatchedAgent = match.Name
		resp.Score = match.Score
		resp.RequiresCreation = false
		resp.Reasons = match.Reasons
	}

	orihttp.WriteJSON(w, resp)
}

func detectHomeAssistantIntent(prompt string) homeAssistantIntent {
	selectedIntent := homeAssistantDefaultIntent
	selectedScore := 0
	text := normalizeRouteToken(prompt)

	for _, intent := range homeAssistantSpecificIntents {
		score := 0
		for _, keyword := range intent.Keywords {
			if strings.Contains(text, keyword) {
				score++
			}
		}
		if score > selectedScore {
			selectedIntent = intent
			selectedScore = score
		}
	}

	if selectedScore > 0 {
		return selectedIntent
	}

	if _, ok := parseHomeAssistantAppLaunchPrompt(prompt); ok {
		return homeAssistantAppLaunchIntent
	}

	return selectedIntent
}

func (h *HomeAssistantRouteHandler) findBestMatch(prompt string, intent homeAssistantIntent) *routedAgentMatch {
	names, current := h.State.ListAgents()
	if len(names) == 0 {
		return nil
	}

	var best *routedAgentMatch
	for _, name := range names {
		ag, ok := h.State.GetAgent(name)
		if !ok || ag == nil {
			continue
		}

		candidate := scoreAgentForIntent(name, current, ag, intent, prompt)
		if isBetterMatch(candidate, best, current) {
			best = candidate
		}
	}

	if best == nil || best.Score < intent.MinScore {
		return nil
	}
	if intent.Key == homeAssistantDefaultIntent.Key && len(best.Reasons) == 0 {
		return nil
	}
	return best
}

func isBetterMatch(candidate, best *routedAgentMatch, current string) bool {
	if candidate == nil {
		return false
	}
	if best == nil {
		return true
	}
	if candidate.Score != best.Score {
		return candidate.Score > best.Score
	}

	if candidate.Name == current && best.Name != current {
		return true
	}
	if best.Name == current && candidate.Name != current {
		return false
	}

	candidateActive := candidate.Agent != nil && candidate.Agent.Status == types.AgentStatusActive
	bestActive := best.Agent != nil && best.Agent.Status == types.AgentStatusActive
	if candidateActive != bestActive {
		return candidateActive
	}

	return strings.ToLower(candidate.Name) < strings.ToLower(best.Name)
}

func scoreAgentForIntent(name, current string, ag *agent.Agent, intent homeAssistantIntent, prompt string) *routedAgentMatch {
	summary := buildAgentSummary(name, ag)
	plugins := extractNormalizedPluginNames(ag)
	mcpServers := extractNormalizedMCPServerNames(ag)
	lowerName := normalizeRouteToken(name)
	promptTokens := tokenizePrompt(prompt)
	score := 0
	reasons := make([]string, 0, 3)

	for _, keyword := range intent.Keywords {
		if keyword == "" {
			continue
		}
		if strings.Contains(summary, keyword) {
			score += 2
			reasons = appendReason(reasons, `matches "`+keyword+`"`)
		}
	}

	for _, preferredPlugin := range intent.PreferredPlugins {
		if preferredPlugin == "" {
			continue
		}
		for _, plugin := range plugins {
			if strings.Contains(plugin, preferredPlugin) {
				score += 3
				reasons = appendReason(reasons, "has plugin support for "+preferredPlugin)
				break
			}
		}
		for _, server := range mcpServers {
			if strings.Contains(server, preferredPlugin) {
				score += 3
				reasons = appendReason(reasons, "has MCP support for "+preferredPlugin)
				break
			}
		}
	}

	if containsNormalized(intent.PreferredTypes, ag.Type) {
		score++
	}
	if ag.Status == types.AgentStatusActive {
		score++
	}
	if ag.Metadata != nil && ag.Metadata.Favorite {
		score++
	}
	if name == current {
		score++
	}

	for _, token := range promptTokens {
		if !isSignalPromptToken(token) {
			continue
		}
		if strings.Contains(lowerName, token) {
			score++
			reasons = appendReason(reasons, `name overlaps "`+token+`"`)
		}
	}

	if intent.Key == homeAssistantDefaultIntent.Key {
		for _, token := range promptTokens {
			if !isSignalPromptToken(token) {
				continue
			}
			if strings.Contains(summary, token) {
				score += 2
				reasons = appendReason(reasons, `context overlaps "`+token+`"`)
			}
		}
	}

	return &routedAgentMatch{
		Name:    name,
		Agent:   ag,
		Score:   score,
		Reasons: reasons,
	}
}

func isSignalPromptToken(token string) bool {
	if len(token) < 4 {
		return false
	}
	return !homeAssistantCommonTokens[token]
}

func buildAgentSummary(name string, ag *agent.Agent) string {
	parts := []string{
		normalizeRouteToken(name),
		normalizeRouteToken(ag.Type),
		normalizeRouteToken(string(ag.Role)),
	}

	if ag.Metadata != nil {
		if ag.Metadata.Description != "" {
			parts = append(parts, normalizeRouteToken(ag.Metadata.Description))
		}
		if len(ag.Metadata.Tags) > 0 {
			parts = append(parts, normalizeRouteToken(strings.Join(ag.Metadata.Tags, " ")))
		}
	}

	if len(ag.Capabilities) > 0 {
		parts = append(parts, normalizeRouteToken(strings.Join(ag.Capabilities, " ")))
	}

	pluginNames := extractNormalizedPluginNames(ag)
	if len(pluginNames) > 0 {
		parts = append(parts, strings.Join(pluginNames, " "))
	}
	mcpServerNames := extractNormalizedMCPServerNames(ag)
	if len(mcpServerNames) > 0 {
		parts = append(parts, strings.Join(mcpServerNames, " "))
	}

	return strings.Join(parts, " ")
}

func extractNormalizedPluginNames(ag *agent.Agent) []string {
	if ag == nil || len(ag.Plugins) == 0 {
		return []string{}
	}

	plugins := make([]string, 0, len(ag.Plugins))
	for name := range ag.Plugins {
		normalized := normalizeRouteToken(strings.ReplaceAll(name, "_", "-"))
		if normalized == "" {
			continue
		}
		plugins = append(plugins, normalized)
	}
	return plugins
}

func extractNormalizedMCPServerNames(ag *agent.Agent) []string {
	if ag == nil || len(ag.MCPServers) == 0 {
		return []string{}
	}

	servers := make([]string, 0, len(ag.MCPServers))
	for _, name := range ag.MCPServers {
		normalized := normalizeRouteToken(strings.ReplaceAll(name, "_", "-"))
		if normalized == "" {
			continue
		}
		servers = append(servers, normalized)
	}
	return servers
}

func appendReason(reasons []string, reason string) []string {
	if reason == "" {
		return reasons
	}
	for _, existing := range reasons {
		if existing == reason {
			return reasons
		}
	}
	if len(reasons) >= 3 {
		return reasons
	}
	return append(reasons, reason)
}

func containsNormalized(values []string, value string) bool {
	target := normalizeRouteToken(value)
	for _, item := range values {
		if normalizeRouteToken(item) == target {
			return true
		}
	}
	return false
}

func normalizeRouteToken(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func tokenizePrompt(prompt string) []string {
	seen := map[string]bool{}
	tokens := make([]string, 0)

	for _, token := range strings.FieldsFunc(normalizeRouteToken(prompt), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if token == "" || seen[token] {
			continue
		}
		seen[token] = true
		tokens = append(tokens, token)
	}

	return tokens
}

func parseHomeAssistantAppLaunchPrompt(prompt string) (string, bool) {
	normalized := normalizeRouteToken(prompt)
	if normalized == "" {
		return "", false
	}

	for _, politePrefix := range []string{"please ", "can you ", "could you ", "would you ", "hey "} {
		if strings.HasPrefix(normalized, politePrefix) {
			normalized = strings.TrimSpace(strings.TrimPrefix(normalized, politePrefix))
			break
		}
	}

	prefixes := []string{"open up ", "open ", "launch ", "start ", "run "}
	target := ""
	for _, prefix := range prefixes {
		if strings.HasPrefix(normalized, prefix) {
			target = strings.TrimSpace(strings.TrimPrefix(normalized, prefix))
			break
		}
	}
	if target == "" {
		return "", false
	}

	target = strings.Trim(target, " .,!?:;\"'")
	target = strings.TrimPrefix(target, "the ")
	target = strings.TrimSuffix(target, " app")
	target = strings.TrimSuffix(target, " application")
	target = strings.TrimSpace(target)
	if target == "" {
		return "", false
	}

	// Skip obvious URL/path-like targets that are more likely file or web intents.
	if strings.Contains(target, "://") || strings.Contains(target, "/") || strings.Contains(target, "\\") {
		return "", false
	}

	return target, true
}

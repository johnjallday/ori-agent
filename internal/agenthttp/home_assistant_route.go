package agenthttp

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"unicode"

	"github.com/johnjallday/ori-agent/internal/agent"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// CalendarOpsPreference is the narrow read Home calendar-intent routing needs
// to prefer the user's Calendar Ops Scheduler over generic agent scoring for
// personal-calendar prompts (FR53), instead of reimplementing Calendar Ops
// workspace resolution here. Implemented by calendarhttp.Handler.
type CalendarOpsPreference interface {
	// PreferredCalendarAgent reports the Calendar Ops entry agent's name when
	// the current user's active Calendar Ops workspace has an effective,
	// ready calendar binding. ok=false means "no preference" -- routing falls
	// back to normal generic agent scoring unchanged.
	PreferredCalendarAgent(ctx context.Context) (agentName string, ok bool)
}

type HomeAssistantRouteHandler struct {
	State                 store.Store
	WorkspaceResolver     *HomeAssistantWorkspaceResolver
	IntakeTraceStore      HomeAssistantIntakeTraceStore
	CalendarOpsPreference CalendarOpsPreference
	RuntimeResolver       interface {
		ResolveAgentForWorkspace(agentName, workspaceID, nodeID string) (*workspace.ResolvedAgentRuntime, error)
	}
	SystemModelReader interface {
		GetSystemModel() (provider, model string)
	}
}

// SetCalendarOpsPreference wires the Calendar Ops preference read (FR53).
func (h *HomeAssistantRouteHandler) SetCalendarOpsPreference(pref CalendarOpsPreference) {
	h.CalendarOpsPreference = pref
}

type resolvedRouteAgent struct {
	*agent.Agent
	MCPServers []string
}

func NewHomeAssistantRouteHandler(state store.Store) *HomeAssistantRouteHandler {
	return &HomeAssistantRouteHandler{State: state}
}

func (h *HomeAssistantRouteHandler) SetSystemModelReader(reader interface {
	GetSystemModel() (provider, model string)
}) {
	h.SystemModelReader = reader
}

func (h *HomeAssistantRouteHandler) SetRuntimeResolver(resolver interface {
	ResolveAgentForWorkspace(agentName, workspaceID, nodeID string) (*workspace.ResolvedAgentRuntime, error)
}) {
	h.RuntimeResolver = resolver
	if h.WorkspaceResolver != nil {
		h.WorkspaceResolver.SetRuntimeResolver(resolver)
	}
}

func (h *HomeAssistantRouteHandler) SetWorkspaceResolver(resolver *HomeAssistantWorkspaceResolver) {
	h.WorkspaceResolver = resolver
	if resolver != nil && h.RuntimeResolver != nil {
		resolver.SetRuntimeResolver(h.RuntimeResolver)
	}
}

func (h *HomeAssistantRouteHandler) SetIntakeTraceStore(store HomeAssistantIntakeTraceStore) {
	h.IntakeTraceStore = store
}

type HomeAssistantRouteRequest struct {
	Prompt  string                     `json:"prompt"`
	Context *HomeAssistantRouteContext `json:"context,omitempty"`
}

type HomeAssistantRouteContext struct {
	Surface     string `json:"surface,omitempty"`
	PagePath    string `json:"page_path,omitempty"`
	WorkspaceID string `json:"workspace_id,omitempty"`
	TaskID      string `json:"task_id,omitempty"`
	SessionID   string `json:"session_id,omitempty"`
	Origin      string `json:"origin,omitempty"`
}

type HomeAssistantRouteResponse struct {
	Intent               string                            `json:"intent"`
	IntentVariant        string                            `json:"intent_variant,omitempty"`
	IntentLabel          string                            `json:"intent_label"`
	RoutingPolicy        string                            `json:"routing_policy"`
	ContextMode          string                            `json:"context_mode"`
	HandoffPolicy        string                            `json:"handoff_policy"`
	MatchedAgent         string                            `json:"matched_agent,omitempty"`
	Score                int                               `json:"score"`
	RequiresCreation     bool                              `json:"requires_creation"`
	WorkspaceRecommended bool                              `json:"workspace_recommended"`
	WorkspaceResolution  *HomeAssistantWorkspaceResolution `json:"workspace_resolution,omitempty"`
	RouteMode            string                            `json:"route_mode"`
	TargetSurface        string                            `json:"target_surface"`
	Reasons              []string                          `json:"reasons,omitempty"`
	SuggestedAgentName   string                            `json:"suggested_agent_name"`
	SuggestedAgentType   string                            `json:"suggested_agent_type"`
}

var errHomeAssistantPromptRequired = errors.New("prompt is required")

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

type normalizedHomeAssistantRouteContext struct {
	Surface     string
	PagePath    string
	WorkspaceID string
	TaskID      string
	SessionID   string
	Origin      string
}

const (
	homeAssistantPolicyAssistantOnly      = "assistant_only"
	homeAssistantPolicyAssistantPreferred = "assistant_preferred"
	homeAssistantPolicySpecialistRequired = "specialist_required"

	homeAssistantContextDirect    = "direct"
	homeAssistantContextWorkspace = "workspace"
	homeAssistantContextScratch   = "scratch"

	homeAssistantHandoffAssistant  = "assistant"
	homeAssistantHandoffSpecialist = "specialist"
	homeAssistantHandoffTool       = "tool"

	// homeAssistantRouteModeInline signals the frontend to answer the prompt
	// inline via the home harness (POST /api/home-assistant/ask) instead of
	// routing to an agent or workspace.
	homeAssistantRouteModeInline = "home_inline"
)

var (
	homeAssistantUtilityIntent = homeAssistantIntent{
		Key:              "utility_direct",
		Label:            "daily utility",
		Keywords:         []string{"time", "timezone", "clock", "date", "weather", "forecast", "temperature", "air quality", "aqi", "pollution", "pm2.5", "pm10", "convert", "conversion", "calculate", "calculator", "quick fact", "fact", "capital", "define", "definition"},
		PreferredPlugins: []string{"time", "weather", "calculator", "math", "search", "web"},
		PreferredTypes:   []string{"general", "tool-calling", "research"},
		DefaultType:      "general",
		SuggestedName:    "Utility Assistant",
		MinScore:         4,
	}
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
	homeAssistantCalendarIntent = homeAssistantIntent{
		Key:              "calendar_check",
		Label:            "calendar or schedule",
		Keywords:         []string{"calendar", "schedule", "meeting", "meetings", "appointment", "appointments", "availability", "free time", "busy", "free", "events"},
		PreferredPlugins: []string{"calendar", "schedule", "google-calendar"},
		PreferredTypes:   []string{"tool-calling", "general"},
		DefaultType:      "tool-calling",
		SuggestedName:    "Calendar Assistant",
		MinScore:         4,
	}
	homeAssistantWorkspaceCreateIntent = homeAssistantIntent{
		Key:              "workspace_create",
		Label:            "workspace creation",
		Keywords:         []string{"create workspace", "new workspace", "workspace called", "workspace named"},
		PreferredPlugins: []string{},
		PreferredTypes:   []string{"general", "tool-calling"},
		DefaultType:      "general",
		// Not "Workspace Assistant": that is one of the labels Issue #350 retires,
		// and this string is offered to the user as a name to create (FR61).
		SuggestedName: "Workspace Builder",
		MinScore:      4,
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
		// Not "Task Assistant": another retired label, and this one surfaces in the
		// panel's routing summary for every unmatched request (FR61).
		SuggestedName: "Task Specialist",
		MinScore:      3,
	}
	// homeAssistantAppIntrospectionIntent matches questions about the user's own
	// Ori data (activity/summary/recap over tasks, sessions, workspaces, usage).
	// Answered inline by the home harness.
	homeAssistantAppIntrospectionIntent = homeAssistantIntent{
		Key:            "app_introspection",
		Label:          "app activity",
		PreferredTypes: []string{"general"},
		DefaultType:    "general",
		SuggestedName:  systemAssistantAgentName,
		MinScore:       3,
	}
	// homeAssistantAppNavigationIntent matches "where/how do I…/open <feature>"
	// requests about app features and locations. Answered inline + grounded in the
	// navigation catalog.
	homeAssistantAppNavigationIntent = homeAssistantIntent{
		Key:            "app_navigation",
		Label:          "app navigation",
		PreferredTypes: []string{"general"},
		DefaultType:    "general",
		SuggestedName:  systemAssistantAgentName,
		MinScore:       3,
	}
	homeAssistantSpecificIntents = []homeAssistantIntent{
		homeAssistantUtilityIntent,
		homeAssistantTravelIntent,
		homeAssistantEmailIntent,
		homeAssistantCalendarIntent,
		homeAssistantWorkspaceCreateIntent,
	}
	homeAssistantWorkspaceScheduleSignals = []string{
		"workspace schedule", "scheduled task", "scheduled tasks", "scheduler", "next run", "next runs",
		"cron", "workspace tasks", "task schedule", "task schedules", "run today in this workspace",
	}
	homeAssistantPersonalCalendarSignals = []string{
		"my calendar", "calendar", "meeting", "meetings", "appointment", "appointments",
		"am i free", "availability", "free time", "busy", "event", "events",
	}
	homeAssistantCommonTokens = map[string]bool{
		"a": true, "an": true, "and": true, "are": true, "can": true, "do": true, "for": true, "help": true,
		"i": true, "in": true, "is": true, "it": true, "my": true, "of": true, "on": true, "open": true,
		"or": true, "please": true, "task": true, "that": true, "the": true, "this": true, "to": true,
		"want": true, "with": true, "you": true,
	}
	homeAssistantComplexProjectBuildVerbs = []string{
		"build", "create", "develop", "design", "implement", "make", "ship", "start", "set up", "setup",
	}
	homeAssistantComplexProjectTargets = []string{
		"website", "web site", "web app", "app", "application", "landing page", "dashboard", "product", "project", "platform", "system",
	}
	homeAssistantComplexProjectSignals = []string{
		"from scratch", "full stack", "frontend", "backend", "database", "authentication", "auth", "api", "deploy", "deployment", "production", "mvp", "architecture", "roadmap", "requirements",
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

	resp, err := h.RoutePrompt(r.Context(), req.Prompt, req.Context)
	if err != nil {
		if errors.Is(err, errHomeAssistantPromptRequired) {
			orihttp.BadRequest(w, err.Error())
			return
		}
		orihttp.InternalError(w, err.Error())
		return
	}
	orihttp.WriteJSON(w, resp)
}

func (h *HomeAssistantRouteHandler) RoutePrompt(ctx context.Context, prompt string, context *HomeAssistantRouteContext) (*HomeAssistantRouteResponse, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return nil, errHomeAssistantPromptRequired
	}

	routeContext := normalizeHomeAssistantRouteContext(context)
	intent := h.classifyHomeIntent(prompt)
	intentVariant := detectHomeAssistantIntentVariant(prompt, intent, routeContext)
	workspaceRecommended := shouldRecommendWorkspace(prompt, intent)
	routeMode, targetSurface := determineRouteModeAndTargetSurface(intent, intentVariant, routeContext, workspaceRecommended)
	routingPolicy := determineRoutingPolicy(intent, intentVariant, routeMode, targetSurface, routeContext)
	contextMode := determineHomeAssistantContextMode(routeMode)
	handoffPolicy := determineHomeAssistantHandoffPolicy(intent, intentVariant)

	var workspaceResolution *HomeAssistantWorkspaceResolution
	if contextMode == homeAssistantContextWorkspace && h.WorkspaceResolver != nil {
		workspaceResolution = h.WorkspaceResolver.Resolve(prompt, routeContext)
	}

	agentRouteContext := routeContext
	if workspaceResolution != nil &&
		(workspaceResolution.State == homeAssistantWorkspaceStateConfident ||
			workspaceResolution.State == homeAssistantWorkspaceStateNeedsRepair) &&
		strings.TrimSpace(workspaceResolution.SelectedWorkspaceID) != "" {
		agentRouteContext.WorkspaceID = strings.TrimSpace(workspaceResolution.SelectedWorkspaceID)
	}

	match := h.calendarOpsPreferredMatch(ctx, intent, intentVariant, routeContext)
	if match == nil {
		match = h.findBestMatch(prompt, intent, agentRouteContext)
	}
	if match == nil {
		match = h.systemAssistantFallback(intent)
	}

	resp := &HomeAssistantRouteResponse{
		Intent:               intent.Key,
		IntentVariant:        intentVariant,
		IntentLabel:          intent.Label,
		RoutingPolicy:        routingPolicy,
		ContextMode:          contextMode,
		HandoffPolicy:        handoffPolicy,
		RequiresCreation:     true,
		WorkspaceRecommended: workspaceRecommended,
		WorkspaceResolution:  workspaceResolution,
		RouteMode:            routeMode,
		TargetSurface:        targetSurface,
		SuggestedAgentName:   intent.SuggestedName,
		SuggestedAgentType:   intent.DefaultType,
	}

	if match != nil {
		resp.MatchedAgent = match.Name
		resp.Score = match.Score
		resp.RequiresCreation = false
		resp.Reasons = match.Reasons
	}

	return resp, nil
}

// calendarOpsPreferredMatch prefers the user's Calendar Ops Scheduler for a
// calendar_check prompt when their Calendar Ops workspace has an effective,
// ready calendar binding (FR53). It returns nil (no preference, fall back to
// findBestMatch unchanged) whenever CalendarOpsPreference isn't wired, the
// intent isn't calendar_check, the request is workspace-schedule ambiguity
// inside a workspace (which must keep routing to that workspace's own task
// scheduler, not Calendar Ops), or the preference read itself says no.
func (h *HomeAssistantRouteHandler) calendarOpsPreferredMatch(ctx context.Context, intent homeAssistantIntent, intentVariant string, routeContext normalizedHomeAssistantRouteContext) *routedAgentMatch {
	if h == nil || h.CalendarOpsPreference == nil || h.State == nil {
		return nil
	}
	if intent.Key != homeAssistantCalendarIntent.Key {
		return nil
	}
	if intentVariant == "workspace_schedule" && routeContext.hasWorkspaceContext() {
		return nil
	}

	agentName, ok := h.CalendarOpsPreference.PreferredCalendarAgent(ctx)
	agentName = strings.TrimSpace(agentName)
	if !ok || agentName == "" {
		return nil
	}
	ag, exists := h.State.GetAgent(agentName)
	if !exists || ag == nil || ag.Status == types.AgentStatusDisabled {
		return nil
	}
	return &routedAgentMatch{
		Name:    agentName,
		Agent:   ag,
		Score:   intent.MinScore,
		Reasons: []string{"calendar ops workspace has a ready calendar connector"},
	}
}

func (h *HomeAssistantRouteHandler) systemAssistantFallback(intent homeAssistantIntent) *routedAgentMatch {
	// Keep specialist intent behavior unchanged; fallback only for utility/general asks.
	if intent.Key != homeAssistantDefaultIntent.Key && intent.Key != homeAssistantUtilityIntent.Key {
		return nil
	}

	ag, ok := h.State.GetAgent(systemAssistantAgentName)
	if !ok || ag == nil {
		return nil
	}

	return &routedAgentMatch{
		Name:    systemAssistantAgentName,
		Agent:   ag,
		Score:   intent.MinScore,
		Reasons: []string{"fallback to system assistant"},
	}
}

func detectHomeAssistantIntent(prompt string) homeAssistantIntent {
	if shouldClassifyWorkspaceCreate(prompt) {
		return homeAssistantWorkspaceCreateIntent
	}

	selectedIntent := homeAssistantDefaultIntent
	selectedScore := 0
	text := normalizeRouteToken(prompt)

	for _, intent := range homeAssistantSpecificIntents {
		score := 0
		for _, keyword := range intent.Keywords {
			if containsRoutePhrase(text, keyword) {
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

// classifyHomeIntent layers the app_introspection / app_navigation intents on top
// of detectHomeAssistantIntent. It only overrides the generic fallback and the
// app_launch case, so specific intents (utility/calendar/email/travel/
// workspace_create) keep their precedence (FR #4). app_navigation beats
// app_launch when the "open …" target is a known app feature or a real workspace
// ("open Action Center" / "open the Q3 Planning workspace"), while "open Safari"
// stays app_launch.
func (h *HomeAssistantRouteHandler) classifyHomeIntent(prompt string) homeAssistantIntent {
	base := detectHomeAssistantIntent(prompt)
	switch base.Key {
	case homeAssistantDefaultIntent.Key:
		if h.isAppNavigationPrompt(prompt) {
			return homeAssistantAppNavigationIntent
		}
		if isAppIntrospectionPrompt(prompt) {
			return homeAssistantAppIntrospectionIntent
		}
	case homeAssistantAppLaunchIntent.Key:
		if h.isAppNavigationPrompt(prompt) {
			return homeAssistantAppNavigationIntent
		}
	}
	return base
}

func (h *HomeAssistantRouteHandler) workspaceStoreOrNil() workspace.Store {
	if h == nil || h.WorkspaceResolver == nil {
		return nil
	}
	return h.WorkspaceResolver.WorkspaceStore
}

// isAppNavigationPrompt reports whether the prompt is asking to find/open an app
// destination: it needs a navigation cue plus a real target (a catalog feature or
// an existing workspace name). Requiring a real target keeps introspection asks
// like "summarize my mcp usage" out of navigation.
func (h *HomeAssistantRouteHandler) isAppNavigationPrompt(prompt string) bool {
	if !hasNavigationCue(prompt) {
		return false
	}
	if promptMatchesNavCatalog(prompt) {
		return true
	}
	return promptMentionsWorkspaceByName(h.workspaceStoreOrNil(), prompt)
}

func hasNavigationCue(prompt string) bool {
	p := normalizeRouteToken(prompt)
	for _, cue := range []string{
		"where", "how do i", "how can i", "how to", "take me to", "go to",
		"navigate", "open", "show me", "which page", "what page", "find the",
		"get to", "bring up",
	} {
		if strings.Contains(p, cue) {
			return true
		}
	}
	return false
}

// isAppIntrospectionPrompt reports whether the prompt asks about the user's own
// Ori data/activity.
func isAppIntrospectionPrompt(prompt string) bool {
	p := normalizeRouteToken(prompt)
	if p == "" {
		return false
	}
	for _, phrase := range []string{
		"task activity", "my tasks", "my workspaces", "my sessions", "my activity",
		"what did i work on", "what have i been working", "what's pending", "whats pending",
		"how many tasks", "recap", "across my workspaces", "all my workspaces", "my recent",
	} {
		if strings.Contains(p, phrase) {
			return true
		}
	}
	hasVerb := containsAnyToken(p, []string{"summarize", "summary", "overview", "recap", "report", "review", "status"})
	hasNoun := containsAnyToken(p, []string{
		"task", "tasks", "workspace", "workspaces", "session", "sessions",
		"activity", "opportunity", "opportunities", "usage", "cost", "spending",
	})
	return hasVerb && hasNoun
}

func containsAnyToken(normalizedPrompt string, needles []string) bool {
	for _, n := range needles {
		if strings.Contains(normalizedPrompt, n) {
			return true
		}
	}
	return false
}

func detectHomeAssistantIntentVariant(prompt string, intent homeAssistantIntent, context normalizedHomeAssistantRouteContext) string {
	if intent.Key != homeAssistantCalendarIntent.Key {
		return ""
	}

	normalized := normalizeRouteToken(prompt)
	if normalized == "" {
		return "personal_calendar"
	}

	if promptContainsAnyRoutePhrase(normalized, homeAssistantWorkspaceScheduleSignals) {
		return "workspace_schedule"
	}
	if promptContainsAnyRoutePhrase(normalized, homeAssistantPersonalCalendarSignals) {
		return "personal_calendar"
	}
	if context.hasWorkspaceSurfaceContext() && strings.Contains(normalized, "schedule") {
		return "ambiguous"
	}
	return "personal_calendar"
}

func (h *HomeAssistantRouteHandler) findBestMatch(prompt string, intent homeAssistantIntent, routeContext normalizedHomeAssistantRouteContext) *routedAgentMatch {
	names := h.State.ListAgents()
	if len(names) == 0 {
		return nil
	}
	current := ""
	if assistant, ok := h.State.GetAgent(systemAssistantAgentName); ok && assistant != nil {
		current = systemAssistantAgentName
	} else if len(names) > 0 {
		current = names[0]
	}

	var best *routedAgentMatch
	for _, name := range names {
		baseAgent, ok := h.State.GetAgent(name)
		if !ok || baseAgent == nil {
			continue
		}
		if baseAgent.Status == types.AgentStatusDisabled {
			continue
		}
		ag := h.resolveAgentForContext(name, baseAgent, routeContext)
		if ag == nil || ag.Status == types.AgentStatusDisabled {
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

func (h *HomeAssistantRouteHandler) resolveAgentForContext(
	agentName string,
	baseAgent *agent.Agent,
	routeContext normalizedHomeAssistantRouteContext,
) *resolvedRouteAgent {
	if h == nil || h.RuntimeResolver == nil || strings.TrimSpace(routeContext.WorkspaceID) == "" {
		return &resolvedRouteAgent{Agent: baseAgent}
	}

	resolved, err := h.RuntimeResolver.ResolveAgentForWorkspace(agentName, routeContext.WorkspaceID, "")
	if errors.Is(err, workspace.ErrAgentPaused) {
		return nil
	}
	if err != nil || resolved == nil || resolved.Agent == nil {
		return &resolvedRouteAgent{Agent: baseAgent}
	}
	return &resolvedRouteAgent{
		Agent:      resolved.Agent,
		MCPServers: append([]string{}, resolved.MCPServers...),
	}
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

func scoreAgentForIntent(name, current string, ag *resolvedRouteAgent, intent homeAssistantIntent, prompt string) *routedAgentMatch {
	summary := buildAgentSummary(name, ag)
	mcpServers := extractNormalizedMCPServerNames(ag)
	lowerName := normalizeRouteToken(name)
	promptTokens := tokenizePrompt(prompt)
	routingProfile := agentRoutingProfile(ag)
	score := 0
	reasons := make([]string, 0, 3)

	routingScore, routingReasons := scoreRoutingProfile(prompt, promptTokens, routingProfile)
	score += routingScore
	for _, reason := range routingReasons {
		reasons = appendReason(reasons, reason)
	}

	for _, keyword := range intent.Keywords {
		if keyword == "" {
			continue
		}
		if containsRoutePhrase(summary, keyword) {
			score += 2
			reasons = appendReason(reasons, `matches "`+keyword+`"`)
		}
	}

	for _, preferredPlugin := range intent.PreferredPlugins {
		if preferredPlugin == "" {
			continue
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
		Agent:   ag.Agent,
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

func buildAgentSummary(name string, ag *resolvedRouteAgent) string {
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

	mcpServerNames := extractNormalizedMCPServerNames(ag)
	if len(mcpServerNames) > 0 {
		parts = append(parts, strings.Join(mcpServerNames, " "))
	}
	if profile := agentRoutingProfile(ag); profile != nil {
		if len(profile.MatchPhrases) > 0 {
			parts = append(parts, normalizeRouteToken(strings.Join(profile.MatchPhrases, " ")))
		}
		if len(profile.ExampleRequests) > 0 {
			parts = append(parts, normalizeRouteToken(strings.Join(profile.ExampleRequests, " ")))
		}
		if len(profile.Domains) > 0 {
			parts = append(parts, normalizeRouteToken(strings.Join(profile.Domains, " ")))
		}
		if len(profile.ExternalSystems) > 0 {
			parts = append(parts, normalizeRouteToken(strings.Join(profile.ExternalSystems, " ")))
		}
		if profile.SideEffects != "" {
			parts = append(parts, normalizeRouteToken(profile.SideEffects))
		}
	}

	return strings.Join(parts, " ")
}

func agentRoutingProfile(ag *resolvedRouteAgent) *types.AgentRoutingProfile {
	if ag == nil || ag.Metadata == nil {
		return nil
	}
	return ag.Metadata.RoutingProfile
}

func scoreRoutingProfile(prompt string, promptTokens []string, profile *types.AgentRoutingProfile) (int, []string) {
	if profile == nil {
		return 0, nil
	}

	normalizedPrompt := normalizeRouteToken(prompt)
	score := 0
	reasons := make([]string, 0, 3)

	for _, phrase := range profile.MatchPhrases {
		normalizedPhrase := normalizeRouteToken(phrase)
		if normalizedPhrase == "" {
			continue
		}
		if strings.Contains(normalizedPrompt, normalizedPhrase) || strings.Contains(normalizedPhrase, normalizedPrompt) {
			score += 4
			reasons = appendReason(reasons, `routing phrase matches "`+strings.TrimSpace(phrase)+`"`)
		}
	}

	for _, domain := range profile.Domains {
		normalizedDomain := normalizeRouteToken(domain)
		if normalizedDomain == "" {
			continue
		}
		if strings.Contains(normalizedPrompt, normalizedDomain) {
			score += 2
			reasons = appendReason(reasons, `domain matches "`+strings.TrimSpace(domain)+`"`)
		}
	}

	targetApp, hasTargetApp := parseHomeAssistantAppLaunchPrompt(prompt)
	normalizedTargetApp := normalizeRouteToken(targetApp)
	for _, system := range profile.ExternalSystems {
		normalizedSystem := normalizeRouteToken(system)
		if normalizedSystem == "" {
			continue
		}
		if strings.Contains(normalizedPrompt, normalizedSystem) {
			score += 3
			reasons = appendReason(reasons, `external system matches "`+strings.TrimSpace(system)+`"`)
			continue
		}
		if hasTargetApp && normalizedSystem == normalizedTargetApp {
			score += 4
			reasons = appendReason(reasons, `launch target matches "`+strings.TrimSpace(system)+`"`)
		}
	}

	overlap, example := bestRoutingExampleOverlap(prompt, promptTokens, profile.ExampleRequests)
	switch {
	case overlap >= 4:
		score += 5
	case overlap >= 3:
		score += 4
	case overlap >= 2:
		score += 2
	}
	if overlap >= 2 && example != "" {
		reasons = appendReason(reasons, `example request overlaps "`+example+`"`)
	}

	return score, reasons
}

func bestRoutingExampleOverlap(prompt string, promptTokens []string, examples []string) (int, string) {
	normalizedPrompt := normalizeRouteToken(prompt)
	bestOverlap := 0
	bestExample := ""

	for _, example := range examples {
		normalizedExample := normalizeRouteToken(example)
		if normalizedExample == "" {
			continue
		}

		overlap := signalTokenOverlap(promptTokens, tokenizePrompt(example))
		switch {
		case normalizedExample == normalizedPrompt:
			overlap = 5
		case strings.Contains(normalizedPrompt, normalizedExample), strings.Contains(normalizedExample, normalizedPrompt):
			if overlap < 4 {
				overlap = 4
			}
		}

		if overlap > bestOverlap {
			bestOverlap = overlap
			bestExample = strings.TrimSpace(example)
		}
	}

	return bestOverlap, bestExample
}

func signalTokenOverlap(left, right []string) int {
	if len(left) == 0 || len(right) == 0 {
		return 0
	}

	leftSet := make(map[string]struct{}, len(left))
	for _, token := range left {
		if !isSignalPromptToken(token) {
			continue
		}
		leftSet[token] = struct{}{}
	}
	if len(leftSet) == 0 {
		return 0
	}

	seen := make(map[string]struct{}, len(right))
	overlap := 0
	for _, token := range right {
		if !isSignalPromptToken(token) {
			continue
		}
		if _, ok := seen[token]; ok {
			continue
		}
		if _, ok := leftSet[token]; ok {
			overlap++
			seen[token] = struct{}{}
		}
	}

	return overlap
}

func extractNormalizedMCPServerNames(ag *resolvedRouteAgent) []string {
	if ag == nil || len(ag.MCPServers) == 0 {
		return []string{}
	}

	servers := make([]string, 0, len(ag.MCPServers))
	for _, name := range ag.MCPServers {
		logicalName := name
		if _, serverName, _, ok := workspace.ParseRuntimeMCPServerName(name); ok {
			logicalName = serverName
		}
		normalized := normalizeRouteToken(strings.ReplaceAll(logicalName, "_", "-"))
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

func normalizeHomeAssistantRouteContext(context *HomeAssistantRouteContext) normalizedHomeAssistantRouteContext {
	if context == nil {
		return normalizedHomeAssistantRouteContext{}
	}
	return normalizedHomeAssistantRouteContext{
		Surface:     normalizeRouteToken(context.Surface),
		PagePath:    normalizeRouteToken(context.PagePath),
		WorkspaceID: strings.TrimSpace(context.WorkspaceID),
		TaskID:      strings.TrimSpace(context.TaskID),
		SessionID:   strings.TrimSpace(context.SessionID),
		Origin:      normalizeRouteToken(context.Origin),
	}
}

func (c normalizedHomeAssistantRouteContext) hasWorkspaceContext() bool {
	if strings.TrimSpace(c.WorkspaceID) != "" {
		return true
	}
	return c.hasWorkspaceSurfaceContext()
}

func (c normalizedHomeAssistantRouteContext) hasWorkspaceSurfaceContext() bool {
	if strings.HasPrefix(c.PagePath, "/workspaces/") {
		return true
	}
	return c.Surface == "workspace" || c.Surface == "workspace_detail" || c.Surface == "workspace_canvas" || c.Surface == "workspace_task"
}

func determineRouteModeAndTargetSurface(intent homeAssistantIntent, intentVariant string, context normalizedHomeAssistantRouteContext, workspaceRecommended bool) (string, string) {
	if intent.Key == homeAssistantAppIntrospectionIntent.Key || intent.Key == homeAssistantAppNavigationIntent.Key {
		return homeAssistantRouteModeInline, "current"
	}
	if intent.Key == homeAssistantUtilityIntent.Key {
		return "utility_direct", "current"
	}
	if intent.Key == homeAssistantWorkspaceCreateIntent.Key {
		return "workspace_task", "workspace"
	}
	if intent.Key == homeAssistantCalendarIntent.Key && intentVariant == "workspace_schedule" && context.hasWorkspaceContext() {
		return "workspace_task", "workspace"
	}
	if intent.Key == homeAssistantCalendarIntent.Key {
		return "specialist_handoff", "chat"
	}
	if intent.Key == homeAssistantAppLaunchIntent.Key {
		return "specialist_handoff", "chat"
	}
	if context.hasWorkspaceContext() {
		return "workspace_task", "workspace"
	}
	if workspaceRecommended {
		return "workspace_task", "workspace"
	}
	return "specialist_handoff", "chat"
}

func determineRoutingPolicy(
	intent homeAssistantIntent,
	intentVariant string,
	routeMode string,
	targetSurface string,
	context normalizedHomeAssistantRouteContext,
) string {
	if routeMode == "workspace_task" && targetSurface == "workspace" {
		return homeAssistantPolicyAssistantOnly
	}

	switch intent.Key {
	case homeAssistantAppIntrospectionIntent.Key, homeAssistantAppNavigationIntent.Key:
		return homeAssistantPolicyAssistantOnly
	case homeAssistantUtilityIntent.Key, homeAssistantWorkspaceCreateIntent.Key:
		return homeAssistantPolicyAssistantOnly
	case homeAssistantEmailIntent.Key, homeAssistantAppLaunchIntent.Key:
		return homeAssistantPolicySpecialistRequired
	case homeAssistantCalendarIntent.Key:
		if intentVariant == "workspace_schedule" && context.hasWorkspaceContext() {
			return homeAssistantPolicyAssistantOnly
		}
		return homeAssistantPolicySpecialistRequired
	default:
		return homeAssistantPolicyAssistantPreferred
	}
}

func determineHomeAssistantContextMode(routeMode string) string {
	switch strings.TrimSpace(routeMode) {
	case "workspace_task":
		return homeAssistantContextWorkspace
	default:
		return homeAssistantContextDirect
	}
}

func determineHomeAssistantHandoffPolicy(intent homeAssistantIntent, intentVariant string) string {
	switch intent.Key {
	case homeAssistantUtilityIntent.Key:
		return homeAssistantHandoffTool
	case homeAssistantEmailIntent.Key, homeAssistantAppLaunchIntent.Key:
		return homeAssistantHandoffSpecialist
	case homeAssistantCalendarIntent.Key:
		if intentVariant != "workspace_schedule" {
			return homeAssistantHandoffSpecialist
		}
	}
	return homeAssistantHandoffAssistant
}

func shouldRecommendWorkspace(prompt string, intent homeAssistantIntent) bool {
	if intent.Key != homeAssistantDefaultIntent.Key {
		return false
	}
	if _, ok := parseHomeAssistantAppLaunchPrompt(prompt); ok {
		return false
	}

	normalized := normalizeRouteToken(prompt)
	if normalized == "" {
		return false
	}

	hasBuildVerb := promptContainsAnyRoutePhrase(normalized, homeAssistantComplexProjectBuildVerbs)
	hasProjectTarget := promptContainsAnyRoutePhrase(normalized, homeAssistantComplexProjectTargets)
	if hasBuildVerb && hasProjectTarget {
		return true
	}

	complexitySignalCount := countRoutePhraseMatches(normalized, homeAssistantComplexProjectSignals)
	if hasProjectTarget && complexitySignalCount >= 1 {
		return true
	}

	tokenCount := len(tokenizePrompt(normalized))
	if hasBuildVerb && complexitySignalCount >= 1 && tokenCount >= 8 {
		return true
	}

	return false
}

func shouldClassifyWorkspaceCreate(prompt string) bool {
	normalized := normalizeRouteToken(prompt)
	if normalized == "" {
		return false
	}
	if _, ok := parseHomeAssistantAppLaunchPrompt(prompt); ok {
		return false
	}
	return strings.HasPrefix(normalized, "create workspace") ||
		strings.HasPrefix(normalized, "create a workspace") ||
		strings.HasPrefix(normalized, "create an workspace") ||
		strings.HasPrefix(normalized, "new workspace")
}

func promptContainsAnyRoutePhrase(normalizedPrompt string, phrases []string) bool {
	if normalizedPrompt == "" || len(phrases) == 0 {
		return false
	}
	for _, phrase := range phrases {
		if phrase == "" {
			continue
		}
		if containsRoutePhrase(normalizedPrompt, phrase) {
			return true
		}
	}
	return false
}

func countRoutePhraseMatches(normalizedPrompt string, phrases []string) int {
	if normalizedPrompt == "" || len(phrases) == 0 {
		return 0
	}
	count := 0
	for _, phrase := range phrases {
		if phrase == "" {
			continue
		}
		if containsRoutePhrase(normalizedPrompt, phrase) {
			count++
		}
	}
	return count
}

func containsRoutePhrase(normalizedText, phrase string) bool {
	if normalizedText == "" {
		return false
	}

	phraseTokens := tokenizePrompt(phrase)
	if len(phraseTokens) == 0 {
		return false
	}
	textTokens := tokenizePrompt(normalizedText)
	if len(textTokens) == 0 {
		return false
	}

	if len(phraseTokens) == 1 {
		for _, token := range textTokens {
			if routeTokenMatches(token, phraseTokens[0]) {
				return true
			}
		}
		return false
	}

	if len(textTokens) < len(phraseTokens) {
		return false
	}
	for i := 0; i <= len(textTokens)-len(phraseTokens); i++ {
		match := true
		for j := 0; j < len(phraseTokens); j++ {
			if !routeTokenMatches(textTokens[i+j], phraseTokens[j]) {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}

	return false
}

func routeTokenMatches(textToken, phraseToken string) bool {
	if textToken == phraseToken {
		return true
	}
	if len(phraseToken) <= 3 {
		return false
	}

	textVariants := routeTokenVariants(textToken)
	phraseVariants := routeTokenVariants(phraseToken)
	for _, textVariant := range textVariants {
		for _, phraseVariant := range phraseVariants {
			if textVariant == phraseVariant {
				return true
			}
		}
	}

	return false
}

func routeTokenVariants(token string) []string {
	if token == "" {
		return nil
	}

	variants := make([]string, 0, 5)
	seen := make(map[string]struct{}, 5)
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		if _, ok := seen[value]; ok {
			return
		}
		seen[value] = struct{}{}
		variants = append(variants, value)
	}

	add(token)
	if len(token) > 4 && strings.HasSuffix(token, "s") {
		add(strings.TrimSuffix(token, "s"))
	}
	if len(token) > 5 && strings.HasSuffix(token, "es") {
		add(strings.TrimSuffix(token, "es"))
	}
	if len(token) > 5 && strings.HasSuffix(token, "ed") {
		trimmed := strings.TrimSuffix(token, "ed")
		add(trimmed)
		add(trimmed + "e")
	}
	if len(token) > 6 && strings.HasSuffix(token, "ing") {
		trimmed := strings.TrimSuffix(token, "ing")
		add(trimmed)
		add(trimmed + "e")
	}

	return variants
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
	if looksLikeWorkspaceArtifactTarget(target) {
		return "", false
	}

	// Skip obvious URL/path-like targets that are more likely file or web intents.
	if strings.Contains(target, "://") || strings.Contains(target, "/") || strings.Contains(target, "\\") || looksLikeWebHostTarget(target) {
		return "", false
	}

	return target, true
}

func looksLikeWorkspaceArtifactTarget(target string) bool {
	tokens := tokenizePrompt(target)
	if len(tokens) == 0 {
		return false
	}

	last := tokens[len(tokens)-1]
	switch last {
	case "note", "notes", "task", "tasks", "workspace", "workspaces", "session", "sessions":
	default:
		return false
	}

	if len(tokens) == 1 {
		return true
	}

	for _, token := range tokens[:len(tokens)-1] {
		switch token {
		case "a", "an", "the", "my", "new", "another", "separate", "this", "that":
			return true
		}
	}

	return false
}

func looksLikeWebHostTarget(target string) bool {
	candidate := strings.TrimSpace(strings.ToLower(target))
	if candidate == "" {
		return false
	}
	// Host-like targets should not be treated as desktop app names.
	if strings.ContainsAny(candidate, " \t\n\r") || !strings.Contains(candidate, ".") {
		return false
	}

	labels := strings.Split(candidate, ".")
	if len(labels) < 2 {
		return false
	}

	for _, label := range labels {
		if label == "" {
			return false
		}
		for _, r := range label {
			if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' {
				return false
			}
		}
		if strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
	}

	return len(labels[len(labels)-1]) >= 2
}

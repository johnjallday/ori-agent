package agenthttp

import "strings"

// homeAssistantConversationIntent is an everyday request the hired personal
// assistant answers itself, in its own conversation: writing, rewriting,
// translating, thinking something through. It is answered inline by the home
// harness and is never a handoff to another agent.
var homeAssistantConversationIntent = homeAssistantIntent{
	Key:           "assistant_conversation",
	Label:         "conversation",
	SuggestedName: "Personal Assistant",
	MinScore:      3,
}

// compositionPolitePrefixes are stripped, repeatedly, before the opening verb
// is read: "please can you write…" opens with "write".
var compositionPolitePrefixes = []string{
	"please ", "hey ", "ok ", "okay ", "now ", "then ", "and ", "also ",
	"can you ", "could you ", "would you ", "will you ",
	"i need you to ", "i want you to ", "i would like you to ", "i'd like you to ",
	"help me to ", "help me ", "let's ", "lets ",
}

// compositionLeads open a request to produce or reshape text. The second group
// are follow-ups that point at an earlier draft ("make it warmer").
var compositionLeads = []string{
	"write ", "draft ", "compose ", "rewrite ", "rephrase ", "reword ",
	"translate ", "proofread ", "polish ", "paraphrase ",
	"make it ", "make this ", "make that ",
	"turn it ", "turn this ", "turn that ",
	"convert it ", "convert this ", "convert that ",
	"change it ", "change the tone", "shorten it", "shorten this", "shorten that",
	"give it to me ", "give me that in ", "give me it in ", "say it ", "put it in ",
}

// stripCompositionPolitePrefixes removes leading politeness and connectives
// from an already-normalized prompt so its opening verb can be read.
func stripCompositionPolitePrefixes(text string) string {
	for stripped := true; stripped; {
		stripped = false
		for _, prefix := range compositionPolitePrefixes {
			if strings.HasPrefix(text, prefix) {
				text = strings.TrimSpace(strings.TrimPrefix(text, prefix))
				stripped = true
			}
		}
	}
	return text
}

// isCompositionRequest reports whether the prompt opens by asking for text to
// be written or reshaped. Composing needs no connector, so such a request never
// has to create an agent or a connection to be answered.
func isCompositionRequest(prompt string) bool {
	text := stripCompositionPolitePrefixes(normalizeRouteToken(prompt))
	for _, lead := range compositionLeads {
		if strings.HasPrefix(text, lead) || text == strings.TrimSpace(lead) {
			return true
		}
	}
	return false
}

// routesToAssistantConversation decides whether the hired assistant answers
// this request in its own conversation. It only takes requests that had no
// better owner: a workspace in the route context, a recommended project
// workspace, app activity/navigation, workspace creation, and any request a
// specialist matched all keep their existing route.
func routesToAssistantConversation(
	workContext *PersonalAssistantWorkContext,
	prompt string,
	intent homeAssistantIntent,
	routeContext normalizedHomeAssistantRouteContext,
	workspaceRecommended bool,
	match *routedAgentMatch,
) bool {
	if workContext == nil || !workContext.ReadyForWork() {
		return false
	}
	if routeContext.hasWorkspaceContext() {
		return false
	}
	// Saving a draft from the conversation is the assistant's own reviewed
	// action. It is never handed to a specialist that happened to match a word
	// such as "todo".
	if isAssistantDraftSaveRequest(prompt) || isAssistantMemoryRequest(prompt) {
		return true
	}
	if workspaceRecommended {
		return false
	}
	switch intent.Key {
	case homeAssistantDefaultIntent.Key:
	case homeAssistantAppIntrospectionIntent.Key, homeAssistantAppNavigationIntent.Key, homeAssistantWorkspaceCreateIntent.Key:
		return false
	default:
		if !isCompositionRequest(prompt) {
			return false
		}
	}
	return match == nil || isAssistantOwnAgent(match.Name, workContext)
}

// isAssistantOwnAgent reports whether a matched agent is the assistant itself
// rather than a specialist: the protected system assistant, or the hired
// profile. Neither is somewhere to hand a conversation off to.
func isAssistantOwnAgent(name string, workContext *PersonalAssistantWorkContext) bool {
	name = strings.TrimSpace(name)
	if name == "" || isSystemAssistantAgent(name) {
		return true
	}
	hired := ""
	if workContext != nil {
		hired = strings.TrimSpace(workContext.ConversationAgent)
	}
	return hired != "" && strings.EqualFold(name, hired)
}

// assistantConversationRoute is the route response for a conversation. It
// names no handler: the protected system assistant is never the hired identity.
func assistantConversationRoute(workContext *PersonalAssistantWorkContext) *HomeAssistantRouteResponse {
	return &HomeAssistantRouteResponse{
		Intent:                 homeAssistantConversationIntent.Key,
		IntentLabel:            homeAssistantConversationIntent.Label,
		PersonalAssistantState: workContext.State,
		AssistantName:          boundedContextText(workContext.DisplayName, 100),
		RoutingPolicy:          homeAssistantPolicyAssistantOnly,
		ContextMode:            homeAssistantContextDirect,
		HandoffPolicy:          homeAssistantHandoffAssistant,
		RouteMode:              homeAssistantRouteModeInline,
		TargetSurface:          "current",
		SuggestedAgentName:     homeAssistantConversationIntent.SuggestedName,
		Reasons:                []string{"answered by your personal assistant"},
	}
}

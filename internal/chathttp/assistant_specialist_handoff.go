package chathttp

import (
	"net/http"
	"strings"

	"github.com/johnjallday/ori-agent/internal/agenthttp"
	"github.com/johnjallday/ori-agent/internal/logger"
)

const assistantRoutingPolicySpecialistRequired = "specialist_required"

func (h *Handler) maybeHandleAssistantSpecialistHandoff(
	w http.ResponseWriter,
	r *http.Request,
	originalQuery string,
	sessionID string,
	routeCtx normalizedChatRouteContext,
	executionAgent executionAgentResolution,
) bool {
	if h == nil || h.store == nil || w == nil || r == nil {
		return false
	}

	prompt := strings.TrimSpace(originalQuery)
	if prompt == "" || !executionAgent.isAssistantMode() || strings.HasPrefix(prompt, "/") {
		return false
	}

	router := agenthttp.NewHomeAssistantRouteHandler(h.store)
	if h.runtimeResolver != nil {
		router.SetRuntimeResolver(h.runtimeResolver)
	}

	routeResp, err := router.RoutePrompt(prompt, &agenthttp.HomeAssistantRouteContext{
		Surface:     routeCtx.Surface,
		PagePath:    routeCtx.PagePath,
		WorkspaceID: routeCtx.WorkspaceID,
		SessionID:   sessionID,
		Origin:      routeCtx.Origin,
	})
	if err != nil {
		logger.Warn("Assistant specialist handoff routing failed", logger.Fields{
			"session_id": sessionID,
			"error":      err.Error(),
		})
		return false
	}
	if routeResp == nil || strings.TrimSpace(routeResp.RoutingPolicy) != assistantRoutingPolicySpecialistRequired {
		return false
	}

	responseText := buildAssistantSpecialistHandoffMessage(routeResp)
	reason := "assistant policy routed request to specialist"

	if h.utilityTelemetry != nil {
		h.utilityTelemetry.RecordDelegationEvent(routeModeSpecialistFlow, reason, strings.TrimSpace(routeResp.MatchedAgent))
	}

	h.storeMessageInSession(r.Context(), sessionID, "user", prompt)
	h.storeMessageInSession(r.Context(), sessionID, "assistant", responseText)

	payload := attachRouteMetadata(map[string]any{
		"response":           responseText,
		"routing_policy":     routeResp.RoutingPolicy,
		"matched_agent":      routeResp.MatchedAgent,
		"requires_creation":  routeResp.RequiresCreation,
		"intent":             routeResp.Intent,
		"intent_label":       routeResp.IntentLabel,
		"specialist_handoff": routeResp,
	}, chatRouteMetadata{
		Mode:   routeModeSpecialistFlow,
		Reason: reason,
	})
	writeJSONResponse(w, payload)
	return true
}

func buildAssistantSpecialistHandoffMessage(routeResp *agenthttp.HomeAssistantRouteResponse) string {
	if routeResp == nil {
		return "This request needs a specialist workflow."
	}

	matchedAgent := strings.TrimSpace(routeResp.MatchedAgent)
	if matchedAgent != "" && !routeResp.RequiresCreation {
		return `This request needs a specialist. Routing it to "` + matchedAgent + `" in a dedicated chat session.`
	}

	suggested := strings.TrimSpace(routeResp.SuggestedAgentName)
	if suggested != "" {
		return `This request needs a specialist, but I couldn't find one ready for it. Create or configure "` + suggested + `" and try again.`
	}

	return "This request needs a specialist, but I couldn't find one ready for it yet."
}

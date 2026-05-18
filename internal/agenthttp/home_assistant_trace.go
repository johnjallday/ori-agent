package agenthttp

import (
	"net/http"
	"strings"
	"time"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
)

type HomeAssistantIntakeTrace struct {
	ID                    string                            `json:"id,omitempty"`
	Prompt                string                            `json:"prompt"`
	Intent                string                            `json:"intent,omitempty"`
	IntentVariant         string                            `json:"intent_variant,omitempty"`
	RoutingPolicy         string                            `json:"routing_policy,omitempty"`
	ContextMode           string                            `json:"context_mode,omitempty"`
	HandoffPolicy         string                            `json:"handoff_policy,omitempty"`
	RouteMode             string                            `json:"route_mode,omitempty"`
	TargetSurface         string                            `json:"target_surface,omitempty"`
	MatchedAgent          string                            `json:"matched_agent,omitempty"`
	WorkspaceState        string                            `json:"workspace_state,omitempty"`
	SelectedWorkspaceID   string                            `json:"selected_workspace_id,omitempty"`
	SelectedWorkspaceName string                            `json:"selected_workspace_name,omitempty"`
	FinalWorkspaceID      string                            `json:"final_workspace_id,omitempty"`
	Confidence            float64                           `json:"confidence,omitempty"`
	Reasons               []string                          `json:"reasons,omitempty"`
	Candidates            []HomeAssistantWorkspaceCandidate `json:"candidates,omitempty"`
	UserOverride          bool                              `json:"user_override,omitempty"`
	FinalHandoffTarget    string                            `json:"final_handoff_target"`
	RouteContext          *HomeAssistantRouteContext        `json:"route_context,omitempty"`
	CreatedAt             time.Time                         `json:"created_at,omitempty"`
}

func (h *HomeAssistantRouteHandler) TraceHandler(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}

	var trace HomeAssistantIntakeTrace
	if !orihttp.ParseJSONBody(w, r, &trace) {
		return
	}

	trace.Prompt = strings.TrimSpace(trace.Prompt)
	trace.FinalHandoffTarget = strings.TrimSpace(trace.FinalHandoffTarget)
	if trace.Prompt == "" {
		orihttp.BadRequest(w, "prompt is required")
		return
	}
	if trace.FinalHandoffTarget == "" {
		orihttp.BadRequest(w, "final_handoff_target is required")
		return
	}
	if h.IntakeTraceStore != nil {
		if err := h.IntakeTraceStore.RecordTrace(r.Context(), &trace); err != nil {
			logger.Error("Failed to persist home assistant intake trace", logger.Fields{"error": err})
			orihttp.InternalError(w, "failed to persist intake trace")
			return
		}
	}

	logger.Info("Home assistant intake trace", logger.Fields{
		"scope":                   "home_assistant.intake",
		"prompt":                  trace.Prompt,
		"intent":                  strings.TrimSpace(trace.Intent),
		"intent_variant":          strings.TrimSpace(trace.IntentVariant),
		"routing_policy":          strings.TrimSpace(trace.RoutingPolicy),
		"context_mode":            strings.TrimSpace(trace.ContextMode),
		"handoff_policy":          strings.TrimSpace(trace.HandoffPolicy),
		"route_mode":              strings.TrimSpace(trace.RouteMode),
		"target_surface":          strings.TrimSpace(trace.TargetSurface),
		"matched_agent":           strings.TrimSpace(trace.MatchedAgent),
		"workspace_state":         strings.TrimSpace(trace.WorkspaceState),
		"selected_workspace_id":   strings.TrimSpace(trace.SelectedWorkspaceID),
		"selected_workspace_name": strings.TrimSpace(trace.SelectedWorkspaceName),
		"final_workspace_id":      strings.TrimSpace(trace.FinalWorkspaceID),
		"confidence":              trace.Confidence,
		"reasons":                 append([]string(nil), trace.Reasons...),
		"candidate_count":         len(trace.Candidates),
		"user_override":           trace.UserOverride,
		"final_handoff_target":    trace.FinalHandoffTarget,
	})
	orihttp.RespondNoContent(w)
}

func (h *HomeAssistantRouteHandler) TraceSummaryHandler(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodGet) {
		return
	}
	if h.IntakeTraceStore == nil {
		orihttp.ServiceUnavailable(w, "intake trace summary is unavailable")
		return
	}

	summary, err := h.IntakeTraceStore.Summary(r.Context())
	if err != nil {
		logger.Error("Failed to summarize home assistant intake traces", logger.Fields{"error": err})
		orihttp.InternalError(w, "failed to summarize intake traces")
		return
	}
	orihttp.WriteJSON(w, summary)
}

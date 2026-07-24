package sessionhttp

import (
	"net/http"
	"strings"

	"github.com/johnjallday/ori-agent/internal/config"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/session"
)

type SmartInputDecision string

const (
	SmartInputDecisionTask SmartInputDecision = "task"
	SmartInputDecisionChat SmartInputDecision = "chat"
	// SmartInputDecisionBacklog captures an explicit "add X to the backlog"
	// intent (PRD workspace-backlog FR21-22): the created item must remain
	// uncommitted (no assignee, no execution) until explicitly promoted, so
	// it is a distinct decision from SmartInputDecisionTask rather than a
	// task variant.
	SmartInputDecisionBacklog SmartInputDecision = "backlog"
)

type SmartInputMethod string

const (
	SmartInputMethodHeuristic SmartInputMethod = "heuristic"
	SmartInputMethodLLM       SmartInputMethod = "llm"
	SmartInputMethodFallback  SmartInputMethod = "fallback"
)

type SmartInputClassifyRequest struct {
	WorkspaceID string `json:"workspace_id,omitempty"`
	Input       string `json:"input"`
}

type SmartInputClassifyResponse struct {
	WorkspaceID       string             `json:"workspace_id,omitempty"`
	Decision          SmartInputDecision `json:"decision"`
	Confidence        float64            `json:"confidence"`
	Method            SmartInputMethod   `json:"method"`
	NeedsConfirmation bool               `json:"needs_confirmation"`
	Message           string             `json:"message,omitempty"`
}

type SmartInputOverrideRequest struct {
	WorkspaceID       string             `json:"workspace_id,omitempty"`
	Input             string             `json:"input"`
	PredictedDecision SmartInputDecision `json:"predicted_decision"`
	SelectedDecision  SmartInputDecision `json:"selected_decision"`
	Confidence        float64            `json:"confidence"`
	Method            SmartInputMethod   `json:"method"`
}

type SmartInputOverrideResponse struct {
	Success bool `json:"success"`
}

type SmartInputHandler struct {
	overrideStore *session.SmartInputOverrideStore
	llmFactory    *llm.Factory
	configManager *config.Manager
}

func NewSmartInputHandler(sessionStore session.HybridStore, llmFactory *llm.Factory, configManager *config.Manager) *SmartInputHandler {
	var overrideStore *session.SmartInputOverrideStore
	if sessionStore != nil {
		overrideStore = session.NewSmartInputOverrideStore(sessionStore.DB())
	}

	return &SmartInputHandler{
		overrideStore: overrideStore,
		llmFactory:    llmFactory,
		configManager: configManager,
	}
}

func (h *SmartInputHandler) HandleClassify(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		_ = orihttp.RespondMethodNotAllowed(w)
		return
	}

	var req SmartInputClassifyRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	input := strings.TrimSpace(req.Input)
	if input == "" {
		_ = orihttp.RespondBadRequest(w, "input is required")
		return
	}

	heuristic := classifySmartInputHeuristic(input)
	resp := SmartInputClassifyResponse{
		WorkspaceID:       req.WorkspaceID,
		Decision:          heuristic.Decision,
		Confidence:        heuristic.Confidence,
		Method:            SmartInputMethodHeuristic,
		NeedsConfirmation: heuristic.Confidence < smartInputAutoConfidence,
	}

	if resp.NeedsConfirmation && h.llmFactory != nil && h.configManager != nil {
		llmResult, err := classifySmartInputWithSystemModel(r.Context(), h.llmFactory, h.configManager, input)
		if err != nil {
			logger.Warn("Smart input LLM classify failed", logger.Fields{"error": err})
			resp.Method = SmartInputMethodFallback
			resp.Message = "LLM unavailable"
		} else {
			resp.Decision = llmResult.Decision
			resp.Confidence = llmResult.Confidence
			resp.Method = SmartInputMethodLLM
			resp.NeedsConfirmation = resp.Confidence < smartInputLLMAutoConfidence
		}
	} else if resp.NeedsConfirmation {
		resp.Method = SmartInputMethodFallback
	}

	orihttp.WriteJSON(w, resp)
}

func (h *SmartInputHandler) HandleOverride(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		_ = orihttp.RespondMethodNotAllowed(w)
		return
	}

	var req SmartInputOverrideRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	input := strings.TrimSpace(req.Input)
	if input == "" {
		_ = orihttp.RespondBadRequest(w, "input is required")
		return
	}

	if !isValidSmartInputDecision(req.PredictedDecision) || !isValidSmartInputDecision(req.SelectedDecision) {
		_ = orihttp.RespondBadRequest(w, "predicted_decision and selected_decision must be task, chat, or backlog")
		return
	}

	if !isValidSmartInputMethod(req.Method) {
		_ = orihttp.RespondBadRequest(w, "method must be heuristic, llm, or fallback")
		return
	}

	if h.overrideStore == nil {
		_ = orihttp.RespondServiceUnavailable(w, "override logging not available")
		return
	}

	confidence := req.Confidence
	if confidence < 0 {
		confidence = 0
	}
	if confidence > 1 {
		confidence = 1
	}

	err := h.overrideStore.LogOverride(r.Context(), &session.SmartInputOverrideLog{
		WorkspaceID:       req.WorkspaceID,
		Input:             input,
		PredictedDecision: string(req.PredictedDecision),
		SelectedDecision:  string(req.SelectedDecision),
		Method:            string(req.Method),
		Confidence:        confidence,
	})
	if err != nil {
		logger.Error("Failed to log smart input override", logger.Fields{"error": err})
		_ = orihttp.RespondInternalError(w, "Failed to log override")
		return
	}

	orihttp.WriteJSON(w, SmartInputOverrideResponse{Success: true})
}

func isValidSmartInputDecision(decision SmartInputDecision) bool {
	switch decision {
	case SmartInputDecisionTask, SmartInputDecisionChat, SmartInputDecisionBacklog:
		return true
	default:
		return false
	}
}

func isValidSmartInputMethod(method SmartInputMethod) bool {
	switch method {
	case SmartInputMethodHeuristic, SmartInputMethodLLM, SmartInputMethodFallback:
		return true
	default:
		return false
	}
}

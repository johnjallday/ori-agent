package workspace

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
)

// GetMission returns the current mission configuration for a workspace.
// Includes computed fields (binding readiness) so the UI can decide whether
// to show the "classify your bindings" prompt without a second round-trip.
func (h *HTTPHandler) GetMission(w http.ResponseWriter, r *http.Request) {
	workspaceID := r.PathValue("workspaceID")
	if workspaceID == "" {
		orihttp.BadRequest(w, "workspace ID is required")
		return
	}
	ws, err := h.store.Get(workspaceID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace not found: %v", err))
		return
	}
	mcpUnclassified, skillUnclassified := UnclassifiedBindings(ws)

	resp := map[string]any{
		"mission":                 ws.Mission,
		"cadence":                 ws.Cadence,
		"autonomy_policy":         ws.AutonomyPolicy,
		"notification_policy":     ws.NotificationPolicy,
		"mission_enabled":         ws.MissionEnabled,
		"last_mission_run_at":     ws.LastMissionRunAt,
		"next_mission_run_at":     ws.NextMissionRunAt,
		"mission_execution_count": ws.MissionExecutionCount,
		"mission_failure_count":   ws.MissionFailureCount,
		"unclassified_mcp_ids":    mcpUnclassified,
		"unclassified_skill_ids":  skillUnclassified,
		"bindings_ready":          len(mcpUnclassified) == 0 && len(skillUnclassified) == 0,
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		logger.Error("encode mission response", logger.Fields{"error": err})
	}
}

// UpdateMissionRequest is the body for PUT /api/workspaces/{workspaceID}/mission.
// Every field uses a pointer so the caller can update one field at a time.
// Cadence tracks explicit null separately so callers can clear scheduling.
// Setting MissionEnabled to true with unclassified bindings is rejected with
// 412 — the UI must surface the classification prompt first.
type UpdateMissionRequest struct {
	Mission            *string             `json:"mission,omitempty"`
	Cadence            *ScheduleConfig     `json:"cadence,omitempty"`
	CadenceSet         bool                `json:"-"`
	AutonomyPolicy     *AutonomyPolicy     `json:"autonomy_policy,omitempty"`
	NotificationPolicy *NotificationPolicy `json:"notification_policy,omitempty"`
	MissionEnabled     *bool               `json:"mission_enabled,omitempty"`
}

func (req *UpdateMissionRequest) UnmarshalJSON(data []byte) error {
	var raw struct {
		Mission            *string             `json:"mission,omitempty"`
		Cadence            json.RawMessage     `json:"cadence,omitempty"`
		AutonomyPolicy     *AutonomyPolicy     `json:"autonomy_policy,omitempty"`
		NotificationPolicy *NotificationPolicy `json:"notification_policy,omitempty"`
		MissionEnabled     *bool               `json:"mission_enabled,omitempty"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	req.Mission = raw.Mission
	req.AutonomyPolicy = raw.AutonomyPolicy
	req.NotificationPolicy = raw.NotificationPolicy
	req.MissionEnabled = raw.MissionEnabled
	req.Cadence = nil
	req.CadenceSet = raw.Cadence != nil
	if !req.CadenceSet || string(raw.Cadence) == "null" {
		return nil
	}

	var cadence ScheduleConfig
	if err := json.Unmarshal(raw.Cadence, &cadence); err != nil {
		return fmt.Errorf("cadence: %w", err)
	}
	req.Cadence = &cadence
	return nil
}

// UpdateMission handles PUT/PATCH /api/workspaces/{workspaceID}/mission.
func (h *HTTPHandler) UpdateMission(w http.ResponseWriter, r *http.Request) {
	workspaceID := r.PathValue("workspaceID")
	if workspaceID == "" {
		orihttp.BadRequest(w, "workspace ID is required")
		return
	}
	var req UpdateMissionRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	if req.AutonomyPolicy != nil {
		switch *req.AutonomyPolicy {
		case AutonomyWatch, AutonomyPropose, "":
			// ok
		default:
			orihttp.BadRequest(w, fmt.Sprintf("unsupported autonomy_policy: %q (v1 supports watch, propose)", *req.AutonomyPolicy))
			return
		}
	}

	enablingNow := req.MissionEnabled != nil && *req.MissionEnabled

	updateErr := h.store.Update(workspaceID, func(ws *Workspace) error {
		if enablingNow && !MissionBindingsReady(ws) {
			return ErrMissionBindingsUnclassified
		}
		if req.Mission != nil {
			ws.Mission = *req.Mission
		}
		if req.CadenceSet {
			ws.Cadence = req.Cadence
		}
		if req.AutonomyPolicy != nil {
			ws.AutonomyPolicy = *req.AutonomyPolicy
		}
		if req.NotificationPolicy != nil {
			ws.NotificationPolicy = req.NotificationPolicy
		}
		if req.MissionEnabled != nil {
			ws.MissionEnabled = *req.MissionEnabled
			// When the user enables a mission, compute NextMissionRunAt right
			// away so the scheduler can pick it up on the next poll without
			// waiting for the first manual trigger.
			if *req.MissionEnabled && ws.Cadence != nil {
				ws.NextMissionRunAt = CalculateNextRun(*ws.Cadence, time.Now())
			}
			if !*req.MissionEnabled {
				ws.NextMissionRunAt = nil
			}
		}
		return nil
	})
	if errors.Is(updateErr, ErrMissionBindingsUnclassified) {
		_ = orihttp.RespondAPIError(w, http.StatusPreconditionFailed,
			orihttp.NewAPIError("mission_bindings_unclassified", updateErr.Error()))
		return
	}
	if updateErr != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to update mission: %v", updateErr))
		return
	}

	ws, err := h.store.Get(workspaceID)
	if err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to reload workspace: %v", err))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{
		"message":             "Mission updated successfully",
		"workspace":           workspaceID,
		"mission_enabled":     ws.MissionEnabled,
		"next_mission_run_at": ws.NextMissionRunAt,
	}); err != nil {
		logger.Error("encode update mission response", logger.Fields{"error": err})
	}
}

// TriggerMission handles POST /api/workspaces/{workspaceID}/mission/trigger.
// Fires a mission run on demand, regardless of cadence. The cycle ordinal is
// the next one in sequence so manual + scheduled runs share a monotonic counter.
func (h *HTTPHandler) TriggerMission(w http.ResponseWriter, r *http.Request) {
	workspaceID := r.PathValue("workspaceID")
	if workspaceID == "" {
		orihttp.BadRequest(w, "workspace ID is required")
		return
	}
	if h.scheduler == nil {
		orihttp.ServiceUnavailable(w, "scheduler is not configured")
		return
	}
	runID, err := h.scheduler.TriggerMissionManually(r.Context(), workspaceID)
	if errors.Is(err, ErrMissionTriggerNotConfigured) {
		orihttp.ServiceUnavailable(w, err.Error())
		return
	}
	if errors.Is(err, ErrMissionBindingsUnclassified) {
		_ = orihttp.RespondAPIError(w, http.StatusPreconditionFailed,
			orihttp.NewAPIError("mission_bindings_unclassified", err.Error()))
		return
	}
	if err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to trigger mission: %v", err))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{
		"message":      "Mission run triggered",
		"workspace":    workspaceID,
		"run_id":       runID,
		"triggered_at": time.Now().UTC(),
	}); err != nil {
		logger.Error("encode trigger mission response", logger.Fields{"error": err})
	}
}

// RunBaselineNow handles POST /api/workspaces/{workspaceID}/mission/baseline.
// Convenience endpoint for the UI's "Run baseline now" button. Functionally
// identical to TriggerMission today — the difference is semantic: callers
// expect this only to be used when no mission has run yet, and the response
// flags whether the resulting run is in fact the baseline.
func (h *HTTPHandler) RunBaselineNow(w http.ResponseWriter, r *http.Request) {
	workspaceID := r.PathValue("workspaceID")
	if workspaceID == "" {
		orihttp.BadRequest(w, "workspace ID is required")
		return
	}
	if h.scheduler == nil {
		orihttp.ServiceUnavailable(w, "scheduler is not configured")
		return
	}
	ws, err := h.store.Get(workspaceID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace not found: %v", err))
		return
	}
	wasBaseline := ws.MissionExecutionCount == 0
	runID, err := h.scheduler.TriggerMissionManually(r.Context(), workspaceID)
	if errors.Is(err, ErrMissionTriggerNotConfigured) {
		orihttp.ServiceUnavailable(w, err.Error())
		return
	}
	if errors.Is(err, ErrMissionBindingsUnclassified) {
		_ = orihttp.RespondAPIError(w, http.StatusPreconditionFailed,
			orihttp.NewAPIError("mission_bindings_unclassified", err.Error()))
		return
	}
	if err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to trigger mission: %v", err))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{
		"message":      "Baseline mission run triggered",
		"workspace":    workspaceID,
		"run_id":       runID,
		"is_baseline":  wasBaseline,
		"triggered_at": time.Now().UTC(),
	}); err != nil {
		logger.Error("encode baseline response", logger.Fields{"error": err})
	}
}

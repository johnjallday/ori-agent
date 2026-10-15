package workspace

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
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

	// Count findings still awaiting triage (new or snoozed) so the UI can show
	// whether the goal's runs are actually producing actionable output.
	openFindings := 0
	for _, opp := range ws.Opportunities {
		if opp.IsOpen() {
			openFindings++
		}
	}

	running, inFlight := h.scheduler.MissionRunning(workspaceID)
	needsAttention := ""
	if ws.GoalToolboxPolicy != nil && ws.GoalToolboxPolicy.NeedsAttention {
		needsAttention = ws.GoalToolboxPolicy.NeedsAttentionReason
	}

	resp := map[string]any{
		// Goal-run progress (FR26, FR27): whether a run is executing now, and
		// why the last attempt to start one could not (e.g. no model).
		"run_in_flight":             inFlight,
		"run_in_flight_detail":      running,
		"needs_attention":           needsAttention,
		"first_run_pending":         ws.GoalFirstRunPending,
		"mission":                   ws.Mission,
		"cadence":                   ws.Cadence,
		"autonomy_policy":           ws.AutonomyPolicy,
		"notification_policy":       ws.NotificationPolicy,
		"mission_enabled":           ws.MissionEnabled,
		"mission_cadence_heartbeat": ws.MissionCadenceHeartbeat,
		"last_mission_run_at":       ws.LastMissionRunAt,
		"next_mission_run_at":       ws.NextMissionRunAt,
		"mission_execution_count":   ws.MissionExecutionCount,
		"mission_failure_count":     ws.MissionFailureCount,
		"mission_runs":              missionRunsOrEmpty(ws.MissionRuns),
		"open_findings_count":       openFindings,
		"unclassified_mcp_ids":      mcpUnclassified,
		"unclassified_skill_ids":    skillUnclassified,
		"bindings_ready":            len(mcpUnclassified) == 0 && len(skillUnclassified) == 0,
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		logger.Error("encode mission response", logger.Fields{"error": err})
	}
}

// missionRunsOrEmpty keeps the run history a JSON array even before any run.
func missionRunsOrEmpty(runs []MissionRunRecord) []MissionRunRecord {
	if runs == nil {
		return []MissionRunRecord{}
	}
	return runs
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
	CadenceHeartbeat   *bool               `json:"mission_cadence_heartbeat,omitempty"`
}

func (req *UpdateMissionRequest) UnmarshalJSON(data []byte) error {
	var raw struct {
		Mission            *string             `json:"mission,omitempty"`
		Cadence            json.RawMessage     `json:"cadence,omitempty"`
		AutonomyPolicy     *AutonomyPolicy     `json:"autonomy_policy,omitempty"`
		NotificationPolicy *NotificationPolicy `json:"notification_policy,omitempty"`
		MissionEnabled     *bool               `json:"mission_enabled,omitempty"`
		CadenceHeartbeat   *bool               `json:"mission_cadence_heartbeat,omitempty"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	req.Mission = raw.Mission
	req.AutonomyPolicy = raw.AutonomyPolicy
	req.NotificationPolicy = raw.NotificationPolicy
	req.MissionEnabled = raw.MissionEnabled
	req.CadenceHeartbeat = raw.CadenceHeartbeat
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

	firstGoal := false
	updateErr := h.store.Update(workspaceID, func(ws *Workspace) error {
		if enablingNow && !MissionBindingsReady(ws) {
			return ErrMissionBindingsUnclassified
		}
		if req.Mission != nil {
			// A goal saved where there was none starts one run (FR24).
			firstGoal = strings.TrimSpace(ws.Mission) == "" && strings.TrimSpace(*req.Mission) != ""
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
		if req.CadenceHeartbeat != nil {
			ws.MissionCadenceHeartbeat = *req.CadenceHeartbeat
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

	// The first goal starts one run once the save has succeeded, whatever
	// MissionEnabled says; a run that cannot start records why on the goal.
	var firstRun map[string]any
	if firstGoal {
		firstRun = h.startGoalRun(workspaceID, MissionOriginFirstGoal)
	}

	ws, err := h.store.Get(workspaceID)
	if err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to reload workspace: %v", err))
		return
	}
	resp := map[string]any{
		"message":             "Mission updated successfully",
		"workspace":           workspaceID,
		"mission_enabled":     ws.MissionEnabled,
		"next_mission_run_at": ws.NextMissionRunAt,
	}
	if firstRun != nil {
		resp["first_run"] = firstRun
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		logger.Error("encode update mission response", logger.Fields{"error": err})
	}
}

// startGoalRun starts a goal run in the background and describes the outcome
// for a response body: started with a run id, or not started and why.
func (h *HTTPHandler) startGoalRun(workspaceID, origin string) map[string]any {
	if h.scheduler == nil {
		return map[string]any{"started": false, "reason": ErrMissionTriggerNotConfigured.Error()}
	}
	runID, err := h.scheduler.StartMissionRun(workspaceID, origin)
	if err != nil {
		outcome := map[string]any{"started": false, "reason": err.Error()}
		var blocked *MissionBlockedError
		if errors.As(err, &blocked) {
			outcome["code"] = blocked.Code
		}
		return outcome
	}
	return map[string]any{"started": true, "run_id": runID}
}

// GoalFirstOpen handles POST /api/workspaces/{workspaceID}/mission/first-open.
// A workspace created with a goal does not run at creation; its first goal
// run starts the first time it is opened (FR25). The pending marker is
// consumed atomically, so reloads and other tabs start nothing more.
func (h *HTTPHandler) GoalFirstOpen(w http.ResponseWriter, r *http.Request) {
	workspaceID := r.PathValue("workspaceID")
	if workspaceID == "" {
		orihttp.BadRequest(w, "workspace ID is required")
		return
	}
	consumed := false
	if err := h.store.Update(workspaceID, func(ws *Workspace) error {
		consumed = ws.GoalFirstRunPending
		ws.GoalFirstRunPending = false
		return nil
	}); err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace not found: %v", err))
		return
	}
	resp := map[string]any{"pending": consumed}
	if consumed {
		resp["first_run"] = h.startGoalRun(workspaceID, MissionOriginFirstGoal)
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		logger.Error("encode goal first-open response", logger.Fields{"error": err})
	}
}

// writeMissionTriggerError writes the response for a refused or failed manual
// goal run and reports whether it wrote one. nil writes nothing.
func writeMissionTriggerError(w http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, ErrMissionTriggerNotConfigured):
		orihttp.ServiceUnavailable(w, err.Error())
	case errors.Is(err, ErrMissionRunInFlight):
		_ = orihttp.RespondAPIError(w, http.StatusConflict,
			orihttp.NewAPIError("goal_run_in_progress", err.Error()))
	case errors.As(err, new(*MissionBlockedError)):
		var blocked *MissionBlockedError
		errors.As(err, &blocked)
		_ = orihttp.RespondAPIError(w, http.StatusPreconditionFailed,
			orihttp.NewAPIError(blocked.Code, blocked.Reason))
	case errors.Is(err, ErrMissionGoalRequired):
		_ = orihttp.RespondAPIError(w, http.StatusPreconditionFailed,
			orihttp.NewAPIError("goal_required", err.Error()))
	case errors.Is(err, ErrMissionBindingsUnclassified):
		_ = orihttp.RespondAPIError(w, http.StatusPreconditionFailed,
			orihttp.NewAPIError("mission_bindings_unclassified", err.Error()))
	default:
		orihttp.InternalError(w, fmt.Sprintf("Failed to trigger mission: %v", err))
	}
	return true
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
	// The run executes in the background, detached from this request; the
	// client polls GET .../mission until run_in_flight clears (FR26).
	runID, err := h.scheduler.StartMissionRun(workspaceID, MissionOriginManual)
	if writeMissionTriggerError(w, err) {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	if err := json.NewEncoder(w).Encode(map[string]any{
		"message":      "Mission run started",
		"workspace":    workspaceID,
		"run_id":       runID,
		"status":       "running",
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
	runID, err := h.scheduler.StartMissionRun(workspaceID, MissionOriginManual)
	if writeMissionTriggerError(w, err) {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	if err := json.NewEncoder(w).Encode(map[string]any{
		"message":      "Baseline mission run started",
		"workspace":    workspaceID,
		"run_id":       runID,
		"status":       "running",
		"is_baseline":  wasBaseline,
		"triggered_at": time.Now().UTC(),
	}); err != nil {
		logger.Error("encode baseline response", logger.Fields{"error": err})
	}
}

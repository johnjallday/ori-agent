package reaperhttp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/reaper"
)

const maxPlanEdits = 64

var (
	ErrPlanInvalid   = errors.New("REAPER track-edit plan is invalid")
	ErrPlanTooLarge  = errors.New("REAPER track-edit plan has too many edits")
	ErrPlanForbidden = errors.New("the assigned agent does not have reaper_live_control access")
)

// BulkEditRunner runs a whole guarded plan as one script inside one undo
// block and returns the receipt it wrote.
type BulkEditRunner interface {
	RunBulkPlan(context.Context, reaper.BulkPlan) (reaper.BulkReceipt, error)
}

// PendingPlan is the one proposed-but-not-applied plan for a workspace.
// Proposing never touches REAPER; only an explicit Apply does.
type PendingPlan struct {
	ID              string             `json:"id"`
	WorkspaceID     string             `json:"workspace_id"`
	Edits           []reaper.TrackEdit `json:"edits"`
	CreatedAt       time.Time          `json:"created_at"`
	agentInstanceID string
}

type planStore struct {
	mu      sync.Mutex
	entries map[string]PendingPlan
}

func newPlanStore() *planStore {
	return &planStore{entries: make(map[string]PendingPlan)}
}

// put replaces any existing pending plan for the workspace — "one proposed
// plan at a time" (PRD requirement 21 / task 5.2).
func (s *planStore) put(plan PendingPlan) {
	if s == nil || strings.TrimSpace(plan.WorkspaceID) == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[plan.WorkspaceID] = plan
}

func (s *planStore) get(workspaceID string) (PendingPlan, bool) {
	if s == nil {
		return PendingPlan{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	plan, found := s.entries[workspaceID]
	return plan, found
}

func (s *planStore) discard(workspaceID string) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, found := s.entries[workspaceID]
	if found {
		delete(s.entries, workspaceID)
	}
	return found
}

func planID() (string, error) {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "plan_" + hex.EncodeToString(buf), nil
}

// proposeEdits builds a BulkPlan from validated edits and stores it. It never
// contacts REAPER — a proposal only creates a pending plan for review.
func (h *Handler) proposeEdits(workspaceID, agentInstanceID string, edits []reaper.TrackEdit) (PendingPlan, error) {
	if h == nil || h.plans == nil {
		return PendingPlan{}, ErrPlanInvalid
	}
	if len(edits) == 0 || len(edits) > maxPlanEdits {
		return PendingPlan{}, ErrPlanTooLarge
	}
	for _, edit := range edits {
		if err := edit.Validate(); err != nil {
			return PendingPlan{}, ErrPlanInvalid
		}
	}
	id, err := planID()
	if err != nil {
		return PendingPlan{}, ErrPlanInvalid
	}
	plan := PendingPlan{
		ID: id, WorkspaceID: strings.TrimSpace(workspaceID), agentInstanceID: strings.TrimSpace(agentInstanceID),
		Edits: edits, CreatedAt: time.Now().UTC(),
	}
	h.plans.put(plan)
	return plan, nil
}

// --- HTTP surface -----------------------------------------------------------

func (h *Handler) GetPendingPlan(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.resolveWorkspace(w, r)
	if !ok {
		return
	}
	plan, found := h.plans.get(ws.ID)
	if !found {
		_ = orihttp.RespondSuccess(w, map[string]any{"plan": nil})
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"plan": plan})
}

// PlanApplyResponse mirrors TrackEditResponse: fresh live state plus the
// outcome, with the same no-transport-detail boundary rule.
type PlanApplyResponse struct {
	reaper.State
	Outcome       string      `json:"outcome"`
	Code          string      `json:"code,omitempty"`
	ErrorReason   string      `json:"error_reason,omitempty"`
	AppliedCount  int         `json:"applied_count,omitempty"`
	FailedIndices []int       `json:"failed_indices,omitempty"`
	Undo          *UndoAction `json:"undo,omitempty"`
}

func (h *Handler) ApplyPendingPlan(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.resolveWorkspace(w, r)
	if !ok {
		return
	}
	var request struct {
		Confirmed bool `json:"confirmed"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		decoder := json.NewDecoder(io.LimitReader(r.Body, maxTrackEditBody))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			_ = orihttp.RespondBadRequest(w, "invalid plan apply request")
			return
		}
	}
	response, status := h.applyPendingPlan(r.Context(), ws.ID, request.Confirmed)
	_ = orihttp.RespondJSON(w, status, response)
}

func (h *Handler) applyPendingPlan(ctx context.Context, workspaceID string, confirmed bool) (PlanApplyResponse, int) {
	// Bulk plans are Tier 2: applying always requires the user's explicit
	// action (PRD requirement 27). An agent cannot set confirmed itself —
	// propose_reaper_track_edits has no apply path at all.
	if !confirmed {
		return PlanApplyResponse{Outcome: "error", Code: "confirmation_required",
			ErrorReason: "Confirm this plan before applying it."}, http.StatusConflict
	}
	plan, found := h.plans.get(workspaceID)
	if !found {
		return PlanApplyResponse{Outcome: "error", Code: "no_pending_plan",
			ErrorReason: "There is no plan to apply."}, http.StatusConflict
	}
	project, applies, err := h.projectSource(workspaceID)
	if err != nil {
		return PlanApplyResponse{Outcome: "error", Code: "reaper_unavailable",
			ErrorReason: "Live REAPER control is not available for this workspace."}, http.StatusServiceUnavailable
	}
	if !applies {
		return PlanApplyResponse{Outcome: "error", Code: "reaper_not_selected",
			ErrorReason: "Live REAPER control is not selected for this workspace."}, http.StatusConflict
	}
	if h.bulkRunner == nil {
		return PlanApplyResponse{Outcome: "error", Code: "reaper_runner_unavailable",
			ErrorReason: "Track editing is unavailable until the REAPER runner is installed."}, http.StatusServiceUnavailable
	}

	response := PlanApplyResponse{Outcome: "error"}
	if planHasMove(plan) {
		state, stateErr := h.client.ReadState(ctx, project)
		if stateErr != nil {
			response.Code = "reaper_state_failed"
			response.ErrorReason = "Live REAPER state could not be read."
			return response, http.StatusBadGateway
		}
		state.Applies = true
		state.TrackEditingAvailable = true
		response.State = state
		for _, edit := range plan.Edits {
			if edit.Kind != reaper.TrackEditMove {
				continue
			}
			if preflightErr := reaper.ValidateTrackMove(state, edit); preflightErr != nil {
				switch {
				case errors.Is(preflightErr, reaper.ErrFolderParentMoveUnsupported):
					response.Code = folderParentMoveCode
					response.ErrorReason = folderParentMoveReason
				case errors.Is(preflightErr, reaper.ErrFolderDepthUnavailable):
					response.Code = folderDepthMissingCode
					response.ErrorReason = folderDepthMissingReason
				default:
					response.Code = "plan_guard_failed"
					response.ErrorReason = "The track list changed — nothing was applied."
					response.FailedIndices = []int{edit.Index}
				}
				return response, http.StatusConflict
			}
		}
	}

	receipt, runErr := h.bulkRunner.RunBulkPlan(ctx, reaper.BulkPlan{Edits: plan.Edits})
	if runErr != nil {
		return h.planApplyFailure(ctx, project, response, runErr)
	}

	state, stateErr := h.client.ReadState(ctx, project)
	if stateErr == nil {
		state.Applies = true
		state.TrackEditingAvailable = true
		response.State = state
	}

	if !receipt.Applied {
		// All-or-nothing: nothing applied. The plan stays pending, discard is
		// still available, and the console re-reads live state so the user
		// sees what actually changed underneath the plan.
		if receipt.Refusal == "folder_parent" {
			response.Code = folderParentMoveCode
			response.ErrorReason = folderParentMoveReason
		} else {
			response.Code = "plan_guard_failed"
			response.ErrorReason = "The track list changed — nothing was applied."
		}
		response.FailedIndices = receipt.FailedIndices
		return response, http.StatusConflict
	}

	h.plans.discard(workspaceID)
	response.Outcome = "ok"
	response.AppliedCount = receipt.AppliedCount
	// One toast for the whole plan, whose Undo fires REAPER's global undo —
	// correct because the plan already ran inside one undo block (PRD
	// requirement 25 / 4.3 item 16).
	response.Undo = &UndoAction{
		Summary:   "Applied " + strconv.Itoa(receipt.AppliedCount) + " changes",
		CommandID: undoCommandID,
	}
	return response, http.StatusOK
}

func planHasMove(plan PendingPlan) bool {
	for _, edit := range plan.Edits {
		if edit.Kind == reaper.TrackEditMove {
			return true
		}
	}
	return false
}

func (h *Handler) planApplyFailure(ctx context.Context, project reaper.ProjectSource, response PlanApplyResponse, runErr error) (PlanApplyResponse, int) {
	if state, err := h.client.ReadState(ctx, project); err == nil {
		state.Applies = true
		response.State = state
	}
	status := http.StatusBadGateway
	switch {
	case errors.Is(runErr, reaper.ErrActionDisconnected):
		status = http.StatusConflict
		response.Code = "reaper_disconnected"
		response.ErrorReason = "REAPER is not connected. Nothing was run."
	case errors.Is(runErr, reaper.ErrRunnerUnavailable):
		status = http.StatusServiceUnavailable
		response.Code = "reaper_runner_unavailable"
		response.ErrorReason = "Track editing is unavailable until the REAPER runner is installed."
	case errors.Is(runErr, reaper.ErrRunnerTimedOut):
		response.Code = "reaper_edit_timed_out"
		response.ErrorReason = "REAPER did not answer in time. Nothing was applied."
	default:
		response.Code = "reaper_edit_failed"
		response.ErrorReason = "REAPER did not apply the plan."
	}
	logger.Warn("Live REAPER plan apply failed", logger.Fields{"category": "reaper_plan_apply_failed"})
	return response, status
}

func (h *Handler) CancelPendingPlan(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.resolveWorkspace(w, r)
	if !ok {
		return
	}
	// Cancel makes no REAPER contact at all (PRD requirement 26).
	if !h.plans.discard(ws.ID) {
		_ = orihttp.RespondAPIError(w, http.StatusConflict,
			orihttp.NewAPIError("no_pending_plan", "There is no plan to cancel."))
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"outcome": "cancelled"})
}

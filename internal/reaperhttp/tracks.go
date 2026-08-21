package reaperhttp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/reaper"
)

const maxTrackEditBody = 4 << 10

// TrackEditRunner executes one guarded single-track edit and returns the
// receipt the generated Lua wrote. The receipt is the authority on whether the
// project changed; the runner reports success whenever the script itself ran,
// including when the guard deliberately refused.
type TrackEditRunner interface {
	RunTrackEdit(context.Context, reaper.TrackEdit) (reaper.EditReceipt, error)
	Available(context.Context) bool
}

// TrackEditResponse mirrors ActionRunResponse: fresh live state plus the
// outcome the console renders. No port, endpoint, or filesystem path may
// appear in any field here.
type TrackEditResponse struct {
	reaper.State
	Outcome     string      `json:"outcome"`
	Code        string      `json:"code,omitempty"`
	ErrorReason string      `json:"error_reason,omitempty"`
	Undo        *UndoAction `json:"undo,omitempty"`
}

type renameTrackRequest struct {
	Name         string `json:"name"`
	ExpectedName string `json:"expected_name"`
}

type colorTrackRequest struct {
	Color        int64  `json:"color"`
	ExpectedName string `json:"expected_name"`
}

type toggleTrackRequest struct {
	Value        bool   `json:"value"`
	ExpectedName string `json:"expected_name"`
}

// undoRecord is the most recent single-track edit for one workspace, together
// with the specific inverse that reverses it. Per PRD requirement 19 these are
// in-memory and per-workspace; they need not survive a restart or a reload.
type undoRecord struct {
	inverse reaper.TrackEdit
	summary string
}

type undoStore struct {
	mu      sync.Mutex
	records map[string]undoRecord
}

func newUndoStore() *undoStore {
	return &undoStore{records: make(map[string]undoRecord)}
}

func (s *undoStore) put(workspaceID string, record undoRecord) {
	if s == nil || strings.TrimSpace(workspaceID) == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records[workspaceID] = record
}

// take removes and returns the record, so one toast's Undo can only ever fire
// once. A second click finds nothing rather than re-applying a stale inverse.
func (s *undoStore) take(workspaceID string) (undoRecord, bool) {
	if s == nil {
		return undoRecord{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, found := s.records[workspaceID]
	if found {
		delete(s.records, workspaceID)
	}
	return record, found
}

func (h *Handler) RenameTrack(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.resolveWorkspace(w, r)
	if !ok {
		return
	}
	index, ok := trackIndexFromPath(w, r)
	if !ok {
		return
	}
	request, ok := decodeRenameRequest(w, r)
	if !ok {
		return
	}
	response, status := h.renameTrack(r.Context(), ws.ID, index, request)
	_ = orihttp.RespondJSON(w, status, response)
}

func (h *Handler) renameTrack(ctx context.Context, workspaceID string, index int, request renameTrackRequest) (TrackEditResponse, int) {
	name := strings.TrimSpace(request.Name)
	if name == "" {
		return TrackEditResponse{
			Outcome: "error", Code: "invalid_track_name",
			ErrorReason: "A track name cannot be empty.",
		}, http.StatusBadRequest
	}
	return h.applyGuardedTrackEdit(ctx, workspaceID, reaper.RenameEdit(index, request.ExpectedName, name))
}

func (h *Handler) ColorTrack(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.resolveWorkspace(w, r)
	if !ok {
		return
	}
	index, ok := trackIndexFromPath(w, r)
	if !ok {
		return
	}
	var request colorTrackRequest
	if !decodeTrackEditRequest(w, r, &request) {
		return
	}
	edit := reaper.ColorEdit(index, request.ExpectedName, request.Color)
	response, status := h.applyGuardedTrackEdit(r.Context(), ws.ID, edit)
	_ = orihttp.RespondJSON(w, status, response)
}

func (h *Handler) MuteTrack(w http.ResponseWriter, r *http.Request) {
	h.toggleTrack(w, r, reaper.MuteEdit)
}

func (h *Handler) SoloTrack(w http.ResponseWriter, r *http.Request) {
	h.toggleTrack(w, r, reaper.SoloEdit)
}

func (h *Handler) ArmTrack(w http.ResponseWriter, r *http.Request) {
	h.toggleTrack(w, r, reaper.ArmEdit)
}

func (h *Handler) toggleTrack(w http.ResponseWriter, r *http.Request, build func(index int, expectedName string, value bool) reaper.TrackEdit) {
	ws, ok := h.resolveWorkspace(w, r)
	if !ok {
		return
	}
	index, ok := trackIndexFromPath(w, r)
	if !ok {
		return
	}
	var request toggleTrackRequest
	if !decodeTrackEditRequest(w, r, &request) {
		return
	}
	response, status := h.applyGuardedTrackEdit(r.Context(), ws.ID, build(index, request.ExpectedName, request.Value))
	_ = orihttp.RespondJSON(w, status, response)
}

// applyGuardedTrackEdit runs any single-track edit through the shared policy
// path with the standard guard vocabulary, then stores its undo record. Color
// and the three toggles all reuse this alongside rename.
func (h *Handler) applyGuardedTrackEdit(ctx context.Context, workspaceID string, edit reaper.TrackEdit) (TrackEditResponse, int) {
	return h.applyTrackEdit(ctx, workspaceID, trackEditPlan{
		edit:        edit,
		summary:     editSummary(edit),
		guardCode:   "track_list_changed",
		guardReason: "The track list changed — refreshed.",
		storeUndo:   true,
	})
}

func (h *Handler) UndoTrackEdit(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.resolveWorkspace(w, r)
	if !ok {
		return
	}
	response, status := h.undoTrackEdit(r.Context(), ws.ID)
	_ = orihttp.RespondJSON(w, status, response)
}

func (h *Handler) undoTrackEdit(ctx context.Context, workspaceID string) (TrackEditResponse, int) {
	record, found := h.undos.take(workspaceID)
	if !found {
		return TrackEditResponse{
			Outcome: "error", Code: "nothing_to_undo",
			ErrorReason: "There is nothing left to undo.",
		}, http.StatusConflict
	}
	// The inverse is guarded on the value Ori wrote, so a track the user
	// changed in REAPER in the meantime is refused rather than clobbered.
	// storeUndo stays false: undoing is not itself an undoable step, so one
	// toast's Undo fires exactly once (PRD non-goal: no deeper undo stack).
	return h.applyTrackEdit(ctx, workspaceID, trackEditPlan{
		edit:        record.inverse,
		summary:     record.summary,
		guardCode:   "track_changed_in_reaper",
		guardReason: "That track changed in REAPER — nothing was undone.",
		storeUndo:   false,
	})
}

// trackEditPlan is one guarded mutation plus the words and policy that go with
// it, so every operation runs the identical path.
type trackEditPlan struct {
	edit        reaper.TrackEdit
	summary     string
	guardCode   string
	guardReason string
	storeUndo   bool
}

// applyTrackEdit is the one path every single-track mutation runs through, so
// the guard, the receipt, the undo record, and the error vocabulary can never
// disagree between operations.
func (h *Handler) applyTrackEdit(ctx context.Context, workspaceID string, plan trackEditPlan) (TrackEditResponse, int) {
	edit := plan.edit
	response := TrackEditResponse{Outcome: "error"}
	if err := edit.Validate(); err != nil {
		response.Code = "invalid_track_edit"
		response.ErrorReason = "That track edit is not valid."
		return response, http.StatusBadRequest
	}
	project, applies, err := h.projectSource(workspaceID)
	if err != nil {
		response.Code = "reaper_unavailable"
		response.ErrorReason = "Live REAPER control is not available for this workspace."
		return response, http.StatusServiceUnavailable
	}
	if !applies {
		response.Code = "reaper_not_selected"
		response.ErrorReason = "Live REAPER control is not selected for this workspace."
		return response, http.StatusConflict
	}
	if h.trackRunner == nil {
		response.Code = "reaper_runner_unavailable"
		response.ErrorReason = "Track editing is unavailable until the REAPER runner is installed."
		return response, http.StatusServiceUnavailable
	}

	receipt, runErr := h.trackRunner.RunTrackEdit(ctx, edit)
	if runErr != nil {
		return h.trackEditFailure(ctx, project, response, runErr)
	}

	state, stateErr := h.client.ReadState(ctx, project)
	if stateErr == nil {
		state.Applies = true
		state.TrackEditingAvailable = true
		response.State = state
	}

	if !receipt.Applied {
		// The guard refused. Nothing was written, and the console re-reads
		// live state so the user sees what is actually there now.
		response.Code = plan.guardCode
		response.ErrorReason = plan.guardReason
		return response, http.StatusConflict
	}

	response.Outcome = "ok"
	if plan.storeUndo {
		inverse := edit.Inverse(receipt.Prior)
		h.undos.put(workspaceID, undoRecord{inverse: inverse, summary: editSummary(inverse)})
		// A single-track edit reverses through its own specific inverse
		// endpoint, not through REAPER's global undo, so CommandID stays empty.
		response.Undo = &UndoAction{Summary: plan.summary}
	}
	return response, http.StatusOK
}

func (h *Handler) trackEditFailure(
	ctx context.Context,
	project reaper.ProjectSource,
	response TrackEditResponse,
	runErr error,
) (TrackEditResponse, int) {
	// Report live truth alongside the failure so the console can revert its
	// optimistic update against what REAPER actually holds.
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
	case errors.Is(runErr, reaper.ErrInvalidTrackEdit):
		status = http.StatusBadRequest
		response.Code = "invalid_track_edit"
		response.ErrorReason = "That track edit is not valid."
	default:
		response.Code = "reaper_edit_failed"
		response.ErrorReason = "REAPER did not apply the change."
	}
	logger.Warn("Live REAPER track edit failed", logger.Fields{"category": "reaper_track_edit_failed"})
	return response, status
}

// editSummary names what an edit does in the user's own words for the toast.
// It is used for both directions: the forward edit's toast, and — applied to
// the inverse — the undo record stored for the next Undo click. Deriving both
// from the same function keeps a color or toggle summary correct without
// string surgery on the forward copy.
func editSummary(edit reaper.TrackEdit) string {
	switch edit.Kind {
	case reaper.TrackEditRename:
		return "Renamed " + quoteForUser(edit.ExpectedName) + " to " + quoteForUser(edit.NewName)
	case reaper.TrackEditColor:
		return "Recolored " + quoteForUser(edit.ExpectedName)
	case reaper.TrackEditMute:
		if edit.NewBool {
			return "Muted " + quoteForUser(edit.ExpectedName)
		}
		return "Unmuted " + quoteForUser(edit.ExpectedName)
	case reaper.TrackEditSolo:
		if edit.NewBool {
			return "Soloed " + quoteForUser(edit.ExpectedName)
		}
		return "Cleared solo on " + quoteForUser(edit.ExpectedName)
	case reaper.TrackEditArm:
		if edit.NewBool {
			return "Armed " + quoteForUser(edit.ExpectedName)
		}
		return "Disarmed " + quoteForUser(edit.ExpectedName)
	default:
		return "Changed " + quoteForUser(edit.ExpectedName)
	}
}

// quoteForUser renders a track name the way the toast reads it aloud, naming
// an unnamed track rather than showing empty quotes.
func quoteForUser(name string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "the untitled track"
	}
	return "‘" + trimmed + "’"
}

func trackIndexFromPath(w http.ResponseWriter, r *http.Request) (int, bool) {
	raw := strings.TrimSpace(r.PathValue("index"))
	index, err := strconv.Atoi(raw)
	if err != nil || index < 1 || index > 10000 {
		_ = orihttp.RespondBadRequest(w, "invalid track index")
		return 0, false
	}
	return index, true
}

func decodeRenameRequest(w http.ResponseWriter, r *http.Request) (renameTrackRequest, bool) {
	var request renameTrackRequest
	ok := decodeTrackEditRequest(w, r, &request)
	return request, ok
}

// decodeTrackEditRequest is the one strict-JSON decode every track-edit
// endpoint uses: a body is required, unknown fields are rejected, and
// trailing content after the object is rejected too.
func decodeTrackEditRequest(w http.ResponseWriter, r *http.Request, out any) bool {
	if r.Body == nil || r.ContentLength == 0 {
		_ = orihttp.RespondBadRequest(w, "invalid track edit request")
		return false
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, maxTrackEditBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		_ = orihttp.RespondBadRequest(w, "invalid track edit request")
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		_ = orihttp.RespondBadRequest(w, "invalid track edit request")
		return false
	}
	return true
}

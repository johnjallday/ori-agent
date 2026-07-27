package downloadsjanitorhttp

import (
	"errors"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/johnjallday/ori-agent/internal/downloadsjanitor"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/userprofile"
)

// Pagination bounds for batch listing. A stable default and a hard cap keep one
// request from having to serialize an unbounded history.
const (
	defaultBatchLimit = 20
	maxBatchLimit     = 100
)

// candidateDTO is the review-surface shape of a candidate.
//
// It carries no filesystem path. Destination is a display label relative to the
// configured folder ("Filed/Documents"), never an absolute path — clients have
// no use for one, and absolute paths do not belong in API responses or logs
// (FR-110). What a client sends back is the candidate ID and a category ID.
type candidateDTO struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Extension   string `json:"extension,omitempty"`
	MIMEType    string `json:"mime_type,omitempty"`
	Size        int64  `json:"size"`
	ModifiedAt  string `json:"modified_at,omitempty"`
	Category    string `json:"category,omitempty"`
	Destination string `json:"destination,omitempty"`
	Reason      string `json:"reason,omitempty"`
	Confidence  string `json:"confidence,omitempty"`
	Classifier  string `json:"classifier,omitempty"`
	NeedsReview bool   `json:"needs_review,omitempty"`
	State       string `json:"state"`
	StateReason string `json:"state_reason,omitempty"`
	// Decision is the user's recorded choice, distinct from the proposed
	// Category above.
	Decision         string `json:"decision,omitempty"`
	DecisionCategory string `json:"decision_category,omitempty"`
}

func toCandidateDTO(candidate downloadsjanitor.JanitorCandidate, filingRootName string) candidateDTO {
	dto := candidateDTO{
		ID:               candidate.ID,
		Name:             candidate.Display(),
		Extension:        candidate.Extension,
		MIMEType:         candidate.MIMEType,
		Size:             candidate.Size,
		Category:         string(candidate.Category),
		Reason:           candidate.Reason,
		Confidence:       string(candidate.Confidence),
		Classifier:       string(candidate.Classifier),
		NeedsReview:      candidate.NeedsReview,
		State:            string(candidate.State),
		StateReason:      candidate.StateReason,
		Decision:         string(candidate.Decision),
		DecisionCategory: string(candidate.DecisionCategory),
	}
	if !candidate.ModifiedAt.IsZero() {
		dto.ModifiedAt = candidate.ModifiedAt.UTC().Format("2006-01-02T15:04:05Z07:00")
	}
	if definition, err := downloadsjanitor.LookupCategory(string(candidate.EffectiveCategory())); err == nil {
		if filingRootName == "" {
			filingRootName = downloadsjanitor.DefaultFilingRootName
		}
		dto.Destination = path.Join(filingRootName, definition.FolderName)
	}
	return dto
}

// ListBatches handles GET /api/workspaces/{workspaceID}/downloads-janitor/batches.
// Supported query parameters: state (pending|partially_applied|resolved),
// limit, offset.
func (h *Handler) ListBatches(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := h.resolveWorkspace(w, r)
	if !ok {
		return
	}
	batches, err := h.service.ListBatches(workspaceID)
	if err != nil {
		h.respondError(w, err, "Failed to list Downloads Janitor batches")
		return
	}

	if state := strings.TrimSpace(r.URL.Query().Get("state")); state != "" {
		filtered := batches[:0:0]
		for _, batch := range batches {
			if string(batch.State) == state {
				filtered = append(filtered, batch)
			}
		}
		batches = filtered
	}

	total := len(batches)
	limit, offset := pagination(r)
	if offset > total {
		offset = total
	}
	end := min(offset+limit, total)

	_ = orihttp.RespondSuccess(w, map[string]any{
		"success": true,
		"batches": batches[offset:end],
		"total":   total,
		"limit":   limit,
		"offset":  offset,
	})
}

func pagination(r *http.Request) (limit, offset int) {
	limit = defaultBatchLimit
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			limit = min(parsed, maxBatchLimit)
		}
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("offset")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			offset = parsed
		}
	}
	return limit, offset
}

// GetBatch handles GET /api/workspaces/{workspaceID}/downloads-janitor/batches/{batchID}.
// The literal batch ID "latest" resolves to the newest batch still awaiting the
// user, which is what the review surface opens on.
func (h *Handler) GetBatch(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := h.resolveWorkspace(w, r)
	if !ok {
		return
	}
	settings, err := h.service.Status(workspaceID)
	if err != nil {
		h.respondError(w, err, "Failed to read Downloads Janitor status")
		return
	}

	batchID := strings.TrimSpace(r.PathValue("batchID"))
	var (
		batch      downloadsjanitor.JanitorBatch
		candidates []downloadsjanitor.JanitorCandidate
	)
	if batchID == "latest" {
		var found bool
		batch, candidates, found, err = h.service.LatestPendingBatch(workspaceID)
		if err != nil {
			h.respondError(w, err, "Failed to read the Downloads Janitor batch")
			return
		}
		if !found {
			// No pending work is a normal, empty state — not an error.
			_ = orihttp.RespondSuccess(w, map[string]any{"success": true, "batch": nil, "candidates": []candidateDTO{}})
			return
		}
	} else {
		batch, candidates, err = h.service.BatchDetail(workspaceID, batchID)
		if err != nil {
			if errors.Is(err, downloadsjanitor.ErrBatchNotFound) {
				_ = orihttp.RespondNotFound(w, "batch not found")
				return
			}
			h.respondError(w, err, "Failed to read the Downloads Janitor batch")
			return
		}
	}

	_ = orihttp.RespondSuccess(w, map[string]any{
		"success":    true,
		"batch":      batch,
		"candidates": candidateDTOs(candidates, settings.Settings.FilingRootName),
	})
}

func candidateDTOs(candidates []downloadsjanitor.JanitorCandidate, filingRootName string) []candidateDTO {
	out := make([]candidateDTO, 0, len(candidates))
	for _, candidate := range candidates {
		out = append(out, toCandidateDTO(candidate, filingRootName))
	}
	return out
}

// TestScan handles POST /api/workspaces/{workspaceID}/downloads-janitor/test-scan:
// a harmless check that reports what a scan would consider and creates nothing.
func (h *Handler) TestScan(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := h.resolveWorkspace(w, r)
	if !ok {
		return
	}
	report, err := h.service.TestScan(workspaceID)
	if err != nil {
		h.respondError(w, err, "Failed to run the Downloads Janitor test scan")
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"success": true, "report": report})
}

// ScanNow handles POST /api/workspaces/{workspaceID}/downloads-janitor/scan:
// a real scan that persists one reviewable batch. A scan that finds nothing new
// creates no batch and says so, rather than returning an empty one.
func (h *Handler) ScanNow(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := h.resolveWorkspace(w, r)
	if !ok {
		return
	}
	batch, created, err := h.service.ScanNow(workspaceID, downloadsjanitor.ScanSourceManual)
	if err != nil {
		h.respondError(w, err, "Failed to run the Downloads Janitor scan")
		return
	}
	response := map[string]any{"success": true, "created": created}
	if created {
		response["batch"] = batch
	}
	_ = orihttp.RespondSuccess(w, response)
}

// UpdateDecisions handles POST
// /api/workspaces/{workspaceID}/downloads-janitor/decisions.
//
// The request carries candidate IDs, an operation, and allowlisted category
// IDs. It deliberately accepts no path of any kind: the server already knows
// where every candidate is, and a client that could name a source or
// destination would be a client that could redirect a move.
//
// Recording a decision changes no file. Applying decisions is a separate,
// explicitly approved step.
func (h *Handler) UpdateDecisions(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := h.resolveWorkspace(w, r)
	if !ok {
		return
	}
	var req struct {
		Decisions []struct {
			CandidateID string `json:"candidate_id"`
			Decision    string `json:"decision"`
			Category    string `json:"category"`
		} `json:"decisions"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	if len(req.Decisions) == 0 {
		_ = orihttp.RespondBadRequest(w, "at least one decision is required")
		return
	}

	updates := make([]downloadsjanitor.DecisionUpdate, 0, len(req.Decisions))
	for _, decision := range req.Decisions {
		normalized := downloadsjanitor.Decision(strings.ToLower(strings.TrimSpace(decision.Decision)))
		switch normalized {
		case downloadsjanitor.DecisionNone, downloadsjanitor.DecisionMove, downloadsjanitor.DecisionSkip:
			// Supported here. Trash is added with its own confirmation path.
		default:
			_ = orihttp.RespondBadRequest(w, "unsupported decision: "+decision.Decision)
			return
		}
		updates = append(updates, downloadsjanitor.DecisionUpdate{
			CandidateID: decision.CandidateID,
			Decision:    normalized,
			Category:    decision.Category,
		})
	}

	changed, err := h.service.ApplyDecisions(workspaceID, updates)
	if err != nil {
		switch {
		case errors.Is(err, downloadsjanitor.ErrCandidateNotFound):
			_ = orihttp.RespondNotFound(w, "candidate not found")
		case errors.Is(err, downloadsjanitor.ErrUnknownCategory):
			_ = orihttp.RespondBadRequest(w, err.Error())
		default:
			h.respondError(w, err, "Failed to record Downloads Janitor decisions")
		}
		return
	}

	status, err := h.service.Status(workspaceID)
	filingRoot := downloadsjanitor.DefaultFilingRootName
	if err == nil {
		filingRoot = status.Settings.FilingRootName
	}
	_ = orihttp.RespondSuccess(w, map[string]any{
		"success":    true,
		"candidates": candidateDTOs(changed, filingRoot),
	})
}

// ResetSkipped handles POST
// /api/workspaces/{workspaceID}/downloads-janitor/skipped/reset. With no key it
// clears every skip, so previously dismissed files can be proposed again.
func (h *Handler) ResetSkipped(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := h.resolveWorkspace(w, r)
	if !ok {
		return
	}
	var req struct {
		Key string `json:"key"`
	}
	// A body is optional here: no body means reset everything.
	if r.ContentLength > 0 && !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	if err := h.service.ResetSkipped(workspaceID, req.Key); err != nil {
		h.respondError(w, err, "Failed to reset skipped Downloads Janitor items")
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"success": true})
}

// Categories handles GET /api/workspaces/{workspaceID}/downloads-janitor/categories:
// the fixed set a client may choose from. Serving it keeps the review UI's
// category picker in step with the server's allowlist instead of hard-coding a
// copy that could drift.
func (h *Handler) Categories(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.resolveWorkspace(w, r); !ok {
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{
		"success":    true,
		"categories": downloadsjanitor.CategoryRegistry,
	})
}

// ------------------------------------------------------- preview and confirm

// decisionPayload is the shared request shape for preview and confirm. Note
// what is absent: no source path, no destination, no filename. The server knows
// where every candidate is, and a client able to name a path would be a client
// able to redirect a move.
type decisionPayload struct {
	CandidateID string `json:"candidate_id"`
	Operation   string `json:"operation"`
	Category    string `json:"category"`
}

func (h *Handler) toPreviewItems(payload []decisionPayload) ([]downloadsjanitor.PreviewRequestItem, error) {
	items := make([]downloadsjanitor.PreviewRequestItem, 0, len(payload))
	for _, entry := range payload {
		operation := downloadsjanitor.Operation(strings.ToLower(strings.TrimSpace(entry.Operation)))
		if operation == "" {
			operation = downloadsjanitor.OperationMove
		}
		if operation != downloadsjanitor.OperationMove && operation != downloadsjanitor.OperationTrash {
			// Only the two real operations exist. Anything else is rejected
			// rather than interpreted — there is no permanent delete to reach.
			return nil, errors.New("unsupported operation: " + entry.Operation)
		}
		items = append(items, downloadsjanitor.PreviewRequestItem{
			CandidateID: entry.CandidateID,
			Operation:   operation,
			Category:    entry.Category,
		})
	}
	return items, nil
}

// PreviewMoves handles POST
// /api/workspaces/{workspaceID}/downloads-janitor/preview: the final,
// server-derived plan plus a single-use approval bound to exactly it.
func (h *Handler) PreviewMoves(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := h.resolveWorkspace(w, r)
	if !ok {
		return
	}
	var req struct {
		Decisions []decisionPayload `json:"decisions"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	if len(req.Decisions) == 0 {
		_ = orihttp.RespondBadRequest(w, "select at least one file")
		return
	}
	items, err := h.toPreviewItems(req.Decisions)
	if err != nil {
		_ = orihttp.RespondBadRequest(w, err.Error())
		return
	}
	userID, ok := h.currentUser(w, r)
	if !ok {
		return
	}

	preview, err := h.service.PreviewMoves(downloadsjanitor.PreviewRequest{
		WorkspaceID: workspaceID, UserID: userID, Items: items,
	})
	if err != nil {
		h.respondReviewError(w, err, "Failed to prepare the Downloads Janitor preview")
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"success": true, "preview": preview})
}

// ConfirmMoves handles POST
// /api/workspaces/{workspaceID}/downloads-janitor/apply: spend the approval and
// apply the plan, reporting one outcome per file.
func (h *Handler) ConfirmMoves(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := h.resolveWorkspace(w, r)
	if !ok {
		return
	}
	var req struct {
		BatchID   string            `json:"batch_id"`
		Token     string            `json:"approval_token"`
		Decisions []decisionPayload `json:"decisions"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	items, err := h.toPreviewItems(req.Decisions)
	if err != nil {
		_ = orihttp.RespondBadRequest(w, err.Error())
		return
	}
	userID, ok := h.currentUser(w, r)
	if !ok {
		return
	}

	result, err := h.service.ConfirmMoves(r.Context(), downloadsjanitor.ConfirmRequest{
		WorkspaceID: workspaceID, UserID: userID, BatchID: req.BatchID,
		Token: req.Token, Items: items,
	})
	if err != nil {
		h.respondReviewError(w, err, "Failed to apply the approved Downloads Janitor moves")
		return
	}
	// A mixed result is a normal outcome, not an error: the response reports
	// per-file results and the caller states them plainly.
	_ = orihttp.RespondSuccess(w, map[string]any{"success": true, "result": result})
}

// Undo handles POST
// /api/workspaces/{workspaceID}/downloads-janitor/history/{actionID}/undo:
// put a moved file back, or restore a trashed one.
func (h *Handler) Undo(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := h.resolveWorkspace(w, r)
	if !ok {
		return
	}
	userID, ok := h.currentUser(w, r)
	if !ok {
		return
	}
	actionID := strings.TrimSpace(r.PathValue("actionID"))
	result, err := h.service.Undo(r.Context(), workspaceID, actionID, userID)
	if err != nil {
		if errors.Is(err, downloadsjanitor.ErrUndoUnavailable) {
			_ = orihttp.RespondAPIError(w, http.StatusConflict,
				orihttp.NewAPIError("undo_unavailable", err.Error()))
			return
		}
		h.respondReviewError(w, err, "Failed to undo the Downloads Janitor action")
		return
	}
	// A refused undo is not an error response: the request was fine, the file
	// simply could not be put back, and the reason belongs in the result.
	_ = orihttp.RespondSuccess(w, map[string]any{"success": true, "undo": result})
}

// History handles GET /api/workspaces/{workspaceID}/downloads-janitor/history.
func (h *Handler) History(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := h.resolveWorkspace(w, r)
	if !ok {
		return
	}
	actions, err := h.service.ListActions(workspaceID)
	if err != nil {
		h.respondError(w, err, "Failed to read Downloads Janitor history")
		return
	}
	// Filters: operation (move|trash) and result (applied|failed|stale), plus
	// undoable=true for "what can I still get back".
	if operation := strings.TrimSpace(r.URL.Query().Get("operation")); operation != "" {
		filtered := actions[:0:0]
		for _, action := range actions {
			if string(action.Operation) == operation {
				filtered = append(filtered, action)
			}
		}
		actions = filtered
	}
	if result := strings.TrimSpace(r.URL.Query().Get("result")); result != "" {
		filtered := actions[:0:0]
		for _, action := range actions {
			if string(action.Result) == result {
				filtered = append(filtered, action)
			}
		}
		actions = filtered
	}
	if strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("undoable")), "true") {
		filtered := actions[:0:0]
		for _, action := range actions {
			if action.Undoable() {
				filtered = append(filtered, action)
			}
		}
		actions = filtered
	}
	total := len(actions)
	limit, offset := pagination(r)
	if offset > total {
		offset = total
	}
	end := min(offset+limit, total)
	_ = orihttp.RespondSuccess(w, map[string]any{
		"success": true,
		"actions": actions[offset:end],
		"total":   total,
		"limit":   limit,
		"offset":  offset,
	})
}

// currentUser resolves the requesting user, whom an approval is bound to.
func (h *Handler) currentUser(w http.ResponseWriter, r *http.Request) (string, bool) {
	userID, err := h.provider.CurrentUserID(r.Context())
	if err != nil {
		_ = orihttp.RespondInternalError(w, "Failed to resolve the current user")
		return "", false
	}
	if strings.TrimSpace(userID) == "" {
		userID = userprofile.LocalUserID
	}
	return userID, true
}

// respondReviewError maps approval and candidate errors onto stable statuses.
// An approval problem is a 409: the request was well formed, but the state it
// assumed no longer holds.
func (h *Handler) respondReviewError(w http.ResponseWriter, err error, fallback string) {
	switch {
	case errors.Is(err, downloadsjanitor.ErrApprovalRequired):
		_ = orihttp.RespondBadRequest(w, "an approval token is required")
	case errors.Is(err, downloadsjanitor.ErrApprovalConsumed):
		_ = orihttp.RespondAPIError(w, http.StatusConflict,
			orihttp.NewAPIError("approval_used", "That approval was already used."))
	case errors.Is(err, downloadsjanitor.ErrApprovalExpired):
		_ = orihttp.RespondAPIError(w, http.StatusConflict,
			orihttp.NewAPIError("approval_expired", "That approval has expired. Review the files and approve again."))
	case errors.Is(err, downloadsjanitor.ErrApprovalInvalid):
		_ = orihttp.RespondAPIError(w, http.StatusConflict,
			orihttp.NewAPIError("approval_invalid", "These files or categories changed since you approved them. Review them and approve again."))
	case errors.Is(err, downloadsjanitor.ErrCandidateNotActionable):
		_ = orihttp.RespondAPIError(w, http.StatusConflict,
			orihttp.NewAPIError("candidate_changed", err.Error()))
	case errors.Is(err, downloadsjanitor.ErrCandidateNotFound):
		_ = orihttp.RespondNotFound(w, "candidate not found")
	case errors.Is(err, downloadsjanitor.ErrUnknownCategory), errors.Is(err, downloadsjanitor.ErrInvalidAction):
		_ = orihttp.RespondBadRequest(w, err.Error())
	default:
		h.respondError(w, err, fallback)
	}
}

// ------------------------------------------------------------------ settings

// UpdateSettings handles PATCH
// /api/workspaces/{workspaceID}/downloads-janitor/settings. Every field is
// optional: an absent field is left alone rather than reset, so a client that
// sends one setting cannot silently clear another.
func (h *Handler) UpdateSettings(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := h.resolveWorkspace(w, r)
	if !ok {
		return
	}
	var req struct {
		DailyScanLocalTime *string `json:"daily_scan_local_time"`
		Timezone           *string `json:"timezone"`
		ContentMode        *string `json:"content_mode"`
		ContentProvider    *string `json:"content_provider"`
		Paused             *bool   `json:"paused"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	update := downloadsjanitor.SettingsUpdate{
		DailyScanLocalTime: req.DailyScanLocalTime,
		Timezone:           req.Timezone,
		ContentProvider:    req.ContentProvider,
		Paused:             req.Paused,
	}
	if req.ContentMode != nil {
		mode := downloadsjanitor.ContentMode(*req.ContentMode)
		update.ContentMode = &mode
	}

	status, err := h.service.UpdateSettings(workspaceID, update)
	if err != nil {
		h.respondError(w, err, "Failed to update Downloads Janitor settings")
		return
	}
	status = h.syncAutomationAndRefresh(workspaceID, status)
	_ = orihttp.RespondSuccess(w, map[string]any{"success": true, "status": status})
}

// GrantConsent handles POST
// /api/workspaces/{workspaceID}/downloads-janitor/content-consent: the user's
// explicit confirmation that file content may go to a named provider.
//
// The provider is named in the request as well as in settings, so a consent
// recorded here is always consent to something the user could see.
func (h *Handler) GrantConsent(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := h.resolveWorkspace(w, r)
	if !ok {
		return
	}
	var req struct {
		Provider string `json:"provider"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	status, err := h.service.GrantContentConsent(workspaceID, req.Provider)
	if err != nil {
		h.respondError(w, err, "Failed to record your confirmation")
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"success": true, "status": status})
}

// Relink handles POST /api/workspaces/{workspaceID}/downloads-janitor/relink.
func (h *Handler) Relink(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := h.resolveWorkspace(w, r)
	if !ok {
		return
	}
	var req struct {
		Path string `json:"path"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	status, err := h.service.Relink(h.lifecycle(), downloadsjanitor.RelinkRequest{
		WorkspaceID: workspaceID, Path: req.Path,
	})
	if err != nil {
		h.respondError(w, err, "Failed to change the folder")
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"success": true, "status": status})
}

// RevokeAccess handles POST
// /api/workspaces/{workspaceID}/downloads-janitor/revoke: disconnect the folder
// entirely. History is kept; nothing further is scanned or acted on.
func (h *Handler) RevokeAccess(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := h.resolveWorkspace(w, r)
	if !ok {
		return
	}
	status, err := h.service.RevokeAccess(h.lifecycle(), workspaceID)
	if err != nil {
		h.respondError(w, err, "Failed to remove folder access")
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"success": true, "status": status})
}

// ListSkipped handles GET
// /api/workspaces/{workspaceID}/downloads-janitor/skipped.
func (h *Handler) ListSkipped(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := h.resolveWorkspace(w, r)
	if !ok {
		return
	}
	items, err := h.service.ListSkipped(workspaceID)
	if err != nil {
		h.respondError(w, err, "Failed to list skipped items")
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"success": true, "skipped": items})
}

// lifecycle returns the automation control for settings operations that must
// stop unattended work, or nil when automation is not wired.
func (h *Handler) lifecycle() downloadsjanitor.WatcherLifecycle {
	if h == nil || h.automation == nil {
		return nil
	}
	if lifecycle, ok := h.automation.(downloadsjanitor.WatcherLifecycle); ok {
		return lifecycle
	}
	return nil
}

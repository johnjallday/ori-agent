package downloadsjanitorhttp

import (
	"errors"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/johnjallday/ori-agent/internal/downloadsjanitor"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
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
		Name:             candidate.Name,
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

package personalassistanthttp

import (
	"errors"
	"io"
	"net/http"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

func (h *Handler) SetKnowledgeInterview(service *personalassistant.KnowledgeInterviewService) {
	if h != nil {
		h.interview = service
	}
}

// GetKnowledgeInterview is a pure post-HQ read: it does not offer an interview
// or save drafts. Only current deterministic questions, status and a bounded
// profile-preference snapshot are returned for exact user review.
func (h *Handler) GetKnowledgeInterview(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodGet) {
		return
	}
	if h == nil || h.interview == nil || h.provider == nil {
		orihttp.ServiceUnavailable(w, "Personal HQ interview is unavailable")
		return
	}
	userID, ok := h.currentUserID(w, r)
	if !ok {
		return
	}
	questions, err := h.interview.Questions(r.Context(), userID)
	if err != nil {
		writeKnowledgeReviewError(w, err)
		return
	}
	status, err := h.interview.Read(r.Context(), userID)
	if err != nil {
		writeKnowledgeReviewError(w, err)
		return
	}
	version, err := h.interview.StateVersion(r.Context(), userID)
	if err != nil {
		writeKnowledgeReviewError(w, err)
		return
	}
	// An empty/missing profile cannot be misrepresented as an approved global
	// preference. The UI may still save selected statements in HQ instead.
	profile, _ := h.interview.ProfileSnapshot(r.Context(), userID)
	answer := map[string]any{"questions": questions, "status": "not_offered", "state_version": version}
	if status != nil {
		answer["status"] = status.Status
		answer["saved_rows"] = interviewSavedRows(status)
	}
	if profile != nil {
		answer["profile"] = profile
	}
	orihttp.Success(w, answer)
}

func interviewSavedRows(interview *personalassistant.KnowledgeInterview) []string {
	rows := make([]string, 0)
	if interview == nil {
		return rows
	}
	for _, receipt := range interview.RowReceipts {
		if receipt.Status == "saved" {
			rows = append(rows, receipt.RowID)
		}
	}
	return rows
}

func (h *Handler) DeferKnowledgeInterview(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	if h == nil || h.interview == nil || h.provider == nil {
		orihttp.ServiceUnavailable(w, "Personal HQ interview is unavailable")
		return
	}
	if r.Body != nil {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 32))
		if err != nil || strings.TrimSpace(string(body)) != "" {
			orihttp.BadRequest(w, "This action does not accept answers")
			return
		}
	}
	userID, ok := h.currentUserID(w, r)
	if !ok {
		return
	}
	status, err := h.interview.Defer(r.Context(), userID)
	if err != nil {
		writeKnowledgeReviewError(w, err)
		return
	}
	orihttp.Success(w, map[string]any{"status": status.Status})
}

func (h *Handler) SaveKnowledgeInterview(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	if h == nil || h.interview == nil || h.provider == nil {
		orihttp.ServiceUnavailable(w, "Personal HQ interview is unavailable")
		return
	}
	var body personalassistant.KnowledgeInterviewSaveRequest
	if err := decodeBoundedRequest(w, r, &body); err != nil || !validReviewRequestID(body.RequestID) || body.StateVersion < 1 {
		orihttp.BadRequest(w, "Review the exact selected answers before saving")
		return
	}
	userID, ok := h.currentUserID(w, r)
	if !ok {
		return
	}
	result, err := h.interview.SaveReviewed(r.Context(), userID, body)
	if err == nil {
		orihttp.Success(w, map[string]any{"interview": result})
		return
	}
	if len(result.SavedRows) > 0 {
		_ = orihttp.RespondJSON(w, http.StatusMultiStatus, map[string]any{
			"interview": result, "error": "Some answers were saved. Review the remaining rows before retrying this same action.",
		})
		return
	}
	if errors.Is(err, personalassistant.ErrValidation) {
		orihttp.BadRequest(w, "Select only safe, exact answers and destinations")
		return
	}
	writeKnowledgeReviewError(w, err)
}

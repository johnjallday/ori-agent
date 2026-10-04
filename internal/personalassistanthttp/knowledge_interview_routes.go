package personalassistanthttp

import (
	"context"
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

// GetKnowledgeInterview is a pure post-HQ read: it does not offer an interview,
// save drafts or scan a folder. It returns the current deterministic questions,
// the status and a bounded profile-preference snapshot for exact user review.
// When the user has already shown the assistant a folder it also returns
// `suggestion` (wording for question 1 from an offer still waiting on Home) or
// `remembered_project` (the project fact already approved from a folder).
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
	// Folders the user already showed the assistant can prefill question 1.
	// Both fields are optional: a failed read leaves them out, never the rest.
	if folders, err := h.interview.FolderSnapshot(r.Context(), userID); err == nil {
		if folders.Suggestion != nil {
			answer["suggestion"] = folders.Suggestion
		}
		if folders.RememberedProject != "" {
			answer["remembered_project"] = folders.RememberedProject
		}
	}
	orihttp.Success(w, answer)
}

// interviewNoSuggestionMessage is shown when a folder names no single project
// to propose. The wizard leaves the answer box as it was.
const interviewNoSuggestionMessage = "I couldn't tell what this folder is for. Type your answer instead."

// interviewRememberedMessage is shown when every project in the folder is
// already remembered from Home, so there is nothing new to propose.
const interviewRememberedMessage = "I already remember the project in this folder. Add anything that matters more right now, or skip."

// interviewScanBusyMessage is the wizard's wording for a scan that is still
// running. Every other scan refusal reads as it does on Home.
const interviewScanBusyMessage = "I'm still looking at the last folder. Try again in a moment."

// SuggestKnowledgeInterview proposes an answer to the interview's first
// question from a folder the user shows the assistant. The body is a folder
// scan's body (exactly one of chip, picker or file, never a path) and the scan
// is the one Home runs, so the offer it records is also waiting there. Only the
// proposed wording is returned; no fact is saved before the wizard's Save.
func (h *Handler) SuggestKnowledgeInterview(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	if h == nil || h.folderDigest == nil {
		orihttp.ServiceUnavailable(w, "Show me a folder is unavailable")
		return
	}
	req, ok := decodeFolderScanRequest(w, r)
	if !ok {
		return
	}
	userID, ok := h.currentUserID(w, r)
	if !ok {
		return
	}
	scanned, err := h.scanForInterview(r.Context(), userID, req)
	if errors.Is(err, personalassistant.ErrFolderScanBusy) {
		orihttp.Conflict(w, interviewScanBusyMessage)
		return
	}
	if err != nil {
		writeFolderDigestError(w, err)
		return
	}
	if scanned == nil {
		orihttp.Success(w, map[string]any{"cancelled": true})
		return
	}
	offer, err := h.folderDigest.StoredOffer(r.Context(), userID, scanned.ID)
	if errors.Is(err, personalassistant.ErrFolderOfferNotFound) {
		// The scan worked. An offer with nothing to decide (an empty folder) can
		// be pruned in the same write when the history is full: that is "nothing
		// to propose", not a failed look.
		orihttp.Success(w, map[string]any{"suggestion": nil, "message": interviewNoSuggestionMessage})
		return
	}
	if err != nil {
		writeFolderDigestError(w, err)
		return
	}
	// The interview service leaves out a project already remembered from a
	// folder; without it the offer is worded as it stands.
	var suggestion personalassistant.InterviewSuggestion
	var remembered bool
	if h.interview != nil {
		suggestion, ok, remembered = h.interview.SuggestionFromOffer(r.Context(), userID, offer)
	} else {
		suggestion, ok = personalassistant.InterviewSuggestionFromOffer(offer)
	}
	switch {
	case ok:
		orihttp.Success(w, map[string]any{"suggestion": suggestion})
	case remembered:
		orihttp.Success(w, map[string]any{"suggestion": nil, "message": interviewRememberedMessage})
	default:
		orihttp.Success(w, map[string]any{"suggestion": nil, "message": interviewNoSuggestionMessage})
	}
}

// scanForInterview runs the one scan the request names, through the same
// service calls Home uses. A cancelled dialog is a nil offer with no error.
func (h *Handler) scanForInterview(ctx context.Context, userID string, req folderScanRequest) (*personalassistant.FolderOfferView, error) {
	switch {
	case req.Picker:
		return h.folderDigest.ScanPicked(ctx, userID)
	case req.File:
		return h.folderDigest.ScanPickedFile(ctx, userID)
	}
	offer, err := h.folderDigest.ScanChip(ctx, userID, req.Chip)
	if err != nil {
		return nil, err
	}
	return &offer, nil
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

package filejanitorhttp

import (
	"errors"
	"net/http"
	"strings"

	"github.com/johnjallday/ori-agent/internal/filejanitor"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
)

// GetReviewGroups serves bounded cards for a single saved batch. It must never
// call Status/readiness or write a draft simply because review was opened.
func (h *Handler) GetReviewGroups(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	workspaceID, ok := h.resolveWorkspace(w, r)
	if !ok {
		return
	}
	groups, ok := h.readReviewGroups(w, r, workspaceID)
	if !ok {
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"success": true, "review": groups})
}

type reviewGroupMemberDTO struct {
	candidateDTO
	Cautions []string `json:"cautions,omitempty"`
}

// GetReviewGroupMembers retains candidate paging bounds even when a group is
// larger than 200. Card counts and future group selections are never page-sized.
func (h *Handler) GetReviewGroupMembers(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	workspaceID, ok := h.resolveWorkspace(w, r)
	if !ok {
		return
	}
	// Validate numeric input before loading/serializing any saved members.
	limit, offset, valid := candidatePagination(r)
	if !valid {
		_ = orihttp.RespondAPIError(w, http.StatusBadRequest,
			orihttp.NewAPIError("invalid_pagination", "That page request is not valid."))
		return
	}
	groups, ok := h.readReviewGroups(w, r, workspaceID)
	if !ok {
		return
	}
	if revision := r.URL.Query().Get("revision"); revision != "" && revision != groups.Revision {
		_ = orihttp.RespondAPIError(w, http.StatusConflict,
			orihttp.NewAPIError("review_changed", "This saved review changed. Reload the group before continuing."))
		return
	}
	groupID := strings.TrimSpace(r.PathValue("groupID"))
	members, found := groups.Members(groupID)
	if !found {
		_ = orihttp.RespondNotFound(w, "group not found")
		return
	}
	total := len(members)
	offset = min(offset, total)
	end := offset + min(limit, total-offset)
	page := make([]reviewGroupMemberDTO, 0, end-offset)
	for _, member := range members[offset:end] {
		page = append(page, reviewGroupMemberDTO{
			candidateDTO: toCandidateDTO(member.Candidate, filejanitor.DefaultFilingRootName),
			Cautions:     member.Cautions,
		})
	}
	_ = orihttp.RespondSuccess(w, map[string]any{
		"success": true, "batch_id": groups.BatchID, "group_id": groupID,
		"revision": groups.Revision, "total": total, "limit": limit, "offset": offset,
		"candidates": page,
	})
}

func (h *Handler) readReviewGroups(w http.ResponseWriter, r *http.Request, workspaceID string) (filejanitor.ReviewGroups, bool) {
	owner, err := h.currentOwner(r.Context())
	if err != nil {
		_ = orihttp.RespondInternalError(w, "Failed to resolve the current user")
		return filejanitor.ReviewGroups{}, false
	}
	groups, err := h.service.ReadReviewGroups(r.Context(), owner, workspaceID, r.PathValue("batchID"))
	if err != nil {
		if errors.Is(err, filejanitor.ErrBatchNotFound) {
			_ = orihttp.RespondNotFound(w, "batch not found")
		} else {
			_ = orihttp.RespondAPIError(w, http.StatusConflict,
				orihttp.NewAPIError("review_unavailable", "This saved review is unavailable. Check File Janitor setup and reload the current review."))
		}
		return filejanitor.ReviewGroups{}, false
	}
	return groups, true
}

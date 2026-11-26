package agenthttp

import (
	"context"
	"net/http"

	"github.com/johnjallday/ori-agent/internal/filesearch"
	"github.com/johnjallday/ori-agent/internal/foldercontext"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

// Both owner domains are resolved by the host. No authority is reconstructed
// from a display label, a path, or an ordinary (unbound) Files review.
type ChatFolderReviews interface {
	PickForChat(context.Context, string, foldercontext.Target, string, string) (*filesearch.FolderReview, error)
	ResumeForChat(context.Context, string, foldercontext.Target, string, string) (*filesearch.FolderReview, string, error)
}

func (h *HomeAssistantAskHandler) FolderAttachmentHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if h.ChatFolders == nil || h.ChatFolderOwner == nil || h.folderStore() == nil {
		writeFolderError(w, foldercontext.ErrInvalid)
		return
	}
	owner, ok := h.ChatFolderOwner(r)
	if !ok {
		writeConversationError(w, http.StatusForbidden, PersonalAssistantFolderUnavailable, "Choose a folder on Home on this Mac.")
		return
	}
	scope, ok := h.readScope(w, r)
	if !ok {
		return
	}
	var req struct {
		ConversationID string `json:"conversation_id,omitempty"`
		DraftID        string `json:"draft_id,omitempty"`
		Revision       string `json:"revision,omitempty"`
		Action         string `json:"action"`
		Kind           string `json:"kind,omitempty"`
		ID             string `json:"id,omitempty"`
	}
	if !strictFolderBody(w, r, &req) {
		return
	}
	ctx := r.Context()
	target, err := h.folderTarget(scope, req.ConversationID, req.DraftID)
	if err != nil {
		writeFolderError(w, err)
		return
	}
	release, ok := h.folderRequests.enter(target)
	if !ok {
		writeFolderError(w, personalassistant.ErrFolderScanBusy)
		return
	}
	defer release()
	check := func() bool {
		state, err := h.folderExpected(ctx, target, req.Revision)
		if err != nil {
			writeFolderError(w, err)
			return false
		}
		if state.OfferID != "" {
			writeConversationError(w, http.StatusConflict, PersonalAssistantFolderConflict, "Finish or close the existing setup review before choosing another folder.")
			return false
		}
		return true
	}
	if !check() {
		return
	}
	var result any
	switch {
	case req.Action == "pick" && req.ID == "" && req.Kind != "":
		result, err = h.ChatFolders.PickForChat(ctx, owner, target, req.Revision, req.Kind)
	case req.Action == "resume" && req.ID != "" && req.Kind == "":
		result, _, err = h.ChatFolders.ResumeForChat(ctx, owner, target, req.Revision, req.ID)
	case req.Action == "inspect" && req.ID != "" && req.Kind == "":
		reader, ok := h.FolderObservations.(interface {
			ObserveReviewed(context.Context, foldercontext.Target, func() (string, error)) (*foldercontext.Observation, error)
		})
		if !ok {
			writeFolderError(w, foldercontext.ErrInvalid)
			return
		}
		result, err = reader.ObserveReviewed(ctx, target, func() (string, error) {
			_, path, e := h.ChatFolders.ResumeForChat(ctx, owner, target, req.Revision, req.ID)
			return path, e
		})
	default:
		err = foldercontext.ErrInvalid
	}
	if err != nil {
		writeFolderError(w, err)
		return
	}
	fresh, ok := h.readScope(w, r)
	if !ok {
		return
	}
	freshOwner, allowed := h.ChatFolderOwner(r)
	if !allowed || owner != freshOwner || fresh != scope {
		writeFolderError(w, foldercontext.ErrInvalid)
		return
	}
	if !check() {
		return
	}
	orihttp.WriteJSON(w, result)
}

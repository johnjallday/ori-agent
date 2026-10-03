package personalhqhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/johnjallday/ori-agent/internal/emailtriage"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
)

// NeedsYouService is the "Needs you" list for the mailbox linked to a workspace
// the user owns.
type NeedsYouService interface {
	NeedsYou(ctx context.Context, userID, workspaceID string) (emailtriage.List, error)
	MarkNeedsYou(ctx context.Context, userID, workspaceID, threadID string, bucket emailtriage.Bucket) error
	TrackNeedsYou(ctx context.Context, userID, workspaceID, threadID string) error
}

// ErrNoMailboxLinked means the workspace has no mailbox, so it has no list.
var ErrNoMailboxLinked = errors.New("no mailbox is linked to this workspace")

// NeedsYouError is a list failure the panel can explain: Code is stable and
// Message is safe to show.
type NeedsYouError struct {
	Code    string
	Message string
	Status  int
}

func (e *NeedsYouError) Error() string { return e.Code + ": " + e.Message }

// SetNeedsYouService wires the list. Nil leaves the endpoints unavailable.
func (h *Handler) SetNeedsYouService(svc NeedsYouService) { h.needsYou = svc }

// WorkspaceNeedsYou handles GET /api/workspaces/{workspaceID}/email/needs-you.
// Every workspace page asks, so a workspace with no mailbox is an ordinary
// answer ({"linked": false}), not an error.
func (h *Handler) WorkspaceNeedsYou(w http.ResponseWriter, r *http.Request) {
	userID, workspaceID, ok := h.needsYouRequest(w, r, http.MethodGet)
	if !ok {
		return
	}
	list, err := h.needsYou.NeedsYou(r.Context(), userID, workspaceID)
	if errors.Is(err, ErrNoMailboxLinked) {
		orihttp.Success(w, map[string]any{"linked": false})
		return
	}
	if err != nil {
		respondNeedsYouError(w, err)
		return
	}
	orihttp.Success(w, map[string]any{"linked": true, "list": list})
}

type needsYouAction struct {
	ThreadID string `json:"thread_id"`
	Bucket   string `json:"bucket,omitempty"`
}

// WorkspaceNeedsYouMark handles POST …/email/needs-you/mark: the user's own
// call on a thread ("Not important", "This needs me").
func (h *Handler) WorkspaceNeedsYouMark(w http.ResponseWriter, r *http.Request) {
	userID, workspaceID, ok := h.needsYouRequest(w, r, http.MethodPost)
	if !ok {
		return
	}
	action, ok := decodeNeedsYouAction(w, r)
	if !ok {
		return
	}
	if err := h.needsYou.MarkNeedsYou(r.Context(), userID, workspaceID, action.ThreadID, emailtriage.Bucket(action.Bucket)); err != nil {
		respondNeedsYouError(w, err)
		return
	}
	orihttp.Success(w, map[string]any{"ok": true})
}

// WorkspaceNeedsYouTrack handles POST …/email/needs-you/track: makes the
// thread a follow-up in this workspace, once.
func (h *Handler) WorkspaceNeedsYouTrack(w http.ResponseWriter, r *http.Request) {
	userID, workspaceID, ok := h.needsYouRequest(w, r, http.MethodPost)
	if !ok {
		return
	}
	action, ok := decodeNeedsYouAction(w, r)
	if !ok {
		return
	}
	if err := h.needsYou.TrackNeedsYou(r.Context(), userID, workspaceID, action.ThreadID); err != nil {
		respondNeedsYouError(w, err)
		return
	}
	orihttp.Success(w, map[string]any{"ok": true})
}

func (h *Handler) needsYouRequest(w http.ResponseWriter, r *http.Request, method string) (string, string, bool) {
	if !orihttp.RequireMethod(w, r, method) {
		return "", "", false
	}
	if h == nil || h.needsYou == nil {
		_ = orihttp.RespondJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "unavailable", "message": "Email isn't available in this build."})
		return "", "", false
	}
	userID, ok := h.resolveUser(w, r)
	if !ok {
		return "", "", false
	}
	workspaceID := strings.TrimSpace(r.PathValue("workspaceID"))
	if workspaceID == "" {
		orihttp.BadRequest(w, "workspace id is required")
		return "", "", false
	}
	return userID, workspaceID, true
}

func decodeNeedsYouAction(w http.ResponseWriter, r *http.Request) (needsYouAction, bool) {
	var action needsYouAction
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&action); err != nil {
		orihttp.BadRequest(w, "the request could not be read")
		return needsYouAction{}, false
	}
	action.ThreadID = strings.TrimSpace(action.ThreadID)
	if action.ThreadID == "" {
		orihttp.BadRequest(w, "thread_id is required")
		return needsYouAction{}, false
	}
	return action, true
}

func respondNeedsYouError(w http.ResponseWriter, err error) {
	var coded *NeedsYouError
	switch {
	case errors.As(err, &coded):
		_ = orihttp.RespondJSON(w, coded.Status, map[string]string{"error": coded.Code, "message": coded.Message})
	case errors.Is(err, ErrNoMailboxLinked):
		_ = orihttp.RespondJSON(w, http.StatusConflict, map[string]string{"error": "not_linked", "message": err.Error()})
	case errors.Is(err, emailtriage.ErrUnknownThread):
		_ = orihttp.RespondJSON(w, http.StatusNotFound, map[string]string{"error": "unknown_thread", "message": "That email is no longer in the list. Refresh and try again."})
	default:
		orihttp.InternalError(w, "the email list could not be read")
	}
}

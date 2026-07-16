package personalhqhttp

import (
	"context"
	"net/http"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/personalhq"
	"github.com/johnjallday/ori-agent/internal/session"
)

// JournalAPI is the end-of-day journal surface, implemented by
// *personalhq.JournalService.
type JournalAPI interface {
	Propose(ctx context.Context, userID, localDate string) (*personalhq.JournalProposal, error)
	Save(ctx context.Context, userID, localDate, content string) (*session.WorkspaceNote, error)
}

// SetJournal wires the end-of-day journal service.
func (h *Handler) SetJournal(api JournalAPI) { h.journal = api }

func (h *Handler) journalReady(w http.ResponseWriter, r *http.Request, method string) bool {
	if !orihttp.RequireMethod(w, r, method) {
		return false
	}
	if h == nil || h.journal == nil {
		orihttp.ServiceUnavailable(w, "personal hq journal is unavailable")
		return false
	}
	return true
}

// ProposeJournal handles GET /api/personal-hq/journal/propose?date=YYYY-MM-DD:
// the grounded, editable end-of-day draft. Never writes.
func (h *Handler) ProposeJournal(w http.ResponseWriter, r *http.Request) {
	if !h.journalReady(w, r, http.MethodGet) {
		return
	}
	userID, ok := h.resolveUser(w, r)
	if !ok {
		return
	}
	proposal, err := h.journal.Propose(r.Context(), userID, strings.TrimSpace(r.URL.Query().Get("date")))
	if err != nil {
		orihttp.InternalError(w, "Failed to prepare the journal: "+err.Error())
		return
	}
	orihttp.Success(w, map[string]any{"proposal": proposal})
}

// SaveJournal handles POST /api/personal-hq/journal/save: persist a reviewed
// journal as a dated note. The default path never writes to MEMORY.md.
func (h *Handler) SaveJournal(w http.ResponseWriter, r *http.Request) {
	if !h.journalReady(w, r, http.MethodPost) {
		return
	}
	var req struct {
		LocalDate string `json:"local_date"`
		Content   string `json:"content"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Content) == "" {
		orihttp.BadRequest(w, "content is required")
		return
	}
	userID, ok := h.resolveUser(w, r)
	if !ok {
		return
	}
	note, err := h.journal.Save(r.Context(), userID, req.LocalDate, req.Content)
	if err != nil {
		orihttp.Conflict(w, err.Error())
		return
	}
	orihttp.Success(w, map[string]any{"note_id": note.ID, "workspace_id": note.WorkspaceID})
}

// DismissJournal handles POST /api/personal-hq/journal/dismiss. Dismissing is a
// pure no-op with NO side effect — an ignored prompt must never create a note or
// memory entry (contract §7).
func (h *Handler) DismissJournal(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	orihttp.Success(w, map[string]any{"ok": true})
}

package externalmemoryhttp

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/externalmemory"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
)

type pasteRequest struct {
	Text string `json:"text"`
}

// PasteClaudeAccount handles POST /api/external-memory/sources/claude_account/paste
// (FR8). The text the user copied from claude.ai replaces the previous paste,
// turns the source on, and is split into entries right away; an unchanged
// line keeps its id, so its promoted link survives. The text is never logged.
func (h *Handler) PasteClaudeAccount(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}
	if _, ok := h.runner.Registry().Get(externalmemory.SourceClaudeAccount); !ok {
		_ = orihttp.RespondNotFound(w, "the Claude account source is not available")
		return
	}
	var req pasteRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	store := h.runner.Store()
	if err := store.SavePaste(externalmemory.SourceClaudeAccount, req.Text, time.Now()); err != nil {
		switch {
		case errors.Is(err, externalmemory.ErrPasteTooLarge):
			_ = orihttp.RespondBadRequest(w, "The paste is larger than 256 KB. Split it and add it in parts.")
		case errors.Is(err, externalmemory.ErrPasteEmpty):
			_ = orihttp.RespondBadRequest(w, "Paste the memory text you copied from claude.ai first.")
		default:
			_ = orihttp.RespondInternalError(w, "The paste could not be saved.")
		}
		return
	}
	if _, err := store.UpdateSources(func(doc *externalmemory.SourcesDocument) error {
		state := doc.Source(externalmemory.SourceClaudeAccount)
		state.Enabled = true
		doc.Sources[externalmemory.SourceClaudeAccount] = state
		return nil
	}); err != nil {
		_ = orihttp.RespondInternalError(w, "The Claude account source could not be turned on.")
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), syncRequestTimeout)
	defer cancel()
	if _, err := h.runner.RunSource(ctx, externalmemory.SourceClaudeAccount, externalmemory.ReasonPaste); err != nil {
		_ = orihttp.RespondInternalError(w, "The paste was saved but could not be read: "+strings.TrimSpace(err.Error()))
		return
	}
	h.writeSources(w, http.StatusOK)
}

package externalmemoryhttp

import (
	"context"
	"net/http"

	"github.com/johnjallday/ori-agent/internal/externalmemory"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
)

// ExportSource reads what Copy for Claude import exports: the global profile
// and the approved Personal HQ facts (never the external mirror itself).
type ExportSource interface {
	ClaudeExport(ctx context.Context) (externalmemory.ExportProfile, []externalmemory.ExportFact, error)
}

// SetExportSource wires the export.
func (h *Handler) SetExportSource(source ExportSource) {
	if h != nil {
		h.export = source
	}
}

// GetClaudeExport handles GET /api/external-memory/export/claude (FR22): the
// approved Personal HQ memory and profile as claude.ai's five-category import
// text, served as text/plain for the Copy button.
func (h *Handler) GetClaudeExport(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.export == nil {
		_ = orihttp.RespondAPIError(w, http.StatusServiceUnavailable,
			orihttp.NewAPIError("unavailable", "The export is not available."))
		return
	}
	profile, facts, err := h.export.ClaudeExport(r.Context())
	if err != nil {
		logger.Debug("Claude import export failed", logger.Fields{"error": err})
		_ = orihttp.RespondAPIError(w, http.StatusServiceUnavailable,
			orihttp.NewAPIError("unavailable", "Your profile and global memory could not be read. Try again."))
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte(externalmemory.RenderClaudeImport(profile, facts))); err != nil {
		logger.Debug("Failed to write the Claude import export", logger.Fields{"error": err})
	}
}

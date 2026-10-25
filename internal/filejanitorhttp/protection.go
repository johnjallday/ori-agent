package filejanitorhttp

import (
	"net/http"

	"github.com/johnjallday/ori-agent/internal/filecuratorguidance"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
)

func (h *Handler) SetProtectionReader(reader filecuratorguidance.ProtectionReader) {
	if h != nil {
		h.protection = reader
	}
}

// GetProtection reads only saved metadata. In particular, it does not use
// GetStatus/Service.Status, whose readiness evaluation probes the managed root.
func (h *Handler) GetProtection(w http.ResponseWriter, r *http.Request) {
	id, ok := h.resolveWorkspace(w, r)
	if !ok {
		return
	}
	owner, err := h.currentOwner(r.Context())
	if err != nil || h.protection == nil {
		_ = orihttp.RespondServiceUnavailable(w, "Stored protection evidence is unavailable.")
		return
	}
	fact, err := h.protection.ReadProtection(r.Context(), owner, id)
	// Recheck authorization after the stored read, not just at entry.
	if _, ok := h.resolveWorkspace(w, r); !ok {
		return
	}
	if err != nil {
		_ = orihttp.RespondServiceUnavailable(w, "Stored protection evidence is unavailable.")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	_ = orihttp.RespondJSON(w, http.StatusOK, fact)
}

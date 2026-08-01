package downloadsjanitorhttp

import (
	"net/http"

	"github.com/johnjallday/ori-agent/internal/workspacecapability"
)

// Register mounts the File Janitor API on mux. Every route is scoped to a
// workspace path segment; there is no global or cross-workspace endpoint, which
// is what keeps one workspace's folder access from being reachable through
// another's (FR-140).
//
// Each endpoint is registered twice: once under the canonical `file-janitor`
// prefix that new callers use, and once under the legacy `downloads-janitor`
// prefix that shipped browser code and any persisted deep link still use
// (FR-132, FR-133). Both patterns resolve to the SAME handler method — there is
// no second implementation and no divergence to keep in sync, so the ownership
// and request-protection checks in resolveWorkspace apply identically to both.
//
// The legacy prefix must not be removed in the same release that introduces the
// canonical one; see tasks/compat-boundary-file-janitor.md §4.
func (h *Handler) Register(mux *http.ServeMux) {
	if h == nil || mux == nil {
		return
	}
	for _, prefix := range routePrefixes() {
		h.registerUnder(mux, prefix)
	}
}

// routePrefixes returns the canonical prefix first, then every retained legacy
// alias, taken from the compiled capability definition so the routes and the
// capability's declared API identity cannot drift apart.
func routePrefixes() []string {
	api := workspacecapability.FileJanitorDefinition().API
	prefixes := make([]string, 0, 1+len(api.LegacyPrefixes))
	if api.Prefix != "" {
		prefixes = append(prefixes, api.Prefix)
	}
	prefixes = append(prefixes, api.LegacyPrefixes...)
	return prefixes
}

// registerUnder mounts the full endpoint set beneath one workspace-scoped
// prefix.
func (h *Handler) registerUnder(mux *http.ServeMux, prefix string) {
	base := "/api/workspaces/{workspaceID}/" + prefix

	mux.HandleFunc("GET "+base, h.GetStatus)
	mux.HandleFunc("GET "+base+"/readiness", h.GetReadiness)
	mux.HandleFunc("POST "+base+"/setup", h.ConfirmSetup)
	mux.HandleFunc("POST "+base+"/pause", h.SetPaused)
	mux.HandleFunc("PATCH "+base+"/settings", h.UpdateSettings)
	mux.HandleFunc("POST "+base+"/content-consent", h.GrantConsent)
	mux.HandleFunc("POST "+base+"/relink", h.Relink)
	mux.HandleFunc("POST "+base+"/revoke", h.RevokeAccess)
	mux.HandleFunc("GET "+base+"/skipped", h.ListSkipped)
	mux.HandleFunc("GET "+base+"/categories", h.Categories)
	mux.HandleFunc("GET "+base+"/batches", h.ListBatches)
	mux.HandleFunc("GET "+base+"/batches/{batchID}", h.GetBatch)
	mux.HandleFunc("POST "+base+"/test-scan", h.TestScan)
	mux.HandleFunc("POST "+base+"/scan", h.ScanNow)
	mux.HandleFunc("POST "+base+"/decisions", h.UpdateDecisions)
	mux.HandleFunc("POST "+base+"/skipped/reset", h.ResetSkipped)
	mux.HandleFunc("POST "+base+"/preview", h.PreviewMoves)
	mux.HandleFunc("POST "+base+"/apply", h.ConfirmMoves)
	mux.HandleFunc("GET "+base+"/history", h.History)
	mux.HandleFunc("POST "+base+"/history/{actionID}/undo", h.Undo)
}

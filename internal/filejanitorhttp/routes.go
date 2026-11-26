package filejanitorhttp

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
	mux.HandleFunc("GET "+base+"/protection", h.GetProtection)
	mux.HandleFunc("POST "+base+"/setup", h.ConfirmSetup)
	mux.HandleFunc("GET "+base+"/setup-review", h.SetupReview)
	mux.HandleFunc("POST "+base+"/setup-review", h.SetupReview)
	mux.HandleFunc("POST "+base+"/prepare-review", h.PrepareReview)
	mux.HandleFunc("POST "+base+"/stop-preparation", h.StopPreparation)
	mux.HandleFunc("POST "+base+"/pause", h.SetPaused)
	mux.HandleFunc("PATCH "+base+"/settings", h.UpdateSettings)
	mux.HandleFunc("POST "+base+"/content-consent", h.GrantConsent)
	mux.HandleFunc("POST "+base+"/relink", h.Relink)
	mux.HandleFunc("POST "+base+"/revoke", h.RevokeAccess)
	mux.HandleFunc("GET "+base+"/connections", h.ListConnections)
	mux.HandleFunc("POST "+base+"/connections", h.CreateConnection)
	mux.HandleFunc("POST "+base+"/connections/{connectionID}/turn-off", h.TurnOffConnection)
	mux.HandleFunc("GET "+base+"/arrivals", h.ListArrivals)
	mux.HandleFunc("POST "+base+"/arrivals/{arrivalID}/dismiss", h.DismissArrival)
	mux.HandleFunc("POST "+base+"/arrivals/{arrivalID}/undo", h.UndoArrival)
	mux.HandleFunc("GET "+base+"/skipped", h.ListSkipped)
	mux.HandleFunc("GET "+base+"/categories", h.Categories)
	mux.HandleFunc("GET "+base+"/batches", h.ListBatches)
	mux.HandleFunc("GET "+base+"/batches/{batchID}", h.GetBatch)
	mux.HandleFunc("GET "+base+"/batches/{batchID}/draft", h.GetReviewDraft)
	mux.HandleFunc("POST "+base+"/batches/{batchID}/draft", h.EditReviewDraft)
	mux.HandleFunc("GET "+base+"/batches/{batchID}/groups", h.GetReviewGroups)
	mux.HandleFunc("GET "+base+"/batches/{batchID}/groups/{groupID}/members", h.GetReviewGroupMembers)
	mux.HandleFunc("GET "+base+"/batches/{batchID}/picture", h.GetReviewPicture)
	mux.HandleFunc("GET "+base+"/batches/{batchID}/picture/files", h.GetReviewPictureFiles)
	mux.HandleFunc("POST "+base+"/batches/{batchID}/picture/files/{candidateID}/reveal", h.RevealReviewFile)
	mux.HandleFunc("POST "+base+"/batches/{batchID}/picture/files/{candidateID}/open", h.OpenReviewFile)
	mux.HandleFunc("GET "+base+"/batches/{batchID}/plan", h.GetReviewPlan)
	mux.HandleFunc("POST "+base+"/batches/{batchID}/file", h.FileReviewPlan)
	mux.HandleFunc("POST "+base+"/batches/{batchID}/suggestion", h.AskForSuggestion)
	mux.HandleFunc("GET "+base+"/filings", h.ListFilings)
	mux.HandleFunc("GET "+base+"/filings/{filingID}", h.GetFiling)
	mux.HandleFunc("POST "+base+"/filings/{filingID}/undo", h.UndoFiling)
	mux.HandleFunc("POST "+base+"/filings/{filingID}/forget-restored", h.ForgetRestoredFiles)
	mux.HandleFunc("POST "+base+"/test-scan", h.TestScan)
	mux.HandleFunc("POST "+base+"/scan", h.ScanNow)
	mux.HandleFunc("POST "+base+"/decisions", h.UpdateDecisions)
	mux.HandleFunc("POST "+base+"/skipped/reset", h.ResetSkipped)
	mux.HandleFunc("POST "+base+"/preview", h.PreviewMoves)
	mux.HandleFunc("POST "+base+"/apply", h.ConfirmMoves)
	mux.HandleFunc("GET "+base+"/history", h.History)
	mux.HandleFunc("POST "+base+"/history/{actionID}/undo", h.Undo)
}

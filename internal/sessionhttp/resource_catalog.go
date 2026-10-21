package sessionhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/resourcecatalog"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// SetResourceCatalogCall supplies the call to a project plugin's declared
// catalog operations (plugin.SurfaceLifecycle.ResourceCatalogCall).
func (h *Handler) SetResourceCatalogCall(call func(context.Context, plugin.InstalledPlugin, string, json.RawMessage) (json.RawMessage, error)) {
	if h != nil {
		h.resourceCatalogsMu.Lock()
		h.resourceCatalogCall, h.resourceCatalogs = call, nil
		h.resourceCatalogsMu.Unlock()
	}
}

// SetResourceCatalogFolderCheck sets the decision on a folder the owner types
// or picks for a catalog: it returns the canonical folder and an optional
// sentence to show, or an error whose text is safe to show as it is.
func (h *Handler) SetResourceCatalogFolderCheck(check func(*http.Request, string) (string, string, error)) {
	if h != nil {
		h.resourceCatalogsMu.Lock()
		h.resourceCatalogFolderCheck = check
		h.resourceCatalogsMu.Unlock()
	}
}

func (h *Handler) catalogFolderCheck() func(*http.Request, string) (string, string, error) {
	h.resourceCatalogsMu.Lock()
	defer h.resourceCatalogsMu.Unlock()
	return h.resourceCatalogFolderCheck
}

// SetResourceCatalogDataDir sets where this computer's catalog grants live.
func (h *Handler) SetResourceCatalogDataDir(dir string) {
	if h != nil {
		h.resourceCatalogsMu.Lock()
		h.resourceCatalogDataDir, h.resourceCatalogs = strings.TrimSpace(dir), nil
		h.resourceCatalogsMu.Unlock()
	}
}

// ResourceCatalogs returns the catalog service, or nil until its store, data
// directory and plugin access are all wired.
func (h *Handler) ResourceCatalogs() *resourcecatalog.Service {
	if h == nil {
		return nil
	}
	h.resourceCatalogsMu.Lock()
	defer h.resourceCatalogsMu.Unlock()
	if h.resourceCatalogs != nil {
		return h.resourceCatalogs
	}
	folders, ok := h.workspaceTaskStore.(workspace.FolderResolver)
	if !ok || h.workspaceTaskStore == nil || h.installedPluginLister == nil || h.resourceCatalogDataDir == "" {
		return nil
	}
	dataDir, err := filepath.Abs(h.resourceCatalogDataDir)
	if err != nil {
		return nil
	}
	h.resourceCatalogs = resourcecatalog.NewService(resourcecatalog.Deps{
		Workspaces: h.workspaceTaskStore, Folders: folders, DataDir: dataDir,
		Provider:              resourceCatalogProvider{list: h.installedPluginLister.List, call: h.resourceCatalogCall},
		HomeProviderAvailable: h.assistantHomeProviderAvailable,
	})
	return h.resourceCatalogs
}

// resourceCatalogProvider finds a Studio's exact provider: the plugin its
// link names, with a resource catalog for the linked blueprint and team.
type resourceCatalogProvider struct {
	list func() ([]plugin.InstalledPlugin, error)
	call func(context.Context, plugin.InstalledPlugin, string, json.RawMessage) (json.RawMessage, error)
}

func (p resourceCatalogProvider) Status(studio *workspace.Workspace) resourcecatalog.ProviderStatus {
	_, status := p.find(studio)
	return status
}

func (p resourceCatalogProvider) Sources(ctx context.Context, studio *workspace.Workspace, input []byte) ([]byte, error) {
	installed, status := p.find(studio)
	if !status.Available || p.call == nil {
		return nil, plugin.ErrResourceCatalogUnavailable
	}
	return p.call(ctx, installed, plugin.ResourceCatalogSources, input)
}

func (p resourceCatalogProvider) Inventory(ctx context.Context, studio *workspace.Workspace, input []byte) ([]byte, error) {
	installed, status := p.find(studio)
	if !status.Available || p.call == nil {
		return nil, plugin.ErrResourceCatalogUnavailable
	}
	return p.call(ctx, installed, plugin.ResourceCatalogInventory, input)
}

func (p resourceCatalogProvider) find(studio *workspace.Workspace) (plugin.InstalledPlugin, resourcecatalog.ProviderStatus) {
	link := studio.GetAssistantProjectLink()
	if !workspace.AssistantProjectLinkIsResource(link) || p.list == nil {
		return plugin.InstalledPlugin{}, resourcecatalog.ProviderStatus{Reason: resourcecatalog.ReasonProviderMissing}
	}
	owner := link.ProjectProvider
	installed, err := p.list()
	if err != nil {
		return plugin.InstalledPlugin{}, resourcecatalog.ProviderStatus{Reason: resourcecatalog.ReasonProviderMissing}
	}
	for _, candidate := range installed {
		if !strings.EqualFold(candidate.Name, owner.PluginID) {
			continue
		}
		if !candidate.Enabled {
			return plugin.InstalledPlugin{}, resourcecatalog.ProviderStatus{Reason: resourcecatalog.ReasonProviderDisabled}
		}
		declaration, err := candidate.WorkspaceSurfaces.ResourceCatalogDeclaration()
		if err != nil || declaration == nil || declaration.BlueprintID != owner.BlueprintID || !resourceTeamInstalled(candidate, owner) {
			return plugin.InstalledPlugin{}, resourcecatalog.ProviderStatus{Reason: resourcecatalog.ReasonCatalogMissing}
		}
		status := resourcecatalog.ProviderStatus{
			Catalog: resourcecatalog.CatalogRef{PluginID: candidate.Name, ID: declaration.ID, Version: declaration.Version},
		}
		if !serviceArtifactAvailable(candidate, declaration.ServiceID) {
			status.Reason = resourcecatalog.ReasonServiceMissing
			return plugin.InstalledPlugin{}, status
		}
		status.Available, status.Declaration = true, declaration
		return candidate, status
	}
	return plugin.InstalledPlugin{}, resourcecatalog.ProviderStatus{Reason: resourcecatalog.ReasonProviderMissing}
}

// resourceTeamInstalled requires the installed blueprint to still declare the
// linked resource team; a release that dropped or replaced it cannot serve
// this Studio.
func resourceTeamInstalled(installed plugin.InstalledPlugin, owner *workspace.AssistantProjectProviderOwner) bool {
	for _, blueprint := range installed.ResolvedBlueprints {
		project := blueprint.Template.AssistantProject
		if blueprint.ID == owner.BlueprintID && project.IsResource() && project.ID == owner.ProjectTeamID {
			return true
		}
	}
	return false
}

func serviceArtifactAvailable(installed plugin.InstalledPlugin, serviceID string) bool {
	for _, artifact := range installed.ResolvedArtifacts {
		if artifact.ServiceID == serviceID && artifact.Available {
			return true
		}
	}
	return false
}

// catalogRequest resolves the owner and the catalog service for one request.
func (h *Handler) catalogRequest(w http.ResponseWriter, r *http.Request) (*resourcecatalog.Service, string, string, bool) {
	service := h.ResourceCatalogs()
	if service == nil || h.currentUserID == nil {
		_ = orihttp.RespondNotFound(w, "This catalog is unavailable")
		return nil, "", "", false
	}
	owner, err := h.currentUserID(r.Context())
	studioID := strings.TrimSpace(r.PathValue("workspaceID"))
	if err != nil || strings.TrimSpace(owner) == "" || studioID == "" {
		_ = orihttp.RespondNotFound(w, "This catalog is unavailable")
		return nil, "", "", false
	}
	return service, owner, studioID, true
}

func respondCatalogError(w http.ResponseWriter, err error) {
	code, status, message := "catalog_unavailable", http.StatusInternalServerError, "The catalog could not be read."
	switch {
	case errors.Is(err, resourcecatalog.ErrNotFound):
		_ = orihttp.RespondNotFound(w, "This catalog is unavailable")
		return
	case errors.Is(err, resourcecatalog.ErrInvalidRequest):
		_ = orihttp.RespondBadRequest(w, "The catalog request is invalid")
		return
	case errors.Is(err, resourcecatalog.ErrDetached):
		code, status, message = "not_attached", http.StatusConflict, "This workspace is no longer attached to its Home; its saved catalog is read-only."
	case errors.Is(err, resourcecatalog.ErrProviderUnavailable):
		code, status, message = "provider_unavailable", http.StatusConflict, "The plugin that serves this catalog is unavailable. What was saved stays readable."
	case errors.Is(err, resourcecatalog.ErrScopeReviewRequired):
		code, status, message = "scope_review_required", http.StatusConflict, "Review the sources before Ori reads them."
	case errors.Is(err, resourcecatalog.ErrReviewStale):
		code, status, message = "review_stale", http.StatusConflict, "The review changed or expired. Review again."
	case errors.Is(err, resourcecatalog.ErrInspectionActive):
		code, status, message = "inspection_active", http.StatusConflict, "An inspection is already running."
	case errors.Is(err, resourcecatalog.ErrConflict):
		code, status, message = "page_stale", http.StatusConflict, "The catalog changed since this page was read. Reload it."
	case errors.Is(err, resourcecatalog.ErrFolderLimit):
		code, status, message = "folder_limit", http.StatusConflict,
			"This catalog already has "+strconv.Itoa(resourcecatalog.MaxOwnerFolders)+" folders on this computer. Remove one first."
	case errors.Is(err, resourcecatalog.ErrCorrupt):
		code, message = "catalog_unreadable", "The saved catalog could not be read; it was left as it is."
	}
	_ = orihttp.RespondAPIError(w, status, orihttp.NewAPIError(code, message))
}

// GetResourceCatalog handles GET /api/workspaces/{workspaceID}/resource-catalog.
func (h *Handler) GetResourceCatalog(w http.ResponseWriter, r *http.Request) {
	service, owner, studioID, ok := h.catalogRequest(w, r)
	if !ok {
		return
	}
	view, err := service.Overview(owner, studioID)
	if err != nil {
		respondCatalogError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, view)
}

// ListResourceCatalogEntries handles GET .../resource-catalog/entries.
func (h *Handler) ListResourceCatalogEntries(w http.ResponseWriter, r *http.Request) {
	service, owner, studioID, ok := h.catalogRequest(w, r)
	if !ok {
		return
	}
	values := r.URL.Query()
	for key, list := range values {
		switch key {
		case "q", "category", "kind", "source", "favorites", "state", "cursor", "limit":
		default:
			_ = orihttp.RespondBadRequest(w, "The catalog request is invalid")
			return
		}
		if len(list) != 1 {
			_ = orihttp.RespondBadRequest(w, "The catalog request is invalid")
			return
		}
	}
	query := resourcecatalog.Query{
		Q: values.Get("q"), Category: values.Get("category"), Kind: values.Get("kind"), Source: values.Get("source"),
		FavoritesOnly: values.Get("favorites") == "1", State: values.Get("state"), Cursor: values.Get("cursor"),
	}
	if raw := values.Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil {
			_ = orihttp.RespondBadRequest(w, "The catalog request is invalid")
			return
		}
		query.Limit = limit
	}
	page, err := service.Entries(owner, studioID, query)
	if err != nil {
		respondCatalogError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, page)
}

// GetResourceCatalogEntry handles GET .../resource-catalog/entries/{entryID}.
func (h *Handler) GetResourceCatalogEntry(w http.ResponseWriter, r *http.Request) {
	service, owner, studioID, ok := h.catalogRequest(w, r)
	if !ok {
		return
	}
	detail, err := service.Entry(owner, studioID, strings.TrimSpace(r.PathValue("entryID")))
	if err != nil {
		respondCatalogError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, detail)
}

// ReviewResourceCatalogSources handles POST .../resource-catalog/sources/review.
func (h *Handler) ReviewResourceCatalogSources(w http.ResponseWriter, r *http.Request) {
	service, owner, studioID, ok := h.catalogRequest(w, r)
	if !ok {
		return
	}
	var request struct{}
	if !decodeStrictAction(w, r, &request, "The review request is invalid") {
		return
	}
	review, err := service.ReviewSources(r.Context(), owner, studioID)
	if err != nil {
		respondCatalogError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, review)
}

// CommitResourceCatalogSources handles POST .../resource-catalog/sources/commit.
func (h *Handler) CommitResourceCatalogSources(w http.ResponseWriter, r *http.Request) {
	service, owner, studioID, ok := h.catalogRequest(w, r)
	if !ok {
		return
	}
	var request struct {
		ReviewToken    string   `json:"review_token"`
		IdempotencyKey string   `json:"idempotency_key"`
		SourceIDs      []string `json:"source_ids"`
	}
	if !decodeStrictAction(w, r, &request, "The approval request is invalid") {
		return
	}
	op, err := service.CommitSources(r.Context(), owner, studioID, request.ReviewToken, request.IdempotencyKey, request.SourceIDs)
	if err != nil {
		respondCatalogError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"operation": op})
}

// StartResourceCatalogInspection handles POST .../resource-catalog/inspections.
func (h *Handler) StartResourceCatalogInspection(w http.ResponseWriter, r *http.Request) {
	service, owner, studioID, ok := h.catalogRequest(w, r)
	if !ok {
		return
	}
	var request struct {
		IdempotencyKey string   `json:"idempotency_key"`
		Mode           string   `json:"mode"`
		SourceIDs      []string `json:"source_ids,omitempty"`
	}
	if !decodeStrictAction(w, r, &request, "The inspection request is invalid") {
		return
	}
	op, err := service.StartInspection(r.Context(), owner, studioID, request.IdempotencyKey, request.Mode, request.SourceIDs)
	if errors.Is(err, resourcecatalog.ErrInspectionActive) {
		_ = orihttp.RespondJSON(w, http.StatusConflict, map[string]any{
			"error": "An inspection is already running.", "code": "inspection_active", "operation": op,
		})
		return
	}
	if err != nil {
		respondCatalogError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"operation": op})
}

// GetResourceCatalogInspection handles GET .../resource-catalog/inspections/{operationID}.
func (h *Handler) GetResourceCatalogInspection(w http.ResponseWriter, r *http.Request) {
	service, owner, studioID, ok := h.catalogRequest(w, r)
	if !ok {
		return
	}
	op, err := service.Operation(owner, studioID, strings.TrimSpace(r.PathValue("operationID")))
	if err != nil {
		respondCatalogError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"operation": op})
}

// CancelResourceCatalogInspection handles POST .../inspections/{operationID}/cancel.
func (h *Handler) CancelResourceCatalogInspection(w http.ResponseWriter, r *http.Request) {
	service, owner, studioID, ok := h.catalogRequest(w, r)
	if !ok {
		return
	}
	var request struct{}
	if !decodeStrictAction(w, r, &request, "The cancel request is invalid") {
		return
	}
	op, err := service.Cancel(owner, studioID, strings.TrimSpace(r.PathValue("operationID")))
	if err != nil {
		respondCatalogError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"operation": op})
}

// SetResourceCatalogAnnotation handles PUT .../entries/{entryID}/annotation:
// the owner's favorite, tags and note, written against base_revision.
func (h *Handler) SetResourceCatalogAnnotation(w http.ResponseWriter, r *http.Request) {
	service, owner, studioID, ok := h.catalogRequest(w, r)
	if !ok {
		return
	}
	var request resourcecatalog.AnnotationInput
	if !decodeStrictAction(w, r, &request, "The notes are invalid") {
		return
	}
	saved, err := service.SetAnnotation(owner, studioID, strings.TrimSpace(r.PathValue("entryID")), request)
	if respondAnnotationConflict(w, err) {
		return
	}
	if err != nil {
		respondCatalogError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"annotation": saved})
}

// MoveResourceCatalogAnnotation handles POST .../entries/{entryID}/annotation/move.
func (h *Handler) MoveResourceCatalogAnnotation(w http.ResponseWriter, r *http.Request) {
	service, owner, studioID, ok := h.catalogRequest(w, r)
	if !ok {
		return
	}
	var request struct {
		ToEntryID    string `json:"to_entry_id"`
		BaseRevision int64  `json:"base_revision"`
	}
	if !decodeStrictAction(w, r, &request, "The move request is invalid") {
		return
	}
	moved, err := service.MoveAnnotation(owner, studioID, strings.TrimSpace(r.PathValue("entryID")), strings.TrimSpace(request.ToEntryID), request.BaseRevision)
	if respondAnnotationConflict(w, err) {
		return
	}
	if err != nil {
		respondCatalogError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"annotation": moved})
}

// respondAnnotationConflict answers a stale annotation write with the current
// value, so the page can show both rather than lose either.
func respondAnnotationConflict(w http.ResponseWriter, err error) bool {
	var conflict *resourcecatalog.AnnotationConflictError
	if !errors.As(err, &conflict) {
		return false
	}
	_ = orihttp.RespondJSON(w, http.StatusConflict, map[string]any{
		"error": "These notes changed somewhere else. Your text was not saved; compare and save again.",
		"code":  "annotation_conflict", "current": conflict.Current,
	})
	return true
}

// AddResourceCatalogFolder handles POST .../resource-catalog/folders: a folder
// of the owner's own for one category. The path passes the shared typed-path
// rules first; nothing is read until the owner approves it in a review.
func (h *Handler) AddResourceCatalogFolder(w http.ResponseWriter, r *http.Request) {
	service, owner, studioID, ok := h.catalogRequest(w, r)
	if !ok {
		return
	}
	var request struct {
		Category string `json:"category"`
		Path     string `json:"path"`
	}
	if !decodeStrictAction(w, r, &request, "The folder request is invalid") {
		return
	}
	check := h.catalogFolderCheck()
	if check == nil {
		_ = orihttp.RespondNotFound(w, "This catalog is unavailable")
		return
	}
	path, note, err := check(r, request.Path)
	if err != nil {
		_ = orihttp.RespondAPIError(w, http.StatusBadRequest, orihttp.NewAPIError("folder_refused", err.Error()))
		return
	}
	folder, err := service.AddFolder(owner, studioID, strings.TrimSpace(request.Category), path)
	if err != nil {
		respondCatalogError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{
		"folder": map[string]string{"id": folder.ID, "category": folder.Category, "path": folder.Path}, "warning_note": note,
	})
}

// RemoveResourceCatalogFolder handles DELETE .../resource-catalog/folders/{folderID}.
func (h *Handler) RemoveResourceCatalogFolder(w http.ResponseWriter, r *http.Request) {
	service, owner, studioID, ok := h.catalogRequest(w, r)
	if !ok {
		return
	}
	view, err := service.RemoveFolder(owner, studioID, strings.TrimSpace(r.PathValue("folderID")))
	if err != nil {
		respondCatalogError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, view)
}

// ReviewResourceCatalogWithdrawal handles POST .../sources/{sourceID}/withdraw/review.
func (h *Handler) ReviewResourceCatalogWithdrawal(w http.ResponseWriter, r *http.Request) {
	service, owner, studioID, ok := h.catalogRequest(w, r)
	if !ok {
		return
	}
	var request struct{}
	if !decodeStrictAction(w, r, &request, "The withdrawal request is invalid") {
		return
	}
	review, err := service.ReviewWithdraw(owner, studioID, strings.TrimSpace(r.PathValue("sourceID")))
	if err != nil {
		respondCatalogError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, review)
}

// CommitResourceCatalogWithdrawal handles POST .../sources/{sourceID}/withdraw/commit.
func (h *Handler) CommitResourceCatalogWithdrawal(w http.ResponseWriter, r *http.Request) {
	service, owner, studioID, ok := h.catalogRequest(w, r)
	if !ok {
		return
	}
	var request struct {
		ReviewToken string `json:"review_token"`
	}
	if !decodeStrictAction(w, r, &request, "The withdrawal request is invalid") {
		return
	}
	view, err := service.CommitWithdraw(owner, studioID, request.ReviewToken)
	if err != nil {
		respondCatalogError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, view)
}

package samplelibraryhttp

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/samplelibrary"
)

type Handler struct{ service *samplelibrary.Service }

func New(service *samplelibrary.Service) *Handler { return &Handler{service: service} }

func (h *Handler) GetState(w http.ResponseWriter, r *http.Request) {
	home := r.PathValue("workspaceID")
	state, roots, err := h.service.Snapshot(r.Context(), home)
	if errors.Is(err, samplelibrary.ErrNotFound) {
		orihttp.NotFound(w, "sample library is not configured")
		return
	}
	if err != nil {
		writeError(w, err)
		return
	}
	_ = orihttp.RespondJSON(w, http.StatusOK, map[string]any{"state": state, "roots": roots})
}
func (h *Handler) ReviewRoot(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SelectionToken string `json:"selection_token"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	review, err := h.service.ReviewRoot(r.Context(), r.PathValue("workspaceID"), req.SelectionToken)
	if err != nil {
		writeError(w, err)
		return
	}
	_ = orihttp.RespondJSON(w, http.StatusOK, map[string]any{"review": review})
}
func (h *Handler) CommitRoot(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ReviewToken    string `json:"review_token"`
		SelectionToken string `json:"selection_token"`
		IdempotencyKey string `json:"idempotency_key"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	state, root, err := h.service.CommitRoot(r.Context(), r.PathValue("workspaceID"), req.ReviewToken, req.SelectionToken, req.IdempotencyKey)
	if err != nil {
		writeError(w, err)
		return
	}
	_ = orihttp.RespondJSON(w, http.StatusOK, map[string]any{"state": state, "root": root})
}
func (h *Handler) ReviewRevocation(w http.ResponseWriter, r *http.Request) {
	review, err := h.service.ReviewRevocation(r.Context(), r.PathValue("workspaceID"), r.PathValue("rootID"))
	if err != nil {
		writeError(w, err)
		return
	}
	_ = orihttp.RespondJSON(w, http.StatusOK, map[string]any{"review": review})
}
func (h *Handler) CommitRevocation(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ReviewToken    string `json:"review_token"`
		IdempotencyKey string `json:"idempotency_key"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	state, root, err := h.service.CommitRevocation(r.Context(), r.PathValue("workspaceID"), r.PathValue("rootID"), req.ReviewToken, req.IdempotencyKey)
	if err != nil {
		writeError(w, err)
		return
	}
	_ = orihttp.RespondJSON(w, http.StatusOK, map[string]any{"state": state, "root": root})
}

func (h *Handler) ReviewAnalysis(w http.ResponseWriter, r *http.Request) {
	var req struct {
		HashEnabled         bool `json:"hash_enabled"`
		EmbeddedTagsEnabled bool `json:"embedded_tags_enabled"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	review, err := h.service.ReviewAnalysis(r.Context(), r.PathValue("workspaceID"), r.PathValue("rootID"), req.HashEnabled, req.EmbeddedTagsEnabled)
	if err != nil {
		writeError(w, err)
		return
	}
	_ = orihttp.RespondJSON(w, http.StatusOK, map[string]any{"review": review})
}
func (h *Handler) CommitAnalysis(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ReviewToken         string `json:"review_token"`
		IdempotencyKey      string `json:"idempotency_key"`
		HashEnabled         bool   `json:"hash_enabled"`
		EmbeddedTagsEnabled bool   `json:"embedded_tags_enabled"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	state, root, err := h.service.CommitAnalysis(r.Context(), r.PathValue("workspaceID"), r.PathValue("rootID"), req.ReviewToken, req.IdempotencyKey, req.HashEnabled, req.EmbeddedTagsEnabled)
	if err != nil {
		writeError(w, err)
		return
	}
	_ = orihttp.RespondJSON(w, http.StatusOK, map[string]any{"state": state, "root": root})
}

func (h *Handler) Index(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IdempotencyKey  string `json:"idempotency_key"`
		CatalogRevision int64  `json:"catalog_revision"`
		RootRevision    int64  `json:"root_revision"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	result, err := h.service.Index(r.Context(), r.PathValue("workspaceID"), r.PathValue("rootID"), req.IdempotencyKey, req.CatalogRevision, req.RootRevision)
	if err != nil {
		writeError(w, err)
		return
	}
	_ = orihttp.RespondJSON(w, http.StatusOK, map[string]any{"result": result})
}
func (h *Handler) Collections(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.Collections(r.Context(), r.PathValue("workspaceID"))
	if err != nil {
		writeError(w, err)
		return
	}
	_ = orihttp.RespondJSON(w, http.StatusOK, map[string]any{"collections": items, "count": len(items)})
}
func (h *Handler) ReviewCollection(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name            string `json:"name"`
		Note            string `json:"note"`
		CatalogRevision int64  `json:"catalog_revision"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	review, err := h.service.ReviewCollection(r.Context(), r.PathValue("workspaceID"), req.Name, req.Note, req.CatalogRevision)
	if err != nil {
		writeError(w, err)
		return
	}
	_ = orihttp.RespondJSON(w, http.StatusOK, map[string]any{"review": review})
}
func (h *Handler) CreateCollection(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name            string `json:"name"`
		Note            string `json:"note"`
		ReviewToken     string `json:"review_token"`
		IdempotencyKey  string `json:"idempotency_key"`
		CatalogRevision int64  `json:"catalog_revision"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	state, item, err := h.service.CommitCollection(r.Context(), r.PathValue("workspaceID"), req.ReviewToken, req.Name, req.Note, req.IdempotencyKey, req.CatalogRevision)
	if err != nil {
		writeError(w, err)
		return
	}
	_ = orihttp.RespondJSON(w, http.StatusOK, map[string]any{"state": state, "collection": item})
}
func (h *Handler) ReviewCollectionMember(w http.ResponseWriter, r *http.Request) {
	var req struct {
		EntryID            string `json:"entry_id"`
		CollectionRevision int64  `json:"collection_revision"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	review, err := h.service.ReviewCollectionMember(r.Context(), r.PathValue("workspaceID"), r.PathValue("collectionID"), req.EntryID, req.CollectionRevision)
	if err != nil {
		writeError(w, err)
		return
	}
	_ = orihttp.RespondJSON(w, http.StatusOK, map[string]any{"review": review})
}
func (h *Handler) AddCollectionMember(w http.ResponseWriter, r *http.Request) {
	var req struct {
		EntryID            string `json:"entry_id"`
		ReviewToken        string `json:"review_token"`
		IdempotencyKey     string `json:"idempotency_key"`
		CollectionRevision int64  `json:"collection_revision"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	item, err := h.service.CommitCollectionMember(r.Context(), r.PathValue("workspaceID"), req.ReviewToken, r.PathValue("collectionID"), req.EntryID, req.IdempotencyKey, req.CollectionRevision)
	if err != nil {
		writeError(w, err)
		return
	}
	_ = orihttp.RespondJSON(w, http.StatusOK, map[string]any{"collection": item})
}

func (h *Handler) ReviewCopy(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ChildWorkspaceID string   `json:"child_workspace_id"`
		EntryIDs         []string `json:"entry_ids"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	review, err := h.service.ReviewCopy(r.Context(), r.PathValue("workspaceID"), req.ChildWorkspaceID, req.EntryIDs)
	if err != nil {
		writeError(w, err)
		return
	}
	_ = orihttp.RespondJSON(w, http.StatusOK, map[string]any{"review": review})
}
func (h *Handler) CommitCopy(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ChildWorkspaceID string   `json:"child_workspace_id"`
		EntryIDs         []string `json:"entry_ids"`
		ReviewToken      string   `json:"review_token"`
		IdempotencyKey   string   `json:"idempotency_key"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	result, err := h.service.CommitCopy(r.Context(), r.PathValue("workspaceID"), req.ChildWorkspaceID, req.ReviewToken, req.IdempotencyKey, req.EntryIDs)
	if err != nil {
		writeError(w, err)
		return
	}
	_ = orihttp.RespondJSON(w, http.StatusOK, map[string]any{"result": result})
}

func (h *Handler) DelegateQuestion(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Question       string `json:"question"`
		IdempotencyKey string `json:"idempotency_key"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	result, err := h.service.DelegateQuestion(r.Context(), r.PathValue("workspaceID"), req.Question, req.IdempotencyKey)
	if err != nil {
		writeError(w, err)
		return
	}
	_ = orihttp.RespondJSON(w, http.StatusOK, map[string]any{"result": result})
}

func (h *Handler) ReviewAnnotation(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserTags           []string `json:"user_tags"`
		PackNote           string   `json:"pack_note"`
		SourceNote         string   `json:"source_note"`
		LicenseNote        string   `json:"license_note"`
		CatalogRevision    int64    `json:"catalog_revision"`
		AnnotationRevision int64    `json:"annotation_revision"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	review, err := h.service.ReviewAnnotation(r.Context(), r.PathValue("workspaceID"), r.PathValue("entryID"), req.UserTags, req.PackNote, req.SourceNote, req.LicenseNote, req.CatalogRevision, req.AnnotationRevision)
	if err != nil {
		writeError(w, err)
		return
	}
	_ = orihttp.RespondJSON(w, http.StatusOK, map[string]any{"review": review})
}
func (h *Handler) SetAnnotation(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ReviewToken        string   `json:"review_token"`
		UserTags           []string `json:"user_tags"`
		PackNote           string   `json:"pack_note"`
		SourceNote         string   `json:"source_note"`
		LicenseNote        string   `json:"license_note"`
		IdempotencyKey     string   `json:"idempotency_key"`
		CatalogRevision    int64    `json:"catalog_revision"`
		AnnotationRevision int64    `json:"annotation_revision"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	state, item, err := h.service.CommitAnnotation(r.Context(), r.PathValue("workspaceID"), req.ReviewToken, r.PathValue("entryID"), req.IdempotencyKey, req.UserTags, req.PackNote, req.SourceNote, req.LicenseNote, req.CatalogRevision, req.AnnotationRevision)
	if err != nil {
		writeError(w, err)
		return
	}
	_ = orihttp.RespondJSON(w, http.StatusOK, map[string]any{"state": state, "annotation": item})
}

func (h *Handler) FindForProject(w http.ResponseWriter, r *http.Request) {
	result, err := h.service.SearchForProject(r.Context(), r.PathValue("workspaceID"), searchOptions(r))
	if err != nil {
		writeError(w, err)
		return
	}
	_ = orihttp.RespondJSON(w, http.StatusOK, map[string]any{"result": result})
}
func searchOptions(r *http.Request) samplelibrary.SearchOptions {
	limit := 200
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 200 {
			limit = parsed
		}
	}
	return samplelibrary.SearchOptions{Query: r.URL.Query().Get("q"), Extension: r.URL.Query().Get("extension"), Sort: r.URL.Query().Get("sort"), Direction: r.URL.Query().Get("direction"), Limit: limit}
}
func (h *Handler) Search(w http.ResponseWriter, r *http.Request) {
	result, err := h.service.SearchWithOptions(r.Context(), r.PathValue("workspaceID"), searchOptions(r))
	if err != nil {
		writeError(w, err)
		return
	}
	_ = orihttp.RespondJSON(w, http.StatusOK, map[string]any{"result": result})
}

func (h *Handler) Entries(w http.ResponseWriter, r *http.Request) {
	limit := 200
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 200 {
			limit = parsed
		}
	}
	entries, err := h.service.Entries(r.Context(), r.PathValue("workspaceID"), r.PathValue("rootID"), limit)
	if err != nil {
		writeError(w, err)
		return
	}
	_ = orihttp.RespondJSON(w, http.StatusOK, map[string]any{"entries": entries, "count": len(entries)})
}
func writeError(w http.ResponseWriter, err error) {
	code := http.StatusConflict
	message := "sample_operation_failed"
	switch {
	case errors.Is(err, samplelibrary.ErrInvalidRoot):
		code = http.StatusBadRequest
		message = "sample_root_invalid"
	case errors.Is(err, samplelibrary.ErrRootConflict):
		message = "sample_root_conflict"
	case errors.Is(err, samplelibrary.ErrRootMissing):
		code = http.StatusNotFound
		message = "sample_root_missing"
	case errors.Is(err, samplelibrary.ErrRootChanged):
		message = "sample_root_changed"
	case errors.Is(err, samplelibrary.ErrPermissionDenied):
		code = http.StatusForbidden
		message = "sample_permission_denied"
	case errors.Is(err, samplelibrary.ErrScanInProgress):
		message = "sample_scan_in_progress"
	case errors.Is(err, samplelibrary.ErrRevisionConflict):
		message = "sample_revision_conflict"
	}
	_ = orihttp.RespondError(w, code, message)
}

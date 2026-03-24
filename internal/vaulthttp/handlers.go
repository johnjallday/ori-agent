package vaulthttp

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/vault"
)

type Handler struct {
	store *vault.Store
}

func NewHandler(store *vault.Store) *Handler {
	return &Handler{store: store}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.store == nil {
		_ = orihttp.RespondError(w, http.StatusServiceUnavailable, "vault is unavailable")
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/vault")
	path = strings.TrimSpace(path)
	if path == "" {
		path = "/"
	}

	switch {
	case path == "/status":
		h.handleStatus(w, r)
	case path == "/unlock":
		h.handleUnlock(w, r)
	case path == "/lock":
		h.handleLock(w, r)
	case path == "/export":
		h.handleExport(w, r)
	case path == "/records" || path == "/records/":
		h.handleRecords(w, r)
	case strings.HasPrefix(path, "/records/"):
		h.handleRecord(w, r, strings.TrimPrefix(path, "/records/"))
	case path == "/grants" || path == "/grants/":
		h.handleGrants(w, r)
	case strings.HasPrefix(path, "/grants/"):
		h.handleGrant(w, r, strings.TrimPrefix(path, "/grants/"))
	default:
		_ = orihttp.RespondNotFound(w, "vault endpoint not found")
	}
}

func (h *Handler) handleStatus(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodGet) {
		return
	}

	status, err := h.store.Status(r.Context())
	if err != nil {
		respondVaultError(w, err)
		return
	}
	orihttp.Success(w, status)
}

func (h *Handler) handleUnlock(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}

	var req struct {
		VaultPassword string `json:"vault_password"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	if err := h.store.Unlock(req.VaultPassword); err != nil {
		respondVaultError(w, err)
		return
	}

	status, err := h.store.Status(r.Context())
	if err != nil {
		respondVaultError(w, err)
		return
	}
	orihttp.Success(w, status)
}

func (h *Handler) handleLock(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}

	if err := h.store.Lock(); err != nil {
		respondVaultError(w, err)
		return
	}

	status, err := h.store.Status(r.Context())
	if err != nil {
		respondVaultError(w, err)
		return
	}
	orihttp.Success(w, status)
}

func (h *Handler) handleRecords(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		filter := vault.RecordFilter{
			WorkspaceID: firstNonEmpty(r.URL.Query().Get("workspace_id"), r.URL.Query().Get("studio_id")),
			Type:        r.URL.Query().Get("type"),
		}
		access := accessFromQuery(r)
		records, err := h.store.ListRecords(r.Context(), filter, access)
		if err != nil {
			respondVaultError(w, err)
			return
		}
		orihttp.Success(w, map[string]any{
			"records": records,
			"count":   len(records),
		})
	case http.MethodPost:
		var req struct {
			Type            string          `json:"type"`
			WorkspaceID     string          `json:"workspace_id,omitempty"`
			Label           string          `json:"label"`
			Tags            []string        `json:"tags,omitempty"`
			Source          string          `json:"source,omitempty"`
			RetentionPolicy string          `json:"retention_policy,omitempty"`
			Payload         json.RawMessage `json:"payload"`
			ActorType       vault.ActorType `json:"actor_type,omitempty"`
			ActorID         string          `json:"actor_id,omitempty"`
		}
		if !orihttp.ParseJSONBody(w, r, &req) {
			return
		}

		record := &vault.Record{
			Type:            req.Type,
			WorkspaceID:     firstNonEmpty(req.WorkspaceID, r.URL.Query().Get("workspace_id"), r.URL.Query().Get("studio_id")),
			Label:           req.Label,
			Tags:            req.Tags,
			Source:          req.Source,
			RetentionPolicy: req.RetentionPolicy,
			Payload:         req.Payload,
		}

		access := accessFromFields(record.WorkspaceID, req.ActorType, req.ActorID)
		if err := h.store.CreateRecord(r.Context(), record, access); err != nil {
			respondVaultError(w, err)
			return
		}
		orihttp.Created(w, map[string]any{
			"success": true,
			"record":  record,
		})
	default:
		_ = orihttp.RespondMethodNotAllowed(w)
	}
}

func (h *Handler) handleRecord(w http.ResponseWriter, r *http.Request, id string) {
	id = strings.TrimSpace(id)
	if id == "" || strings.Contains(id, "/") {
		_ = orihttp.RespondBadRequest(w, "record id is required")
		return
	}

	switch r.Method {
	case http.MethodGet:
		record, err := h.store.GetRecord(r.Context(), id, accessFromQuery(r))
		if err != nil {
			respondVaultError(w, err)
			return
		}
		orihttp.Success(w, record)
	case http.MethodPatch, http.MethodPut:
		var req struct {
			Label           *string          `json:"label,omitempty"`
			Tags            *[]string        `json:"tags,omitempty"`
			Source          *string          `json:"source,omitempty"`
			RetentionPolicy *string          `json:"retention_policy,omitempty"`
			Payload         *json.RawMessage `json:"payload,omitempty"`
			WorkspaceID     string           `json:"workspace_id,omitempty"`
			ActorType       vault.ActorType  `json:"actor_type,omitempty"`
			ActorID         string           `json:"actor_id,omitempty"`
		}
		if !orihttp.ParseJSONBody(w, r, &req) {
			return
		}

		update := vault.RecordUpdate{
			Label:           req.Label,
			Tags:            req.Tags,
			Source:          req.Source,
			RetentionPolicy: req.RetentionPolicy,
		}
		if req.Payload != nil {
			payload := json.RawMessage(*req.Payload)
			update.Payload = &payload
		}

		workspaceID := firstNonEmpty(req.WorkspaceID, r.URL.Query().Get("workspace_id"), r.URL.Query().Get("studio_id"))
		access := accessFromFields(workspaceID, req.ActorType, req.ActorID)
		if access.ActorID == "" && access.ActorType == "" {
			access = accessFromQuery(r)
		}

		record, err := h.store.UpdateRecord(r.Context(), id, update, access)
		if err != nil {
			respondVaultError(w, err)
			return
		}
		orihttp.Success(w, map[string]any{
			"success": true,
			"record":  record,
		})
	case http.MethodDelete:
		if err := h.store.DeleteRecord(r.Context(), id, accessFromQuery(r)); err != nil {
			respondVaultError(w, err)
			return
		}
		orihttp.Success(w, map[string]any{"success": true})
	default:
		_ = orihttp.RespondMethodNotAllowed(w)
	}
}

func (h *Handler) handleGrants(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		workspaceID := firstNonEmpty(r.URL.Query().Get("workspace_id"), r.URL.Query().Get("studio_id"))
		grants, err := h.store.ListGrants(r.Context(), workspaceID)
		if err != nil {
			respondVaultError(w, err)
			return
		}
		orihttp.Success(w, map[string]any{
			"grants": grants,
			"count":  len(grants),
		})
	case http.MethodPost:
		var grant vault.Grant
		if !orihttp.ParseJSONBody(w, r, &grant) {
			return
		}
		grant.WorkspaceID = firstNonEmpty(grant.WorkspaceID, r.URL.Query().Get("workspace_id"), r.URL.Query().Get("studio_id"))
		if err := h.store.CreateGrant(r.Context(), &grant); err != nil {
			respondVaultError(w, err)
			return
		}
		orihttp.Created(w, map[string]any{
			"success": true,
			"grant":   grant,
		})
	default:
		_ = orihttp.RespondMethodNotAllowed(w)
	}
}

func (h *Handler) handleGrant(w http.ResponseWriter, r *http.Request, id string) {
	id = strings.TrimSpace(id)
	if id == "" || strings.Contains(id, "/") {
		_ = orihttp.RespondBadRequest(w, "grant id is required")
		return
	}
	if !orihttp.RequireMethod(w, r, http.MethodDelete) {
		return
	}
	if err := h.store.DeleteGrant(r.Context(), id); err != nil {
		respondVaultError(w, err)
		return
	}
	orihttp.Success(w, map[string]any{"success": true})
}

func (h *Handler) handleExport(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}

	var req struct {
		WorkspaceID   string `json:"workspace_id,omitempty"`
		StudioID      string `json:"studio_id,omitempty"`
		VaultPassword string `json:"vault_password"`
		Confirm       bool   `json:"confirm"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	if !req.Confirm {
		_ = orihttp.RespondBadRequest(w, "confirm must be true before exporting vault data")
		return
	}

	bundle, err := h.store.Export(r.Context(), vault.ExportRequest{
		WorkspaceID: firstNonEmpty(req.WorkspaceID, req.StudioID),
		Password:    req.VaultPassword,
	})
	if err != nil {
		respondVaultError(w, err)
		return
	}
	orihttp.Success(w, bundle)
}

func accessFromQuery(r *http.Request) vault.AccessContext {
	return accessFromFields(
		firstNonEmpty(r.URL.Query().Get("workspace_id"), r.URL.Query().Get("studio_id")),
		vault.ActorType(r.URL.Query().Get("actor_type")),
		r.URL.Query().Get("actor_id"),
	)
}

func accessFromFields(workspaceID string, actorType vault.ActorType, actorID string) vault.AccessContext {
	return vault.AccessContext{
		WorkspaceID: strings.TrimSpace(workspaceID),
		ActorType:   actorType,
		ActorID:     strings.TrimSpace(actorID),
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func respondVaultError(w http.ResponseWriter, err error) {
	switch {
	case err == nil:
		return
	case errors.Is(err, vault.ErrRecordNotFound), errors.Is(err, vault.ErrGrantNotFound):
		_ = orihttp.RespondError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, vault.ErrPermissionDenied):
		_ = orihttp.RespondError(w, http.StatusForbidden, err.Error())
	case errors.Is(err, vault.ErrVaultLocked):
		_ = orihttp.RespondError(w, http.StatusLocked, err.Error())
	case errors.Is(err, vault.ErrVaultPasswordRequired), errors.Is(err, vault.ErrExportPasswordEmpty):
		_ = orihttp.RespondError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, vault.ErrVaultKeyUnavailable), errors.Is(err, vault.ErrMalformedRecord):
		_ = orihttp.RespondError(w, http.StatusInternalServerError, err.Error())
	case errors.Is(err, vault.ErrSecretStoreUnavailable), errors.Is(err, vault.ErrSecretStoreLocked):
		_ = orihttp.RespondError(w, http.StatusServiceUnavailable, err.Error())
	default:
		_ = orihttp.RespondError(w, http.StatusInternalServerError, err.Error())
	}
}

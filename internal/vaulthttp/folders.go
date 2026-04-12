package vaulthttp

import (
	"net/http"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/vault"
)

func (h *Handler) handleFolders(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		folders, err := h.store.ListFolders(r.Context(), vaultIDFromRequest(r))
		if err != nil {
			respondVaultError(w, err)
			return
		}
		orihttp.Success(w, map[string]any{
			"folders": folders,
			"count":   len(folders),
		})
	case http.MethodPost:
		var req struct {
			VaultID string `json:"vault_id,omitempty"`
			Path    string `json:"path"`
		}
		if !orihttp.ParseJSONBody(w, r, &req) {
			return
		}

		folder, err := h.store.CreateFolder(r.Context(), &vault.Folder{
			VaultID: vaultIDFromRequest(r, req.VaultID),
			Path:    req.Path,
		})
		if err != nil {
			respondVaultError(w, err)
			return
		}

		orihttp.Created(w, map[string]any{
			"success": true,
			"folder":  folder,
		})
	case http.MethodDelete:
		var req struct {
			VaultID   string `json:"vault_id,omitempty"`
			Path      string `json:"path,omitempty"`
			Recursive bool   `json:"recursive,omitempty"`
		}
		if r.ContentLength > 0 {
			if !orihttp.ParseJSONBody(w, r, &req) {
				return
			}
		}

		path := req.Path
		if path == "" {
			path = r.URL.Query().Get("path")
		}
		recursive := req.Recursive || r.URL.Query().Get("recursive") == "true"

		if err := h.store.DeleteFolder(r.Context(), vaultIDFromRequest(r, req.VaultID), path, recursive); err != nil {
			respondVaultError(w, err)
			return
		}

		orihttp.Success(w, map[string]any{
			"success": true,
		})
	default:
		_ = orihttp.RespondMethodNotAllowed(w)
	}
}

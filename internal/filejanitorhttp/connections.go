package filejanitorhttp

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/johnjallday/ori-agent/internal/filejanitor"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
)

// maxConnectRequestBytes bounds the one connect request: a workspace ID and up to
// eight kinds.
const maxConnectRequestBytes = 2 * 1024

// connectRequestBody is the closed shape of a connect request. It names the
// receiving workspace by ID and never a folder: the server works out both folders
// from its own saved state (FR 17). Unknown fields are refused.
type connectRequestBody struct {
	ReceiverWorkspaceID string   `json:"receiver_workspace_id"`
	Kinds               []string `json:"kinds"`
}

// ListConnections serves this workspace's "Sends to" and "Receives from"
// sections and the workspaces it could send to. It reads saved state and checks
// each folder's identity; it moves nothing.
func (h *Handler) ListConnections(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	workspaceID, ok := h.resolveWorkspace(w, r)
	if !ok {
		return
	}
	owner, err := h.currentOwner(r.Context())
	if err != nil {
		_ = orihttp.RespondInternalError(w, "Failed to resolve the current user")
		return
	}
	connections, err := h.service.ListConnections(owner, workspaceID)
	if err != nil {
		h.respondError(w, err, "Failed to read Filing connections")
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"success": true, "connections": connections})
}

// CreateConnection turns on "this workspace sends to that one" for the kinds the
// person ticked, after every check passes. A refusal is one plain sentence.
func (h *Handler) CreateConnection(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	workspaceID, ok := h.resolveWorkspace(w, r)
	if !ok {
		return
	}
	if r.ContentLength > maxConnectRequestBytes {
		_ = orihttp.RespondBadRequest(w, "That request is too large.")
		return
	}
	var body connectRequestBody
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxConnectRequestBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		_ = orihttp.RespondBadRequest(w, "That connect request is not valid.")
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		_ = orihttp.RespondBadRequest(w, "Send exactly one connect request.")
		return
	}
	if len(body.Kinds) > len(filejanitor.CategoryRegistry) {
		_ = orihttp.RespondBadRequest(w, "That connect request is not valid.")
		return
	}
	owner, err := h.currentOwner(r.Context())
	if err != nil {
		_ = orihttp.RespondInternalError(w, "Failed to resolve the current user")
		return
	}
	connections, err := h.service.ConnectWorkspace(owner, workspaceID, filejanitor.ConnectRequest{
		ReceiverWorkspaceID: body.ReceiverWorkspaceID, Kinds: body.Kinds,
	})
	if err != nil {
		h.respondError(w, err, "Failed to turn the connection on")
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"success": true, "connections": connections})
}

// TurnOffConnection removes a connection from either side. The request has no
// body; the connection is named by the path. It moves no file.
func (h *Handler) TurnOffConnection(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	workspaceID, ok := h.resolveWorkspace(w, r)
	if !ok {
		return
	}
	if r.ContentLength > 0 {
		_ = orihttp.RespondBadRequest(w, "That request takes no body.")
		return
	}
	owner, err := h.currentOwner(r.Context())
	if err != nil {
		_ = orihttp.RespondInternalError(w, "Failed to resolve the current user")
		return
	}
	connections, err := h.service.TurnOffConnection(owner, workspaceID, r.PathValue("connectionID"))
	if err != nil {
		h.respondError(w, err, "Failed to turn the connection off")
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"success": true, "connections": connections})
}

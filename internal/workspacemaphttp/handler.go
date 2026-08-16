// Package workspacemaphttp serves the current user's coordinate-based
// Workspace Map layout: where their buildings sit, where their camera is, and
// whether snapping is on.
//
// Two boundaries are enforced here rather than deeper.
//
// The layout is always the requesting user's. There is no user path segment, no
// user query parameter, and a user_id in the body is refused outright — the
// identity comes from the request context and nowhere else (FR-98).
//
// These routes are deliberately separate from /api/workspaces/{id}/layout,
// which owns a single workspace's internal Canvas. Sharing that endpoint would
// have made one workspace's task/agent/station arrangement and every user's
// global map two meanings of the same word (FR-104).
package workspacemaphttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspacemap"
)

// LayoutService is the map domain seam this handler serves.
type LayoutService interface {
	Load(ctx context.Context, userID string) (workspacemap.Layout, error)
	Apply(ctx context.Context, userID string, patch workspacemap.Patch) (workspacemap.Result, error)
	Reset(ctx context.Context, userID string) (workspacemap.Result, error)
}

// Handler serves the current-user map-layout endpoints.
type Handler struct {
	service  LayoutService
	provider userprofile.UserProvider
}

// NewHandler builds the map layout handler. A nil service makes every endpoint
// report 503 rather than panicking: the Map degrades to deterministic fallback
// placement with read-only navigation, which is a usable map, while a panic
// would take unrelated API routes down with it (FR-105).
func NewHandler(service LayoutService, provider userprofile.UserProvider) *Handler {
	if provider == nil {
		provider = userprofile.LocalUserProvider{}
	}
	return &Handler{service: service, provider: provider}
}

// GetLayout handles GET /api/workspace-map/layout.
//
// It returns the user's saved anchors, camera, snap preference, schema version,
// and revision. Reading never writes, so a user who has never moved anything
// gets defaults rather than a freshly created record (FR-23, FR-95).
func (h *Handler) GetLayout(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.currentUser(w, r)
	if !ok {
		return
	}
	layout, err := h.service.Load(r.Context(), userID)
	if err != nil {
		h.respondError(w, err, "Failed to load the workspace map layout")
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{
		"success": true,
		"layout":  layout,
	})
}

// PatchLayout handles PATCH /api/workspace-map/layout.
//
// The body carries explicit partial operations, never a whole-layout snapshot,
// so a browser tab that has been open since yesterday can move one building
// without erasing coordinates it never knew about (FR-96, FR-101). The response
// reports what was actually committed and the revision it produced, which is
// what the client reconciles against (FR-102).
func (h *Handler) PatchLayout(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.currentUser(w, r)
	if !ok {
		return
	}

	var req struct {
		// UserID exists in this struct only so a client that supplies one is
		// told plainly that it is refused, rather than having it silently
		// ignored and believing it targeted someone else's layout (FR-98).
		UserID string `json:"user_id,omitempty"`
		// Operations stay raw until each one is decoded strictly below.
		Operations []json.RawMessage `json:"operations"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.UserID) != "" {
		_ = orihttp.RespondBadRequest(w, "user_id is not accepted; the map layout is always the current user's")
		return
	}

	operations, ok := decodeOperations(w, req.Operations)
	if !ok {
		return
	}

	result, err := h.service.Apply(r.Context(), userID, workspacemap.Patch{Operations: operations})
	if err != nil {
		h.respondError(w, err, "Failed to save the workspace map layout")
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{
		"success": true,
		"result":  result,
	})
}

// decodeOperations turns the raw operation array into typed operations, one
// strictly-decoded object at a time.
//
// Strict decoding is the point. encoding/json ignores keys it does not
// recognise, so `{"op":"set_group_frame","frame":{...},"colour":"red"}` would
// otherwise be accepted and silently do only part of what the caller wrote — and
// a misspelled field on a district operation is exactly the case where "applied
// most of it" is worse than a refusal (#346 FR-180). The operation count is
// bounded before any decoding so a hostile body cannot buy unbounded work with a
// huge array of tiny objects (FR-182).
func decodeOperations(w http.ResponseWriter, raw []json.RawMessage) ([]workspacemap.Operation, bool) {
	if len(raw) > workspacemap.MaxOperationsPerPatch {
		_ = orihttp.RespondBadRequest(w, fmt.Sprintf(
			"a layout patch carries at most %d operations", workspacemap.MaxOperationsPerPatch))
		return nil, false
	}
	operations := make([]workspacemap.Operation, 0, len(raw))
	for i, item := range raw {
		var op workspacemap.Operation
		decoder := json.NewDecoder(bytes.NewReader(item))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&op); err != nil {
			_ = orihttp.RespondBadRequest(w, fmt.Sprintf("operation %d: %s", i, err.Error()))
			return nil, false
		}
		operations = append(operations, op)
	}
	return operations, true
}

// ResetLayout handles DELETE /api/workspace-map/layout.
//
// It clears this user's custom anchors so deterministic fallback placement
// takes over. No workspace is deleted, renamed, reordered, or reparented — the
// only thing removed is the user's own arrangement of them (FR-110, FR-111).
func (h *Handler) ResetLayout(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.currentUser(w, r)
	if !ok {
		return
	}
	result, err := h.service.Reset(r.Context(), userID)
	if err != nil {
		h.respondError(w, err, "Failed to reset the workspace map layout")
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{
		"success": true,
		"result":  result,
	})
}

// currentUser resolves the requesting user and confirms the service is wired.
func (h *Handler) currentUser(w http.ResponseWriter, r *http.Request) (string, bool) {
	if h == nil || h.service == nil {
		_ = orihttp.RespondAPIError(w, http.StatusServiceUnavailable,
			orihttp.NewAPIError("unavailable", "The workspace map layout is not available."))
		return "", false
	}
	userID, err := h.provider.CurrentUserID(r.Context())
	if err != nil {
		logger.Warn("Failed to resolve current user for the workspace map layout", logger.Fields{"error": err})
		_ = orihttp.RespondAPIError(w, http.StatusServiceUnavailable,
			orihttp.NewAPIError("unavailable", "The current user could not be resolved."))
		return "", false
	}
	if strings.TrimSpace(userID) == "" {
		userID = userprofile.LocalUserID
	}
	return userID, true
}

// respondError maps a domain error onto a stable status.
//
// Malformed geometry is the client's problem and says so precisely; a record
// this build cannot read is a conflict the user can resolve by upgrading rather
// than a request to retry; anything unrecognised is logged server-side and
// reported generically.
func (h *Handler) respondError(w http.ResponseWriter, err error, fallback string) {
	switch {
	case errors.Is(err, workspacemap.ErrInvalidPatch),
		errors.Is(err, workspacemap.ErrInvalidCoordinate),
		errors.Is(err, workspacemap.ErrInvalidZoom),
		errors.Is(err, workspacemap.ErrInvalidNodeID),
		errors.Is(err, workspacemap.ErrInvalidFrame),
		errors.Is(err, workspacemap.ErrUnsupportedPreset),
		errors.Is(err, workspacemap.ErrPatchTooLarge):
		_ = orihttp.RespondBadRequest(w, err.Error())
	case errors.Is(err, workspacemap.ErrGroupNotEligible):
		// The record exists and is the user's — it simply is not something the
		// Map draws a district for, which is a client-side mistake worth naming
		// rather than a missing resource (FR-181).
		_ = orihttp.RespondBadRequest(w, err.Error())
	case errors.Is(err, workspacemap.ErrNodeNotFound),
		errors.Is(err, workspacemap.ErrNodeNotOwned):
		// A record owned by someone else is reported as missing rather than
		// forbidden, so the API never confirms that another user's workspace
		// exists.
		_ = orihttp.RespondNotFound(w, "workspace not found")
	case errors.Is(err, workspacemap.ErrUnsupportedSchemaVersion):
		_ = orihttp.RespondConflict(w, "this map layout was saved by a newer version of Ori")
	case errors.Is(err, workspacemap.ErrServiceUnavailable),
		errors.Is(err, workspacemap.ErrStoreUnavailable),
		errors.Is(err, workspacemap.ErrGroupResolverUnavailable):
		_ = orihttp.RespondAPIError(w, http.StatusServiceUnavailable,
			orihttp.NewAPIError("unavailable", "The workspace map layout is not available."))
	default:
		logger.Warn("Workspace map layout request failed", logger.Fields{"error": err.Error()})
		_ = orihttp.RespondInternalError(w, fallback)
	}
}

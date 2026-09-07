// Package agentmaphttp serves the current user's coordinate-based Agent Map
// layout: where their agent tiles sit, where their camera is, and whether
// snapping is on.
//
// It mirrors internal/workspacemaphttp, including the two boundaries that
// package enforces at this layer rather than deeper.
//
// The layout is always the requesting user's. There is no user path segment, no
// user query parameter, and a user_id in the body is refused outright — the
// identity comes from the request context and nowhere else.
//
// These routes are deliberately separate from every agent CRUD route. Nothing
// here can change an agent; the only thing this API writes is where a tile sits.
package agentmaphttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/johnjallday/ori-agent/internal/agentmap"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/userprofile"
)

// LayoutService is the map domain seam this handler serves.
type LayoutService interface {
	Load(ctx context.Context, userID string) (agentmap.Layout, error)
	Apply(ctx context.Context, userID string, patch agentmap.Patch) (agentmap.Result, error)
	Reset(ctx context.Context, userID string) (agentmap.Result, error)
}

// Handler serves the current-user agent-map layout endpoints.
type Handler struct {
	// resolve is called per request rather than captured once. Handlers are
	// wired in an earlier server phase than the stores they depend on, so a
	// service captured at construction time is nil and every endpoint fails
	// silently — the trap that has bitten this repo before.
	resolve  func() LayoutService
	provider userprofile.UserProvider
}

// NewHandler builds the layout handler over a service resolver.
//
// A resolver returning nil makes every endpoint report 503 rather than
// panicking: the Map degrades to automatic placement with read-only
// navigation, which is a usable map, while a panic would take unrelated API
// routes down with it.
func NewHandler(resolve func() LayoutService, provider userprofile.UserProvider) *Handler {
	if provider == nil {
		provider = userprofile.LocalUserProvider{}
	}
	return &Handler{resolve: resolve, provider: provider}
}

// GetLayout handles GET /api/agent-map/layout.
//
// It returns the user's saved anchors, camera, snap preference, schema version,
// and revision. Reading never writes, so a user who has never moved anything
// gets defaults rather than a freshly created record.
func (h *Handler) GetLayout(w http.ResponseWriter, r *http.Request) {
	service, userID, ok := h.begin(w, r)
	if !ok {
		return
	}
	layout, err := service.Load(r.Context(), userID)
	if err != nil {
		h.respondError(w, err, "Failed to load the agent map layout")
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{
		"success": true,
		"layout":  layout,
	})
}

// PatchLayout handles PATCH /api/agent-map/layout.
//
// The body carries explicit partial operations, never a whole-layout snapshot,
// so a browser tab that has been open since yesterday can move one tile without
// erasing coordinates it never knew about. The response reports what was
// actually committed and the revision it produced, which is what the client
// reconciles against (FR-53).
func (h *Handler) PatchLayout(w http.ResponseWriter, r *http.Request) {
	service, userID, ok := h.begin(w, r)
	if !ok {
		return
	}

	var req struct {
		// UserID exists in this struct only so a client that supplies one is
		// told plainly that it is refused, rather than having it silently
		// ignored and believing it targeted someone else's layout.
		UserID string `json:"user_id,omitempty"`
		// ExpectedRevision is the revision the client last received. Omitting it
		// means "I did not check"; sending a stale one is refused.
		ExpectedRevision int64 `json:"expected_revision,omitempty"`
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

	result, err := service.Apply(r.Context(), userID, agentmap.Patch{
		Operations:       operations,
		ExpectedRevision: req.ExpectedRevision,
	})
	if err != nil {
		h.respondError(w, err, "Failed to save the agent map layout")
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
// recognise, so `{"op":"set_positions","positions":{...},"viewport":{...}}`
// with a typo in a field name would otherwise be accepted and silently do only
// part of what the caller wrote. The operation count is bounded before any
// decoding so a hostile body cannot buy unbounded work with a huge array of
// tiny objects.
func decodeOperations(w http.ResponseWriter, raw []json.RawMessage) ([]agentmap.Operation, bool) {
	if len(raw) > agentmap.MaxOperationsPerPatch {
		_ = orihttp.RespondBadRequest(w, fmt.Sprintf(
			"a layout patch carries at most %d operations", agentmap.MaxOperationsPerPatch))
		return nil, false
	}
	operations := make([]agentmap.Operation, 0, len(raw))
	for i, item := range raw {
		var op agentmap.Operation
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

// ResetLayout handles DELETE /api/agent-map/layout.
//
// It clears this user's custom anchors so deterministic automatic placement
// takes over. No agent is deleted, renamed, or changed in any way — the only
// thing removed is the user's own arrangement of them (FR-72).
func (h *Handler) ResetLayout(w http.ResponseWriter, r *http.Request) {
	service, userID, ok := h.begin(w, r)
	if !ok {
		return
	}
	result, err := service.Reset(r.Context(), userID)
	if err != nil {
		h.respondError(w, err, "Failed to reset the agent map layout")
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{
		"success": true,
		"result":  result,
	})
}

// begin resolves the service and the requesting user, or answers 503.
func (h *Handler) begin(w http.ResponseWriter, r *http.Request) (LayoutService, string, bool) {
	if h == nil || h.resolve == nil {
		respondUnavailable(w)
		return nil, "", false
	}
	service := h.resolve()
	if service == nil {
		respondUnavailable(w)
		return nil, "", false
	}
	userID, err := h.provider.CurrentUserID(r.Context())
	if err != nil {
		logger.Warn("Failed to resolve current user for the agent map layout", logger.Fields{"error": err})
		_ = orihttp.RespondAPIError(w, http.StatusServiceUnavailable,
			orihttp.NewAPIError("unavailable", "The current user could not be resolved."))
		return nil, "", false
	}
	if strings.TrimSpace(userID) == "" {
		userID = userprofile.LocalUserID
	}
	return service, userID, true
}

func respondUnavailable(w http.ResponseWriter) {
	_ = orihttp.RespondAPIError(w, http.StatusServiceUnavailable,
		orihttp.NewAPIError("unavailable", "The agent map layout is not available."))
}

// respondError maps a domain error onto a stable status.
//
// Malformed geometry is the client's problem and says so precisely; a stale
// revision is a conflict the client resolves by reloading; a record this build
// cannot read is a conflict the user resolves by upgrading; anything
// unrecognised is logged server-side and reported generically.
func (h *Handler) respondError(w http.ResponseWriter, err error, fallback string) {
	switch {
	case errors.Is(err, agentmap.ErrInvalidPatch),
		errors.Is(err, agentmap.ErrInvalidCoordinate),
		errors.Is(err, agentmap.ErrInvalidZoom),
		errors.Is(err, agentmap.ErrInvalidAgentName),
		errors.Is(err, agentmap.ErrPatchTooLarge):
		_ = orihttp.RespondBadRequest(w, err.Error())
	case errors.Is(err, agentmap.ErrStaleRevision):
		// 409, not 400: the request was well formed and would have been accepted
		// a moment ago. The client reloads and re-applies rather than retrying
		// the same body.
		_ = orihttp.RespondConflict(w, err.Error())
	case errors.Is(err, agentmap.ErrAgentNotFound):
		_ = orihttp.RespondNotFound(w, "agent not found")
	case errors.Is(err, agentmap.ErrUnsupportedSchemaVersion):
		_ = orihttp.RespondConflict(w, "this map layout was saved by a newer version of Ori")
	case errors.Is(err, agentmap.ErrServiceUnavailable),
		errors.Is(err, agentmap.ErrStoreUnavailable):
		respondUnavailable(w)
	default:
		logger.Warn("Agent map layout request failed", logger.Fields{"error": err.Error()})
		_ = orihttp.RespondInternalError(w, fallback)
	}
}

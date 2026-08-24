// Package workspacesurfacehttp exposes the one generic authenticated HTTP
// boundary used by every Workspace Surface. Plugins never register routes.
package workspacesurfacehttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacesurface"
)

const maxBrokerBodyBytes = workspacesurface.MaxOperationInputBytes + (8 << 10)

// WorkspaceStore is the canonical workspace read surface required for ownership
// checks. The synchronized workspace store satisfies it.
type WorkspaceStore interface {
	Get(string) (*workspace.Workspace, error)
}

// AttachmentChecker resolves the current inert workspace attachment. It is
// intentionally separate from Registry: global plugin enablement and
// per-workspace attachment are different lifecycle decisions.
type AttachmentChecker interface {
	Attached(context.Context, string, workspacesurface.RegisteredSurface) bool
}

type AttachmentCheckerFunc func(context.Context, string, workspacesurface.RegisteredSurface) bool

func (f AttachmentCheckerFunc) Attached(ctx context.Context, workspaceID string, surface workspacesurface.RegisteredSurface) bool {
	return f != nil && f(ctx, workspaceID, surface)
}

// ContextResolver injects canonical host paths/scopes after ownership and
// attachment validation. A browser request cannot supply these fields.
type ContextResolver interface {
	Resolve(context.Context, string, workspacesurface.RegisteredSurface) (workspacesurface.WorkspaceContext, error)
}

type ContextResolverFunc func(context.Context, string, workspacesurface.RegisteredSurface) (workspacesurface.WorkspaceContext, error)

func (f ContextResolverFunc) Resolve(ctx context.Context, workspaceID string, surface workspacesurface.RegisteredSurface) (workspacesurface.WorkspaceContext, error) {
	if f == nil {
		return workspacesurface.WorkspaceContext{}, errors.New("workspace surface context resolver is unavailable")
	}
	return f(ctx, workspaceID, surface)
}

type Handler struct {
	registry    *workspacesurface.Registry
	workspaces  WorkspaceStore
	users       userprofile.UserProvider
	attachments AttachmentChecker
	contexts    ContextResolver
	sessions    *sessionStore
	now         func() time.Time
	timeoutFor  func(workspacesurface.TimeoutClass) time.Duration
}

func NewHandler(registry *workspacesurface.Registry, workspaces WorkspaceStore, users userprofile.UserProvider, attachments AttachmentChecker, contexts ContextResolver) *Handler {
	if registry == nil {
		registry = workspacesurface.NewRegistry()
	}
	if users == nil {
		users = userprofile.LocalUserProvider{}
	}
	return &Handler{
		registry: registry, workspaces: workspaces, users: users,
		attachments: attachments, contexts: contexts, sessions: newSessionStore(), now: time.Now,
		timeoutFor: operationTimeout,
	}
}

// Register installs only generic host routes. The registry decides which plugin
// surfaces are available; no plugin can add a ServeMux pattern.
func (h *Handler) Register(mux *http.ServeMux) {
	if h == nil || mux == nil {
		return
	}
	mux.HandleFunc("GET /api/workspaces/{workspaceID}/surfaces", h.Catalog)
	mux.HandleFunc("POST /api/workspaces/{workspaceID}/surfaces/{surfaceKey}/sessions", h.OpenSession)
	mux.HandleFunc("GET /api/workspace-surfaces/frames/{frameToken}/{assetPath...}", h.FrameAsset)
	mux.HandleFunc("POST /api/workspace-surfaces/operations", h.InvokeOperation)
	mux.HandleFunc("DELETE /api/workspace-surfaces/sessions", h.CloseSession)
}

type catalogResponse struct {
	Surfaces []catalogSurface `json:"surfaces"`
}

type catalogSurface struct {
	Key          string                         `json:"key"`
	Plugin       catalogPlugin                  `json:"plugin"`
	CapabilityID string                         `json:"capability_id"`
	SurfaceID    string                         `json:"surface_id"`
	Label        string                         `json:"label"`
	Description  string                         `json:"description,omitempty"`
	Icon         workspacesurface.Icon          `json:"icon"`
	Placement    string                         `json:"placement"`
	Modal        workspacesurface.Modal         `json:"modal"`
	Status       workspacesurface.StationStatus `json:"status"`
	Available    bool                           `json:"available"`
	Unavailable  string                         `json:"unavailable_code,omitempty"`
	Polling      workspacesurface.Polling       `json:"polling"`
}

type catalogPlugin struct {
	ID         string `json:"id"`
	Version    string `json:"version"`
	Generation string `json:"generation"`
}

func (h *Handler) Catalog(w http.ResponseWriter, r *http.Request) {
	workspaceID := strings.TrimSpace(r.PathValue("workspaceID"))
	if _, ok := h.ownedWorkspace(r.Context(), workspaceID); !ok {
		respondError(w, http.StatusNotFound, "workspace_not_found", "Workspace not found.")
		return
	}

	response := catalogResponse{Surfaces: []catalogSurface{}}
	for _, surface := range h.registry.Surfaces() {
		if h.attachments == nil || !h.attachments.Attached(r.Context(), workspaceID, surface) {
			continue
		}
		item := h.catalogItem(r.Context(), workspaceID, surface)
		response.Surfaces = append(response.Surfaces, item)
	}
	_ = orihttp.RespondSuccess(w, response)
}

func (h *Handler) catalogItem(ctx context.Context, workspaceID string, surface workspacesurface.RegisteredSurface) catalogSurface {
	item := catalogSurface{
		Key:          surface.Key,
		Plugin:       catalogPlugin{ID: surface.Owner.ID, Version: surface.Owner.Version, Generation: strconv.FormatUint(surface.Owner.Generation, 10)},
		CapabilityID: surface.Capability.ID, SurfaceID: surface.Surface.ID,
		Label: surface.Surface.Label, Description: surface.Surface.Description,
		Icon: surface.Surface.Icon, Placement: surface.Surface.Placement,
		Modal: surface.Surface.Modal, Polling: surface.Surface.Polling, Available: true,
	}
	binding, ok := h.registry.Binding(surface.Key)
	if !ok || binding.Runtime == nil {
		item.Available = false
		item.Unavailable = "surface_unavailable"
		item.Status = unavailableStatus(h.clock())
		return item
	}
	workspaceContext, err := h.resolveContext(ctx, workspaceID, surface)
	if err != nil {
		item.Available = false
		item.Unavailable = "surface_unavailable"
		item.Status = unavailableStatus(h.clock())
		return item
	}
	workspaceContext.WorkspaceID = workspaceID
	status, err := binding.Runtime.Status(ctx, workspaceContext)
	if err != nil {
		item.Available = false
		item.Unavailable = "service_unavailable"
		item.Status = workspacesurface.NormalizeStationStatus(workspacesurface.StationStatus{
			State: workspacesurface.StationDegraded, Value: "Unavailable",
			Description: "The plugin service could not report its status.",
		}, h.clock())
		return item
	}
	item.Status = workspacesurface.NormalizeStationStatus(status, h.clock())
	return item
}

type openSessionResponse struct {
	Session   string    `json:"session"`
	FrameURL  string    `json:"frame_url"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (h *Handler) OpenSession(w http.ResponseWriter, r *http.Request) {
	workspaceID := strings.TrimSpace(r.PathValue("workspaceID"))
	userID, ok := h.ownedWorkspace(r.Context(), workspaceID)
	if !ok {
		respondError(w, http.StatusNotFound, "workspace_not_found", "Workspace not found.")
		return
	}
	surfaceKey, err := url.PathUnescape(strings.TrimSpace(r.PathValue("surfaceKey")))
	if err != nil {
		respondError(w, http.StatusNotFound, "surface_unavailable", "That workspace surface is not available.")
		return
	}
	surface, binding, ok := h.eligibleSurface(r.Context(), workspaceID, surfaceKey)
	if !ok || binding.Runtime == nil {
		respondError(w, http.StatusNotFound, "surface_unavailable", "That workspace surface is not available.")
		return
	}
	record, err := h.sessions.open(surfaceSession{
		UserID: userID, WorkspaceID: workspaceID, SurfaceKey: surface.Key,
		PluginID: surface.Owner.ID, Generation: surface.Owner.Generation,
	})
	if err != nil {
		respondError(w, http.StatusServiceUnavailable, "surface_unavailable", "That workspace surface could not be opened.")
		return
	}
	frameURL := "/api/workspace-surfaces/frames/" + url.PathEscape(record.FrameToken) + "/" + binding.EntryAsset
	_ = orihttp.RespondCreated(w, openSessionResponse{Session: record.Credential, FrameURL: frameURL, ExpiresAt: record.ExpiresAt})
}

func (h *Handler) FrameAsset(w http.ResponseWriter, r *http.Request) {
	record, err := h.sessions.frame(strings.TrimSpace(r.PathValue("frameToken")))
	if err != nil {
		respondError(w, http.StatusNotFound, "session_unknown", "This plugin surface is no longer available.")
		return
	}
	surface, binding, ok := h.eligibleSurface(r.Context(), record.WorkspaceID, record.SurfaceKey)
	if !ok || surface.Owner.ID != record.PluginID || surface.Owner.Generation != record.Generation {
		h.sessions.close(record.Credential)
		respondError(w, http.StatusGone, "session_invalidated", "This plugin surface is no longer available.")
		return
	}
	asset, err := workspacesurface.ReadAsset(binding, r.PathValue("assetPath"))
	if err != nil {
		respondError(w, http.StatusNotFound, "asset_not_found", "Plugin asset not found.")
		return
	}
	w.Header().Set("Content-Type", asset.ContentType)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; font-src 'self'; connect-src 'none'; object-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'self'")
	// Opaque sandbox documents have origin "null". ES module subresources
	// therefore need explicit CORS even though they share this random frame-token
	// path. The token authorizes assets only, credentials are never allowed, and
	// CSP still forbids fetch/connect from plugin code.
	w.Header().Set("Access-Control-Allow-Origin", "null")
	w.Header().Set("Cross-Origin-Resource-Policy", "cross-origin")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	orihttp.WriteBytes(w, asset.Data)
}

type operationRequest struct {
	Session           string          `json:"session"`
	OperationID       string          `json:"operation_id"`
	Input             json.RawMessage `json:"input"`
	ConfirmationToken string          `json:"confirmation_token,omitempty"`
}

type operationResponse struct {
	Output json.RawMessage `json:"output"`
}

func (h *Handler) InvokeOperation(w http.ResponseWriter, r *http.Request) {
	request, ok := decodeClosedJSON(w, r, maxBrokerBodyBytes, &operationRequest{})
	if !ok {
		return
	}
	input := request.(*operationRequest)
	record, err := h.sessions.credential(strings.TrimSpace(input.Session))
	if err != nil {
		respondError(w, http.StatusNotFound, "session_unknown", "This plugin surface session is not available.")
		return
	}
	userID, owned := h.ownedWorkspace(r.Context(), record.WorkspaceID)
	if !owned || userID != record.UserID {
		respondError(w, http.StatusNotFound, "session_unknown", "This plugin surface session is not available.")
		return
	}
	surface, binding, eligible := h.eligibleSurface(r.Context(), record.WorkspaceID, record.SurfaceKey)
	if !eligible || surface.Owner.Generation != record.Generation || surface.Owner.ID != record.PluginID {
		h.sessions.close(record.Credential)
		respondError(w, http.StatusGone, "session_invalidated", "This plugin surface is no longer available.")
		return
	}
	operationID := strings.TrimSpace(input.OperationID)
	operation, declared := binding.Operations[operationID]
	if !declared || operation.ID != operationID || !contains(surface.Surface.OperationIDs, operationID) {
		respondError(w, http.StatusNotFound, "operation_unknown", "That plugin operation is not available.")
		return
	}
	if operation.Policy == workspacesurface.PolicyConfirmationRequired && strings.TrimSpace(input.ConfirmationToken) == "" {
		respondError(w, http.StatusConflict, "confirmation_required", "Review and confirm this plugin action before it runs.")
		return
	}
	if operation.Policy == workspacesurface.PolicyConfirmationRequired {
		// The prototype cannot treat a caller-supplied token as authority. The
		// host-owned confirmation store lands in the production platform slice.
		respondError(w, http.StatusConflict, "confirmation_invalid", "That plugin confirmation is not valid.")
		return
	}
	if err := workspacesurface.ValidateOperationInput(operation, input.Input); err != nil {
		respondError(w, http.StatusBadRequest, "input_invalid", "The plugin operation input is invalid.")
		return
	}
	workspaceContext, err := h.resolveContext(r.Context(), record.WorkspaceID, surface)
	if err != nil {
		respondError(w, http.StatusServiceUnavailable, "surface_unavailable", "That workspace surface is not available.")
		return
	}
	workspaceContext.WorkspaceID = record.WorkspaceID
	callContext, cancel := context.WithTimeout(r.Context(), h.operationTimeout(operation.Timeout))
	defer cancel()
	result, err := binding.Runtime.Invoke(callContext, workspacesurface.Invocation{
		Workspace: workspaceContext, Operation: operationID, Input: append(json.RawMessage(nil), input.Input...),
	})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(callContext.Err(), context.DeadlineExceeded) {
			respondError(w, http.StatusGatewayTimeout, "service_timeout", "The plugin service did not answer in time.")
			return
		}
		respondError(w, http.StatusBadGateway, "service_unavailable", "The plugin service could not complete that operation.")
		return
	}
	if len(result.Output) == 0 || len(result.Output) > operation.MaxOutputBytes || !json.Valid(result.Output) {
		respondError(w, http.StatusBadGateway, "output_invalid", "The plugin service returned an invalid result.")
		return
	}
	_ = orihttp.RespondSuccess(w, operationResponse{Output: result.Output})
}

type closeSessionRequest struct {
	Session string `json:"session"`
}

func (h *Handler) CloseSession(w http.ResponseWriter, r *http.Request) {
	request, ok := decodeClosedJSON(w, r, 4<<10, &closeSessionRequest{})
	if !ok {
		return
	}
	input := request.(*closeSessionRequest)
	record, err := h.sessions.credential(strings.TrimSpace(input.Session))
	if err != nil {
		respondError(w, http.StatusNotFound, "session_unknown", "This plugin surface session is not available.")
		return
	}
	userID, owned := h.ownedWorkspace(r.Context(), record.WorkspaceID)
	if !owned || userID != record.UserID {
		respondError(w, http.StatusNotFound, "session_unknown", "This plugin surface session is not available.")
		return
	}
	h.sessions.close(record.Credential)
	orihttp.RespondNoContent(w)
}

func (h *Handler) eligibleSurface(ctx context.Context, workspaceID, key string) (workspacesurface.RegisteredSurface, workspacesurface.Binding, bool) {
	surface, ok := h.registry.Surface(strings.TrimSpace(key))
	if !ok || h.attachments == nil || !h.attachments.Attached(ctx, workspaceID, surface) {
		return workspacesurface.RegisteredSurface{}, workspacesurface.Binding{}, false
	}
	binding, ok := h.registry.Binding(surface.Key)
	return surface, binding, ok
}

func (h *Handler) ownedWorkspace(ctx context.Context, workspaceID string) (string, bool) {
	if h == nil || h.workspaces == nil || strings.TrimSpace(workspaceID) == "" {
		return "", false
	}
	ws, err := h.workspaces.Get(workspaceID)
	if err != nil || ws == nil {
		return "", false
	}
	userID, err := h.users.CurrentUserID(ctx)
	if err != nil {
		return "", false
	}
	userID = strings.TrimSpace(userID)
	if userID == "" {
		userID = userprofile.LocalUserID
	}
	owner := strings.TrimSpace(ws.OwnerUserID)
	if owner == "" {
		owner = userprofile.LocalUserID
	}
	if !strings.EqualFold(owner, userID) {
		return "", false
	}
	return userID, true
}

func (h *Handler) resolveContext(ctx context.Context, workspaceID string, surface workspacesurface.RegisteredSurface) (workspacesurface.WorkspaceContext, error) {
	if h == nil || h.contexts == nil {
		return workspacesurface.WorkspaceContext{}, errors.New("workspace surface context is unavailable")
	}
	return h.contexts.Resolve(ctx, workspaceID, surface)
}

func (h *Handler) clock() time.Time {
	if h == nil || h.now == nil {
		return time.Now()
	}
	return h.now()
}

func (h *Handler) operationTimeout(class workspacesurface.TimeoutClass) time.Duration {
	if h == nil || h.timeoutFor == nil {
		return operationTimeout(class)
	}
	return h.timeoutFor(class)
}

func operationTimeout(class workspacesurface.TimeoutClass) time.Duration {
	switch class {
	case workspacesurface.TimeoutFast:
		return 3 * time.Second
	case workspacesurface.TimeoutLong:
		return 60 * time.Second
	default:
		return 15 * time.Second
	}
}

func unavailableStatus(now time.Time) workspacesurface.StationStatus {
	return workspacesurface.NormalizeStationStatus(workspacesurface.StationStatus{
		State: workspacesurface.StationUnavailable, Value: "Unavailable",
		Description: "This workspace surface is not available.",
	}, now)
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func decodeClosedJSON(w http.ResponseWriter, r *http.Request, maximum int64, target any) (any, bool) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maximum))
	if err != nil || len(bytes.TrimSpace(body)) == 0 {
		respondError(w, http.StatusBadRequest, "input_invalid", "The request body is invalid.")
		return nil, false
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		respondError(w, http.StatusBadRequest, "input_invalid", "The request body is invalid.")
		return nil, false
	}
	return target, true
}

type errorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func respondError(w http.ResponseWriter, status int, code, message string) {
	_ = orihttp.RespondJSON(w, status, errorResponse{Code: code, Message: message})
}

// InvalidateOwner is the lifecycle hook used before update/disable/uninstall.
func (h *Handler) InvalidateOwner(pluginID string, generation uint64) int {
	if h == nil || h.sessions == nil {
		return 0
	}
	return h.sessions.invalidateOwner(strings.TrimSpace(pluginID), generation)
}

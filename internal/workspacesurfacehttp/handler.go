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

// OperationAuthorizer is the optional runtime-grant/agent policy seam. Browser
// read-only fixture calls need no extra grant; agent/runtime operations install
// an authorizer and converge on this same broker order.
type OperationAuthorizer interface {
	Authorize(context.Context, string, string, workspacesurface.RegisteredSurface, workspacesurface.Operation) error
}

type OperationAuthorizerFunc func(context.Context, string, string, workspacesurface.RegisteredSurface, workspacesurface.Operation) error

func (f OperationAuthorizerFunc) Authorize(ctx context.Context, userID, workspaceID string, surface workspacesurface.RegisteredSurface, operation workspacesurface.Operation) error {
	if f == nil {
		return nil
	}
	return f(ctx, userID, workspaceID, surface, operation)
}

type ContextResolverFunc func(context.Context, string, workspacesurface.RegisteredSurface) (workspacesurface.WorkspaceContext, error)

func (f ContextResolverFunc) Resolve(ctx context.Context, workspaceID string, surface workspacesurface.RegisteredSurface) (workspacesurface.WorkspaceContext, error) {
	if f == nil {
		return workspacesurface.WorkspaceContext{}, errors.New("workspace surface context resolver is unavailable")
	}
	return f(ctx, workspaceID, surface)
}

type Handler struct {
	registry      *workspacesurface.Registry
	workspaces    WorkspaceStore
	users         userprofile.UserProvider
	attachments   AttachmentChecker
	contexts      ContextResolver
	authorizer    OperationAuthorizer
	sessions      *sessionStore
	confirmations *confirmationStore
	state         *workspacesurface.StateStore
	now           func() time.Time
	timeoutFor    func(workspacesurface.TimeoutClass) time.Duration
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
		attachments: attachments, contexts: contexts,
		sessions: newSessionStore(), confirmations: newConfirmationStore(), now: time.Now,
		timeoutFor: operationTimeout,
	}
}

// Register installs only generic host routes. The registry decides which plugin
// surfaces are available; no plugin can add a ServeMux pattern.
func (h *Handler) SetStateStore(store *workspacesurface.StateStore) {
	if h != nil {
		h.state = store
	}
}

func (h *Handler) SetOperationAuthorizer(authorizer OperationAuthorizer) {
	if h != nil {
		h.authorizer = authorizer
	}
}

func (h *Handler) Register(mux *http.ServeMux) {
	if h == nil || mux == nil {
		return
	}
	mux.HandleFunc("GET /api/workspaces/{workspaceID}/surfaces", h.Catalog)
	mux.HandleFunc("POST /api/workspaces/{workspaceID}/surfaces/{surfaceKey}/sessions", h.OpenSession)
	mux.HandleFunc("GET /api/workspace-surfaces/frames/{frameToken}/{assetVersion}/{assetPath...}", h.FrameAsset)
	mux.HandleFunc("POST /api/workspace-surfaces/operations", h.InvokeOperation)
	mux.HandleFunc("POST /api/workspace-surfaces/confirmations", h.ApproveConfirmation)
	mux.HandleFunc("DELETE /api/workspace-surfaces/confirmations", h.CancelConfirmation)
	mux.HandleFunc("POST /api/workspace-surfaces/state", h.State)
	mux.HandleFunc("POST /api/workspace-surfaces/intents", h.Intent)
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
	Features     catalogFeatures                `json:"features"`
}

type catalogFeatures struct {
	Confirmation bool `json:"confirmation"`
	State        bool `json:"state"`
	AskOri       bool `json:"ask_ori"`
	OpenSetup    bool `json:"open_setup"`
	Close        bool `json:"close"`
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
		Modal: surface.Surface.Modal, Polling: surface.Surface.Polling,
		Features: catalogFeatures{
			Confirmation: surface.Surface.ConfirmationEnabled, State: surface.Surface.StateEnabled,
			AskOri:    len(surface.Surface.AskOriCapabilities) > 0,
			OpenSetup: surface.Surface.SetupProviderID != "", Close: surface.Surface.CloseEnabled,
		},
		Available: surface.Available, Unavailable: surface.UnavailableCode,
	}
	if !surface.Available {
		item.Status = unavailableStatus(h.clock())
		return item
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
		CapabilityID: surface.Capability.ID, SurfaceID: surface.Surface.ID,
	})
	if err != nil {
		respondError(w, http.StatusServiceUnavailable, "surface_unavailable", "That workspace surface could not be opened.")
		return
	}
	frameURL := "/api/workspace-surfaces/frames/" + url.PathEscape(record.FrameToken) + "/" + url.PathEscape(binding.AssetVersion) + "/" + binding.EntryAsset
	_ = orihttp.RespondCreated(w, openSessionResponse{Session: record.Credential, FrameURL: frameURL, ExpiresAt: record.AbsoluteExpiresAt})
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
	if strings.TrimSpace(r.PathValue("assetVersion")) != binding.AssetVersion {
		respondError(w, http.StatusGone, "session_invalidated", "This plugin asset version is no longer available.")
		return
	}
	asset, err := workspacesurface.ReadAsset(binding, r.PathValue("assetPath"))
	if err != nil {
		respondError(w, http.StatusNotFound, "asset_not_found", "Plugin asset not found.")
		return
	}
	w.Header().Set("Content-Type", asset.ContentType)
	if asset.Path == binding.EntryAsset {
		w.Header().Set("Cache-Control", "no-store")
	} else {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
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
	if err := workspacesurface.ValidateOperationInput(operation, input.Input); err != nil {
		respondError(w, http.StatusBadRequest, "input_invalid", "The plugin operation input is invalid.")
		return
	}
	if h.authorizer != nil {
		if err := h.authorizer.Authorize(r.Context(), userID, record.WorkspaceID, surface, operation); err != nil {
			respondError(w, http.StatusForbidden, "runtime_grant_required", "This operation requires an authorized runtime grant.")
			return
		}
	}
	workspaceContext, err := h.resolveContext(r.Context(), record.WorkspaceID, surface)
	if err != nil {
		respondError(w, http.StatusServiceUnavailable, "surface_unavailable", "That workspace surface is not available.")
		return
	}
	workspaceContext.WorkspaceID = record.WorkspaceID
	confirmation := confirmationBinding{
		UserID: userID, WorkspaceID: record.WorkspaceID, PluginID: record.PluginID,
		Generation: record.Generation, CapabilityID: record.CapabilityID,
		CallerID: record.SurfaceKey, OperationID: operationID,
	}
	confirmationToken := strings.TrimSpace(input.ConfirmationToken)
	if operation.Policy != workspacesurface.PolicyConfirmationRequired && confirmationToken != "" {
		respondError(w, http.StatusConflict, "confirmation_invalid", "That plugin confirmation is not valid.")
		return
	}
	if operation.Policy == workspacesurface.PolicyConfirmationRequired {
		if confirmationToken == "" {
			confirmationID, err := h.confirmations.issue(confirmation, input.Input)
			if err != nil {
				respondError(w, http.StatusServiceUnavailable, "confirmation_unavailable", "This plugin action could not be prepared for confirmation.")
				return
			}
			respondConfirmationRequired(w, confirmationID)
			return
		}
		if err := h.confirmations.consume(confirmationToken, confirmation, input.Input); err != nil {
			respondError(w, http.StatusConflict, "confirmation_invalid", "That plugin confirmation is not valid.")
			return
		}
	}
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
	if len(result.Output) == 0 || len(result.Output) > operation.MaxOutputBytes ||
		!json.Valid(result.Output) || workspacesurface.ValidateOperationOutput(operation, result.Output) != nil {
		respondError(w, http.StatusBadGateway, "output_invalid", "The plugin service returned an invalid result.")
		return
	}
	_ = orihttp.RespondSuccess(w, operationResponse{Output: result.Output})
}

type confirmationRequest struct {
	Session        string `json:"session"`
	ConfirmationID string `json:"confirmation_id"`
}

type confirmationResponse struct {
	ConfirmationToken string `json:"confirmation_token"`
}

func (h *Handler) ApproveConfirmation(w http.ResponseWriter, r *http.Request) {
	request, ok := decodeClosedJSON(w, r, 4<<10, &confirmationRequest{})
	if !ok {
		return
	}
	input := request.(*confirmationRequest)
	binding, ok := h.confirmationCaller(r.Context(), input.Session)
	if !ok {
		respondError(w, http.StatusNotFound, "session_unknown", "This plugin surface session is not available.")
		return
	}
	token, err := h.confirmations.approve(strings.TrimSpace(input.ConfirmationID), binding)
	if err != nil {
		respondError(w, http.StatusConflict, "confirmation_invalid", "That plugin confirmation is not valid.")
		return
	}
	_ = orihttp.RespondSuccess(w, confirmationResponse{ConfirmationToken: token})
}

func (h *Handler) CancelConfirmation(w http.ResponseWriter, r *http.Request) {
	request, ok := decodeClosedJSON(w, r, 4<<10, &confirmationRequest{})
	if !ok {
		return
	}
	input := request.(*confirmationRequest)
	binding, ok := h.confirmationCaller(r.Context(), input.Session)
	if !ok || !h.confirmations.cancel(strings.TrimSpace(input.ConfirmationID), binding) {
		respondError(w, http.StatusNotFound, "confirmation_invalid", "That plugin confirmation is not available.")
		return
	}
	orihttp.RespondNoContent(w)
}

func (h *Handler) confirmationCaller(ctx context.Context, sessionToken string) (confirmationBinding, bool) {
	record, err := h.sessions.credential(strings.TrimSpace(sessionToken))
	if err != nil {
		return confirmationBinding{}, false
	}
	userID, owned := h.ownedWorkspace(ctx, record.WorkspaceID)
	if !owned || userID != record.UserID {
		return confirmationBinding{}, false
	}
	surface, _, eligible := h.eligibleSurface(ctx, record.WorkspaceID, record.SurfaceKey)
	if !eligible || surface.Owner.ID != record.PluginID || surface.Owner.Generation != record.Generation {
		return confirmationBinding{}, false
	}
	return confirmationBinding{
		UserID: userID, WorkspaceID: record.WorkspaceID, PluginID: record.PluginID,
		Generation: record.Generation, CapabilityID: record.CapabilityID, CallerID: record.SurfaceKey,
	}, true
}

type intentRequest struct {
	Session string `json:"session"`
	Type    string `json:"type"`
	Context string `json:"context,omitempty"`
}

type intentResponse struct {
	Intent               string   `json:"intent"`
	WorkspaceID          string   `json:"workspace_id"`
	PluginContext        string   `json:"plugin_context,omitempty"`
	RequiredCapabilities []string `json:"required_capabilities,omitempty"`
	ProviderID           string   `json:"provider_id,omitempty"`
}

func (h *Handler) Intent(w http.ResponseWriter, r *http.Request) {
	request, ok := decodeClosedJSON(w, r, 8<<10, &intentRequest{})
	if !ok {
		return
	}
	input := request.(*intentRequest)
	record, err := h.sessions.credential(strings.TrimSpace(input.Session))
	if err != nil {
		respondError(w, http.StatusNotFound, "session_unknown", "This plugin surface session is not available.")
		return
	}
	userID, owned := h.ownedWorkspace(r.Context(), record.WorkspaceID)
	surface, _, eligible := h.eligibleSurface(r.Context(), record.WorkspaceID, record.SurfaceKey)
	if !owned || userID != record.UserID || !eligible {
		respondError(w, http.StatusForbidden, "intent_unavailable", "That host action is not available for this surface.")
		return
	}
	response := intentResponse{WorkspaceID: record.WorkspaceID}
	switch strings.TrimSpace(input.Type) {
	case "ask_ori":
		if len(surface.Surface.AskOriCapabilities) == 0 || len([]byte(input.Context)) > 2000 || strings.ContainsRune(input.Context, 0) {
			respondError(w, http.StatusBadRequest, "intent_unavailable", "Ask Ori is not available for this surface.")
			return
		}
		response.Intent = "ask_ori"
		response.PluginContext = strings.TrimSpace(input.Context)
		response.RequiredCapabilities = append([]string(nil), surface.Surface.AskOriCapabilities...)
	case "open_setup":
		if surface.Surface.SetupProviderID == "" {
			respondError(w, http.StatusBadRequest, "intent_unavailable", "Setup is not available for this surface.")
			return
		}
		response.Intent = "open_setup"
		response.ProviderID = surface.Surface.SetupProviderID
	default:
		respondError(w, http.StatusBadRequest, "intent_unavailable", "That host action is not available for this surface.")
		return
	}
	_ = orihttp.RespondSuccess(w, response)
}

type stateRequest struct {
	Session          string          `json:"session"`
	Action           string          `json:"action"`
	Key              string          `json:"key"`
	SchemaVersion    int             `json:"schema_version,omitempty"`
	ExpectedRevision string          `json:"expected_revision,omitempty"`
	Value            json.RawMessage `json:"value,omitempty"`
}

func (h *Handler) State(w http.ResponseWriter, r *http.Request) {
	request, ok := decodeClosedJSON(w, r, maxBrokerBodyBytes, &stateRequest{})
	if !ok {
		return
	}
	input := request.(*stateRequest)
	record, err := h.sessions.credential(strings.TrimSpace(input.Session))
	if err != nil {
		respondError(w, http.StatusNotFound, "session_unknown", "This plugin surface session is not available.")
		return
	}
	userID, owned := h.ownedWorkspace(r.Context(), record.WorkspaceID)
	surface, _, eligible := h.eligibleSurface(r.Context(), record.WorkspaceID, record.SurfaceKey)
	if !owned || userID != record.UserID || !eligible || !surface.Surface.StateEnabled || h.state == nil {
		respondError(w, http.StatusForbidden, "state_unavailable", "Plugin state is not available for this surface.")
		return
	}
	var result workspacesurface.StateValue
	switch strings.TrimSpace(input.Action) {
	case "get":
		result, err = h.state.Get(record.PluginID, record.WorkspaceID, strings.TrimSpace(input.Key))
	case "set":
		result, err = h.state.Set(record.PluginID, record.WorkspaceID, strings.TrimSpace(input.Key), input.SchemaVersion, input.ExpectedRevision, input.Value)
	case "delete":
		result, err = h.state.Delete(record.PluginID, record.WorkspaceID, strings.TrimSpace(input.Key), input.ExpectedRevision)
	default:
		err = workspacesurface.ErrStateInvalid
	}
	if err != nil {
		switch {
		case errors.Is(err, workspacesurface.ErrStateConflict):
			respondError(w, http.StatusConflict, "state_conflict", "Plugin state changed. Read it again before saving.")
		case errors.Is(err, workspacesurface.ErrStateQuotaExceeded):
			respondError(w, http.StatusRequestEntityTooLarge, "state_quota_exceeded", "Plugin state exceeds its storage quota.")
		default:
			respondError(w, http.StatusBadRequest, "state_invalid", "The plugin state request is invalid.")
		}
		return
	}
	_ = orihttp.RespondSuccess(w, result)
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
	Code           string `json:"code"`
	Message        string `json:"message"`
	ConfirmationID string `json:"confirmation_id,omitempty"`
}

func respondError(w http.ResponseWriter, status int, code, message string) {
	_ = orihttp.RespondJSON(w, status, errorResponse{Code: code, Message: message})
}

func respondConfirmationRequired(w http.ResponseWriter, confirmationID string) {
	_ = orihttp.RespondJSON(w, http.StatusConflict, errorResponse{
		Code: "confirmation_required", Message: "Review and confirm this plugin action before it runs.",
		ConfirmationID: confirmationID,
	})
}

// InvalidateOwner is the lifecycle hook used before update/disable/uninstall or
// a service generation restart.
func (h *Handler) InvalidateOwner(pluginID string, generation uint64) int {
	if h == nil || h.sessions == nil {
		return 0
	}
	pluginID = strings.TrimSpace(pluginID)
	h.confirmations.invalidateOwner(pluginID, generation)
	return h.sessions.invalidateOwner(pluginID, generation)
}

// InvalidateCapability closes sessions immediately when a workspace detaches a
// contributed capability.
func (h *Handler) InvalidateCapability(workspaceID, pluginID, capabilityID string) int {
	if h == nil || h.sessions == nil {
		return 0
	}
	workspaceID = strings.TrimSpace(workspaceID)
	pluginID = strings.TrimSpace(pluginID)
	capabilityID = strings.TrimSpace(capabilityID)
	h.confirmations.invalidateCapability(workspaceID, pluginID, capabilityID)
	return h.sessions.invalidateCapability(workspaceID, pluginID, capabilityID)
}

// InvalidateServiceRestart uses the same generation fence as update/disable:
// an open frame never silently crosses a native process replacement.
func (h *Handler) InvalidateServiceRestart(pluginID string, generation uint64) int {
	return h.InvalidateOwner(pluginID, generation)
}

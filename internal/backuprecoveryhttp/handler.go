// Package backuprecoveryhttp owns user-control endpoints, not agent tools.
// Registering these routes does not activate the adapter or grant metadata read
// authority. Server wiring follows advisor isolation and lifecycle coverage.
package backuprecoveryhttp

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"github.com/johnjallday/ori-agent/internal/backuprecovery"
	"github.com/johnjallday/ori-agent/internal/connectionshttp"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/userprofile"
)

const (
	APIPrefix    = "/api/workspaces/{workspaceID}/backup-recovery"
	ActionHeader = "X-Ori-Backup-Action"
	ActionValue  = "user-control"
)

type Domain interface {
	GetStatus(context.Context, string, string) (backuprecovery.Status, error)
	GetSettings(context.Context, string, string) (backuprecovery.Settings, error)
	GetHistory(context.Context, string, string, uint64) (backuprecovery.History, error)
	Pick(context.Context, string, string, string) (string, error)
	DescribeCredential(context.Context, string, string, string, string) (backuprecovery.CredentialReference, error)
	Review(context.Context, string, string, backuprecovery.ReviewRequest) (backuprecovery.Review, error)
	Connect(context.Context, string, string, backuprecovery.ConnectRequest) (backuprecovery.OperationResult, error)
	Refresh(context.Context, string, string, backuprecovery.RefreshRequest) (backuprecovery.OperationResult, error)
	Revoke(context.Context, string, string, backuprecovery.ControlRequest, bool) (backuprecovery.OperationResult, error)
	ReviewMonitoring(context.Context, string, string, backuprecovery.MonitoringReviewRequest) (backuprecovery.MonitoringReview, error)
	SetMonitoring(context.Context, string, string, backuprecovery.MonitoringChangeRequest) (backuprecovery.OperationResult, error)
}
type Handler struct {
	service Domain
	users   userprofile.UserProvider
	local   *connectionshttp.OriginGuard
}

func NewHandler(service Domain, users userprofile.UserProvider) *Handler {
	if users == nil {
		users = userprofile.LocalUserProvider{}
	}
	return &Handler{service, users, connectionshttp.NewOriginGuard()}
}

func (h *Handler) Register(mux *http.ServeMux) {
	for suffix, method := range map[string]string{"": "GET", "/guide": "GET", "/settings": "GET", "/history": "GET", "/selection": "POST", "/credential": "POST", "/review": "POST", "/connect": "POST", "/refresh": "POST", "/revoke": "POST", "/monitoring/review": "POST", "/monitoring": "POST"} {
		mux.HandleFunc(method+" "+APIPrefix+suffix, h.serve(suffix, method))
	}
	mux.HandleFunc("DELETE "+APIPrefix, h.serve("/delete", http.MethodDelete))
}
func (h *Handler) scope(w http.ResponseWriter, r *http.Request, mutating bool) (string, string, bool) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if h == nil || h.local == nil || !LocalRequestAllowed(r, mutating) {
		deny(w)
		return "", "", false
	}
	if h.service == nil {
		respondError(w, backuprecovery.ErrUnavailable)
		return "", "", false
	}
	owner, err := h.users.CurrentUserID(r.Context())
	if err != nil {
		respondError(w, backuprecovery.ErrUnavailable)
		return "", "", false
	}
	if owner != "local" {
		respondError(w, backuprecovery.ErrWorkspaceUnavailable)
		return "", "", false
	}
	workspace := r.PathValue("workspaceID")
	if workspace == "" || len(workspace) > 128 {
		respondError(w, backuprecovery.ErrWorkspaceUnavailable)
		return "", "", false
	}
	for _, c := range workspace {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			respondError(w, backuprecovery.ErrWorkspaceUnavailable)
			return "", "", false
		}
	}
	return owner, workspace, true
}
func effectivePort(u *url.URL) string {
	if u.Port() != "" {
		return u.Port()
	}
	if u.Scheme == "https" {
		return "443"
	}
	return "80"
}
func deny(w http.ResponseWriter) {
	_ = orihttp.RespondJSON(w, http.StatusForbidden, orihttp.ErrorResponse{Success: false, Code: "request_rejected", Error: "This request must come from Ori's local page."})
}
func respondError(w http.ResponseWriter, err error) {
	status, code, message := http.StatusServiceUnavailable, "backup_unavailable", "Backup & Recovery is unavailable."
	switch {
	case errors.Is(err, backuprecovery.ErrWorkspaceUnavailable):
		status, code, message = http.StatusNotFound, "not_found", "Workspace not found."
	case errors.Is(err, backuprecovery.ErrInvalidRequest):
		status, code, message = http.StatusBadRequest, "invalid_request", "The backup request is invalid."
	case errors.Is(err, backuprecovery.ErrConfigurationChanged):
		status, code, message = http.StatusConflict, "configuration_changed", "Backup settings changed. Review them again."
	case errors.Is(err, backuprecovery.ErrReviewExpired):
		status, code, message = http.StatusConflict, "review_expired", "This approval expired. Review the selection again."
	case errors.Is(err, backuprecovery.ErrRemovalPending):
		status, code, message = http.StatusConflict, "removal_pending", "Capability removal is pending. Metadata access remains fenced; retry capability removal."
	case errors.Is(err, backuprecovery.ErrBusy):
		status, code, message = http.StatusConflict, "check_in_progress", "A metadata check is still in progress."
	case errors.Is(err, backuprecovery.ErrLocalReviewRequired):
		status, code, message = http.StatusConflict, "review_required", "Review local metadata access before checking."
	case errors.Is(err, backuprecovery.ErrCredentialUnavailable):
		message = "The selected credential is unavailable. Check its workspace scope and unlock its Vault outside this console."
	}
	// Neither raw errors nor request bodies are logged/returned here.
	_ = orihttp.RespondJSON(w, status, orihttp.ErrorResponse{Success: false, Code: code, Error: message})
}
func (h *Handler) serve(action, method string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != method {
			w.Header().Set("Allow", method)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		owner, workspace, ok := h.scope(w, r, method != http.MethodGet)
		if !ok {
			return
		}
		var data any
		var err error
		key := ""
		if action != "/history" && r.URL.RawQuery != "" {
			respondError(w, backuprecovery.ErrInvalidRequest)
			return
		}
		switch action {
		case "":
			key = "status"
			data, err = h.service.GetStatus(r.Context(), owner, workspace)
		case "/guide":
			_, err = h.service.GetStatus(r.Context(), owner, workspace) // scope check only; no provider read
			key = "guide"
			data = backuprecovery.ExternalSetupGuide()
		case "/settings":
			key = "settings"
			data, err = h.service.GetSettings(r.Context(), owner, workspace)
		case "/history":
			query, queryErr := url.ParseQuery(r.URL.RawQuery)
			if queryErr != nil || len(query) > 1 || (len(query) > 0 && len(query["cursor"]) != 1) {
				respondError(w, backuprecovery.ErrInvalidRequest)
				return
			}
			var cursor uint64
			if value := query.Get("cursor"); value != "" {
				cursor, err = strconv.ParseUint(value, 10, 64)
				if err != nil {
					respondError(w, backuprecovery.ErrInvalidRequest)
					return
				}
			}
			key = "history"
			data, err = h.service.GetHistory(r.Context(), owner, workspace, cursor)
		case "/selection":
			var request struct {
				Kind string `json:"kind"`
			}
			if !parseBody(w, r, &request) {
				return
			}
			key = "selection"
			var token string
			token, err = h.service.Pick(r.Context(), owner, workspace, request.Kind)
			data = map[string]any{"token": token, "selected": err == nil}
			if errors.Is(err, backuprecovery.ErrSelectionCancelled) {
				err = nil
			}
		case "/credential":
			var request struct {
				VaultID  string `json:"vault_id"`
				RecordID string `json:"record_id"`
			}
			if !parseBody(w, r, &request) {
				return
			}
			key = "credential"
			data, err = h.service.DescribeCredential(r.Context(), owner, workspace, request.VaultID, request.RecordID)
		case "/review":
			var request backuprecovery.ReviewRequest
			if !parseBody(w, r, &request) {
				return
			}
			key = "review"
			data, err = h.service.Review(r.Context(), owner, workspace, request)
		case "/connect":
			var request backuprecovery.ConnectRequest
			if !parseBody(w, r, &request) {
				return
			}
			key = "operation"
			data, err = h.service.Connect(r.Context(), owner, workspace, request)
		case "/refresh":
			var request backuprecovery.RefreshRequest
			if !parseBody(w, r, &request) {
				return
			}
			key = "operation"
			data, err = h.service.Refresh(r.Context(), owner, workspace, request)
		case "/monitoring/review":
			var request backuprecovery.MonitoringReviewRequest
			if !parseBody(w, r, &request) {
				return
			}
			key = "review"
			data, err = h.service.ReviewMonitoring(r.Context(), owner, workspace, request)
		case "/monitoring":
			var request backuprecovery.MonitoringChangeRequest
			if !parseBody(w, r, &request) {
				return
			}
			key = "operation"
			data, err = h.service.SetMonitoring(r.Context(), owner, workspace, request)
		case "/revoke", "/delete":
			var request backuprecovery.ControlRequest
			if !parseBody(w, r, &request) {
				return
			}
			key = "operation"
			data, err = h.service.Revoke(r.Context(), owner, workspace, request, action == "/delete")
		default:
			respondError(w, backuprecovery.ErrInvalidRequest)
			return
		}
		if err != nil {
			respondError(w, err)
			return
		}
		_ = orihttp.RespondJSON(w, http.StatusOK, map[string]any{"success": true, key: data})
	}
}

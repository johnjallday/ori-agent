package userhttp

import (
	"context"
	"errors"
	"net/http"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/userprofile"
)

type Handler struct {
	store    userprofile.UserStore
	provider userprofile.UserProvider
}

func NewHandler(store userprofile.UserStore, provider userprofile.UserProvider) *Handler {
	if provider == nil {
		provider = userprofile.LocalUserProvider{}
	}
	return &Handler{store: store, provider: provider}
}

func (h *Handler) Profile(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.getProfile(w, r)
	case http.MethodPut:
		h.putProfile(w, r)
	default:
		orihttp.MethodNotAllowed(w)
	}
}

func (h *Handler) getProfile(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.store == nil {
		orihttp.ServiceUnavailable(w, "user profile store is unavailable")
		return
	}
	userID, err := h.currentUserID(r.Context())
	if err != nil {
		orihttp.InternalError(w, "Failed to resolve current user: "+err.Error())
		return
	}
	profile, err := h.store.Get(r.Context(), userID)
	if errors.Is(err, userprofile.ErrNotFound) {
		profile = &userprofile.UserProfile{ID: userID}
	} else if err != nil {
		orihttp.InternalError(w, "Failed to load user profile: "+err.Error())
		return
	}
	orihttp.Success(w, map[string]any{
		"profile":                 profile,
		"allowed_preference_keys": userprofile.AllowedPreferenceKeys(),
	})
}

func (h *Handler) putProfile(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.store == nil {
		orihttp.ServiceUnavailable(w, "user profile store is unavailable")
		return
	}
	userID, err := h.currentUserID(r.Context())
	if err != nil {
		orihttp.InternalError(w, "Failed to resolve current user: "+err.Error())
		return
	}
	var req userprofile.UserProfile
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	req.ID = userID
	if err := h.store.Upsert(r.Context(), &req); err != nil {
		orihttp.BadRequest(w, err.Error())
		return
	}
	profile, err := h.store.Get(r.Context(), userID)
	if err != nil {
		orihttp.InternalError(w, "Failed to reload user profile: "+err.Error())
		return
	}
	orihttp.Success(w, map[string]any{"profile": profile})
}

func (h *Handler) currentUserID(ctx context.Context) (string, error) {
	if h.provider == nil {
		return userprofile.LocalUserID, nil
	}
	userID, err := h.provider.CurrentUserID(ctx)
	if err != nil {
		return "", err
	}
	if userID == "" {
		return userprofile.LocalUserID, nil
	}
	return userID, nil
}

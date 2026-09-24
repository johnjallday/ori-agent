package userhttp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspace"
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
	case http.MethodPatch:
		h.patchProfilePreference(w, r)
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
	if !req.UpdatedAt.IsZero() {
		versioned, ok := h.store.(interface {
			UpsertIfVersion(context.Context, *userprofile.UserProfile, time.Time) error
		})
		if !ok {
			orihttp.ServiceUnavailable(w, "versioned profile editing is unavailable")
			return
		}
		err = versioned.UpsertIfVersion(r.Context(), &req, req.UpdatedAt)
	} else {
		// Preserve the pre-existing unversioned API for older clients. The
		// current editor supplies updated_at for every existing profile.
		err = h.store.Upsert(r.Context(), &req)
	}
	if errors.Is(err, userprofile.ErrProfileConflict) {
		orihttp.Conflict(w, "The global profile changed. Refresh and review it before saving.")
		return
	}
	if err != nil {
		orihttp.BadRequest(w, "The profile could not be saved")
		return
	}
	profile, err := h.store.Get(r.Context(), userID)
	if err != nil {
		orihttp.InternalError(w, "Failed to reload user profile: "+err.Error())
		return
	}
	orihttp.Success(w, map[string]any{"profile": profile})
}

// patchProfilePreference is an explicit-user, single-field alternative to the
// legacy full-profile editor. The expected SQL version and current field value
// prevent a stale dossier action from overwriting another editor's changes.
// This route owns no assistant review metadata and cannot accept an owner ID.
func (h *Handler) patchProfilePreference(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.store == nil {
		orihttp.ServiceUnavailable(w, "user profile store is unavailable")
		return
	}
	store, ok := h.store.(interface {
		UpdateFieldCAS(context.Context, string, string, time.Time, string, string) (*userprofile.UserProfile, error)
	})
	if !ok {
		orihttp.ServiceUnavailable(w, "profile field editing is unavailable")
		return
	}
	userID, err := h.currentUserID(r.Context())
	if err != nil {
		orihttp.ServiceUnavailable(w, "current user is unavailable")
		return
	}
	var req struct {
		Field             string    `json:"field"`
		ExpectedUpdatedAt time.Time `json:"expected_updated_at"`
		ExpectedValue     string    `json:"expected_value"`
		Value             string    `json:"value"`
	}
	if r.Body == nil {
		orihttp.BadRequest(w, "A field edit is required")
		return
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&req) != nil || decoder.Decode(new(any)) != io.EOF {
		orihttp.BadRequest(w, "Invalid or oversized profile field edit")
		return
	}
	switch req.Field {
	case "preferences.response_style", "preferences.units", "preferences.language":
	default:
		orihttp.BadRequest(w, "Unsupported profile preference")
		return
	}
	if req.ExpectedUpdatedAt.IsZero() || len(req.ExpectedValue) > userprofile.AboutMaxLen || len(req.Value) > workspace.MemoryEntryMaxLen {
		orihttp.BadRequest(w, "Invalid preference version or length")
		return
	}
	if req.Value != "" {
		clean, err := workspace.ValidateMemoryText(req.Value)
		if err != nil || clean != req.Value {
			orihttp.BadRequest(w, "The preference must be one exact, safe line of at most 500 UTF-8 bytes")
			return
		}
	}
	profile, err := store.UpdateFieldCAS(r.Context(), userID, req.Field, req.ExpectedUpdatedAt, req.ExpectedValue, req.Value)
	if errors.Is(err, userprofile.ErrProfileConflict) {
		orihttp.Conflict(w, "The global profile changed. Refresh and review it before trying again.")
		return
	}
	if err != nil {
		// Do not echo submitted text or a database error into a page or log.
		orihttp.BadRequest(w, "The preference could not be saved")
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

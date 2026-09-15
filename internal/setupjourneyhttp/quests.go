package setupjourneyhttp

import (
	"context"
	"errors"
	"net/http"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/setupjourney"
)

// Authentication is resolved once at the request boundary, then held stable
// through the delegated handler's normal current-user checks.
type questRequestUser string

func (user questRequestUser) CurrentUserID(context.Context) (string, error) {
	return string(user), nil
}

type questService interface {
	ListQuests(context.Context) ([]setupjourney.QuestSummary, error)
	ForQuest(context.Context, string, string, string) (*setupjourney.Service, error)
	ForUserTemplateQuest(context.Context, string, string, string) (*setupjourney.Service, error)
	ForHostQuest(context.Context, string, string) (*setupjourney.Service, error)
}

// statusService is the non-creating read a quest-scoped service offers.
type statusService interface {
	Status(context.Context, string) (*setupjourney.JourneyProjection, bool, error)
}

type statusResponse struct {
	Exists  bool                            `json:"exists"`
	Journey *setupjourney.JourneyProjection `json:"setup_journey,omitempty"`
}

// ListQuests exposes inert catalog metadata, never progress creation or plugin
// execution. Installed plugin identity and the reviewed registry own the list.
func (h *Handler) ListQuests(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodGet) {
		return
	}
	if !noQuery(r) {
		h.writeFailure(w, r, "", "", setupjourney.FailureFor(setupjourney.ReasonInputInvalid, 0))
		return
	}
	if _, ok := h.currentUser(w, r); !ok {
		return
	}
	service, ok := h.service.(questService)
	if !ok {
		h.writeFailure(w, r, "", "", setupjourney.FailureFor(setupjourney.ReasonJourneyUnavailable, 0))
		return
	}
	quests, err := service.ListQuests(r.Context())
	if err != nil {
		h.writeFailure(w, r, "", "", err)
		return
	}
	orihttp.Success(w, struct {
		Quests []setupjourney.QuestSummary `json:"quests"`
	}{Quests: quests})
}

// ScopeQuest binds only validated owner/quest IDs for this request. It reuses
// the existing read/review/commit handlers and does not mutate the shared
// service or synthesize an accepted assistant relationship.
func (h *Handler) ScopeQuest(next func(*Handler, http.ResponseWriter, *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := h.currentUser(w, r)
		if !ok {
			return
		}
		service, ok := h.service.(questService)
		if !ok {
			h.writeFailure(w, r, "", "", setupjourney.FailureFor(setupjourney.ReasonJourneyUnavailable, 0))
			return
		}
		scoped, err := service.ForQuest(r.Context(), userID, r.PathValue("pluginID"), r.PathValue("questID"))
		if err != nil {
			h.writeFailure(w, r, "", "", err)
			return
		}
		next(NewHandler(scoped, questRequestUser(userID)), w, r)
	}
}

// ScopeUserTemplateQuest binds the current user plus an exact local template
// and host-generated attachment identity. The URL has no plugin owner segment.
func (h *Handler) ScopeUserTemplateQuest(next func(*Handler, http.ResponseWriter, *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := h.currentUser(w, r)
		if !ok {
			return
		}
		service, ok := h.service.(questService)
		if !ok {
			h.writeFailure(w, r, "", "", setupjourney.FailureFor(setupjourney.ReasonJourneyUnavailable, 0))
			return
		}
		scoped, err := service.ForUserTemplateQuest(r.Context(), userID, r.PathValue("templateID"), r.PathValue("attachmentID"))
		if err != nil {
			h.writeFailure(w, r, "", "", err)
			return
		}
		next(NewHandler(scoped, questRequestUser(userID)), w, r)
	}
}

// ScopeHostQuest binds the current user plus one quest compiled into Ori for a
// built-in template. The URL has no plugin or template attachment segment.
func (h *Handler) ScopeHostQuest(next func(*Handler, http.ResponseWriter, *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := h.currentUser(w, r)
		if !ok {
			return
		}
		service, ok := h.service.(questService)
		if !ok {
			h.writeFailure(w, r, "", "", setupjourney.FailureFor(setupjourney.ReasonJourneyUnavailable, 0))
			return
		}
		scoped, err := service.ForHostQuest(r.Context(), userID, r.PathValue("questID"))
		if err != nil {
			h.writeFailure(w, r, "", "", err)
			return
		}
		next(NewHandler(scoped, questRequestUser(userID)), w, r)
	}
}

// Status reports whether the current user has started the scoped quest and,
// if so, its reconciled projection. It never creates progress, so Home can ask
// on every render without making a quest appear started.
func (h *Handler) Status(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodGet) {
		return
	}
	if !noQuery(r) {
		h.writeFailure(w, r, "", "", setupjourney.FailureFor(setupjourney.ReasonInputInvalid, 0))
		return
	}
	userID, ok := h.currentUser(w, r)
	if !ok {
		return
	}
	service, ok := h.service.(statusService)
	if !ok {
		h.writeFailure(w, r, "", "", setupjourney.FailureFor(setupjourney.ReasonOwnerUnavailable, 0))
		return
	}
	projection, exists, err := service.Status(r.Context(), userID)
	if err != nil {
		// writeFailure attaches a fresh Read to conflicts, and Read creates the
		// root; a status request must never do that, so answer directly.
		var failure *setupjourney.Failure
		if !errors.As(err, &failure) {
			failure = setupjourney.FailureFor(setupjourney.ReasonOperationFailed, 0)
		}
		_ = orihttp.RespondJSON(w, statusForReason(failure.ReasonCode), errorResponse{Error: failure})
		return
	}
	orihttp.Success(w, statusResponse{Exists: exists, Journey: projection})
}

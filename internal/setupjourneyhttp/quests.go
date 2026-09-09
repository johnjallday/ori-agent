package setupjourneyhttp

import (
	"context"
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

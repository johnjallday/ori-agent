package personalassistant

import (
	"context"
	"errors"
	"sort"
	"strings"
)

// homeSuggestionsMax bounds how many songs one Home suggests at once.
const homeSuggestionsMax = 5

// HomeSuggestions lists, newest first, the single songs this Home's manager
// suggests opening: a song whose Set up made (or joined) this Home and stopped
// at the hand-off, and a later song whose plan adds it to this ready Home. Each
// is an offer view, so the Home's card reads the same plan or run the
// assistant's card does and presses the same Set up. A Home that is no Home of
// this owner's offers yields an empty list.
func (s *FolderDigestService) HomeSuggestions(ctx context.Context, userID, homeID string) ([]FolderOfferView, error) {
	if s == nil || s.store == nil {
		return nil, ErrRepairNeeded
	}
	homeID = strings.TrimSpace(homeID)
	if userID == "" || homeID == "" {
		return nil, ErrFolderOutcomeUnavailable
	}
	binding, err := s.store.Binding(ctx, userID)
	if errors.Is(err, ErrNotFound) {
		return []FolderOfferView{}, nil
	}
	if err != nil {
		return nil, err
	}
	doc, err := s.store.Read(ctx, userID)
	if errors.Is(err, ErrNotFound) {
		return []FolderOfferView{}, nil
	}
	if err != nil {
		return nil, err
	}
	var offers []FolderOffer
	for _, offer := range doc.Offers {
		if offer.PlacementHandoff != nil || offer.Portfolio != nil || !eligibleForOneCard(offer) {
			continue
		}
		switch {
		case offer.Status == FolderOfferAwaitingOutcome && offer.Setup != nil:
			if offer.Setup.HomeID == homeID && hasPlanLineKind(offer.Setup.Lines, FolderPlanHandoff) &&
				(offer.Setup.StopReason == FolderStopHandoffReady || offer.Setup.Opening) {
				offers = append(offers, offer)
			}
		case offer.Status == FolderOfferPending && offer.Subject.Kind == FolderChoiceProject && !doc.Tombstoned(offer.Subject.Key):
			offers = append(offers, offer)
		}
	}
	sort.SliceStable(offers, func(i, j int) bool { return offers[i].CreatedAt.After(offers[j].CreatedAt) })
	views := make([]FolderOfferView, 0, homeSuggestionsMax)
	for _, offer := range offers {
		if len(views) == homeSuggestionsMax {
			break
		}
		view := s.viewFor(ctx, userID, offer, binding.Paused)
		if offer.Setup == nil && !suggestedForHome(view.Plan, homeID) {
			continue
		}
		views = append(views, view)
	}
	return views, nil
}

// suggestedForHome says a pending song's plan is the Home's own suggestion: it
// adds the song to this Home, ready and staffed, so the one press is the open.
func suggestedForHome(plan *FolderSetupPlan, homeID string) bool {
	if plan == nil || !hasPlanLineKind(plan.Lines, FolderPlanHandoff) || len(plan.Intent.OpensProjects) == 0 ||
		plan.Intent.CreatesHome || plan.Intent.StaffsHome {
		return false
	}
	destination := plan.Destination
	if destination == nil {
		destination = plan.Intent.Destination
	}
	return destination != nil && destination.WorkspaceID == homeID
}

// folderOfferSuggestedIn is the Home an offer's song waits in: the ready Home
// its plan leads to, or the one its run stopped in at the hand-off.
func folderOfferSuggestedIn(v FolderOfferView) *FolderHomeLink {
	var link *FolderHomeLink
	switch {
	case v.Setup != nil && v.Setup.Status == FolderSetupStopped && v.Setup.StopReason == FolderStopHandoffReady:
		link = v.Setup.SuggestedIn
	case v.Setup == nil && v.Plan != nil:
		link = v.Plan.SuggestedIn
	}
	if link == nil || link.Route == "" || !validFolderHomeLink(link) {
		return nil
	}
	return link
}

// HomeSuggestionReady says the first press on a song's plan belongs in its
// Home: the plan neither makes nor staffs the Home, so the assistant's card
// leads there instead of setting anything up.
func HomeSuggestionReady(plan FolderSetupPlan) bool {
	return len(plan.Intent.OpensProjects) > 0 && !plan.Intent.CreatesHome && !plan.Intent.StaffsHome
}

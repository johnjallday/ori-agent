package server

import (
	"context"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
	"github.com/johnjallday/ori-agent/internal/sessionhttp"
)

// ObserveSetupHome reads only the existing setup witness and canonical Home.
// It deliberately avoids Plan/target resolution (which can require a current
// plugin or model) and never repairs a missing progress write.
func (h *folderSetupHost) ObserveSetupHome(ctx context.Context, req personalassistant.FolderSetupRequest) personalassistant.FolderHomeResult {
	result := personalassistant.FolderHomeResult{State: "unavailable"}
	run := req.Offer.Setup
	if h == nil || h.builder == nil || h.builder.sessionHandler == nil || run == nil || run.Intent.Destination == nil {
		return result
	}
	expected := run.Intent.Destination
	if expected.OwnerUserID != req.UserID || expected.Kind != "home" || expected.Validate() != nil {
		return result
	}
	var provider *reviewedintegration.HomeProvider
	for _, entry := range reviewedintegration.HomeProviders() {
		if entry.PluginID == expected.ProviderPlugin && entry.ProgramID == expected.ProgramID {
			value := entry
			provider = &value
		}
	}
	if provider == nil {
		return result
	}
	home, err := h.station(req.UserID, *provider)
	if err != nil || home == nil {
		return result
	}
	actual, err := folderSetupHomeDestination(home)
	if err != nil {
		return result
	}
	if expected.Status == "new" {
		w := run.HomeOperation
		if w == nil || w.Validate() != nil || w.OfferID != req.Offer.ID || w.RequestID != req.Offer.RequestID || w.PlanDigest != run.PlanDigest {
			result.State = "unproven"
			return result
		}
		id, created, err := h.builder.sessionHandler.ObservePortfolioHome(ctx, sessionhttp.PortfolioHomeReview{
			GroupTemplateID: w.GroupTemplateID, Revision: w.Revision, Name: w.Name, Token: w.ReviewToken,
		}, w.IdempotencyKey)
		if err != nil || id != home.ID {
			return result
		}
		// A successful operation that reused a concurrently-created Home is
		// not new-subject authority. Its canonical identity is still visible.
		result.Created = created
		actual.Status, actual.WorkspaceID = "new", ""
	} else if expected.Status != "existing" || expected.WorkspaceID != home.ID {
		return result
	}
	actual.RecordVersion, actual.ProgramRevision = expected.RecordVersion, expected.ProgramRevision
	if *actual != *expected || (run.HomeID != "" && run.HomeID != home.ID) {
		return personalassistant.FolderHomeResult{State: "unavailable"}
	}
	route, ok := homeSuggestionsRoute(home)
	if !ok {
		return personalassistant.FolderHomeResult{State: "unavailable"}
	}
	result.State, result.HomeID, result.Name, result.Route = "verified", home.ID, home.Name, route
	return result
}

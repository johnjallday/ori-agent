package sessionhttp

import (
	"runtime"
	"slices"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
	"github.com/johnjallday/ori-agent/internal/projectlibrary"
	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
)

// libraryIntegrationOffer names the one reviewed integration that could make a
// saved song's observed format connectable. It is guidance only: it starts no
// install, and the reviewed install quest owns every consequence. Its fields
// come from the host's compiled registry, never from the browser.
type libraryIntegrationOffer struct {
	Key         string `json:"key"`
	QuestID     string `json:"quest_id"`
	DisplayName string `json:"display_name"`
}

// libraryActivationResponse is eligibility plus the optional integration offer.
type libraryActivationResponse struct {
	projectlibrary.ActivationEligibility
	IntegrationOffer *libraryIntegrationOffer `json:"integration_offer,omitempty"`
}

// libraryIntegrationOfferFor offers a reviewed integration only when it is the
// honest remedy: eligibility reached the project-integration check (so the Home
// provider, the source and the folder are not what blocks setup), the observed
// format is one a reviewed integration supports, and this platform is supported.
// A revoked or unavailable source, an unsupported format, an ambiguous or stale
// provider, a folder owned elsewhere and an already-connected song each stay
// their own blocker and never get an install offer.
func libraryIntegrationOfferFor(result projectlibrary.ActivationEligibility, platform string) *libraryIntegrationOffer {
	if result.State != "project_provider_unavailable" || result.ObservedFormat == "" {
		return nil
	}
	key, ok := folderdigest.IntegrationKeyForProjectFormat(result.ObservedFormat)
	if !ok {
		return nil
	}
	entry, ok := reviewedintegration.Get(key)
	if !ok || !entry.ReleaseReady || !slices.Contains(entry.SupportedPlatforms, platform) {
		return nil
	}
	return &libraryIntegrationOffer{Key: entry.Key, QuestID: entry.InstallQuestID(), DisplayName: entry.DisplayName}
}

func hostPlatform() string { return runtime.GOOS + "/" + runtime.GOARCH }

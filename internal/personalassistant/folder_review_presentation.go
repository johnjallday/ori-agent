package personalassistant

import "strings"

// FolderReviewPresentation is closed, bounded display data, not a plan, subject
// witness, route or execution grant. Unknown placement is never standalone.
// It is freshly projected by the host and never accepted from a caller.
type FolderReviewPresentation struct {
	Version          int    `json:"version"`
	Kind             string `json:"kind"`              // workspace, group, unknown
	State            string `json:"state"`             // proposed, unavailable
	Effect           string `json:"effect"`            // workspace, home_library, library, unknown
	DestinationState string `json:"destination_state"` // unknown, standalone, new, existing
	DestinationName  string `json:"destination_name,omitempty"`
}

func folderReviewPresentation(offer FolderOffer, view FolderOfferView) *FolderReviewPresentation {
	p := &FolderReviewPresentation{Version: 1, Kind: "unknown", State: "unavailable", Effect: "unknown", DestinationState: "unknown"}
	if view.CreateAvailable || view.Plan != nil {
		p.State = "proposed"
		if offer.Portfolio == nil {
			p.Kind, p.Effect = "workspace", "workspace"
		} else if view.Plan != nil && view.Plan.Intent.Portfolio {
			p.Kind, p.Effect = "group", "library"
			if view.Plan.Intent.CreatesHome {
				p.Effect = "home_library"
			}
		}
	}
	var destination *FolderSetupDestination
	if offer.ConversationReview != nil {
		if offer.ConversationReview.DestinationUnavailable {
			p.State, p.Effect = "unavailable", "unknown"
			return p
		}
		destination = offer.ConversationReview.Destination
	}
	if view.Plan != nil && view.Plan.Destination != nil {
		destination = view.Plan.Destination
	}
	if destination != nil && destination.Validate() == nil {
		p.DestinationState = destination.Status
		name := []rune(strings.TrimSpace(destination.Name))
		if len(name) > 96 {
			name = append(name[:95], '…')
		}
		p.DestinationName = string(name)
	}
	return p
}

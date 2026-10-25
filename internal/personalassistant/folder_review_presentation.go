package personalassistant

import "strings"

// FolderReviewPresentation is closed, bounded display data, not a plan, subject
// witness, route or execution grant. Unknown placement is never standalone.
// It is freshly projected by the host and never accepted from a caller.
type FolderReviewPresentation struct {
	Version          int    `json:"version"`
	Kind             string `json:"kind"`              // workspace, group, structure, unknown
	State            string `json:"state"`             // proposed, unavailable
	Effect           string `json:"effect"`            // workspace, home_library, library, group_workspaces, unknown
	DestinationState string `json:"destination_state"` // unknown, standalone, new, existing
	DestinationName  string `json:"destination_name,omitempty"`
	// Availability says why an option is unavailable, or why a structure's
	// group is the plain one (FolderAvailability*). Notes are the sentences to
	// show for it, each once.
	Availability string   `json:"availability,omitempty"`
	Notes        []string `json:"notes,omitempty"`
	// A structure's group and members: GroupType is FolderGroupHome or
	// FolderGroupPlain, Members the proposed workspaces in the folder's order,
	// and MembersOmitted how many more folders the bounded list left out.
	GroupType      string               `json:"group_type,omitempty"`
	Members        []FolderReviewMember `json:"members,omitempty"`
	MembersOmitted int                  `json:"members_omitted,omitempty"`
}

// FolderReviewMember is one proposed workspace of a structure, as a card lists
// it: display text only.
type FolderReviewMember struct {
	Name         string `json:"name"`
	TypeLabel    string `json:"type_label"`
	Included     bool   `json:"included"`
	Availability string `json:"availability,omitempty"`
}

func folderReviewPresentation(offer FolderOffer, view FolderOfferView) *FolderReviewPresentation {
	if structure := offer.Structure.view(); structure != nil {
		// A structure is always proposed: what it cannot use, it says.
		p := &FolderReviewPresentation{
			Version: 1, Kind: "structure", State: "proposed", Effect: "group_workspaces",
			DestinationState: structure.Group.DestinationState, DestinationName: structure.Group.Name,
			Availability: structure.Group.Availability, Notes: structure.Notes,
			GroupType: structure.Group.Type, MembersOmitted: structure.Omitted,
		}
		if structure.Group.Type == FolderGroupLibrary {
			p.Effect = "library"
			if structure.Group.DestinationState == "new" {
				p.Effect = "home_library"
			}
		}
		for _, member := range structure.Members {
			p.Members = append(p.Members, FolderReviewMember{
				Name: member.Name, TypeLabel: member.TypeLabel, Included: member.Included, Availability: member.Availability,
			})
		}
		return p
	}
	p := &FolderReviewPresentation{Version: 1, Kind: "unknown", State: "unavailable", Effect: "unknown", DestinationState: "unknown"}
	// A software project is proposed before it has a plan: its plan comes from
	// the review the option leads to.
	if view.CreateAvailable || view.Plan != nil || isSoftwareOffer(offer) {
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

// unavailableReviewPresentation is the presentation of an option that is listed
// although nothing can be set up from it right now: its reason and the one
// sentence that says so. subject names the integration or Home it waits on.
func unavailableReviewPresentation(p *FolderReviewPresentation, reason, subject string) *FolderReviewPresentation {
	if p == nil {
		p = &FolderReviewPresentation{Version: 1, Kind: "unknown", Effect: "unknown", DestinationState: "unknown"}
	}
	p.State, p.Availability = "unavailable", reason
	// Nothing is proposed in place of a single option, so a Home that cannot
	// be used is worded as plainly unavailable here.
	worded := reason
	if worded == FolderAvailabilityProviderUnverified || worded == FolderAvailabilityProviderNotInstalled {
		worded = FolderAvailabilityPlanUnavailable
	}
	if note := FolderAvailabilityCopy(worded, subject); note != "" {
		p.Notes = []string{note}
	}
	return p
}

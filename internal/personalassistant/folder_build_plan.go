package personalassistant

import (
	"context"
	"strings"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
	"github.com/johnjallday/ori-agent/internal/folderdigest"
)

// BuildFolderNeed is one plugin a recognized folder's setup needs, and where
// it stands now: FolderInstallReady, Install, Enable or Update.
type BuildFolderNeed struct {
	// Kind is FolderPlanIntegration or FolderPlanProvider.
	Kind  string `json:"kind"`
	Name  string `json:"name"`
	State string `json:"state"`
}

// BuildFolderHome is the Home the workspace goes into: one Set it up creates
// ("new"), or the owner's own ("existing").
type BuildFolderHome struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

// BuildFolderPlan is the plan Home's card shows for the same folder, read for
// the folder a workspace build attached: the host's lines and their digest,
// what they need, the Home and the placement. Reading it creates nothing.
type BuildFolderPlan struct {
	Lines     []FolderPlanLine  `json:"lines"`
	Digest    string            `json:"digest,omitempty"`
	Needs     []BuildFolderNeed `json:"needs,omitempty"`
	Home      *BuildFolderHome  `json:"home,omitempty"`
	Placement string            `json:"placement,omitempty"`
	// Evidence is the digest's own reason for the suggestion, the "Why this
	// suggestion?" line: what it saw, never a path.
	Evidence string `json:"evidence,omitempty"`
	// Ready is true when Set it up can run this plan now.
	Ready bool `json:"ready"`
	// Unavailable says, in a proposal's words (FolderAvailability*), why there
	// is no plan; UnavailableSubject names what it waits on.
	Unavailable        string `json:"unavailable,omitempty"`
	UnavailableSubject string `json:"unavailable_subject,omitempty"`
}

// BuildPlan reads the setup plan for a recognized project in a held selection,
// exactly as a conversation's review options do: the canonical review offer is
// prepared but never stored, and the host plans it. workspaceName is the name
// the build's draft gives the workspace ("" names it after the folder), so the
// plan and its digest say the name Set it up will create.
//
// Only a single project with a reviewed capability is planned here; anything
// else, and a host that cannot plan it right now, comes back Unavailable.
func (s *FolderObservationService) BuildPlan(ctx context.Context, target foldercontext.Target, observationID, candidateID, workspaceName string) (*BuildFolderPlan, error) {
	held, _, err := s.resolve(ctx, target, observationID)
	if err != nil {
		return nil, err
	}
	offer, err := s.prepareReviewOffer(ctx, held, target, candidateID)
	if err != nil {
		return nil, err
	}
	unavailable := func(reason string) *BuildFolderPlan {
		if reason == "" {
			reason = FolderAvailabilityPlanUnavailable
		}
		return &BuildFolderPlan{Lines: []FolderPlanLine{}, Unavailable: reason, UnavailableSubject: folderAvailabilitySubject(offer, reason)}
	}
	if offer.Structure != nil || offer.Portfolio != nil || !isProjectCapabilityOffer(offer) {
		return unavailable(""), nil
	}
	offer.ConversationReview.WorkspaceName = strings.TrimSpace(workspaceName)
	view := s.digest.describeOffer(ctx, offer, held.binding.Paused)
	s.digest.attachSetupPlan(ctx, target.UserID, offer, &view)
	row, capability := folderdigest.ProjectCapabilityFor(folderdigest.Shape(offer.Subject.Shape), offer.Subject.MarkerName, offer.Subject.DominantExtension)
	if view.Plan == nil || view.Plan.Digest == "" {
		plan := unavailable(view.Availability)
		// A new Home is made from its provider's own template: with the
		// provider not installed there is no Home to plan yet, exactly as on
		// Home's card.
		if blocker := offer.ConversationReview.DestinationBlocker; blocker != nil && blocker.HomeProviderKey != "" {
			plan.Unavailable = FolderAvailabilityProviderNotInstalled
		}
		// A plugin the plan waits on is named as the plugin, not its Home.
		switch plan.Unavailable {
		case FolderAvailabilityProviderNotInstalled, FolderAvailabilityProviderUnverified:
			if capability && row.Offer.HomeProviderName != "" {
				plan.UnavailableSubject = row.Offer.HomeProviderName
			}
		}
		plan.Evidence = view.Reason
		return plan, nil
	}
	plan := &BuildFolderPlan{
		Lines: append([]FolderPlanLine(nil), view.Plan.Lines...), Digest: view.Plan.Digest,
		Placement: view.Plan.Intent.Placement, Evidence: view.Reason, Ready: true,
	}
	if capability {
		if state := view.Plan.Intent.Integration; state != "" {
			plan.Needs = append(plan.Needs, BuildFolderNeed{Kind: FolderPlanIntegration, Name: row.Offer.IntegrationName + " integration", State: state})
		}
		if state := view.Plan.Intent.Provider; state != "" && row.Offer.HomeProviderName != "" {
			plan.Needs = append(plan.Needs, BuildFolderNeed{Kind: FolderPlanProvider, Name: row.Offer.HomeProviderName, State: state})
		}
	}
	if destination := view.Plan.Destination; destination != nil && (destination.Status == "new" || destination.Status == "existing") {
		plan.Home = &BuildFolderHome{Name: destination.Name, Status: destination.Status}
	}
	return plan, nil
}

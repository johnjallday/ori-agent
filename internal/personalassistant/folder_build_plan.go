package personalassistant

import (
	"context"

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
	Visual    *FolderSetupVisual `json:"visual,omitempty"`
	Readiness *FolderSetupVisual `json:"readiness,omitempty"`
	Lines     []FolderPlanLine   `json:"lines"`
	Digest    string             `json:"digest,omitempty"`
	Needs     []BuildFolderNeed  `json:"needs,omitempty"`
	Home      *BuildFolderHome   `json:"home,omitempty"`
	Placement string             `json:"placement,omitempty"`
	// Evidence is the digest's own reason for the suggestion, the "Why this
	// suggestion?" line: what it saw, never a path.
	Evidence string `json:"evidence,omitempty"`
	// Ready is true when Set it up can run this plan now.
	Ready bool `json:"ready"`
	// Unavailable says, in a proposal's words (FolderAvailability*), why there
	// is no plan; UnavailableSubject names what it waits on.
	Unavailable        string `json:"unavailable,omitempty"`
	UnavailableSubject string `json:"unavailable_subject,omitempty"`
	// Hire is who Hire and start hires for the folder and BriefDefault the goal
	// its question starts with, as on Home's card; neither is in the digest.
	Hire         *FolderHire `json:"hire,omitempty"`
	BriefDefault string      `json:"brief_default,omitempty"`
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
	// Named as Set it up's review will name it, so the digests are the same.
	ctx = WithFolderReviewWorkspaceName(ctx, workspaceName)
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
	s.recheckSetupFacts(ctx, target, offer)
	view := s.digest.describeOffer(ctx, offer, held.binding.Paused)
	s.digest.attachSetupPlan(ctx, target.UserID, offer, &view)
	// BuildPlan also runs in attachment responses. Preserve those existing
	// planning reads; only an explicit check may acquire independent release
	// facts after a prerequisite prevented the complete plan.
	if ctx.Value(folderSetupRecheckKey{}) != nil {
		s.digest.attachSetupReadiness(ctx, target.UserID, offer, &view)
	}
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
		plan.Readiness = CloneFolderSetupVisual(view.Readiness)
		return plan, nil
	}
	plan := &BuildFolderPlan{
		Lines: append([]FolderPlanLine(nil), view.Plan.Lines...), Digest: view.Plan.Digest,
		Placement: view.Plan.Intent.Placement, Evidence: view.Reason, Ready: true,
		Needs:  folderPlanNeeds(offer.Subject, view.Plan.Intent),
		Visual: CloneFolderSetupVisual(view.Plan.Visual),
	}
	if hire := view.Plan.Hire; hire != nil {
		copied := *hire
		plan.Hire = &copied
	}
	plan.BriefDefault = view.Plan.BriefDefault
	if destination := view.Plan.Destination; destination != nil && (destination.Status == "new" || destination.Status == "existing") {
		plan.Home = &BuildFolderHome{Name: destination.Name, Status: destination.Status}
	}
	return plan, nil
}

// folderPlanNeeds are the plugins a host plan needs, as both entries say them:
// the project's integration and its Home's provider, each in the state the
// plan's intent gives it (ready, install, enable, update). None for a project
// without a host capability row.
func folderPlanNeeds(subject FolderCandidateRecord, intent FolderSetupIntent) []BuildFolderNeed {
	row, ok := folderdigest.ProjectCapabilityFor(folderdigest.Shape(subject.Shape), subject.MarkerName, subject.DominantExtension)
	if !ok || row.Offer == nil {
		return nil
	}
	var needs []BuildFolderNeed
	if intent.Integration != "" {
		needs = append(needs, BuildFolderNeed{Kind: FolderPlanIntegration, Name: row.Offer.IntegrationName + " integration", State: intent.Integration})
	}
	if intent.Provider != "" && row.Offer.HomeProviderName != "" {
		needs = append(needs, BuildFolderNeed{Kind: FolderPlanProvider, Name: row.Offer.HomeProviderName, State: intent.Provider})
	}
	return needs
}

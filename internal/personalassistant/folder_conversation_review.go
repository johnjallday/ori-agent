package personalassistant

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
	"github.com/johnjallday/ori-agent/internal/folderdigest"
	"github.com/johnjallday/ori-agent/internal/logger"
)

var ErrFolderReviewPending = errors.New("another folder setup review is pending; finish or close that review first")

// FolderConversationReview is provenance, not authority. Only the owning
// canonical conversation and a still-live selection can authorize a new setup.
// No source path is persisted here or exposed by FolderOfferView.
type FolderConversationReview struct {
	Target                 foldercontext.Target    `json:"target"`
	ObservationID          string                  `json:"observation_id"`
	CandidateID            string                  `json:"candidate_id"`
	CandidateIdentity      string                  `json:"candidate_identity"`
	Destination            *FolderSetupDestination `json:"destination,omitempty"`
	DestinationUnavailable bool                    `json:"destination_unavailable,omitempty"`
	// DestinationBlocker says why the destination is unavailable when the user
	// can clear it, by key. Absent for an unavailability the host cannot name.
	DestinationBlocker *FolderDestinationBlocker `json:"destination_blocker,omitempty"`
	Operation          string                    `json:"operation,omitempty"`
	DestinationID      string                    `json:"destination_id,omitempty"`
	// Structure is what the user changed in Adjust on a collection's proposal,
	// kept so a refreshed review proposes the same group, names and members.
	Structure *FolderStructureAdjustment `json:"structure,omitempty"`
	// WorkspaceName is the name the user gave the new workspace in the review
	// (a workspace build's draft). Empty names it after the folder, as Home's
	// card does. The plan, its digest and the run all use it.
	WorkspaceName string `json:"workspace_name,omitempty"`
}

// ConfigureConversationReviews is wired once at host construction. The guard
// validates canonical state; the lease shares the turn/detach concurrency gate.
func (s *FolderObservationService) ConfigureConversationReviews(
	guard func(context.Context, FolderOffer) error,
	lease func(foldercontext.Target) (func(), bool),
) {
	s.reviewGuard, s.reviewLease = guard, lease
	s.digest.conversationSource = s.reviewSource
	s.digest.conversationLease = s.acquireReview
}

// reviewAuthority is the guard and lease that vouch for a review target: a
// workspace build's own for a build review, the conversation's otherwise.
// Neither ever vouches for the other's reviews.
func (s *FolderObservationService) reviewAuthority(target foldercontext.Target) (func(context.Context, FolderOffer) error, func(foldercontext.Target) (func(), bool)) {
	if IsBuildReview(target) {
		return s.buildGuard, s.buildLease
	}
	return s.reviewGuard, s.reviewLease
}

func (s *FolderObservationService) acquireReview(ctx context.Context, offer FolderOffer) (func(), error) {
	if offer.ConversationReview == nil {
		return func() {}, nil
	}
	guard, lease := s.reviewAuthority(offer.ConversationReview.Target)
	if guard == nil || lease == nil {
		return nil, ErrFolderSelection
	}
	release, ok := lease(offer.ConversationReview.Target)
	if !ok {
		return nil, ErrFolderScanBusy
	}
	if err := guard(ctx, offer); err != nil {
		release()
		return nil, ErrFolderSelection
	}
	if _, ok := s.reviewSource(offer); !ok {
		release()
		return nil, ErrFolderSelection
	}
	return release, nil
}

func (s *FolderObservationService) reviewSource(offer FolderOffer) (string, bool) {
	review := offer.ConversationReview
	if review == nil {
		return "", false
	}
	guard, _ := s.reviewAuthority(review.Target)
	if guard == nil {
		return "", false
	}
	ctx := context.Background()
	if err := guard(ctx, offer); err != nil {
		return "", false
	}
	held, _, err := s.resolve(ctx, review.Target, review.ObservationID)
	if err != nil || FolderKey(held.root) != offer.FolderKey || held.identity != offer.RootIdentity {
		return "", false
	}
	candidate, ok := observedReviewCandidate(held, review.CandidateID)
	if !ok || FolderKey(candidate.Path) != offer.Subject.Key {
		return "", false
	}
	canonical, err := s.digest.deps.ValidateRoot(candidate.Path)
	identity, identityErr := portfolioDirectoryIdentity(candidate.Path)
	if err != nil || canonical != candidate.Path || identityErr != nil || identity != review.CandidateIdentity {
		return "", false
	}
	return held.root, true
}

func observedReviewCandidate(held heldFolderObservation, id string) (folderdigest.Candidate, bool) {
	// Only candidates actually disclosed in the bounded preview are selectable.
	for _, shown := range held.observation.Projects {
		if shown.ID != id {
			continue
		}
		for index, candidate := range held.result.Candidates {
			if fmt.Sprintf("candidate-%d", index) == id {
				return candidate, true
			}
		}
	}
	return folderdigest.Candidate{}, false
}

// Review creates/reuses an ordinary canonical offer, but never decides it. The
// caller must append the returned ID to the canonical conversation before its
// controls can authorize anything. A failed append can close this pending offer.
func (s *FolderObservationService) Review(ctx context.Context, source foldercontext.Target, conversationID, observationID, candidateID string) (FolderOfferView, error) {
	if conversationID == "" {
		return FolderOfferView{}, ErrFolderSelection
	}
	held, _, err := s.resolve(ctx, source, observationID)
	if err != nil {
		return FolderOfferView{}, err
	}
	target := source
	target.ConversationID, target.DraftID = conversationID, ""
	d := s.digest
	// A review opened again without new Adjust choices keeps the ones its
	// pending review already has. If they no longer fit (the Home they chose is
	// gone), the proposal is made afresh rather than refused.
	carried := false
	if folderStructureAdjustment(ctx) == nil {
		if doc, readErr := d.store.Read(ctx, source.UserID); readErr == nil {
			for _, prior := range doc.Offers {
				review := prior.ConversationReview
				if prior.Status == FolderOfferPending && review != nil && review.Target == target &&
					review.ObservationID == observationID && review.CandidateID == candidateID && review.Structure != nil {
					ctx, carried = WithFolderStructureAdjustment(ctx, review.Structure), true
				}
			}
		}
	}
	offer, err := s.prepareReviewOffer(ctx, held, target, candidateID)
	if carried && errors.Is(err, ErrFolderStructureAdjustment) {
		offer, err = s.prepareReviewOffer(context.WithValue(ctx, folderStructureAdjustmentKey{}, (*FolderStructureAdjustment)(nil)), held, target, candidateID)
	}
	if err != nil {
		return FolderOfferView{}, err
	}
	offer.ID = d.deps.NewID()
	var result FolderOffer
	_, err = d.store.Mutate(ctx, source.UserID, func(doc *FolderDigestDocument) error {
		// Reuse a review/result for this exact conversation and candidate. A fresh
		// explicit pick can recover a confirmed setup, never adopt a replacement dir.
		for i := range doc.Offers {
			prior := &doc.Offers[i]
			// A saved arrangement's review belongs to that arrangement: a manual
			// review never adopts, refreshes or re-targets it. Only its confirmed,
			// unfinished run is recovered here, like any confirmed run, when its
			// folder is picked again after the held folder was lost.
			if prior.ConversationReview == nil || prior.ConversationReview.Target != target ||
				(prior.Structure.Arranged() && prior.Status != FolderOfferAwaitingOutcome) {
				continue
			}
			if prior.FolderKey == offer.FolderKey && prior.Subject.Key == offer.Subject.Key &&
				prior.RootIdentity == offer.RootIdentity && prior.ConversationReview.CandidateIdentity == offer.ConversationReview.CandidateIdentity &&
				prior.ConversationReview.Operation == offer.ConversationReview.Operation && prior.ConversationReview.DestinationID == offer.ConversationReview.DestinationID &&
				((prior.Status == FolderOfferPending && prior.ConversationReview.ObservationID == observationID && prior.ConversationReview.CandidateID == candidateID) || prior.Status == FolderOfferAwaitingOutcome || prior.Status == FolderOfferResolved) {
				// Recovering the folder is not consent to retarget an already
				// confirmed run or completed outcome. An explicit pending Review
				// may refresh material disclosure, changing its confirmation digest.
				if prior.Status != FolderOfferPending {
					offer.ConversationReview.Destination = prior.ConversationReview.Destination
					offer.ConversationReview.DestinationUnavailable = prior.ConversationReview.DestinationUnavailable
					offer.ConversationReview.DestinationBlocker = prior.ConversationReview.DestinationBlocker.clone()
					offer.ConversationReview.Structure = prior.ConversationReview.Structure.clone()
					// A confirmed run creates the name it was planned with.
					offer.ConversationReview.WorkspaceName = prior.ConversationReview.WorkspaceName
				} else {
					// A pending review's proposal follows the review: what is
					// available now, and what the user chose in Adjust.
					prior.Structure = offer.Structure.stored()
				}
				prior.ConversationReview = offer.ConversationReview
				result = *prior
				return nil
			}
		}
		if pending := doc.Pending(); pending != nil {
			// An open arrangement review is closed explicitly (Keep chatting), never
			// silently retired by an unrelated manual review. This conversation's
			// own review, and a card waiting on Home for this very project, give
			// way to the review the user just asked for here.
			if !ownConversationReview(pending, target) && !sameProjectHomeOffer(pending, offer.Subject.Key) {
				return ErrFolderReviewPending
			}
			// Retirement is not a decline, tombstone or memory preference.
			pending.Status = FolderOfferClosed
		}
		doc.Offers = append(doc.Offers, offer)
		pruneFolderDigest(doc)
		result = offer
		return nil
	})
	if err != nil {
		return FolderOfferView{}, err
	}
	return d.viewFor(ctx, source.UserID, result, held.binding.Paused), nil
}

// prepareReviewOffer is shared by explicit Review and read-only suggestions.
// It validates the observed candidate but neither allocates nor stores an offer.
func (s *FolderObservationService) prepareReviewOffer(ctx context.Context, held heldFolderObservation, target foldercontext.Target, candidateID string) (FolderOffer, error) {
	candidate, ok := observedReviewCandidate(held, candidateID)
	if !ok {
		return FolderOffer{}, ErrFolderSelection
	}
	canonical, err := s.digest.deps.ValidateRoot(candidate.Path)
	identity, identityErr := portfolioDirectoryIdentity(candidate.Path)
	if err != nil || canonical != candidate.Path || identityErr != nil {
		return FolderOffer{}, ErrFolderPathLost
	}
	d := s.digest
	now := d.now()
	offer := FolderOffer{
		Status:    FolderOfferPending,
		FolderKey: FolderKey(held.root), FolderName: held.result.Name, RootIdentity: held.identity,
		Verdict: string(folderdigest.KindProject), Reason: folderdigest.DescribeCandidate(candidate, now),
		Partial: held.result.Partial, ScannedAt: held.observation.ScannedAt, CreatedAt: now,
		Subject:       folderCandidateRecord(candidate, FolderChoiceProject, folderdigest.DescribeCandidate(candidate, now)),
		ProjectsCount: 1,
		ConversationReview: &FolderConversationReview{
			Target: target, ObservationID: held.observation.ID, CandidateID: candidateID, CandidateIdentity: identity,
			WorkspaceName: folderReviewWorkspaceName(ctx),
		},
	}
	offer.Enclosing = d.enclosingHint(offer.Subject.Repository, candidate.Path)
	placement := folderReviewPlacement(ctx)
	offer.ConversationReview.Operation, offer.ConversationReview.DestinationID = placement.Operation, placement.DestinationID
	if placement.Operation == FolderOperationSupport {
		offer.CapabilitySuppressed = true
	}
	doc, err := d.store.Read(ctx, target.UserID)
	if err != nil {
		return FolderOffer{}, err
	}
	// A folder that holds several projects makes its own group, or joins the
	// shape's Home. The page on screen is not where it goes.
	collection := candidate.IsRoot && placement.Operation != FolderOperationSupport && folderdigest.IsCollection(held.result, now)
	// A No thanks on a single folder still ends its question. On a collection's
	// root it is shown on the proposal, which can be asked about again.
	tombstone := doc.Tombstone(offer.Subject.Key)
	if tombstone != nil && !collection {
		return FolderOffer{}, ErrFolderOfferDecided
	}
	// Only the root option can describe a collection. Review still requires
	// the user to choose it explicitly, with the same canonical Home checks.
	verdict := folderdigest.Exclude(held.result, now, func(c folderdigest.Candidate) bool { return doc.Tombstoned(FolderKey(c.Path)) })
	if candidate.IsRoot && verdict.Portfolio != nil && d.deps.HomeExists != nil {
		row, found := folderdigest.CapabilityForShape(verdict.Portfolio.Shape)
		if found && row.Offer != nil && row.Offer.HomeProviderKey != "" {
			exists, homeErr := d.deps.HomeExists(ctx, target.UserID, row.Offer.HomeProviderKey)
			if homeErr != nil {
				return FolderOffer{}, homeErr
			}
			if !exists || d.existingHomeReadable(ctx, target.UserID, row.Offer.HomeProviderKey) {
				offer = buildPortfolioOffer(offer, verdict, row.Offer.HomeProviderKey)
				offer.Portfolio.ExistingHome = exists
				offer.Portfolio.IntegrationKey, offer.Portfolio.IntegrationProjects = folderdigest.IntegrationProjects(held.result, verdict.Portfolio.Shape, now)
			}
		}
	}
	// A No thanks to a kind of work is not read here: in a conversation the
	// user asked about this folder, and every option is described as it is.
	if collection {
		offer.ConversationReview.DestinationID = ""
	}
	if reader, ok := d.deps.Setup.(FolderSetupDestinationReader); ok {
		destination, err := reader.ReadSetupDestination(ctx, d.setupRequest(ctx, target.UserID, offer))
		if err != nil {
			offer.ConversationReview.DestinationUnavailable = true
			offer.ConversationReview.DestinationBlocker = folderDestinationBlocker(err)
		} else if destination != nil {
			if destination.Validate() != nil {
				offer.ConversationReview.DestinationUnavailable = true
			} else {
				copy := *destination
				offer.ConversationReview.Destination = &copy
			}
		}
	}
	// What the user changed in Adjust applies only to a collection's own
	// proposal. It is kept on the review so a refresh proposes the same thing.
	adjustment := folderStructureAdjustment(ctx)
	if adjustment != nil && !collection {
		return FolderOffer{}, structureAdjustmentError("this folder is not proposed as a group")
	}
	if collection {
		offer.Structure, err = d.structureFor(ctx, target.UserID, held.result, verdict, offer, nil, adjustment)
		if err != nil {
			return FolderOffer{}, err
		}
		offer.ConversationReview.Structure = adjustment.clone()
	}
	if tombstone != nil {
		// A declined root with no group left to propose has no question left.
		if offer.Structure == nil {
			return FolderOffer{}, ErrFolderOfferDecided
		}
		offer.Declined = &FolderRootDecline{FolderKey: tombstone.Key, Name: tombstone.Name, At: tombstone.CreatedAt}
	}
	if offer.Structure != nil && offer.Structure.Group.Type == FolderGroupPlain {
		// A plain group is created by the setup itself: there is no Home to
		// witness, and a Home that could not be read does not hold it up.
		review := offer.ConversationReview
		review.Destination, review.DestinationUnavailable, review.DestinationBlocker = nil, false, nil
	}
	return offer, nil
}

// declinedDomains reports the capability domains the owner said no to: in the
// digest, or on the older app card. A prior answer that cannot be read counts as
// a no, so nothing is proposed against it.
func (s *FolderDigestService) declinedDomains(ctx context.Context, userID string, doc FolderDigestDocument) func(domain string) bool {
	answers := map[string]bool{}
	return func(domain string) bool {
		if domain == "" {
			return false
		}
		if declined, known := answers[domain]; known {
			return declined
		}
		declined := doc.DeclineFor(domain) != nil
		if !declined {
			legacy, err := s.legacyDeclined(ctx, userID, doc, domain)
			declined = legacy || err != nil
		}
		answers[domain] = declined
		return declined
	}
}

// FolderReviewOption is a path-free description, never a plan or execution grant.
type FolderReviewOption struct {
	CandidateID   string                    `json:"candidate_id"`
	WorkspaceType string                    `json:"workspace_type"`
	Presentation  *FolderReviewPresentation `json:"presentation,omitempty"`
	// Structure is the proposed group on the root option of a collection: the
	// same record the attach response and the offer carry, so every surface
	// shows and adjusts one thing.
	Structure *FolderStructure `json:"structure,omitempty"`
	// Declined is the earlier No thanks on that collection's root.
	Declined *FolderRootDecline `json:"declined,omitempty"`
}

// ReviewOptions reads the same candidate and setup availability as Review. It
// does not scan, create an offer, advance a journey, or disturb pending work.
func (s *FolderObservationService) ReviewOptions(ctx context.Context, target foldercontext.Target, observationID string) []FolderReviewOption {
	held, _, err := s.resolve(ctx, target, observationID)
	if err != nil {
		return nil
	}
	doc, err := s.digest.store.Read(ctx, target.UserID)
	if err != nil || doc.Pending() != nil {
		return nil
	}
	var options []FolderReviewOption
	for _, project := range held.observation.Projects {
		if option, ok := s.reviewOption(ctx, held, target, project.ID); ok {
			options = append(options, option)
		}
	}
	return options
}

// reviewOption describes one observed project as a review option. ok is false
// for a folder that was lost, a single folder the user said never to ask about
// again (a collection's root still has its group), and a supporting link,
// which is not a new setup.
func (s *FolderObservationService) reviewOption(ctx context.Context, held heldFolderObservation, target foldercontext.Target, candidateID string) (FolderReviewOption, bool) {
	offer, err := s.prepareReviewOffer(ctx, held, target, candidateID)
	if err != nil || offer.ConversationReview.Operation == FolderOperationSupport {
		return FolderReviewOption{}, false
	}
	// Describe without a canonical review/root grant: that grant can only
	// exist after the user's later Review click is successfully persisted.
	view := s.digest.describeOffer(ctx, offer, held.binding.Paused)
	s.digest.attachSetupPlan(ctx, target.UserID, offer, &view)
	option := FolderReviewOption{CandidateID: candidateID, WorkspaceType: "Blank workspace", Presentation: folderReviewPresentation(offer, view)}
	// An option that cannot be set up right now is still listed, with its
	// reason (PRD FR10). It is never a control that leads nowhere: its state
	// says it is unavailable.
	unavailable := func(reason string) {
		if reason == "" {
			reason = FolderAvailabilityPlanUnavailable
		}
		option.Presentation = unavailableReviewPresentation(option.Presentation, reason, folderAvailabilitySubject(offer, reason))
	}
	row, software := softwareCapability(offer.Subject)
	switch {
	case offer.Structure.ProposesWorkspaces():
		// A structure never waits on a plugin, only on a host that can set
		// one up at all.
		option.WorkspaceType = offer.Structure.summary()
		option.Declined = offer.Declined.clone()
		if view.Plan == nil {
			unavailable(view.Availability)
		} else {
			option.Structure = offer.Structure.view()
		}
	case software:
		// A software project's setup is reviewed on its own card; the option
		// only names what that review is for.
		option.WorkspaceType = row.Builtin.Workspace
		if s.digest.deps.InspectSoftware == nil || s.digest.deps.Setup == nil {
			unavailable(FolderAvailabilityPlanUnavailable)
		}
	case view.Capability != nil:
		option.WorkspaceType = view.Capability.Workspace
		if view.Plan == nil {
			unavailable(view.Availability)
		}
	case !view.CreateAvailable:
		unavailable(FolderAvailabilityPlanUnavailable)
	case view.BlueprintLabel != "":
		option.WorkspaceType = view.BlueprintLabel + " workspace"
	}
	return option, true
}

// FolderHireCandidate is what attaching one folder answers with: the project
// whose review opens at once, or why it cannot open now. Both are empty for
// any folder that is not one project Ori sets up as a hire.
type FolderHireCandidate struct {
	CandidateID string
	// Unavailable is FolderStructureReviewPending while another setup review
	// waits for its answer, the one thing that keeps the card away.
	Unavailable string
}

// HireCandidate is the one project an attached folder is, when its setup can be
// presented as a hire right away: a host capability with a plan, or a software
// project whose card offers its review. The composer opens that review as soon
// as the folder is attached, so the hire card answers the attach. It reads
// only, like ReviewOptions. A review this conversation already has open does
// not stand in the way: reviewing another folder retires it (Review). Nothing
// for a collection, a folder of no kind Ori sets up, a folder the user said no
// to, or one that cannot be set up now.
func (s *FolderObservationService) HireCandidate(ctx context.Context, target foldercontext.Target, observationID string) FolderHireCandidate {
	recognition, err := s.Recognize(ctx, target, observationID)
	if err != nil || recognition == nil || recognition.Verdict != BuildFolderProject || recognition.CandidateID == "" {
		return FolderHireCandidate{}
	}
	if recognition.IntegrationKey == "" && recognition.Shape != string(folderdigest.ShapeCode) {
		return FolderHireCandidate{}
	}
	held, _, err := s.resolve(ctx, target, observationID)
	if err != nil {
		return FolderHireCandidate{}
	}
	doc, err := s.digest.store.Read(ctx, target.UserID)
	if err != nil {
		return FolderHireCandidate{}
	}
	if pending := doc.Pending(); pending != nil && !ownConversationReview(pending, target) {
		// A card waiting on Home for this very project gives way to the review
		// (Review closes it); any other waiting card keeps it away.
		offer, err := s.prepareReviewOffer(ctx, held, target, recognition.CandidateID)
		if err != nil || !sameProjectHomeOffer(pending, offer.Subject.Key) {
			return FolderHireCandidate{Unavailable: FolderStructureReviewPending}
		}
	}
	option, ok := s.reviewOption(ctx, held, target, recognition.CandidateID)
	if !ok || (option.Presentation != nil && option.Presentation.State == "unavailable") {
		return FolderHireCandidate{}
	}
	return FolderHireCandidate{CandidateID: option.CandidateID}
}

// sameProjectHomeOffer is a card waiting on Home (Explore a folder) for the
// very project a conversation now reviews. The conversation's review takes its
// place, so the same folder never has two cards and the user is never told to
// finish a setup that is the one they are asking for. A collection's or an
// arranged card is never this: its subject is the whole folder.
func sameProjectHomeOffer(pending *FolderOffer, subjectKey string) bool {
	return pending.ConversationReview == nil && pending.Structure == nil && pending.Portfolio == nil &&
		subjectKey != "" && pending.Subject.Key == subjectKey
}

// ownConversationReview is a pending review this conversation opened, which a
// new review in the same conversation retires (Review). An arranged review is
// closed explicitly, and any other pending card waits for its own answer.
func ownConversationReview(pending *FolderOffer, target foldercontext.Target) bool {
	review := pending.ConversationReview
	if review == nil || pending.Structure.Arranged() || target.ConversationID == "" {
		return false
	}
	own := target
	own.DraftID = ""
	return review.Target == own
}

// folderRootCandidateID is the root's id in every scan: the root is the first
// candidate.
const folderRootCandidateID = "candidate-0"

// Why a collection's card cannot be shown when it is attached. The composer
// says the reason in one line instead of showing nothing.
const (
	FolderStructureReviewPending    = "review_pending"
	FolderStructureSetupUnavailable = "setup_unavailable"
)

// FolderStructureProposal is what attaching a folder that holds several
// projects shows: the proposed group and the earlier No thanks on it, or why
// no card can be shown. It is empty for any other folder.
type FolderStructureProposal struct {
	Structure   *FolderStructure
	Declined    *FolderRootDecline
	Unavailable string
}

// Structure describes an attached folder that holds several projects as one
// group with a workspace per subfolder, for the card shown as soon as the folder
// is attached. Like ReviewOptions it reads only: no scan, no offer, no model.
// Neither an earlier No thanks nor a Home in place keeps the card away; only
// another pending setup review and a host that cannot set a group up do, and
// each says so. A collection whose folders are all workspaces already has no
// card and no reason.
func (s *FolderObservationService) Structure(ctx context.Context, target foldercontext.Target, observationID string) FolderStructureProposal {
	held, _, err := s.resolve(ctx, target, observationID)
	if err != nil || !folderdigest.IsCollection(held.result, s.digest.now()) {
		return FolderStructureProposal{}
	}
	unavailable := func(reason string) FolderStructureProposal {
		logger.Info("Folder structure card not shown", logger.Fields{"reason": reason})
		return FolderStructureProposal{Unavailable: reason}
	}
	if s.digest.deps.Setup == nil {
		return unavailable(FolderStructureSetupUnavailable)
	}
	doc, err := s.digest.store.Read(ctx, target.UserID)
	if err != nil {
		return FolderStructureProposal{}
	}
	if doc.Pending() != nil {
		return unavailable(FolderStructureReviewPending)
	}
	offer, err := s.prepareReviewOffer(ctx, held, target, folderRootCandidateID)
	if err != nil || !offer.Structure.ProposesWorkspaces() {
		return FolderStructureProposal{}
	}
	return FolderStructureProposal{Structure: offer.Structure.view(), Declined: offer.Declined.clone()}
}

// folderOfferCapabilityWorkspace names the workspace a recognized offer's
// capability would make, whether or not that capability is offered right now.
func folderOfferCapabilityWorkspace(offer FolderOffer) (string, bool) {
	if offer.Portfolio != nil {
		if row, ok := folderdigest.CapabilityForShape(folderdigest.Shape(offer.Portfolio.Shape)); ok && row.Offer != nil && row.Offer.HomeName != "" {
			return row.Offer.HomeName, true
		}
		return "", false
	}
	if offer.Verdict != string(folderdigest.KindProject) || offer.Subject.Kind != FolderChoiceProject {
		return "", false
	}
	row, ok := folderdigest.ProjectCapabilityFor(folderdigest.Shape(offer.Subject.Shape), offer.Subject.MarkerName, offer.Subject.DominantExtension)
	if !ok {
		return "", false
	}
	return row.Blueprint.Label + " workspace", true
}

// folderAvailabilitySubject names what an unavailable offer waits on: the
// shape's Home for a collection or an unverified provider, else the integration.
func folderAvailabilitySubject(offer FolderOffer, reason string) string {
	shape := folderdigest.Shape(offer.Subject.Shape)
	if offer.Portfolio != nil {
		shape = folderdigest.Shape(offer.Portfolio.Shape)
	}
	row, ok := folderdigest.CapabilityForShape(shape)
	if !ok || row.Offer == nil {
		return ""
	}
	if offer.Portfolio != nil || reason == FolderAvailabilityProviderUnverified || reason == FolderAvailabilityProviderNotInstalled {
		return row.Offer.HomeName
	}
	return row.Offer.IntegrationName
}

// CloseReview closes only an unconfirmed review owned by this conversation.
// It is not No thanks: no preference, decision, memory, or task is written.
func (s *FolderObservationService) CloseReview(ctx context.Context, target foldercontext.Target, offerID string) error {
	_, err := s.digest.store.Mutate(ctx, target.UserID, func(doc *FolderDigestDocument) error {
		offer := doc.Offer(offerID)
		if offer == nil || offer.ConversationReview == nil || offer.ConversationReview.Target != target {
			return ErrFolderOfferNotFound
		}
		if offer.Status == FolderOfferClosed {
			return errFolderReplay
		}
		if offer.Status != FolderOfferPending || offer.DecidedAt != nil {
			return ErrFolderOfferDecided
		}
		offer.Status = FolderOfferClosed
		return nil
	})
	if errors.Is(err, errFolderReplay) {
		return nil
	}
	return err
}

// ReadReview projects an exact stored reference, never promoting offers or
// executing their outcome. Even completed receipts remain owned by the thread.
func (s *FolderObservationService) ReadReview(ctx context.Context, target foldercontext.Target, offerID string) (*FolderOfferView, error) {
	offer, err := s.digest.StoredOffer(ctx, target.UserID, offerID)
	if err != nil {
		return nil, err
	}
	if offer.ConversationReview == nil || offer.ConversationReview.Target != target {
		return nil, ErrFolderOfferNotFound
	}
	binding, err := s.digest.store.Binding(ctx, target.UserID)
	if err != nil {
		return nil, err
	}
	view := s.digest.viewFor(ctx, target.UserID, offer, binding.Paused)
	return &view, nil
}

func (s *FolderDigestService) checkReviewDestination(ctx context.Context, offer FolderOffer) error {
	review := offer.ConversationReview
	if review == nil || (offer.Status != FolderOfferPending && offer.Status != FolderOfferAwaitingOutcome) {
		return nil
	}
	if review.DestinationUnavailable {
		return ErrFolderOutcomeUnavailable
	}
	if review.Destination == nil {
		return nil
	} // unbound historical/unsupported placement is not invented
	if review.Destination.Status == "new" && offer.Setup != nil {
		if offer.Setup.Intent.Destination == nil || *offer.Setup.Intent.Destination != *review.Destination {
			return ErrFolderPlanChanged
		}
		guard, ok := s.deps.Setup.(FolderSetupDestinationGuard)
		if !ok {
			return ErrFolderOutcomeUnavailable
		}
		request := s.setupRequest(ctx, review.Target.UserID, offer)
		request.Plan = FolderSetupPlan{Digest: offer.Setup.PlanDigest, Destination: offer.Setup.Intent.Destination, Intent: offer.Setup.Intent}
		return guard.ValidateSetupDestination(ctx, request)
	}
	reader, ok := s.deps.Setup.(FolderSetupDestinationReader)
	if !ok {
		return ErrFolderOutcomeUnavailable
	}
	actual, err := reader.ReadSetupDestination(ctx, s.setupRequest(ctx, review.Target.UserID, offer))
	if err != nil {
		return ErrFolderOutcomeUnavailable
	}
	if actual == nil || actual.Validate() != nil {
		return ErrFolderPlanChanged
	}
	copy := *actual
	if offer.Setup != nil {
		// Own progress revisions cannot retarget the material witness.
		copy.RecordVersion, copy.ProgramRevision = review.Destination.RecordVersion, review.Destination.ProgramRevision
	}
	if copy != *review.Destination {
		return ErrFolderPlanChanged
	}
	return nil
}

func (s *FolderDigestService) acquireConversationReview(ctx context.Context, offer FolderOffer) (func(), error) {
	if err := s.checkReviewDestination(ctx, offer); err != nil {
		return nil, ErrFolderPlanChanged
	}
	if offer.ConversationReview == nil {
		return func() {}, nil
	}
	if s.conversationLease == nil {
		return nil, ErrFolderSelection
	}
	return s.conversationLease(ctx, offer)
}

func conversationReviewDigest(offer FolderOffer, view FolderOfferView) string {
	value := fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%t", offer.ID, offer.ConversationReview.ObservationID,
		offer.Subject.Key, view.Subject.Name, view.Subject.Shape, view.Blueprint, view.Remember)
	value += "\x00" + offer.ConversationReview.Operation
	if offer.ConversationReview.Destination != nil {
		value += "\x00" + DestinationPlanDigest(nil, offer.ConversationReview.Destination)
	}
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func validateConversationReview(review *FolderConversationReview) error {
	if review == nil {
		return nil
	}
	if review.Destination != nil && review.Destination.Validate() != nil {
		return errFolderDigestInvalid
	}
	if review.Structure != nil && review.Structure.Validate() != nil {
		return errFolderDigestInvalid
	}
	if !review.Target.Valid() || review.Target.ConversationID == "" || review.Target.DraftID != "" {
		return errFolderDigestInvalid
	}
	if review.Operation != "" && review.Operation != FolderOperationCreate && review.Operation != FolderOperationSupport {
		return errFolderDigestInvalid
	}
	if review.WorkspaceName != "" && (strings.TrimSpace(review.WorkspaceName) != review.WorkspaceName || validateFolderName(review.WorkspaceName) != nil) {
		return errFolderDigestInvalid
	}
	if len(review.DestinationID) > 200 || strings.ContainsAny(review.DestinationID, "/\\\x00\r\n") {
		return errFolderDigestInvalid
	}
	if blocker := review.DestinationBlocker; blocker != nil {
		if *blocker == (FolderDestinationBlocker{}) {
			return errFolderDigestInvalid
		}
		for _, value := range []string{blocker.IntegrationKey, blocker.HomeProviderKey} {
			if len(value) > 200 || strings.ContainsAny(value, "/\\\x00\r\n") {
				return errFolderDigestInvalid
			}
		}
	}
	for _, value := range []string{review.Target.UserID, review.Target.WorkspaceID, review.Target.AgentName, review.Target.ConversationID, review.ObservationID, review.CandidateID, review.CandidateIdentity} {
		if strings.TrimSpace(value) == "" || len(value) > 200 || strings.ContainsAny(value, "/\\\x00\r\n") {
			return errFolderDigestInvalid
		}
	}
	return nil
}

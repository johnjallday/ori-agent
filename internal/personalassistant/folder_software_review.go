package personalassistant

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// A software setup is reviewed before it is confirmed. The review begins when
// the user presses Review setup on the card: that click, and only that click,
// lets Ori read the project's README, instructions and build files through the
// host's bounded inspector. Recognizing the folder never does. What the
// inspection found is kept in two parts: a small structural summary on the
// offer, so the plan survives a reload, and the excerpts it copied out of the
// project, which stay in this process's memory and are never written down.

// FolderSoftwareReview is the stored part of a review: when the project was
// inspected, the revision of what was read, and facts with a fixed vocabulary
// or a relative reference. It holds no absolute path and no free text copied
// from the project. WorkspaceName and AssistantName are the user's changes to
// the suggestion.
type FolderSoftwareReview struct {
	InspectedAt      time.Time `json:"inspected_at"`
	EvidenceRevision string    `json:"evidence_revision"`
	// Stack names what the manifests declare ("Go", "Node.js").
	Stack []string `json:"stack,omitempty"`
	// Guides are the instruction files that were read; Files is everything
	// that was read. Both are paths relative to the project.
	Guides []string           `json:"guides,omitempty"`
	Files  []string           `json:"files,omitempty"`
	Git    *FolderSoftwareGit `json:"git,omitempty"`
	// Tools are development tools the project carries configuration for. A
	// configuration file is not proof the tool is installed or working.
	Tools         []FolderSoftwareTool `json:"tools,omitempty"`
	Gaps          []FolderSoftwareGap  `json:"gaps,omitempty"`
	Partial       bool                 `json:"partial,omitempty"`
	PartialReason string               `json:"partial_reason,omitempty"`
	WorkspaceName string               `json:"workspace_name,omitempty"`
	AssistantName string               `json:"assistant_name,omitempty"`
}

// FolderSoftwareGit is what the inspection read from Git's own metadata.
// Remote is a host and a path with no user, password or query: a label, not a
// way in.
type FolderSoftwareGit struct {
	Layout string `json:"layout"`
	Branch string `json:"branch,omitempty"`
	Remote string `json:"remote,omitempty"`
	// RepositoryKey relates checkouts of one repository. It is a hint for
	// explaining; it never decides that two folders are the same.
	RepositoryKey string `json:"repository_key,omitempty"`
}

// FolderSoftwareTool is a development tool configured for the project.
type FolderSoftwareTool struct {
	Name   string `json:"name"`
	Source string `json:"source"`
}

// FolderSoftwareGap is something the inspection did not establish.
type FolderSoftwareGap struct {
	Code   string `json:"code"`
	Source string `json:"source,omitempty"`
}

// FolderSoftwareCommand is a command found in the project. Declared ones are
// written there; inferred ones are only what its manifests usually imply. None
// was run.
type FolderSoftwareCommand struct {
	Kind     string `json:"kind"`
	Command  string `json:"command"`
	Source   string `json:"source"`
	Declared bool   `json:"declared"`
}

// FolderSoftwareEvidence is the part of a review copied out of the project:
// the README's opening and the commands it declares. It is held in memory for
// the card and is never stored.
type FolderSoftwareEvidence struct {
	Purpose       string                  `json:"purpose,omitempty"`
	PurposeSource string                  `json:"purpose_source,omitempty"`
	Commands      []FolderSoftwareCommand `json:"commands,omitempty"`
}

// FolderSoftwareInspection is what the host's inspector returns for one click.
type FolderSoftwareInspection struct {
	Review   FolderSoftwareReview
	Evidence FolderSoftwareEvidence
}

// FolderSoftwareRequest is what the service holds about a software offer when
// it asks the host to plan or run its setup.
type FolderSoftwareRequest struct {
	// Identity is the witness of the directory the offer is about. The path
	// itself travels only in FolderSetupRequest.Path, for a run.
	Identity string
	// Existing are the user's workspaces already linked to that directory.
	Existing []FolderExistingProject
	// Review is the stored review, nil until the user asks for one.
	Review *FolderSoftwareReview
}

// FolderSoftwareReviewView is a review as the card shows it: the stored facts,
// and the excerpts while this process still holds them.
type FolderSoftwareReviewView struct {
	FolderSoftwareReview
	*FolderSoftwareEvidence
	// EvidenceHeld is false after a restart: the facts above are still true of
	// what was read, and the excerpts can be read again with Review setup.
	EvidenceHeld bool `json:"evidence_held"`
}

const (
	softwareReviewMaxRefs  = 24
	softwareReviewMaxGaps  = 24
	softwareReviewMaxItems = 8
)

var (
	softwareRef   = regexp.MustCompile(`^[A-Za-z0-9._-][A-Za-z0-9._ -]*(/[A-Za-z0-9._-][A-Za-z0-9._ -]*){0,3}$`)
	softwareLabel = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 .+#()_-]{0,59}$`)
	softwareCode  = regexp.MustCompile(`^[a-z][a-z_]{0,39}$`)
	softwareHex   = regexp.MustCompile(`^[0-9a-f]{16,64}$`)
)

// validSoftwareRef accepts a short path relative to a project: no leading
// slash, no parent step, no backslash.
func validSoftwareRef(ref string) bool {
	return len(ref) <= 160 && softwareRef.MatchString(ref) && !strings.Contains(ref, "..")
}

func validateSoftwareReview(review *FolderSoftwareReview) error {
	if review == nil {
		return nil
	}
	invalid := fmt.Errorf("%w: software review", errFolderDigestInvalid)
	inspected := review.EvidenceRevision != ""
	if inspected != !review.InspectedAt.IsZero() || (inspected && !softwareHex.MatchString(review.EvidenceRevision)) {
		return invalid
	}
	if len(review.Stack) > softwareReviewMaxItems || len(review.Guides) > softwareReviewMaxItems || len(review.Tools) > softwareReviewMaxItems ||
		len(review.Files) > softwareReviewMaxRefs || len(review.Gaps) > softwareReviewMaxGaps || len(review.PartialReason) > 20 {
		return invalid
	}
	for _, label := range review.Stack {
		if !softwareLabel.MatchString(label) {
			return invalid
		}
	}
	for _, ref := range append(append([]string(nil), review.Guides...), review.Files...) {
		if !validSoftwareRef(ref) {
			return invalid
		}
	}
	for _, tool := range review.Tools {
		if !softwareLabel.MatchString(tool.Name) || !validSoftwareRef(tool.Source) {
			return invalid
		}
	}
	for _, gap := range review.Gaps {
		if !softwareCode.MatchString(gap.Code) || len(gap.Source) > 400 || strings.ContainsAny(gap.Source, "\\\x00\r\n") || strings.HasPrefix(gap.Source, "/") {
			return invalid
		}
	}
	if git := review.Git; git != nil {
		if !softwareCode.MatchString(git.Layout) || len(git.Branch) > 200 || strings.ContainsAny(git.Branch, "\\\x00\r\n ") || strings.HasPrefix(git.Branch, "/") ||
			len(git.Remote) > 300 || strings.ContainsAny(git.Remote, "@?#\\\x00\r\n ") || strings.HasPrefix(git.Remote, "/") ||
			(git.RepositoryKey != "" && !softwareHex.MatchString(git.RepositoryKey)) {
			return invalid
		}
	}
	for _, name := range []string{review.WorkspaceName, review.AssistantName} {
		if name != "" && (validateFolderName(name) != nil || len(name) > 120) {
			return invalid
		}
	}
	return nil
}

// softwareReviewable is true for an offer whose setup can be reviewed now: one
// recognized software project, chosen, with no setup under way. That is an
// offer still waiting for its answer, and one whose confirmed setup stopped:
// a review made again there puts the stopped run aside, and the plan it shows
// is drawn from what that run saved.
func softwareReviewable(offer FolderOffer) bool {
	if !isSoftwareOffer(offer) || offer.NeedsCandidate {
		return false
	}
	return planPending(offer) || softwareRunStopped(offer)
}

// softwareRunStopped reports a confirmed software setup that has a run on
// record and no outcome. Whether that run is still live is the caller's check.
func softwareRunStopped(offer FolderOffer) bool {
	return offer.Status == FolderOfferAwaitingOutcome && offer.Choice == FolderChoiceProject &&
		offer.Setup != nil && offer.Setup.Status != FolderSetupDone
}

// softwarePlannable says whether a plan may be shown for a software offer: the
// user has asked for a review, the offer is about one chosen project whose
// folder the server still holds, and no workspace already set up for that
// folder stands in the way. One workspace linked to the folder some other way
// can be completed by a review; several, or one made for a directory since
// replaced, cannot be chosen for the user.
func softwarePlannable(offer FolderOffer, v *FolderOfferView) bool {
	if offer.SoftwareReview == nil || offer.SoftwareReview.EvidenceRevision == "" || offer.NeedsCandidate || v.NeedsPick {
		return false
	}
	if v.Software == nil || v.Software.ExistingUnavailable {
		return false
	}
	switch len(v.Software.Existing) {
	case 0:
		return true
	case 1:
		return !v.Software.Existing[0].Software && !v.Software.Existing[0].Replaced
	default:
		return false
	}
}

// softwareSetupAvailable says whether a reviewed software setup can be run and
// then checked: the host can run the recipe, re-read its result, and inspect
// the project. Without all three no Set up is offered.
func (s *FolderDigestService) softwareSetupAvailable() bool {
	runner, ok := s.deps.Setup.(interface{ SoftwareSetupAvailable() bool })
	return ok && runner.SoftwareSetupAvailable() && s.deps.SoftwareProject != nil && s.deps.InspectSoftware != nil
}

// recheckSoftwareEvidence reads the project's files again for a Set up click
// and compares them with the review being confirmed. changed is true when they
// differ: the new evidence is then stored, so the plan the card redraws is the
// one for the project as it is now.
func (s *FolderDigestService) recheckSoftwareEvidence(ctx context.Context, userID string, offer FolderOffer, path string) (current FolderOffer, changed bool, err error) {
	if offer.SoftwareReview == nil || offer.SoftwareReview.EvidenceRevision == "" {
		return offer, false, ErrFolderReviewRequired
	}
	if s.deps.InspectSoftware == nil {
		return offer, false, ErrFolderOutcomeUnavailable
	}
	found, err := s.deps.InspectSoftware(ctx, path)
	if err != nil || validateSoftwareReview(&found.Review) != nil || found.Review.EvidenceRevision == "" {
		return offer, false, ErrFolderOutcomeUnavailable
	}
	if _, _, err := s.softwareSubject(offer); err != nil {
		return offer, false, err
	}
	s.holdEvidence(offer.ID, found.Evidence)
	if found.Review.EvidenceRevision == offer.SoftwareReview.EvidenceRevision {
		return offer, false, nil
	}
	updated, err := s.store.Mutate(ctx, userID, func(d *FolderDigestDocument) error {
		item := d.Offer(offer.ID)
		if item == nil || !softwareReviewable(*item) || item.SoftwareReview == nil {
			return ErrFolderOfferDecided
		}
		review := found.Review
		review.WorkspaceName, review.AssistantName = item.SoftwareReview.WorkspaceName, item.SoftwareReview.AssistantName
		item.SoftwareReview = &review
		return nil
	})
	if err != nil {
		return offer, false, err
	}
	if stored := updated.Offer(offer.ID); stored != nil {
		offer = *stored
	}
	return offer, true, nil
}

// resolveSoftware settles a finished software run. The run saying it is done
// is not enough: the host re-reads the workspace from canonical state, for
// this owner, this exact directory and the intent that was confirmed, and the
// offer's receipt is what that read returned.
func (s *FolderDigestService) resolveSoftware(ctx context.Context, userID, offerID string) error {
	if s.deps.SoftwareProject == nil {
		return ErrFolderOutcomeUnavailable
	}
	doc, err := s.store.Read(ctx, userID)
	if err != nil {
		return err
	}
	offer := doc.Offer(offerID)
	if offer == nil || offer.Setup == nil || offer.Setup.Status != FolderSetupDone || offer.Status != FolderOfferAwaitingOutcome ||
		offer.Setup.Intent.Recipe == "" || offer.Setup.Intent.Software == nil {
		return ErrFolderOfferDecided
	}
	path, identity, err := s.softwareSubject(*offer)
	if err != nil {
		return err
	}
	verified, err := s.deps.SoftwareProject(ctx, userID, *offer, path, identity)
	if err != nil || strings.TrimSpace(verified.WorkspaceID) == "" || !strings.HasPrefix(verified.Route, "/workspaces/") {
		return ErrFolderWorkspaceRefused
	}
	now := s.now()
	requestID := "setup-" + offerID + "-software"
	var resolved FolderOffer
	_, err = s.store.Mutate(ctx, userID, func(d *FolderDigestDocument) error {
		item := d.Offer(offerID)
		if item == nil || item.Status != FolderOfferAwaitingOutcome || item.Choice != FolderChoiceProject {
			return ErrFolderOfferDecided
		}
		blueprint, _, _ := s.blueprintForOffer(*item)
		item.Status, item.ResolvedAt = FolderOfferResolved, &now
		item.Outcome = &FolderOutcome{
			Kind: FolderChoiceProject, WorkspaceID: verified.WorkspaceID, Route: verified.Route,
			Blueprint: blueprint, Existing: !verified.Created, Parent: verified.Parent,
			Receipt: append([]FolderReceiptRow(nil), verified.Receipt...),
		}
		d.Receipts = append(d.Receipts, FolderReceipt{RequestID: requestID, OfferID: offerID, Action: "software", At: now})
		pruneFolderDigest(d)
		resolved = *item
		return nil
	})
	if err != nil {
		return err
	}
	s.afterOutcome(ctx, userID, resolved)
	return nil
}

// softwareRequest is what the host needs about a software offer: its witness,
// the workspaces that already cover it, and the review so far. It reads the
// host's saved bindings; it never opens a file in the folder.
func (s *FolderDigestService) softwareRequest(ctx context.Context, userID string, offer FolderOffer) (*FolderSoftwareRequest, error) {
	_, identity, err := s.softwareSubject(offer)
	if err != nil {
		return nil, err
	}
	existing, err := s.softwareExisting(ctx, userID, offer)
	if err != nil {
		return nil, err
	}
	request := &FolderSoftwareRequest{Identity: identity, Existing: existing}
	if offer.SoftwareReview != nil {
		review := *offer.SoftwareReview
		request.Review = &review
	}
	return request, nil
}

func (s *FolderDigestService) holdEvidence(offerID string, evidence FolderSoftwareEvidence) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.evidence == nil {
		s.evidence = map[string]FolderSoftwareEvidence{}
	}
	s.evidence[offerID] = evidence
}

func (s *FolderDigestService) heldEvidence(offerID string) (FolderSoftwareEvidence, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	evidence, ok := s.evidence[offerID]
	return evidence, ok
}

// softwareReviewView is the review as the card shows it.
func (s *FolderDigestService) softwareReviewView(offer FolderOffer) *FolderSoftwareReviewView {
	if offer.SoftwareReview == nil {
		return nil
	}
	view := &FolderSoftwareReviewView{FolderSoftwareReview: *offer.SoftwareReview}
	if evidence, held := s.heldEvidence(offer.ID); held && offer.SoftwareReview.EvidenceRevision != "" {
		view.FolderSoftwareEvidence, view.EvidenceHeld = &evidence, true
	}
	return view
}

// FolderSoftwareReviewInput is the click on Review setup, or a change to the
// suggested names. A nil name is left as it is; an empty one returns to the
// suggestion.
type FolderSoftwareReviewInput struct {
	RequestID     string
	WorkspaceName *string
	AssistantName *string
}

// ReviewSoftware is the click on Review setup. It reads the project's README,
// instructions and build files through the host's bounded inspector, records
// what was read on the offer, and returns the offer with its plan. It creates
// nothing, installs nothing, and calls no model. Pressing it again reads the
// files again, which is how a review is refreshed after the project changes.
func (s *FolderDigestService) ReviewSoftware(ctx context.Context, userID, offerID string, input FolderSoftwareReviewInput) (FolderOfferView, error) {
	return s.changeSoftwareReview(ctx, userID, offerID, "review", input, true)
}

// AdjustSoftware changes the workspace or assistant name the review suggests.
// It reads nothing from the project and creates nothing; the plan, and so the
// digest the user confirms, follows the new names.
func (s *FolderDigestService) AdjustSoftware(ctx context.Context, userID, offerID string, input FolderSoftwareReviewInput) (FolderOfferView, error) {
	if input.WorkspaceName == nil && input.AssistantName == nil {
		return FolderOfferView{}, fmt.Errorf("%w: nothing to adjust", ErrValidation)
	}
	return s.changeSoftwareReview(ctx, userID, offerID, "adjust", input, false)
}

func (s *FolderDigestService) changeSoftwareReview(ctx context.Context, userID, offerID, action string, input FolderSoftwareReviewInput, inspect bool) (FolderOfferView, error) {
	if s == nil || s.store == nil {
		return FolderOfferView{}, ErrRepairNeeded
	}
	input.RequestID = strings.TrimSpace(input.RequestID)
	if input.RequestID == "" || len(input.RequestID) > folderRequestIDMax {
		return FolderOfferView{}, fmt.Errorf("%w: request id", ErrValidation)
	}
	names := map[string]*string{"workspace": input.WorkspaceName, "assistant": input.AssistantName}
	for label, name := range names {
		if name == nil {
			continue
		}
		*name = strings.TrimSpace(*name)
		if *name != "" && (validateFolderName(*name) != nil || len(*name) > 120) {
			return FolderOfferView{}, fmt.Errorf("%w: %s name", ErrValidation, label)
		}
	}
	binding, err := s.store.Binding(ctx, userID)
	if err != nil {
		return FolderOfferView{}, err
	}
	doc, err := s.store.Read(ctx, userID)
	if err != nil {
		return FolderOfferView{}, err
	}
	offer := doc.Offer(offerID)
	if offer == nil {
		return FolderOfferView{}, ErrFolderOfferNotFound
	}
	if receipt := doc.Receipt(input.RequestID); receipt != nil {
		if receipt.OfferID != offerID || receipt.Action != action {
			return FolderOfferView{}, ErrFolderOfferDecided
		}
		return s.viewFor(ctx, userID, *offer, binding.Paused), nil
	}
	if !softwareReviewable(*offer) {
		if isSoftwareOffer(*offer) && offer.NeedsCandidate {
			return FolderOfferView{}, ErrFolderCandidateRequired
		}
		return FolderOfferView{}, ErrFolderOfferDecided
	}
	// A setup that stopped is put aside only by a fresh review, and never while
	// it is still running in this process.
	if softwareRunStopped(*offer) {
		if s.isRunning(offerID) {
			return FolderOfferView{}, ErrFolderOfferDecided
		}
		if !inspect {
			return FolderOfferView{}, ErrFolderReviewRequired
		}
	}
	release, err := s.acquireConversationReview(ctx, *offer)
	if err != nil {
		return FolderOfferView{}, err
	}
	defer release()

	var inspection *FolderSoftwareInspection
	if inspect {
		if s.deps.InspectSoftware == nil {
			return FolderOfferView{}, ErrFolderReviewUnavailable
		}
		path, _, subjectErr := s.softwareSubject(*offer)
		if subjectErr != nil {
			return FolderOfferView{}, subjectErr
		}
		found, inspectErr := s.deps.InspectSoftware(ctx, path)
		if inspectErr != nil {
			return FolderOfferView{}, ErrFolderReviewUnavailable
		}
		// The folder is checked again after the read: a directory swapped in
		// while it was being read is not the one the review is about.
		if _, _, subjectErr := s.softwareSubject(*offer); subjectErr != nil {
			return FolderOfferView{}, subjectErr
		}
		if validateSoftwareReview(&found.Review) != nil || found.Review.EvidenceRevision == "" {
			return FolderOfferView{}, ErrFolderReviewUnavailable
		}
		inspection = &found
	} else if offer.SoftwareReview == nil {
		// Names are adjusted on a review, and there is none yet.
		return FolderOfferView{}, ErrFolderOfferDecided
	}

	now := s.now()
	var result FolderOffer
	_, err = s.store.Mutate(ctx, userID, func(d *FolderDigestDocument) error {
		item := d.Offer(offerID)
		if item == nil {
			return ErrFolderOfferNotFound
		}
		if !softwareReviewable(*item) {
			return ErrFolderOfferDecided
		}
		review := FolderSoftwareReview{}
		if item.SoftwareReview != nil {
			review = *item.SoftwareReview
		}
		if inspection != nil {
			kept := review
			review = inspection.Review
			review.WorkspaceName, review.AssistantName = kept.WorkspaceName, kept.AssistantName
		}
		if input.WorkspaceName != nil {
			review.WorkspaceName = *input.WorkspaceName
		}
		if input.AssistantName != nil {
			review.AssistantName = *input.AssistantName
		}
		if err := validateSoftwareReview(&review); err != nil {
			return err
		}
		item.SoftwareReview = &review
		if inspection != nil && softwareRunStopped(*item) {
			// The stopped run is put aside. What it saved is still saved; the
			// plan drawn next starts from that, and needs its own confirmation.
			item.Setup = nil
		}
		d.Receipts = append(d.Receipts, FolderReceipt{RequestID: input.RequestID, OfferID: item.ID, Action: action, At: now})
		pruneFolderDigest(d)
		result = *item
		return nil
	})
	if err != nil {
		if errors.Is(err, errFolderDigestInvalid) {
			return FolderOfferView{}, ErrFolderReviewUnavailable
		}
		return FolderOfferView{}, err
	}
	if inspection != nil {
		s.holdEvidence(offerID, inspection.Evidence)
	}
	return s.viewFor(ctx, userID, result, binding.Paused), nil
}

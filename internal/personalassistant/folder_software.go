package personalassistant

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
)

// ErrFolderProjectExists means a workspace of the user's is already linked to
// the exact folder the offer is about. The card opens that workspace; a second
// one is never made for the same checkout.
var ErrFolderProjectExists = errors.New("personal assistant: this folder already has a workspace")

// FolderExistingProject is a workspace of the user's whose saved primary folder
// is the exact directory an offer is about. It never carries a path.
type FolderExistingProject struct {
	WorkspaceID string `json:"workspace_id"`
	Name        string `json:"name"`
	Route       string `json:"route"`
	// Software says the workspace was set up as a software project for this
	// directory, rather than linked to it some other way.
	Software bool `json:"software,omitempty"`
	// Replaced says the workspace was set up for a directory at this path that
	// is no longer the one there: the folder was deleted and made again. It is
	// reported so the user can decide; it is never treated as the same project.
	Replaced bool `json:"replaced,omitempty"`
}

// FolderSoftwareCandidate is one software project the scan found in a folder
// that holds several. ID is opaque and good only for this offer.
type FolderSoftwareCandidate struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Marker     string `json:"marker,omitempty"`
	Repository string `json:"repository,omitempty"`
	// Current marks the candidate the offer is about now.
	Current bool `json:"current,omitempty"`
}

// FolderSoftwareView is what the card says about a recognized software
// project: what marked it, whether the user must still choose among several,
// and any workspace that already covers this exact folder.
type FolderSoftwareView struct {
	Recipe string `json:"recipe"`
	// Marker is the recognized marker's plain label ("Go module").
	Marker string `json:"marker"`
	// Repository is "directory" for an ordinary checkout and "file" for a
	// worktree or submodule; empty when the folder has no version control.
	Repository     string                    `json:"repository,omitempty"`
	NeedsCandidate bool                      `json:"needs_candidate,omitempty"`
	Candidates     []FolderSoftwareCandidate `json:"candidates,omitempty"`
	Existing       []FolderExistingProject   `json:"existing,omitempty"`
	// ExistingUnavailable says Ori could not read which workspaces already
	// cover this folder, so it will not offer to make another.
	ExistingUnavailable bool `json:"existing_unavailable,omitempty"`
}

// softwareCapability returns the host's software setup for a subject whose own
// marker says it is a software project.
func softwareCapability(subject FolderCandidateRecord) (folderdigest.CapabilityRow, bool) {
	if subject.Kind != FolderChoiceProject {
		return folderdigest.CapabilityRow{}, false
	}
	return folderdigest.BuiltinCapabilityFor(folderdigest.Shape(subject.Shape), subject.MarkerName)
}

// isSoftwareOffer is true for an offer about one recognized software project.
// A suppressed capability and a supporting-folder link are not setups.
func isSoftwareOffer(offer FolderOffer) bool {
	if offer.CapabilitySuppressed || offer.Portfolio != nil {
		return false
	}
	_, ok := softwareCapability(offer.Subject)
	return ok
}

// softwareCandidateRef is the opaque name a browser uses for one candidate of
// one offer. It reveals neither the folder key nor anything derived from a
// path alone.
func softwareCandidateRef(offerID, key string) string {
	sum := sha256.Sum256([]byte(offerID + "\x00candidate\x00" + key))
	return hex.EncodeToString(sum[:8])
}

// markCandidateChoice requires a choice when the offer's subject is one of
// several software projects the scan found side by side. An offer made for an
// explicitly chosen candidate (a conversation review) is never marked.
func markCandidateChoice(offer *FolderOffer) {
	offer.NeedsCandidate = false
	if offer.Status != FolderOfferPending || offer.ConversationReview != nil || offer.Subject.IsRoot {
		return
	}
	if _, ok := softwareCapability(offer.Subject); !ok {
		return
	}
	for _, queued := range offer.Queue {
		if _, ok := softwareCapability(queued); ok {
			offer.NeedsCandidate = true
			return
		}
	}
}

// enclosingHint looks, within the bounded check, for a repository a folder
// sits inside. repository is how the folder's own version-control marker
// appeared; a folder that is itself a checkout has no enclosing one to report.
func (s *FolderDigestService) enclosingHint(repository, path string) *FolderEnclosingHint {
	if repository != "" || strings.TrimSpace(path) == "" {
		return nil
	}
	home, err := s.deps.HomeDir()
	if err != nil {
		home = ""
	}
	found, ok := folderdigest.EnclosingRepository(path, home)
	if !ok || validateFolderName(found.Name) != nil {
		return nil
	}
	return &FolderEnclosingHint{Levels: found.Levels, Name: found.Name}
}

// softwareSubject returns the offer's folder and its witness as they are now.
// It fails with ErrFolderPathLost when the server no longer holds the folder,
// or when the directory there is not the one the offer was made about: a
// folder deleted and made again at the same path is a different folder, and
// only a fresh pick can make an offer about it.
func (s *FolderDigestService) softwareSubject(offer FolderOffer) (path, identity string, err error) {
	path, err = s.subjectPathOf(offer)
	if err != nil {
		return "", "", ErrFolderPathLost
	}
	identity, err = portfolioDirectoryIdentity(path)
	if err != nil {
		return "", "", ErrFolderPathLost
	}
	expected := offer.SubjectIdentity
	if review := offer.ConversationReview; review != nil {
		expected = review.CandidateIdentity
	}
	if expected != "" && expected != identity {
		return "", "", ErrFolderPathLost
	}
	return path, identity, nil
}

// softwareExisting reads the workspaces that already cover the offer's exact
// folder. It returns ErrFolderPathLost when the folder is not the one the offer
// was made about, and ErrFolderOutcomeUnavailable when the host cannot say; in
// neither case is a new workspace promised.
func (s *FolderDigestService) softwareExisting(ctx context.Context, userID string, offer FolderOffer) ([]FolderExistingProject, error) {
	if s.deps.ExistingProjects == nil {
		return nil, nil // a host with no lookup has nothing to reuse
	}
	path, identity, err := s.softwareSubject(offer)
	if err != nil {
		return nil, err
	}
	found, err := s.deps.ExistingProjects(ctx, userID, path, identity)
	if err != nil {
		return nil, ErrFolderOutcomeUnavailable
	}
	out := make([]FolderExistingProject, 0, len(found))
	for _, project := range found {
		if strings.TrimSpace(project.WorkspaceID) == "" || !strings.HasPrefix(project.Route, "/workspaces/") {
			continue
		}
		// The offer's own workspace is its outcome, not a competing one.
		if offer.Outcome != nil && offer.Outcome.WorkspaceID == project.WorkspaceID {
			continue
		}
		out = append(out, project)
	}
	return out, nil
}

// attachSoftware describes a recognized software project on the offer's view.
// It reads; it never scans, opens a file, or creates anything.
func (s *FolderDigestService) attachSoftware(ctx context.Context, userID string, offer FolderOffer, v *FolderOfferView) {
	if offer.Enclosing != nil {
		hint := *offer.Enclosing
		v.Enclosing = &hint
	}
	row, ok := softwareCapability(offer.Subject)
	if !ok || offer.CapabilitySuppressed || offer.Portfolio != nil {
		return
	}
	view := &FolderSoftwareView{
		Recipe: row.Builtin.Recipe, Marker: offer.Subject.Marker, Repository: offer.Subject.Repository,
		NeedsCandidate: offer.NeedsCandidate,
	}
	if offer.NeedsCandidate {
		view.Candidates = append(view.Candidates, softwareCandidateView(offer.ID, offer.Subject, true))
		for _, queued := range offer.Queue {
			if _, software := softwareCapability(queued); software {
				view.Candidates = append(view.Candidates, softwareCandidateView(offer.ID, queued, false))
			}
		}
		// Nothing is decided about any one of them yet, so no setup is offered.
		v.CreateAvailable = false
	}
	// Only an offer still waiting for its answer asks which workspaces cover the
	// folder: that is the one moment the answer changes what the card offers.
	if offer.Status == FolderOfferPending {
		existing, err := s.softwareExisting(ctx, userID, offer)
		switch {
		case errors.Is(err, ErrFolderPathLost):
			// The folder is gone, replaced, or no longer held: only a fresh pick
			// can continue, and the card says so instead of offering setup.
			v.NeedsPick = true
			v.CreateAvailable = false
		case err != nil:
			view.ExistingUnavailable = true
			v.CreateAvailable = false
		case len(existing) > 0 && !offer.NeedsCandidate:
			view.Existing = existing
			v.CreateAvailable = false
		}
	}
	v.Software = view
}

func softwareCandidateView(offerID string, record FolderCandidateRecord, current bool) FolderSoftwareCandidate {
	return FolderSoftwareCandidate{
		ID: softwareCandidateRef(offerID, record.Key), Name: record.Name,
		Marker: record.Marker, Repository: record.Repository, Current: current,
	}
}

// guardSoftwareYes refuses a yes that would guess among several projects or
// make a second workspace for a folder that already has one.
func (s *FolderDigestService) guardSoftwareYes(ctx context.Context, userID string, offer FolderOffer) error {
	if !isSoftwareOffer(offer) {
		return nil
	}
	if offer.NeedsCandidate {
		return ErrFolderOfferChoice
	}
	existing, err := s.softwareExisting(ctx, userID, offer)
	if err != nil {
		return err
	}
	if len(existing) > 0 {
		return ErrFolderProjectExists
	}
	return nil
}

// witnessSoftwareSubject records the directory a software offer is about, so a
// later yes can tell that folder from another one made at the same path.
func (s *FolderDigestService) witnessSoftwareSubject(offer *FolderOffer, subjectPath string) {
	offer.SubjectIdentity = ""
	if offer.ConversationReview != nil || !isSoftwareOffer(*offer) {
		return
	}
	if identity, err := portfolioDirectoryIdentity(subjectPath); err == nil {
		offer.SubjectIdentity = identity
	}
}

// FolderCandidateInput is the user's choice among the software projects a
// folder holds. CandidateID is one the offer's view listed.
type FolderCandidateInput struct {
	CandidateID string
	RequestID   string
}

// ChooseCandidate makes one disclosed software project the offer's subject.
// It only reorders what the scan already found for this offer: no path comes
// from the browser, nothing is scanned again, and nothing is set up. The
// candidates the user did not choose stay queued for later offers.
func (s *FolderDigestService) ChooseCandidate(ctx context.Context, userID, offerID string, input FolderCandidateInput) (FolderOfferView, error) {
	if s == nil || s.store == nil {
		return FolderOfferView{}, ErrRepairNeeded
	}
	input.CandidateID = strings.TrimSpace(input.CandidateID)
	input.RequestID = strings.TrimSpace(input.RequestID)
	if input.CandidateID == "" || len(input.CandidateID) > 64 || input.RequestID == "" || len(input.RequestID) > folderRequestIDMax {
		return FolderOfferView{}, fmt.Errorf("%w: candidate choice", ErrValidation)
	}
	binding, err := s.store.Binding(ctx, userID)
	if err != nil {
		return FolderOfferView{}, err
	}
	now := s.now()
	var result FolderOffer
	_, err = s.store.Mutate(ctx, userID, func(d *FolderDigestDocument) error {
		offer := d.Offer(offerID)
		if offer == nil {
			return ErrFolderOfferNotFound
		}
		if receipt := d.Receipt(input.RequestID); receipt != nil {
			if receipt.OfferID != offerID || receipt.Action != "candidate" {
				return ErrFolderOfferDecided
			}
			result = *offer
			return errFolderReplay
		}
		if offer.Status != FolderOfferPending || offer.ConversationReview != nil || !offer.NeedsCandidate {
			return ErrFolderOfferDecided
		}
		if softwareCandidateRef(offer.ID, offer.Subject.Key) != input.CandidateID {
			chosen := -1
			for i, queued := range offer.Queue {
				if _, software := softwareCapability(queued); software && softwareCandidateRef(offer.ID, queued.Key) == input.CandidateID {
					chosen = i
					break
				}
			}
			if chosen < 0 {
				return fmt.Errorf("%w: candidate choice", ErrValidation)
			}
			subject := offer.Queue[chosen]
			rest := make([]FolderCandidateRecord, 0, len(offer.Queue))
			rest = append(rest, offer.Subject)
			rest = append(rest, offer.Queue[:chosen]...)
			rest = append(rest, offer.Queue[chosen+1:]...)
			offer.Subject, offer.Queue, offer.Reason = subject, rest, subject.Reason
		}
		offer.NeedsCandidate = false
		// The witness and the enclosing check belong to the folder now chosen,
		// which the server still holds; the browser named a candidate, not a path.
		root, held := s.rootPath(*offer)
		if !held {
			return ErrFolderPathLost
		}
		subjectPath := root
		if !offer.Subject.IsRoot && offer.Subject.RelPath != "" {
			subjectPath = filepath.Join(root, offer.Subject.RelPath)
		}
		offer.Enclosing = s.enclosingHint(offer.Subject.Repository, subjectPath)
		s.witnessSoftwareSubject(offer, subjectPath)
		d.Receipts = append(d.Receipts, FolderReceipt{RequestID: input.RequestID, OfferID: offer.ID, Action: "candidate", At: now})
		pruneFolderDigest(d)
		result = *offer
		return nil
	})
	if err != nil && !errors.Is(err, errFolderReplay) {
		return FolderOfferView{}, err
	}
	return s.viewFor(ctx, userID, result, binding.Paused), nil
}

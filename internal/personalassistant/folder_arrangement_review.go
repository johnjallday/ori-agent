package personalassistant

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
	"github.com/johnjallday/ori-agent/internal/folderdigest"
)

// ErrFolderArrangementReview is an arranged group that cannot be reviewed as it
// stands. Its message names the part, in words a card can show; nothing is
// saved and no other folder is substituted.
var ErrFolderArrangementReview = errors.New("this arranged group cannot be reviewed")

func arrangementReviewError(message string) error {
	return fmt.Errorf("%w: %s", ErrFolderArrangementReview, message)
}

// FolderArrangementGroup is one canonical create_group intention whose owning
// conversation, record revision and runtime the caller already authorized.
// CandidateIDs are the record's exact resolved members: held candidate IDs from
// the whole scan, never a preview position, a path or a model-supplied name.
type FolderArrangementGroup struct {
	Binding      FolderStructureArrangement
	Name         string
	CandidateIDs []string
}

// folderArrangementReason is the line the review card shows under its question.
const folderArrangementReason = "Selected in a saved arrangement from this conversation. Names, file counts and dates are what the attach observed."

// ReviewArrangementGroup compiles one arranged group into the existing
// conversation-bound setup review: the same plan, confirmation, run and
// receipt every structure uses. It creates or refreshes a pending offer and
// never decides it. The caller must bind the returned offer to the canonical
// conversation and arrangement before any control can authorize anything; a
// failed bind closes a newly created offer. created is false when an existing
// review or run of this exact intention is returned instead.
func (s *FolderObservationService) ReviewArrangementGroup(ctx context.Context, target foldercontext.Target, observationID string, group FolderArrangementGroup) (FolderOfferView, bool, error) {
	if s == nil || s.digest == nil || s.digest.store == nil || !target.Valid() || target.ConversationID == "" || target.DraftID != "" || !group.Binding.valid() {
		return FolderOfferView{}, false, ErrFolderSelection
	}
	held, _, err := s.resolve(ctx, target, observationID)
	if err != nil {
		return FolderOfferView{}, false, err
	}
	offer, err := s.prepareArrangementOffer(ctx, held, target, group)
	if err != nil {
		return FolderOfferView{}, false, err
	}
	d := s.digest
	offer.ID = d.deps.NewID()
	created := true
	var result FolderOffer
	_, err = d.store.Mutate(ctx, target.UserID, func(doc *FolderDigestDocument) error {
		for i := range doc.Offers {
			prior := &doc.Offers[i]
			binding := prior.Structure
			if prior.ConversationReview == nil || prior.ConversationReview.Target != target || !binding.Arranged() ||
				binding.Arrangement.RecordID != group.Binding.RecordID || binding.Arrangement.Operation != group.Binding.Operation {
				continue
			}
			switch prior.Status {
			case FolderOfferAwaitingOutcome, FolderOfferResolved:
				// A confirmed run or its outcome is recovered, never run twice.
				result, created = *prior, false
				return nil
			case FolderOfferPending:
				if *binding.Arrangement != group.Binding || prior.ConversationReview.ObservationID != observationID {
					continue
				}
				// The same intention reviewed again: what is available now, the
				// same exact members, the same offer.
				prior.Structure = offer.Structure.stored()
				prior.ConversationReview = offer.ConversationReview
				result, created = *prior, false
				return nil
			}
		}
		// Every other pending review is protected, in this conversation or another.
		if doc.Pending() != nil {
			return ErrFolderReviewPending
		}
		doc.Offers = append(doc.Offers, offer)
		pruneFolderDigest(doc)
		result = offer
		return nil
	})
	if err != nil {
		return FolderOfferView{}, false, err
	}
	return d.viewFor(ctx, target.UserID, result, held.binding.Paused), created, nil
}

// heldCandidateIndex maps an exact held candidate ID to its index in the whole
// scan. Only immediate subfolders are creator candidates; the root is the group.
func heldCandidateIndex(held heldFolderObservation, id string) (int, bool) {
	raw, found := strings.CutPrefix(id, "candidate-")
	index, err := strconv.Atoi(raw)
	if !found || err != nil || strconv.Itoa(index) != raw || index < 1 || index >= len(held.result.Candidates) {
		return 0, false
	}
	if held.result.Candidates[index].IsRoot {
		return 0, false
	}
	return index, true
}

// prepareArrangementOffer builds the review from the record's exact selection
// and current directory identity. It reads no file and adds no folder: a member
// that moved, became a link or already belongs to a workspace is refused by
// name rather than dropped, replaced or set up twice.
func (s *FolderObservationService) prepareArrangementOffer(ctx context.Context, held heldFolderObservation, target foldercontext.Target, group FolderArrangementGroup) (FolderOffer, error) {
	d := s.digest
	if held.result.SoftwareRoot() {
		return FolderOffer{}, arrangementReviewError("a software project is set up as one project, not as a group of its parts")
	}
	root, ok := observedReviewCandidate(held, folderRootCandidateID)
	if !ok || !root.IsRoot {
		return FolderOffer{}, ErrFolderSelection
	}
	canonical, err := d.deps.ValidateRoot(root.Path)
	rootIdentity, identityErr := portfolioDirectoryIdentity(root.Path)
	if err != nil || canonical != root.Path || identityErr != nil {
		return FolderOffer{}, ErrFolderPathLost
	}
	if !validStructureWorkspaceName(group.Name) {
		return FolderOffer{}, arrangementReviewError("the group name is not one a workspace group can have; ask for a revised arrangement")
	}
	if len(group.CandidateIDs) == 0 || len(group.CandidateIDs) > FolderStructureMaxMembers {
		return FolderOffer{}, arrangementReviewError(fmt.Sprintf("a group is reviewed with between 1 and %d folders", FolderStructureMaxMembers))
	}
	doc, err := d.store.Read(ctx, target.UserID)
	if err != nil {
		return FolderOffer{}, err
	}
	if doc.Tombstoned(FolderKey(root.Path)) {
		return FolderOffer{}, ErrFolderOfferDecided
	}
	now := d.now()
	folder := foldercontext.DisplayName(held.result.Name)
	if folder == "" {
		folder = "Selected folder"
	}
	binding := group.Binding
	structure := &FolderStructure{
		Version: FolderStructureSelectedVersion, Folder: folder, Arrangement: &binding,
		Group:   FolderStructureGroup{Name: group.Name, Type: FolderGroupPlain, DestinationState: "new"},
		Members: []FolderStructureMember{},
	}
	indexes := make([]int, 0, len(group.CandidateIDs))
	seen := map[int]bool{}
	for _, id := range group.CandidateIDs {
		index, ok := heldCandidateIndex(held, id)
		if !ok || seen[index] {
			return FolderOffer{}, arrangementReviewError("a selected folder is not an immediate subfolder of this attachment")
		}
		seen[index] = true
		indexes = append(indexes, index)
	}
	// Members are listed in the scan's order, like every other structure.
	sort.Ints(indexes)
	for _, index := range indexes {
		candidate := held.result.Candidates[index]
		name := foldercontext.DisplayName(candidate.Name)
		if name == "" {
			name = "Unnamed folder"
		}
		if validateFolderName(candidate.RelPath) != nil || filepath.Base(candidate.RelPath) != candidate.RelPath {
			return FolderOffer{}, arrangementReviewError(name + " is not an immediate subfolder of this attachment")
		}
		path := filepath.Join(held.root, candidate.RelPath)
		resolved, err := d.deps.ValidateRoot(path)
		identity, identityErr := portfolioDirectoryIdentity(path)
		if err != nil || resolved != path || identityErr != nil {
			return FolderOffer{}, arrangementReviewError(name + " is no longer the folder that was observed; attach the folder again")
		}
		if d.deps.ExistingProjects != nil {
			existing, err := d.deps.ExistingProjects(ctx, target.UserID, path, identity)
			if err != nil {
				return FolderOffer{}, err
			}
			if len(existing) > 0 {
				return FolderOffer{}, arrangementReviewError(name + " is already the workspace " + foldercontext.DisplayName(existing[0].Name) + "; ask for a revised arrangement without it")
			}
		}
		member := FolderStructureMember{
			CandidateID: "candidate-" + strconv.Itoa(index), Name: name, Folder: name, TypeLabel: folderPlainWorkspaceLabel,
			Shape: string(folderdigest.ShapeFor(candidate)), Project: folderdigest.IsProject(candidate, now),
			Files: candidate.FileCount, Included: true, RelPath: candidate.RelPath,
			DominantExtension: candidate.DominantExtension, Identity: identity,
		}
		if member.Files > foldercontext.MaxEntries {
			member.Files = foldercontext.MaxEntries
		}
		if candidate.Marker != nil {
			// The observed marker is evidence only: the workspace is plain.
			member.Marker, member.MarkerName = foldercontext.DisplayName(candidate.Marker.Label), candidate.Marker.Name
		}
		if !candidate.NewestModTime.IsZero() {
			member.LastChanged = candidate.NewestModTime.Format(foldercontext.DateLayout)
		}
		if member.Project {
			structure.Projects++
		}
		structure.Members = append(structure.Members, member)
	}
	// Default member names that can be created; the group keeps the
	// arrangement's own name, and a taken one is refused rather than renamed.
	s.digest.freeStructureNames(ctx, structure)
	structure.Group.Name = group.Name
	if err := s.digest.checkStructureNames(ctx, structure); err != nil {
		detail := strings.TrimPrefix(err.Error(), ErrFolderStructureAdjustment.Error()+": ")
		return FolderOffer{}, arrangementReviewError(detail)
	}
	if err := structure.Validate(); err != nil {
		return FolderOffer{}, arrangementReviewError("the selection does not fit one reviewed group")
	}
	offer := FolderOffer{
		Status:    FolderOfferPending,
		FolderKey: FolderKey(held.root), FolderName: held.result.Name, RootIdentity: held.identity,
		Verdict: string(folderdigest.KindProject), Reason: folderArrangementReason,
		Partial: held.result.Partial, ScannedAt: held.observation.ScannedAt, CreatedAt: now,
		Subject:            folderCandidateRecord(root, FolderChoiceProject, folderArrangementReason),
		ProjectsCount:      1,
		Structure:          structure,
		ConversationReview: &FolderConversationReview{Target: target, ObservationID: held.observation.ID, CandidateID: folderRootCandidateID, CandidateIdentity: rootIdentity},
	}
	offer.Subject.Reason = folderArrangementReason
	return offer, nil
}

// FolderArrangementOutcome is what one arranged group's review has become, read
// from the stored offer alone: no scan, no plan and no probe of the folder.
type FolderArrangementOutcome struct {
	Binding     FolderStructureArrangement
	Status      FolderOfferStatus
	SetupStatus string
	StopReason  string
	Running     bool
	// WorkspaceID, Route and Workspaces are the resolved group and how many
	// member workspaces its canonical receipt read back. ReceiptDigest pins
	// that receipt without copying its names.
	WorkspaceID   string
	Route         string
	Workspaces    int
	ReceiptDigest string
}

// ConversationOfferStatus reads where one review this conversation's folder
// events name stands. A review that no longer exists (a resolved offer can be
// pruned) is ErrFolderOfferNotFound.
func (s *FolderObservationService) ConversationOfferStatus(ctx context.Context, target foldercontext.Target, offerID string) (FolderOfferStatus, error) {
	if s == nil || s.digest == nil || !target.Valid() || target.ConversationID == "" {
		return "", ErrFolderOfferNotFound
	}
	offer, err := s.digest.StoredOffer(ctx, target.UserID, offerID)
	if err != nil {
		return "", err
	}
	if offer.ConversationReview == nil || offer.ConversationReview.Target != target {
		return "", ErrFolderOfferNotFound
	}
	return offer.Status, nil
}

// ArrangementOutcome reads one arranged review owned by this conversation.
func (s *FolderObservationService) ArrangementOutcome(ctx context.Context, target foldercontext.Target, offerID string) (FolderArrangementOutcome, error) {
	if s == nil || s.digest == nil || !target.Valid() || target.ConversationID == "" {
		return FolderArrangementOutcome{}, ErrFolderOfferNotFound
	}
	offer, err := s.digest.StoredOffer(ctx, target.UserID, offerID)
	if err != nil {
		return FolderArrangementOutcome{}, err
	}
	if offer.ConversationReview == nil || offer.ConversationReview.Target != target || !offer.Structure.Arranged() {
		return FolderArrangementOutcome{}, ErrFolderOfferNotFound
	}
	out := FolderArrangementOutcome{Binding: *offer.Structure.Arrangement, Status: offer.Status, Running: s.digest.isRunning(offer.ID)}
	if offer.Setup != nil {
		out.SetupStatus, out.StopReason = offer.Setup.Status, offer.Setup.StopReason
	}
	if outcome := offer.Outcome; offer.Status == FolderOfferResolved && outcome != nil && outcome.Kind == FolderChoiceGroup {
		encoded, err := json.Marshal(outcome.Receipt)
		if err != nil || outcome.WorkspaceID == "" || len(outcome.Receipt) < 2 {
			return FolderArrangementOutcome{}, ErrFolderWorkspaceRefused
		}
		sum := sha256.Sum256(encoded)
		out.WorkspaceID, out.Route, out.Workspaces, out.ReceiptDigest = outcome.WorkspaceID, outcome.Route, len(outcome.Receipt)-1, hex.EncodeToString(sum[:])
	}
	return out, nil
}

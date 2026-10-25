package personalassistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
	"github.com/johnjallday/ori-agent/internal/folderdigest"
	"github.com/johnjallday/ori-agent/internal/projectlibrary"
	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacearrangement"
)

// A folder that holds several projects is proposed as one group with a
// workspace per subfolder. FolderStructure is that proposal: what every folder
// entry (the assistant's composer, Home, the assisted Create Workspace chat)
// shows before anything is created, and what a confirmed setup is held to.
const (
	FolderStructureVersion = 1
	// FolderStructureSelectedVersion is a saved arrangement's exact selection:
	// its members are the folders that arrangement chose from the whole held
	// scan, not the recognition rule's collection or its bounded preview. It
	// has no threshold, no omitted tail and no Adjust; a change is a new
	// arrangement revision.
	FolderStructureSelectedVersion = 2
	// FolderStructureMaxMembers bounds the member list. Subfolders past it are
	// counted in Omitted and get no workspace from this proposal.
	FolderStructureMaxMembers = 24
	folderStructureMaxBytes   = 16 * 1024
)

// FolderStructureArrangement binds a selected structure to the one canonical
// arrangement intention it was compiled from. It is a fence, never approval:
// the owning conversation must still hold that record at that revision, with
// that operation bound to this exact review.
type FolderStructureArrangement struct {
	RecordID       string `json:"record_id"`
	RecordRevision int64  `json:"record_revision"`
	Operation      int    `json:"operation"`
}

func (a *FolderStructureArrangement) valid() bool {
	return a != nil && validStructureName(a.RecordID) && !strings.ContainsAny(a.RecordID, `/\:`) &&
		a.RecordRevision >= 1 && a.Operation >= 0 && a.Operation < workspacearrangement.MaxOperations
}

// Arranged reports whether the structure is a saved arrangement's selection.
func (s *FolderStructure) Arranged() bool {
	return s != nil && s.Version == FolderStructureSelectedVersion && s.Arrangement != nil
}

// Group types of a proposed structure.
const (
	// FolderGroupHome is the shape's Home (today "Music Production Home"), which
	// a reviewed provider supplies.
	FolderGroupHome = "home"
	// FolderGroupPlain is a built-in group named after the folder. It needs no
	// plugin, so a structure can always be proposed.
	FolderGroupPlain = "group"
	// FolderGroupLibrary lists the collection in the shape's Home and makes no
	// workspace per project. Recognized collections recommend this operation;
	// a plain group remains an explicit alternative.
	FolderGroupLibrary = "library"
)

// FolderStructureAdjustment is what the user changed in Adjust. Every field is
// optional: an empty one keeps what was proposed. It names members only by the
// ids the proposal itself gave, and holds no path.
type FolderStructureAdjustment struct {
	GroupName string                            `json:"group_name,omitempty"`
	GroupType string                            `json:"group_type,omitempty"`
	Members   []FolderStructureMemberAdjustment `json:"members,omitempty"`
}

// FolderStructureMemberAdjustment is one member's change: whether it gets a
// workspace, and that workspace's name.
type FolderStructureMemberAdjustment struct {
	CandidateID string `json:"candidate_id"`
	Included    bool   `json:"included"`
	Name        string `json:"name,omitempty"`
}

// ErrFolderStructureAdjustment is an adjustment the proposal cannot take. Its
// message says which part, in words a card can show.
var ErrFolderStructureAdjustment = errors.New("that change cannot be applied to this proposal")

func structureAdjustmentError(message string) error {
	return fmt.Errorf("%w: %s", ErrFolderStructureAdjustment, message)
}

// validStructureWorkspaceName is a name a group or workspace can be given: a
// display name that is also a valid folder name.
func validStructureWorkspaceName(name string) bool {
	return validStructureName(name) && validateFolderName(name) == nil
}

// Validate checks an adjustment's shape alone: bounded, known types, no path.
// Whether its ids and choices fit a proposal is checked when it is applied.
func (a FolderStructureAdjustment) Validate() error {
	if a.GroupName != "" && !validStructureWorkspaceName(a.GroupName) {
		return structureAdjustmentError("the group name is not a usable name")
	}
	switch a.GroupType {
	case "", FolderGroupPlain, FolderGroupHome, FolderGroupLibrary:
	default:
		return structureAdjustmentError("the group type is not one this proposal offers")
	}
	if len(a.Members) > FolderStructureMaxMembers {
		return structureAdjustmentError("too many folders")
	}
	seen := map[string]bool{}
	for _, member := range a.Members {
		if !validStructureName(member.CandidateID) || seen[member.CandidateID] {
			return structureAdjustmentError("a folder is named twice or not at all")
		}
		seen[member.CandidateID] = true
		if member.Name != "" && !validStructureWorkspaceName(member.Name) {
			return structureAdjustmentError("a workspace name is not a usable name")
		}
	}
	return nil
}

func (a *FolderStructureAdjustment) clone() *FolderStructureAdjustment {
	if a == nil {
		return nil
	}
	copied := *a
	copied.Members = append([]FolderStructureMemberAdjustment(nil), a.Members...)
	return &copied
}

type folderStructureAdjustmentKey struct{}

// WithFolderStructureAdjustment carries the user's Adjust choices to an explicit
// Review. Like placement, it is request-local input the host decoded from its
// own controls, never something read out of a conversation.
func WithFolderStructureAdjustment(ctx context.Context, adjustment *FolderStructureAdjustment) context.Context {
	if adjustment == nil {
		return ctx
	}
	return context.WithValue(ctx, folderStructureAdjustmentKey{}, adjustment.clone())
}

func folderStructureAdjustment(ctx context.Context) *FolderStructureAdjustment {
	adjustment, _ := ctx.Value(folderStructureAdjustmentKey{}).(*FolderStructureAdjustment)
	return adjustment
}

// applyMembers sets which members get a workspace and what each is called. A
// member the adjustment does not name keeps what was proposed.
func (s *FolderStructure) applyMembers(adjustment *FolderStructureAdjustment) error {
	if adjustment == nil {
		return nil
	}
	for _, change := range adjustment.Members {
		member := s.Member(change.CandidateID)
		if member == nil {
			return structureAdjustmentError("a folder is not part of this proposal")
		}
		if change.Included && member.ExistingWorkspace != "" {
			return structureAdjustmentError(member.Folder + " is already the workspace " + member.ExistingWorkspace)
		}
		member.Included = change.Included
		if change.Name != "" {
			member.Name = change.Name
		}
	}
	if s.Group.Type == FolderGroupLibrary {
		return nil
	}
	names := map[string]bool{}
	for _, member := range s.Included() {
		key := strings.ToLower(member.Name)
		if names[key] {
			return structureAdjustmentError("two workspaces would have the name " + member.Name)
		}
		names[key] = true
	}
	if len(names) == 0 {
		return structureAdjustmentError("choose at least one folder")
	}
	return nil
}

// folderPlainWorkspaceLabel is the type label of a member no marker names.
const folderPlainWorkspaceLabel = "Plain workspace"

// Why a part of a proposal is not what it could be. An unavailable part is
// still proposed, in its plain form, and says so; it is never dropped.
const (
	FolderAvailabilityIntegrationNotInstalled = "integration_not_installed"
	// FolderAvailabilityProviderNotInstalled: the plugin that supplies the
	// shape's Home is not on this computer. It is known without a release lookup,
	// and is the usual reason a first collection gets a plain group.
	FolderAvailabilityProviderNotInstalled = "provider_not_installed"
	// FolderAvailabilityProviderUnverified: that plugin is installed, but its
	// reviewed release could not be verified right now.
	FolderAvailabilityProviderUnverified = "provider_unverified"
	FolderAvailabilityDeclinedEarlier    = "declined_earlier"
	FolderAvailabilityPlanUnavailable    = "plan_unavailable"
)

// FolderAvailabilityCopy is the one sentence shown for an availability reason.
// subject names what is unavailable: the integration's application or the
// shape's Home. An unknown reason has no sentence, and neither has an earlier
// decline: Home's card says that on its own line, with Show.
func FolderAvailabilityCopy(reason, subject string) string {
	subject = strings.TrimSpace(subject)
	switch reason {
	case FolderAvailabilityIntegrationNotInstalled:
		if subject == "" {
			return "The integration is not installed; it can be added later."
		}
		return subject + " integration is not installed; it can be added later."
	case FolderAvailabilityProviderNotInstalled:
		if subject == "" {
			subject = "The Home"
		}
		return subject + " is not installed; a plain group is proposed instead."
	case FolderAvailabilityProviderUnverified:
		if subject == "" {
			subject = "The Home"
		}
		return subject + " could not be verified right now; a plain group is proposed instead."
	case FolderAvailabilityPlanUnavailable:
		if subject == "" {
			return "One-click setup is unavailable right now; it can be added later."
		}
		return subject + " setup is unavailable right now; it can be added later."
	}
	return ""
}

// setupAvailability words a failed plan the way a proposal does. A host error
// that names its own reason is believed; anything else is simply unavailable.
func setupAvailability(err error) string {
	var reasoned interface{ SetupAvailability() string }
	if errors.As(err, &reasoned) {
		if reason := reasoned.SetupAvailability(); reason != "" && validFolderAvailability(reason) {
			return reason
		}
	}
	return FolderAvailabilityPlanUnavailable
}

// FolderStructureAvailabilityReader is an optional seam of the setup host: what
// a collection's proposal can use right now. Both answers are "" when the part
// is available and a FolderAvailability* reason when it is not. Neither
// installs, creates or changes anything, and neither takes a folder.
type FolderStructureAvailabilityReader interface {
	// HomeAvailability says whether the reviewed provider of a shape's Home can
	// be planned for this owner.
	HomeAvailability(ctx context.Context, userID, providerKey string) string
	// IntegrationAvailability says whether a reviewed integration is installed
	// and ready, so a workspace can start from its blueprint with nothing to
	// install.
	IntegrationAvailability(ctx context.Context, userID, integrationKey string) string
}

// structureFor describes a scanned collection for one owner: the proposal from
// the scan, then what each part can be right now. offer is the root's offer;
// its portfolio evidence, when present, says the owner can be offered the
// shape's Home at all. declined reports a capability domain the owner said no
// to. It returns nil for a folder that is not a collection.
//
// Nothing about a plugin fails the proposal: a Home that cannot be planned
// becomes a plain group, and a member whose blueprint cannot be used becomes a
// blank workspace, each with its reason (PRD FR10, decision 3A). The only
// error is an adjustment the proposal cannot take (ErrFolderStructureAdjustment):
// the user's own choice of a Home that is not available, a folder that is not a
// member, or no member left.
func (s *FolderDigestService) structureFor(ctx context.Context, userID string, result folderdigest.Result, verdict folderdigest.Verdict, offer FolderOffer, declined func(domain string) bool, adjustment *FolderStructureAdjustment) (*FolderStructure, error) {
	structure := BuildFolderStructure(result, verdict, s.now(), false)
	if structure == nil {
		return nil, nil
	}
	structure.Recommendation = s.recommendCollection(ctx, userID, result, verdict)
	reader, _ := s.deps.Setup.(FolderStructureAvailabilityReader)
	if declined == nil {
		declined = func(string) bool { return false }
	}
	chosen := ""
	if adjustment != nil {
		chosen = adjustment.GroupType
		if chosen == FolderGroupPlain {
			structure.Recommendation = nil
		}
	}
	if chosen == "" && structure.Recommendation != nil && structure.Recommendation.Operation == FolderGroupLibrary &&
		structure.Recommendation.Destination.State != "unknown" && offer.Portfolio != nil {
		chosen = FolderGroupLibrary
	}
	wantsHome := chosen == FolderGroupHome || chosen == FolderGroupLibrary
	revived := ""
	if structure.Group.HomeName != "" {
		row, _ := folderdigest.CapabilityForShape(verdict.Portfolio.Shape)
		reason := FolderAvailabilityPlanUnavailable
		if offer.Portfolio != nil && reader != nil {
			reason = reader.HomeAvailability(ctx, userID, row.Offer.HomeProviderKey)
			// A review that could not read where the Home is has no Home to use,
			// whatever the provider says.
			if reason == "" && offer.ConversationReview != nil && offer.ConversationReview.DestinationUnavailable {
				reason = FolderAvailabilityPlanUnavailable
			}
		}
		// The Home can be reviewed whenever the owner can be offered it; local
		// recognition recommends library mode without granting any setup effect.
		structure.Group.HomeAvailable = offer.Portfolio != nil
		useHome := false
		switch {
		case chosen == FolderGroupPlain:
			// A plain group chosen over a Home that could have been used is the
			// user's own choice. When the Home could not be used anyway, the
			// group is still the fallback, and still says why.
			structure.Group.Availability = reason
		case chosen == FolderGroupHome:
			// A workspace per folder inside the Home is made by the Home's own
			// library when a project is opened, with that project's file chosen
			// then. No setup creates them all at once yet, so it is not offered.
			return nil, structureAdjustmentError("a workspace for each folder inside " + structure.Group.HomeName + " is not available yet")
		case wantsHome:
			// Listing the collection in its Home is the earlier setup, with its own
			// review and, when the provider is not installed yet, its own guided
			// steps. It can be chosen whenever the owner can be offered that Home.
			if offer.Portfolio == nil {
				return nil, structureAdjustmentError(structure.Group.HomeName + " cannot be used right now")
			}
			// Choosing the Home is how Adjust brings an earlier no back.
			useHome, revived = true, row.Offer.Slug
		case reason != "":
			// A Home that cannot be used says why first, whatever was declined.
			structure.Group.Availability = reason
		case declined(row.Offer.Slug):
			structure.Group.Availability = FolderAvailabilityDeclinedEarlier
		}
		if useHome {
			// Listed in the Home, as before: each project gets its workspace, from
			// its own blueprint, the first time it is opened there.
			structure.Group.Type, structure.Group.Name = FolderGroupLibrary, structure.Group.HomeName
			if offer.Portfolio.ExistingHome {
				structure.Group.DestinationState = "existing"
				if review := offer.ConversationReview; review != nil && review.Destination != nil {
					structure.Group.Name = review.Destination.Name
				} else if structure.Recommendation != nil && structure.Recommendation.Destination.State == "existing" {
					structure.Group.Name = structure.Recommendation.Destination.Name
				}
			}
		}
	} else if wantsHome {
		return nil, structureAdjustmentError("this folder has no Home to use")
	}
	integrations := map[string]string{}
	s.readStructureLinks(ctx, userID, result, structure)
	for i := range structure.Members {
		member := &structure.Members[i]
		row, ok := folderdigest.ProjectCapabilityFor(folderdigest.Shape(member.Shape), member.MarkerName, member.DominantExtension)
		if !ok {
			// No integration is involved. The workspace starts blank: a blueprint
			// brings agents of its own, and a group setup adds none it did not list.
			continue
		}
		member.Integration = row.Offer.IntegrationName
		reason, read := integrations[row.Offer.IntegrationKey]
		if !read {
			reason = FolderAvailabilityPlanUnavailable
			if reader != nil {
				reason = reader.IntegrationAvailability(ctx, userID, row.Offer.IntegrationKey)
			}
			integrations[row.Offer.IntegrationKey] = reason
		}
		switch {
		case row.Offer.Slug != revived && declined(row.Offer.Slug):
			member.Availability = FolderAvailabilityDeclinedEarlier
		case reason != "":
			member.Availability = reason
		case row.Offer.HomeProviderKey != "":
			// The blueprint's workspaces live in the shape's Home, which makes
			// them itself. In a group the member is a blank workspace, whatever
			// is installed.
			member.Availability = FolderAvailabilityPlanUnavailable
		default:
			member.BlueprintID, member.BlueprintLabel = row.Blueprint.BlueprintID, row.Blueprint.Label
		}
	}
	// Default names first, then what the user chose in Adjust over them.
	s.freeStructureNames(ctx, structure)
	if adjustment != nil && adjustment.GroupName != "" && structure.Group.Type == FolderGroupPlain {
		structure.Group.Name = adjustment.GroupName
	}
	if err := structure.applyMembers(adjustment); err != nil {
		return nil, err
	}
	// Only the user's own names are refused here. Default names were made free
	// above, and a proposal is never dropped because one of them is taken: the
	// setup's review says so, and Adjust can rename it.
	if adjustment != nil {
		if err := s.checkStructureNames(ctx, structure); err != nil {
			return nil, err
		}
	}
	if len(structure.Included()) == 0 && structure.Group.Type != FolderGroupLibrary {
		// Every folder is already a workspace: there is no group left to propose.
		return nil, nil
	}
	return structure, nil
}

// structureNameTaken reports whether a workspace or group of this name already
// exists. Workspace names share one namespace, whatever group they are in.
func (s *FolderDigestService) structureNameTaken(ctx context.Context, name string) bool {
	return s.deps.WorkspaceNameTaken != nil && s.deps.WorkspaceNameTaken(ctx, name)
}

// freeStructureNames gives a proposal default names that can actually be
// created: a group or workspace whose folder name is already taken, by an
// existing workspace or by another member, is proposed under a name that says
// where it comes from ("Album-1 (Albums)"), and then a number. The user can
// change any of them in Adjust.
func (s *FolderDigestService) freeStructureNames(ctx context.Context, structure *FolderStructure) {
	used := map[string]bool{}
	free := func(candidates ...string) string {
		for attempt := 0; attempt < 8; attempt++ {
			for _, candidate := range candidates {
				name := candidate
				if attempt > 0 {
					name = fmt.Sprintf("%s %d", candidate, attempt+1)
				}
				slug := workspace.Slugify(name)
				if validStructureWorkspaceName(name) && slug != "" && !used[slug] && !s.structureNameTaken(ctx, name) {
					used[slug] = true
					return name
				}
			}
		}
		return candidates[0]
	}
	if structure.Group.Type == FolderGroupPlain {
		structure.Group.Name = free(structure.Folder, structure.Folder+" group")
	}
	for i := range structure.Members {
		member := &structure.Members[i]
		if member.ExistingWorkspace == "" {
			member.Name = free(member.Folder, member.Folder+" ("+structure.Folder+")")
		}
	}
}

// checkStructureNames refuses a proposal that could not be created as it
// stands: its group, or a workspace it includes, would have a name that is
// already taken, here or by another of its own workspaces.
func (s *FolderDigestService) checkStructureNames(ctx context.Context, structure *FolderStructure) error {
	if structure.Group.Type == FolderGroupLibrary {
		return nil
	}
	seen := map[string]bool{}
	names := make([]string, 0, len(structure.Members)+1)
	if structure.Group.Type == FolderGroupPlain {
		names = append(names, structure.Group.Name)
	}
	for _, member := range structure.Included() {
		names = append(names, member.Name)
	}
	for _, name := range names {
		slug := workspace.Slugify(name)
		if slug == "" || seen[slug] {
			return structureAdjustmentError("two workspaces would have the name " + name)
		}
		if s.structureNameTaken(ctx, name) {
			return structureAdjustmentError("a workspace named " + name + " already exists")
		}
		seen[slug] = true
	}
	return nil
}

// adjusted applies Adjust choices to a stored proposal, for an offer made on
// Home, where no scan is held to describe the folder afresh. Members and what
// each can be stay as the scan found them; the group's type and name, and which
// members get a workspace under which name, are what change. existingHome says
// the owner already has the Home the collection would be listed in.
func (s *FolderStructure) adjusted(adjustment *FolderStructureAdjustment, existingHome bool) (*FolderStructure, error) {
	next := s.stored()
	switch adjustment.GroupType {
	case "":
	case FolderGroupPlain:
		if next.Group.Type != FolderGroupPlain {
			next.Group.Type, next.Group.Name, next.Group.DestinationState = FolderGroupPlain, s.Folder, "new"
		}
	case FolderGroupLibrary:
		if !s.Group.HomeAvailable || s.Group.HomeName == "" {
			return nil, structureAdjustmentError("this folder has no Home to use")
		}
		next.Group.Type, next.Group.Name, next.Group.DestinationState = FolderGroupLibrary, s.Group.HomeName, "new"
		if existingHome {
			next.Group.DestinationState = "existing"
		}
	default:
		return nil, structureAdjustmentError("a workspace for each folder inside " + s.Group.HomeName + " is not available yet")
	}
	if adjustment.GroupName != "" && next.Group.Type == FolderGroupPlain {
		next.Group.Name = adjustment.GroupName
	}
	if err := next.applyMembers(adjustment); err != nil {
		return nil, err
	}
	if next.Validate() != nil {
		return nil, structureAdjustmentError("the proposal would no longer be valid")
	}
	return next, nil
}

// AdjustStructure applies what the user changed in Adjust to a pending Home
// offer's proposed group. It creates nothing and scans nothing: the plan the
// card shows next is stamped from the adjusted proposal. A review made in a
// conversation is adjusted through that conversation's own Review instead.
func (s *FolderDigestService) AdjustStructure(ctx context.Context, userID, offerID string, adjustment FolderStructureAdjustment) (FolderOfferView, error) {
	if s == nil || s.store == nil {
		return FolderOfferView{}, ErrFolderOutcomeUnavailable
	}
	if err := adjustment.Validate(); err != nil {
		return FolderOfferView{}, err
	}
	binding, err := s.store.Binding(ctx, userID)
	if err != nil {
		return FolderOfferView{}, err
	}
	var adjusted FolderOffer
	_, err = s.store.Mutate(ctx, userID, func(d *FolderDigestDocument) error {
		item := d.Offer(offerID)
		if item == nil {
			return ErrFolderOfferNotFound
		}
		if item.Status != FolderOfferPending || item.ConversationReview != nil || item.Structure == nil {
			return ErrFolderOfferDecided
		}
		next, adjustErr := item.Structure.adjusted(&adjustment, item.Portfolio != nil && item.Portfolio.ExistingHome)
		if adjustErr != nil {
			return adjustErr
		}
		if nameErr := s.checkStructureNames(ctx, next); nameErr != nil {
			return nameErr
		}
		item.Structure = next
		adjusted = *item
		return nil
	})
	if err != nil {
		return FolderOfferView{}, err
	}
	return s.viewFor(ctx, userID, adjusted, binding.Paused), nil
}

// StructureMemberRoot returns one member's own directory under the root the
// server holds for a structure's offer. The path stays on the server: it is
// what a run links, never something a browser or a model is given. A member
// that is no longer the directory the review saw (moved, replaced, or turned
// into a link) is ErrFolderPathLost, and nothing is linked.
func (s *FolderDigestService) StructureMemberRoot(root string, member FolderStructureMember) (string, error) {
	if s == nil || strings.TrimSpace(root) == "" || member.RelPath == "" || validateFolderName(member.RelPath) != nil {
		return "", ErrFolderPathLost
	}
	path := filepath.Join(root, member.RelPath)
	canonical, err := s.deps.ValidateRoot(path)
	if err != nil || canonical != path {
		return "", ErrFolderPathLost
	}
	identity, err := portfolioDirectoryIdentity(path)
	if err != nil {
		// A platform with no directory identity relies on the canonical path.
		if member.Identity != "" || !errors.Is(err, projectlibrary.ErrUnavailable) {
			return "", ErrFolderPathLost
		}
		return path, nil
	}
	if member.Identity != "" && identity != member.Identity {
		return "", ErrFolderPathLost
	}
	return path, nil
}

// resolveStructure settles a finished structure run: the host re-reads the
// group and its workspaces from canonical state, and only what it read back
// becomes the offer's outcome and receipt.
func (s *FolderDigestService) resolveStructure(ctx context.Context, userID, offerID string) error {
	if s.deps.StructureGroup == nil {
		return ErrFolderOutcomeUnavailable
	}
	doc, err := s.store.Read(ctx, userID)
	if err != nil {
		return err
	}
	offer := doc.Offer(offerID)
	if offer == nil || offer.Setup == nil || offer.Setup.Status != FolderSetupDone || offer.Setup.HomeID == "" ||
		offer.Status != FolderOfferAwaitingOutcome || offer.Setup.Intent.Structure == nil {
		return ErrFolderOfferDecided
	}
	root, err := s.subjectPathOf(*offer)
	if err != nil {
		return err
	}
	verified, err := s.deps.StructureGroup(ctx, userID, *offer, root, offer.Setup.HomeID)
	if err != nil || verified.WorkspaceID == "" || !strings.HasPrefix(verified.Route, "/workspaces/") || len(verified.Receipt) == 0 {
		return ErrFolderWorkspaceRefused
	}
	now := s.now()
	var resolved FolderOffer
	_, err = s.store.Mutate(ctx, userID, func(d *FolderDigestDocument) error {
		item := d.Offer(offerID)
		if item == nil || item.Status != FolderOfferAwaitingOutcome {
			return ErrFolderOfferDecided
		}
		item.Status, item.ResolvedAt = FolderOfferResolved, &now
		item.Outcome = &FolderOutcome{
			Kind: FolderChoiceGroup, WorkspaceID: verified.WorkspaceID, Route: verified.Route,
			Receipt: append([]FolderReceiptRow(nil), verified.Receipt...),
		}
		d.Receipts = append(d.Receipts, FolderReceipt{RequestID: "setup-" + offerID + "-structure", OfferID: offerID, Action: "structure", At: now})
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

func validFolderAvailability(reason string) bool {
	switch reason {
	case "", FolderAvailabilityIntegrationNotInstalled, FolderAvailabilityProviderNotInstalled,
		FolderAvailabilityProviderUnverified, FolderAvailabilityDeclinedEarlier, FolderAvailabilityPlanUnavailable:
		return true
	}
	return false
}

// FolderStructure is a path-free proposal: one group and its member
// workspaces. It is a description, never a plan or an execution grant.
type FolderStructure struct {
	Version        int                   `json:"version"`
	Recommendation *FolderRecommendation `json:"recommendation,omitempty"`
	// Folder is the attached folder's display name.
	Folder  string                  `json:"folder"`
	Group   FolderStructureGroup    `json:"group"`
	Members []FolderStructureMember `json:"members"`
	// MinMembers is the recognition rule the proposal was made under: how many
	// project subfolders make a folder a collection.
	MinMembers int `json:"min_members"`
	// Projects counts the members recognized as projects; the rest are other
	// folders that hold files.
	Projects int `json:"projects"`
	// Omitted counts subfolders left out of the bounded member list.
	Omitted int `json:"omitted,omitempty"`
	// Notes are the availability sentences of the group and the included
	// members, each said once. Projected for display; never stored.
	Notes []string `json:"notes,omitempty"`
	// Arrangement is set on a selected structure (version 2) only: the saved
	// arrangement intention its exact members came from.
	Arrangement *FolderStructureArrangement `json:"arrangement,omitempty"`
}

// FolderStructureGroup is the proposed parent of the member workspaces.
type FolderStructureGroup struct {
	Name string `json:"name"`
	Type string `json:"type"` // FolderGroupHome or FolderGroupPlain
	// DestinationState is "new" for a group this setup creates and "existing"
	// for the owner's Home the members join.
	DestinationState string `json:"destination_state"`
	// HomeName names the shape's Home when the collection has one to offer,
	// whether or not it is the proposed type. HomeAvailable says it can be
	// chosen now; Availability says why not.
	HomeName      string `json:"home_name,omitempty"`
	HomeAvailable bool   `json:"home_available,omitempty"`
	Availability  string `json:"availability,omitempty"`
}

// FolderStructureMember is one subfolder and the workspace proposed for it.
type FolderStructureMember struct {
	// CandidateID is opaque within the scan the proposal was built from.
	CandidateID string `json:"candidate_id"`
	// Name is the proposed workspace name; Folder the subfolder's display name,
	// which a rename never changes.
	Name   string `json:"name"`
	Folder string `json:"folder"`
	// TypeLabel says what the folder was recognized as (its marker's label), or
	// "Plain workspace".
	TypeLabel string `json:"type_label"`
	Shape     string `json:"shape,omitempty"`
	Marker    string `json:"marker,omitempty"`
	// Project says the folder carries a project signal of its own.
	Project     bool   `json:"project,omitempty"`
	Files       int    `json:"files"`
	LastChanged string `json:"last_changed,omitempty"`
	Included    bool   `json:"included"`
	// BlueprintID is the blueprint the workspace would start from; empty is the
	// blank workspace. Integration names the reviewed integration the member's
	// own blueprint needs, when it needs one.
	BlueprintID    string `json:"blueprint_id,omitempty"`
	BlueprintLabel string `json:"blueprint_label,omitempty"`
	Integration    string `json:"integration,omitempty"`
	Availability   string `json:"availability,omitempty"`
	// ExistingWorkspace names the workspace this folder already belongs to.
	ExistingWorkspace string `json:"existing_workspace,omitempty"`

	// The fields below stay on the server: they are cleared from every view.
	// RelPath is the subfolder's own name below the root, and MarkerName and
	// DominantExtension are what it was recognized by, for the run and for a
	// later integration to find the workspace again.
	RelPath           string `json:"rel_path,omitempty"`
	MarkerName        string `json:"marker_name,omitempty"`
	DominantExtension string `json:"dominant_ext,omitempty"`
	// Identity witnesses the member's directory once a review is opened on it.
	Identity string `json:"identity,omitempty"`
}

// BuildFolderStructure describes a scanned root as a group with one workspace
// per subfolder, or returns nil when the root is not a collection (PRD FR1).
// Every immediate subfolder that holds a file is a member, included by default
// (FR2). The group is the shape's Home when the scan saw a portfolio and
// homeAvailable says that Home can be planned; otherwise a plain group named
// after the folder (FR4). What each member's workspace starts from, and why a
// part is unavailable, is filled in by the host afterwards.
func BuildFolderStructure(result folderdigest.Result, verdict folderdigest.Verdict, now time.Time, homeAvailable bool) *FolderStructure {
	if !folderdigest.IsCollection(result, now) {
		return nil
	}
	folder := foldercontext.DisplayName(result.Name)
	if folder == "" {
		folder = "Selected folder"
	}
	structure := &FolderStructure{
		Version: FolderStructureVersion, Folder: folder, MinMembers: folderdigest.CollectionMinProjects,
		Group:   FolderStructureGroup{Name: folder, Type: FolderGroupPlain, DestinationState: "new"},
		Members: []FolderStructureMember{},
	}
	// A member is a subfolder that holds a file, or one a marker alone makes a
	// project. When there are more than the list can hold, projects are kept
	// before other folders; the list itself stays in the scan's name order.
	keep := map[int]bool{}
	for _, wantProject := range []bool{true, false} {
		for index, candidate := range result.Candidates {
			project := folderdigest.IsProject(candidate, now)
			if candidate.IsRoot || project != wantProject || (!project && candidate.FileCount < 1) {
				continue
			}
			if len(keep) >= FolderStructureMaxMembers {
				structure.Omitted++
				continue
			}
			keep[index] = true
		}
	}
	for index, candidate := range result.Candidates {
		if !keep[index] {
			continue
		}
		name := foldercontext.DisplayName(candidate.Name)
		if name == "" {
			name = "Unnamed folder"
		}
		member := FolderStructureMember{
			CandidateID: "candidate-" + strconv.Itoa(index), Name: name, Folder: name,
			TypeLabel: folderPlainWorkspaceLabel, Shape: string(folderdigest.ShapeFor(candidate)),
			Project: folderdigest.IsProject(candidate, now), Files: candidate.FileCount, Included: true,
			RelPath: candidate.RelPath, DominantExtension: candidate.DominantExtension,
		}
		if member.Files > foldercontext.MaxEntries {
			member.Files = foldercontext.MaxEntries
		}
		if candidate.Marker != nil {
			member.Marker, member.MarkerName = foldercontext.DisplayName(candidate.Marker.Label), candidate.Marker.Name
			if member.Marker != "" {
				member.TypeLabel = member.Marker
			}
		}
		if !candidate.NewestModTime.IsZero() {
			member.LastChanged = candidate.NewestModTime.Format(foldercontext.DateLayout)
		}
		if member.Project {
			structure.Projects++
		}
		structure.Members = append(structure.Members, member)
	}
	if verdict.Portfolio != nil {
		if row, ok := folderdigest.CapabilityForShape(verdict.Portfolio.Shape); ok && row.Offer != nil && row.Offer.HomeName != "" {
			structure.Group.HomeName = row.Offer.HomeName
			if homeAvailable {
				structure.Group.HomeAvailable = true
				structure.Group.Type, structure.Group.Name = FolderGroupHome, row.Offer.HomeName
			}
		}
	}
	return structure
}

// Included returns the members a setup would create a workspace for.
func (s *FolderStructure) Included() []FolderStructureMember {
	if s == nil {
		return nil
	}
	included := make([]FolderStructureMember, 0, len(s.Members))
	for _, member := range s.Members {
		if member.Included {
			included = append(included, member)
		}
	}
	return included
}

// ProposesWorkspaces selects the group/member execution path. It is false for
// library listing and must not be used to decide whether discovery is visible.
func (s *FolderStructure) ProposesWorkspaces() bool {
	return s != nil && s.Group.Type != FolderGroupLibrary
}

// AvailabilityNotes are the sentences for what the proposal goes without: the
// group's first, then each included member's, every sentence once.
func (s *FolderStructure) AvailabilityNotes() []string { return s.availabilityNotes() }

// summary names the proposal in a few words ("Music Production Home with 6
// workspaces", "Group with 2 workspaces").
func (s *FolderStructure) summary() string {
	if s.Group.Type == FolderGroupLibrary {
		return s.Group.Name + " library"
	}
	group := "Group"
	if s.Group.Type == FolderGroupHome {
		group = s.Group.Name
	}
	count := len(s.Included())
	if count == 1 {
		return group + " with 1 workspace"
	}
	return fmt.Sprintf("%s with %d workspaces", group, count)
}

// Member finds a member by its candidate id.
func (s *FolderStructure) Member(candidateID string) *FolderStructureMember {
	if s == nil {
		return nil
	}
	for i := range s.Members {
		if s.Members[i].CandidateID == candidateID {
			return &s.Members[i]
		}
	}
	return nil
}

// availabilityNotes are the sentences a card shows: the group's first, then
// each included member's, every sentence once.
func (s *FolderStructure) availabilityNotes() []string {
	if s == nil {
		return nil
	}
	var notes []string
	add := func(note string) {
		if note == "" {
			return
		}
		for _, seen := range notes {
			if seen == note {
				return
			}
		}
		notes = append(notes, note)
	}
	add(FolderAvailabilityCopy(s.Group.Availability, s.Group.HomeName))
	for _, member := range s.Members {
		if member.Included {
			add(FolderAvailabilityCopy(member.Availability, member.Integration))
		}
	}
	return notes
}

// clone returns a copy that shares no slice with the original.
func (s *FolderStructure) clone() *FolderStructure {
	if s == nil {
		return nil
	}
	copied := *s
	copied.Members = append([]FolderStructureMember(nil), s.Members...)
	copied.Notes = append([]string(nil), s.Notes...)
	if s.Recommendation != nil {
		recommendation := *s.Recommendation
		copied.Recommendation = &recommendation
	}
	if s.Arrangement != nil {
		binding := *s.Arrangement
		copied.Arrangement = &binding
	}
	return &copied
}

// view is the structure as a browser or a model may see it: the server's own
// fields are cleared and the availability sentences are filled in.
func (s *FolderStructure) view() *FolderStructure {
	if s == nil {
		return nil
	}
	view := s.clone()
	for i := range view.Members {
		view.Members[i].RelPath, view.Members[i].MarkerName = "", ""
		view.Members[i].DominantExtension, view.Members[i].Identity = "", ""
	}
	view.Notes = view.availabilityNotes()
	return view
}

// stored is the structure as the sidecar keeps it: without the projected notes.
func (s *FolderStructure) stored() *FolderStructure {
	if s == nil {
		return nil
	}
	stored := s.clone()
	stored.Notes = nil
	return stored
}

func validStructureName(value string) bool {
	return value != "" && utf8.ValidString(value) && utf8.RuneCountInString(value) <= foldercontext.MaxNameRunes &&
		foldercontext.DisplayName(value) == value
}

// Validate keeps a structure bounded and path-free, the way an observation is:
// display names only, known types and reasons, counts within the scan's bounds.
func (s FolderStructure) Validate() error {
	invalid := func(part string) error { return fmt.Errorf("%w: structure %s", errFolderDigestInvalid, part) }
	if !s.Recommendation.valid() {
		return invalid("recommendation")
	}
	selected := s.Version == FolderStructureSelectedVersion
	if selected {
		return s.validateSelected(invalid)
	}
	if s.Version != FolderStructureVersion || !validStructureName(s.Folder) || s.MinMembers != folderdigest.CollectionMinProjects || s.Arrangement != nil {
		return invalid("header")
	}
	if !validStructureName(s.Group.Name) ||
		(s.Group.Type != FolderGroupHome && s.Group.Type != FolderGroupPlain && s.Group.Type != FolderGroupLibrary) ||
		(s.Group.DestinationState != "new" && s.Group.DestinationState != "existing") ||
		(s.Group.HomeName != "" && !validStructureName(s.Group.HomeName)) || !validFolderAvailability(s.Group.Availability) {
		return invalid("group")
	}
	// A Home is proposed only while it can be chosen, and only a Home can be one
	// the owner already has.
	if (s.Group.Type != FolderGroupPlain && (!s.Group.HomeAvailable || s.Group.HomeName == "")) ||
		(s.Group.Type == FolderGroupPlain && s.Group.DestinationState != "new") {
		return invalid("group type")
	}
	if len(s.Members) < s.MinMembers || len(s.Members) > FolderStructureMaxMembers ||
		s.Projects < s.MinMembers || s.Projects > len(s.Members) || s.Omitted < 0 || s.Omitted > foldercontext.MaxEntries {
		return invalid("members")
	}
	seen := map[string]bool{}
	for _, member := range s.Members {
		if !validStructureName(member.CandidateID) || seen[member.CandidateID] || !validStructureName(member.Name) ||
			!validStructureName(member.Folder) || !validStructureName(member.TypeLabel) ||
			member.Files < 0 || member.Files > foldercontext.MaxEntries || (member.Files == 0 && !member.Project) ||
			!validFolderAvailability(member.Availability) {
			return invalid("member")
		}
		seen[member.CandidateID] = true
		for _, text := range []string{member.Shape, member.Marker, member.BlueprintID, member.BlueprintLabel,
			member.Integration, member.ExistingWorkspace, member.MarkerName, member.DominantExtension, member.Identity} {
			if len(text) > 200 || strings.ContainsAny(text, "/\\\x00\r\n") {
				return invalid("member text")
			}
		}
		if member.RelPath != "" && validateFolderName(member.RelPath) != nil {
			return invalid("member folder")
		}
		if member.LastChanged != "" {
			if _, err := time.Parse(foldercontext.DateLayout, member.LastChanged); err != nil {
				return invalid("member date")
			}
		}
	}
	if len(s.Notes) > FolderStructureMaxMembers+1 {
		return invalid("notes")
	}
	encoded, err := json.Marshal(s)
	if err != nil || len(encoded) > folderStructureMaxBytes {
		return invalid("size")
	}
	return nil
}

// validateSelected holds a saved arrangement's selection to its own rules, not
// the recognition rule's: only a new plain group, between one and the creator's
// limit of exact members, every one of them created, none already a workspace,
// and no omitted tail. It never accepts a weaker version-1 proposal.
func (s FolderStructure) validateSelected(invalid func(string) error) error {
	if !validStructureName(s.Folder) || s.MinMembers != 0 || s.Omitted != 0 || !s.Arrangement.valid() {
		return invalid("selection header")
	}
	if !validStructureWorkspaceName(s.Group.Name) || s.Group.Type != FolderGroupPlain || s.Group.DestinationState != "new" ||
		s.Group.HomeName != "" || s.Group.HomeAvailable || !validFolderAvailability(s.Group.Availability) {
		return invalid("selection group")
	}
	if len(s.Members) < 1 || len(s.Members) > FolderStructureMaxMembers || s.Projects < 0 || s.Projects > len(s.Members) {
		return invalid("selection members")
	}
	seen := map[string]bool{}
	for _, member := range s.Members {
		if !validStructureName(member.CandidateID) || member.CandidateID == folderRootCandidateID || seen[member.CandidateID] ||
			!validStructureWorkspaceName(member.Name) || !validStructureName(member.Folder) || member.TypeLabel != folderPlainWorkspaceLabel ||
			member.Files < 0 || member.Files > foldercontext.MaxEntries || !member.Included || member.ExistingWorkspace != "" ||
			member.BlueprintID != "" || member.BlueprintLabel != "" || member.Integration != "" || !validFolderAvailability(member.Availability) ||
			member.RelPath == "" || validateFolderName(member.RelPath) != nil {
			return invalid("selection member")
		}
		seen[member.CandidateID] = true
		for _, text := range []string{member.Shape, member.Marker, member.MarkerName, member.DominantExtension, member.Identity} {
			if len(text) > 200 || strings.ContainsAny(text, "/\\\x00\r\n") {
				return invalid("selection member text")
			}
		}
		if member.LastChanged != "" {
			if _, err := time.Parse(foldercontext.DateLayout, member.LastChanged); err != nil {
				return invalid("selection member date")
			}
		}
	}
	if len(s.Notes) > FolderStructureMaxMembers+1 {
		return invalid("notes")
	}
	encoded, err := json.Marshal(s)
	if err != nil || len(encoded) > folderStructureMaxBytes {
		return invalid("size")
	}
	return nil
}

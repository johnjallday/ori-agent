package personalassistant

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
)

// rootStructure is the proposal an explicit review of the Albums root would
// carry, with the given Adjust choices.
func rootStructure(t *testing.T, service *FolderObservationService, observationID string, adjustment *FolderStructureAdjustment) (*FolderStructure, error) {
	t.Helper()
	held := service.selections[observationID]
	target := held.target
	target.ConversationID, target.DraftID = "chat-1", ""
	offer, err := service.prepareReviewOffer(WithFolderStructureAdjustment(context.Background(), adjustment), held, target, folderRootCandidateID)
	return offer.Structure, err
}

func TestFolderStructureAdjustment_RenamesTheGroupAndUnchecksAMember(t *testing.T) {
	f, service, _, observation := albumsObservation(t)
	f.service.deps.Setup = &structureSetup{fakeFolderSetup: &fakeFolderSetup{lines: oneCardLines()},
		home: FolderAvailabilityProviderNotInstalled, integration: FolderAvailabilityIntegrationNotInstalled}
	base, err := rootStructure(t, service, observation.ID, nil)
	if err != nil || base == nil || len(base.Included()) != 6 {
		t.Fatalf("base proposal: %+v, %v", base, err)
	}
	var sketch, first string
	for _, member := range base.Members {
		if member.Folder == "Logic Sketch" {
			sketch = member.CandidateID
		} else if first == "" {
			first = member.CandidateID
		}
	}
	adjusted, err := rootStructure(t, service, observation.ID, &FolderStructureAdjustment{
		GroupName: "Albums 2026",
		Members: []FolderStructureMemberAdjustment{
			{CandidateID: sketch, Included: false},
			{CandidateID: first, Included: true, Name: "First Record"},
		},
	})
	if err != nil || adjusted.Validate() != nil {
		t.Fatalf("adjusted proposal: %v / %v", err, adjusted)
	}
	if adjusted.Group.Name != "Albums 2026" || adjusted.Group.Type != FolderGroupPlain || len(adjusted.Included()) != 5 ||
		adjusted.summary() != "Group with 5 workspaces" {
		t.Fatalf("group: %+v, %s", adjusted.Group, adjusted.summary())
	}
	renamed := adjusted.Member(first)
	if renamed.Name != "First Record" || renamed.Folder == "First Record" || adjusted.Member(sketch).Included {
		t.Fatalf("members: %+v / %+v", renamed, adjusted.Member(sketch))
	}
	// The sheet always names the group type. With no Home to choose, a plain
	// group is still the fallback and still says why.
	chosen, err := rootStructure(t, service, observation.ID, &FolderStructureAdjustment{GroupType: FolderGroupPlain})
	if err != nil || chosen.Group.Availability != FolderAvailabilityProviderNotInstalled {
		t.Fatalf("a plain group with no Home to choose: %+v, %v", chosen.Group, err)
	}
}

func TestFolderStructureAdjustment_RefusesWhatTheProposalCannotTake(t *testing.T) {
	f, service, _, observation := albumsObservation(t)
	f.service.deps.Setup = &structureSetup{fakeFolderSetup: &fakeFolderSetup{lines: oneCardLines()},
		home: FolderAvailabilityProviderNotInstalled, integration: FolderAvailabilityIntegrationNotInstalled}
	base, err := rootStructure(t, service, observation.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	none := make([]FolderStructureMemberAdjustment, 0, len(base.Members))
	for _, member := range base.Members {
		none = append(none, FolderStructureMemberAdjustment{CandidateID: member.CandidateID})
	}
	a, b := base.Members[0].CandidateID, base.Members[1].CandidateID
	cases := map[string]struct {
		adjustment FolderStructureAdjustment
		says       string
	}{
		"a folder that is not a member": {FolderStructureAdjustment{Members: []FolderStructureMemberAdjustment{{CandidateID: "candidate-99", Included: true}}}, "not part of this proposal"},
		"the root as a member":          {FolderStructureAdjustment{Members: []FolderStructureMemberAdjustment{{CandidateID: folderRootCandidateID, Included: true}}}, "not part of this proposal"},
		"every member unchecked":        {FolderStructureAdjustment{Members: none}, "choose at least one folder"},
		"two workspaces with one name": {FolderStructureAdjustment{Members: []FolderStructureMemberAdjustment{
			{CandidateID: a, Included: true, Name: "Same"}, {CandidateID: b, Included: true, Name: "same"},
		}}, "two workspaces would have the name"},
		"a workspace per folder in a Home": {FolderStructureAdjustment{GroupType: FolderGroupHome}, "is not available yet"},
	}
	// Listing the collection in its Home is the earlier setup, with its own
	// guided steps when the Home is not installed yet: it can always be chosen
	// for a folder that has a Home to offer.
	if library, err := rootStructure(t, service, observation.ID, &FolderStructureAdjustment{GroupType: FolderGroupLibrary}); err != nil ||
		library.Group.Type != FolderGroupLibrary || library.ProposesWorkspaces() {
		t.Fatalf("listing in a Home that is not installed: %+v, %v", library, err)
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			adjustment := tc.adjustment
			structure, err := rootStructure(t, service, observation.ID, &adjustment)
			if !errors.Is(err, ErrFolderStructureAdjustment) || structure != nil || !strings.Contains(err.Error(), tc.says) {
				t.Fatalf("got %+v, %v", structure, err)
			}
		})
	}
	// A shape that is wrong before any proposal is looked at.
	for name, adjustment := range map[string]FolderStructureAdjustment{
		"a path as the group name":   {GroupName: "/Users/riley/Albums"},
		"a path as a workspace name": {Members: []FolderStructureMemberAdjustment{{CandidateID: a, Included: true, Name: "Albums/One"}}},
		"an unknown group type":      {GroupType: "portfolio"},
		"a folder named twice":       {Members: []FolderStructureMemberAdjustment{{CandidateID: a, Included: true}, {CandidateID: a}}},
	} {
		if !errors.Is(adjustment.Validate(), ErrFolderStructureAdjustment) {
			t.Fatalf("%s was accepted", name)
		}
	}
	// A single folder has no group to adjust.
	held := service.selections[observation.ID]
	target := held.target
	target.ConversationID, target.DraftID = "chat-1", ""
	ctx := WithFolderStructureAdjustment(context.Background(), &FolderStructureAdjustment{GroupName: "Anything"})
	if _, err := service.prepareReviewOffer(ctx, held, target, a); !errors.Is(err, ErrFolderStructureAdjustment) {
		t.Fatalf("an adjustment on a single folder: %v", err)
	}
}

func TestFolderStructureAdjustment_ChoosingTheHomeBringsADeclinedSetupBack(t *testing.T) {
	ctx := context.Background()
	f, service, target, observation := albumsObservation(t)
	f.service.deps.Setup = &structureSetup{fakeFolderSetup: &fakeFolderSetup{lines: oneCardLines()}}
	if _, err := f.store.Mutate(ctx, target.UserID, func(doc *FolderDigestDocument) error {
		doc.DomainDeclines = append(doc.DomainDeclines, FolderDomainDecline{Domain: "music_production", FirstEvidence: "portfolio", Count: 2, At: f.now})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	declined, err := rootStructure(t, service, observation.ID, nil)
	if err != nil || declined.Group.Type != FolderGroupPlain || declined.Group.Availability != FolderAvailabilityDeclinedEarlier || !declined.Group.HomeAvailable {
		t.Fatalf("declined proposal: %+v, %v", declined, err)
	}
	// A Home that is not installed says so, whatever was declined: that is what
	// keeps it from being chosen.
	ready := f.service.deps.Setup
	f.service.deps.Setup = &structureSetup{fakeFolderSetup: &fakeFolderSetup{lines: oneCardLines()}, home: FolderAvailabilityProviderNotInstalled}
	if missing, err := rootStructure(t, service, observation.ID, nil); err != nil || missing.Group.Availability != FolderAvailabilityProviderNotInstalled {
		t.Fatalf("declined and not installed: %+v, %v", missing, err)
	}
	f.service.deps.Setup = ready
	// Choosing to list the collection in the Home is an explicit yes to it.
	library, err := rootStructure(t, service, observation.ID, &FolderStructureAdjustment{GroupType: FolderGroupLibrary})
	if err != nil || library.Validate() != nil || library.Group.Type != FolderGroupLibrary ||
		library.Group.Name != "Music Production Home" || library.Group.Availability != "" || library.ProposesWorkspaces() {
		t.Fatalf("library choice: %+v, %v", library, err)
	}
	// A workspace per folder inside the Home is the Home's own doing, one
	// project at a time; no setup makes them all at once, so it is refused
	// rather than promised.
	if _, err := rootStructure(t, service, observation.ID, &FolderStructureAdjustment{GroupType: FolderGroupHome}); !errors.Is(err, ErrFolderStructureAdjustment) ||
		!strings.Contains(err.Error(), "is not available yet") {
		t.Fatalf("a Home with a workspace per folder: %v", err)
	}
	// And a plain group can still be chosen over the Home.
	plain, err := rootStructure(t, service, observation.ID, &FolderStructureAdjustment{GroupType: FolderGroupPlain})
	if err != nil || plain.Group.Type != FolderGroupPlain || plain.Group.Availability != "" || !plain.ProposesWorkspaces() {
		t.Fatalf("plain choice: %+v, %v", plain, err)
	}
}

func TestFolderStructureAdjustment_APendingReviewKeepsItsChoicesWhenOpenedAgain(t *testing.T) {
	ctx := context.Background()
	f, service, target, observation := albumsObservation(t)
	setup := &structureSetup{fakeFolderSetup: &fakeFolderSetup{lines: oneCardLines()},
		home: FolderAvailabilityProviderNotInstalled, integration: FolderAvailabilityIntegrationNotInstalled}
	f.service.deps.Setup = setup
	service.ConfigureConversationReviews(
		func(context.Context, FolderOffer) error { return nil },
		func(foldercontext.Target) (func(), bool) { return func() {}, true },
	)
	base, err := rootStructure(t, service, observation.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	sketch := ""
	for _, member := range base.Members {
		if member.Folder == "Logic Sketch" {
			sketch = member.CandidateID
		}
	}
	adjustment := &FolderStructureAdjustment{GroupName: "Albums 2026", Members: []FolderStructureMemberAdjustment{{CandidateID: sketch}}}
	first, err := service.Review(WithFolderStructureAdjustment(ctx, adjustment), target, "chat-1", observation.ID, folderRootCandidateID)
	if err != nil || first.Structure == nil || first.Structure.Group.Name != "Albums 2026" || len(first.Structure.Included()) != 5 {
		t.Fatalf("adjusted review: %+v, %v", first.Structure, err)
	}
	// The view a browser gets is the proposal, never the server's own fields.
	if first.Structure.Members[0].RelPath != "" || first.Structure.Members[0].Identity != "" {
		t.Fatalf("the review view carries server fields: %+v", first.Structure.Members[0])
	}
	// Review again, with nothing new chosen: the same offer, the same choices.
	saved := target
	saved.ConversationID, saved.DraftID = "chat-1", ""
	service.BindSaved(target, observation.ID, "chat-1")
	again, err := service.Review(ctx, saved, "chat-1", observation.ID, folderRootCandidateID)
	if err != nil || again.ID != first.ID || again.Structure.Group.Name != "Albums 2026" || len(again.Structure.Included()) != 5 {
		t.Fatalf("reopened review: %+v, %v", again.Structure, err)
	}
	// A new choice replaces the old one on the same offer.
	replaced, err := service.Review(WithFolderStructureAdjustment(ctx, &FolderStructureAdjustment{GroupName: "Records"}), saved, "chat-1", observation.ID, folderRootCandidateID)
	if err != nil || replaced.ID != first.ID || replaced.Structure.Group.Name != "Records" || len(replaced.Structure.Included()) != 6 {
		t.Fatalf("replaced choices: %+v, %v", replaced.Structure, err)
	}
	doc, err := f.store.Read(ctx, target.UserID)
	if err != nil || len(doc.Offers) != 1 || doc.Offers[0].Structure.Group.Name != "Records" || doc.Offers[0].Structure.Members[0].RelPath == "" {
		t.Fatalf("stored offer: %+v, %v", doc.Offers, err)
	}
}

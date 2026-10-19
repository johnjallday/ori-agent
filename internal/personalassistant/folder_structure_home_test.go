package personalassistant

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// workspaceSlugOf is the folder name a workspace of this name would get.
func workspaceSlugOf(name string) string { return workspace.Slugify(name) }

// homeCollectionFixture is Home's "Explore a folder" over the Albums collection
// in Music, on a host that can set a group up but has neither the Home's
// provider nor the integration installed.
func homeCollectionFixture(t *testing.T) (*folderDigestFixture, *fakeFolderCreator) {
	t.Helper()
	f := newFolderDigestFixture(t)
	seedAlbums(t, filepath.Join(f.home, "Music"))
	creator := &fakeFolderCreator{}
	f.service.deps.Creator = creator
	f.service.deps.HomeExists = func(context.Context, string, string) (bool, error) { return false, nil }
	f.service.deps.Setup = &structureSetup{
		fakeFolderSetup: &fakeFolderSetup{lines: oneCardLines()},
		home:            FolderAvailabilityProviderNotInstalled, integration: FolderAvailabilityIntegrationNotInstalled,
	}
	return f, creator
}

func TestFolderDigest_ACollectionExploredOnHomeIsOneGroupOffer(t *testing.T) {
	ctx := context.Background()
	f, creator := homeCollectionFixture(t)
	offer, err := f.service.ScanChip(ctx, "local", "music")
	if err != nil {
		t.Fatal(err)
	}
	if offer.Structure == nil || !offer.Structure.ProposesWorkspaces() || !offer.Subject.IsRoot || offer.Subject.Name != "Music" ||
		offer.Verdict != "project" || offer.Status != FolderOfferPending || len(offer.Structure.Included()) != 6 ||
		offer.Structure.Group.Name != "Music" || offer.Structure.Group.Availability != FolderAvailabilityProviderNotInstalled {
		t.Fatalf("Home offer: %+v / %+v", offer, offer.Structure)
	}
	// The plan is the group's own, so Set up is there although no plugin is.
	if offer.Plan == nil || offer.Plan.Lines[0].Name != "Creates group Music" || offer.SetupUnavailableReason != "" {
		t.Fatalf("plan: %+v, reason %q", offer.Plan, offer.SetupUnavailableReason)
	}
	doc, err := f.store.Read(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	stored := doc.Offer(offer.ID)
	if stored == nil || len(stored.Queue) != 0 || stored.RootIdentity == "" || stored.Structure.Members[0].RelPath == "" || stored.NeedsCandidate {
		t.Fatalf("stored offer: %+v", stored)
	}
	// A bare yes can not turn the whole folder into one workspace.
	_, err = f.service.Decide(ctx, "local", offer.ID, FolderDecisionInput{Decision: FolderDecisionYes, Choice: FolderChoiceProject, Create: true, RequestID: "yes-1"})
	if !errors.Is(err, ErrFolderReviewRequired) || len(creator.requests) != 0 {
		t.Fatalf("a bare yes: %v, %d creates", err, len(creator.requests))
	}
}

func TestFolderDigest_AdjustOnHomeChangesTheProposalAndItsPlan(t *testing.T) {
	ctx := context.Background()
	f, _ := homeCollectionFixture(t)
	offer, err := f.service.ScanChip(ctx, "local", "music")
	if err != nil {
		t.Fatal(err)
	}
	var members []FolderStructureMemberAdjustment
	for _, member := range offer.Structure.Members {
		members = append(members, FolderStructureMemberAdjustment{CandidateID: member.CandidateID, Name: member.Name, Included: member.Folder != "Logic Sketch"})
	}
	adjusted, err := f.service.AdjustStructure(ctx, "local", offer.ID, FolderStructureAdjustment{GroupType: FolderGroupPlain, GroupName: "Albums 2026", Members: members})
	if err != nil || adjusted.ID != offer.ID || adjusted.Structure.Group.Name != "Albums 2026" || len(adjusted.Structure.Included()) != 5 ||
		adjusted.Plan == nil || adjusted.Plan.Lines[0].Name != "Creates group Albums 2026" || adjusted.Plan.Digest == offer.Plan.Digest {
		t.Fatalf("adjusted offer: %+v, %v", adjusted.Structure, err)
	}
	// The view never carries the server's own fields; the stored record keeps them.
	if adjusted.Structure.Members[0].RelPath != "" || adjusted.Structure.Members[0].Identity != "" {
		t.Fatalf("view member: %+v", adjusted.Structure.Members[0])
	}
	doc, err := f.store.Read(ctx, "local")
	if err != nil || doc.Offer(offer.ID).Structure.Members[0].RelPath == "" || doc.Offer(offer.ID).Structure.Validate() != nil {
		t.Fatalf("stored structure: %v", err)
	}

	// What the proposal cannot take is refused, and nothing is saved.
	for name, adjustment := range map[string]FolderStructureAdjustment{
		"a folder that is not a member": {Members: []FolderStructureMemberAdjustment{{CandidateID: "candidate-99", Included: true}}},
		"every folder unchecked": {Members: func() []FolderStructureMemberAdjustment {
			none := append([]FolderStructureMemberAdjustment(nil), members...)
			for i := range none {
				none[i].Included = false
			}
			return none
		}()},
		"a workspace per folder in the Home": {GroupType: FolderGroupHome},
		"a path as the group name":           {GroupName: "/Users/riley/Albums"},
	} {
		if _, err := f.service.AdjustStructure(ctx, "local", offer.ID, adjustment); !errors.Is(err, ErrFolderStructureAdjustment) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	kept, err := f.store.Read(ctx, "local")
	if err != nil || kept.Offer(offer.ID).Structure.Group.Name != "Albums 2026" || len(kept.Offer(offer.ID).Structure.Included()) != 5 {
		t.Fatalf("a refused adjustment changed the offer: %v", err)
	}

	// "List them in the Home" is the earlier collection setup, with its own
	// card and steps; choosing the plain group again brings the proposal back.
	listed, err := f.service.AdjustStructure(ctx, "local", offer.ID, FolderStructureAdjustment{GroupType: FolderGroupLibrary})
	if err != nil || listed.Structure.ProposesWorkspaces() || listed.Structure.Group.Type != FolderGroupLibrary ||
		listed.Structure.Group.Name != "Music Production Home" || listed.Capability == nil {
		t.Fatalf("listed in the Home: %+v, %v", listed.Structure, err)
	}
	back, err := f.service.AdjustStructure(ctx, "local", offer.ID, FolderStructureAdjustment{GroupType: FolderGroupPlain})
	if err != nil || !back.Structure.ProposesWorkspaces() || back.Structure.Group.Name != "Music" {
		t.Fatalf("back to a plain group: %+v, %v", back.Structure, err)
	}
}

func TestFolderDigest_AProposedGroupIsNamedAroundWhatAlreadyExists(t *testing.T) {
	ctx := context.Background()
	f, _ := homeCollectionFixture(t)
	// The user already has workspaces called Music and Album-1 (and Album-2,
	// under both of its natural names).
	taken := map[string]bool{"music": true, "album-1": true, "album-2": true, "album-2-music": true}
	f.service.deps.WorkspaceNameTaken = func(_ context.Context, name string) bool { return taken[workspaceSlugOf(name)] }
	offer, err := f.service.ScanChip(ctx, "local", "music")
	if err != nil || offer.Structure == nil {
		t.Fatalf("offer: %+v, %v", offer, err)
	}
	names := map[string]string{}
	for _, member := range offer.Structure.Members {
		names[member.Folder] = member.Name
	}
	if offer.Structure.Group.Name != "Music group" || names["Album-1"] != "Album-1 (Music)" || names["Album-2"] != "Album-2 2" ||
		names["Album-3"] != "Album-3" || names["Logic Sketch"] != "Logic Sketch" {
		t.Fatalf("names: group %q, members %v", offer.Structure.Group.Name, names)
	}
	// A name the user chooses must be free too, and says so when it is not.
	clash := FolderStructureAdjustment{Members: []FolderStructureMemberAdjustment{{CandidateID: offer.Structure.Members[2].CandidateID, Included: true, Name: "Album-1"}}}
	if _, err := f.service.AdjustStructure(ctx, "local", offer.ID, clash); !errors.Is(err, ErrFolderStructureAdjustment) ||
		err.Error() != ErrFolderStructureAdjustment.Error()+": a workspace named Album-1 already exists" {
		t.Fatalf("a taken workspace name: %v", err)
	}
	if _, err := f.service.AdjustStructure(ctx, "local", offer.ID, FolderStructureAdjustment{GroupName: "Music"}); !errors.Is(err, ErrFolderStructureAdjustment) {
		t.Fatalf("a taken group name: %v", err)
	}
	// Two folders that would become the same workspace folder are told apart.
	same := FolderStructureAdjustment{Members: []FolderStructureMemberAdjustment{
		{CandidateID: offer.Structure.Members[2].CandidateID, Included: true, Name: "Third Record"},
		{CandidateID: offer.Structure.Members[3].CandidateID, Included: true, Name: "third-record"},
	}}
	if _, err := f.service.AdjustStructure(ctx, "local", offer.ID, same); !errors.Is(err, ErrFolderStructureAdjustment) {
		t.Fatalf("two workspaces with one folder name: %v", err)
	}
}

func TestFolderDigest_NoThanksOnAGroupIsShownWhenTheFolderIsShownAgain(t *testing.T) {
	ctx := context.Background()
	f, _ := homeCollectionFixture(t)
	offer, err := f.service.ScanChip(ctx, "local", "music")
	if err != nil {
		t.Fatal(err)
	}
	if offer.Declined != nil {
		t.Fatalf("a folder never declined says it was: %+v", offer.Declined)
	}
	if _, err := f.service.Decide(ctx, "local", offer.ID, FolderDecisionInput{Decision: FolderDecisionNo, RequestID: "no-1"}); err != nil {
		t.Fatal(err)
	}
	doc, err := f.store.Read(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	// The No thanks is about this folder only; no kind of work was declined.
	key := doc.Offer(offer.ID).Subject.Key
	if !doc.Tombstoned(key) || len(doc.DomainDeclines) != 0 {
		t.Fatalf("after No thanks: tombstones %+v, declines %+v", doc.Tombstones, doc.DomainDeclines)
	}
	// Shown again, the folder is still proposed as its group, and the card says
	// when the user said no so it can be asked about again.
	again, err := f.service.ScanChip(ctx, "local", "music")
	if err != nil {
		t.Fatal(err)
	}
	if again.Structure == nil || !again.Structure.ProposesWorkspaces() || !again.Subject.IsRoot || len(again.Structure.Included()) != 6 ||
		again.Declined == nil || again.Declined.FolderKey != key || again.Declined.Name != "Music" || !again.Declined.At.Equal(f.now) {
		t.Fatalf("a declined folder was not proposed as its group: %+v / %+v", again, again.Declined)
	}
	stored, err := f.store.Read(ctx, "local")
	if err != nil || stored.Offer(again.ID).Declined == nil {
		t.Fatalf("the decline was not kept on the stored offer: %v", err)
	}
}

func TestFolderDigest_AReadyHomeIsAChoiceNotTheDefault(t *testing.T) {
	ctx := context.Background()
	f, _ := homeCollectionFixture(t)
	f.service.deps.Setup = &structureSetup{fakeFolderSetup: &fakeFolderSetup{lines: oneCardLines()}}
	offer, err := f.service.ScanChip(ctx, "local", "music")
	if err != nil {
		t.Fatal(err)
	}
	if offer.Structure == nil || offer.Structure.Group.Type != FolderGroupPlain || offer.Structure.Group.Name != "Music" ||
		!offer.Structure.Group.HomeAvailable || offer.Structure.Group.Availability != "" || offer.Portfolio == nil {
		t.Fatalf("Home offer with the Home ready: %+v", offer.Structure)
	}
	listed, err := f.service.AdjustStructure(ctx, "local", offer.ID, FolderStructureAdjustment{GroupType: FolderGroupLibrary})
	if err != nil || listed.Structure.Group.Type != FolderGroupLibrary || listed.Structure.Group.Name != "Music Production Home" {
		t.Fatalf("Adjust → the Home: %+v, %v", listed.Structure, err)
	}
}

func TestFolderDigest_OtherFoldersKeepTheirQuestion(t *testing.T) {
	ctx := context.Background()
	f, _ := homeCollectionFixture(t)
	// A landing zone of loose files with projects in it: a project, or a tidy.
	documents, err := f.service.ScanChip(ctx, "local", "documents")
	if err != nil || documents.Structure != nil || documents.Verdict != "mixed" || documents.Subject.IsRoot {
		t.Fatalf("a landing zone: %+v, %v", documents, err)
	}
	// A single project.
	if err := os.WriteFile(filepath.Join(f.home, "Desktop", "Song.rpp"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	desktop, err := f.service.ScanChip(ctx, "local", "desktop")
	if err != nil || desktop.Structure != nil || desktop.Verdict != "project" {
		t.Fatalf("a single project: %+v, %v", desktop, err)
	}
	// Fewer than five same-kind projects: still a group, with no Home to offer.
	trio := filepath.Join(f.home, "Downloads", "Trio")
	for _, name := range []string{"One", "Two", "Three"} {
		if err := os.MkdirAll(filepath.Join(trio, name), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(trio, name, name+".rpp"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	f.service.deps.Picker = fakeFolderPicker{path: trio, chosen: true}
	picked, err := f.service.ScanPicked(ctx, "local")
	if err != nil || picked == nil || picked.Structure == nil || picked.Portfolio != nil || picked.Structure.Group.HomeName != "" ||
		len(picked.Structure.Included()) != 3 || picked.Structure.Group.Name != "Trio" || !picked.Subject.IsRoot {
		t.Fatalf("three projects: %+v, %v", picked, err)
	}
	if _, err := f.service.AdjustStructure(ctx, "local", picked.ID, FolderStructureAdjustment{GroupType: FolderGroupLibrary}); !errors.Is(err, ErrFolderStructureAdjustment) {
		t.Fatalf("a folder with no Home was listed in one: %v", err)
	}
}

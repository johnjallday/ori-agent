package personalassistant

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
	"github.com/johnjallday/ori-agent/internal/folderdigest"
)

// reasonedPlanError is a host's plan failure that says why, the way the setup
// host's own errors do.
type reasonedPlanError string

func (e reasonedPlanError) Error() string             { return "plan unavailable: " + string(e) }
func (e reasonedPlanError) SetupAvailability() string { return string(e) }

// structureSetup is a setup host that can also say what a collection's proposal
// can use. home and integration are the reasons it gives ("" is available).
type structureSetup struct {
	*fakeFolderSetup
	home, integration           string
	homeReads, integrationReads int
}

// Plan plans a proposed group itself, as the real host does: a group needs no
// plugin, so whatever the fake says about plugins does not stand in its way.
func (s *structureSetup) Plan(ctx context.Context, req FolderSetupRequest) (FolderSetupPlan, error) {
	if req.Offer.Structure.ProposesWorkspaces() {
		return NewFolderSetupPlan([]FolderPlanLine{{Kind: FolderPlanGroup, Name: "Creates group " + req.Offer.Structure.Group.Name}}).Stamped(), nil
	}
	return s.fakeFolderSetup.Plan(ctx, req)
}

func (s *structureSetup) HomeAvailability(context.Context, string, string) string {
	s.homeReads++
	return s.home
}

func (s *structureSetup) IntegrationAvailability(context.Context, string, string) string {
	s.integrationReads++
	return s.integration
}

// albumsObservation attaches the Albums collection through the Music chip.
func albumsObservation(t *testing.T) (*folderDigestFixture, *FolderObservationService, foldercontext.Target, *foldercontext.Observation) {
	t.Helper()
	f, service, target := observationFixture(t)
	seedAlbums(t, filepath.Join(f.home, "Music"))
	f.service.deps.Creator = &fakeFolderCreator{}
	f.service.deps.HomeExists = func(context.Context, string, string) (bool, error) { return false, nil }
	observation, err := service.Observe(context.Background(), target, "chip", "music")
	if err != nil {
		t.Fatal(err)
	}
	return f, service, target, observation
}

func TestFolderReviewOptions_ACollectionRootIsAStructureListedFirst(t *testing.T) {
	ctx := context.Background()
	f, service, target, observation := albumsObservation(t)
	before, err := f.store.Read(ctx, target.UserID)
	if err != nil {
		t.Fatal(err)
	}
	ids := f.ids
	// The sandbox case: neither the Home's provider nor the integration can be
	// used. The whole structure is still proposed, as a plain group.
	setup := &structureSetup{
		fakeFolderSetup: &fakeFolderSetup{planErr: reasonedPlanError(FolderAvailabilityProviderUnverified)},
		home:            FolderAvailabilityProviderUnverified, integration: FolderAvailabilityIntegrationNotInstalled,
	}
	f.service.deps.Setup = setup
	options := service.ReviewOptions(ctx, target, observation.ID)
	if len(options) != 7 {
		t.Fatalf("the root and six folders should be listed, got %d: %+v", len(options), options)
	}
	root := options[0]
	p := root.Presentation
	if root.CandidateID != "candidate-0" || root.WorkspaceType != "Group with 6 workspaces" || p == nil || p.Kind != "structure" ||
		p.State != "proposed" || p.Effect != "group_workspaces" || p.GroupType != FolderGroupPlain ||
		p.DestinationState != "new" || p.DestinationName != "Music" || p.Availability != FolderAvailabilityProviderUnverified {
		t.Fatalf("root option: %+v / %+v", root, p)
	}
	sessions := 0
	for _, member := range p.Members {
		if !member.Included {
			t.Fatalf("member not included by default: %+v", member)
		}
		if member.TypeLabel == "REAPER session" {
			sessions++
			if member.Availability != FolderAvailabilityIntegrationNotInstalled {
				t.Fatalf("session member availability: %+v", member)
			}
		} else if member.TypeLabel != "Plain workspace" || member.Name != "Logic Sketch" || member.Availability != "" {
			t.Fatalf("plain member: %+v", member)
		}
	}
	wantNotes := []string{
		"Music Production Home could not be verified right now; a plain group is proposed instead.",
		"REAPER integration is not installed; it can be added later.",
	}
	if len(p.Members) != 6 || sessions != 5 || len(p.Notes) != 2 || p.Notes[0] != wantNotes[0] || p.Notes[1] != wantNotes[1] {
		t.Fatalf("members and notes: %d members, %d sessions, %q", len(p.Members), sessions, p.Notes)
	}
	// One integration is read once for five albums.
	if setup.homeReads != 1 || setup.integrationReads != 1 {
		t.Fatalf("availability reads: home %d, integration %d", setup.homeReads, setup.integrationReads)
	}
	// Each album is listed too, unavailable with the reason its own plan gave.
	for _, option := range options[1:] {
		candidate, ok := observedReviewCandidate(service.selections[observation.ID], option.CandidateID)
		if !ok || option.Presentation == nil {
			t.Fatalf("invented option: %+v", option)
		}
		if candidate.Name == "Logic Sketch" {
			if option.Presentation.State != "proposed" || option.WorkspaceType != "Blank workspace" {
				t.Fatalf("plain folder: %+v", option.Presentation)
			}
			continue
		}
		if option.Presentation.State != "unavailable" || option.Presentation.Availability != FolderAvailabilityProviderUnverified ||
			option.WorkspaceType != "REAPER song workspace" {
			t.Fatalf("%s: %+v / %s", candidate.Name, option.Presentation, option.WorkspaceType)
		}
	}
	data, err := json.Marshal(options)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{f.home, "rel_path", "identity", "offer_id", "plan_digest"} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("options carry %q: %s", forbidden, data)
		}
	}
	after, err := f.store.Read(ctx, target.UserID)
	if err != nil || after.Version != before.Version || f.ids != ids {
		t.Fatal("describing a collection wrote an offer or allocated an id")
	}

	// With the provider and the integration in place the default is still the
	// plain group: listing the collection in the shape's Home (which makes each
	// project's workspace when it is opened) waits in Adjust, so nothing is
	// created lazily behind the user's back.
	ready := &structureSetup{fakeFolderSetup: &fakeFolderSetup{lines: oneCardLines()}}
	f.service.deps.Setup = ready
	options = service.ReviewOptions(ctx, target, observation.ID)
	if len(options) != 7 || options[0].CandidateID != "candidate-0" || options[0].Structure == nil ||
		options[0].Presentation.Kind != "structure" || options[0].WorkspaceType != "Group with 6 workspaces" ||
		options[0].Structure.Group.Type != FolderGroupPlain || !options[0].Structure.Group.HomeAvailable ||
		options[0].Structure.Group.Availability != "" {
		t.Fatalf("collection with its Home in place: %+v / %+v", options[0], options[0].Presentation)
	}
	if attached := service.Structure(ctx, target, observation.ID); attached.Structure == nil || attached.Structure.Group.Type != FolderGroupPlain ||
		attached.Structure.Group.HomeName != "Music Production Home" || attached.Declined != nil || attached.Unavailable != "" {
		t.Fatalf("a collection whose Home is in place was not proposed as a plain group on attach: %+v", attached)
	}
	offer, err := service.prepareReviewOffer(ctx, service.selections[observation.ID], target, "candidate-0")
	if err != nil || offer.Structure == nil || offer.Structure.Validate() != nil || offer.Structure.Group.Type != FolderGroupPlain ||
		offer.Structure.Group.Name != "Music" || !offer.Structure.Group.HomeAvailable || offer.Portfolio == nil {
		t.Fatalf("root offer: %+v, %v", offer.Structure, err)
	}
	// The Home is one choice in Adjust.
	library, err := service.prepareReviewOffer(WithFolderStructureAdjustment(ctx, &FolderStructureAdjustment{GroupType: FolderGroupLibrary}),
		service.selections[observation.ID], target, "candidate-0")
	if err != nil || library.Structure.Group.Type != FolderGroupLibrary || library.Structure.Group.Name != "Music Production Home" {
		t.Fatalf("Adjust → the Home: %+v, %v", library.Structure, err)
	}
	// The members a group would get are known all the same: each folder is
	// witnessed, and none starts from a blueprint that lives in the Home.
	for _, member := range offer.Structure.Members {
		if member.Identity == "" || member.BlueprintID != "" {
			t.Fatalf("member: %+v", member)
		}
	}
}

func TestFolderReviewOptions_ADeclinedDomainDoesNotChangeTheConversation(t *testing.T) {
	ctx := context.Background()
	f, service, target, observation := albumsObservation(t)
	f.service.deps.Setup = &structureSetup{fakeFolderSetup: &fakeFolderSetup{lines: oneCardLines()}}
	before, err := json.Marshal(service.ReviewOptions(ctx, target, observation.ID))
	if err != nil {
		t.Fatal(err)
	}
	// A No thanks to the kind of work, in the digest and on the older app card.
	legacyReads := 0
	f.service.deps.LegacyDeclined = func(context.Context, string, string) (bool, error) {
		legacyReads++
		return true, nil
	}
	if _, err := f.store.Mutate(ctx, target.UserID, func(doc *FolderDigestDocument) error {
		doc.DomainDeclines = append(doc.DomainDeclines, FolderDomainDecline{Domain: "music_production", FirstEvidence: "portfolio", Count: 2, At: f.now})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	options := service.ReviewOptions(ctx, target, observation.ID)
	after, err := json.Marshal(options)
	if err != nil {
		t.Fatal(err)
	}
	// The user asked about this folder: every option is described as it is.
	if string(after) != string(before) || legacyReads != 0 || strings.Contains(string(after), FolderAvailabilityDeclinedEarlier) ||
		strings.Contains(string(after), "declined") {
		t.Fatalf("a declined domain changed the options (%d legacy reads):\nbefore %s\nafter  %s", legacyReads, before, after)
	}
	attached := service.Structure(ctx, target, observation.ID)
	if attached.Structure == nil || attached.Structure.Group.Availability != "" || attached.Declined != nil {
		t.Fatalf("attach after a declined domain: %+v", attached)
	}
	for _, member := range attached.Structure.Members {
		if member.Availability == FolderAvailabilityDeclinedEarlier {
			t.Fatalf("member said declined: %+v", member)
		}
	}
}

func TestFolderReviewOptions_ADeclinedCollectionIsStillProposedAndSaysSo(t *testing.T) {
	ctx := context.Background()
	f, service, target, observation := albumsObservation(t)
	f.service.deps.Setup = &structureSetup{fakeFolderSetup: &fakeFolderSetup{lines: oneCardLines()}}
	held := service.selections[observation.ID]
	albumID := ""
	for _, project := range observation.Projects {
		if project.Name == "Album-1" {
			albumID = project.ID
		}
	}
	declinedAt := f.now.AddDate(0, 0, -13)
	// One No thanks on the whole folder, one on a single album in it.
	if _, err := f.store.Mutate(ctx, target.UserID, func(doc *FolderDigestDocument) error {
		doc.Tombstones = append(doc.Tombstones,
			FolderTombstone{Key: FolderKey(held.root), Name: "Music", CreatedAt: declinedAt},
			FolderTombstone{Key: FolderKey(filepath.Join(held.root, "Album-1")), Name: "Album-1", CreatedAt: declinedAt})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	options := service.ReviewOptions(ctx, target, observation.ID)
	root := options[0]
	if len(options) != 6 || root.CandidateID != folderRootCandidateID || root.Structure == nil || len(root.Structure.Included()) != 6 ||
		root.Declined == nil || root.Declined.FolderKey != FolderKey(held.root) || root.Declined.Name != "Music" || !root.Declined.At.Equal(declinedAt) {
		t.Fatalf("the declined root option: %d options, %+v / %+v", len(options), root, root.Declined)
	}
	for _, option := range options {
		if option.CandidateID == albumID {
			t.Fatalf("a single folder the user declined was listed: %+v", option)
		}
	}
	attached := service.Structure(ctx, target, observation.ID)
	if attached.Structure == nil || attached.Declined == nil || attached.Declined.FolderKey != FolderKey(held.root) || attached.Unavailable != "" {
		t.Fatalf("attach of a declined collection: %+v", attached)
	}
	// The album the user declined on its own still has no question of its own.
	if _, err := service.prepareReviewOffer(ctx, held, target, albumID); !errors.Is(err, ErrFolderOfferDecided) {
		t.Fatalf("a declined single folder: %v", err)
	}
	data, err := json.Marshal(attached.Declined)
	if err != nil || strings.Contains(string(data), f.home) {
		t.Fatalf("the decline carries a path: %s", data)
	}
	// Reviewing it keeps the decline on the stored offer, which reads back valid.
	reviewed, err := service.Review(ctx, target, "conversation-declined", observation.ID, folderRootCandidateID)
	if err != nil || reviewed.Structure == nil || reviewed.Declined == nil || reviewed.Declined.Name != "Music" {
		t.Fatalf("review of a declined collection: %+v, %v", reviewed, err)
	}
	doc, err := f.store.Read(ctx, target.UserID)
	if err != nil || doc.Offer(reviewed.ID).Declined == nil || doc.Offer(reviewed.ID).Declined.FolderKey != FolderKey(held.root) {
		t.Fatalf("stored review: %v", err)
	}
}

func TestFolderStructure_OnlyAPendingReviewOrNoSetupHostKeepsTheCardAway(t *testing.T) {
	ctx := context.Background()
	f, service, target, observation := albumsObservation(t)
	f.service.deps.Setup = nil
	if got := service.Structure(ctx, target, observation.ID); got.Structure != nil || got.Unavailable != FolderStructureSetupUnavailable {
		t.Fatalf("no setup host: %+v", got)
	}
	f.service.deps.Setup = &structureSetup{fakeFolderSetup: &fakeFolderSetup{lines: oneCardLines()}}
	if got := service.Structure(ctx, target, observation.ID); got.Structure == nil || got.Unavailable != "" {
		t.Fatalf("a setup host: %+v", got)
	}
	if _, err := f.service.ScanChip(ctx, target.UserID, "documents"); err != nil {
		t.Fatal(err)
	}
	if got := service.Structure(ctx, target, observation.ID); got.Structure != nil || got.Unavailable != FolderStructureReviewPending {
		t.Fatalf("another review pending: %+v", got)
	}
	// A single folder has no card and no reason.
	desktop, err := service.Observe(ctx, target, "chip", "desktop")
	if err != nil {
		t.Fatal(err)
	}
	if got := service.Structure(ctx, target, desktop.ID); got != (FolderStructureProposal{}) {
		t.Fatalf("a single folder: %+v", got)
	}
}

func TestFolderReviewOptions_ASingleFolderIsStillOneWorkspace(t *testing.T) {
	ctx := context.Background()
	f, service, target := observationFixture(t)
	f.service.deps.Creator = &fakeFolderCreator{}
	f.service.deps.Setup = &structureSetup{fakeFolderSetup: &fakeFolderSetup{lines: oneCardLines()}}
	markNotInbox(t, f, "Desktop")
	observation, err := service.Observe(ctx, target, "chip", "desktop")
	if err != nil {
		t.Fatal(err)
	}
	options := service.ReviewOptions(ctx, target, observation.ID)
	if len(options) != 1 || options[0].WorkspaceType != "Blank workspace" || options[0].Presentation.Kind != "workspace" ||
		options[0].Presentation.State != "proposed" || len(options[0].Presentation.Members) != 0 || options[0].Presentation.Availability != "" {
		t.Fatalf("single folder: %+v / %+v", options, options[0].Presentation)
	}
}

func TestFolderReviewOptions_DescribingChoicesDoesNotRescanOrChooseScope(t *testing.T) {
	ctx := context.Background()
	f, service, target := observationFixture(t)
	creator := &fakeFolderCreator{}
	f.service.deps.Creator = creator
	observation, err := service.Observe(ctx, target, "chip", "documents")
	if err != nil {
		t.Fatal(err)
	}
	before, err := f.store.Read(ctx, target.UserID)
	if err != nil {
		t.Fatal(err)
	}
	ids := f.ids
	noScan := func(string) (folderdigest.Result, error) {
		t.Fatal("describing options rescanned the source")
		return folderdigest.Result{}, nil
	}
	f.service.deps.Scan, f.service.deps.ScanObservation = noScan, noScan
	// Missing and empty specialized plans both withdraw that option; plain
	// folders remain independently reviewable. No preferred or selected scope
	// is encoded merely because only some observed folders can be set up.
	baseline := service.ReviewOptions(ctx, target, observation.ID)
	f.service.deps.Setup = &fakeFolderSetup{}
	withEmptyPlan := service.ReviewOptions(ctx, target, observation.ID)
	if len(baseline) == 0 || len(withEmptyPlan) != len(baseline) {
		t.Fatalf("empty plan changed compatible choices: %+v / %+v", baseline, withEmptyPlan)
	}
	for _, option := range withEmptyPlan {
		data, err := json.Marshal(option)
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{f.home, "offer_id", "destination_id", "recommended", "selected", "plan_digest"} {
			if strings.Contains(string(data), forbidden) {
				t.Fatalf("description invented private/executable/default scope: %s", data)
			}
		}
	}
	after, err := f.store.Read(ctx, target.UserID)
	if err != nil || after.Version != before.Version || f.ids != ids || len(after.Offers) != len(before.Offers) || len(creator.requests) != 0 {
		t.Fatal("read-only choices allocated or executed setup")
	}
}

func TestFolderReviewOptions_ReadFailureWithdrawsDescriptions(t *testing.T) {
	ctx := context.Background()
	f, service, target := observationFixture(t)
	observation, err := service.Observe(ctx, target, "chip", "documents")
	if err != nil {
		t.Fatal(err)
	}
	before, err := f.store.Mutate(ctx, target.UserID, func(*FolderDigestDocument) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(f.folder.path, ".ori", folderDigestFileName)
	data, err := os.ReadFile(path) // #nosec G304 -- fixture-owned temporary sidecar, not a source file
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	ids := f.ids
	if got := service.ReviewOptions(ctx, target, observation.ID); len(got) != 0 {
		t.Fatal("read failure invented options", got)
	}
	if f.ids != ids {
		t.Fatal("description allocated an offer")
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	after, err := f.store.Read(ctx, target.UserID)
	if err != nil || after.Version != before.Version {
		t.Fatal("failed description mutated the sidecar", err)
	}
}

func TestFolderReviewOptions_ReadOnlyAvailabilityAndScope(t *testing.T) {
	ctx := context.Background()
	f, service, target := observationFixture(t)
	creator := &fakeFolderCreator{}
	f.service.deps.Creator = creator
	observation, err := service.Observe(ctx, target, "chip", "documents")
	if err != nil {
		t.Fatal(err)
	}
	before, err := f.store.Read(ctx, target.UserID)
	if err != nil {
		t.Fatal(err)
	}
	ids := f.ids
	// Every observed folder is listed, whether or not it can be set up now. The
	// one whose setup needs a plan no host can make is listed as unavailable,
	// never as a control that leads somewhere.
	state := func(options []FolderReviewOption, name string) (string, string) {
		t.Helper()
		for _, option := range options {
			candidate, ok := observedReviewCandidate(service.selections[observation.ID], option.CandidateID)
			if !ok || option.WorkspaceType == "" || option.Presentation == nil || option.Presentation.Version != 1 {
				t.Fatal("invented option", option)
			}
			if candidate.Name == name {
				return option.Presentation.State, option.Presentation.Availability
			}
		}
		t.Fatalf("%s was dropped from %+v", name, options)
		return "", ""
	}
	options := service.ReviewOptions(ctx, target, observation.ID)
	if len(options) != len(observation.Projects) {
		t.Fatalf("an observed folder was dropped: %d options for %d folders", len(options), len(observation.Projects))
	}
	if got, reason := state(options, "Album"); got != "unavailable" || reason != FolderAvailabilityPlanUnavailable {
		t.Fatalf("specialized setup without a plan: %s/%s", got, reason)
	}
	if got, reason := state(options, "Thesis"); got != "proposed" || reason != "" {
		t.Fatalf("plain folder: %s/%s", got, reason)
	}
	// Documents holds three projects, so its own option is the group. With no
	// host to set one up it is listed as unavailable too.
	if got, reason := state(options, "Documents"); got != "unavailable" || reason != FolderAvailabilityPlanUnavailable ||
		options[0].Presentation.Kind != "structure" || len(options[0].Presentation.Members) != 4 {
		t.Fatalf("collection root without a setup host: %s/%s %+v", got, reason, options[0].Presentation)
	}
	f.service.deps.Setup = &fakeFolderSetup{lines: oneCardLines()}
	withSetup := service.ReviewOptions(ctx, target, observation.ID)
	if got, reason := state(withSetup, "Documents"); got != "proposed" || reason != "" || withSetup[0].WorkspaceType != "Group with 4 workspaces" {
		t.Fatalf("collection root: %s/%s %s", got, reason, withSetup[0].WorkspaceType)
	}
	if got, reason := state(withSetup, "Album"); len(withSetup) != len(options) || got != "proposed" || reason != "" {
		t.Fatalf("available specialized setup not offered: %s/%s", got, reason)
	}
	// A host whose integration plans fail still plans the group itself.
	f.service.deps.Setup = &structureSetup{fakeFolderSetup: &fakeFolderSetup{planErr: reasonedPlanError(FolderAvailabilityIntegrationNotInstalled)}}
	unplanned := service.ReviewOptions(ctx, target, observation.ID)
	if got, reason := state(unplanned, "Documents"); got != "proposed" || reason != "" {
		t.Fatalf("the group waited on a plugin: %s/%s", got, reason)
	}
	if got, reason := state(unplanned, "Album"); len(unplanned) != len(options) || got != "unavailable" || reason != FolderAvailabilityIntegrationNotInstalled {
		t.Fatalf("unavailable plan: %s/%s", got, reason)
	}
	for _, option := range unplanned {
		if p := option.Presentation; p.Availability == FolderAvailabilityIntegrationNotInstalled &&
			(len(p.Notes) != 1 || p.Notes[0] != "REAPER integration is not installed; it can be added later.") {
			t.Fatalf("unavailable option says %q", p.Notes)
		}
	}
	after, err := f.store.Read(ctx, target.UserID)
	if err != nil || before.Version != after.Version || f.ids != ids || len(creator.requests) != 0 {
		t.Fatal("suggestions wrote offers, allocated IDs, or executed setup")
	}
	f.service.deps.Creator = nil
	uncreatable := service.ReviewOptions(ctx, target, observation.ID)
	if got, reason := state(uncreatable, "Thesis"); len(uncreatable) != len(options) || got != "unavailable" || reason != FolderAvailabilityPlanUnavailable {
		t.Fatalf("a host that cannot create: %s/%s", got, reason)
	}
	f.service.deps.Creator = creator
	foreign := target
	foreign.DraftID = "other"
	if got := service.ReviewOptions(ctx, foreign, observation.ID); len(got) != 0 {
		t.Fatal("foreign selection offered")
	}
	pending, err := f.service.ScanChip(ctx, target.UserID, "documents")
	if err != nil {
		t.Fatal(err)
	}
	if got := service.ReviewOptions(ctx, target, observation.ID); len(got) != 0 {
		t.Fatal("pending review ignored")
	}
	doc, err := f.store.Read(ctx, target.UserID)
	if err != nil || doc.Pending().ID != pending.ID {
		t.Fatal("pending review displaced")
	}
	f.now = f.now.Add(foldercontext.SelectionTTL + time.Second)
	if got := service.ReviewOptions(ctx, target, observation.ID); len(got) != 0 {
		t.Fatal("expired selection offered")
	}
}

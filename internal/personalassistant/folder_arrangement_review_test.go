package personalassistant

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
)

// arrangedFixture attaches a Desktop holding thirty song folders, more than
// the eight-name preview and far beyond the collection threshold, then binds
// the held selection to a saved conversation as a canonical turn would.
func arrangedFixture(t *testing.T) (*folderDigestFixture, *FolderObservationService, foldercontext.Target, string, map[string]string) {
	t.Helper()
	f, service, draft := observationFixture(t)
	for i := 0; i < 30; i++ {
		folder := filepath.Join(f.home, "Desktop", fmt.Sprintf("Song %02d", i))
		if err := os.Mkdir(folder, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(folder, "song.rpp"), []byte("PRIVATE CONTENT"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	f.service.deps.Setup = &structureSetup{fakeFolderSetup: &fakeFolderSetup{lines: oneCardLines()}}
	observation, err := service.Observe(context.Background(), draft, FolderObserveChip, "desktop")
	if err != nil {
		t.Fatal(err)
	}
	service.BindSaved(draft, observation.ID, "chat-1")
	target := draft
	target.DraftID, target.ConversationID = "", "chat-1"
	ids := map[string]string{}
	for index, candidate := range service.selections[observation.ID].result.Candidates {
		ids[candidate.Name] = fmt.Sprintf("candidate-%d", index)
	}
	return f, service, target, observation.ID, ids
}

func arrangedGroup(name string, ids ...string) FolderArrangementGroup {
	return FolderArrangementGroup{Binding: FolderStructureArrangement{RecordID: "arrangement-1", RecordRevision: 1, Operation: 0}, Name: name, CandidateIDs: ids}
}

func TestReviewArrangementGroup_ReviewsTheExactSelectionBeyondThePreviewAndThreshold(t *testing.T) {
	f, service, target, observationID, ids := arrangedFixture(t)
	before, err := f.store.Read(t.Context(), target.UserID)
	if err != nil {
		t.Fatal(err)
	}
	// Two folders: fewer than a collection needs, and both beyond the preview.
	view, created, err := service.ReviewArrangementGroup(t.Context(), target, observationID, arrangedGroup("Client work", ids["Song 29"], ids["Song 25"]))
	if err != nil || !created {
		t.Fatalf("review: %v created=%v", err, created)
	}
	s := view.Structure
	if s == nil || !s.Arranged() || s.Version != FolderStructureSelectedVersion || s.Group.Name != "Client work" || s.Group.Type != FolderGroupPlain ||
		s.MinMembers != 0 || s.Omitted != 0 || len(s.Members) != 2 || s.Members[0].Folder != "Song 25" || s.Members[1].Folder != "Song 29" {
		t.Fatalf("selected structure: %+v", s)
	}
	for _, member := range s.Members {
		if !member.Included || member.TypeLabel != "Plain workspace" || member.BlueprintID != "" || member.RelPath != "" || member.Identity != "" {
			t.Fatalf("member is not an exact, path-free plain workspace: %+v", member)
		}
	}
	if view.Plan == nil || view.Status != FolderOfferPending || view.ConversationID != "chat-1" {
		t.Fatalf("review view: %+v", view)
	}
	doc, err := f.store.Read(t.Context(), target.UserID)
	if err != nil || len(doc.Offers) != len(before.Offers)+1 {
		t.Fatalf("stored offers: %d, %v", len(doc.Offers), err)
	}
	stored := doc.Offer(view.ID)
	if stored == nil || stored.Structure.Validate() != nil || stored.Structure.Members[0].RelPath != "Song 25" || stored.Structure.Members[0].Identity == "" ||
		*stored.Structure.Arrangement != (FolderStructureArrangement{RecordID: "arrangement-1", RecordRevision: 1, Operation: 0}) {
		t.Fatalf("stored structure: %+v", stored)
	}
	// Reviewing the same intention again refreshes the same offer.
	again, created, err := service.ReviewArrangementGroup(t.Context(), target, observationID, arrangedGroup("Client work", ids["Song 25"], ids["Song 29"]))
	if err != nil || created || again.ID != view.ID {
		t.Fatalf("repeat review: %v created=%v id=%s", err, created, again.ID)
	}
	// Any other review, manual or arranged, is protected while this one is open.
	if _, _, err := service.ReviewArrangementGroup(t.Context(), target, observationID, FolderArrangementGroup{
		Binding: FolderStructureArrangement{RecordID: "arrangement-1", RecordRevision: 1, Operation: 1}, Name: "Experiments", CandidateIDs: []string{ids["Song 01"]},
	}); !errors.Is(err, ErrFolderReviewPending) {
		t.Fatalf("second arranged review: %v", err)
	}
	if _, err := service.Review(t.Context(), target, "chat-1", observationID, folderRootCandidateID); !errors.Is(err, ErrFolderReviewPending) {
		t.Fatalf("a manual review retired or adopted the arranged one: %v", err)
	}
	doc, _ = f.store.Read(t.Context(), target.UserID)
	if kept := doc.Offer(view.ID); kept == nil || kept.Status != FolderOfferPending || !kept.Structure.Arranged() || len(kept.Structure.Members) != 2 {
		t.Fatalf("arranged review changed: %+v", kept)
	}
	// Closing it (Keep chatting) lets a different review open.
	if err := service.CloseReview(t.Context(), target, view.ID); err != nil {
		t.Fatal(err)
	}
	if _, created, err := service.ReviewArrangementGroup(t.Context(), target, observationID, arrangedGroup("Client work", ids["Song 25"], ids["Song 29"])); err != nil || !created {
		t.Fatalf("review after close: %v created=%v", err, created)
	}
}

func TestReviewArrangementGroup_RefusesWhatOneReviewedGroupCannotHold(t *testing.T) {
	f, service, target, observationID, ids := arrangedFixture(t)
	taken := map[string]bool{"taken-name": true}
	f.service.deps.WorkspaceNameTaken = func(_ context.Context, name string) bool { return taken[workspaceSlugOf(name)] }
	owned := filepath.Join(f.home, "Desktop", "Song 03")
	f.service.deps.ExistingProjects = func(_ context.Context, _ string, path, _ string) ([]FolderExistingProject, error) {
		if path == owned {
			return []FolderExistingProject{{WorkspaceID: "ws-3", Name: "Song Three"}}, nil
		}
		return nil, nil
	}
	tooMany := make([]string, 0, FolderStructureMaxMembers+1)
	for i := 0; i <= FolderStructureMaxMembers; i++ {
		tooMany = append(tooMany, ids[fmt.Sprintf("Song %02d", i)])
	}
	cases := map[string]struct {
		group FolderArrangementGroup
		says  string
	}{
		"the root as a member":          {arrangedGroup("Group", folderRootCandidateID), "not an immediate subfolder"},
		"an invented candidate":         {arrangedGroup("Group", "candidate-999"), "not an immediate subfolder"},
		"a padded candidate id":         {arrangedGroup("Group", "candidate-01"), "not an immediate subfolder"},
		"the same folder twice":         {arrangedGroup("Group", ids["Song 01"], ids["Song 01"]), "not an immediate subfolder"},
		"a folder that is a workspace":  {arrangedGroup("Group", ids["Song 02"], ids["Song 03"]), "Song 03 is already the workspace Song Three"},
		"more than one creator can run": {arrangedGroup("Group", tooMany...), "between 1 and 24 folders"},
		"no folder at all":              {arrangedGroup("Group"), "between 1 and 24 folders"},
		"a group name that is a path":   {arrangedGroup("Clients/2026", ids["Song 01"]), "group name"},
		"a group name already taken":    {arrangedGroup("Taken Name", ids["Song 01"]), "already exists"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, _, err := service.ReviewArrangementGroup(t.Context(), target, observationID, tc.group)
			if !errors.Is(err, ErrFolderArrangementReview) || !strings.Contains(err.Error(), tc.says) {
				t.Fatalf("got %v", err)
			}
		})
	}
	if _, _, err := service.ReviewArrangementGroup(t.Context(), target, observationID, FolderArrangementGroup{Name: "Group", CandidateIDs: []string{ids["Song 01"]}}); !errors.Is(err, ErrFolderSelection) {
		t.Fatalf("an unbound group: %v", err)
	}
	// A member replaced by a link since the attach is refused by name.
	moved := filepath.Join(f.home, "Desktop", "Song 07")
	if err := os.Rename(moved, moved+" old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(moved+" old", moved); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.ReviewArrangementGroup(t.Context(), target, observationID, arrangedGroup("Group", ids["Song 07"])); !errors.Is(err, ErrFolderArrangementReview) ||
		!strings.Contains(err.Error(), "Song 07 is no longer the folder") {
		t.Fatalf("a swapped member: %v", err)
	}
	doc, err := f.store.Read(t.Context(), target.UserID)
	if err != nil || doc.Pending() != nil {
		t.Fatalf("a refused review left a pending offer: %+v, %v", doc.Pending(), err)
	}
	// Another conversation cannot review this conversation's held folder.
	other := target
	other.ConversationID = "chat-2"
	if _, _, err := service.ReviewArrangementGroup(t.Context(), other, observationID, arrangedGroup("Group", ids["Song 01"])); err == nil {
		t.Fatal("a foreign conversation reviewed the held folder")
	}
}

func TestReviewArrangementGroup_OutcomeIsReadFromTheStoredOfferOnly(t *testing.T) {
	f, service, target, observationID, ids := arrangedFixture(t)
	view, _, err := service.ReviewArrangementGroup(t.Context(), target, observationID, arrangedGroup("Client work", ids["Song 01"], ids["Song 02"]))
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := service.ArrangementOutcome(t.Context(), target, view.ID)
	if err != nil || outcome.Status != FolderOfferPending || outcome.Binding.RecordID != "arrangement-1" || outcome.WorkspaceID != "" {
		t.Fatalf("pending outcome: %+v, %v", outcome, err)
	}
	// A resolved group reports its group and how many workspaces it read back.
	if _, err := f.store.Mutate(t.Context(), target.UserID, func(doc *FolderDigestDocument) error {
		offer := doc.Offer(view.ID)
		offer.Status = FolderOfferResolved
		offer.Outcome = &FolderOutcome{Kind: FolderChoiceGroup, WorkspaceID: "group-1", Route: "/workspaces/client-work", Receipt: []FolderReceiptRow{
			{Kind: "workspace", Name: "Client work", Detail: "group", Route: "/workspaces/client-work"},
			{Kind: "workspace", Name: "Song 01", Detail: "linked to Song 01", Route: "/workspaces/song-01"},
			{Kind: "workspace", Name: "Song 02", Detail: "linked to Song 02", Route: "/workspaces/song-02"},
		}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// The folder is not looked at to read an outcome.
	f.service.deps.ValidateRoot = func(string) (string, error) { t.Fatal("outcome probed the folder"); return "", nil }
	outcome, err = service.ArrangementOutcome(t.Context(), target, view.ID)
	if err != nil || outcome.WorkspaceID != "group-1" || outcome.Workspaces != 2 || len(outcome.ReceiptDigest) != 64 {
		t.Fatalf("resolved outcome: %+v, %v", outcome, err)
	}
	other := target
	other.ConversationID = "chat-2"
	if _, err := service.ArrangementOutcome(t.Context(), other, view.ID); !errors.Is(err, ErrFolderOfferNotFound) {
		t.Fatalf("a foreign conversation read the outcome: %v", err)
	}
	if status, err := service.ConversationOfferStatus(t.Context(), target, view.ID); err != nil || status != FolderOfferResolved {
		t.Fatalf("status: %q, %v", status, err)
	}
	if _, err := service.ConversationOfferStatus(t.Context(), other, view.ID); !errors.Is(err, ErrFolderOfferNotFound) {
		t.Fatalf("a foreign conversation read the status: %v", err)
	}
}

// An arranged review is closed with Keep chatting or the arrangement's own
// Decline. It is never put off, and never becomes a folder decline.
func TestReviewArrangementGroup_IsNeverPutOffOrDeclinedAsAFolder(t *testing.T) {
	f, service, target, observationID, ids := arrangedFixture(t)
	// The host guard authorizes this review; the refusal is the service's own.
	service.ConfigureConversationReviews(func(context.Context, FolderOffer) error { return nil }, func(foldercontext.Target) (func(), bool) { return func() {}, true })
	view, _, err := service.ReviewArrangementGroup(t.Context(), target, observationID, arrangedGroup("Client work", ids["Song 01"], ids["Song 02"]))
	if err != nil {
		t.Fatal(err)
	}
	for _, decision := range []string{FolderDecisionLater, FolderDecisionNo} {
		if _, err := f.service.Decide(t.Context(), target.UserID, view.ID, FolderDecisionInput{Decision: decision, RequestID: "decide-" + decision}); !errors.Is(err, ErrValidation) {
			t.Fatalf("%s: %v", decision, err)
		}
	}
	doc, err := f.store.Read(t.Context(), target.UserID)
	if err != nil || doc.Offer(view.ID).Status != FolderOfferPending || len(doc.Decisions) != 0 || len(doc.Tombstones) != 0 || len(doc.DomainDeclines) != 0 {
		t.Fatalf("a refused decision changed something: %+v %v", doc.Offer(view.ID), err)
	}
}

func TestFolderStructure_SelectedVersionHasItsOwnRules(t *testing.T) {
	valid := FolderStructure{
		Version: FolderStructureSelectedVersion, Folder: "Desktop",
		Group:       FolderStructureGroup{Name: "Client work", Type: FolderGroupPlain, DestinationState: "new"},
		Members:     []FolderStructureMember{{CandidateID: "candidate-4", Name: "Song 04", Folder: "Song 04", TypeLabel: folderPlainWorkspaceLabel, Files: 0, Included: true, RelPath: "Song 04"}},
		Arrangement: &FolderStructureArrangement{RecordID: "arrangement-1", RecordRevision: 2, Operation: 3},
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("a one-member selection with an empty folder is valid: %v", err)
	}
	mutations := map[string]func(*FolderStructure){
		"no arrangement binding":  func(s *FolderStructure) { s.Arrangement = nil },
		"a recognition threshold": func(s *FolderStructure) { s.MinMembers = 2 },
		"an omitted tail":         func(s *FolderStructure) { s.Omitted = 1 },
		"a Home group": func(s *FolderStructure) {
			s.Group.Type, s.Group.HomeName, s.Group.HomeAvailable = FolderGroupHome, "Home", true
		},
		"an excluded member":         func(s *FolderStructure) { s.Members[0].Included = false },
		"the root as a member":       func(s *FolderStructure) { s.Members[0].CandidateID = folderRootCandidateID },
		"a member with a blueprint":  func(s *FolderStructure) { s.Members[0].BlueprintID = "reaper-song" },
		"a member already owned":     func(s *FolderStructure) { s.Members[0].ExistingWorkspace = "Song" },
		"a member with no folder":    func(s *FolderStructure) { s.Members[0].RelPath = "" },
		"a nested member folder":     func(s *FolderStructure) { s.Members[0].RelPath = "Albums/Song 04" },
		"a recognized type label":    func(s *FolderStructure) { s.Members[0].TypeLabel = "REAPER session" },
		"a binding past operations":  func(s *FolderStructure) { s.Arrangement.Operation = 32 },
		"a binding with no revision": func(s *FolderStructure) { s.Arrangement.RecordRevision = 0 },
		"no members":                 func(s *FolderStructure) { s.Members = nil },
	}
	for name, mutate := range mutations {
		copied := *valid.clone()
		mutate(&copied)
		if copied.Validate() == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
	// A version-1 proposal never carries a binding, and keeps its own rules.
	legacy := *valid.clone()
	legacy.Version, legacy.MinMembers = FolderStructureVersion, 2
	if legacy.Validate() == nil {
		t.Fatal("a version-1 structure accepted an arrangement selection")
	}
}

// An open arranged review authorizes Set up only while its held folder is
// live in this process: an expired selection or a restarted service refuses
// before anything runs, and the offer stays an unconfirmed review.
func TestReviewArrangementGroup_SetUpNeedsTheLiveHeldFolder(t *testing.T) {
	f, service, target, observationID, ids := arrangedFixture(t)
	permissive := func(context.Context, FolderOffer) error { return nil }
	lease := func(foldercontext.Target) (func(), bool) { return func() {}, true }
	service.ConfigureConversationReviews(permissive, lease)
	setup := f.service.deps.Setup.(*structureSetup)
	view, _, err := service.ReviewArrangementGroup(t.Context(), target, observationID, arrangedGroup("Client work", ids["Song 01"], ids["Song 02"]))
	if err != nil || view.Plan == nil {
		t.Fatalf("review: %+v %v", view.Plan, err)
	}
	pending := func() {
		t.Helper()
		doc, err := f.store.Read(t.Context(), target.UserID)
		if err != nil || doc.Offer(view.ID).Status != FolderOfferPending || doc.Offer(view.ID).Setup != nil || setup.runCount() != 0 {
			t.Fatalf("a refused Set up changed the review or ran: %+v %v", doc.Offer(view.ID), err)
		}
	}
	input := FolderSetupInput{RequestID: "arranged-set-up", PlanDigest: view.Plan.Digest}
	// Expired: the held selection is gone even though the review is stored.
	start := f.now
	f.now = start.Add(foldercontext.SelectionTTL)
	if _, err := f.service.StartSetup(t.Context(), target.UserID, view.ID, input); err == nil {
		t.Fatal("an expired held folder authorized Set up")
	}
	pending()
	// Restarted: a new observation service holds no selection.
	f.now = start
	restarted := NewFolderObservationService(f.service)
	restarted.ConfigureConversationReviews(permissive, lease)
	if _, err := f.service.StartSetup(t.Context(), target.UserID, view.ID, input); err == nil {
		t.Fatal("a restarted process confirmed a review it no longer holds")
	}
	pending()
	// The same review in the live process still confirms exactly once.
	service.ConfigureConversationReviews(permissive, lease)
	if _, err := f.service.StartSetup(t.Context(), target.UserID, view.ID, input); err != nil {
		t.Fatalf("live Set up: %v", err)
	}
	if _, err := f.service.StartSetup(t.Context(), target.UserID, view.ID, input); err != nil {
		t.Fatalf("replayed request: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for setup.runCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if setup.runCount() != 1 {
		t.Fatalf("runs: %d", setup.runCount())
	}
}

package personalassistant

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/secondbrain"
)

func oneFact(text string) secondbrain.HeldFact {
	return secondbrain.HeldFact{Text: text, How: "You told Ori this", Date: "21 Oct 2026"}
}

func fillInHoldings() secondbrain.Holdings {
	return secondbrain.Holdings{
		Facts:  []secondbrain.HeldFact{oneFact("You mix on Fridays.")},
		People: []secondbrain.HeldFact{oneFact("Dana Lee is my distributor.")},
	}
}

func TestSecondBrainSync_FillsInTheNotesAndTheLedgerAndASecondRunWritesNothing(t *testing.T) {
	ctx := context.Background()
	h, brain := createdHarness(t)
	h.service.Wait()
	h.service.SetHoldings(func(context.Context, string) (secondbrain.Holdings, error) { return fillInHoldings(), nil })

	result, err := h.service.Sync(ctx, "local", "test")
	if err != nil || result.Written[secondbrain.NoteKindFacts] != 1 || result.Written[secondbrain.NoteKindPerson] != 1 {
		t.Fatalf("Sync = %+v, %v", result, err)
	}
	content := readTestFile(t, brain, "People", "Dana Lee.md")
	if mark := secondbrain.ReadMark([]byte(content)); !mark.OwnedByOri || mark.Kind != secondbrain.NoteKindPerson {
		t.Fatalf("person note = %q", content)
	}
	doc, err := h.service.read(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	fillIn := doc.Connected().FillIn
	if fillIn == nil || len(fillIn.Mirrors) != 2 || fillIn.LastAt == nil || fillIn.MorePending {
		t.Fatalf("ledger = %+v", fillIn)
	}

	tree := snapshotTree(t, brain)
	again, err := h.service.Sync(ctx, "local", "test")
	if err != nil || len(again.Written) != 0 {
		t.Fatalf("second Sync = %+v, %v", again, err)
	}
	if after := snapshotTree(t, brain); after != tree {
		t.Fatalf("a second sync wrote.\nbefore:\n%s\nafter:\n%s", tree, after)
	}
	unchanged, err := h.service.read(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Version != doc.Version {
		t.Fatalf("a sync that did nothing saved the record: %d → %d", doc.Version, unchanged.Version)
	}
}

// Personal HQ is a workspace like any other to the store that lists them, so the
// sync leaves out the one the binding names (FR4).
func TestSecondBrainSync_NeverMirrorsPersonalHQAsAProject(t *testing.T) {
	ctx := context.Background()
	h, brain := createdHarness(t)
	h.service.Wait()
	binding, err := h.service.store.Binding(ctx, "local")
	if err != nil || binding.HQWorkspaceID == "" {
		t.Fatalf("binding = %+v, %v", binding, err)
	}
	h.service.SetHoldings(func(context.Context, string) (secondbrain.Holdings, error) {
		return secondbrain.Holdings{Projects: []secondbrain.HeldProject{
			{ID: binding.HQWorkspaceID, Name: "My HQ"}, {ID: "ws-launch", Name: "Launch"},
		}}, nil
	})
	result, err := h.service.Sync(ctx, "local", "test")
	if err != nil || result.Written[secondbrain.NoteKindProject] != 1 {
		t.Fatalf("Sync = %+v, %v", result, err)
	}
	if _, err := os.Lstat(filepath.Join(brain, "Projects", "Launch", "README.md")); err != nil {
		t.Fatalf("the project was not written: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(brain, "Projects", "My HQ")); !os.IsNotExist(err) {
		t.Fatalf("Personal HQ was mirrored as a project: %v", err)
	}
}

// A connected vault is never written, whatever HQ holds (FR12, US8).
func TestSecondBrainSync_NeverWritesAFolderTheUserConnected(t *testing.T) {
	ctx := context.Background()
	h := newSecondBrainHarness(t)
	h.connectVault(t)
	h.service.Wait()
	h.service.SetHoldings(func(context.Context, string) (secondbrain.Holdings, error) { return fillInHoldings(), nil })
	path := h.music.Path
	before := snapshotTree(t, path)
	result, err := h.service.Sync(ctx, "local", "test")
	if err != nil || len(result.Written) != 0 {
		t.Fatalf("Sync = %+v, %v", result, err)
	}
	if after := snapshotTree(t, path); after != before {
		t.Fatalf("a connected vault changed.\nbefore:\n%s\nafter:\n%s", before, after)
	}
	doc, err := h.service.read(ctx, "local")
	if err != nil || doc.Connected().FillIn != nil {
		t.Fatalf("a connected vault's ledger = %+v, %v", doc.Connected().FillIn, err)
	}
}

// Holdings that cannot be read are unknown, not empty: nothing goes stale, and
// no note loses a line, because a store was briefly unavailable.
func TestSecondBrainSync_UnreadableHoldingsChangeNothing(t *testing.T) {
	ctx := context.Background()
	h, brain := createdHarness(t)
	h.service.Wait()
	h.service.SetHoldings(func(context.Context, string) (secondbrain.Holdings, error) { return fillInHoldings(), nil })
	if _, err := h.service.Sync(ctx, "local", "test"); err != nil {
		t.Fatal(err)
	}
	before := snapshotTree(t, brain)
	doc, err := h.service.read(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("store unavailable")
	h.service.SetHoldings(func(context.Context, string) (secondbrain.Holdings, error) { return secondbrain.Holdings{}, failure })
	if _, err := h.service.Sync(ctx, "local", "test"); !errors.Is(err, failure) {
		t.Fatalf("err = %v", err)
	}
	if after := snapshotTree(t, brain); after != before {
		t.Fatalf("the notes changed.\nbefore:\n%s\nafter:\n%s", before, after)
	}
	after, err := h.service.read(ctx, "local")
	if err != nil || after.Version != doc.Version {
		t.Fatalf("the record changed: %d → %d, %v", doc.Version, after.Version, err)
	}
}

func TestSecondBrainSync_AFolderThatIsGoneIsNotRecreated(t *testing.T) {
	ctx := context.Background()
	h, brain := createdHarness(t)
	h.service.Wait()
	h.service.SetHoldings(func(context.Context, string) (secondbrain.Holdings, error) { return fillInHoldings(), nil })
	if err := os.RemoveAll(brain); err != nil {
		t.Fatal(err)
	}
	if result, err := h.service.Sync(ctx, "local", "test"); err != nil || len(result.Written) != 0 {
		t.Fatalf("Sync = %+v, %v", result, err)
	}
	if _, err := os.Lstat(brain); !os.IsNotExist(err) {
		t.Fatalf("the folder came back: %v", err)
	}
}

// The approval has already succeeded, so a sync that fails is only logged, and
// triggers that land during a run share one more run.
func TestSecondBrainSyncSoon_NeverBlocksAndCoalesces(t *testing.T) {
	h, _ := createdHarness(t)
	h.service.Wait()
	var runs atomic.Int32
	entered, release := make(chan struct{}, 8), make(chan struct{})
	h.service.SetHoldings(func(context.Context, string) (secondbrain.Holdings, error) {
		runs.Add(1)
		entered <- struct{}{}
		<-release
		return secondbrain.Holdings{}, errors.New("store unavailable")
	})
	h.service.SyncSoon("local", "first")
	<-entered
	for range 5 {
		h.service.SyncSoon("local", "again")
	}
	close(release)
	h.service.Wait()
	if got := runs.Load(); got != 2 {
		t.Fatalf("runs = %d, want the first and one more", got)
	}
}

// An explicit save goes through ApproveCandidate, so it reaches the observers; a
// refused one does not. The interview's save is SaveExplicit too.
func TestKnowledgeOnChanged_AnApprovalReachesItsObserversAndARefusalDoesNot(t *testing.T) {
	ctx := context.Background()
	fixture := newKnowledgeFixture(t)
	learning := NewKnowledgeLifecycleService(NewKnowledgeStore(fixture.resolver(), fixture.folder), fixture.memory, nil)
	var changes atomic.Int32
	learning.OnChanged(func(userID string) {
		if userID != "local" {
			t.Errorf("userID = %q", userID)
		}
		changes.Add(1)
	})
	version, err := learning.CurrentStateVersion(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := learning.SaveExplicit(ctx, "local", version, "remember-1", "people", "Dana Lee is my distributor."); err != nil {
		t.Fatal(err)
	}
	if changes.Load() == 0 {
		t.Fatal("the approval did not reach its observers")
	}
	before := changes.Load()
	if _, err := learning.SaveExplicit(ctx, "local", version, "remember-2", "people", "not valid\nacross lines"); err == nil {
		t.Fatal("a refused save succeeded")
	}
	if changes.Load() != before {
		t.Fatal("a refused save reached the observers")
	}
}

// The loop (FR4): accepting an open item makes a follow-up from the notes. A sync
// writes nothing for it, the next look still reads the same one line as the same
// item, and accepting again adds nothing.
func TestSecondBrainSync_AcceptSyncLookDoesNotLoop(t *testing.T) {
	ctx := context.Background()
	h := newSecondBrainHarness(t)
	h.connectVault(t)
	lookID, items := triageItems(t, h)
	if len(items) == 0 {
		t.Fatal("the fixture look found nothing to accept")
	}
	first, err := h.service.AcceptFollowUps(ctx, "local", lookID, SecondBrainAcceptInput{ItemIDs: []string{items[0].ID}, RequestID: "accept-1"})
	if err != nil || first.Created != 1 {
		t.Fatalf("accept = %+v, %v", first, err)
	}
	// What the server gathers: a follow-up from the notes says so.
	held := secondbrain.Holdings{FollowUps: []secondbrain.HeldFollowUp{{ID: "f-1", Title: items[0].Text, FromNotes: true}}}
	for _, note := range secondbrain.PlanNotes(held) {
		if note.Ref.Kind == secondbrain.NoteKindFollowUp {
			t.Fatalf("a follow-up from the notes was planned: %+v", note)
		}
	}
	again, err := h.service.AcceptFollowUps(ctx, "local", lookID, SecondBrainAcceptInput{ItemIDs: []string{items[0].ID}, RequestID: "accept-2"})
	if err != nil || again.Created != 0 {
		t.Fatalf("accepting again = %+v, %v", again, err)
	}
}

// A follow_up note reads back as seeded, as a seed does: never suggested, and
// never added again.
func TestSecondBrainSync_AFollowUpNoteIsSeededInTheNextLook(t *testing.T) {
	ctx := context.Background()
	h, _ := createdHarness(t)
	h.service.Wait()
	h.service.SetHoldings(func(context.Context, string) (secondbrain.Holdings, error) {
		return secondbrain.Holdings{FollowUps: []secondbrain.HeldFollowUp{{ID: "f-1", Title: "Call the pressing plant"}}}, nil
	})
	if result, err := h.service.Sync(ctx, "local", "test"); err != nil || result.Written[secondbrain.NoteKindFollowUp] != 1 {
		t.Fatalf("Sync = %+v, %v", result, err)
	}
	*h.clock = h.clock.Add(SecondBrainLookAgainAfter + time.Minute)
	if _, err := h.service.LookAgain(ctx, "local", "look-again-1"); err != nil {
		t.Fatal(err)
	}
	h.service.Wait()
	look, err := h.service.Look(ctx, "local")
	if err != nil || look.Result == nil {
		t.Fatalf("look = %+v, %v", look, err)
	}
	found := false
	for _, item := range look.Result.Items {
		if strings.Contains(item.Text, "Call the pressing plant") {
			found = true
			if !item.Seeded || item.Suggested {
				t.Fatalf("item = %+v", item)
			}
		}
	}
	if !found {
		t.Fatalf("the note's item is not in the look: %+v", look.Result.Items)
	}
}

// What the station and the Today row read (FR8, FR9).
func TestSecondBrainStatus_CountsWhatIsYoursAndTheTodayRowIsShownOnce(t *testing.T) {
	ctx := context.Background()
	h, brain := createdHarness(t)
	h.service.Wait()
	holdings := fillInHoldings()
	holdings.Projects = []secondbrain.HeldProject{{ID: "ws-1", Name: "Launch"}}
	h.service.SetHoldings(func(context.Context, string) (secondbrain.Holdings, error) { return holdings, nil })
	if _, err := h.service.Sync(ctx, "local", "test"); err != nil {
		t.Fatal(err)
	}
	report, found := h.service.WrittenReport(ctx, "local")
	if !found || report.Sentence != "Ori added a person and a project to your notes and updated what it knows about you" {
		t.Fatalf("report = %+v, %v", report, found)
	}
	// It stays through the day it was shown, then leaves without being asked.
	if _, still := h.service.WrittenReport(ctx, "local"); !still {
		t.Fatal("the row left at once")
	}
	*h.clock = h.clock.Add(secondBrainReportLasts + time.Minute)
	if _, still := h.service.WrittenReport(ctx, "local"); still {
		t.Fatal("the row nags")
	}

	full := filepath.Join(brain, "People", "Dana Lee.md")
	edited := readTestFile(t, full) + "Mine now.\n"
	if err := os.WriteFile(full, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := h.service.Sync(ctx, "local", "test"); err != nil {
		t.Fatal(err)
	}
	if _, found := h.service.WrittenReport(ctx, "local"); found {
		t.Fatal("a sync that wrote nothing raised a row")
	}
	status, err := h.service.Status(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	if status.FillIn == nil || status.FillIn.UserOwned != 1 || status.FillIn.Written != 2 || status.FillIn.LastAt == nil {
		t.Fatalf("fill_in = %+v", status.FillIn)
	}
	if by := status.WrittenByOri.ByKind; by[secondbrain.NoteKindPerson] != 1 || by[secondbrain.NoteKindProject] != 1 || by[secondbrain.NoteKindFacts] != 1 {
		t.Fatalf("by_kind = %v", by)
	}
	for _, project := range status.Projects {
		if project.Name == "Launch" {
			t.Fatalf("a project that is a workspace is offered as one: %+v", status.Projects)
		}
	}
}

func TestPlanNotes_DateAndHowReachThePersonNote(t *testing.T) {
	notes := secondbrain.PlanNotes(fillInHoldings())
	var person string
	for _, note := range notes {
		if filepath.Dir(note.Rel) == "People" {
			person = string(note.Body)
		}
	}
	if !strings.Contains(person, "You told Ori this, 21 Oct 2026.") {
		t.Fatalf("person note = %q", person)
	}
}

package personalassistant

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

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

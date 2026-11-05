package projectlibrary

import (
	"errors"
	"testing"
)

func bulkFixture(t *testing.T) (*Roots, Scope, []Entry) {
	t.Helper()
	r, scope, _, _, root := connectedMusicRoot(t)
	reviewedRootScan(t, r, scope, root, "bulk-base")
	doc, err := r.library.Read(scope)
	if err != nil || len(doc.Entries) != 4 {
		t.Fatalf("fixture: %+v %v", doc.Entries, err)
	}
	return r, scope, doc.Entries
}

func bulkEntries(entries []Entry) []BulkFieldsEntry {
	requested := make([]BulkFieldsEntry, 0, len(entries))
	for _, entry := range entries {
		requested = append(requested, BulkFieldsEntry{EntryID: entry.ID, IfFieldsRevision: entry.Fields.Revision})
	}
	return requested
}

func TestBulkFields_UpdatesEachSongOnItsOwnAndSkipsChangedOrMissingOnes(t *testing.T) {
	r, scope, entries := bulkFixture(t)
	// One song is edited elsewhere after the shelf loaded.
	setHumanFields(t, r, scope, entries[0].ID, Fields{NextAction: "Edited in another tab"})
	requested := append(bulkEntries(entries), BulkFieldsEntry{EntryID: "forgotten-song", IfFieldsRevision: 0})
	result, err := r.library.CommitBulkFields(scope, requested, FieldsPatch{Status: text("active")}, "bulk-1", "owner")
	if err != nil || result.Replay {
		t.Fatalf("bulk: %+v %v", result, err)
	}
	if len(result.Updated) != 3 || len(result.Skipped) != 2 ||
		result.Skipped[0] != (BulkFieldsSkipped{EntryID: entries[0].ID, Reason: BulkSkipRevisionChanged}) ||
		result.Skipped[1] != (BulkFieldsSkipped{EntryID: "forgotten-song", Reason: BulkSkipNotFound}) {
		t.Fatalf("outcomes: %+v", result)
	}
	doc, err := r.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	for _, updated := range result.Updated {
		entry, _ := entryByID(doc, updated.EntryID)
		// Same author, source and time a single edit records (FR20).
		if entry.Fields.Status != "active" || entry.Fields.Revision != 1 || entry.Fields.Author != "owner" ||
			entry.Fields.Source != "reviewed_user" || entry.Fields.UpdatedAt.IsZero() ||
			updated.Fields.Revision != entry.Fields.Revision || !updated.Fields.UpdatedAt.Equal(entry.Fields.UpdatedAt) {
			t.Fatalf("updated song: %+v vs %+v", entry.Fields, updated.Fields)
		}
	}
	if skipped, _ := entryByID(doc, entries[0].ID); skipped.Fields.Status != "" ||
		skipped.Fields.NextAction != "Edited in another tab" {
		t.Fatalf("a song edited elsewhere was overwritten: %+v", skipped.Fields)
	}
	// A retry with the same key returns the first outcome and writes nothing.
	again, err := r.library.CommitBulkFields(scope, requested, FieldsPatch{Status: text("active")}, "bulk-1", "owner")
	if err != nil || !again.Replay || len(again.Updated) != 3 || len(again.Skipped) != 2 ||
		again.Skipped[0].Reason != BulkSkipRevisionChanged {
		t.Fatalf("replay: %+v %v", again, err)
	}
	if after, _ := r.library.Read(scope); after.Revision != doc.Revision {
		t.Fatalf("replay wrote again: %d -> %d", doc.Revision, after.Revision)
	}
	// The same key with a different request is a conflict, never a second edit.
	if _, err := r.library.CommitBulkFields(scope, requested, FieldsPatch{Status: text("on_hold")}, "bulk-1", "owner"); !errors.Is(err, ErrConflict) {
		t.Fatalf("reused key: %v", err)
	}
}

func TestBulkFields_PriorityAndClearAndUndoWithFreshRevisions(t *testing.T) {
	r, scope, entries := bulkFixture(t)
	two := 2
	first, err := r.library.CommitBulkFields(scope, bulkEntries(entries[:2]), FieldsPatch{Priority: &two, Stage: text("mixing")}, "bulk-p", "owner")
	if err != nil || len(first.Updated) != 2 {
		t.Fatalf("priority: %+v %v", first, err)
	}
	// Undo sends the previous values with the revisions the first call returned.
	undo := []BulkFieldsEntry{}
	for _, updated := range first.Updated {
		if updated.Fields.Priority == nil || *updated.Fields.Priority != 2 || updated.Fields.Stage != "mixing" {
			t.Fatalf("updated fields: %+v", updated.Fields)
		}
		undo = append(undo, BulkFieldsEntry{EntryID: updated.EntryID, IfFieldsRevision: updated.Fields.Revision})
	}
	reverted, err := r.library.CommitBulkFields(scope, undo, FieldsPatch{ClearPriority: true, Stage: text("")}, "bulk-undo", "owner")
	if err != nil || len(reverted.Updated) != 2 || len(reverted.Skipped) != 0 {
		t.Fatalf("undo: %+v %v", reverted, err)
	}
	for _, updated := range reverted.Updated {
		if updated.Fields.Priority != nil || updated.Fields.Stage != "" || updated.Fields.Revision != 2 {
			t.Fatalf("undo fields: %+v", updated.Fields)
		}
	}
}

func TestBulkFields_RefusesOtherFieldsBadValuesAndBadSelections(t *testing.T) {
	r, scope, entries := bulkFixture(t)
	before, err := r.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	nine, one := 9, 1
	many := make([]BulkFieldsEntry, MaxBulkEntries+1)
	for i := range many {
		many[i] = BulkFieldsEntry{EntryID: newID()}
	}
	for name, call := range map[string]func() error{
		"next action": func() error {
			_, err := r.library.CommitBulkFields(scope, bulkEntries(entries), FieldsPatch{NextAction: text("x")}, "k1", "owner")
			return err
		},
		"empty patch": func() error {
			_, err := r.library.CommitBulkFields(scope, bulkEntries(entries), FieldsPatch{}, "k2", "owner")
			return err
		},
		"priority and clear": func() error {
			_, err := r.library.CommitBulkFields(scope, bulkEntries(entries), FieldsPatch{Priority: &one, ClearPriority: true}, "k3", "owner")
			return err
		},
		"priority out of range": func() error {
			_, err := r.library.CommitBulkFields(scope, bulkEntries(entries), FieldsPatch{Priority: &nine}, "k4", "owner")
			return err
		},
		"unknown stage": func() error {
			_, err := r.library.CommitBulkFields(scope, bulkEntries(entries), FieldsPatch{Stage: text("done")}, "k5", "owner")
			return err
		},
		"no songs": func() error {
			_, err := r.library.CommitBulkFields(scope, nil, FieldsPatch{Status: text("active")}, "k6", "owner")
			return err
		},
		"too many songs": func() error {
			_, err := r.library.CommitBulkFields(scope, many, FieldsPatch{Status: text("active")}, "k7", "owner")
			return err
		},
		"duplicate song": func() error {
			twice := append(bulkEntries(entries[:1]), bulkEntries(entries[:1])...)
			_, err := r.library.CommitBulkFields(scope, twice, FieldsPatch{Status: text("active")}, "k8", "owner")
			return err
		},
		"no key": func() error {
			_, err := r.library.CommitBulkFields(scope, bulkEntries(entries), FieldsPatch{Status: text("active")}, "", "owner")
			return err
		},
	} {
		if err := call(); !errors.Is(err, ErrConflict) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if after, _ := r.library.Read(scope); after.Revision != before.Revision {
		t.Fatalf("a refused bulk edit wrote: %d -> %d", before.Revision, after.Revision)
	}
}

func TestBulkFields_ReceiptsStayBoundedAndAnAgedOutReplayIsAConflict(t *testing.T) {
	r, scope, entries := bulkFixture(t)
	requested := bulkEntries(entries[:1])
	for i := 0; i < maxBulkFieldReceipts+1; i++ {
		result, err := r.library.CommitBulkFields(scope, requested, FieldsPatch{Status: text("active")}, "bulk-n-"+string(rune('a'+i)), "owner")
		if err != nil || len(result.Updated) != 1 {
			t.Fatalf("bulk %d: %+v %v", i, result, err)
		}
		requested[0].IfFieldsRevision = result.Updated[0].Fields.Revision
	}
	doc, err := r.library.Read(scope)
	if err != nil || len(doc.BulkFieldReceipts) != maxBulkFieldReceipts || doc.BulkFieldReceipts[0].Key != "bulk-n-b" {
		t.Fatalf("receipts: %d %v", len(doc.BulkFieldReceipts), err)
	}
	// The first key's operation still exists, but its outcome aged out: a
	// retry is refused rather than answered with a guess.
	first := []BulkFieldsEntry{{EntryID: entries[0].ID, IfFieldsRevision: 0}}
	if _, err := r.library.CommitBulkFields(scope, first, FieldsPatch{Status: text("active")}, "bulk-n-a", "owner"); !errors.Is(err, ErrConflict) {
		t.Fatalf("aged-out replay: %v", err)
	}
}

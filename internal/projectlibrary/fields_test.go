package projectlibrary

import (
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func text(value string) *string { return &value }

func TestFields_SparseReviewedPatchKeepsUnmentionedMetadataAcrossRestart(t *testing.T) {
	r, scope, file, _, root := connectedMusicRoot(t)
	entryID := newID()
	baseScan := reviewedRootScan(t, r, scope, root, "base-for-fields")
	if baseScan.Status != "complete" {
		t.Fatal(baseScan)
	}
	doc, err := r.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = r.library.mutate(scope, doc.Revision,
		operation{key: "seed-fields", action: "seed", digest: "fixture"},
		func(current *Document) (string, error) {
			current.Entries = append(current.Entries, Entry{ID: entryID, Revision: 1,
				Observations: []Observation{{RootID: root.ID, RelativeFolder: "Unlinked",
					FileIdentity: "fixture:1", Format: "reaper", Availability: "not_scanned",
					ScanID: baseScan.ID, ScannedAt: time.Now().UTC()}},
				Fields: Fields{Status: "on_hold", Milestones: []Milestone{{ID: "master", Label: "Master", Complete: true}},
					Blockers: []string{"Get approval"}, Revision: 1}})
			return entryID, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	priority := 0
	patch := FieldsPatch{NextAction: text("Schedule a studio session"), Priority: &priority}
	review, err := r.library.ReviewFields(scope, entryID, 1, patch, "local")
	if err != nil || review.Before.Status != "on_hold" || review.After.NextAction != "Schedule a studio session" {
		t.Fatalf("sparse review: %+v %v", review, err)
	}
	prior, err := r.library.Read(scope)
	if err != nil || prior.Entries[len(prior.Entries)-1].Fields.NextAction != "" {
		t.Fatalf("review mutated fields: %+v %v", prior, err)
	}
	changed, replay, err := r.library.CommitFields(scope, entryID, review.Token, "fields-once", 1, patch, "local")
	if err != nil || replay || changed.Fields.Revision != 2 || changed.Fields.Status != "on_hold" ||
		changed.Fields.Priority == nil || *changed.Fields.Priority != 0 ||
		changed.Fields.Milestones[0].ID != "master" || changed.Fields.Source != "reviewed_user" {
		t.Fatalf("commit erased previous metadata: %+v %v %v", changed, replay, err)
	}
	if same, replay, err := r.library.CommitFields(scope, entryID, review.Token, "fields-once", 1, patch, "local"); err != nil || !replay || same.ID != entryID {
		t.Fatalf("one-key retry: %+v %v %v", same, replay, err)
	}
	if _, _, err := r.library.CommitFields(scope, entryID, review.Token, "fields-once", 1,
		FieldsPatch{NextAction: text("Changed request")}, "local"); !errors.Is(err, ErrConflict) {
		t.Fatalf("altered payload reused receipt: %v", err)
	}
	if _, _, err := r.library.CommitFields(scope, entryID, review.Token, "different-key", 1,
		patch, "local"); !errors.Is(err, ErrConflict) {
		t.Fatalf("consumed review accepted a second key: %v", err)
	}
	newFile, err := workspace.NewFileStore(filepath.Dir(filepath.Dir(file.GetFilesPath(scope.HomeID))))
	if err != nil {
		t.Fatal(err)
	}
	persisted, err := NewStore(newFile).Read(scope)
	if err != nil || persisted.Entries[len(persisted.Entries)-1].Fields.NextAction != "Schedule a studio session" ||
		persisted.Entries[len(persisted.Entries)-1].Observations[0].FileIdentity != "fixture:1" {
		t.Fatalf("restart lost fields or machine evidence: %+v %v", persisted, err)
	}
}

func TestFields_MoreThan32ReviewedEditsAndStaleReview(t *testing.T) {
	r, scope, _, _, root := connectedMusicRoot(t)
	baseScan := reviewedRootScan(t, r, scope, root, "base-for-forty-fields")
	if baseScan.Status != "complete" {
		t.Fatal(baseScan)
	}
	doc, err := r.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = r.library.mutate(scope, doc.Revision,
		operation{key: "seed-40", action: "seed", digest: "fixture"},
		func(current *Document) (string, error) {
			for i := 0; i < 40; i++ {
				current.Entries = append(current.Entries, Entry{ID: fmt.Sprintf("catalog-%d", i), Revision: 1,
					Observations: []Observation{{RootID: root.ID, RelativeFolder: fmt.Sprintf("song-%d", i),
						FileIdentity: fmt.Sprintf("fixture:%d", i), Format: "reaper", Availability: "not_scanned",
						ScanID: baseScan.ID, ScannedAt: time.Now().UTC()}}})
			}
			return "fixture", nil
		})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 40; i++ {
		id := fmt.Sprintf("catalog-%d", i)
		patch := FieldsPatch{Status: text("active")}
		review, err := r.library.ReviewFields(scope, id, 0, patch, "local")
		if err != nil {
			t.Fatalf("review %d: %v", i, err)
		}
		if _, _, err := r.library.CommitFields(scope, id, review.Token,
			fmt.Sprintf("edit-%d", i), 0, patch, "local"); err != nil {
			t.Fatalf("edit %d rejected by old 32-item ceiling: %v", i, err)
		}
	}
	stored, err := r.library.Read(scope)
	if err != nil || len(stored.Entries) != 44 || stored.Entries[len(stored.Entries)-1].Fields.Status != "active" {
		t.Fatalf("40 reviewed edits not retained: %+v %v", stored, err)
	}
	stale, err := r.library.ReviewFields(scope, "catalog-0", 1,
		FieldsPatch{Status: text("complete")}, "local")
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := r.library.ReviewFields(scope, "catalog-0", 1,
		FieldsPatch{NextAction: text("Schedule mix")}, "local")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.library.CommitFields(scope, "catalog-0", stale.Token, "stale", 1,
		FieldsPatch{Status: text("complete")}, "local"); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale review overwrote concurrent edit: %v", err)
	}
	if _, _, err := r.library.CommitFields(scope, "catalog-0", fresh.Token, "fresh", 1,
		FieldsPatch{NextAction: text("Schedule mix")}, "local"); err != nil {
		t.Fatalf("most recent review rejected: %v", err)
	}
}

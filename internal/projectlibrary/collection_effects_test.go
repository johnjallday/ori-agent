package projectlibrary

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspacearrangement"
)

func collectionCompileFixture(t *testing.T) (Document, string, time.Time) {
	t.Helper()
	f, authority, record := homeArrangementFixture(t)
	saved, _, err := f.library.SaveArrangement(t.Context(), f.scope.OwnerUserID, authority, f.doc(t).Revision, true, record)
	if err != nil {
		t.Fatal(err)
	}
	return f.doc(t), saved.Record.ID, record.UpdatedAt.Add(time.Minute)
}

func TestCollectionCompiler_CreateIsStableDetachedAndMetadataOnly(t *testing.T) {
	doc, id, at := collectionCompileFixture(t)
	before, _ := json.Marshal(doc)
	changes, err := compileCollectionChanges(doc, id, at)
	if err != nil || len(changes.Collections) != 1 || len(changes.Effects) != 1 {
		t.Fatal(changes, err)
	}
	c := changes.Collections[0]
	if c.Name != "Client references" || c.Author != doc.Arrangements[0].ManagerName || c.ArrangementID != id || c.Revision != 1 {
		t.Fatal(c)
	}
	again, err := compileCollectionChanges(doc, id, at)
	if err != nil || !reflect.DeepEqual(changes, again) {
		t.Fatal("unstable compilation", err)
	}
	changes.Collections[0].EntryIDs[0] = "foreign"
	after, _ := json.Marshal(doc)
	if string(before) != string(after) {
		t.Fatal("compiler mutated canonical inputs")
	}
}

func TestCollectionCompiler_ExactChangesAllowOverlapAndEmptyMembership(t *testing.T) {
	for _, kind := range []string{workspacearrangement.RenameCollection, workspacearrangement.SetMembers, workspacearrangement.RemoveCollection} {
		t.Run(kind, func(t *testing.T) {
			doc, id, at := collectionCompileFixture(t)
			initial, err := compileCollectionChanges(doc, id, at)
			if err != nil {
				t.Fatal(err)
			}
			doc.Collections = initial.Collections
			target := doc.Collections[0]
			// Model a separately saved, current revision targeting this exact collection.
			r := &doc.Arrangements[0].Record
			r.Revision++
			r.ItemRevisions = map[string]string{}
			r.CollectionRevisions = map[string]int64{target.ID: target.Revision}
			op := workspacearrangement.Operation{Kind: kind, CollectionID: target.ID, CollectionRevision: target.Revision}
			if kind == workspacearrangement.RenameCollection {
				op.Name = "New client"
			}
			r.Draft.Operations = []workspacearrangement.Operation{op}
			before, _ := json.Marshal(doc)
			changes, err := compileCollectionChanges(doc, id, at.Add(time.Minute))
			if err != nil {
				t.Fatal(err)
			}
			if kind == workspacearrangement.RemoveCollection {
				if len(changes.Collections) != 0 || changes.Effects[0].AfterRevision != 0 {
					t.Fatal("remove kept metadata")
				}
			} else {
				c := changes.Collections[0]
				if c.Revision != target.Revision+1 || c.ID != target.ID {
					t.Fatal("identity/revision lost", c)
				}
				if kind == workspacearrangement.SetMembers && len(c.EntryIDs) != 0 {
					t.Fatal("empty membership was ignored")
				}
				if kind == workspacearrangement.RenameCollection && (!reflect.DeepEqual(c.EntryIDs, target.EntryIDs) || c.Name != "New client") {
					t.Fatal("rename changed membership")
				}
			}
			after, _ := json.Marshal(doc)
			if string(before) != string(after) {
				t.Fatal("compile changed entries, links or source state")
			}
		})
	}
	doc, id, at := collectionCompileFixture(t)
	op := doc.Arrangements[0].Record.Draft.Operations[0]
	op.Name = "Overlapping client"
	doc.Arrangements[0].Record.Draft.Operations = append(doc.Arrangements[0].Record.Draft.Operations, op)
	changes, err := compileCollectionChanges(doc, id, at)
	if err != nil || len(changes.Collections) != 2 || changes.Collections[0].ID == changes.Collections[1].ID || !reflect.DeepEqual(changes.Collections[0].EntryIDs, changes.Collections[1].EntryIDs) {
		t.Fatal("logical overlap refused or duplicated identity", err)
	}
}

func TestCollectionCompiler_PreservesExplicitExclusionsWhenDroppingMembership(t *testing.T) {
	for _, kind := range []string{workspacearrangement.SetMembers, workspacearrangement.RemoveCollection, workspacearrangement.RenameCollection} {
		for _, needsReview := range []bool{false, true} {
			doc, id, at := collectionCompileFixture(t)
			initial, err := compileCollectionChanges(doc, id, at)
			if err != nil {
				t.Fatal(err)
			}
			doc.Collections = initial.Collections
			target := doc.Collections[0]
			r := &doc.Arrangements[0].Record
			r.Revision++
			r.CollectionRevisions = map[string]int64{target.ID: target.Revision}
			selection := workspacearrangement.Selection{IDs: append([]string(nil), target.EntryIDs...)}
			if needsReview {
				r.Draft.NeedsReview = []workspacearrangement.Recommendation{{Items: selection, Reason: "Leave pending"}}
			} else {
				r.Draft.Unchanged = selection
			}
			op := workspacearrangement.Operation{Kind: kind, CollectionID: target.ID, CollectionRevision: target.Revision}
			if kind == workspacearrangement.RenameCollection {
				op.Name = "New label"
			}
			r.Draft.Operations = []workspacearrangement.Operation{op}
			if err := r.Validate(); err != nil {
				t.Fatal("fixture must be canonical", err)
			}
			_, err = compileCollectionChanges(doc, id, at.Add(time.Minute))
			if kind == workspacearrangement.RenameCollection {
				if err != nil {
					t.Fatal("rename changes no membership", err)
				}
			} else if !errors.Is(err, ErrConflict) {
				t.Fatal("explicit exclusion lost", kind, err)
			}
		}
	}
}

func TestCollectionCompiler_MembershipEnvelopeAndManualAuthorship(t *testing.T) {
	doc, id, at := collectionCompileFixture(t)
	seed := doc.Entries[0]
	for len(doc.Entries) < workspacearrangement.MaxSourceItems {
		entry := seed
		entry.ID = fmt.Sprintf("extra-entry-%d", len(doc.Entries))
		doc.Entries = append(doc.Entries, entry)
	}
	ids := make([]string, len(doc.Entries))
	for i, entry := range doc.Entries {
		ids[i] = entry.ID
	}
	for i := 0; i < 4; i++ {
		doc.Collections = append(doc.Collections, LogicalCollection{ID: fmt.Sprintf("large-%d", i), Name: "Large", Revision: 1, EntryIDs: ids, Author: "owner", Origin: "manual", CreatedAt: at, UpdatedAt: at})
	}
	if _, err := compileCollectionChanges(doc, id, at); !errors.Is(err, ErrLimit) {
		t.Fatal("membership cap ignored", err)
	}
	r := &doc.Arrangements[0].Record
	r.Draft.Operations[0].Members.IDs = nil
	r.ItemRevisions = map[string]string{}
	r.Origin = "manual"
	changes, err := compileCollectionChanges(doc, id, at)
	if err != nil || len(changes.Collections) != 5 || changes.Collections[4].Author != doc.OwnerUserID || len(changes.Collections[4].EntryIDs) != 0 {
		t.Fatal("empty manual collection at membership boundary refused or misattributed", err)
	}
}

func TestCollectionCompiler_RejectsStaleForeignAndMixedIntentWithoutPartialResult(t *testing.T) {
	for _, failure := range []string{"catalog", "entry", "foreign", "state", "home", "clarification", "activation", "limit", "duplicate_record"} {
		t.Run(failure, func(t *testing.T) {
			doc, id, at := collectionCompileFixture(t)
			r := &doc.Arrangements[0].Record
			switch failure {
			case "catalog":
				doc.Revision++
			case "entry":
				doc.Entries[0].Revision++
			case "foreign":
				r.Draft.Operations[0].Members.IDs = []string{"foreign"}
				r.ItemRevisions = map[string]string{"foreign": "1-1"}
			case "state":
				r.State = "stale"
			case "home":
				r.SourceID = "another-home"
			case "clarification":
				doc.Arrangements[0].Kind = "clarification"
			case "activation":
				r.Draft.Operations = append(r.Draft.Operations,
					workspacearrangement.Operation{Kind: workspacearrangement.SelectActivation, Members: workspacearrangement.Selection{IDs: r.Draft.Operations[0].Members.IDs[:1]}},
					workspacearrangement.Operation{Kind: workspacearrangement.SelectActivation, Members: workspacearrangement.Selection{IDs: r.Draft.Operations[0].Members.IDs[1:2]}})
			case "limit":
				for i := 0; i < 100; i++ {
					doc.Collections = append(doc.Collections, LogicalCollection{ID: fmt.Sprintf("existing-%d", i), Name: "Existing", Revision: 1, Author: "owner", Origin: "manual", CreatedAt: at, UpdatedAt: at})
				}
			case "duplicate_record":
				doc.Arrangements = append(doc.Arrangements, doc.Arrangements[0])
			}
			changes, err := compileCollectionChanges(doc, id, at)
			if err == nil || len(changes.Collections) != 0 || len(changes.Effects) != 0 {
				t.Fatal("unsafe partial compilation", changes, err)
			}
			if failure == "activation" && !errors.Is(err, ErrUnavailable) {
				t.Fatal("mixed effects did not fail explicitly", err)
			}
		})
	}
}

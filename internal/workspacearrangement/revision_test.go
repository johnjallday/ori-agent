package workspacearrangement

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func revisionRecord(t *testing.T, count int) (Record, *Snapshot) {
	t.Helper()
	e, snapshot := evidenceFixture(t, count)
	selection, err := e.Select(t.Context(), Filter{})
	if err != nil {
		t.Fatal(err)
	}
	wire := Draft{Version: Version, SourceRevision: selection.Revision, Goal: "Preserve the owner's selected references", Operations: []Operation{{Kind: CreateCollection, Name: "Owner corrected name", Members: Selection{Handle: selection.Handle}}}}
	raw, _ := json.Marshal(wire)
	draft, err := e.Resolve(t.Context(), string(raw))
	if err != nil {
		t.Fatal(err)
	}
	record, err := e.NewRecord(t.Context(), draft, Provenance{Origin: "manual", SourceID: "home"})
	if err != nil {
		t.Fatal(err)
	}
	return record, snapshot
}
func freshRevisionEvidence(t *testing.T, snapshot *Snapshot) *Evidence {
	t.Helper()
	e, err := NewEvidence(t.Context(), func(context.Context) (Snapshot, error) { return *snapshot, nil })
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestRevisionContext_ThousandExactMembersReboundWithoutModelIDDump(t *testing.T) {
	record, snapshot := revisionRecord(t, 1000)
	before, _ := json.Marshal(record)
	snapshot.Revision = "new-catalog-envelope"
	e := freshRevisionEvidence(t, snapshot)
	wire, err := e.RebindDraft(t.Context(), record)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(wire)
	if len(raw) > 2048 || strings.Contains(string(raw), "item-0999") || wire.SourceRevision == record.Draft.SourceRevision || wire.Operations[0].Name != "Owner corrected name" || len(wire.Operations[0].Members.IDs) != 0 {
		t.Fatal("unbounded or altered prior context", string(raw))
	}
	resolved, err := e.Resolve(t.Context(), string(raw))
	if err != nil || !reflect.DeepEqual(resolved.Operations[0].Members.IDs, record.Draft.Operations[0].Members.IDs) {
		t.Fatal("lost exact original membership", err)
	}
	after, _ := json.Marshal(record)
	if string(before) != string(after) {
		t.Fatal("rebind mutated canonical intent")
	}
	filter := Filter{SelectionHandle: wire.Operations[0].Members.Handle, RootID: "root-1"}
	seen, cursor := map[string]bool{}, ""
	for {
		page, err := e.Search(t.Context(), filter, cursor, 20)
		if err != nil || page.Total != 250 || len(page.Rows) > 20 {
			t.Fatal(page, err)
		}
		for _, row := range page.Rows {
			if seen[row.ID] {
				t.Fatal("duplicate")
			}
			seen[row.ID] = true
		}
		cursor = page.NextCursor
		if cursor == "" {
			break
		}
	}
	if len(seen) != 250 {
		t.Fatal("subset paging lost members")
	}
	selected, err := e.Select(t.Context(), filter)
	if err != nil || selected.Count != 250 {
		t.Fatal(selected, err)
	}
	if _, err := e.Search(t.Context(), Filter{SelectionHandle: "invented"}, "", 20); !errors.Is(err, ErrInvalid) {
		t.Fatal("invented selector accepted", err)
	}
}

func TestRevisionContext_PreservesExclusionsQuestionsAndDeduplicatesSets(t *testing.T) {
	record, snapshot := revisionRecord(t, 6)
	ids := record.Draft.Operations[0].Members.IDs
	record.Draft.Unchanged = Selection{IDs: []string{ids[0]}}
	record.Draft.NeedsReview = []Recommendation{{Items: Selection{IDs: []string{ids[1]}}, Reason: "Owner must choose the version"}}
	record.Draft.Operations[0].Members.IDs = slices.Clone(ids[2:])
	other := record.Draft.Operations[0]
	other.Name = "Overlapping references"
	other.Members.IDs = slices.Clone(other.Members.IDs)
	slices.Reverse(other.Members.IDs)
	record.Draft.Operations = append(record.Draft.Operations, other)
	e := freshRevisionEvidence(t, snapshot)
	wire, err := e.RebindDraft(t.Context(), record)
	if err != nil {
		t.Fatal(err)
	}
	if wire.Unchanged.Handle == "" || wire.NeedsReview[0].Items.Handle == "" || wire.Operations[0].Members.Handle != wire.Operations[1].Members.Handle || len(e.selections) != 3 {
		t.Fatal("lost exclusions or duplicated exact sets", wire)
	}
	raw, _ := json.Marshal(wire)
	resolved, err := e.Resolve(t.Context(), string(raw))
	if err != nil || resolved.Unchanged.IDs[0] != ids[0] || resolved.NeedsReview[0].Items.IDs[0] != ids[1] {
		t.Fatal(resolved, err)
	}
}

func TestRevisionContext_FailsClosedWithoutIssuingPartialAuthority(t *testing.T) {
	for _, mode := range []string{"changed-item", "changed-kind", "changed-collection", "changed-authority", "authority-lost-during-rebind", "expired", "cancelled", "completed", "too-many-sets"} {
		t.Run(mode, func(t *testing.T) {
			record, snapshot := revisionRecord(t, 20)
			if mode == "changed-item" {
				snapshot.Items[0].Revision = "2"
			}
			if mode == "changed-kind" {
				record.SourceKind = Attachment
			}
			if mode == "completed" {
				record.State = "completed"
			}
			if mode == "too-many-sets" {
				ids := record.Draft.Operations[0].Members.IDs
				record.Draft.Operations = nil
				for _, id := range ids {
					record.Draft.Operations = append(record.Draft.Operations, Operation{Kind: CreateCollection, Name: "Separate", Members: Selection{IDs: []string{id}}})
				}
			}
			if mode == "changed-collection" {
				record.ItemRevisions = nil
				record.CollectionRevisions = map[string]int64{"collection-a": 1}
				record.Draft.Operations = []Operation{{Kind: RenameCollection, Name: "New label", CollectionID: "collection-a", CollectionRevision: 1}}
				snapshot.Collections = []CollectionEvidence{{ID: "collection-a", Name: "Old label", Revision: 2}}
			}
			e := freshRevisionEvidence(t, snapshot)
			if mode == "changed-authority" {
				snapshot.Authority = "different-owner"
			}
			if mode == "authority-lost-during-rebind" {
				calls := 0
				e.source = func(context.Context) (Snapshot, error) {
					calls++
					if calls == 2 {
						snapshot.Authority = "lost-owner"
					}
					return *snapshot, nil
				}
			}
			if mode == "expired" {
				e.now = func() time.Time { return e.expires }
			}
			ctx := t.Context()
			if mode == "cancelled" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if _, err := e.RebindDraft(ctx, record); err == nil {
				t.Fatal("invalid prior intent rebound")
			}
			if len(e.selections) != 0 || len(e.collections) != 0 || len(e.issued) != 0 {
				t.Fatal("partial references escaped a rejected rebind")
			}
		})
	}
}

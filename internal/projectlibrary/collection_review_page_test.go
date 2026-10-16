package projectlibrary

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspacearrangement"
)

func TestCollectionReviewPage_ExactBoundedMembershipReadOnlyAndStaleRefusal(t *testing.T) {
	f := newRunFixture(t)
	authority := ManagerAuthority{HomeID: f.scope.HomeID, AgentInstanceID: managerBinding.AgentInstanceID, AgentName: managerBinding.AgentName}
	doc := f.doc(t)
	_, _, err := f.library.mutate(f.scope, doc.Revision, operation{key: "page-fixture", action: "fixture", digest: "fixture"}, func(current *Document) (string, error) {
		seed := current.Entries[0]
		current.Entries = nil
		for i := 0; i < 101; i++ {
			entry := seed
			entry.ID = fmt.Sprintf("entry-%03d", i)
			entry.Observations = append([]Observation(nil), seed.Observations...)
			entry.Observations[0].RelativeFolder = fmt.Sprintf("project-%03d", i)
			current.Entries = append(current.Entries, entry)
		}
		return "fixture", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := workspacearrangement.NewEvidence(t.Context(), func(ctx context.Context) (workspacearrangement.Snapshot, error) {
		return f.library.ArrangementEvidence(ctx, f.scope.OwnerUserID, authority)
	})
	if err != nil {
		t.Fatal(err)
	}
	selection, err := evidence.Select(t.Context(), workspacearrangement.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(workspacearrangement.Draft{Version: 1, SourceRevision: selection.Revision, Goal: "Keep these catalog-only", Operations: []workspacearrangement.Operation{{Kind: workspacearrangement.CreateCollection, Name: "All references", Members: workspacearrangement.Selection{Handle: selection.Handle}}}})
	draft, err := evidence.Resolve(t.Context(), string(raw))
	if err != nil {
		t.Fatal(err)
	}
	record, err := evidence.NewRecord(t.Context(), draft, workspacearrangement.Provenance{Origin: "manual", SourceID: f.scope.HomeID})
	if err != nil {
		t.Fatal(err)
	}
	saved, _, err := f.library.SaveArrangement(t.Context(), f.scope.OwnerUserID, authority, f.doc(t).Revision, true, record)
	if err != nil {
		t.Fatal(err)
	}
	service := NewCollectionReviewService(f.library, nil) // Owner-reviewed manual intent needs no model.
	page, err := service.Review(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, saved.Record.ID, f.doc(t).Revision, 1)
	if err != nil || page.Total != 101 || len(page.Members) != 50 || page.NextOffset == nil || *page.NextOffset != 50 {
		t.Fatal(page, err)
	}
	before := f.doc(t)
	seen := map[string]bool{}
	for offset := 0; offset < 101; offset += 50 {
		current, err := service.Page(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, saved.Record.ID, page.Token, 0, offset)
		if err != nil || len(current.Members) > 50 {
			t.Fatal(err)
		}
		for _, member := range current.Members {
			if seen[member.ID] || member.Before || !member.After {
				t.Fatal("duplicate or changed membership", member)
			}
			seen[member.ID] = true
		}
		encoded, _ := json.Marshal(current)
		if strings.Contains(string(encoded), before.Roots[0].Path) || strings.Contains(string(encoded), "file_identity") {
			t.Fatal("private source authority disclosed")
		}
	}
	if len(seen) != 101 || !reflect.DeepEqual(before, f.doc(t)) {
		t.Fatal("paging changed canonical state or omitted membership")
	}
	for _, args := range []struct {
		owner             string
		operation, offset int
	}{{"foreign", 0, 0}, {f.scope.OwnerUserID, 1, 0}, {f.scope.OwnerUserID, 0, 1}, {f.scope.OwnerUserID, 0, 150}} {
		if _, err := service.Page(t.Context(), args.owner, f.scope.HomeID, saved.Record.ID, page.Token, args.operation, args.offset); err == nil {
			t.Fatal("invalid page accepted", args)
		}
	}
	if _, err := service.Commit(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, saved.Record.ID, page.Token, "explicit", true); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Page(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, saved.Record.ID, page.Token, 0, 0); err == nil {
		t.Fatal("consumed page stayed current")
	}
	if len(f.doc(t).Collections[0].EntryIDs) != 101 {
		t.Fatal("compiled exact membership changed")
	}
}

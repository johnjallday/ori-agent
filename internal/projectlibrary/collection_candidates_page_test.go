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

func TestCollectionCandidates_PagedSearchManualAdditionAndAuthorityLoss(t *testing.T) {
	f, authority, _ := homeArrangementFixture(t)
	_, _, err := f.library.mutate(f.scope, f.doc(t).Revision, operation{key: "candidate-fixture", action: "fixture", digest: "fixture"}, func(doc *Document) (string, error) {
		seed := doc.Entries[0]
		doc.Entries = nil
		for i := 0; i < 61; i++ {
			entry := seed
			entry.ID = fmt.Sprintf("entry-%02d", i)
			entry.Fields.DisplayName = fmt.Sprintf("Client song %02d", i)
			doc.Entries = append(doc.Entries, entry)
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
	snapshot, err := evidence.Overview(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	draft := workspacearrangement.Draft{Version: 1, SourceRevision: snapshot.Revision, Goal: "Select saved references manually", Operations: []workspacearrangement.Operation{{Kind: workspacearrangement.CreateCollection, Name: "Owner selection"}}}
	record, err := evidence.NewRecord(t.Context(), draft, workspacearrangement.Provenance{Origin: "manual", SourceID: f.scope.HomeID})
	if err != nil {
		t.Fatal(err)
	}
	saved, _, err := f.library.SaveArrangement(t.Context(), f.scope.OwnerUserID, authority, f.doc(t).Revision, true, record)
	if err != nil {
		t.Fatal(err)
	}
	service := NewCollectionReviewService(f.library, nil)
	review, err := service.Review(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, saved.Record.ID, f.doc(t).Revision, 1)
	if err != nil {
		t.Fatal(err)
	}
	before := f.doc(t)
	seen := map[string]bool{}
	for offset := 0; offset < 61; offset += 20 {
		page, err := service.Candidates(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, saved.Record.ID, review.Token, 0, offset, "")
		if err != nil {
			t.Fatal(err)
		}
		if page.Total != 61 || len(page.Members) != min(20, 61-offset) {
			t.Fatal("wrong page", page)
		}
		for _, member := range page.Members {
			if seen[member.ID] {
				t.Fatal("duplicate", member.ID)
			}
			seen[member.ID] = true
		}
		raw, _ := json.Marshal(page)
		if strings.Contains(string(raw), before.Roots[0].Path) || strings.Contains(string(raw), "file_identity") {
			t.Fatal("source authority leaked")
		}
	}
	filtered, err := service.Candidates(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, saved.Record.ID, review.Token, 0, 0, "SONG 0")
	if err != nil || filtered.Total != 10 {
		t.Fatal(filtered, err)
	}
	for _, query := range []string{"' OR true", "<script>", "*"} {
		page, err := service.Candidates(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, saved.Record.ID, review.Token, 0, 0, query)
		if err != nil || page.Total != 0 {
			t.Fatal("query was not literal", page, err)
		}
	}
	for _, offset := range []int{-1, 1, 80, 5020} {
		if _, err := service.Candidates(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, saved.Record.ID, review.Token, 0, offset, ""); err == nil {
			t.Fatal("invalid offset accepted", offset)
		}
	}
	if len(seen) != 61 || !reflect.DeepEqual(before, f.doc(t)) {
		t.Fatal("candidate paging altered or lost catalog state")
	}
	f.available.Store(false)
	if _, err := service.Candidates(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, saved.Record.ID, review.Token, 0, 0, ""); err == nil {
		t.Fatal("provider loss did not close picker")
	}
	f.available.Store(true)
	if _, err := service.Correct(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, saved.Record.ID, CollectionCorrectionRequest{ReviewToken: review.Token, AddIDs: []string{"entry-00", "entry-60"}}); err != nil {
		t.Fatal(err)
	}
	if len(f.doc(t).Collections) != 0 || f.doc(t).Arrangements[0].Record.Validate() != nil {
		t.Fatal("manual addition applied effects or saved invalid pins")
	}
}

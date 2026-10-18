package workspacearrangement

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func evidenceFixture(t *testing.T, count int) (*Evidence, *Snapshot) {
	t.Helper()
	snapshot := &Snapshot{Kind: HomeLibrary, Revision: "1", Authority: "owner-home-instance-provider",
		Coverage: Coverage{Origin: "saved_catalog", Note: "Saved metadata, no inspection", Partial: true}}
	for i := 0; i < count; i++ {
		snapshot.Items = append(snapshot.Items, Item{ID: fmt.Sprintf("item-%04d", i), Revision: "1", Name: fmt.Sprintf("Song %03d", i%250),
			Format: "reaper", Connection: "catalog_only", Availability: "available",
			Sources: []ItemSource{{RootID: fmt.Sprintf("root-%d", i%4), Format: "reaper", Availability: "available"}}})
	}
	evidence, err := NewEvidence(t.Context(), func(context.Context) (Snapshot, error) { return *snapshot, nil })
	if err != nil {
		t.Fatal(err)
	}
	return evidence, snapshot
}

func TestEvidence_ThousandRowsArePagedAndSelectionsStayFrozen(t *testing.T) {
	e, _ := evidenceFixture(t, 1000)
	o, err := e.Overview(t.Context())
	if err != nil || o.Total != 1000 || len(o.Samples) != 5 || o.Roots["root-0"] != 250 || o.SampleNotice == "" {
		t.Fatalf("overview: %+v %v", o, err)
	}
	seen, cursor := map[string]bool{}, ""
	for {
		page, err := e.Search(t.Context(), Filter{}, cursor, 20)
		if err != nil || page.Total != 1000 || len(page.Rows) > 20 {
			t.Fatalf("page: %+v %v", page, err)
		}
		for _, row := range page.Rows {
			if seen[row.ID] {
				t.Fatal("duplicate name changed exact identity")
			}
			seen[row.ID] = true
		}
		cursor = page.NextCursor
		if cursor == "" {
			break
		}
	}
	if len(seen) != 1000 {
		t.Fatalf("lost tail: %d", len(seen))
	}
	selection, err := e.Select(t.Context(), Filter{RootID: "root-1"})
	if err != nil || selection.Count != 250 || selection.Handle == "" {
		t.Fatalf("selection: %+v %v", selection, err)
	}
	again, err := e.Select(t.Context(), Filter{RootID: "root-1"})
	if err != nil || again != selection {
		t.Fatal("same exact set consumed another handle")
	}
	draft := Draft{Version: Version, SourceRevision: o.Revision, Goal: "Keep a client reference collection",
		Operations: []Operation{{Kind: CreateCollection, Name: "Client work", Members: Selection{Handle: selection.Handle}}}}
	raw, err := json.Marshal(draft)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := e.Resolve(t.Context(), string(raw))
	if err != nil || len(resolved.Operations[0].Members.IDs) != 250 || resolved.Operations[0].Members.Handle != "" {
		t.Fatalf("not an exact durable selection: %+v %v", resolved, err)
	}
	resolved.Operations[0].Members.IDs[0] = "tampered"
	resolved, err = e.Resolve(t.Context(), string(raw))
	if err != nil || resolved.Operations[0].Members.IDs[0] == "tampered" {
		t.Fatal("returned draft aliased held selector")
	}
}

func TestEvidence_QueriesDoNotIssueGuessedIDsOrShareMutableState(t *testing.T) {
	e, _ := evidenceFixture(t, 1000)
	if _, err := e.Detail(t.Context(), "item-0999"); !errors.Is(err, ErrInvalid) {
		t.Fatal("guessed ID was readable")
	}
	o, err := e.Overview(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	o.Samples[0].Sources[0].RootID = "tampered"
	row, err := e.Detail(t.Context(), o.Samples[0].ID)
	if err != nil || row.Sources[0].RootID == "tampered" {
		t.Fatal("overview aliased private snapshot")
	}
	frozen, err := e.Select(t.Context(), Filter{})
	if err != nil {
		t.Fatal(err)
	}
	// A handle lets the model select its whole exact set, not pretend to have
	// inspected arbitrary individual items inside it.
	d := Draft{Version: Version, SourceRevision: frozen.Revision, Goal: "Guess an item", Unchanged: Selection{IDs: []string{"item-0999"}}}
	raw, _ := json.Marshal(d)
	if _, err := e.Resolve(t.Context(), string(raw)); !errors.Is(err, ErrInvalid) {
		t.Fatal("selector silently issued every individual item ID")
	}
}

func TestEvidence_CursorAndAuthorityCannotCrossTurnsOrRevisions(t *testing.T) {
	for _, change := range []string{"revision", "authority", "metadata", "expired"} {
		t.Run(change, func(t *testing.T) {
			e, s := evidenceFixture(t, 40)
			page, err := e.Search(t.Context(), Filter{}, "", 5)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "revision":
				s.Revision = "2"
			case "authority":
				s.Authority = "replacement Manager"
			case "metadata":
				s.Items[0].Name = "changed without revision"
			case "expired":
				e.now = func() time.Time { return e.expires }
			}
			if _, err := e.Search(t.Context(), Filter{}, page.NextCursor, 5); !errors.Is(err, ErrStale) {
				t.Fatalf("changed source silently refreshed: %v", err)
			}
		})
	}
	e, _ := evidenceFixture(t, 40)
	page, _ := e.Search(t.Context(), Filter{}, "", 5)
	other, _ := evidenceFixture(t, 40)
	if _, err := other.Search(t.Context(), Filter{}, page.NextCursor, 5); !errors.Is(err, ErrStale) {
		t.Fatal("cursor crossed a turn")
	}
	for _, cursor := range []string{page.NextCursor + "x", "../../somewhere"} {
		if _, err := e.Search(t.Context(), Filter{}, cursor, 5); !errors.Is(err, ErrStale) {
			t.Fatal("forged cursor accepted")
		}
	}
	if _, err := e.Search(t.Context(), Filter{Text: "Song"}, page.NextCursor, 5); !errors.Is(err, ErrStale) {
		t.Fatal("cursor crossed filters")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := e.Overview(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled query returned evidence")
	}
}

func TestEvidence_RootFilterKeepsFormatAndAvailabilityTogether(t *testing.T) {
	_, s := evidenceFixture(t, 1)
	s.Items[0].Sources = append(s.Items[0].Sources, ItemSource{RootID: "older-root", Format: "ableton", Availability: "revoked_source"})
	e, err := NewEvidence(t.Context(), func(context.Context) (Snapshot, error) { return *s, nil })
	if err != nil {
		t.Fatal(err)
	}
	page, err := e.Search(t.Context(), Filter{RootID: "older-root", Format: "ableton", Availability: "revoked_source"}, "", 5)
	if err != nil || page.Total != 1 || page.Rows[0].Format != "ableton" || page.Rows[0].Availability != "revoked_source" {
		t.Fatalf("root facts mixed: %+v %v", page, err)
	}
	page, err = e.Search(t.Context(), Filter{RootID: "older-root", Availability: "available"}, "", 5)
	if err != nil || page.Total != 0 {
		t.Fatal("revoked root borrowed another source's availability")
	}
	if _, err := e.Select(t.Context(), Filter{RootID: "foreign"}); !errors.Is(err, ErrInvalid) {
		t.Fatal("foreign root accepted")
	}
}

func TestEvidence_CollectionsBeyondTheOverviewSampleAreReachable(t *testing.T) {
	_, snapshot := evidenceFixture(t, 20)
	for i := 0; i < 20; i++ {
		snapshot.Collections = append(snapshot.Collections, CollectionEvidence{ID: fmt.Sprintf("collection-%02d", i), Name: fmt.Sprintf("Client %02d", i), Revision: 1, MemberIDs: []string{fmt.Sprintf("item-%04d", i)}})
	}
	e, err := NewEvidence(t.Context(), func(context.Context) (Snapshot, error) { return *snapshot, nil })
	if err != nil {
		t.Fatal(err)
	}
	first, err := e.Overview(t.Context())
	if err != nil || len(first.Collections) != 5 || first.TotalCollections != 20 || first.NextCollectionCursor == "" {
		t.Fatalf("overview: %+v %v", first, err)
	}
	second, err := e.OverviewPage(t.Context(), "", first.NextCollectionCursor)
	if err != nil || second.Collections[0].ID != "collection-05" {
		t.Fatalf("next collection page: %+v %v", second, err)
	}
	if _, err := e.Search(t.Context(), Filter{}, first.NextCollectionCursor, 5); !errors.Is(err, ErrStale) {
		t.Fatal("collection cursor crossed into item pages")
	}
	found, err := e.OverviewPage(t.Context(), "Client 19", "")
	if err != nil || len(found.Collections) != 1 || found.MatchingCollections != 1 || found.Collections[0].ID != "collection-19" {
		t.Fatalf("late collection: %+v %v", found, err)
	}
	selected, err := e.Select(t.Context(), Filter{CollectionID: "collection-19"})
	if err != nil || selected.Count != 1 {
		t.Fatalf("late membership inaccessible: %+v %v", selected, err)
	}
	if _, err := e.OverviewPage(t.Context(), "Client 19", first.NextCollectionCursor); !errors.Is(err, ErrStale) {
		t.Fatal("cursor crossed collection filters")
	}
}

func TestEvidence_EmptyResultsLimitsAndUnavailableSource(t *testing.T) {
	e, _ := evidenceFixture(t, 40)
	selection, err := e.Select(t.Context(), Filter{Text: "no matching project"})
	if err != nil || selection.Count != 0 || selection.Handle != "" {
		t.Fatalf("empty selection: %+v %v", selection, err)
	}
	for i := 0; i < 16; i++ {
		if _, err := e.Select(t.Context(), Filter{Text: fmt.Sprintf("Song %03d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.Select(t.Context(), Filter{}); !errors.Is(err, ErrLimit) {
		t.Fatal("unbounded selectors")
	}
	if _, err := e.Search(t.Context(), Filter{}, "", 51); !errors.Is(err, ErrLimit) {
		t.Fatal("unbounded page")
	}
	e.source = func(context.Context) (Snapshot, error) { return Snapshot{}, ErrUnavailable }
	if _, err := e.Detail(t.Context(), "item-0000"); !errors.Is(err, ErrUnavailable) {
		t.Fatal("lost authority reused cached evidence")
	}

	_, s := evidenceFixture(t, 20)
	for i := range s.Items {
		s.Items[i].Name = strings.Repeat("<", 384)
	}
	e, err = NewEvidence(t.Context(), func(context.Context) (Snapshot, error) { return *s, nil })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Search(t.Context(), Filter{}, "", 20); !errors.Is(err, ErrLimit) {
		t.Fatal("oversized result silently truncated")
	}
	page, err := e.Search(t.Context(), Filter{}, "", 1)
	if err != nil || page.Total != 20 || len(page.Rows) != 1 {
		t.Fatal("narrowing failed")
	}
}

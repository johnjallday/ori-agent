package projectlibrary

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspacearrangement"
)

// Opt-in normal-build envelope evidence (ORI_ARRANGEMENT_PERF=1): the full
// 5,000-entry catalog over four roots through real authorization, evidence,
// save, review and apply paths. Observations are fixture-seeded metadata, not
// a discovery grant or a fresh scan; timings are warm in-process measurements
// unless labelled otherwise and include no model time.
func TestArrangementEnvelope_FiveThousandEntriesFourRootsAndHardBounds(t *testing.T) {
	if os.Getenv("ORI_ARRANGEMENT_PERF") != "1" {
		t.Skip("set ORI_ARRANGEMENT_PERF=1 for the 5,000-entry envelope measurement")
	}
	f, authority, record := homeArrangementFixture(t)
	const total, roots, selected = 5000, 4, 1250
	_, _, err := f.library.mutate(f.scope, f.doc(t).Revision, operation{key: "envelope-fixture", action: "fixture", digest: "fixture"}, func(doc *Document) (string, error) {
		if len(doc.Roots) == 0 || len(doc.Scans) == 0 || len(doc.Entries) == 0 || len(doc.Entries[0].Observations) == 0 {
			return "", ErrCorrupt
		}
		baseRoot, baseScan, seed := doc.Roots[0], doc.Scans[0], doc.Entries[0]
		for i := 1; i < roots; i++ {
			root := baseRoot
			root.ID, root.Path, root.FileIdentity = fmt.Sprintf("%s-%d", baseRoot.ID, i), fmt.Sprintf("%s-%d", baseRoot.Path, i), fmt.Sprintf("%s-%d", baseRoot.FileIdentity, i)
			doc.Roots = append(doc.Roots, root)
			scan := baseScan
			scan.ID, scan.RootID = fmt.Sprintf("%s-%d", baseScan.ID, i), root.ID
			doc.Scans = append(doc.Scans, scan)
		}
		doc.Entries = nil
		for i := 0; i < total; i++ {
			entry := seed
			entry.ID = fmt.Sprintf("entry-%04d", i)
			label := "Experiment"
			if i%4 == 0 {
				label = "Client"
			}
			entry.Fields.DisplayName = fmt.Sprintf("%s %04d", label, i)
			observation := seed.Observations[0]
			observation.RootID, observation.ScanID = doc.Roots[i%roots].ID, doc.Scans[i%roots].ID
			observation.RelativeFolder = fmt.Sprintf("%s %04d", label, i)
			observation.FileIdentity = fmt.Sprintf("identity-%04d", i)
			observation.Alternates = nil
			entry.Observations = []Observation{observation}
			doc.Entries = append(doc.Entries, entry)
		}
		return "fixture", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	full := f.doc(t)
	encoded, _ := json.Marshal(full)
	if len(full.Entries) != total || len(full.Roots) != roots {
		t.Fatalf("fixture: %d entries, %d roots", len(full.Entries), len(full.Roots))
	}
	report := func(phase string, first time.Duration, samples []time.Duration) {
		slices.Sort(samples)
		p95 := samples[len(samples)*95/100-1]
		t.Logf("envelope phase=%s go=%s os=%s arch=%s cpus=%d entries=%d roots=%d document_bytes=%d samples=%d first=%s p50=%s p95=%s max=%s",
			phase, runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.NumCPU(), total, roots, len(encoded), len(samples), first, samples[len(samples)/2-1], p95, samples[len(samples)-1])
		if p95 > 250*time.Millisecond {
			t.Errorf("%s warm p95 %s exceeds the 250ms target", phase, p95)
		}
	}

	// Hard bounds: one entry past 5,000, or a document past 8 MiB, is refused
	// whole and leaves the canonical document exactly as it was.
	if _, _, err := f.library.mutate(f.scope, full.Revision, operation{key: "envelope-over-count", action: "fixture", digest: "fixture"}, func(doc *Document) (string, error) {
		extra := doc.Entries[0]
		extra.ID = "entry-5000"
		doc.Entries = append(doc.Entries, extra)
		return "fixture", nil
	}); err == nil {
		t.Fatal("a 5,001st entry was accepted")
	}
	alternates := make([]string, 64)
	for i := range alternates {
		alternates[i] = fmt.Sprintf("%s-%02d.rpp", strings.Repeat("a", 200), i)
	}
	if _, _, err := f.library.mutate(f.scope, full.Revision, operation{key: "envelope-over-bytes", action: "fixture", digest: "fixture"}, func(doc *Document) (string, error) {
		for i := range doc.Entries {
			doc.Entries[i].Observations[0].Alternates = alternates
		}
		return "fixture", nil
	}); !errors.Is(err, ErrLimit) {
		t.Fatalf("a document past 8 MiB: %v", err)
	}
	if after := f.doc(t); !reflect.DeepEqual(after, full) {
		t.Fatal("a refused bound wrote the library")
	}

	// Evidence: aggregate overview and filtered model pages over the whole
	// catalog, through the exact Manager authorization on every call.
	evidence, err := workspacearrangement.NewEvidence(t.Context(), func(ctx context.Context) (workspacearrangement.Snapshot, error) {
		return f.library.ArrangementEvidence(ctx, f.scope.OwnerUserID, authority)
	})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	overview, err := evidence.Overview(t.Context())
	first := time.Since(start)
	if err != nil || overview.Total != total || len(overview.Roots) != roots || len(overview.Samples) > 5 {
		t.Fatalf("overview: total=%d roots=%d samples=%d %v", overview.Total, len(overview.Roots), len(overview.Samples), err)
	}
	if raw, _ := json.Marshal(overview); len(raw) > workspacearrangement.MaxToolResultBytes {
		t.Fatalf("overview exceeds the tool result bound: %d bytes", len(raw))
	}
	queries := make([]time.Duration, 100)
	for i := range queries {
		filter := workspacearrangement.Filter{Text: []string{"Client", "Experiment"}[i%2]}
		start = time.Now()
		page, err := evidence.Search(t.Context(), filter, "", workspacearrangement.ModelPageSize)
		queries[i] = time.Since(start)
		if err != nil || len(page.Rows) != workspacearrangement.ModelPageSize || page.Total == 0 {
			t.Fatalf("search %d: %+v %v", i, page.Total, err)
		}
		if raw, _ := json.Marshal(page); len(raw) > workspacearrangement.MaxToolResultBytes {
			t.Fatalf("search page exceeds the tool result bound: %d bytes", len(raw))
		}
	}
	report("evidence_search", first, queries)

	// A frozen 1,250-member selection is saved as exact membership, reviewed in
	// 50-row pages and applied as metadata only.
	frozen, err := evidence.Select(t.Context(), workspacearrangement.Filter{Text: "Client"})
	if err != nil || frozen.Count != total/4 {
		t.Fatalf("select: %+v %v", frozen, err)
	}
	draft := workspacearrangement.Draft{Version: 1, SourceRevision: frozen.Revision, Goal: "Group the client references; leave experiments catalog-only",
		Operations: []workspacearrangement.Operation{{Kind: workspacearrangement.CreateCollection, Name: "Client work", Members: workspacearrangement.Selection{Handle: frozen.Handle}}}}
	raw, _ := json.Marshal(draft)
	resolved, err := evidence.Resolve(t.Context(), string(raw))
	if err != nil || len(resolved.Operations[0].Members.IDs) != selected {
		t.Fatalf("resolve: %d %v", len(resolved.Operations[0].Members.IDs), err)
	}
	prepared, err := evidence.NewRecord(t.Context(), resolved, workspacearrangement.Provenance{Origin: "model", SourceID: f.scope.HomeID, Provider: record.Provider, Model: record.Model, ProviderFingerprint: record.ProviderFingerprint})
	if err != nil {
		t.Fatal(err)
	}
	start = time.Now()
	saved, _, err := f.library.SaveArrangement(t.Context(), f.scope.OwnerUserID, authority, f.doc(t).Revision, true, prepared)
	saveTime := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	service := correctionService(t, f, prepared)
	start = time.Now()
	review, err := service.Review(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, saved.Record.ID, f.doc(t).Revision, 1)
	reviewTime := time.Since(start)
	if err != nil || review.Total != selected || len(review.Members) != workspacearrangement.MaxPageSize {
		t.Fatalf("review: total=%d %v", review.Total, err)
	}
	pages := make([]time.Duration, 100)
	for i := range pages {
		offset := (i % (selected / workspacearrangement.MaxPageSize)) * workspacearrangement.MaxPageSize
		start = time.Now()
		page, err := service.Page(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, saved.Record.ID, review.Token, 0, offset)
		pages[i] = time.Since(start)
		if err != nil || page.Offset != offset || len(page.Members) != workspacearrangement.MaxPageSize {
			t.Fatalf("page %d: %v", offset, err)
		}
	}
	report("review_page", reviewTime, pages)
	start = time.Now()
	receipt, err := service.Commit(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, saved.Record.ID, review.Token, "envelope-apply", true)
	applyTime := time.Since(start)
	if err != nil || receipt.CollectionReceipt == nil {
		t.Fatal(err)
	}
	after := f.doc(t)
	afterBytes, _ := json.Marshal(after)
	if len(after.Collections) != 1 || len(after.Collections[0].EntryIDs) != selected || !reflect.DeepEqual(after.Entries, full.Entries) || len(afterBytes) > maxDocumentBytes {
		t.Fatalf("apply: %d collections, %d bytes", len(after.Collections), len(afterBytes))
	}
	t.Logf("envelope phase=save_review_apply entries=%d members=%d save=%s review_first_page=%s apply=%s document_bytes_after=%d", total, selected, saveTime, reviewTime, applyTime, len(afterBytes))
	if reviewTime > 500*time.Millisecond {
		t.Errorf("first review page %s exceeds the 500ms target", reviewTime)
	}
	if applyTime > time.Second {
		t.Errorf("metadata apply %s exceeds the 1s target", applyTime)
	}
}

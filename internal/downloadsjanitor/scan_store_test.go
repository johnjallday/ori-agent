package downloadsjanitor

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func candidateFor(name string, mutate ...func(*JanitorCandidate)) JanitorCandidate {
	c := JanitorCandidate{
		ID:           "cand-" + name,
		WorkspaceID:  "ws-1",
		Name:         name,
		Size:         1024,
		ModifiedAt:   time.Date(2026, 7, 24, 9, 0, 0, 0, time.UTC),
		DiscoveredAt: time.Date(2026, 7, 24, 9, 1, 0, 0, time.UTC),
		Fingerprint:  Fingerprint{Name: name, Size: 1024, ModTime: time.Date(2026, 7, 24, 9, 0, 0, 0, time.UTC)},
		Category:     "documents",
		State:        CandidatePending,
	}
	for _, m := range mutate {
		m(&c)
	}
	return c
}

func batchFor(id string, source ScanSource) JanitorBatch {
	return JanitorBatch{ID: id, WorkspaceID: "ws-1", Source: source, StartedAt: time.Now()}
}

func TestLoadScanState_MissingRecordIsEmptyNotAnError(t *testing.T) {
	store, _ := newTestStore(t)
	state, err := store.LoadScanState("ws-1")
	if err != nil {
		t.Fatalf("LoadScanState: %v", err)
	}
	if len(state.Batches) != 0 || len(state.Candidates) != 0 {
		t.Fatalf("expected empty state, got %+v", state)
	}
}

func TestAppendBatch_StoresCandidatesAndSummary(t *testing.T) {
	store, resolver := newTestStore(t)

	batch, err := store.AppendBatch("ws-1", batchFor("b1", ScanSourceManual), []JanitorCandidate{
		candidateFor("a.pdf"),
		candidateFor("b.zip", func(c *JanitorCandidate) { c.NeedsReview = true }),
	})
	if err != nil {
		t.Fatalf("AppendBatch: %v", err)
	}
	if len(batch.CandidateIDs) != 2 || batch.Summary.Proposed != 2 || batch.Summary.NeedsReview != 1 {
		t.Fatalf("stored batch = %+v", batch)
	}

	// A restarted process reads the same answer from disk.
	restarted := NewStore(resolver)
	state, err := restarted.LoadScanState("ws-1")
	if err != nil {
		t.Fatalf("LoadScanState after restart: %v", err)
	}
	if len(state.CandidatesFor("b1")) != 2 {
		t.Fatalf("candidates did not survive a restart: %+v", state.Candidates)
	}
	if len(state.PendingCandidates()) != 2 {
		t.Fatal("pending candidates must survive a restart")
	}
}

func TestUpdateCandidate_PersistsDecisionsAndResummarizes(t *testing.T) {
	store, resolver := newTestStore(t)
	if _, err := store.AppendBatch("ws-1", batchFor("b1", ScanSourceManual), []JanitorCandidate{
		candidateFor("a.pdf"), candidateFor("b.pdf"),
	}); err != nil {
		t.Fatalf("AppendBatch: %v", err)
	}

	updated, err := store.UpdateCandidate("ws-1", "cand-a.pdf", func(c *JanitorCandidate) error {
		c.Decision = DecisionMove
		c.DecisionCategory = "archives"
		c.DecidedAt = time.Now()
		return nil
	})
	if err != nil {
		t.Fatalf("UpdateCandidate: %v", err)
	}
	if updated.EffectiveCategory() != "archives" {
		t.Fatalf("decision not applied: %+v", updated)
	}

	// A pending decision is exactly what must not be lost to a restart: the
	// user did that work.
	restarted := NewStore(resolver)
	state, err := restarted.LoadScanState("ws-1")
	if err != nil {
		t.Fatalf("LoadScanState: %v", err)
	}
	got, ok := state.Candidate("cand-a.pdf")
	if !ok || got.Decision != DecisionMove || got.DecisionCategory != "archives" {
		t.Fatalf("decision did not survive a restart: %+v", got)
	}

	// Marking one skipped re-summarizes the batch in the same write.
	if _, err := store.UpdateCandidate("ws-1", "cand-b.pdf", func(c *JanitorCandidate) error {
		c.State = CandidateSkipped
		c.Decision = DecisionSkip
		return nil
	}); err != nil {
		t.Fatalf("UpdateCandidate: %v", err)
	}
	state, err = store.LoadScanState("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	batch, _ := state.Batch("b1")
	if batch.Summary.Skipped != 1 || batch.Summary.Proposed != 1 {
		t.Fatalf("batch summary not updated with the decision: %+v", batch.Summary)
	}
}

func TestUpdateCandidate_UnknownIDIsNotFound(t *testing.T) {
	store, _ := newTestStore(t)
	if _, err := store.UpdateCandidate("ws-1", "nope", func(*JanitorCandidate) error { return nil }); !errors.Is(err, ErrCandidateNotFound) {
		t.Fatalf("expected ErrCandidateNotFound, got %v", err)
	}
}

// A candidate stored under one workspace must be invisible — and unusable —
// from another, whatever ID the caller guesses.
func TestScanState_IsWorkspaceScoped(t *testing.T) {
	store, _ := newTestStore(t)
	if _, err := store.AppendBatch("ws-1", batchFor("b1", ScanSourceManual), []JanitorCandidate{candidateFor("a.pdf")}); err != nil {
		t.Fatalf("AppendBatch: %v", err)
	}

	other, err := store.LoadScanState("ws-2")
	if err != nil {
		t.Fatalf("LoadScanState: %v", err)
	}
	if len(other.Candidates) != 0 {
		t.Fatalf("another workspace must not see these candidates: %+v", other.Candidates)
	}
	if _, err := store.UpdateCandidate("ws-2", "cand-a.pdf", func(*JanitorCandidate) error { return nil }); !errors.Is(err, ErrCandidateNotFound) {
		t.Fatalf("cross-workspace update must fail as not-found, got %v", err)
	}
}

func TestLoadScanState_RejectsAnotherWorkspacesRecord(t *testing.T) {
	store, _ := newTestStore(t)
	if _, err := store.AppendBatch("ws-2", batchFor("b1", ScanSourceManual), []JanitorCandidate{
		candidateFor("a.pdf", func(c *JanitorCandidate) { c.WorkspaceID = "ws-2" }),
	}); err != nil {
		t.Fatalf("AppendBatch: %v", err)
	}
	src, _ := store.scanStatePath("ws-2")
	dst, _ := store.scanStatePath("ws-1")
	data, err := os.ReadFile(src) // #nosec G304 -- test fixture path
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadScanState("ws-1"); !errors.Is(err, ErrWorkspaceMismatch) {
		t.Fatalf("expected ErrWorkspaceMismatch, got %v", err)
	}
}

func TestUpdateScanState_RejectsMalformedRecords(t *testing.T) {
	store, _ := newTestStore(t)

	cases := map[string]func(*ScanState) error{
		"candidate with a path name": func(s *ScanState) error {
			s.Candidates = append(s.Candidates, candidateFor("a.pdf", func(c *JanitorCandidate) {
				c.Name = "sub/a.pdf"
				c.Fingerprint.Name = "sub/a.pdf"
			}))
			return nil
		},
		"candidate from another workspace": func(s *ScanState) error {
			s.Candidates = append(s.Candidates, candidateFor("a.pdf", func(c *JanitorCandidate) { c.WorkspaceID = "ws-2" }))
			return nil
		},
		"duplicate candidate id": func(s *ScanState) error {
			s.Candidates = append(s.Candidates, candidateFor("a.pdf"), candidateFor("a.pdf"))
			return nil
		},
		"batch referencing an unknown candidate": func(s *ScanState) error {
			batch := batchFor("b1", ScanSourceManual)
			batch.CandidateIDs = []string{"ghost"}
			s.Batches = append(s.Batches, batch)
			return nil
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := store.UpdateScanState("ws-1", mutate); !errors.Is(err, ErrInvalidCandidate) {
				t.Fatalf("expected ErrInvalidCandidate, got %v", err)
			}
			state, err := store.LoadScanState("ws-1")
			if err != nil {
				t.Fatal(err)
			}
			if len(state.Candidates) != 0 {
				t.Fatalf("a rejected write must not reach disk: %+v", state.Candidates)
			}
		})
	}
}

func TestPruneScanState_NeverDropsBatchesWithPendingWork(t *testing.T) {
	store, _ := newTestStore(t)

	// One old pending batch, then more than the cap in resolved batches.
	if _, err := store.AppendBatch("ws-1", batchFor("pending-batch", ScanSourceManual), []JanitorCandidate{
		candidateFor("keep-me.pdf"),
	}); err != nil {
		t.Fatalf("AppendBatch: %v", err)
	}
	for i := range MaxRetainedBatches + 5 {
		name := fmt.Sprintf("resolved-%d.pdf", i)
		if _, err := store.AppendBatch("ws-1", batchFor(fmt.Sprintf("b-%d", i), ScanSourceDaily), []JanitorCandidate{
			candidateFor(name, func(c *JanitorCandidate) { c.State = CandidateApplied }),
		}); err != nil {
			t.Fatalf("AppendBatch: %v", err)
		}
	}

	state, err := store.LoadScanState("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Batches) > MaxRetainedBatches {
		t.Fatalf("batches not bounded: %d", len(state.Batches))
	}
	if _, ok := state.Batch("pending-batch"); !ok {
		t.Fatal("a batch with pending work must never be pruned")
	}
	if _, ok := state.Candidate("cand-keep-me.pdf"); !ok {
		t.Fatal("a pending candidate must never be pruned")
	}
	// The oldest resolved batches went first.
	if _, ok := state.Batch("b-0"); ok {
		t.Fatal("expected the oldest resolved batch to be pruned")
	}
}

func TestPruneScanState_BoundsObservationsAndSkips(t *testing.T) {
	now := time.Now()
	state := newScanState("ws-1")
	for i := range MaxRetainedObservations + 50 {
		state.Observations = append(state.Observations, SettledObservation{
			Name: fmt.Sprintf("f-%d", i), ObservedAt: now.Add(-time.Duration(i) * time.Minute),
		})
	}
	// One long-expired observation ages out regardless of the cap.
	state.Observations = append(state.Observations, SettledObservation{Name: "ancient", ObservedAt: now.Add(-2 * ObservationTTL)})
	for i := range MaxRetainedSkips + 50 {
		state.Skipped = append(state.Skipped, SkippedFingerprint{
			Key: fmt.Sprintf("k-%d", i), SkippedAt: now.Add(-time.Duration(i) * time.Minute),
		})
	}

	pruned := pruneScanState(state, now)
	if len(pruned.Observations) > MaxRetainedObservations {
		t.Fatalf("observations not bounded: %d", len(pruned.Observations))
	}
	for _, observation := range pruned.Observations {
		if observation.Name == "ancient" {
			t.Fatal("an expired observation must age out")
		}
	}
	if len(pruned.Skipped) > MaxRetainedSkips {
		t.Fatalf("skips not bounded: %d", len(pruned.Skipped))
	}
	// Skips are user decisions: the most recent survive the cap.
	if pruned.Skipped[len(pruned.Skipped)-1].Key != "k-0" {
		t.Fatalf("expected the newest skip retained, got %q", pruned.Skipped[len(pruned.Skipped)-1].Key)
	}
}

func TestRecordObservation_TracksUnchangedSpanAndRestartsOnChange(t *testing.T) {
	state := newScanState("ws-1")
	start := time.Date(2026, 7, 24, 9, 0, 0, 0, time.UTC)
	modTime := start.Add(-time.Minute)

	first := RecordObservation(&state, "big.iso", 100, modTime, start)
	if !first.FirstSeenAt.Equal(start) {
		t.Fatalf("first sighting = %+v", first)
	}

	// Same size and timestamp 40s later: the settling span grows.
	second := RecordObservation(&state, "big.iso", 100, modTime, start.Add(40*time.Second))
	if !second.FirstSeenAt.Equal(start) {
		t.Fatal("an unchanged file must keep its original first-seen time")
	}
	if second.ObservedAt.Sub(second.FirstSeenAt) != 40*time.Second {
		t.Fatalf("settling span = %v", second.ObservedAt.Sub(second.FirstSeenAt))
	}
	if len(state.Observations) != 1 {
		t.Fatalf("expected one observation per file, got %d", len(state.Observations))
	}

	// The file grows: the clock restarts, so a still-downloading file can never
	// accumulate its way to "settled".
	third := RecordObservation(&state, "big.iso", 200, modTime.Add(time.Second), start.Add(70*time.Second))
	if !third.FirstSeenAt.Equal(start.Add(70 * time.Second)) {
		t.Fatalf("a changed file must restart the settling clock: %+v", third)
	}
}

func TestSkips_RememberedByFingerprintAndResettable(t *testing.T) {
	state := newScanState("ws-1")
	fingerprint := Fingerprint{Name: "ad.pdf", Size: 10, ModTime: time.Now().UTC()}
	now := time.Now()

	if state.IsSkipped(fingerprint) {
		t.Fatal("nothing is skipped initially")
	}
	MarkSkipped(&state, fingerprint, now)
	if !state.IsSkipped(fingerprint) {
		t.Fatal("the dismissed file state must stay dismissed")
	}

	// The same file, changed, is a new proposal — that is the point of keying
	// skips by fingerprint rather than by name.
	changed := fingerprint
	changed.Size = 20
	if state.IsSkipped(changed) {
		t.Fatal("a changed file must not inherit the skip")
	}

	MarkSkipped(&state, fingerprint, now)
	if len(state.Skipped) != 1 {
		t.Fatalf("re-skipping must not duplicate the record: %+v", state.Skipped)
	}

	ClearSkipped(&state, fingerprint.Key())
	if state.IsSkipped(fingerprint) {
		t.Fatal("clearing one skip must forget it")
	}

	MarkSkipped(&state, fingerprint, now)
	ClearSkipped(&state, "")
	if len(state.Skipped) != 0 {
		t.Fatal("clearing with no key must reset every skip")
	}
}

func TestActiveFingerprints_ExcludesResolvedAndStaleCandidates(t *testing.T) {
	state := newScanState("ws-1")
	pending := candidateFor("a.pdf")
	applied := candidateFor("b.pdf", func(c *JanitorCandidate) { c.State = CandidateApplied })
	stale := candidateFor("c.pdf", func(c *JanitorCandidate) { c.State = CandidateStale })
	state.Candidates = []JanitorCandidate{pending, applied, stale}

	active := state.ActiveFingerprints()
	if _, ok := active[pending.Fingerprint.Key()]; !ok {
		t.Fatal("a pending candidate is active")
	}
	if _, ok := active[applied.Fingerprint.Key()]; ok {
		t.Fatal("an applied candidate must not block a fresh proposal")
	}
	// A stale candidate is exactly the case that should be re-proposed once the
	// file settles again.
	if _, ok := active[stale.Fingerprint.Key()]; ok {
		t.Fatal("a stale candidate must not block a fresh proposal")
	}
}

func TestScanState_PersistsNoFileContent(t *testing.T) {
	store, _ := newTestStore(t)
	if _, err := store.AppendBatch("ws-1", batchFor("b1", ScanSourceManual), []JanitorCandidate{candidateFor("a.pdf")}); err != nil {
		t.Fatalf("AppendBatch: %v", err)
	}
	path, _ := store.scanStatePath("ws-1")
	data, err := os.ReadFile(path) // #nosec G304 -- test fixture path
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("scan state is not valid JSON: %v", err)
	}
	// Nothing in the record may carry bytes read from a file, extracted text,
	// or an absolute path.
	for _, banned := range []string{"content", "text", "excerpt", "preview", "absolute", "root_path"} {
		if strings.Contains(string(data), `"`+banned) {
			t.Fatalf("scan state must not persist %q", banned)
		}
	}
}

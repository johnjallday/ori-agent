package filejanitor

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func draftFixture() (ScanState, reviewDraftBinding) {
	batch, candidates := groupFixture("one.pdf", "two.pdf")
	binding := reviewDraftBinding{Owner: "local", RootID: "root-1", InstalledAt: time.Unix(100, 0), CapabilityVersion: 1, Digest: strings.Repeat("a", 64)}
	state := ScanState{SchemaVersion: ScanStateSchemaVersion, WorkspaceID: batch.WorkspaceID, Batches: []JanitorBatch{batch}, Candidates: candidates}
	state.ReviewDrafts = []ReviewDraft{{
		SchemaVersion: ReviewDraftSchemaVersion, BatchID: batch.ID, Owner: binding.Owner,
		RootID: binding.RootID, InstalledAt: binding.InstalledAt, CapabilityVersion: binding.CapabilityVersion,
		ScopeDigest: binding.Digest, Generation: 1,
		Choices: []ReviewChoice{{CandidateID: candidates[0].ID, FingerprintKey: candidates[0].Fingerprint.Key(), Operation: OperationMove, SourceGroupID: reviewGroupID("filing-documents")}},
	}}
	return state, binding
}

func persistRawDraftState(t *testing.T, store *Store, data []byte) string {
	t.Helper()
	path, err := store.scanStatePath("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReviewDraft_RestartRestoresOnlyExplicitChoices(t *testing.T) {
	state, binding := draftFixture()
	// A legacy category edit on another member must not select it.
	state.Candidates[1].Decision = DecisionMove
	state.Candidates[1].DecisionCategory = CategoryImages
	store, resolver := newTestStore(t)
	if _, err := store.UpdateScanState("ws-1", func(current *ScanState) error { *current = state; return nil }); err != nil {
		t.Fatal(err)
	}
	restarted := NewStore(resolver)
	loaded, err := restarted.LoadScanState("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	view := projectReviewDraft(loaded, "batch-1", binding, "members")
	if view.State != "saved" || len(view.Choices) != 1 || view.Choices[0].CandidateID != state.Candidates[0].ID || view.Choices[0].Category != CategoryDocuments {
		t.Fatalf("lost exact intent or inferred category selection: %+v", view)
	}
	before := projectReviewDraft(state, "batch-1", binding, "members")
	if before.Revision != view.Revision {
		t.Fatal("restart changed intent revision")
	}
	data, err := json.Marshal(view)
	if err != nil || strings.Contains(string(data), "fingerprint") || strings.Contains(string(data), "token") || strings.Contains(string(data), "scope_digest") {
		t.Fatalf("browser draft exposed private binding/authority: %s %v", data, err)
	}
	if len(loaded.Approvals) != 0 || len(loaded.Actions) != 0 {
		t.Fatal("persisting choices issued authority or performed actions")
	}
}

func TestReviewDraft_LegacyScanSchemasNeverInferSelection(t *testing.T) {
	for _, schema := range []int{0, 1, 2} {
		t.Run(fmt.Sprint(schema), func(t *testing.T) {
			state, binding := draftFixture()
			state.SchemaVersion = schema
			state.Candidates[0].Decision, state.Candidates[0].DecisionCategory = DecisionMove, CategoryImages
			state.Candidates[0].DecisionGroupID = reviewGroupID("filing-images") // stray new-schema provenance is ignored too
			data, err := json.Marshal(state)                                     // stray draft-shaped fields are ignored under legacy schema
			if err != nil {
				t.Fatal(err)
			}
			store, _ := newTestStore(t)
			path := persistRawDraftState(t, store, data)
			loaded, err := store.LoadScanState("ws-1")
			if err != nil || loaded.SchemaVersion != ScanStateSchemaVersion || len(loaded.ReviewDrafts) != 0 {
				t.Fatalf("legacy selection invented: %+v %v", loaded, err)
			}
			view := projectReviewDraft(loaded, "batch-1", binding, "members")
			if view.State != "empty" || len(view.Choices) != 0 || loaded.Candidates[0].DecisionCategory != CategoryImages || loaded.Candidates[0].DecisionGroupID != "" {
				t.Fatal("legacy destination/selection compatibility lost")
			}
			after, err := os.ReadFile(path)
			if err != nil || string(after) != string(data) {
				t.Fatal("loading migrated state by writing", err)
			}
		})
	}
}

func TestReviewDraft_UnsupportedScanSchemaRefusesDownWrite(t *testing.T) {
	store, _ := newTestStore(t)
	data := []byte(`{"schema_version":999,"workspace_id":"ws-1","review_drafts":[{"schema_version":99}]}`)
	path := persistRawDraftState(t, store, data)
	if _, err := store.LoadScanState("ws-1"); !errors.Is(err, ErrUnsupportedScanSchema) {
		t.Fatalf("unknown scan schema normalized away: %v", err)
	}
	called := false
	if _, err := store.UpdateScanState("ws-1", func(*ScanState) error { called = true; return nil }); !errors.Is(err, ErrUnsupportedScanSchema) || called {
		t.Fatalf("future schema reached writer: called=%v err=%v", called, err)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(data) {
		t.Fatal("unsupported schema overwritten", err)
	}
}

func TestReviewDraft_UnsupportedHeaderIsCheckedBeforeUnknownPayload(t *testing.T) {
	for _, raw := range []string{
		`{"schema_version":999,"workspace_id":"ws-1","candidates":{},"review_drafts":{}}`,
		`{"schema_version":"future","workspace_id":"ws-1"}`,
	} {
		store, _ := newTestStore(t)
		path := persistRawDraftState(t, store, []byte(raw))
		if _, err := store.UpdateScanState("ws-1", func(*ScanState) error { t.Fatal("future state reached writer"); return nil }); !errors.Is(err, ErrUnsupportedScanSchema) {
			t.Fatalf("future payload was reset before raw schema check: %v", err)
		}
		after, _ := os.ReadFile(path)
		if string(after) != raw {
			t.Fatal("unknown payload down-written")
		}
	}
}

func TestReviewDraft_MalformedAndFutureDraftsAreInertAndPreserved(t *testing.T) {
	for _, raw := range []string{
		`{"batch_id":"batch-1","schema_version":999,"choices":[{"candidate_id":"cand-0","operation":"move"}]}`,
		`{"batch_id":"batch-1","schema_version":1,"choices":"not-an-array"}`,
		`{"batch_id":"batch-1","schema_version":1,"choices":[{"operation":"trash"}]}`,
	} {
		t.Run(raw, func(t *testing.T) {
			state, binding := draftFixture()
			state.ReviewDrafts = nil
			data, err := json.Marshal(state)
			if err != nil {
				t.Fatal(err)
			}
			var envelope map[string]json.RawMessage
			if err := json.Unmarshal(data, &envelope); err != nil {
				t.Fatal(err)
			}
			envelope["review_drafts"] = json.RawMessage("[" + raw + "]")
			data, _ = json.Marshal(envelope)
			store, _ := newTestStore(t)
			persistRawDraftState(t, store, data)
			loaded, err := store.LoadScanState("ws-1")
			if err != nil || len(loaded.Candidates) != 2 {
				t.Fatalf("bad draft stranded canonical batch: %v", err)
			}
			view := projectReviewDraft(loaded, "batch-1", binding, "members")
			if view.State != "invalid" || len(view.Choices) != 0 {
				t.Fatalf("bad draft supplied selection: %+v", view)
			}
			stored, err := store.UpdateScanState("ws-1", func(s *ScanState) error { s.Approvals = nil; return nil })
			if err != nil || len(stored.ReviewDrafts) != 1 {
				t.Fatalf("unrelated write dropped invalid pending record: %v", err)
			}
			encoded, err := json.Marshal(stored.ReviewDrafts[0])
			if err != nil || string(encoded) != raw {
				t.Fatalf("down-wrote unknown draft schema: %s %v", encoded, err)
			}
			reloaded, err := store.LoadScanState("ws-1")
			if err != nil {
				t.Fatal(err)
			}
			wire, err := json.Marshal(reloaded.ReviewDrafts[0])
			if err != nil {
				t.Fatal(err)
			}
			var original, roundTrip any
			if err := json.Unmarshal([]byte(raw), &original); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(wire, &roundTrip); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(original, roundTrip) {
				t.Fatal("unrelated write lost opaque future draft fields")
			}
		})
	}
}

func TestReviewDraft_MalformedContainerCannotOverwriteCanonicalState(t *testing.T) {
	store, _ := newTestStore(t)
	data := []byte(`{"schema_version":3,"workspace_id":"ws-1","review_drafts":"broken"}`)
	path := persistRawDraftState(t, store, data)
	if _, err := store.UpdateScanState("ws-1", func(*ScanState) error { t.Fatal("malformed draft reached writer"); return nil }); !errors.Is(err, ErrReviewDraftInvalid) {
		t.Fatalf("malformed container was silently reset: %v", err)
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(data) {
		t.Fatal("failed-closed draft loading changed disk")
	}
}

func TestReviewDraft_ReconcilesScopeStateAndOrphansWithoutWriting(t *testing.T) {
	for _, mode := range []string{"owner", "root", "install", "binding", "orphan", "foreign batch", "changed fingerprint", "skipped", "failed", "needs review", "duplicate draft"} {
		t.Run(mode, func(t *testing.T) {
			state, binding := draftFixture()
			switch mode {
			case "owner":
				binding.Owner = "other"
			case "root":
				binding.RootID = "other-root"
			case "install":
				binding.InstalledAt = binding.InstalledAt.Add(time.Second)
			case "binding":
				binding.Digest = strings.Repeat("b", 64)
			case "orphan":
				state.Candidates = state.Candidates[1:]
			case "foreign batch":
				state.Candidates[0].BatchID = "other"
			case "changed fingerprint":
				state.Candidates[0].Fingerprint.FileID = "replacement"
			case "skipped":
				state.Candidates[0].State = CandidateSkipped
			case "failed":
				state.Candidates[0].State = CandidateFailed
			case "needs review":
				state.Candidates[0].NeedsReview = true
			case "duplicate draft":
				state.ReviewDrafts = append(state.ReviewDrafts, state.ReviewDrafts[0])
			}
			before, _ := json.Marshal(state)
			view := projectReviewDraft(state, "batch-1", binding, "members")
			if len(view.Choices) != 0 || view.RemovedStale == 0 {
				t.Fatalf("stale choices survived: %+v", view)
			}
			after, _ := json.Marshal(state)
			if string(before) != string(after) {
				t.Fatal("read projection persisted reconciliation")
			}
		})
	}
}

func TestReviewDraft_RevisionBindsIntentAndCanonicalDestinationNotTokenChurn(t *testing.T) {
	state, binding := draftFixture()
	before := projectReviewDraft(state, "batch-1", binding, "members")
	state.UpdatedAt = time.Now()
	state.Approvals = []ApprovalRecord{{ID: "unrelated-token"}}
	after := projectReviewDraft(state, "batch-1", binding, "members")
	if before.Revision != after.Revision {
		t.Fatal("timestamp/token churn invalidated intent")
	}
	state.Candidates[1].DecisionCategory = CategoryImages // even an unselected category edit must CAS
	if projectReviewDraft(state, "batch-1", binding, "members").Revision == before.Revision {
		t.Fatal("category edit can be overwritten by a stale tab")
	}
	state.Candidates[1].DecisionCategory = ""
	state.ReviewDrafts[0].Choices = nil
	if projectReviewDraft(state, "batch-1", binding, "members").Revision == before.Revision {
		t.Fatal("cleared intent did not change revision")
	}
}

func TestReviewDraft_RetentionPreservesPendingAndPrunesResolvedChoices(t *testing.T) {
	state, _ := draftFixture()
	state.Candidates[0].State = CandidateStale
	pruned := pruneScanState(state, time.Now())
	if len(pruned.Batches) != 1 || len(pruned.ReviewDrafts) != 1 || len(pruned.ReviewDrafts[0].Choices) != 0 || pruned.ReviewDrafts[0].RemovedStale != 1 {
		t.Fatal("resolved choice not reconciled while other pending work retained")
	}
	state, _ = draftFixture()
	pruned = pruneScanState(state, time.Now())
	if !reflect.DeepEqual(state.ReviewDrafts, pruned.ReviewDrafts) {
		t.Fatal("pruning discarded pending explicit work")
	}
	state.Batches = nil
	pruned = pruneScanState(state, time.Now())
	if len(pruned.ReviewDrafts) != 0 {
		t.Fatal("draft outlived its retained canonical batch")
	}
}

func TestReviewDraft_BoundsRefuseRatherThanTruncate(t *testing.T) {
	state, _ := draftFixture()
	choices := make([]ReviewChoice, MaxReviewChoicesPerBatch+1)
	for i := range choices {
		choices[i] = ReviewChoice{CandidateID: fmt.Sprintf("c-%d", i), FingerprintKey: strings.Repeat("a", 32), Operation: OperationMove}
	}
	state.ReviewDrafts[0].Choices = choices
	if err := validateScanState(state); !errors.Is(err, ErrReviewBounds) {
		t.Fatalf("oversized draft not rejected: %v", err)
	}
	state.ReviewDrafts[0].Choices = choices[:MaxReviewChoicesPerBatch]
	for i := range 10 {
		draft := state.ReviewDrafts[0]
		draft.BatchID = fmt.Sprintf("batch-extra-%d", i)
		state.ReviewDrafts = append(state.ReviewDrafts, draft)
	}
	if err := validateScanState(state); !errors.Is(err, ErrReviewBounds) {
		t.Fatalf("workspace draft cap not enforced: %v", err)
	}
}

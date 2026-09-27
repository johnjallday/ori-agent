package projectlibrary

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func connectedMusicRoot(t *testing.T) (*Roots, Scope, *workspace.FileStore, musicTree, Root) {
	t.Helper()
	r, scope, file, picker := rootTestService(t)
	tree := newMusicTree(t)
	picker.path, _ = filepath.EvalSymlinks(tree.root)
	pick, err := r.Pick(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	review, err := r.Review(scope, pick, 1)
	if err != nil {
		t.Fatal(err)
	}
	root, _, err := r.Commit(scope, review.Token, "connect-fixture")
	if err != nil {
		t.Fatal(err)
	}
	return r, scope, file, tree, root
}

func TestScans_ReviewedOneShotPersistsMetadataAndNeverScansOnRead(t *testing.T) {
	r, scope, file, tree, root := connectedMusicRoot(t)
	before := fileDigest(t, filepath.Join(tree.single, "Song.rpp"))
	doc, err := r.library.Read(scope)
	if err != nil || doc.Revision != 3 || len(doc.Scans) != 0 {
		t.Fatalf("reading scanned filesystem: %+v %v", doc, err)
	}
	preview, err := r.ReviewScan(scope, root.ID, doc.Revision)
	if err != nil || !preview.IncludesScan || preview.RootPath != root.Path || preview.MaxEntries != scanEntryLimit {
		t.Fatalf("scan disclosure: %+v %v", preview, err)
	}
	if doc, err = r.library.Read(scope); err != nil || len(doc.Scans) != 0 {
		t.Fatalf("review triggered scan: %+v %v", doc, err)
	}
	result, replay, err := r.CommitScan(context.Background(), scope, root.ID, preview.Token, "scan-once")
	if err != nil || replay || result.Status != "complete" || result.FinishedAt == nil ||
		result.RootDigest == "" || result.ResultDigest == "" || result.EntriesSeen == 0 {
		t.Fatalf("scan commit: %+v replay=%v err=%v", result, replay, err)
	}
	stored, err := r.library.Read(scope)
	if err != nil || len(stored.Scans) != 1 || len(stored.Entries) != 4 {
		t.Fatalf("scan not persisted: %+v %v", stored, err)
	}
	byFormat := map[string]int{}
	for _, entry := range stored.Entries {
		if entry.Fields.DisplayName != "" || entry.Fields.Status != "" || len(entry.Observations) != 1 ||
			entry.Observations[0].ScanID != result.ID {
			t.Fatalf("scan forged user fields or links: %+v", entry)
		}
		byFormat[entry.Observations[0].Format]++
	}
	if byFormat["reaper"] != 2 || byFormat["logic"] != 1 || byFormat["ableton"] != 1 {
		t.Fatalf("wrong DAW evidence: %v", byFormat)
	}
	afterRead, err := r.library.Read(scope)
	if err != nil || afterRead.Revision != stored.Revision {
		t.Fatalf("read migrated on its own: %+v %v", afterRead, err)
	}
	newStore, err := workspace.NewFileStore(filepath.Dir(filepath.Dir(file.GetFilesPath(scope.HomeID))))
	if err != nil {
		t.Fatal(err)
	}
	restarted := NewRoots(NewStore(newStore), nil, nil, r.guards)
	replayed, wasReplay, err := restarted.CommitScan(context.Background(), scope, root.ID, preview.Token, "scan-once")
	if err != nil || !wasReplay || replayed.ID != result.ID || replayed.Status != "complete" {
		t.Fatalf("restart replay reran scan: %+v replay=%v err=%v", replayed, wasReplay, err)
	}
	rescanReview, err := restarted.ReviewScan(scope, root.ID, stored.Revision)
	if err != nil {
		t.Fatalf("explicit rescan review: %v", err)
	}
	rescan, replay, err := restarted.CommitScan(context.Background(), scope, root.ID, rescanReview.Token, "same-root-rescan")
	if err != nil || replay || rescan.Status != "complete" {
		t.Fatalf("stable rescan: %+v %v %v", rescan, replay, err)
	}
	again, err := restarted.library.Read(scope)
	if err != nil || len(again.Entries) != len(stored.Entries) {
		t.Fatalf("rescan duplicated records: %+v %v", again, err)
	}
	for i := range stored.Entries {
		if again.Entries[i].ID != stored.Entries[i].ID || again.Entries[i].Fields.Revision != stored.Entries[i].Fields.Revision {
			t.Fatalf("rescan replaced stable ID/user fields: before=%+v after=%+v", stored.Entries[i], again.Entries[i])
		}
	}
	if after := fileDigest(t, filepath.Join(tree.single, "Song.rpp")); after != before {
		t.Fatal("scan modified source bytes")
	}
}

func TestScans_KnownScopeChoicesArePagedInertAndBoundToCurrentRoot(t *testing.T) {
	r, scope, _, tree, root := connectedMusicRoot(t)
	for i := 0; i < 22; i++ {
		if err := os.Mkdir(filepath.Join(tree.root, fmt.Sprintf("Scope-%02d", i)), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	review, err := r.ReviewScan(scope, root.ID, 3)
	if err != nil {
		t.Fatal(err)
	}
	initial, _, err := r.CommitScan(context.Background(), scope, root.ID, review.Token, "known-choices")
	if err != nil || initial.Status != "complete" {
		t.Fatalf("initial scan: %+v %v", initial, err)
	}
	doc, err := r.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	first, err := r.ListScanScopes(scope, root.ID, doc.Revision, 0)
	if err != nil || first.Total < 27 || len(first.Rows) != 20 || first.NextOffset != 20 || first.Revision != doc.Revision {
		t.Fatalf("bounded first scope page: %+v %v", first, err)
	}
	second, err := r.ListScanScopes(scope, root.ID, doc.Revision, first.NextOffset)
	if err != nil || len(second.Rows) != first.Total-20 || second.NextOffset != 0 {
		t.Fatalf("bounded last scope page: %+v %v", second, err)
	}
	for _, choice := range append(first.Rows, second.Rows...) {
		if choice.ID == "" || choice.RelativeFolder == "" || filepath.IsAbs(choice.RelativeFolder) {
			t.Fatalf("unusable scope choice: %+v", choice)
		}
	}
	if after, err := r.library.Read(scope); err != nil || after.Revision != doc.Revision || len(after.Scans) != 1 {
		t.Fatalf("scope GET changed Home or scanned source: %+v %v", after, err)
	}
	if _, err := r.ListScanScopes(scope, root.ID, doc.Revision-1, 0); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale page accepted: %v", err)
	}
	if _, err := r.ListScanScopes(scope, "other-root", doc.Revision, 0); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("foreign root revealed choices: %v", err)
	}
	if _, err := r.ListScanScopes(scope, root.ID, doc.Revision, -1); !errors.Is(err, ErrLimit) {
		t.Fatalf("negative page accepted: %v", err)
	}
	secondReview, err := r.ReviewScan(scope, root.ID, doc.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if secondScan, _, err := r.CommitScan(context.Background(), scope, root.ID, secondReview.Token, "repeat-known-choices"); err != nil || secondScan.Status != "complete" {
		t.Fatalf("repeated root scan: %+v %v", secondScan, err)
	}
	doc, err = r.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	latestChoices, err := r.ListScanScopes(scope, root.ID, doc.Revision, 0)
	if err != nil || latestChoices.Total != first.Total || latestChoices.Rows[0].ID == first.Rows[0].ID {
		t.Fatalf("old scope ID was offered instead of the latest witness: %+v %v", latestChoices, err)
	}
	if _, err := os.Stat(tree.root); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tree.root, tree.root+"-offline"); err != nil {
		t.Fatal(err)
	}
	if offline, err := r.ListScanScopes(scope, root.ID, doc.Revision, 0); err != nil || offline.Total != first.Total {
		t.Fatalf("historical scope listing incorrectly probed offline drive: %+v %v", offline, err)
	}
	impact, err := r.ReviewRevoke(scope, root.ID, doc.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.CommitRevoke(scope, root.ID, impact.Token, "forget-known-scope-authority"); err != nil {
		t.Fatal(err)
	}
	latest, err := r.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.ListScanScopes(scope, root.ID, latest.Revision, 0); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("disconnected root still offered future follow-up choices: %v", err)
	}
}

func TestScans_ScopedFollowUpRequiresPersistedDirectoryIDAndFreshReview(t *testing.T) {
	r, scope, _, tree, root := connectedMusicRoot(t)
	deep := filepath.Join(tree.single, "Subproject")
	if err := os.Mkdir(deep, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deep, "Second.rpp"), []byte("never read by catalog"), 0o600); err != nil {
		t.Fatal(err)
	}
	first, err := r.ReviewScan(scope, root.ID, 3)
	if err != nil {
		t.Fatal(err)
	}
	initial, _, err := r.CommitScan(context.Background(), scope, root.ID, first.Token, "initial-with-deep")
	if err != nil || initial.Status != "complete" {
		t.Fatalf("initial scan: %+v %v", initial, err)
	}
	doc, err := r.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Entries) != 4 {
		t.Fatalf("deep song was auto-scanned without narrower review: %+v", doc.Entries)
	}
	if _, err := r.ReviewScopedScan(scope, root.ID, "../../elsewhere", doc.Revision); !errors.Is(err, ErrConflict) {
		t.Fatalf("forged relative scope accepted: %v", err)
	}
	var scopeID string
	for _, known := range initial.KnownScopes {
		if known.RelativeFolder == filepath.Join("Single", "Subproject") {
			scopeID = known.ID
		}
	}
	if scopeID == "" {
		t.Fatalf("deep directory was not saved as server-known scope: %+v", initial.KnownScopes)
	}
	second, err := r.ReviewScopedScan(scope, root.ID, scopeID, doc.Revision)
	if err != nil || second.RelativeFolder != filepath.Join("Single", "Subproject") || !second.IncludesScan {
		t.Fatalf("follow-up disclosure: %+v %v", second, err)
	}
	if doc, err := r.library.Read(scope); err != nil || len(doc.Entries) != 4 {
		t.Fatalf("review scanned without consent: %+v %v", doc, err)
	}
	deepScan, replay, err := r.CommitScopedScan(context.Background(), scope, root.ID, scopeID, second.Token, "scoped-once")
	if err != nil || replay || deepScan.Status != "complete" || deepScan.ScopeID != scopeID ||
		deepScan.Scope != filepath.Join("Single", "Subproject") {
		t.Fatalf("follow-up scan: %+v %v %v", deepScan, replay, err)
	}
	doc, err = r.library.Read(scope)
	if err != nil || len(doc.Entries) != 5 || len(doc.Scans) != 2 ||
		doc.Entries[4].Observations[0].RelativeFolder != deepScan.Scope {
		t.Fatalf("scoped scan associated unrelated candidates: %+v %v", doc, err)
	}
	secondReview, err := r.ReviewScopedScan(scope, root.ID, scopeID, doc.Revision)
	if err != nil {
		t.Fatalf("same-scope rescan review: %v", err)
	}
	rescanned, wasReplay, err := r.CommitScopedScan(context.Background(), scope, root.ID, scopeID, secondReview.Token, "scoped-rescan")
	if err != nil || wasReplay || rescanned.Status != "complete" {
		t.Fatalf("same-scope reconciliation: %+v %v %v", rescanned, wasReplay, err)
	}
	again, err := r.library.Read(scope)
	if err != nil || len(again.Entries) != 5 || again.Entries[4].ID != doc.Entries[4].ID {
		t.Fatalf("scoped rescan duplicated a project: %+v %v", again, err)
	}
	got, replay, err := r.CommitScopedScan(context.Background(), scope, root.ID, scopeID, second.Token, "scoped-once")
	if err != nil || !replay || got.ID != deepScan.ID {
		t.Fatalf("scope retry repeated filesystem scan: %+v %v %v", got, replay, err)
	}
}

func TestScans_ReplacedKnownScopeRefusesCommitBeforeRunningState(t *testing.T) {
	r, scope, _, tree, root := connectedMusicRoot(t)
	deep := filepath.Join(tree.single, "Subproject")
	if err := os.Mkdir(deep, 0o750); err != nil {
		t.Fatal(err)
	}
	first, err := r.ReviewScan(scope, root.ID, 3)
	if err != nil {
		t.Fatal(err)
	}
	initial, _, err := r.CommitScan(context.Background(), scope, root.ID, first.Token, "initial-before-replace")
	if err != nil || initial.Status != "complete" {
		t.Fatalf("initial scan: %+v %v", initial, err)
	}
	var scopeID string
	for _, known := range initial.KnownScopes {
		if known.RelativeFolder == filepath.Join("Single", "Subproject") {
			scopeID = known.ID
		}
	}
	if scopeID == "" {
		t.Fatal("known child not persisted")
	}
	doc, err := r.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	review, err := r.ReviewScopedScan(scope, root.ID, scopeID, doc.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(deep, deep+"-original"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(deep, 0o750); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.CommitScopedScan(context.Background(), scope, root.ID, scopeID, review.Token, "replacement"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("same-name replacement accessed: %v", err)
	}
	doc, err = r.library.Read(scope)
	if err != nil || len(doc.Scans) != 1 || len(doc.Entries) != 4 {
		t.Fatalf("rejected scope created a scan: %+v %v", doc, err)
	}
}

func TestScans_InterruptedRunningRecordIsNeverPresentedAsComplete(t *testing.T) {
	r, scope, file, _, root := connectedMusicRoot(t)
	start := Scan{ID: newID(), RootID: root.ID, RootRevision: root.Revision,
		RootDigest: rootDigest(scope, "scan_source", root.Path, root.FileIdentity, root.Revision),
		Status:     "running", StartedAt: time.Now().UTC()}
	_, _, err := r.library.mutate(scope, 3, operation{key: "crash-after-start", action: "scan_start", digest: "reviewed"},
		func(doc *Document) (string, error) {
			doc.Scans = append(doc.Scans, start)
			return start.ID, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := workspace.NewFileStore(filepath.Dir(filepath.Dir(file.GetFilesPath(scope.HomeID))))
	if err != nil {
		t.Fatal(err)
	}
	restarted := NewRoots(NewStore(reopened), nil, nil, r.guards)
	doc, err := restarted.library.Read(scope)
	if err != nil || len(doc.Scans) != 1 || doc.Scans[0].Status != "running" || len(doc.Entries) != 0 {
		t.Fatalf("restart falsely completed interrupted scan: %+v %v", doc, err)
	}
	preview, err := restarted.ReviewScan(scope, root.ID, doc.Revision)
	if err != nil || !preview.InterruptsPriorScan {
		t.Fatalf("new review failed to disclose interrupted scan: %+v %v", preview, err)
	}
	complete, replay, err := restarted.CommitScan(context.Background(), scope, root.ID, preview.Token, "new-explicit-scan")
	if err != nil || replay || complete.Status != "complete" {
		t.Fatalf("new review could not resume scan: %+v %v %v", complete, replay, err)
	}
	doc, err = restarted.library.Read(scope)
	if err != nil || len(doc.Scans) != 2 || doc.Scans[0].Status != "interrupted" ||
		doc.Scans[1].Status != "complete" || doc.Scans[0].FinishedAt == nil {
		t.Fatalf("interrupted/complete states: %+v %v", doc, err)
	}
}

// This wrapper injects cancellation after the running state is durably saved,
// before the scanner is entered. No filesystem timing assumptions required.
type cancelAfterScanStart struct {
	workspace.Store
	cancel context.CancelFunc
}

func (c *cancelAfterScanStart) Update(id string, fn func(*workspace.Workspace) error) error {
	err := c.Store.Update(id, fn)
	if err == nil {
		if ws, readErr := c.Get(id); readErr == nil {
			if state := ws.GetAssistantProgramState(); state != nil && len(state.ProjectLibrary) != 0 {
				if doc, decodeErr := decodeDocument(state.ProjectLibrary, Scope{OwnerUserID: ws.OwnerUserID,
					HomeID: id, ProviderID: state.Key.PluginID, ProgramID: state.Key.ProgramID}); decodeErr == nil {
					for _, scan := range doc.Scans {
						if scan.Status == "running" {
							c.cancel()
							break
						}
					}
				}
			}
		}
	}
	return err
}

func TestScans_PartialCoveragePreservesHistoricalUserFields(t *testing.T) {
	r, scope, file, _, root := connectedMusicRoot(t)
	linkedID := "project"
	linkID := workspace.AssistantProjectLinkID(scope.HomeID, linkedID)
	project := &workspace.Workspace{ID: linkedID, Name: "Previously linked", OwnerUserID: scope.OwnerUserID,
		Status: workspace.StatusActive, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	project.SetAssistantProjectLink(&workspace.AssistantProjectLink{ID: linkID,
		StationWorkspaceID: scope.HomeID, StateRevision: 2,
		Key: workspace.AssistantProgramKey{OwnerUserID: scope.OwnerUserID,
			PluginID: scope.ProviderID, ProgramID: scope.ProgramID}})
	if err := file.Save(project); err != nil {
		t.Fatal(err)
	}
	if err := file.Update(scope.HomeID, func(home *workspace.Workspace) error {
		state := home.GetAssistantProgramState()
		state.LinkedProjectIDs = append(state.LinkedProjectIDs, linkedID)
		home.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	providerRevision, err := r.currentProviderRevision(scope)
	if err != nil {
		t.Fatal(err)
	}
	started := Scan{ID: newID(), RootID: root.ID, RootRevision: root.Revision,
		RootDigest:       rootDigest(scope, "scan_source", root.Path, root.FileIdentity, root.Revision),
		ProviderRevision: providerRevision, Status: "running", StartedAt: time.Now().UTC()}
	oldID := newID()
	_, _, err = r.library.mutate(scope, 3, operation{key: "start-partial", action: "scan_start", digest: "reviewed"},
		func(doc *Document) (string, error) {
			doc.Scans = append(doc.Scans, started)
			doc.Entries = append(doc.Entries, Entry{ID: oldID, Revision: 7,
				Link:   &ExactLink{WorkspaceID: linkedID, LinkID: linkID, Revision: 2},
				Fields: Fields{DisplayName: "Human title", Status: "on_hold", Revision: 4}})
			return started.ID, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	rows, _, err := r.ReadDirectory(context.Background(), scope, root.ID, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	var singleID string
	for _, row := range rows {
		if row.Name == "Single" {
			singleID = row.Identity
		}
	}
	if singleID == "" {
		t.Fatal("fixture directory identity missing")
	}
	observation := Discovery{RootID: root.ID, RootRevision: root.Revision,
		StartedAt: time.Now().UTC(), EntriesSeen: scanEntryLimit, SkippedLinks: 2, SkippedOther: 4,
		PartialReason: "entries", Candidates: []Candidate{{
			RelativeFolder: "Single", FileIdentity: singleID, Format: "reaper",
			Alternates: []string{"Song.rpp"}}}}
	partial, err := r.finishScan(scope, started, observation, "partial", observation.PartialReason)
	if err != nil || partial.Status != "partial" || partial.PartialReason != "entries" ||
		partial.EntriesSeen != scanEntryLimit || partial.SkippedLinks != 2 || partial.SkippedOther != 4 {
		t.Fatalf("partial scan receipt: %+v %v", partial, err)
	}
	doc, err := r.library.Read(scope)
	if err != nil || len(doc.Entries) != 2 || doc.Entries[0].ID != oldID ||
		doc.Entries[0].Fields.DisplayName != "Human title" || doc.Entries[0].Fields.Revision != 4 ||
		doc.Entries[1].Fields.Status != "" {
		t.Fatalf("partial scan replaced user metadata: %+v %v", doc, err)
	}
}

func TestScans_CancellationAfterDurableStartRecordsNoEntries(t *testing.T) {
	r, scope, file, _, root := connectedMusicRoot(t)
	review, err := r.ReviewScan(scope, root.ID, 3)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.library = NewStore(&cancelAfterScanStart{Store: file, cancel: cancel})
	result, replay, err := r.CommitScan(ctx, scope, root.ID, review.Token, "cancelled-start")
	if err != nil || replay || result.Status != "cancelled" || result.FinishedAt == nil {
		t.Fatalf("cancelled scan: %+v replay=%v err=%v", result, replay, err)
	}
	doc, err := NewStore(file).Read(scope)
	if err != nil || len(doc.Scans) != 1 || doc.Scans[0].Status != "cancelled" || len(doc.Entries) != 0 {
		t.Fatalf("cancelled scan published candidates: %+v %v", doc, err)
	}
}

func TestScans_ProviderLossFinalizesAsFailedWithoutPublishingLateResults(t *testing.T) {
	r, scope, file, _, root := connectedMusicRoot(t)
	available := true
	r.library = NewStore(file).WithProviderEvidence(func(candidate Scope, home *workspace.Workspace) bool {
		return available && candidate == scope && home.ID == scope.HomeID
	})
	home, err := file.Get(scope.HomeID)
	if err != nil {
		t.Fatal(err)
	}
	started := Scan{ID: newID(), RootID: root.ID, RootRevision: root.Revision,
		RootDigest:       rootDigest(scope, "scan_source", root.Path, root.FileIdentity, root.Revision),
		ProviderRevision: home.GetAssistantProgramState().StateRevision,
		Status:           "running", StartedAt: time.Now().UTC()}
	_, _, err = r.library.mutate(scope, 3, operation{key: "running-before-provider-loss", action: "scan_start", digest: "reviewed"},
		func(doc *Document) (string, error) { doc.Scans = append(doc.Scans, started); return started.ID, nil })
	if err != nil {
		t.Fatal(err)
	}
	observed, err := r.Discover(context.Background(), scope, root.ID)
	if err != nil || len(observed.Candidates) != 4 {
		t.Fatalf("observed before provider loss: %+v %v", observed, err)
	}
	available = false
	finished, err := r.finishScan(scope, started, observed, "complete", "")
	if err != nil || finished.Status != "failed" || finished.PartialReason != "provider_changed_during_scan" {
		t.Fatalf("provider loss left a running scan or published success: %+v %v", finished, err)
	}
	doc, err := r.library.Read(scope)
	if err != nil || len(doc.Entries) != 0 || len(doc.Scans) != 1 || doc.Scans[0].Status != "failed" {
		t.Fatalf("provider loss published candidates: %+v %v", doc, err)
	}
	if _, err := r.ReviewScan(scope, root.ID, doc.Revision); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("providerless Home offered a new scan: %v", err)
	}
}

// This store flips installed evidence after the scan's read but immediately
// before the Home update. A provider check made only before Update would
// incorrectly publish its already-observed candidates.
type providerDropsOnUpdate struct {
	workspace.Store
	drop func()
}

func (s *providerDropsOnUpdate) Update(id string, fn func(*workspace.Workspace) error) error {
	if s.drop != nil {
		s.drop()
		s.drop = nil
	}
	return s.Store.Update(id, fn)
}

func TestScans_ProviderLossAtFinalHomeUpdateCannotPublishCandidates(t *testing.T) {
	r, scope, file, _, root := connectedMusicRoot(t)
	home, err := file.Get(scope.HomeID)
	if err != nil {
		t.Fatal(err)
	}
	started := Scan{ID: newID(), RootID: root.ID, RootRevision: root.Revision,
		RootDigest:       rootDigest(scope, "scan_source", root.Path, root.FileIdentity, root.Revision),
		ProviderRevision: home.GetAssistantProgramState().StateRevision,
		Status:           "running", StartedAt: time.Now().UTC()}
	_, _, err = r.library.mutate(scope, 3, operation{key: "running-before-final-update", action: "scan_start", digest: "reviewed"},
		func(doc *Document) (string, error) { doc.Scans = append(doc.Scans, started); return started.ID, nil })
	if err != nil {
		t.Fatal(err)
	}
	observed, err := r.Discover(context.Background(), scope, root.ID)
	if err != nil || len(observed.Candidates) != 4 {
		t.Fatalf("pre-removal observation: %+v %v", observed, err)
	}
	available := true
	dropping := &providerDropsOnUpdate{Store: file, drop: func() { available = false }}
	r.library = NewStore(dropping).WithProviderEvidence(func(candidate Scope, home *workspace.Workspace) bool {
		return available && candidate == scope && home.ID == scope.HomeID
	})
	finished, err := r.finishScan(scope, started, observed, "complete", "")
	if err != nil || finished.Status != "failed" || finished.PartialReason != "provider_changed_during_scan" {
		t.Fatalf("late provider removal published results: %+v %v", finished, err)
	}
	doc, err := r.library.Read(scope)
	if err != nil || len(doc.Entries) != 0 || len(doc.Scans) != 1 || doc.Scans[0].Status != "failed" {
		t.Fatalf("providerless finalization damaged history: %+v %v", doc, err)
	}
}

func TestScans_RevocationPreventsLateResultAssociation(t *testing.T) {
	r, scope, _, _, root := connectedMusicRoot(t)
	start := Scan{ID: newID(), RootID: root.ID, RootRevision: root.Revision,
		RootDigest: rootDigest(scope, "scan_source", root.Path, root.FileIdentity, root.Revision),
		Status:     "running", StartedAt: time.Now().UTC()}
	_, _, err := r.library.mutate(scope, 3, operation{key: "start-then-revoke", action: "scan_start", digest: "reviewed"},
		func(doc *Document) (string, error) { doc.Scans = append(doc.Scans, start); return start.ID, nil })
	if err != nil {
		t.Fatal(err)
	}
	observed, err := r.Discover(context.Background(), scope, root.ID)
	if err != nil || len(observed.Candidates) != 4 {
		t.Fatalf("fixture scan: %+v %v", observed, err)
	}
	review, err := r.ReviewRevoke(scope, root.ID, 4)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.CommitRevoke(scope, root.ID, review.Token, "revoke-during-scan"); err != nil {
		t.Fatal(err)
	}
	failed, err := r.finishScan(scope, start, observed, "complete", "")
	if err != nil || failed.Status != "failed" || failed.PartialReason != "root_changed_during_scan" {
		t.Fatalf("late result was not recorded as failed: %+v %v", failed, err)
	}
	doc, err := r.library.Read(scope)
	if err != nil || len(doc.Entries) != 0 || doc.Scans[0].Status != "failed" {
		t.Fatalf("revocation published stale scan: %+v %v", doc, err)
	}
}

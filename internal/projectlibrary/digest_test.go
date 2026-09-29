package projectlibrary

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type eventRecorder struct {
	mu     sync.Mutex
	events []workspace.Event
}

func (r *eventRecorder) Publish(event workspace.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
}

func (r *eventRecorder) all() []workspace.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]workspace.Event(nil), r.events...)
}

func alwaysProviderEvidence(Scope, *workspace.Workspace) bool { return true }

// scanRoot runs one reviewed scan exactly as the HTTP route does.
func scanRoot(t *testing.T, r *Roots, scope Scope, rootID, key string) Scan {
	t.Helper()
	doc, err := r.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	review, err := r.ReviewScan(scope, rootID, doc.Revision)
	if err != nil {
		t.Fatal(err)
	}
	result, replay, err := r.CommitScan(context.Background(), scope, rootID, review.Token, key)
	if err != nil || replay {
		t.Fatalf("scan %s: %+v replay=%v err=%v", key, result, replay, err)
	}
	return result
}

func TestDigest_CompletedScanPublishesOncePerScanWithPersistedCounts(t *testing.T) {
	_, scope, roots, file, tree, installed := activationFixture(t)
	events := &eventRecorder{}
	roots.library = NewStore(file).WithProviderEvidence(alwaysProviderEvidence).
		WithInstalledPlugins(installed).WithEventBus(events)
	before := fileDigest(t, filepath.Join(tree.single, "Song.rpp"))
	doc, err := roots.library.Read(scope)
	if err != nil || doc.Digest != nil {
		t.Fatalf("fixture already had a digest: %+v %v", doc.Digest, err)
	}
	rootID := doc.Roots[0].ID
	review, err := roots.ReviewScan(scope, rootID, doc.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if got := events.all(); len(got) != 0 {
		t.Fatalf("a scan review announced a completion: %+v", got)
	}
	first, _, err := roots.CommitScan(context.Background(), scope, rootID, review.Token, "digest-scan")
	if err != nil || first.Status != "complete" {
		t.Fatalf("scan: %+v %v", first, err)
	}
	stored, err := roots.library.Read(scope)
	if err != nil || stored.Digest == nil {
		t.Fatalf("digest not written with the scan: %+v %v", stored.Digest, err)
	}
	digest := *stored.Digest
	// Single and Alternates carry .rpp files the installed blueprint attaches;
	// the Ableton and Logic folders are cataloged but not attachable.
	if digest.ScanID != first.ID || digest.RootID != rootID || digest.Coverage != "complete" ||
		digest.Projects != 4 || digest.Activatable != 2 || digest.UnsupportedFormat != 2 ||
		digest.SetupNote != "" || digest.New+digest.Updated > digest.Projects || digest.New < 1 {
		t.Fatalf("digest counts: %+v", digest)
	}
	published := events.all()
	if len(published) != 1 {
		t.Fatalf("want one scan-completed event, got %d", len(published))
	}
	payload, ok := workspace.LibraryScanCompletedFromEvent(published[0])
	if !ok || payload.HomeID != scope.HomeID || payload.ScanID != digest.ScanID || payload.RootID != digest.RootID ||
		payload.Projects != digest.Projects || payload.New != digest.New || payload.Updated != digest.Updated ||
		payload.Unavailable != digest.Unavailable || payload.UnsupportedFormat != digest.UnsupportedFormat ||
		payload.Activatable != digest.Activatable || payload.Coverage != digest.Coverage {
		t.Fatalf("event differs from the persisted digest: %+v vs %+v", payload, digest)
	}
	encoded, err := json.Marshal(digest)
	if err != nil || len(encoded) > 512 || strings.Contains(string(encoded), tree.root) ||
		strings.Contains(string(encoded), ".rpp") || strings.Contains(string(encoded), "Single") {
		t.Fatalf("digest is unbounded or leaks a path or file name: %s", encoded)
	}
	// Replaying the commit key neither rescans nor re-announces.
	if replayed, replay, err := roots.CommitScan(context.Background(), scope, rootID, review.Token, "digest-scan"); err != nil ||
		!replay || replayed.ID != first.ID {
		t.Fatalf("replay: %+v %v %v", replayed, replay, err)
	}
	// A restart reads the Home without announcing anything.
	folderPath, err := file.GetFolderPath(scope.HomeID)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := workspace.NewFileStore(filepath.Dir(folderPath))
	if err != nil {
		t.Fatal(err)
	}
	restarted := NewRoots(NewStore(reopened).WithProviderEvidence(alwaysProviderEvidence).
		WithInstalledPlugins(installed).WithEventBus(events), nil, nil, roots.guards)
	if _, err := restarted.library.Summary(scope); err != nil {
		t.Fatal(err)
	}
	if _, replay, err := restarted.CommitScan(context.Background(), scope, rootID, review.Token, "digest-scan"); err != nil || !replay {
		t.Fatalf("restart replay: %v %v", replay, err)
	}
	if got := events.all(); len(got) != 1 {
		t.Fatalf("replay or restart re-announced a scan: %d events", len(got))
	}
	// A newer completed scan replaces the digest; nothing is new the second time.
	second := scanRoot(t, restarted, scope, rootID, "digest-rescan")
	again, err := restarted.library.Read(scope)
	if err != nil || again.Digest == nil || again.Digest.ScanID != second.ID || again.Digest.New != 0 ||
		again.Digest.Projects != 4 || again.Digest.Activatable != 2 {
		t.Fatalf("rescan digest: %+v %v", again.Digest, err)
	}
	if got := events.all(); len(got) != 2 {
		t.Fatalf("rescan must announce exactly once more: %d events", len(got))
	}
	if fileDigest(t, filepath.Join(tree.single, "Song.rpp")) != before {
		t.Fatal("digest or event read or changed a project file")
	}
}

func TestDigest_FailedCancelledAndInterruptedScansPublishNothing(t *testing.T) {
	t.Run("cancelled", func(t *testing.T) {
		r, scope, file, _, root := connectedMusicRoot(t)
		review, err := r.ReviewScan(scope, root.ID, 3)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		events := &eventRecorder{}
		r.library = NewStore(&cancelAfterScanStart{Store: file, cancel: cancel}).WithEventBus(events)
		result, _, err := r.CommitScan(ctx, scope, root.ID, review.Token, "cancelled-digest")
		if err != nil || result.Status != "cancelled" {
			t.Fatalf("cancelled scan: %+v %v", result, err)
		}
		if doc, err := NewStore(file).Read(scope); err != nil || doc.Digest != nil || len(events.all()) != 0 {
			t.Fatalf("cancelled scan left a digest or event: %+v %d %v", doc.Digest, len(events.all()), err)
		}
	})
	t.Run("failed", func(t *testing.T) {
		r, scope, file, _, root := connectedMusicRoot(t)
		available := true
		events := &eventRecorder{}
		r.library = NewStore(file).WithEventBus(events).WithProviderEvidence(func(Scope, *workspace.Workspace) bool {
			return available
		})
		started := runningScanRecord(t, r, scope, root, "running-before-failure")
		observed, err := r.Discover(context.Background(), scope, root.ID)
		if err != nil {
			t.Fatal(err)
		}
		available = false
		finished, err := r.finishScan(scope, started, observed, "complete", "")
		if err != nil || finished.Status != "failed" {
			t.Fatalf("provider loss: %+v %v", finished, err)
		}
		if doc, err := r.library.Read(scope); err != nil || doc.Digest != nil || len(events.all()) != 0 {
			t.Fatalf("failed scan left a digest or event: %+v %d %v", doc.Digest, len(events.all()), err)
		}
	})
	t.Run("interrupted", func(t *testing.T) {
		r, scope, _, _, root := connectedMusicRoot(t)
		events := &eventRecorder{}
		r.library.WithEventBus(events)
		started := runningScanRecord(t, r, scope, root, "running-before-interruption")
		observed, err := r.Discover(context.Background(), scope, root.ID)
		if err != nil {
			t.Fatal(err)
		}
		doc, err := r.library.Read(scope)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := r.library.mutate(scope, doc.Revision, operation{key: "interrupt", action: "fixture_interrupt", digest: "interrupt"},
			func(current *Document) (string, error) {
				now := time.Now().UTC()
				current.Scans[len(current.Scans)-1].Status = "interrupted"
				current.Scans[len(current.Scans)-1].FinishedAt = &now
				return started.ID, nil
			}); err != nil {
			t.Fatal(err)
		}
		if _, err := r.finishScan(scope, started, observed, "complete", ""); !errors.Is(err, ErrConflict) {
			t.Fatalf("late results finished an interrupted scan: %v", err)
		}
		if doc, err := r.library.Read(scope); err != nil || doc.Digest != nil || len(events.all()) != 0 {
			t.Fatalf("interrupted scan left a digest or event: %+v %d %v", doc.Digest, len(events.all()), err)
		}
	})
}

func runningScanRecord(t *testing.T, r *Roots, scope Scope, root Root, key string) Scan {
	t.Helper()
	home, err := r.library.workspaces.Get(scope.HomeID)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := r.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	started := Scan{ID: newID(), RootID: root.ID, RootRevision: root.Revision,
		RootDigest:       rootDigest(scope, "scan_source", root.Path, root.FileIdentity, root.Revision),
		ProviderRevision: home.GetAssistantProgramState().StateRevision,
		Status:           "running", StartedAt: time.Now().UTC()}
	if _, _, err := r.library.mutate(scope, doc.Revision, operation{key: key, action: "scan_start", digest: "reviewed"},
		func(current *Document) (string, error) {
			current.Scans = append(current.Scans, started)
			return started.ID, nil
		}); err != nil {
		t.Fatal(err)
	}
	return started
}

func TestDigest_MissingSetupEvidenceCountsProjectsOnly(t *testing.T) {
	t.Run("no installed source", func(t *testing.T) {
		r, scope, _, _, root := connectedMusicRoot(t)
		scanRoot(t, r, scope, root.ID, "unchecked-scan")
		doc, err := r.library.Read(scope)
		if err != nil || doc.Digest == nil || doc.Digest.SetupNote != setupNoteUnchecked ||
			doc.Digest.Projects != 4 || doc.Digest.New != 4 || doc.Digest.Activatable != 0 || doc.Digest.UnsupportedFormat != 0 {
			t.Fatalf("unchecked digest: %+v %v", doc.Digest, err)
		}
	})
	t.Run("project integration missing", func(t *testing.T) {
		_, scope, roots, file, _, installed := activationFixture(t)
		roots.library = NewStore(file).WithProviderEvidence(alwaysProviderEvidence).WithInstalledPlugins(installed[:1])
		doc, err := roots.library.Read(scope)
		if err != nil {
			t.Fatal(err)
		}
		scanRoot(t, roots, scope, doc.Roots[0].ID, "no-project-provider-scan")
		doc, err = roots.library.Read(scope)
		if err != nil || doc.Digest == nil || doc.Digest.SetupNote != setupNoteProjectProviderMissing ||
			doc.Digest.Activatable != 0 || doc.Digest.UnsupportedFormat != 0 || doc.Digest.Projects != 4 {
			t.Fatalf("missing project integration digest: %+v %v", doc.Digest, err)
		}
	})
}

func TestDigest_DocumentRejectsForgedDigests(t *testing.T) {
	r, scope, _, _, root := connectedMusicRoot(t)
	scanRoot(t, r, scope, root.ID, "forgery-baseline")
	doc, err := r.library.Read(scope)
	if err != nil || doc.Digest == nil || !doc.valid(scope) {
		t.Fatalf("baseline: %+v %v", doc.Digest, err)
	}
	for name, forge := range map[string]func(*LibraryDigest){
		"unknown scan":        func(d *LibraryDigest) { d.ScanID = "missing" },
		"other root":          func(d *LibraryDigest) { d.RootID = "other" },
		"coverage mismatch":   func(d *LibraryDigest) { d.Coverage = "partial" },
		"negative count":      func(d *LibraryDigest) { d.Unavailable = -1 },
		"more new than seen":  func(d *LibraryDigest) { d.New = d.Projects + 1 },
		"note with a count":   func(d *LibraryDigest) { d.Activatable = 1 },
		"unknown setup note":  func(d *LibraryDigest) { d.SetupNote = "install it for me" },
		"missing scan time":   func(d *LibraryDigest) { d.ScannedAt = time.Time{} },
		"overcounted setup":   func(d *LibraryDigest) { d.SetupNote, d.Activatable, d.UnsupportedFormat = "", d.Projects, 1 },
		"failed scan claimed": func(d *LibraryDigest) { d.Coverage = "failed" },
	} {
		forged := doc
		copied := *doc.Digest
		forge(&copied)
		forged.Digest = &copied
		if forged.valid(scope) {
			t.Fatalf("%s: forged digest accepted: %+v", name, copied)
		}
	}
}

func TestSummary_ReadsPersistedStateOnlyAndDropsDisconnectedDigest(t *testing.T) {
	file, scope := libraryHome(t)
	if summary, err := NewStore(file).Summary(scope); err != nil || summary.Initialized || summary.Digest != nil ||
		summary.ReadyProposals != 0 {
		t.Fatalf("uninitialized Home summary: %+v %v", summary, err)
	}
	r, scope, _, tree, root := connectedMusicRoot(t)
	scan := scanRoot(t, r, scope, root.ID, "summary-scan")
	before, err := r.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	summary, err := r.library.Summary(scope)
	if err != nil || !summary.Initialized || summary.Digest == nil || summary.Digest.ScanID != scan.ID ||
		summary.Revision != before.Revision || summary.ReadyProposals != 0 {
		t.Fatalf("summary: %+v %v", summary, err)
	}
	encoded, err := json.Marshal(summary)
	if err != nil || len(encoded) > 1024 || strings.Contains(string(encoded), tree.root) {
		t.Fatalf("summary is unbounded or leaks a path: %s", encoded)
	}
	if after, err := r.library.Read(scope); err != nil || after.Revision != before.Revision || len(after.Scans) != len(before.Scans) {
		t.Fatalf("summary mutated or scanned: %d -> %d, %v", before.Revision, after.Revision, err)
	}
	review, err := r.ReviewRevoke(scope, root.ID, before.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.CommitRevoke(scope, root.ID, review.Token, "summary-revoke"); err != nil {
		t.Fatal(err)
	}
	if summary, err := r.library.Summary(scope); err != nil || summary.Digest != nil {
		t.Fatalf("disconnected root still advertised its digest: %+v %v", summary, err)
	}
}

// The digest lives inside the mirrored library document, so a SQLite-primary
// Home with a folder mirror must still read as agreeing after a scan.
func TestDigest_MirroredHomeStaysInAgreementAfterScan(t *testing.T) {
	_, scope, roots, file, _, installed := activationFixture(t)
	db, err := database.Open(t.Context(), &database.Config{Path: filepath.Join(t.TempDir(), "digest.db"), WALMode: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Errorf("close SQLite digest fixture: %v", err)
		}
	}()
	primary := session.NewWorkspaceStoreAdapter(session.NewHybridStoreWithDB(db, 10))
	home, err := file.Get(scope.HomeID)
	if err != nil {
		t.Fatal(err)
	}
	if err := primary.Save(home); err != nil {
		t.Fatal(err)
	}
	events := &eventRecorder{}
	roots.library = NewStore(workspace.NewSyncStore(primary, file)).WithProviderEvidence(alwaysProviderEvidence).
		WithInstalledPlugins(installed).WithEventBus(events)
	doc, err := roots.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	scan := scanRoot(t, roots, scope, doc.Roots[0].ID, "mirrored-digest-scan")
	folderPath, err := file.GetFolderPath(scope.HomeID)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := workspace.NewFileStore(filepath.Dir(folderPath))
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := NewStore(workspace.NewSyncStore(primary, reopened)).Read(scope)
	if err != nil || fresh.Digest == nil || fresh.Digest.ScanID != scan.ID {
		t.Fatalf("mirrored digest diverged or was lost: %+v %v", fresh.Digest, err)
	}
	if len(events.all()) != 1 {
		t.Fatalf("mirrored scan announced %d times", len(events.all()))
	}
}

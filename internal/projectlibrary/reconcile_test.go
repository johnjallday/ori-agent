package projectlibrary

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/projectconnection"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func reviewedRootScan(t *testing.T, r *Roots, scope Scope, root Root, key string) Scan {
	t.Helper()
	doc, err := r.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	review, err := r.ReviewScan(scope, root.ID, doc.Revision)
	if err != nil {
		t.Fatal(err)
	}
	result, replay, err := r.CommitScan(context.Background(), scope, root.ID, review.Token, key)
	if err != nil || replay {
		t.Fatalf("reviewed scan: %+v %v %v", result, replay, err)
	}
	return result
}

func observationAt(doc Document, rootID, relative string) (Entry, Observation, bool) {
	for _, entry := range doc.Entries {
		for _, observation := range entry.Observations {
			if observation.RootID == rootID && observation.RelativeFolder == relative &&
				(observation.Availability == "available" || observation.Availability == "ambiguous") {
				return entry, observation, true
			}
		}
	}
	return Entry{}, Observation{}, false
}

func TestReconcile_ReplacementAndRenameLeaveEditedIdentityHistorical(t *testing.T) {
	r, scope, _, tree, root := connectedMusicRoot(t)
	if scan := reviewedRootScan(t, r, scope, root, "initial"); scan.Status != "complete" {
		t.Fatal(scan)
	}
	before, err := r.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	oldEntry, oldObservation, ok := observationAt(before, root.ID, "Single")
	if !ok {
		t.Fatal("initial identity absent")
	}
	_, _, err = r.library.mutate(scope, before.Revision,
		operation{key: "edit-test", action: "edit_fields", digest: "human"},
		func(current *Document) (string, error) {
			for i := range current.Entries {
				if current.Entries[i].ID == oldEntry.ID {
					current.Entries[i].Fields = Fields{DisplayName: "Human title", Status: "on_hold",
						Revision: 1, Source: "human", UpdatedAt: time.Now().UTC()}
					current.Entries[i].Revision++
					return oldEntry.ID, nil
				}
			}
			return "", ErrConflict
		})
	if err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(tree.root, "MovedSong")
	if err := os.Rename(tree.single, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(tree.single, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree.single, "New.rpp"), []byte("different project"), 0o600); err != nil {
		t.Fatal(err)
	}
	if scan := reviewedRootScan(t, r, scope, root, "after-replacement"); scan.Status != "complete" {
		t.Fatal(scan)
	}
	after, err := r.library.Read(scope)
	if err != nil || len(after.Entries) != len(before.Entries)+2 {
		t.Fatalf("replacement and rename lost history or forged a merge: %+v %v", after, err)
	}
	oldFound := false
	for _, entry := range after.Entries {
		if entry.ID != oldEntry.ID {
			continue
		}
		oldFound = true
		if entry.Fields.DisplayName != "Human title" || entry.Fields.Status != "on_hold" ||
			len(entry.Observations) != 1 || entry.Observations[0].FileIdentity != oldObservation.FileIdentity ||
			entry.Observations[0].Availability != "unavailable" || entry.Observations[0].LastCheckedAt.IsZero() {
			t.Fatalf("user metadata was silently moved to replacement: %+v", entry)
		}
	}
	if !oldFound {
		t.Fatal("historical entry was dropped")
	}
	for _, relative := range []string{"Single", "MovedSong"} {
		entry, _, ok := observationAt(after, root.ID, relative)
		if !ok || entry.ID == oldEntry.ID || entry.Fields.Revision != 0 {
			t.Fatalf("new location was automatically given historical user fields: %+v", entry)
		}
	}
}

func TestReconcile_PartialResultCannotMarkUnvisitedFoldersMissing(t *testing.T) {
	r, scope, _, tree, root := connectedMusicRoot(t)
	if scan := reviewedRootScan(t, r, scope, root, "before-partial"); scan.Status != "complete" {
		t.Fatal(scan)
	}
	before, err := r.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	original, _, ok := observationAt(before, root.ID, "Single")
	if !ok {
		t.Fatal("original missing")
	}
	if err := os.Remove(filepath.Join(tree.single, "Song.rpp")); err != nil {
		t.Fatal(err)
	}
	providerRevision, err := r.currentProviderRevision(scope)
	if err != nil {
		t.Fatal(err)
	}
	started := Scan{ID: newID(), RootID: root.ID, RootRevision: root.Revision,
		RootDigest:       rootDigest(scope, "scan_source", root.Path, root.FileIdentity, root.Revision),
		ProviderRevision: providerRevision, Status: "running", StartedAt: time.Now().UTC()}
	_, _, err = r.library.mutate(scope, before.Revision,
		operation{key: "partial-start", action: "scan_start", digest: "explicit"},
		func(doc *Document) (string, error) {
			doc.Scans = append(doc.Scans, started)
			return started.ID, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	observed := Discovery{RootID: root.ID, RootRevision: root.Revision,
		StartedAt: time.Now().UTC(), PartialReason: "entry_limit", EntriesSeen: scanEntryLimit}
	partial, err := r.finishScan(scope, started, observed, "partial", observed.PartialReason)
	if err != nil || partial.Status != "partial" {
		t.Fatalf("partial receipt: %+v %v", partial, err)
	}
	incomplete, err := r.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	retained, _, stillAvailable := observationAt(incomplete, root.ID, "Single")
	if !stillAvailable || retained.ID != original.ID || len(incomplete.Entries) != len(before.Entries) {
		t.Fatalf("partial coverage erased unvisited evidence: %+v", incomplete.Entries)
	}
	if scan := reviewedRootScan(t, r, scope, root, "full-after-partial"); scan.Status != "complete" {
		t.Fatal(scan)
	}
	full, err := r.library.Read(scope)
	if err != nil || len(full.Entries) != len(before.Entries) {
		t.Fatalf("complete rescan deleted historical record: %+v %v", full, err)
	}
	for _, entry := range full.Entries {
		if entry.ID == original.ID && entry.Observations[0].Availability != "unavailable" {
			t.Fatalf("complete coverage failed to mark absent marker: %+v", entry)
		}
	}
}

func TestReconcile_OverlappingReviewedRootsShareOnlyVerifiedPhysicalFolder(t *testing.T) {
	r, scope, _, tree, outer := connectedMusicRoot(t)
	if scan := reviewedRootScan(t, r, scope, outer, "outer-scan"); scan.Status != "complete" {
		t.Fatal(scan)
	}
	before, err := r.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	original, _, ok := observationAt(before, outer.ID, "Single")
	if !ok {
		t.Fatal("first observation missing")
	}
	picker := r.picker.(*testRootPicker)
	picker.path, err = filepath.EvalSymlinks(tree.single)
	if err != nil {
		t.Fatal(err)
	}
	token, err := r.Pick(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	review, err := r.Review(scope, token, before.Revision)
	if err != nil {
		t.Fatal(err)
	}
	inner, replay, err := r.Commit(scope, review.Token, "nested-root")
	if err != nil || replay {
		t.Fatalf("overlapping grant: %+v %v %v", inner, replay, err)
	}
	if scan := reviewedRootScan(t, r, scope, inner, "inner-scan"); scan.Status != "complete" {
		t.Fatal(scan)
	}
	after, err := r.library.Read(scope)
	if err != nil || len(after.Entries) != len(before.Entries) {
		t.Fatalf("overlapping path created new project: %+v %v", after, err)
	}
	shared, _, ok := observationAt(after, inner.ID, "")
	if !ok || shared.ID != original.ID || len(shared.Observations) != 2 {
		t.Fatalf("verified physical overlap did not reuse entry ID: %+v", shared)
	}
}

func TestReconcile_ExactLinkedProjectDirectoryReferenceKeepsPortfolioFields(t *testing.T) {
	r, scope, file, tree, root := connectedMusicRoot(t)
	key := workspace.AssistantProgramKey{OwnerUserID: scope.OwnerUserID,
		PluginID: scope.ProviderID, ProgramID: scope.ProgramID}
	child := &workspace.Workspace{ID: "already-attached", Name: "Saved title", OwnerUserID: scope.OwnerUserID,
		Status: workspace.StatusActive, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	link := &workspace.AssistantProjectLink{ID: workspace.AssistantProjectLinkID(scope.HomeID, child.ID),
		StationWorkspaceID: scope.HomeID, Key: key, StateRevision: 1}
	child.SetAssistantProjectLink(link)
	if err := projectconnection.RecordAttachedProject(child, "Song", tree.single, "Song.rpp", "approved-directory"); err != nil {
		t.Fatal(err)
	}
	if err := file.Save(child); err != nil {
		t.Fatal(err)
	}
	if err := file.Update(scope.HomeID, func(home *workspace.Workspace) error {
		state := home.GetAssistantProgramState()
		state.LinkedProjectIDs = append(state.LinkedProjectIDs, child.ID)
		home.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	doc, err := r.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	originalID := newID()
	_, _, err = r.library.mutate(scope, doc.Revision,
		operation{key: "existing-link", action: "edit_fields", digest: "saved"},
		func(current *Document) (string, error) {
			current.Entries = append(current.Entries, Entry{ID: originalID, Revision: 1,
				Link:   &ExactLink{WorkspaceID: child.ID, LinkID: link.ID, Revision: link.StateRevision},
				Fields: Fields{Status: "active", DisplayName: "Confirmed by user", Revision: 1}})
			return originalID, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if scan := reviewedRootScan(t, r, scope, root, "linked-project-scan"); scan.Status != "complete" {
		t.Fatal(scan)
	}
	after, err := r.library.Read(scope)
	if err != nil || len(after.Entries) != 4 {
		t.Fatalf("exact linked project duplicated: %+v %v", after, err)
	}
	entry, _, ok := observationAt(after, root.ID, "Single")
	if !ok || entry.ID != originalID || entry.Fields.DisplayName != "Confirmed by user" ||
		entry.Link == nil || entry.Link.WorkspaceID != child.ID {
		t.Fatalf("verified link was not associated without changing fields: %+v", entry)
	}
}

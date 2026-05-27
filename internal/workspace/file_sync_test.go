package workspace

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// addOwnedAttachmentWithFile writes content to relPath under the workspace files
// dir and registers an owned attachment for it with a fresh checksum, mimicking
// an upload through the normal path.
func addOwnedAttachmentWithFile(t *testing.T, store *FileStore, ws *Workspace, id, relPath string, content []byte) {
	t.Helper()
	abs := filepath.Join(store.GetFilesPath(ws.ID), filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(abs, content, 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	sum, modTime, size, err := hashFileSHA256(abs)
	if err != nil {
		t.Fatalf("hashFileSHA256: %v", err)
	}
	att := Attachment{
		ID:          id,
		WorkspaceID: ws.ID,
		Title:       relPath,
		Type:        AttachmentTypeDoc,
		File: &AttachmentFileMeta{
			Name:            filepath.Base(relPath),
			Size:            size,
			RelativePath:    relPath,
			URL:             workspaceFileURL(ws.ID, relPath),
			Checksum:        sum,
			ChecksumModTime: modTime,
		},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := ws.AddAttachment(att); err != nil {
		t.Fatalf("AddAttachment: %v", err)
	}
}

func attachmentByID(ws *Workspace, id string) *Attachment {
	for i := range ws.Attachments {
		if ws.Attachments[i].ID == id {
			return &ws.Attachments[i]
		}
	}
	return nil
}

func TestReconcileWorkspaceFilesRebindsRenamedFile(t *testing.T) {
	store, ws, _ := newFolderHandlerTest(t, "ws-sync-rename", "Sync Rename")
	filesPath := store.GetFilesPath(ws.ID)
	addOwnedAttachmentWithFile(t, store, ws, "att-1", "old.txt", []byte("hello world"))

	if err := os.Rename(filepath.Join(filesPath, "old.txt"), filepath.Join(filesPath, "new.txt")); err != nil {
		t.Fatalf("rename: %v", err)
	}

	changed, events, err := reconcileWorkspaceFiles(ws, filesPath)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if !changed {
		t.Fatal("expected reconcile to report a change")
	}
	if len(events) != 1 || events[0].oldPath != "old.txt" || events[0].newPath != "new.txt" {
		t.Fatalf("unexpected events: %#v", events)
	}

	att := attachmentByID(ws, "att-1")
	if att.File.RelativePath != "new.txt" {
		t.Fatalf("expected relative path new.txt, got %q", att.File.RelativePath)
	}
	if att.File.Name != "new.txt" {
		t.Fatalf("expected name new.txt, got %q", att.File.Name)
	}
	if att.File.Status != "" {
		t.Fatalf("expected cleared status, got %q", att.File.Status)
	}
	if att.File.URL != workspaceFileURL(ws.ID, "new.txt") {
		t.Fatalf("expected url rebind, got %q", att.File.URL)
	}
}

func TestReconcileWorkspaceFilesRebindsMovedFileIntoFolder(t *testing.T) {
	store, ws, _ := newFolderHandlerTest(t, "ws-sync-move", "Sync Move")
	filesPath := store.GetFilesPath(ws.ID)
	addOwnedAttachmentWithFile(t, store, ws, "att-1", "report.pdf", []byte("pdf-bytes"))

	if err := os.MkdirAll(filepath.Join(filesPath, "archive"), 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.Rename(filepath.Join(filesPath, "report.pdf"), filepath.Join(filesPath, "archive", "report.pdf")); err != nil {
		t.Fatalf("rename: %v", err)
	}

	changed, events, err := reconcileWorkspaceFiles(ws, filesPath)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if !changed || len(events) != 1 || events[0].newPath != "archive/report.pdf" {
		t.Fatalf("unexpected reconcile result: changed=%v events=%#v", changed, events)
	}
	if got := attachmentByID(ws, "att-1").File.RelativePath; got != "archive/report.pdf" {
		t.Fatalf("expected archive/report.pdf, got %q", got)
	}
}

func TestReconcileWorkspaceFilesFlagsDeletedFileAsMissing(t *testing.T) {
	store, ws, _ := newFolderHandlerTest(t, "ws-sync-delete", "Sync Delete")
	filesPath := store.GetFilesPath(ws.ID)
	addOwnedAttachmentWithFile(t, store, ws, "att-1", "doomed.txt", []byte("bye"))

	if err := os.Remove(filepath.Join(filesPath, "doomed.txt")); err != nil {
		t.Fatalf("remove: %v", err)
	}

	changed, events, err := reconcileWorkspaceFiles(ws, filesPath)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if !changed {
		t.Fatal("expected a change (status flagged)")
	}
	if len(events) != 0 {
		t.Fatalf("expected no rebind events, got %#v", events)
	}
	att := attachmentByID(ws, "att-1")
	if att.File.Status != string(AttachmentFileStatusMissing) {
		t.Fatalf("expected missing status, got %q", att.File.Status)
	}
	if att.DeletedAt != nil {
		t.Fatal("missing file must not be soft-deleted")
	}
	if att.File.RelativePath != "doomed.txt" {
		t.Fatalf("missing attachment should keep its path, got %q", att.File.RelativePath)
	}
}

func TestReconcileWorkspaceFilesAmbiguousDuplicatesStayMissing(t *testing.T) {
	store, ws, _ := newFolderHandlerTest(t, "ws-sync-dup", "Sync Dup")
	filesPath := store.GetFilesPath(ws.ID)
	addOwnedAttachmentWithFile(t, store, ws, "att-1", "same.txt", []byte("identical"))

	// Remove the original and leave TWO identical-content orphans: the single
	// missing attachment now has two equally-valid matches, so it must not be
	// auto-rebound to either.
	if err := os.Remove(filepath.Join(filesPath, "same.txt")); err != nil {
		t.Fatalf("remove: %v", err)
	}
	for _, name := range []string{"copy-a.txt", "copy-b.txt"} {
		if err := os.WriteFile(filepath.Join(filesPath, name), []byte("identical"), 0644); err != nil {
			t.Fatalf("WriteFile %s: %v", name, err)
		}
	}

	_, events, err := reconcileWorkspaceFiles(ws, filesPath)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("expected no rebind on ambiguity, got %#v", events)
	}
	if attachmentByID(ws, "att-1").File.Status != string(AttachmentFileStatusMissing) {
		t.Fatal("expected ambiguous match to remain missing")
	}
}

func TestReconcileWorkspaceFilesDoesNotRebindToTrashedAttachmentFile(t *testing.T) {
	store, ws, _ := newFolderHandlerTest(t, "ws-sync-trash-claimed", "Sync Trash Claimed")
	filesPath := store.GetFilesPath(ws.ID)
	addOwnedAttachmentWithFile(t, store, ws, "att-active", "missing.txt", []byte("same bytes"))
	addOwnedAttachmentWithFile(t, store, ws, "att-trashed", "trashed.txt", []byte("same bytes"))
	deletedAt := time.Now()
	attachmentByID(ws, "att-trashed").DeletedAt = &deletedAt

	if err := os.Remove(filepath.Join(filesPath, "missing.txt")); err != nil {
		t.Fatalf("remove active file: %v", err)
	}

	changed, events, err := reconcileWorkspaceFiles(ws, filesPath)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if !changed {
		t.Fatal("expected active attachment to be marked missing")
	}
	if len(events) != 0 {
		t.Fatalf("expected no rebind to trashed attachment file, got %#v", events)
	}
	active := attachmentByID(ws, "att-active")
	if active.File.RelativePath != "missing.txt" {
		t.Fatalf("expected active attachment to keep missing.txt, got %q", active.File.RelativePath)
	}
	if active.File.Status != string(AttachmentFileStatusMissing) {
		t.Fatalf("expected active attachment missing status, got %q", active.File.Status)
	}
	if trashed := attachmentByID(ws, "att-trashed"); trashed.File.RelativePath != "trashed.txt" || trashed.DeletedAt == nil {
		t.Fatalf("expected trashed attachment to keep its file claim, got path=%q deleted=%v", trashed.File.RelativePath, trashed.DeletedAt != nil)
	}
}

func TestReconcileWorkspaceFilesBackfillsLegacyChecksumThenRebinds(t *testing.T) {
	store, ws, _ := newFolderHandlerTest(t, "ws-sync-legacy", "Sync Legacy")
	filesPath := store.GetFilesPath(ws.ID)
	addOwnedAttachmentWithFile(t, store, ws, "att-1", "legacy.txt", []byte("legacy content"))

	// Simulate a pre-checksum attachment: clear the cached fingerprint.
	att := attachmentByID(ws, "att-1")
	att.File.Checksum = ""
	att.File.ChecksumModTime = time.Time{}

	// First reconcile while the file is in place backfills the checksum.
	changed, _, err := reconcileWorkspaceFiles(ws, filesPath)
	if err != nil {
		t.Fatalf("reconcile (backfill): %v", err)
	}
	if !changed || attachmentByID(ws, "att-1").File.Checksum == "" {
		t.Fatalf("expected checksum backfill, changed=%v checksum=%q", changed, attachmentByID(ws, "att-1").File.Checksum)
	}

	// A later rename is then resolvable because the checksum is known.
	if err := os.Rename(filepath.Join(filesPath, "legacy.txt"), filepath.Join(filesPath, "renamed.txt")); err != nil {
		t.Fatalf("rename: %v", err)
	}
	_, events, err := reconcileWorkspaceFiles(ws, filesPath)
	if err != nil {
		t.Fatalf("reconcile (rebind): %v", err)
	}
	if len(events) != 1 || events[0].newPath != "renamed.txt" {
		t.Fatalf("expected rebind to renamed.txt, got %#v", events)
	}
}

func TestReconcileWorkspaceFilesNoChangeWhenInPlace(t *testing.T) {
	store, ws, _ := newFolderHandlerTest(t, "ws-sync-stable", "Sync Stable")
	filesPath := store.GetFilesPath(ws.ID)
	addOwnedAttachmentWithFile(t, store, ws, "att-1", "stable.txt", []byte("unchanged"))

	changed, events, err := reconcileWorkspaceFiles(ws, filesPath)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if changed || len(events) != 0 {
		t.Fatalf("expected no-op for an unchanged file, changed=%v events=%#v", changed, events)
	}
}

func TestReconcileWorkspaceFilesBackfillBudgetIsAmortized(t *testing.T) {
	store, ws, _ := newFolderHandlerTest(t, "ws-sync-budget", "Sync Budget")
	filesPath := store.GetFilesPath(ws.ID)
	addOwnedAttachmentWithFile(t, store, ws, "att-1", "one.txt", []byte("aaaa"))
	addOwnedAttachmentWithFile(t, store, ws, "att-2", "two.txt", []byte("bbbb"))

	// Simulate pre-checksum (legacy) attachments that still sit in place.
	for _, id := range []string{"att-1", "att-2"} {
		att := attachmentByID(ws, id)
		att.File.Checksum = ""
		att.File.ChecksumModTime = time.Time{}
	}

	// A tiny budget lets only the first file hash this pass.
	orig := reconcileBackfillByteBudget
	reconcileBackfillByteBudget = 1
	defer func() { reconcileBackfillByteBudget = orig }()

	if _, _, err := reconcileWorkspaceFiles(ws, filesPath); err != nil {
		t.Fatalf("reconcile pass 1: %v", err)
	}
	hashed := 0
	for _, id := range []string{"att-1", "att-2"} {
		if attachmentByID(ws, id).File.Checksum != "" {
			hashed++
		}
	}
	if hashed != 1 {
		t.Fatalf("expected exactly 1 file hashed under a tiny budget, got %d", hashed)
	}

	// Remaining legacy files are backfilled on a later pass.
	if _, _, err := reconcileWorkspaceFiles(ws, filesPath); err != nil {
		t.Fatalf("reconcile pass 2: %v", err)
	}
	for _, id := range []string{"att-1", "att-2"} {
		if attachmentByID(ws, id).File.Checksum == "" {
			t.Fatalf("expected %s checksum backfilled after a second pass", id)
		}
	}
}

func TestGetWorkspaceFilesTreeReconcilesRename(t *testing.T) {
	store, ws, handler := newFolderHandlerTest(t, "ws-tree-rename", "Tree Rename")
	filesPath := store.GetFilesPath(ws.ID)
	addOwnedAttachmentWithFile(t, store, ws, "att-1", "before.txt", []byte("tree content"))
	if err := store.Save(ws); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if err := os.Rename(filepath.Join(filesPath, "before.txt"), filepath.Join(filesPath, "after.txt")); err != nil {
		t.Fatalf("rename: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/"+ws.ID+"/files/tree", nil)
	rr := httptest.NewRecorder()
	handler.GetWorkspaceFilesTree(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	files := decodeFileTreeResponse(t, rr.Body.Bytes())
	if findFileInfo(files, "before.txt") != nil {
		t.Fatal("stale path before.txt should no longer appear")
	}
	after := findFileInfo(files, "after.txt")
	if after == nil {
		t.Fatalf("expected after.txt in tree, got %#v", files)
	}
	if after.AttachmentID != "att-1" {
		t.Fatalf("expected after.txt bound to att-1, got %q", after.AttachmentID)
	}
	if after.Status == string(AttachmentFileStatusMissing) {
		t.Fatal("rebound file should not be flagged missing")
	}

	stored, _ := store.Get(ws.ID)
	if got := attachmentByID(stored, "att-1").File.RelativePath; got != "after.txt" {
		t.Fatalf("expected persisted rebind to after.txt, got %q", got)
	}
}

func TestLocateAttachmentFileAdoptsOrphan(t *testing.T) {
	store, ws, handler := newFolderHandlerTest(t, "ws-locate", "Locate")
	filesPath := store.GetFilesPath(ws.ID)

	// Missing attachment (no file on disk) + an orphan to point it at.
	if err := ws.AddAttachment(Attachment{
		ID:          "att-1",
		WorkspaceID: ws.ID,
		Title:       "Gone",
		Type:        AttachmentTypeDoc,
		File: &AttachmentFileMeta{
			Name:         "gone.txt",
			RelativePath: "gone.txt",
			URL:          workspaceFileURL(ws.ID, "gone.txt"),
			Status:       string(AttachmentFileStatusMissing),
		},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("AddAttachment: %v", err)
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := os.WriteFile(filepath.Join(filesPath, "found.txt"), []byte("recovered"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	req := httptest.NewRequest(http.MethodPatch, "/api/workspaces/"+ws.ID+"/attachments/att-1/locate", bytes.NewBufferString(`{"relative_path":"found.txt"}`))
	rr := httptest.NewRecorder()
	handler.LocateAttachmentFile(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	stored, _ := store.Get(ws.ID)
	att := attachmentByID(stored, "att-1")
	if att.File.RelativePath != "found.txt" {
		t.Fatalf("expected relink to found.txt, got %q", att.File.RelativePath)
	}
	if att.File.Checksum == "" {
		t.Fatal("expected checksum to be computed on locate")
	}
	if att.File.Status != "" {
		t.Fatalf("expected cleared status, got %q", att.File.Status)
	}
}

func TestLocateAttachmentFileRejectsDoubleClaim(t *testing.T) {
	store, ws, handler := newFolderHandlerTest(t, "ws-locate-dup", "Locate Dup")
	addOwnedAttachmentWithFile(t, store, ws, "att-owner", "owned.txt", []byte("owned"))
	if err := ws.AddAttachment(Attachment{
		ID:          "att-missing",
		WorkspaceID: ws.ID,
		Title:       "Missing",
		Type:        AttachmentTypeDoc,
		File: &AttachmentFileMeta{
			Name:         "gone.txt",
			RelativePath: "gone.txt",
			Status:       string(AttachmentFileStatusMissing),
		},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("AddAttachment: %v", err)
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("Save: %v", err)
	}

	req := httptest.NewRequest(http.MethodPatch, "/api/workspaces/"+ws.ID+"/attachments/att-missing/locate", bytes.NewBufferString(`{"relative_path":"owned.txt"}`))
	rr := httptest.NewRecorder()
	handler.LocateAttachmentFile(rr, req)
	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409 on double-claim, got %d: %s", rr.Code, rr.Body.String())
	}
}

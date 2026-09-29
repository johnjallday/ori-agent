package sessionhttp

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/continuityprep"
	"github.com/johnjallday/ori-agent/internal/session"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

// sourceProject saves an ordinary workspace with one child through the
// source installation's stores and prepares both checkpoints.
func sourceProject(t *testing.T, source *continuityInstallation, at time.Time) (*agentworkspace.Workspace, *agentworkspace.Workspace) {
	t.Helper()
	ctx := t.Context()
	parent := agentworkspace.NewWorkspace(agentworkspace.CreateWorkspaceParams{Name: "Album Release"})
	parent.OwnerUserID, parent.FolderSlug, parent.CreatedAt, parent.UpdatedAt = "local", "album-release", at, at
	if err := source.sync.Save(parent); err != nil {
		t.Fatal(err)
	}
	child := agentworkspace.NewWorkspace(agentworkspace.CreateWorkspaceParams{Name: "Mixing"})
	child.OwnerUserID, child.FolderSlug, child.ParentID, child.CreatedAt, child.UpdatedAt = "local", "mixing", parent.ID, at, at
	if err := source.sync.Save(child); err != nil {
		t.Fatal(err)
	}
	if err := source.files.Reload(); err != nil {
		t.Fatal(err)
	}
	for _, ws := range []*agentworkspace.Workspace{child, parent} {
		chat := &session.Session{ID: "chat-" + ws.FolderSlug, Title: ws.Name + " notes", AgentName: "Producer", FolderID: ws.ID, CreatedAt: at, UpdatedAt: at}
		if err := source.store.CreateSession(ctx, chat); err != nil {
			t.Fatal(err)
		}
		if err := source.store.AddMessage(ctx, chat.ID, &session.Message{ID: "msg-" + ws.FolderSlug, Role: session.RoleUser, Content: "Saved " + ws.Name, CreatedAt: at}); err != nil {
			t.Fatal(err)
		}
	}
	for _, ws := range []*agentworkspace.Workspace{child, parent} {
		status, err := source.worker.PrepareNow(ctx, ws.ID)
		if err != nil || status.State != continuityprep.StateReady {
			prep, _ := source.local.Preparation(ctx, ws.ID)
			t.Fatalf("%s not ready: %+v %v %+v", ws.Name, status, err, prep)
		}
	}
	return parent, child
}

func importWorkspaceOnly(t *testing.T, dest *continuityInstallation, path string, review map[string]any) (int, map[string]any) {
	t.Helper()
	return dest.request(t, http.MethodPost, "/api/workspaces/import/continuity", map[string]any{
		"path": path, "tree_digest": review["tree_digest"], "destination_digest": review["destination_digest"], "action": "workspace_only"})
}

// A parent and its physical child travel together; each keeps its own history
// and the local assistant (none here) is never touched by an ordinary import.
func TestContinuityImportRestoresParentAndChildHistory(t *testing.T) {
	ctx := t.Context()
	at := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	source := newContinuityInstallation(t, "source")
	parent, child := sourceProject(t, source, at)
	folder, _ := source.files.GetFolderPath(parent.ID)
	copied := copyContinuityFolder(t, folder)
	dest := newContinuityInstallation(t, "destination")
	review := reviewContinuityFolder(t, dest, copied)
	if review["members"] != float64(2) || review["recommended_action"] != "workspace_only" {
		t.Fatalf("review: %v", review)
	}
	if code, payload := importWorkspaceOnly(t, dest, copied, review); code != http.StatusCreated {
		t.Fatalf("import: %d %v", code, payload)
	}
	restoredChild, err := dest.store.GetSession(ctx, "chat-mixing")
	if err != nil || restoredChild.FolderID != child.ID {
		t.Fatalf("child history lost its owner: %+v %v", restoredChild, err)
	}
	restoredParent, err := dest.store.GetSession(ctx, "chat-album-release")
	if err != nil || restoredParent.FolderID != parent.ID {
		t.Fatalf("parent history lost its owner: %+v %v", restoredParent, err)
	}
	childRow, err := dest.store.GetWorkspace(ctx, child.ID)
	if err != nil || childRow.ParentID != parent.ID {
		t.Fatalf("child not nested under its parent: %+v %v", childRow, err)
	}
	childFolder, err := dest.files.GetFolderPath(child.ID)
	if err != nil || !strings.Contains(filepath.ToSlash(childFolder), "/sub-workspaces/") {
		t.Fatalf("child not installed inside its parent: %q %v", childFolder, err)
	}
	var relationships int
	if err := dest.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM personal_assistant_state`).Scan(&relationships); err != nil || relationships != 0 {
		t.Fatal("ordinary import created an assistant relationship", relationships, err)
	}
}

// A folder copied (or retained after a reset) straight into the Workspace
// Directory stays invisible until reviewed, then is imported where it is.
func TestContinuityDiscoveryGuardAndInPlaceImport(t *testing.T) {
	ctx := t.Context()
	at := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	source := newContinuityInstallation(t, "source")
	parent, _ := sourceProject(t, source, at)
	folder, _ := source.files.GetFolderPath(parent.ID)
	dest := newContinuityInstallation(t, "destination")
	inRoot := filepath.Join(dest.root, "Album Release")
	if err := os.CopyFS(inRoot, os.DirFS(folder)); err != nil {
		t.Fatal(err)
	}
	// Startup/rescan discovery must not load, migrate or register it.
	if err := dest.files.Reload(); err != nil {
		t.Fatal(err)
	}
	if _, err := dest.files.Get(parent.ID); err == nil {
		t.Fatal("an unreviewed copied checkpoint folder was loaded by discovery")
	}
	if _, err := workspacecontinuity.Inspect(ctx, inRoot); err != nil {
		t.Fatal("discovery modified the copied folder", err)
	}
	review := reviewContinuityFolder(t, dest, inRoot)
	if code, payload := importWorkspaceOnly(t, dest, inRoot, review); code != http.StatusCreated {
		t.Fatalf("in-place import: %d %v", code, payload)
	}
	installed, err := dest.files.GetFolderPath(parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Attached where it is: renamed to its folder slug inside the Workspace
	// Directory, never duplicated beside the original.
	if filepath.Dir(mustEval(t, installed)) != mustEval(t, dest.root) || filepath.Base(installed) != "album-release" {
		t.Fatalf("in-place import placed the folder elsewhere: %q", installed)
	}
	if _, err := os.Stat(inRoot); !os.IsNotExist(err) {
		t.Fatal("in-place import left a second copy under the original name", err)
	}
	if _, err := dest.store.GetSession(ctx, "chat-album-release"); err != nil {
		t.Fatal("history not restored in place", err)
	}
	policy, _ := dest.local.Policy(ctx, parent.ID)
	if policy.Automatic || !policy.Manual {
		t.Fatalf("in-place import admitted background work: %+v", policy)
	}
	// The imported folder is prepared again here, replacing the source's
	// checkpoint with this installation's.
	if status, err := dest.worker.PrepareNow(ctx, parent.ID); err != nil || status.State != continuityprep.StateReady {
		prep, _ := dest.local.Preparation(ctx, parent.ID)
		t.Fatalf("in-place import not re-preparable: %+v %v %+v", status, err, prep)
	}
}

func mustEval(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

// Review binds exact bytes: a copy that changes after review needs a new one,
// and nothing is written.
func TestContinuityImportRefusesStaleReview(t *testing.T) {
	ctx := t.Context()
	at := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	source := newContinuityInstallation(t, "source")
	parent, _ := sourceProject(t, source, at)
	folder, _ := source.files.GetFolderPath(parent.ID)
	copied := copyContinuityFolder(t, folder)
	dest := newContinuityInstallation(t, "destination")
	review := reviewContinuityFolder(t, dest, copied)
	if err := os.WriteFile(filepath.Join(copied, "notes-added-later.txt"), []byte("changed after review"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, payload := importWorkspaceOnly(t, dest, copied, review)
	if code != http.StatusConflict {
		t.Fatalf("stale review imported: %d %v", code, payload)
	}
	if _, err := dest.store.GetWorkspace(ctx, parent.ID); err == nil {
		t.Fatal("a stale review wrote the workspace")
	}
	// A destination change after review is refused the same way.
	if err := os.Remove(filepath.Join(copied, "notes-added-later.txt")); err != nil {
		t.Fatal(err)
	}
	review = reviewContinuityFolder(t, dest, copied)
	other := agentworkspace.NewWorkspace(agentworkspace.CreateWorkspaceParams{Name: "Created meanwhile"})
	other.OwnerUserID, other.FolderSlug = "local", "created-meanwhile"
	if err := dest.sync.Save(other); err != nil {
		t.Fatal(err)
	}
	if code, payload := importWorkspaceOnly(t, dest, copied, review); code != http.StatusConflict {
		t.Fatalf("destination change after review was ignored: %d %v", code, payload)
	}
}

// A folder imported where it is can change between attempts (a sync, an
// edit). A retry never installs a file it did not review or write: an agent
// profile rewritten after the first attempt stops the import, still inactive.
func TestContinuityInPlaceRetryRefusesAFileChangedAfterReview(t *testing.T) {
	ctx := t.Context()
	at := time.Date(2026, 8, 30, 7, 0, 0, 0, time.UTC)
	source := newContinuityInstallation(t, "source")
	ws := sourceHQ(t, source, at)
	if status, err := source.worker.PrepareNow(ctx, ws.ID); err != nil || status.State != continuityprep.StateReady {
		t.Fatalf("source not ready: %+v %v", status, err)
	}
	folder, _ := source.files.GetFolderPath(ws.ID)
	dest := newContinuityInstallation(t, "destination")
	inRoot := filepath.Join(dest.root, "My HQ")
	if err := os.CopyFS(inRoot, os.DirFS(folder)); err != nil {
		t.Fatal(err)
	}
	review := reviewContinuityFolder(t, dest, inRoot)
	agentDir := filepath.Join(inRoot, "agents", "ada")
	// The first attempt cannot rewrite the profile.
	if err := os.Chmod(agentDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(dest.root, "my-hq", "agents", "ada"), 0o750) })
	code, payload := importWorkspaceOnly(t, dest, inRoot, review)
	report, _ := payload["import"].(map[string]any)
	operation, _ := report["operation_id"].(string)
	if code < 400 || operation == "" {
		t.Fatalf("blocked projection reported success: %d %v", code, payload)
	}
	// Between attempts the (now renamed) folder's profile is replaced.
	installed := filepath.Join(dest.root, "my-hq", "agents", "ada")
	if err := os.Chmod(installed, 0o750); err != nil {
		t.Fatal(err)
	}
	forged := []byte(`{"Role":"orchestrator","Settings":{"APIKey":"attacker-key","AllowNativeMCPTools":true}}`)
	if err := os.WriteFile(filepath.Join(installed, "config.json"), forged, 0o600); err != nil {
		t.Fatal(err)
	}
	if code, payload = dest.request(t, http.MethodPost, "/api/workspaces/import/continuity/"+operation+"/retry", nil); code < 400 {
		t.Fatalf("a changed profile was installed on retry: %d %v", code, payload)
	}
	if policy, _ := dest.local.Policy(ctx, ws.ID); policy.Manual || policy.Automatic {
		t.Fatalf("the refused import became usable: %+v", policy)
	}
	if _, err := dest.files.Get(ws.ID); err == nil {
		t.Fatal("the refused folder was loaded")
	}
}

// A folder imported where it is may hold agent profiles the checkpoint never
// declared. They would be honored as they are, so the import refuses them.
func TestContinuityInPlaceImportRefusesUnreviewedAgentProfile(t *testing.T) {
	ctx := t.Context()
	at := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	source := newContinuityInstallation(t, "source")
	parent, _ := sourceProject(t, source, at)
	folder, _ := source.files.GetFolderPath(parent.ID)
	dest := newContinuityInstallation(t, "destination")
	inRoot := filepath.Join(dest.root, "Album Release")
	if err := os.CopyFS(inRoot, os.DirFS(folder)); err != nil {
		t.Fatal(err)
	}
	rogue := filepath.Join(inRoot, "agents", "rogue")
	if err := os.MkdirAll(rogue, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rogue, "config.json"), []byte(`{"Settings":{"APIKey":"attacker-key"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	review := reviewContinuityFolder(t, dest, inRoot)
	if review["import_supported"] == true {
		if code, payload := importWorkspaceOnly(t, dest, inRoot, review); code < 400 {
			t.Fatalf("an unreviewed agent profile was installed: %d %v", code, payload)
		}
	}
	if policy, _ := dest.local.Policy(ctx, parent.ID); policy.Manual || policy.Automatic {
		t.Fatalf("the refused import became usable: %+v", policy)
	}
}

// A page on another site can send a text/plain POST without a CORS preflight;
// the consent endpoints take only JSON, so such a request changes nothing.
func TestContinuityConsentEndpointsRefuseNonJSONRequests(t *testing.T) {
	dest := newContinuityInstallation(t, "destination")
	for _, target := range []string{"/api/workspaces/import/continuity", "/api/workspaces/import/continuity/op-1/retry"} {
		req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(`{"path":"/tmp/x","action":"continue"}`))
		req.Header.Set("Content-Type", "text/plain")
		rec := httptest.NewRecorder()
		dest.handler.HandleWorkspaces(rec, req)
		if rec.Code != http.StatusUnsupportedMediaType {
			t.Fatalf("%s accepted text/plain: %d %s", target, rec.Code, rec.Body.String())
		}
	}
}

// An upload that cannot be written interrupts the import instead of being
// skipped; the retry installs it exactly once with the original bytes.
func TestContinuityImportResumesAfterUploadWriteFailure(t *testing.T) {
	ctx := t.Context()
	at := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	source := newContinuityInstallation(t, "source")
	ws := agentworkspace.NewWorkspace(agentworkspace.CreateWorkspaceParams{Name: "Field Recordings"})
	ws.OwnerUserID, ws.FolderSlug, ws.CreatedAt, ws.UpdatedAt = "local", "field-recordings", at, at
	if err := source.sync.Save(ws); err != nil {
		t.Fatal(err)
	}
	if err := source.store.CreateSession(ctx, &session.Session{ID: "chat-rain", Title: "Rain takes", AgentName: "Engineer",
		FolderID: ws.ID, CreatedAt: at, UpdatedAt: at}); err != nil {
		t.Fatal(err)
	}
	take := []byte("rain take 3 notes")
	entry, err := source.uploads.AddFileFromReader("chat-rain", bytes.NewReader(take), "take-3.txt", int64(len(take)))
	if err != nil {
		t.Fatal(err)
	}
	if status, err := source.worker.PrepareNow(ctx, ws.ID); err != nil || status.State != continuityprep.StateReady {
		t.Fatalf("source not ready: %+v %v", status, err)
	}
	folder, _ := source.files.GetFolderPath(ws.ID)
	copied := copyContinuityFolder(t, folder)

	dest := newContinuityInstallation(t, "destination")
	review := reviewContinuityFolder(t, dest, copied)
	uploadsRoot := dest.uploads.BasePath()
	if err := os.MkdirAll(uploadsRoot, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(uploadsRoot, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(uploadsRoot, 0o750) })
	code, payload := importWorkspaceOnly(t, dest, copied, review)
	report, _ := payload["import"].(map[string]any)
	operation, _ := report["operation_id"].(string)
	if code < 400 || operation == "" || report["status"] == "complete" {
		t.Fatalf("an unwritable upload did not interrupt the import: %d %v", code, payload)
	}
	if err := os.Chmod(uploadsRoot, 0o750); err != nil {
		t.Fatal(err)
	}
	if code, payload = dest.request(t, http.MethodPost, "/api/workspaces/import/continuity/"+operation+"/retry", nil); code != http.StatusCreated {
		t.Fatalf("retry: %d %v", code, payload)
	}
	files, err := dest.uploads.ListFiles("chat-rain")
	if err != nil || len(files) != 1 || files[0].ID != entry.ID {
		t.Fatalf("retry lost or duplicated the upload: %+v %v", files, err)
	}
	path, err := dest.uploads.GetFilePath("chat-rain", entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(path); err != nil || !bytes.Equal(data, take) {
		t.Fatal("restored upload bytes differ", err)
	}
}

// An interrupted import keeps its receipt, resumes without duplicating
// anything, and an identical re-import after local edits changes nothing.
func TestContinuityImportResumesAndRepeatsWithoutDuplicates(t *testing.T) {
	ctx := t.Context()
	at := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	source := newContinuityInstallation(t, "source")
	parent, _ := sourceProject(t, source, at)
	folder, _ := source.files.GetFolderPath(parent.ID)
	copied := copyContinuityFolder(t, folder)
	dest := newContinuityInstallation(t, "destination")
	review := reviewContinuityFolder(t, dest, copied)
	// Fail the install step after records are restored: the Workspace
	// Directory is read-only for the first attempt.
	if err := os.Chmod(dest.root, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dest.root, 0o750) })
	code, payload := importWorkspaceOnly(t, dest, copied, review)
	if code < 400 {
		t.Fatalf("blocked install reported success: %d %v", code, payload)
	}
	report, _ := payload["import"].(map[string]any)
	operation, _ := report["operation_id"].(string)
	if operation == "" || report["status"] == "complete" {
		t.Fatalf("interrupted import lost its receipt: %v", payload)
	}
	policy, _ := dest.local.Policy(ctx, parent.ID)
	if policy.Manual || policy.Automatic {
		t.Fatalf("a partial import became usable: %+v", policy)
	}
	if err := os.Chmod(dest.root, 0o750); err != nil {
		t.Fatal(err)
	}
	code, payload = dest.request(t, http.MethodPost, "/api/workspaces/import/continuity/"+operation+"/retry", nil)
	if code != http.StatusCreated {
		t.Fatalf("retry: %d %v", code, payload)
	}
	var messages int
	if err := dest.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages WHERE session_id='chat-album-release'`).Scan(&messages); err != nil || messages != 1 {
		t.Fatal("retry duplicated or lost messages", messages, err)
	}
	// Edit locally, then confirm the identical copy again: a no-op.
	if _, err := dest.db.ExecContext(ctx, `UPDATE sessions SET title='Edited here' WHERE id='chat-album-release'`); err != nil {
		t.Fatal(err)
	}
	review = reviewContinuityFolder(t, dest, copied)
	if review["already_imported"] == nil {
		t.Fatalf("identical copy not recognized: %v", review)
	}
	code, _ = importWorkspaceOnly(t, dest, copied, review)
	if code != http.StatusOK {
		t.Fatalf("identical re-import: %d", code)
	}
	restored, err := dest.store.GetSession(ctx, "chat-album-release")
	if err != nil || restored.Title != "Edited here" {
		t.Fatalf("re-import rolled back a local edit: %+v %v", restored, err)
	}
	var sessions int
	if err := dest.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions`).Scan(&sessions); err != nil || sessions != 2 {
		t.Fatal("re-import duplicated sessions", sessions, err)
	}
}

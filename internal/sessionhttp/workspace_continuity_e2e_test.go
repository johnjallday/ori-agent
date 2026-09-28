package sessionhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/continuityprep"
	"github.com/johnjallday/ori-agent/internal/dailybrief"
	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/followup"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/sessionfiles"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

// continuityInstallation is one isolated Ori installation for directory-only
// continuity tests: its own database, Workspace Directory and uploads store.
type continuityInstallation struct {
	db      *database.DB
	store   session.HybridStore
	local   *workspacecontinuity.LocalStore
	files   *agentworkspace.FileStore
	sync    agentworkspace.Store
	uploads *sessionfiles.Store
	handler *Handler
	worker  *continuityprep.Worker
	root    string
}

func newContinuityInstallation(t *testing.T, name string) *continuityInstallation {
	t.Helper()
	return newContinuityInstallationAt(t, name, "")
}

// newContinuityInstallationAt starts an installation with its own fresh
// database whose Workspace Directory is root (a new one when empty) — e.g. the
// same machine after an app-record reset that retained its workspace folders.
func newContinuityInstallationAt(t *testing.T, name, root string) *continuityInstallation {
	t.Helper()
	base := filepath.Join(t.TempDir(), name)
	db, err := database.Open(t.Context(), &database.Config{Path: filepath.Join(base, "data", "sessions.db")})
	if err != nil {
		t.Fatal(err)
	}
	store := session.NewHybridStoreWithDB(db, 50)
	t.Cleanup(func() { _ = store.Close() })
	local := workspacecontinuity.NewLocalStore(db)
	if root == "" {
		root = filepath.Join(base, "Ori Workspaces")
	}
	files, err := agentworkspace.NewFileStoreWithContinuity(root, local)
	if err != nil {
		t.Fatal(err)
	}
	uploads, err := sessionfiles.NewStore(filepath.Join(base, "session_files"))
	if err != nil {
		t.Fatal(err)
	}
	handler := New(store)
	handler.SetWorkspaceStore(files)
	allowlist := agentworkspace.NewAllowlist(filepath.Join(base, "data", "allowlist.json"))
	handler.SetWorkspaceAllowlist(allowlist)
	handler.SetContinuityImport(uploads, nil)
	spool := filepath.Join(base, "data", "spool")
	if err := os.MkdirAll(spool, 0o700); err != nil {
		t.Fatal(err)
	}
	worker := &continuityprep.Worker{DB: db, Local: local, Folders: files, SpoolParent: spool, Debounce: time.Nanosecond,
		Collector: &continuityprep.Collector{Sessions: session.NewSQLiteStore(db), Briefs: dailybrief.NewSQLiteStore(db),
			Assistants: personalassistant.NewSQLiteStore(db), UploadsBase: uploads.BasePath}}
	return &continuityInstallation{db: db, store: store, local: local, files: files, uploads: uploads,
		sync: agentworkspace.NewSyncStore(session.NewWorkspaceStoreAdapter(store), files), handler: handler, worker: worker, root: root}
}

func (c *continuityInstallation) request(t *testing.T, method, target string, body any) (int, map[string]any) {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		reader = bytes.NewReader(data)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, target, reader)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	c.handler.HandleWorkspaces(w, req)
	var payload map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &payload)
	return w.Code, payload
}

// The golden path: real source saves → prepared checkpoint → copy ONLY the
// folder → fresh destination review → confirmed import → history back in the
// normal stores, inactive, with no source database or upload root.
func TestContinuityDirectoryOnlyImportRestoresSavedWork(t *testing.T) {
	ctx := t.Context()
	source := newContinuityInstallation(t, "source")
	at := time.Date(2026, 9, 1, 9, 30, 0, 0, time.UTC)

	ws := agentworkspace.NewWorkspace(agentworkspace.CreateWorkspaceParams{Name: "Garden Project"})
	ws.OwnerUserID, ws.FolderSlug = "local", "garden-project"
	ws.CreatedAt, ws.UpdatedAt = at, at
	ws.Tasks = []agentworkspace.Task{{ID: "task-done", WorkspaceID: ws.ID, Description: "Plan beds", Status: agentworkspace.TaskStatusCompleted,
		Result: "Three raised beds", CreatedAt: at}}
	if err := source.sync.Save(ws); err != nil {
		t.Fatal(err)
	}
	folder, err := source.files.GetFolderPath(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "MEMORY.md"), []byte("Tomatoes need full sun.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	chat := &session.Session{ID: "chat-1", Title: "Planning chat", AgentName: "Gardener", FolderID: ws.ID, CreatedAt: at, UpdatedAt: at}
	if err := source.store.CreateSession(ctx, chat); err != nil {
		t.Fatal(err)
	}
	for i, text := range []string{"Where should the beds go?", "Along the south fence."} {
		role := session.RoleUser
		if i == 1 {
			role = session.RoleAssistant
		}
		if err := source.store.AddMessage(ctx, chat.ID, &session.Message{ID: "msg-" + string(rune('a'+i)), Role: role, Content: text,
			CreatedAt: at.Add(time.Duration(i) * time.Minute)}); err != nil {
			t.Fatal(err)
		}
	}
	unrelated := &session.Session{ID: "global-chat", Title: "Unrelated", AgentName: "Gardener", CreatedAt: at, UpdatedAt: at}
	if err := source.store.CreateSession(ctx, unrelated); err != nil {
		t.Fatal(err)
	}
	upload := []byte("soil test results")
	entry, err := source.uploads.AddFileFromReader(chat.ID, bytes.NewReader(upload), "soil.txt", int64(len(upload)))
	if err != nil {
		t.Fatal(err)
	}
	if err := session.NewSQLiteStore(source.db).CreateNote(ctx, &session.WorkspaceNote{ID: "note-1", WorkspaceID: ws.ID, Name: "Seeds",
		Content: "# Seeds\nBuy heirloom seeds.", Tags: []string{"shopping"}, CreatedAt: at, UpdatedAt: at}); err != nil {
		t.Fatal(err)
	}
	follow := &followup.FollowUp{ID: "follow-1", UserID: "local", WorkspaceID: ws.ID, Category: followup.CategoryIOwe,
		Direction: followup.DirectionOutbound, Title: "Order compost", Source: followup.SourceRef{Type: "manual"},
		Provenance: followup.ProvenanceManual, Status: followup.StatusCompleted, CreatedAt: at, UpdatedAt: at, CompletedAt: &at}
	if err := followup.NewSQLiteStore(source.db).Create(ctx, follow); err != nil {
		t.Fatal(err)
	}

	status, err := source.worker.PrepareNow(ctx, ws.ID)
	if err != nil || status.State != continuityprep.StateReady {
		prep, _ := source.local.Preparation(ctx, ws.ID)
		t.Fatalf("source not ready: %+v err=%v prep=%+v", status, err, prep)
	}

	// Copy only the workspace folder; nothing else of the source travels.
	copied := filepath.Join(t.TempDir(), "usb-stick", "Garden Project")
	if err := os.MkdirAll(filepath.Dir(copied), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(copied, os.DirFS(folder)); err != nil {
		t.Fatal(err)
	}

	dest := newContinuityInstallation(t, "destination")
	code, payload := dest.request(t, http.MethodGet, "/api/workspaces/import/check?path="+url.QueryEscape(copied), nil)
	if code != http.StatusOK {
		t.Fatal(code, payload)
	}
	review, _ := payload["continuity"].(map[string]any)
	if review["status"] != "review_required" || review["import_supported"] != true || review["recommended_action"] != "workspace_only" {
		t.Fatalf("review: %v", review)
	}
	code, payload = dest.request(t, http.MethodPost, "/api/workspaces/import/continuity", map[string]any{
		"path": copied, "tree_digest": review["tree_digest"], "destination_digest": review["destination_digest"], "action": "workspace_only"})
	if code != http.StatusCreated {
		t.Fatalf("import: %d %v", code, payload)
	}
	report, _ := payload["import"].(map[string]any)
	if report["status"] != "complete" || report["workspace_id"] != ws.ID {
		t.Fatalf("report: %v", report)
	}

	// Saved history is back in the normal stores with original identity and dates.
	restoredChat, err := dest.store.GetSession(ctx, chat.ID)
	if err != nil || restoredChat.FolderID != ws.ID || len(restoredChat.Messages) != 2 || !restoredChat.Messages[0].CreatedAt.Equal(at) {
		t.Fatalf("chat not restored: %+v %v", restoredChat, err)
	}
	if _, err := dest.store.GetSession(ctx, unrelated.ID); err == nil {
		t.Fatal("unrelated global chat travelled with the folder")
	}
	files, err := dest.uploads.ListFiles(chat.ID)
	if err != nil || len(files) != 1 || files[0].ID != entry.ID {
		t.Fatalf("upload not restored: %+v %v", files, err)
	}
	uploadPath, err := dest.uploads.GetFilePath(chat.ID, entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(uploadPath); err != nil || !bytes.Equal(data, upload) {
		t.Fatal("upload bytes differ", err)
	}
	note, err := session.NewSQLiteStore(dest.db).GetNote(ctx, "note-1")
	if err != nil || note.WorkspaceID != ws.ID || !note.CreatedAt.Equal(at) {
		t.Fatalf("note not restored: %+v %v", note, err)
	}
	restoredFollow, err := followup.NewSQLiteStore(dest.db).Get(ctx, "local", "follow-1")
	if err != nil || restoredFollow.Status != followup.StatusCompleted {
		t.Fatalf("follow-up not restored exactly: %+v %v", restoredFollow, err)
	}

	// The workspace is attached inactive: readable and usable by hand, no
	// background routines until the user enables them here.
	policy, err := dest.local.Policy(ctx, ws.ID)
	if err != nil || policy.State != workspacecontinuity.ImportedInactive || policy.Automatic || !policy.Manual {
		t.Fatalf("admission: %+v %v", policy, err)
	}
	if err := agentworkspace.RequireWorkspaceExecution(ctx, dest.files, ws.ID, true); err == nil {
		t.Fatal("imported workspace admitted automatic work")
	}
	loaded, err := dest.files.Get(ws.ID)
	if err != nil || len(loaded.Tasks) != 1 || loaded.Tasks[0].Result != "Three raised beds" {
		t.Fatalf("installed workspace unreadable: %v", err)
	}
	installed, _ := dest.files.GetFolderPath(ws.ID)
	if memory, err := os.ReadFile(filepath.Join(installed, "MEMORY.md")); err != nil || string(memory) != "Tomatoes need full sun.\n" {
		t.Fatal("memory not carried", err)
	}
	// The source copy itself is untouched.
	if _, err := workspacecontinuity.Inspect(ctx, copied); err != nil {
		t.Fatal("import modified the selected folder", err)
	}

	// An identical second confirmation is a no-op that returns the same result.
	code, payload = dest.request(t, http.MethodGet, "/api/workspaces/import/check?path="+url.QueryEscape(copied), nil)
	review, _ = payload["continuity"].(map[string]any)
	if code != http.StatusOK || review["already_imported"] == nil {
		t.Fatalf("repeat review: %d %v", code, review)
	}
	// And the destination prepares its own checkpoint for the imported folder.
	if status, err := dest.worker.PrepareNow(ctx, ws.ID); err != nil || status.State != continuityprep.StateReady {
		prep, _ := dest.local.Preparation(ctx, ws.ID)
		t.Fatalf("imported workspace not re-preparable: %+v %v %+v", status, err, prep)
	}
	_ = context.Background
}

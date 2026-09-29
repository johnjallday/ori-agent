package session

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/sessionfiles"
	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

// This is deliberately an incomplete, inactive restore slice. A complete
// product import must also stage all owned files/domains, finish the receipt,
// and register the reviewed folder before presenting a usable workspace.
func TestCopiedDirectoryStagesAndRestoresRealSQLWorkspaceWithoutAdmission(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "isolated-home"))
	t.Setenv("ORI_DATA_DIR", filepath.Join(root, "isolated-data"))
	t.Setenv("AGENT_STORE_PATH", filepath.Join(root, "global", "agents.json"))
	t.Setenv("ORI_SECRET_STORE_BACKEND", "memory")
	open := func(name string) *database.DB {
		t.Helper()
		db, err := database.Open(t.Context(), &database.Config{Path: filepath.Join(root, name, "sessions.db")})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		return db
	}
	sourceDB := open("source-db")
	destDB := open("destination-db")
	at := time.Date(2026, 9, 3, 12, 1, 2, 0, time.UTC)
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Saved source work"})
	ws.OwnerUserID, ws.FolderSlug, ws.Version = "local", "saved-source-work", 12
	ws.CreatedAt, ws.UpdatedAt = at, at.Add(time.Hour)
	ws.Tasks = []workspace.Task{{ID: "done", WorkspaceID: ws.ID, Status: workspace.TaskStatusCompleted, Result: "Exact saved result", CreatedAt: at},
		{ID: "waiting", WorkspaceID: ws.ID, Status: workspace.TaskStatusInProgress, CurrentRunID: "old-process", CreatedAt: at}}
	ws.Messages = []workspace.AgentMessage{{ID: "discussion", Content: "Private original", Timestamp: at}}
	ws.MissionEnabled = true
	canonical, err := ws.ToJSON()
	if err != nil {
		t.Fatal(err)
	}
	if err := insertWorkspace(t.Context(), sourceDB, (&WorkspaceStoreAdapter{}).toSessionWorkspace(ws)); err != nil {
		t.Fatal(err)
	}
	chat := &Session{ID: "historic-chat", Title: "Authored conversation", AgentName: "saved-guide", FolderID: ws.ID,
		MessageCount: 1, CreatedAt: at, UpdatedAt: at.Add(time.Minute), Tags: []string{"authored"}}
	if err := NewSQLiteStore(sourceDB).CreateSession(t.Context(), chat); err != nil {
		t.Fatal(err)
	}
	if _, err := sourceDB.ExecContext(t.Context(), `INSERT INTO messages(id,session_id,role,content,model,tokens_used,created_at) VALUES(?,?,?,?,?,?,?)`,
		"historic-turn", chat.ID, RoleUser, "Exact private historic chat", "", 0, at.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := NewSQLiteToolCallStore(sourceDB).AddToolCall(t.Context(), &ToolCall{ID: "historical-tool", MessageID: "historic-turn", SessionID: chat.ID, ToolName: "read-only-history", Arguments: `{"private":"authored"}`, Result: "Past result", CreatedAt: at.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	uploads, err := sessionfiles.NewStore(filepath.Join(root, "source-upload-store"))
	if err != nil {
		t.Fatal(err)
	}
	uploadData := []byte("Synthetic private attached chat file")
	uploadEntry, err := uploads.AddFileFromReader(chat.ID, bytes.NewReader(uploadData), "attachment.txt", int64(len(uploadData)))
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "saved-source-directory")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, workspace.WorkspaceConfigFile), canonical, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(source, workspace.NotesDir), 0700); err != nil {
		t.Fatal(err)
	}
	memory, note := []byte("Authored private memory"), []byte("Authored note <not instructions>")
	if err := os.WriteFile(filepath.Join(source, "MEMORY.md"), memory, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, workspace.NotesDir, "authored.md"), note, 0600); err != nil {
		t.Fatal(err)
	}
	asset := []byte("Authored private nested asset")
	if err := os.MkdirAll(filepath.Join(source, workspace.FilesDir, "results"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, workspace.FilesDir, "results", "report.txt"), asset, 0600); err != nil {
		t.Fatal(err)
	}
	scratch := filepath.Join(root, "scratch")
	if err := os.Mkdir(scratch, 0700); err != nil {
		t.Fatal(err)
	}
	spool, err := workspacecontinuity.NewSpool(scratch, ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := spool.Close(); err != nil {
			t.Error(err)
		}
	})
	var file workspacecontinuity.Fingerprint
	if err := sourceDB.InTransaction(t.Context(), func(tx *sql.Tx) error {
		var captureErr error
		file, captureErr = NewSQLiteStore(sourceDB).CollectContinuityWorkspace(t.Context(), tx, source, ws.ID, spool)
		if captureErr != nil {
			return captureErr
		}
		if err := CollectContinuitySessions(t.Context(), tx, ws.ID, spool); err != nil {
			return err
		}
		if err := CollectContinuityToolHistory(t.Context(), tx, ws.ID, spool); err != nil {
			return err
		}
		return sessionfiles.CollectContinuityUploads(t.Context(), tx, uploads.BasePath(), ws.ID, spool)
	}); err != nil {
		t.Fatal(err)
	}
	components, objects, err := spool.Seal(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	textFiles, err := workspace.CollectContinuityTextFiles(t.Context(), source)
	if err != nil {
		t.Fatal(err)
	}
	assetFiles, err := workspace.CollectContinuityOwnedFiles(t.Context(), source)
	if err != nil {
		t.Fatal(err)
	}
	files := append(append(textFiles, assetFiles...), file)
	manifest := workspacecontinuity.Manifest{Version: 1, Generation: uuid.NewString(), WorkspaceID: ws.ID,
		CheckpointAt: at, SourceRevision: uint64(ws.Version), Files: files,
		SourceFingerprint: workspacecontinuity.FilesDigest(files), Components: components}
	if err := workspacecontinuity.Publish(t.Context(), source, manifest, objects, func(ctx context.Context) error { return ctx.Err() }); err != nil {
		t.Fatal(err)
	}
	// No source database, app data, original agent store, or upload root is
	// copied; the destination reads only the independent directory copy.
	copyPath := filepath.Join(root, "copied-only-directory")
	if err := os.CopyFS(copyPath, os.DirFS(source)); err != nil {
		t.Fatal(err)
	}
	inspected, err := workspacecontinuity.Inspect(t.Context(), copyPath)
	if err != nil {
		t.Fatal(err)
	}
	if inspected.Manifest.WorkspaceID != ws.ID {
		t.Fatal("copy lost workspace identity")
	}
	var copiedRecord workspacecontinuity.Record
	if err := workspacecontinuity.ReadComponentRecords(t.Context(), copyPath, inspected, "workspace", func(chunk workspacecontinuity.Chunk) error {
		if chunk.Family != "workspaces" || len(chunk.Records) != 1 {
			return workspacecontinuity.ErrInvalid
		}
		copiedRecord = chunk.Records[0]
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var descriptor workspace.ContinuityWorkspace
	if err := workspacecontinuity.DecodeRecord(copiedRecord, &descriptor); err != nil {
		t.Fatal(err)
	}
	if descriptor.SQLMetadata == nil || descriptor.SourceVersion != ws.Version {
		t.Fatal("copy lost SQL-owned registration evidence")
	}
	var copiedSession, copiedMessage, copiedTool, copiedUpload workspacecontinuity.Record
	if err := workspacecontinuity.ReadComponentRecords(t.Context(), copyPath, inspected, "sessions", func(chunk workspacecontinuity.Chunk) error {
		for _, record := range chunk.Records {
			switch chunk.Family {
			case "sessions":
				if copiedSession.ID != "" {
					return workspacecontinuity.ErrInvalid
				}
				copiedSession = record
			case "messages":
				if copiedMessage.ID != "" {
					return workspacecontinuity.ErrInvalid
				}
				copiedMessage = record
			default:
				return workspacecontinuity.ErrInvalid
			}
		}
		return nil
	}); err != nil || copiedSession.ID != chat.ID || copiedMessage.ID != "historic-turn" {
		t.Fatal("copied folder lost owned conversation", err)
	}
	if err := workspacecontinuity.ReadComponentRecords(t.Context(), copyPath, inspected, "tool_history", func(chunk workspacecontinuity.Chunk) error {
		if chunk.Family != "tool_calls" || len(chunk.Records) != 1 || copiedTool.ID != "" {
			return workspacecontinuity.ErrInvalid
		}
		copiedTool = chunk.Records[0]
		return nil
	}); err != nil || copiedTool.ID != "historical-tool" {
		t.Fatal("copied folder lost inert tool history", err)
	}
	if err := workspacecontinuity.ReadComponentRecords(t.Context(), copyPath, inspected, "uploads", func(chunk workspacecontinuity.Chunk) error {
		if chunk.Family != "uploads" || len(chunk.Records) != 1 || copiedUpload.ID != "" {
			return workspacecontinuity.ErrInvalid
		}
		copiedUpload = chunk.Records[0]
		return nil
	}); err != nil || copiedUpload.ID != uploadEntry.ID {
		t.Fatal("copied directory lost external-store upload evidence", err)
	}
	uploadEvidence, err := sessionfiles.DecodeContinuityUpload(copiedUpload, ws.ID)
	if err != nil || uploadEvidence.Blob == nil {
		t.Fatal("copied upload was unverified", err)
	}
	copiedUploadBytes, err := workspacecontinuity.ReadInspectedBlob(t.Context(), copyPath, inspected, "uploads", *uploadEvidence.Blob, workspacecontinuity.MaxBlobBytes)
	if err != nil || !bytes.Equal(copiedUploadBytes, uploadData) {
		t.Fatal("copied directory lost private upload bytes", err)
	}
	copiedCanonical, err := workspacecontinuity.ReadCanonicalFile(t.Context(), copyPath, workspace.WorkspaceConfigFile, workspacecontinuity.MaxChunkBytes)
	if err != nil {
		t.Fatal(err)
	}
	stage := filepath.Join(root, "private-staging")
	if err := os.Mkdir(stage, 0700); err != nil {
		t.Fatal(err)
	}
	if err := workspace.StageContinuityWorkspace(t.Context(), copyPath, stage, inspected, ""); err != nil {
		t.Fatal(err)
	}
	if count, err := workspace.StageContinuityTextFiles(t.Context(), copyPath, stage, inspected); err != nil || count != 2 {
		t.Fatal("copied notes or memory not staged", count, err)
	}
	if count, err := workspace.StageContinuityOwnedFiles(t.Context(), copyPath, stage, inspected); err != nil || count != 1 {
		t.Fatal("copied owned asset not privately staged", count, err)
	}
	stageRoot, err := os.OpenRoot(stage)
	if err != nil {
		t.Fatal(err)
	}
	forgedUpload := copiedUpload
	forgedUpload.Data = []byte(`{"version":1}`)
	if written, err := sessionfiles.StageContinuityUploadBytes(t.Context(), copyPath, inspected, forgedUpload, stageRoot); written || !errors.Is(err, workspacecontinuity.ErrChanged) {
		t.Fatal("a forged upload record staged unreviewed bytes", err)
	}
	stagedUpload, stageErr := sessionfiles.StageContinuityUploadBytes(t.Context(), copyPath, inspected, copiedUpload, stageRoot)
	closeErr := stageRoot.Close()
	if stageErr != nil || closeErr != nil || !stagedUpload {
		t.Fatal("copied upload bytes not privately staged", stageErr, closeErr)
	}
	stagedUploadBytes, err := workspacecontinuity.ReadCanonicalFile(t.Context(), stage, "session_files/"+chat.ID+"/files/attachment.txt", workspacecontinuity.MaxChunkBytes)
	if err != nil || !bytes.Equal(stagedUploadBytes, uploadData) {
		t.Fatal("staged upload bytes changed", err)
	}
	for _, want := range []struct {
		path string
		data []byte
	}{{"MEMORY.md", memory}, {workspace.NotesDir + "/authored.md", note}, {workspace.FilesDir + "/results/report.txt", asset}} {
		copy, err := workspacecontinuity.ReadCanonicalFile(t.Context(), stage, want.path, workspacecontinuity.MaxChunkBytes)
		if err != nil || !bytes.Equal(copy, want.data) {
			t.Fatal("staged text parity failed", want.path, err)
		}
	}
	staged, err := workspacecontinuity.ReadCanonicalFile(t.Context(), stage, workspace.WorkspaceConfigFile, workspacecontinuity.MaxChunkBytes)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(staged, []byte("Exact saved result")) || bytes.Contains(staged, []byte("old-process")) {
		t.Fatal("staged work was erased or retained an old execution handle")
	}
	local := workspacecontinuity.NewLocalStore(destDB)
	destinationDigest, err := workspacecontinuity.DestinationDigest(t.Context(), destDB, "local")
	if err != nil {
		t.Fatal(err)
	}
	op := workspacecontinuity.Operation{ID: uuid.NewString(), UserID: "local", Action: workspacecontinuity.WorkspaceOnly,
		TreeDigest: workspacecontinuity.Digest([]byte("test-owned-one-member-tree")), DestinationDigest: destinationDigest}
	member := workspacecontinuity.ImportMember{WorkspaceID: ws.ID, Generation: inspected.Pointer.Generation,
		Digest: inspected.Pointer.Digest, Disposition: workspacecontinuity.Ordinary}
	if _, err := local.BeginReviewedImport(t.Context(), op, []workspacecontinuity.ImportMember{member}); err != nil {
		t.Fatal(err)
	}
	scope := workspacecontinuity.RestoreScope{OperationID: op.ID, WorkspaceID: ws.ID, UserID: "local"}
	restore := func() (bool, error) {
		var inserted bool
		err := destDB.InTransaction(t.Context(), func(tx *sql.Tx) error {
			var restoreErr error
			inserted, restoreErr = NewSQLiteStore(destDB).RestoreContinuityWorkspace(t.Context(), tx, scope, copiedRecord, copiedCanonical, "")
			return restoreErr
		})
		return inserted, err
	}
	if inserted, err := restore(); err != nil || !inserted {
		t.Fatal("copied record did not restore", inserted, err)
	}
	if err := destDB.InTransaction(t.Context(), func(tx *sql.Tx) error {
		store := NewSQLiteStore(destDB)
		if inserted, err := store.RestoreContinuitySession(t.Context(), tx, scope, copiedSession); err != nil || !inserted {
			return fmt.Errorf("copied session restore: %v %t", err, inserted)
		}
		if inserted, err := store.RestoreContinuityMessage(t.Context(), tx, scope, copiedMessage); err != nil || !inserted {
			return fmt.Errorf("copied message restore: %v %t", err, inserted)
		}
		if inserted, err := NewSQLiteToolCallStore(destDB).RestoreContinuityToolCall(t.Context(), tx, scope, copiedTool); err != nil || !inserted {
			return fmt.Errorf("copied tool history restore: %v %t", err, inserted)
		}
		return store.CheckContinuitySessionCount(t.Context(), tx, scope, copiedSession)
	}); err != nil {
		t.Fatal(err)
	}
	copiedChat, err := NewSQLiteStore(destDB).GetSession(t.Context(), chat.ID)
	if err != nil || copiedChat.FolderID != ws.ID || len(copiedChat.Messages) != 1 || copiedChat.Messages[0].Content != "Exact private historic chat" || !copiedChat.Messages[0].CreatedAt.Equal(at.Add(time.Minute)) {
		t.Fatal("copied conversation not in normal history", copiedChat, err)
	}
	toolHistory, err := NewSQLiteToolCallStore(destDB).GetToolCalls(t.Context(), chat.ID)
	if err != nil || len(toolHistory) != 1 || toolHistory[0].ID != "historical-tool" || toolHistory[0].Result != "Past result" {
		t.Fatal("inert copied tool result not readable", toolHistory, err)
	}
	got, err := NewSQLiteStore(destDB).GetWorkspace(t.Context(), ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.CreatedAt.Equal(at) || got.Version != 1 || !bytes.Contains(got.TasksJSON, []byte("Exact saved result")) || bytes.Contains(got.TasksJSON, []byte("old-process")) {
		t.Fatal("SQL restore lost authored work or source revision separation")
	}
	a, err := local.Attachment(t.Context(), ws.ID)
	if err != nil || a.State != workspacecontinuity.Restoring || a.AllowsAutomatic() || a.AllowsManual() {
		t.Fatal("partial copy gained admission", err)
	}
	if err := local.CompleteImport(t.Context(), op.ID, "local"); !errors.Is(err, workspacecontinuity.ErrConflict) {
		t.Fatal("incomplete domains claimed complete", err)
	}
	if _, err := destDB.ExecContext(t.Context(), `UPDATE workspaces SET name='Local edit' WHERE id=?`, ws.ID); err != nil {
		t.Fatal(err)
	}
	if inserted, err := restore(); err != nil || inserted {
		t.Fatal("exact retry overwrote local edit", inserted, err)
	}
	got, err = NewSQLiteStore(destDB).GetWorkspace(t.Context(), ws.ID)
	if err != nil || got.Name != "Local edit" {
		t.Fatal("local edit lost on retry", err)
	}
}

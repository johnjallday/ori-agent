package session

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

func TestContinuityWorkspaceRestoreIsOwnedPreservingAndInsertOnly(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("ORI_DATA_DIR", filepath.Join(root, "data"))
	db, err := database.Open(t.Context(), &database.Config{Path: filepath.Join(root, "data", "sessions.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := NewSQLiteStore(db)
	at := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Copied work", Agents: []string{"Guide"}})
	ws.OwnerUserID, ws.CreatedAt, ws.UpdatedAt, ws.Version = "local", at, at.Add(time.Hour), 17
	ws.FolderSlug = "copied-work"
	ws.MCPBindings = []workspace.MCPBinding{{ID: "connector", ServerName: "offline", AllowedTools: []string{}}}
	ws.Messages = []workspace.AgentMessage{{ID: "collaboration", Content: "Exact historical text", Timestamp: at}}
	ws.Tasks = []workspace.Task{{ID: "done", WorkspaceID: ws.ID, Description: "Finished", Status: workspace.TaskStatusCompleted,
		CreatedAt: at, Result: "Retained result"}}
	data, err := ws.ToJSON()
	if err != nil {
		t.Fatal(err)
	}
	sourceDB, err := database.Open(t.Context(), &database.Config{Path: filepath.Join(root, "source", "sessions.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sourceDB.Close() })
	sourceStore := NewSQLiteStore(sourceDB)
	sourceWorkspace := (&WorkspaceStoreAdapter{}).toSessionWorkspace(ws)
	sourceWorkspace.Color = "#a1b2c3"
	if err := insertWorkspace(t.Context(), sourceDB, sourceWorkspace); err != nil {
		t.Fatal(err)
	}
	// Canonical SQL retains local grants by design; capture compares their denied
	// definition, not their installation-private values.
	if _, err := sourceDB.ExecContext(t.Context(), `UPDATE workspaces SET mcp_bindings_json=? WHERE id=?`,
		`[{"id":"connector","server_name":"offline","allowed_tools":["native-tool"],"config":{"key":"synthetic-local-secret"}}]`, ws.ID); err != nil {
		t.Fatal(err)
	}
	var record workspacecontinuity.Record
	err = sourceDB.InTransaction(t.Context(), func(tx *sql.Tx) error {
		var err error
		record, err = sourceStore.SnapshotContinuityWorkspace(t.Context(), tx, data)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sourceDB.ExecContext(t.Context(), `UPDATE workspaces SET name='Changed concurrently' WHERE id=?`, ws.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := sourceStore.SnapshotContinuityWorkspace(t.Context(), sourceDB, data); !errors.Is(err, workspacecontinuity.ErrChanged) {
		t.Fatal("mixed canonical registration/file evidence was accepted")
	}
	// A mirror that lags the folder (here: SQL lost the task) does not block the
	// checkpoint, and the folder's content is what travels — never the SQL copy.
	if _, err := sourceDB.ExecContext(t.Context(), `UPDATE workspaces SET name=?,tasks_json='[]',version=version+5 WHERE id=?`, ws.Name, ws.ID); err != nil {
		t.Fatal(err)
	}
	lagging, err := sourceStore.SnapshotContinuityWorkspace(t.Context(), sourceDB, data)
	if err != nil {
		t.Fatal("a lagging SQL mirror blocked the checkpoint:", err)
	}
	var descriptor workspace.ContinuityWorkspace
	if err := workspacecontinuity.DecodeRecord(lagging, &descriptor); err != nil ||
		descriptor.FileDigest != workspacecontinuity.Digest(data) || descriptor.TaskCount != 1 {
		t.Fatalf("checkpoint did not carry the folder's work: %+v %v", descriptor, err)
	}
	local := workspacecontinuity.NewLocalStore(db)
	op, err := local.BeginImport(t.Context(), workspacecontinuity.Operation{ID: uuid.NewString(), UserID: "local", Action: workspacecontinuity.WorkspaceOnly,
		TreeDigest: workspacecontinuity.Digest([]byte("tree")), DestinationDigest: workspacecontinuity.Digest([]byte("empty"))},
		[]workspacecontinuity.ImportMember{{WorkspaceID: ws.ID, Generation: uuid.NewString(), Digest: workspacecontinuity.Digest(data), Disposition: workspacecontinuity.Ordinary}})
	if err != nil {
		t.Fatal(err)
	}
	scope := workspacecontinuity.RestoreScope{OperationID: op.ID, WorkspaceID: ws.ID, UserID: "local"}
	restore := func(parent string) (bool, error) {
		var inserted bool
		err := db.InTransaction(t.Context(), func(tx *sql.Tx) error {
			var err error
			inserted, err = store.RestoreContinuityWorkspace(t.Context(), tx, scope, record, data, parent)
			return err
		})
		return inserted, err
	}
	// File-only identity is not complete canonical registration evidence.
	captured := record
	record, err = workspace.SnapshotContinuityWorkspace(data)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restore(""); !errors.Is(err, workspacecontinuity.ErrIncomplete) {
		t.Fatal("file-only description fabricated SQL-owned metadata")
	}
	record = captured
	// A caller cannot install an unrelated existing workspace as the parent.
	if _, err := restore("unreviewed-parent"); !errors.Is(err, workspacecontinuity.ErrConflict) {
		t.Fatalf("unowned parent was accepted: %v", err)
	}
	inserted, err := restore("")
	if err != nil || !inserted {
		t.Fatalf("first canonical insertion: %t %v", inserted, err)
	}
	got, err := store.GetWorkspace(t.Context(), ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.CreatedAt.Equal(ws.CreatedAt) || !got.UpdatedAt.Equal(ws.UpdatedAt) || got.OrderIndex != 0 || got.Version != 1 || got.SessionCount != 0 || got.Color != sourceWorkspace.Color {
		t.Fatal("restore behaved like new workspace creation or imported source CAS/counts")
	}
	adapted := (&WorkspaceStoreAdapter{}).toAgentWorkspace(got)
	if len(adapted.Messages) != 1 || adapted.Messages[0].Content != ws.Messages[0].Content || len(adapted.Tasks) != 1 || adapted.Tasks[0].Result != ws.Tasks[0].Result {
		t.Fatal("canonical SQL owner did not preserve saved work")
	}
	attachment, err := local.Attachment(t.Context(), ws.ID)
	if err != nil || attachment.State != workspacecontinuity.Restoring || attachment.AllowsAutomatic() {
		t.Fatal("canonical insertion changed admission")
	}
	if _, err := db.ExecContext(t.Context(), `UPDATE workspaces SET name='Local edit' WHERE id=?`, ws.ID); err != nil {
		t.Fatal(err)
	}
	inserted, err = restore("different-parent-on-retry")
	if err != nil || inserted {
		t.Fatalf("exact retry revalidated mutable choices: %t %v", inserted, err)
	}
	got, err = store.GetWorkspace(t.Context(), ws.ID)
	if err != nil || got.Name != "Local edit" {
		t.Fatal("retry overwrote destination work")
	}
	if _, err := db.ExecContext(t.Context(), `DELETE FROM workspaces WHERE id=?`, ws.ID); err != nil {
		t.Fatal(err)
	}
	inserted, err = restore("")
	if err != nil || inserted {
		t.Fatalf("exact receipt did not preserve deletion: %t %v", inserted, err)
	}
	if _, err := store.GetWorkspace(context.Background(), ws.ID); !errors.Is(err, ErrWorkspaceNotFound) {
		t.Fatal("retry resurrected deleted work")
	}
	// An exact receipt on a detached member permits a no-op, not new records.
	err = db.InTransaction(t.Context(), func(tx *sql.Tx) error {
		_, err := workspacecontinuity.ClaimRecord(t.Context(), tx, scope, "agents", "profiles", "new-profile", workspacecontinuity.Digest([]byte("new")))
		return err
	})
	if !errors.Is(err, workspacecontinuity.ErrConflict) {
		t.Fatal("detached receipt allowed new canonical claims")
	}
}

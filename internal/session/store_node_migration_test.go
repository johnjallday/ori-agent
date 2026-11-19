package session

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// These tests run the startup pass against a real SQLite database and a real
// folder store, seeded with the raw legacy state an upgraded user would have: a
// store_nodes_json column, tickets naming a store, and a workspace.json carrying
// the same keys. Every directory is a t.TempDir(); nothing touches real data.

const legacyMigrationWorkspaceID = "ws-legacy-stores"

type storeNodeMigrationFixture struct {
	t       *testing.T
	root    string
	dataDir string
	db      *database.DB
	sync    *workspace.SyncStore
	folders *workspace.FileStore
	baseDir string
}

// legacyWorkspace is the workspace as an older Ori saved it: a store bound to an
// agent, one ticket that names a store, and one agent ticket with no storage.
func (f *storeNodeMigrationFixture) legacyWorkspace() *workspace.Workspace {
	now := time.Now().UTC().Truncate(time.Second)
	return &workspace.Workspace{
		ID:          legacyMigrationWorkspaceID,
		Name:        "Legacy stores",
		OwnerUserID: "local",
		Status:      workspace.StatusActive,
		CreatedAt:   now,
		UpdatedAt:   now,
		// Folder-only state: it lives in workspace.json, not in a SQL column.
		ProjectPath: "/work/legacy-project",
		Designation: "test-designation",
		LegacyStoreNodes: []workspace.LegacyStoreNode{{
			ID: "store-1", CanvasNodeID: "canvas-1", AgentNodeID: "agent-node-1", WorkspaceID: legacyMigrationWorkspaceID,
			Name: "Reports", BaseDir: f.baseDir, Format: "text", WriteMode: "append", AutoCreateDir: true, AutoStore: true,
		}},
		Tasks: []workspace.Task{
			{
				// Real tickets already carry an assignment mode. Without one the folder
				// store's own boot migration would rewrite workspace.json before this pass
				// looked at it, which says nothing about store nodes.
				ID: "t-named", WorkspaceID: legacyMigrationWorkspaceID, Description: "Daily report",
				AssignmentMode: workspace.TaskAssignmentModeLegacyUnknown,
				Status:         workspace.TaskStatusPending, CreatedAt: now,
				// A fixed file in a store that appends: the one case the rewrite cannot reproduce exactly.
				ResultStorage: &workspace.ResultStorageConfig{Enabled: true, LegacyStoreNodeID: "store-1", FilePath: "out.txt", Format: "text"},
			},
			{
				ID: "t-agent", WorkspaceID: legacyMigrationWorkspaceID, Description: "Agent ticket",
				AssignmentMode: workspace.TaskAssignmentModeLegacyUnknown,
				Status:         workspace.TaskStatusPending, CreatedAt: now, AssignedNodeID: "agent-node-1",
			},
		},
	}
}

func newStoreNodeMigrationFixture(t *testing.T) *storeNodeMigrationFixture {
	t.Helper()
	root := t.TempDir()
	db, err := database.Open(context.Background(), &database.Config{InMemory: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	folders, err := workspace.NewFileStore(filepath.Join(root, "workspaces"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = folders.Close() })
	adapter := NewWorkspaceStoreAdapter(NewHybridStoreWithDB(db, 10))
	return &storeNodeMigrationFixture{
		t: t, root: root, dataDir: filepath.Join(root, "data"), db: db,
		sync: workspace.NewSyncStore(adapter, folders), folders: folders,
		baseDir: filepath.Join(root, "reports"),
	}
}

// seedLegacy saves the workspace through the real stores, then rewrites the SQL
// columns and workspace.json to the raw legacy shape, as an older Ori left them.
func (f *storeNodeMigrationFixture) seedLegacy() (rawFile []byte) {
	f.t.Helper()
	legacy := f.legacyWorkspace()
	clean := f.legacyWorkspace()
	clean.LegacyStoreNodes = nil
	for i := range clean.Tasks {
		if clean.Tasks[i].ResultStorage != nil {
			clean.Tasks[i].ResultStorage.LegacyStoreNodeID = ""
		}
	}
	if err := f.sync.Save(clean); err != nil {
		f.t.Fatalf("Save: %v", err)
	}

	nodesJSON, err := json.Marshal(legacy.LegacyStoreNodes)
	if err != nil {
		f.t.Fatal(err)
	}
	tasksJSON, err := json.Marshal(legacy.Tasks)
	if err != nil {
		f.t.Fatal(err)
	}
	if _, err := f.db.ExecContext(context.Background(), `UPDATE workspaces SET store_nodes_json=?, tasks_json=? WHERE id=?`,
		string(nodesJSON), string(tasksJSON), legacy.ID); err != nil {
		f.t.Fatalf("seed legacy columns: %v", err)
	}

	path := f.workspaceFilePath()
	raw, err := os.ReadFile(path)
	if err != nil {
		f.t.Fatalf("read workspace.json: %v", err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		f.t.Fatal(err)
	}
	doc["store_nodes"], doc["tasks"] = nodesJSON, tasksJSON
	rawFile, err = json.MarshalIndent(doc, "", "  ")
	if err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(path, rawFile, 0o600); err != nil {
		f.t.Fatal(err)
	}
	return rawFile
}

func (f *storeNodeMigrationFixture) workspaceFilePath() string {
	f.t.Helper()
	folder, err := f.folders.GetFolderPath(legacyMigrationWorkspaceID)
	if err != nil {
		f.t.Fatalf("GetFolderPath: %v", err)
	}
	return filepath.Join(folder, workspace.WorkspaceConfigFile)
}

func (f *storeNodeMigrationFixture) migration() StoreNodeMigration {
	return StoreNodeMigration{DataDir: f.dataDir, DB: f.db, Workspaces: f.sync, Folders: f.folders}
}

func (f *storeNodeMigrationFixture) sqlColumns() (storeNodes, tasks string) {
	f.t.Helper()
	if err := f.db.QueryRowContext(context.Background(),
		`SELECT store_nodes_json, tasks_json FROM workspaces WHERE id=?`, legacyMigrationWorkspaceID).Scan(&storeNodes, &tasks); err != nil {
		f.t.Fatalf("read columns: %v", err)
	}
	return storeNodes, tasks
}

func (f *storeNodeMigrationFixture) readMarker() StoreNodeMigrationReport {
	f.t.Helper()
	data, err := os.ReadFile(filepath.Join(f.dataDir, StoreNodeMigrationMarker))
	if err != nil {
		f.t.Fatalf("read marker: %v", err)
	}
	var marker StoreNodeMigrationReport
	if err := json.Unmarshal(data, &marker); err != nil {
		f.t.Fatalf("decode marker: %v", err)
	}
	return marker
}

func TestStoreNodeMigration_RewritesALegacyWorkspaceAndKeepsAnUndoablePreImage(t *testing.T) {
	f := newStoreNodeMigrationFixture(t)
	rawFile := f.seedLegacy()
	rawStoreNodes, rawTasks := f.sqlColumns()
	if !strings.Contains(rawStoreNodes, "store-1") || !strings.Contains(rawTasks, `"store_node_id":"store-1"`) {
		t.Fatalf("the fixture should hold legacy columns, got %s / %s", rawStoreNodes, rawTasks)
	}

	report, err := f.migration().Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.Status != StoreNodeMigrationDone || len(report.Migrated) != 1 || report.Migrated[0] != legacyMigrationWorkspaceID || len(report.Failed) != 0 {
		t.Fatalf("unexpected report: %+v", report)
	}

	t.Run("the SQLite row no longer carries a store node or a reference", func(t *testing.T) {
		storeNodes, tasks := f.sqlColumns()
		if strings.TrimSpace(storeNodes) != "[]" {
			t.Fatalf("store_nodes_json = %q, want []", storeNodes)
		}
		if strings.Contains(tasks, "store_node_id") {
			t.Fatalf("tasks_json still names a store: %s", tasks)
		}
		wantDir := strings.ReplaceAll(f.baseDir, `\`, `\\`)
		if !strings.Contains(tasks, wantDir) {
			t.Fatalf("the agent ticket should now target %q, got %s", f.baseDir, tasks)
		}
	})

	t.Run("workspace.json is rewritten and keeps its folder-only fields", func(t *testing.T) {
		data, err := os.ReadFile(f.workspaceFilePath())
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		if strings.Contains(text, "store_nodes") || strings.Contains(text, "store_node_id") {
			t.Fatalf("workspace.json still carries the retired keys: %s", text)
		}
		if !strings.Contains(text, "/work/legacy-project") || !strings.Contains(text, "test-designation") {
			t.Fatalf("folder-only fields were lost in the rewrite: %s", text)
		}
	})

	t.Run("what it replaced is kept, with private permissions", func(t *testing.T) {
		if !strings.HasPrefix(report.Backup, filepath.Join(f.dataDir, "recovery", "store-nodes-to-tickets-")) {
			t.Fatalf("backup = %q", report.Backup)
		}
		wsBackup := filepath.Join(report.Backup, legacyMigrationWorkspaceID)
		sqlData, err := os.ReadFile(filepath.Join(wsBackup, "sqlite.json"))
		if err != nil {
			t.Fatalf("sqlite backup: %v", err)
		}
		var sqlBackup map[string]string
		if err := json.Unmarshal(sqlData, &sqlBackup); err != nil {
			t.Fatal(err)
		}
		if sqlBackup["store_nodes_json"] != rawStoreNodes || sqlBackup["tasks_json"] != rawTasks {
			t.Fatalf("the sqlite backup should hold the raw columns, got %v", sqlBackup)
		}
		fileBackup, err := os.ReadFile(filepath.Join(wsBackup, "workspace.json"))
		if err != nil {
			t.Fatalf("workspace.json backup: %v", err)
		}
		if string(fileBackup) != string(rawFile) {
			t.Fatal("the workspace.json backup should be the raw legacy file")
		}
		if info, err := os.Stat(filepath.Join(wsBackup, "workspace.json")); err != nil || info.Mode().Perm()&0o077 != 0 {
			t.Fatalf("backup files must be private, got %v err=%v", info, err)
		}
	})

	t.Run("the marker records the run and what could not be reproduced", func(t *testing.T) {
		marker := f.readMarker()
		if marker.Status != StoreNodeMigrationDone || marker.Backup != report.Backup || len(marker.Migrated) != 1 {
			t.Fatalf("unexpected marker: %+v", marker)
		}
		// The store appended to one fixed file for a ticket that was not in append
		// mode. The pass computes this from the raw state, because the stores had
		// already rewritten the workspace by the time it looked.
		if len(marker.Lossy) != 1 || marker.Lossy[0].TaskID != "t-named" || marker.Lossy[0].WorkspaceID != legacyMigrationWorkspaceID {
			t.Fatalf("expected exactly the fixed-file ticket to be reported, got %+v", marker.Lossy)
		}
	})

	t.Run("a second run does nothing and leaves the first run's record alone", func(t *testing.T) {
		before, err := os.ReadFile(filepath.Join(f.dataDir, StoreNodeMigrationMarker))
		if err != nil {
			t.Fatal(err)
		}
		again, err := f.migration().Run()
		if err != nil {
			t.Fatalf("second Run: %v", err)
		}
		if again.Status != StoreNodeMigrationNotNeeded || again.Backup != "" {
			t.Fatalf("a migrated workspace needs nothing, got %+v", again)
		}
		after, _ := os.ReadFile(filepath.Join(f.dataDir, StoreNodeMigrationMarker))
		if string(before) != string(after) {
			t.Fatal("a pass with nothing to do must not rewrite the marker")
		}
		entries, err := os.ReadDir(filepath.Join(f.dataDir, "recovery"))
		if err != nil || len(entries) != 1 {
			t.Fatalf("exactly one backup folder is expected, got %v err=%v", entries, err)
		}
	})
}

func TestStoreNodeMigration_NothingLegacyWritesNoBackupAndNoMarker(t *testing.T) {
	f := newStoreNodeMigrationFixture(t)
	clean := f.legacyWorkspace()
	clean.LegacyStoreNodes = nil
	for i := range clean.Tasks {
		if clean.Tasks[i].ResultStorage != nil {
			clean.Tasks[i].ResultStorage.LegacyStoreNodeID = ""
		}
	}
	if err := f.sync.Save(clean); err != nil {
		t.Fatal(err)
	}

	report, err := f.migration().Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.Status != StoreNodeMigrationNotNeeded {
		t.Fatalf("status = %q", report.Status)
	}
	if _, err := os.Stat(f.dataDir); !os.IsNotExist(err) {
		t.Fatalf("nothing to do must write nothing under the data dir, stat err=%v", err)
	}
}

func TestStoreNodeMigration_AFailedBackupSavesNothingAndRetriesOnTheNextStart(t *testing.T) {
	f := newStoreNodeMigrationFixture(t)
	f.seedLegacy()
	rawStoreNodes, rawTasks := f.sqlColumns()

	// A file where the recovery folder must go makes the backup impossible.
	if err := os.MkdirAll(f.dataDir, 0o750); err != nil {
		t.Fatal(err)
	}
	blocker := filepath.Join(f.dataDir, "recovery")
	if err := os.WriteFile(blocker, []byte("not a folder"), 0o600); err != nil {
		t.Fatal(err)
	}

	report, err := f.migration().Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.Status != StoreNodeMigrationBackupFailed || report.Error == "" || len(report.Migrated) != 0 {
		t.Fatalf("expected a backup failure, got %+v", report)
	}
	if gotNodes, gotTasks := f.sqlColumns(); gotNodes != rawStoreNodes || gotTasks != rawTasks {
		t.Fatalf("a failed backup must save nothing: %s / %s", gotNodes, gotTasks)
	}
	if f.readMarker().Status != StoreNodeMigrationBackupFailed {
		t.Fatal("the marker should record the failed backup")
	}

	if err := os.Remove(blocker); err != nil {
		t.Fatal(err)
	}
	retried, err := f.migration().Run()
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if retried.Status != StoreNodeMigrationDone || len(retried.Migrated) != 1 {
		t.Fatalf("the next start should finish the job, got %+v", retried)
	}
}

// The continuity fence compares the SQL mirror with workspace.json after both
// are read through the same migration, so an upgraded install whose SQL row still
// holds store nodes is not reported as split from its (migrated) file.
func TestStoreNodeMigration_FenceTreatsALegacyRowAndItsMigratedFileAsEqual(t *testing.T) {
	ctx := context.Background()

	// The baseline for a workspace with no store nodes at all: whatever this
	// fixture's unrelated differences are, they are not store nodes' doing.
	baselineFixture := newStoreNodeMigrationFixture(t)
	clean := baselineFixture.legacyWorkspace()
	clean.LegacyStoreNodes = nil
	for i := range clean.Tasks {
		if clean.Tasks[i].ResultStorage != nil {
			clean.Tasks[i].ResultStorage.LegacyStoreNodeID = ""
		}
	}
	if err := baselineFixture.sync.Save(clean); err != nil {
		t.Fatal(err)
	}
	cleanDiffs, err := continuityMirrorDifferences(ctx, baselineFixture.db, clean)
	if err != nil {
		t.Fatalf("baseline fence: %v", err)
	}

	f := newStoreNodeMigrationFixture(t)
	rawFile := f.seedLegacy()
	// What an import hands the fence: the reviewed file, read and so migrated.
	migratedFile, err := workspace.FromJSON(rawFile)
	if err != nil {
		t.Fatalf("FromJSON: %v", err)
	}
	if workspace.HasLegacyStoreNodes(migratedFile) {
		t.Fatal("the file side should be migrated")
	}
	diffs, err := continuityMirrorDifferences(ctx, f.db, migratedFile)
	if err != nil {
		t.Fatalf("fence: %v", err)
	}
	if strings.Join(diffs, ",") != strings.Join(cleanDiffs, ",") {
		t.Fatalf("a legacy row must differ from its migrated file in nothing a clean row does not; clean=%v legacy=%v", cleanDiffs, diffs)
	}

	// Negative control: a SQL row whose store pointed somewhere else migrates to
	// different tickets, and the fence still sees it.
	rawStoreNodes, _ := f.sqlColumns()
	elsewhere := strings.ReplaceAll(rawStoreNodes, strings.ReplaceAll(f.baseDir, `\`, `\\`), filepath.Join(f.root, "somewhere-else"))
	if elsewhere == rawStoreNodes {
		t.Fatal("the control should change the store's directory")
	}
	if _, err := f.db.ExecContext(ctx, `UPDATE workspaces SET store_nodes_json=? WHERE id=?`, elsewhere, legacyMigrationWorkspaceID); err != nil {
		t.Fatal(err)
	}
	diffs, err = continuityMirrorDifferences(ctx, f.db, migratedFile)
	if err != nil {
		t.Fatalf("fence (control): %v", err)
	}
	if !strings.Contains(strings.Join(diffs, ","), "tasks_json") {
		t.Fatalf("the fence should see tickets that migrate differently, got %v", diffs)
	}
}

// A save that reports success is not taken on trust: the stores are read back.
type forgetfulUpdater struct{}

func (forgetfulUpdater) Update(string, func(*workspace.Workspace) error) error { return nil }

func TestStoreNodeMigration_ASaveThatChangedNothingIsReportedAsFailed(t *testing.T) {
	f := newStoreNodeMigrationFixture(t)
	f.seedLegacy()
	m := f.migration()
	m.Workspaces = forgetfulUpdater{}

	report, err := m.Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.Status != StoreNodeMigrationIncomplete || len(report.Migrated) != 0 {
		t.Fatalf("expected an incomplete run, got %+v", report)
	}
	if reason := report.Failed[legacyMigrationWorkspaceID]; !strings.Contains(reason, "still carries store nodes") {
		t.Fatalf("failure reason = %q", reason)
	}
}

// Without SQLite as the primary store the folder store is both the source and the
// place the rewrite is saved.
func TestStoreNodeMigration_FolderStoreOnly(t *testing.T) {
	f := newStoreNodeMigrationFixture(t)
	f.seedLegacy()
	if err := f.folders.Close(); err != nil {
		t.Fatal(err)
	}
	// A fresh store reads workspace.json from disk, as a restarted app does.
	reopened, err := workspace.NewFileStore(filepath.Join(f.root, "workspaces"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })

	report, err := StoreNodeMigration{DataDir: f.dataDir, Workspaces: reopened, Folders: reopened}.Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.Status != StoreNodeMigrationDone || len(report.Migrated) != 1 {
		t.Fatalf("unexpected report: %+v", report)
	}
	data, err := os.ReadFile(f.workspaceFilePath())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "store_nodes") || strings.Contains(string(data), "store_node_id") {
		t.Fatalf("workspace.json still carries the retired keys: %s", data)
	}
	if !strings.Contains(string(data), "/work/legacy-project") {
		t.Fatalf("folder-only fields were lost: %s", data)
	}
}

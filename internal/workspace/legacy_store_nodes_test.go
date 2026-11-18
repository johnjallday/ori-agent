package workspace

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// These tests are the store-node retirement's proof (tasks/tasks-retire-workspace-canvas.md,
// groups 1.6 and 7). Group 1 pinned where a ticket's result was written while a
// store node was involved; here the same legacy workspaces are migrated and then
// run through the real AutoStoreResult against a real FileStore, and the file on
// disk is looked at. Where the migrated ticket writes somewhere different from the
// store node, the difference is one the migration contract accepts and the test
// says so.
//
// File names carry a timestamp, so a default name is matched by pattern.

var legacyStoreTimestampedText = regexp.MustCompile(`^Daily_report_\d{8}-\d{6}\.txt$`)

type legacyStoreFixture struct {
	fileStore *FileStore
	ws        *Workspace
}

func newLegacyStoreFixture(t *testing.T) *legacyStoreFixture {
	t.Helper()
	store, ws := newResultStorageTestWorkspace(t, "ws-store-migration", "Store Migration")
	return &legacyStoreFixture{fileStore: store, ws: ws}
}

func (f *legacyStoreFixture) addTasks(t *testing.T, tasks ...Task) {
	t.Helper()
	for _, task := range tasks {
		task.WorkspaceID = f.ws.ID
		if err := f.ws.AddTask(task); err != nil {
			t.Fatalf("AddTask(%s): %v", task.ID, err)
		}
	}
}

// migrate rewrites the workspace and checks the properties every case shares: it
// reports a change, it leaves no store node and no store reference behind, and a
// second run changes nothing.
func (f *legacyStoreFixture) migrate(t *testing.T) LegacyStoreMigration {
	t.Helper()
	if !HasLegacyStoreNodes(f.ws) {
		t.Fatal("fixture should carry something legacy")
	}
	got := MigrateLegacyStoreNodesWithReport(f.ws)
	if !got.Changed {
		t.Fatal("migration of a legacy workspace should report a change")
	}
	if len(f.ws.LegacyStoreNodes) != 0 {
		t.Fatalf("store nodes should be gone, found %d", len(f.ws.LegacyStoreNodes))
	}
	for i := range f.ws.Tasks {
		if storage := f.ws.Tasks[i].ResultStorage; storage != nil && storage.LegacyStoreNodeID != "" {
			t.Fatalf("task %s still names store %q", f.ws.Tasks[i].ID, storage.LegacyStoreNodeID)
		}
	}
	if HasLegacyStoreNodes(f.ws) {
		t.Fatal("nothing legacy should remain")
	}
	if again := MigrateLegacyStoreNodesWithReport(f.ws); again.Changed || len(again.Lossy) != 0 {
		t.Fatalf("a second run should change nothing, got %+v", again)
	}
	return got
}

// store saves result for the ticket the way the executor does: through
// AutoStoreResult with the workspace as the store resolver.
func (f *legacyStoreFixture) store(t *testing.T, taskID, result string) {
	t.Helper()
	task, err := f.ws.GetTask(taskID)
	if err != nil {
		t.Fatalf("GetTask(%s): %v", taskID, err)
	}
	AutoStoreResult(f.ws, task, result, f.fileStore)
}

func (f *legacyStoreFixture) outputsDir() string { return f.fileStore.GetOutputsPath(f.ws.ID) }

// filesUnder lists regular files below root as slash-separated relative paths.
// A root that does not exist holds no files.
func filesUnder(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) && path == root {
				return filepath.SkipAll
			}
			return err
		}
		if entry.Type().IsRegular() {
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	sort.Strings(files)
	return files
}

func readLegacyStoreFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func requireLegacyStoreFiles(t *testing.T, root string, patterns ...*regexp.Regexp) []string {
	t.Helper()
	files := filesUnder(t, root)
	if len(files) != len(patterns) {
		t.Fatalf("files under %s = %v, want %d file(s)", root, files, len(patterns))
	}
	for i, pattern := range patterns {
		if !pattern.MatchString(files[i]) {
			t.Fatalf("file %d under %s = %q, want match for %s (all: %v)", i, root, files[i], pattern, files)
		}
	}
	return files
}

func exactName(name string) *regexp.Regexp {
	return regexp.MustCompile("^" + regexp.QuoteMeta(name) + "$")
}

func lossyTasks(m LegacyStoreMigration) []string {
	var ids []string
	for _, change := range m.Lossy {
		ids = append(ids, change.TaskID)
	}
	sort.Strings(ids)
	return ids
}

// (a) A ticket that names a store node whose base directory is absolute saves
// under that directory, exactly as before.
func TestMigrateLegacyStoreNodes_TicketNamingAStoreSavesUnderItsBaseDir(t *testing.T) {
	cases := []struct {
		name     string
		ref      string
		filePath string
		want     *regexp.Regexp
	}{
		{"default name, referenced by id", "store-abs", "", legacyStoreTimestampedText},
		{"default name, referenced by canvas node id", "canvas-store-abs", "", legacyStoreTimestampedText},
		{"explicit relative file path", "store-abs", "weekly/summary.txt", exactName("weekly/summary.txt")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newLegacyStoreFixture(t)
			base := t.TempDir()
			f.ws.LegacyStoreNodes = []LegacyStoreNode{{
				ID: "store-abs", CanvasNodeID: "canvas-store-abs", WorkspaceID: f.ws.ID, Name: "Reports",
				BaseDir: base, Format: "text", WriteMode: "overwrite", AutoCreateDir: true,
			}}
			f.addTasks(t, Task{
				ID: "task-a", Description: "Daily report",
				ResultStorage: &ResultStorageConfig{Enabled: true, LegacyStoreNodeID: tc.ref, FilePath: tc.filePath, Format: "text"},
			})

			report := f.migrate(t)
			if len(report.Lossy) != 0 {
				t.Fatalf("nothing is lost for this case, got %+v", report.Lossy)
			}
			f.store(t, "task-a", "result body")

			files := requireLegacyStoreFiles(t, base, tc.want)
			if got := readLegacyStoreFile(t, filepath.Join(base, files[0])); got != "result body" {
				t.Fatalf("stored content = %q, want %q", got, "result body")
			}
			if left := filesUnder(t, f.outputsDir()); len(left) != 0 {
				t.Fatalf("nothing belongs in the workspace outputs folder, found %v", left)
			}
		})
	}
}

// The store's own settings decided how a write behaved. The migrated ticket keeps
// its own format and creates its directory; where that differs from the store,
// the contract accepted it and each case below says so.
func TestMigrateLegacyStoreNodes_StoreSettingsBecomeTheTicketsBehaviour(t *testing.T) {
	newNode := func(base, format, writeMode string, autoCreateDir bool) LegacyStoreNode {
		return LegacyStoreNode{
			ID: "store-mode", WorkspaceID: "ws-store-migration", Name: "Mode", BaseDir: base,
			Format: format, WriteMode: writeMode, AutoCreateDir: autoCreateDir,
		}
	}
	ticket := func(id, description string) Task {
		return Task{
			ID: id, Description: description,
			ResultStorage: &ResultStorageConfig{Enabled: true, LegacyStoreNodeID: "store-mode", FilePath: "out.txt", Format: "text"},
		}
	}

	t.Run("overwrite replaces the file, as before", func(t *testing.T) {
		f := newLegacyStoreFixture(t)
		base := t.TempDir()
		f.ws.LegacyStoreNodes = []LegacyStoreNode{newNode(base, "text", "overwrite", true)}
		f.addTasks(t, ticket("t1", "Daily report"), ticket("t2", "Daily report"))
		if report := f.migrate(t); len(report.Lossy) != 0 {
			t.Fatalf("nothing is lost, got %+v", report.Lossy)
		}
		f.store(t, "t1", "first")
		f.store(t, "t2", "second")
		requireLegacyStoreFiles(t, base, exactName("out.txt"))
		if got := readLegacyStoreFile(t, filepath.Join(base, "out.txt")); got != "second" {
			t.Fatalf("content = %q, want the second write only", got)
		}
	})

	t.Run("a store that appended to one file now writes a timestamped file beside it, and says so", func(t *testing.T) {
		f := newLegacyStoreFixture(t)
		base := t.TempDir()
		f.ws.LegacyStoreNodes = []LegacyStoreNode{newNode(base, "text", "append", true)}
		f.addTasks(t, ticket("t1", "Daily report"), ticket("t2", "Weekly report"))
		report := f.migrate(t)
		if got := lossyTasks(report); !reflect.DeepEqual(got, []string{"t1", "t2"}) {
			t.Fatalf("both tickets are reported, got %v", got)
		}
		f.store(t, "t1", "first")
		f.store(t, "t2", "second")
		requireLegacyStoreFiles(t, base,
			regexp.MustCompile(`^Daily_report_\d{8}-\d{6}\.txt$`),
			regexp.MustCompile(`^Weekly_report_\d{8}-\d{6}\.txt$`),
		)
		if _, err := os.Stat(filepath.Join(base, "out.txt")); !os.IsNotExist(err) {
			t.Fatalf("the old fixed log name is never written to, stat err=%v", err)
		}
	})

	t.Run("a json store now writes compact json", func(t *testing.T) {
		f := newLegacyStoreFixture(t)
		base := t.TempDir()
		f.ws.LegacyStoreNodes = []LegacyStoreNode{newNode(base, "json", "overwrite", true)}
		task := ticket("t1", "Daily report")
		task.ResultStorage.FilePath = "out.json"
		task.ResultStorage.Format = "json"
		f.addTasks(t, task)
		f.migrate(t)
		f.store(t, "t1", "plain text result")
		got := readLegacyStoreFile(t, filepath.Join(base, "out.json"))
		if !json.Valid([]byte(got)) {
			t.Fatalf("expected valid json, got %q", got)
		}
		if strings.Contains(got, "\n  ") {
			t.Fatalf("the ticket writes compact json, not the store's two-space indent, got %q", got)
		}
	})

	t.Run("a store that did not create its directory now does", func(t *testing.T) {
		f := newLegacyStoreFixture(t)
		base := filepath.Join(t.TempDir(), "not-created-yet")
		f.ws.LegacyStoreNodes = []LegacyStoreNode{newNode(base, "text", "overwrite", false)}
		f.addTasks(t, ticket("t1", "Daily report"))
		f.migrate(t)
		f.store(t, "t1", "result body")
		requireLegacyStoreFiles(t, base, exactName("out.txt"))
	})

	t.Run("an absolute file path was refused by the store, so the ticket stays off", func(t *testing.T) {
		f := newLegacyStoreFixture(t)
		base := t.TempDir()
		elsewhere := t.TempDir()
		absolute := filepath.Join(elsewhere, "out.txt")
		f.ws.LegacyStoreNodes = []LegacyStoreNode{newNode(base, "text", "overwrite", true)}
		task := ticket("t1", "Daily report")
		task.ResultStorage.FilePath = absolute
		f.addTasks(t, task)
		report := f.migrate(t)
		if got := lossyTasks(report); !reflect.DeepEqual(got, []string{"t1"}) {
			t.Fatalf("the refused ticket is reported, got %v", got)
		}
		migrated, err := f.ws.GetTask("t1")
		if err != nil {
			t.Fatalf("GetTask: %v", err)
		}
		if migrated.ResultStorage.Enabled {
			t.Fatal("a ticket that never saved must not start saving")
		}
		if migrated.ResultStorage.FilePath != absolute {
			t.Fatalf("the path is kept as written, got %q", migrated.ResultStorage.FilePath)
		}
		f.store(t, "t1", "result body")
		if files := filesUnder(t, base); len(files) != 0 {
			t.Fatalf("store base should stay empty, found %v", files)
		}
		if files := filesUnder(t, elsewhere); len(files) != 0 {
			t.Fatalf("the absolute path must not be written either, found %v", files)
		}
	})

	t.Run("a store with no base directory never saved, so the ticket stays off", func(t *testing.T) {
		f := newLegacyStoreFixture(t)
		f.ws.LegacyStoreNodes = []LegacyStoreNode{newNode("", "text", "overwrite", true)}
		f.addTasks(t, ticket("t1", "Daily report"))
		report := f.migrate(t)
		if got := lossyTasks(report); !reflect.DeepEqual(got, []string{"t1"}) {
			t.Fatalf("the ticket is reported, got %v", got)
		}
		f.store(t, "t1", "result body")
		if files := filesUnder(t, f.outputsDir()); len(files) != 0 {
			t.Fatalf("nothing is saved, found %v in the outputs folder", files)
		}
	})
}

// (b) A store on a workspace folder resolves under the workspace's files root. The
// ticket's own workspace_folder target names the same folder.
func TestMigrateLegacyStoreNodes_StoreOnAWorkspaceFolderBecomesThatFolderTarget(t *testing.T) {
	f := newLegacyStoreFixture(t)
	folder := filepath.Join("reports", "daily")
	f.ws.LegacyStoreNodes = []LegacyStoreNode{{
		ID: "store-folder", CanvasNodeID: "canvas-store-folder", WorkspaceID: f.ws.ID, Name: "Daily reports",
		StorageTarget: StorageTargetWorkspaceFolder, Folder: folder,
		Format: "text", WriteMode: "overwrite", AutoCreateDir: true,
	}}
	f.addTasks(t, Task{
		ID: "via-store", Description: "Daily report",
		ResultStorage: &ResultStorageConfig{Enabled: true, LegacyStoreNodeID: "store-folder", Format: "text"},
	})
	f.migrate(t)

	migrated, err := f.ws.GetTask("via-store")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got := migrated.ResultStorage; got.StorageTarget != StorageTargetWorkspaceFolder || got.Folder != folder {
		t.Fatalf("expected the ticket to target workspace folder %q, got %+v", folder, got)
	}

	f.store(t, "via-store", "via the store node")
	want := filepath.Join(f.fileStore.GetFilesPath(f.ws.ID), folder)
	requireLegacyStoreFiles(t, want, legacyStoreTimestampedText)
	if left := filesUnder(t, f.outputsDir()); len(left) != 0 {
		t.Fatalf("nothing belongs in the workspace outputs folder, found %v", left)
	}
}

// (c) An agent-bound store with auto_store caught the results of tickets assigned
// to that agent node, unless the ticket had storage of its own. Each ticket that
// existed is given the store's directory; tickets created later are not.
func TestMigrateLegacyStoreNodes_AgentBoundStoreIsCopiedOntoEachExistingTicket(t *testing.T) {
	f := newLegacyStoreFixture(t)
	base := t.TempDir()
	own := t.TempDir()
	f.ws.LegacyStoreNodes = []LegacyStoreNode{{
		ID: "store-agent", CanvasNodeID: "canvas-store-agent", AgentNodeID: "agent-node-1", WorkspaceID: f.ws.ID, Name: "Agent output",
		BaseDir: base, Format: "text", WriteMode: "overwrite", AutoCreateDir: true, AutoStore: true,
	}}
	f.addTasks(t,
		// No storage of its own: the agent's store took it.
		Task{ID: "11111111-aaaa", Description: "First", AssignedNodeID: "agent-node-1"},
		// Its own enabled storage won; the agent store was not touched.
		Task{
			ID: "22222222-bbbb", Description: "Second", AssignedNodeID: "agent-node-1",
			ResultStorage: &ResultStorageConfig{Enabled: true, FilePath: filepath.Join(own, "out.txt"), Format: "text"},
		},
		// Disabled storage does not count as storage of its own.
		Task{
			ID: "33333333-cccc", Description: "Third", AssignedNodeID: "agent-node-1",
			ResultStorage: &ResultStorageConfig{Enabled: false, FilePath: filepath.Join(own, "ignored.txt")},
		},
		// Unassigned, or assigned to another agent, were never stored.
		Task{ID: "44444444-dddd", Description: "Fourth", AssignedNodeID: "unassigned"},
		Task{ID: "55555555-eeee", Description: "Fifth", AssignedNodeID: "agent-node-2"},
		Task{ID: "66666666-ffff", Description: "Sixth"},
	)
	report := f.migrate(t)
	if len(report.Lossy) != 0 {
		t.Fatalf("nothing is lost, got %+v", report.Lossy)
	}

	for _, id := range []string{"11111111-aaaa", "22222222-bbbb", "33333333-cccc", "44444444-dddd", "55555555-eeee", "66666666-ffff"} {
		f.store(t, id, "from "+id)
	}

	// The names changed from task-<id8>-<ts> to <description>_<ts>, in the same
	// directory (an accepted difference in the contract).
	requireLegacyStoreFiles(t, base,
		regexp.MustCompile(`^First_\d{8}-\d{6}\.txt$`),
		regexp.MustCompile(`^Third_\d{8}-\d{6}\.txt$`),
	)
	requireLegacyStoreFiles(t, own, exactName("out.txt"))
	if got := readLegacyStoreFile(t, filepath.Join(own, "out.txt")); got != "from 22222222-bbbb" {
		t.Fatalf("own storage content = %q", got)
	}
	if left := filesUnder(t, f.outputsDir()); len(left) != 0 {
		t.Fatalf("nothing belongs in the workspace outputs folder, found %v", left)
	}

	// A ticket created after the migration inherits nothing: the store is gone.
	f.addTasks(t, Task{ID: "77777777-gggg", Description: "Seventh", AssignedNodeID: "agent-node-1"})
	f.store(t, "77777777-gggg", "from a new ticket")
	if files := filesUnder(t, base); len(files) != 2 {
		t.Fatalf("a later ticket must not inherit the agent store, found %v", files)
	}
}

func TestMigrateLegacyStoreNodes_AgentBoundStoreWithAutoStoreOffGivesNothing(t *testing.T) {
	f := newLegacyStoreFixture(t)
	base := t.TempDir()
	f.ws.LegacyStoreNodes = []LegacyStoreNode{{
		ID: "store-agent", AgentNodeID: "agent-node-1", WorkspaceID: f.ws.ID, Name: "Agent output",
		BaseDir: base, Format: "text", WriteMode: "overwrite", AutoCreateDir: true, AutoStore: false,
	}}
	f.addTasks(t, Task{ID: "11111111-aaaa", Description: "First", AssignedNodeID: "agent-node-1"})
	f.migrate(t)

	task, err := f.ws.GetTask("11111111-aaaa")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.ResultStorage != nil {
		t.Fatalf("auto_store off copies nothing, got %+v", task.ResultStorage)
	}
	f.store(t, "11111111-aaaa", "from ticket one")
	if files := filesUnder(t, base); len(files) != 0 {
		t.Fatalf("auto_store off means no write, found %v", files)
	}
}

// Only the first store bound to an agent ever took its results.
func TestMigrateLegacyStoreNodes_OnlyTheFirstStoreBoundToAnAgentCounts(t *testing.T) {
	f := newLegacyStoreFixture(t)
	first := t.TempDir()
	second := t.TempDir()
	f.ws.LegacyStoreNodes = []LegacyStoreNode{
		{ID: "store-1", AgentNodeID: "agent-node-1", BaseDir: first, Format: "text", AutoCreateDir: true, AutoStore: true},
		{ID: "store-2", AgentNodeID: "agent-node-1", BaseDir: second, Format: "text", AutoCreateDir: true, AutoStore: true},
	}
	f.addTasks(t, Task{ID: "t1", Description: "Daily report", AssignedNodeID: "agent-node-1"})
	f.migrate(t)
	f.store(t, "t1", "result body")
	requireLegacyStoreFiles(t, first, legacyStoreTimestampedText)
	if files := filesUnder(t, second); len(files) != 0 {
		t.Fatalf("the second store never took results, found %v", files)
	}
}

// (d) A ticket that named a store node that no longer existed wrote nothing, and
// did not fall back to the agent's store. Clearing the dangling id leaves a ticket
// that is enabled with no destination, which saves to the workspace outputs
// folder: an accepted change, reported.
func TestMigrateLegacyStoreNodes_TicketNamingAMissingStoreNowSavesToOutputs(t *testing.T) {
	f := newLegacyStoreFixture(t)
	base := t.TempDir()
	f.ws.LegacyStoreNodes = []LegacyStoreNode{{
		ID: "store-agent", AgentNodeID: "agent-node-1", WorkspaceID: f.ws.ID, Name: "Agent output",
		BaseDir: base, Format: "text", WriteMode: "overwrite", AutoCreateDir: true, AutoStore: true,
	}}
	f.addTasks(t, Task{
		ID: "dangling", Description: "Daily report", AssignedNodeID: "agent-node-1",
		ResultStorage: &ResultStorageConfig{Enabled: true, LegacyStoreNodeID: "store-that-was-deleted", Format: "text"},
	})
	report := f.migrate(t)
	if got := lossyTasks(report); !reflect.DeepEqual(got, []string{"dangling"}) {
		t.Fatalf("the ticket whose store was missing is reported, got %v", got)
	}

	f.store(t, "dangling", "result body")
	if files := filesUnder(t, base); len(files) != 0 {
		t.Fatalf("a dangling store id must not fall back to the agent store, found %v", files)
	}
	requireLegacyStoreFiles(t, f.outputsDir(), legacyStoreTimestampedText)
}

func TestMigrateLegacyStoreNodes_ADisabledTicketNamingAMissingStoreIsNotReported(t *testing.T) {
	f := newLegacyStoreFixture(t)
	f.addTasks(t, Task{
		ID: "off", Description: "Daily report",
		ResultStorage: &ResultStorageConfig{Enabled: false, LegacyStoreNodeID: "store-that-was-deleted", Format: "text"},
	})
	report := f.migrate(t)
	if len(report.Lossy) != 0 {
		t.Fatalf("a ticket that was off stays off and loses nothing, got %+v", report.Lossy)
	}
	task, _ := f.ws.GetTask("off")
	if task.ResultStorage.Enabled {
		t.Fatal("the ticket stays off")
	}
}

func TestMigrateLegacyStoreNodes_WorkspaceWithoutStoreNodesIsUntouched(t *testing.T) {
	ws := newTestWorkspace("ws-plain", "Plain")
	ws.Tasks = []Task{{
		ID: "t1", Description: "Daily report",
		ResultStorage: &ResultStorageConfig{Enabled: true, FilePath: "/tmp/out.txt", Format: "text"},
	}}
	before, err := json.Marshal(ws)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if HasLegacyStoreNodes(ws) {
		t.Fatal("a plain workspace has nothing legacy")
	}
	if MigrateLegacyStoreNodes(ws) {
		t.Fatal("a plain workspace reports no change")
	}
	after, _ := json.Marshal(ws)
	if string(before) != string(after) {
		t.Fatalf("a plain workspace must be unchanged:\nbefore %s\nafter  %s", before, after)
	}
	if MigrateLegacyStoreNodes(nil) {
		t.Fatal("a nil workspace reports no change")
	}
}

// The migrated workspace is the same bytes however many times it is read: the
// continuity fence compares canonical JSON of the SQL row and the file.
func TestMigrateLegacyStoreNodes_IsIdempotentAsJSON(t *testing.T) {
	ws := newTestWorkspace("ws-idem", "Idempotent")
	ws.LegacyStoreNodes = []LegacyStoreNode{
		{ID: "store-1", CanvasNodeID: "canvas-1", AgentNodeID: "agent-node-1", BaseDir: "/data/out", Format: "text", AutoStore: true},
	}
	ws.Tasks = []Task{
		{ID: "t1", Description: "Named", ResultStorage: &ResultStorageConfig{Enabled: true, LegacyStoreNodeID: "canvas-1", Format: "text"}},
		{ID: "t2", Description: "Agent's", AssignedNodeID: "agent-node-1"},
	}
	if !MigrateLegacyStoreNodes(ws) {
		t.Fatal("first run should change the workspace")
	}
	first, err := json.Marshal(ws)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if MigrateLegacyStoreNodes(ws) {
		t.Fatal("second run should not change the workspace")
	}
	second, _ := json.Marshal(ws)
	if string(first) != string(second) {
		t.Fatalf("a second run changed the JSON:\nfirst  %s\nsecond %s", first, second)
	}
	if strings.Contains(string(first), "store_nodes") || strings.Contains(string(first), "store_node_id") {
		t.Fatalf("the migrated workspace must not write the legacy keys, got %s", first)
	}
}

// A workspace file written before store nodes were retired still carries the keys.
// Every decode path accepts them and rewrites them away.
func TestLegacyStoreNodes_AreAcceptedByEveryDecodePathAndRewrittenAway(t *testing.T) {
	legacy := newTestWorkspace("ws-legacy-file", "Legacy File")
	legacy.OwnerUserID = "local"
	legacy.LegacyStoreNodes = []LegacyStoreNode{
		{ID: "store-1", CanvasNodeID: "canvas-1", BaseDir: "/data/out", Format: "text", WriteMode: "overwrite", AutoCreateDir: true},
	}
	// The strict decoder also checks the ticket is a well-formed one.
	legacy.Tasks = []Task{{
		ID: "t1", WorkspaceID: legacy.ID, Description: "Daily report", Status: TaskStatusPending, CreatedAt: legacy.CreatedAt,
		ResultStorage: &ResultStorageConfig{Enabled: true, LegacyStoreNodeID: "store-1", Format: "text"},
	}}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(data), `"store_nodes"`) || !strings.Contains(string(data), `"store_node_id"`) {
		t.Fatalf("the fixture should carry the legacy keys, got %s", data)
	}

	assertMigrated := func(t *testing.T, ws *Workspace) {
		t.Helper()
		if len(ws.LegacyStoreNodes) != 0 {
			t.Fatalf("store nodes should be gone, found %d", len(ws.LegacyStoreNodes))
		}
		storage := ws.Tasks[0].ResultStorage
		if storage == nil || storage.LegacyStoreNodeID != "" || !storage.Enabled {
			t.Fatalf("the ticket should keep saving with no store reference, got %+v", storage)
		}
		if want := filepath.Clean("/data/out") + string(filepath.Separator); storage.FilePath != want {
			t.Fatalf("file path = %q, want %q", storage.FilePath, want)
		}
		out, err := json.Marshal(ws)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		if strings.Contains(string(out), "store_nodes") || strings.Contains(string(out), "store_node_id") {
			t.Fatalf("a migrated workspace must not write the legacy keys, got %s", out)
		}
	}

	t.Run("FromJSON", func(t *testing.T) {
		ws, err := FromJSON(data)
		if err != nil {
			t.Fatalf("FromJSON: %v", err)
		}
		assertMigrated(t, ws)
	})

	t.Run("FromJSONMetadata", func(t *testing.T) {
		ws, err := FromJSONMetadata(data)
		if err != nil {
			t.Fatalf("FromJSONMetadata: %v", err)
		}
		assertMigrated(t, ws)
	})

	t.Run("the strict continuity decoder accepts the keys", func(t *testing.T) {
		ws, err := decodePortableWorkspaceFile(data)
		if err != nil {
			t.Fatalf("decodePortableWorkspaceFile rejected a legacy file: %v", err)
		}
		if len(ws.LegacyStoreNodes) != 1 {
			t.Fatalf("the strict decoder should read the legacy store node, got %d", len(ws.LegacyStoreNodes))
		}
		MigrateLegacyStoreNodes(ws)
		assertMigrated(t, ws)
	})
}

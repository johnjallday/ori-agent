package workspace

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// These tests pin where a ticket's result is written TODAY when a store node is
// involved. They are characterization tests for the store-node retirement
// (tasks/tasks-retire-workspace-canvas.md, groups 1.6 and 7): the migration to
// plain file targets must write each case to the same place afterwards. They run
// the real AutoStoreResult against a real FileStore and look at the disk, so they
// pin the resolved path, not the helpers that build it.
//
// File names carry a timestamp, so a default name is matched by pattern.

var (
	storeNodePinTimestampedText = regexp.MustCompile(`^Daily_report_\d{8}-\d{6}\.txt$`)
	storeNodePinAgentBoundText  = regexp.MustCompile(`^task-11111111-\d{8}-\d{6}\.txt$`)
)

type storeNodePinFixture struct {
	store *FileStore
	ws    *Workspace
}

func newStoreNodePinFixture(t *testing.T) *storeNodePinFixture {
	t.Helper()
	store, ws := newStoreOperationTestWorkspace(t, "ws-store-pin", "Store Pin")
	return &storeNodePinFixture{store: store, ws: ws}
}

// run stores result for task the way the executor does: through AutoStoreResult
// with the workspace as the store resolver.
func (f *storeNodePinFixture) run(t *testing.T, task Task, result string) {
	t.Helper()
	task.WorkspaceID = f.ws.ID
	if err := f.ws.AddTask(task); err != nil {
		t.Fatalf("AddTask(%s): %v", task.ID, err)
	}
	AutoStoreResult(f.ws, &task, result, f.store)
}

func (f *storeNodePinFixture) outputsDir() string { return f.store.GetOutputsPath(f.ws.ID) }

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

func readStoreNodePinFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func requireStoreNodePinFiles(t *testing.T, root string, patterns ...*regexp.Regexp) []string {
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

// (a) A ticket that names a store node whose base directory is absolute.
func TestStoreNodePin_TicketNamingStoreNodeWritesUnderItsAbsoluteBaseDir(t *testing.T) {
	cases := []struct {
		name     string
		ref      string
		filePath string
		want     *regexp.Regexp
	}{
		{"default name, referenced by id", "store-abs", "", storeNodePinTimestampedText},
		{"default name, referenced by canvas node id", "canvas-store-abs", "", storeNodePinTimestampedText},
		{"explicit relative file path", "store-abs", "weekly/summary.txt", exactName("weekly/summary.txt")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newStoreNodePinFixture(t)
			base := t.TempDir()
			f.ws.StoreNodes = []StoreNode{{
				ID: "store-abs", CanvasNodeID: "canvas-store-abs", WorkspaceID: f.ws.ID, Name: "Reports",
				BaseDir: base, Format: "text", WriteMode: "overwrite", AutoCreateDir: true,
			}}

			f.run(t, Task{
				ID: "task-a", Description: "Daily report",
				ResultStorage: &ResultStorageConfig{Enabled: true, StoreNodeID: tc.ref, FilePath: tc.filePath, Format: "text"},
			}, "result body")

			files := requireStoreNodePinFiles(t, base, tc.want)
			if got := readStoreNodePinFile(t, filepath.Join(base, files[0])); got != "result body" {
				t.Fatalf("stored content = %q, want %q", got, "result body")
			}
			if left := filesUnder(t, f.outputsDir()); len(left) != 0 {
				t.Fatalf("nothing belongs in the workspace outputs folder, found %v", left)
			}
		})
	}
}

// The store node's own settings decide how a write behaves. The migration maps
// these onto a ticket's plain target, so today's behaviour is pinned here.
func TestStoreNodePin_StoreSettingsDecideTheWriteBehaviour(t *testing.T) {
	newNode := func(base, format, writeMode string, autoCreateDir bool) StoreNode {
		return StoreNode{
			ID: "store-mode", WorkspaceID: "ws-store-pin", Name: "Mode", BaseDir: base,
			Format: format, WriteMode: writeMode, AutoCreateDir: autoCreateDir,
		}
	}
	ticket := func(id string) Task {
		return Task{
			ID: id, Description: "Daily report",
			ResultStorage: &ResultStorageConfig{Enabled: true, StoreNodeID: "store-mode", FilePath: "out.txt", Format: "text"},
		}
	}

	t.Run("overwrite replaces the file", func(t *testing.T) {
		f := newStoreNodePinFixture(t)
		base := t.TempDir()
		f.ws.StoreNodes = []StoreNode{newNode(base, "text", "overwrite", true)}
		f.run(t, ticket("t1"), "first")
		f.run(t, ticket("t2"), "second")
		requireStoreNodePinFiles(t, base, exactName("out.txt"))
		if got := readStoreNodePinFile(t, filepath.Join(base, "out.txt")); got != "second" {
			t.Fatalf("content = %q, want the second write only", got)
		}
	})

	t.Run("append keeps both writes, newline separated, for a ticket that is not in append mode", func(t *testing.T) {
		f := newStoreNodePinFixture(t)
		base := t.TempDir()
		f.ws.StoreNodes = []StoreNode{newNode(base, "text", "append", true)}
		f.run(t, ticket("t1"), "first")
		f.run(t, ticket("t2"), "second")
		requireStoreNodePinFiles(t, base, exactName("out.txt"))
		if got := readStoreNodePinFile(t, filepath.Join(base, "out.txt")); got != "first\nsecond" {
			t.Fatalf("content = %q, want %q", got, "first\nsecond")
		}
	})

	t.Run("json format is pretty printed by the store", func(t *testing.T) {
		f := newStoreNodePinFixture(t)
		base := t.TempDir()
		f.ws.StoreNodes = []StoreNode{newNode(base, "json", "overwrite", true)}
		task := ticket("t1")
		task.ResultStorage.FilePath = "out.json"
		task.ResultStorage.Format = "json"
		f.run(t, task, "plain text result")
		got := readStoreNodePinFile(t, filepath.Join(base, "out.json"))
		if !strings.HasPrefix(got, "{\n  \"") {
			t.Fatalf("json should be indented two spaces by the store, got %q", got)
		}
	})

	t.Run("a missing directory is an error when auto_create_dir is off", func(t *testing.T) {
		f := newStoreNodePinFixture(t)
		base := filepath.Join(t.TempDir(), "not-created-yet")
		f.ws.StoreNodes = []StoreNode{newNode(base, "text", "overwrite", false)}
		f.run(t, ticket("t1"), "result body")
		if files := filesUnder(t, base); len(files) != 0 {
			t.Fatalf("nothing should be written without the directory, found %v", files)
		}
	})

	t.Run("an absolute file path is refused by the store", func(t *testing.T) {
		f := newStoreNodePinFixture(t)
		base := t.TempDir()
		elsewhere := t.TempDir()
		f.ws.StoreNodes = []StoreNode{newNode(base, "text", "overwrite", true)}
		task := ticket("t1")
		task.ResultStorage.FilePath = filepath.Join(elsewhere, "out.txt")
		f.run(t, task, "result body")
		if files := filesUnder(t, base); len(files) != 0 {
			t.Fatalf("store base should stay empty, found %v", files)
		}
		if files := filesUnder(t, elsewhere); len(files) != 0 {
			t.Fatalf("the absolute path must not be written either, found %v", files)
		}
	})
}

// (b) A store node on a workspace folder resolves under the workspace's files
// root. A ticket's own workspace_folder target is the plain equivalent, and it
// resolves to the same directory.
func TestStoreNodePin_StoreOnWorkspaceFolderMatchesAPlainWorkspaceFolderTarget(t *testing.T) {
	f := newStoreNodePinFixture(t)
	folder := filepath.Join("reports", "daily")
	f.ws.StoreNodes = []StoreNode{{
		ID: "store-folder", CanvasNodeID: "canvas-store-folder", WorkspaceID: f.ws.ID, Name: "Daily reports",
		StorageTarget: StorageTargetWorkspaceFolder, Folder: folder,
		Format: "text", WriteMode: "overwrite", AutoCreateDir: true,
	}}
	want := filepath.Join(f.store.GetFilesPath(f.ws.ID), folder)

	f.run(t, Task{
		ID: "via-store", Description: "Daily report",
		ResultStorage: &ResultStorageConfig{Enabled: true, StoreNodeID: "store-folder", Format: "text"},
	}, "via the store node")
	requireStoreNodePinFiles(t, want, storeNodePinTimestampedText)

	f.run(t, Task{
		ID: "plain", Description: "Daily report",
		ResultStorage: &ResultStorageConfig{Enabled: true, StorageTarget: StorageTargetWorkspaceFolder, Folder: folder, FilePath: "plain.txt", Format: "text"},
	}, "via a plain target")
	files := filesUnder(t, want)
	if len(files) != 2 || files[1] != "plain.txt" {
		t.Fatalf("both tickets should write under %s, found %v", want, files)
	}
	if left := filesUnder(t, f.outputsDir()); len(left) != 0 {
		t.Fatalf("nothing belongs in the workspace outputs folder, found %v", left)
	}
}

// (c) An agent-bound store with auto_store catches the results of tickets
// assigned to that agent node, unless the ticket has storage of its own.
func TestStoreNodePin_AgentBoundAutoStoreTakesTicketsWithoutStorageOfTheirOwn(t *testing.T) {
	f := newStoreNodePinFixture(t)
	base := t.TempDir()
	own := t.TempDir()
	f.ws.StoreNodes = []StoreNode{{
		ID: "store-agent", CanvasNodeID: "canvas-store-agent", AgentNodeID: "agent-node-1", WorkspaceID: f.ws.ID, Name: "Agent output",
		BaseDir: base, Format: "text", WriteMode: "overwrite", AutoCreateDir: true, AutoStore: true,
	}}

	// Ticket 1: no storage of its own. The agent's store takes it, named by task id.
	f.run(t, Task{ID: "11111111-aaaa", Description: "First", AssignedNodeID: "agent-node-1"}, "from ticket one")
	requireStoreNodePinFiles(t, base, storeNodePinAgentBoundText)

	// Ticket 2: its own enabled storage wins; the agent store is not touched.
	f.run(t, Task{
		ID: "22222222-bbbb", Description: "Second", AssignedNodeID: "agent-node-1",
		ResultStorage: &ResultStorageConfig{Enabled: true, FilePath: filepath.Join(own, "out.txt"), Format: "text"},
	}, "from ticket two")
	requireStoreNodePinFiles(t, own, exactName("out.txt"))
	requireStoreNodePinFiles(t, base, storeNodePinAgentBoundText)
	if got := readStoreNodePinFile(t, filepath.Join(own, "out.txt")); got != "from ticket two" {
		t.Fatalf("own storage content = %q", got)
	}

	// Disabled storage does not count as storage of its own: the agent store takes it.
	f.run(t, Task{
		ID: "33333333-cccc", Description: "Third", AssignedNodeID: "agent-node-1",
		ResultStorage: &ResultStorageConfig{Enabled: false, FilePath: filepath.Join(own, "ignored.txt")},
	}, "from ticket three")
	if files := filesUnder(t, base); len(files) != 2 {
		t.Fatalf("a disabled ticket storage should fall back to the agent store, found %v", files)
	}
	requireStoreNodePinFiles(t, own, exactName("out.txt"))

	// Tickets that are unassigned, or assigned to another agent, are not stored.
	f.run(t, Task{ID: "44444444-dddd", Description: "Fourth", AssignedNodeID: "unassigned"}, "unassigned")
	f.run(t, Task{ID: "55555555-eeee", Description: "Fifth", AssignedNodeID: "agent-node-2"}, "other agent")
	f.run(t, Task{ID: "66666666-ffff", Description: "Sixth"}, "no agent")
	if files := filesUnder(t, base); len(files) != 2 {
		t.Fatalf("only tickets assigned to the bound agent are stored, found %v", files)
	}
	if left := filesUnder(t, f.outputsDir()); len(left) != 0 {
		t.Fatalf("nothing belongs in the workspace outputs folder, found %v", left)
	}
}

func TestStoreNodePin_AgentBoundStoreWithAutoStoreOffStoresNothing(t *testing.T) {
	f := newStoreNodePinFixture(t)
	base := t.TempDir()
	f.ws.StoreNodes = []StoreNode{{
		ID: "store-agent", AgentNodeID: "agent-node-1", WorkspaceID: f.ws.ID, Name: "Agent output",
		BaseDir: base, Format: "text", WriteMode: "overwrite", AutoCreateDir: true, AutoStore: false,
	}}
	f.run(t, Task{ID: "11111111-aaaa", Description: "First", AssignedNodeID: "agent-node-1"}, "from ticket one")
	if files := filesUnder(t, base); len(files) != 0 {
		t.Fatalf("auto_store off means no write, found %v", files)
	}
}

// (d) A ticket that names a store node that no longer exists writes nothing, and
// does not fall back to the agent's store. A ticket whose storage is enabled with
// no store, no path and no folder writes to the workspace outputs folder: that is
// where a migrated ticket would land if the dangling id were only cleared.
func TestStoreNodePin_TicketNamingAMissingStoreNodeWritesNothing(t *testing.T) {
	f := newStoreNodePinFixture(t)
	base := t.TempDir()
	f.ws.StoreNodes = []StoreNode{{
		ID: "store-agent", AgentNodeID: "agent-node-1", WorkspaceID: f.ws.ID, Name: "Agent output",
		BaseDir: base, Format: "text", WriteMode: "overwrite", AutoCreateDir: true, AutoStore: true,
	}}

	f.run(t, Task{
		ID: "dangling", Description: "Daily report", AssignedNodeID: "agent-node-1",
		ResultStorage: &ResultStorageConfig{Enabled: true, StoreNodeID: "store-that-was-deleted", Format: "text"},
	}, "result body")
	if files := filesUnder(t, base); len(files) != 0 {
		t.Fatalf("a dangling store id must not fall back to the agent store, found %v", files)
	}
	if files := filesUnder(t, f.outputsDir()); len(files) != 0 {
		t.Fatalf("a dangling store id writes nothing, found %v in the outputs folder", files)
	}

	// The same ticket with only the dangling id cleared is a different ticket.
	f.run(t, Task{
		ID: "cleared", Description: "Daily report",
		ResultStorage: &ResultStorageConfig{Enabled: true, Format: "text"},
	}, "result body")
	requireStoreNodePinFiles(t, f.outputsDir(), storeNodePinTimestampedText)
}

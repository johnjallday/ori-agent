package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const outsideSecret = "OUTSIDE_ROOT_SECRET_MUST_NEVER_BE_READ"

// containedFixture is an approved folder beside a folder it must never reach.
func containedFixture(t *testing.T) (root, outside string) {
	t.Helper()
	base := t.TempDir()
	root, outside = filepath.Join(base, "approved"), filepath.Join(base, "outside")
	for _, dir := range []string{filepath.Join(root, "sub"), outside} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	for path, content := range map[string]string{
		filepath.Join(root, "notes.txt"):        "inside notes",
		filepath.Join(root, "sub", "deep.md"):   "inside deep",
		filepath.Join(root, ".env"):             "hidden",
		filepath.Join(root, "MEMORY.md"):        "managed memory",
		filepath.Join(root, "sub", "MEMORY.md"): "managed memory",
		filepath.Join(root, "workspace.json"):   `{"directory_references":[{"path":"/Users/someone/private"}]}`,
		filepath.Join(outside, "secret.txt"):    outsideSecret,
		filepath.Join(outside, "deep.md"):       outsideSecret,
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root, outside
}

func TestReadContainedFile_ReadsOnlyRegularFilesInsideTheApprovedFolder(t *testing.T) {
	root, outside := containedFixture(t)
	for rel, want := range map[string]string{"notes.txt": "inside notes", "sub/deep.md": "inside deep", "./sub/../notes.txt": "inside notes"} {
		got, err := ReadContainedFile(root, rel, 1024)
		if err != nil || string(got.Data) != want || got.Size != int64(len(want)) || got.ModTime.IsZero() {
			t.Fatalf("%s: %q %v", rel, got.Data, err)
		}
	}
	// No link is followed: not one that leaves the folder, and not one that stays
	// inside it, which could give a hidden file or one of Ori's records a name
	// that is not excluded.
	if err := os.Symlink("notes.txt", filepath.Join(root, "alias.txt")); err != nil {
		t.Skip("symbolic links are unavailable here")
	}
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git", "config"), []byte("hidden"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, target := range map[string]string{
		"escape.txt": filepath.Join(outside, "secret.txt"), "relative-escape.txt": "../outside/secret.txt", "escape-dir": outside,
		"visible-env.txt": ".env", "visible-memory.md": "MEMORY.md", "sub/up-to-records.json": "../workspace.json", "pub": ".git", "sub-alias": "sub",
	} {
		if err := os.Symlink(target, filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	for _, rel := range []string{
		"alias.txt", "escape.txt", "relative-escape.txt", "escape-dir/secret.txt",
		"visible-env.txt", "visible-memory.md", "sub/up-to-records.json", "pub/config", "sub-alias/deep.md",
	} {
		got, err := ReadContainedFile(root, rel, 1024)
		if !errors.Is(err, ErrSourceLinked) || len(got.Data) != 0 {
			t.Fatalf("%s was read through a link: %q %v", rel, got.Data, err)
		}
	}
	for rel, want := range map[string]error{
		"":                                   ErrSourceOutside,
		"../outside/secret.txt":              ErrSourceOutside,
		"sub/../../outside/x":                ErrSourceOutside,
		filepath.Join(outside, "secret.txt"): ErrSourceOutside,
		"/etc/hosts":                         ErrSourceOutside,
		"notes.txt\x00.md":                   ErrSourceOutside,
		".env":                               ErrSourceExcluded,
		".git/config":                        ErrSourceExcluded,
		"sub/.hidden/file.txt":               ErrSourceExcluded,
		"MEMORY.md":                          ErrSourceExcluded,
		"sub/memory.MD":                      ErrSourceExcluded,
		"workspace.json":                     ErrSourceExcluded,
		"sub/Agent_Settings.json":            ErrSourceExcluded,
		"sub/mcp_servers.json":               ErrSourceExcluded,
		"skills_state.json":                  ErrSourceExcluded,
		"missing.txt":                        ErrSourceMissing,
		"sub/missing/deep.md":                ErrSourceMissing,
		"notes.txt/inside-a-file":            ErrSourceMissing,
		"sub":                                ErrSourceNotRegular,
	} {
		got, err := ReadContainedFile(root, rel, 1024)
		if !errors.Is(err, want) || len(got.Data) != 0 {
			t.Fatalf("%q: got %q %v, want %v", rel, got.Data, err, want)
		}
		if err != nil && (strings.Contains(err.Error(), root) || strings.Contains(err.Error(), outside)) {
			t.Fatalf("%q: the refusal names a filesystem path: %v", rel, err)
		}
	}
	if _, err := ReadContainedFile(root, "notes.txt", 5); !errors.Is(err, ErrSourceTooLarge) {
		t.Fatalf("a file over the limit: %v", err)
	}
}

// An approved folder can be one of Ori's workspace folders, or hold others. A
// parent's folder holds its child workspaces; reading the parent must not reach
// a child's notes or files, the workspace's agent snapshots, or any other
// workspace's folder found below it. They are neither read nor listed.
func TestContainedSources_NeverReachAnotherWorkspaceOrOriFolders(t *testing.T) {
	root, _ := containedFixture(t) // the fixture's root holds a workspace record
	const private = "ANOTHER_WORKSPACE_MUST_NEVER_BE_READ"
	for rel, content := range map[string]string{
		"agents/scout/config.json":                     private,
		"sub-workspaces/album-2/workspace.json":        `{"id":"child"}`,
		"sub-workspaces/album-2/notes/plan.md":         private,
		"sub-workspaces/album-2/files/artwork.md":      private,
		"archive/old-project/workspace.json":           `{"id":"other"}`,
		"archive/old-project/notes/plan.md":            private,
		"archive/readme.txt":                           "an ordinary file beside another workspace",
		"sub/agents/readme.txt":                        "an ordinary folder that happens to be called agents",
		"sub/sub-workspaces/readme.txt":                "likewise",
		"notes/own-note.md":                            "this workspace's own note",
		"outputs/result.md":                            "this workspace's own output",
		"Sub-Workspaces-notes/readme.txt":              "a different name",
		"archive/old-project/sub-workspaces/x/deep.md": private,
	} {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, rel := range []string{
		"agents/scout/config.json", "Agents/scout/config.json", "sub-workspaces/album-2/notes/plan.md", "sub-workspaces/album-2/files/artwork.md",
		"archive/old-project/notes/plan.md", "archive/old-project/sub-workspaces/x/deep.md",
	} {
		got, err := ReadContainedFile(root, rel, 1024)
		if !errors.Is(err, ErrSourceExcluded) || len(got.Data) != 0 {
			t.Fatalf("%s was read from another workspace or an Ori folder: %q %v", rel, got.Data, err)
		}
	}
	for rel, want := range map[string]string{
		"archive/readme.txt": "an ordinary file beside another workspace", "sub/agents/readme.txt": "an ordinary folder that happens to be called agents",
		"sub/sub-workspaces/readme.txt": "likewise", "notes/own-note.md": "this workspace's own note", "outputs/result.md": "this workspace's own output",
		"Sub-Workspaces-notes/readme.txt": "a different name",
	} {
		if got, err := ReadContainedFile(root, rel, 1024); err != nil || string(got.Data) != want {
			t.Fatalf("%s: %q %v", rel, got.Data, err)
		}
	}
	listing, err := ListContainedEntries(root, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]bool{}
	for _, entry := range listing.Entries {
		listed[entry.RelativePath] = true
	}
	for _, absent := range []string{"agents", "agents/scout", "sub-workspaces", "sub-workspaces/album-2", "archive/old-project", "archive/old-project/notes"} {
		if listed[absent] {
			t.Fatalf("%s was listed: %v", absent, listed)
		}
	}
	for _, present := range []string{"archive", "archive/readme.txt", "sub/agents", "sub/agents/readme.txt", "notes/own-note.md", "outputs/result.md"} {
		if !listed[present] {
			t.Fatalf("%s is missing from the listing: %v", present, listed)
		}
	}
	for _, start := range []string{"agents", "sub-workspaces", "sub-workspaces/album-2", "archive/old-project", "archive/old-project/notes"} {
		if _, err := ListContainedEntries(root, start, 1); !errors.Is(err, ErrSourceExcluded) {
			t.Fatalf("list %s: %v", start, err)
		}
	}
	// A folder that is not a workspace folder keeps its own "agents" directory.
	plain := t.TempDir()
	if err := os.MkdirAll(filepath.Join(plain, "agents"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plain, "agents", "roster.txt"), []byte("a user's own file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadContainedFile(plain, "agents/roster.txt", 1024); err != nil || string(got.Data) != "a user's own file" {
		t.Fatalf("an ordinary agents folder: %q %v", got.Data, err)
	}
}

func TestReadContainedFile_RefusesALinkedOrMissingFolder(t *testing.T) {
	root, outside := containedFixture(t)
	link := filepath.Join(filepath.Dir(root), "linked-folder")
	if err := os.Symlink(outside, link); err != nil {
		t.Skip("symbolic links are unavailable here")
	}
	if got, err := ReadContainedFile(link, "secret.txt", 1024); !errors.Is(err, ErrSourceOutside) || len(got.Data) != 0 {
		t.Fatalf("a folder that is itself a link was read: %q %v", got.Data, err)
	}
	if _, err := ReadContainedFile(filepath.Join(root, "gone"), "notes.txt", 1024); !errors.Is(err, ErrSourceMissing) {
		t.Fatalf("a removed folder: %v", err)
	}
	for _, bad := range []string{"relative/root", ""} {
		if _, err := ReadContainedFile(bad, "notes.txt", 1024); err == nil {
			t.Fatalf("root %q was accepted", bad)
		}
	}
	if _, err := ReadContainedFile(root, "notes.txt", 0); err == nil {
		t.Fatal("a read without a size limit was accepted")
	}
}

// While a file and a folder on the way to it are swapped back and forth for
// links that point outside, no read may ever return the outside content. A read
// may fail; it may not read a substitute.
func TestReadContainedFile_NeverReadsASubstituteDuringReplacement(t *testing.T) {
	root, outside := containedFixture(t)
	if err := os.Symlink("notes.txt", filepath.Join(root, "probe")); err != nil {
		t.Skip("symbolic links are unavailable here")
	}
	_ = os.Remove(filepath.Join(root, "probe"))
	file, folder := filepath.Join(root, "notes.txt"), filepath.Join(root, "sub")
	stop := make(chan struct{})
	var swaps sync.WaitGroup
	swaps.Add(1)
	go func() {
		defer swaps.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			_ = os.Remove(file)
			_ = os.RemoveAll(folder)
			if i%2 == 0 {
				_ = os.Symlink(filepath.Join(outside, "secret.txt"), file)
				_ = os.Symlink(outside, folder)
			} else {
				_ = os.WriteFile(file, []byte("inside notes"), 0o600)
				_ = os.Mkdir(folder, 0o750)
				_ = os.WriteFile(filepath.Join(folder, "deep.md"), []byte("inside deep"), 0o600)
			}
		}
	}()
	reads := 0
	for i := 0; i < 4000; i++ {
		for _, rel := range []string{"notes.txt", "sub/deep.md"} {
			got, err := ReadContainedFile(root, rel, 1024)
			if strings.Contains(string(got.Data), outsideSecret) {
				close(stop)
				swaps.Wait()
				t.Fatalf("read %d of %s returned content from outside the approved folder (err=%v)", i, rel, err)
			}
			if err == nil {
				reads++
				if text := string(got.Data); text != "inside notes" && text != "inside deep" && text != "" {
					t.Errorf("unexpected content %q", text)
				}
			}
		}
	}
	close(stop)
	swaps.Wait()
	t.Logf("%d of 8000 reads succeeded while the targets were being replaced; none read outside content", reads)
}

// The same while a file and a folder are swapped for links that stay inside the
// approved folder but point at what is excluded from it: a hidden file and a
// hidden folder. No read may return the excluded content under a visible name.
func TestReadContainedFile_NeverReadsAnExcludedEntryThroughAnInsideLink(t *testing.T) {
	root, _ := containedFixture(t)
	const excluded = "EXCLUDED_ENTRY_MUST_NEVER_BE_READ"
	hidden := filepath.Join(root, ".private")
	if err := os.Mkdir(hidden, 0o750); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(root, ".env"), filepath.Join(hidden, "deep.md")} {
		if err := os.WriteFile(path, []byte(excluded), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(".env", filepath.Join(root, "probe")); err != nil {
		t.Skip("symbolic links are unavailable here")
	}
	_ = os.Remove(filepath.Join(root, "probe"))
	file, folder := filepath.Join(root, "notes.txt"), filepath.Join(root, "sub")
	stop := make(chan struct{})
	var swaps sync.WaitGroup
	swaps.Add(1)
	go func() {
		defer swaps.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			_ = os.Remove(file)
			_ = os.RemoveAll(folder)
			if i%2 == 0 {
				_ = os.Symlink(".env", file)
				_ = os.Symlink(".private", folder)
			} else {
				_ = os.WriteFile(file, []byte("inside notes"), 0o600)
				_ = os.Mkdir(folder, 0o750)
				_ = os.WriteFile(filepath.Join(folder, "deep.md"), []byte("inside deep"), 0o600)
			}
		}
	}()
	reads := 0
	for i := 0; i < 4000; i++ {
		for _, rel := range []string{"notes.txt", "sub/deep.md"} {
			got, err := ReadContainedFile(root, rel, 1024)
			if strings.Contains(string(got.Data), excluded) {
				close(stop)
				swaps.Wait()
				t.Fatalf("read %d of %s returned an excluded entry's content (err=%v)", i, rel, err)
			}
			if err == nil {
				reads++
			}
		}
		if listing, err := ListContainedEntries(root, "sub", 1); err == nil {
			for _, entry := range listing.Entries {
				if entry.Size == int64(len(excluded)) {
					close(stop)
					swaps.Wait()
					t.Fatalf("listing %d of sub described the hidden folder's file", i)
				}
			}
		}
	}
	close(stop)
	swaps.Wait()
	t.Logf("%d of 8000 reads succeeded while the targets were being replaced by inside links; none read an excluded entry", reads)
}

func TestListContainedEntries_NamesOnlyWithinBoundsAndNoFollowedLinks(t *testing.T) {
	root, outside := containedFixture(t)
	if err := os.Symlink(outside, filepath.Join(root, "linked-out")); err != nil {
		t.Skip("symbolic links are unavailable here")
	}
	if err := os.MkdirAll(filepath.Join(root, "a", "b", "c", "d"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a", "b", "c", "d", "too-deep.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	listing, err := ListContainedEntries(root, "", 0)
	if err != nil || listing.Truncated {
		t.Fatal(err, listing.Truncated)
	}
	kinds := map[string]string{}
	for _, entry := range listing.Entries {
		kinds[entry.RelativePath] = entry.Kind
	}
	for path, kind := range map[string]string{"notes.txt": "file", "sub": "folder", "sub/deep.md": "file", "linked-out": "link", "a/b/c": "folder"} {
		if kinds[path] != kind {
			t.Fatalf("%s: %q in %v", path, kinds[path], kinds)
		}
	}
	for _, absent := range []string{".env", "MEMORY.md", "sub/MEMORY.md", "workspace.json", "linked-out/secret.txt", "a/b/c/d", "a/b/c/d/too-deep.txt"} {
		if _, listed := kinds[absent]; listed {
			t.Fatalf("%s must not be listed: %v", absent, kinds)
		}
	}
	// A link that stays inside the folder is not a way into a hidden one either.
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(".git", filepath.Join(root, "pub")); err != nil {
		t.Fatal(err)
	}
	for rel, want := range map[string]error{"../outside": ErrSourceOutside, "linked-out": ErrSourceLinked, "pub": ErrSourceLinked, "linked-out/x": ErrSourceLinked, ".git": ErrSourceExcluded, "missing": ErrSourceMissing, "notes.txt": ErrSourceNotRegular} {
		if _, err := ListContainedEntries(root, rel, 1); !errors.Is(err, want) {
			t.Fatalf("list %q: %v, want %v", rel, err, want)
		}
	}
	many := filepath.Join(root, "many")
	if err := os.Mkdir(many, 0o750); err != nil {
		t.Fatal(err)
	}
	for i := range DirectoryListMaxEntries + 20 {
		if err := os.WriteFile(filepath.Join(many, "f"+strings.Repeat("0", 3)+string(rune('a'+i%26))+strings.Repeat("x", i/26)), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	bounded, err := ListContainedEntries(root, "many", 1)
	if err != nil || !bounded.Truncated || len(bounded.Entries) != DirectoryListMaxEntries {
		t.Fatalf("entry bound: %d truncated=%v err=%v", len(bounded.Entries), bounded.Truncated, err)
	}
}

func TestProjectEntrySource_ExactEntryOnlyAndNeverACapabilityFolder(t *testing.T) {
	workspaceRoot := t.TempDir()
	if err := os.Mkdir(filepath.Join(workspaceRoot, "song"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspaceRoot, "song", "main.rpp"), []byte("managed project"), 0o600); err != nil {
		t.Fatal(err)
	}
	managed := NewWorkspace(CreateWorkspaceParams{Name: "Managed"})
	managed.ID, managed.ProjectPath, managed.SharedData = "managed-workspace", "song", map[string]any{}
	if err := SetProjectEntryPath(managed.SharedData, "main.rpp"); err != nil {
		t.Fatal(err)
	}
	root, rel, err := ProjectEntrySource(managed, workspaceRoot)
	if err != nil || root != filepath.Clean(workspaceRoot) || rel != "song/main.rpp" {
		t.Fatalf("managed entry: %q %q %v", root, rel, err)
	}
	if got, err := ReadContainedFile(root, rel, 1024); err != nil || string(got.Data) != "managed project" {
		t.Fatalf("managed read: %q %v", got.Data, err)
	}

	externalRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(externalRoot, "Existing.RPP"), []byte("external project"), 0o600); err != nil {
		t.Fatal(err)
	}
	external := NewWorkspace(CreateWorkspaceParams{Name: "External"})
	external.ID, external.SharedData = "external-workspace", map[string]any{}
	if err := external.AddDirectoryReference(DirectoryReference{ID: "reference-1", Name: "Existing", Path: externalRoot}); err != nil {
		t.Fatal(err)
	}
	if err := SetProjectEntryLocator(external.SharedData, ProjectEntryLocator{SchemaVersion: ProjectEntryLocatorSchemaVersion, Kind: ProjectEntryDirectoryReference, DirectoryReferenceID: "reference-1", RelativePath: "Existing.RPP"}); err != nil {
		t.Fatal(err)
	}
	root, rel, err = ProjectEntrySource(external, workspaceRoot)
	if err != nil || root != filepath.Clean(externalRoot) || rel != "Existing.RPP" {
		t.Fatalf("external entry: %q %q %v", root, rel, err)
	}
	// A folder a capability owns is never an ordinary file source.
	external.DirectoryReferences[0].Purpose = "sample_library"
	if _, _, err := ProjectEntrySource(external, workspaceRoot); err == nil {
		t.Fatal("a capability-owned folder was offered as a project entry source")
	}
	external.DirectoryReferences[0].Purpose = ""
	external.DirectoryReferences = nil
	if _, _, err := ProjectEntrySource(external, workspaceRoot); err == nil {
		t.Fatal("a removed folder reference still resolved")
	}
	if _, _, err := ProjectEntrySource(NewWorkspace(CreateWorkspaceParams{Name: "None"}), workspaceRoot); err == nil {
		t.Fatal("a workspace without a project entry resolved one")
	}
}

func TestAttachmentSourcePath_OnlyAStoredWorkspaceFile(t *testing.T) {
	for name, test := range map[string]struct {
		meta *AttachmentFileMeta
		want string
	}{
		"stored file":          {&AttachmentFileMeta{RelativePath: "docs/plan.pdf"}, "docs/plan.pdf"},
		"stored file by URL":   {&AttachmentFileMeta{URL: "/api/workspaces/ws-1/files/docs/plan%20v2.pdf"}, "docs/plan v2.pdf"},
		"another workspace":    {&AttachmentFileMeta{URL: "/api/workspaces/ws-2/files/docs/plan.pdf"}, ""},
		"remembered location":  {&AttachmentFileMeta{OriginalPath: "/Users/me/Documents/plan.pdf"}, ""},
		"file link":            {&AttachmentFileMeta{URL: "file:///Users/me/Documents/plan.pdf"}, ""},
		"climbing stored path": {&AttachmentFileMeta{RelativePath: "../../etc/hosts"}, ""},
		"no file":              {nil, ""},
	} {
		if got := AttachmentSourcePath("ws-1", test.meta); got != test.want {
			t.Errorf("%s: %q, want %q", name, got, test.want)
		}
	}
}

package projectlibrary

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
	"github.com/johnjallday/ori-agent/internal/pathselection"
	"github.com/johnjallday/ori-agent/internal/projectconnection"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// These are foundation/fixture contracts, not a mock implementation of the
// library. Product-path tests will reuse the disposable tree and synthetic rows
// after the library store and reviewed discovery API exist.
type musicTree struct {
	root, empty, single, alternate, ableton, logic string
}

func newMusicTree(t *testing.T) musicTree {
	t.Helper()
	root := filepath.Join(t.TempDir(), "Music")
	tree := musicTree{
		root: root, empty: filepath.Join(root, "Empty"),
		single: filepath.Join(root, "Single"), alternate: filepath.Join(root, "Alternates"),
		ableton: filepath.Join(root, "Ableton"), logic: filepath.Join(root, "Logic"),
	}
	for _, dir := range []string{tree.empty, tree.single, tree.alternate, tree.ableton, tree.logic} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	for relative, contents := range map[string]string{
		"Single/Song.rpp":           "fixture rpp; never parse this content",
		"Alternates/Take A.rpp":     "first alternate",
		"Alternates/Take B.RPP":     "second alternate",
		"Alternates/Take B.rpp-bak": "backup is not a new song",
		"Ableton/Arrangement.als":   "unsupported DAW",
		"Logic/Arrangement.logicx":  "unsupported DAW",
	} {
		path := filepath.Join(root, relative)
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return tree
}

func fileDigest(t *testing.T, name string) [32]byte {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return sha256.Sum256(data)
}

func TestLibraryFixtures_EmptySingleMixedDAWAndAlternates(t *testing.T) {
	tree := newMusicTree(t)
	original := fileDigest(t, filepath.Join(tree.single, "Song.rpp"))
	result, err := folderdigest.Scan(tree.root, folderdigest.Options{})
	if err != nil || result.Partial || len(result.Subfolders()) != 5 {
		t.Fatalf("bounded root scan: %+v, %v", result, err)
	}
	byName := make(map[string]folderdigest.Candidate)
	for _, candidate := range result.Subfolders() {
		byName[candidate.Name] = candidate
	}
	if byName["Empty"].FileCount != 0 || byName["Single"].Marker == nil ||
		byName["Ableton"].Marker == nil || byName["Logic"].Marker == nil {
		t.Fatalf("fixture markers: %#v", byName)
	}
	decl := &projecttemplates.AttachExistingDeclaration{EntryExtensions: []string{".rpp"}}
	selected, err := projectconnection.ScanExistingProject(tree.single, decl)
	if err != nil || len(selected.Candidates) != 1 || selected.Candidates[0] != "Song.rpp" {
		t.Fatalf("single project selection: %+v, %v", selected, err)
	}
	alternates, err := projectconnection.ScanExistingProject(tree.alternate, decl)
	if err != nil || len(alternates.Candidates) != 2 {
		t.Fatalf("alternates should exclude .rpp-bak: %+v, %v", alternates, err)
	}
	if selected, err := projectconnection.SelectProjectEntry("", alternates.Candidates); err != nil || selected != "" {
		t.Fatalf("ambiguous project was selected: %q, %v", selected, err)
	}
	if selected, err := projectconnection.SelectProjectEntry("Take B.RPP", alternates.Candidates); err != nil || selected != "Take B.RPP" {
		t.Fatalf("explicit selection: %q, %v", selected, err)
	}
	for _, dir := range []string{tree.empty, tree.ableton, tree.logic} {
		if _, err := projectconnection.ScanExistingProject(dir, decl); !errors.Is(err, projectconnection.ErrNoProjectEntry) {
			t.Fatalf("%s should be catalog-only or empty: %v", dir, err)
		}
	}
	if after := fileDigest(t, filepath.Join(tree.single, "Song.rpp")); after != original {
		t.Fatal("metadata inspection changed a source file")
	}
}

type fixtureOwnerStore struct{ workspace.Store }

func (fixtureOwnerStore) GetFolderPath(string) (string, error) { return "", os.ErrNotExist }

func TestLibraryFixtures_ExactLinkAndForeignSameNameCannotClaimFolder(t *testing.T) {
	tree := newMusicTree(t)
	store := workspace.NewInMemoryStore()
	key := workspace.AssistantProgramKey{OwnerUserID: "local", PluginID: "music-project-management", ProgramID: "music-producer-assistant"}
	home := &workspace.Workspace{ID: "home", Name: "Music Production Home", FolderSlug: "music-home", OwnerUserID: "local"}
	linked := &workspace.Workspace{ID: "linked", Name: "Same title", FolderSlug: "linked-song", OwnerUserID: "local"}
	foreign := &workspace.Workspace{ID: "foreign", Name: "Same title", FolderSlug: "foreign-song", OwnerUserID: "other"}
	home.SetAssistantProgramState(&workspace.AssistantProgramState{
		SchemaVersion: workspace.AssistantProgramStateSchemaVersion, Key: key, LinkedProjectIDs: []string{linked.ID},
	})
	linked.SetAssistantProjectLink(&workspace.AssistantProjectLink{
		ID: workspace.AssistantProjectLinkID(home.ID, linked.ID), StationWorkspaceID: home.ID, Key: key,
	})
	if err := projectconnection.RecordAttachedProject(linked, "Linked", tree.single, "Song.rpp", "existing-project"); err != nil {
		t.Fatal(err)
	}
	if err := projectconnection.RecordAttachedProject(foreign, "Other owner", tree.alternate, "Take A.rpp", "foreign-project"); err != nil {
		t.Fatal(err)
	}
	for _, ws := range []*workspace.Workspace{home, linked, foreign} {
		if err := store.Save(ws); err != nil {
			t.Fatal(err)
		}
	}
	projects, err := workspace.NewAssistantPortfolioService(store).List(home.ID)
	if err != nil || len(projects) != 1 || projects[0].ProjectWorkspaceID != linked.ID {
		t.Fatalf("same-name foreign workspace was adopted: %+v, %v", projects, err)
	}
	owner, err := projectconnection.FindFolderOwner(fixtureOwnerStore{store}, tree.single, "")
	if err != nil || owner == nil || owner.WorkspaceID != linked.ID {
		t.Fatalf("exact folder owner: %+v, %v", owner, err)
	}
	foreignOwner, err := projectconnection.FindFolderOwner(fixtureOwnerStore{store}, tree.alternate, "")
	if err != nil || foreignOwner == nil || foreignOwner.WorkspaceID != foreign.ID {
		t.Fatalf("foreign folder owner must block adoption: %+v, %v", foreignOwner, err)
	}
}

func TestLibraryFixtures_OverlappingRootsAndOfflineDriveAreNotEmptyScans(t *testing.T) {
	tree := newMusicTree(t)
	outer, err := folderdigest.Scan(tree.root, folderdigest.Options{})
	if err != nil || len(outer.Subfolders()) != 5 {
		t.Fatalf("outer root: %+v, %v", outer, err)
	}
	inner, err := folderdigest.Scan(tree.single, folderdigest.Options{})
	if err != nil || inner.RootCandidate().Marker == nil {
		t.Fatalf("overlapping root: %+v, %v", inner, err)
	}
	// The library reconciler must reuse the same folder evidence, not create a
	// second song just because this directory was scanned from a second root.
	if inner.Root != filepath.Join(outer.Root, "Single") {
		t.Fatalf("same folder has different canonical evidence: %q vs %q", inner.Root, outer.Root)
	}
	if err := os.Rename(tree.root, filepath.Join(filepath.Dir(tree.root), "Disconnected")); err != nil {
		t.Fatal(err)
	}
	if _, err := folderdigest.Scan(tree.root, folderdigest.Options{}); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing root must be unavailable, not an empty inventory: %v", err)
	}
}

// A read-only exercise of the existing creator boundary. This uses a native-
// picker-shaped token for a test-only path; it must not be reused for an
// approved discovery root or a browser-supplied child path.
func TestLibraryFixture_ExistingProjectPreviewIsInert(t *testing.T) {
	tree := newMusicTree(t)
	folders, err := workspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = folders.Close() })
	store := workspace.NewSyncStore(workspace.NewInMemoryStore(), folders)
	selections := pathselection.NewStore()
	token, err := selections.Issue(tree.single)
	if err != nil {
		t.Fatal(err)
	}
	template := projecttemplates.Template{
		ID: "plugin:fixture:reaper-song", Name: "Fixture REAPER Song",
		PluginOwner:      &workspace.PluginTemplateOwner{PluginID: "fixture", PluginVersion: "1.0.0", BlueprintID: "reaper-song", BlueprintVersion: 1},
		AssistantProgram: &workspace.AssistantProgramDeclaration{ID: "fixture-home", StationName: "Fixture Home"},
		ProjectConnection: &projecttemplates.ProjectConnectionDeclaration{
			SchemaVersion:  projecttemplates.ProjectConnectionSchemaVersion,
			SupportedModes: []projecttemplates.ProjectConnectionMode{projecttemplates.ProjectConnectionExistingProject},
			AttachExisting: &projecttemplates.AttachExistingDeclaration{EntryExtensions: []string{".rpp"}},
		},
	}
	before := fileDigest(t, filepath.Join(tree.single, "Song.rpp"))
	preview, err := projectconnection.NewService(store, selections).Preview(t.Context(), projectconnection.Scope{
		OwnerUserID: "local", RunID: "fixture-run", Template: template,
	}, projectconnection.Request{
		ModeID: projecttemplates.ProjectConnectionExistingProject, SelectionToken: token, WorkspaceName: "Chosen Song",
	})
	if err != nil || preview.Projection.EntryName != "Song.rpp" || !preview.Projection.HomeWillBeCreated {
		t.Fatalf("inert existing-project preview: %+v, %v", preview.Projection, err)
	}
	ids, err := store.List()
	if err != nil || len(ids) != 0 {
		t.Fatalf("preview created workspace(s): %v, %v", ids, err)
	}
	if after := fileDigest(t, filepath.Join(tree.single, "Song.rpp")); after != before {
		t.Fatal("preview changed the source project")
	}
}

type syntheticCatalogRow struct {
	ID, Name, Format string
	Edited           bool
}

func syntheticCatalogRows(n int) []syntheticCatalogRow {
	rows := make([]syntheticCatalogRow, 0, n)
	for i := range n {
		format := "rpp"
		if i%5 == 0 {
			format = "als"
		}
		rows = append(rows, syntheticCatalogRow{
			ID: fmt.Sprintf("entry-%04d", i), Name: fmt.Sprintf("Song %04d", i),
			Format: format, Edited: i%20 == 0,
		})
	}
	return rows
}

func TestLibraryFixture_HasScaleAndEditedRecordsForA4(t *testing.T) {
	rows := syntheticCatalogRows(1000)
	edited := 0
	seen := make(map[string]bool, len(rows))
	for _, row := range rows {
		if seen[row.ID] {
			t.Fatalf("duplicate catalog identity %s", row.ID)
		}
		seen[row.ID] = true
		if row.Edited {
			edited++
		}
	}
	if edited <= 32 || len(rows) < 1000 {
		t.Fatalf("fixture scale: %d entries, %d edits", len(rows), edited)
	}
}

package resourcecatalog

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const archivePath = "/Users/me/Music/Templates Archive"

func sourceNamed(view OwnerView, id string) *SourceView {
	for index := range view.Sources {
		if view.Sources[index].SourceID == id {
			return &view.Sources[index]
		}
	}
	return nil
}

// A folder the owner adds is only listed until the owner approves it in a
// review; then the provider is told its path on every page it reads.
func TestOwnerFolders_AreReadOnlyAfterApprovalAndNamedOnEveryPage(t *testing.T) {
	f := newCatalogFixture(t)
	view := f.overview(t)
	if !view.CanAddFolder || len(view.FolderCategories) != 2 || view.FolderCategories[0].ID != "templates" || view.FolderCategories[1].ID != "scripts" {
		t.Fatalf("folder categories = %+v (can add %v)", view.FolderCategories, view.CanAddFolder)
	}
	folder, err := f.service.AddFolder("local", f.studio.ID, "templates", archivePath)
	if err != nil {
		t.Fatal(err)
	}
	if folder.ID != FolderID("templates", archivePath) {
		t.Fatalf("folder = %+v", folder)
	}
	f.provider.set(func(p *fakeProvider) {
		p.entries[folder.ID] = []InventoryEntry{
			{NativeID: "Live/Drums.RPP", Kind: "project_template", Name: "Drums", Facts: []Fact{}},
			{NativeID: "Vocals.RPP", Kind: "project_template", Name: "Vocals", Facts: []Fact{}},
		}
	})
	pending := sourceNamed(f.overview(t), folder.ID)
	if pending == nil || !pending.Folder || pending.Approved || pending.Location != archivePath || pending.Label != "Templates Archive" {
		t.Fatalf("an added folder is not listed as waiting for review: %+v", pending)
	}
	if len(f.provider.calls) != 0 || len(f.provider.sourceInputs) != 0 {
		t.Fatal("adding a folder called the provider")
	}

	f.approveAll(t)
	if input := f.provider.sourceInputs[0]; !strings.Contains(input, `"path":"`+archivePath+`"`) || !strings.Contains(input, `"id":"`+folder.ID+`"`) {
		t.Fatalf("the sources input did not carry the folder: %s", input)
	}
	pages := 0
	for _, call := range f.provider.calls {
		switch {
		case call.SourceID == folder.ID:
			pages++
			if call.Folder == nil || call.Folder.Path != archivePath || call.Folder.Category != "templates" {
				t.Fatalf("a folder page did not name its folder: %+v", call)
			}
		case call.Folder != nil:
			t.Fatalf("a provider source was sent a folder: %+v", call)
		}
	}
	if pages != 2 {
		t.Fatalf("folder pages = %d, want 2 (one entry per page)", pages)
	}
	approved := sourceNamed(f.overview(t), folder.ID)
	if approved == nil || !approved.Folder || !approved.Approved || approved.Label != "Folder Templates Archive" {
		t.Fatalf("approved folder = %+v", approved)
	}
	found := 0
	for _, row := range f.rows(t, Query{Category: "templates"}) {
		if row.SourceID == folder.ID && row.State == EntryPresent {
			found++
		}
	}
	if found != 2 || categoryNamed(f.overview(t), "templates").Counts.Present != 4 {
		t.Fatalf("folder entries = %d, templates = %+v", found, categoryNamed(f.overview(t), "templates"))
	}
}

// Removing a folder withdraws it at once and forgets its path; what it found
// stays readable as withdrawn, and the same folder added again finds it.
func TestOwnerFolders_RemovingWithdrawsKeepsHistoryAndReAddingFindsIt(t *testing.T) {
	f := newCatalogFixture(t)
	folder, err := f.service.AddFolder("local", f.studio.ID, "templates", archivePath)
	if err != nil {
		t.Fatal(err)
	}
	f.provider.set(func(p *fakeProvider) {
		p.entries[folder.ID] = []InventoryEntry{{NativeID: "Drums.RPP", Kind: "project_template", Name: "Drums", Facts: []Fact{}}}
	})
	f.approveAll(t)
	view, err := f.service.RemoveFolder("local", f.studio.ID, folder.ID)
	if err != nil {
		t.Fatal(err)
	}
	removed := sourceNamed(view, folder.ID)
	if removed == nil || !removed.Withdrawn || removed.Folder || removed.Approved {
		t.Fatalf("a removed folder = %+v", removed)
	}
	if templates := categoryNamed(view, "templates"); templates.Counts.Withdrawn != 1 || templates.Counts.Present != 2 {
		t.Fatalf("templates after removal = %+v", templates.Counts)
	}
	folders, err := readFolders(f.studioRef(t).paths)
	if err != nil || len(folders) != 0 {
		t.Fatalf("folders after removal = %+v, %v", folders, err)
	}
	f.provider.set(func(p *fakeProvider) { p.calls = nil })
	f.refresh(t, "after-removal")
	for _, call := range f.provider.calls {
		if call.SourceID == folder.ID {
			t.Fatal("a removed folder was read again")
		}
	}
	if _, err := f.service.RemoveFolder("local", f.studio.ID, folder.ID); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("removing it twice = %v", err)
	}

	again, err := f.service.AddFolder("local", f.studio.ID, "templates", archivePath)
	if err != nil || again.ID != folder.ID {
		t.Fatalf("re-added folder = %+v, %v", again, err)
	}
	review, err := f.service.ReviewSources(context.Background(), "local", f.studio.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range review.Sources {
		if source.SourceID == folder.ID && (!source.Withdrawn || source.Approved) {
			t.Fatalf("a re-added folder skipped its review: %+v", source)
		}
	}
}

func TestOwnerFolders_RemovingOneDuringAReadStopsIt(t *testing.T) {
	f := newCatalogFixture(t)
	folder, err := f.service.AddFolder("local", f.studio.ID, "scripts", "/Users/me/Scripts")
	if err != nil {
		t.Fatal(err)
	}
	f.provider.set(func(p *fakeProvider) {
		p.entries[folder.ID] = []InventoryEntry{
			{NativeID: "a.lua", Kind: "script", Name: "a", Facts: []Fact{}},
			{NativeID: "b.lua", Kind: "script", Name: "b", Facts: []Fact{}},
		}
		pages := 0
		p.hook = func(_ context.Context, request InventoryRequest) error {
			if request.SourceID != folder.ID {
				return nil
			}
			if pages++; pages == 1 {
				_, err := f.service.RemoveFolder("local", f.studio.ID, folder.ID)
				return err
			}
			return nil
		}
	})
	op := f.approveAll(t)
	for _, source := range op.Sources {
		if source.SourceID == folder.ID && source.State != OutcomeWithdrawn {
			t.Fatalf("the removed folder's read ended %q", source.State)
		}
	}
	for _, row := range f.rows(t, Query{Category: "scripts"}) {
		if row.SourceID == folder.ID {
			t.Fatalf("a page of a removed folder was published: %+v", row)
		}
	}
}

func TestOwnerFolders_RefuseWhatTheyCannotHold(t *testing.T) {
	f := newCatalogFixture(t)
	for name, attempt := range map[string][2]string{
		"category without folders": {"audio_plugins", "/Users/me/Plugins"},
		"unknown category":         {"samples", "/Users/me/Samples"},
		"relative path":            {"templates", "Music/Templates"},
		"unclean path":             {"templates", "/Users/me/Music/../Music"},
		"two lines":                {"templates", "/Users/me/a\nb"},
	} {
		if _, err := f.service.AddFolder("local", f.studio.ID, attempt[0], attempt[1]); !errors.Is(err, ErrInvalidRequest) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if _, err := f.service.AddFolder("someone-else", f.studio.ID, "templates", archivePath); !errors.Is(err, ErrNotFound) {
		t.Errorf("another owner: err = %v", err)
	}
	first, err := f.service.AddFolder("local", f.studio.ID, "templates", archivePath)
	if err != nil {
		t.Fatal(err)
	}
	if again, err := f.service.AddFolder("local", f.studio.ID, "templates", archivePath); err != nil || again != first {
		t.Fatalf("adding the same folder twice = %+v, %v", again, err)
	}
	for index := 1; index < MaxOwnerFolders; index++ {
		if _, err := f.service.AddFolder("local", f.studio.ID, "scripts", "/Users/me/Scripts "+itoa(index)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.service.AddFolder("local", f.studio.ID, "scripts", "/Users/me/One too many"); !errors.Is(err, ErrFolderLimit) {
		t.Fatalf("a ninth folder: err = %v", err)
	}
	if view := f.overview(t); view.CanAddFolder {
		t.Fatal("the page still offers to add a folder at the limit")
	}
	f.provider.set(func(p *fakeProvider) { p.status.Available, p.status.Reason = false, ReasonProviderDisabled })
	if _, err := f.service.AddFolder("local", f.studio.ID, "templates", "/Users/me/Elsewhere"); !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("with the provider off: err = %v", err)
	}
	if view := f.overview(t); len(view.FolderCategories) != 0 || view.CanAddFolder {
		t.Fatalf("with the provider off the page offers folders: %+v", view.FolderCategories)
	}
	// Removing still works without the provider, like a withdrawal.
	if _, err := f.service.RemoveFolder("local", f.studio.ID, first.ID); err != nil {
		t.Fatalf("removing with the provider off: %v", err)
	}
}

// A provider that leaves out a folder it was given is refused whole: the
// owner must never approve a list that silently lost a folder.
func TestOwnerFolders_AProviderMustReportEveryFolder(t *testing.T) {
	f := newCatalogFixture(t)
	if _, err := f.service.AddFolder("local", f.studio.ID, "templates", archivePath); err != nil {
		t.Fatal(err)
	}
	f.provider.set(func(p *fakeProvider) { p.dropFolders = true })
	if _, err := f.service.ReviewSources(context.Background(), "local", f.studio.ID); !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("err = %v, want ErrProviderUnavailable", err)
	}
}

// Folder paths belong to this computer: they live in the data directory and
// never in the Studio's own files, which travel with the workspace.
func TestOwnerFolders_StayOnThisComputer(t *testing.T) {
	f := newCatalogFixture(t)
	folder, err := f.service.AddFolder("local", f.studio.ID, "templates", archivePath)
	if err != nil {
		t.Fatal(err)
	}
	f.provider.set(func(p *fakeProvider) {
		p.entries[folder.ID] = []InventoryEntry{{NativeID: "Drums.RPP", Kind: "project_template", Name: "Drums", Facts: []Fact{}}}
	})
	f.approveAll(t)
	ref := f.studioRef(t)
	if _, err := os.Stat(ref.paths.folders()); err != nil || !strings.HasPrefix(ref.paths.folders(), f.dataDir) {
		t.Fatalf("folders file %s: %v", ref.paths.folders(), err)
	}
	err = filepath.Walk(ref.paths.catalogDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		data, readErr := os.ReadFile(path) // #nosec G304 -- test temp dir
		if readErr == nil && strings.Contains(string(data), archivePath) {
			t.Errorf("%s holds the folder's path", filepath.Base(path))
		}
		return readErr
	})
	if err != nil {
		t.Fatal(err)
	}
}

func (f *catalogFixture) studioRef(t *testing.T) studioRef {
	t.Helper()
	ref, err := f.service.resolveStudio("local", f.studio.ID)
	if err != nil {
		t.Fatal(err)
	}
	return ref
}

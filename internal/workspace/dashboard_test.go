package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func newTestDashboardStore(t *testing.T, workspaceFolders map[string]string) *DashboardStore {
	t.Helper()
	return NewDashboardStore(staticFolderResolver{folders: workspaceFolders})
}

// writeDashboard creates <folder>/.ori/dashboard/index.html and returns the
// asset root it should be discovered at.
func writeDashboard(t *testing.T, folder, html string) string {
	t.Helper()
	assetRoot := filepath.Join(folder, SidecarDirName, CustomDashboardDirName)
	if err := os.MkdirAll(assetRoot, 0o750); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", assetRoot, err)
	}
	if err := os.WriteFile(filepath.Join(assetRoot, CustomDashboardEntryAsset), []byte(html), 0o600); err != nil {
		t.Fatalf("WriteFile(index.html) error = %v", err)
	}
	return assetRoot
}

func TestDashboardStoreFindsEntryAsset(t *testing.T) {
	folder := t.TempDir()
	assetRoot := writeDashboard(t, folder, "<!doctype html><title>Dash</title>")
	store := newTestDashboardStore(t, map[string]string{"ws1": folder})

	dashboard, ok, err := store.Find("ws1")
	if err != nil || !ok {
		t.Fatalf("Find() = %+v, %v, %v", dashboard, ok, err)
	}
	if dashboard.AssetRoot != assetRoot {
		t.Fatalf("AssetRoot = %q, want %q", dashboard.AssetRoot, assetRoot)
	}
	if dashboard.EntryAsset != CustomDashboardEntryAsset {
		t.Fatalf("EntryAsset = %q", dashboard.EntryAsset)
	}
	// workspacesurface.Binding validation requires an absolute, already-clean
	// asset root, so discovery has to produce one directly.
	if !filepath.IsAbs(dashboard.AssetRoot) || filepath.Clean(dashboard.AssetRoot) != dashboard.AssetRoot {
		t.Fatalf("AssetRoot %q is not absolute and clean", dashboard.AssetRoot)
	}
	if want := filepath.Join(assetRoot, CustomDashboardEntryAsset); dashboard.EntryPath() != want {
		t.Fatalf("EntryPath() = %q, want %q", dashboard.EntryPath(), want)
	}
}

func TestDashboardStoreReportsAbsenceWithoutError(t *testing.T) {
	folder := t.TempDir()
	store := newTestDashboardStore(t, map[string]string{"ws1": folder})

	assertAbsent := func(t *testing.T, stage string) {
		t.Helper()
		dashboard, ok, err := store.Find("ws1")
		if ok || err != nil || dashboard != (CustomDashboard{}) {
			t.Fatalf("%s: Find() = %+v, %v, %v; want absent with no error", stage, dashboard, ok, err)
		}
	}

	assertAbsent(t, "no sidecar directory")

	if err := os.MkdirAll(filepath.Join(folder, SidecarDirName), 0o750); err != nil {
		t.Fatal(err)
	}
	assertAbsent(t, "sidecar without dashboard directory")

	assetRoot := filepath.Join(folder, SidecarDirName, CustomDashboardDirName)
	if err := os.MkdirAll(assetRoot, 0o750); err != nil {
		t.Fatal(err)
	}
	assertAbsent(t, "dashboard directory without index.html")

	// A directory named index.html is not an entry asset.
	if err := os.MkdirAll(filepath.Join(assetRoot, CustomDashboardEntryAsset), 0o750); err != nil {
		t.Fatal(err)
	}
	assertAbsent(t, "index.html is a directory")
}

// Discovery must agree with workspacesurface.ReadAsset, which rejects symlinks
// at the root and at every path component. A dashboard that is discoverable but
// unservable would give the user a tab that can never open.
func TestDashboardStoreRejectsSymlinks(t *testing.T) {
	t.Run("symlinked entry asset", func(t *testing.T) {
		folder := t.TempDir()
		assetRoot := filepath.Join(folder, SidecarDirName, CustomDashboardDirName)
		if err := os.MkdirAll(assetRoot, 0o750); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(folder, "elsewhere.html")
		if err := os.WriteFile(target, []byte("<p>x</p>"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(assetRoot, CustomDashboardEntryAsset)); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		store := newTestDashboardStore(t, map[string]string{"ws1": folder})
		if _, ok, err := store.Find("ws1"); ok || err != nil {
			t.Fatalf("Find() = %v, %v; want absent for a symlinked entry asset", ok, err)
		}
	})

	t.Run("symlinked dashboard directory", func(t *testing.T) {
		folder := t.TempDir()
		real := filepath.Join(folder, "real-dashboard")
		if err := os.MkdirAll(real, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(real, CustomDashboardEntryAsset), []byte("<p>x</p>"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(folder, SidecarDirName), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(real, filepath.Join(folder, SidecarDirName, CustomDashboardDirName)); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		store := newTestDashboardStore(t, map[string]string{"ws1": folder})
		if _, ok, err := store.Find("ws1"); ok || err != nil {
			t.Fatalf("Find() = %v, %v; want absent for a symlinked dashboard directory", ok, err)
		}
	})
}

// FR9: a dashboard belongs to exactly one workspace. Workspace B must not see
// workspace A's dashboard, even though both resolve through the same store.
func TestDashboardStoreIsolatesWorkspaces(t *testing.T) {
	folderA, folderB := t.TempDir(), t.TempDir()
	rootA := writeDashboard(t, folderA, "<p>A</p>")
	store := newTestDashboardStore(t, map[string]string{"ws-a": folderA, "ws-b": folderB})

	dashboardA, ok, err := store.Find("ws-a")
	if err != nil || !ok || dashboardA.AssetRoot != rootA {
		t.Fatalf("Find(ws-a) = %+v, %v, %v", dashboardA, ok, err)
	}
	dashboardB, ok, err := store.Find("ws-b")
	if ok || err != nil || dashboardB != (CustomDashboard{}) {
		t.Fatalf("Find(ws-b) = %+v, %v, %v; want workspace B to see nothing", dashboardB, ok, err)
	}
}

// FR5: discovery is evaluated per call against the filesystem, never cached, so
// creating and deleting the file take effect without a restart.
func TestDashboardStoreReflectsFilesystemChanges(t *testing.T) {
	folder := t.TempDir()
	store := newTestDashboardStore(t, map[string]string{"ws1": folder})

	if _, ok, _ := store.Find("ws1"); ok {
		t.Fatal("Find() reported a dashboard before one existed")
	}
	assetRoot := writeDashboard(t, folder, "<p>now here</p>")
	if _, ok, err := store.Find("ws1"); !ok || err != nil {
		t.Fatalf("Find() = %v, %v after the file was created", ok, err)
	}
	if err := os.Remove(filepath.Join(assetRoot, CustomDashboardEntryAsset)); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.Find("ws1"); ok || err != nil {
		t.Fatalf("Find() = %v, %v after the file was deleted", ok, err)
	}
}

// FR13: editing a dashboard and reloading must serve the new bytes. The version
// is what the browser keys immutable sibling caching on, so it has to move for
// every kind of change a user actually makes.
func TestDashboardAssetVersionChangesWithTheDashboard(t *testing.T) {
	folder := t.TempDir()
	assetRoot := writeDashboard(t, folder, "<p>first</p>")
	store := newTestDashboardStore(t, map[string]string{"ws1": folder})

	version := func(t *testing.T) string {
		t.Helper()
		dashboard, ok, err := store.Find("ws1")
		if !ok || err != nil {
			t.Fatalf("Find() = %v, %v", ok, err)
		}
		if !canonicalDashboardVersion(dashboard.AssetVersion) {
			t.Fatalf("AssetVersion %q is not a canonical asset version", dashboard.AssetVersion)
		}
		return dashboard.AssetVersion
	}

	first := version(t)
	if second := version(t); second != first {
		t.Fatalf("AssetVersion is unstable across calls: %q then %q", first, second)
	}

	// Same length, different bytes: only content hashing catches this.
	if err := os.WriteFile(filepath.Join(assetRoot, CustomDashboardEntryAsset), []byte("<p>secnd</p>"), 0o600); err != nil {
		t.Fatal(err)
	}
	afterEntryEdit := version(t)
	if afterEntryEdit == first {
		t.Fatal("AssetVersion did not change after a same-length edit to index.html")
	}

	// A sibling asset is the case that actually matters: siblings are served
	// `immutable` for a year, keyed by this version.
	sibling := filepath.Join(assetRoot, "dashboard.js")
	if err := os.WriteFile(sibling, []byte("console.log(1)"), 0o600); err != nil {
		t.Fatal(err)
	}
	afterSiblingAdd := version(t)
	if afterSiblingAdd == afterEntryEdit {
		t.Fatal("AssetVersion did not change when a sibling asset was added")
	}

	if err := os.WriteFile(sibling, []byte("console.log(1234567)"), 0o600); err != nil {
		t.Fatal(err)
	}
	afterSiblingEdit := version(t)
	if afterSiblingEdit == afterSiblingAdd {
		t.Fatal("AssetVersion did not change when a sibling asset was edited")
	}

	if err := os.Remove(sibling); err != nil {
		t.Fatal(err)
	}
	if afterSiblingRemove := version(t); afterSiblingRemove == afterSiblingEdit {
		t.Fatal("AssetVersion did not change when a sibling asset was removed")
	}

	if err := os.MkdirAll(filepath.Join(assetRoot, "partials"), 0o750); err != nil {
		t.Fatal(err)
	}
	if afterDirAdd := version(t); afterDirAdd == afterEntryEdit {
		t.Fatal("AssetVersion did not change when a subdirectory was added")
	}
}

// Two dashboards with identical entry files but different siblings must not
// share a version, or one would serve the other's cached assets.
func TestDashboardAssetVersionDistinguishesSiblingSets(t *testing.T) {
	folderA, folderB := t.TempDir(), t.TempDir()
	rootA := writeDashboard(t, folderA, "<p>same</p>")
	rootB := writeDashboard(t, folderB, "<p>same</p>")
	if err := os.WriteFile(filepath.Join(rootA, "a.css"), []byte("body{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rootB, "b.css"), []byte("body{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := newTestDashboardStore(t, map[string]string{"ws-a": folderA, "ws-b": folderB})

	dashboardA, _, err := store.Find("ws-a")
	if err != nil {
		t.Fatal(err)
	}
	dashboardB, _, err := store.Find("ws-b")
	if err != nil {
		t.Fatal(err)
	}
	if dashboardA.AssetVersion == dashboardB.AssetVersion {
		t.Fatalf("distinct dashboards share asset version %q", dashboardA.AssetVersion)
	}
}

// Siblings contribute size and modification time rather than content. A
// same-size sibling edit is therefore caught only by the timestamp, so assert it
// with an explicit Chtimes instead of relying on write timing.
func TestDashboardAssetVersionTracksSiblingModificationTime(t *testing.T) {
	folder := t.TempDir()
	assetRoot := writeDashboard(t, folder, "<p>x</p>")
	sibling := filepath.Join(assetRoot, "style.css")
	if err := os.WriteFile(sibling, []byte("body{color:red}"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := newTestDashboardStore(t, map[string]string{"ws1": folder})

	before, _, err := store.Find("ws1")
	if err != nil {
		t.Fatal(err)
	}
	// Same byte count, different content and a distinctly different timestamp.
	if err := os.WriteFile(sibling, []byte("body{color:blu}"), 0o600); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(sibling, future, future); err != nil {
		t.Fatal(err)
	}
	after, _, err := store.Find("ws1")
	if err != nil {
		t.Fatal(err)
	}
	if after.AssetVersion == before.AssetVersion {
		t.Fatal("AssetVersion did not change for a same-size sibling edit with a new modification time")
	}
}

func TestDashboardAssetVersionRejectsOversizedTrees(t *testing.T) {
	folder := t.TempDir()
	assetRoot := writeDashboard(t, folder, "<p>big</p>")
	for i := 0; i <= maxDashboardAssetEntries; i++ {
		name := filepath.Join(assetRoot, "asset-"+strconv.Itoa(i)+".css")
		if err := os.WriteFile(name, []byte("body{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	store := newTestDashboardStore(t, map[string]string{"ws1": folder})
	if _, ok, err := store.Find("ws1"); ok || err == nil {
		t.Fatalf("Find() = %v, %v; want an error for an oversized dashboard tree", ok, err)
	}
}

// canonicalDashboardVersion mirrors workspacesurface.canonicalAssetVersion,
// which is unexported there. A version that fails this makes validateBinding
// reject the synthesized binding.
func canonicalDashboardVersion(value string) bool {
	if len(value) < 1 || len(value) > 128 {
		return false
	}
	for _, r := range value {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '.' && r != '_' && r != '-' {
			return false
		}
	}
	return true
}

func TestDashboardStoreErrorsWhenFolderCannotBeResolved(t *testing.T) {
	store := newTestDashboardStore(t, map[string]string{})
	if _, ok, err := store.Find("unknown"); ok || !errors.Is(err, ErrDashboardFolderUnavailable) {
		t.Fatalf("Find(unknown) = %v, %v; want ErrDashboardFolderUnavailable", ok, err)
	}

	var nilStore *DashboardStore
	if _, ok, err := nilStore.Find("ws1"); ok || !errors.Is(err, ErrDashboardFolderUnavailable) {
		t.Fatalf("nil store Find() = %v, %v", ok, err)
	}
	if _, ok, err := NewDashboardStore(nil).Find("ws1"); ok || !errors.Is(err, ErrDashboardFolderUnavailable) {
		t.Fatalf("resolver-less Find() = %v, %v", ok, err)
	}
}

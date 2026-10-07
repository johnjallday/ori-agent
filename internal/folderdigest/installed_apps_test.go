package folderdigest

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func mkBundle(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(path, 0o750); err != nil {
		t.Fatal(err)
	}
	return path
}

func installedIDs(apps []InstalledApp) []string {
	ids := make([]string, 0, len(apps))
	for _, app := range apps {
		ids = append(ids, app.ToolID)
	}
	return ids
}

func TestDetectInstalledAppsFindsBundlesInTableOrder(t *testing.T) {
	system, home := t.TempDir(), t.TempDir()
	t.Setenv(applicationsDirEnv, system)
	mkBundle(t, filepath.Join(home, "Applications"), "Logic Pro.app")
	mkBundle(t, system, "REAPER64.app")
	mkBundle(t, system, "Ableton Live 12 Suite.app")
	mkBundle(t, system, "Safari.app")

	apps := DetectInstalledApps(home)
	if got := installedIDs(apps); !reflect.DeepEqual(got, []string{"reaper", "logic-pro", "ableton-live"}) {
		t.Fatalf("installed = %v", got)
	}
	if apps[0].Name != "REAPER" || apps[1].Name != "Logic Pro" || apps[2].Name != "Ableton Live" {
		t.Fatalf("display names = %+v", apps)
	}
}

func TestDetectInstalledAppsReportsNothingForAnEmptyComputer(t *testing.T) {
	t.Setenv(applicationsDirEnv, t.TempDir())
	if apps := DetectInstalledApps(t.TempDir()); len(apps) != 0 {
		t.Fatalf("installed = %+v", apps)
	}
	// A missing folder, a relative home and no home at all are all "nothing".
	t.Setenv(applicationsDirEnv, filepath.Join(t.TempDir(), "missing"))
	for _, home := range []string{"", "relative/home", filepath.Join(t.TempDir(), "missing")} {
		if apps := DetectInstalledApps(home); len(apps) != 0 {
			t.Fatalf("home %q installed = %+v", home, apps)
		}
	}
}

func TestDetectInstalledAppsIgnoresFilesAndSymbolicLinks(t *testing.T) {
	system, elsewhere := t.TempDir(), t.TempDir()
	t.Setenv(applicationsDirEnv, system)
	// A plain file with a bundle's name is not an application.
	if err := os.WriteFile(filepath.Join(system, "REAPER.app"), []byte("not a bundle"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A link to a real bundle is not followed, for an exact name or a pattern.
	real := mkBundle(t, elsewhere, "Logic Pro.app")
	if err := os.Symlink(real, filepath.Join(system, "Logic Pro.app")); err != nil {
		t.Skipf("symbolic links are unavailable: %v", err)
	}
	live := mkBundle(t, elsewhere, "Ableton Live 12.app")
	if err := os.Symlink(live, filepath.Join(system, "Ableton Live 12.app")); err != nil {
		t.Fatal(err)
	}
	if apps := DetectInstalledApps(""); len(apps) != 0 {
		t.Fatalf("installed = %+v", apps)
	}
}

func TestDetectInstalledAppsReadsNothingInsideABundle(t *testing.T) {
	system := t.TempDir()
	t.Setenv(applicationsDirEnv, system)
	bundle := mkBundle(t, system, "REAPER.app")
	// A bundle nobody may open still counts: detection never looks inside.
	if err := os.Chmod(bundle, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(bundle, 0o750) })
	if got := installedIDs(DetectInstalledApps("")); !reflect.DeepEqual(got, []string{"reaper"}) {
		t.Fatalf("installed = %v", got)
	}
}

func TestDetectInstalledAppsFallsBackToTheSystemFolder(t *testing.T) {
	// A relative override is ignored rather than resolved against the working
	// directory.
	t.Setenv(applicationsDirEnv, "Applications")
	home := t.TempDir()
	mkBundle(t, filepath.Join(home, "Applications"), "Logic Pro X.app")
	found := false
	for _, app := range DetectInstalledApps(home) {
		found = found || app.ToolID == "logic-pro"
	}
	if !found {
		t.Fatal("the user's own Applications folder was not searched")
	}
}

func TestInstallableToolsAndProjectFormats(t *testing.T) {
	tools := InstallableTools()
	if len(tools) != 3 {
		t.Fatalf("installable tools = %+v", tools)
	}
	tools[0].AppBundles[0] = "changed"
	if InstallableTools()[0].AppBundles[0] == "changed" {
		t.Fatal("InstallableTools exposes the authoritative table")
	}
	for id, format := range map[string]string{"reaper": "reaper", "logic-pro": "logic", "ableton-live": "ableton", "latex": "", "go": "", "unknown": ""} {
		if got := ProjectFormatForTool(id); got != format {
			t.Errorf("ProjectFormatForTool(%q) = %q, want %q", id, got, format)
		}
	}
}

// Every bundle name is one path element, so a table row can never make
// detection look outside an Applications folder.
func TestToolTableBundleNamesAreSinglePathElements(t *testing.T) {
	for _, tool := range Tools {
		for _, bundle := range tool.AppBundles {
			if bundle == "" || bundle != filepath.Base(bundle) || filepath.Ext(bundle) != ".app" {
				t.Errorf("tool %q bundle %q is not a bare .app name", tool.ToolID, bundle)
			}
		}
	}
	if clone := AllCapabilities(); len(clone[0].Tools[0].AppBundles) == 0 {
		t.Fatal("a detached capability row lost its bundle names")
	} else {
		clone[0].Tools[0].AppBundles[0] = "changed"
		if Tools[0].AppBundles[0] == "changed" {
			t.Fatal("AllCapabilities exposes the authoritative bundle names")
		}
	}
}

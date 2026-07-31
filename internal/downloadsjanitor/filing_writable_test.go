package downloadsjanitor

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestSetup_RefusesAFolderItCannotFileInto is FR-52.
//
// Existence is not permission. A Filed/ directory that exists but cannot be
// written to would let setup report Ready and then fail on the user's first
// approved move — the worst possible moment to find out. Setup probes the real
// permission instead of inferring it from mode bits, which say nothing useful
// when the folder is owned by another user or governed by an ACL.
func TestSetup_RefusesAFolderItCannotFileInto(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits do not model Windows ACLs")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions")
	}

	store, _ := newTestStore(t)
	service := NewService(store, newFakeWorkspaceStore("ws-1"))

	root := mkdir(t, filepath.Join(tempDirCanonical(t), "Downloads"))
	// A Filed/ that already exists and is read-only: setup will not need to
	// create it, so only a real write probe can catch the problem.
	filed := mkdir(t, filepath.Join(root, DefaultFilingRootName))
	if err := os.Chmod(filed, 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(filed, 0o750) })

	_, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-1", Path: root})
	if err == nil {
		t.Fatal("setup accepted a folder it cannot file into")
	}
	var setupError *SetupError
	if !errors.As(err, &setupError) {
		t.Fatalf("expected a SetupError, got %v", err)
	}
	if setupError.Code != CodeDestinationBlocked && setupError.Code != CodePermissionDenied {
		t.Fatalf("code = %q, want a destination or permission failure", setupError.Code)
	}
	if setupError.Repair == "" {
		t.Fatal("the failure must offer a repair")
	}

	// Nothing was granted: a refused setup leaves the workspace unconfigured.
	settings, err := service.store.LoadSettings("ws-1")
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if settings.IsSetUp() {
		t.Fatal("a refused setup still recorded a folder grant")
	}
}

// TestSetup_WriteProbeLeavesNothingBehind keeps the check invisible: it must
// prove writability without littering the user's folder.
func TestSetup_WriteProbeLeavesNothingBehind(t *testing.T) {
	store, _ := newTestStore(t)
	service := NewService(store, newFakeWorkspaceStore("ws-1"))
	root := mkdir(t, filepath.Join(tempDirCanonical(t), "Downloads"))

	if _, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-1", Path: root}); err != nil {
		t.Fatalf("ConfirmSetup: %v", err)
	}

	entries, err := os.ReadDir(filepath.Join(root, DefaultFilingRootName))
	if err != nil {
		t.Fatalf("read Filed/: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("setup left files behind in the filing root: %v", entries)
	}

	// And the root itself gained only Filed/.
	rootEntries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read root: %v", err)
	}
	if len(rootEntries) != 1 || rootEntries[0].Name() != DefaultFilingRootName {
		t.Fatalf("setup wrote something unexpected into the managed folder: %v", rootEntries)
	}
}

// TestReadiness_ReportsFolderAccessAndFilingAccessSeparately is FR-55: the two
// are distinct failures with distinct repairs, so a user whose Filed/ is broken
// is not told their folder is inaccessible.
func TestReadiness_ReportsFolderAccessAndFilingAccessSeparately(t *testing.T) {
	service, root := configuredService(t)

	status, err := service.Status("ws-1")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	components := map[ReadinessComponent]ComponentStatus{}
	for _, check := range status.Readiness.Checks {
		components[check.Component] = check.Status
	}
	for _, required := range []ReadinessComponent{
		ComponentDirectoryAccess,
		ComponentDestination,
		ComponentPersistence,
		ComponentWatcher,
		ComponentScheduler,
	} {
		if _, present := components[required]; !present {
			t.Fatalf("readiness does not report %q independently: %+v", required, status.Readiness.Checks)
		}
	}

	// Break only the filing destination; folder access must stay healthy.
	if err := os.RemoveAll(filepath.Join(root, DefaultFilingRootName)); err != nil {
		t.Fatal(err)
	}
	status, err = service.Status("ws-1")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	var access, destination ComponentCheck
	for _, check := range status.Readiness.Checks {
		switch check.Component {
		case ComponentDirectoryAccess:
			access = check
		case ComponentDestination:
			destination = check
		}
	}
	if access.Status == ComponentFailed {
		t.Fatalf("a missing Filed/ was reported as a folder-access failure: %+v", access)
	}
	if destination.Status != ComponentFailed {
		t.Fatalf("a missing Filed/ was not reported: %+v", destination)
	}
	if destination.Repair == "" {
		t.Fatal("the filing failure must offer its own repair")
	}
}

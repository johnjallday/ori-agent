package workspacedashboard

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacesurface"
)

// newTestSource builds a source over real workspace folders on disk, so
// discovery, fingerprinting, and validation all run for real.
func newTestSource(t *testing.T, workspaceFolders map[string]string) *Source {
	t.Helper()
	return NewSource(workspace.NewDashboardStore(staticFolders(workspaceFolders)), NewRuntime(nil, nil, nil))
}

type staticFolders map[string]string

func (f staticFolders) GetFolderPath(workspaceID string) (string, error) {
	folder, ok := f[workspaceID]
	if !ok {
		return "", errors.New("workspace not found")
	}
	return folder, nil
}

func writeDashboard(t *testing.T, folder, html string) string {
	t.Helper()
	assetRoot := filepath.Join(folder, workspace.SidecarDirName, workspace.CustomDashboardDirName)
	if err := os.MkdirAll(assetRoot, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(assetRoot, workspace.CustomDashboardEntryAsset), []byte(html), 0o600); err != nil {
		t.Fatal(err)
	}
	return assetRoot
}

func TestSourceSynthesizesAValidSurfaceAndBinding(t *testing.T) {
	folder := t.TempDir()
	assetRoot := writeDashboard(t, folder, "<!doctype html><title>My dashboard</title>")
	source := newTestSource(t, map[string]string{"ws1": folder})

	surface, binding, ok, err := source.Resolve("ws1")
	if !ok || err != nil {
		t.Fatalf("Resolve() = %v, %v", ok, err)
	}
	if surface.Owner.Kind != workspacesurface.OwnerUser {
		t.Fatalf("owner kind = %q, want the untrusted user kind", surface.Owner.Kind)
	}
	if !surface.Available || surface.Key != Key() {
		t.Fatalf("surface = %+v", surface)
	}
	if binding.AssetRoot != assetRoot || binding.EntryAsset != workspace.CustomDashboardEntryAsset || binding.AssetVersion == "" {
		t.Fatalf("binding = %+v", binding)
	}
	if binding.Runtime == nil {
		t.Fatal("binding has no runtime; the broker would refuse to open it")
	}
	// The synthesized registration must satisfy exactly what RegisterTrusted
	// demands, or the surface would be servable while a registered equivalent
	// would have been rejected.
	if err := workspacesurface.ValidateRegistration(workspacesurface.Registration{
		Owner:        surface.Owner,
		Capabilities: []workspacesurface.Capability{surface.Capability},
		Bindings:     []workspacesurface.Binding{binding},
	}); err != nil {
		t.Fatalf("synthesized registration is invalid: %v", err)
	}
}

// FR10: the synthesized registration must never be admitted to the global
// registry, whatever else changes about it.
func TestSourceOutputIsRefusedByTheGlobalRegistry(t *testing.T) {
	folder := t.TempDir()
	writeDashboard(t, folder, "<p>x</p>")
	source := newTestSource(t, map[string]string{"ws1": folder})
	surface, binding, ok, err := source.Resolve("ws1")
	if !ok || err != nil {
		t.Fatalf("Resolve() = %v, %v", ok, err)
	}

	registry := workspacesurface.NewRegistry()
	registerErr := registry.RegisterTrusted(workspacesurface.Registration{
		Owner:        surface.Owner,
		Capabilities: []workspacesurface.Capability{surface.Capability},
		Bindings:     []workspacesurface.Binding{binding},
	})
	if registerErr == nil {
		t.Fatal("the global registry accepted a user dashboard")
	}
	if len(registry.Surfaces()) != 0 {
		t.Fatalf("registry published %d surfaces", len(registry.Surfaces()))
	}
}

func TestSourceReportsNoDashboardWithoutError(t *testing.T) {
	source := newTestSource(t, map[string]string{"ws1": t.TempDir()})
	surface, binding, ok, err := source.Resolve("ws1")
	if ok || err != nil {
		t.Fatalf("Resolve() = %v, %v; want a clean absence", ok, err)
	}
	if surface.Key != "" || binding.Runtime != nil {
		t.Fatalf("absent dashboard produced surface=%+v binding=%+v", surface, binding)
	}
}

// FR26/FR27: a dashboard whose file is present but unusable stays visible and
// unavailable, and its description names the file. Hiding it would leave the
// user with no signal that Ori saw their file at all — and no way to find out
// what is wrong with it, since the frame gives them nothing.
func TestSourceSurfacesABrokenDashboardAsUnavailable(t *testing.T) {
	folder := t.TempDir()
	assetRoot := writeDashboard(t, folder, "")
	source := newTestSource(t, map[string]string{"ws1": folder})

	surface, binding, ok, err := source.Resolve("ws1")
	if !ok {
		t.Fatal("a broken dashboard was hidden instead of reported")
	}
	if !errors.Is(err, ErrDashboardUnavailable) {
		t.Fatalf("error = %v, want ErrDashboardUnavailable", err)
	}
	if surface.Key != Key() || surface.Available {
		t.Fatalf("surface = %+v; want the dashboard key marked unavailable", surface)
	}
	if surface.UnavailableCode == "" {
		t.Fatal("unavailable dashboard carries no code")
	}
	entryPath := filepath.Join(assetRoot, workspace.CustomDashboardEntryAsset)
	if !strings.Contains(surface.Surface.Description, entryPath) {
		t.Fatalf("description %q does not name the entry file %q", surface.Surface.Description, entryPath)
	}
	if !strings.Contains(strings.ToLower(surface.Surface.Description), "empty") {
		t.Fatalf("description %q does not say why", surface.Surface.Description)
	}
	// The description is shown to the user, so it must satisfy the same text
	// validation any surface description does.
	if err := workspacesurface.ValidateRegistration(workspacesurface.Registration{
		Owner:           surface.Owner,
		Capabilities:    []workspacesurface.Capability{surface.Capability},
		UnavailableCode: surface.UnavailableCode,
	}); err != nil {
		t.Fatalf("the unavailable descriptor does not validate: %v", err)
	}
	// No binding: a failed resolution must not be openable.
	if binding.Runtime != nil || binding.AssetRoot != "" {
		t.Fatalf("broken dashboard returned a usable binding: %+v", binding)
	}
}

// An unresolvable workspace folder is different: there is no dashboard identity
// to show, and nothing useful to say about a file that was never found.
func TestSourceHidesTheSurfaceWhenTheFolderIsUnresolvable(t *testing.T) {
	source := NewSource(failingFinder{}, NewRuntime(nil, nil, nil))
	surface, _, ok, err := source.Resolve("ws1")
	if ok || surface.Key != "" {
		t.Fatalf("Resolve() = %v, %+v; want nothing shown", ok, surface)
	}
	if !errors.Is(err, ErrDashboardUnavailable) {
		t.Fatalf("error = %v", err)
	}
}

type failingFinder struct{}

func (failingFinder) Find(string) (workspace.CustomDashboard, bool, error) {
	return workspace.CustomDashboard{}, false, errors.New("disk on fire")
}

// FR9: the surface key is identical across workspaces by design, so scoping
// cannot come from the key. It must come from resolving per workspace id.
func TestSourceScopesByWorkspaceNotByKey(t *testing.T) {
	folderA, folderB := t.TempDir(), t.TempDir()
	rootA := writeDashboard(t, folderA, "<p>A</p>")
	source := newTestSource(t, map[string]string{"ws-a": folderA, "ws-b": folderB})

	surfaceA, bindingA, ok, err := source.Resolve("ws-a")
	if !ok || err != nil {
		t.Fatalf("Resolve(ws-a) = %v, %v", ok, err)
	}
	if bindingA.AssetRoot != rootA {
		t.Fatalf("workspace A resolved asset root %q", bindingA.AssetRoot)
	}
	if _, _, ok, err := source.Resolve("ws-b"); ok || err != nil {
		t.Fatalf("Resolve(ws-b) = %v, %v; workspace B has no dashboard", ok, err)
	}
	if !strings.HasPrefix(surfaceA.Key, string(workspacesurface.OwnerUser)+":") {
		t.Fatalf("dashboard key %q is not owned by the user kind", surfaceA.Key)
	}
}

func TestSourceWithoutDependenciesResolvesNothing(t *testing.T) {
	var nilSource *Source
	if _, _, ok, err := nilSource.Resolve("ws1"); ok || err != nil {
		t.Fatalf("nil source Resolve() = %v, %v", ok, err)
	}
	if _, _, ok, err := NewSource(nil, NewRuntime(nil, nil, nil)).Resolve("ws1"); ok || err != nil {
		t.Fatalf("finder-less Resolve() = %v, %v", ok, err)
	}
	if _, _, ok, err := NewSource(staticFinder{}, nil).Resolve("ws1"); ok || err != nil {
		t.Fatalf("runtime-less Resolve() = %v, %v", ok, err)
	}
}

type staticFinder struct{}

func (staticFinder) Find(string) (workspace.CustomDashboard, bool, error) {
	return workspace.CustomDashboard{}, false, nil
}

// The synthesized surface must declare exactly the v1 vocabulary, and its
// binding must define exactly those operations. A mismatch is what
// ValidateRegistration rejects, and it would make the dashboard unopenable.
func TestSourceDeclaresTheFullOperationVocabulary(t *testing.T) {
	folder := t.TempDir()
	writeDashboard(t, folder, "<p>x</p>")
	source := newTestSource(t, map[string]string{"ws1": folder})

	surface, binding, ok, err := source.Resolve("ws1")
	if !ok || err != nil {
		t.Fatalf("Resolve() = %v, %v", ok, err)
	}
	want := []string{
		OpWorkspaceSummary, OpTasksList, OpNotesList,
		OpAgentsList, OpSessionsList, OpFilesList,
	}
	if len(surface.Surface.OperationIDs) != len(want) {
		t.Fatalf("surface declares %v", surface.Surface.OperationIDs)
	}
	for _, id := range want {
		if !slices.Contains(surface.Surface.OperationIDs, id) {
			t.Fatalf("surface does not declare %q", id)
		}
		operation, declared := binding.Operations[id]
		if !declared {
			t.Fatalf("binding does not define %q", id)
		}
		// Read-only is what keeps a dashboard out of the write and confirmation
		// paths entirely.
		if operation.Policy != workspacesurface.PolicyReadOnly {
			t.Fatalf("operation %q policy = %q, want read-only", id, operation.Policy)
		}
	}
}

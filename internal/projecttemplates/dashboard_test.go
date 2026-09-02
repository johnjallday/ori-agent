package projecttemplates

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// writeTemplateDashboard creates <template>/dashboard/ with an entry file and
// the given siblings, and returns the template path.
func writeTemplateDashboard(t *testing.T, templatePath string, siblings map[string]string) string {
	t.Helper()
	dir := filepath.Join(templatePath, DashboardDirName)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, workspace.CustomDashboardEntryAsset),
		[]byte("<!doctype html><title>Template dashboard</title>"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, content := range siblings {
		full := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return templatePath
}

func dashboardEntry(workspaceFolder string) string {
	return filepath.Join(workspaceFolder, workspace.SidecarDirName,
		workspace.CustomDashboardDirName, workspace.CustomDashboardEntryAsset)
}

func TestInstallDashboardCopiesTheWholeDirectory(t *testing.T) {
	templatePath := writeTemplateDashboard(t, t.TempDir(), map[string]string{
		"dashboard.css":   "body{}",
		"dashboard.js":    "console.log(1)",
		"assets/logo.svg": "<svg/>",
		"ori-bridge.js":   "window.Ori = {};",
	})
	workspaceFolder := t.TempDir()

	installed, err := InstallDashboard(templatePath, workspaceFolder)
	if err != nil || !installed {
		t.Fatalf("InstallDashboard() = %v, %v", installed, err)
	}
	if _, err := os.Stat(dashboardEntry(workspaceFolder)); err != nil {
		t.Fatalf("entry file was not installed: %v", err)
	}
	root := filepath.Join(workspaceFolder, workspace.SidecarDirName, workspace.CustomDashboardDirName)
	for _, name := range []string{"dashboard.css", "dashboard.js", "ori-bridge.js", "assets/logo.svg"} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(name))); err != nil {
			t.Fatalf("sibling %q was not installed: %v", name, err)
		}
	}

	// The installed dashboard must be discoverable by the runtime that serves
	// it — installing something the dashboard store cannot find is useless.
	store := workspace.NewDashboardStore(fixedFolder(workspaceFolder))
	dashboard, ok, err := store.Find("ws")
	if !ok || err != nil {
		t.Fatalf("the installed dashboard is not discoverable: ok=%v err=%v", ok, err)
	}
	if dashboard.AssetVersion == "" {
		t.Fatal("installed dashboard has no asset version")
	}
}

type fixedFolder string

func (f fixedFolder) GetFolderPath(string) (string, error) { return string(f), nil }

func TestInstallDashboardIsANoOpWithoutOne(t *testing.T) {
	// A template folder with no dashboard/ at all.
	installed, err := InstallDashboard(t.TempDir(), t.TempDir())
	if installed || err != nil {
		t.Fatalf("InstallDashboard() = %v, %v; want a clean no-op", installed, err)
	}

	// A dashboard/ directory with no index.html ships no dashboard: it would
	// produce no surface, so installing it would silently do nothing.
	templatePath := t.TempDir()
	if err := os.MkdirAll(filepath.Join(templatePath, DashboardDirName), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(templatePath, DashboardDirName, "styles.css"), []byte("body{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if HasDashboard(templatePath) {
		t.Fatal("a dashboard directory without an entry file counts as a dashboard")
	}
	workspaceFolder := t.TempDir()
	if installed, err := InstallDashboard(templatePath, workspaceFolder); installed || err != nil {
		t.Fatalf("InstallDashboard() = %v, %v", installed, err)
	}
	if _, err := os.Stat(filepath.Join(workspaceFolder, workspace.SidecarDirName)); !os.IsNotExist(err) {
		t.Fatal("a template without a dashboard created a sidecar directory anyway")
	}
}

// Whoever already put a dashboard in the workspace wins over the template.
func TestInstallDashboardNeverOverwrites(t *testing.T) {
	templatePath := writeTemplateDashboard(t, t.TempDir(), nil)
	workspaceFolder := t.TempDir()
	existing := filepath.Join(workspaceFolder, workspace.SidecarDirName, workspace.CustomDashboardDirName)
	if err := os.MkdirAll(existing, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(existing, workspace.CustomDashboardEntryAsset), []byte("MINE"), 0o600); err != nil {
		t.Fatal(err)
	}

	installed, err := InstallDashboard(templatePath, workspaceFolder)
	if installed || !errors.Is(err, ErrDashboardExists) {
		t.Fatalf("InstallDashboard() = %v, %v; want ErrDashboardExists", installed, err)
	}
	body, err := os.ReadFile(dashboardEntry(workspaceFolder))
	if err != nil || string(body) != "MINE" {
		t.Fatalf("the existing dashboard was overwritten: %q, %v", body, err)
	}
}

// Symlinks are skipped exactly as they are in the skeleton copy: following one
// could pull in files from outside the template.
func TestInstallDashboardSkipsSymlinks(t *testing.T) {
	templatePath := writeTemplateDashboard(t, t.TempDir(), nil)
	outside := filepath.Join(t.TempDir(), "secrets.env")
	if err := os.WriteFile(outside, []byte("API_KEY=sk-live"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(templatePath, DashboardDirName, "leak.js")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	workspaceFolder := t.TempDir()
	if installed, err := InstallDashboard(templatePath, workspaceFolder); !installed || err != nil {
		t.Fatalf("InstallDashboard() = %v, %v", installed, err)
	}
	root := filepath.Join(workspaceFolder, workspace.SidecarDirName, workspace.CustomDashboardDirName)
	if _, err := os.Lstat(filepath.Join(root, "leak.js")); !os.IsNotExist(err) {
		t.Fatal("a symlinked dashboard file was copied into the workspace")
	}
}

// A template that ships ONLY a dashboard is still metadata-only: it must not
// start scaffolding an otherwise empty project folder.
func TestDashboardOnlyTemplateHasNoSkeleton(t *testing.T) {
	templatePath := writeTemplateDashboard(t, t.TempDir(), map[string]string{"dashboard.css": "body{}"})
	if err := os.WriteFile(filepath.Join(templatePath, ManifestFileName), []byte(`{"name":"Dash"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if hasSkeletonFiles(templatePath) {
		t.Fatal("a dashboard-only template reports a skeleton")
	}

	// Add real project content and it becomes a skeleton again.
	if err := os.WriteFile(filepath.Join(templatePath, "README.md"), []byte("# hi"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !hasSkeletonFiles(templatePath) {
		t.Fatal("a template with project files does not report a skeleton")
	}
}

// The dashboard belongs in the workspace's sidecar, not duplicated inside the
// project folder the skeleton scaffolds.
func TestInstantiateSkipsTheDashboardDirectory(t *testing.T) {
	templatePath := writeTemplateDashboard(t, t.TempDir(), map[string]string{"dashboard.css": "body{}"})
	if err := os.WriteFile(filepath.Join(templatePath, ManifestFileName), []byte(`{"name":"Dash"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(templatePath, "README.md"), []byte("# hi"), 0o600); err != nil {
		t.Fatal(err)
	}
	workspaceFolder := t.TempDir()

	projectPath, err := Instantiate(templatePath, workspaceFolder, "My Project")
	if err != nil {
		t.Fatalf("Instantiate() error = %v", err)
	}
	projectRoot := filepath.Join(workspaceFolder, projectPath)
	if _, err := os.Stat(filepath.Join(projectRoot, "README.md")); err != nil {
		t.Fatalf("project content was not scaffolded: %v", err)
	}
	if _, err := os.Stat(filepath.Join(projectRoot, DashboardDirName)); !os.IsNotExist(err) {
		t.Fatal("the dashboard was duplicated into the project folder")
	}
}

// The Email Ops built-in ships a dashboard, and it must be a valid one — not
// merely present. In particular it must not use inline script/style, which the
// frame's CSP blocks silently.
func TestEmailOpsShipsAUsableDashboard(t *testing.T) {
	library := t.TempDir()
	if err := EnsureLibrary(library); err != nil {
		t.Fatal(err)
	}
	templatePath := filepath.Join(library, "email-ops")
	if !HasDashboard(templatePath) {
		t.Fatal("the Email Ops built-in does not ship a dashboard")
	}

	entry, err := os.ReadFile(filepath.Join(templatePath, DashboardDirName, workspace.CustomDashboardEntryAsset))
	if err != nil {
		t.Fatal(err)
	}
	html := string(entry)
	// An inline <script>/<style> is blocked by script-src/style-src 'self'
	// with no unsafe-inline, and fails silently — a blank or unstyled page.
	if strings.Contains(html, "<style>") {
		t.Fatal("the shipped dashboard uses an inline <style>, which CSP blocks")
	}
	for _, fragment := range []string{`<script src=`, `<link rel="stylesheet"`} {
		if !strings.Contains(html, fragment) {
			t.Fatalf("the shipped dashboard is missing %q", fragment)
		}
	}
	// An inline script is a <script> with no src attribute.
	for _, chunk := range strings.Split(html, "<script")[1:] {
		head := chunk
		if end := strings.Index(chunk, ">"); end >= 0 {
			head = chunk[:end]
		}
		if !strings.Contains(head, "src=") {
			t.Fatal("the shipped dashboard uses an inline <script>, which CSP blocks")
		}
	}

	// And it must actually install and resolve.
	workspaceFolder := t.TempDir()
	if installed, err := InstallDashboard(templatePath, workspaceFolder); !installed || err != nil {
		t.Fatalf("InstallDashboard(email-ops) = %v, %v", installed, err)
	}
	if _, ok, err := workspace.NewDashboardStore(fixedFolder(workspaceFolder)).Find("ws"); !ok || err != nil {
		t.Fatalf("the installed Email Ops dashboard is not discoverable: ok=%v err=%v", ok, err)
	}
}

// A built-in dashboard must reach installs that already exist, not only fresh
// ones — the manifest refresh alone would never carry the files across.
func TestEnsureLibraryInstallsAShippedDashboardIntoAnExistingTemplate(t *testing.T) {
	library := t.TempDir()
	if err := EnsureLibrary(library); err != nil {
		t.Fatal(err)
	}
	templatePath := filepath.Join(library, "email-ops")

	// Simulate an older install: remove the dashboard and roll the manifest back.
	if err := os.RemoveAll(filepath.Join(templatePath, DashboardDirName)); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(templatePath, ManifestFileName)
	data, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	older := strings.Replace(string(data), `"builtin_version": 4`, `"builtin_version": 1`, 1)
	if older == string(data) {
		t.Fatal("could not roll the manifest version back; update this test alongside builtin_version")
	}
	if err := os.WriteFile(manifest, []byte(older), 0o640); err != nil {
		t.Fatal(err)
	}
	if HasDashboard(templatePath) {
		t.Fatal("the dashboard was not actually removed")
	}

	if err := EnsureLibrary(library); err != nil {
		t.Fatal(err)
	}
	if !HasDashboard(templatePath) {
		t.Fatal("a version bump did not carry the shipped dashboard to an existing install")
	}
}

// A dashboard already on disk may be the user's own edit of a built-in, so a
// refresh must leave it alone.
func TestEnsureLibraryDoesNotOverwriteAnEditedDashboard(t *testing.T) {
	library := t.TempDir()
	if err := EnsureLibrary(library); err != nil {
		t.Fatal(err)
	}
	templatePath := filepath.Join(library, "email-ops")
	entry := filepath.Join(templatePath, DashboardDirName, workspace.CustomDashboardEntryAsset)
	if err := os.WriteFile(entry, []byte("<!doctype html><title>MY EDIT</title>"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(templatePath, ManifestFileName)
	data, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	older := strings.Replace(string(data), `"builtin_version": 4`, `"builtin_version": 1`, 1)
	if err := os.WriteFile(manifest, []byte(older), 0o640); err != nil {
		t.Fatal(err)
	}

	if err := EnsureLibrary(library); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(entry)
	if err != nil || !strings.Contains(string(body), "MY EDIT") {
		t.Fatalf("a refresh overwrote an edited dashboard: %q", body)
	}
}

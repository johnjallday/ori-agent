package server

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompiledDomainExtractionLeavesOnlyGenericHostProductionCode(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	for _, retired := range []string{
		"internal/reaper", "internal/reaperhttp", "internal/reapersetup",
		"internal/server/reaper_control.go", "internal/sessionhttp/workspace_reaper_setup.go",
		"internal/workspace/reaper_pins.go", "internal/projecttemplates/starter/reaper-song",
		"internal/web/static/js/modules/reaper-console.js",
		"internal/web/static/js/modules/reaper-readiness-panel.js",
		"internal/web/static/js/modules/reaper-plugin-install.js",
	} {
		if _, err := os.Stat(filepath.Join(root, retired)); !os.IsNotExist(err) {
			t.Errorf("retired production path still exists: %s", retired)
		}
	}

	var findings []string
	err := fs.WalkDir(os.DirFS(root), ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		clean := filepath.ToSlash(path)
		if entry.IsDir() {
			if clean == ".git" || clean == "docs" || clean == "tasks" || clean == "examples" ||
				strings.Contains(clean, "/testdata") || strings.Contains(clean, "/node_modules") {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasPrefix(clean, "internal/") && !strings.HasPrefix(clean, "cmd/") {
			return nil
		}
		// Inert metadata may name external plugins; it registers no route,
		// module, runtime, template, or capability implementation.
		//
		// The host-owned folder capability table is inert data: file
		// signatures, tool names, and presentation metadata. Matching and
		// actions live in generic code. The audit below checks that the
		// table does not grow domain-specific imports or behaviors.
		if clean == "internal/server/marketplace_cache_official.json" ||
			clean == "internal/reviewedintegration/entries.go" ||
			clean == "internal/folderdigest/tables.go" {
			return nil
		}
		if strings.HasSuffix(clean, "_test.go") || strings.HasSuffix(clean, ".test.js") {
			return nil
		}
		switch strings.ToLower(filepath.Ext(clean)) {
		case ".go", ".js", ".css", ".tmpl", ".json":
		default:
			return nil
		}
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(clean))) // #nosec G304 -- repository-relative source audit path
		if err != nil {
			return err
		}
		lower := strings.ToLower(string(data))
		for _, forbidden := range []string{
			"reaper", ".rpp", "pinned_reaper_scripts", "/reaper-setup", "/reaper/",
		} {
			if strings.Contains(lower, forbidden) {
				findings = append(findings, clean+" contains "+forbidden)
				break
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Fatalf("compiled domain extraction audit failed:\n%s", strings.Join(findings, "\n"))
	}
}

// Domain metadata must exist only in the merged host table. Generic
// projection and copy helpers may live alongside it, but cannot import a
// plugin or add a second domain registry.
func TestFolderDigestTablesStayHostOwned(t *testing.T) {
	path := filepath.Join("..", "folderdigest", "tables.go")
	data, err := os.ReadFile(filepath.Clean(path)) // #nosec G304 -- fixed repository-relative audit path
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	source := string(data)
	if strings.Contains(source, "\nimport") || strings.Contains(source, "\nconst ") {
		t.Error("folder capability table must not import behavior or define domain-specific constants")
	}
	if strings.Count(source, "\nvar capabilityRows =") != 1 ||
		strings.Count(source, "\nvar Markers, Tools, ShapeBlueprints = deriveTables()") != 1 {
		t.Error("recognition and offer metadata must derive from one host-owned row table")
	}
	if _, err := os.Stat(filepath.Join("..", "specialist", "domains.go")); !os.IsNotExist(err) {
		t.Error("the independent specialist domain table must remain retired")
	}
}

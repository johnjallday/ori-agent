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
		// The specialist mapping's data file is the same shape as the
		// marketplace cache: onboarding copy, a card ordering, and the ID of a
		// blueprint published by a plugin. Everything that acts on it —
		// matching, the hire offer, capability ordering, persistence — is
		// generic and lives elsewhere. The guard below keeps it that way by
		// asserting the file stays data only.
		if clean == "internal/server/marketplace_cache_official.json" ||
			clean == "internal/specialist/domains.go" {
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

// The specialist mapping's data file is excepted from the audit above only
// because it is data. This holds it to that: no imports, no functions, no
// types — one package-level var of mapping entries. A domain that needs code
// in the host is a domain implementation, and belongs in a plugin.
func TestSpecialistDomainDataFileStaysDataOnly(t *testing.T) {
	path := filepath.Join("..", "specialist", "domains.go")
	data, err := os.ReadFile(filepath.Clean(path)) // #nosec G304 -- fixed repository-relative audit path
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	source := string(data)
	for _, forbidden := range []string{"\nimport", "\nfunc ", "\ntype ", "\nconst "} {
		if strings.Contains(source, forbidden) {
			t.Errorf("internal/specialist/domains.go must stay data only; found %q", strings.TrimSpace(forbidden))
		}
	}
	if strings.Count(source, "\nvar ") != 1 {
		t.Error("internal/specialist/domains.go must declare exactly one var: the mapping entries")
	}
}

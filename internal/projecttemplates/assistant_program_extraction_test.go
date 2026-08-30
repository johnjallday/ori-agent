package projecttemplates

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoReaperDomainLanguageOutsidePlugin protects the generic host boundary:
// domain roles, prompts, workflow copy, and product names belong to plugin
// declarations, never the assistant-program parser, store, API, scheduler, or UI.
func TestNoReaperDomainLanguageOutsidePlugin(t *testing.T) {
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Clean(filepath.Join(workingDirectory, "..", ".."))
	patterns := []string{
		"internal/workspace/assistant_*.go",
		"internal/sessionhttp/assistant_program.go",
		"internal/server/assistant_reflection_model.go",
		"internal/web/static/js/modules/assistant-program*.js",
		"internal/web/static/css/assistant-program.css",
		"internal/web/templates/pages/workspace-assistant.tmpl",
		"docs/architecture/assistant-program-contract.md",
	}
	forbidden := []string{"reaper", "music producer", "mix engineer", "songwriter", " daw "}
	for _, pattern := range patterns {
		matches, globErr := filepath.Glob(filepath.Join(root, pattern))
		if globErr != nil {
			t.Fatal(globErr)
		}
		if len(matches) == 0 {
			t.Fatalf("extraction audit pattern matched no files: %s", pattern)
		}
		for _, path := range matches {
			if strings.HasSuffix(path, "_test.go") || strings.HasSuffix(path, ".test.js") {
				continue
			}
			contents, readErr := os.ReadFile(path) // #nosec G304 -- paths come only from the fixed repository-local patterns above
			if readErr != nil {
				t.Fatal(readErr)
			}
			normalized := " " + strings.ToLower(string(contents)) + " "
			for _, term := range forbidden {
				if strings.Contains(normalized, term) {
					t.Errorf("generic host file %s contains plugin-domain term %q", filepath.ToSlash(strings.TrimPrefix(path, root+string(filepath.Separator))), strings.TrimSpace(term))
				}
			}
		}
	}
}

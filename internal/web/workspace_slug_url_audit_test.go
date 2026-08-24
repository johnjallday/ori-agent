package web

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestWorkspaceBrowserURLsDoNotUseInternalIDs is a narrow regression audit for
// the hard page-route replacement. API URLs still use UUIDs; only browser URLs
// under /workspaces/ are prohibited from interpolating ID-shaped values.
func TestWorkspaceBrowserURLsDoNotUseInternalIDs(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	roots := []string{
		filepath.Join(root, "internal", "web", "static", "js"),
		filepath.Join(root, "internal", "web", "templates"),
		filepath.Join(root, "internal", "server"),
		filepath.Join(root, "internal", "agenthttp"),
		filepath.Join(root, "internal", "chathttp"),
		filepath.Join(root, "internal", "workspace"),
		filepath.Join(root, "internal", "runtimecapability"),
		filepath.Join(root, "internal", "workspaceplan"),
	}
	forbidden := []*regexp.Regexp{
		regexp.MustCompile(`/workspaces/\$\{[^}\n]*(?:workspaceI[Dd]|studioI[Dd]|selectedId|\.id)\b`),
		regexp.MustCompile(`(?s)/workspaces/["'\x60]?\s*\+\s*encodeURIComponent\([^)]*(?:workspaceI[Dd]|studioI[Dd]|selectedId|\.id)\b`),
		regexp.MustCompile(`(?s)"/workspaces/"\s*\+\s*(?:workspaceID|task\.WorkspaceID|plan\.WorkspaceID|ws\.ID)\b`),
		regexp.MustCompile(`href="/workspaces/\{\{\.Extra\.WorkspaceID\}\}`),
	}

	var findings []string
	for _, scanRoot := range roots {
		err := filepath.WalkDir(scanRoot, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			ext := strings.ToLower(filepath.Ext(path))
			if ext != ".js" && ext != ".go" && ext != ".tmpl" {
				return nil
			}
			if strings.HasSuffix(path, "_test.go") || strings.HasSuffix(path, ".test.js") {
				return nil
			}
			contents, err := os.ReadFile(path) // #nosec G304 -- paths come only from fixed repository roots above.
			if err != nil {
				return err
			}
			// UUID APIs are intentionally unchanged and are outside this audit.
			source := strings.ReplaceAll(string(contents), "/api/workspaces/", "/api/__workspace_uuid__/")
			for _, pattern := range forbidden {
				if loc := pattern.FindStringIndex(source); loc != nil {
					start := max(0, loc[0]-45)
					end := min(len(source), loc[1]+65)
					findings = append(findings, fmt.Sprintf("%s: %q", path, source[start:end]))
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("scan workspace browser URLs: %v", err)
		}
	}
	if len(findings) > 0 {
		t.Fatalf("workspace browser URLs must use an explicit slug/helper; UUID APIs remain allowed:\n%s", strings.Join(findings, "\n"))
	}
}

func TestCanonicalWorkspaceRouteSourcesNameSlugsExplicitly(t *testing.T) {
	required := map[string]string{
		"static/js/modules/workspace-routes.js":                    "workspaceSlug",
		"static/js/modules/home-workspace-cockpit.js":              "folder_slug",
		"static/js/modules/workspace-detail.js":                    "this.workspaceSlug",
		"templates/pages/workspace-detail.tmpl":                    "WorkspaceSlug",
		filepath.Join("..", "server", "home_assistant_ask.go"):     "FolderSlug",
		filepath.Join("..", "chathttp", "route_context_prompt.go"): "WorkspaceSlug",
		filepath.Join("..", "workspace", "task_markdown_sync.go"):  "workspaceSlug",
		filepath.Join("..", "agenthttp", "ori_guide_dynamic.go"):   "FolderSlug",
	}
	for path, marker := range required {
		contents, err := os.ReadFile(path) // #nosec G304 -- every path is a fixed test constant.
		if err != nil {
			t.Fatalf("read canonical route source %s: %v", path, err)
		}
		if !strings.Contains(string(contents), marker) {
			t.Errorf("%s no longer names canonical route identity with %q", path, marker)
		}
	}
}

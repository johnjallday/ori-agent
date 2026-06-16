package agenthttp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestHomeNavCatalogHrefsAreRegisteredRoutes asserts every catalog href maps to a
// route registered in internal/server/routes.go, keeping the catalog honest
// (PRD 4.8 / FR #33).
func TestHomeNavCatalogHrefsAreRegisteredRoutes(t *testing.T) {
	routesPath := filepath.Join("..", "server", "routes.go")
	raw, err := os.ReadFile(routesPath)
	if err != nil {
		t.Fatalf("read routes.go: %v", err)
	}
	routes := string(raw)
	for _, entry := range HomeNavCatalog() {
		needle := `"` + entry.Href + `"`
		if !strings.Contains(routes, needle) {
			t.Errorf("catalog entry %q href %q is not a registered route in routes.go", entry.Key, entry.Href)
		}
	}
}

func TestFindHomeNavEntry(t *testing.T) {
	cases := map[string]string{
		"Action Center":   "action-center",
		"mcp connectors":  "mcp",
		"my profile":      "profile",
		"settings":        "settings",
		"the agents page": "agents",
	}
	for query, wantKey := range cases {
		entry, ok := FindHomeNavEntry(query)
		if !ok {
			t.Errorf("FindHomeNavEntry(%q): expected a match", query)
			continue
		}
		if entry.Key != wantKey {
			t.Errorf("FindHomeNavEntry(%q): got %q, want %q", query, entry.Key, wantKey)
		}
	}
	if _, ok := FindHomeNavEntry("definitely not a page"); ok {
		t.Error("FindHomeNavEntry: unexpected match for unknown phrase")
	}
}

func TestMatchNavEntryInPrompt(t *testing.T) {
	entry, ok := MatchNavEntryInPrompt("where do I manage my mcp connectors again")
	if !ok || entry.Key != "mcp" {
		t.Fatalf("expected mcp match, got %q ok=%v", entry.Key, ok)
	}
	if _, ok := MatchNavEntryInPrompt("summarize my recent activity"); ok {
		t.Error("did not expect a catalog match for an introspection prompt without a feature name")
	}
}

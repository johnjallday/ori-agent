package web

import (
	"strings"
	"testing"
)

// TestLoadTemplates_Parses confirms every embedded template parses cleanly.
// Catches malformed Go template syntax in the .tmpl files at test time
// rather than at runtime when a page is requested.
func TestLoadTemplates_Parses(t *testing.T) {
	r := NewTemplateRenderer()
	if err := r.LoadTemplates(); err != nil {
		t.Fatalf("LoadTemplates failed: %v", err)
	}
}

// TestRenderGroupDetailPage confirms the group-detail page is registered and
// renders with the server-injected group ID and its scaffold markers.
func TestRenderGroupDetailPage(t *testing.T) {
	r := NewTemplateRenderer()
	if err := r.LoadTemplates(); err != nil {
		t.Fatalf("LoadTemplates failed: %v", err)
	}

	data := TemplateData{
		Title: "Group - Ori Agent",
		Extra: map[string]any{"WorkspaceID": "grp-123"},
	}
	html, err := r.RenderTemplate("group-detail", data)
	if err != nil {
		t.Fatalf("RenderTemplate(group-detail) failed: %v", err)
	}

	for _, want := range []string{
		`id="group-detail-view"`,
		`id="group-not-found"`,
		"/js/modules/group-detail.js",
		"grp-123",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("rendered group-detail page missing %q", want)
		}
	}
}

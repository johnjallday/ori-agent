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

// TestRenderWorkspaceDetailGroupScaffold confirms the workspace-detail page
// (which groups now share) carries the group-only scaffold: the Members panel
// and the header identity elements, all hidden until the page detects a group.
func TestRenderWorkspaceDetailGroupScaffold(t *testing.T) {
	r := NewTemplateRenderer()
	if err := r.LoadTemplates(); err != nil {
		t.Fatalf("LoadTemplates failed: %v", err)
	}

	data := TemplateData{
		Title: "Workspace - Ori Agent",
		Extra: map[string]any{"WorkspaceID": "grp-123"},
	}
	html, err := r.RenderTemplate("workspace-detail", data)
	if err != nil {
		t.Fatalf("RenderTemplate(workspace-detail) failed: %v", err)
	}

	for _, want := range []string{
		`id="workspace-detail-members-panel"`,
		`id="workspace-detail-members-list"`,
		`id="workspace-group-badge"`,
		`id="workspace-group-color"`,
		`id="workspace-member-stat"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("rendered workspace-detail page missing %q", want)
		}
	}
}

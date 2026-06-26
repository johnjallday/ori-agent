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

// TestRenderTemplatesPage confirms the /templates page renders, carries the
// master/detail scaffold and lifecycle controls, and highlights its sidebar
// link when CurrentPage is "templates".
func TestRenderTemplatesPage(t *testing.T) {
	r := NewTemplateRenderer()
	if err := r.LoadTemplates(); err != nil {
		t.Fatalf("LoadTemplates failed: %v", err)
	}

	data := TemplateData{Title: "Templates - Ori Agent", CurrentPage: "templates"}
	html, err := r.RenderTemplate("templates", data)
	if err != nil {
		t.Fatalf("RenderTemplate(templates) failed: %v", err)
	}

	for _, want := range []string{
		`id="tplList"`,
		`id="tplCreateBtn"`,
		`id="tplImportBtn"`,
		`id="tplDetail"`,
		`id="tplEditTags"`,
		`id="tplNameModal"`,
		`id="tplFileTree"`,
		`id="tplEditorTextarea"`,
		`id="tplDirtyModal"`,
		`/js/modules/templates-page.js`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("rendered templates page missing %q", want)
		}
	}

	// The Templates sidebar link should be marked active for this page.
	if !strings.Contains(html, `href="/templates" class="sidebar-nav-link active"`) {
		t.Errorf("templates sidebar link not highlighted as active")
	}
}

func TestRenderAgentsDetailDefaultsSidebarHidden(t *testing.T) {
	r := NewTemplateRenderer()
	if err := r.LoadTemplates(); err != nil {
		t.Fatalf("LoadTemplates failed: %v", err)
	}

	html, err := r.RenderTemplate("agents-detail", TemplateData{Title: "Agent Detail - Ori Agent"})
	if err != nil {
		t.Fatalf("RenderTemplate(agents-detail) failed: %v", err)
	}

	if !strings.Contains(html, `data-sidebar-default="hidden"`) {
		t.Fatalf("rendered agents-detail page should default the sidebar to hidden")
	}
	if strings.Contains(html, `data-sidebar-default="visible"`) {
		t.Fatalf("rendered agents-detail page should not default the sidebar to visible")
	}
}

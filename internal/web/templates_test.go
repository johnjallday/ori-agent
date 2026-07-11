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

// TestRenderWorkspaceDetailSharedHosts confirms the workspace-detail page (now
// Command-only) carries the hidden shared-hosts container the Command view
// mounts live DOM into, plus the Command mount and the Members panel the
// Detachment surface reuses. The old Detailed subtree and its header identity
// elements are gone.
func TestRenderWorkspaceDetailSharedHosts(t *testing.T) {
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
		`id="workspaceCommandView"`,
		`id="workspace-detail-shared-hosts"`,
		`id="workspace-detail-settings-panel"`,
		`id="workspace-detail-tasks-board"`,
		`id="workspace-detail-tools-card"`,
		`id="workspace-detail-members-panel"`,
		`id="workspace-detail-members-list"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("rendered workspace-detail page missing %q", want)
		}
	}

	// The deleted Detailed view must be gone.
	for _, gone := range []string{
		`id="workspace-detail-view"`,
		`id="workspace-command-toggle"`,
		`id="workspaceDetailPanelBackdrop"`,
	} {
		if strings.Contains(html, gone) {
			t.Errorf("rendered workspace-detail page still contains deleted element %q", gone)
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
		`id="tplStarterTasksList"`,
		`id="tplStarterTaskAddBtn"`,
		`id="tplEditProjectEntryPath"`,
		`id="tplEditProjectEntryDefault"`,
		`id="tplToolsSkills"`,
		`id="tplToolsMcp"`,
		`id="tplToolsPlugins"`,
		`id="tplToolsSaveBtn"`,
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

func TestRenderCreateWorkspaceProjectOpenOption(t *testing.T) {
	r := NewTemplateRenderer()
	if err := r.LoadTemplates(); err != nil {
		t.Fatalf("LoadTemplates failed: %v", err)
	}

	html, err := r.RenderTemplate("workspaces", TemplateData{Title: "Workspaces - Ori Agent"})
	if err != nil {
		t.Fatalf("RenderTemplate(workspaces) failed: %v", err)
	}
	for _, want := range []string{
		`id="projectTemplateOpenAfterCreate"`,
		`id="projectTemplateOpenAfterCreateToggle"`,
		`Open project after creation`,
		`Uses your system's default app for this file type.`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("rendered Create Workspace modal missing %q", want)
		}
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

func TestRenderAgentsCodexDetailPage(t *testing.T) {
	r := NewTemplateRenderer()
	if err := r.LoadTemplates(); err != nil {
		t.Fatalf("LoadTemplates failed: %v", err)
	}

	html, err := r.RenderTemplate("agents-codex-detail", TemplateData{Title: "Codex - Ori Agent"})
	if err != nil {
		t.Fatalf("RenderTemplate(agents-codex-detail) failed: %v", err)
	}

	for _, want := range []string{
		`id="codexDetailTitle"`,
		`id="codexSyncContent"`,
		`/js/modules/codex-sync.js`,
		`/js/agents-codex-detail.js`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("rendered agents-codex-detail page missing %q", want)
		}
	}
}

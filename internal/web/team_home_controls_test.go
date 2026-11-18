package web

import (
	"strings"
	"testing"
)

func TestTeamHomeSharedControlsRetainHostAndTableSemantics(t *testing.T) {
	r := NewTemplateRenderer()
	if err := r.LoadTemplates(); err != nil {
		t.Fatal(err)
	}
	html, err := r.RenderTemplate("workspace-assistant", GetDefaultData())
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		`class="assistant-program-body ui-page-body"`,
		`class="assistant-program-hero ui-page-header"`,
		`id="assistantProgramWorkspaceLink" class="modern-btn modern-btn-secondary"`,
		`class="assistant-program-stage-chip badge badge-secondary"`,
		`class="assistant-program-hire-dialog ui-section"`,
		`class="project-library-tabs nav-tabs" role="tablist"`,
		`class="project-library-table" role="table"`,
		`id="projectLibraryRows" role="rowgroup"`,
		`class="project-library-table-wrap" role="region" tabindex="0"`,
		`class="sample-library-roots" role="group"`,
	} {
		if !strings.Contains(html, fragment) {
			t.Errorf("missing Team Home contract %q", fragment)
		}
	}
	for _, id := range []string{"projectLibraryTabOverview", "projectLibraryTabFiles", "projectLibraryTabSessions"} {
		if !strings.Contains(html, `id="`+id+`" type="button" class="nav-link" role="tab"`) {
			t.Errorf("%s must remain a native tab consuming shared controls", id)
		}
	}
	for _, path := range []string{"static/css/assistant-program.css", "static/css/project-library-browser.css"} {
		css, err := Static.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, retired := range []string{"Chakra Petch", "!important", "--bs-"} {
			if strings.Contains(string(css), retired) {
				t.Errorf("%s retains private typography/override %q", path, retired)
			}
		}
	}
}

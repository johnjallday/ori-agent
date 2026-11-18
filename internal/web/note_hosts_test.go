package web

import (
	"strings"
	"testing"
)

func TestNoteHostsLoadSharedEditorModules(t *testing.T) {
	r := NewTemplateRenderer()
	if err := r.LoadTemplates(); err != nil {
		t.Fatal(err)
	}
	for _, page := range []string{"index", "workspace-detail", "note-page"} {
		t.Run(page, func(t *testing.T) {
			html, err := r.RenderTemplate(page, GetDefaultData())
			if err != nil {
				t.Fatal(err)
			}
			for _, module := range []string{"note-editor", "note-toc", "note-staging", "note-wikilinks", "note-backlinks", "note-presence", "note-rail-notes", "note-ai-assist"} {
				tag := `<script type="module" src="/js/modules/` + module + `.js"></script>`
				if count := strings.Count(html, tag); count != 1 {
					t.Errorf("%s must load %s exactly once; got %d", page, module, count)
				}
			}
		})
	}
}

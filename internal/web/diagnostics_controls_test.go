package web

import (
	"strings"
	"testing"
)

func TestDiagnosticsUsesSharedControlsAndReadableResultRegion(t *testing.T) {
	r := NewTemplateRenderer()
	if err := r.LoadTemplates(); err != nil {
		t.Fatal(err)
	}
	html, err := r.RenderTemplate("workspace-diagnostics", GetDefaultData())
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		`<header class="ui-page-header diagnostics-header">`,
		`<h1>Diagnostics</h1>`,
		`class="ui-section workspace-detail-health-card`,
		`id="workspace-detail-health-refresh" class="modern-btn modern-btn-secondary`,
		`id="workspace-detail-ui-smoke-test" class="modern-btn modern-btn-secondary`,
		`role="region" tabindex="0" aria-label="UI route check results"`,
	} {
		if !strings.Contains(html, fragment) {
			t.Errorf("missing Diagnostics contract %q", fragment)
		}
	}
	css, err := Static.ReadFile("static/css/workspace-health.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, alias := range []string{"--health-success: var(--success-text)", "--health-warning: var(--warning-text)", "--health-danger: var(--danger-text)", "--health-info: var(--info-text)"} {
		if !strings.Contains(string(css), alias) {
			t.Errorf("health palette must consume shared semantic token: %s", alias)
		}
	}
}

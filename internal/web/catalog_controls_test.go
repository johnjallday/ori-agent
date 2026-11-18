package web

import (
	"strings"
	"testing"
)

func TestCatalogRetainsNativeRolesWithSharedControls(t *testing.T) {
	r := NewTemplateRenderer()
	if err := r.LoadTemplates(); err != nil {
		t.Fatal(err)
	}
	html, err := r.RenderTemplate("workspace-catalog", GetDefaultData())
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		`class="resource-catalog-header ui-page-header"`,
		`id="resourceCatalogWorkspaceLink" class="modern-btn modern-btn-secondary"`,
		`class="resource-catalog-tabs nav-tabs" role="tablist"`,
		`id="resourceCatalogSearch" class="form-control" type="search"`,
		`id="resourceCatalogSource" class="form-select"`,
		`id="resourceCatalogFolderPath" class="form-control"`,
		`class="resource-catalog-detail ui-section"`,
	} {
		if !strings.Contains(html, fragment) {
			t.Errorf("missing Catalog control contract %q", fragment)
		}
	}
	for _, id := range []string{"resourceCatalogReview", "resourceCatalogWithdraw", "resourceCatalogFolder", "resourceCatalogRemoveFolder"} {
		if !strings.Contains(html, `id="`+id+`" class="resource-catalog-dialog ui-section"`) {
			t.Errorf("%s must keep its native dialog with a shared surface", id)
		}
	}
	if strings.Index(html, `id="resourceCatalogTitle"`) > strings.Index(html, `id="resourceCatalogContent"`) {
		t.Error("Catalog heading must remain available when the content read fails")
	}
	css, err := Static.ReadFile("static/css/resource-catalog.css")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(css), "--bs-") {
		t.Error("Catalog must consume the canonical semantic palette, not another Bootstrap skin")
	}
}

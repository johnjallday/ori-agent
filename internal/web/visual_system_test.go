package web

import (
	"regexp"
	"strings"
	"testing"
)

func TestSharedVisualTokensHaveOneOwner(t *testing.T) {
	// Feature-local geometry/artwork tokens are intentionally outside this
	// contract. It prevents a later shared layer silently replacing the palette.
	core := regexp.MustCompile(`(?m)^\s*--(?:bg-(?:primary|secondary|tertiary)|text-(?:primary|secondary|muted)|primary-(?:color|light|dark)|radius-(?:sm|md|lg|xl)|font-(?:body|display|mono)|modal-padding|ui-space-\w+|ui-text-[\w-]+)\s*:`)
	for _, path := range []string{"static/styles.css", "static/css/components.css", "static/css/common.css", "static/css/ui-refresh.css"} {
		data, err := Static.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if match := core.Find(data); match != nil {
			t.Errorf("%s redefines shared token %q; use variables.css", path, match)
		}
	}
	data, err := Static.ReadFile("static/css/variables.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"--font-body:", "--surface-solid:", "--focus-color:", "--control-height:", "--touch-target:", "--app-modal-layer:", "--bg-color: var(--bg-primary)", "--card-bg: var(--bg-secondary)"} {
		if !strings.Contains(string(data), token) {
			t.Errorf("missing canonical token/compatibility alias %q", token)
		}
	}
	if strings.Contains(string(data), "@import") {
		t.Error("font loading belongs to the shared head, not an extra imported family")
	}
}

func TestVisualTokenOwnerLoadsBeforeSharedConsumers(t *testing.T) {
	r := NewTemplateRenderer()
	if err := r.LoadTemplates(); err != nil {
		t.Fatal(err)
	}
	for _, page := range []string{"index", "skills", "plugins", "settings", "agents-create", "agents-roster", "workspace-detail", "workspace-diagnostics", "models"} {
		html, err := r.RenderTemplate(page, GetDefaultData())
		if err != nil {
			t.Fatalf("%s: %v", page, err)
		}
		assertTokenAssetOrder(t, page, html)
	}
	// Retained static hosts are not a second theme definition, even if reached
	// outside the current server routes.
	for _, path := range []string{"static/agents-create.html", "static/workspaces.html"} {
		data, err := Static.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		assertTokenAssetOrder(t, path, string(data))
	}
}

func TestPilotPagesPreserveControlHooksAndHierarchy(t *testing.T) {
	r := NewTemplateRenderer()
	if err := r.LoadTemplates(); err != nil {
		t.Fatal(err)
	}
	for page, ids := range map[string][]string{
		"skills":  {"skillsCreateBtn", "skillsDiscoverBtn", "skillsAgentSelect", "skillsSearch", "skillsRefreshBtn", "skillsImportMenuItem", "skillsImportPanel", "skillsList", "skillsEditorForm", "skillsEditorModal", "skillsSaveBtn"},
		"plugins": {"pluginList", "pluginInstalledHeading", "pluginSource", "pluginFormat", "pluginTrust", "pluginTrustConfirm", "pluginUpdateModal", "pluginUpdateConfirm", "officialModal", "marketplaceSource", "marketplaceList"},
	} {
		html, err := r.RenderTemplate(page, GetDefaultData())
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(html, `<header class="ui-page-header">`) {
			t.Errorf("%s lacks the shared header", page)
		}
		for _, id := range ids {
			if strings.Count(html, `id="`+id+`"`) != 1 {
				t.Errorf("%s must preserve exactly one #%s", page, id)
			}
		}
		if page == "plugins" {
			list := strings.Index(html, `id="pluginList"`)
			install := strings.Index(html, `<details id="pluginInstallSection"`)
			if install < list {
				t.Error("installed plugins must precede the secondary installation disclosure")
			}
		} else if !strings.Contains(html, `for="skillsSearch">Search skills</label>`) {
			t.Error("Skills search must retain an explicit accessible label")
		}
	}
}

func assertTokenAssetOrder(t *testing.T, name, html string) {
	t.Helper()
	owner := strings.Index(html, `href="/css/variables.css"`)
	consumer := strings.Index(html, `href="/styles.css"`)
	if owner < 0 || consumer < owner {
		t.Errorf("%s: variables must load before legacy styles (owner %d, consumer %d)", name, owner, consumer)
	}
	if strings.Count(html, `href="/css/variables.css"`) != 1 {
		t.Errorf("%s: token owner must be loaded once", name)
	}
}

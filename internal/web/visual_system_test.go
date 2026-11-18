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

func TestSettingsAndProfileUseSharedFieldsAndAccessibleNavigation(t *testing.T) {
	refresh, err := Static.ReadFile("static/css/ui-refresh.css")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(refresh), "body.settings-page .settings-") || strings.Contains(string(refresh), "body.settings-page .ui-density-option") {
		t.Error("Settings geometry and density controls belong to settings.css, not a later override layer")
	}
	r := NewTemplateRenderer()
	if err := r.LoadTemplates(); err != nil {
		t.Fatal(err)
	}
	classes := regexp.MustCompile(`class="([^"]*\bsettings-input\b[^"]*)"`)
	for _, page := range []string{"settings", "profile"} {
		html, err := r.RenderTemplate(page, GetDefaultData())
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range classes.FindAllStringSubmatch(html, -1) {
			if !strings.Contains(match[1], "form-control") && !strings.Contains(match[1], "form-select") {
				t.Errorf("%s: field hook %q must retain its shared control class", page, match[1])
			}
		}
		if page == "settings" {
			for _, hook := range []string{`id="settingsNavDisclosure"`, `class="ui-disclosure-title"`, `id="settingsSearch"`, `href="#voice-language"`, `id="resetConfirmModal"`, `id="confirmResetBtn"`, `aria-label="Settings sections"`} {
				if !strings.Contains(html, hook) {
					t.Errorf("Settings lost navigation/confirmation hook %s", hook)
				}
			}
		} else if !strings.Contains(html, "Excluded until you approve it") || !strings.Contains(html, "Stays inside its workspace") {
			t.Error("Profile must keep approval and workspace scope explanations explicit")
		}
	}
}

func TestVaultDialogsKeepUniqueControllerHooks(t *testing.T) {
	r := NewTemplateRenderer()
	if err := r.LoadTemplates(); err != nil {
		t.Fatal(err)
	}
	html, err := r.RenderTemplate("vault", GetDefaultData())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"vaultPageEmptySetup", "vaultPageSearchInput", "vaultExplorerAddBtn", "vaultOpenCreateDialogBtn", "vaultCreateOverlay", "vaultUnlockOverlay", "vaultEntryOverlay", "vaultImportOverlay", "vaultConfirmVaultPassword", "vaultImportConfirmVaultPassword", "vaultSaveEntryBtn", "vaultDeleteEntryBtn"} {
		if strings.Count(html, `id="`+id+`"`) != 1 {
			t.Errorf("Vault must preserve exactly one #%s", id)
		}
	}
	if !strings.Contains(html, `for="vaultImportConfirmVaultPassword"`) {
		t.Error("import confirmation must label its own field, not the creation password")
	}
	if !strings.Contains(html, `<summary class="ui-disclosure-title">More actions</summary>`) {
		t.Error("secondary Vault actions require a native keyboard disclosure")
	}
}

func TestTemplatesKeepNativeNavigationAndNamedDialogs(t *testing.T) {
	r := NewTemplateRenderer()
	if err := r.LoadTemplates(); err != nil {
		t.Fatal(err)
	}
	html, err := r.RenderTemplate("templates", GetDefaultData())
	if err != nil {
		t.Fatal(err)
	}
	for _, hook := range []string{`<h1>Templates</h1>`, `href="/settings#templates"`, `<a id="tplQuestOpen"`, `id="tplGroupPreviewBtn"`, `id="tplBackToLibrary"`, `aria-labelledby="tplNameModalTitle"`, `aria-labelledby="tplDirtyModalTitle"`, `id="tplRootPath"`} {
		if !strings.Contains(html, hook) {
			t.Errorf("Templates lost hierarchy/navigation/confirmation hook %s", hook)
		}
	}
}

func TestMCPUsesNamedDialogsAndControlledSourcesDisclosure(t *testing.T) {
	r := NewTemplateRenderer()
	if err := r.LoadTemplates(); err != nil {
		t.Fatal(err)
	}
	html, err := r.RenderTemplate("mcp", GetDefaultData())
	if err != nil {
		t.Fatal(err)
	}
	for _, hook := range []string{`<h1>MCP Servers</h1>`, `<button id="mcpSourcesToggle"`, `aria-expanded="false" aria-controls="sourcesPanel"`, `aria-labelledby="addServerTitle"`, `aria-labelledby="removeServerTitle"`, `aria-labelledby="addSourceTitle"`, `aria-labelledby="serverDetailsTitle"`, `for="manualName"`, `for="browseSearch"`, `src="/js/modules/toast.js"`} {
		if !strings.Contains(html, hook) {
			t.Errorf("MCP lost shared control/disclosure hook %s", hook)
		}
	}
}

func TestModelsKeepNamedDialogsAndNativeControlHooks(t *testing.T) {
	r := NewTemplateRenderer()
	if err := r.LoadTemplates(); err != nil {
		t.Fatal(err)
	}
	html, err := r.RenderTemplate("models", GetDefaultData())
	if err != nil {
		t.Fatal(err)
	}
	for _, hook := range []string{`<h1>Available Models</h1>`, `id="categoryEditorForm" novalidate`, `type="submit" form="categoryEditorForm"`, `class="badge category-badge-preview category-swatch-badge"`, `aria-labelledby="modelsViewLabel"`, `aria-labelledby="categoryModalTitle"`, `aria-labelledby="categoryEditorTitle"`, `aria-labelledby="assignmentModalTitle"`, `aria-labelledby="autoCategorizeTitle"`, `for="categoryName"`, `for="customColor"`, `aria-labelledby="categoryColorLabel"`, `aria-labelledby="categoryIconLabel"`, `id="viewProvider"`, `id="viewCategory"`, `src="/js/modules/toast.js"`} {
		if !strings.Contains(html, hook) {
			t.Errorf("Models lost a named dialog/control hook %s", hook)
		}
	}
}

func TestWorkflowsKeepCompactHierarchyAndNativeLibraryHooks(t *testing.T) {
	r := NewTemplateRenderer()
	if err := r.LoadTemplates(); err != nil {
		t.Fatal(err)
	}
	html, err := r.RenderTemplate("workflows", GetDefaultData())
	if err != nil {
		t.Fatal(err)
	}
	for _, hook := range []string{`<h1 class="behavior-page__title">Workflows</h1>`, `id="behaviorStudioLibrary" open`, `<summary class="ui-disclosure-title behavior-library-summary">`, `for="behaviorStudioSearch"`, `for="behaviorStudioSourceFilter"`, `for="behaviorStudioCategoryFilter"`, `id="behaviorStudioNewBtn"`, `id="behaviorStudioEditor"`, `id="behaviorStudioPreview"`, `aria-labelledby="behaviorLaunchModalLabel"`, `id="behaviorLaunchSubmit"`, `src="/js/modules/behavior-studio.js"`} {
		if !strings.Contains(html, hook) {
			t.Errorf("Workflows lost a shared hierarchy/native control hook %s", hook)
		}
	}
	source, err := Templates.ReadFile("templates/pages/workflows.tmpl")
	if err != nil {
		t.Fatal(err)
	}
	for _, obsolete := range []string{`max-width: 13ch`, `behaviorPanelRise`, `backdrop-filter: blur`, `"Avenir Next"`, `--behavior-panel-sheen`} {
		if strings.Contains(string(source), obsolete) {
			t.Errorf("Workflows retained superseded local primitive %s", obsolete)
		}
	}
}

func TestReportsKeepNamedControlsAndBoundedDataTables(t *testing.T) {
	r := NewTemplateRenderer()
	if err := r.LoadTemplates(); err != nil {
		t.Fatal(err)
	}
	pages := map[string][]string{
		"usage":  {`<h1>Usage &amp; Costs</h1>`, `id="usageRefreshBtn"`, `id="usageFeedback"`, `id="usageEmpty"`, `ui-metric-grid`, `aria-label="Usage by model"`, `scope="col"`},
		"review": {`<h1>Conversation Review</h1>`, `id="filterAgent"`, `id="filterType"`, `id="reviewExportMenu"`, `<a class="btn btn-secondary" id="reviewExportJSON"`, `aria-labelledby="triggerModalTitle"`, `for="triggerSensitivity"`, `for="triggerAgent"`, `id="reviewProgressSection"`, `id="reviewResult"`, `aria-label="Detected conversation issues"`},
	}
	for page, hooks := range pages {
		html, err := r.RenderTemplate(page, GetDefaultData())
		if err != nil {
			t.Fatal(err)
		}
		for _, hook := range hooks {
			if !strings.Contains(html, hook) {
				t.Errorf("%s lost shared report/control hook %s", page, hook)
			}
		}
	}
}

func TestActionCenterKeepsSharedHierarchyAndTriageHooks(t *testing.T) {
	r := NewTemplateRenderer()
	if err := r.LoadTemplates(); err != nil {
		t.Fatal(err)
	}
	html, err := r.RenderTemplate("action-center", GetDefaultData())
	if err != nil {
		t.Fatal(err)
	}
	for _, hook := range []string{`<h1>Action Center</h1>`, `for="action-center-status-filter"`, `for="action-center-sort"`, `id="action-center-results" aria-label="Findings" aria-busy="true"`, `id="action-center-list" hidden`, `aria-labelledby="action-center-dismiss-title"`, `aria-labelledby="action-center-snooze-title"`, `id="action-center-snooze-error"`, `id="action-center-dismiss-confirm"`, `id="action-center-snooze-custom-go"`} {
		if !strings.Contains(html, hook) {
			t.Errorf("Action Center lost shared hierarchy/triage hook %s", hook)
		}
	}
	components, err := Static.ReadFile("static/css/components.css")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(components), ".action-center-library") {
		t.Error("Action Center library geometry belongs to action-center.css, not a competing shared owner")
	}
}

func TestNavbarUsesSharedOwnersAndKeepsNativeHooks(t *testing.T) {
	r := NewTemplateRenderer()
	if err := r.LoadTemplates(); err != nil {
		t.Fatal(err)
	}
	for _, fixed := range []bool{false, true} {
		data := GetDefaultData()
		data.CurrentPage = "settings"
		data.BrandText = "Ori Agent"
		data.NavbarFixed = fixed
		html, err := r.RenderTemplate("settings", data)
		if err != nil {
			t.Fatal(err)
		}
		nav := navbarMarkup(t, html)
		for _, hook := range []string{`navbar ori-navbar`, `aria-label="Primary navigation"`, `class="ori-navbar-brand-text">Ori Agent</span>`, `var(--accent-text)`, `href="/agents" id="navAgentsLink"`, `id="vaultLauncherButton"`, `id="vaultNavCount"`, `data-bs-target="#vaultModal"`, `id="navSettingsLink"`, `aria-current="page"`, `id="oriGuideLauncher"`, `id="keyboardNavigationToggle"`, `id="darkModeToggle"`} {
			if !strings.Contains(nav, hook) {
				t.Errorf("fixed=%v: navbar lost named native hook %s", fixed, hook)
			}
		}
		if strings.Contains(nav, "backdrop-filter") || strings.Contains(nav, "modern-card glassmorphism") {
			t.Errorf("fixed=%v: navbar still competes with legacy glass/card surfaces", fixed)
		}
		if strings.Contains(nav, "position-fixed") != fixed {
			t.Errorf("fixed=%v: lost the caller's positioning contract", fixed)
		}
	}
	data := GetDefaultData()
	data.BrandColor = "#123456"
	data.BrandText = "A caller's custom brand"
	data.BrandURL = "/agents"
	data.UseLegacyIcon = true
	html, err := r.RenderTemplate("skills", data)
	if err != nil {
		t.Fatal(err)
	}
	nav := navbarMarkup(t, html)
	for _, hook := range []string{`style="color: #123456;"`, `data-icon="cpu"`, `style="color: currentColor;"`, `href="/agents"`} {
		if !strings.Contains(nav, hook) {
			t.Errorf("navbar lost the caller's brand hook %s", hook)
		}
	}
	for _, path := range []string{"static/styles.css", "static/css/common.css", "static/css/ui-refresh.css"} {
		data, err := Static.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, obsolete := range []string{".nav-link-item {", ".ori-navbar-shell {", ".ori-navbar-vault-count {"} {
			if strings.Contains(string(data), obsolete) {
				t.Errorf("%s competes with the navbar's shared component/layout owners: %s", path, obsolete)
			}
		}
	}
}

func TestAgentsCollectionKeepsSharedControlsAndNativeGalleryHooks(t *testing.T) {
	r := NewTemplateRenderer()
	if err := r.LoadTemplates(); err != nil {
		t.Fatal(err)
	}
	html, err := r.RenderTemplate("agents-roster", GetDefaultData())
	if err != nil {
		t.Fatal(err)
	}
	for _, hook := range []string{`class="ui-page-header collection-head"`, `class="collection-summary"`, `id="rosterSearch" class="form-control"`, `class="form-select" id="rosterSort"`, `id="filtersPanel" aria-labelledby="filtersPanelTitle"`, `class="modern-btn modern-btn-primary stage__fullpage"`, `class="nav nav-tabs stage__tabs"`, `id="inspector" aria-label="Selected agent"`, `id="bulkDeleteDialog" aria-labelledby="bulkDeleteTitle"`, `id="rosterCoachmark"`, `id="rosterFaces"`, `src="/js/agents-roster.js"`} {
		if !strings.Contains(html, hook) {
			t.Errorf("Agents lost a shared control or native gallery hook: %s", hook)
		}
	}
	data, err := Static.ReadFile("static/css/agents-roster.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, competing := range []string{"--bg-primary:", "--text-primary:", ".btn-primary {", ".btn-danger {", ".btn-ghost {"} {
		if strings.Contains(string(data), competing) {
			t.Errorf("Agents must not redefine shared palette/control primitives: %s", competing)
		}
	}
}

func TestCompactButtonPaddingHasOneOwner(t *testing.T) {
	common, err := Static.ReadFile("static/css/common.css")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(common), ".modern-btn-sm {") {
		t.Fatal("touch target floors must not redefine compact button padding")
	}
	components, err := Static.ReadFile("static/css/components.css")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(components), "--bs-btn-padding-x: 0.75rem;") {
		t.Fatal("components.css must retain the compact padding contract")
	}
	if !strings.Contains(string(components), ".btn-close {\n  box-sizing: border-box;\n  min-width: var(--control-height);\n  min-height: var(--control-height);") {
		t.Fatal("Bootstrap close artwork must keep shared border-box hit-area geometry")
	}
}

func TestReducedMotionDoesNotCreateStaticThemeTransitions(t *testing.T) {
	data, err := Static.ReadFile("static/css/common.css")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "transition-duration: 0s !important;") || strings.Contains(string(data), "transition-duration: 0.01ms !important;") {
		t.Error("reduced motion must not introduce a nonzero transition duration on otherwise static elements")
	}
}

func TestAgentCreateUsesSharedSurfacesAndControls(t *testing.T) {
	page, err := Templates.ReadFile("templates/pages/agents-create.tmpl")
	if err != nil {
		t.Fatal(err)
	}
	for _, competing := range []string{".form-input:focus-visible", ".btn-check:checked + .btn", "linear-gradient(", "!important", "form-card modern-card", `class="form-textarea"`} {
		if strings.Contains(string(page), competing) {
			t.Errorf("Create must not restore competing control/surface styling: %s", competing)
		}
	}
	for _, required := range []string{`id="createAgentForm" class="ui-section" novalidate`, `class="form-card ui-section ui-section--quiet"`, `class="tags-input-container tag-input-chips"`} {
		if !strings.Contains(string(page), required) {
			t.Errorf("Create must retain shared form ownership: %s", required)
		}
	}
	appearance, err := Static.ReadFile("static/css/agent-appearance.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, competing := range []string{".appearance-btn:hover", ".appearance-btn:disabled", "padding: 6px 12px"} {
		if strings.Contains(string(appearance), competing) {
			t.Errorf("Appearance must inherit shared button metrics/states: %s", competing)
		}
	}
}

func TestAgentDetailUsesSharedVisualOwners(t *testing.T) {
	for _, name := range []string{"agents-claude-detail", "agents-codex-detail"} {
		data, err := Templates.ReadFile("templates/pages/" + name + ".tmpl")
		if err != nil {
			t.Fatal(err)
		}
		for _, required := range []string{`class="ui-page-header"`, `class="cli-detail-container"`, `class="modern-badge">CLI · Read-only`, `role="status" aria-live="polite"`} {
			if !strings.Contains(string(data), required) {
				t.Errorf("%s must retain shared/read-only semantics: %s", name, required)
			}
		}
		if strings.Contains(string(data), "<style>") || strings.Contains(string(data), "overflow-x: hidden") {
			t.Errorf("%s must not restore its private palette or hide page overflow", name)
		}
	}
	data, err := Templates.ReadFile("templates/pages/agents-detail.tmpl")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{`--agent-edit-success: var(--success-text)`, `--agent-edit-danger: var(--danger-text)`, `id="deleteButton" class="modern-btn modern-btn-danger"`, `grid-template-columns: repeat(auto-fit, minmax(min(100%, 15rem), 1fr))`} {
		if !strings.Contains(string(data), required) {
			t.Errorf("agent detail must retain shared controls and bounded appearance geometry: %s", required)
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

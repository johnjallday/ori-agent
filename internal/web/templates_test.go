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

func TestRenderProfileIncludesReviewableKnowledgeScopes(t *testing.T) {
	r := NewTemplateRenderer()
	if err := r.LoadTemplates(); err != nil {
		t.Fatalf("LoadTemplates failed: %v", err)
	}
	html, err := r.RenderTemplate("profile", TemplateData{Title: "What Ori knows - Ori Agent"})
	if err != nil {
		t.Fatalf("RenderTemplate(profile) failed: %v", err)
	}
	for _, want := range []string{
		`What Ori knows about you`,
		`id="userKnowledgeOverview"`,
		`id="userKnowledgeWorkspaceList"`,
		`id="userKnowledgeLearningList"`,
		`Excluded until you approve it`,
		`src="/js/modules/user-knowledge.js"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("rendered profile page missing %q", want)
		}
	}
}

func TestRenderWorkspaceAssistantPassesUnquotedIdentityToModule(t *testing.T) {
	r := NewTemplateRenderer()
	if err := r.LoadTemplates(); err != nil {
		t.Fatalf("LoadTemplates failed: %v", err)
	}
	html, err := r.RenderTemplate("workspace-assistant", TemplateData{
		Title: "Assistant - Ori Agent",
		Extra: map[string]any{"WorkspaceID": "ws-1", "WorkspaceSlug": "project-one"},
	})
	if err != nil {
		t.Fatalf("RenderTemplate(workspace-assistant) failed: %v", err)
	}
	for _, want := range []string{`workspaceId: "ws-1"`, `workspaceSlug: "project-one"`} {
		if !strings.Contains(html, want) {
			t.Errorf("rendered assistant page missing %q", want)
		}
	}
	if strings.Contains(html, `workspaceSlug: "\"project-one\""`) {
		t.Fatal("rendered assistant page passed JSON quotes as part of the workspace slug")
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

func TestWorkspaceDetailHasOneRuntimeCapabilityEntryPoint(t *testing.T) {
	r := NewTemplateRenderer()
	if err := r.LoadTemplates(); err != nil {
		t.Fatalf("LoadTemplates failed: %v", err)
	}
	html, err := r.RenderTemplate("workspace-detail", TemplateData{
		Title: "Workspace - Ori Agent",
		Extra: map[string]any{"WorkspaceID": "runtime-1"},
	})
	if err != nil {
		t.Fatalf("RenderTemplate(workspace-detail) failed: %v", err)
	}
	if count := strings.Count(html, `id="setupWizardStatusChip"`); count != 1 {
		t.Fatalf("authoritative runtime capability entry points = %d, want 1", count)
	}
	for _, gone := range []string{
		`id="reaperReadinessChip"`,
		`id="reaperReadinessCard"`,
		`/js/modules/reaper-setup-card.js`,
		`id="reaperSetupCard"`,
	} {
		if strings.Contains(html, gone) {
			t.Errorf("workspace detail contains duplicate legacy runtime surface %q", gone)
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

// createWorkspaceModalMarkup returns just the Create Workspace modal's markup
// from a rendered page, so wizard assertions — especially absence checks — can't
// be satisfied or broken by unrelated modals on the same page. The modal's footer
// create button is its last element, which makes a reliable end boundary.
func createWorkspaceModalMarkup(t *testing.T, page string) string {
	t.Helper()

	start := strings.Index(page, `id="addFolderModal"`)
	if start < 0 {
		t.Fatalf("rendered page does not contain the Create Workspace modal")
	}
	end := strings.Index(page[start:], `id="createFolderBtn"`)
	if end < 0 {
		t.Fatalf("Create Workspace modal is missing its create button; update this helper")
	}
	return page[start : start+end]
}

// TestRenderCreateWorkspaceWizardReviewContract pins the four-step Create mode
// shell: Blueprint → Details → Team → Review, each step numbered "of 4", with
// Team owning the roster surface and Review reading as a confirmation.
func TestRenderCreateWorkspaceWizardReviewContract(t *testing.T) {
	r := NewTemplateRenderer()
	if err := r.LoadTemplates(); err != nil {
		t.Fatalf("LoadTemplates failed: %v", err)
	}

	page, err := r.RenderTemplate("workspaces", TemplateData{Title: "Workspaces - Ori Agent"})
	if err != nil {
		t.Fatalf("RenderTemplate(workspaces) failed: %v", err)
	}
	// Scope every assertion to the Create Workspace modal. The page also renders
	// unrelated modals (onboarding has its own "Step N of 3" label), so a
	// page-wide absence check would couple this test to other components.
	html := createWorkspaceModalMarkup(t, page)

	for _, want := range []string{
		// Four ordered step sections.
		`id="wizardStep1"`,
		`id="wizardStep2"`,
		`id="wizardStep3"`,
		`id="wizardStep4"`,
		`aria-current="step"`,
		// Stepper labels and numbers.
		`data-step="4"`,
		`>Blueprint`,
		`>Details`,
		`>Team`,
		`>Review`,
		`Step 1 of 4`,
		`Step 2 of 4`,
		`Step 3 of 4`,
		`Step 4 of 4`,
		// Step headings: Team assembles, Review confirms.
		`Build your workspace team`,
		`Ready to create?`,
		// Blueprint shows a read-only, plan-derived included-agent summary.
		`id="blueprintAgentSummary"`,
		`id="blueprintAgentSummaryText"`,
		`Included agents`,
		// Blueprint keeps selection, briefing, scaffold, add-ons, and Manage.
		`id="templatePicker"`,
		`id="projectTemplateManageLink"`,
		`id="templateBriefing"`,
		`id="templateBriefingScaffoldRow"`,
		`id="templateBriefingAddonsRow"`,
		// Details keeps the project-open preference. Runtime setup is disclosed
		// read-only on Review and never blocks creation.
		`id="projectTemplateOpenAfterCreate"`,
		// Team owns ONE roster plus the inline saved-agent picker, and the
		// Advanced include-team disclosure now lives here rather than on Details.
		`id="workspaceTeamLayout"`,
		`id="workspaceTeamReview"`,
		`id="workspaceTeamRoster"`,
		`id="workspaceTeamIssues"`,
		`id="workspaceTeamAdvanced"`,
		`id="templateAgentReviewToggle"`,
		`id="existingAgentRosterPanel"`,
		`id="existingAgentRosterSearch"`,
		`id="workspaceTeamLiveRegion"`,
		`Resulting workspace team`,
		`Advanced team options`,
		// Review keeps the read-only post-create setup preview.
		`id="workspaceSetupPreview"`,
		`aria-label="Close create workspace"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("rendered Create Workspace wizard missing %q", want)
		}
	}
	for _, gone := range []string{
		"Select Blueprint",
		"Construct",
		"Template Agents",
		// Three-step remnants.
		"Step 1 of 3",
		"Step 2 of 3",
		"Step 3 of 3",
		"Review &amp; Create",
		"Choose Blueprint",
		"Workspace Details",
		// The Review mount that used to receive relocated Details controls: the
		// mutable readiness cards now stay on Details (FR29-FR31).
		`id="wizardStep3ReviewMount"`,
		`id="wizardStep4ReviewMount"`,
		// The duplicate REAPER-specific pre-create card was replaced by the
		// normalized blueprint setup preview.
		`id="reaperSetupCard"`,
		// The picker is permanent inline markup on Team, so it has no close
		// button and is never opened from a Review-step button.
		`id="closeExistingAgentRosterBtn"`,
		// Blueprint carries NO interactive agent surface (FR20): no map preview,
		// no create-all shortcut, and no reusable-agent setup form.
		`id="workspaceAgentMapPreview"`,
		`id="workspaceAgentMapNode"`,
		`id="workspaceAgentMapStatus"`,
		`id="workspaceAgentMapAvatarAction"`,
		`id="workspaceAgentMapCreateAll"`,
		`id="workspaceAgentMapSpecialists"`,
		`id="workspaceTemplateAgentSetup"`,
		`id="workspaceTemplateAgentSetupForm"`,
		`id="workspaceTemplateAgentSetupSave"`,
		`Create all defaults`,
		`Save reusable agent`,
		// The roster is one list: no nested "Blueprint agents" card, no separate
		// saved-agent list, and no drop zone (FR33, FR63).
		`id="templateAgentReview"`,
		`id="templateAgentReviewMount"`,
		`id="templateAgentReviewList"`,
		`id="existingAgentTeamList"`,
		`id="workspaceTeamDropZone"`,
		`Blueprint agents`,
		`Make a workspace copy`,
		`Drop an existing agent here`,
	} {
		if strings.Contains(html, gone) {
			t.Errorf("rendered Create Workspace wizard contains stale markup/copy %q", gone)
		}
	}
}

// TestCreateWorkspaceTeamDraftLoadsBeforeSessions guards the script-order
// dependency for the Create Workspace wizard: sessions.js reads
// window.CreateWorkspaceTeamDraft while binding the modal, so the helper must be
// deferred ahead of it on every page that renders the modal. Both tags are
// `defer`, which executes in document order, making tag position the contract —
// and a silent one, because a helper that loads too late leaves the wizard
// bound to an undefined draft while every API call still succeeds.
func TestCreateWorkspaceTeamDraftLoadsBeforeSessions(t *testing.T) {
	r := NewTemplateRenderer()
	if err := r.LoadTemplates(); err != nil {
		t.Fatalf("LoadTemplates failed: %v", err)
	}

	// Every page whose layout includes session-modals.tmpl, which renders
	// components/workspaces/create-workspace-modal.tmpl. "index" covers
	// layout/base.tmpl, which owns its own script block.
	pages := []string{"index", "workspaces", "workspace-detail", "workspace-canvas", "workspace-task"}
	const (
		helper   = `/js/modules/create-workspace-team-draft.js`
		sessions = `/js/modules/sessions.js`
	)

	for _, page := range pages {
		data := TemplateData{
			Title: page + " - Ori Agent",
			Extra: map[string]any{"WorkspaceID": "ws-1", "TaskID": "task-1"},
		}
		html, err := r.RenderTemplate(page, data)
		if err != nil {
			t.Fatalf("RenderTemplate(%s) failed: %v", page, err)
		}

		if !strings.Contains(html, `id="addFolderModal"`) {
			// If a page stops rendering the modal this assertion is moot; fail
			// loudly rather than silently passing a vacuous check.
			t.Fatalf("page %s no longer renders the Create Workspace modal; update this test", page)
		}

		helperAt := strings.Index(html, helper)
		sessionsAt := strings.Index(html, sessions)
		if helperAt < 0 {
			t.Errorf("page %s does not load %s", page, helper)
			continue
		}
		if sessionsAt < 0 {
			t.Errorf("page %s does not load %s", page, sessions)
			continue
		}
		if helperAt > sessionsAt {
			t.Errorf("page %s loads %s after %s; the team-draft helper must come first",
				page, helper, sessions)
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

// TestRenderHomeCockpitShell confirms the Home page renders the Map-first
// cockpit shell: one full-width workspace area holding Map and Tree as peer
// views, one stable Bootstrap context modal outside the cockpit grid, and the
// header-anchored Updates/Quests flyouts. Issue #366 retires the docked context
// rail while preserving its context body hooks inside the modal.
// PRD FR14-FR21, FR74; Issues #334 and #366.
func TestRenderHomeCockpitShell(t *testing.T) {
	r := NewTemplateRenderer()
	if err := r.LoadTemplates(); err != nil {
		t.Fatalf("LoadTemplates failed: %v", err)
	}

	data := TemplateData{
		Title: "Ori Agent",
		Extra: map[string]any{
			"HomeCommandBridge": true,
			"WorkspaceCount":    3,
			"IsFirstRun":        false,
		},
	}
	html, err := r.RenderTemplate("index", data)
	if err != nil {
		t.Fatalf("RenderTemplate(index) failed: %v", err)
	}

	for _, want := range []string{
		// Cockpit shell.
		`id="homeCockpit"`,
		`id="cockpitWorkspaceArea"`,
		`data-cockpit-area-title`,
		`id="cockpitMap"`,
		`id="cockpitTree"`,
		`id="cockpitWorkspaceStatus"`,
		`id="cockpitContextModal"`,
		`class="modal-dialog modal-dialog-centered modal-dialog-scrollable"`,
		`id="cockpitContextModalLabel"`,
		`id="cockpitRailContext"`,
		`id="cockpitRailLive"`,
		// Mutually exclusive Map/Tree control (FR17, FR24).
		`data-cockpit-view="map"`,
		`data-cockpit-view="tree"`,
		// Map-only signal filters and the rail footer (FR31, FR88, FR101).
		`id="cockpitSignalFilters"`,
		`id="cockpitSummaryBtn"`,
		`id="cockpitCaptureBtn"`,
		`id="cockpitCapturePanel"`,
		// Updates: header-anchored flyout, never a rail column (Issue #334 FR1-FR25).
		`id="cockpitRailToggle"`,
		`aria-controls="cockpitUpdatesFlyout"`,
		`cockpit-flyout-toggle__label">Updates<`,
		`id="cockpitUpdatesFlyout"`,
		`id="cockpitUpdatesFlyoutBody"`,
		// Quests: Progression's always-available compact entry point, adjacent
		// to Updates (Issue #334 FR26-FR40).
		`id="cockpitQuestsToggle"`,
		`aria-controls="cockpitQuestsFlyout"`,
		`cockpit-flyout-toggle__label">Quests<`,
		`data-role="quests-summary"`,
		`id="cockpitQuestsFlyout"`,
		// Ask Ori's work activity reaches Home through the one universal panel
		// rather than a Home-only rail state (Issue #350 FR39/FR68). The element
		// ids and the panel surface contract are unchanged, so all existing
		// routing, planning, and confirmation behaviour still applies.
		`data-home-assistant-surface="panel"`,
		`id="homeAssistantThinkingModal"`,
		`id="homeAssistantRoutingSummary"`,
		`id="homeAssistantConversation"`,
		`id="homeAssistantCard"`,
		// The one composer, from the universal panel.
		`id="oriGuideInput"`,
		// Creation reuses the existing modal contract (FR105).
		`id="cockpitCreateWorkspaceBtn"`,
		`data-bs-target="#addFolderModal"`,
		// Today's sources survive both migrations — into the rail (FR77, FR81,
		// FR82, FR84, FR86) and then out of it into the Updates flyout
		// (Issue #334) — with the same element ids throughout.
		`id="homeDailyBrief"`,
		`id="homeCalendarOpsPortal"`,
		`id="homeRecentActivity"`,
		`id="questLog"`,
		// Optional Personal HQ mounts survive the migration (FR115).
		`id="hqUpgradeMount"`,
		`id="hqEmailMount"`,
		`id="hqMailReviewMount"`,
		`id="hqFollowUpMount"`,
		`id="hqJournalMount"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("rendered Home page missing %q", want)
		}
	}

	// The retired Operations Board and its duplicate workspace overview must
	// not render below or beside the cockpit (FR22).
	//
	// Also listed: two controls retired for duplicating a surface the page
	// already had. cockpitRailViewBtn was a second Map/Tree toggle beside the
	// primary one in the workspace-area header; homeUpcomingTasks was a second
	// rendering of the scheduled-run data "Scheduled today" already shows, from
	// a second fetch of the same endpoint.
	for _, gone := range []string{
		`id="homeDashboardSections"`,
		`id="homeRecentWorkspaces"`,
		`home-operations-board`,
		`class="home-command-layout"`,
		`aria-label="Operations board"`,
		`id="cockpitRailViewBtn"`,
		// Home's own command strip, retired by Issue #350: a second composer
		// beside the global Ask Ori launcher. Its input, send button, kicker,
		// and suggested chips all moved into the one universal panel.
		`id="homeAssistantInput"`,
		`id="homeAssistantForm"`,
		`id="homeAssistantSendBtn"`,
		`home-command-strip`,
		`home-command-kicker`,
		`id="homeUpcomingTasks"`,
		// Issue #334: the rail's own "Today" panel is retired. Issue #366 also
		// retires the docked shell and its layout-coupled open attribute.
		`id="cockpitRailToday"`,
		`id="cockpitRail"`,
		`data-rail-open=`,
		`cockpit-flyout-toggle__label">Today<`,
	} {
		if strings.Contains(html, gone) {
			t.Errorf("rendered Home page still contains retired element %q", gone)
		}
	}

	for _, id := range []string{"cockpitContextModal", "cockpitContextModalLabel", "cockpitRailContext", "cockpitRailLive"} {
		if count := strings.Count(html, `id="`+id+`"`); count != 1 {
			t.Errorf("Home page renders %d copies of stable context id %q; want 1", count, id)
		}
	}
	modalAt := strings.Index(html, `id="cockpitContextModal"`)
	mapAt := strings.Index(html, `id="cockpitMap"`)
	if modalAt < mapAt {
		t.Errorf("context modal must be mounted after the cockpit Map (modalAt=%d mapAt=%d)", modalAt, mapAt)
	}

	// FR96: Ask Ori activity must NOT be a blocking modal over the cockpit.
	// The panel keeps the historical element id (dashboard.js addresses it by
	// that id), so the assertion is about the modal chrome, not the id.
	askAt := strings.Index(html, `id="homeAssistantThinkingModal"`)
	if askAt < 0 {
		t.Fatalf("Home page no longer renders the Ask Ori activity surface")
	}
	// Look at the element's own tag, not the whole page.
	tagStart := strings.LastIndex(html[:askAt], "<")
	tagEnd := strings.Index(html[askAt:], ">") + askAt
	askTag := html[tagStart:tagEnd]
	for _, forbidden := range []string{`class="modal`, `modal fade`, `data-bs-backdrop`} {
		if strings.Contains(askTag, forbidden) {
			t.Errorf("Ask Ori activity is still a blocking modal: tag contains %q\ntag: %s", forbidden, askTag)
		}
	}
	if !strings.Contains(askTag, `data-home-assistant-surface="panel"`) {
		t.Errorf("Ask Ori activity must declare the embedded panel surface; tag: %s", askTag)
	}
	if strings.Contains(html, `home-thinking-modal-content`) {
		t.Error("Home page still renders the thinking-modal dialog chrome")
	}
}

// TestHomeCockpitLoadsMapBeforeCoordinator pins the standing script-order
// requirement: workspace-map.js defines window.OriWorkspaceMap, and the cockpit
// coordinator calls OriWorkspaceMap.mount(...), so the map must load first
// (PRD FR123). Classic deferred scripts run before non-async module scripts,
// so source order here is the whole contract.
func TestHomeCockpitLoadsMapBeforeCoordinator(t *testing.T) {
	r := NewTemplateRenderer()
	if err := r.LoadTemplates(); err != nil {
		t.Fatalf("LoadTemplates failed: %v", err)
	}

	data := TemplateData{
		Title: "Ori Agent",
		Extra: map[string]any{"HomeCommandBridge": true, "WorkspaceCount": 0, "IsFirstRun": true},
	}
	html, err := r.RenderTemplate("index", data)
	if err != nil {
		t.Fatalf("RenderTemplate(index) failed: %v", err)
	}

	const (
		mapJS     = `/js/modules/workspace-map.js`
		cockpitJS = `/js/modules/home-workspace-cockpit.js`
	)
	mapAt := strings.Index(html, mapJS)
	cockpitAt := strings.Index(html, cockpitJS)
	if mapAt < 0 {
		t.Fatalf("Home page does not load %s", mapJS)
	}
	if cockpitAt < 0 {
		t.Fatalf("Home page does not load %s", cockpitJS)
	}
	if mapAt > cockpitAt {
		t.Errorf("Home loads %s after %s; the map must be defined first", mapJS, cockpitJS)
	}

	// The map script must be a classic deferred script, not a module: module
	// scripts execute after ALL deferred classics, which would invert the
	// ordering this test just proved.
	if !strings.Contains(html, `<script defer src="`+mapJS+`">`) {
		t.Errorf("%s must load as a classic deferred script to keep the ordering guarantee", mapJS)
	}
}

// TestHomeCockpitStylesheetLoadsOnHomeOnly confirms the cockpit stylesheet is
// scoped to Home rather than shipped to every page.
func TestHomeCockpitStylesheetLoadsOnHomeOnly(t *testing.T) {
	r := NewTemplateRenderer()
	if err := r.LoadTemplates(); err != nil {
		t.Fatalf("LoadTemplates failed: %v", err)
	}

	const sheet = `/css/home-workspace-cockpit.css`

	home, err := r.RenderTemplate("index", TemplateData{
		Title: "Ori Agent",
		Extra: map[string]any{"HomeCommandBridge": true, "WorkspaceCount": 1},
	})
	if err != nil {
		t.Fatalf("RenderTemplate(index) failed: %v", err)
	}
	if !strings.Contains(home, sheet) {
		t.Errorf("Home page does not load %s", sheet)
	}

	other, err := r.RenderTemplate("agents-roster", TemplateData{Title: "Agents - Ori Agent"})
	if err != nil {
		t.Fatalf("RenderTemplate(agents-roster) failed: %v", err)
	}
	if strings.Contains(other, sheet) {
		t.Errorf("non-Home page should not load %s", sheet)
	}
}

// TestWorkspaceDetailFileJanitorContract pins the split between the compact
// card and the console (FR-100, FR-114, FR-115).
//
// Workspace Details carries ONE mount, which JavaScript fills with a summary.
// The review table, the settings form, and history belong to the console and
// exist nowhere else on the page. A second copy in the template would be a
// surface acting on the user's files while sitting stale and unseen behind the
// modal — which is precisely what a template can reintroduce silently, because
// no JavaScript test would notice markup it never rendered.
func TestWorkspaceDetailFileJanitorContract(t *testing.T) {
	r := NewTemplateRenderer()
	if err := r.LoadTemplates(); err != nil {
		t.Fatalf("LoadTemplates failed: %v", err)
	}

	html, err := r.RenderTemplate("workspace-detail", TemplateData{
		Title: "Workspace - Ori Agent",
		Extra: map[string]any{"WorkspaceID": "ws-1"},
	})
	if err != nil {
		t.Fatalf("RenderTemplate(workspace-detail) failed: %v", err)
	}

	if got := strings.Count(html, `id="downloadsJanitorMount"`); got != 1 {
		t.Errorf("File Janitor mount count = %d, want exactly 1", got)
	}

	// The console builds itself on <body> at open time, so the template must
	// not pre-place a host that could be styled, focused, or found while empty.
	if strings.Contains(html, `id="fileJanitorConsole"`) {
		t.Error("the console host must be created by the controller, not the template")
	}

	// Nothing in the template may pre-render the surfaces the console owns.
	for _, forbidden := range []string{
		`id="downloadsJanitorBatch"`,
		`id="downloadsJanitorSettingsHost"`,
		`id="downloadsJanitorHistoryHost"`,
		`id="downloadsJanitorConfirmHost"`,
	} {
		if strings.Contains(html, forbidden) {
			t.Errorf("workspace-detail must not contain %s; it belongs to the console", forbidden)
		}
	}

	// The controller and the one overlay coordinator are both loaded. Without
	// the coordinator the console would open with no single-modal rule and no
	// inert background.
	for _, want := range []string{
		`/js/modules/file-janitor-console.js`,
		`/js/modules/workspace-overlay-coordinator.js`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("workspace-detail must load %s", want)
		}
	}

	// The retired module name must not linger in a second script tag.
	if strings.Contains(html, "downloads-janitor.js") {
		t.Error("downloads-janitor.js was renamed; a stale script tag would 404")
	}
}

// TestRenderHomeCarriesPersonalHQBuildModal guards issue #322.
//
// personal-hq-onboarding.js binds the Build My HQ flow by element id, and its
// modal lookup returns null when the markup is absent — which produced a button
// that did nothing at all: no console error, no request. The markup originally
// lived in workspace-hub.tmpl, reachable only from pages/workspaces.tmpl, and
// went dark the moment `/workspaces` started redirecting to Home.
//
// Home loads the script, so Home must carry the modal. Every id asserted here
// is one the script actually reads; dropping any of them silently breaks a
// field rather than the whole dialog.
func TestRenderHomeCarriesPersonalHQBuildModal(t *testing.T) {
	r := NewTemplateRenderer()
	if err := r.LoadTemplates(); err != nil {
		t.Fatalf("LoadTemplates failed: %v", err)
	}

	html, err := r.RenderTemplate("index", GetDefaultData())
	if err != nil {
		t.Fatalf("RenderTemplate(index) failed: %v", err)
	}

	for _, want := range []string{
		`id="hqBuildModal"`,
		`id="hqBuildName"`,
		`id="hqBuildTimezone"`,
		`id="hqBuildWorkspaceRoot"`,
		`id="hqBuildWorkspaceRootBrowseBtn"`,
		`id="hqBuildWorkspaceRootStatus"`,
		`id="hqBuildAdvancedToggle"`,
		`id="hqBuildAdvanced"`,
		`id="hqBuildTime"`,
		`id="hqBuildScope"`,
		`id="hqBuildIncludeFuture"`,
		`id="hqBuildNotify"`,
		`id="hqBuildError"`,
		`id="hqBuildSubmitBtn"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("Home is missing %s — the Build My HQ flow binds to it (#322)", want)
		}
	}

	// The script that binds all of the above must actually load here, otherwise
	// the modal is inert markup and this test proves nothing.
	if !strings.Contains(html, "/js/modules/personal-hq-onboarding.js") {
		t.Error("Home must load personal-hq-onboarding.js to wire the modal above")
	}

	// Exactly one definition: a second copy would give getElementById an
	// ambiguous target and let the two dialogs drift apart.
	if n := strings.Count(html, `id="hqBuildModal"`); n != 1 {
		t.Errorf(`Home renders id="hqBuildModal" %d times, want exactly 1`, n)
	}
}

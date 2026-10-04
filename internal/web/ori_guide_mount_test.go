package web

import (
	"io/fs"
	"strings"
	"testing"
)

// The guide mounts through navbar.tmpl rather than base.tmpl.
//
// That distinction is the whole reason this file exists: standalone pages such
// as /agents build their own shell and never render base.tmpl, so a mount there
// silently produced a Home-only guide while looking global. These tests fail if
// anyone moves it back (PRD FR-20).

func readTemplate(t *testing.T, path string) string {
	t.Helper()
	raw, err := fs.ReadFile(Templates, path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(raw)
}

func pageTemplates(t *testing.T) []string {
	t.Helper()
	entries, err := fs.ReadDir(Templates, "templates/pages")
	if err != nil {
		t.Fatalf("read pages dir: %v", err)
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".tmpl") {
			out = append(out, "templates/pages/"+e.Name())
		}
	}
	if len(out) == 0 {
		t.Fatal("no page templates found")
	}
	return out
}

// Every authenticated page reaches the guide, either by rendering the navbar
// directly or by going through base.tmpl (which renders the navbar itself).
func TestEveryPageTemplateReachesTheGuide(t *testing.T) {
	navbar := readTemplate(t, "templates/components/navbar.tmpl")
	if !strings.Contains(navbar, `{{template "ori-guide.tmpl"`) {
		t.Fatal("navbar.tmpl no longer mounts the guide; standalone pages would lose it")
	}

	base := readTemplate(t, "templates/layout/base.tmpl")
	if !strings.Contains(base, `{{template "navbar.tmpl"`) {
		t.Fatal("base.tmpl no longer renders the navbar")
	}

	for _, path := range pageTemplates(t) {
		body := readTemplate(t, path)
		viaNavbar := strings.Contains(body, `{{template "navbar.tmpl"`)
		viaBase := strings.Contains(body, `{{template "base.tmpl"`)
		if !viaNavbar && !viaBase {
			t.Errorf("%s renders neither the navbar nor base, so it has no Ori Guide", path)
		}
	}
}

// Exactly one instance. Two launchers would mean two panels, two focus traps,
// and two competing open states (FR-20/FR-115).
func TestGuideIsMountedExactlyOnce(t *testing.T) {
	navbar := readTemplate(t, "templates/components/navbar.tmpl")
	if got := strings.Count(navbar, `{{template "ori-guide.tmpl"`); got != 1 {
		t.Errorf("navbar mounts the guide %d times, want 1", got)
	}

	// base.tmpl already gets the guide through the navbar; mounting it again
	// there would double every page built on base.
	base := readTemplate(t, "templates/layout/base.tmpl")
	if strings.Contains(base, `{{template "ori-guide.tmpl"`) {
		t.Error("base.tmpl mounts the guide as well as the navbar — that is two instances per page")
	}

	for _, path := range pageTemplates(t) {
		if strings.Contains(readTemplate(t, path), `{{template "ori-guide.tmpl"`) {
			t.Errorf("%s mounts the guide directly; it already gets one from the navbar", path)
		}
	}
}

// The markup carries exactly one of each id the controller binds to.
func TestGuideMarkupHasOneOfEachControl(t *testing.T) {
	body := readTemplate(t, "templates/components/ori-guide.tmpl")
	for _, id := range []string{
		`id="oriGuideLauncher"`, `id="oriGuidePanel"`, `id="oriGuideInput"`,
		`id="oriGuideSend"`, `id="oriGuideForm"`, `id="oriGuideReply"`, `id="oriGuideClose"`,
		`id="oriGuideContext"`, `id="oriGuideActivity"`,
	} {
		if got := strings.Count(body, id); got != 1 {
			t.Errorf("%s appears %d times, want 1", id, got)
		}
	}
}

// PAF adds a hidden-by-default work panel; its controller loads the canonical
// relationship and then makes Help and work mutually exclusive.
func TestPAFPanelHasDistinctHiddenComposer(t *testing.T) {
	body := readTemplate(t, "templates/components/ori-guide.tmpl")
	// Exactly two composers take a prompt: Help and the PAF work composer. The
	// only other forms are reviews of an action the user already chose (saving
	// a reply), and each starts hidden so it can never be a second place to
	// type a request.
	reviews := strings.Count(body, `class="personal-assistant-review"`)
	if got := strings.Count(body, "<form"); got != 2+reviews {
		t.Errorf("template has %d forms, want one Help composer, one PAF work composer, and %d review form(s)", got, reviews)
	}
	if hidden := strings.Count(body, "hidden novalidate>"); hidden != reviews {
		t.Errorf("%d of %d review form(s) start hidden; every review must", hidden, reviews)
	}
	for _, marker := range []string{
		`id="oriGuideForm"`, `id="personalAssistantForm"`,
		`id="personalAssistantLauncher"`, `id="personalAssistantPanel"`,
	} {
		if got := strings.Count(body, marker); got != 1 {
			t.Errorf("%s appears %d times, want 1", marker, got)
		}
	}
	if !strings.Contains(body, `id="personalAssistantLauncher"`) ||
		!strings.Contains(body, `aria-controls="personalAssistantPanel" hidden`) {
		t.Error("PAF work launcher must be hidden until the authoritative status read")
	}
}

// assistantDrawerMarkup returns the Personal Assistant drawer's own markup from
// a rendered page: from its opening tag to the tag that closes it. The drawer
// nests other sections, so its end is the first one closed after the composer's
// Send button, which is the last control inside it.
func assistantDrawerMarkup(t *testing.T, html string) string {
	t.Helper()
	start := strings.Index(html, `<section id="personalAssistantPanel"`)
	send := strings.Index(html, `id="personalAssistantSend"`)
	if start < 0 || send < start {
		t.Fatal("rendered page has no Personal Assistant drawer with a composer")
	}
	end := strings.Index(html[send:], `</section>`)
	if end < 0 {
		t.Fatal("the Personal Assistant drawer is never closed")
	}
	return html[start : send+end+len(`</section>`)]
}

// On Home the drawer is ONE view: the header, then what needs you, the brief
// row and the progress row, then the conversation, and the composer last. There
// are no tabs, and the Daily Brief itself is not in it.
func TestPAFPanelRendersHomeAsOneViewWithTheComposerLast(t *testing.T) {
	r := NewTemplateRenderer()
	if err := r.LoadTemplates(); err != nil {
		t.Fatalf("LoadTemplates failed: %v", err)
	}
	html, err := r.RenderTemplate("index", TemplateData{
		Title:       "Ori Agent",
		CurrentPage: "index",
		Extra:       map[string]any{"HomeCommandBridge": true},
	})
	if err != nil {
		t.Fatalf("RenderTemplate(index) failed: %v", err)
	}

	// In reading order. Each controller id has exactly one mount.
	order := []string{
		`id="personalAssistantPanel"`,
		`id="personalAssistantPanelTitle"`, `id="personalAssistantCheckIn"`,
		`id="personalAssistantMore"`, `id="personalAssistantClose"`,
		`id="personalAssistantScroll"`, `id="personalAssistantToday"`,
		`id="personalAssistantTodayBanner"`, `id="personalAssistantSummary"`,
		`id="personalAssistantNeedsYou"`,
		`id="personalAssistantBriefRow"`, `id="personalAssistantProgressRow"`,
		// The conversation: the shared activity, then the folder flow as a
		// turn of its own (the request, the chooser, the scene, the offer).
		`id="personalAssistantThread"`, `id="personalAssistantActivityMount"`,
		`id="personalAssistantFolder"`, `id="personalAssistantFolderRequest"`,
		`id="personalAssistantFolderChooser"`, `id="personalAssistantFolderScene"`,
		`id="personalAssistantFolderOffer"`,
		// The one suggestion, directly above the composer.
		`id="personalAssistantPanelStatus"`, `id="personalAssistantChips"`,
		`id="personalAssistantFolderChip"`, `id="personalAssistantForm"`,
		`id="personalAssistantInput"`, `id="personalAssistantSend"`,
	}
	last := -1
	for _, id := range order {
		if got := strings.Count(html, id); got != 1 {
			t.Errorf("rendered Home %s count = %d, want 1", id, got)
			continue
		}
		at := strings.Index(html, id)
		if at < last {
			t.Errorf("rendered Home %s is out of order: the drawer reads header, needs you, brief row, progress row, conversation, chip, composer", id)
		}
		last = at
	}
	// Home lists what needs the user; the one-line stand-in is for other pages.
	if strings.Contains(html, `id="personalAssistantNeedsLine"`) {
		t.Error("rendered Home has the other pages' needs-you line as well as Needs you")
	}
	// The folder flow is in the conversation, not in Today's Needs you.
	today := html[strings.Index(html, `id="personalAssistantToday"`):strings.Index(html, `id="personalAssistantThread"`)]
	if strings.Contains(today, `personalAssistantFolder`) {
		t.Error("the folder flow is still rendered inside Today; it belongs in the conversation")
	}
	if got := strings.Count(html, `id="personalAssistantLauncher"`); got != 1 {
		t.Errorf("rendered Home launcher count = %d, want 1", got)
	}

	drawer := assistantDrawerMarkup(t, html)
	for _, gone := range []string{`role="tablist"`, `role="tab"`, `role="tabpanel"`} {
		if strings.Contains(drawer, gone) {
			t.Errorf("the drawer still has a tab affordance: %s", gone)
		}
	}
	// Nothing after the composer but the end of the drawer.
	if tail := strings.TrimSpace(drawer[strings.LastIndex(drawer, `</form>`)+len(`</form>`):]); tail != `</section>` {
		t.Errorf("the composer is not the last thing in the drawer; after it: %.80q", tail)
	}

	// The full Daily Brief is displayed in My HQ, never here.
	if strings.Contains(html, `id="homeDailyBrief"`) {
		t.Error("rendered Home still mounts the full Daily Brief")
	}

	dashboard := readTemplate(t, "templates/components/dashboard.tmpl")
	if strings.Contains(dashboard, `id="personalAssistantToday"`) {
		t.Error("dashboard still owns drawer markup id=\"personalAssistantToday\"")
	}
}

func TestHomeCockpitHasNoTodayOrWorkingAgreementGridSibling(t *testing.T) {
	body := readTemplate(t, "templates/components/dashboard.tmpl")
	if strings.Contains(body, `id="personalAssistantToday"`) {
		t.Fatal("Today must be owned by the shared Personal Assistant drawer, not the Home grid")
	}
	cockpit := strings.Index(body, `id="homeCockpit"`)
	agreement := strings.Index(body, `id="personalAssistantContinuity"`)
	if cockpit < 0 || agreement < 0 {
		t.Fatalf("could not locate cockpit=%d agreement=%d", cockpit, agreement)
	}
	bridgeEndOffset := strings.Index(body[cockpit:], "\n</div>\n\n<!-- Working Agreement")
	if bridgeEndOffset < 0 {
		t.Fatal("could not locate the Home command bridge boundary")
	}
	if agreement < cockpit+bridgeEndOffset {
		t.Fatal("Working Agreement still occupies the Home command grid ahead of the cockpit")
	}
}

// On every other page the drawer is the same one view without Home's Today:
// the header (with its check-in line and More menu), the conversation, the
// chip, and the composer. It has no Needs you cards, brief row or progress
// row, and no folder flow of its own: the chip goes to Home's.
func TestPAFPanelOnOtherPagesIsTheConversationAndComposer(t *testing.T) {
	r := NewTemplateRenderer()
	if err := r.LoadTemplates(); err != nil {
		t.Fatalf("LoadTemplates failed: %v", err)
	}
	html, err := r.RenderTemplate("settings", TemplateData{
		Title:       "Settings - Ori Agent",
		CurrentPage: "settings",
		Extra:       map[string]any{},
	})
	if err != nil {
		t.Fatalf("RenderTemplate(settings) failed: %v", err)
	}
	for _, absent := range []string{
		`id="personalAssistantToday"`, `id="personalAssistantNeedsYou"`,
		`id="personalAssistantBriefRow"`, `id="personalAssistantProgressRow"`,
		`id="personalAssistantSummary"`, `id="personalAssistantFolder"`,
		`id="personalAssistantFolderOffer"`,
		`id="homeDailyBrief"`,
	} {
		if strings.Contains(html, absent) {
			t.Errorf("non-Home assistant panel unexpectedly contains %s", absent)
		}
	}
	for _, present := range []string{
		`id="personalAssistantPanel"`, `id="personalAssistantCheckIn"`,
		`id="personalAssistantMore"`, `id="personalAssistantScroll"`,
		// How much needs the user, in one line that goes to Home.
		`id="personalAssistantNeedsLine"`,
		`id="personalAssistantThread"`, `id="personalAssistantActivityMount"`,
		`id="personalAssistantFolderChip"`, `id="personalAssistantForm"`,
	} {
		if got := strings.Count(html, present); got != 1 {
			t.Errorf("non-Home %s count = %d, want 1", present, got)
		}
	}
	if strings.Contains(assistantDrawerMarkup(t, html), `role="tab`) {
		t.Error("the non-Home drawer has a tab affordance")
	}
}

func TestPAFPanelPresentsGuideAndAssistantRolesBeforeInput(t *testing.T) {
	body := stripHTMLComments(readTemplate(t, "templates/components/ori-guide.tmpl"))
	for _, want := range []string{
		`id="oriGuideTitle">Ask Ori<`, `id="oriGuideRole" hidden>App Guide<`,
		// The line under the assistant's name says "Personal Assistant" until
		// the next check-in is known, and the launcher always carries the role.
		`class="personal-assistant-panel__checkin">Personal Assistant<`,
		`class="personal-assistant-launcher__role">Personal Assistant<`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("identity boundary missing %q", want)
		}
	}
	for _, retired := range []string{"Workspace Manager", "Workspace Assistant", "Workspaces Assistant", "Task Assistant"} {
		if strings.Contains(body, retired) {
			t.Errorf("rendered panel copy still contains the retired label %q", retired)
		}
	}
}

func TestPAFComposersStateTheirSeparatePurposes(t *testing.T) {
	body := readTemplate(t, "templates/components/ori-guide.tmpl")
	if !strings.Contains(body, "Ask Ori about the app") ||
		!strings.Contains(body, "Ask a question or describe what you want help with") {
		t.Error("Help and hired-assistant composers must state separate purposes")
	}
}

func stripHTMLComments(body string) string {
	var out strings.Builder
	rest := body
	for {
		start := strings.Index(rest, "<!--")
		if start < 0 {
			out.WriteString(rest)
			return out.String()
		}
		out.WriteString(rest[:start])
		end := strings.Index(rest[start:], "-->")
		if end < 0 {
			return out.String()
		}
		rest = rest[start+end+3:]
	}
}

func TestGuideMarkupCarriesDialogSemantics(t *testing.T) {
	body := readTemplate(t, "templates/components/ori-guide.tmpl")
	for _, attr := range []string{
		`role="dialog"`, `aria-labelledby="oriGuideTitle"`,
		`aria-expanded="false"`, `aria-controls="oriGuidePanel"`,
		`aria-live="polite"`,
	} {
		if !strings.Contains(body, attr) {
			t.Errorf("guide markup is missing %s", attr)
		}
	}
}

// Ori's art is decorative here — the panel states its name and role as text —
// so it must not add noise for a screen reader (FR-117).
func TestGuideArtIsDecorative(t *testing.T) {
	body := readTemplate(t, "templates/components/ori-guide.tmpl")
	if strings.Count(body, `alt=""`) < 2 {
		t.Error("the launcher sprite and panel portrait should both be decorative")
	}
}

// The scripts load from the shared head so every page that renders the navbar
// also gets the controller, and coachmarks load first because the controller
// consults that registry while validating actions.
func TestGuideScriptsLoadFromTheSharedHeadInOrder(t *testing.T) {
	head := readTemplate(t, "templates/layout/head.tmpl")
	coach := strings.Index(head, "ori-guide-coachmarks.js")
	controller := strings.Index(head, "/js/modules/ori-guide.js")

	if coach < 0 || controller < 0 {
		t.Fatal("guide scripts are not loaded from the shared head")
	}
	if coach > controller {
		t.Error("the coachmark allowlist must load before the controller that consults it")
	}
	if !strings.Contains(head, "/css/ori-guide.css") {
		t.Error("guide styles are not loaded from the shared head")
	}
}

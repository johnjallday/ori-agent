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

// One composer, not one per intent. A user must never have to choose a
// guide-versus-work surface, or a Task/Ask/Note mode, before typing (FR5/FR23).
func TestUniversalPanelHasExactlyOneComposer(t *testing.T) {
	body := readTemplate(t, "templates/components/ori-guide.tmpl")
	if got := strings.Count(body, "<form"); got != 1 {
		t.Errorf("panel has %d forms, want exactly 1 composer", got)
	}
	if got := strings.Count(body, `type="text"`) + strings.Count(body, "<textarea"); got != 1 {
		t.Errorf("panel has %d text inputs, want exactly 1", got)
	}
}

// Issue #350: Ask Ori is the identity. The panel must not reintroduce the old
// "this is only a guide, the real work happens elsewhere" boundary, and must not
// name any of the retired surfaces (FR3/FR61).
func TestUniversalPanelPresentsOneIdentityWithContext(t *testing.T) {
	body := readTemplate(t, "templates/components/ori-guide.tmpl")

	if !strings.Contains(body, `id="oriGuideTitle">Ask Ori<`) {
		t.Error("the panel heading must be Ask Ori")
	}

	// Comments explaining the migration may name the retired surfaces; rendered
	// copy may not. Strip the comments before checking.
	rendered := stripHTMLComments(body)
	for _, retired := range []string{
		"Workspace Manager", "Workspace Assistant", "Workspaces Assistant",
		"Task Assistant", "App Guide", "not a work agent",
	} {
		if strings.Contains(rendered, retired) {
			t.Errorf("rendered panel copy still contains the retired label %q", retired)
		}
	}
}

// The composer has to invite both halves of what Ask Ori now does, or users will
// keep assuming it is navigation-only (FR65).
func TestComposerInvitesQuestionsAndWork(t *testing.T) {
	body := readTemplate(t, "templates/components/ori-guide.tmpl")
	if !strings.Contains(body, "Ask a question or describe what you want done") {
		t.Error("composer placeholder should invite both questions and work")
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

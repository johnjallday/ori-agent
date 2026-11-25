package web

import (
	"strings"
	"testing"
)

func renderHome(t *testing.T, extra map[string]any) string {
	t.Helper()
	r := NewTemplateRenderer()
	if err := r.LoadTemplates(); err != nil {
		t.Fatalf("LoadTemplates failed: %v", err)
	}
	extra["HomeCommandBridge"] = true
	body, err := r.RenderTemplate("index", TemplateData{CurrentPage: "index", Extra: extra})
	if err != nil {
		t.Fatalf("RenderTemplate(index) failed: %v", err)
	}
	return body
}

// The Desk rail is Home's only view switcher (merge-tree-into-files FR1-FR6):
// Files then Map for every user, the Map/Tree toggle gone, and the current
// destination marked on both views.
func TestHomeDeskRailIsTheOnlySwitcher(t *testing.T) {
	cases := []struct {
		name        string
		extra       map[string]any
		current     string // the destination marked aria-current
		heading     string
		landingText string // Files' starting-page control, "" when there is none
		landingHide bool   // ...and hidden (it belongs to Files)
		local       bool
	}{
		{"remote on Map", map[string]any{}, "map", "Workspaces", "", false, false},
		{"remote on Files", map[string]any{"HomeViewTree": true}, "tree", "Files", "", false, false},
		{"local on Map", map[string]any{"FileSearchAvailable": true, "SearchLanding": "later"}, "map", "Workspaces", "Make Files my starting page", true, true},
		{"local on Files", map[string]any{"FileSearchAvailable": true, "HomeViewTree": true, "SearchLanding": "search"}, "tree", "Files", "Start on Map instead", false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := renderHome(t, tc.extra)

			files := strings.Index(body, `data-desk-destination="tree"`)
			mapAt := strings.Index(body, `data-desk-destination="map"`)
			if files < 0 || mapAt < 0 {
				t.Fatal("the rail must list Files and Map")
			}
			if mapAt > files {
				t.Error("Map must come before Files in the atlas toolbar")
			}

			for _, gone := range []string{`cockpitViewMap`, `cockpitViewTree`, `data-cockpit-view`, `cockpit-view-toggle`, `data-map-destination`} {
				if strings.Contains(body, gone) {
					t.Errorf("the Map/Tree toggle must be gone, found %q", gone)
				}
			}

			for _, dest := range []string{"tree", "map"} {
				tag := openingTag(t, body, `data-desk-destination="`+dest+`"`)
				if got, want := strings.Contains(tag, `aria-current="page"`), dest == tc.current; got != want {
					t.Errorf("%s link aria-current = %t, want %t: %s", dest, got, want, tag)
				}
			}
			if !strings.Contains(openingTag(t, body, `data-desk-destination="map"`), `href="/?view=map"`) {
				t.Error("Map must link to /?view=map: a bare / can land on the standalone page")
			}
			if !strings.Contains(openingTag(t, body, `data-desk-destination="tree"`), `href="/?view=tree"`) {
				t.Error("Files must link to /?view=tree")
			}

			// The narrow screen's picker offers the same two options on both views.
			picker := body[strings.Index(body, `data-map-picker`):]
			picker = picker[:strings.Index(picker, "</select>")]
			if strings.Count(picker, "<option") != 2 ||
				!strings.Contains(picker, `value="tree"`) || !strings.Contains(picker, `value="map"`) {
				t.Errorf("the Destination picker must offer exactly Files and Map: %s", picker)
			}
			// It has no href to fall back on, so it must not be usable before the
			// cockpit has attached its handler: the cockpit enables it.
			if !strings.Contains(openingTag(t, body, `data-map-picker`), " disabled") {
				t.Error("the Destination picker must be rendered disabled until the cockpit enables it")
			}
			if !strings.Contains(picker, `value="`+tc.current+`" selected`) {
				t.Errorf("the picker must have %s selected: %s", tc.current, picker)
			}

			// The cockpit itself starts in the same view, so nothing keyed on
			// data-view flashes the wrong destination before the script runs.
			if !strings.Contains(openingTag(t, body, `id="homeCockpit"`), `data-view="`+tc.current+`"`) {
				t.Errorf("#homeCockpit must start with data-view=%q", tc.current)
			}

			for id, wantHidden := range map[string]bool{
				"cockpitSummaryBtn":        tc.current == "map",
				"cockpitUpdatesSummaryBtn": tc.current == "tree",
			} {
				selector := `id="` + id + `"`
				if strings.Count(body, selector) != 1 || strings.Contains(openingTag(t, body, selector), " hidden") != wantHidden {
					t.Errorf("expected one #%s with hidden=%t before hydration", id, wantHidden)
				}
			}

			title := body[strings.Index(body, `data-cockpit-area-title`):]
			title = title[strings.Index(title, ">")+1 : strings.Index(title, "</h2>")]
			if title != tc.heading {
				t.Errorf("heading = %q, want %q", title, tc.heading)
			}

			// Folders are managed on their rows in Files now: no link to the
			// standalone page is left (merge-tree-into-files PR 2, req. 43). The
			// starting page is chosen beside the Files heading instead (req. 40).
			if strings.Contains(body, `data-files-manage`) || strings.Contains(body, "Manage search locations") {
				t.Fatal("the temporary Manage search locations link must be gone")
			}
			if got := strings.Contains(body, `data-files-landing`); got != (tc.landingText != "") {
				t.Fatalf("starting-page control present = %t, want %t", got, tc.landingText != "")
			}
			if tc.landingText != "" {
				tag := openingTag(t, body, `data-files-landing`)
				if strings.Contains(tag, " hidden") != tc.landingHide {
					t.Errorf("starting-page control hidden = %t, want %t: %s", !tc.landingHide, tc.landingHide, tag)
				}
				if !strings.Contains(tag, `data-landing="`+tc.extra["SearchLanding"].(string)+`"`) {
					t.Errorf("the control must carry the current choice as data-landing: %s", tag)
				}
				label := body[strings.Index(body, `data-files-landing`):]
				label = label[strings.Index(label, ">")+1 : strings.Index(label, "</button>")]
				if label != tc.landingText {
					t.Errorf("starting-page control reads %q, want %q", label, tc.landingText)
				}
			}

			// Only the local Mac keeps a same-tab place for the Map; a remote
			// browser has no endpoint to hold one.
			rail := openingTag(t, body, `class="desk-rail"`)
			if strings.Contains(rail, "data-desk-local") != tc.local {
				t.Errorf("data-desk-local on the rail = %t, want %t", !tc.local, tc.local)
			}
		})
	}
}

package workspacemap

import (
	"errors"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Frames (#346 FR-29, FR-43 – FR-45)
// ---------------------------------------------------------------------------

func TestNormalizeFrameAcceptsAUsableRectangle(t *testing.T) {
	frame, err := NormalizeFrame(Frame{X: 10.0004, Y: -20.0006, Width: 400.5, Height: 300.25})
	if err != nil {
		t.Fatalf("NormalizeFrame: %v", err)
	}
	// Rounded for storage, exactly as coordinates are: free placement needs
	// sub-unit precision, not float noise from repeated screen conversions.
	if frame != (Frame{X: 10, Y: -20.001, Width: 400.5, Height: 300.25}) {
		t.Errorf("normalized = %+v", frame)
	}
}

func TestNormalizeFrameRefusesRatherThanClamps(t *testing.T) {
	for name, frame := range map[string]Frame{
		"zero width":     {X: 0, Y: 0, Width: 0, Height: MinFrameHeight},
		"negative width": {X: 0, Y: 0, Width: -400, Height: MinFrameHeight},
		"below minimum":  {X: 0, Y: 0, Width: MinFrameWidth - 1, Height: MinFrameHeight},
		"below minimum height": {
			X: 0, Y: 0, Width: MinFrameWidth, Height: MinFrameHeight - 1,
		},
		"outside the world": {
			X: MaxCoordinate - 10, Y: 0, Width: MinFrameWidth, Height: MinFrameHeight,
		},
		"corner outside the world": {
			X: MinCoordinate - 1, Y: 0, Width: MinFrameWidth, Height: MinFrameHeight,
		},
	} {
		if _, err := NormalizeFrame(frame); !errors.Is(err, ErrInvalidFrame) {
			t.Errorf("%s: error = %v, want ErrInvalidFrame", name, err)
		}
	}
}

func TestFrameUnionContainsBoth(t *testing.T) {
	a := Frame{X: 0, Y: 0, Width: 200, Height: 200}
	b := Frame{X: 300, Y: -100, Width: 100, Height: 100}
	union := a.Union(b)
	if union != (Frame{X: 0, Y: -100, Width: 400, Height: 300}) {
		t.Errorf("union = %+v", union)
	}
}

func TestFrameTranslateKeepsItsSize(t *testing.T) {
	frame := Frame{X: 10, Y: 20, Width: 400, Height: 300}
	moved := frame.Translate(Point{X: -30, Y: 45})
	if moved != (Frame{X: -20, Y: 65, Width: 400, Height: 300}) {
		t.Errorf("translated = %+v", moved)
	}
}

// ---------------------------------------------------------------------------
// Presets (#346 FR-121 – FR-127, FR-194)
// ---------------------------------------------------------------------------

func TestCuratedCatalogsMeetTheDocumentedMinimums(t *testing.T) {
	// The default plus at least five accessible alternatives (FR-122).
	if len(Accents()) < 6 {
		t.Errorf("accents = %v, want the default plus at least five", Accents())
	}
	// The default district treatment plus at least two alternatives (FR-123).
	if len(Themes()) < 3 {
		t.Errorf("themes = %v, want the default plus at least two", Themes())
	}
	if !IsSupportedAccent(DefaultAccent) || !IsSupportedTheme(DefaultTheme) {
		t.Fatal("the defaults must be in their own catalogs")
	}
	if got := AccentLabel(DefaultAccent); got != "Ori green" {
		t.Errorf("default accent label = %q, want Ori green", got)
	}
	// Every identifier is a plain app-defined token, never anything that could
	// be interpolated into a stylesheet (FR-125, FR-194).
	for _, id := range append(Accents(), Themes()...) {
		if strings.ContainsAny(id, "(){};:#/\\\"'<> ") {
			t.Errorf("preset %q is not a safe stable identifier", id)
		}
		if AccentLabel(id) == "" && ThemeLabel(id) == "" {
			t.Errorf("preset %q has no human name", id)
		}
	}
}

func TestNormalizePresetsRefuseAnythingUncurated(t *testing.T) {
	for _, hostile := range []string{
		"", "  ", "url(https://evil.example/x.css)", "red; background:url(x)",
		"<script>", "DEFAULT", "moss ",
	} {
		if _, err := NormalizeAccent(hostile); !errors.Is(err, ErrUnsupportedPreset) {
			// "moss " is trimmed and therefore valid; assert that separately.
			if strings.TrimSpace(hostile) != "moss" {
				t.Errorf("accent %q: error = %v, want ErrUnsupportedPreset", hostile, err)
			}
		}
		if _, err := NormalizeTheme(hostile); !errors.Is(err, ErrUnsupportedPreset) {
			if strings.TrimSpace(hostile) != "blueprint" {
				t.Errorf("theme %q: error = %v, want ErrUnsupportedPreset", hostile, err)
			}
		}
	}
	if accent, err := NormalizeAccent("  moss  "); err != nil || accent != "moss" {
		t.Errorf("a padded valid accent = %q, %v", accent, err)
	}
}

// ---------------------------------------------------------------------------
// Tolerant reads (#346 FR-192, FR-194)
// ---------------------------------------------------------------------------

func TestSanitizeGroupPresentationDegradesPerFacet(t *testing.T) {
	valid := Frame{X: 0, Y: 0, Width: 400, Height: 400}
	clean := SanitizeGroupPresentation(GroupPresentation{
		SizingMode: SizingMode("elastic"),
		Frame:      &valid,
		Collapsed:  true,
		Accent:     "url(evil)",
		Theme:      "moss", // a real accent, but not a theme
	})
	if clean.SizingMode != SizingModeAuto || clean.Frame != nil {
		t.Errorf("an unknown sizing mode must fall back to auto with no frame: %+v", clean)
	}
	if !clean.Collapsed {
		t.Error("the collapse state is independent of the sizing mode and survives")
	}
	if clean.Accent != DefaultAccent || clean.Theme != DefaultTheme {
		t.Errorf("unknown presets must fall back: %+v", clean)
	}

	// A custom mode with an unusable rectangle keeps everything else.
	bad := Frame{X: 0, Y: 0, Width: 1, Height: 1}
	degraded := SanitizeGroupPresentation(GroupPresentation{
		SizingMode: SizingModeCustom,
		Frame:      &bad,
		Accent:     "tide",
		Theme:      "blueprint",
	})
	if degraded.SizingMode != SizingModeAuto || degraded.Frame != nil {
		t.Errorf("an unusable frame must drop to automatic sizing: %+v", degraded)
	}
	if degraded.Accent != "tide" || degraded.Theme != "blueprint" {
		t.Errorf("a bad frame must not cost the district its appearance: %+v", degraded)
	}
}

func TestDefaultGroupPresentationIsRecognisedAsDefault(t *testing.T) {
	record := DefaultGroupPresentation()
	if !record.IsDefault() {
		t.Fatal("the default record must report itself as default")
	}
	if record.HasCustomAppearance() {
		t.Error("the default appearance is not a custom one")
	}
	record.Accent = "moss"
	if record.IsDefault() || !record.HasCustomAppearance() {
		t.Error("a chosen accent is a customization")
	}
}

// ---------------------------------------------------------------------------
// Operation validation (#346 FR-178 – FR-180)
// ---------------------------------------------------------------------------

func TestGroupOperationsRejectFieldsFromOtherKinds(t *testing.T) {
	frame := Frame{X: 0, Y: 0, Width: 400, Height: 400}
	collapsed := true
	for name, op := range map[string]Operation{
		"frame on fit":       {Kind: OpFitGroupToContents, GroupID: "g", Frame: &frame},
		"collapsed on frame": {Kind: OpSetGroupFrame, GroupID: "g", Frame: &frame, Collapsed: &collapsed},
		"accent on collapse": {Kind: OpSetGroupCollapsed, GroupID: "g", Collapsed: &collapsed, Accent: "moss"},
		"positions on frame": {
			Kind: OpSetGroupFrame, GroupID: "g", Frame: &frame,
			Positions: map[string]Point{"a": {X: 1, Y: 1}},
		},
		"frame on set_positions": {
			Kind: OpSetPositions, Positions: map[string]Point{"a": {X: 1, Y: 1}}, Frame: &frame,
		},
		"group on reset": {Kind: OpReset, GroupID: "g"},
	} {
		if _, err := NormalizeOperation(op); !errors.Is(err, ErrInvalidPatch) {
			t.Errorf("%s: error = %v, want ErrInvalidPatch", name, err)
		}
	}
}

func TestGroupOperationsRequireTheirOwnFields(t *testing.T) {
	for name, op := range map[string]Operation{
		"frame without a rectangle": {Kind: OpSetGroupFrame, GroupID: "g"},
		"collapse without a state":  {Kind: OpSetGroupCollapsed, GroupID: "g"},
		"appearance with neither":   {Kind: OpSetGroupAppearance, GroupID: "g"},
		"fit without a group":       {Kind: OpFitGroupToContents},
		"frame without a group":     {Kind: OpSetGroupFrame, Frame: &Frame{X: 0, Y: 0, Width: 400, Height: 400}},
	} {
		if _, err := NormalizeOperation(op); err == nil {
			t.Errorf("%s: expected a rejection", name)
		}
	}
}

func TestGroupAppearanceAcceptsEitherFacetAlone(t *testing.T) {
	accentOnly, err := NormalizeOperation(SetGroupAppearance("g", "moss", ""))
	if err != nil {
		t.Fatalf("accent only: %v", err)
	}
	if accentOnly.Accent != "moss" || accentOnly.Theme != "" {
		t.Errorf("accent-only operation = %+v; an unmentioned theme means 'leave it'", accentOnly)
	}
	themeOnly, err := NormalizeOperation(SetGroupAppearance("g", "", "blueprint"))
	if err != nil {
		t.Fatalf("theme only: %v", err)
	}
	if themeOnly.Theme != "blueprint" || themeOnly.Accent != "" {
		t.Errorf("theme-only operation = %+v", themeOnly)
	}
}

package overview

import (
	"regexp"
	"strings"
	"testing"
)

// escapePattern matches an ANSI select-graphic-rendition sequence.
var escapePattern = regexp.MustCompile("\x1b\\[[0-9;]*m")

func renderColored(t *testing.T, snapshot Snapshot) string {
	t.Helper()
	var out strings.Builder
	if err := RenderCompact(&out, snapshot, RenderOptions{NoColor: false}); err != nil {
		t.Fatalf("RenderCompact: %v", err)
	}
	return out.String()
}

// stripEscapes removes styling so colored and plain output can be compared
// character for character.
func stripEscapes(value string) string { return escapePattern.ReplaceAllString(value, "") }

func TestColorIsAddedWithoutChangingTheText(t *testing.T) {
	// Color is an enhancement. Removing it from the styled output must yield
	// exactly the plain rendering, which is what guarantees a reader with
	// color disabled loses nothing but emphasis.
	snapshot := richSnapshot(t)

	var plain strings.Builder
	if err := RenderCompact(&plain, snapshot, RenderOptions{NoColor: true}); err != nil {
		t.Fatalf("RenderCompact: %v", err)
	}
	colored := renderColored(t, snapshot)

	if !strings.Contains(colored, "\x1b[") {
		t.Fatal("color was enabled but no styling was emitted")
	}
	if stripEscapes(colored) != plain.String() {
		t.Fatalf("styling changed the text.\ncolored (stripped):\n%s\nplain:\n%s", stripEscapes(colored), plain.String())
	}
}

func TestColoredColumnsStayAligned(t *testing.T) {
	// The classic failure: escape sequences counted as printed width push
	// every subsequent column out of line. Padding must be computed on
	// printed width, so both renderings share a column layout exactly.
	snapshot := richSnapshot(t)

	var plain strings.Builder
	if err := RenderCompact(&plain, snapshot, RenderOptions{NoColor: true}); err != nil {
		t.Fatalf("RenderCompact: %v", err)
	}
	plainLines := strings.Split(strings.TrimRight(plain.String(), "\n"), "\n")
	coloredLines := strings.Split(strings.TrimRight(renderColored(t, snapshot), "\n"), "\n")

	if len(plainLines) != len(coloredLines) {
		t.Fatalf("line counts differ: plain %d, colored %d", len(plainLines), len(coloredLines))
	}
	for index := range plainLines {
		if got, want := stripEscapes(coloredLines[index]), plainLines[index]; got != want {
			t.Fatalf("line %d misaligned:\ncolored (stripped): %q\nplain:              %q", index, got, want)
		}
	}
}

func TestNoColorEmitsNoEscapesAnywhere(t *testing.T) {
	snapshot := richSnapshot(t)
	row, _ := snapshot.Feature("downloads-janitor")

	surfaces := map[string]func(*strings.Builder) error{
		"compact":  func(out *strings.Builder) error { return RenderCompact(out, snapshot, RenderOptions{NoColor: true}) },
		"expanded": func(out *strings.Builder) error { return RenderExpanded(out, snapshot, RenderOptions{NoColor: true}) },
		"detail": func(out *strings.Builder) error {
			return RenderDetail(out, snapshot, row, RenderOptions{NoColor: true})
		},
	}
	for name, render := range surfaces {
		var out strings.Builder
		if err := render(&out); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if strings.Contains(out.String(), "\x1b") {
			t.Fatalf("%s surface emitted an escape sequence with color disabled", name)
		}
	}
}

func TestEverySurfaceCanBeColored(t *testing.T) {
	snapshot := richSnapshot(t)
	row, _ := snapshot.Feature("downloads-janitor")

	surfaces := map[string]func(*strings.Builder) error{
		"compact":  func(out *strings.Builder) error { return RenderCompact(out, snapshot, RenderOptions{}) },
		"expanded": func(out *strings.Builder) error { return RenderExpanded(out, snapshot, RenderOptions{}) },
		"detail":   func(out *strings.Builder) error { return RenderDetail(out, snapshot, row, RenderOptions{}) },
	}
	for name, render := range surfaces {
		var out strings.Builder
		if err := render(&out); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !strings.Contains(out.String(), "\x1b[") {
			t.Fatalf("%s surface emitted no color", name)
		}
	}
}

func TestSeverityColorsAreDistinct(t *testing.T) {
	colors := palette{enabled: true}
	error, warning, info := colors.severity(SeverityError), colors.severity(SeverityWarning), colors.severity(SeverityInfo)

	if error == warning || warning == info || error == info {
		t.Fatalf("severities share styling: %q %q %q", error, warning, info)
	}
	// Text must survive intact so meaning never depends on the color alone.
	for _, pair := range [][2]string{{error, "error"}, {warning, "warning"}, {info, "info"}} {
		if stripEscapes(pair[0]) != pair[1] {
			t.Fatalf("styled %q lost its label, got %q", pair[1], stripEscapes(pair[0]))
		}
	}
}

func TestIncompleteSnapshotIsColoredAsAlarming(t *testing.T) {
	colors := palette{enabled: true}
	complete := colors.snapshotStatus("complete", true)
	incomplete := colors.snapshotStatus("INCOMPLETE", false)

	if !strings.Contains(complete, ansiGreen) {
		t.Fatalf("a complete snapshot was not styled as healthy: %q", complete)
	}
	if !strings.Contains(incomplete, ansiRed) || !strings.Contains(incomplete, ansiBold) {
		t.Fatalf("an incomplete snapshot must not read like a healthy one: %q", incomplete)
	}
}

func TestAgentColorTracksTheWeakestBinding(t *testing.T) {
	colors := palette{enabled: true}

	healthy := feature("a")
	healthy.Agents = []Agent{{Binding: BindingExact, StatusAvailability: AvailabilityAvailable, Status: AgentIdle, Role: "builder"}}

	drifted := feature("b")
	drifted.Agents = []Agent{
		{Binding: BindingExact, StatusAvailability: AvailabilityAvailable, Status: AgentIdle, Role: "builder"},
		{Binding: BindingPossibleDrift, StatusAvailability: AvailabilityAvailable, Status: AgentIdle, Role: "reviewer"},
	}

	broken := feature("c")
	broken.Agents = []Agent{{Binding: BindingMissing, StatusAvailability: AvailabilityAvailable, Status: AgentMissing, Role: "builder"}}

	if !strings.Contains(colors.agents(healthy), ansiGreen) {
		t.Fatal("a healthy binding was not styled green")
	}
	// One drifted role among healthy ones is the thing worth noticing.
	if !strings.Contains(colors.agents(drifted), ansiYellow) {
		t.Fatal("a drifted role did not downgrade the row's styling")
	}
	if !strings.Contains(colors.agents(broken), ansiRed) {
		t.Fatal("a missing agent was not styled red")
	}
}

func TestWidthIgnoresEscapeSequences(t *testing.T) {
	colors := palette{enabled: true}
	styled := colors.paint("abc", ansiRed, ansiBold)

	if got := width(styled); got != 3 {
		t.Fatalf("width(%q) = %d, want 3", styled, got)
	}
	if got := width("abc"); got != 3 {
		t.Fatalf("width of plain text = %d, want 3", got)
	}
	if got := width(""); got != 0 {
		t.Fatalf("width of empty string = %d, want 0", got)
	}
}

func TestPadUsesPrintedWidth(t *testing.T) {
	colors := palette{enabled: true}
	styled := colors.paint("ab", ansiGreen)

	padded := pad(styled, 5)
	if width(padded) != 5 {
		t.Fatalf("padded printed width = %d, want 5", width(padded))
	}
	if !strings.HasSuffix(padded, "   ") {
		t.Fatalf("padding was not appended after the reset: %q", padded)
	}
	// Already wide enough: leave it alone.
	if got := pad(styled, 1); got != styled {
		t.Fatalf("pad shrank a value: %q", got)
	}
}

func TestDisabledPaletteIsAPassthrough(t *testing.T) {
	colors := palette{enabled: false}
	if got := colors.paint("text", ansiRed, ansiBold); got != "text" {
		t.Fatalf("disabled palette styled the text: %q", got)
	}
	if got := colors.severity(SeverityError); got != "error" {
		t.Fatalf("disabled palette styled a severity: %q", got)
	}
}

func TestColoredOutputHasNoTrailingWhitespace(t *testing.T) {
	for _, line := range strings.Split(renderColored(t, richSnapshot(t)), "\n") {
		if line != strings.TrimRight(line, " \t") {
			t.Fatalf("colored line has trailing whitespace: %q", line)
		}
	}
}

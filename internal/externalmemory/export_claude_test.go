package externalmemory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderClaudeImport_GoldenFile(t *testing.T) {
	profile := ExportProfile{
		Name: "Sam Example", Role: "Software developer", TimeZone: "Asia/Seoul", Language: "ko-KR",
		About:           "Builds trading research tools and a personal agent app.",
		Specializations: []string{"Go", "trading research"},
		Preferences:     map[string]string{"response_style": "Plain language, no jargon", "detail-level": "Lead with the result, then the method", "empty": " "},
		Date:            "2026-09-30",
	}
	facts := []ExportFact{
		{Category: "routines", Text: "Mixes at night.", Date: "2026-10-01"},
		{Category: "projects", Text: "chart-lab: fee-aware backtests of EMA exit rules.", Date: "2026-10-05"},
		{Category: "how_you_work", Text: "Reports backtests net of fees.", Date: "2026-08-01"},
		{Category: "sources", Text: "Relies on the exchange's public fee schedule."},
		{Category: "how_you_work", Text: "Album 3 masters at -14 LUFS.", Date: "2026-10-12", FromWorkspace: true},
	}
	got := RenderClaudeImport(profile, facts)
	golden := filepath.Join("testdata", "export", "claude_import.golden")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(golden, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Fatalf("export differs from %s:\n--- got\n%s--- want\n%s", golden, got, want)
	}
}

func TestRenderClaudeImport_EmptyKeepsAllFiveHeadingsAndRoundTripsThroughThePaste(t *testing.T) {
	empty := RenderClaudeImport(ExportProfile{}, nil)
	for _, section := range ClaudeImportSections {
		if !strings.Contains(empty, "## "+section+"\n") {
			t.Fatalf("missing %s heading:\n%s", section, empty)
		}
	}
	// What Ori exports, Ori's own paste parser reads back line for line.
	text := RenderClaudeImport(ExportProfile{Name: "Sam", Date: "2026-09-30"}, []ExportFact{
		{Category: "projects", Text: "chart-lab backtests.", Date: "2026-10-05"},
		{Category: "people", Text: "Works with Jordan on mixes."},
	})
	entries := ParseAccountPaste(text)
	if len(entries) != 3 {
		t.Fatalf("round trip gave %d entries:\n%s", len(entries), text)
	}
	if e := find(t, entries, "chart-lab backtests."); e.Category != "projects" || e.Date != "2026-10-05" {
		t.Fatalf("round-tripped project: %+v", e)
	}
	if e := find(t, entries, "Works with Jordan"); e.Category != "preferences" || e.Date != "" {
		t.Fatalf("round-tripped undated fact: %+v", e)
	}
}

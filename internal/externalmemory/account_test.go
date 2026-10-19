package externalmemory

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func readFixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "account", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestParseAccountPaste_ExportFormat(t *testing.T) {
	entries := ParseAccountPaste(readFixture(t, "export.md"))
	if len(entries) != 7 {
		t.Fatalf("entries = %d, want one per dated line (7)", len(entries))
	}
	assertCategories(t, entries, map[string]int{"instructions": 2, "identity": 1, "career": 1, "projects": 2, "preferences": 1})
	first := entries[0]
	if first.Body != "Lead with the result, then the method." || first.Date != "2026-09-02" || first.Category != "instructions" {
		t.Fatalf("first entry = %+v", first)
	}
	career := find(t, entries, "Previously worked as a music producer.")
	if career.Date != "" {
		t.Fatalf("[unknown] should leave the date empty, got %q", career.Date)
	}
	for _, e := range entries {
		if e.Source != SourceClaudeAccount || !strings.HasPrefix(e.Origin, "paste#") || strings.Contains(e.Body, "[20") {
			t.Fatalf("entry %+v", e)
		}
	}
}

func TestParseAccountPaste_VerbatimProse(t *testing.T) {
	entries := ParseAccountPaste(readFixture(t, "verbatim.md"))
	// The intro line, the bold pseudo-heading, the identity paragraph, two
	// bullets, and two numbered items.
	if len(entries) != 7 {
		var titles []string
		for _, e := range entries {
			titles = append(titles, e.Category+": "+e.Title)
		}
		t.Fatalf("entries = %d: %q", len(entries), titles)
	}
	intro := find(t, entries, "Here is everything I remember")
	if intro.Category != "account" {
		t.Fatalf("text before any heading should be category account, got %q", intro.Category)
	}
	if pref := find(t, entries, "You want backtest results reported net of fees."); pref.Category != "preferences" {
		t.Fatalf("bullet under # Preferences: %q", pref.Category)
	}
	find(t, entries, "결과는 한국어로")
}

func TestParseAccountPaste_UnchangedTextKeepsItsID(t *testing.T) {
	first := idsByTitle(ParseAccountPaste(readFixture(t, "export.md")))
	edited := strings.Replace(readFixture(t, "export.md"),
		"[2026-07-20] - Prefers plain language over jargon.",
		"[2026-10-19] - Prefers plain language and short answers.", 1)
	// Moving a line under another heading does not change its id either.
	edited = strings.Replace(edited, "## Career\n", "## Background\n", 1)
	second := idsByTitle(ParseAccountPaste(edited))
	for title, id := range first {
		if title == "Prefers plain language over jargon." {
			if _, still := second[title]; still {
				t.Fatal("the edited line should be gone")
			}
			continue
		}
		if second[title] != id {
			t.Fatalf("unchanged line %q changed id", title)
		}
	}
	if len(second) != len(first) {
		t.Fatalf("second paste has %d entries, want %d", len(second), len(first))
	}
}

func TestStore_PasteRoundTripAndLimits(t *testing.T) {
	store := NewStore(t.TempDir())
	if _, ok := store.LoadPaste(SourceClaudeAccount); ok {
		t.Fatal("no paste yet")
	}
	at := time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC)
	if err := store.SavePaste(SourceClaudeAccount, "- a fact", at); err != nil {
		t.Fatal(err)
	}
	if text, ok := store.LoadPaste(SourceClaudeAccount); !ok || text != "- a fact" {
		t.Fatalf("paste = %q %v", text, ok)
	}
	if got := store.LoadSources().Source(SourceClaudeAccount).PasteDate; got == nil || !got.Equal(at) {
		t.Fatalf("paste date = %v", got)
	}
	if err := store.SavePaste(SourceClaudeAccount, strings.Repeat("x", MaxPasteBytes+1), at); err != ErrPasteTooLarge {
		t.Fatalf("oversized paste: %v", err)
	}
	if err := store.SavePaste(SourceClaudeAccount, "  \n ", at); err != ErrPasteEmpty {
		t.Fatalf("empty paste: %v", err)
	}
	if err := store.SavePaste("../x", "text", at); err == nil {
		t.Fatal("path-like source accepted")
	}
}

func TestClaudeAccountSource_RepasteReplacesAndKeepsPromotedLinks(t *testing.T) {
	store := NewStore(t.TempDir())
	enable(t, store, SourceClaudeAccount)
	runner := NewRunner(store, NewRegistry(NewClaudeAccountSource(store)))
	source := NewClaudeAccountSource(store)
	if source.Available() {
		t.Fatal("available before any paste")
	}
	if err := store.SavePaste(SourceClaudeAccount, readFixture(t, "export.md"), time.Now()); err != nil {
		t.Fatal(err)
	}
	result, err := runner.RunSource(context.Background(), SourceClaudeAccount, ReasonPaste)
	if err != nil || onlyResult(t, result).Counts.Added != 7 {
		t.Fatalf("first paste: %+v %v", result, err)
	}
	kept := find(t, store.LoadEntries(SourceClaudeAccount), "Lead with the result")
	dropped := find(t, store.LoadEntries(SourceClaudeAccount), "Prefers plain language")
	for _, id := range []string{kept.ID, dropped.ID} {
		if _, _, err := store.UpdateEntry(SourceClaudeAccount, id, func(e *Entry) { e.PromotedItemID = "item-" + id[:4] }); err != nil {
			t.Fatal(err)
		}
	}
	repaste := strings.Replace(readFixture(t, "export.md"), "[2026-07-20] - Prefers plain language over jargon.\n", "", 1)
	if err := store.SavePaste(SourceClaudeAccount, repaste, time.Now()); err != nil {
		t.Fatal(err)
	}
	result, err = runner.RunSource(context.Background(), SourceClaudeAccount, ReasonPaste)
	if counts := onlyResult(t, result).Counts; err != nil || counts.Removed != 1 || counts.Added != 0 || counts.Entries != 6 {
		t.Fatalf("re-paste: %+v %v", counts, err)
	}
	for _, e := range store.LoadEntries(SourceClaudeAccount) {
		switch e.ID {
		case kept.ID:
			if !e.Active() || e.PromotedItemID == "" {
				t.Fatalf("unchanged line lost its state: %+v", e)
			}
		case dropped.ID:
			if e.Active() || e.PromotedItemID == "" {
				t.Fatalf("dropped line should be removed with its link kept: %+v", e)
			}
		}
	}
	// A paste-only sync leaves the daily timer alone.
	if store.LoadSources().LastCompletedSync != nil {
		t.Fatal("a paste sync armed the daily timer")
	}
}

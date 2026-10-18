package workspace

import (
	"fmt"
	"strings"
	"testing"
)

// overBudgetPromptFixture is a hand-edited MEMORY.md that exceeds the prompt
// budget: every entry type, an unknown type, a copied-from-global entry, an HQ
// managed marker, unstructured prose, equal dates (stable order), and short
// old entries that fit the leftover budget after longer ones were dropped.
func overBudgetPromptFixture() string {
	var b strings.Builder
	b.WriteString("# Workspace Memory\n\n")
	b.WriteString("Hand-written context line that is never injected.\n\n")
	b.WriteString("- [watch, 2025-01-03, run:w1 (Scout)] Checked upstream through commit 95c10b3.\n")
	b.WriteString("- [fact, 2026-05-01, ori-hq:item-1:rev-2] HQ-SENTINEL reviewed learning must never reach the workspace prompt.\n")
	b.WriteString("- [thread, 2025-02-11, run:t1] Open investigation: flaky sync test on Linux runners.\n")
	b.WriteString("- [decision, 2026-04-20, user] Ship weekly on Thursdays; rationale recorded in notes/release.md.\n")
	b.WriteString("- [dead-end, 2026-04-18, run:d1] Rewriting the sync loop with fsnotify did not fix the flake.\n")
	b.WriteString("- [feedback, 2026-04-19, user] Keep summaries under five bullets.\n")
	b.WriteString("- [fact, 2026-04-19, global:gm-42] Prefers metric units in every report.\n")
	b.WriteString("- [mystery, 2026-04-17, run:m1] Unknown type degrades to fact.\n")
	b.WriteString("- a plain bullet without the entry shape\n")
	types := []string{"fact", "decision", "feedback", "dead-end"}
	for i := 0; i < 36; i++ {
		fmt.Fprintf(&b, "- [%s, 2026-03-%02d, run:f%02d] Filler %02d %s\n",
			types[i%len(types)], (i%28)+1, i, i, strings.TrimSpace(strings.Repeat("lorem ipsum dolor ", 12)))
	}
	b.WriteString("- [fact, 2025-12-01, user] Short old fact A.\n")
	b.WriteString("- [decision, 2025-12-02, user] Short old decision B.\n")
	b.WriteString("- [dead-end, 2025-12-03, user] Short old dead end C.\n")
	b.WriteString("- [thread, 2025-03-01, run:t2] Second open thread: release notes drift.\n")
	return b.String()
}

// underBudgetPromptFixture fits the budget; only the managed marker is held back.
const underBudgetPromptFixture = `# Workspace Memory

Prose the user wrote by hand.

- [fact, 2026-06-01, user] Releases are human-triggered from main.
- [feedback, 2026-06-02, user] Prefer short status updates.
- [decision, 2026-06-03, run:a1b2] Use SQLite for the local index.
- [dead-end, 2026-06-04, run:c3d4] Polling the API every minute hit rate limits.
- [watch, 2026-06-05, run:g8h9 (Scout)] Checked through commit 95c10b3.
- [thread, 2026-06-06, run:e5f6] Investigating the slow startup.
- [fact, 2026-06-07, global:gm-7] Time zone is America/Los_Angeles.
- [fact, 2026-06-08, ori-hq:item-9:rev-1] HQ-SENTINEL managed line.
`

// Captured from RenderMemoryPromptSection before MemoryPromptSelection
// existed. The prompt section must stay byte-identical (memory station FR30).
const overBudgetPromptGolden = `## Workspace Memory

- [watch, 2025-01-03, run:w1 (Scout)] Checked upstream through commit 95c10b3.
- [thread, 2025-02-11, run:t1] Open investigation: flaky sync test on Linux runners.
- [decision, 2026-04-20, user] Ship weekly on Thursdays; rationale recorded in notes/release.md.
- [dead-end, 2026-04-18, run:d1] Rewriting the sync loop with fsnotify did not fix the flake.
- [feedback, 2026-04-19, user] Keep summaries under five bullets.
- [fact, 2026-04-19, global:gm-42] Prefers metric units in every report.
- [fact, 2026-04-17, run:m1] Unknown type degrades to fact.
- [fact, 2026-03-05, run:f04] Filler 04 lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor
- [decision, 2026-03-06, run:f05] Filler 05 lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor
- [feedback, 2026-03-07, run:f06] Filler 06 lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor
- [dead-end, 2026-03-08, run:f07] Filler 07 lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor
- [fact, 2026-03-09, run:f08] Filler 08 lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor
- [decision, 2026-03-10, run:f09] Filler 09 lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor
- [feedback, 2026-03-11, run:f10] Filler 10 lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor
- [dead-end, 2026-03-12, run:f11] Filler 11 lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor
- [fact, 2026-03-13, run:f12] Filler 12 lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor
- [decision, 2026-03-14, run:f13] Filler 13 lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor
- [feedback, 2026-03-15, run:f14] Filler 14 lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor
- [dead-end, 2026-03-16, run:f15] Filler 15 lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor
- [fact, 2026-03-17, run:f16] Filler 16 lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor
- [decision, 2026-03-18, run:f17] Filler 17 lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor
- [feedback, 2026-03-19, run:f18] Filler 18 lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor
- [dead-end, 2026-03-20, run:f19] Filler 19 lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor
- [fact, 2026-03-21, run:f20] Filler 20 lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor
- [decision, 2026-03-22, run:f21] Filler 21 lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor
- [feedback, 2026-03-23, run:f22] Filler 22 lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor
- [dead-end, 2026-03-24, run:f23] Filler 23 lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor
- [fact, 2026-03-25, run:f24] Filler 24 lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor
- [decision, 2026-03-26, run:f25] Filler 25 lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor
- [feedback, 2026-03-27, run:f26] Filler 26 lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor
- [dead-end, 2026-03-28, run:f27] Filler 27 lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor
- [fact, 2026-03-05, run:f32] Filler 32 lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor
- [decision, 2026-03-06, run:f33] Filler 33 lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor
- [feedback, 2026-03-07, run:f34] Filler 34 lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor
- [dead-end, 2026-03-08, run:f35] Filler 35 lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor lorem ipsum dolor
- [decision, 2025-12-02, user] Short old decision B.
- [dead-end, 2025-12-03, user] Short old dead end C.
- [thread, 2025-03-01, run:t2] Second open thread: release notes drift.

(memory truncated — 9 entries not shown; consider pruning)
`

// Captured the same way, rendered with the tool guidance (mission/chat path).
const underBudgetPromptGolden = `## Workspace Memory

Persistent knowledge accumulated for this workspace across runs and from the user. Treat it as context you already established; verify load-bearing facts before relying on them. Use memory_write to record durable operational knowledge (stable facts, decisions and their rationale, dead ends, watch-state/baselines, open threads) and memory_forget to remove what's wrong or stale. Do not store deliverables (write a note), work items (create a task), or secrets/credentials (use the Vault) here.

- [fact, 2026-06-01, user] Releases are human-triggered from main.
- [feedback, 2026-06-02, user] Prefer short status updates.
- [decision, 2026-06-03, run:a1b2] Use SQLite for the local index.
- [dead-end, 2026-06-04, run:c3d4] Polling the API every minute hit rate limits.
- [watch, 2026-06-05, run:g8h9 (Scout)] Checked through commit 95c10b3.
- [thread, 2026-06-06, run:e5f6] Investigating the slow startup.
- [fact, 2026-06-07, global:gm-7] Time zone is America/Los_Angeles.
`

func TestRenderMemoryPromptSection_GoldenOverBudget(t *testing.T) {
	got := RenderMemoryPromptSection(ParseMemoryDocument(overBudgetPromptFixture()), false)
	if got != overBudgetPromptGolden {
		t.Fatalf("over-budget prompt section changed.\n--- got ---\n%s\n--- want ---\n%s", got, overBudgetPromptGolden)
	}
}

func TestRenderMemoryPromptSection_GoldenUnderBudget(t *testing.T) {
	got := RenderMemoryPromptSection(ParseMemoryDocument(underBudgetPromptFixture), true)
	if got != underBudgetPromptGolden {
		t.Fatalf("under-budget prompt section changed.\n--- got ---\n%s\n--- want ---\n%s", got, underBudgetPromptGolden)
	}
}

// renderedPromptLines returns the set of lines in a rendered prompt section.
func renderedPromptLines(section string) map[string]bool {
	lines := make(map[string]bool)
	for _, line := range strings.Split(section, "\n") {
		lines[line] = true
	}
	return lines
}

func TestMemoryPromptSelection_MatchesRendererOverBudget(t *testing.T) {
	doc := ParseMemoryDocument(overBudgetPromptFixture())
	entries := doc.Entries()
	inPrompt := MemoryPromptSelection(entries)
	if len(inPrompt) != len(entries) {
		t.Fatalf("selection has %d slots for %d entries", len(inPrompt), len(entries))
	}
	rendered := renderedPromptLines(RenderMemoryPromptSection(doc, false))
	left := 0
	for i, entry := range entries {
		if want := rendered[entry.Render()]; inPrompt[i] != want {
			t.Errorf("entry %d %q: in prompt = %v, rendered = %v", i, entry.Text, inPrompt[i], want)
		}
		if !inPrompt[i] && !IsHQManagedProvenance(entry.Provenance) {
			left++
		}
	}
	if left != 9 {
		t.Errorf("ordinary entries left out = %d, want the 9 the truncation notice reports", left)
	}
	if entries[1].Text != "HQ-SENTINEL reviewed learning must never reach the workspace prompt." || inPrompt[1] {
		t.Errorf("managed marker at index 1 must be out of the prompt: %+v in=%v", entries[1], inPrompt[1])
	}
}

func TestMemoryPromptSelection_UnderBudgetEveryOrdinaryEntryIsIn(t *testing.T) {
	entries := ParseMemoryDocument(underBudgetPromptFixture).Entries()
	inPrompt := MemoryPromptSelection(entries)
	if len(inPrompt) != len(entries) || len(entries) != 8 {
		t.Fatalf("selection has %d slots for %d entries", len(inPrompt), len(entries))
	}
	for i, entry := range entries {
		managed := IsHQManagedProvenance(entry.Provenance)
		if inPrompt[i] == managed {
			t.Errorf("entry %d %q: in prompt = %v, managed = %v", i, entry.Text, inPrompt[i], managed)
		}
	}
}

func TestMemoryPromptSelection_EmptyAndManagedOnly(t *testing.T) {
	if got := MemoryPromptSelection(ParseMemoryDocument("").Entries()); len(got) != 0 {
		t.Errorf("empty memory selection = %v", got)
	}
	got := MemoryPromptSelection(ParseMemoryDocument("- [fact, 2026-06-08, ori-hq:item-9:rev-1] managed only\n").Entries())
	if len(got) != 1 || got[0] {
		t.Errorf("managed-only selection = %v, want [false]", got)
	}
}

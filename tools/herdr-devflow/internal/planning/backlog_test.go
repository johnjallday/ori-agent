package planning

import (
	"path/filepath"
	"testing"
	"time"
)

const sampleBacklog = `# Backlog

## Ideas
- 2026-07-19 Template: Downloads Janitor — file-watch ~/Downloads #small
- 2026-07-22 google-account easy connection setup

## Doing
- Email Ops workspace spin-off → PRD at tasks/prd-email-ops-workspace.md (station → portal)
- calendar-ops-mcp -> PRD at tasks/prd-calendar-ops-mcp.md (started 2026-07-20)
- downloads-janitor -> PRD at tasks/prd-downloads-janitor.md (started 2026-07-24)

## Shipped / dropped
- 2026-07-24 herdr-start-kind - PR #260 merged to dev (2026-07-24)
- 2026-07-23 calendar-ops-mcp -> PRD at tasks/prd-calendar-ops-mcp.md - PR #257 merged to dev
- 2026-07-20 legacy-plugin-runtime dropped in favour of MCP servers
- 2026-07-19 HQ cross-workspace visibility (hq_overview + Watchtower) — PR #240 merged to dev
`

func readSample(t *testing.T) Backlog {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "BACKLOG.md")
	writeFile(t, path, sampleBacklog)
	return ReadBacklog(path, time.Now())
}

func TestReadBacklogIgnoresIdeasAndKeepsLifecycleSections(t *testing.T) {
	backlog := readSample(t)
	if backlog.State != StateAvailable {
		t.Fatalf("state = %q, want available", backlog.State)
	}
	if len(backlog.All) != 7 {
		t.Fatalf("parsed %d lifecycle entries, want 7 (Ideas must be skipped)", len(backlog.All))
	}
	if _, ok := backlog.Entry("google-account"); ok {
		t.Fatal("an Ideas line was joined to a slug")
	}
}

func TestReadBacklogResolvesSlugsFromPRDReferenceAndLeadingToken(t *testing.T) {
	backlog := readSample(t)

	entry, ok := backlog.Entry("email-ops-workspace")
	if !ok {
		t.Fatal("prose entry with a PRD reference was not resolved")
	}
	if entry.Lifecycle != LifecycleDoing {
		t.Fatalf("lifecycle = %q, want doing", entry.Lifecycle)
	}

	if entry, ok = backlog.Entry("downloads-janitor"); !ok || entry.Lifecycle != LifecycleDoing {
		t.Fatalf("downloads-janitor = %+v, ok=%v, want doing", entry, ok)
	}
	if entry, ok = backlog.Entry("herdr-start-kind"); !ok || entry.Lifecycle != LifecycleShipped {
		t.Fatalf("herdr-start-kind = %+v, ok=%v, want shipped", entry, ok)
	}
}

func TestReadBacklogTerminalSectionWinsOverDoing(t *testing.T) {
	backlog := readSample(t)
	entry, ok := backlog.Entry("calendar-ops-mcp")
	if !ok {
		t.Fatal("calendar-ops-mcp missing")
	}
	if entry.Lifecycle != LifecycleShipped {
		t.Fatalf("lifecycle = %q, want shipped (terminal record wins over a stale Doing entry)", entry.Lifecycle)
	}
	// The stale Doing line must still be visible so backlog drift is reportable.
	doing := 0
	for _, candidate := range backlog.All {
		if candidate.Slug == "calendar-ops-mcp" && candidate.Lifecycle == LifecycleDoing {
			doing++
		}
	}
	if doing != 1 {
		t.Fatalf("stale Doing entry count = %d, want 1 retained for drift reporting", doing)
	}
}

func TestReadBacklogClassifiesDroppedWithinCombinedSection(t *testing.T) {
	backlog := readSample(t)
	entry, ok := backlog.Entry("legacy-plugin-runtime")
	if !ok {
		t.Fatal("dropped entry was not resolved")
	}
	if entry.Lifecycle != LifecycleDropped {
		t.Fatalf("lifecycle = %q, want dropped", entry.Lifecycle)
	}
}

func TestReadBacklogKeepsProseEntriesUnjoined(t *testing.T) {
	backlog := readSample(t)
	for _, entry := range backlog.All {
		if entry.Line == 0 || entry.Text == "" {
			t.Fatalf("entry lost provenance: %+v", entry)
		}
	}
	// A merged-PR line whose subject is prose must not be joined at all, and
	// its date prefix must never be mistaken for a hyphenated slug.
	for _, entry := range backlog.All {
		if entry.Line != 24 {
			continue
		}
		if entry.Slug != "" {
			t.Fatalf("prose entry guessed the slug %q", entry.Slug)
		}
	}
	if _, ok := backlog.Entry("2026-07-19"); ok {
		t.Fatal("a date prefix was joined as a feature slug")
	}
}

func TestReadBacklogMissingFileIsAbsent(t *testing.T) {
	backlog := ReadBacklog(filepath.Join(t.TempDir(), "BACKLOG.md"), time.Now())
	if backlog.State != StateAbsent {
		t.Fatalf("state = %q, want absent", backlog.State)
	}
	if len(backlog.Entries) != 0 {
		t.Fatalf("entries = %v, want none", backlog.Entries)
	}
}

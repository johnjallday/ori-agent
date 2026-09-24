package workspace

import (
	"strings"
	"testing"
)

func TestMemoryPromptReadFailsClosedForHQManagedLinesButCanonicalReadStillWorks(t *testing.T) {
	store, dir := newTestMemoryStore(t)
	const candidate = "PENDING-SENTINEL never prompt"
	const legacy = "Legacy ordinary fact remains available"
	original := "# Workspace Memory\r\n" +
		"- [fact, 2026-09-01, user] " + legacy + "\r\n" +
		"- [fact, 2026-09-02, ori-hq:item:revision] " + candidate
	writeTestMemoryFile(t, dir, original)
	raw, err := store.ReadRaw("ws1")
	if err != nil || raw != original {
		t.Fatalf("canonical editor read lost managed bytes: %q %v", raw, err)
	}
	prompt, err := store.ReadPromptRaw("ws1")
	if err != nil || strings.Contains(prompt, candidate) || !strings.Contains(prompt, legacy) {
		t.Fatalf("raw prompt filter did not exclude managed fact: %q %v", prompt, err)
	}
	doc, err := store.Read("ws1")
	if err != nil || len(doc.Entries()) != 2 {
		t.Fatalf("canonical memory index lost managed line: %+v %v", doc.Entries(), err)
	}
	if section := RenderMemoryPromptSection(doc, false); strings.Contains(section, candidate) || !strings.Contains(section, legacy) {
		t.Fatalf("structured prompt rendered unreviewed marker: %q", section)
	}
}

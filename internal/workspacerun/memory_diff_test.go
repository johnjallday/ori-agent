package workspacerun

import (
	"testing"
)

func TestDiffMemoryLines(t *testing.T) {
	before := []string{
		"- [fact, 2026-06-01, user] alpha",
		"- [watch, 2026-06-10, run:x] cursor at v1",
	}
	after := []string{
		"- [fact, 2026-06-01, user] alpha",          // unchanged
		"- [watch, 2026-06-11, run:y] cursor at v2", // edit => remove old + add new
		"- [decision, 2026-06-12, user] new call",   // pure addition
	}

	added, removed := diffMemoryLines(before, after)

	wantAdded := map[string]bool{
		"- [watch, 2026-06-11, run:y] cursor at v2": true,
		"- [decision, 2026-06-12, user] new call":   true,
	}
	if len(added) != len(wantAdded) {
		t.Fatalf("added = %v, want %d entries", added, len(wantAdded))
	}
	for _, l := range added {
		if !wantAdded[l] {
			t.Errorf("unexpected added line: %q", l)
		}
	}
	if len(removed) != 1 || removed[0] != "- [watch, 2026-06-10, run:x] cursor at v1" {
		t.Errorf("removed = %v, want the old watch cursor", removed)
	}
}

func TestDiffMemoryLines_NoChange(t *testing.T) {
	lines := []string{"- [fact, 2026-06-01, user] alpha"}
	added, removed := diffMemoryLines(lines, lines)
	if len(added) != 0 || len(removed) != 0 {
		t.Errorf("identical snapshots should produce no diff, got added=%v removed=%v", added, removed)
	}
}

func TestMemoryDiffArtifact(t *testing.T) {
	// No change => no artifact (keeps empty runs quiet).
	if a := memoryDiffArtifact("run-1", []string{"x"}, []string{"x"}); a != nil {
		t.Error("unchanged memory should produce no artifact")
	}

	a := memoryDiffArtifact("run-1", nil, []string{"- [fact, 2026-06-12, run:run-1] learned a thing"})
	if a == nil {
		t.Fatal("a change should produce an artifact")
	}
	if a.Metadata["role"] != memoryDiffRole {
		t.Errorf("artifact role = %v, want %q", a.Metadata["role"], memoryDiffRole)
	}
	added, ok := a.Metadata["added"].([]string)
	if !ok || len(added) != 1 {
		t.Errorf("artifact should record one added entry, got %v", a.Metadata["added"])
	}
}

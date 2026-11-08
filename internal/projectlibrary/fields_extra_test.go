package projectlibrary

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestFieldsExtra_ValidLimits(t *testing.T) {
	line := strings.Repeat("a", 240)
	many := func(n int) []string {
		lines := make([]string, n)
		for i := range lines {
			lines[i] = "x"
		}
		return lines
	}
	nine := map[string][]string{}
	for _, id := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"} {
		nine[id] = []string{"x"}
	}
	for name, tc := range map[string]struct {
		extra map[string][]string
		ok    bool
	}{
		"none":            {nil, true},
		"one text":        {map[string][]string{"key": {"F minor"}}, true},
		"list at limit":   {map[string][]string{"references": many(16)}, true},
		"line at limit":   {map[string][]string{"mix_notes": {line}}, true},
		"too many ids":    {nine, false},
		"too many lines":  {map[string][]string{"references": many(17)}, false},
		"line too long":   {map[string][]string{"mix_notes": {line + "a"}}, false},
		"empty line":      {map[string][]string{"key": {""}}, false},
		"no lines":        {map[string][]string{"key": {}}, false},
		"upper-case id":   {map[string][]string{"Key": {"x"}}, false},
		"control in line": {map[string][]string{"key": {"a\x00b"}}, false},
	} {
		if got := (Fields{Extra: tc.extra}).valid(); got != tc.ok {
			t.Errorf("%s: valid = %v, want %v", name, got, tc.ok)
		}
	}
}

func TestFieldsExtra_PatchReplacesClearsAndLeavesAlone(t *testing.T) {
	previous := Fields{NextAction: "Bounce", Extra: map[string][]string{"key": {"F minor"}, "old": {"kept"}}, Revision: 3}
	at := time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC)

	// Nil leaves the map alone, and the result does not share it.
	kept, err := FieldsPatch{NextAction: text("Mix")}.apply(previous, "local", at)
	if err != nil || len(kept.Extra) != 2 {
		t.Fatalf("nil extra: %+v %v", kept, err)
	}
	kept.Extra["key"][0] = "changed"
	if previous.Extra["key"][0] != "F minor" {
		t.Fatal("apply shares the previous lines")
	}

	// A map replaces the whole map.
	replaced := map[string][]string{"references": {"Song A", "Song B"}}
	got, err := FieldsPatch{Extra: &replaced}.apply(previous, "local", at)
	if err != nil || len(got.Extra) != 1 || len(got.Extra["references"]) != 2 || got.Revision != 4 {
		t.Fatalf("replace: %+v %v", got, err)
	}

	// An empty map clears it.
	empty := map[string][]string{}
	cleared, err := FieldsPatch{Extra: &empty}.apply(previous, "local", at)
	if err != nil || cleared.Extra != nil {
		t.Fatalf("clear: %+v %v", cleared, err)
	}

	// An extra-only patch counts as a patch; an invalid map is refused.
	bad := map[string][]string{"key": {""}}
	if _, err := (FieldsPatch{Extra: &bad}).apply(previous, "local", at); !errors.Is(err, ErrConflict) {
		t.Fatalf("invalid extra: %v", err)
	}
	if BulkPatchAllowed(FieldsPatch{Stage: text("mixing"), Extra: &replaced}) {
		t.Fatal("bulk edit accepts plugin fields")
	}
}

func TestFieldsExtra_DigestCoversTheMap(t *testing.T) {
	scope := Scope{OwnerUserID: "o", HomeID: "h", ProviderID: "p", ProgramID: "g"}
	one := map[string][]string{"key": {"F minor"}}
	two := map[string][]string{"key": {"G minor"}}
	first, err := fieldDigest(scope, "e", 1, FieldsPatch{Extra: &one}, "local")
	if err != nil {
		t.Fatal(err)
	}
	second, _ := fieldDigest(scope, "e", 1, FieldsPatch{Extra: &two}, "local")
	none, _ := fieldDigest(scope, "e", 1, FieldsPatch{}, "local")
	if first == second || first == none {
		t.Fatal("the field digest does not cover the plugin fields")
	}
	again, _ := fieldDigest(scope, "e", 1, FieldsPatch{Extra: &map[string][]string{"key": {"F minor"}}}, "local")
	if again != first {
		t.Fatal("the same map digests differently")
	}
}

func TestFieldsExtra_ReviewedCommitAndReusedDigest(t *testing.T) {
	r, scope, _, _, root := connectedMusicRoot(t)
	entryID := newID()
	baseScan := reviewedRootScan(t, r, scope, root, "base-for-extra")
	doc, err := r.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.library.mutate(scope, doc.Revision,
		operation{key: "seed-extra", action: "seed", digest: "fixture"},
		func(current *Document) (string, error) {
			current.Entries = append(current.Entries, Entry{ID: entryID, Revision: 1,
				Observations: []Observation{{RootID: root.ID, RelativeFolder: "Extra", FileIdentity: "fixture:extra",
					Format: "reaper", Availability: "not_scanned", ScanID: baseScan.ID, ScannedAt: time.Now().UTC()}},
				Fields: Fields{Extra: map[string][]string{"retired": {"kept on disk"}}, Revision: 1}})
			return entryID, nil
		}); err != nil {
		t.Fatal(err)
	}
	extra := map[string][]string{"key": {"F minor"}, "retired": {"kept on disk"}}
	patch := FieldsPatch{Extra: &extra}
	review, err := r.library.ReviewFields(scope, entryID, 1, patch, "local")
	if err != nil || review.After.Extra["key"][0] != "F minor" {
		t.Fatalf("review: %+v %v", review, err)
	}
	changed, _, err := r.library.CommitFields(scope, entryID, review.Token, "extra-once", 1, patch, "local")
	if err != nil || changed.Fields.Extra["key"][0] != "F minor" || changed.Fields.Extra["retired"][0] != "kept on disk" {
		t.Fatalf("commit: %+v %v", changed, err)
	}
	other := map[string][]string{"key": {"G minor"}}
	if _, _, err := r.library.CommitFields(scope, entryID, review.Token, "extra-once", 1,
		FieldsPatch{Extra: &other}, "local"); !errors.Is(err, ErrConflict) {
		t.Fatalf("a changed map reused the receipt: %v", err)
	}
	persisted, err := r.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range persisted.Entries {
		if entry.ID == entryID && entry.Fields.Extra["key"][0] != "F minor" {
			t.Fatalf("persisted: %+v", entry.Fields)
		}
	}
}

func TestFieldsExtra_DeclaredAndShown(t *testing.T) {
	words := Words{Noun: "Song", NounPlural: "Songs", RosterTitle: "Songs",
		Fields: []WordsField{{ID: "key", Label: "Key", Kind: "text"}, {ID: "references", Label: "References", Kind: "list"}}}
	if !DeclaredExtraFields(map[string][]string{"key": {"F"}, "references": {"a", "b"}}, words) {
		t.Fatal("declared fields refused")
	}
	if DeclaredExtraFields(map[string][]string{"key": {"F", "G"}}, words) {
		t.Fatal("a text field took two lines")
	}
	if DeclaredExtraFields(map[string][]string{"mood": {"dark"}}, words) {
		t.Fatal("an undeclared id was accepted")
	}
	shown := ShownExtraFields(map[string][]string{"key": {"F"}, "retired": {"x"}}, words)
	if len(shown) != 1 || shown["key"][0] != "F" {
		t.Fatalf("shown = %+v", shown)
	}
	if ShownExtraFields(map[string][]string{"retired": {"x"}}, NeutralWords()) != nil {
		t.Fatal("neutral words show plugin fields")
	}
}

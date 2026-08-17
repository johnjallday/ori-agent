package systemassistant

import "testing"

// The whole point of Issue #350: one identity. "Ask Ori" is the surviving name
// (#323 Q2=A), so the protected system assistant is canonically called that and
// nothing else.
func TestCanonicalNameIsAskOri(t *testing.T) {
	if CanonicalName != "Ask Ori" {
		t.Fatalf("CanonicalName = %q, want %q", CanonicalName, "Ask Ori")
	}
	if !IsCanonicalName(CanonicalName) {
		t.Fatalf("%q must be recognized as canonical", CanonicalName)
	}
}

// FR50: every previously-canonical name must stay covered so a user who skipped
// releases still resolves to the same assistant. "Workspace Manager" joins the
// list this feature retires; it does not replace the older entries.
func TestLegacyNamesCoverEverySupportedOlderIdentity(t *testing.T) {
	for _, want := range []string{"Workspace Manager", "Ori", "__assistant__"} {
		if !IsLegacyName(want) {
			t.Errorf("%q must be recognized as a legacy system-assistant name", want)
		}
		if !IsKnownName(want) {
			t.Errorf("%q must resolve as a known system-assistant name", want)
		}
	}
}

// "Workspace Manager" is the name this feature retires, so it must be first in
// the migration order: the newest legacy record wins over an older stale one.
func TestLegacyNamesAreOrderedNewestFirst(t *testing.T) {
	if len(LegacyNames) == 0 || LegacyNames[0] != "Workspace Manager" {
		t.Fatalf("LegacyNames = %v, want %q first", LegacyNames, "Workspace Manager")
	}
}

// The canonical name must not appear in its own legacy list, otherwise migration
// would treat an already-migrated install as needing another move (FR54).
func TestCanonicalNameIsNotAlsoALegacyName(t *testing.T) {
	if IsLegacyName(CanonicalName) {
		t.Fatalf("%q must not be listed as a legacy name", CanonicalName)
	}
}

func TestNameMatchingIgnoresCaseAndSurroundingSpace(t *testing.T) {
	for _, name := range []string{"ask ori", "  Ask Ori  ", "ASK ORI"} {
		if !IsCanonicalName(name) {
			t.Errorf("IsCanonicalName(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"workspace manager", "  Workspace Manager  "} {
		if !IsLegacyName(name) {
			t.Errorf("IsLegacyName(%q) = false, want true", name)
		}
	}
}

// FR56: an unrelated user-authored agent must never be swept up by identity
// matching just because its name contains a retired product word.
func TestUnrelatedAgentNamesAreNotSystemNames(t *testing.T) {
	for _, name := range []string{
		"",
		"   ",
		"Workspace Assistant",
		"Workspaces Assistant",
		"Task Assistant",
		"Ask Ori Helper",
		"My Workspace Manager",
		"Release Manager Codex",
	} {
		if IsKnownName(name) {
			t.Errorf("IsKnownName(%q) = true, want false", name)
		}
	}
}

// Canonicalize is the compatibility seam (FR57): a persisted legacy reference
// resolves to the canonical identity, while anything else is left alone.
func TestCanonicalizeResolvesKnownNamesAndPreservesOthers(t *testing.T) {
	for _, name := range append([]string{CanonicalName}, LegacyNames...) {
		if got := Canonicalize(name); got != CanonicalName {
			t.Errorf("Canonicalize(%q) = %q, want %q", name, got, CanonicalName)
		}
	}
	if got := Canonicalize("  Research Buddy  "); got != "Research Buddy" {
		t.Errorf("Canonicalize trimmed-user-agent = %q, want %q", got, "Research Buddy")
	}
	if got := Canonicalize(""); got != "" {
		t.Errorf("Canonicalize(%q) = %q, want empty", "", got)
	}
}

// FR49/FR55: name alone cannot distinguish the protected record from a
// user-created agent that happens to be called "Ask Ori". A durable marker on
// the stored record is what positively identifies the protected assistant.
func TestProtectedMarkerIdentifiesTheStoredSystemRecord(t *testing.T) {
	if HasProtectedMarker(nil) {
		t.Error("a record with no tags must not read as the protected assistant")
	}
	if HasProtectedMarker([]string{"system", "orchestrator"}) {
		t.Error("the ordinary descriptive tags must not be mistaken for the protected marker")
	}

	marked := EnsureProtectedMarker([]string{"system"})
	if !HasProtectedMarker(marked) {
		t.Fatalf("EnsureProtectedMarker(%v) = %v, which is not marked", []string{"system"}, marked)
	}
	if len(marked) != 2 {
		t.Errorf("EnsureProtectedMarker must append, got %v", marked)
	}
}

func TestEnsureProtectedMarkerIsIdempotent(t *testing.T) {
	once := EnsureProtectedMarker(nil)
	twice := EnsureProtectedMarker(once)
	if len(once) != len(twice) {
		t.Fatalf("marker applied twice grew the tag list: %v -> %v", once, twice)
	}
}

// The marker is namespaced so it cannot collide with an ordinary user tag.
func TestProtectedMarkerIsNamespaced(t *testing.T) {
	if ProtectedMarker == "system" || ProtectedMarker == "assistant" {
		t.Fatalf("ProtectedMarker = %q is too generic to be collision-safe", ProtectedMarker)
	}
}

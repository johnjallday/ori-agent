package specialist

import (
	"testing"
	"time"
)

func TestMatchIsCaseInsensitiveAndVersionTolerant(t *testing.T) {
	for _, name := range []string{
		"REAPER", "reaper", "Reaper", "REAPER64", "Reaper 7", "REAPER.app",
		"reaper64.app", "Reaper x64", "REAPER 7 (x64)",
	} {
		entry, ok := Match([]App{{Name: name, LastUsed: time.Now()}})
		if !ok {
			t.Fatalf("expected %q to match a specialist", name)
		}
		if entry.Slug != "music_production" {
			t.Fatalf("expected %q to map to music_production, got %q", name, entry.Slug)
		}
	}
}

func TestMatchRejectsUnrelatedApps(t *testing.T) {
	for _, name := range []string{
		"Safari", "Visual Studio Code", "Grim Reaper", "Reaperbot",
		"Soul Reaper Deluxe", "", "   ", "Logic Pro", "Ableton Live 12",
	} {
		if entry, ok := Match([]App{{Name: name, LastUsed: time.Now()}}); ok {
			t.Fatalf("expected %q not to match, got %q", name, entry.Slug)
		}
	}
}

func TestMatchReturnsAtMostOneSpecialistMostRecentlyUsed(t *testing.T) {
	older := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)

	entry, ok := Match([]App{
		{Name: "Safari", LastUsed: newer.Add(time.Hour)},
		{Name: "REAPER", LastUsed: older},
		{Name: "Reaper 7", LastUsed: newer},
	})
	if !ok {
		t.Fatal("expected a specialist match")
	}
	// Both REAPER entries collapse to the same specialist; the point of the
	// tie-break is that exactly one offer is produced regardless.
	if entry.Slug != "music_production" {
		t.Fatalf("unexpected slug %q", entry.Slug)
	}
}

func TestMatchIsDeterministicOnEqualTimestamps(t *testing.T) {
	same := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)
	apps := []App{{Name: "Reaper 7", LastUsed: same}, {Name: "REAPER", LastUsed: same}}

	first, ok := Match(apps)
	if !ok {
		t.Fatal("expected a match")
	}
	second, ok := Match([]App{apps[1], apps[0]})
	if !ok {
		t.Fatal("expected a match on the reordered input")
	}
	if first.Slug != second.Slug {
		t.Fatalf("match is not deterministic: %q vs %q", first.Slug, second.Slug)
	}
}

func TestMatchOnEmptyInput(t *testing.T) {
	if _, ok := Match(nil); ok {
		t.Fatal("expected no specialist for a nil app list")
	}
	if _, ok := Match([]App{}); ok {
		t.Fatal("expected no specialist for an empty app list")
	}
}

func TestGetRejectsUnknownSlugs(t *testing.T) {
	if _, ok := Get(""); ok {
		t.Fatal("empty slug must not resolve")
	}
	if _, ok := Get("not_a_domain"); ok {
		t.Fatal("unknown slug must not resolve")
	}
	entry, ok := Get("music_production")
	if !ok {
		t.Fatal("music_production must resolve")
	}
	if entry.SuggestedTemplateID != "reaper-song" {
		t.Fatalf("unexpected suggested template %q", entry.SuggestedTemplateID)
	}
}

// v1 ships exactly one entry (PRD FR 7). A second domain is a deliberate
// decision, not something that arrives by accident with a copy edit.
func TestRegistryShipsExactlyOneEntry(t *testing.T) {
	if got := len(All()); got != 1 {
		t.Fatalf("expected exactly one mapping entry in v1, got %d", got)
	}
}

func TestEntryShapeIsComplete(t *testing.T) {
	entry, ok := Get("music_production")
	if !ok {
		t.Fatal("music_production must resolve")
	}
	if entry.DisplayName == "" || entry.SpecialistName == "" {
		t.Fatal("display and specialist names are required")
	}
	if entry.OfferCopy.Headline == "" || entry.OfferCopy.Question == "" {
		t.Fatal("offer copy is required")
	}
	if entry.OfferCopy.AcceptLabel == "" || entry.OfferCopy.DeclineLabel == "" {
		t.Fatal("offer needs both an accept and a one-click decline")
	}
	if entry.OfferCopy.AcceptedNote == "" || entry.OfferCopy.ManualLabel == "" {
		t.Fatal("offer needs an accepted note and a manual path label")
	}
	if len(entry.FocusAreas) == 0 || len(entry.FocusAreas) > 6 {
		t.Fatalf("focus areas must be 1..6, got %d", len(entry.FocusAreas))
	}
	types := map[string]bool{}
	for _, label := range entry.AssignmentLabels {
		if label.Label == "" {
			t.Fatalf("assignment label for %q is empty", label.Type)
		}
		types[label.Type] = true
	}
	for _, want := range []string{ItemPriority, ItemIOwe, ItemWaitingOn, ItemFixedCommitment} {
		if !types[want] {
			t.Fatalf("missing assignment label for %q", want)
		}
	}
	if len(entry.AssignmentSteps) != 3 {
		t.Fatalf("expected 3 assignment steps, got %d", len(entry.AssignmentSteps))
	}
	if len(entry.CapabilityOrder) == 0 {
		t.Fatal("capability order is required")
	}
	if entry.Suggestion.ActionRoute == "" || entry.Suggestion.Title == "" {
		t.Fatal("post-hire suggestion is required")
	}
}

func TestMatchesTemplateAcceptsPluginNamespacedIDs(t *testing.T) {
	entry, _ := Get("music_production")
	for _, id := range []string{"reaper-song", "plugin:reaper-plugin:reaper-song"} {
		if !entry.MatchesTemplate(id) {
			t.Fatalf("expected %q to match the suggested template", id)
		}
	}
	for _, id := range []string{"", "calendar-ops", "reaper-song-extra", "plugin:x:other"} {
		if entry.MatchesTemplate(id) {
			t.Fatalf("expected %q not to match the suggested template", id)
		}
	}
}

func TestAllReturnsACopy(t *testing.T) {
	entries := All()
	if len(entries) == 0 {
		t.Fatal("expected at least one entry")
	}
	entries[0].Slug = "mutated"
	if fresh := All(); fresh[0].Slug == "mutated" {
		t.Fatal("All must not expose the built-in registry for mutation")
	}
}

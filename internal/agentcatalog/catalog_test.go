package agentcatalog

import (
	"testing"

	"github.com/johnjallday/ori-agent/internal/types"
)

// sparkStageSlotCap mirrors the spark-stage slot cap from PRD FR10 (2 active
// skill slots). Starter skill lists must never exceed it so no catalog agent
// is born already over-cap.
const sparkStageSlotCap = 2

func TestRegistryHasExactlySixEntries(t *testing.T) {
	entries := Registry()
	if len(entries) != 6 {
		t.Fatalf("expected exactly 6 catalog entries, got %d", len(entries))
	}
}

func TestRegistryEntriesHaveUniqueValidSlugs(t *testing.T) {
	forbidden := map[types.AgentRole]bool{
		types.RoleGeneral:  true,
		types.RoleCLIAgent: true,
	}
	seen := make(map[types.AgentRole]bool)

	for _, e := range Registry() {
		if e.Slug == "" {
			t.Fatalf("entry %q has empty slug", e.DisplayName)
		}
		if forbidden[e.Slug] {
			t.Fatalf("entry %q uses forbidden slug %q", e.DisplayName, e.Slug)
		}
		if seen[e.Slug] {
			t.Fatalf("duplicate slug %q", e.Slug)
		}
		seen[e.Slug] = true
	}
}

func TestRegistryEntriesStarterSkillsWithinSparkCap(t *testing.T) {
	for _, e := range Registry() {
		if len(e.StarterSkills) > sparkStageSlotCap {
			t.Errorf("entry %q has %d starter skills, exceeds spark-stage cap of %d",
				e.DisplayName, len(e.StarterSkills), sparkStageSlotCap)
		}
	}
}

func TestRegistryEntriesHaveNonEmptyDisplayFields(t *testing.T) {
	for _, e := range Registry() {
		if e.DisplayName == "" {
			t.Errorf("entry with slug %q has empty display name", e.Slug)
		}
		if e.Emblem == "" {
			t.Errorf("entry %q has empty emblem", e.DisplayName)
		}
		if e.AccentColor == "" {
			t.Errorf("entry %q has empty accent color", e.DisplayName)
		}
		if e.Tagline == "" {
			t.Errorf("entry %q has empty tagline", e.DisplayName)
		}
		if e.Description == "" {
			t.Errorf("entry %q has empty description", e.DisplayName)
		}
		if e.StarterPrompt == "" {
			t.Errorf("entry %q has empty starter prompt", e.DisplayName)
		}
		if e.ModelTier == "" {
			t.Errorf("entry %q has empty model tier", e.DisplayName)
		}
	}
}

func TestRegistryReturnsIndependentCopy(t *testing.T) {
	first := Registry()
	first[0].DisplayName = "Mutated"

	second := Registry()
	if second[0].DisplayName == "Mutated" {
		t.Fatal("Registry() returned a slice sharing backing storage with package state")
	}
}

func TestFindReturnsEntryForKnownSlug(t *testing.T) {
	entry, ok := Find(types.RoleOrchestrator)
	if !ok {
		t.Fatal("expected to find orchestrator entry")
	}
	if entry.DisplayName != "Commander" {
		t.Fatalf("expected DisplayName 'Commander', got %q", entry.DisplayName)
	}
}

func TestFindReturnsFalseForUnknownSlug(t *testing.T) {
	if _, ok := Find(types.RoleGeneral); ok {
		t.Fatal("expected RoleGeneral to not be in the catalog")
	}
	if _, ok := Find(types.AgentRole("nonexistent")); ok {
		t.Fatal("expected unknown slug to not be found")
	}
}

func TestOnlySpecialistSupportsDomain(t *testing.T) {
	for _, e := range Registry() {
		expected := e.Slug == types.RoleSpecialist
		if e.SupportsDomain != expected {
			t.Errorf("entry %q: SupportsDomain=%v, expected %v", e.DisplayName, e.SupportsDomain, expected)
		}
	}
}

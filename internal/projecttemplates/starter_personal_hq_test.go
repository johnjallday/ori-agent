package projecttemplates_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/projecttemplates"
)

// TestPersonalOpsStarterEvolvedToPersonalHQ pins the Personal Ops -> Personal
// HQ manifest evolution (PRD FR119-FR122, FR128): the internal template ID
// and folder name are frozen for compatibility, but the display metadata,
// roster, and starter tasks now describe the Personal HQ blueprint.
func TestPersonalOpsStarterEvolvedToPersonalHQ(t *testing.T) {
	libDir := filepath.Join(t.TempDir(), "templates")
	if err := projecttemplates.EnsureLibrary(libDir); err != nil {
		t.Fatalf("EnsureLibrary: %v", err)
	}

	// The internal ID/folder name must never change — existing workspaces'
	// stored template provenance and any hardcoded references key off this.
	tpl, err := projecttemplates.FindLibraryTemplate(libDir, "personal-ops")
	if err != nil {
		t.Fatalf("FindLibraryTemplate(personal-ops): %v", err)
	}
	if tpl.ID != "personal-ops" {
		t.Fatalf("template ID must stay personal-ops, got %q", tpl.ID)
	}
	if !tpl.Builtin {
		t.Fatal("personal-ops must remain a built-in template")
	}
	if tpl.BuiltinVersion < 3 {
		t.Fatalf("personal-ops builtin_version = %d, want at least 3", tpl.BuiltinVersion)
	}

	// Display metadata now presents this as Personal HQ.
	if tpl.Name != "Personal HQ" {
		t.Fatalf("display name = %q, want %q", tpl.Name, "Personal HQ")
	}
	if !strings.Contains(strings.ToLower(tpl.Description), "personal command center") {
		t.Fatalf("description must describe a personal command center, got %q", tpl.Description)
	}

	// Roster: the Personal Chief of Staff must be present and, being the
	// only declared agent, is first (the entry agent).
	if len(tpl.Agents) != 1 || tpl.Agents[0].Name != "Personal Chief of Staff" {
		t.Fatalf("expected Personal Chief of Staff as the sole/entry agent, got %+v", tpl.Agents)
	}
	prompt := tpl.Agents[0].SystemPrompt
	for _, want := range []string{"prioriti", "brief", "follow-up", "route the user"} {
		if !strings.Contains(strings.ToLower(prompt), want) {
			t.Errorf("Chief of Staff prompt missing scope keyword %q: %s", want, prompt)
		}
	}
	if !strings.Contains(strings.ToLower(prompt), "not the specialist work itself") &&
		!strings.Contains(strings.ToLower(prompt), "not take it on yourself") &&
		!strings.Contains(strings.ToLower(prompt), "route the user to the right project workspace") {
		t.Errorf("Chief of Staff prompt must not claim ownership of specialist project work: %s", prompt)
	}

	// Starter tasks: a small set, at most one (here exactly one) marked
	// setup:true, and it must be first.
	if len(tpl.StarterTasks) != 3 {
		t.Fatalf("expected 3 starter tasks (1 setup + 2 recurring), got %d: %+v", len(tpl.StarterTasks), tpl.StarterTasks)
	}
	setupCount := 0
	for i, task := range tpl.StarterTasks {
		if task.Setup {
			setupCount++
			if i != 0 {
				t.Errorf("setup task must be first in the list, found at index %d", i)
			}
		}
	}
	if setupCount != 1 {
		t.Fatalf("expected exactly one setup:true starter task, got %d", setupCount)
	}
	if tpl.StarterTasks[1].Description != "Morning briefing" || tpl.StarterTasks[2].Description != "End-of-day journal" {
		t.Fatalf("expected the pre-existing Morning briefing / End-of-day journal tasks preserved, got %+v", tpl.StarterTasks)
	}

	if len(tpl.Warnings) != 0 {
		t.Fatalf("personal-ops should load without warnings, got %v", tpl.Warnings)
	}
}

// TestDailyBriefingsStarterEvolvedToCustomDigest pins the Daily Briefings ->
// Custom Digest manifest evolution (PRD FR123-FR126): frozen internal ID,
// renamed display metadata, and positioning for externally sourced digests
// so it does not duplicate the Personal HQ Daily Brief.
func TestDailyBriefingsStarterEvolvedToCustomDigest(t *testing.T) {
	libDir := filepath.Join(t.TempDir(), "templates")
	if err := projecttemplates.EnsureLibrary(libDir); err != nil {
		t.Fatalf("EnsureLibrary: %v", err)
	}

	tpl, err := projecttemplates.FindLibraryTemplate(libDir, "daily-briefings")
	if err != nil {
		t.Fatalf("FindLibraryTemplate(daily-briefings): %v", err)
	}
	if tpl.ID != "daily-briefings" {
		t.Fatalf("template ID must stay daily-briefings, got %q", tpl.ID)
	}
	if !tpl.Builtin {
		t.Fatal("daily-briefings must remain a built-in template")
	}
	if tpl.BuiltinVersion < 3 {
		t.Fatalf("daily-briefings builtin_version = %d, want at least 3", tpl.BuiltinVersion)
	}

	if tpl.Name != "Custom Digest" {
		t.Fatalf("display name = %q, want %q", tpl.Name, "Custom Digest")
	}
	lowerDesc := strings.ToLower(tpl.Description)
	for _, want := range []string{"externally sourced", "news", "market", "research"} {
		if !strings.Contains(lowerDesc, want) {
			t.Errorf("description missing external-digest positioning keyword %q: %s", want, tpl.Description)
		}
	}
	// Must not read as the app-wide Personal HQ Daily Brief.
	if strings.Contains(lowerDesc, "personal command center") {
		t.Errorf("Custom Digest description must not read as the Personal HQ command center: %s", tpl.Description)
	}

	if len(tpl.Agents) != 1 || tpl.Agents[0].Name != "Briefing Editor" {
		t.Fatalf("expected Briefing Editor as the sole/entry agent, got %+v", tpl.Agents)
	}
	prompt := strings.ToLower(tpl.Agents[0].SystemPrompt)
	if !strings.Contains(prompt, "externally sourced") {
		t.Errorf("Briefing Editor prompt should clarify externally sourced feeds: %s", prompt)
	}
	if !strings.Contains(prompt, "not the app's personal hq daily brief") {
		t.Errorf("Briefing Editor prompt should distinguish itself from the Personal HQ Daily Brief: %s", prompt)
	}

	if len(tpl.Warnings) != 0 {
		t.Fatalf("daily-briefings should load without warnings, got %v", tpl.Warnings)
	}
}

// TestPersonalHQAndCustomDigestIDsFrozen guards against ever renaming the
// shipped folder/internal IDs as part of a future copy pass (PRD FR120,
// FR125): user-visible copy must change without touching these.
func TestPersonalHQAndCustomDigestIDsFrozen(t *testing.T) {
	libDir := filepath.Join(t.TempDir(), "templates")
	if err := projecttemplates.EnsureLibrary(libDir); err != nil {
		t.Fatalf("EnsureLibrary: %v", err)
	}
	templates, err := projecttemplates.ListLibrary(libDir)
	if err != nil {
		t.Fatalf("ListLibrary: %v", err)
	}
	ids := make(map[string]string, len(templates))
	for _, tpl := range templates {
		ids[tpl.ID] = tpl.Name
	}
	if name, ok := ids["personal-ops"]; !ok || name != "Personal HQ" {
		t.Fatalf("expected personal-ops -> Personal HQ, got %q (present=%v)", name, ok)
	}
	if name, ok := ids["daily-briefings"]; !ok || name != "Custom Digest" {
		t.Fatalf("expected daily-briefings -> Custom Digest, got %q (present=%v)", name, ok)
	}
}

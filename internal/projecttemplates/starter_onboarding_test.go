package projecttemplates_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/projecttemplates"
)

// TestReaperStarterSetupTask pins the converted reaper-song manifest: the
// intake-era onboarding block is gone, replaced by an agent roster and a
// single setup starter task ("defaults first, adjust conversationally").
func TestReaperStarterSetupTask(t *testing.T) {
	libDir := filepath.Join(t.TempDir(), "templates")
	if err := projecttemplates.EnsureLibrary(libDir); err != nil {
		t.Fatalf("EnsureLibrary: %v", err)
	}
	tpl, err := projecttemplates.FindLibraryTemplate(libDir, "reaper-song")
	if err != nil {
		t.Fatalf("FindLibraryTemplate: %v", err)
	}

	if tpl.HasOnboarding() {
		t.Fatal("reaper-song must not carry a legacy onboarding block")
	}
	if len(tpl.Warnings) != 0 {
		t.Fatalf("reaper-song should load without warnings, got %v", tpl.Warnings)
	}
	if tpl.BuiltinVersion < 3 {
		t.Fatalf("reaper-song builtin_version = %d, want at least 3", tpl.BuiltinVersion)
	}
	if tpl.ProjectEntry == nil || tpl.ProjectEntry.RelativePath != "{{name}}.rpp" || !tpl.ProjectEntry.OpenAfterCreateDefault {
		t.Fatalf("reaper-song project entry = %#v", tpl.ProjectEntry)
	}

	// Roster: exactly one producer entry agent with the REAPER skill bound.
	if len(tpl.Agents) != 1 || tpl.Agents[0].Name != "Reaper Producer" {
		t.Fatalf("unexpected roster: %+v", tpl.Agents)
	}
	if skills := tpl.Agents[0].Tools.Skills; len(skills) != 1 || skills[0] != "reaper-session-setup" {
		t.Fatalf("entry agent skills = %#v, want reaper-session-setup", tpl.Agents[0].Tools.Skills)
	}

	// Starter tasks: the setup help task first, then the session adjustment as
	// normal work.
	//
	// These used to be one task, and splitting them is the point (FR-114/115).
	// The wizard completes its setup task automatically when setup passes — so
	// whatever that task claims to cover had better be what setup actually did.
	// Adjusting tempo, key, and arrangement is work someone does with the user;
	// reporting it as done because a plugin got attached would be a lie the
	// wizard tells on the agent's behalf.
	if len(tpl.StarterTasks) != 3 {
		t.Fatalf("expected 3 starter tasks (setup help, adjust, arrange), got %+v", tpl.StarterTasks)
	}
	setup := tpl.StarterTasks[0]
	if !setup.Setup {
		t.Fatalf("the first task must be the setup help task: %+v", tpl.StarterTasks[0])
	}
	for i, task := range tpl.StarterTasks[1:] {
		if task.Setup {
			t.Fatalf("task %d must not be a setup task: %+v", i+1, task)
		}
	}

	// The setup help task explains the choice and is honest about its ceiling.
	for _, want := range []string{"file only", "Ori-assisted", "not that REAPER is running"} {
		if !strings.Contains(setup.Details, want) {
			t.Errorf("setup help task missing %q: %s", want, setup.Details)
		}
	}
	if !strings.Contains(setup.Details, "do not claim setup is complete") {
		t.Errorf("setup help task must not claim completion it did not perform: %s", setup.Details)
	}
	for _, forbidden := range []string{"Tempo (BPM)", "Musical key"} {
		if strings.Contains(setup.Details, forbidden) {
			t.Errorf("session work must not live in the auto-completed setup task: %q", forbidden)
		}
	}

	// The adjust task keeps every bit of the truthful project-control guidance.
	adjust := tpl.StarterTasks[1]
	for _, heading := range []string{"## Created defaults", "## Questions to ask", "## Validation", "## How to apply changes"} {
		if !strings.Contains(adjust.Details, heading) {
			t.Errorf("adjust details missing %q heading", heading)
		}
	}
	for _, want := range []string{"120 BPM", "40 and 240", "reaper-session-setup"} {
		if !strings.Contains(adjust.Details, want) {
			t.Errorf("adjust details missing %q", want)
		}
	}
	for _, want := range []string{
		"one authoritative REAPER session file",
		"never create or initialize a second .rpp file",
		"does not prove that REAPER has finished opening it or that Web Remote is ready",
		"Do not claim that live changes were applied",
		"ask for the user's explicit confirmation",
		"file change rather than a verified live-session change",
	} {
		if !strings.Contains(adjust.Details, want) {
			t.Errorf("adjust details missing truthful project-control guidance %q", want)
		}
	}
	if !strings.Contains(adjust.Details, "never completed automatically") {
		t.Errorf("the adjust task must say it is not completed by setup: %s", adjust.Details)
	}
	if strings.Contains(adjust.Details, "Otherwise edit the .rpp file directly") {
		t.Error("adjust task must not silently fall back to direct .rpp edits")
	}

	prompt := tpl.Agents[0].SystemPrompt
	for _, want := range []string{
		"only authoritative song project",
		"never create or initialize a second .rpp file",
		"does not prove that REAPER or Web Remote is ready",
		"never claim live changes were applied",
		"obtain the user's explicit confirmation",
		"distinguish file changes from verified live-session changes",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("Reaper Producer prompt missing %q", want)
		}
	}

	// The skeleton still ships the .rpp at the documented defaults.
	if !tpl.HasSkeleton {
		t.Fatal("reaper-song must keep its skeleton")
	}
}

// TestReaperStarterSetupWizard pins the manifest's wizard declaration: the
// version 1 mode choice, the two checks behind it, and the plugin the assisted
// mode needs. The declaration is inert data — what it must get right is naming
// a registered adapter and a plugin the blueprint actually declares, because a
// typo in either is a workspace nobody can finish setting up.
func TestReaperStarterSetupWizard(t *testing.T) {
	libDir := filepath.Join(t.TempDir(), "templates")
	if err := projecttemplates.EnsureLibrary(libDir); err != nil {
		t.Fatalf("EnsureLibrary: %v", err)
	}
	tpl, err := projecttemplates.FindLibraryTemplate(libDir, "reaper-song")
	if err != nil {
		t.Fatalf("FindLibraryTemplate: %v", err)
	}

	if !tpl.HasSetupWizard() {
		t.Fatal("reaper-song must declare a setup wizard")
	}
	if tpl.HasInvalidSetupWizard() {
		t.Fatalf("reaper-song wizard failed to parse: %v", tpl.SetupWizardError)
	}
	if tpl.BuiltinVersion < 7 {
		t.Fatalf("builtin_version = %d, want at least 7 so existing workspaces refresh", tpl.BuiltinVersion)
	}
	wizard := tpl.SetupWizard
	if wizard.Version != 1 {
		t.Fatalf("wizard version = %d, want 1", wizard.Version)
	}
	if len(wizard.Steps) != 3 {
		t.Fatalf("expected mode, readiness, and summary steps, got %+v", wizard.Steps)
	}

	mode := wizard.Steps[0]
	if mode.Kind != "plugin_readiness" || !mode.Required {
		t.Fatalf("the mode step must be the required plugin-readiness step: %+v", mode)
	}
	if mode.Adapter != "reaper_song" {
		t.Fatalf("mode adapter = %q, want the registered reaper_song adapter", mode.Adapter)
	}
	if mode.RequirementKey != "reaper-plugin" {
		t.Fatalf("mode requirement_key = %q, want the plugin the manifest declares", mode.RequirementKey)
	}
	// The disclosure is where a user learns what the assisted path will ask of
	// them, before they pick it.
	for _, want := range []string{"no plugin", "nothing is installed or enabled without you choosing it"} {
		if !strings.Contains(mode.Disclosure, want) {
			t.Errorf("mode disclosure missing %q: %s", want, mode.Disclosure)
		}
	}

	for i, step := range wizard.Steps {
		if step.Adapter != "reaper_song" {
			t.Errorf("step %d (%s) adapter = %q", i, step.ID, step.Adapter)
		}
		if !step.Required {
			t.Errorf("step %d (%s) must be required: setup is not finished with it outstanding", i, step.ID)
		}
	}
	// The plugin the assisted mode needs is one the blueprint declares, so its
	// install source resolves without the user hunting for it.
	if source := tpl.Tools.PluginSources["reaper-plugin"]; source == "" {
		t.Fatal("reaper-plugin must keep a declared install source")
	}
}

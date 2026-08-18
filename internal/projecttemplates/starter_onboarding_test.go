package projecttemplates_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/projecttemplates"
)

// TestReaperStarterRuntimeTasks pins the activated reaper-song manifest: setup
// is deterministic runtime state, not model work, while the two real song tasks
// remain available for file-only and assisted work.
func TestReaperStarterRuntimeTasks(t *testing.T) {
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
	if tpl.BuiltinVersion != 8 {
		t.Fatalf("reaper-song builtin_version = %d, want 8 for runtime-contract activation", tpl.BuiltinVersion)
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

	// Setup is represented by runtime mode/readiness steps, never by a starter
	// task that spends a model call or gets auto-completed. The only retained
	// starters are real user work and neither is marked as setup.
	if len(tpl.StarterTasks) != 2 {
		t.Fatalf("expected exactly 2 real starter tasks (adjust, arrange), got %+v", tpl.StarterTasks)
	}
	for i, task := range tpl.StarterTasks {
		if task.Setup {
			t.Fatalf("task %d must not be a setup-help task: %+v", i, task)
		}
		if strings.EqualFold(strings.TrimSpace(task.Description), "Help with the REAPER setup choices") {
			t.Fatalf("legacy setup-help task must not be seeded: %+v", task)
		}
	}
	if tpl.StarterTasks[0].Description != "Adjust the new REAPER session to the user's preferences" {
		t.Fatalf("first real starter task changed: %+v", tpl.StarterTasks[0])
	}
	if tpl.StarterTasks[1].Description != "Sketch the song's arrangement" {
		t.Fatalf("second real starter task changed: %+v", tpl.StarterTasks[1])
	}

	// The adjust task keeps every bit of the truthful project-control guidance.
	adjust := tpl.StarterTasks[0]
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

	arrange := tpl.StarterTasks[1]
	for _, want := range []string{"plan", "live", "file"} {
		if !strings.Contains(strings.ToLower(arrange.Details), want) {
			t.Errorf("arrangement task must distinguish offline planning from applying %s changes: %s", want, arrange.Details)
		}
	}

	for _, copy := range []string{tpl.Description, tpl.Tagline} {
		if strings.Contains(strings.ToLower(copy), "ready to adjust in chat") {
			t.Errorf("blueprint copy makes an unverified live-control promise: %q", copy)
		}
	}
	for _, want := range []string{"project-file", "verified live control"} {
		if !strings.Contains(strings.ToLower(tpl.Description+" "+tpl.Tagline), want) {
			t.Errorf("blueprint copy must distinguish immediate file work from assisted control; missing %q", want)
		}
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

// TestReaperStarterRuntimeContract pins the first built-in activation of the
// generalized contract: exactly two modes, one compiled runtime requirement,
// and runtime-aware wizard steps that all resolve from the workspace snapshot.
func TestReaperStarterRuntimeContract(t *testing.T) {
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
	if tpl.BuiltinVersion != 8 {
		t.Fatalf("builtin_version = %d, want 8 so installed libraries receive the runtime contract", tpl.BuiltinVersion)
	}
	contract := tpl.RuntimeRequirements
	if contract == nil || !contract.StructurallyValid() {
		t.Fatalf("reaper-song must declare a valid runtime contract: %#v (%s)", contract, tpl.RuntimeRequirementsError)
	}
	if contract.SchemaVersion != projecttemplates.RuntimeRequirementsSchemaVersion {
		t.Fatalf("runtime schema_version = %d", contract.SchemaVersion)
	}
	if len(contract.OperatingModes) != 2 {
		t.Fatalf("operating modes = %+v, want exactly File-only and Ori-assisted", contract.OperatingModes)
	}
	fileOnly, ok := contract.Mode("file_only")
	if !ok || fileOnly.Label != "File-only" || len(fileOnly.Requires) != 0 {
		t.Fatalf("file-only mode = %+v, ok=%v", fileOnly, ok)
	}
	assisted, ok := contract.Mode("ori_assisted")
	if !ok || assisted.Label != "Ori-assisted REAPER" || len(assisted.Requires) != 1 || assisted.Requires[0] != "reaper_live_control" {
		t.Fatalf("assisted mode = %+v, ok=%v", assisted, ok)
	}
	if len(contract.Requirements) != 1 {
		t.Fatalf("runtime requirements = %+v, want exactly reaper_live_control", contract.Requirements)
	}
	requirement, ok := contract.Requirement("reaper_live_control")
	if !ok || requirement.Adapter != "reaper_live_control" {
		t.Fatalf("reaper_live_control requirement = %+v, ok=%v", requirement, ok)
	}
	for _, want := range []string{"loopback", "runner", "selected agent", "without ori's per-call confirmation"} {
		if !strings.Contains(strings.ToLower(requirement.Disclosure), want) {
			t.Errorf("REAPER access disclosure missing %q: %s", want, requirement.Disclosure)
		}
	}

	wizard := tpl.SetupWizard
	if wizard.Version != 1 {
		t.Fatalf("wizard version = %d, want 1", wizard.Version)
	}
	if len(wizard.Steps) != 3 {
		t.Fatalf("expected runtime mode, runtime readiness, and summary steps, got %+v", wizard.Steps)
	}
	wantKinds := []string{"runtime_mode", "runtime_readiness", "summary"}
	for i, step := range wizard.Steps {
		if step.Kind != wantKinds[i] {
			t.Errorf("step %d kind = %q, want %q", i, step.Kind, wantKinds[i])
		}
		if step.Adapter != "" {
			t.Errorf("runtime-aware step %d (%s) must resolve through the runtime contract, got legacy adapter %q", i, step.ID, step.Adapter)
		}
		if !step.Required {
			t.Errorf("step %d (%s) must be required: setup is not finished with it outstanding", i, step.ID)
		}
	}
	if wizard.Steps[1].RequirementKey != "reaper_live_control" {
		t.Fatalf("runtime readiness requirement_key = %q", wizard.Steps[1].RequirementKey)
	}
	if wizard.Steps[0].RequirementKey != "" || wizard.Steps[2].RequirementKey != "" {
		t.Fatalf("only runtime readiness may name a requirement: %+v", wizard.Steps)
	}

	// The plugin the assisted mode needs remains declared for the runtime
	// adapter's explicit install/attach repair actions.
	if source := tpl.Tools.PluginSources["reaper-plugin"]; source == "" {
		t.Fatal("reaper-plugin must keep a declared install source")
	}
}

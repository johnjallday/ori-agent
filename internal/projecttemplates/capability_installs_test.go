package projecttemplates

import (
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// TestNormalizeCapabilityInstalls_AcceptsOnlyCompiledCapabilities is the FR-14
// boundary at the authoring layer: a manifest may SELECT a built-in capability,
// never introduce one. Anything the compiled registry does not know is dropped.
func TestNormalizeCapabilityInstalls_AcceptsOnlyCompiledCapabilities(t *testing.T) {
	installs, warnings := normalizeCapabilityInstalls([]CapabilityInstall{
		{ID: "file-janitor", Source: "downloads-janitor-preset"},
		{ID: "totally-made-up"},
	})

	if len(installs) != 1 {
		t.Fatalf("expected only the compiled capability to survive, got %+v", installs)
	}
	if installs[0].ID != workspace.CapabilityFileJanitor {
		t.Fatalf("unexpected capability: %q", installs[0].ID)
	}
	if installs[0].Source != "downloads-janitor-preset" {
		t.Fatalf("source not preserved: %q", installs[0].Source)
	}

	// Dropped, but not silently: a blueprint whose whole purpose is to create a
	// File Janitor workspace, that quietly creates an ordinary one because of a
	// typo, is worse than a visible complaint.
	if len(warnings) != 1 || !strings.Contains(warnings[0], "totally-made-up") {
		t.Fatalf("expected a warning naming the unknown capability, got %v", warnings)
	}
}

func TestNormalizeCapabilityInstalls_NormalizesAndDeduplicates(t *testing.T) {
	installs, _ := normalizeCapabilityInstalls([]CapabilityInstall{
		{ID: "  FILE-JANITOR  ", Source: "  Blueprint  "},
		{ID: "file-janitor", Source: "in-place"},
	})

	if len(installs) != 1 {
		t.Fatalf("expected one record per capability, got %+v", installs)
	}
	if installs[0].ID != "file-janitor" {
		t.Fatalf("id not normalized: %q", installs[0].ID)
	}
	if installs[0].Source != "blueprint" {
		t.Fatalf("source not normalized, or first-seen did not win: %q", installs[0].Source)
	}
}

func TestNormalizeCapabilityInstalls_HandlesAbsentAndBlank(t *testing.T) {
	if installs, warnings := normalizeCapabilityInstalls(nil); installs != nil || warnings != nil {
		t.Fatalf("no declaration should yield nothing: %+v %v", installs, warnings)
	}
	installs, warnings := normalizeCapabilityInstalls([]CapabilityInstall{{ID: "   "}})
	if installs != nil {
		t.Fatalf("a blank id should be dropped: %+v", installs)
	}
	if len(warnings) != 1 {
		t.Fatalf("expected a warning for the blank id, got %v", warnings)
	}
}

// TestDownloadsPreset_DeclaresTheCapabilityExplicitly is FR-135: the preset
// states which capability it installs, rather than the install being inferred
// from the template ID. Inference is exactly what the capability model replaces.
func TestDownloadsPreset_DeclaresTheCapabilityExplicitly(t *testing.T) {
	libDir := t.TempDir()
	if err := EnsureLibrary(libDir); err != nil {
		t.Fatalf("EnsureLibrary: %v", err)
	}
	tpl, err := FindLibraryTemplate(libDir, "downloads-janitor")
	if err != nil {
		t.Fatalf("FindLibraryTemplate: %v", err)
	}

	if len(tpl.Capabilities) != 1 {
		t.Fatalf("expected exactly one declared capability, got %+v", tpl.Capabilities)
	}
	install := tpl.Capabilities[0]
	if install.ID != workspace.CapabilityFileJanitor {
		t.Fatalf("capability = %q, want file-janitor", install.ID)
	}
	if install.Source == "" {
		t.Fatal("the preset should record which flow installed the capability")
	}

	// The declaration must not have been mistaken for, or merged with, the
	// unrelated connector-capability requirements (FR-7, FR-31).
	for _, req := range tpl.CapabilityRequirements {
		if req.Key == workspace.CapabilityFileJanitor {
			t.Fatal("the built-in capability leaked into connector capability requirements")
		}
	}

	if tpl.Warnings != nil {
		for _, warning := range tpl.Warnings {
			if strings.Contains(warning, "capabilit") {
				t.Fatalf("the shipped preset produced a capability warning: %q", warning)
			}
		}
	}
}

// TestDownloadsPreset_StillOnlySuggestsItsFolder guards FR-33/FR-50: declaring
// the capability must not have turned the preset's suggested folder into a
// grant. The requirement stays unresolved until the user approves it.
func TestDownloadsPreset_StillOnlySuggestsItsFolder(t *testing.T) {
	libDir := t.TempDir()
	if err := EnsureLibrary(libDir); err != nil {
		t.Fatalf("EnsureLibrary: %v", err)
	}
	tpl, err := FindLibraryTemplate(libDir, "downloads-janitor")
	if err != nil {
		t.Fatalf("FindLibraryTemplate: %v", err)
	}

	if len(tpl.DirectoryRequirements) == 0 {
		t.Fatal("the preset should still ask for a folder")
	}
	req := tpl.DirectoryRequirements[0]
	if !strings.Contains(req.SuggestedPath, "Downloads") {
		t.Fatalf("suggested path = %q, want the Downloads suggestion", req.SuggestedPath)
	}
	// Unresolved on purpose: no "~" expansion, no absolute path, no grant.
	if strings.HasPrefix(req.SuggestedPath, "/") {
		t.Fatalf("the suggestion was resolved to an absolute path: %q", req.SuggestedPath)
	}
}

// TestFileJanitorBlueprint_IsGenericAndGrantsNothing pins the generic
// blueprint's contract (FR-31-FR-34).
//
// The distinction from the Downloads preset is the whole point: the preset
// suggests ~/Downloads, this one suggests nothing. A blueprint that quietly
// proposed a real folder would make the user's approval a formality.
func TestFileJanitorBlueprint_IsGenericAndGrantsNothing(t *testing.T) {
	libDir := t.TempDir()
	if err := EnsureLibrary(libDir); err != nil {
		t.Fatalf("EnsureLibrary: %v", err)
	}
	tpl, err := FindLibraryTemplate(libDir, "file-janitor")
	if err != nil {
		t.Fatalf("FindLibraryTemplate: %v", err)
	}

	// Declares the capability explicitly, as a blueprint install.
	if len(tpl.Capabilities) != 1 || tpl.Capabilities[0].ID != workspace.CapabilityFileJanitor {
		t.Fatalf("blueprint capabilities = %+v", tpl.Capabilities)
	}
	if tpl.Capabilities[0].Source != "blueprint" {
		t.Fatalf("install source = %q, want blueprint", tpl.Capabilities[0].Source)
	}

	// Asks for one folder, and proposes none.
	if len(tpl.DirectoryRequirements) != 1 {
		t.Fatalf("expected exactly one folder requirement, got %+v", tpl.DirectoryRequirements)
	}
	req := tpl.DirectoryRequirements[0]
	if req.Key != "file-janitor-root" {
		t.Fatalf("requirement key = %q, want the canonical one", req.Key)
	}
	if req.SuggestedPath != "" {
		t.Fatalf("the generic blueprint proposed a folder (%q); only a preset may", req.SuggestedPath)
	}
	if req.AccessDisclosure == "" {
		t.Fatal("the folder request must disclose what approving it grants")
	}

	// Carries a runnable wizard on the canonical adapter.
	if !tpl.HasSetupWizard() || tpl.HasInvalidSetupWizard() {
		t.Fatalf("wizard unusable: %v", tpl.SetupWizardError)
	}
	for _, step := range tpl.SetupWizard.Steps {
		if step.Adapter != "" && step.Adapter != "file_janitor" {
			t.Fatalf("step %q names adapter %q, want the canonical file_janitor", step.ID, step.Adapter)
		}
	}

	// Automation is requested, not started — and is keyed to this blueprint's
	// own requirement so the recipe actually resolves.
	if len(tpl.AutomationRecipes) != 1 {
		t.Fatalf("expected one automation recipe, got %+v", tpl.AutomationRecipes)
	}
	if tpl.AutomationRecipes[0].DirectoryKey != req.Key {
		t.Fatalf("automation recipe keyed to %q, not the declared folder %q",
			tpl.AutomationRecipes[0].DirectoryKey, req.Key)
	}

	// Includes the Curator by default, under the folder-neutral name (FR-35).
	if len(tpl.Agents) != 1 {
		t.Fatalf("expected exactly one companion agent, got %+v", tpl.Agents)
	}
	if tpl.Agents[0].Name != "File Curator" {
		t.Fatalf("companion = %q, want File Curator", tpl.Agents[0].Name)
	}

	if len(tpl.Warnings) != 0 {
		t.Fatalf("the shipped blueprint produced warnings: %v", tpl.Warnings)
	}
}

// TestFileJanitorBlueprint_CarriesNoDownloadsAssumptions guards the generic
// copy: this blueprint is for any inbox-style folder, so naming Downloads as
// though it were the subject would be wrong everywhere except the preset.
func TestFileJanitorBlueprint_CarriesNoDownloadsAssumptions(t *testing.T) {
	libDir := t.TempDir()
	if err := EnsureLibrary(libDir); err != nil {
		t.Fatalf("EnsureLibrary: %v", err)
	}
	tpl, err := FindLibraryTemplate(libDir, "file-janitor")
	if err != nil {
		t.Fatalf("FindLibraryTemplate: %v", err)
	}

	if strings.Contains(tpl.Name, "Downloads") {
		t.Fatalf("blueprint name is Downloads-specific: %q", tpl.Name)
	}
	if strings.Contains(tpl.Tagline, "Downloads") {
		t.Fatalf("tagline is Downloads-specific: %q", tpl.Tagline)
	}
	for _, step := range tpl.SetupWizard.Steps {
		if strings.Contains(step.Title, "Downloads") {
			t.Fatalf("wizard step %q has a Downloads-specific title: %q", step.ID, step.Title)
		}
	}
	// The description may mention Downloads as one EXAMPLE among several; that
	// is the point of the blueprint. It must not read as the only one.
	if strings.Contains(tpl.Description, "Downloads") &&
		!strings.Contains(tpl.Description, "Desktop") {
		t.Fatalf("description names Downloads without offering alternatives: %q", tpl.Description)
	}
}

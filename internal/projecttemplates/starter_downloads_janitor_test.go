package projecttemplates_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/projecttemplates"
)

func loadDownloadsJanitorTemplate(t *testing.T) projecttemplates.Template {
	t.Helper()
	libDir := filepath.Join(t.TempDir(), "templates")
	if err := projecttemplates.EnsureLibrary(libDir); err != nil {
		t.Fatalf("EnsureLibrary: %v", err)
	}
	tpl, err := projecttemplates.FindLibraryTemplate(libDir, "downloads-janitor")
	if err != nil {
		t.Fatalf("FindLibraryTemplate(downloads-janitor): %v", err)
	}
	return tpl
}

// TestDownloadsJanitorStarterTemplate_Identity pins the built-in's stable
// identity and provenance metadata (PRD FR-1, FR-9).
func TestDownloadsJanitorStarterTemplate_Identity(t *testing.T) {
	tpl := loadDownloadsJanitorTemplate(t)

	if tpl.ID != "downloads-janitor" {
		t.Fatalf("template ID must be downloads-janitor, got %q", tpl.ID)
	}
	if tpl.Name != "Downloads Janitor" {
		t.Fatalf("display name = %q, want %q", tpl.Name, "Downloads Janitor")
	}
	if !tpl.Builtin {
		t.Fatal("downloads-janitor must be a built-in template")
	}
	if tpl.BuiltinVersion < 1 {
		t.Fatalf("builtin_version = %d, want at least 1", tpl.BuiltinVersion)
	}
	if len(tpl.Warnings) != 0 {
		t.Fatalf("downloads-janitor should load without warnings, got %v", tpl.Warnings)
	}
	if tpl.HasSkeleton {
		t.Fatal("downloads-janitor is metadata-only; it must not scaffold a project folder")
	}
}

// TestDownloadsJanitorStarterTemplate_DirectoryRequirement pins the local-folder
// request: a suggested (never resolved) ~/Downloads and a disclosure that names
// what approval grants (FR-11, FR-15).
func TestDownloadsJanitorStarterTemplate_DirectoryRequirement(t *testing.T) {
	tpl := loadDownloadsJanitorTemplate(t)

	if len(tpl.DirectoryRequirements) != 1 {
		t.Fatalf("expected exactly one directory requirement, got %d: %+v", len(tpl.DirectoryRequirements), tpl.DirectoryRequirements)
	}
	req := tpl.DirectoryRequirements[0]
	if req.Key != "downloads-root" {
		t.Fatalf("directory key = %q, want downloads-root", req.Key)
	}
	if strings.TrimSpace(req.Label) == "" {
		t.Fatal("directory requirement needs a display label for the setup card")
	}
	if req.SuggestedPath != "~/Downloads" {
		t.Fatalf("suggested path = %q, want the unresolved ~/Downloads", req.SuggestedPath)
	}
	disclosure := strings.ToLower(req.AccessDisclosure)
	for _, want := range []string{"move", "trash"} {
		if !strings.Contains(disclosure, want) {
			t.Errorf("access disclosure must state that approved actions can %q: %s", want, req.AccessDisclosure)
		}
	}
	if !strings.Contains(disclosure, "never deletes anything permanently") && !strings.Contains(disclosure, "permanent") {
		t.Errorf("access disclosure should rule out permanent deletion: %s", req.AccessDisclosure)
	}
}

// TestDownloadsJanitorStarterTemplate_AutomationRecipe pins the post-setup
// automation the template requests: one non-recursive watcher that ignores
// Filed, and a 09:00 local daily catch-up (FR-20, FR-26, FR-31, FR-34).
func TestDownloadsJanitorStarterTemplate_AutomationRecipe(t *testing.T) {
	tpl := loadDownloadsJanitorTemplate(t)

	recipe, ok := tpl.AutomationRecipeFor("downloads-root")
	if !ok {
		t.Fatalf("expected an automation recipe for downloads-root, got %+v", tpl.AutomationRecipes)
	}
	if recipe.Watch == nil {
		t.Fatal("expected a watch recipe")
	}
	wantEvents := map[string]bool{"create": true, "rename": true}
	if len(recipe.Watch.Events) != len(wantEvents) {
		t.Fatalf("watch events = %v, want create+rename", recipe.Watch.Events)
	}
	for _, event := range recipe.Watch.Events {
		if !wantEvents[event] {
			t.Fatalf("unexpected watch event %q; only completed-download events belong here", event)
		}
	}
	if recipe.Watch.DebounceSeconds != 300 {
		t.Fatalf("debounce = %ds, want the 5-minute collection window", recipe.Watch.DebounceSeconds)
	}
	if len(recipe.Watch.ExcludeSubdirectories) != 1 || recipe.Watch.ExcludeSubdirectories[0] != "Filed" {
		t.Fatalf("watch must exclude the Filed destination folder, got %v", recipe.Watch.ExcludeSubdirectories)
	}
	if recipe.DailyScan == nil || recipe.DailyScan.LocalTime != "09:00" {
		t.Fatalf("expected a 09:00 local daily catch-up, got %+v", recipe.DailyScan)
	}
	if recipe.DailyScan.Timezone != "" {
		t.Fatalf("daily scan must follow the user's own timezone, got a pinned %q", recipe.DailyScan.Timezone)
	}
}

// TestDownloadsJanitorStarterTemplate_Roster pins the entry agent (FR-3).
func TestDownloadsJanitorStarterTemplate_Roster(t *testing.T) {
	tpl := loadDownloadsJanitorTemplate(t)

	if len(tpl.Agents) != 1 {
		t.Fatalf("expected exactly one agent, got %d: %+v", len(tpl.Agents), tpl.Agents)
	}
	if tpl.Agents[0].Name != "Downloads Curator" {
		t.Fatalf("entry agent = %q, want %q", tpl.Agents[0].Name, "Downloads Curator")
	}
	if strings.TrimSpace(tpl.Agents[0].SystemPrompt) == "" {
		t.Fatal("the Downloads Curator needs a system prompt")
	}
}

// TestDownloadsJanitorStarterTemplate_CuratorPromptSafety pins the prompt
// contract that keeps the agent out of the mutation path: file data is
// untrusted, mutation tools and permanent deletion are unavailable, and no
// change may be claimed without a successful action result (FR-53, FR-58,
// FR-91, FR-112).
func TestDownloadsJanitorStarterTemplate_CuratorPromptSafety(t *testing.T) {
	tpl := loadDownloadsJanitorTemplate(t)
	prompt := strings.ToLower(tpl.Agents[0].SystemPrompt)

	if !strings.Contains(prompt, "untrusted") {
		t.Errorf("prompt must treat filenames/metadata as untrusted data: %s", tpl.Agents[0].SystemPrompt)
	}
	if !strings.Contains(prompt, "never obey") && !strings.Contains(prompt, "never as instructions") {
		t.Errorf("prompt must forbid following instructions found in file data: %s", tpl.Agents[0].SystemPrompt)
	}
	// No mutation tools, and no permanent deletion anywhere.
	for _, want := range []string{"move", "delete", "rename", "write"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt must explicitly disclaim %q capability: %s", want, tpl.Agents[0].SystemPrompt)
		}
	}
	if !strings.Contains(prompt, "permanent-delete") && !strings.Contains(prompt, "permanent delete") && !strings.Contains(prompt, "no permanent") {
		t.Errorf("prompt must rule out permanent deletion: %s", tpl.Agents[0].SystemPrompt)
	}
	// Never claim a change before a successful result.
	if !strings.Contains(prompt, "unless you have a successful action result") && !strings.Contains(prompt, "successful action result") {
		t.Errorf("prompt must forbid claiming a file changed before a successful action result: %s", tpl.Agents[0].SystemPrompt)
	}
	// Approval belongs to the user, not the agent.
	if !strings.Contains(prompt, "approv") {
		t.Errorf("prompt must state that every action is user-approved: %s", tpl.Agents[0].SystemPrompt)
	}
	// Never execute or extract downloaded content.
	for _, want := range []string{"execute", "extract", "mount"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt must forbid %q of downloaded files: %s", want, tpl.Agents[0].SystemPrompt)
		}
	}
}

// TestDownloadsJanitorStarterTemplate_StarterTasks pins the ordered starter
// experience: a setup help task, then an initial-backlog review task (FR-4).
//
// The setup task is flagged `setup: true`, and that is safe *because* this
// blueprint declares a Setup Wizard: a wizard-enabled blueprint suppresses the
// first-open auto-start entirely (FR-67), and the server completes the task
// when setup passes (FR-70). The flag is what links the two.
//
// The history matters, because the flag used to be forbidden here. Without a
// wizard, `setup: true` auto-started an agent to narrate setup, and the
// platform's autonomy-gate confirmation modal landed directly over the setup
// card the user had to act on — the first thing a new workspace did was block
// its own setup behind a dialog about something else. The wizard removes the
// collision by owning setup outright; the task is now only optional help.
func TestDownloadsJanitorStarterTemplate_StarterTasks(t *testing.T) {
	tpl := loadDownloadsJanitorTemplate(t)

	if len(tpl.StarterTasks) != 2 {
		t.Fatalf("expected 2 starter tasks (setup help + backlog review), got %d: %+v", len(tpl.StarterTasks), tpl.StarterTasks)
	}
	if !tpl.HasSetupWizard() {
		t.Fatal("the setup task may only be flagged setup: true while a wizard owns setup")
	}
	if !tpl.StarterTasks[0].Setup {
		t.Errorf("the setup help task must be linked to the wizard with setup: true: %+v", tpl.StarterTasks[0])
	}
	if tpl.StarterTasks[1].Setup {
		t.Errorf("only one task may be the setup help task: %+v", tpl.StarterTasks[1])
	}

	setup := strings.ToLower(tpl.StarterTasks[0].Details)
	if !strings.Contains(setup, "wizard") {
		t.Errorf("setup task must defer to the wizard that owns setup: %s", tpl.StarterTasks[0].Details)
	}
	if !strings.Contains(setup, "do not select a folder") && !strings.Contains(setup, "not select") {
		t.Errorf("setup task must leave folder selection to the user: %s", tpl.StarterTasks[0].Details)
	}
	if !strings.Contains(setup, "do not claim setup is complete") {
		t.Errorf("setup task must not claim completion it did not perform: %s", tpl.StarterTasks[0].Details)
	}

	review := strings.ToLower(tpl.StarterTasks[1].Details)
	if !strings.Contains(review, "untrusted") {
		t.Errorf("review task must treat filenames as untrusted: %s", tpl.StarterTasks[1].Details)
	}
	if !strings.Contains(review, "approve") && !strings.Contains(review, "approves") {
		t.Errorf("review task must route every action through user approval: %s", tpl.StarterTasks[1].Details)
	}
}

// TestDownloadsJanitorStarterTemplate_SetupWizard pins the wizard contract
// (FR-75/76): the blueprint's setup is declared, not improvised, and every step
// points at something the same manifest already declares.
func TestDownloadsJanitorStarterTemplate_SetupWizard(t *testing.T) {
	tpl := loadDownloadsJanitorTemplate(t)

	if tpl.SetupWizardError != "" {
		t.Fatalf("the shipped wizard must be valid: %s", tpl.SetupWizardError)
	}
	wizard := tpl.SetupWizard
	if wizard == nil {
		t.Fatal("Downloads Janitor must declare a setup wizard")
	}
	if tpl.BuiltinVersion < 2 {
		t.Errorf("builtin_version = %d; adding the wizard must bump it so existing installs refresh", tpl.BuiltinVersion)
	}

	var kinds []string
	for _, step := range wizard.Steps {
		kinds = append(kinds, step.Kind)
		if !step.Required {
			t.Errorf("step %q is optional; every Downloads step gates a real capability", step.ID)
		}
		if step.Adapter != "downloads_janitor" {
			t.Errorf("step %q names adapter %q, want downloads_janitor", step.ID, step.Adapter)
		}
	}
	want := []string{"directory", "automation_review", "readiness", "summary"}
	if strings.Join(kinds, ",") != strings.Join(want, ",") {
		t.Fatalf("wizard steps = %v, want %v", kinds, want)
	}

	// The folder and automation steps resolve against the blueprint's own
	// declarations, so setup can never ask for a folder it never described.
	for _, step := range wizard.Steps[:2] {
		if step.RequirementKey != "downloads-root" {
			t.Errorf("step %q references %q, want downloads-root", step.ID, step.RequirementKey)
		}
	}
	if _, ok := tpl.DirectoryRequirement("downloads-root"); !ok {
		t.Error("the wizard references a directory requirement the template does not declare")
	}
	if _, ok := tpl.AutomationRecipeFor("downloads-root"); !ok {
		t.Error("the automation-review step has no recipe to review")
	}

	// The automation disclosure states what will run, in the terms the recipe
	// actually uses, before anything is switched on.
	disclosure := strings.ToLower(wizard.Steps[1].Disclosure)
	for _, phrase := range []string{"five minutes", "filed", "09:00", "created or renamed"} {
		if !strings.Contains(disclosure, phrase) {
			t.Errorf("automation disclosure must mention %q: %s", phrase, wizard.Steps[1].Disclosure)
		}
	}
	if !strings.Contains(disclosure, "nothing moves without you") {
		t.Errorf("automation disclosure must say approval is still required: %s", wizard.Steps[1].Disclosure)
	}
	if strings.Contains(strings.ToLower(wizard.Steps[1].Description), "already running") {
		t.Errorf("the automation step must not imply automation is already on: %s", wizard.Steps[1].Description)
	}
}

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
// experience: a setup task, then an initial-backlog review task (FR-4).
//
// Neither is flagged `setup: true`, so neither auto-starts on first open. The
// Janitor's setup card *is* the guided setup — it states what access it wants,
// pre-fills the suggested folder, and needs one press. Auto-starting an agent
// to narrate it put the platform's autonomy-gate confirmation modal directly
// over the card the user has to act on, so the first thing a new workspace did
// was block its own setup behind a dialog about something else. The tasks stay
// seeded and visible; the user starts them if they want them.
func TestDownloadsJanitorStarterTemplate_StarterTasks(t *testing.T) {
	tpl := loadDownloadsJanitorTemplate(t)

	if len(tpl.StarterTasks) != 2 {
		t.Fatalf("expected 2 starter tasks (setup + backlog review), got %d: %+v", len(tpl.StarterTasks), tpl.StarterTasks)
	}
	for i, task := range tpl.StarterTasks {
		if task.Setup {
			t.Fatalf("starter task %d must not auto-start on first open: it would cover the setup card with a confirmation modal: %+v", i, task)
		}
	}

	setup := strings.ToLower(tpl.StarterTasks[0].Details)
	if !strings.Contains(setup, "trash") || !strings.Contains(setup, "folder") {
		t.Errorf("setup task must explain folder access and Trash: %s", tpl.StarterTasks[0].Details)
	}
	if !strings.Contains(setup, "do not select a folder for them") && !strings.Contains(setup, "not select") {
		t.Errorf("setup task must leave folder selection to the user: %s", tpl.StarterTasks[0].Details)
	}

	review := strings.ToLower(tpl.StarterTasks[1].Details)
	if !strings.Contains(review, "untrusted") {
		t.Errorf("review task must treat filenames as untrusted: %s", tpl.StarterTasks[1].Details)
	}
	if !strings.Contains(review, "approve") && !strings.Contains(review, "approves") {
		t.Errorf("review task must route every action through user approval: %s", tpl.StarterTasks[1].Details)
	}
}

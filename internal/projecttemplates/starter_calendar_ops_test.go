package projecttemplates_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/projecttemplates"
)

// TestCalendarOpsStarterManifest pins the Calendar Ops builtin template
// contract (PRD FR17-FR23): stable id, builtin metadata, the Scheduler +
// Meeting Prep roster in order, grounded/untrusted/read-only/confirmation and
// permissioned-context prompt guarantees, the setup + agenda + prep starter
// tasks, and the abstract "calendar" capability requirement.
func TestCalendarOpsStarterManifest(t *testing.T) {
	libDir := filepath.Join(t.TempDir(), "templates")
	if err := projecttemplates.EnsureLibrary(libDir); err != nil {
		t.Fatalf("EnsureLibrary: %v", err)
	}

	tpl, err := projecttemplates.FindLibraryTemplate(libDir, "calendar-ops")
	if err != nil {
		t.Fatalf("FindLibraryTemplate(calendar-ops): %v", err)
	}

	// Identity / provenance key. The id (folder name) is what workspace
	// provenance records as template_id: "calendar-ops" so Home/Personal HQ can
	// resolve the user's Calendar Ops workspace (FR23) — it must never change.
	if tpl.ID != "calendar-ops" {
		t.Fatalf("template ID must be calendar-ops, got %q", tpl.ID)
	}
	if !tpl.Builtin {
		t.Fatal("calendar-ops must be a built-in template")
	}
	if !projecttemplates.IsBuiltinStarterID("calendar-ops") {
		t.Fatal("IsBuiltinStarterID(calendar-ops) must be true")
	}
	if tpl.BuiltinVersion < 1 {
		t.Fatalf("calendar-ops builtin_version = %d, want at least 1", tpl.BuiltinVersion)
	}
	if tpl.Name != "Calendar Ops" {
		t.Fatalf("display name = %q, want %q", tpl.Name, "Calendar Ops")
	}
	if strings.TrimSpace(tpl.Icon) == "" {
		t.Fatal("calendar-ops must ship an icon for the Construct wizard card")
	}
	if strings.TrimSpace(tpl.Tagline) == "" {
		t.Fatal("calendar-ops must ship a tagline for the Construct wizard card")
	}
	if projecttemplates.NormalizeBehaviorProfile(tpl.BehaviorProfile) != projecttemplates.BehaviorProfileGeneral {
		t.Fatalf("calendar-ops behavior profile = %q, want general", tpl.BehaviorProfile)
	}

	// Roster: Scheduler (entry/orchestrator) then Meeting Prep (specialist),
	// order preserved.
	wantRoster := []string{"Scheduler", "Meeting Prep"}
	if len(tpl.Agents) != len(wantRoster) {
		t.Fatalf("expected %d agents %v, got %d: %+v", len(wantRoster), wantRoster, len(tpl.Agents), tpl.Agents)
	}
	for i, want := range wantRoster {
		if tpl.Agents[i].Name != want {
			t.Fatalf("agent[%d] = %q, want %q (order preserved; first is the entry agent)", i, tpl.Agents[i].Name, want)
		}
	}
	if !strings.EqualFold(tpl.Agents[0].Role, "orchestrator") {
		t.Errorf("Scheduler role = %q, want orchestrator", tpl.Agents[0].Role)
	}
	if !strings.EqualFold(tpl.Agents[1].Role, "specialist") {
		t.Errorf("Meeting Prep role = %q, want specialist", tpl.Agents[1].Role)
	}

	// Scheduler prompt: grounded schedule claims, read-only tools, explicit
	// mutation confirmation, untrusted event content (FR19).
	scheduler := strings.ToLower(tpl.Agents[0].SystemPrompt)
	for _, want := range []string{"read-only", "confirm", "untrusted", "ground"} {
		if !strings.Contains(scheduler, want) {
			t.Errorf("Scheduler prompt missing guarantee keyword %q: %s", want, tpl.Agents[0].SystemPrompt)
		}
	}
	if !strings.Contains(scheduler, "never") {
		t.Errorf("Scheduler prompt must forbid claiming an unconfirmed change: %s", tpl.Agents[0].SystemPrompt)
	}

	// Meeting Prep prompt: grounded in the selected event + explicitly permitted
	// context, untrusted external content, no invented history (FR20).
	prep := strings.ToLower(tpl.Agents[1].SystemPrompt)
	for _, want := range []string{"untrusted", "permitted", "gap", "never"} {
		if !strings.Contains(prep, want) {
			t.Errorf("Meeting Prep prompt missing guarantee keyword %q: %s", want, tpl.Agents[1].SystemPrompt)
		}
	}

	// Starter tasks: exactly one setup:true (first), plus the agenda and prep
	// tasks (FR21, FR22).
	if len(tpl.StarterTasks) != 3 {
		t.Fatalf("expected 3 starter tasks (1 setup + agenda + prep), got %d: %+v", len(tpl.StarterTasks), tpl.StarterTasks)
	}
	setupCount := 0
	for i, task := range tpl.StarterTasks {
		if task.Setup {
			setupCount++
			if i != 0 {
				t.Errorf("setup task must be first, found at index %d", i)
			}
		}
	}
	if setupCount != 1 {
		t.Fatalf("expected exactly one setup:true starter task, got %d", setupCount)
	}
	if !strings.Contains(strings.ToLower(tpl.StarterTasks[1].Description), "agenda") {
		t.Errorf("second starter task should review today's agenda, got %q", tpl.StarterTasks[1].Description)
	}
	if !strings.Contains(strings.ToLower(tpl.StarterTasks[2].Description), "meeting") {
		t.Errorf("third starter task should prepare for a meeting, got %q", tpl.StarterTasks[2].Description)
	}

	// Capability requirement: the calendar capability, not a hard-coded MCP
	// server name (FR8, FR9, FR10).
	if len(tpl.CapabilityRequirements) != 1 {
		t.Fatalf("expected exactly one capability requirement, got %d: %+v", len(tpl.CapabilityRequirements), tpl.CapabilityRequirements)
	}
	req := tpl.CapabilityRequirements[0]
	if req.Key != "calendar" {
		t.Fatalf("capability key = %q, want calendar", req.Key)
	}
	assertContainsAll(t, "required_operations", req.RequiredOperations, []string{"list_calendars", "list_events"})
	assertContainsAll(t, "optional_operations", req.OptionalOperations, []string{
		"get_event", "freebusy", "suggest_time", "create_event", "update_event", "list_accounts", "connect_account",
	})

	if len(tpl.Warnings) != 0 {
		t.Fatalf("calendar-ops should load without warnings, got %v", tpl.Warnings)
	}
}

// TestCalendarOpsAppearsInLibrary confirms the built-in surfaces in the library
// listing (which backs the Construct wizard picker) as a built-in.
func TestCalendarOpsAppearsInLibrary(t *testing.T) {
	libDir := filepath.Join(t.TempDir(), "templates")
	if err := projecttemplates.EnsureLibrary(libDir); err != nil {
		t.Fatalf("EnsureLibrary: %v", err)
	}
	templates, err := projecttemplates.ListLibrary(libDir)
	if err != nil {
		t.Fatalf("ListLibrary: %v", err)
	}
	for _, tpl := range templates {
		if tpl.ID == "calendar-ops" {
			if !tpl.Builtin {
				t.Fatal("calendar-ops must list as built-in")
			}
			return
		}
	}
	t.Fatal("calendar-ops did not appear in the library listing")
}

func assertContainsAll(t *testing.T, label string, got, want []string) {
	t.Helper()
	set := make(map[string]bool, len(got))
	for _, g := range got {
		set[g] = true
	}
	for _, w := range want {
		if !set[w] {
			t.Errorf("%s missing %q; got %v", label, w, got)
		}
	}
}

// TestCalendarOpsStarterManifest_SetupWizard pins the wizard contract (FR-85):
// the blueprint declares its setup, and the steps line up with the domain's own
// state machine — connect, then map and validate, then check, then summarize.
func TestCalendarOpsStarterManifest_SetupWizard(t *testing.T) {
	libDir := filepath.Join(t.TempDir(), "templates")
	if err := projecttemplates.EnsureLibrary(libDir); err != nil {
		t.Fatalf("EnsureLibrary: %v", err)
	}
	tpl, err := projecttemplates.FindLibraryTemplate(libDir, "calendar-ops")
	if err != nil {
		t.Fatalf("FindLibraryTemplate(calendar-ops): %v", err)
	}

	if tpl.SetupWizardError != "" {
		t.Fatalf("the shipped wizard must be valid: %s", tpl.SetupWizardError)
	}
	wizard := tpl.SetupWizard
	if wizard == nil {
		t.Fatal("Calendar Ops must declare a setup wizard")
	}
	if tpl.BuiltinVersion < 2 {
		t.Errorf("builtin_version = %d; adding the wizard must bump it", tpl.BuiltinVersion)
	}

	var kinds []string
	for _, step := range wizard.Steps {
		kinds = append(kinds, step.Kind)
		if step.Adapter != "calendar_ops" {
			t.Errorf("step %q names adapter %q, want calendar_ops", step.ID, step.Adapter)
		}
		if !step.Required {
			t.Errorf("step %q is optional; every Calendar step gates the capability", step.ID)
		}
	}
	want := []string{"capability_connect", "capability_configure", "readiness", "summary"}
	if strings.Join(kinds, ",") != strings.Join(want, ",") {
		t.Fatalf("wizard steps = %v, want %v", kinds, want)
	}
	for _, step := range wizard.Steps[:2] {
		if step.RequirementKey != "calendar" {
			t.Errorf("step %q references %q, want the declared calendar capability", step.ID, step.RequirementKey)
		}
	}

	// Least privilege has to be stated where the user is deciding, not only in
	// the code: mutation stays unmapped unless they map it themselves (FR-90).
	configure := strings.ToLower(wizard.Steps[1].Disclosure)
	for _, phrase := range []string{"listing calendars", "listing events", "required"} {
		if !strings.Contains(configure, phrase) {
			t.Errorf("the mapping step must state what is required (%q): %s", phrase, wizard.Steps[1].Disclosure)
		}
	}
	if !strings.Contains(configure, "unmapped") {
		t.Errorf("the mapping step must say mutation stays unmapped by default: %s", wizard.Steps[1].Disclosure)
	}

	// Signing in happens with the provider; the connect step says so before it
	// sends anyone there.
	connect := strings.ToLower(wizard.Steps[0].Disclosure)
	if !strings.Contains(connect, "provider") {
		t.Errorf("the connect step must state where sign-in happens: %s", wizard.Steps[0].Disclosure)
	}
}

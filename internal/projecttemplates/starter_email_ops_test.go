package projecttemplates_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/projecttemplates"
)

// TestEmailOpsStarterTemplate pins the Email Ops blueprint (Mail spin-off PRD
// FR1-6): a built-in template whose ordered roster is Postmaster (entry) +
// Inbox specialist, with a setup starter task that connects an email account
// and a first-triage task. The Inbox specialist must be named exactly "Inbox"
// so the mailbox-access gate (isInboxAgent) recognizes it.
func TestEmailOpsStarterTemplate(t *testing.T) {
	libDir := filepath.Join(t.TempDir(), "templates")
	if err := projecttemplates.EnsureLibrary(libDir); err != nil {
		t.Fatalf("EnsureLibrary: %v", err)
	}

	tpl, err := projecttemplates.FindLibraryTemplate(libDir, "email-ops")
	if err != nil {
		t.Fatalf("FindLibraryTemplate(email-ops): %v", err)
	}
	if tpl.ID != "email-ops" {
		t.Fatalf("template ID must be email-ops, got %q", tpl.ID)
	}
	if !tpl.Builtin {
		t.Fatal("email-ops must be a built-in template")
	}
	// builtin_version gates the on-disk manifest refresh: an existing install
	// keeps its old copy unless the shipped version is higher. Bumping it is how
	// a blueprint change actually reaches users (see refreshBuiltinManifest).
	if tpl.BuiltinVersion < 2 {
		t.Fatalf("email-ops builtin_version = %d, want at least 2 so existing installs refresh the manifest", tpl.BuiltinVersion)
	}
	if tpl.Name != "Email Ops" {
		t.Fatalf("display name = %q, want %q", tpl.Name, "Email Ops")
	}

	// Roster: Postmaster (entry/orchestrator) then Inbox (specialist). Order is
	// preserved and the first agent is the entry agent.
	wantRoster := []string{"Postmaster", "Inbox"}
	if len(tpl.Agents) != len(wantRoster) {
		t.Fatalf("expected %d agents %v, got %d: %+v", len(wantRoster), wantRoster, len(tpl.Agents), tpl.Agents)
	}
	for i, want := range wantRoster {
		if tpl.Agents[i].Name != want {
			t.Fatalf("agent[%d] = %q, want %q (order preserved; first is the entry agent)", i, tpl.Agents[i].Name, want)
		}
	}

	// The specialist must be named exactly "Inbox" so isInboxAgent (in
	// internal/server/mailbox_access.go) grants it mailbox access without a
	// gate change.
	if tpl.Agents[1].Name != "Inbox" {
		t.Fatalf("specialist must be named exactly \"Inbox\" for the mailbox-access gate, got %q", tpl.Agents[1].Name)
	}

	// Postmaster prompt scopes it to email ops and disclaims non-email work.
	postmaster := strings.ToLower(tpl.Agents[0].SystemPrompt)
	for _, want := range []string{"inbox", "route", "follow-up"} {
		if !strings.Contains(postmaster, want) {
			t.Errorf("Postmaster prompt missing scope keyword %q: %s", want, tpl.Agents[0].SystemPrompt)
		}
	}

	// The Inbox specialist must promise explicit send confirmation and treat
	// mail as untrusted (contract carried over from the HQ Inbox).
	inbox := strings.ToLower(tpl.Agents[1].SystemPrompt)
	if !strings.Contains(inbox, "confirm") || !strings.Contains(inbox, "untrusted") {
		t.Errorf("Inbox prompt must promise explicit send confirmation and treat mail as untrusted: %s", tpl.Agents[1].SystemPrompt)
	}

	// Starter tasks: a setup task (first, setup:true) that connects email, plus
	// a first-triage task.
	if len(tpl.StarterTasks) != 2 {
		t.Fatalf("expected 2 starter tasks (1 setup + 1 triage), got %d: %+v", len(tpl.StarterTasks), tpl.StarterTasks)
	}
	if !tpl.StarterTasks[0].Setup {
		t.Errorf("first starter task must be the setup task (setup:true): %+v", tpl.StarterTasks[0])
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

	if len(tpl.Warnings) != 0 {
		t.Fatalf("email-ops should load without warnings, got %v", tpl.Warnings)
	}
}

// The Email Ops blueprint must stay provider-neutral: Gmail is the only
// operational adapter in this release, but the template names no provider, so
// adding Microsoft 365 or IMAP later needs no second blueprint (FR 32, 83).
func TestEmailOpsStarterTemplate_ProviderNeutral(t *testing.T) {
	libDir := filepath.Join(t.TempDir(), "templates")
	if err := projecttemplates.EnsureLibrary(libDir); err != nil {
		t.Fatalf("EnsureLibrary: %v", err)
	}
	tpl, err := projecttemplates.FindLibraryTemplate(libDir, "email-ops")
	if err != nil {
		t.Fatalf("FindLibraryTemplate(email-ops): %v", err)
	}

	// Everything the user reads, in one haystack.
	var prose []string
	prose = append(prose, tpl.Name, tpl.Description, tpl.Tagline)
	for _, a := range tpl.Agents {
		prose = append(prose, a.Name, a.SystemPrompt)
	}
	for _, st := range tpl.StarterTasks {
		prose = append(prose, st.Description, st.Details)
	}
	prose = append(prose, tpl.Tags...)
	// The Setup Wizard's copy is blueprint prose too, and it is the surface
	// where naming a provider would be most tempting — it is the step that
	// actually connects one.
	if tpl.SetupWizard != nil {
		prose = append(prose, tpl.SetupWizard.Title)
		for _, step := range tpl.SetupWizard.Steps {
			prose = append(prose, step.Title, step.Description, step.Disclosure)
		}
	}
	haystack := strings.ToLower(strings.Join(prose, "\n"))

	for _, provider := range []string{"gmail", "google", "outlook", "microsoft", "imap", "smtp", "fastmail", "proton"} {
		if strings.Contains(haystack, provider) {
			t.Errorf("Email Ops blueprint names the provider %q; it must stay provider-neutral", provider)
		}
	}
}

// The mail-dependent starter task declares the abstract capability it needs, so
// execution can stop with an actionable repair instead of spending a model call
// on an inbox it cannot read (FR 34). The setup task must NOT declare it — that
// task exists precisely to establish the connection.
func TestEmailOpsStarterTemplate_TriageRequiresEmailCapability(t *testing.T) {
	libDir := filepath.Join(t.TempDir(), "templates")
	if err := projecttemplates.EnsureLibrary(libDir); err != nil {
		t.Fatalf("EnsureLibrary: %v", err)
	}
	tpl, err := projecttemplates.FindLibraryTemplate(libDir, "email-ops")
	if err != nil {
		t.Fatalf("FindLibraryTemplate(email-ops): %v", err)
	}

	for _, st := range tpl.StarterTasks {
		requires := strings.Join(st.Requires, ",")
		if st.Setup {
			if requires != "" {
				t.Errorf("the setup task must not require the connection it creates, got %q", requires)
			}
			continue
		}
		if requires != "email" {
			t.Errorf("mail-dependent task %q requires %q, want \"email\"", st.Description, requires)
		}
		// Provider-neutral by construction.
		for _, key := range st.Requires {
			if strings.Contains(strings.ToLower(key), "gmail") {
				t.Errorf("capability key %q names a provider", key)
			}
		}
	}
}

// TestEmailOpsStarterTemplate_SetupWizard pins the wizard contract (FR-95/96):
// the blueprint declares its setup, and the steps keep the three things that
// look alike from the outside apart — the account connection, the mail
// permission, and this workspace's own link.
func TestEmailOpsStarterTemplate_SetupWizard(t *testing.T) {
	libDir := filepath.Join(t.TempDir(), "templates")
	if err := projecttemplates.EnsureLibrary(libDir); err != nil {
		t.Fatalf("EnsureLibrary: %v", err)
	}
	tpl, err := projecttemplates.FindLibraryTemplate(libDir, "email-ops")
	if err != nil {
		t.Fatalf("FindLibraryTemplate(email-ops): %v", err)
	}

	if tpl.SetupWizardError != "" {
		t.Fatalf("the shipped wizard must be valid: %s", tpl.SetupWizardError)
	}
	wizard := tpl.SetupWizard
	if wizard == nil {
		t.Fatal("Email Ops must declare a setup wizard")
	}
	if tpl.BuiltinVersion < 3 {
		t.Errorf("builtin_version = %d; adding the wizard must bump it", tpl.BuiltinVersion)
	}

	var kinds []string
	for _, step := range wizard.Steps {
		kinds = append(kinds, step.Kind)
		if step.Adapter != "email_ops" {
			t.Errorf("step %q names adapter %q, want email_ops", step.ID, step.Adapter)
		}
		if !step.Required {
			t.Errorf("step %q is optional; every Email step gates the capability", step.ID)
		}
	}
	if strings.Join(kinds, ",") != "account_link,readiness,summary" {
		t.Fatalf("wizard steps = %v, want account_link,readiness,summary", kinds)
	}

	// The account-link step references the abstract capability the blueprint
	// declares — "email", never a provider.
	if wizard.Steps[0].RequirementKey != "email" {
		t.Errorf("the link step references %q, want the declared email capability", wizard.Steps[0].RequirementKey)
	}
	if _, ok := tpl.CapabilityRequirement("email"); !ok {
		t.Error("the wizard references a capability the template does not declare")
	}

	// The boundary the user is agreeing to is stated where they agree to it:
	// read and draft, and nothing sent without confirming that message.
	disclosure := strings.ToLower(wizard.Steps[0].Disclosure)
	for _, phrase := range []string{"reads your mail", "drafts", "never sends"} {
		if !strings.Contains(disclosure, phrase) {
			t.Errorf("the link step must state the read/draft boundary (%q): %s", phrase, wizard.Steps[0].Disclosure)
		}
	}
	if !strings.Contains(disclosure, "separate") {
		t.Errorf("the link step must separate signing in from linking a mailbox: %s", wizard.Steps[0].Disclosure)
	}
	// No step may ask for permission to send.
	for _, step := range wizard.Steps {
		if strings.Contains(strings.ToLower(step.Disclosure+step.Description), "send permission") {
			t.Errorf("version 1 setup must not request send permission: %+v", step)
		}
	}
}

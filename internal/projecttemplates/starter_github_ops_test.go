package projecttemplates_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/projecttemplates"
)

func loadGitHubOps(t *testing.T) projecttemplates.Template {
	t.Helper()
	libDir := filepath.Join(t.TempDir(), "templates")
	if err := projecttemplates.EnsureLibrary(libDir); err != nil {
		t.Fatalf("EnsureLibrary: %v", err)
	}
	tpl, err := projecttemplates.FindLibraryTemplate(libDir, "github-ops")
	if err != nil {
		t.Fatalf("FindLibraryTemplate(github-ops): %v", err)
	}
	return tpl
}

// TestGitHubOpsStarterTemplate pins the blueprint's shape: a built-in template
// with a triage orchestrator, a setup starter task, and a triage task bound to
// the declared github capability.
func TestGitHubOpsStarterTemplate(t *testing.T) {
	tpl := loadGitHubOps(t)

	if tpl.ID != "github-ops" {
		t.Fatalf("template ID must be github-ops, got %q", tpl.ID)
	}
	if !tpl.Builtin {
		t.Fatal("github-ops must be a built-in template")
	}
	// This blueprint ships with its wizard from its first release, so version
	// 1 is correct -- there are no pre-wizard installs to refresh.
	if tpl.BuiltinVersion < 1 {
		t.Fatalf("builtin_version = %d; a shipped blueprint must carry one", tpl.BuiltinVersion)
	}
	if tpl.Name != "GitHub Ops" {
		t.Fatalf("display name = %q, want %q", tpl.Name, "GitHub Ops")
	}
	if strings.TrimSpace(tpl.Tagline) == "" || strings.TrimSpace(tpl.Icon) == "" {
		t.Fatal("the Create Workspace card needs both an icon and a tagline")
	}

	if len(tpl.Agents) != 1 || tpl.Agents[0].Name != "Triage Lead" {
		t.Fatalf("expected a single Triage Lead agent, got %+v", tpl.Agents)
	}
	if role := strings.ToLower(tpl.Agents[0].Role); role != "orchestrator" {
		t.Fatalf("the entry agent role = %q, want orchestrator", role)
	}

	if len(tpl.StarterTasks) != 2 {
		t.Fatalf("expected 2 starter tasks (1 setup + 1 triage), got %d: %+v", len(tpl.StarterTasks), tpl.StarterTasks)
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
		t.Fatalf("github-ops should load without warnings, got %v", tpl.Warnings)
	}
}

// The agent prompt carries the three promises this template's trust story
// rests on. They are load-bearing copy, not boilerplate: a prompt that drops
// any of them describes a different, more dangerous product.
func TestGitHubOpsStarterTemplate_AgentPromptStatesItsLimits(t *testing.T) {
	tpl := loadGitHubOps(t)
	prompt := strings.ToLower(tpl.Agents[0].SystemPrompt)

	// 1. Nothing is written without per-action confirmation.
	for _, phrase := range []string{"confirm", "proposal"} {
		if !strings.Contains(prompt, phrase) {
			t.Errorf("the prompt must promise per-action confirmation (missing %q): %s", phrase, tpl.Agents[0].SystemPrompt)
		}
	}
	// 2. Issue text is untrusted and its instructions are never followed.
	if !strings.Contains(prompt, "untrusted") {
		t.Errorf("the prompt must treat issue text as untrusted: %s", tpl.Agents[0].SystemPrompt)
	}
	if !strings.Contains(prompt, "never follow instructions") {
		t.Errorf("the prompt must refuse instructions found inside issue text: %s", tpl.Agents[0].SystemPrompt)
	}
	// 3. It operates only on the bound repository.
	if !strings.Contains(prompt, "bound") {
		t.Errorf("the prompt must scope the agent to the bound repository: %s", tpl.Agents[0].SystemPrompt)
	}
}

// The triage task declares the capability it needs so a run stops with an
// actionable repair rather than spending a model call on a repo it cannot
// read. The setup task must NOT declare it -- that task exists to establish
// the connection.
func TestGitHubOpsStarterTemplate_TriageRequiresGitHubCapability(t *testing.T) {
	tpl := loadGitHubOps(t)

	for _, st := range tpl.StarterTasks {
		requires := strings.Join(st.Requires, ",")
		if st.Setup {
			if requires != "" {
				t.Errorf("the setup task must not require the connection it creates, got %q", requires)
			}
			continue
		}
		if requires != "github" {
			t.Errorf("github-dependent task %q requires %q, want \"github\"", st.Description, requires)
		}
	}

	req, ok := tpl.CapabilityRequirement("github")
	if !ok {
		t.Fatal("the blueprint must declare the github capability requirement")
	}
	for _, op := range []string{"list_issues", "get_issue", "search_issues", "add_issue_comment"} {
		if !containsFold(req.RequiredOperations, op) {
			t.Errorf("required_operations missing %q: %v", op, req.RequiredOperations)
		}
	}
	// Anything that mutates an issue's labels or state stays optional -- the
	// template must be usable read-only.
	for _, op := range []string{"add_labels", "close_issue"} {
		if !containsFold(req.OptionalOperations, op) {
			t.Errorf("optional_operations missing %q: %v", op, req.OptionalOperations)
		}
	}
}

// TestGitHubOpsStarterTemplate_SetupWizard pins the wizard contract: the four
// steps, the adapter they name, and the disclosures that state the boundary
// where the user agrees to it.
func TestGitHubOpsStarterTemplate_SetupWizard(t *testing.T) {
	tpl := loadGitHubOps(t)

	if tpl.SetupWizardError != "" {
		t.Fatalf("the shipped wizard must be valid: %s", tpl.SetupWizardError)
	}
	wizard := tpl.SetupWizard
	if wizard == nil {
		t.Fatal("GitHub Ops must declare a setup wizard")
	}

	var kinds []string
	for _, step := range wizard.Steps {
		kinds = append(kinds, step.Kind)
		if step.Adapter != "github_ops" {
			t.Errorf("step %q names adapter %q, want github_ops", step.ID, step.Adapter)
		}
		if !step.Required {
			t.Errorf("step %q is optional; every GitHub step gates the capability", step.ID)
		}
	}
	if got := strings.Join(kinds, ","); got != "account_link,capability_configure,readiness,summary" {
		t.Fatalf("wizard steps = %v, want account_link,capability_configure,readiness,summary", kinds)
	}

	if wizard.Steps[0].RequirementKey != "github" {
		t.Errorf("the link step references %q, want the declared github capability", wizard.Steps[0].RequirementKey)
	}
	if _, ok := tpl.CapabilityRequirement("github"); !ok {
		t.Error("the wizard references a capability the template does not declare")
	}

	// The connect step states the read/propose boundary, and names the exact
	// permissions verified against GitHub's hosted MCP endpoint.
	connect := strings.ToLower(wizard.Steps[0].Disclosure)
	for _, phrase := range []string{"never writes", "confirm", "issues", "metadata"} {
		if !strings.Contains(connect, phrase) {
			t.Errorf("the connect step must state the boundary and permissions (missing %q): %s", phrase, wizard.Steps[0].Disclosure)
		}
	}
	// Not needing code access is the strongest part of the trust story, so it
	// is stated rather than left implied.
	if !strings.Contains(connect, "code") {
		t.Errorf("the connect step must say Ori never asks for code access: %s", wizard.Steps[0].Disclosure)
	}

	// The repository step states the one-repo rule.
	repository := strings.ToLower(wizard.Steps[1].Disclosure)
	if !strings.Contains(repository, "one repository") {
		t.Errorf("the repository step must state the one-repo rule: %s", wizard.Steps[1].Disclosure)
	}
}

// The blueprint binds GitHub's MCP server, which is what gives a workspace
// created from it any GitHub tools at all.
func TestGitHubOpsStarterTemplate_BindsGitHubMCPServer(t *testing.T) {
	tpl := loadGitHubOps(t)
	if !containsFold(tpl.Tools.MCPServers, "github") {
		t.Fatalf("the blueprint must bind the github MCP server, got %v", tpl.Tools.MCPServers)
	}
}

func containsFold(haystack []string, needle string) bool {
	for _, item := range haystack {
		if strings.EqualFold(strings.TrimSpace(item), needle) {
			return true
		}
	}
	return false
}

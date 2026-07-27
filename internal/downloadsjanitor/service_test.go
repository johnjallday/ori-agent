package downloadsjanitor

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	workspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// fakeWorkspaceStore keeps workspaces in memory; Update mirrors the real
// store's read-modify-write contract.
type fakeWorkspaceStore struct {
	workspaces map[string]*workspace.Workspace
	updateErr  error
}

func newFakeWorkspaceStore(ids ...string) *fakeWorkspaceStore {
	store := &fakeWorkspaceStore{workspaces: map[string]*workspace.Workspace{}}
	for _, id := range ids {
		ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: id})
		ws.ID = id
		store.workspaces[id] = ws
	}
	return store
}

func (f *fakeWorkspaceStore) Get(id string) (*workspace.Workspace, error) {
	ws, ok := f.workspaces[id]
	if !ok {
		return nil, errors.New("workspace not found")
	}
	return ws, nil
}

func (f *fakeWorkspaceStore) Update(id string, fn func(*workspace.Workspace) error) error {
	if f.updateErr != nil {
		return f.updateErr
	}
	ws, ok := f.workspaces[id]
	if !ok {
		return errors.New("workspace not found")
	}
	return fn(ws)
}

func newTestService(t *testing.T) (*Service, *fakeWorkspaceStore) {
	t.Helper()
	store, _ := newTestStore(t)
	workspaces := newFakeWorkspaceStore("ws-1", "ws-2")
	return NewService(store, workspaces), workspaces
}

// inboxFixture is an isolated stand-in for the user's Downloads folder. Tests
// never touch the developer's real one.
func inboxFixture(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "Inbox")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestStatus_BeforeSetupIsSetupRequiredWithTheTemplateSuggestion(t *testing.T) {
	service, workspaces := newTestService(t)
	ws := workspaces.workspaces["ws-1"]
	ws.SetTemplateProvenance(&workspace.TemplateProvenance{
		TemplateID: "downloads-janitor",
		Builtin:    true,
		DirectoryRequirements: []workspace.DirectoryRequirement{{
			Key:              DirectoryRequirementKey,
			Label:            "Downloads folder",
			SuggestedPath:    "~/Downloads",
			AccessDisclosure: "Ori can list files here.",
		}},
		AutomationRecipes: []workspace.AutomationRecipe{{
			DirectoryKey: DirectoryRequirementKey,
			DailyScan:    &workspace.DailyScanRecipe{LocalTime: "09:00"},
		}},
	})

	status, err := service.Status("ws-1")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.Readiness.State != ReadinessSetupRequired {
		t.Fatalf("state = %q, want setup_required", status.Readiness.State)
	}
	if status.Suggestion == nil {
		t.Fatal("expected the template's folder suggestion for the setup card")
	}
	// Still a suggestion, not a resolution: no access exists yet.
	if status.Suggestion.SuggestedPath != "~/Downloads" {
		t.Fatalf("suggested path = %q, want the unresolved ~/Downloads", status.Suggestion.SuggestedPath)
	}
	if status.Suggestion.AccessDisclosure == "" {
		t.Fatal("the setup card needs the access disclosure before approval")
	}
	if status.Suggestion.FilingRootName != DefaultFilingRootName || status.Suggestion.DailyScanLocalTime != "09:00" {
		t.Fatalf("suggestion did not carry the destination/time to disclose: %+v", status.Suggestion)
	}
	if len(ws.DirectoryReferences) != 0 || len(ws.MCPBindings) != 0 {
		t.Fatal("merely viewing status must not link a folder or grant access")
	}
	for _, check := range status.Readiness.Checks {
		if check.Status == ComponentOK {
			t.Fatalf("no component may pass before setup: %+v", check)
		}
	}
}

func TestConfirmSetup_ConfiguresFolderAccessAndDestination(t *testing.T) {
	service, workspaces := newTestService(t)
	root := inboxFixture(t)

	status, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-1", Path: root})
	if err != nil {
		t.Fatalf("ConfirmSetup: %v", err)
	}
	if !status.Settings.IsSetUp() {
		t.Fatalf("workspace should be set up: %+v", status.Settings)
	}
	if status.Settings.RootPath != filepath.Clean(root) {
		t.Fatalf("root = %q, want %q", status.Settings.RootPath, root)
	}
	if status.Settings.ContentMode != ContentModeMetadataOnly {
		t.Fatalf("content inspection must stay off after folder approval, got %q", status.Settings.ContentMode)
	}

	// Filed exists; no category folder was created ahead of an approved move.
	filed := filepath.Join(root, DefaultFilingRootName)
	info, err := os.Stat(filed)
	if err != nil || !info.IsDir() {
		t.Fatalf("expected %s to exist: %v", filed, err)
	}
	entries, err := os.ReadDir(filed)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("no category folder may exist before an approved move, got %v", entries)
	}

	ws := workspaces.workspaces["ws-1"]
	if len(ws.DirectoryReferences) != 1 || filepath.Clean(ws.DirectoryReferences[0].Path) != filepath.Clean(root) {
		t.Fatalf("expected one directory reference for the root: %+v", ws.DirectoryReferences)
	}
	if ws.DirectoryReferences[0].ID != status.Settings.DirectoryReferenceID {
		t.Fatal("settings must point at the created directory reference")
	}
}

func TestConfirmSetup_GrantsOnlyReadToolsOverTheRoot(t *testing.T) {
	service, workspaces := newTestService(t)
	root := inboxFixture(t)

	if _, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-1", Path: root}); err != nil {
		t.Fatalf("ConfirmSetup: %v", err)
	}

	ws := workspaces.workspaces["ws-1"]
	var janitor *workspace.MCPBinding
	for i := range ws.MCPBindings {
		if ws.MCPBindings[i].Alias == JanitorBindingAlias {
			janitor = &ws.MCPBindings[i]
		}
	}
	if janitor == nil {
		t.Fatalf("expected a Janitor filesystem binding: %+v", ws.MCPBindings)
	}
	if janitor.AllowedTools == nil {
		t.Fatal("a nil allowlist means every tool; the Janitor binding must be explicit")
	}
	want := []string{"list_directory", "list_directory_with_sizes", "search_files", "get_file_info"}
	if len(janitor.AllowedTools) != len(want) {
		t.Fatalf("allowed tools = %v, want exactly %v", janitor.AllowedTools, want)
	}
	for _, tool := range want {
		if !slices.Contains(janitor.AllowedTools, tool) {
			t.Fatalf("missing read tool %q: %v", tool, janitor.AllowedTools)
		}
	}
	for _, forbidden := range forbiddenAgentTools {
		if slices.Contains(janitor.AllowedTools, forbidden) {
			t.Fatalf("binding exposes forbidden tool %q", forbidden)
		}
	}
	// The binding is scoped to the approved root and nothing else.
	roots := toStringSlice(janitor.Config["roots"])
	if len(roots) != 1 || filepath.Clean(roots[0]) != filepath.Clean(root) {
		t.Fatalf("binding roots = %v, want just the approved root", janitor.Config["roots"])
	}
}

// Linking a folder into a workspace that has no explicit filesystem binding
// must not widen Ori's synthesized all-tools binding to cover the Downloads
// root — that would hand the agent mutation tools over the user's downloads.
func TestConfirmSetup_DoesNotWidenSynthesizedFilesystemAccess(t *testing.T) {
	service, workspaces := newTestService(t)
	ws := workspaces.workspaces["ws-1"]
	workspaceFolder := t.TempDir()
	if err := ws.AddDirectoryReference(workspace.DirectoryReference{Name: "Workspace", Path: workspaceFolder}); err != nil {
		t.Fatal(err)
	}
	root := inboxFixture(t)

	if _, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-1", Path: root}); err != nil {
		t.Fatalf("ConfirmSetup: %v", err)
	}

	for _, binding := range ws.MCPBindings {
		if binding.Alias == JanitorBindingAlias {
			continue
		}
		// Every other filesystem binding must stay off the Downloads root.
		for _, key := range []string{"roots"} {
			for _, source := range []map[string]any{binding.Config, binding.Scope} {
				raw, ok := source[key]
				if !ok {
					continue
				}
				for _, r := range toStringSlice(raw) {
					if filepath.Clean(r) == filepath.Clean(root) {
						t.Fatalf("binding %q (allowed tools %v) was widened to the Downloads root", binding.Alias, binding.AllowedTools)
					}
				}
			}
		}
	}
}

func toStringSlice(raw any) []string {
	switch v := raw.(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func TestConfirmSetup_IsIdempotent(t *testing.T) {
	service, workspaces := newTestService(t)
	root := inboxFixture(t)

	first, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-1", Path: root})
	if err != nil {
		t.Fatalf("ConfirmSetup: %v", err)
	}
	second, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-1", Path: root + string(filepath.Separator)})
	if err != nil {
		t.Fatalf("ConfirmSetup (repeat): %v", err)
	}

	if first.Settings.DirectoryReferenceID != second.Settings.DirectoryReferenceID {
		t.Fatal("repeating setup must reuse the directory reference, not create a second one")
	}
	ws := workspaces.workspaces["ws-1"]
	if len(ws.DirectoryReferences) != 1 {
		t.Fatalf("expected one directory reference, got %+v", ws.DirectoryReferences)
	}
	janitorBindings := 0
	for _, binding := range ws.MCPBindings {
		if binding.Alias == JanitorBindingAlias {
			janitorBindings++
		}
	}
	if janitorBindings != 1 {
		t.Fatalf("expected exactly one Janitor binding, got %d", janitorBindings)
	}
}

// A binding edited to include a mutation tool must be repaired by re-running
// setup, and must not report Ready in the meantime.
func TestReadiness_DetectsAWidenedBinding(t *testing.T) {
	service, workspaces := newTestService(t)
	root := inboxFixture(t)
	if _, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-1", Path: root}); err != nil {
		t.Fatalf("ConfirmSetup: %v", err)
	}

	ws := workspaces.workspaces["ws-1"]
	for i := range ws.MCPBindings {
		if ws.MCPBindings[i].Alias == JanitorBindingAlias {
			ws.MCPBindings[i].AllowedTools = append(ws.MCPBindings[i].AllowedTools, "move_file")
		}
	}

	status, err := service.Status("ws-1")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.Readiness.State == ReadinessReady {
		t.Fatal("a binding exposing move_file must not be Ready")
	}
	found := false
	for _, check := range status.Readiness.Failing() {
		if check.Component == ComponentMCPBinding {
			found = true
			if check.Repair == "" {
				t.Error("a failing binding check needs a repair action")
			}
		}
	}
	if !found {
		t.Fatalf("expected a failing binding check: %+v", status.Readiness.Checks)
	}

	if _, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-1", Path: root}); err != nil {
		t.Fatalf("ConfirmSetup (repair): %v", err)
	}
	repaired, err := service.Status("ws-1")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	for _, check := range repaired.Readiness.Failing() {
		if check.Component == ComponentMCPBinding {
			t.Fatalf("re-running setup should repair the binding: %+v", check)
		}
	}
}

func TestConfirmSetup_RejectsUnusableSelections(t *testing.T) {
	service, _ := newTestService(t)
	file := filepath.Join(t.TempDir(), "not-a-folder.txt")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	cases := map[string]struct {
		path string
		code string
	}{
		"empty":     {"", CodeInvalidPath},
		"missing":   {filepath.Join(t.TempDir(), "nope"), CodeRootMissing},
		"is a file": {file, CodeNotADirectory},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-1", Path: tc.path})
			var setupError *SetupError
			if !errors.As(err, &setupError) {
				t.Fatalf("expected a SetupError, got %v", err)
			}
			if setupError.Code != tc.code {
				t.Fatalf("code = %q, want %q", setupError.Code, tc.code)
			}
			if setupError.Message == "" {
				t.Error("a setup failure must carry a user-facing message")
			}
		})
	}
}

func TestConfirmSetup_FailureLeavesNoPartialSetup(t *testing.T) {
	service, workspaces := newTestService(t)
	_, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-1", Path: filepath.Join(t.TempDir(), "missing")})
	if err == nil {
		t.Fatal("expected the setup to fail")
	}

	status, statusErr := service.Status("ws-1")
	if statusErr != nil {
		t.Fatalf("Status: %v", statusErr)
	}
	if status.Settings.IsSetUp() || status.Settings.RootPath != "" {
		t.Fatalf("a failed setup must persist nothing: %+v", status.Settings)
	}
	ws := workspaces.workspaces["ws-1"]
	if len(ws.DirectoryReferences) != 0 || len(ws.MCPBindings) != 0 {
		t.Fatal("a failed setup must not link a folder or grant access")
	}
}

func TestConfirmSetup_RejectsBadScheduleValues(t *testing.T) {
	service, _ := newTestService(t)
	root := inboxFixture(t)

	if _, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-1", Path: root, DailyScanLocalTime: "9am"}); err == nil {
		t.Fatal("expected a malformed daily time to be rejected")
	}
	if _, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-1", Path: root, Timezone: "Mars/Olympus"}); err == nil {
		t.Fatal("expected an unknown timezone to be rejected")
	}
	status, err := service.Status("ws-1")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.Settings.IsSetUp() {
		t.Fatal("a rejected setup must not configure the workspace")
	}
}

func TestConfirmSetup_ExpandsHomeOnlyOnConfirmation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, "Downloads"), 0o750); err != nil {
		t.Fatal(err)
	}
	service, _ := newTestService(t)

	status, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-1", Path: "~/Downloads"})
	if err != nil {
		t.Fatalf("ConfirmSetup: %v", err)
	}
	want := filepath.Join(home, "Downloads")
	if status.Settings.RootPath != want {
		t.Fatalf("root = %q, want the expanded %q", status.Settings.RootPath, want)
	}
	if strings.Contains(status.Settings.RootPath, "~") {
		t.Fatal("the stored root must be a normalized absolute path")
	}
}

func TestReadiness_ReportsAMissingRootWithARepair(t *testing.T) {
	service, _ := newTestService(t)
	root := inboxFixture(t)
	if _, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-1", Path: root}); err != nil {
		t.Fatalf("ConfirmSetup: %v", err)
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}

	status, err := service.Status("ws-1")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.Readiness.State != ReadinessNeedsAttention {
		t.Fatalf("state = %q, want needs_attention", status.Readiness.State)
	}
	var access ComponentCheck
	for _, check := range status.Readiness.Checks {
		if check.Component == ComponentDirectoryAccess {
			access = check
		}
	}
	if access.Status != ComponentFailed || access.Code != CodeRootMissing || access.Repair == "" {
		t.Fatalf("expected an actionable missing-root check, got %+v", access)
	}
	if strings.Contains(access.Message, root) {
		t.Fatalf("readiness messages must not leak absolute paths: %q", access.Message)
	}
}

// Ready is withheld while any required component is unchecked. Watcher and
// scheduler registration land in the automation group; until then the workspace
// must not claim to be running unattended.
func TestReadiness_NotReadyUntilEveryComponentPasses(t *testing.T) {
	service, _ := newTestService(t)
	root := inboxFixture(t)
	status, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-1", Path: root})
	if err != nil {
		t.Fatalf("ConfirmSetup: %v", err)
	}
	if status.Readiness.State == ReadinessReady {
		t.Fatal("Ready must not be reported while watcher/scheduler checks are pending")
	}
	for _, component := range []ReadinessComponent{ComponentDirectoryAccess, ComponentDestination, ComponentMCPBinding, ComponentPersistence} {
		found := false
		for _, check := range status.Readiness.Checks {
			if check.Component == component {
				found = true
				if check.Status != ComponentOK {
					t.Fatalf("%s should pass after setup: %+v", component, check)
				}
			}
		}
		if !found {
			t.Fatalf("missing check for %s", component)
		}
	}

	// With every component passing, the state resolves to Ready — proving the
	// only thing withholding it is the pending automation.
	var complete []ComponentCheck
	for _, component := range RequiredComponents {
		complete = append(complete, ComponentCheck{Component: component, Status: ComponentOK})
	}
	if got := DeriveReadinessState(true, complete); got != ReadinessReady {
		t.Fatalf("state with all checks OK = %q, want ready", got)
	}
}

func TestStatus_IsWorkspaceScoped(t *testing.T) {
	service, _ := newTestService(t)
	root := inboxFixture(t)
	if _, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-1", Path: root}); err != nil {
		t.Fatalf("ConfirmSetup: %v", err)
	}

	other, err := service.Status("ws-2")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if other.Settings.IsSetUp() || other.Settings.RootPath != "" {
		t.Fatalf("one workspace's setup must not configure another: %+v", other.Settings)
	}
}

func TestAppliesTo_OnlyDownloadsJanitorWorkspaces(t *testing.T) {
	service, workspaces := newTestService(t)

	if service.AppliesTo("ws-1") {
		t.Fatal("a plain workspace must not mount the Janitor surface")
	}

	workspaces.workspaces["ws-1"].SetTemplateProvenance(&workspace.TemplateProvenance{
		TemplateID: TemplateID,
		Builtin:    true,
	})
	if !service.AppliesTo("ws-1") {
		t.Fatal("a workspace created from the built-in template must mount the Janitor surface")
	}

	// A workspace that only carries the directory requirement (e.g. a
	// duplicated template) also counts.
	workspaces.workspaces["ws-2"].SetTemplateProvenance(&workspace.TemplateProvenance{
		TemplateID:            "my-copy",
		DirectoryRequirements: []workspace.DirectoryRequirement{{Key: DirectoryRequirementKey, Label: "Downloads"}},
	})
	if !service.AppliesTo("ws-2") {
		t.Fatal("a workspace declaring the Janitor directory requirement must mount the surface")
	}
}

// Pausing is a choice, not a fault. A workspace the user paused must not report
// "Needs attention": a badge that fires for the user's own deliberate action
// trains them to ignore it, and then a real permission loss goes unnoticed.
func TestReadiness_PausingIsNotAProblem(t *testing.T) {
	// Everything healthy except the two components a pause switches off.
	pausedButHealthy := []ComponentCheck{
		{Component: ComponentDirectoryAccess, Status: ComponentOK},
		{Component: ComponentDestination, Status: ComponentOK},
		{Component: ComponentMCPBinding, Status: ComponentOK},
		{Component: ComponentPersistence, Status: ComponentOK},
		{Component: ComponentWatcher, Status: ComponentPending},
		{Component: ComponentScheduler, Status: ComponentPending},
	}
	if got := DeriveReadinessStateWhenPaused(true, true, pausedButHealthy); got != ReadinessReady {
		t.Fatalf("a paused workspace is behaving as instructed, got %q", got)
	}
	// The same checks when the user did NOT pause mean something really is
	// wrong: the watcher failed to register.
	if got := DeriveReadinessStateWhenPaused(true, false, pausedButHealthy); got != ReadinessNeedsAttention {
		t.Fatalf("an unregistered watcher nobody asked for is a problem, got %q", got)
	}
	// And a pause never masks a failure elsewhere.
	broken := append([]ComponentCheck(nil), pausedButHealthy...)
	broken[0] = ComponentCheck{Component: ComponentDirectoryAccess, Status: ComponentFailed}
	if got := DeriveReadinessStateWhenPaused(true, true, broken); got != ReadinessNeedsAttention {
		t.Fatalf("a pause must not mask a real failure, got %q", got)
	}
}

// Pausing through the service keeps saying, per component, what is off and why.
func TestSetPaused_ExplainsWhatItSwitchedOff(t *testing.T) {
	service, _ := configuredService(t)
	status, err := service.SetPaused("ws-1", true)
	if err != nil {
		t.Fatalf("SetPaused: %v", err)
	}
	if !status.Settings.Paused {
		t.Fatal("it must report that it is paused")
	}
	for _, check := range status.Readiness.Checks {
		if check.Component != ComponentWatcher && check.Component != ComponentScheduler {
			continue
		}
		if !strings.Contains(strings.ToLower(check.Message), "paused") {
			t.Fatalf("%s must explain itself: %+v", check.Component, check)
		}
	}
}

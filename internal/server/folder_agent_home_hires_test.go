package server

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/folderlook"
	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/pluginhttp"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// setup-card-clarity: the card names the agents a Home step will hire before
// anything exists, from the provider's declaration in the reviewed release,
// under the names the run will use.

func TestHomeHireSet_NamesLikeTheRun(t *testing.T) {
	roles := []homeRoleDecl{
		{ID: "lead", Label: "Lead Role", Description: "Plans the work.", Required: true, Primary: true},
		{ID: "extra", Label: "Extra Role", Description: "Optional.", Required: false},
		{ID: "filed", Label: "Filed Role", Required: true},
		{ID: "second", Label: "Second Role", Description: "Keeps things.", Required: true},
	}
	free := func(string) bool { return false }

	got := homeHireSet(roles, "Portfolio Manager", map[string]bool{"filed": true}, free)
	want := []homeHire{
		// The primary role is hired under the Home's default name, not its label.
		{RoleID: "lead", Name: "Portfolio Manager", Base: "Portfolio Manager", Description: "Plans the work."},
		// An optional role and a filled one are not hired.
		{RoleID: "second", Name: "Second Role", Base: "Second Role", Description: "Keeps things."},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("hires = %+v, want %+v", got, want)
	}

	// With no default name the primary role keeps its label.
	if got := homeHireSet(roles[:1], "  ", nil, free); got[0].Name != "Lead Role" {
		t.Fatalf("no default name = %+v", got)
	}

	// An agent already holding the name is never adopted: a new one is hired
	// beside it, as "<name> 2", and the hire remembers what it replaced.
	taken := func(name string) bool { return strings.EqualFold(name, "portfolio manager") }
	got = homeHireSet(roles[:1], "Portfolio Manager", nil, taken)
	if got[0].Name != "Portfolio Manager 2" || got[0].Base != "Portfolio Manager" {
		t.Fatalf("a taken name = %+v", got)
	}
}

// hiresBuilder is a ServerBuilder just big enough for the folder card's Home
// facts: workspaces on disk, a plugin store, the fixed releases, and agents.
func hiresBuilder(t *testing.T, releases *homeReleases, agents ...string) (*ServerBuilder, *workspace.FileStore, string) {
	t.Helper()
	files, err := workspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = files.Close() })
	plugins := t.TempDir()
	held := &journeyAgentStore{agents: map[string]*agent.Agent{}}
	for _, name := range agents {
		held.agents[name] = &agent.Agent{}
	}
	b := &ServerBuilder{workspaceFileStore: files, workspaceStore: files, st: held,
		pluginHandler: pluginhttp.NewHandler(nil, nil, plugins), server: &Server{reviewedReleases: releases.updates()}}
	b.folderSetupHost = &folderSetupHost{builder: b}
	return b, files, plugins
}

// hireReleases publishes 0.6.0 and 0.5.0 of the provider; the Home it declares
// has a required primary role, a second required role and an optional one.
func hireReleases(t *testing.T) *homeReleases {
	t.Helper()
	releases := newHomeReleases(t, "0.6.0", "0.5.0")
	releases.primaryName = "Portfolio Manager"
	releases.roles = []projecttemplates.AssistantProgramHomeRole{
		{ID: "lead", Label: "Lead Role", Description: "Coordinates status and priorities, and explicit handoffs without inheriting project access.", Required: true, Primary: true, SystemPrompt: "SECRET PROMPT lead"},
		{ID: "optional", Label: "Optional Role", Description: "Not hired by setup.", SystemPrompt: "SECRET PROMPT optional"},
	}
	return releases
}

func TestFolderAgentHomeFacts_RolesBeforeTheHome(t *testing.T) {
	releases := hireReleases(t)
	provider := releases.provider
	b, files, plugins := hiresBuilder(t, releases)

	// Not installed, no Home: the roles come from the release the preview
	// inspected, and nothing is installed or created to read them.
	facts, ok := folderAgentHomeFacts(t.Context(), b, "local", provider.Key)
	if !ok || facts.HomeExists || facts.ProviderReady || facts.ProviderVersion != "0.6.0" {
		t.Fatalf("facts = %+v ok=%v", facts, ok)
	}
	if !facts.RolesKnown || !reflect.DeepEqual(facts.MissingRoles, []string{"Portfolio Manager"}) || len(facts.Hires) != 1 {
		t.Fatalf("roles before the Home = %+v", facts)
	}
	hire := facts.Hires[0]
	if hire.Name != "Portfolio Manager" || hire.Taken != "" || !strings.HasPrefix(hire.Job, "Coordinates status") {
		t.Fatalf("hire = %+v", hire)
	}
	if strings.Contains(hire.Job, "SECRET") {
		t.Fatalf("a system prompt reached the card's facts: %+v", hire)
	}
	if list, err := b.pluginHandler.Manager().List(); err != nil || len(list) != 0 {
		t.Fatalf("reading the roles installed something: %v %v", list, err)
	}
	if ids, err := files.List(); err != nil || len(ids) != 0 {
		t.Fatalf("reading the roles created a workspace: %v %v", ids, err)
	}

	// Installed at the reviewed release: the same roles, and the provider is ready.
	store := plugin.NewStore(plugins)
	if err := store.Put(plugin.InstalledPlugin{Name: provider.PluginID, Version: "0.6.0", Enabled: true,
		Format: provider.SourceFormat, Source: releases.candidates[0].Source}); err != nil {
		t.Fatal(err)
	}
	current, ok := folderAgentHomeFacts(t.Context(), b, "local", provider.Key)
	if !ok || !current.ProviderReady || !reflect.DeepEqual(current.Hires, facts.Hires) || !reflect.DeepEqual(current.MissingRoles, facts.MissingRoles) {
		t.Fatalf("installed facts = %+v ok=%v", current, ok)
	}

	// Installed at an older reviewed release: the run updates first, so the
	// roles are the release's, and the provider is not ready yet.
	if err := store.Put(plugin.InstalledPlugin{Name: provider.PluginID, Version: "0.5.0", Enabled: true,
		Format: provider.SourceFormat, Source: provider.ReleaseEntry().PinnedSource(provider.FallbackCommit)}); err != nil {
		t.Fatal(err)
	}
	older, ok := folderAgentHomeFacts(t.Context(), b, "local", provider.Key)
	if !ok || older.ProviderReady || older.ProviderVersion != "0.6.0" || !reflect.DeepEqual(older.Hires, facts.Hires) {
		t.Fatalf("older facts = %+v ok=%v", older, ok)
	}
}

func TestFolderAgentHomeFacts_ATakenNameIsHiredBesideIt(t *testing.T) {
	releases := hireReleases(t)
	b, _, _ := hiresBuilder(t, releases, "portfolio manager")
	facts, ok := folderAgentHomeFacts(t.Context(), b, "local", releases.provider.Key)
	if !ok || !facts.RolesKnown || len(facts.Hires) != 1 {
		t.Fatalf("facts = %+v ok=%v", facts, ok)
	}
	if hire := facts.Hires[0]; hire.Name != "Portfolio Manager 2" || hire.Taken != "Portfolio Manager" ||
		!reflect.DeepEqual(facts.MissingRoles, []string{"Portfolio Manager 2"}) {
		t.Fatalf("hire = %+v roles = %v", hire, facts.MissingRoles)
	}
}

func TestFolderAgentHomeFacts_AnUnreadableOrEmptyReleaseLeavesTheRolesUnknown(t *testing.T) {
	// The release cannot be inspected (offline, clone failed): the facts say so
	// and the plan keeps its generic lines.
	releases := hireReleases(t)
	releases.inspectErr[releases.candidates[0].Source] = errors.New("clone failed")
	b, _, _ := hiresBuilder(t, releases)
	if facts, ok := folderAgentHomeFacts(t.Context(), b, "local", releases.provider.Key); ok || facts.RolesKnown || facts.Hires != nil {
		t.Fatalf("an unreadable release gave facts = %+v ok=%v", facts, ok)
	}

	// A release whose Home hires nobody names none, and does not invent a roster.
	empty := newHomeReleases(t, "0.6.0", "0.5.0")
	empty.roles = []projecttemplates.AssistantProgramHomeRole{{ID: "optional", Label: "Optional Role", SystemPrompt: "x"}}
	b, _, _ = hiresBuilder(t, empty)
	facts, ok := folderAgentHomeFacts(t.Context(), b, "local", empty.provider.Key)
	if !ok || facts.RolesKnown || facts.Hires != nil || facts.MissingRoles != nil {
		t.Fatalf("a Home hiring nobody = %+v ok=%v", facts, ok)
	}
}

func TestFolderAgentHomeFacts_AnExistingHomeReadsItsOwnDeclaration(t *testing.T) {
	releases := hireReleases(t)
	provider := releases.provider
	b, files, _ := hiresBuilder(t, releases)
	owner := workspace.AssistantProgramHomeOwner{PluginID: provider.PluginID, PluginVersion: "0.6.0", ProgramID: provider.ProgramID,
		HomeSchemaVersion: 1, HomeVersion: 1, DeclarationDigest: strings.Repeat("a", 64), PluginGeneration: 2, ComponentFingerprint: strings.Repeat("b", 64)}
	decl := &workspace.AssistantProgramDeclaration{SchemaVersion: workspace.AssistantProgramSchemaVersion, ID: provider.ProgramID,
		StationName: "My Home", DefaultPrimaryName: "Portfolio Manager",
		Roles: []workspace.AssistantProgramRoleSpec{
			{ID: "lead", Label: "Lead Role", Description: "Plans the work.", Scope: workspace.AssistantRoleScopeHome, Required: true, Primary: true, SystemPrompt: "x"},
			{ID: "second", Label: "Second Role", Description: "Keeps things.", Scope: workspace.AssistantRoleScopeHome, Required: true, SystemPrompt: "x"},
			{ID: "optional", Label: "Optional Role", Scope: workspace.AssistantRoleScopeHome, SystemPrompt: "x"},
		}}
	if _, _, err := workspace.NewAssistantProgramStore(files).EnsureNamedIndependentStation(
		workspace.AssistantProgramKey{OwnerUserID: "local", PluginID: provider.PluginID, ProgramID: provider.ProgramID}, decl, "My Home", owner); err != nil {
		t.Fatal(err)
	}
	facts, ok := folderAgentHomeFacts(t.Context(), b, "local", provider.Key)
	if !ok || !facts.HomeExists || !facts.RolesKnown {
		t.Fatalf("facts = %+v ok=%v", facts, ok)
	}
	want := []folderlook.HomeHire{
		{Name: "Portfolio Manager", Job: "Plans the work."},
		{Name: "Second Role", Job: "Keeps things."},
	}
	if !reflect.DeepEqual(facts.Hires, want) || !reflect.DeepEqual(facts.MissingRoles, []string{"Portfolio Manager", "Second Role"}) {
		t.Fatalf("hires = %+v roles = %v", facts.Hires, facts.MissingRoles)
	}
}

func TestMissingStillNamesTheRolesTheRunHires(t *testing.T) {
	// Missing is the run's own list and now reads through the same helper: the
	// card and the run cannot name a hire differently.
	releases := hireReleases(t)
	provider := releases.provider
	b, files, _ := hiresBuilder(t, releases, "Portfolio Manager")
	owner := workspace.AssistantProgramHomeOwner{PluginID: provider.PluginID, PluginVersion: "0.6.0", ProgramID: provider.ProgramID,
		HomeSchemaVersion: 1, HomeVersion: 1, DeclarationDigest: strings.Repeat("a", 64), PluginGeneration: 2, ComponentFingerprint: strings.Repeat("b", 64)}
	decl := &workspace.AssistantProgramDeclaration{SchemaVersion: workspace.AssistantProgramSchemaVersion, ID: provider.ProgramID,
		StationName: "My Home", DefaultPrimaryName: "Portfolio Manager",
		Roles: []workspace.AssistantProgramRoleSpec{
			{ID: "lead", Label: "Lead Role", Scope: workspace.AssistantRoleScopeHome, Required: true, Primary: true, SystemPrompt: "x"},
		}}
	home, _, err := workspace.NewAssistantProgramStore(files).EnsureNamedIndependentStation(
		workspace.AssistantProgramKey{OwnerUserID: "local", PluginID: provider.PluginID, ProgramID: provider.ProgramID}, decl, "My Home", owner)
	if err != nil {
		t.Fatal(err)
	}
	roles, err := portfolioHomeStaffing{builder: b}.Missing(t.Context(), home.ID)
	if err != nil || len(roles) != 1 || roles[0].RoleID != "lead" || roles[0].Name != "Portfolio Manager 2" {
		t.Fatalf("Missing = %+v %v", roles, err)
	}
}

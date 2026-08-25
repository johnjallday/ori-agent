package pluginworkspace

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacecapability"
)

// fakePlugins is an in-memory PluginManager built from a temporary fixture set,
// so tests never read the developer's ignored plugins/ registry.
type fakePlugins struct {
	list      []plugin.InstalledPlugin
	enableErr error
	enabled   map[string]bool
}

func (f *fakePlugins) List() ([]plugin.InstalledPlugin, error) { return f.list, nil }

func (f *fakePlugins) SetEnabled(name string, enabled bool) error {
	if f.enableErr != nil {
		return f.enableErr
	}
	if f.enabled == nil {
		f.enabled = map[string]bool{}
	}
	f.enabled[name] = enabled
	for i := range f.list {
		if f.list[i].Name == name {
			f.list[i].Enabled = enabled
		}
	}
	return nil
}

// fakeStore holds one workspace in memory and can be told to fail on Save.
type fakeStore struct {
	ws      *workspace.Workspace
	saveErr error
	saves   int
}

func (f *fakeStore) Get(id string) (*workspace.Workspace, error) {
	if f.ws == nil || f.ws.ID != id {
		return nil, nil
	}
	return f.ws, nil
}

func (f *fakeStore) Save(ws *workspace.Workspace) error {
	f.saves++
	if f.saveErr != nil {
		return f.saveErr
	}
	f.ws = ws
	return nil
}

func newWS() *workspace.Workspace {
	return workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Test"})
}

func reaperPlugin(enabled bool) plugin.InstalledPlugin {
	return plugin.InstalledPlugin{
		Name:       "reaper-plugin",
		Enabled:    enabled,
		Skills:     []string{"reaper-session-setup", "reaper-web-remote"},
		MCPServers: nil,
	}
}

func skillNames(ws *workspace.Workspace) []string {
	var out []string
	for _, b := range ws.GetSkillBindings() {
		out = append(out, strings.ToLower(b.SkillName))
	}
	return out
}

func hasSkill(ws *workspace.Workspace, name string) bool {
	return slices.Contains(skillNames(ws), strings.ToLower(name))
}

func contributedPluginCapability(t *testing.T, ws *workspace.Workspace) (plugin.InstalledPlugin, workspacecapability.Definition) {
	t.Helper()
	owner := workspace.CapabilityOwner{
		Kind: workspace.CapabilityOwnerPlugin, PluginID: "demo-plugin", PluginVersion: "1.0.0",
	}
	if _, err := ws.AddInstalledCapability(workspace.InstalledCapability{
		ID: "demo-tools", Version: 1, Source: workspace.InstallSourceInPlace, Owner: &owner,
	}); err != nil {
		t.Fatal(err)
	}
	installed := plugin.InstalledPlugin{
		Name: "demo-plugin", Version: "1.0.0", Enabled: true,
		Skills: []string{"demo-skill"}, MCPServers: []string{"demo-plugin/demo-mcp"},
		WorkspaceSurfaces: &plugin.SurfaceContribution{Capabilities: []plugin.ContributedCapability{{ID: "demo-tools", Version: 1}}},
	}
	definition := workspacecapability.Definition{
		ID: "demo-tools", Version: 1, Owner: &owner,
		Display: workspacecapability.Display{Name: "Demo Tools"},
	}
	return installed, definition
}

func TestCapabilityReconcileAttachesAndDetachesOnlyOwningPluginResources(t *testing.T) {
	ws := newWS()
	installed, definition := contributedPluginCapability(t, ws)
	// A pre-existing user binding with the same skill is shared and must survive
	// capability removal.
	if err := ws.UpsertSkillBinding(workspace.SkillBinding{ID: "user-skill", SkillName: "demo-skill", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	store := &fakeStore{ws: ws}
	reconciler := New(&fakePlugins{list: []plugin.InstalledPlugin{installed}}, store)
	if err := reconciler.AttachCapability(ws.ID, definition); err != nil {
		t.Fatal(err)
	}
	if !hasSkill(ws, "demo-skill") || len(ws.GetMCPBindings()) != 1 {
		t.Fatalf("bindings after attach: skills=%+v mcp=%+v", ws.GetSkillBindings(), ws.GetMCPBindings())
	}
	record, _ := ws.GetInstalledCapability("demo-tools")
	if len(record.OwnedResources) != 2 {
		t.Fatalf("owned resources = %+v", record.OwnedResources)
	}
	if err := reconciler.DetachCapability(ws.ID, definition); err != nil {
		t.Fatal(err)
	}
	if !hasSkill(ws, "demo-skill") {
		t.Fatal("detach removed the user's shared skill binding")
	}
	if len(ws.GetMCPBindings()) != 0 {
		t.Fatalf("detach left plugin-created MCP binding: %+v", ws.GetMCPBindings())
	}
}

func TestCapabilityReconcileRejectsForeignWorkspaceAndDisabledOwner(t *testing.T) {
	ws := newWS()
	installed, definition := contributedPluginCapability(t, ws)
	foreign := newWS()
	foreign.ID = "foreign-workspace"
	reconciler := New(&fakePlugins{list: []plugin.InstalledPlugin{installed}}, &fakeStore{ws: foreign})
	if err := reconciler.AttachCapability(foreign.ID, definition); err == nil {
		t.Fatal("foreign workspace without the owner-aware record was attached")
	}

	installed.Enabled = false
	reconciler = New(&fakePlugins{list: []plugin.InstalledPlugin{installed}}, &fakeStore{ws: ws})
	if err := reconciler.AttachCapability(ws.ID, definition); err == nil {
		t.Fatal("disabled owning plugin was attached")
	}
}

func TestReconcile_MissingPlugin(t *testing.T) {
	ws := newWS()
	store := &fakeStore{ws: ws}
	r := New(&fakePlugins{}, store)

	res, err := r.Reconcile(Request{WorkspaceID: ws.ID, Plugins: []string{"reaper-plugin"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Plugins) != 1 || res.Plugins[0].State != PluginStateMissing {
		t.Fatalf("expected missing state, got %+v", res.Plugins)
	}
	if store.saves != 0 {
		t.Fatalf("missing plugin should not save, saves=%d", store.saves)
	}
	if len(res.Applied) != 0 {
		t.Fatalf("nothing should be applied, got %v", res.Applied)
	}
}

func TestReconcile_DisabledNotEnabledWithoutPermission(t *testing.T) {
	ws := newWS()
	store := &fakeStore{ws: ws}
	pm := &fakePlugins{list: []plugin.InstalledPlugin{reaperPlugin(false)}}
	r := New(pm, store)

	res, err := r.Reconcile(Request{WorkspaceID: ws.ID, Plugins: []string{"reaper-plugin"}, AllowEnable: false})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pr := res.Plugins[0]
	if pr.State != PluginStateDisabled {
		t.Fatalf("expected disabled state, got %s", pr.State)
	}
	if hasSkill(ws, "reaper-session-setup") {
		t.Fatalf("disabled plugin must not attach components")
	}
	if pm.enabled["reaper-plugin"] {
		t.Fatalf("template application must not enable a disabled plugin")
	}
	if len(pr.Missing) != 2 {
		t.Fatalf("expected 2 missing components, got %d", len(pr.Missing))
	}
}

func TestReconcile_DisabledEnabledWithPermission(t *testing.T) {
	ws := newWS()
	store := &fakeStore{ws: ws}
	pm := &fakePlugins{list: []plugin.InstalledPlugin{reaperPlugin(false)}}
	r := New(pm, store)

	res, err := r.Reconcile(Request{WorkspaceID: ws.ID, Plugins: []string{"reaper-plugin"}, AllowEnable: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := res.Plugins[0].State; got != PluginStateAttached {
		t.Fatalf("expected attached state after enable, got %s", got)
	}
	if !pm.enabled["reaper-plugin"] {
		t.Fatalf("AllowEnable should have enabled the plugin")
	}
	if !hasSkill(ws, "reaper-session-setup") || !hasSkill(ws, "reaper-web-remote") {
		t.Fatalf("both skills should be attached, got %v", skillNames(ws))
	}
}

func TestReconcile_FullAttachAndIdempotent(t *testing.T) {
	ws := newWS()
	store := &fakeStore{ws: ws}
	pm := &fakePlugins{list: []plugin.InstalledPlugin{reaperPlugin(true)}}
	r := New(pm, store)

	res1, err := r.Reconcile(Request{WorkspaceID: ws.ID, Plugins: []string{"reaper-plugin"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res1.Plugins[0].State != PluginStateAttached || len(res1.Applied) != 2 {
		t.Fatalf("first pass should attach 2, got state=%s applied=%v", res1.Plugins[0].State, res1.Applied)
	}

	// Second pass: no new bindings, no duplicate, no save.
	savesBefore := store.saves
	res2, err := r.Reconcile(Request{WorkspaceID: ws.ID, Plugins: []string{"reaper-plugin"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res2.Applied) != 0 {
		t.Fatalf("second pass should apply nothing, got %v", res2.Applied)
	}
	if res2.Plugins[0].State != PluginStateAttached {
		t.Fatalf("second pass should be attached, got %s", res2.Plugins[0].State)
	}
	if store.saves != savesBefore {
		t.Fatalf("idempotent pass should not save again: before=%d after=%d", savesBefore, store.saves)
	}
	if len(ws.GetSkillBindings()) != 2 {
		t.Fatalf("expected exactly 2 skill bindings, got %d", len(ws.GetSkillBindings()))
	}
}

func TestReconcile_PreservesUnrelatedBindings(t *testing.T) {
	ws := newWS()
	// Pre-existing unrelated binding.
	if err := ws.UpsertSkillBinding(workspace.SkillBinding{ID: "keep", SkillName: "note-taking", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	store := &fakeStore{ws: ws}
	pm := &fakePlugins{list: []plugin.InstalledPlugin{reaperPlugin(true)}}
	r := New(pm, store)

	if _, err := r.Reconcile(Request{WorkspaceID: ws.ID, Plugins: []string{"reaper-plugin"}}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hasSkill(ws, "note-taking") {
		t.Fatalf("unrelated binding must be preserved")
	}
	if len(ws.GetSkillBindings()) != 3 {
		t.Fatalf("expected 3 bindings (1 unrelated + 2 plugin), got %d", len(ws.GetSkillBindings()))
	}
}

func TestReconcile_CaseInsensitiveDedup(t *testing.T) {
	ws := newWS()
	// Existing binding in a different case than what the plugin records.
	if err := ws.UpsertSkillBinding(workspace.SkillBinding{ID: "pre", SkillName: "Reaper-Session-Setup", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	store := &fakeStore{ws: ws}
	pm := &fakePlugins{list: []plugin.InstalledPlugin{reaperPlugin(true)}}
	r := New(pm, store)

	res, err := r.Reconcile(Request{WorkspaceID: ws.ID, Plugins: []string{"reaper-plugin"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Only reaper-web-remote should be newly applied; the case variant is deduped.
	if len(res.Plugins[0].Applied) != 1 || res.Plugins[0].Applied[0].Name != "reaper-web-remote" {
		t.Fatalf("expected only reaper-web-remote applied, got %+v", res.Plugins[0].Applied)
	}
	if len(ws.GetSkillBindings()) != 2 {
		t.Fatalf("expected 2 bindings (no case-dup), got %d", len(ws.GetSkillBindings()))
	}
}

func TestReconcile_SaveFailureReportsNoSuccess(t *testing.T) {
	ws := newWS()
	store := &fakeStore{ws: ws, saveErr: errors.New("disk full")}
	pm := &fakePlugins{list: []plugin.InstalledPlugin{reaperPlugin(true)}}
	r := New(pm, store)

	res, err := r.Reconcile(Request{WorkspaceID: ws.ID, Plugins: []string{"reaper-plugin"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.SaveErr == nil {
		t.Fatalf("expected SaveErr to be reported")
	}
	if len(res.Applied) != 0 {
		t.Fatalf("save failure must not report applied components, got %v", res.Applied)
	}
	if res.Plugins[0].State != PluginStateDetached {
		t.Fatalf("expected detached state after save failure, got %s", res.Plugins[0].State)
	}
	if len(res.Plugins[0].Missing) != 2 {
		t.Fatalf("both components should be reported missing after save failure, got %d", len(res.Plugins[0].Missing))
	}
}

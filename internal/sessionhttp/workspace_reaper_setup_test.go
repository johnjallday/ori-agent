package sessionhttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/pluginworkspace"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/reapersetup"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type fakePluginMgr struct{ list []plugin.InstalledPlugin }

func (f *fakePluginMgr) List() ([]plugin.InstalledPlugin, error) { return f.list, nil }
func (f *fakePluginMgr) SetEnabled(string, bool) error           { return nil }

// reaperSetupHandler wires a session handler with an in-memory workspace store, a
// reconciler over a fixture plugin manager, and the readiness resolver.
func reaperSetupHandler(t *testing.T, plugins []plugin.InstalledPlugin) (*Handler, *workspace.InMemoryStore) {
	t.Helper()
	store := workspace.NewInMemoryStore()
	pm := &fakePluginMgr{list: plugins}
	rec := pluginworkspace.New(pm, store)
	resolver := reapersetup.NewResolver(store, rec)
	repairer := reapersetup.NewRepairer(store, rec, resolver)
	h := New(nil)
	h.SetWorkspaceTaskStore(store)
	h.SetReaperSetup(resolver, pm, rec, repairer)
	return h, store
}

func TestHandleReaperReadiness_OriReady(t *testing.T) {
	h, store := reaperSetupHandler(t, []plugin.InstalledPlugin{
		{Name: "reaper-plugin", Enabled: true, Skills: []string{"reaper-session-setup"}},
	})
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Song", Agents: []string{"Reaper Producer"}})
	ws.SetTemplateProvenance(&workspace.TemplateProvenance{TemplateID: reapersetup.ReaperSongTemplateID})
	ws.AllowNativeMCPCLI = true
	if err := ws.UpsertSkillBinding(workspace.SkillBinding{ID: "b1", SkillName: "reaper-session-setup", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	ws.Tasks = append(ws.Tasks, workspace.Task{ID: "s1", To: "Reaper Producer", Status: workspace.TaskStatusPending, Context: map[string]any{reapersetup.TaskContextTemplateSetup: true}})
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}
	yes := true
	if err := store.SaveWorkspaceAgent(ws.ID, "Reaper Producer", &agent.Agent{Type: "orchestrator", Settings: types.Settings{Provider: "codex", AllowNativeMCPTools: &yes}}); err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/"+ws.ID+"/reaper-setup", nil)
	h.handleReaperReadiness(rr, req, ws.ID)

	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	var got reapersetup.Readiness
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Status != reapersetup.StatusOriReady {
		t.Fatalf("expected ori_ready, got %s", got.Status)
	}
	if got.LiveVerification != "not_checked" {
		t.Fatalf("readiness must never claim REAPER connected; live_verification=%q", got.LiveVerification)
	}
}

func TestHandleReaperReadiness_PluginMissingStaysFileOnly(t *testing.T) {
	h, store := reaperSetupHandler(t, nil) // no plugins installed
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Song", Agents: []string{"Reaper Producer"}})
	ws.SetTemplateProvenance(&workspace.TemplateProvenance{TemplateID: reapersetup.ReaperSongTemplateID})
	ws.Tasks = append(ws.Tasks, workspace.Task{ID: "s1", To: "Reaper Producer", Status: workspace.TaskStatusPending, Context: map[string]any{reapersetup.TaskContextTemplateSetup: true}})
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/"+ws.ID+"/reaper-setup", nil)
	h.handleReaperReadiness(rr, req, ws.ID)

	var got reapersetup.Readiness
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Status != reapersetup.StatusPluginMissing {
		t.Fatalf("expected plugin_missing, got %s", got.Status)
	}
	if got.ProjectMode != "file_only" {
		t.Fatalf("expected file_only project mode, got %s", got.ProjectMode)
	}
}

func TestHandleReaperRepair_PreviewAndApply(t *testing.T) {
	h, store := reaperSetupHandler(t, []plugin.InstalledPlugin{
		{Name: "reaper-plugin", Enabled: true, Skills: []string{"reaper-session-setup", "reaper-web-remote"}},
	})
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Song", Agents: []string{"Reaper Producer"}})
	ws.SetTemplateProvenance(&workspace.TemplateProvenance{TemplateID: reapersetup.ReaperSongTemplateID})
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}

	// Preview: detached -> plan to attach both components.
	rr := httptest.NewRecorder()
	h.handleReaperRepair(rr, httptest.NewRequest(http.MethodGet, "/api/workspaces/"+ws.ID+"/reaper-setup/repair", nil), ws.ID)
	var plan reapersetup.RepairPlan
	if err := json.Unmarshal(rr.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	if !plan.Identified || len(plan.AttachPlan) != 2 {
		t.Fatalf("expected a 2-component attach plan, got %+v", plan)
	}

	// Apply: attaches components.
	rr2 := httptest.NewRecorder()
	h.handleReaperRepair(rr2, httptest.NewRequest(http.MethodPost, "/api/workspaces/"+ws.ID+"/reaper-setup/repair", nil), ws.ID)
	var res reapersetup.RepairResult
	if err := json.Unmarshal(rr2.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Attached) != 2 {
		t.Fatalf("expected 2 components attached, got %+v", res)
	}
	got, _ := store.Get(ws.ID)
	if len(got.GetSkillBindings()) != 2 {
		t.Fatalf("repair should leave 2 skill bindings, got %d", len(got.GetSkillBindings()))
	}
}

func TestUnsatisfiedRequiredPlugins(t *testing.T) {
	tools := projecttemplates.ToolDefaults{Plugins: []string{"reaper-plugin"}}

	// Missing plugin.
	h, _ := reaperSetupHandler(t, nil)
	if missing, disabled := h.unsatisfiedRequiredPlugins(tools); len(missing) != 1 || missing[0] != "reaper-plugin" || len(disabled) != 0 {
		t.Fatalf("missing plugin: got missing=%v disabled=%v", missing, disabled)
	}

	// Installed but disabled.
	h2, _ := reaperSetupHandler(t, []plugin.InstalledPlugin{{Name: "reaper-plugin", Enabled: false}})
	if missing, disabled := h2.unsatisfiedRequiredPlugins(tools); len(disabled) != 1 || disabled[0] != "reaper-plugin" || len(missing) != 0 {
		t.Fatalf("disabled plugin: got missing=%v disabled=%v", missing, disabled)
	}

	// Installed and enabled: satisfied.
	h3, _ := reaperSetupHandler(t, []plugin.InstalledPlugin{{Name: "reaper-plugin", Enabled: true}})
	if missing, disabled := h3.unsatisfiedRequiredPlugins(tools); len(missing)+len(disabled) != 0 {
		t.Fatalf("enabled plugin should be satisfied: missing=%v disabled=%v", missing, disabled)
	}

	// Template declares no plugins: never blocks.
	if missing, disabled := h.unsatisfiedRequiredPlugins(projecttemplates.ToolDefaults{}); len(missing)+len(disabled) != 0 {
		t.Fatalf("no declared plugins should never block")
	}
}

func TestGetReaperCreatePreview(t *testing.T) {
	h, _ := reaperSetupHandler(t, []plugin.InstalledPlugin{
		{Name: "reaper-plugin", Enabled: true, Skills: []string{"reaper-session-setup", "reaper-web-remote"}},
	})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/reaper-setup/preview", nil)
	h.GetReaperCreatePreview(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	var got reapersetup.CreatePreview
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Status != "ready_to_attach" || len(got.WouldAttach) != 2 {
		t.Fatalf("expected ready_to_attach with 2 components, got %s / %v", got.Status, got.WouldAttach)
	}
}

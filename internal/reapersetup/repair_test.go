package reapersetup

import (
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/pluginworkspace"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// repairEnv wires a repairer over an in-memory workspace store and a fixture
// plugin manager.
func repairEnv(t *testing.T, plugins []plugin.InstalledPlugin) (*Repairer, *workspace.InMemoryStore, *fakePM) {
	t.Helper()
	store := workspace.NewInMemoryStore()
	pm := &fakePM{list: plugins, enabled: map[string]bool{}}
	rec := pluginworkspace.New(pm, store)
	resolver := NewResolver(store, rec)
	return NewRepairer(store, rec, resolver), store, pm
}

type fakePM struct {
	list    []plugin.InstalledPlugin
	enabled map[string]bool
}

func (f *fakePM) List() ([]plugin.InstalledPlugin, error) { return f.list, nil }
func (f *fakePM) SetEnabled(name string, enabled bool) error {
	f.enabled[name] = enabled
	for i := range f.list {
		if f.list[i].Name == name {
			f.list[i].Enabled = enabled
		}
	}
	return nil
}

func reaperRepairWS(t *testing.T, store *workspace.InMemoryStore) *workspace.Workspace {
	t.Helper()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Song", Agents: []string{"Reaper Producer"}})
	ws.SetTemplateProvenance(&workspace.TemplateProvenance{TemplateID: ReaperSongTemplateID})
	_ = workspace.SetProjectEntryPath(ws.SharedData, "Song.rpp")
	ws.Tasks = append(ws.Tasks, workspace.Task{ID: "s1", To: "Reaper Producer", Status: workspace.TaskStatusPending, Context: map[string]any{TaskContextTemplateSetup: true}})
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}
	return ws
}

func TestRepair_DetachedAttachesMissing(t *testing.T) {
	rp, store, _ := repairEnv(t, []plugin.InstalledPlugin{
		{Name: "reaper-plugin", Enabled: true, Skills: []string{"reaper-session-setup", "reaper-web-remote"}},
	})
	ws := reaperRepairWS(t, store)
	// One of two components already attached => detached.
	_ = ws.UpsertSkillBinding(workspace.SkillBinding{ID: "b1", SkillName: "reaper-session-setup", Enabled: true})
	_ = store.Save(ws)

	res, err := rp.Apply(ws.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Attached) != 1 || res.Attached[0].Name != "reaper-web-remote" {
		t.Fatalf("expected reaper-web-remote attached, got %+v", res.Attached)
	}
	// Idempotent: second apply is a no-op.
	res2, _ := rp.Apply(ws.ID, false)
	if !res2.NoOp || len(res2.Attached) != 0 {
		t.Fatalf("second repair should be a no-op, got %+v", res2)
	}
}

func TestRepair_DisabledRequiresConfirmation(t *testing.T) {
	rp, store, pm := repairEnv(t, []plugin.InstalledPlugin{
		{Name: "reaper-plugin", Enabled: false, Skills: []string{"reaper-session-setup"}},
	})
	ws := reaperRepairWS(t, store)

	// Without confirmation: does not enable, does not attach.
	res, _ := rp.Apply(ws.ID, false)
	if !res.NeedsConfirm {
		t.Fatalf("disabled plugin repair must require confirmation, got %+v", res)
	}
	if pm.enabled["reaper-plugin"] {
		t.Fatalf("must not enable without confirmation")
	}

	// With confirmation: enables and attaches.
	res2, _ := rp.Apply(ws.ID, true)
	if !res2.Enabled || len(res2.Attached) != 1 {
		t.Fatalf("confirmed repair should enable+attach, got %+v", res2)
	}
}

func TestRepair_MissingPluginNeedsInstall(t *testing.T) {
	rp, store, _ := repairEnv(t, nil)
	ws := reaperRepairWS(t, store)
	res, _ := rp.Apply(ws.ID, true)
	if !res.NeedsInstall || len(res.Attached) != 0 {
		t.Fatalf("missing plugin should report needs_install and attach nothing, got %+v", res)
	}
}

func TestRepair_NotIdentifiedNoOp(t *testing.T) {
	rp, store, _ := repairEnv(t, []plugin.InstalledPlugin{{Name: "reaper-plugin", Enabled: true, Skills: []string{"reaper-session-setup"}}})
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Plain"})
	_ = store.Save(ws)
	res, _ := rp.Apply(ws.ID, true)
	if res.Identified {
		t.Fatalf("plain workspace must not be repairable")
	}
}

// TestRepair_ForbiddenMutations proves repair never touches native access, the
// agent definition, the task set, or the .rpp project entry.
func TestRepair_ForbiddenMutations(t *testing.T) {
	rp, store, _ := repairEnv(t, []plugin.InstalledPlugin{
		{Name: "reaper-plugin", Enabled: true, Skills: []string{"reaper-session-setup", "reaper-web-remote"}},
	})
	ws := reaperRepairWS(t, store)
	no := false
	_ = store.SaveWorkspaceAgent(ws.ID, "Reaper Producer", &agent.Agent{Type: "orchestrator", Settings: types.Settings{Provider: "openai", AllowNativeMCPTools: &no}})

	before, _ := store.Get(ws.ID)
	wsNativeBefore := before.AllowNativeMCPCLI
	taskCountBefore := len(before.Tasks)
	entryBefore, _ := workspace.GetProjectEntryPath(before.SharedData)

	if _, err := rp.Apply(ws.ID, true); err != nil {
		t.Fatal(err)
	}

	after, _ := store.Get(ws.ID)
	if after.AllowNativeMCPCLI != wsNativeBefore {
		t.Fatalf("repair must not change workspace native-CLI access")
	}
	ag, _, _ := store.GetWorkspaceAgent(ws.ID, "Reaper Producer")
	if ag.Settings.Provider != "openai" || ag.Settings.IsNativeMCPToolsAllowed() {
		t.Fatalf("repair must not mutate the agent provider or native-access opt-in")
	}
	if len(after.Tasks) != taskCountBefore {
		t.Fatalf("repair must not create or remove tasks")
	}
	entryAfter, _ := workspace.GetProjectEntryPath(after.SharedData)
	if entryAfter != entryBefore {
		t.Fatalf("repair must not touch the .rpp project entry")
	}
}

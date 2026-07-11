package reapersetup

import (
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/pluginworkspace"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// fakeReader serves one workspace and a per-agent snapshot.
type fakeReader struct {
	ws     *workspace.Workspace
	agents map[string]*agent.Agent
}

func (f *fakeReader) Get(id string) (*workspace.Workspace, error) {
	if f.ws == nil || f.ws.ID != id {
		return nil, nil
	}
	return f.ws, nil
}

func (f *fakeReader) GetWorkspaceAgent(workspaceID, name string) (*agent.Agent, bool, error) {
	ag, ok := f.agents[name]
	return ag, ok, nil
}

// fakeInspector returns a canned plugin state.
type fakeInspector struct{ result pluginworkspace.PluginResult }

func (f *fakeInspector) Inspect(string, []string) ([]pluginworkspace.PluginResult, error) {
	return []pluginworkspace.PluginResult{f.result}, nil
}

func cliAgent(provider string, native bool) *agent.Agent {
	return &agent.Agent{Settings: types.Settings{Provider: provider, AllowNativeMCPTools: &native}}
}

// reaperWS builds a workspace identified as REAPER (via provenance) with an entry
// agent and a pending setup task assigned to it.
func reaperWS(t *testing.T) *workspace.Workspace {
	t.Helper()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Song", Agents: []string{"Reaper Producer"}})
	ws.SetTemplateProvenance(&workspace.TemplateProvenance{TemplateID: ReaperSongTemplateID, Builtin: true, Version: 4})
	ws.Tasks = append(ws.Tasks, workspace.Task{
		ID:      "setup-1",
		To:      "Reaper Producer",
		Status:  workspace.TaskStatusPending,
		Context: map[string]any{TaskContextTemplateSetup: true},
	})
	return ws
}

func attachedPlugin() pluginworkspace.PluginResult {
	return pluginworkspace.PluginResult{
		Name: ReaperPluginName, Installed: true, Enabled: true,
		State:    pluginworkspace.PluginStateAttached,
		Attached: []pluginworkspace.Component{{Kind: "skill", Name: "reaper-session-setup"}},
	}
}

func TestResolve_NotIdentified(t *testing.T) {
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Plain"})
	r := NewResolver(&fakeReader{ws: ws}, &fakeInspector{result: attachedPlugin()})
	got, err := r.Resolve(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Identified {
		t.Fatalf("plain workspace must not be identified as REAPER")
	}
}

func TestResolve_IdentificationSignals(t *testing.T) {
	// setup-task provenance only.
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Song"})
	ws.Tasks = append(ws.Tasks, workspace.Task{
		ID:          "t1",
		TemplateRef: &workspace.TaskTemplateRef{TemplateID: ReaperSongTemplateID},
		Context:     map[string]any{},
	})
	r := NewResolver(&fakeReader{ws: ws}, &fakeInspector{result: attachedPlugin()})
	got, _ := r.Resolve(ws.ID)
	if !got.Identified || got.IdentifiedBy != "setup_task" {
		t.Fatalf("expected setup_task identification, got %+v", got.IdentifiedBy)
	}

	// tag alone is NOT sufficient.
	ws2 := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Song"})
	ws2.Tags = []string{"reaper"}
	r2 := NewResolver(&fakeReader{ws: ws2}, &fakeInspector{result: attachedPlugin()})
	got2, _ := r2.Resolve(ws2.ID)
	if got2.Identified {
		t.Fatalf("reaper tag alone must not identify a REAPER workspace")
	}
}

func TestResolve_StatusPrecedence(t *testing.T) {
	cases := []struct {
		name   string
		plugin pluginworkspace.PluginResult
		agent  *agent.Agent
		wsNCLI bool
		want   Status
	}{
		{
			name:   "missing beats all",
			plugin: pluginworkspace.PluginResult{Name: ReaperPluginName, State: pluginworkspace.PluginStateMissing},
			agent:  cliAgent("codex", true), wsNCLI: true,
			want: StatusPluginMissing,
		},
		{
			name:   "disabled",
			plugin: pluginworkspace.PluginResult{Name: ReaperPluginName, Installed: true, Enabled: false, State: pluginworkspace.PluginStateDisabled},
			agent:  cliAgent("codex", true), wsNCLI: true,
			want: StatusPluginDisabled,
		},
		{
			name: "detached",
			plugin: pluginworkspace.PluginResult{Name: ReaperPluginName, Installed: true, Enabled: true, State: pluginworkspace.PluginStateDetached,
				Missing: []pluginworkspace.Component{{Kind: "skill", Name: "reaper-web-remote"}}},
			agent: cliAgent("codex", true), wsNCLI: true,
			want: StatusPluginDetached,
		},
		{
			name:   "cli agent required",
			plugin: attachedPlugin(),
			agent:  cliAgent("openai", true), wsNCLI: true,
			want: StatusCLIAgentRequired,
		},
		{
			name:   "native access required (workspace off)",
			plugin: attachedPlugin(),
			agent:  cliAgent("codex", true), wsNCLI: false,
			want: StatusNativeCLIAccessRequired,
		},
		{
			name:   "native access required (agent off)",
			plugin: attachedPlugin(),
			agent:  cliAgent("codex", false), wsNCLI: true,
			want: StatusNativeCLIAccessRequired,
		},
		{
			name:   "ori ready",
			plugin: attachedPlugin(),
			agent:  cliAgent("codex", true), wsNCLI: true,
			want: StatusOriReady,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ws := reaperWS(t)
			ws.AllowNativeMCPCLI = tc.wsNCLI
			r := NewResolver(
				&fakeReader{ws: ws, agents: map[string]*agent.Agent{"Reaper Producer": tc.agent}},
				&fakeInspector{result: tc.plugin},
			)
			got, err := r.Resolve(ws.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != tc.want {
				t.Fatalf("want %s, got %s (%s)", tc.want, got.Status, got.Explanation)
			}
			// ori_ready must never claim a live REAPER connection.
			if got.LiveVerification != "not_checked" {
				t.Fatalf("LiveVerification must stay not_checked, got %q", got.LiveVerification)
			}
		})
	}
}

func TestResolve_OriReadyIsNotReaperConnected(t *testing.T) {
	ws := reaperWS(t)
	ws.AllowNativeMCPCLI = true
	r := NewResolver(
		&fakeReader{ws: ws, agents: map[string]*agent.Agent{"Reaper Producer": cliAgent("claude_code", true)}},
		&fakeInspector{result: attachedPlugin()},
	)
	got, _ := r.Resolve(ws.ID)
	if got.Status != StatusOriReady {
		t.Fatalf("expected ori_ready, got %s", got.Status)
	}
	if got.ProjectMode != "ori_ready" {
		t.Fatalf("expected project mode ori_ready")
	}
	// Explanation must talk about checking REAPER when setup runs, never assert a live connection.
	if got.LiveVerification != "not_checked" {
		t.Fatalf("live verification must be not_checked")
	}
}

func TestResolve_FileOnlyWhenNoPendingSetupTask(t *testing.T) {
	ws := reaperWS(t)
	// Consume the setup task so nothing is pending.
	ws.Tasks[0].Context[TaskContextSetupConsumedAt] = "2026-07-11T00:00:00Z"
	r := NewResolver(
		&fakeReader{ws: ws, agents: map[string]*agent.Agent{"Reaper Producer": cliAgent("openai", false)}},
		&fakeInspector{result: pluginworkspace.PluginResult{Name: ReaperPluginName, State: pluginworkspace.PluginStateMissing}},
	)
	got, _ := r.Resolve(ws.ID)
	if got.Status != StatusFileOnly {
		t.Fatalf("expected file_only when no pending setup task, got %s", got.Status)
	}
	if got.HasPendingSetupTask {
		t.Fatalf("consumed setup task must not count as pending")
	}
}

func TestResolve_EffectiveAssigneeEntryFallback(t *testing.T) {
	ws := reaperWS(t)
	// Unassigned setup task -> falls back to entry agent.
	ws.Tasks[0].To = ""
	ws.AllowNativeMCPCLI = true
	r := NewResolver(
		&fakeReader{ws: ws, agents: map[string]*agent.Agent{"Reaper Producer": cliAgent("codex", true)}},
		&fakeInspector{result: attachedPlugin()},
	)
	got, _ := r.Resolve(ws.ID)
	if got.SetupAgent != "Reaper Producer" {
		t.Fatalf("expected entry-agent fallback assignee, got %q", got.SetupAgent)
	}
	if got.Status != StatusOriReady {
		t.Fatalf("expected ori_ready with entry-agent fallback, got %s", got.Status)
	}
}

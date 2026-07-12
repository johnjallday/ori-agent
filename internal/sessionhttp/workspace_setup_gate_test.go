package sessionhttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/reapersetup"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// setupGateWS builds a REAPER workspace with an assigned pending setup task, an
// entry agent snapshot, and native access wired per args.
func setupGateWS(t *testing.T, store *workspace.InMemoryStore, provider string, wsNative, agentNative bool, attachSkill bool) *workspace.Workspace {
	t.Helper()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Song", Agents: []string{"Reaper Producer"}})
	ws.SetTemplateProvenance(&workspace.TemplateProvenance{TemplateID: reapersetup.ReaperSongTemplateID})
	ws.AllowNativeMCPCLI = wsNative
	if attachSkill {
		_ = ws.UpsertSkillBinding(workspace.SkillBinding{ID: "b1", SkillName: "reaper-session-setup", Enabled: true})
	}
	ws.Tasks = append(ws.Tasks, workspace.Task{
		ID: "setup-1", WorkspaceID: ws.ID, To: "Reaper Producer",
		Status: workspace.TaskStatusPending, Context: map[string]any{reapersetup.TaskContextTemplateSetup: true},
	})
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}
	_ = store.SaveWorkspaceAgent(ws.ID, "Reaper Producer", &agent.Agent{
		Type: "orchestrator", Settings: types.Settings{Provider: provider, AllowNativeMCPTools: &agentNative},
	})
	return ws
}

func postSetupStart(t *testing.T, h *Handler, wsID string) map[string]any {
	t.Helper()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/"+wsID+"/template-setup/start", nil)
	h.handleTemplateSetupStart(rr, req, wsID)
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("bad response %q: %v", rr.Body.String(), err)
	}
	return body
}

func TestSetupStart_BlockedWhenNotOriReady(t *testing.T) {
	h, store := reaperSetupHandler(t, nil) // plugin missing => not ori_ready
	var started int
	h.templateSetupStarter = func(string, string) error { started++; return nil }
	ws := setupGateWS(t, store, "codex", true, true, false)

	body := postSetupStart(t, h, ws.ID)
	if body["started"] != false {
		t.Fatalf("must not start when not ori_ready: %+v", body)
	}
	if body["reason"] != "not_ready" || body["readiness_status"] != "plugin_missing" {
		t.Fatalf("expected not_ready/plugin_missing blocker, got %+v", body)
	}
	if started != 0 {
		t.Fatalf("starter must not run when blocked")
	}
	// Marker must NOT be written: task stays pending and unconsumed.
	got, _ := store.Get(ws.ID)
	if _, consumed := got.Tasks[0].Context[reapersetup.TaskContextSetupConsumedAt]; consumed {
		t.Fatalf("consumed marker must not be written while blocked")
	}
}

func TestSetupStart_StartsOnceWhenOriReady(t *testing.T) {
	h, store := reaperSetupHandler(t, []plugin.InstalledPlugin{
		{Name: "reaper-plugin", Enabled: true, Skills: []string{"reaper-session-setup"}},
	})
	var mu sync.Mutex
	started := 0
	h.templateSetupStarter = func(string, string) error { mu.Lock(); started++; mu.Unlock(); return nil }
	ws := setupGateWS(t, store, "codex", true, true, true)

	body := postSetupStart(t, h, ws.ID)
	if body["started"] != true {
		t.Fatalf("expected started when ori_ready: %+v", body)
	}
	// Idempotent: a second call (reload / concurrent tab) does not start again.
	body2 := postSetupStart(t, h, ws.ID)
	if body2["started"] != false || body2["reason"] != "already_consumed" {
		t.Fatalf("second call should be already_consumed no-op, got %+v", body2)
	}
	if started != 1 {
		t.Fatalf("setup task must start exactly once, started=%d", started)
	}
}

func TestSetupStart_NonReaperTemplateUnaffected(t *testing.T) {
	h, store := reaperSetupHandler(t, nil)
	var started int
	h.templateSetupStarter = func(string, string) error { started++; return nil }
	// No REAPER provenance -> not identified -> gate does not apply.
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Notes", Agents: []string{"Writer"}})
	ws.Tasks = append(ws.Tasks, workspace.Task{
		ID: "s1", WorkspaceID: ws.ID, To: "Writer",
		Status: workspace.TaskStatusPending, Context: map[string]any{reapersetup.TaskContextTemplateSetup: true},
	})
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}

	body := postSetupStart(t, h, ws.ID)
	if body["started"] != true {
		t.Fatalf("non-REAPER setup task should start as before, got %+v", body)
	}
	if started != 1 {
		t.Fatalf("non-REAPER starter should run once, started=%d", started)
	}
}

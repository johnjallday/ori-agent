package sessionhttp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	agentstore "github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestCreateBackupRecoveryUsesReviewedTeamWithoutModelOrBackupGrants(t *testing.T) {
	for _, mode := range []string{"create", "assign"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			handler, lib, cleanup := capabilityTemplateEnv(t)
			defer cleanup()
			starts := 0
			handler.SetTemplateSetupTaskStarter(func(string, string) error { starts++; return nil })
			if err := projecttemplates.EnsureLibrary(lib); err != nil {
				t.Fatal(err)
			}
			name := "My Backup Advisor"
			if mode == "assign" {
				name = "Backup Advisor"
				if err := handler.agentStore.CreateAgent(name, &agentstore.CreateAgentConfig{SystemPrompt: "Existing definition stays unchanged."}); err != nil {
					t.Fatal(err)
				}
			}
			tpl, err := handler.resolveProjectTemplate("backup-and-recovery", "")
			if err != nil {
				t.Fatal(err)
			}
			plan := handler.buildTemplateAgentPlan(tpl)
			if plan.AssistantProgram != nil || plan.GroupRequirement != nil || len(plan.Agents) != 1 {
				t.Fatal("backup plan proposed an extra team or Home")
			}
			body := strictCreateBody(tpl, plan, "Invented backup workspace", fmt.Sprintf(`[{"role_id":"backup-advisor","mode":%q,"name":%q}]`, mode, name))
			response, result := postCreateWorkspace(t, handler, body)
			if response.Code != http.StatusCreated {
				t.Fatalf("create: %d %s", response.Code, response.Body.String())
			}
			id := result["folder"].(map[string]any)["id"].(string)
			ws, err := handler.workspaceTaskStore.Get(id)
			if err != nil {
				t.Fatal(err)
			}
			all, err := handler.store.ListWorkspaces(context.Background())
			if err != nil || len(all) != 1 || all[0].Kind != "workspace" {
				t.Fatal("created more than one workspace", err)
			}
			instances := ws.GetAgentInstances()
			if len(instances) != 1 || instances[0].Name != name || instances[0].RoleID != "backup-advisor" || ws.EntryAgentName() != name {
				t.Fatal("reviewed role lost or extra coordinator created")
			}
			if ws.Kind != "workspace" || ws.GetAssistantProgramState() != nil || ws.AllowNativeMCPCLI || len(ws.GetSkillBindings()) != 0 {
				t.Fatal("creation added a Home, native execution or skill binding")
			}
			// Every managed workspace already gets one self-folder reference and
			// workspace-files binding. This template adds no source/repository
			// root. These generic defaults are NOT advisor runtime isolation.
			folder, err := handler.workspaceStore.GetFolderPath(id)
			if err != nil || len(ws.DirectoryReferences) != 1 || ws.DirectoryReferences[0].Path != folder {
				t.Fatal("unexpected folder authority", err)
			}
			bindings := ws.GetMCPBindings()
			if len(bindings) != 1 || bindings[0].Alias != workspaceFilesMCPAlias || bindings[0].ServerName != "filesystem" || len(bindings[0].Config) != 1 {
				t.Fatal("unexpected tool binding")
			}
			encoded, err := json.Marshal(bindings[0].Config["roots"])
			if err != nil {
				t.Fatal(err)
			}
			var roots []string
			if err := json.Unmarshal(encoded, &roots); err != nil || len(roots) != 1 || roots[0] != folder {
				t.Fatal("template granted a non-workspace root", err)
			}
			caps := ws.GetInstalledCapabilities()
			if len(caps) != 1 || caps[0].ID != workspace.CapabilityBackupRecovery || caps[0].Version != 1 || caps[0].InstalledAt.IsZero() || len(caps[0].OwnedResources) != 0 {
				t.Fatal("creation installed an unrelated capability or owned runtime")
			}
			// Wizard provenance is folder-owned; the SQLite task projection does
			// not carry it. Inspect the same owner the wizard actually reads.
			persisted := folderWorkspace(t, handler, id)
			prov := persisted.GetTemplateProvenance()
			if prov == nil || prov.SetupWizard == nil || len(prov.SetupWizard.Steps) != 2 || persisted.IsSetupWizardReady() {
				t.Fatal("creation lost wizard or asserted readiness")
			}
			for range 2 {
				opened := postTemplateSetupStart(t, handler, id)
				if opened["started"] != false || opened["reason"] != "setup_wizard_owned" {
					t.Fatal("opening wizard started a task", opened)
				}
			}
			if starts != 0 {
				t.Fatal("creation/setup executed a task")
			}
			if len(ws.Tasks) != len(tpl.StarterTasks) {
				t.Fatal("optional tasks missing")
			}
			for _, task := range ws.Tasks {
				if task.Status != workspace.TaskStatusPending {
					t.Fatal("creation advanced an optional task")
				}
			}
			if len(handler.agentStore.ListAgents()) != 1 {
				t.Fatal("unexpected global advisor/coordinator duplication")
			}
			if mode == "assign" {
				saved, ok := handler.agentStore.GetAgent(name)
				if !ok || saved.Settings.SystemPrompt != "Existing definition stays unchanged." {
					t.Fatal("reuse rewrote existing agent")
				}
			}
		})
	}
}

func TestCreateBackupRecoveryRejectsStaleTeamReviewBeforeWrites(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	handler, lib, cleanup := capabilityTemplateEnv(t)
	defer cleanup()
	if err := projecttemplates.EnsureLibrary(lib); err != nil {
		t.Fatal(err)
	}
	tpl, err := handler.resolveProjectTemplate("backup-and-recovery", "")
	if err != nil {
		t.Fatal(err)
	}
	plan := handler.buildTemplateAgentPlan(tpl)
	if err := handler.agentStore.CreateAgent("Backup Advisor", nil); err != nil {
		t.Fatal(err)
	}
	response, _ := postCreateWorkspace(t, handler, strictCreateBody(tpl, plan, "Not created", `[{"role_id":"backup-advisor","mode":"create","name":"New advisor"}]`))
	if response.Code != http.StatusConflict {
		t.Fatalf("stale review: %d %s", response.Code, response.Body.String())
	}
	assertNoReadinessSideEffects(t, handler, "New advisor")
}

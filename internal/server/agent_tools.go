package server

import (
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// makeAgentToolApplier returns a function that binds a seeded template agent's
// per-agent tools, applying only what resolves on the machine (apply-if-present)
// and reporting the rest:
//
//   - skills are enabled on the agent itself (per-agent skill state); agent
//     skills are disabled-by-default, so binding == enabling here;
//   - MCP servers have no per-agent scope, so they bind at the workspace level.
//
// Builder fields are read at apply time, so wiring order is moot.
func makeAgentToolApplier(b *ServerBuilder) func(string, string, projecttemplates.ToolDefaults) ([]string, []string) {
	return func(workspaceID, agentName string, tools projecttemplates.ToolDefaults) (applied, missing []string) {
		if mgr := b.skillsManager; mgr != nil {
			for _, s := range tools.Skills {
				name := strings.TrimSpace(s)
				if name == "" {
					continue
				}
				if _, ok, err := mgr.ResolveSkillByName(name); err != nil || !ok {
					missing = append(missing, "skill:"+name)
					continue
				}
				if err := mgr.SetSkillEnabled(agentName, name, true); err != nil {
					missing = append(missing, "skill:"+name)
					continue
				}
				applied = append(applied, "skill:"+name)
			}
		}

		a, m := bindWorkspaceMCPServers(b, workspaceID, tools.MCPServers)
		applied = append(applied, a...)
		missing = append(missing, m...)
		return applied, missing
	}
}

// bindWorkspaceMCPServers binds MCP servers onto a workspace (apply-if-present),
// returning applied + missing labels. MCP has no per-agent scope, so per-agent
// template MCP servers land here at the workspace level. It binds through the
// same workspace store the binding endpoints read from so the result is visible.
func bindWorkspaceMCPServers(b *ServerBuilder, workspaceID string, servers []string) (applied, missing []string) {
	store := b.workspaceStore
	if store == nil || len(servers) == 0 {
		return nil, nil
	}
	ws, err := store.Get(workspaceID)
	if err != nil || ws == nil {
		return nil, nil
	}
	now := time.Now()

	bound := map[string]bool{}
	for _, bnd := range ws.GetMCPBindings() {
		bound[strings.ToLower(strings.TrimSpace(bnd.ServerName))] = true
	}

	changed := false
	for _, srv := range servers {
		name := strings.TrimSpace(srv)
		key := strings.ToLower(name)
		if name == "" || bound[key] {
			continue
		}
		if cfg := b.mcpConfigManager; cfg != nil {
			if _, err := cfg.GetServer(name); err != nil {
				missing = append(missing, "mcp:"+name)
				continue
			}
		}
		bound[key] = true
		if err := ws.UpsertMCPBinding(workspace.WorkspaceMCPBinding{
			ID: uuid.NewString(), ServerName: name, Enabled: true, CreatedAt: now, UpdatedAt: now,
		}); err == nil {
			applied = append(applied, "mcp:"+name)
			changed = true
		}
	}

	if changed {
		if err := store.Save(ws); err != nil {
			return nil, missing
		}
	}
	return applied, missing
}

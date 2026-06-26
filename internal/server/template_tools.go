package server

import (
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// templatePluginsDir is the managed plugins directory the plugin handler uses;
// the applier reads installed.json from here to expand plugin references.
const templatePluginsDir = "plugins"

// makeTemplateToolApplier returns a function that binds a template's declared
// default tools onto a freshly created workspace, applying only what resolves on
// the machine and reporting the rest (apply-if-present):
//
//   - skills bind by name (resolved downstream by the skill manager);
//   - MCP servers bind when configured (else reported missing);
//   - plugins expand to their installed component skills + MCP servers.
//
// It binds through the same workspace store the binding endpoints read from (the
// SyncStore), Get→bind→Save by id, so the new bindings are immediately visible —
// writing through the raw FileStore would land on disk but bypass the read path.
// Builder fields are read at apply time (request time), so wiring order is moot.
func makeTemplateToolApplier(b *ServerBuilder) func(string, projecttemplates.ToolDefaults) ([]string, []string) {
	return func(workspaceID string, tools projecttemplates.ToolDefaults) (applied, missing []string) {
		store := b.workspaceStore
		if store == nil {
			return nil, nil
		}
		ws, err := store.Get(workspaceID)
		if err != nil || ws == nil {
			return nil, nil
		}
		now := time.Now()

		boundSkills := map[string]bool{}
		for _, bnd := range ws.GetSkillBindings() {
			boundSkills[strings.ToLower(strings.TrimSpace(bnd.SkillName))] = true
		}
		boundMCP := map[string]bool{}
		for _, bnd := range ws.GetMCPBindings() {
			boundMCP[strings.ToLower(strings.TrimSpace(bnd.ServerName))] = true
		}

		bindSkill := func(name string) {
			name = strings.TrimSpace(name)
			key := strings.ToLower(name)
			if name == "" || boundSkills[key] {
				return
			}
			boundSkills[key] = true
			if err := ws.UpsertSkillBinding(workspace.WorkspaceSkillBinding{
				ID: uuid.NewString(), SkillName: name, Enabled: true, CreatedAt: now, UpdatedAt: now,
			}); err == nil {
				applied = append(applied, "skill:"+name)
			}
		}
		bindMCP := func(name string) {
			name = strings.TrimSpace(name)
			key := strings.ToLower(name)
			if name == "" || boundMCP[key] {
				return
			}
			boundMCP[key] = true
			if err := ws.UpsertMCPBinding(workspace.WorkspaceMCPBinding{
				ID: uuid.NewString(), ServerName: name, Enabled: true, CreatedAt: now, UpdatedAt: now,
			}); err == nil {
				applied = append(applied, "mcp:"+name)
			}
		}

		// Skills: bound by name; presence is resolved at runtime by the manager.
		for _, s := range tools.Skills {
			bindSkill(s)
		}

		// MCP servers: bind when the server is configured, else report missing.
		for _, srv := range tools.MCPServers {
			if cfg := b.mcpConfigManager; cfg != nil {
				if _, err := cfg.GetServer(strings.TrimSpace(srv)); err != nil {
					missing = append(missing, "mcp:"+srv)
					continue
				}
			}
			bindMCP(srv)
		}

		// Plugins: expand each installed plugin to its component skills + servers.
		if len(tools.Plugins) > 0 {
			installed, _ := plugin.NewStore(templatePluginsDir).List()
			byName := make(map[string]plugin.InstalledPlugin, len(installed))
			for _, p := range installed {
				byName[strings.ToLower(p.Name)] = p
			}
			for _, pl := range tools.Plugins {
				p, ok := byName[strings.ToLower(strings.TrimSpace(pl))]
				if !ok {
					missing = append(missing, "plugin:"+pl)
					continue
				}
				for _, s := range p.Skills {
					bindSkill(s)
				}
				for _, srv := range p.MCPServers {
					bindMCP(srv)
				}
			}
		}

		if len(applied) > 0 {
			if err := store.Save(ws); err != nil {
				return nil, missing
			}
		}
		return applied, missing
	}
}

package server

import (
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/johnjallday/ori-agent/internal/pluginworkspace"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// makeTemplateToolApplier returns a function that binds a template's declared
// default tools onto a freshly created workspace, applying only what resolves on
// the machine and reporting the rest (apply-if-present):
//
//   - skills bind by name (resolved downstream by the skill manager);
//   - MCP servers bind when configured (else reported missing);
//   - plugins expand to their installed component skills + MCP servers through
//     the shared pluginworkspace reconciler, which reads installed-plugin state
//     from the configured plugin manager/store used by the Plugins API and
//     honors the plugin's enabled flag.
//
// It binds through the same workspace store the binding endpoints read from (the
// SyncStore), Get→bind→Save by id, so the new bindings are immediately visible —
// writing through the raw FileStore would land on disk but bypass the read path.
// Builder fields are read at apply time (request time), so wiring order is moot.
//
// Skills/MCP are bound and saved first; plugin components are then reconciled as
// a separate Get→bind→Save pass so the two writes never clobber each other
// regardless of whether the store returns shared or cloned workspace values.
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

		changed := false
		bindSkill := func(name string) {
			name = strings.TrimSpace(name)
			key := strings.ToLower(name)
			if name == "" || boundSkills[key] {
				return
			}
			boundSkills[key] = true
			if err := ws.UpsertSkillBinding(workspace.SkillBinding{
				ID: uuid.NewString(), SkillName: name, Enabled: true, CreatedAt: now, UpdatedAt: now,
			}); err == nil {
				applied = append(applied, "skill:"+name)
				changed = true
			}
		}
		bindMCP := func(name string) {
			name = strings.TrimSpace(name)
			key := strings.ToLower(name)
			if name == "" || boundMCP[key] {
				return
			}
			boundMCP[key] = true
			if err := ws.UpsertMCPBinding(workspace.MCPBinding{
				ID: uuid.NewString(), ServerName: name, Enabled: true, CreatedAt: now, UpdatedAt: now,
			}); err == nil {
				applied = append(applied, "mcp:"+name)
				changed = true
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

		if changed {
			if err := store.Save(ws); err != nil {
				return nil, missing
			}
		}

		// Plugins: reconcile each declared plugin's recorded components through the
		// shared service. Template application reports a disabled plugin without
		// enabling it (AllowEnable=false); enabling is an explicit user decision in
		// manual attachment or repair.
		if len(tools.Plugins) > 0 && b.pluginHandler != nil {
			rec := pluginworkspace.New(b.pluginHandler.Manager(), store)
			res, err := rec.Reconcile(pluginworkspace.Request{
				WorkspaceID: workspaceID,
				Plugins:     tools.Plugins,
				AllowEnable: false,
			})
			if err == nil {
				applied = append(applied, res.Applied...)
				for _, pr := range res.Plugins {
					switch pr.State {
					case pluginworkspace.PluginStateMissing:
						missing = append(missing, "plugin:"+pr.Name)
					case pluginworkspace.PluginStateDisabled:
						missing = append(missing, "plugin:"+pr.Name+" (globally disabled)")
					case pluginworkspace.PluginStateDetached:
						for _, c := range pr.Missing {
							missing = append(missing, c.String())
						}
					}
				}
			}
		}

		return applied, missing
	}
}

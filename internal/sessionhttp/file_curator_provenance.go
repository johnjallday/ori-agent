package sessionhttp

import (
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// recordSeededCurator runs only after a trusted roster slot was successfully
// filled. The built-in single-role slot, not its editable label/prompt or the
// entry agent, identifies the companion. name is the seeder's actual result,
// never a search for a Curator-like display name on an existing workspace.
func recordSeededCurator(result *seedAgentsResult, ws *session.Workspace, tpl projecttemplates.Template, index int, name string, reused bool) {
	if !tpl.Builtin || tpl.PluginOwner != nil || tpl.ID != "file-janitor" || index != 0 || len(tpl.Agents) != 1 {
		return
	}
	var resource workspace.CapabilityResource
	for _, instance := range ws.AgentInstances {
		if instance.Name != name {
			continue
		}
		if resource.ID != "" || instance.ID == "" {
			return
		}
		resource = workspace.CapabilityResource{Kind: workspace.ResourceCompanionAgent, ID: instance.ID, Shared: reused || instance.EntryPoint}
	}
	if resource.ID != "" {
		result.CuratorResources = append(result.CuratorResources, resource)
	}
}

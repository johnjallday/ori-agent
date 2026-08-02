package workspace

import (
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/internal/logger"
)

// Toolbox-driven runtime resolution (PRD FR-2, FR-6–FR-7, FR-12–FR-17, FR-32,
// §9.4).
//
// This is where the feature actually takes effect. The legacy resolver answered
// "what may this agent use?" by MERGING sources — every globally enabled skill,
// plus every enabled workspace binding the instance had not explicitly been
// narrowed away from. That merge is why binding a new MCP server to a workspace
// silently changed what every agent in it could do.
//
// Once an instance has an explicit assignment, resolution stops merging and
// starts LOOKING UP: the pinned Toolbox version names exact skill sources and
// exact operation subsets, and nothing outside that list is added. A capability
// that appears in the workspace tomorrow is not in the version pinned today, so
// it does not appear in the agent's hands (FR-32).
//
// Core capabilities are the one thing still added unconditionally. They are
// "always present" by definition — the synthesized filesystem binding and
// workspace-settings-managed skills are Ori runtime abilities, not user
// selections — so storing them in each Toolbox would create a second place they
// could drift out of. They are resolved here and reported as `core` in previews
// and Focus (FR-31, FR-59).

// resolveRuntimeFromToolbox fills a ResolvedAgentRuntime from one pinned
// Toolbox version, adding nothing the version does not name.
func (r *AgentRuntimeResolver) resolveRuntimeFromToolbox(
	ws *Workspace,
	instance *AgentInstance,
	agentName string,
	resolved *ResolvedAgentRuntime,
	definition ToolboxDefinition,
	recipe ToolboxRecipe,
) (*ResolvedAgentRuntime, error) {
	resolved.EffectiveSkills = r.resolveToolboxSkills(ws, agentName, recipe)

	bindings := ws.GetMCPBindings()
	byID := make(map[string]MCPBinding, len(bindings))
	// overriddenServerNames preserves the legacy gate: a workspace takes over
	// the agent's MCP configuration only when it has MCP bindings at all. It is
	// computed from the workspace's bindings, not the Toolbox's, because an
	// empty Toolbox in a workspace that HAS bindings means "no MCP servers",
	// not "fall back to the agent's own".
	overriddenServerNames := make([]string, 0, len(bindings))
	for _, binding := range bindings {
		serverName := strings.TrimSpace(binding.ServerName)
		if serverName == "" || !binding.IsRuntimeMCP() {
			continue
		}
		byID[strings.ToLower(strings.TrimSpace(binding.ID))] = binding
		overriddenServerNames = append(overriddenServerNames, serverName)
	}

	coreBindings := r.resolveCoreMCPBindings(ws)
	if len(overriddenServerNames) == 0 && len(coreBindings) == 0 {
		return resolved, nil
	}

	selected := make([]MCPBinding, 0, len(recipe.MCPBindings)+len(coreBindings))
	// A Toolbox entry's tool selection, keyed by binding ID. A nil value means
	// the entry defers to the binding's own AllowedTools policy — the migrated
	// all-tools case (see ToolboxMCPRef.InheritsBindingTools).
	toolSelection := make(map[string][]string, len(recipe.MCPBindings))
	deferToBinding := make(map[string]bool, len(recipe.MCPBindings))

	for _, ref := range recipe.MCPBindings {
		key := strings.ToLower(strings.TrimSpace(ref.BindingID))
		binding, exists := byID[key]
		if !exists {
			// The binding was removed, disabled, or is a native capability with
			// no MCP server. A required one makes the Toolbox `Needs repair`
			// for the preview surface to report; the runtime's job here is
			// simply not to substitute anything for it (FR-113).
			continue
		}
		if !binding.Enabled {
			continue
		}
		selected = append(selected, binding)
		if ref.InheritsBindingTools {
			deferToBinding[key] = true
			continue
		}
		toolSelection[key] = append([]string(nil), ref.AllowedTools...)
	}

	selected = append(selected, coreBindings...)
	for _, binding := range coreBindings {
		deferToBinding[strings.ToLower(strings.TrimSpace(binding.ID))] = true
	}

	effectiveServers := make([]string, 0, len(selected))
	var toolAllowlist map[string][]string
	for _, binding := range selected {
		runtimeName, err := r.materializeRuntimeBinding(ws.ID, binding)
		if err != nil {
			return nil, err
		}
		effectiveServers = append(effectiveServers, runtimeName)

		key := strings.ToLower(strings.TrimSpace(binding.ID))
		allowed, narrowed := toolSelection[key]
		switch {
		case narrowed:
			// The Toolbox names an exact operation subset. It was validated
			// against the binding's policy on save and can only narrow it
			// (FR-12), so it is applied as-is — including an empty list, which
			// legitimately exposes no operations from this binding.
			if toolAllowlist == nil {
				toolAllowlist = make(map[string][]string, len(selected))
			}
			toolAllowlist[runtimeName] = allowed
		case deferToBinding[key] && !binding.AllowsAllTools():
			if toolAllowlist == nil {
				toolAllowlist = make(map[string][]string, len(selected))
			}
			toolAllowlist[runtimeName] = append([]string(nil), binding.AllowedTools...)
		}
	}

	resolved.MCPServers = dedupeStringsPreserveOrder(effectiveServers)
	resolved.MCPToolAllowlist = toolAllowlist

	logger.Debug("Resolved agent runtime from pinned toolbox", logger.Fields{
		"workspace_id":      ws.ID,
		"agent_instance_id": instance.ID,
		"toolbox_id":        definition.ID,
		"toolbox_version":   recipe.Version,
		"skills":            len(resolved.EffectiveSkills),
		"mcp_servers":       len(resolved.MCPServers),
	})
	return resolved, nil
}

// resolveToolboxSkills resolves exactly the skills a Toolbox version names,
// from exactly the sources it names (FR-6).
//
// Nothing is appended afterwards. In particular the agent's globally enabled
// skills are FILTERED by the Toolbox rather than added to it: that append is
// what made stage-based capacity disagree with the real effective toolbox, and
// removing it is what lets capacity be enforced on the final set (FR-55).
func (r *AgentRuntimeResolver) resolveToolboxSkills(ws *Workspace, agentName string, recipe ToolboxRecipe) []ResolvedSkill {
	if r.skillResolver == nil {
		return nil
	}

	wantLearned := make(map[string]struct{})
	workspaceBindingIDs := make(map[string]string) // normalized identity -> binding ID
	for _, ref := range recipe.Skills {
		switch NormalizeToolboxSource(ref.Source) {
		case ToolboxSourceAgentLearned:
			wantLearned[ref.CapabilityID] = struct{}{}
		case ToolboxSourceWorkspaceProvided:
			workspaceBindingIDs[ref.CapabilityID] = ref.BindingID
		}
	}

	effective := make([]ResolvedSkill, 0, len(recipe.Skills))

	if len(wantLearned) > 0 {
		learned, err := r.skillResolver.ListEnabledAgentSkills(agentName)
		if err != nil {
			logger.Warn("Failed to load agent-learned skills for toolbox resolution", logger.Fields{
				"agent": agentName,
				"error": err,
			})
		}
		for _, skill := range learned {
			if _, wanted := wantLearned[NormalizeToolboxCapabilityID(skill.Name)]; wanted {
				effective = append(effective, skill)
			}
		}
	}

	if len(workspaceBindingIDs) > 0 {
		bindingsByID := make(map[string]SkillBinding)
		for _, binding := range ws.GetSkillBindings() {
			bindingsByID[strings.ToLower(strings.TrimSpace(binding.ID))] = binding
		}

		names := make([]string, 0, len(workspaceBindingIDs))
		bindingByName := make(map[string]SkillBinding, len(workspaceBindingIDs))
		for _, bindingID := range workspaceBindingIDs {
			binding, exists := bindingsByID[strings.ToLower(strings.TrimSpace(bindingID))]
			if !exists || !binding.Enabled {
				// A removed or disabled binding resolves to nothing. The
				// preview reports it as `Missing capability`; the runtime must
				// not silently substitute a same-named skill from another
				// source (FR-6, FR-113).
				continue
			}
			name := strings.TrimSpace(binding.SkillName)
			if name == "" {
				continue
			}
			names = append(names, name)
			bindingByName[NormalizeToolboxCapabilityID(name)] = binding
		}

		if len(names) > 0 {
			resolvedSkills, unresolved, err := r.skillResolver.ResolveSkillsByNames(names)
			if err != nil {
				logger.Warn("Failed to resolve workspace-provided toolbox skills", logger.Fields{"error": err})
			} else {
				if len(unresolved) > 0 {
					logger.Warn("Some toolbox skills could not be resolved", logger.Fields{"unresolved": unresolved})
				}
				for i := range resolvedSkills {
					if binding, ok := bindingByName[NormalizeToolboxCapabilityID(resolvedSkills[i].Name)]; ok {
						resolvedSkills[i].Trusted = binding.Trusted
						resolvedSkills[i].Enabled = true
						resolvedSkills[i].Config = cloneInterfaceMap(binding.Config)
					}
					effective = append(effective, resolvedSkills[i])
				}
			}
		}
	}

	// Core skills last. They are not Toolbox contents and are not deselectable,
	// but they must not double up with a Toolbox entry of the same identity.
	coreBindings := r.resolveSettingsManagedSkillBindings(ws, agentName)
	if len(coreBindings) > 0 {
		present := make(map[string]struct{}, len(effective))
		for _, skill := range effective {
			present[NormalizeToolboxCapabilityID(skill.Name)] = struct{}{}
		}
		names := make([]string, 0, len(coreBindings))
		bindingByName := make(map[string]SkillBinding, len(coreBindings))
		for _, binding := range coreBindings {
			name := strings.TrimSpace(binding.SkillName)
			key := NormalizeToolboxCapabilityID(name)
			if name == "" {
				continue
			}
			if _, exists := present[key]; exists {
				continue
			}
			names = append(names, name)
			bindingByName[key] = binding
		}
		if len(names) > 0 {
			resolvedSkills, _, err := r.skillResolver.ResolveSkillsByNames(names)
			if err != nil {
				logger.Warn("Failed to resolve core workspace-settings skills", logger.Fields{"error": err})
			} else {
				for i := range resolvedSkills {
					if binding, ok := bindingByName[NormalizeToolboxCapabilityID(resolvedSkills[i].Name)]; ok {
						resolvedSkills[i].Trusted = binding.Trusted
						resolvedSkills[i].Enabled = true
						resolvedSkills[i].Config = cloneInterfaceMap(binding.Config)
					}
					effective = append(effective, resolvedSkills[i])
				}
			}
		}
	}

	if len(effective) == 0 {
		return nil
	}
	return effective
}

// resolveCoreMCPBindings returns the synthesized bindings that are present
// regardless of Toolbox selection — today, the workspace filesystem binding
// derived from the workspace's directory references (FR-31).
func (r *AgentRuntimeResolver) resolveCoreMCPBindings(ws *Workspace) []MCPBinding {
	synthesized := synthesizeFilesystemBinding(ws.GetMCPBindings(), ws)
	if synthesized == nil {
		return nil
	}
	return []MCPBinding{*synthesized}
}

// toolboxAssignmentForInstance resolves the pinned Toolbox for one instance.
//
// A resolution FAILURE is returned as an error rather than falling back to the
// legacy implicit merge. Falling back would hand the agent every enabled
// workspace binding at the exact moment its explicit selection became
// unreadable — a silent permission expansion, which is the one thing this
// feature must never do (FR-157, success metric 2).
func toolboxAssignmentForInstance(ws *Workspace, instance *AgentInstance) (ToolboxDefinition, ToolboxRecipe, bool, error) {
	if ws == nil || instance == nil {
		return ToolboxDefinition{}, ToolboxRecipe{}, false, nil
	}
	definition, recipe, ok, err := ws.ResolveAssignedToolbox(instance.ID)
	if err != nil {
		return ToolboxDefinition{}, ToolboxRecipe{}, false, fmt.Errorf(
			"agent instance %s has an unreadable toolbox assignment and cannot run until it is repaired: %w", instance.ID, err)
	}
	return definition, recipe, ok, nil
}

package workspace

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/mcp"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
)

const synthesizedFilesystemBindingID = "workspace-filesystem"

type runtimeMCPRegistry interface {
	UpsertServer(config mcp.ServerConfig) error
}

type mcpTemplateLookup interface {
	GetServer(name string) (*mcp.ServerConfig, error)
}

// ResolvedAgentRuntime is the effective runtime configuration for an agent executing inside a workspace.
type ResolvedAgentRuntime struct {
	Agent         *agent.Agent
	AgentInstance *AgentInstance
	MCPServers    []string
}

// AgentRuntimeResolver composes base agent configuration with workspace-owned MCP bindings.
type AgentRuntimeResolver struct {
	agentStore       store.Store
	workspaceStore   Store
	mcpRegistry      runtimeMCPRegistry
	mcpConfigManager mcpTemplateLookup
}

// NewAgentRuntimeResolver creates a runtime resolver for workspace task execution.
func NewAgentRuntimeResolver(
	agentStore store.Store,
	workspaceStore Store,
	mcpRegistry runtimeMCPRegistry,
	mcpConfigManager mcpTemplateLookup,
) *AgentRuntimeResolver {
	return &AgentRuntimeResolver{
		agentStore:       agentStore,
		workspaceStore:   workspaceStore,
		mcpRegistry:      mcpRegistry,
		mcpConfigManager: mcpConfigManager,
	}
}

// ResolveAgentForTask returns a cloned agent with effective runtime MCP servers for the task workspace.
func (r *AgentRuntimeResolver) ResolveAgentForTask(agentName string, task Task) (*ResolvedAgentRuntime, error) {
	return r.resolveAgentRuntime(agentName, task.WorkspaceID, task.AssignedNodeID)
}

// ResolveAgentForWorkspace returns a cloned agent with effective runtime MCP servers for a workspace request.
func (r *AgentRuntimeResolver) ResolveAgentForWorkspace(agentName, workspaceID, nodeID string) (*ResolvedAgentRuntime, error) {
	return r.resolveAgentRuntime(agentName, workspaceID, nodeID)
}

func (r *AgentRuntimeResolver) resolveAgentRuntime(agentName, workspaceID, nodeID string) (*ResolvedAgentRuntime, error) {
	if r == nil || r.agentStore == nil {
		return nil, fmt.Errorf("agent runtime resolver is not configured")
	}

	baseAgent, ok := r.agentStore.GetAgent(agentName)
	if !ok || baseAgent == nil {
		return nil, fmt.Errorf("agent %s not found", agentName)
	}

	clonedAgent := cloneRuntimeAgent(baseAgent)
	resolved := &ResolvedAgentRuntime{
		Agent:      clonedAgent,
		MCPServers: nil,
	}

	if strings.TrimSpace(workspaceID) == "" || r.workspaceStore == nil || r.mcpRegistry == nil || r.mcpConfigManager == nil {
		return resolved, nil
	}

	ws, err := r.workspaceStore.Get(workspaceID)
	if err != nil {
		return nil, fmt.Errorf("load workspace %s for runtime resolution: %w", workspaceID, err)
	}

	instance, _ := ws.FindAgentInstance(agentName, nodeID)
	resolved.AgentInstance = instance

	allowedBindings, overriddenServerNames := r.resolveWorkspaceBindings(ws, instance)
	if len(overriddenServerNames) == 0 {
		return resolved, nil
	}

	effectiveServers := make([]string, 0, len(allowedBindings))
	for _, binding := range allowedBindings {
		runtimeName, err := r.materializeRuntimeBinding(ws.ID, binding)
		if err != nil {
			return nil, err
		}
		effectiveServers = append(effectiveServers, runtimeName)
	}

	resolved.MCPServers = dedupeStringsPreserveOrder(effectiveServers)
	return resolved, nil
}

func (r *AgentRuntimeResolver) resolveWorkspaceBindings(ws *Workspace, instance *AgentInstance) ([]WorkspaceMCPBinding, []string) {
	if ws == nil {
		return nil, nil
	}

	bindings := ws.GetMCPBindings()
	if synthesized := synthesizeFilesystemBinding(bindings, ws); synthesized != nil {
		bindings = append(bindings, *synthesized)
	}

	if len(bindings) == 0 {
		return nil, nil
	}

	enabledBindings := make([]WorkspaceMCPBinding, 0, len(bindings))
	overriddenServerNames := make([]string, 0, len(bindings))
	for _, binding := range bindings {
		serverName := strings.TrimSpace(binding.ServerName)
		if serverName == "" {
			continue
		}
		overriddenServerNames = append(overriddenServerNames, serverName)
		if !binding.Enabled {
			continue
		}
		enabledBindings = append(enabledBindings, binding)
	}

	if len(enabledBindings) == 0 {
		return nil, dedupeStringsPreserveOrder(overriddenServerNames)
	}

	if instance == nil {
		return enabledBindings, dedupeStringsPreserveOrder(overriddenServerNames)
	}

	accessEntry, ok := ws.GetAgentMCPAccess(instance.ID)
	if !ok {
		return enabledBindings, dedupeStringsPreserveOrder(overriddenServerNames)
	}

	allowedIDs := normalizeValueSet(accessEntry.EnabledBindingIDs)
	if len(allowedIDs) == 0 {
		return nil, dedupeStringsPreserveOrder(overriddenServerNames)
	}

	filtered := make([]WorkspaceMCPBinding, 0, len(enabledBindings))
	for _, binding := range enabledBindings {
		if allowedIDs[strings.ToLower(strings.TrimSpace(binding.ID))] {
			filtered = append(filtered, binding)
		}
	}

	return filtered, dedupeStringsPreserveOrder(overriddenServerNames)
}

func synthesizeFilesystemBinding(bindings []WorkspaceMCPBinding, ws *Workspace) *WorkspaceMCPBinding {
	if ws == nil {
		return nil
	}

	for _, binding := range bindings {
		if strings.EqualFold(strings.TrimSpace(binding.ServerName), "filesystem") {
			return nil
		}
	}

	roots := collectWorkspaceDirectoryRoots(ws)
	if len(roots) == 0 {
		return nil
	}

	now := ws.UpdatedAt
	if now.IsZero() {
		now = ws.CreatedAt
	}

	return &WorkspaceMCPBinding{
		ID:         synthesizedFilesystemBindingID,
		ServerName: "filesystem",
		Alias:      "workspace_filesystem",
		Enabled:    true,
		Scope: map[string]interface{}{
			"roots": roots,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func collectWorkspaceDirectoryRoots(ws *Workspace) []string {
	if ws == nil || len(ws.DirectoryReferences) == 0 {
		return nil
	}

	roots := make([]string, 0, len(ws.DirectoryReferences))
	seen := make(map[string]struct{}, len(ws.DirectoryReferences))
	for _, dir := range ws.DirectoryReferences {
		path := strings.TrimSpace(dir.Path)
		if path == "" {
			continue
		}
		cleaned := filepath.Clean(path)
		if !filepath.IsAbs(cleaned) {
			continue
		}
		key := strings.ToLower(cleaned)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		roots = append(roots, cleaned)
	}
	sort.Strings(roots)
	return roots
}

func (r *AgentRuntimeResolver) materializeRuntimeBinding(workspaceID string, binding WorkspaceMCPBinding) (string, error) {
	templateName := strings.TrimSpace(binding.ServerName)
	if templateName == "" {
		return "", fmt.Errorf("binding %s has no server name", binding.ID)
	}

	template, err := r.mcpConfigManager.GetServer(templateName)
	if err != nil {
		return "", fmt.Errorf("load MCP template %s for binding %s: %w", templateName, binding.ID, err)
	}
	if template == nil {
		return "", fmt.Errorf("MCP template %s for binding %s was nil", templateName, binding.ID)
	}

	runtimeName := RuntimeMCPServerName(workspaceID, templateName, binding.ID)
	runtimeConfig := cloneServerConfig(*template)
	runtimeConfig.Name = runtimeName
	runtimeConfig.Enabled = false
	applyWorkspaceBindingRuntimeConfig(&runtimeConfig, binding.Config)

	if strings.EqualFold(templateName, "filesystem") {
		roots := extractFilesystemRoots(binding.Scope)
		if len(roots) == 0 {
			return "", fmt.Errorf("filesystem binding %s has no workspace roots", binding.ID)
		}
		runtimeConfig.Args = rebuildFilesystemServerArgs(template.Args, roots)
	}

	if err := r.mcpRegistry.UpsertServer(runtimeConfig); err != nil {
		return "", fmt.Errorf("materialize MCP binding %s for workspace %s: %w", binding.ID, workspaceID, err)
	}

	logger.Debug("Resolved workspace MCP runtime binding", logger.Fields{
		"workspace_id": workspaceID,
		"binding_id":   binding.ID,
		"server":       templateName,
		"runtime":      runtimeName,
	})

	return runtimeName, nil
}

func runtimeMCPServerName(workspaceID, serverName, bindingID string) string {
	return RuntimeMCPServerName(workspaceID, serverName, bindingID)
}

// RuntimeMCPServerName returns the materialized runtime server name for a workspace MCP binding.
func RuntimeMCPServerName(workspaceID, serverName, bindingID string) string {
	return fmt.Sprintf(
		"ws:%s:mcp:%s:%s",
		strings.TrimSpace(workspaceID),
		strings.TrimSpace(serverName),
		strings.TrimSpace(bindingID),
	)
}

// ParseRuntimeMCPServerName extracts workspace, logical server, and binding IDs from a runtime server name.
func ParseRuntimeMCPServerName(name string) (workspaceID, serverName, bindingID string, ok bool) {
	parts := strings.Split(strings.TrimSpace(name), ":")
	if len(parts) < 5 {
		return "", "", "", false
	}
	if parts[0] != "ws" || parts[2] != "mcp" {
		return "", "", "", false
	}
	workspaceID = strings.TrimSpace(parts[1])
	serverName = strings.TrimSpace(parts[3])
	bindingID = strings.TrimSpace(strings.Join(parts[4:], ":"))
	if workspaceID == "" || serverName == "" || bindingID == "" {
		return "", "", "", false
	}
	return workspaceID, serverName, bindingID, true
}

func extractFilesystemRoots(scope map[string]interface{}) []string {
	if len(scope) == 0 {
		return nil
	}

	raw, ok := scope["roots"]
	if !ok {
		return nil
	}

	roots := make([]string, 0)
	switch typed := raw.(type) {
	case []string:
		roots = append(roots, typed...)
	case []interface{}:
		for _, value := range typed {
			text, ok := value.(string)
			if ok {
				roots = append(roots, text)
			}
		}
	default:
		return nil
	}

	cleaned := make([]string, 0, len(roots))
	seen := make(map[string]struct{}, len(roots))
	for _, root := range roots {
		trimmed := strings.TrimSpace(root)
		if trimmed == "" {
			continue
		}
		abs := filepath.Clean(trimmed)
		if !filepath.IsAbs(abs) {
			continue
		}
		key := strings.ToLower(abs)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		cleaned = append(cleaned, abs)
	}
	sort.Strings(cleaned)
	return cleaned
}

func rebuildFilesystemServerArgs(args []string, roots []string) []string {
	if len(roots) == 0 {
		return append([]string{}, args...)
	}

	rebuilt := make([]string, 0, len(args)+len(roots))
	packageSeen := false
	for _, arg := range args {
		trimmed := strings.TrimSpace(arg)
		if trimmed == "" {
			continue
		}
		if packageSeen && filepath.IsAbs(trimmed) {
			continue
		}
		rebuilt = append(rebuilt, arg)
		if strings.Contains(trimmed, "server-filesystem") {
			packageSeen = true
		}
	}

	return append(rebuilt, roots...)
}

func cloneRuntimeAgent(src *agent.Agent) *agent.Agent {
	if src == nil {
		return nil
	}

	cloned := *src
	if len(src.Capabilities) > 0 {
		cloned.Capabilities = append([]string{}, src.Capabilities...)
	}
	if len(src.Plugins) > 0 {
		cloned.Plugins = make(map[string]types.LoadedPlugin, len(src.Plugins))
		for key, value := range src.Plugins {
			cloned.Plugins[key] = value
		}
	}
	return &cloned
}

func cloneServerConfig(src mcp.ServerConfig) mcp.ServerConfig {
	cloned := src
	if len(src.Args) > 0 {
		cloned.Args = append([]string{}, src.Args...)
	}
	if len(src.Env) > 0 {
		cloned.Env = make(map[string]string, len(src.Env))
		for key, value := range src.Env {
			cloned.Env[key] = value
		}
	}
	return cloned
}

func applyWorkspaceBindingRuntimeConfig(runtimeConfig *mcp.ServerConfig, config map[string]interface{}) {
	if runtimeConfig == nil || len(config) == 0 {
		return
	}

	if command, ok := stringFromConfigValue(config["command"]); ok {
		runtimeConfig.Command = command
	}
	if transport, ok := stringFromConfigValue(config["transport"]); ok {
		runtimeConfig.Transport = transport
	}
	if args, ok := stringSliceFromConfigValue(config["args"]); ok {
		runtimeConfig.Args = args
	}
	if env, ok := stringMapFromConfigValue(config["env"]); ok {
		if runtimeConfig.Env == nil {
			runtimeConfig.Env = make(map[string]string, len(env))
		}
		for key, value := range env {
			runtimeConfig.Env[key] = value
		}
	}
}

func stringFromConfigValue(value interface{}) (string, bool) {
	text, ok := value.(string)
	if !ok {
		return "", false
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return "", false
	}
	return text, true
}

func stringSliceFromConfigValue(value interface{}) ([]string, bool) {
	switch typed := value.(type) {
	case []string:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			trimmed := strings.TrimSpace(item)
			if trimmed != "" {
				out = append(out, trimmed)
			}
		}
		return out, len(out) > 0
	case []interface{}:
		out := make([]string, 0, len(typed))
		for _, raw := range typed {
			item, ok := raw.(string)
			if !ok {
				continue
			}
			trimmed := strings.TrimSpace(item)
			if trimmed != "" {
				out = append(out, trimmed)
			}
		}
		return out, len(out) > 0
	default:
		return nil, false
	}
}

func stringMapFromConfigValue(value interface{}) (map[string]string, bool) {
	rawMap, ok := value.(map[string]interface{})
	if !ok || len(rawMap) == 0 {
		return nil, false
	}

	out := make(map[string]string, len(rawMap))
	for key, raw := range rawMap {
		trimmedKey := strings.TrimSpace(key)
		if trimmedKey == "" {
			continue
		}

		text, ok := raw.(string)
		if !ok {
			continue
		}
		out[trimmedKey] = text
	}

	return out, len(out) > 0
}

func normalizeValueSet(values []string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		trimmed := strings.ToLower(strings.TrimSpace(value))
		if trimmed == "" {
			continue
		}
		set[trimmed] = true
	}
	return set
}

func dedupeStringsPreserveOrder(values []string) []string {
	if len(values) == 0 {
		return nil
	}

	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

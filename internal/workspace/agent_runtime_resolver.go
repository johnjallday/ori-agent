package workspace

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/mcp"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspacesettings"
)

const synthesizedFilesystemBindingID = "workspace-filesystem"
const synthesizedWorkspaceSettingsSkillBindingPrefix = "workspace-settings-"

var ErrAgentPaused = errors.New("agent is paused")

type runtimeMCPRegistry interface {
	UpsertServer(config mcp.ServerConfig) error
}

type mcpTemplateLookup interface {
	GetServer(name string) (*mcp.ServerConfig, error)
}

// ResolvedSkill is a minimal skill representation used by the runtime resolver
// to avoid importing the skills package directly.
type ResolvedSkill struct {
	Name               string
	Description        string
	Prompt             string
	Source             string
	AllowedTools       []string
	DisallowedTools    []string
	RequiredMCPServers []string
	Config             map[string]any
	Model              string
	Color              string
	Enabled            bool
	Trusted            bool
}

// SkillResolver resolves skills by name from all available sources.
type SkillResolver interface {
	ResolveSkillsByNames(skillNames []string) ([]ResolvedSkill, []string, error)
	ListEnabledAgentSkills(agentName string) ([]ResolvedSkill, error)
}

// ResolvedAgentRuntime is the effective runtime configuration for an agent executing inside a workspace.
type ResolvedAgentRuntime struct {
	Agent           *agent.Agent
	AgentInstance   *AgentInstance
	MCPServers      []string
	EffectiveSkills []ResolvedSkill
	// MCPToolAllowlist maps a materialized runtime server name (see
	// RuntimeMCPServerName) to the tool names its binding permits. A missing
	// key means the server carries no restriction (legacy all-tools
	// behavior, MCPBinding.AllowsAllTools()); a present key -- even an empty
	// slice -- means only those tool names may be listed or invoked for that
	// server. Callers that expose MCP tools to chat, workspace tasks,
	// missions, or delegated tasks must consult this map (see
	// chathttp.getMCPToolsForServer / LLMTaskHandler.getAgentMCPTools).
	MCPToolAllowlist map[string][]string
	// MCPRepoScope maps a materialized runtime server name to the single
	// GitHub repository ("owner/name") its binding is confined to. A missing
	// key means no repository constraint applies.
	//
	// It is a parallel to MCPToolAllowlist and is consulted at the same
	// points, because it answers the other half of the same question: the
	// allowlist says *which tools* a binding may use, this says *what they
	// may be pointed at*. Restricting the tool list alone would still let a
	// permitted list_issues call read someone else's repository -- and for
	// a fine-grained GitHub token, that means any public repo on GitHub,
	// which is why token scoping cannot cover this.
	MCPRepoScope map[string]string
}

// AgentRuntimeResolver composes base agent configuration with workspace-owned MCP bindings and skills.
type AgentRuntimeResolver struct {
	agentStore       store.Store
	workspaceStore   Store
	mcpRegistry      runtimeMCPRegistry
	mcpConfigManager mcpTemplateLookup
	skillResolver    SkillResolver
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

// SetSkillResolver configures the skill resolver for workspace skill binding resolution.
func (r *AgentRuntimeResolver) SetSkillResolver(sr SkillResolver) {
	if r != nil {
		r.skillResolver = sr
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

	var baseAgent *agent.Agent
	if strings.TrimSpace(workspaceID) != "" && r.workspaceStore != nil {
		if local, ok, err := r.workspaceStore.GetWorkspaceAgent(workspaceID, agentName); err != nil {
			logger.Warn("workspace-local agent lookup failed; falling back to global agent store", logger.Fields{
				"workspace_id": workspaceID,
				"agent":        agentName,
				"error":        err.Error(),
			})
		} else if ok && local != nil {
			baseAgent = local
		}
	}
	if baseAgent == nil {
		if globalAgent, ok := r.agentStore.GetAgent(agentName); ok && globalAgent != nil {
			baseAgent = globalAgent
		}
	}
	if baseAgent == nil {
		return nil, fmt.Errorf("agent %s not found", agentName)
	}
	if baseAgent.Status == types.AgentStatusDisabled {
		return nil, fmt.Errorf("%w: %s", ErrAgentPaused, agentName)
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

	// An instance with an explicit Toolbox assignment resolves from that
	// assignment alone (PRD §9.4). The legacy merge below runs only for
	// instances that have not been migrated yet — a workspace mid-migration, or
	// an instance attached in the same request that resolves it.
	definition, recipe, assigned, err := toolboxAssignmentForInstance(ws, instance)
	if err != nil {
		return nil, err
	}
	if assigned {
		return r.resolveRuntimeFromToolbox(ws, instance, agentName, resolved, definition, recipe)
	}

	// Resolve effective skills
	resolved.EffectiveSkills = r.resolveEffectiveSkills(ws, instance, agentName)

	allowedBindings, overriddenServerNames := r.resolveWorkspaceBindings(ws, instance)
	if len(overriddenServerNames) == 0 {
		return resolved, nil
	}

	effectiveServers := make([]string, 0, len(allowedBindings))
	var toolAllowlist map[string][]string
	var repoScope map[string]string
	for _, binding := range allowedBindings {
		runtimeName, err := r.materializeRuntimeBinding(ws.ID, binding)
		if err != nil {
			return nil, err
		}
		effectiveServers = append(effectiveServers, runtimeName)
		if !binding.AllowsAllTools() {
			if toolAllowlist == nil {
				toolAllowlist = make(map[string][]string, len(allowedBindings))
			}
			toolAllowlist[runtimeName] = append([]string(nil), binding.AllowedTools...)
		}
		if repo := BindingRepoScope(binding); repo != "" {
			if repoScope == nil {
				repoScope = make(map[string]string, len(allowedBindings))
			}
			repoScope[runtimeName] = repo
		}
	}

	resolved.MCPServers = dedupeStringsPreserveOrder(effectiveServers)
	resolved.MCPToolAllowlist = toolAllowlist
	resolved.MCPRepoScope = repoScope
	return resolved, nil
}

// BindingRepoScope returns the single repository a binding is confined to, or
// "" when it carries no such constraint.
//
// The key is defined here rather than in the GitHub package because this is
// where it is read: internal/githubhttp writes it, but it imports this package
// and so cannot be imported back.
func BindingRepoScope(binding MCPBinding) string {
	if binding.Scope == nil {
		return ""
	}
	repo, ok := binding.Scope["repo"].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(repo)
}

func (r *AgentRuntimeResolver) resolveWorkspaceBindings(ws *Workspace, instance *AgentInstance) ([]MCPBinding, []string) {
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

	enabledBindings := make([]MCPBinding, 0, len(bindings))
	overriddenServerNames := make([]string, 0, len(bindings))
	for _, binding := range bindings {
		serverName := strings.TrimSpace(binding.ServerName)
		if serverName == "" {
			continue
		}
		// Native capability bindings (email) have no MCP server template. They must
		// be excluded here — before template lookup, before MCP override
		// bookkeeping, and before tool-allowlist bookkeeping — because a `gmail`
		// binding is authorization for Ori's own mailbox tools, not a server to
		// launch (FR 24, 25). The binding stays in workspace state, where the
		// mailbox access gate reads it (FR 26).
		if !binding.IsRuntimeMCP() {
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

	filtered := make([]MCPBinding, 0, len(enabledBindings))
	for _, binding := range enabledBindings {
		if allowedIDs[strings.ToLower(strings.TrimSpace(binding.ID))] {
			filtered = append(filtered, binding)
		}
	}

	return filtered, dedupeStringsPreserveOrder(overriddenServerNames)
}

func synthesizeFilesystemBinding(bindings []MCPBinding, ws *Workspace) *MCPBinding {
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

	return &MCPBinding{
		ID:         synthesizedFilesystemBindingID,
		ServerName: "filesystem",
		Alias:      "workspace_filesystem",
		Enabled:    true,
		Scope: map[string]any{
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

// MaterializeRuntimeBinding materializes one workspace MCP binding as a runtime
// server and returns its name, so a caller can invoke a tool on exactly that
// binding.
//
// It is exported for features that call an MCP tool themselves rather than
// through an agent — Downloads Janitor issues its own move_file after its own
// validation, precisely so the agent never holds a mutation tool. The binding's
// scoping (its roots, and its AllowedTools) is the binding's own; this only
// instantiates it.
func (r *AgentRuntimeResolver) MaterializeRuntimeBinding(workspaceID string, binding MCPBinding) (string, error) {
	return r.materializeRuntimeBinding(workspaceID, binding)
}

func (r *AgentRuntimeResolver) materializeRuntimeBinding(workspaceID string, binding MCPBinding) (string, error) {
	templateName := strings.TrimSpace(binding.ServerName)
	if templateName == "" {
		return "", fmt.Errorf("binding %s has no server name", binding.ID)
	}

	// Fail-closed guard for direct callers (MaterializeRuntimeBinding is exported).
	// Reaching a template lookup with a native — or unclassifiable — binding is a
	// programming error, and answering it by registering a stand-in server would
	// be worse than failing (FR 24, 28).
	kind, err := binding.EffectiveRuntimeKind()
	if err != nil {
		return "", fmt.Errorf("classify binding %s: %w", binding.ID, err)
	}
	if kind != RuntimeKindMCP {
		return "", fmt.Errorf("binding %s is a %s capability and has no MCP server to materialize", binding.ID, kind)
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
		roots := extractFilesystemRootsFromBinding(binding)
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

// RuntimeMCPServerName returns the materialized runtime server name for a workspace MCP binding.
// The format is "ws:{workspaceID}:mcp:{serverName}:{bindingID}".
// Assumption: workspaceID, serverName, and bindingID must not contain colons.
func RuntimeMCPServerName(workspaceID, serverName, bindingID string) string {
	return fmt.Sprintf(
		"ws:%s:mcp:%s:%s",
		strings.TrimSpace(workspaceID),
		strings.TrimSpace(serverName),
		strings.TrimSpace(bindingID),
	)
}

// ParseRuntimeMCPServerName extracts workspace, logical server, and binding IDs from a runtime server name.
// Assumes parts do not contain colons (see RuntimeMCPServerName).
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

func extractFilesystemRoots(scope map[string]any) []string {
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
	case []any:
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

func extractFilesystemRootsFromBinding(binding MCPBinding) []string {
	if roots := extractFilesystemRoots(binding.Config); len(roots) > 0 {
		return roots
	}
	return extractFilesystemRoots(binding.Scope)
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
	// Deep-copied so a workspace runtime can never write through a shared
	// pointer into the reusable agent's direct-chat selection (FR-26, FR-156).
	cloned.DefaultToolbox = src.DefaultToolbox.Clone()
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

func applyWorkspaceBindingRuntimeConfig(runtimeConfig *mcp.ServerConfig, config map[string]any) {
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

func stringFromConfigValue(value any) (string, bool) {
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

func stringSliceFromConfigValue(value any) ([]string, bool) {
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
	case []any:
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

func stringMapFromConfigValue(value any) (map[string]string, bool) {
	rawMap, ok := value.(map[string]any)
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

// resolveWorkspaceSkillBindings filters workspace skill bindings by enabled state
// and per-agent access control, returning the allowed bindings.
func (r *AgentRuntimeResolver) resolveWorkspaceSkillBindings(ws *Workspace, instance *AgentInstance) []SkillBinding {
	if ws == nil {
		return nil
	}

	bindings := ws.GetSkillBindings()
	if len(bindings) == 0 {
		return nil
	}

	enabledBindings := make([]SkillBinding, 0, len(bindings))
	for _, binding := range bindings {
		if strings.TrimSpace(binding.SkillName) == "" || !binding.Enabled {
			continue
		}
		enabledBindings = append(enabledBindings, binding)
	}

	if len(enabledBindings) == 0 || instance == nil {
		return enabledBindings
	}

	accessEntry, ok := ws.GetAgentSkillAccess(instance.ID)
	if !ok {
		return enabledBindings
	}

	allowedIDs := normalizeValueSet(accessEntry.EnabledBindingIDs)
	if len(allowedIDs) == 0 {
		return nil
	}

	filtered := make([]SkillBinding, 0, len(enabledBindings))
	for _, binding := range enabledBindings {
		if allowedIDs[strings.ToLower(strings.TrimSpace(binding.ID))] {
			filtered = append(filtered, binding)
		}
	}

	return filtered
}

func (r *AgentRuntimeResolver) resolveSettingsManagedSkillBindings(ws *Workspace, agentName string) []SkillBinding {
	if ws == nil {
		return nil
	}

	entryAgentName := strings.TrimSpace(ws.EntryAgentName())
	if entryAgentName == "" || !strings.EqualFold(entryAgentName, strings.TrimSpace(agentName)) {
		return nil
	}

	effective := workspacesettings.BuildEffectiveBehavior(workspacesettings.Extract(ws.SharedData))
	if len(effective.ManagedSkills) == 0 {
		return nil
	}

	bindings := make([]SkillBinding, 0, len(effective.ManagedSkills))
	for _, managed := range effective.ManagedSkills {
		skillName := strings.TrimSpace(managed.SkillName)
		if skillName == "" || !managed.Active {
			continue
		}
		bindings = append(bindings, SkillBinding{
			ID:        synthesizedWorkspaceSettingsSkillBindingPrefix + strings.ToLower(skillName),
			SkillName: skillName,
			Enabled:   true,
			Trusted:   false,
			Config:    cloneInterfaceMap(managed.Config),
		})
	}

	return bindings
}

// resolveEffectiveSkills merges workspace skill bindings with agent-specific skills.
// Agent-specific skills take priority on name collision.
func (r *AgentRuntimeResolver) resolveEffectiveSkills(ws *Workspace, instance *AgentInstance, agentName string) []ResolvedSkill {
	if r.skillResolver == nil {
		return nil
	}

	// Load agent-specific skills (highest priority)
	var agentSkills []ResolvedSkill
	agentSkills, err := r.skillResolver.ListEnabledAgentSkills(agentName)
	if err != nil {
		logger.Warn("Failed to load agent-specific skills", logger.Fields{
			"agent": agentName,
			"error": err,
		})
	}

	// Build set of agent skill names for deduplication
	agentSkillNames := make(map[string]struct{}, len(agentSkills))
	for _, s := range agentSkills {
		agentSkillNames[strings.ToLower(strings.TrimSpace(s.Name))] = struct{}{}
	}

	// Resolve workspace skill bindings
	allowedBindings := r.resolveWorkspaceSkillBindings(ws, instance)
	if managedBindings := r.resolveSettingsManagedSkillBindings(ws, agentName); len(managedBindings) > 0 {
		existingNames := make(map[string]struct{}, len(allowedBindings))
		for _, binding := range allowedBindings {
			existingNames[strings.ToLower(strings.TrimSpace(binding.SkillName))] = struct{}{}
		}
		for _, binding := range managedBindings {
			key := strings.ToLower(strings.TrimSpace(binding.SkillName))
			if key == "" {
				continue
			}
			if _, exists := existingNames[key]; exists {
				continue
			}
			existingNames[key] = struct{}{}
			allowedBindings = append(allowedBindings, binding)
		}
	}
	if len(allowedBindings) == 0 {
		return agentSkills
	}

	skillNames := make([]string, 0, len(allowedBindings))
	bindingMap := make(map[string]SkillBinding, len(allowedBindings))
	for _, binding := range allowedBindings {
		name := strings.TrimSpace(binding.SkillName)
		// Skip workspace skills that are overridden by agent-specific skills
		if _, overridden := agentSkillNames[strings.ToLower(name)]; overridden {
			continue
		}
		skillNames = append(skillNames, name)
		bindingMap[strings.ToLower(name)] = binding
	}

	if len(skillNames) == 0 {
		return agentSkills
	}

	wsSkills, unresolved, err := r.skillResolver.ResolveSkillsByNames(skillNames)
	if err != nil {
		logger.Warn("Failed to resolve workspace skill bindings", logger.Fields{
			"error": err,
		})
		return agentSkills
	}
	if len(unresolved) > 0 {
		logger.Warn("Some workspace skill bindings could not be resolved", logger.Fields{
			"unresolved": unresolved,
		})
	}

	// Apply workspace binding overrides (trusted flag)
	for i := range wsSkills {
		key := strings.ToLower(strings.TrimSpace(wsSkills[i].Name))
		if binding, ok := bindingMap[key]; ok {
			wsSkills[i].Trusted = binding.Trusted
			wsSkills[i].Enabled = true
			wsSkills[i].Config = cloneInterfaceMap(binding.Config)
		}
	}

	// Merge: agent-specific first, then workspace
	effective := make([]ResolvedSkill, 0, len(agentSkills)+len(wsSkills))
	effective = append(effective, agentSkills...)
	effective = append(effective, wsSkills...)
	return effective
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

package sessionhttp

import (
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
)

// createdAgent is a roster entry the seeder newly created (not reused), carried
// so the post-persist pass can bind its per-agent tools. Reused agents are
// omitted — their existing tools are left untouched (PRD FR13/FR14).
type createdAgent struct {
	Name  string
	Tools projecttemplates.ToolDefaults
}

// seedAgentsResult is the outcome of seeding a template's agent roster.
type seedAgentsResult struct {
	Created  []createdAgent
	Warnings []string
	// ReuseNotices carries an informational message for each roster entry that
	// matched an existing global agent by name and was attached as-is (its saved
	// prompt/model/tools win over the template's). Surfaced to the user so the
	// name-match reuse is visible rather than silent (PRD FR7).
	ReuseNotices []string
	EntrySet     bool
}

type templateAgentPlan struct {
	HasAgents             bool                    `json:"has_agents"`
	TemplateID            string                  `json:"template_id,omitempty"`
	TemplateName          string                  `json:"template_name,omitempty"`
	EntryAgentName        string                  `json:"entry_agent_name,omitempty"`
	SystemModelConfigured bool                    `json:"system_model_configured"`
	SystemProvider        string                  `json:"system_provider,omitempty"`
	SystemModel           string                  `json:"system_model,omitempty"`
	Agents                []templateAgentPlanItem `json:"agents"`
	Warnings              []string                `json:"warnings,omitempty"`
}

type templateAgentPlanItem struct {
	Name string `json:"name"`
	// Scope is intentionally explicit in the plan so the UI never describes a
	// template agent as workspace-only. Template agents are global reusable
	// definitions; a workspace gets an attachment with its own role, context,
	// and custom instructions.
	Scope           string                        `json:"scope"`
	Action          string                        `json:"action"`
	EntryPoint      bool                          `json:"entry_point"`
	Role            string                        `json:"role,omitempty"`
	Type            string                        `json:"type,omitempty"`
	Model           string                        `json:"model,omitempty"`
	Provider        string                        `json:"provider,omitempty"`
	ReasoningEffort string                        `json:"reasoning_effort,omitempty"`
	SystemPrompt    string                        `json:"system_prompt,omitempty"`
	ModelSource     string                        `json:"model_source,omitempty"`
	Tools           projecttemplates.ToolDefaults `json:"tools,omitempty"`
	Warning         string                        `json:"warning,omitempty"`
}

type templateAgentOverride struct {
	Index        *int    `json:"index"`
	Name         *string `json:"name,omitempty"`
	Role         *string `json:"role,omitempty"`
	Type         *string `json:"type,omitempty"`
	Model        *string `json:"model,omitempty"`
	Provider     *string `json:"provider,omitempty"`
	SystemPrompt *string `json:"system_prompt,omitempty"`
}

// blankWorkspaceEntryAgentName is the reusable entry agent seeded for the Blank
// blueprint. Like every other template's entry agent (e.g. "Reaper Producer"),
// it is a normal global agent reused on name-match across blank workspaces.
const blankWorkspaceEntryAgentName = "Workspace Manager"

const blankWorkspaceEntryPrompt = "You are the workspace manager. Act as the default front door for this workspace: " +
	"clarify user intent, answer directly when the request only needs shared context, and break work into " +
	"tasks for specialists when needed."

// blankWorkspaceTemplate is the synthetic single-agent roster for the Blank
// blueprint. It flows through the normal template-agent plan/seed machinery so a
// blank workspace ships with a reviewable, editable entry agent — but it carries
// no skeleton, starter tasks, or project, so only its Agents roster is ever used.
func blankWorkspaceTemplate() projecttemplates.Template {
	return projecttemplates.Template{
		Name: "Blank workspace",
		Agents: []projecttemplates.AgentSpec{{
			Name:         blankWorkspaceEntryAgentName,
			Role:         string(types.RoleOrchestrator),
			Type:         agent.TypeGeneral,
			SystemPrompt: blankWorkspaceEntryPrompt,
		}},
	}
}

// validAgentTypes canonicalizes a template-declared agent type to the real
// vocabulary; an empty/unrecognized value maps to "" so the store applies its
// own default (PRD FR8).
var validAgentTypes = map[string]string{
	agent.TypeToolCalling: agent.TypeToolCalling,
	agent.TypeGeneral:     agent.TypeGeneral,
	agent.TypeResearch:    agent.TypeResearch,
}

// validAgentRoles canonicalizes a template-declared role. cli_agent is
// intentionally omitted: CLI agents are a v1 non-goal, so a template cannot mint
// one — an unrecognized role falls back to the store default.
var validAgentRoles = map[string]string{
	string(types.RoleOrchestrator): string(types.RoleOrchestrator),
	string(types.RoleResearcher):   string(types.RoleResearcher),
	string(types.RoleAnalyzer):     string(types.RoleAnalyzer),
	string(types.RoleSynthesizer):  string(types.RoleSynthesizer),
	string(types.RoleValidator):    string(types.RoleValidator),
	string(types.RoleSpecialist):   string(types.RoleSpecialist),
	string(types.RoleGeneral):      string(types.RoleGeneral),
}

func canonicalAgentType(s string) string {
	return validAgentTypes[strings.ToLower(strings.TrimSpace(s))]
}

func canonicalAgentRole(s string) string {
	return validAgentRoles[strings.ToLower(strings.TrimSpace(s))]
}

func applyTemplateAgentOverrides(tpl projecttemplates.Template, overrides []templateAgentOverride) (projecttemplates.Template, error) {
	if len(overrides) == 0 {
		return tpl, nil
	}
	if !tpl.HasAgents() {
		return tpl, fmt.Errorf("template has no agents to customize")
	}

	next := tpl
	next.Agents = append([]projecttemplates.AgentSpec(nil), tpl.Agents...)
	seenIndexes := make(map[int]struct{}, len(overrides))
	for _, override := range overrides {
		if override.Index == nil {
			return tpl, fmt.Errorf("template agent override is missing an index")
		}
		idx := *override.Index
		if idx < 0 || idx >= len(next.Agents) {
			return tpl, fmt.Errorf("template agent override index %d is out of range", idx)
		}
		if _, exists := seenIndexes[idx]; exists {
			return tpl, fmt.Errorf("template agent override index %d is duplicated", idx)
		}
		seenIndexes[idx] = struct{}{}

		spec := next.Agents[idx]
		if override.Name != nil {
			name := strings.TrimSpace(*override.Name)
			if err := validateTemplateAgentOverrideName(name); err != nil {
				return tpl, err
			}
			spec.Name = name
		}
		if override.Role != nil {
			spec.Role = strings.TrimSpace(*override.Role)
		}
		if override.Type != nil {
			spec.Type = strings.TrimSpace(*override.Type)
		}
		if override.Model != nil {
			spec.Model = strings.TrimSpace(*override.Model)
		}
		if override.Provider != nil {
			spec.Provider = strings.TrimSpace(*override.Provider)
		}
		if override.SystemPrompt != nil {
			spec.SystemPrompt = strings.TrimSpace(*override.SystemPrompt)
			if err := projecttemplates.ValidateAgentPrompts([]projecttemplates.AgentSpec{spec}); err != nil {
				return tpl, err
			}
		}
		next.Agents[idx] = spec
	}
	if err := validateTemplateAgentOverrideNames(next.Agents); err != nil {
		return tpl, err
	}
	return next, nil
}

func validateTemplateAgentOverrideName(name string) error {
	if name == "" {
		return fmt.Errorf("template agent name cannot be empty")
	}
	if len(name) > 100 {
		return fmt.Errorf("template agent name %q is too long (max 100 characters)", name)
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == ' ' || r == '_' || r == '-':
		default:
			return fmt.Errorf("template agent name %q contains invalid characters (only alphanumeric, spaces, underscores, and hyphens allowed)", name)
		}
	}
	return nil
}

func validateTemplateAgentOverrideNames(specs []projecttemplates.AgentSpec) error {
	seen := make(map[string]string, len(specs))
	for _, spec := range specs {
		name := strings.TrimSpace(spec.Name)
		if name == "" {
			return fmt.Errorf("template agent name cannot be empty")
		}
		key := strings.ToLower(name)
		if original, exists := seen[key]; exists {
			return fmt.Errorf("template agent name %q duplicates %q", name, original)
		}
		seen[key] = name
	}
	return nil
}

func (h *Handler) templateAgentCreateConfig(spec projecttemplates.AgentSpec) (*store.CreateAgentConfig, string) {
	model, provider, reasoningEffort, modelSource := h.templateAgentModelDefaults(spec)
	return &store.CreateAgentConfig{
		Type:            canonicalAgentType(spec.Type),
		Role:            types.AgentRole(canonicalAgentRole(spec.Role)),
		Model:           model,
		LLMProvider:     provider,
		ReasoningEffort: reasoningEffort,
		SystemPrompt:    spec.SystemPrompt,
	}, modelSource
}

// seedTemplateAgents creates (or reuses) the agents a template declares and
// attaches them to ws in roster order. The first declared agent becomes the
// workspace entry agent; the rest are specialist sub-agents. It runs before the
// workspace is persisted so the roster is part of the stored agent list and the
// entry agent suppresses the mandatory "create an entry agent" prompt.
//
// Reuse-on-name-match (PRD FR13): a name that already exists as a global agent is
// attached as-is and never mutated — the template's prompt/model/tools for that
// entry are ignored. Only unmatched names create a new global agent.
//
// Failure handling (PRD FR15): a specialist that fails to create is recorded as a
// warning and skipped; if the entry agent (first) fails, seeding stops and the
// workspace is left agent-less so the mandatory-prompt fallback runs — no
// specialist is promoted in its place.
func (h *Handler) seedTemplateAgents(ws *session.Workspace, tpl projecttemplates.Template) seedAgentsResult {
	var result seedAgentsResult
	if h == nil || h.agentStore == nil || ws == nil || !tpl.HasAgents() {
		return result
	}

	for i, spec := range tpl.Agents {
		isEntry := i == 0
		_, exists := h.agentStore.GetAgent(spec.Name)
		if !exists {
			cfg, _ := h.templateAgentCreateConfig(spec)
			if err := h.agentStore.CreateAgent(spec.Name, cfg); err != nil {
				if isEntry {
					logger.Warn("Failed to seed template entry agent; falling back to entry-agent prompt",
						logger.Fields{"workspace": ws.ID, "agent": spec.Name, "error": err})
					result.Warnings = append(result.Warnings,
						fmt.Sprintf("Entry agent %q could not be created - you'll be prompted to add one.", spec.Name))
					return result
				}
				logger.Warn("Failed to seed template specialist agent (skipped)",
					logger.Fields{"workspace": ws.ID, "agent": spec.Name, "error": err})
				result.Warnings = append(result.Warnings,
					fmt.Sprintf("Specialist agent %q could not be created and was skipped.", spec.Name))
				continue
			}
		}

		if exists {
			// Name-match reuse: the template's declared prompt/model/tools for
			// this entry are ignored in favor of the existing definition. Make
			// that visible (PRD FR7).
			result.ReuseNotices = append(result.ReuseNotices,
				fmt.Sprintf("Reusing existing agent %q - its saved prompt, model, and tools are used, not the template's.", spec.Name))
		}

		if isEntry {
			setWorkspaceEntryAgent(ws, spec.Name)
			result.EntrySet = true
		} else {
			attachWorkspaceSpecialist(ws, spec.Name)
		}
		// Only newly-created agents get the template's per-agent tools; a reused
		// agent keeps its own (PRD FR14).
		if !exists && !spec.Tools.IsEmpty() {
			result.Created = append(result.Created, createdAgent{Name: spec.Name, Tools: spec.Tools})
		}
	}

	return result
}

func (h *Handler) templateAgentModelDefaults(spec projecttemplates.AgentSpec) (model, provider, reasoningEffort, source string) {
	model = strings.TrimSpace(spec.Model)
	provider = strings.TrimSpace(spec.Provider)
	if model != "" || h == nil || h.systemModelReader == nil {
		if model != "" {
			return model, provider, "", "template"
		}
		return "", "", "", "agent_default"
	}

	provider, model = h.systemModelReader.GetSystemModel()
	provider = strings.TrimSpace(provider)
	model = strings.TrimSpace(model)
	if provider == "" || model == "" {
		return "", "", "", "agent_default"
	}
	if strings.EqualFold(provider, "codex") {
		reasoningEffort = h.systemModelReader.GetSystemReasoningEffort()
	}
	return model, provider, reasoningEffort, "system"
}

func (h *Handler) buildTemplateAgentPlan(tpl projecttemplates.Template) templateAgentPlan {
	plan := templateAgentPlan{
		HasAgents:    tpl.HasAgents(),
		TemplateID:   tpl.ID,
		TemplateName: tpl.Name,
		Agents:       []templateAgentPlanItem{},
	}
	if h != nil && h.systemModelReader != nil {
		provider, model := h.systemModelReader.GetSystemModel()
		plan.SystemProvider = strings.TrimSpace(provider)
		plan.SystemModel = strings.TrimSpace(model)
		plan.SystemModelConfigured = plan.SystemProvider != "" && plan.SystemModel != ""
	}
	if !tpl.HasAgents() {
		return plan
	}

	for i, spec := range tpl.Agents {
		item := h.buildTemplateAgentPlanItem(spec, i == 0)
		if item.EntryPoint {
			plan.EntryAgentName = item.Name
		}
		if item.Warning != "" {
			plan.Warnings = append(plan.Warnings, item.Warning)
		}
		plan.Agents = append(plan.Agents, item)
	}
	return plan
}

func (h *Handler) buildTemplateAgentPlanItem(spec projecttemplates.AgentSpec, entryPoint bool) templateAgentPlanItem {
	name := strings.TrimSpace(spec.Name)
	item := templateAgentPlanItem{
		Name:       name,
		Scope:      "reusable",
		Action:     "create",
		EntryPoint: entryPoint,
		Tools:      spec.Tools,
	}

	if h != nil && h.agentStore != nil {
		if ag, exists := h.agentStore.GetAgent(name); exists && ag != nil {
			item.Action = "reuse"
			item.Type = strings.TrimSpace(ag.Type)
			item.Role = strings.TrimSpace(string(ag.Role))
			item.Model = strings.TrimSpace(ag.Settings.Model)
			item.Provider = strings.TrimSpace(ag.Settings.Provider)
			item.ReasoningEffort = strings.TrimSpace(ag.Settings.EffectiveReasoningEffort(ag.Settings.Provider))
			item.SystemPrompt = strings.TrimSpace(ag.Settings.SystemPrompt)
			item.ModelSource = "existing"
			item.Tools = projecttemplates.ToolDefaults{}
			item.Warning = fmt.Sprintf("Reusing existing agent %q - its saved prompt, model, and tools are used, not the template's.", name)
			return item
		}
	}

	cfg, modelSource := h.templateAgentCreateConfig(spec)
	item.Type = strings.TrimSpace(cfg.Type)
	if item.Type == "" {
		if cfg.Model != "" {
			item.Type = agent.GetTypeForModel(cfg.Model)
		} else {
			item.Type = agent.TypeToolCalling
		}
	}
	item.Role = strings.TrimSpace(string(cfg.Role))
	if item.Role == "" {
		item.Role = string(types.RoleGeneral)
	}
	item.Model = strings.TrimSpace(cfg.Model)
	item.Provider = strings.TrimSpace(cfg.LLMProvider)
	item.ReasoningEffort = strings.TrimSpace(cfg.ReasoningEffort)
	item.SystemPrompt = strings.TrimSpace(cfg.SystemPrompt)
	item.ModelSource = modelSource
	if item.Model == "" {
		item.Warning = fmt.Sprintf("Agent %q has no template or system model; it will use the app's default agent model.", name)
	}
	return item
}

// bindSeededAgentTools binds per-agent tools for the agents the seeder created,
// after the workspace is persisted (MCP binding reads the stored workspace).
// Apply-if-present and non-fatal: a missing skill/server becomes a warning, not
// a failure. Returns warnings to surface to the user.
func (h *Handler) bindSeededAgentTools(workspaceID string, created []createdAgent) []string {
	if h == nil || h.applyAgentTools == nil || len(created) == 0 {
		return nil
	}
	var warnings []string
	for _, ca := range created {
		if ca.Tools.IsEmpty() {
			continue
		}
		if _, missing := h.applyAgentTools(workspaceID, ca.Name, ca.Tools); len(missing) > 0 {
			logger.Info("Some template agent tools were not found (skipped)",
				logger.Fields{"workspace": workspaceID, "agent": ca.Name, "missing": missing})
			warnings = append(warnings,
				fmt.Sprintf("Some tools for agent %q were not found and were skipped: %s", ca.Name, strings.Join(missing, ", ")))
		}
	}
	return warnings
}

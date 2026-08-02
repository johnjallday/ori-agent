package workspace

import (
	"fmt"
	"sort"
	"strings"
)

// Preview: resolving a Toolbox against the live workspace WITHOUT changing
// anything (PRD FR-73–FR-77).
//
// Purity is the whole contract. A preview reads the current Workshop, the
// current connection state, and the current agent, and returns exactly what
// would happen — it never installs software, enables a disabled binding,
// requests credentials, opens OAuth, changes trust, classifies a side effect,
// widens a scope, or touches an agent (FR-76). Everything in this file operates
// on values read from the workspace and returns a new struct; there is no store
// handle here to write through even by accident.
//
// The reason that matters: the preview is what a user reads before consenting.
// A preview with side effects would mean consent came after the fact.

// Readiness states (FR-73).
const (
	// ReadinessReady means every referenced capability exists, is enabled, has
	// its connection, fits capacity, and needs no new permission decision.
	ReadinessReady = "Ready"
	// ReadinessNeedsConnection means a referenced binding exists but is off.
	ReadinessNeedsConnection = "Needs connection"
	// ReadinessNeedsApproval means an operation's side effect is unclassified,
	// so it fails closed under a Goal's autonomy gate until the user says what
	// it does (FR-159).
	ReadinessNeedsApproval = "Needs approval"
	// ReadinessMissingCapability means a required capability is not in this
	// workspace at all — including an explicit unmet **Add requirement**.
	ReadinessMissingCapability = "Missing capability"
	// ReadinessToolboxFull means the selection exceeds the agent's stage
	// capacity and Expert mode is off (FR-33, FR-56).
	ReadinessToolboxFull = "Toolbox full"
	// ReadinessNeedsRepair means the saved recipe references something that no
	// longer resolves in a way the user must reconcile — a binding whose tools
	// were never pinned, or a skill whose source disappeared.
	ReadinessNeedsRepair = "Needs repair"
	// ReadinessArchived means the Toolbox may no longer be newly selected.
	ReadinessArchived = "Archived"
)

// ToolboxIssue is one concrete thing standing between a Toolbox and `Ready`.
//
// Each is a separate step by construction, which is what lets **Review & Use**
// present them individually and never treat completing one as approving another
// (FR-80).
type ToolboxIssue struct {
	// State is the readiness state this issue produces.
	State string `json:"state"`
	// CapabilityID / BindingID identify what the issue is about.
	CapabilityID string `json:"capability_id,omitempty"`
	BindingID    string `json:"binding_id,omitempty"`
	// Message is the plain-language explanation shown to the user.
	Message string `json:"message"`
	// Action names the user action that resolves it, e.g. "connect",
	// "classify", "install", "remove_skill". The UI links each to the existing
	// flow that owns it rather than performing it inline (FR-79, FR-80).
	Action string `json:"action,omitempty"`
	// Blocking marks a hard failure. A non-blocking issue is a warning: an
	// OPTIONAL capability that is missing is simply omitted (FR-14).
	Blocking bool `json:"blocking"`
}

// ToolboxPreview is the complete read-only answer to "what would using this
// Toolbox mean for this agent instance right now?".
type ToolboxPreview struct {
	WorkspaceID      string `json:"workspace_id"`
	WorkspaceVersion int64  `json:"workspace_version"`
	AgentInstanceID  string `json:"agent_instance_id"`
	AgentName        string `json:"agent_name"`
	ToolboxID        string `json:"toolbox_id"`
	ToolboxName      string `json:"toolbox_name"`
	ToolboxVersion   int64  `json:"toolbox_version"`

	// Readiness is the single worst state across all issues (FR-73).
	Readiness string         `json:"readiness"`
	Issues    []ToolboxIssue `json:"issues,omitempty"`
	Focus     FocusResult    `json:"focus"`

	// Skills / MCPBindings are the EFFECTIVE capabilities — what the agent
	// would actually receive, with unresolvable optional entries already
	// dropped (FR-75).
	Skills      []PreviewSkill `json:"skills,omitempty"`
	MCPBindings []PreviewMCP   `json:"mcp_bindings,omitempty"`

	Capacity ToolboxCapacity `json:"capacity"`

	// Diff describes the change from the instance's current assignment. Nil
	// when the instance has none (FR-77).
	Diff *ToolboxDiff `json:"diff,omitempty"`
	// CurrentToolboxID / CurrentToolboxVersion identify what is in force now.
	CurrentToolboxID      string `json:"current_toolbox_id,omitempty"`
	CurrentToolboxVersion int64  `json:"current_toolbox_version,omitempty"`

	// ExpandsPermissions reports whether using this would grant any operation,
	// scope, or skill the instance does not already have. It is the gate
	// between one-click **Use This Toolbox** and **Review & Use** (FR-78).
	ExpandsPermissions bool `json:"expands_permissions"`
	// CanUseDirectly is true only when the Toolbox is Ready AND nothing
	// expands. Anything else routes through review (FR-78, FR-79).
	CanUseDirectly bool `json:"can_use_directly"`
}

// PreviewSkill is one effective skill with its resolved provenance.
type PreviewSkill struct {
	CapabilityID      string `json:"capability_id"`
	DisplayName       string `json:"display_name"`
	Source            string `json:"source"`
	BindingID         string `json:"binding_id,omitempty"`
	OwnerCapabilityID string `json:"owner_capability_id,omitempty"`
	Required          bool   `json:"required"`
	Available         bool   `json:"available"`
	Trusted           bool   `json:"trusted,omitempty"`
	// PromptChars contributes to the Focus context readout.
	PromptChars int `json:"prompt_chars,omitempty"`
}

// PreviewMCP is one effective binding with its exact exposed operations.
type PreviewMCP struct {
	BindingID         string   `json:"binding_id"`
	ServerName        string   `json:"server_name,omitempty"`
	Alias             string   `json:"alias,omitempty"`
	OwnerCapabilityID string   `json:"owner_capability_id,omitempty"`
	Required          bool     `json:"required"`
	Available         bool     `json:"available"`
	Connected         bool     `json:"connected"`
	AllowedTools      []string `json:"allowed_tools,omitempty"`
	// InheritsBindingTools marks an entry still deferring to the binding's own
	// policy — a repair, and an unknown real operation count (FR-13).
	InheritsBindingTools bool              `json:"inherits_binding_tools,omitempty"`
	Scope                map[string]any    `json:"scope,omitempty"`
	DefaultSideEffect    string            `json:"default_side_effect,omitempty"`
	ToolRisks            map[string]string `json:"tool_risks,omitempty"`
	UnclassifiedTools    []string          `json:"unclassified_tools,omitempty"`
}

// PreviewToolbox resolves one Toolbox version against the live workspace.
//
// It is a pure function of the workspace snapshot it is handed: nothing is
// written, and no argument is mutated (FR-74, FR-76).
func PreviewToolbox(
	ws *Workspace,
	instance *AgentInstance,
	definition ToolboxDefinition,
	recipe ToolboxRecipe,
	learned []ResolvedSkill,
	capacity int,
	expertMode bool,
	thresholds FocusThresholds,
) ToolboxPreview {
	preview := ToolboxPreview{
		WorkspaceID:      ws.ID,
		WorkspaceVersion: ws.Version,
		ToolboxID:        definition.ID,
		ToolboxName:      definition.Name,
		ToolboxVersion:   recipe.Version,
	}
	if instance != nil {
		preview.AgentInstanceID = instance.ID
		preview.AgentName = instance.Name
	}

	skillBindings := make(map[string]SkillBinding)
	for _, binding := range ws.GetSkillBindings() {
		skillBindings[strings.ToLower(strings.TrimSpace(binding.ID))] = binding
	}
	mcpBindings := make(map[string]MCPBinding)
	for _, binding := range ws.GetMCPBindings() {
		mcpBindings[strings.ToLower(strings.TrimSpace(binding.ID))] = binding
	}
	learnedByIdentity := make(map[string]ResolvedSkill, len(learned))
	for _, skill := range learned {
		learnedByIdentity[NormalizeToolboxCapabilityID(skill.Name)] = skill
	}

	var issues []ToolboxIssue
	inputs := FocusInputs{SkillCapacity: capacity, ExpertMode: expertMode}

	// --- Skills ---
	for _, ref := range recipe.Skills {
		entry := PreviewSkill{
			CapabilityID:      ref.CapabilityID,
			DisplayName:       firstNonEmpty(ref.DisplayName, ref.CapabilityID),
			Source:            NormalizeToolboxSource(ref.Source),
			BindingID:         ref.BindingID,
			OwnerCapabilityID: ref.OwnerCapabilityID,
			Required:          ref.Required,
		}

		switch entry.Source {
		case ToolboxSourceWorkspaceProvided:
			binding, exists := skillBindings[strings.ToLower(strings.TrimSpace(ref.BindingID))]
			switch {
			case !exists:
				entry.Available = false
				issues = append(issues, ToolboxIssue{
					State:        ReadinessMissingCapability,
					CapabilityID: ref.CapabilityID,
					Message:      fmt.Sprintf("%q is not available in this workspace.", entry.DisplayName),
					Action:       "install",
					Blocking:     ref.Required,
				})
			case !binding.Enabled:
				entry.Available = false
				issues = append(issues, ToolboxIssue{
					State:        ReadinessNeedsConnection,
					CapabilityID: ref.CapabilityID,
					BindingID:    binding.ID,
					Message:      fmt.Sprintf("%q is switched off in this workspace.", entry.DisplayName),
					Action:       "enable",
					Blocking:     ref.Required,
				})
			default:
				entry.Available = true
				entry.Trusted = binding.Trusted
			}
		case ToolboxSourceAgentLearned:
			skill, known := learnedByIdentity[ref.CapabilityID]
			// With no skill source wired there is nothing to check against, so
			// availability is not asserted either way rather than guessed.
			if len(learned) == 0 {
				entry.Available = true
			} else if !known {
				entry.Available = false
				issues = append(issues, ToolboxIssue{
					State:        ReadinessMissingCapability,
					CapabilityID: ref.CapabilityID,
					Message:      fmt.Sprintf("%s no longer has the %q skill.", previewAgentLabel(instance), entry.DisplayName),
					Action:       "install",
					Blocking:     ref.Required,
				})
			} else {
				entry.Available = true
				entry.PromptChars = len(skill.Prompt)
				entry.Trusted = skill.Trusted
			}
		default:
			entry.Available = true
		}

		if entry.Available {
			inputs.PromptChars += entry.PromptChars
		}
		preview.Skills = append(preview.Skills, entry)
	}

	// --- MCP bindings ---
	exposedByBinding := make(map[string][]string)
	effectiveBindings := make([]MCPBinding, 0, len(recipe.MCPBindings))
	for _, ref := range recipe.MCPBindings {
		key := strings.ToLower(strings.TrimSpace(ref.BindingID))
		binding, exists := mcpBindings[key]
		entry := PreviewMCP{
			BindingID:            ref.BindingID,
			OwnerCapabilityID:    ref.OwnerCapabilityID,
			Required:             ref.Required,
			AllowedTools:         append([]string(nil), ref.AllowedTools...),
			InheritsBindingTools: ref.InheritsBindingTools,
		}

		if !exists {
			issues = append(issues, ToolboxIssue{
				State:     ReadinessMissingCapability,
				BindingID: ref.BindingID,
				Message:   fmt.Sprintf("The connection %q is not set up in this workspace.", ref.BindingID),
				Action:    "install",
				Blocking:  ref.Required,
			})
			preview.MCPBindings = append(preview.MCPBindings, entry)
			continue
		}

		entry.ServerName = binding.ServerName
		entry.Alias = binding.Alias
		entry.Scope = binding.Scope
		entry.Connected = binding.Enabled
		entry.Available = binding.Enabled
		entry.DefaultSideEffect = string(binding.DefaultSideEffect)

		if !binding.Enabled {
			issues = append(issues, ToolboxIssue{
				State:     ReadinessNeedsConnection,
				BindingID: binding.ID,
				Message:   fmt.Sprintf("%q is set up but switched off.", firstNonEmpty(binding.Alias, binding.ServerName, binding.ID)),
				Action:    "connect",
				Blocking:  ref.Required,
			})
		}

		// An entry still deferring to the binding's policy has an unknown real
		// operation count, so it is a repair rather than a ready state (FR-13).
		exposed := entry.AllowedTools
		if ref.InheritsBindingTools {
			if binding.AllowsAllTools() {
				exposed = nil
				inputs.UnpinnedBindings++
				issues = append(issues, ToolboxIssue{
					State:     ReadinessNeedsRepair,
					BindingID: binding.ID,
					Message:   fmt.Sprintf("%q still allows every operation. Pin the exact tools this toolbox should expose.", firstNonEmpty(binding.Alias, binding.ServerName, binding.ID)),
					Action:    "pin_tools",
					Blocking:  false,
				})
			} else {
				exposed = append([]string(nil), binding.AllowedTools...)
				entry.AllowedTools = exposed
			}
		}

		if len(binding.ToolOverrides) > 0 {
			entry.ToolRisks = make(map[string]string, len(binding.ToolOverrides))
			for tool, effect := range binding.ToolOverrides {
				entry.ToolRisks[tool] = string(effect)
			}
		}

		for _, tool := range exposed {
			effect := binding.DefaultSideEffect
			if override, ok := binding.ToolOverrides[tool]; ok {
				effect = override
			}
			switch effect {
			case SideEffectRead:
				inputs.ReadOperations++
			case SideEffectWrite:
				inputs.WriteOperations++
			case SideEffectExternal:
				inputs.ExternalOperations++
			default:
				inputs.UnclassifiedOperations++
				entry.UnclassifiedTools = append(entry.UnclassifiedTools, tool)
			}
		}
		sort.Strings(entry.UnclassifiedTools)
		if len(entry.UnclassifiedTools) > 0 && binding.Enabled {
			// Unclassified operations fail closed under a Goal's autonomy gate,
			// so the Toolbox is not Ready until the user says what they do.
			issues = append(issues, ToolboxIssue{
				State:     ReadinessNeedsApproval,
				BindingID: binding.ID,
				Message: fmt.Sprintf("%d operation(s) on %q have no read/write classification and will be blocked until you set one.",
					len(entry.UnclassifiedTools), firstNonEmpty(binding.Alias, binding.ServerName, binding.ID)),
				Action:   "classify",
				Blocking: true,
			})
		}

		inputs.ExposedOperations += len(exposed)
		exposedByBinding[key] = exposed
		effectiveBindings = append(effectiveBindings, binding)
		preview.MCPBindings = append(preview.MCPBindings, entry)
	}

	// --- Core: always present, never consuming a skill space (FR-48, FR-58) ---
	if synthesized := synthesizeFilesystemBinding(ws.GetMCPBindings(), ws); synthesized != nil {
		inputs.CoreCapabilities++
	}
	inputs.CoreCapabilities += len(settingsManagedSkillBindings(ws, previewAgentName(instance)))

	inputs.ActiveSkills = countToolboxSkillSpaces(recipe.Skills)
	inputs.OverlapGroups = BuildFocusOverlapGroups(effectiveBindings, exposedByBinding)

	preview.Capacity = EvaluateToolboxCapacity(recipe.Skills, capacity, expertMode)
	if preview.Capacity.Full && preview.Capacity.Grandfathered {
		issues = append(issues, ToolboxIssue{
			State: ReadinessToolboxFull,
			Message: fmt.Sprintf("This toolbox uses %d skill spaces and %s has %d. You can remove or swap skills, or turn on expert mode.",
				preview.Capacity.Used, previewAgentLabel(instance), preview.Capacity.Capacity),
			Action:   "reduce_skills",
			Blocking: true,
		})
	}

	if definition.Archived() {
		issues = append(issues, ToolboxIssue{
			State:    ReadinessArchived,
			Message:  "This toolbox is archived and cannot be selected. Restore it first, or duplicate it into a new one.",
			Action:   "restore",
			Blocking: true,
		})
	}

	issues = sortToolboxIssues(issues)
	preview.Issues = issues
	preview.Readiness = worstReadiness(issues)
	preview.Focus = EvaluateFocus(inputs, thresholds, blockingIssueMessages(issues))
	return preview
}

func previewAgentName(instance *AgentInstance) string {
	if instance == nil {
		return ""
	}
	return instance.Name
}

func previewAgentLabel(instance *AgentInstance) string {
	if instance == nil || strings.TrimSpace(instance.Name) == "" {
		return "this agent"
	}
	return instance.Name
}

// readinessSeverity orders states worst-first so the overall readiness is the
// single most important thing standing in the way.
func readinessSeverity(state string) int {
	switch state {
	case ReadinessArchived:
		return 6
	case ReadinessNeedsRepair:
		return 5
	case ReadinessToolboxFull:
		return 4
	case ReadinessMissingCapability:
		return 3
	case ReadinessNeedsApproval:
		return 2
	case ReadinessNeedsConnection:
		return 1
	default:
		return 0
	}
}

func worstReadiness(issues []ToolboxIssue) string {
	state := ReadinessReady
	for _, issue := range issues {
		if !issue.Blocking {
			continue
		}
		if readinessSeverity(issue.State) > readinessSeverity(state) {
			state = issue.State
		}
	}
	// A non-blocking repair still means the Toolbox is not fully Ready — its
	// real surface is unknown — so it surfaces when nothing worse applies.
	if state == ReadinessReady {
		for _, issue := range issues {
			if issue.State == ReadinessNeedsRepair {
				return ReadinessNeedsRepair
			}
		}
	}
	return state
}

func blockingIssueMessages(issues []ToolboxIssue) []string {
	var messages []string
	for _, issue := range issues {
		if issue.Blocking {
			messages = append(messages, issue.Message)
		}
	}
	return messages
}

// sortToolboxIssues orders issues worst-first and then alphabetically, so the
// checklist a user reads is deterministic across requests (FR-69).
func sortToolboxIssues(issues []ToolboxIssue) []ToolboxIssue {
	sort.SliceStable(issues, func(i, j int) bool {
		if issues[i].Blocking != issues[j].Blocking {
			return issues[i].Blocking
		}
		if a, b := readinessSeverity(issues[i].State), readinessSeverity(issues[j].State); a != b {
			return a > b
		}
		return issues[i].Message < issues[j].Message
	})
	return issues
}

// ApplyCurrentAssignmentDiff fills in the change this preview represents
// relative to the instance's current assignment, and decides the Use/Review
// gate (FR-77, FR-78, FR-79).
func (p *ToolboxPreview) ApplyCurrentAssignmentDiff(ws *Workspace) {
	current, currentRecipe, assigned, err := ws.ResolveAssignedToolbox(p.AgentInstanceID)
	if err == nil && assigned {
		p.CurrentToolboxID = current.ID
		p.CurrentToolboxVersion = currentRecipe.Version
		target, _, targetErr := resolveToolboxVersion(ws, p.ToolboxID, p.ToolboxVersion)
		if targetErr == nil {
			diff := CompareToolboxRecipes(currentRecipe, target)
			p.Diff = &diff
			p.ExpandsPermissions = diff.ExpandsOperations()
		}
	} else {
		// No current assignment means everything this Toolbox grants is new.
		p.ExpandsPermissions = len(p.Skills) > 0 || len(p.MCPBindings) > 0
	}

	// One-click is offered ONLY for a ready, non-expanding switch. Every other
	// case — a missing connection, an unclassified operation, a widened
	// surface — routes through Review & Use so the decision is explicit
	// (FR-78, FR-79, success metric 2).
	p.CanUseDirectly = p.Readiness == ReadinessReady && !p.ExpandsPermissions
}

func resolveToolboxVersion(ws *Workspace, toolboxID string, version int64) (ToolboxRecipe, ToolboxDefinition, error) {
	definition, exists := ws.GetToolbox(toolboxID)
	if !exists {
		return ToolboxRecipe{}, ToolboxDefinition{}, fmt.Errorf("%w: %s", ErrToolboxNotFound, toolboxID)
	}
	recipe, err := definition.ResolveVersion(version)
	if err != nil {
		return ToolboxRecipe{}, ToolboxDefinition{}, err
	}
	return recipe, *definition, nil
}

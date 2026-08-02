package workspace

import (
	"fmt"
	"sort"
	"strings"
)

// Recommending a Toolbox for a Goal (PRD FR-95–FR-102).
//
// Two properties define this file.
//
// DETERMINISTIC. The same accepted brief and the same capability state produce
// the same ranking, every time (FR-95). That is not a nicety: a recommendation
// that reshuffled between page loads would be impossible to trust or to test,
// and users would learn to ignore it. So scoring is integer arithmetic over
// measured facts, ties break on stable identifiers, and nothing consults a
// clock or a model.
//
// INERT. Ranking reads; it never selects, applies, installs, connects, requests
// credentials, widens a scope, enables Expert mode, or raises autonomy (FR-99).
// The output is a list with explanations. Acting on one goes through the same
// preview and Review & Use gate as any other switch, which is what stops a
// recommendation from becoming a permission grant.

// ToolboxRecommendation is one ranked candidate with its reasoning.
type ToolboxRecommendation struct {
	ToolboxID      string `json:"toolbox_id"`
	ToolboxName    string `json:"toolbox_name"`
	ToolboxVersion int64  `json:"toolbox_version"`

	// Score is the ranking value. It is exposed for tests and debugging, NOT
	// as a user-facing quality claim — the UI shows the reasons, not this
	// (FR-72, FR-98).
	Score int `json:"score"`
	// Rank is the 1-based position after sorting.
	Rank int `json:"rank"`

	// Covers / Missing name the brief's requirements this Toolbox does and does
	// not satisfy, so "why this one" is answerable (FR-98).
	Covers  []string `json:"covers,omitempty"`
	Missing []string `json:"missing,omitempty"`
	// Extra names capabilities the goal did not ask for. They are not
	// disqualifying, but they are why a smaller toolbox outranks a bigger one
	// (FR-97).
	Extra []string `json:"extra,omitempty"`

	// Readiness and Focus come from the same preview the user would see, so a
	// recommendation cannot claim `Ready` for something preview would refuse
	// (FR-96, success metric 6).
	Readiness string      `json:"readiness"`
	Focus     FocusResult `json:"focus"`
	// SkillSpaces / Operations describe the size of the surface.
	SkillSpaces int `json:"skill_spaces"`
	Operations  int `json:"operations"`

	// IntroducesPermissions lists side-effect classes this Toolbox exposes
	// beyond the brief's autonomy ceiling (FR-98).
	IntroducesPermissions []string `json:"introduces_permissions,omitempty"`
	// ExceedsAutonomy marks a candidate whose surface goes past the ceiling
	// the user set. It is ranked DOWN, never filtered out — the user may
	// legitimately choose it and raise the ceiling deliberately (FR-99).
	ExceedsAutonomy bool `json:"exceeds_autonomy,omitempty"`

	// Explanation is the plain-language summary shown next to the candidate.
	Explanation string `json:"explanation"`
	// IsCurrent marks the Toolbox the entry agent is already using.
	IsCurrent bool `json:"is_current,omitempty"`
	// FullyCovers reports that every required capability and operation is
	// satisfied and the Toolbox is Ready.
	FullyCovers bool `json:"fully_covers"`
}

// ToolboxRecommendationResult is the complete answer for one Goal.
type ToolboxRecommendationResult struct {
	AgentInstanceID string `json:"agent_instance_id"`
	AgentName       string `json:"agent_name,omitempty"`
	// BriefVersion names the accepted brief this ranking was computed from, so
	// a stale recommendation is recognizable.
	BriefVersion int64 `json:"brief_version,omitempty"`

	Recommendations []ToolboxRecommendation `json:"recommendations,omitempty"`
	// BestMatch is the top candidate's Toolbox ID, or empty when nothing is
	// usable.
	BestMatch string `json:"best_match,omitempty"`
	// AnyFullyCovers reports whether ANY candidate fully covers the brief.
	// When false the UI must show closest-safe options and an inert proposed
	// variant rather than claiming a match (FR-101).
	AnyFullyCovers bool `json:"any_fully_covers"`
	// ProposedVariant is an unsaved, unselected draft that would cover the gap.
	// It exists only when nothing fully covers the brief (FR-101, FR-102).
	ProposedVariant *ProposedToolboxVariant `json:"proposed_variant,omitempty"`
	// Message explains an empty or partial result honestly.
	Message string `json:"message,omitempty"`
}

// ProposedToolboxVariant is a suggested Toolbox that has NOT been created.
//
// It carries no ID because it does not exist. Acting on it goes through the
// normal create → preview → Review & Use path, so the user reviews and confirms
// exactly as they would for anything else (FR-102).
type ProposedToolboxVariant struct {
	// BasedOnToolboxID names the closest existing Toolbox this extends.
	BasedOnToolboxID   string `json:"based_on_toolbox_id,omitempty"`
	BasedOnToolboxName string `json:"based_on_toolbox_name,omitempty"`
	// AddSkills / AddBindings are what would need adding to cover the brief.
	AddSkills   []ToolboxSkillRef `json:"add_skills,omitempty"`
	AddBindings []ToolboxMCPRef   `json:"add_bindings,omitempty"`
	// UnavailableRequirements are brief requirements nothing in this workspace
	// can satisfy. They would enter a variant as explicit requirements, leaving
	// it `Missing capability` until the user sets them up (FR-46).
	UnavailableRequirements []string `json:"unavailable_requirements,omitempty"`
	Explanation             string   `json:"explanation"`
}

// Ranking weights. Integers, and deliberately far apart, so the ORDER of
// concerns is readable from the numbers: covering the goal beats being ready,
// being ready beats being small, and being small beats everything else.
const (
	weightRequiredCapability = 1000
	weightOperation          = 250
	weightReady              = 400
	weightFocused            = 60
	weightFlexible           = 20
	// Penalties. Exceeding the user's autonomy ceiling is the heaviest, because
	// it is the only one that concerns permissions rather than fit.
	penaltyExceedsAutonomy = 900
	penaltyNotReady        = 350
	penaltyMissingRequired = 800
	// penaltyMissingOperation is lighter than a missing required capability —
	// an uncovered operation is a gap, not a disqualification — but it has to
	// exist. Without it a toolbox that misses an operation could outrank one
	// that has it purely by being smaller, inverting FR-96 (coverage) and
	// FR-97 (size) into the wrong order.
	penaltyMissingOperation = 200
	// penaltyPerExtraOperation is what makes the SMALLEST covering Toolbox win
	// (FR-97). It is small so it only decides between candidates that are
	// otherwise equal on coverage and readiness.
	penaltyPerExtraOperation = 4
	penaltyPerExtraSkill     = 25
)

// RecommendToolboxes ranks the workspace's saved Toolboxes against an accepted
// Goal brief.
//
// brief must be ACCEPTED; an unaccepted brief returns an empty result with an
// explanation rather than ranking against a draft nobody approved (FR-94).
func RecommendToolboxes(
	ws *Workspace,
	instance *AgentInstance,
	brief *GoalBrief,
	learned []ResolvedSkill,
	capacity int,
	expertMode bool,
	thresholds FocusThresholds,
) ToolboxRecommendationResult {
	result := ToolboxRecommendationResult{}
	if instance != nil {
		result.AgentInstanceID = instance.ID
		result.AgentName = instance.Name
	}

	if !brief.Accepted() {
		result.Message = "Review and accept the goal brief to see recommendations."
		return result
	}
	result.BriefVersion = brief.Version

	normalized := brief.Clone()
	normalized.Normalize()

	currentToolboxID := ""
	if _, _, assigned, err := ws.ResolveAssignedToolbox(result.AgentInstanceID); err == nil && assigned {
		if current, ok := ws.GetToolboxAssignment(result.AgentInstanceID); ok {
			currentToolboxID = current.ToolboxID
		}
	}

	for _, definition := range ws.GetToolboxes() {
		// An archived Toolbox cannot be newly selected, so recommending it
		// would be recommending something the user cannot act on (FR-20).
		if definition.Archived() {
			continue
		}
		candidate := scoreToolbox(ws, instance, definition, normalized, learned, capacity, expertMode, thresholds)
		candidate.IsCurrent = strings.EqualFold(definition.ID, currentToolboxID)
		result.Recommendations = append(result.Recommendations, candidate)
		if candidate.FullyCovers {
			result.AnyFullyCovers = true
		}
	}

	// Deterministic ordering: score desc, then the smaller surface, then the
	// stable ID. The last tiebreak matters — without it, two identical
	// toolboxes would swap places between requests (FR-95).
	sort.SliceStable(result.Recommendations, func(i, j int) bool {
		a, b := result.Recommendations[i], result.Recommendations[j]
		if a.Score != b.Score {
			return a.Score > b.Score
		}
		if a.SkillSpaces != b.SkillSpaces {
			return a.SkillSpaces < b.SkillSpaces
		}
		if a.Operations != b.Operations {
			return a.Operations < b.Operations
		}
		return a.ToolboxID < b.ToolboxID
	})
	for i := range result.Recommendations {
		result.Recommendations[i].Rank = i + 1
	}
	if len(result.Recommendations) > 0 {
		result.BestMatch = result.Recommendations[0].ToolboxID
	}

	if len(result.Recommendations) == 0 {
		result.Message = "This workspace has no saved toolboxes to recommend yet."
		return result
	}
	if !result.AnyFullyCovers {
		// Honesty over helpfulness: say nothing matches, show the closest
		// options, and offer an INERT variant rather than presenting a partial
		// match as ready (FR-101).
		result.Message = "No saved toolbox covers everything this goal needs. The closest options are below."
		result.ProposedVariant = proposeVariant(ws, normalized, result.Recommendations[0])
	}
	return result
}

func scoreToolbox(
	ws *Workspace,
	instance *AgentInstance,
	definition ToolboxDefinition,
	brief *GoalBrief,
	learned []ResolvedSkill,
	capacity int,
	expertMode bool,
	thresholds FocusThresholds,
) ToolboxRecommendation {
	recipe := definition.CurrentRecipe()
	// The SAME preview the user would see. Reusing it is what keeps a
	// recommendation from claiming a readiness the preview would deny
	// (success metric 6).
	preview := PreviewToolbox(ws, instance, definition, recipe, learned, capacity, expertMode, thresholds)

	candidate := ToolboxRecommendation{
		ToolboxID:      definition.ID,
		ToolboxName:    definition.Name,
		ToolboxVersion: recipe.Version,
		Readiness:      preview.Readiness,
		Focus:          preview.Focus,
		SkillSpaces:    preview.Capacity.Used,
		Operations:     preview.Focus.Inputs.ExposedOperations,
	}

	// --- Coverage ---
	provided := providedCapabilityIdentities(ws, recipe)
	providedOperations := providedSemanticOperations(ws, recipe)

	score := 0
	for _, required := range brief.RequiredCapabilities {
		if _, ok := provided[required]; ok {
			candidate.Covers = append(candidate.Covers, required)
			score += weightRequiredCapability
		} else {
			candidate.Missing = append(candidate.Missing, required)
			score -= penaltyMissingRequired
		}
	}
	for _, operation := range brief.Operations {
		if _, ok := providedOperations[operation]; ok {
			candidate.Covers = append(candidate.Covers, operation)
			score += weightOperation
		} else {
			candidate.Missing = append(candidate.Missing, operation)
			score -= penaltyMissingOperation
		}
	}

	// Capabilities the goal did not ask for. Not disqualifying — this is how a
	// smaller toolbox wins a tie, not how a bigger one loses outright (FR-97).
	wanted := make(map[string]struct{}, len(brief.RequiredCapabilities))
	for _, required := range brief.RequiredCapabilities {
		wanted[required] = struct{}{}
	}
	for identity := range provided {
		if _, asked := wanted[identity]; !asked {
			candidate.Extra = append(candidate.Extra, identity)
		}
	}
	sort.Strings(candidate.Extra)
	score -= penaltyPerExtraSkill * len(candidate.Extra)
	score -= penaltyPerExtraOperation * candidate.Operations

	// --- Readiness and Focus ---
	if preview.Readiness == ReadinessReady {
		score += weightReady
	} else {
		score -= penaltyNotReady
	}
	switch preview.Focus.State {
	case FocusFocused:
		score += weightFocused
	case FocusFlexible:
		score += weightFlexible
	}

	// --- Permission / autonomy compatibility ---
	inputs := preview.Focus.Inputs
	highest := GoalAutonomyRead
	if inputs.WriteOperations > 0 {
		highest = GoalAutonomyWrite
	}
	if inputs.ExternalOperations > 0 {
		highest = GoalAutonomyExternal
	}
	if brief.MaxAutonomy != "" && goalAutonomyRank(highest) > goalAutonomyRank(brief.MaxAutonomy) {
		candidate.ExceedsAutonomy = true
		score -= penaltyExceedsAutonomy
	}
	if inputs.WriteOperations > 0 {
		candidate.IntroducesPermissions = append(candidate.IntroducesPermissions,
			fmt.Sprintf("%d operation(s) that change things", inputs.WriteOperations))
	}
	if inputs.ExternalOperations > 0 {
		candidate.IntroducesPermissions = append(candidate.IntroducesPermissions,
			fmt.Sprintf("%d operation(s) that reach outside this workspace", inputs.ExternalOperations))
	}

	candidate.Score = score
	candidate.FullyCovers = len(candidate.Missing) == 0 && preview.Readiness == ReadinessReady
	candidate.Explanation = explainCandidate(candidate)
	sort.Strings(candidate.Covers)
	sort.Strings(candidate.Missing)
	return candidate
}

// providedCapabilityIdentities returns the normalized skill identities a
// recipe activates.
func providedCapabilityIdentities(ws *Workspace, recipe ToolboxRecipe) map[string]struct{} {
	provided := make(map[string]struct{}, len(recipe.Skills))
	for _, ref := range recipe.Skills {
		provided[ref.CapabilityID] = struct{}{}
	}
	// A binding's server and alias are capability identities too: a goal that
	// requires "calendar" is satisfied by a calendar binding, not only by a
	// skill named calendar.
	byID := make(map[string]MCPBinding)
	for _, binding := range ws.GetMCPBindings() {
		byID[strings.ToLower(strings.TrimSpace(binding.ID))] = binding
	}
	for _, ref := range recipe.MCPBindings {
		if binding, ok := byID[strings.ToLower(strings.TrimSpace(ref.BindingID))]; ok {
			provided[NormalizeToolboxCapabilityID(binding.ServerName)] = struct{}{}
			if alias := NormalizeToolboxCapabilityID(binding.Alias); alias != "" {
				provided[alias] = struct{}{}
			}
			for _, mapping := range binding.CapabilityMappings {
				provided[NormalizeToolboxCapabilityID(mapping.Capability)] = struct{}{}
			}
		}
	}
	return provided
}

// providedSemanticOperations returns the semantic operations a recipe can
// perform, using declared CapabilityMappings first and name heuristics second —
// the same precedence Focus uses, so a brief and a Focus reading agree about
// what a tool does.
func providedSemanticOperations(ws *Workspace, recipe ToolboxRecipe) map[string]struct{} {
	byID := make(map[string]MCPBinding)
	for _, binding := range ws.GetMCPBindings() {
		byID[strings.ToLower(strings.TrimSpace(binding.ID))] = binding
	}

	operations := make(map[string]struct{})
	for _, ref := range recipe.MCPBindings {
		binding, ok := byID[strings.ToLower(strings.TrimSpace(ref.BindingID))]
		if !ok {
			continue
		}
		tools := ref.AllowedTools
		if ref.InheritsBindingTools {
			tools = binding.AllowedTools
		}
		declared := make(map[string]string)
		for _, mapping := range binding.CapabilityMappings {
			for operationName, operation := range mapping.Operations {
				declared[strings.ToLower(strings.TrimSpace(operation.Tool))] = strings.ToLower(strings.TrimSpace(operationName))
			}
		}
		for _, tool := range tools {
			if semantic, ok := declared[strings.ToLower(strings.TrimSpace(tool))]; ok {
				operations[semantic] = struct{}{}
				continue
			}
			if inferred := classifyOperationByName(tool); inferred != "" {
				operations[inferred] = struct{}{}
			}
		}
	}
	// A skill can satisfy an operation too — a "summarize" skill covers a
	// summarize requirement without any MCP tool.
	for _, ref := range recipe.Skills {
		if inferred := classifyOperationByName(ref.CapabilityID); inferred != "" {
			operations[inferred] = struct{}{}
		}
		operations[ref.CapabilityID] = struct{}{}
	}
	return operations
}

// explainCandidate writes the plain-language "why this one" (FR-98).
func explainCandidate(candidate ToolboxRecommendation) string {
	parts := make([]string, 0, 4)
	if len(candidate.Covers) > 0 {
		parts = append(parts, fmt.Sprintf("covers %s", strings.Join(candidate.Covers, ", ")))
	}
	if len(candidate.Missing) > 0 {
		parts = append(parts, fmt.Sprintf("missing %s", strings.Join(candidate.Missing, ", ")))
	}
	if candidate.Readiness != ReadinessReady {
		parts = append(parts, strings.ToLower(candidate.Readiness))
	}
	if candidate.ExceedsAutonomy {
		parts = append(parts, "goes beyond the autonomy you set for this goal")
	}
	if len(candidate.Extra) > 0 {
		parts = append(parts, fmt.Sprintf("%d capability(s) this goal does not need", len(candidate.Extra)))
	}
	if len(parts) == 0 {
		return "Covers this goal with nothing extra."
	}
	return strings.ToUpper(parts[0][:1]) + parts[0][1:] + ifMore(parts)
}

func ifMore(parts []string) string {
	if len(parts) < 2 {
		return "."
	}
	return "; " + strings.Join(parts[1:], "; ") + "."
}

// proposeVariant builds an INERT draft that would close the gap between the
// closest candidate and the brief.
//
// Nothing here is saved or selected. It is a diff the user can look at and
// choose to turn into a real Toolbox through the normal create flow (FR-101,
// FR-102).
func proposeVariant(ws *Workspace, brief *GoalBrief, closest ToolboxRecommendation) *ProposedToolboxVariant {
	definition, exists := ws.GetToolbox(closest.ToolboxID)
	if !exists {
		return nil
	}

	variant := &ProposedToolboxVariant{
		BasedOnToolboxID:   definition.ID,
		BasedOnToolboxName: definition.Name,
	}

	skillBindings := make(map[string]SkillBinding)
	for _, binding := range ws.GetSkillBindings() {
		skillBindings[NormalizeToolboxCapabilityID(binding.SkillName)] = binding
	}
	mcpBindings := make(map[string]MCPBinding)
	for _, binding := range ws.GetMCPBindings() {
		mcpBindings[NormalizeToolboxCapabilityID(binding.ServerName)] = binding
		if alias := NormalizeToolboxCapabilityID(binding.Alias); alias != "" {
			mcpBindings[alias] = binding
		}
	}

	for _, missing := range closest.Missing {
		if binding, ok := skillBindings[missing]; ok {
			variant.AddSkills = append(variant.AddSkills, ToolboxSkillRef{
				CapabilityID: missing,
				DisplayName:  binding.SkillName,
				Source:       ToolboxSourceWorkspaceProvided,
				BindingID:    binding.ID,
				Required:     true,
			})
			continue
		}
		if binding, ok := mcpBindings[missing]; ok {
			tools := append([]string(nil), binding.AllowedTools...)
			if tools == nil {
				tools = []string{}
			}
			variant.AddBindings = append(variant.AddBindings, ToolboxMCPRef{
				BindingID:    binding.ID,
				AllowedTools: tools,
				Required:     true,
			})
			continue
		}
		// Nothing in this workspace can satisfy it. Saying so is the honest
		// answer; a variant that silently dropped it would look ready and run
		// short (FR-101).
		variant.UnavailableRequirements = append(variant.UnavailableRequirements, missing)
	}

	switch {
	case len(variant.AddSkills) == 0 && len(variant.AddBindings) == 0 && len(variant.UnavailableRequirements) > 0:
		variant.Explanation = fmt.Sprintf(
			"Nothing in this workspace provides %s yet. Set it up first, then this goal can use it.",
			strings.Join(variant.UnavailableRequirements, ", "))
	case len(variant.UnavailableRequirements) > 0:
		variant.Explanation = fmt.Sprintf(
			"Starting from %s, adding what this workspace already has would still leave %s to set up.",
			definition.Name, strings.Join(variant.UnavailableRequirements, ", "))
	default:
		variant.Explanation = fmt.Sprintf(
			"Starting from %s and adding what this goal needs would cover it. Nothing is saved until you confirm.",
			definition.Name)
	}
	return variant
}

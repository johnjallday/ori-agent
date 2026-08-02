package workspace

import (
	"fmt"
	"sort"
	"strings"
)

// Focus: a deterministic, explainable assessment of how clear or crowded an
// effective Toolbox is (PRD FR-63–FR-72).
//
// The design rule that shapes this file: Focus EXPLAINS, it does not score. A
// single number would be both unfalsifiable and unactionable — "your toolbox is
// 62% focused" tells a user nothing they can do. So every result carries the
// concrete reasons that produced it ("3 overlapping search operations", "14
// exposed tools"), and the thresholds that turn those inputs into a state are
// server-owned constants a test can pin and a future release can recalibrate
// from real outcome data (FR-69, FR-70).
//
// Focus is also NOT a quality guarantee. `Focused` does not mean the agent will
// do well; it means the surface it was handed is small and non-overlapping
// (FR-72). `Flexible` and `Crowded` are advisory and never block anything
// (FR-66). Only `Needs attention` corresponds to a hard problem, and it is
// always a readiness failure rather than a judgment about size (FR-65).

// Focus states (FR-64).
const (
	// FocusFocused is a small, coherent, non-overlapping surface.
	FocusFocused = "Focused"
	// FocusFlexible is a broad surface that is still coherent — more choice
	// than strictly needed, which costs tool-selection precision but is a
	// legitimate deliberate build.
	FocusFlexible = "Flexible"
	// FocusCrowded is a surface large or overlapping enough that tool selection
	// is likely to become unpredictable. Advisory only.
	FocusCrowded = "Crowded"
	// FocusNeedsAttention is a HARD readiness failure — over capacity, missing
	// a required capability or connection, or carrying unclassified operations.
	// It is not a size judgment (FR-65).
	FocusNeedsAttention = "Needs attention"
)

// FocusThresholds are the server-owned constants that turn measured inputs into
// a Focus state.
//
// They are deliberately conservative and deliberately visible: the PRD asks for
// no universal MCP-tool hard cap before Ori has measured real task outcomes, so
// these start as soft advisory boundaries that a later release recalibrates
// from completion, retry, blocked-call, latency, and unused-operation data
// (§9.6, FR-70).
type FocusThresholds struct {
	// FlexibleOperations is the exposed-operation count above which a Toolbox
	// reads as `Flexible`; CrowdedOperations is where it reads as `Crowded`.
	FlexibleOperations int `json:"flexible_operations"`
	CrowdedOperations  int `json:"crowded_operations"`
	// OverlapFlexible / OverlapCrowded count semantically overlapping
	// operations — two ways to do the same thing, which is what actually makes
	// tool selection unpredictable (FR-67).
	OverlapFlexible int `json:"overlap_flexible"`
	OverlapCrowded  int `json:"overlap_crowded"`
	// MutatingCrowded is the number of write/external operations above which a
	// Toolbox reads as `Crowded` regardless of total size: a broad read-only
	// surface is a very different risk from a broad mutating one (FR-67).
	MutatingCrowded int `json:"mutating_crowded"`
	// PromptCharsFlexible / PromptCharsCrowded bound the combined skill prompt
	// text, the closest proxy Ori has to context pressure without tokenizing.
	PromptCharsFlexible int `json:"prompt_chars_flexible"`
	PromptCharsCrowded  int `json:"prompt_chars_crowded"`
}

// DefaultFocusThresholds is V1's calibration. Every value is a starting point
// chosen to be hard to hit accidentally, not a measured optimum.
func DefaultFocusThresholds() FocusThresholds {
	return FocusThresholds{
		FlexibleOperations:  12,
		CrowdedOperations:   24,
		OverlapFlexible:     2,
		OverlapCrowded:      4,
		MutatingCrowded:     8,
		PromptCharsFlexible: 12000,
		PromptCharsCrowded:  24000,
	}
}

// FocusInputs are the measured facts a Focus result was computed from.
//
// They are returned alongside the result so a test can pin them, a UI can show
// separate readouts instead of one opaque number (FR-71), and a future
// recalibration can be checked against what was actually observed (FR-70).
type FocusInputs struct {
	// ActiveSkills is the deduplicated count of space-consuming skills;
	// SkillCapacity is the agent's stage allowance (0 = unresolvable).
	ActiveSkills  int  `json:"active_skills"`
	SkillCapacity int  `json:"skill_capacity"`
	ExpertMode    bool `json:"expert_mode,omitempty"`
	// CoreCapabilities counts always-present abilities. They consume no skill
	// space but DO contribute tools, context, and risk, so they are counted
	// here and excluded only from capacity (FR-48, FR-58).
	CoreCapabilities int `json:"core_capabilities"`

	// ExposedOperations counts concrete operations, never servers (FR-68).
	ExposedOperations int `json:"exposed_operations"`
	// UnpinnedBindings counts bindings still deferring to their own tool
	// policy, whose real operation count is unknown.
	UnpinnedBindings int `json:"unpinned_bindings"`

	// ReadOperations / WriteOperations / ExternalOperations classify the
	// exposed surface; UnclassifiedOperations are the ones that fail closed
	// under a Goal's autonomy gate until classified (FR-159).
	ReadOperations         int `json:"read_operations"`
	WriteOperations        int `json:"write_operations"`
	ExternalOperations     int `json:"external_operations"`
	UnclassifiedOperations int `json:"unclassified_operations"`

	// OverlapGroups are sets of operations that do the same semantic thing.
	OverlapGroups []FocusOverlapGroup `json:"overlap_groups,omitempty"`
	// PromptChars is the combined length of the active skills' prompt text.
	PromptChars int `json:"prompt_chars"`
	// MemoryContextChars is reserved for Phase 2 Field Notes and is always 0
	// in V1. It is present so the Focus contract does not change shape when
	// agent memory arrives (FR-67, deferred FR-123–FR-143).
	MemoryContextChars int `json:"memory_context_chars"`
}

// FocusOverlapGroup is one set of operations that do the same semantic thing —
// two search tools, three ways to write a file.
//
// Overlap matters more than raw count: a model choosing between two tools that
// both "search" has a real chance of choosing wrong, while ten tools that each
// do something distinct do not compete (FR-67).
type FocusOverlapGroup struct {
	// Operation is the semantic operation, e.g. "search" or "calendar.list".
	Operation string `json:"operation"`
	// Providers names the bindings offering it.
	Providers []string `json:"providers"`
	// Heuristic marks a group inferred from NAMES rather than from a declared
	// CapabilityMapping. Marked because a name match is a guess, and a guess
	// presented as a measurement is worse than no measurement (FR-70, §9.6).
	Heuristic bool `json:"heuristic,omitempty"`
}

// FocusResult is the assessment plus everything needed to explain it.
type FocusResult struct {
	State string `json:"state"`
	// Reasons are the human-readable facts behind the state, in a
	// deterministic order (FR-69).
	Reasons []string    `json:"reasons,omitempty"`
	Inputs  FocusInputs `json:"inputs"`
}

// Advisory reports whether this state is guidance rather than a blocker. Only
// `Needs attention` blocks (FR-66).
func (r FocusResult) Advisory() bool {
	return r.State == FocusFlexible || r.State == FocusCrowded
}

// EvaluateFocus computes a deterministic Focus result from measured inputs.
//
// It takes already-measured inputs rather than raw workspace state so the
// classification is trivially testable at every threshold boundary, and so
// preview, recommendation, and run-snapshot paths cannot measure differently
// from one another.
func EvaluateFocus(inputs FocusInputs, thresholds FocusThresholds, hardFailures []string) FocusResult {
	result := FocusResult{Inputs: inputs}

	// Hard failures win outright. `Needs attention` is about readiness, not
	// size, so it is never reached by being large (FR-65).
	if len(hardFailures) > 0 {
		result.State = FocusNeedsAttention
		result.Reasons = append(result.Reasons, hardFailures...)
		return result
	}

	var reasons []string
	state := FocusFocused
	raise := func(next string, reason string) {
		reasons = append(reasons, reason)
		if focusSeverity(next) > focusSeverity(state) {
			state = next
		}
	}

	switch {
	case inputs.ExposedOperations >= thresholds.CrowdedOperations:
		raise(FocusCrowded, fmt.Sprintf("%d exposed tools", inputs.ExposedOperations))
	case inputs.ExposedOperations >= thresholds.FlexibleOperations:
		raise(FocusFlexible, fmt.Sprintf("%d exposed tools", inputs.ExposedOperations))
	}

	overlapping := countOverlappingOperations(inputs.OverlapGroups)
	switch {
	case overlapping >= thresholds.OverlapCrowded:
		raise(FocusCrowded, describeOverlap(overlapping, inputs.OverlapGroups))
	case overlapping >= thresholds.OverlapFlexible:
		raise(FocusFlexible, describeOverlap(overlapping, inputs.OverlapGroups))
	}

	if mutating := inputs.WriteOperations + inputs.ExternalOperations; mutating >= thresholds.MutatingCrowded {
		raise(FocusCrowded, fmt.Sprintf("%d tools that change or send things", mutating))
	}

	switch {
	case inputs.PromptChars >= thresholds.PromptCharsCrowded:
		raise(FocusCrowded, fmt.Sprintf("%d characters of skill instructions", inputs.PromptChars))
	case inputs.PromptChars >= thresholds.PromptCharsFlexible:
		raise(FocusFlexible, fmt.Sprintf("%d characters of skill instructions", inputs.PromptChars))
	}

	// An unpinned binding's operation count is unknown, so the surface may be
	// larger than measured. Say so rather than reporting a number that could be
	// wrong (FR-13, FR-72).
	if inputs.UnpinnedBindings > 0 {
		raise(FocusFlexible, fmt.Sprintf("%d connection(s) still allow every operation, so the real tool count is unknown", inputs.UnpinnedBindings))
	}

	if state == FocusFocused {
		reasons = append(reasons, focusedSummary(inputs))
	}

	result.State = state
	result.Reasons = reasons
	return result
}

func focusSeverity(state string) int {
	switch state {
	case FocusNeedsAttention:
		return 3
	case FocusCrowded:
		return 2
	case FocusFlexible:
		return 1
	default:
		return 0
	}
}

func focusedSummary(inputs FocusInputs) string {
	if inputs.SkillCapacity > 0 {
		return fmt.Sprintf("%d of %d skill spaces, %d exposed tools",
			inputs.ActiveSkills, inputs.SkillCapacity, inputs.ExposedOperations)
	}
	return fmt.Sprintf("%d active skills, %d exposed tools", inputs.ActiveSkills, inputs.ExposedOperations)
}

func countOverlappingOperations(groups []FocusOverlapGroup) int {
	total := 0
	for _, group := range groups {
		if len(group.Providers) > 1 {
			total += len(group.Providers)
		}
	}
	return total
}

func describeOverlap(count int, groups []FocusOverlapGroup) string {
	names := make([]string, 0, len(groups))
	heuristic := false
	for _, group := range groups {
		if len(group.Providers) > 1 {
			names = append(names, group.Operation)
			heuristic = heuristic || group.Heuristic
		}
	}
	sort.Strings(names)
	reason := fmt.Sprintf("%d overlapping %s operations", count, strings.Join(names, "/"))
	if heuristic {
		// Naming the guess is the point: an inferred overlap must never read
		// like a measured one (§9.6).
		reason += " (matched by name, not declared)"
	}
	return reason
}

// nameHeuristicOperations maps a tool-name fragment onto the semantic operation
// it probably performs.
//
// This is the FALLBACK. A binding that declares CapabilityMappings gets its
// real semantics; only bindings with none fall through to here, and everything
// this produces is marked Heuristic so it is never mistaken for a declaration
// (§9.6, FR-70).
var nameHeuristicOperations = map[string]string{
	"search":   "search",
	"find":     "search",
	"query":    "search",
	"lookup":   "search",
	"read":     "read",
	"get":      "read",
	"fetch":    "read",
	"list":     "list",
	"write":    "write",
	"create":   "create",
	"add":      "create",
	"update":   "update",
	"edit":     "update",
	"patch":    "update",
	"delete":   "delete",
	"remove":   "delete",
	"send":     "send",
	"post":     "send",
	"schedule": "schedule",
}

// classifyOperationByName returns the semantic operation a tool name suggests,
// or "" when nothing recognizable is in it.
func classifyOperationByName(tool string) string {
	normalized := strings.ToLower(strings.TrimSpace(tool))
	if normalized == "" {
		return ""
	}
	// Longest fragment first so "search" beats "get" in "get_search_results".
	best, bestLen := "", 0
	for fragment, operation := range nameHeuristicOperations {
		if strings.Contains(normalized, fragment) && len(fragment) > bestLen {
			best, bestLen = operation, len(fragment)
		}
	}
	return best
}

// BuildFocusOverlapGroups groups the exposed operations by what they do,
// preferring declared CapabilityMappings and falling back to name heuristics.
//
// Declared mappings win because they are the workspace's own statement of what
// a tool means; a name match is a guess about someone else's naming (§9.6).
func BuildFocusOverlapGroups(bindings []MCPBinding, exposedByBinding map[string][]string) []FocusOverlapGroup {
	type providerSet struct {
		providers map[string]struct{}
		heuristic bool
	}
	byOperation := make(map[string]*providerSet)

	record := func(operation, provider string, heuristic bool) {
		if operation == "" || provider == "" {
			return
		}
		entry, exists := byOperation[operation]
		if !exists {
			entry = &providerSet{providers: make(map[string]struct{})}
			byOperation[operation] = entry
		}
		entry.providers[provider] = struct{}{}
		// A group is heuristic only if EVERY contribution to it was a guess.
		// One declared mapping makes the overlap real.
		if !heuristic {
			entry.heuristic = false
		} else if len(entry.providers) == 1 {
			entry.heuristic = true
		}
	}

	for _, binding := range bindings {
		exposed := exposedByBinding[strings.ToLower(strings.TrimSpace(binding.ID))]
		if len(exposed) == 0 {
			continue
		}
		provider := firstNonEmpty(binding.Alias, binding.ServerName, binding.ID)

		// Operations is keyed BY semantic operation name, so the inversion here
		// is tool -> "capability.operation": that is the direction Focus needs,
		// since it starts from an exposed tool and asks what it means.
		declared := make(map[string]string, len(binding.CapabilityMappings))
		for _, mapping := range binding.CapabilityMappings {
			for operationName, operation := range mapping.Operations {
				if tool := strings.ToLower(strings.TrimSpace(operation.Tool)); tool != "" {
					declared[tool] = strings.TrimSpace(mapping.Capability) + "." + strings.TrimSpace(operationName)
				}
			}
		}

		for _, tool := range exposed {
			if semantic, ok := declared[strings.ToLower(strings.TrimSpace(tool))]; ok {
				record(semantic, provider, false)
				continue
			}
			record(classifyOperationByName(tool), provider, true)
		}
	}

	groups := make([]FocusOverlapGroup, 0, len(byOperation))
	for operation, entry := range byOperation {
		providers := make([]string, 0, len(entry.providers))
		for provider := range entry.providers {
			providers = append(providers, provider)
		}
		sort.Strings(providers)
		groups = append(groups, FocusOverlapGroup{
			Operation: operation,
			Providers: providers,
			Heuristic: entry.heuristic,
		})
	}
	sort.SliceStable(groups, func(i, j int) bool { return groups[i].Operation < groups[j].Operation })
	return groups
}

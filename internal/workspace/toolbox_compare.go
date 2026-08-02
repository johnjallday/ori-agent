package workspace

import (
	"sort"
	"strings"
)

// Side-by-side comparison of two Toolbox versions (PRD FR-51, FR-52).
//
// The comparison is EXACT rather than descriptive. "This version has more
// tools" is not something a user can act on; "this version adds write_note and
// removes the citation-audit skill" is. So the diff names every entry that
// moved and, for a binding present in both, the individual operations that were
// added or dropped.
//
// Everything here is computed from two normalized recipes and nothing else,
// which is what makes it deterministic: NormalizeToolboxContent already sorted
// and deduplicated both sides, so equal selections compare equal regardless of
// the order the user happened to build them in.

// ToolboxSkillChange is one skill present in both versions but resolved
// differently — most importantly, from a different source (FR-6).
type ToolboxSkillChange struct {
	CapabilityID string          `json:"capability_id"`
	DisplayName  string          `json:"display_name,omitempty"`
	From         ToolboxSkillRef `json:"from"`
	To           ToolboxSkillRef `json:"to"`
	// Fields names what actually differs: "source", "binding", "required".
	Fields []string `json:"fields"`
}

// ToolboxMCPChange is one binding present in both versions with a different
// operation selection or requirement level.
type ToolboxMCPChange struct {
	BindingID    string        `json:"binding_id"`
	From         ToolboxMCPRef `json:"from"`
	To           ToolboxMCPRef `json:"to"`
	AddedTools   []string      `json:"added_tools,omitempty"`
	RemovedTools []string      `json:"removed_tools,omitempty"`
	Fields       []string      `json:"fields"`
}

// ToolboxDiff is the complete comparison of two versions.
type ToolboxDiff struct {
	FromVersion int64 `json:"from_version"`
	ToVersion   int64 `json:"to_version"`

	SkillsAdded   []ToolboxSkillRef    `json:"skills_added,omitempty"`
	SkillsRemoved []ToolboxSkillRef    `json:"skills_removed,omitempty"`
	SkillsChanged []ToolboxSkillChange `json:"skills_changed,omitempty"`

	BindingsAdded   []ToolboxMCPRef    `json:"bindings_added,omitempty"`
	BindingsRemoved []ToolboxMCPRef    `json:"bindings_removed,omitempty"`
	BindingsChanged []ToolboxMCPChange `json:"bindings_changed,omitempty"`

	SkillSpacesBefore int `json:"skill_spaces_before"`
	SkillSpacesAfter  int `json:"skill_spaces_after"`
	// OperationsBefore/After count concrete exposed operations, not servers
	// (FR-68). A value of -1 means at least one entry still defers to its
	// binding's own tool policy, so the real count is not knowable from the
	// recipe alone (see ToolboxMCPRef.InheritsBindingTools).
	OperationsBefore int `json:"operations_before"`
	OperationsAfter  int `json:"operations_after"`
}

// Identical reports whether the two versions grant exactly the same
// capabilities.
func (d ToolboxDiff) Identical() bool {
	return len(d.SkillsAdded) == 0 && len(d.SkillsRemoved) == 0 && len(d.SkillsChanged) == 0 &&
		len(d.BindingsAdded) == 0 && len(d.BindingsRemoved) == 0 && len(d.BindingsChanged) == 0
}

// ExpandsOperations reports whether moving from `from` to `to` exposes any
// operation the earlier version did not.
//
// This is the input to the safety gate that decides between one-click **Use
// This Toolbox** and **Review & Use** (FR-78, FR-79): a switch that only
// removes access needs no permission review, while one that adds any operation
// does. A binding that defers to its own tool policy counts as an expansion
// because its real surface is unknown — the conservative reading is the only
// safe one here.
func (d ToolboxDiff) ExpandsOperations() bool {
	for _, ref := range d.BindingsAdded {
		if ref.NeedsExplicitTools() || len(ref.AllowedTools) > 0 {
			return true
		}
	}
	for _, change := range d.BindingsChanged {
		if len(change.AddedTools) > 0 {
			return true
		}
		if change.To.NeedsExplicitTools() && !change.From.NeedsExplicitTools() {
			return true
		}
	}
	return len(d.SkillsAdded) > 0
}

// CompareToolboxRecipes returns the exact difference between two versions.
func CompareToolboxRecipes(from, to ToolboxRecipe) ToolboxDiff {
	fromSkills, fromBindings := NormalizeToolboxContent(from.Skills, from.MCPBindings)
	toSkills, toBindings := NormalizeToolboxContent(to.Skills, to.MCPBindings)

	diff := ToolboxDiff{
		FromVersion:       from.Version,
		ToVersion:         to.Version,
		SkillSpacesBefore: countToolboxSkillSpaces(fromSkills),
		SkillSpacesAfter:  countToolboxSkillSpaces(toSkills),
		OperationsBefore:  countExposedOperations(fromBindings),
		OperationsAfter:   countExposedOperations(toBindings),
	}

	// Skills are keyed by identity, not by identity+source: a skill that moved
	// from the workspace binding to the agent's own collection is a CHANGE the
	// user needs to see, not an unrelated add and remove.
	fromByIdentity := make(map[string]ToolboxSkillRef, len(fromSkills))
	for _, ref := range fromSkills {
		fromByIdentity[ref.CapabilityID] = ref
	}
	toByIdentity := make(map[string]ToolboxSkillRef, len(toSkills))
	for _, ref := range toSkills {
		toByIdentity[ref.CapabilityID] = ref
	}

	for _, ref := range toSkills {
		previous, existed := fromByIdentity[ref.CapabilityID]
		if !existed {
			diff.SkillsAdded = append(diff.SkillsAdded, ref)
			continue
		}
		if fields := skillRefDifferences(previous, ref); len(fields) > 0 {
			diff.SkillsChanged = append(diff.SkillsChanged, ToolboxSkillChange{
				CapabilityID: ref.CapabilityID,
				DisplayName:  ref.DisplayName,
				From:         previous,
				To:           ref,
				Fields:       fields,
			})
		}
	}
	for _, ref := range fromSkills {
		if _, still := toByIdentity[ref.CapabilityID]; !still {
			diff.SkillsRemoved = append(diff.SkillsRemoved, ref)
		}
	}

	fromByBinding := make(map[string]ToolboxMCPRef, len(fromBindings))
	for _, ref := range fromBindings {
		fromByBinding[strings.ToLower(ref.BindingID)] = ref
	}
	toByBinding := make(map[string]ToolboxMCPRef, len(toBindings))
	for _, ref := range toBindings {
		toByBinding[strings.ToLower(ref.BindingID)] = ref
	}

	for _, ref := range toBindings {
		previous, existed := fromByBinding[strings.ToLower(ref.BindingID)]
		if !existed {
			diff.BindingsAdded = append(diff.BindingsAdded, ref)
			continue
		}
		added, removed := diffToolNames(previous.AllowedTools, ref.AllowedTools)
		fields := mcpRefDifferences(previous, ref, added, removed)
		if len(fields) == 0 {
			continue
		}
		diff.BindingsChanged = append(diff.BindingsChanged, ToolboxMCPChange{
			BindingID:    ref.BindingID,
			From:         previous,
			To:           ref,
			AddedTools:   added,
			RemovedTools: removed,
			Fields:       fields,
		})
	}
	for _, ref := range fromBindings {
		if _, still := toByBinding[strings.ToLower(ref.BindingID)]; !still {
			diff.BindingsRemoved = append(diff.BindingsRemoved, ref)
		}
	}

	return diff
}

func skillRefDifferences(from, to ToolboxSkillRef) []string {
	var fields []string
	if from.Source != to.Source {
		fields = append(fields, "source")
	}
	if !strings.EqualFold(from.BindingID, to.BindingID) {
		fields = append(fields, "binding")
	}
	if from.Required != to.Required {
		fields = append(fields, "required")
	}
	return fields
}

func mcpRefDifferences(from, to ToolboxMCPRef, addedTools, removedTools []string) []string {
	var fields []string
	if len(addedTools) > 0 || len(removedTools) > 0 {
		fields = append(fields, "allowed_tools")
	}
	if from.NeedsExplicitTools() != to.NeedsExplicitTools() {
		fields = append(fields, "tool_policy")
	}
	if from.Required != to.Required {
		fields = append(fields, "required")
	}
	return fields
}

// diffToolNames returns the operations gained and lost, compared
// case-insensitively but reported in their saved casing.
func diffToolNames(from, to []string) (added, removed []string) {
	fromSet := make(map[string]struct{}, len(from))
	for _, name := range from {
		fromSet[strings.ToLower(strings.TrimSpace(name))] = struct{}{}
	}
	toSet := make(map[string]struct{}, len(to))
	for _, name := range to {
		toSet[strings.ToLower(strings.TrimSpace(name))] = struct{}{}
	}

	for _, name := range to {
		if _, existed := fromSet[strings.ToLower(strings.TrimSpace(name))]; !existed {
			added = append(added, name)
		}
	}
	for _, name := range from {
		if _, still := toSet[strings.ToLower(strings.TrimSpace(name))]; !still {
			removed = append(removed, name)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	return added, removed
}

// countExposedOperations counts concrete operations across a version's
// bindings, or -1 when any entry still defers to its binding's own tool policy.
func countExposedOperations(bindings []ToolboxMCPRef) int {
	total := 0
	for _, ref := range bindings {
		if ref.NeedsExplicitTools() {
			return -1
		}
		total += len(ref.AllowedTools)
	}
	return total
}

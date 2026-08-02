package types

import (
	"strings"
	"testing"
)

// Default Toolbox domain coverage (task 1.15; PRD FR-24–FR-27).

func TestSetSkills_NormalizesDeduplicatesAndVersions(t *testing.T) {
	toolbox := NewAgentDefaultToolbox()
	startingVersion := toolbox.Version

	if err := toolbox.SetSkills([]DefaultToolboxSkillRef{
		{DisplayName: "  Zeta  "},
		{CapabilityID: "ALPHA", DisplayName: "Alpha"},
		{CapabilityID: "zeta"},
		{DisplayName: "   "},
	}); err != nil {
		t.Fatalf("SetSkills() error = %v", err)
	}

	if len(toolbox.Skills) != 2 {
		t.Fatalf("expected case-insensitive deduplication and blank rejection, got %+v", toolbox.Skills)
	}
	if toolbox.Skills[0].CapabilityID != "alpha" || toolbox.Skills[1].CapabilityID != "zeta" {
		t.Fatalf("expected deterministic ordering, got %+v", toolbox.Skills)
	}
	if toolbox.Version != startingVersion+1 {
		t.Fatalf("expected the version to increase on edit, got %d", toolbox.Version)
	}
}

// FR-25: the Default Toolbox is skill-only and may not reference a workspace
// binding. The type has no field for one; this covers the remaining route in,
// an identity string shaped like a materialized workspace runtime server name.
func TestSetSkills_RejectsWorkspaceBindingReferences(t *testing.T) {
	toolbox := NewAgentDefaultToolbox()

	err := toolbox.SetSkills([]DefaultToolboxSkillRef{{CapabilityID: "ws:workspace-1:mcp:notes:mb-1"}})
	if err == nil {
		t.Fatalf("expected a workspace binding reference to be rejected")
	}
	if !strings.Contains(err.Error(), "agent-learned skills only") {
		t.Fatalf("expected the error to explain the skill-only rule, got %v", err)
	}
	if len(toolbox.Skills) != 0 {
		t.Fatalf("expected a rejected write to leave the selection untouched, got %+v", toolbox.Skills)
	}
}

func TestValidateDefaultToolbox_BoundsTheName(t *testing.T) {
	toolbox := NewAgentDefaultToolbox()
	toolbox.Name = strings.Repeat("x", MaxDefaultToolboxNameLength+1)

	if err := ValidateDefaultToolbox(toolbox); err == nil {
		t.Fatalf("expected an over-long name to be rejected")
	}

	toolbox.Name = "Research Default"
	if err := ValidateDefaultToolbox(toolbox); err != nil {
		t.Fatalf("expected a reasonable name to be accepted, got %v", err)
	}
	if err := ValidateDefaultToolbox(nil); err != nil {
		t.Fatalf("expected a nil (unmigrated) default toolbox to validate, got %v", err)
	}
}

func TestDefaultToolbox_HasAndSkillNames(t *testing.T) {
	toolbox := NewAgentDefaultToolbox()
	if err := toolbox.SetSkills([]DefaultToolboxSkillRef{{CapabilityID: "code-review", DisplayName: "Code-Review"}}); err != nil {
		t.Fatalf("SetSkills() error = %v", err)
	}

	if !toolbox.Has("  CODE-REVIEW  ") {
		t.Fatalf("expected a case- and whitespace-insensitive match")
	}
	if toolbox.Has("mac-automation") {
		t.Fatalf("expected an unselected skill not to match")
	}
	names := toolbox.SkillNames()
	if len(names) != 1 || names[0] != "Code-Review" {
		t.Fatalf("expected the exact-case display name for resolution, got %v", names)
	}

	var missing *AgentDefaultToolbox
	if missing.Has("anything") || missing.SkillNames() != nil {
		t.Fatalf("expected a nil default toolbox to answer safely")
	}
}

func TestDefaultToolbox_CloneIsDeep(t *testing.T) {
	toolbox := NewAgentDefaultToolbox()
	if err := toolbox.SetSkills([]DefaultToolboxSkillRef{{CapabilityID: "code-review"}}); err != nil {
		t.Fatalf("SetSkills() error = %v", err)
	}

	clone := toolbox.Clone()
	clone.Skills[0].CapabilityID = "mutated"

	if toolbox.Skills[0].CapabilityID != "code-review" {
		t.Fatalf("expected Clone to deep-copy the selection, got %q", toolbox.Skills[0].CapabilityID)
	}
	if (*AgentDefaultToolbox)(nil).Clone() != nil {
		t.Fatalf("expected cloning nil to stay nil")
	}
}

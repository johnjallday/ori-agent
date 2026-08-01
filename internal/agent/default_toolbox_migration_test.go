package agent

import (
	"testing"

	"github.com/johnjallday/ori-agent/internal/types"
)

// Default Toolbox migration coverage (task 1.17; PRD FR-24–FR-28).

func TestMigrateDefaultToolbox_SeedsFromCurrentlyEnabledSkills(t *testing.T) {
	ag := &Agent{}

	if !MigrateDefaultToolbox(ag, []string{"Code-Review", "  mac-automation  ", "", "code-review"}) {
		t.Fatalf("expected an unmigrated agent to be modified")
	}
	if ag.DefaultToolbox == nil {
		t.Fatalf("expected a default toolbox to be created")
	}
	if len(ag.DefaultToolbox.Skills) != 2 {
		t.Fatalf("expected case-insensitive deduplication and blank rejection, got %+v", ag.DefaultToolbox.Skills)
	}
	if !ag.DefaultToolbox.Has("CODE-REVIEW") || !ag.DefaultToolbox.Has("mac-automation") {
		t.Fatalf("expected both enabled skills to be selected, got %+v", ag.DefaultToolbox.Skills)
	}
	if ag.DefaultToolbox.Skills[0].DisplayName != "Code-Review" {
		t.Fatalf("expected the exact-case skill name to be preserved, got %q", ag.DefaultToolbox.Skills[0].DisplayName)
	}
}

// Re-running migration must not re-add skills the user removed (FR-34).
func TestMigrateDefaultToolbox_IsIdempotentAndRespectsAnEmptiedSelection(t *testing.T) {
	ag := &Agent{}
	MigrateDefaultToolbox(ag, []string{"code-review"})

	if MigrateDefaultToolbox(ag, []string{"code-review", "mac-automation"}) {
		t.Fatalf("expected a second migration run to be a no-op")
	}
	if len(ag.DefaultToolbox.Skills) != 1 {
		t.Fatalf("expected the existing selection to be preserved, got %+v", ag.DefaultToolbox.Skills)
	}

	emptied := &Agent{DefaultToolbox: types.NewAgentDefaultToolbox()}
	if MigrateDefaultToolbox(emptied, []string{"code-review"}) {
		t.Fatalf("expected a deliberately emptied default toolbox to stay empty")
	}
	if len(emptied.DefaultToolbox.Skills) != 0 {
		t.Fatalf("expected migration not to refill an emptied selection, got %+v", emptied.DefaultToolbox.Skills)
	}
}

func TestMigrateDefaultToolbox_AgentWithNoSkillsGetsAnExplicitEmptySelection(t *testing.T) {
	ag := &Agent{}

	if !MigrateDefaultToolbox(ag, nil) {
		t.Fatalf("expected an agent with no skills to still be migrated")
	}
	if ag.DefaultToolbox == nil || len(ag.DefaultToolbox.Skills) != 0 {
		t.Fatalf("expected an explicit empty default toolbox, got %+v", ag.DefaultToolbox)
	}
}

func TestInitializeDefaultToolbox_IsIdempotent(t *testing.T) {
	ag := &Agent{}
	ag.InitializeDefaultToolbox()
	first := ag.DefaultToolbox

	ag.InitializeDefaultToolbox()
	if ag.DefaultToolbox != first {
		t.Fatalf("expected InitializeDefaultToolbox to leave an existing toolbox alone")
	}
}

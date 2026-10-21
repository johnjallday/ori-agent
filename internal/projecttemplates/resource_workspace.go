package projecttemplates

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// validateResourceWorkspaceTemplate keeps a resource workspace's blueprint a
// resource workspace: it has no project file, no song inputs or starter work,
// no project setup or runtime, and it can only ever be created inside an
// existing Home. Declaring any of those next to a resource team refuses the
// team, which in turn refuses a plugin blueprint whole.
func validateResourceWorkspaceTemplate(m manifest, requirement *GroupRequirement) error {
	declared := func(raw json.RawMessage) bool {
		trimmed := bytes.TrimSpace(raw)
		return len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null"))
	}
	forbidden := []struct {
		name    string
		present bool
	}{
		{"project_entry", declared(m.ProjectEntry)},
		{"project_connection", declared(m.ProjectConnection)},
		{"inputs", declared(m.Inputs)},
		{"starter_tasks", len(m.StarterTasks) > 0},
		{"setup_quest", strings.TrimSpace(m.SetupQuestID) != ""},
		{"user_setup_quest", declared(m.UserSetupQuest)},
		{"runtime_requirements", declared(m.RuntimeRequirements)},
		{"setup_wizard", declared(m.SetupWizard)},
		{"standalone_composition", declared(m.StandaloneComposition)},
		{"intake_requirements", declared(m.IntakeRequirements)},
		{"template_variant", declared(m.TemplateVariant)},
	}
	for _, component := range forbidden {
		if component.present {
			return fmt.Errorf("%w: a resource workspace cannot declare %s", ErrInvalidAssistantProgram, component.name)
		}
	}
	if requirement == nil || requirement.Policy != GroupPolicyRequired || requirement.MissingHome != MissingHomeExistingOnly {
		return fmt.Errorf("%w: a resource workspace requires an existing Home (policy required, missing_home existing_only)", ErrInvalidAssistantProgram)
	}
	return nil
}

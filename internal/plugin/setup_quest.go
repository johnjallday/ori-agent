package plugin

import (
	"fmt"

	"github.com/johnjallday/ori-agent/internal/specialist"
)

// SetupQuest declares a bounded pre-workspace journey. Its fixed step kinds
// select host primitives, never plugin code. The owning plugin and referenced
// blueprint are checked again by the journey resolver before any action.
type SetupQuest = specialist.SetupJourney

func validateSetupQuests(c *SurfaceContribution) error {
	if len(c.SetupQuests) > 8 {
		return fmt.Errorf("plugin setup quest count exceeds its limit")
	}
	if len(c.SetupQuests) == 0 {
		return nil
	}
	feature := false
	for _, name := range c.RequiresHostFeatures {
		feature = feature || name == HostFeatureSetupQuestsV1
	}
	if !feature {
		return fmt.Errorf("plugin setup quests require %s", HostFeatureSetupQuestsV1)
	}
	seen := make(map[string]bool, len(c.SetupQuests))
	for index, quest := range c.SetupQuests {
		normalized, err := specialist.NormalizeSetupJourney(quest)
		if err != nil || normalized.WorkspaceLaunch == nil {
			return fmt.Errorf("plugin setup quest %d is invalid", index)
		}
		if seen[normalized.ID] {
			return fmt.Errorf("plugin setup quest id is duplicated")
		}
		seen[normalized.ID] = true
		found := false
		for _, blueprint := range c.Blueprints {
			found = found || blueprint.ID == normalized.ExpectedBlueprintID
		}
		if !found {
			return fmt.Errorf("plugin setup quest references an unowned blueprint")
		}
		c.SetupQuests[index] = *normalized
	}
	return nil
}

func validateQuestBlueprints(c *SurfaceContribution, blueprints []ResolvedBlueprint) error {
	quests := make(map[string]SetupQuest, len(c.SetupQuests))
	for _, quest := range c.SetupQuests {
		quests[quest.ID] = quest
	}
	used := make(map[string]bool, len(quests))
	for _, blueprint := range blueprints {
		id := blueprint.Template.SetupQuestID
		if id == "" {
			continue
		}
		quest, ok := quests[id]
		program := blueprint.Template.AssistantProgram
		if !ok || quest.ExpectedBlueprintID != blueprint.ID || program == nil ||
			program.ID != quest.ExpectedAssistantProgramID || blueprint.Template.ProjectConnection == nil {
			return fmt.Errorf("plugin blueprint setup quest reference is unavailable")
		}
		used[id] = true
	}
	for id := range quests {
		if !used[id] {
			return fmt.Errorf("plugin setup quest is not referenced by its target blueprint")
		}
	}
	return nil
}

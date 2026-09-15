package setupjourney

import (
	"context"
	"sort"

	"github.com/johnjallday/ori-agent/internal/specialist"
)

type hostQuestCatalog struct {
	definitions map[string]*specialist.SetupJourney
}

// NewHostQuestCatalog serves quests compiled into Ori for built-in templates.
// It never consults an installed plugin, the reviewed-integration registry, the
// filesystem, or the progress store, so listing and lookup create nothing.
// Declarations that are not a valid host account-link quest are skipped.
func NewHostQuestCatalog(declarations []specialist.SetupJourney) QuestCatalog {
	catalog := &hostQuestCatalog{definitions: make(map[string]*specialist.SetupJourney, len(declarations))}
	for index := range declarations {
		normalized, err := specialist.NormalizeSetupJourney(declarations[index])
		if err != nil || normalized.OwnerPluginID != "" || normalized.Shape() != specialist.SetupJourneyShapeAccountLink {
			continue
		}
		if _, duplicate := catalog.definitions[normalized.ID]; duplicate {
			continue
		}
		catalog.definitions[normalized.ID] = normalized
	}
	return catalog
}

func (c *hostQuestCatalog) List(context.Context) ([]QuestSummary, error) {
	if c == nil {
		return nil, failure(ReasonJourneyUnavailable, 0)
	}
	result := make([]QuestSummary, 0, len(c.definitions))
	for id, declaration := range c.definitions {
		result = append(result, QuestSummary{
			QuestKey: QuestKey{Source: QuestSourceHost, ID: id},
			Title:    declaration.Title, Description: declaration.Description,
			TemplateID: declaration.ExpectedBlueprintID, Ownership: string(QuestSourceHost),
		})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

func (c *hostQuestCatalog) Lookup(_ context.Context, key QuestKey) (QuestDefinition, error) {
	key = normalizeQuestKey(key)
	if c == nil || key.Source != QuestSourceHost || !validQuestKey(key) {
		return QuestDefinition{}, failure(ReasonInputInvalid, 0)
	}
	declaration, ok := c.definitions[key.ID]
	if !ok {
		return QuestDefinition{}, failure(ReasonJourneyUnavailable, 0)
	}
	copy := *declaration
	copy.Steps = append([]specialist.SetupJourneyStep(nil), declaration.Steps...)
	return QuestDefinition{Key: key, Declaration: &copy, Ownership: string(QuestSourceHost)}, nil
}

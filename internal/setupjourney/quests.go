package setupjourney

import (
	"context"
	"errors"

	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
	"github.com/johnjallday/ori-agent/internal/specialist"
)

// QuestKey identifies display/setup data, not executable behavior or a source.
type QuestKey struct {
	PluginID string `json:"plugin_id"`
	ID       string `json:"id"`
}

type QuestSummary struct {
	QuestKey
	Title       string `json:"title"`
	Description string `json:"description"`
	TemplateID  string `json:"template_id"`
	Ownership   string `json:"ownership"`
}

type QuestDefinition struct {
	Declaration *specialist.SetupJourney
	LegacySlug  string
	Ownership   string
}

type QuestCatalog interface {
	List(context.Context) ([]QuestSummary, error)
	Lookup(context.Context, QuestKey) (QuestDefinition, error)
}

type installedQuestCatalog struct {
	manager interface {
		List() ([]plugin.InstalledPlugin, error)
	}
}

func NewInstalledQuestCatalog(manager interface {
	List() ([]plugin.InstalledPlugin, error)
}) QuestCatalog {
	return &installedQuestCatalog{manager: manager}
}

// List projects only bounded inert metadata. It does not create progress rows,
// inspect/download a source, start a service, or invoke a plugin operation.
func (c *installedQuestCatalog) List(ctx context.Context) ([]QuestSummary, error) {
	definitions, err := c.definitions(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]QuestSummary, 0, len(definitions))
	for _, item := range definitions {
		d := item.Declaration
		result = append(result, QuestSummary{
			QuestKey: QuestKey{PluginID: d.OwnerPluginID, ID: d.ID}, Title: d.Title, Description: d.Description,
			TemplateID: "plugin:" + d.OwnerPluginID + ":" + d.ExpectedBlueprintID, Ownership: item.Ownership,
		})
	}
	return result, nil
}

func (c *installedQuestCatalog) Lookup(ctx context.Context, key QuestKey) (QuestDefinition, error) {
	if !validQuestKey(key) {
		return QuestDefinition{}, failure(ReasonInputInvalid, 0)
	}
	definitions, err := c.definitions(ctx)
	if err != nil {
		return QuestDefinition{}, err
	}
	for _, item := range definitions {
		if item.Declaration.OwnerPluginID == key.PluginID && item.Declaration.ID == key.ID {
			return item, nil
		}
	}
	return QuestDefinition{}, failure(ReasonJourneyUnavailable, 0)
}

func (c *installedQuestCatalog) definitions(_ context.Context) ([]QuestDefinition, error) {
	if c == nil || c.manager == nil {
		return nil, failure(ReasonJourneyUnavailable, 0)
	}
	installed, err := c.manager.List()
	if err != nil {
		return nil, failure(ReasonOwnerUnavailable, 0)
	}
	result := make([]QuestDefinition, 0)
	// V1 keeps the host-reviewed installation boundary. A plugin cannot claim
	// another integration's key, blueprint, program, or legacy progress.
	for _, entry := range reviewedintegration.All() {
		var current *plugin.InstalledPlugin
		for i := range installed {
			if installed[i].Name == entry.PluginID {
				if current != nil {
					return nil, failure(ReasonJourneyUnavailable, 0)
				}
				current = &installed[i]
			}
		}
		var legacy *specialist.Entry
		for _, item := range specialist.All() {
			if item.SetupJourney != nil && item.SetupJourney.IntegrationKey == entry.Key {
				copy := item
				legacy = &copy
			}
		}
		declaresQuests := false
		if current != nil && current.WorkspaceSurfaces != nil {
			for _, feature := range current.WorkspaceSurfaces.RequiresHostFeatures {
				declaresQuests = declaresQuests || feature == plugin.HostFeatureSetupQuestsV1
			}
			if declaresQuests && len(current.WorkspaceSurfaces.SetupQuests) == 0 {
				return nil, failure(ReasonJourneyUnavailable, 0)
			}
		}
		if current != nil && current.WorkspaceSurfaces != nil && len(current.WorkspaceSurfaces.SetupQuests) > 0 {
			if !declaresQuests {
				return nil, failure(ReasonDeclarationInvalid, 0)
			}
			if len(current.WorkspaceSurfaces.SetupQuests) > 8 {
				return nil, failure(ReasonDeclarationInvalid, 0)
			}
			seen := make(map[string]bool)
			for _, authored := range current.WorkspaceSurfaces.SetupQuests {
				d, err := specialist.NormalizeSetupJourney(authored)
				if err != nil || d.WorkspaceLaunch == nil || seen[d.ID] || d.IntegrationKey != entry.Key ||
					d.ExpectedBlueprintID != entry.ExpectedBlueprintID || d.ExpectedAssistantProgramID != entry.ExpectedProgramID {
					return nil, failure(ReasonDeclarationInvalid, 0)
				}
				seen[d.ID] = true
				found := false
				for _, b := range current.ResolvedBlueprints {
					if b.ID == d.ExpectedBlueprintID && b.Template.SetupQuestID == d.ID &&
						b.Template.AssistantProgram != nil && b.Template.AssistantProgram.ID == d.ExpectedAssistantProgramID {
						found = true
					}
				}
				if !found {
					return nil, failure(ReasonDeclarationInvalid, 0)
				}
				d.OwnerPluginID = entry.PluginID
				definition := QuestDefinition{Declaration: d, Ownership: "plugin"}
				if legacy != nil && legacy.SetupJourney.ID == d.ID {
					definition.LegacySlug = legacy.Slug
				}
				result = append(result, definition)
			}
			continue
		}
		// Published older integrations and pre-install discovery need a trusted
		// bootstrap. This compatibility copy is never claimed as plugin-owned;
		// once a plugin declares quests, missing/invalid IDs never fall back.
		if legacy != nil {
			d, err := specialist.NormalizeSetupJourney(*legacy.SetupJourney)
			if err != nil {
				return nil, failure(ReasonDeclarationInvalid, 0)
			}
			d.OwnerPluginID = entry.PluginID
			result = append(result, QuestDefinition{Declaration: d, LegacySlug: legacy.Slug, Ownership: "host_compatibility"})
		}
	}
	return result, nil
}

func validQuestKey(key QuestKey) bool {
	return validateStableID(key.PluginID) && validateStableID(key.ID)
}
func questRelationshipID(key QuestKey) string {
	return "quest:" + Digest([]byte(key.PluginID+":"+key.ID))
}

type declarationIdentity struct {
	UserID, AssistantID, SpecialistSlug string
}

// SetQuestCatalog is startup-only wiring. Request scope is immutable: ForQuest
// returns a service copy rather than changing the shared service's selection.
func (s *Service) SetQuestCatalog(catalog QuestCatalog) { s.quests = catalog }

func (s *Service) ListQuests(ctx context.Context) ([]QuestSummary, error) {
	if s == nil || s.quests == nil {
		return nil, failure(ReasonJourneyUnavailable, 0)
	}
	return s.quests.List(ctx)
}

func (s *Service) ForQuest(ctx context.Context, userID, pluginID, questID string) (*Service, error) {
	if s == nil || s.quests == nil {
		return nil, failure(ReasonJourneyUnavailable, 0)
	}
	key := QuestKey{PluginID: pluginID, ID: questID}
	if !validQuestKey(key) || !validateCanonicalRef(userID, false) {
		return nil, failure(ReasonInputInvalid, 0)
	}
	copy := *s
	copy.quest = &key
	if _, _, err := copy.questDeclaration(ctx, userID, key); err != nil {
		return nil, err
	}
	return &copy, nil
}

func (s *Service) questDeclaration(ctx context.Context, userID string, key QuestKey) (*declarationIdentity, *specialist.SetupJourney, error) {
	definition, err := s.quests.Lookup(ctx, key)
	if err != nil {
		return nil, nil, err
	}
	if definition.Declaration == nil || definition.Declaration.OwnerPluginID != key.PluginID {
		return nil, nil, failure(ReasonDeclarationInvalid, 0)
	}
	d, err := specialist.NormalizeSetupJourney(*definition.Declaration)
	if err != nil || d.ID != key.ID {
		return nil, nil, failure(ReasonDeclarationInvalid, 0)
	}
	d.OwnerPluginID = key.PluginID
	identity := &declarationIdentity{UserID: userID, AssistantID: questRelationshipID(key), SpecialistSlug: "plugin_quest"}
	root, err := s.store.FindQuestRoot(ctx, userID, key, definition.LegacySlug)
	if err == nil {
		identity.AssistantID, identity.SpecialistSlug = root.RelationshipID, root.SpecialistSlug
	} else if !errors.Is(err, ErrNotFound) {
		return nil, nil, failure(ReasonJourneyUnavailable, 0)
	}
	return identity, d, nil
}

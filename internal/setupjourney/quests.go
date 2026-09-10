package setupjourney

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
	"github.com/johnjallday/ori-agent/internal/specialist"
)

type QuestSource string

const (
	QuestSourcePlugin       QuestSource = "plugin"
	QuestSourceUserTemplate QuestSource = "user_template"
)

// QuestKey is source-aware. Plugin identity and user-template attachment
// identity are mutually exclusive, so a local quest never fabricates a plugin
// owner in progress, routes, events, or projections.
type QuestKey struct {
	Source       QuestSource `json:"source"`
	PluginID     string      `json:"plugin_id,omitempty"`
	TemplateID   string      `json:"template_id,omitempty"`
	AttachmentID string      `json:"attachment_id,omitempty"`
	ID           string      `json:"id"`
}

type QuestSummary struct {
	QuestKey
	Title       string `json:"title"`
	Description string `json:"description"`
	TemplateID  string `json:"template_id"`
	Ownership   string `json:"ownership"`
}

type QuestDefinition struct {
	Key              QuestKey
	Declaration      *specialist.SetupJourney
	LegacySlug       string
	Ownership        string
	DefinitionDigest string
	ExecutionDigest  string
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
			QuestKey: QuestKey{Source: QuestSourcePlugin, PluginID: d.OwnerPluginID, ID: d.ID}, Title: d.Title, Description: d.Description,
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
			item.Key = key
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

// UserTemplateQuestLibrary keeps filesystem/config knowledge outside the
// journey domain while exposing already-normalized local templates.
type UserTemplateQuestLibrary interface {
	ListUserSetupQuestTemplates(context.Context) ([]projecttemplates.Template, error)
	FindUserSetupQuestTemplate(context.Context, string) (projecttemplates.Template, error)
	WithUserSetupQuestMutationLock(context.Context, func() error) error
}

type userTemplateQuestMutationLocker interface {
	WithUserSetupQuestMutationLock(context.Context, func() error) error
}

type userTemplateQuestCatalog struct {
	library UserTemplateQuestLibrary
}

func NewUserTemplateQuestCatalog(library UserTemplateQuestLibrary) QuestCatalog {
	return &userTemplateQuestCatalog{library: library}
}

func (c *userTemplateQuestCatalog) List(ctx context.Context) ([]QuestSummary, error) {
	if c == nil || c.library == nil {
		return nil, failure(ReasonJourneyUnavailable, 0)
	}
	templates, err := c.library.ListUserSetupQuestTemplates(ctx)
	if err != nil {
		return nil, failure(ReasonOwnerUnavailable, 0)
	}
	attachmentCounts := make(map[string]int)
	questCounts := make(map[string]int)
	for _, template := range templates {
		if validUserTemplateQuest(template) {
			attachmentCounts[template.UserSetupQuest.AttachmentID]++
			questCounts[template.UserSetupQuest.Declaration.ID]++
		}
	}
	result := make([]QuestSummary, 0, len(templates))
	for _, template := range templates {
		if !validUserTemplateQuest(template) || attachmentCounts[template.UserSetupQuest.AttachmentID] != 1 ||
			questCounts[template.UserSetupQuest.Declaration.ID] != 1 {
			continue
		}
		declaration := template.UserSetupQuest.Declaration
		result = append(result, QuestSummary{
			QuestKey: QuestKey{Source: QuestSourceUserTemplate, TemplateID: template.ID, AttachmentID: template.UserSetupQuest.AttachmentID, ID: declaration.ID},
			Title:    declaration.Title, Description: declaration.Description, TemplateID: template.ID, Ownership: string(QuestSourceUserTemplate),
		})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].TemplateID == result[j].TemplateID {
			return result[i].ID < result[j].ID
		}
		return result[i].TemplateID < result[j].TemplateID
	})
	return result, nil
}

func (c *userTemplateQuestCatalog) Lookup(ctx context.Context, key QuestKey) (QuestDefinition, error) {
	key = normalizeQuestKey(key)
	if c == nil || c.library == nil || key.Source != QuestSourceUserTemplate || !validQuestKey(key) {
		return QuestDefinition{}, failure(ReasonInputInvalid, 0)
	}
	template, err := c.library.FindUserSetupQuestTemplate(ctx, key.TemplateID)
	if err != nil || !validUserTemplateQuest(template) || template.UserSetupQuest.AttachmentID != key.AttachmentID ||
		template.UserSetupQuest.Declaration.ID != key.ID {
		return QuestDefinition{}, failure(ReasonJourneyUnavailable, 0)
	}
	// Fail closed when a copied/imported attachment collides anywhere in the
	// current library. A writer must remap identities before this catalog sees it.
	all, err := c.List(ctx)
	if err != nil {
		return QuestDefinition{}, err
	}
	found := 0
	for _, summary := range all {
		if summary.Source == key.Source && summary.TemplateID == key.TemplateID && summary.AttachmentID == key.AttachmentID && summary.ID == key.ID {
			found++
		}
	}
	if found != 1 {
		return QuestDefinition{}, failure(ReasonDeclarationInvalid, 0)
	}
	return QuestDefinition{
		Key: key, Declaration: template.UserSetupQuest.Declaration, Ownership: string(QuestSourceUserTemplate),
		DefinitionDigest: projecttemplates.UserSetupQuestDefinitionDigest(template.UserSetupQuest),
		ExecutionDigest:  projecttemplates.UserSetupQuestExecutionDigest(template),
	}, nil
}

func validUserTemplateQuest(template projecttemplates.Template) bool {
	if template.Builtin || template.PluginOwner != nil || template.UserSetupQuest == nil ||
		template.UserSetupQuest.Declaration == nil || template.UserSetupQuestError != "" || !template.UserSetupQuestEligibility.Eligible {
		return false
	}
	declaration := template.UserSetupQuest.Declaration
	if declaration.OwnerPluginID != "" || declaration.ExpectedBlueprintID != template.ID || template.AssistantProgram == nil ||
		declaration.ExpectedAssistantProgramID != template.AssistantProgram.ID {
		return false
	}
	_, reviewed := reviewedintegration.Get(declaration.IntegrationKey)
	return reviewed
}

type combinedQuestCatalog struct {
	catalogs []QuestCatalog
}

func CombineQuestCatalogs(catalogs ...QuestCatalog) QuestCatalog {
	filtered := make([]QuestCatalog, 0, len(catalogs))
	for _, catalog := range catalogs {
		if catalog != nil {
			filtered = append(filtered, catalog)
		}
	}
	return &combinedQuestCatalog{catalogs: filtered}
}

func (c *combinedQuestCatalog) List(ctx context.Context) ([]QuestSummary, error) {
	result := make([]QuestSummary, 0)
	for _, catalog := range c.catalogs {
		items, err := catalog.List(ctx)
		if err != nil {
			return nil, err
		}
		result = append(result, items...)
	}
	sort.Slice(result, func(i, j int) bool {
		left, right := normalizeQuestKey(result[i].QuestKey), normalizeQuestKey(result[j].QuestKey)
		return string(left.Source)+left.PluginID+left.TemplateID+left.AttachmentID+left.ID <
			string(right.Source)+right.PluginID+right.TemplateID+right.AttachmentID+right.ID
	})
	return result, nil
}

func (c *combinedQuestCatalog) WithUserSetupQuestMutationLock(ctx context.Context, operation func() error) error {
	for _, catalog := range c.catalogs {
		if locker, ok := catalog.(userTemplateQuestMutationLocker); ok {
			return locker.WithUserSetupQuestMutationLock(ctx, operation)
		}
	}
	return errors.New("user template mutation lock is unavailable")
}

func (c *userTemplateQuestCatalog) WithUserSetupQuestMutationLock(ctx context.Context, operation func() error) error {
	return c.library.WithUserSetupQuestMutationLock(ctx, operation)
}

func (c *combinedQuestCatalog) Lookup(ctx context.Context, key QuestKey) (QuestDefinition, error) {
	key = normalizeQuestKey(key)
	for _, catalog := range c.catalogs {
		definition, err := catalog.Lookup(ctx, key)
		if err == nil {
			return definition, nil
		}
	}
	return QuestDefinition{}, failure(ReasonJourneyUnavailable, 0)
}

func validQuestKey(key QuestKey) bool {
	key = normalizeQuestKey(key)
	if !validateStableID(key.ID) {
		return false
	}
	switch key.Source {
	case QuestSourcePlugin:
		return validateStableID(key.PluginID) && key.TemplateID == "" && key.AttachmentID == ""
	case QuestSourceUserTemplate:
		return validateStableID(key.TemplateID) && userTemplateAttachmentPattern.MatchString(key.AttachmentID) && key.PluginID == ""
	default:
		return false
	}
}

func normalizeQuestKey(key QuestKey) QuestKey {
	key.Source = QuestSource(strings.ToLower(strings.TrimSpace(string(key.Source))))
	key.PluginID = strings.ToLower(strings.TrimSpace(key.PluginID))
	key.TemplateID = strings.ToLower(strings.TrimSpace(key.TemplateID))
	key.AttachmentID = strings.ToLower(strings.TrimSpace(key.AttachmentID))
	key.ID = strings.ToLower(strings.TrimSpace(key.ID))
	if key.Source == "" && key.PluginID != "" {
		key.Source = QuestSourcePlugin
	}
	return key
}

func questRelationshipID(key QuestKey) string {
	key = normalizeQuestKey(key)
	identity := string(key.Source) + ":" + key.PluginID + ":" + key.TemplateID + ":" + key.AttachmentID + ":" + key.ID
	return "quest:" + Digest([]byte(identity))
}

type declarationIdentity struct {
	UserID, AssistantID, SpecialistSlug string
	QuestKey                            QuestKey
	Binding                             *UserTemplateBinding
}

// SetQuestCatalog is startup-only wiring. Request scope is immutable: ForQuest
// returns a service copy rather than changing the shared service's selection.
func (s *Service) SetQuestCatalog(catalog QuestCatalog) {
	s.quests = catalog
	s.userTemplateLocker, _ = catalog.(userTemplateQuestMutationLocker)
}

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
	key := QuestKey{Source: QuestSourcePlugin, PluginID: pluginID, ID: questID}
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

func (s *Service) ForUserTemplateQuest(ctx context.Context, userID, templateID, attachmentID string) (*Service, error) {
	if s == nil || s.quests == nil {
		return nil, failure(ReasonJourneyUnavailable, 0)
	}
	key := QuestKey{Source: QuestSourceUserTemplate, TemplateID: templateID, AttachmentID: attachmentID}
	// The route intentionally omits a separately caller-selectable quest ID.
	// Resolve it from the catalog summary bound to this exact attachment.
	items, err := s.quests.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		if item.Source == QuestSourceUserTemplate && item.TemplateID == strings.ToLower(strings.TrimSpace(templateID)) &&
			item.AttachmentID == strings.ToLower(strings.TrimSpace(attachmentID)) {
			key.ID = item.ID
			break
		}
	}
	key = normalizeQuestKey(key)
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
	key = normalizeQuestKey(key)
	definition, err := s.quests.Lookup(ctx, key)
	if err != nil {
		return nil, nil, err
	}
	if definition.Declaration == nil {
		return nil, nil, failure(ReasonDeclarationInvalid, 0)
	}
	d, err := specialist.NormalizeSetupJourney(*definition.Declaration)
	if err != nil || d.ID != key.ID {
		return nil, nil, failure(ReasonDeclarationInvalid, 0)
	}
	identity := &declarationIdentity{UserID: userID, AssistantID: questRelationshipID(key), QuestKey: key}
	switch key.Source {
	case QuestSourcePlugin:
		if d.OwnerPluginID != key.PluginID {
			return nil, nil, failure(ReasonDeclarationInvalid, 0)
		}
		d.OwnerPluginID = key.PluginID
		identity.SpecialistSlug = "plugin_quest"
	case QuestSourceUserTemplate:
		if d.OwnerPluginID != "" || d.ExpectedBlueprintID != key.TemplateID ||
			!validateDigest(definition.DefinitionDigest, false) || !validateDigest(definition.ExecutionDigest, false) {
			return nil, nil, failure(ReasonDeclarationInvalid, 0)
		}
		identity.SpecialistSlug = "user_template_quest"
		identity.Binding = &UserTemplateBinding{
			UserID: userID, TemplateID: key.TemplateID, AttachmentID: key.AttachmentID, QuestID: key.ID,
			DefinitionDigest: definition.DefinitionDigest, ExecutionDigest: definition.ExecutionDigest,
		}
	default:
		return nil, nil, failure(ReasonDeclarationInvalid, 0)
	}
	root, err := s.store.FindQuestRoot(ctx, userID, key, definition.LegacySlug)
	if err == nil {
		identity.AssistantID, identity.SpecialistSlug = root.RelationshipID, root.SpecialistSlug
	} else if !errors.Is(err, ErrNotFound) {
		return nil, nil, failure(ReasonJourneyUnavailable, 0)
	}
	return identity, d, nil
}

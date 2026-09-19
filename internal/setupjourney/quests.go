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
	// QuestSourceHost is a quest compiled into Ori for a built-in template. It
	// has no plugin owner or template attachment, and its declaration is inert
	// embedded data normalized at startup.
	QuestSourceHost QuestSource = "host"
)

const (
	pluginQuestSlug       = "plugin_quest"
	userTemplateQuestSlug = "user_template_quest"
	hostQuestSlug         = "host_quest"
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
	// IntegrationKey, DisplayName and PublisherLabel are set only on
	// host-generated install quests, so a surface can list them as available
	// integrations. They are inert reviewed registry copy.
	IntegrationKey string `json:"integration_key,omitempty"`
	DisplayName    string `json:"display_name,omitempty"`
	PublisherLabel string `json:"publisher_label,omitempty"`
}

type QuestDefinition struct {
	Key              QuestKey
	Declaration      *specialist.SetupJourney
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
	// Only an installed reviewed integration's own valid setup_quests_v2
	// declarations are listed. Before install there is nothing here: the
	// generated install quest is the only guidance. A plugin cannot claim another
	// integration's key, blueprint or program.
	for _, entry := range reviewedintegration.All() {
		result = append(result, installedIntegrationQuests(entry, installed)...)
	}
	return result, nil
}

// installedIntegrationQuests returns one integration's valid plugin quests, or
// none. Any invalid declaration, an ambiguous install, or a manifest that does
// not require setup_quests_v2 lists nothing for that integration, so one
// outdated plugin fails closed without hiding every other quest.
func installedIntegrationQuests(entry reviewedintegration.Entry, installed []plugin.InstalledPlugin) []QuestDefinition {
	var current *plugin.InstalledPlugin
	for i := range installed {
		if installed[i].Name == entry.PluginID {
			if current != nil {
				return nil
			}
			current = &installed[i]
		}
	}
	if current == nil || current.WorkspaceSurfaces == nil {
		return nil
	}
	surfaces := current.WorkspaceSurfaces
	requiresV2 := false
	for _, feature := range surfaces.RequiresHostFeatures {
		requiresV2 = requiresV2 || feature == plugin.HostFeatureSetupQuestsV2
	}
	if !requiresV2 || len(surfaces.SetupQuests) == 0 || len(surfaces.SetupQuests) > 8 {
		return nil
	}
	result := make([]QuestDefinition, 0, len(surfaces.SetupQuests))
	seen := make(map[string]bool, len(surfaces.SetupQuests))
	for _, authored := range surfaces.SetupQuests {
		d, err := specialist.NormalizeSetupJourney(authored)
		if err != nil || d.Shape() != specialist.SetupJourneyShapeProjectSetup || d.WorkspaceLaunch == nil || seen[d.ID] ||
			d.IntegrationKey != entry.Key || d.ExpectedBlueprintID != entry.ExpectedBlueprintID ||
			d.ExpectedAssistantProgramID != entry.ExpectedProgramID {
			return nil
		}
		seen[d.ID] = true
		found := false
		for _, b := range current.ResolvedBlueprints {
			programID := ""
			if b.Template.AssistantProgram != nil {
				programID = b.Template.AssistantProgram.ID
			} else if b.Template.AssistantProject != nil {
				programID = b.Template.AssistantProject.Home.ProgramID
			}
			if b.ID == d.ExpectedBlueprintID && b.Template.SetupQuestID == d.ID && programID == d.ExpectedAssistantProgramID {
				found = true
			}
		}
		if !found {
			return nil
		}
		d.OwnerPluginID = entry.PluginID
		result = append(result, QuestDefinition{Declaration: d, Ownership: "plugin"})
	}
	return result
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
	plugins installedPluginLister
}

// NewUserTemplateQuestCatalog serves quests attached to user-owned templates.
// List offers a quest only while its integration's plugin is installed (FR 16);
// Lookup and scoping resolve regardless, so an open run can show the
// integration precondition instead of disappearing.
func NewUserTemplateQuestCatalog(library UserTemplateQuestLibrary, plugins installedPluginLister) QuestCatalog {
	return &userTemplateQuestCatalog{library: library, plugins: plugins}
}

func (c *userTemplateQuestCatalog) List(ctx context.Context) ([]QuestSummary, error) {
	all, err := c.listAll(ctx)
	if err != nil {
		return nil, err
	}
	if c.plugins == nil {
		return []QuestSummary{}, nil
	}
	installed, err := c.plugins.List()
	if err != nil {
		return nil, failure(ReasonOwnerUnavailable, 0)
	}
	present := make(map[string]bool, len(installed))
	for _, item := range installed {
		present[strings.ToLower(strings.TrimSpace(item.Name))] = true
	}
	result := make([]QuestSummary, 0, len(all))
	for _, summary := range all {
		if present[summary.integrationPluginID] {
			result = append(result, summary.QuestSummary)
		}
	}
	return result, nil
}

// userTemplateQuestID resolves the quest ID bound to one attachment without the
// install gate, so an existing run stays reachable.
func (c *userTemplateQuestCatalog) userTemplateQuestID(ctx context.Context, templateID, attachmentID string) (string, error) {
	all, err := c.listAll(ctx)
	if err != nil {
		return "", err
	}
	templateID = strings.ToLower(strings.TrimSpace(templateID))
	attachmentID = strings.ToLower(strings.TrimSpace(attachmentID))
	for _, item := range all {
		if item.TemplateID == templateID && item.AttachmentID == attachmentID {
			return item.ID, nil
		}
	}
	return "", failure(ReasonJourneyUnavailable, 0)
}

type userTemplateQuestSummary struct {
	QuestSummary
	integrationPluginID string
}

// listAll lists every valid, unambiguous user-template quest, installed or not.
func (c *userTemplateQuestCatalog) listAll(ctx context.Context) ([]userTemplateQuestSummary, error) {
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
	result := make([]userTemplateQuestSummary, 0, len(templates))
	for _, template := range templates {
		if !validUserTemplateQuest(template) || attachmentCounts[template.UserSetupQuest.AttachmentID] != 1 ||
			questCounts[template.UserSetupQuest.Declaration.ID] != 1 {
			continue
		}
		declaration := template.UserSetupQuest.Declaration
		integration, _ := reviewedintegration.Get(declaration.IntegrationKey)
		result = append(result, userTemplateQuestSummary{
			QuestSummary: QuestSummary{
				QuestKey: QuestKey{Source: QuestSourceUserTemplate, TemplateID: template.ID, AttachmentID: template.UserSetupQuest.AttachmentID, ID: declaration.ID},
				Title:    declaration.Title, Description: declaration.Description, TemplateID: template.ID, Ownership: string(QuestSourceUserTemplate),
			},
			integrationPluginID: integration.PluginID,
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
	// The install gate does not apply here: an open run keeps resolving.
	all, err := c.listAll(ctx)
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

// questSourceLister lists the quests of one source without reading catalogs
// that serve other sources.
type questSourceLister interface {
	listSource(context.Context, QuestSource) ([]QuestSummary, error)
}

// sourcedQuestCatalog is implemented by catalogs that serve exactly one source.
type sourcedQuestCatalog interface {
	questSource() QuestSource
}

func (c *installedQuestCatalog) questSource() QuestSource          { return QuestSourcePlugin }
func (c *userTemplateQuestCatalog) questSource() QuestSource       { return QuestSourceUserTemplate }
func (c *hostQuestCatalog) questSource() QuestSource               { return QuestSourceHost }
func (c *integrationInstallQuestCatalog) questSource() QuestSource { return QuestSourceHost }

type combinedQuestCatalog struct {
	catalogs []QuestCatalog
}

// listSource lists every catalog that serves the source, or whose source is
// unknown, and keeps only that source's quests.
func (c *combinedQuestCatalog) listSource(ctx context.Context, source QuestSource) ([]QuestSummary, error) {
	result := make([]QuestSummary, 0)
	for _, catalog := range c.catalogs {
		if sourced, ok := catalog.(sourcedQuestCatalog); ok && sourced.questSource() != source {
			continue
		}
		items, err := catalog.List(ctx)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			if normalizeQuestKey(item.QuestKey).Source == source {
				result = append(result, item)
			}
		}
	}
	return result, nil
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

// userTemplateQuestResolver resolves the quest bound to one attachment.
type userTemplateQuestResolver interface {
	userTemplateQuestID(ctx context.Context, templateID, attachmentID string) (string, error)
}

func (c *combinedQuestCatalog) userTemplateQuestID(ctx context.Context, templateID, attachmentID string) (string, error) {
	// A nested combined catalog is a resolver even when it holds no user
	// templates, so try each resolver until one knows the attachment.
	var lastErr error = failure(ReasonJourneyUnavailable, 0)
	for _, catalog := range c.catalogs {
		if resolver, ok := catalog.(userTemplateQuestResolver); ok {
			id, err := resolver.userTemplateQuestID(ctx, templateID, attachmentID)
			if err == nil {
				return id, nil
			}
			lastErr = err
		}
	}
	return "", lastErr
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

// validHostQuestShape accepts the two shapes a host quest may have. An install
// quest must be the one generated for its reviewed integration key.
func validHostQuestShape(declaration *specialist.SetupJourney) bool {
	switch declaration.Shape() {
	case specialist.SetupJourneyShapeAccountLink:
		return true
	case specialist.SetupJourneyShapeIntegrationInstall:
		entry, reviewed := reviewedintegration.Get(declaration.IntegrationKey)
		return reviewed && declaration.ID == IntegrationInstallQuestID(entry.Key) &&
			declaration.ExpectedBlueprintID == entry.ExpectedBlueprintID &&
			declaration.ExpectedAssistantProgramID == entry.ExpectedProgramID
	default:
		return false
	}
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
	case QuestSourceHost:
		return key.PluginID == "" && key.TemplateID == "" && key.AttachmentID == ""
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
	// Resolve it from the catalog entry bound to this exact attachment, without
	// the catalog's install gate so an existing run stays reachable.
	if resolver, ok := s.quests.(userTemplateQuestResolver); ok {
		if id, err := resolver.userTemplateQuestID(ctx, templateID, attachmentID); err == nil {
			key.ID = id
		}
	} else {
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

// ForHostQuest scopes a service copy to one host-compiled quest for one user.
// An unknown ID fails before any handler runs; nothing is created here.
func (s *Service) ForHostQuest(ctx context.Context, userID, questID string) (*Service, error) {
	if s == nil || s.quests == nil {
		return nil, failure(ReasonJourneyUnavailable, 0)
	}
	key := normalizeQuestKey(QuestKey{Source: QuestSourceHost, ID: questID})
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

// Status reports the scoped quest's current projection without ever creating
// a root. A user who has never opened the quest gets exists=false; otherwise
// it is an ordinary Read of the one existing root.
func (s *Service) Status(ctx context.Context, userID string) (*JourneyProjection, bool, error) {
	if s == nil || s.quest == nil || s.store == nil {
		return nil, false, failure(ReasonJourneyUnavailable, 0)
	}
	userID = strings.TrimSpace(userID)
	if !validateCanonicalRef(userID, false) {
		return nil, false, failure(ReasonInputInvalid, 0)
	}
	definition, err := s.quests.Lookup(ctx, normalizeQuestKey(*s.quest))
	if err != nil {
		return nil, false, err
	}
	if definition.Declaration == nil {
		return nil, false, failure(ReasonDeclarationInvalid, 0)
	}
	if _, err := s.store.FindQuestRoot(ctx, userID, *s.quest); err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, false, nil
		}
		return nil, false, failure(ReasonJourneyUnavailable, 0)
	}
	projection, err := s.Read(ctx, userID, "")
	if err != nil {
		return nil, false, err
	}
	return projection, true, nil
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
	// Plugin and user-template quests are project_setup-shaped; the host shapes
	// are checked in their own branch.
	authored := d.Shape() == specialist.SetupJourneyShapeProjectSetup
	switch key.Source {
	case QuestSourcePlugin:
		if d.OwnerPluginID != key.PluginID || !authored {
			return nil, nil, failure(ReasonDeclarationInvalid, 0)
		}
		d.OwnerPluginID = key.PluginID
		identity.SpecialistSlug = pluginQuestSlug
	case QuestSourceUserTemplate:
		if d.OwnerPluginID != "" || d.ExpectedBlueprintID != key.TemplateID || !authored ||
			!validateDigest(definition.DefinitionDigest, false) || !validateDigest(definition.ExecutionDigest, false) {
			return nil, nil, failure(ReasonDeclarationInvalid, 0)
		}
		identity.SpecialistSlug = userTemplateQuestSlug
		identity.Binding = &UserTemplateBinding{
			UserID: userID, TemplateID: key.TemplateID, AttachmentID: key.AttachmentID, QuestID: key.ID,
			DefinitionDigest: definition.DefinitionDigest, ExecutionDigest: definition.ExecutionDigest,
		}
	case QuestSourceHost:
		// Host quests are compiled data: no plugin owner, no attachment binding,
		// and no legacy assistant-owned progress to adopt. They are either an
		// embedded account-link quest or the install quest the host generated for
		// exactly one reviewed integration.
		if d.OwnerPluginID != "" || !validHostQuestShape(d) {
			return nil, nil, failure(ReasonDeclarationInvalid, 0)
		}
		identity.SpecialistSlug = hostQuestSlug
	default:
		return nil, nil, failure(ReasonDeclarationInvalid, 0)
	}
	root, err := s.store.FindQuestRoot(ctx, userID, key)
	if err == nil {
		identity.AssistantID, identity.SpecialistSlug = root.RelationshipID, root.SpecialistSlug
	} else if !errors.Is(err, ErrNotFound) {
		return nil, nil, failure(ReasonJourneyUnavailable, 0)
	}
	return identity, d, nil
}

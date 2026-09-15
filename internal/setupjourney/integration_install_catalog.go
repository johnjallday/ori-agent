package setupjourney

import (
	"context"
	"sort"
	"strings"

	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
	"github.com/johnjallday/ori-agent/internal/specialist"
)

const (
	// integrationInstallQuestPrefix prefixes a reviewed integration key to form
	// its generated install quest ID, for example install_<integration_key>.
	integrationInstallQuestPrefix = reviewedintegration.InstallQuestPrefix
	// Step IDs are fixed for every generated install quest.
	integrationInstallStepID        = "integration"
	integrationInstallSummaryStepID = "summary"
)

// IntegrationInstallQuestID returns the host quest ID generated for one
// reviewed integration key.
func IntegrationInstallQuestID(integrationKey string) string {
	return reviewedintegration.Entry{Key: integrationKey}.InstallQuestID()
}

type installedPluginLister interface {
	List() ([]plugin.InstalledPlugin, error)
}

type integrationInstallQuestCatalog struct {
	entries func() []reviewedintegration.Entry
	manager installedPluginLister
}

// NewIntegrationInstallQuestCatalog generates one two-step install quest per
// reviewed integration. Declarations are built from the compiled registry and
// never from a plugin, template, browser or file. Lookup resolves every entry
// regardless of install state so an open run can finish; List offers an
// install quest only while its plugin is not installed. Neither creates
// progress, and neither inspects, downloads or starts a plugin.
func NewIntegrationInstallQuestCatalog(entries func() []reviewedintegration.Entry, manager installedPluginLister) QuestCatalog {
	return &integrationInstallQuestCatalog{entries: entries, manager: manager}
}

func (c *integrationInstallQuestCatalog) List(ctx context.Context) ([]QuestSummary, error) {
	if c == nil || c.entries == nil || c.manager == nil {
		return nil, failure(ReasonJourneyUnavailable, 0)
	}
	installed, err := c.manager.List()
	if err != nil {
		return nil, failure(ReasonOwnerUnavailable, 0)
	}
	present := make(map[string]bool, len(installed))
	for _, item := range installed {
		present[strings.ToLower(strings.TrimSpace(item.Name))] = true
	}
	result := make([]QuestSummary, 0)
	for _, entry := range c.entries() {
		if present[entry.PluginID] {
			continue
		}
		declaration, ok := integrationInstallDeclaration(entry)
		if !ok {
			continue
		}
		result = append(result, QuestSummary{
			QuestKey: QuestKey{Source: QuestSourceHost, ID: declaration.ID},
			Title:    declaration.Title, Description: declaration.Description,
			Ownership: string(QuestSourceHost), IntegrationKey: declaration.IntegrationKey,
			DisplayName: entry.DisplayName, PublisherLabel: entry.PublisherLabel,
		})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

func (c *integrationInstallQuestCatalog) Lookup(_ context.Context, key QuestKey) (QuestDefinition, error) {
	key = normalizeQuestKey(key)
	if c == nil || c.entries == nil || key.Source != QuestSourceHost || !validQuestKey(key) ||
		!strings.HasPrefix(key.ID, integrationInstallQuestPrefix) {
		return QuestDefinition{}, failure(ReasonInputInvalid, 0)
	}
	for _, entry := range c.entries() {
		if IntegrationInstallQuestID(entry.Key) != key.ID {
			continue
		}
		declaration, ok := integrationInstallDeclaration(entry)
		if !ok {
			return QuestDefinition{}, failure(ReasonDeclarationInvalid, 0)
		}
		return QuestDefinition{Key: key, Declaration: declaration, Ownership: string(QuestSourceHost)}, nil
	}
	return QuestDefinition{}, failure(ReasonJourneyUnavailable, 0)
}

// integrationInstallDeclaration builds and normalizes the install quest for one
// registry entry. Copy comes only from the entry's reviewed display fields.
func integrationInstallDeclaration(entry reviewedintegration.Entry) (*specialist.SetupJourney, bool) {
	name := strings.TrimSpace(entry.DisplayName)
	declaration, err := specialist.NormalizeSetupJourney(specialist.SetupJourney{
		SchemaVersion: specialist.SetupJourneySchemaVersion, Version: 1,
		ID:                         IntegrationInstallQuestID(entry.Key),
		Title:                      entry.InstallTitle,
		Description:                "Install and verify Ori's reviewed " + name + " integration before setting it up.",
		IntegrationKey:             entry.Key,
		ExpectedBlueprintID:        entry.ExpectedBlueprintID,
		ExpectedAssistantProgramID: entry.ExpectedProgramID,
		Steps: []specialist.SetupJourneyStep{
			{
				ID: integrationInstallStepID, Kind: specialist.SetupStepIntegrationInstall,
				Title: entry.InstallTitle, Description: entry.InstallDescription,
			},
			{
				ID: integrationInstallSummaryStepID, Kind: specialist.SetupStepSummary,
				Title:       name + " plugin ready",
				Description: "Setup continues in the " + name + " plugin's own guided setup.",
			},
		},
	})
	if err != nil || declaration.Shape() != specialist.SetupJourneyShapeIntegrationInstall {
		return nil, false
	}
	return declaration, true
}

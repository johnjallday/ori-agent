package server

import (
	"context"
	"errors"
	"runtime"

	"github.com/johnjallday/ori-agent/internal/agenthttp"
	"github.com/johnjallday/ori-agent/internal/assistantdiscovery"
	"github.com/johnjallday/ori-agent/internal/chathttp"
	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
	"github.com/johnjallday/ori-agent/internal/setupjourney"
	"github.com/johnjallday/ori-agent/internal/skills"
)

// journeyQuestList lets the reviewed-integration reader ask the setup journey
// service which quests are listed. It is a list only: nothing is looked up,
// opened or started through it.
type journeyQuestList struct{ journeys *setupjourney.Service }

func (l journeyQuestList) List(ctx context.Context) ([]setupjourney.QuestSummary, error) {
	return l.journeys.ListQuests(ctx)
}

func (journeyQuestList) Lookup(context.Context, setupjourney.QuestKey) (setupjourney.QuestDefinition, error) {
	return setupjourney.QuestDefinition{}, errors.New("quest lookup is not offered to the assistant's reader")
}

// reviewedPluginQuest resolves, for a reviewed integration whose plugin is
// installed and enabled, the plugin's own guided setup (the quest an install
// quest hands off to) and whether this user has finished it. It never creates
// a run: a quest the user has not opened is simply not complete.
func reviewedPluginQuest(journeys *setupjourney.Service) func(context.Context, string, string) (assistantdiscovery.PluginSetupQuest, bool) {
	if journeys == nil {
		return nil
	}
	return func(ctx context.Context, userID, integrationKey string) (assistantdiscovery.PluginSetupQuest, bool) {
		handoff := setupjourney.IntegrationHandoff(ctx, journeyQuestList{journeys}, integrationKey)
		if handoff == nil {
			return assistantdiscovery.PluginSetupQuest{}, false
		}
		quest := assistantdiscovery.PluginSetupQuest{Ref: assistantdiscovery.SetupQuestRef{
			Source: assistantdiscovery.SetupQuestSourcePlugin, PluginID: handoff.PluginID, ID: handoff.ID,
		}}
		scoped, err := journeys.ForQuest(ctx, userID, handoff.PluginID, handoff.ID)
		if err != nil {
			return quest, true
		}
		if projection, opened, err := scoped.Status(ctx, userID); err == nil && opened && projection != nil {
			quest.Complete = projection.Lifecycle == setupjourney.LifecycleReady
		}
		return quest, true
	}
}

// Constructing this adapter performs no inventory scan, refresh, subprocess or
// MCP start. All readers reuse the existing UI's exact source/cache owners.
func (s *Server) newPersonalAssistantDiscovery() agenthttp.AssistantDiscoveryReader {
	if s.Handlers == nil {
		return nil
	}
	var manager assistantdiscovery.SkillInventoryReader
	var servers assistantdiscovery.MCPInventoryReader
	var registry assistantdiscovery.MCPCatalogReader
	var catalog skills.MarketplaceCatalog
	if s.Handlers.Skills != nil {
		if source := s.Handlers.Skills.Manager(); source != nil {
			manager = source
		}
		catalog = s.Handlers.Skills.MarketplaceCatalog()
	}
	if s.Handlers.MCP != nil {
		if source := s.Handlers.MCP.CatalogStore(); source != nil {
			registry = source
		}
	}
	if s.Integration != nil && s.Integration.MCPRegistry != nil {
		servers = s.Integration.MCPRegistry
	}
	if manager == nil && servers == nil && registry == nil {
		return nil
	}
	service := assistantdiscovery.NewService(manager, servers, registry, catalog)
	// The reviewed registry is the only source of an integration's name; the
	// plugin store says whether it is installed and enabled.
	if s.Handlers.Plugin != nil {
		if plugins := s.Handlers.Plugin.Manager(); plugins != nil {
			service.ConfigureReviewedIntegrations(assistantdiscovery.ReviewedIntegrationSources{
				Entries:     reviewedintegration.All,
				Plugins:     plugins,
				Platform:    runtime.GOOS + "/" + runtime.GOARCH,
				PluginQuest: reviewedPluginQuest(s.setupJourneys),
			})
		}
	}
	if s.Core != nil && s.Core.ConfigManager != nil {
		manager := s.Core.ConfigManager
		service.ConfigurePublicSearch(chathttp.NewPublicWebSearchAdapter(), func() bool {
			settings := manager.Get().Utility
			return settings.Enabled && settings.SearchProvider == "duckduckgo"
		})
	}
	return service
}

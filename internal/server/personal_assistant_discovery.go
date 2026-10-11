package server

import (
	"github.com/johnjallday/ori-agent/internal/agenthttp"
	"github.com/johnjallday/ori-agent/internal/assistantdiscovery"
	"github.com/johnjallday/ori-agent/internal/chathttp"
	"github.com/johnjallday/ori-agent/internal/skills"
)

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
	if s.Core != nil && s.Core.ConfigManager != nil {
		manager := s.Core.ConfigManager
		service.ConfigurePublicSearch(chathttp.NewPublicWebSearchAdapter(), func() bool {
			settings := manager.Get().Utility
			return settings.Enabled && settings.SearchProvider == "duckduckgo"
		})
	}
	return service
}

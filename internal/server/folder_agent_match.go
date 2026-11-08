package server

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/johnjallday/ori-agent/internal/folderlook"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/mcp"
	"github.com/johnjallday/ori-agent/internal/mcp/mcpregistry"
	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/pluginworkspace"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// folderAgentSearchTimeout bounds the three marketplace searches together.
const folderAgentSearchTimeout = 15 * time.Second

// folderAgentSkillsPerWord is how many skills.sh results one word asks for.
const folderAgentSkillsPerWord = 4

// skillsMarketplaceName labels a skills.sh hit in the report and plan.
const skillsMarketplaceName = "skills.sh"

// folderAgentMatches is sources (c) and (d) of the tool candidates: the same
// services the Find tools panel searches, called in process with the host
// table's words only, and the plugins, skills and servers already installed.
type folderAgentMatches struct{ builder *ServerBuilder }

func (m folderAgentMatches) Search(parent context.Context, words []string) []folderlook.SearchHit {
	b := m.builder
	ctx, cancel := context.WithTimeout(parent, folderAgentSearchTimeout)
	defer cancel()
	var hits []folderlook.SearchHit
	for _, word := range words {
		if b.skillsHandler != nil {
			if catalog := b.skillsHandler.MarketplaceCatalog(); catalog != nil {
				result := catalog.Search(ctx, word, folderAgentSkillsPerWord)
				for _, match := range result.Results {
					if match.Package == "" || match.Skill == "" {
						continue
					}
					hits = append(hits, folderlook.SearchHit{Kind: folderlook.KindSkill, ID: match.Package, Name: match.Skill,
						Description: strings.TrimSpace(match.Repository + " · " + match.Installs), Marketplace: skillsMarketplaceName})
				}
			}
		}
		if b.mcpCatalogStore != nil {
			// Cache only: the search never refreshes the registry itself.
			result := b.mcpCatalogStore.SearchCached(ctx, mcpregistry.SearchQuery{Text: word, Limit: 6, MetadataOnly: true})
			for _, match := range result.Matches {
				entry := match.Entry
				if entry.Name == "" {
					continue
				}
				hits = append(hits, folderlook.SearchHit{Kind: folderlook.KindMCP, ID: entry.Name, Name: entry.Name,
					Description: entry.Description, Marketplace: entry.Source})
			}
		}
		if b.pluginHandler != nil {
			marketplaces, err := b.pluginHandler.Manager().Marketplaces()
			if err == nil {
				for _, marketplace := range marketplaces {
					for _, entry := range marketplace.Plugins {
						if pluginMatches(entry, word) {
							hits = append(hits, folderlook.SearchHit{Kind: folderlook.KindPlugin, ID: marketplace.Name + "/" + entry.Name,
								Name: entry.Name, Description: entry.Description, Marketplace: marketplace.Name})
						}
					}
				}
			}
		}
		if ctx.Err() != nil {
			break
		}
	}
	return hits
}

// pluginMatches compares one word with a marketplace entry's name,
// description, keywords and tags, case-insensitively.
func pluginMatches(entry plugin.MarketplaceEntry, word string) bool {
	word = strings.ToLower(strings.TrimSpace(word))
	if word == "" {
		return false
	}
	fields := append([]string{entry.Name, entry.Description}, append(entry.Keywords, entry.Tags...)...)
	for _, field := range fields {
		if strings.Contains(strings.ToLower(field), word) {
			return true
		}
	}
	return false
}

func (m folderAgentMatches) Installed(context.Context) folderlook.Installed {
	b := m.builder
	plugins := map[string]bool{}
	if b.pluginHandler != nil {
		if installed, err := b.pluginHandler.Manager().List(); err == nil {
			for _, p := range installed {
				plugins[strings.ToLower(p.Name)] = true
			}
		}
	}
	servers := map[string]bool{}
	if b.mcpRegistry != nil {
		for _, server := range b.mcpRegistry.ListServers() {
			servers[strings.ToLower(server.Name)] = true
		}
	}
	return func(kind, id string) bool {
		switch kind {
		case folderlook.KindPlugin:
			_, name, _ := strings.Cut(id, "/")
			return plugins[strings.ToLower(name)]
		case folderlook.KindMCP:
			return servers[strings.ToLower(id)]
		}
		// A skills.sh package installs under its own skill name; whether it
		// is already in the Skills folder is settled at install time.
		return false
	}
}

// addFolderAgentAddOn installs one selected add-on and binds it to the
// workspace, the way the Find tools panel's Add selected does.
func (b *ServerBuilder) addFolderAgentAddOn(ctx context.Context, workspaceID string, c folderlook.Candidate) error {
	_, id, ok := strings.Cut(c.ID, ":")
	if c.Ref != "" {
		id = c.Ref
	}
	if !ok || id == "" {
		return fmt.Errorf("unknown add-on %q", c.ID)
	}
	switch c.Kind {
	case folderlook.KindPlugin:
		marketplace, name, ok := strings.Cut(id, "/")
		if !ok || b.pluginHandler == nil {
			return errors.New("plugins are unavailable")
		}
		manager := b.pluginHandler.Manager()
		installed, err := manager.List()
		if err != nil {
			return err
		}
		present := false
		for _, p := range installed {
			present = present || strings.EqualFold(p.Name, name)
		}
		if !present {
			// The owner's Set it up is the confirmation the panel's
			// "Add selected" sends.
			if _, err := manager.InstallFromMarketplace(marketplace, name, "", func(plugin.TrustReport) bool { return true }); err != nil {
				return err
			}
		}
		result, err := pluginworkspace.New(manager, b.workspaceStore).Reconcile(pluginworkspace.Request{
			WorkspaceID: workspaceID, Plugins: []string{name}, AllowEnable: true,
		})
		if err == nil && result.SaveErr != nil {
			err = result.SaveErr
		}
		return err
	case folderlook.KindSkill:
		if b.skillsHandler == nil {
			return errors.New("skills are unavailable")
		}
		skillName, err := b.skillsHandler.InstallMarketplacePackage(ctx, id)
		if err != nil {
			return err
		}
		return b.workspaceStore.Update(workspaceID, func(ws *workspace.Workspace) error {
			return ws.UpsertSkillBinding(workspace.SkillBinding{ID: uuid.NewString(), SkillName: skillName, Enabled: true, Trusted: true})
		})
	case folderlook.KindMCP:
		if err := b.ensureFolderAgentServer(ctx, id); err != nil {
			return err
		}
		return b.workspaceStore.Update(workspaceID, func(ws *workspace.Workspace) error {
			return ws.UpsertMCPBinding(workspace.MCPBinding{ID: uuid.NewString(), ServerName: id, Enabled: true})
		})
	}
	return fmt.Errorf("unknown add-on kind %q", c.Kind)
}

// ensureFolderAgentServer configures a catalog MCP server the user does not
// have yet, from the registry's own cached listing.
func (b *ServerBuilder) ensureFolderAgentServer(ctx context.Context, name string) error {
	if b.mcpConfigManager == nil || b.mcpRegistry == nil || b.mcpCatalogStore == nil {
		return errors.New("MCP servers are unavailable")
	}
	if existing, err := b.mcpConfigManager.GetServer(name); err == nil && existing != nil {
		return nil
	}
	result := b.mcpCatalogStore.SearchCached(ctx, mcpregistry.SearchQuery{Text: name, Limit: 50})
	for _, match := range result.Matches {
		entry := match.Entry
		if entry.Name != name {
			continue
		}
		config := mcp.ServerConfig{Name: entry.Name, Command: entry.Command, Args: entry.Args, Env: entry.Env,
			EnvRequired: entry.EnvRequired, Transport: entry.Transport, Enabled: true}
		if err := b.mcpConfigManager.AddServer(config); err != nil {
			return err
		}
		if err := b.mcpRegistry.AddServer(config); err != nil {
			logger.Warn("Folder agent added an MCP server the registry could not start yet", logger.Fields{"server": name, "error": err.Error()})
		}
		return nil
	}
	return fmt.Errorf("the server %s is no longer listed", name)
}

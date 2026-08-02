package server

import (
	"sort"
	"strings"

	"github.com/johnjallday/ori-agent/internal/mcp"
	"github.com/johnjallday/ori-agent/internal/skills"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// toolboxLibraryAdapter supplies the Workshop editor's "global library" group:
// capabilities Ori knows about that a given workspace has NOT approved
// (PRD FR-43).
//
// The group exists to make the difference between "select an approved thing"
// and "go set something up" visible before the click. Everything it returns is
// therefore presented as unavailable, with a setup hint — reading this list
// never installs, connects, enables, trusts, or classifies anything (FR-45).
type toolboxLibraryAdapter struct {
	skills *skills.Manager
	mcp    mcpTemplateLister
}

// mcpTemplateLister is the slice of the MCP config manager this adapter needs.
// Narrow on purpose: the library is a read model and must not be able to
// register, start, or modify a server.
//
// It lists only ENABLED templates. A globally disabled server is not something
// the user can connect to this workspace right now, and offering it would be a
// dead end presented as an option.
type mcpTemplateLister interface {
	GetEnabledServers() ([]mcp.ServerConfig, error)
}

func newToolboxLibraryAdapter(skillsManager *skills.Manager, mcpConfig mcpTemplateLister) *toolboxLibraryAdapter {
	return &toolboxLibraryAdapter{skills: skillsManager, mcp: mcpConfig}
}

func (a *toolboxLibraryAdapter) ListLibrarySkills() []workspace.ToolboxLibraryItem {
	if a == nil || a.skills == nil {
		return nil
	}
	// The empty agent name lists the shared sources (repo, .agents, personal)
	// rather than one agent's private collection — this group is about what Ori
	// as a whole offers, not what one agent has learned.
	available, err := a.skills.ListSkills("")
	if err != nil {
		return nil
	}

	items := make([]workspace.ToolboxLibraryItem, 0, len(available))
	seen := make(map[string]struct{}, len(available))
	for _, skill := range available {
		name := strings.TrimSpace(skill.Name)
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		items = append(items, workspace.ToolboxLibraryItem{
			Name:      name,
			Summary:   strings.TrimSpace(skill.Description),
			SetupHint: "Add this skill to the workspace to make it selectable here.",
		})
	}
	sort.SliceStable(items, func(i, j int) bool {
		return strings.ToLower(items[i].Name) < strings.ToLower(items[j].Name)
	})
	return items
}

func (a *toolboxLibraryAdapter) ListLibraryMCPServers() []workspace.ToolboxLibraryItem {
	if a == nil || a.mcp == nil {
		return nil
	}

	configured, err := a.mcp.GetEnabledServers()
	if err != nil {
		return nil
	}

	items := make([]workspace.ToolboxLibraryItem, 0, len(configured))
	for _, server := range configured {
		name := strings.TrimSpace(server.Name)
		if name == "" {
			continue
		}
		items = append(items, workspace.ToolboxLibraryItem{
			Name:      name,
			SetupHint: "Connect this server to the workspace to make its operations selectable here.",
		})
	}
	sort.SliceStable(items, func(i, j int) bool {
		return strings.ToLower(items[i].Name) < strings.ToLower(items[j].Name)
	})
	return items
}

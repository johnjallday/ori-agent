package agenthttp

import (
	"strings"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// HomeNavEntry is one destination in the app navigation catalog. The catalog is
// the single source of truth for navigation answers and `navigate` actions
// (PRD 4.8): the harness may only emit static destinations that exist here, or
// dynamic destinations resolved to a real workspace/session/task id.
type HomeNavEntry struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Href        string `json:"href"`
	Description string `json:"description"`
	// Aliases are phrases a user might say to mean this destination. Used for
	// "open X" disambiguation and for grounding navigation answers.
	Aliases []string `json:"-"`
}

// homeNavCatalog is kept in sync with the page routes registered in
// internal/server/routes.go. home_nav_catalog_test.go asserts every Href maps to
// a registered route.
var homeNavCatalog = []HomeNavEntry{
	{
		Key:         "home",
		Label:       "Home",
		Href:        "/",
		Description: "The dashboard with Ask Ori, recent workspaces, upcoming tasks, and recent activity.",
		Aliases:     []string{"home", "dashboard", "start page", "main page"},
	},
	{
		Key:         "agents",
		Label:       "Agents",
		Href:        "/agents",
		Description: "Create, configure, and manage your agents (model, system prompt, tools).",
		Aliases:     []string{"agents", "agent", "my agents", "agent list"},
	},
	{
		Key:         "workspaces",
		Label:       "Workspaces",
		Href:        "/workspaces",
		Description: "Browse and open workspaces — collaborative spaces with tasks, notes, files, and sessions.",
		Aliases:     []string{"workspaces", "workspace", "my workspaces", "workspace list"},
	},
	{
		Key:         "action-center",
		Label:       "Action Center",
		Href:        "/action-center",
		Description: "Review opportunities surfaced across workspaces and decide what to act on next.",
		Aliases:     []string{"action center", "action-center", "actions", "opportunities", "action centre"},
	},
	{
		Key:         "vaults",
		Label:       "Vaults",
		Href:        "/vaults",
		Description: "Manage stored secrets and credentials used by agents and connectors.",
		Aliases:     []string{"vaults", "vault", "secrets", "credentials", "api keys vault"},
	},
	{
		Key:         "mcp",
		Label:       "MCP",
		Href:        "/mcp",
		Description: "Configure Model Context Protocol connectors (MCP servers) that provide external tools.",
		Aliases:     []string{"mcp", "mcp settings", "mcp page", "mcp connectors", "mcp servers", "connectors"},
	},
	{
		Key:         "settings",
		Label:       "Settings",
		Href:        "/settings",
		Description: "Global configuration: API keys, LLM provider, system model, and preferences.",
		Aliases:     []string{"settings", "preferences", "configuration", "config", "api keys"},
	},
	{
		Key:         "profile",
		Label:       "Profile",
		Href:        "/profile",
		Description: "Manage user identity, context, and assistant preferences.",
		Aliases:     []string{"profile", "user profile", "about you", "my profile"},
	},
	{
		Key:         "usage",
		Label:       "Usage",
		Href:        "/usage",
		Description: "Track LLM usage and cost across providers, models, and agents.",
		Aliases:     []string{"usage", "cost", "costs", "spending", "billing", "usage and cost"},
	},
}

// HomeNavCatalog returns a copy of the navigation catalog.
func HomeNavCatalog() []HomeNavEntry {
	out := make([]HomeNavEntry, len(homeNavCatalog))
	copy(out, homeNavCatalog)
	return out
}

// FindHomeNavEntry resolves a phrase to a catalog entry by exact label/alias
// match (case-insensitive). Returns false when nothing matches; callers must not
// invent a destination.
func FindHomeNavEntry(query string) (HomeNavEntry, bool) {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return HomeNavEntry{}, false
	}
	q = strings.Trim(q, " .,!?:;\"'")
	q = strings.TrimPrefix(q, "the ")
	q = strings.TrimSuffix(q, " page")
	q = strings.TrimSpace(q)
	for _, entry := range homeNavCatalog {
		if strings.EqualFold(entry.Label, q) || strings.EqualFold(entry.Key, q) {
			return entry, true
		}
		for _, alias := range entry.Aliases {
			if alias == q {
				return entry, true
			}
		}
	}
	return HomeNavEntry{}, false
}

// MatchNavEntryInPrompt returns the catalog entry whose label/alias appears in
// the prompt, preferring the longest match. Used to build a grounded `navigate`
// action for navigation answers.
func MatchNavEntryInPrompt(prompt string) (HomeNavEntry, bool) {
	p := strings.ToLower(prompt)
	if p == "" {
		return HomeNavEntry{}, false
	}
	best := HomeNavEntry{}
	bestLen := 0
	for _, entry := range homeNavCatalog {
		if entry.Key == "home" {
			continue
		}
		candidates := append([]string{strings.ToLower(entry.Label)}, entry.Aliases...)
		for _, c := range candidates {
			if len(c) >= 4 && strings.Contains(p, c) && len(c) > bestLen {
				best = entry
				bestLen = len(c)
			}
		}
	}
	return best, bestLen > 0
}

// matchWorkspaceInPrompt returns the id and name of a workspace whose name
// appears in the prompt (longest match wins), so navigation answers resolve to a
// real workspace id.
func matchWorkspaceInPrompt(store workspace.Store, prompt string) (id, name string, ok bool) {
	if store == nil {
		return "", "", false
	}
	p := strings.ToLower(prompt)
	ids, err := store.List()
	if err != nil {
		return "", "", false
	}
	bestLen := 0
	for _, wsID := range ids {
		ws, getErr := store.Get(wsID)
		if getErr != nil || ws == nil {
			continue
		}
		n := strings.ToLower(strings.TrimSpace(ws.Name))
		if len(n) >= 3 && strings.Contains(p, n) && len(n) > bestLen {
			id, name, bestLen = ws.ID, ws.Name, len(n)
		}
	}
	return id, name, bestLen > 0
}

// promptMatchesNavCatalog reports whether the prompt mentions any catalog
// destination (label/alias substring). Used by intent classification to route
// "where do I…/open <feature>" asks to app_navigation.
func promptMatchesNavCatalog(prompt string) bool {
	p := strings.ToLower(prompt)
	if p == "" {
		return false
	}
	for _, entry := range homeNavCatalog {
		if entry.Key == "home" {
			continue // too generic to be a reliable signal
		}
		if strings.Contains(p, strings.ToLower(entry.Label)) {
			return true
		}
		for _, alias := range entry.Aliases {
			if len(alias) >= 4 && strings.Contains(p, alias) {
				return true
			}
		}
	}
	return false
}

// promptMentionsWorkspaceByName reports whether the prompt contains a phrase that
// resolves to a real workspace name. Used so "open the Q3 Planning workspace"
// routes to app_navigation only when that workspace actually exists.
func promptMentionsWorkspaceByName(store workspace.Store, prompt string) bool {
	if store == nil {
		return false
	}
	p := strings.ToLower(prompt)
	if !strings.Contains(p, "workspace") {
		return false
	}
	ids, err := store.List()
	if err != nil {
		return false
	}
	for _, id := range ids {
		ws, getErr := store.Get(id)
		if getErr != nil || ws == nil {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(ws.Name))
		if len(name) >= 3 && strings.Contains(p, name) {
			return true
		}
	}
	return false
}

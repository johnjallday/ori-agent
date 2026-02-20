package mcpregistry

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// builtinServers is the Ori Curated list of well-known MCP servers.
var builtinServers = []RegistryEntry{
	{
		Name:        "filesystem",
		Description: "Provides read/write access to local files and directories",
		Command:     "npx",
		Args:        []string{"-y", "@modelcontextprotocol/server-filesystem", "/path/to/allowed/directory"},
		Transport:   "stdio",
		Category:    "file-system",
		Maintainer:  "Anthropic",
		Tags:        []string{"files", "local", "read", "write"},
	},
	{
		Name:        "github",
		Description: "Interact with GitHub repositories, issues, and pull requests",
		Command:     "npx",
		Args:        []string{"-y", "@modelcontextprotocol/server-github"},
		Transport:   "stdio",
		Category:    "development",
		Maintainer:  "Anthropic",
		Tags:        []string{"git", "github", "code", "issues", "prs"},
		EnvRequired: map[string]string{"GITHUB_TOKEN": "GitHub personal access token"},
	},
	{
		Name:        "brave-search",
		Description: "Perform web searches using the Brave Search API",
		Command:     "npx",
		Args:        []string{"-y", "@modelcontextprotocol/server-brave-search"},
		Transport:   "stdio",
		Category:    "search",
		Maintainer:  "Anthropic",
		Tags:        []string{"search", "web", "brave"},
		EnvRequired: map[string]string{"BRAVE_API_KEY": "Brave Search API key"},
	},
	{
		Name:        "postgres",
		Description: "Query and manage PostgreSQL databases with read/write capabilities",
		Command:     "npx",
		Args:        []string{"-y", "@modelcontextprotocol/server-postgres"},
		Transport:   "stdio",
		Category:    "database",
		Maintainer:  "Anthropic",
		Tags:        []string{"database", "sql", "postgres"},
		EnvRequired: map[string]string{"DATABASE_URL": "PostgreSQL connection string"},
	},
	{
		Name:        "memory",
		Description: "Persistent key-value memory storage across conversations",
		Command:     "npx",
		Args:        []string{"-y", "@modelcontextprotocol/server-memory"},
		Transport:   "stdio",
		Category:    "storage",
		Maintainer:  "Anthropic",
		Tags:        []string{"memory", "storage", "persistence"},
	},
	{
		Name:        "sqlite",
		Description: "Read and query local SQLite database files",
		Command:     "npx",
		Args:        []string{"-y", "@modelcontextprotocol/server-sqlite", "--db-path", "/path/to/db.sqlite"},
		Transport:   "stdio",
		Category:    "database",
		Maintainer:  "Anthropic",
		Tags:        []string{"database", "sqlite", "sql"},
	},
	{
		Name:        "puppeteer",
		Description: "Browser automation and web scraping using Puppeteer",
		Command:     "npx",
		Args:        []string{"-y", "@modelcontextprotocol/server-puppeteer"},
		Transport:   "stdio",
		Category:    "automation",
		Maintainer:  "Anthropic",
		Tags:        []string{"browser", "scraping", "automation", "puppeteer"},
	},
	{
		Name:        "slack",
		Description: "Read and send messages in Slack workspaces",
		Command:     "npx",
		Args:        []string{"-y", "@modelcontextprotocol/server-slack"},
		Transport:   "stdio",
		Category:    "communication",
		Maintainer:  "Anthropic",
		Tags:        []string{"slack", "messaging", "team"},
		EnvRequired: map[string]string{
			"SLACK_BOT_TOKEN": "Slack bot token",
			"SLACK_TEAM_ID":   "Slack team/workspace ID",
		},
	},
	{
		Name:        "google-maps",
		Description: "Access Google Maps for location, directions, and place search",
		Command:     "npx",
		Args:        []string{"-y", "@modelcontextprotocol/server-google-maps"},
		Transport:   "stdio",
		Category:    "location",
		Maintainer:  "Anthropic",
		Tags:        []string{"maps", "location", "places", "directions"},
		EnvRequired: map[string]string{"GOOGLE_MAPS_API_KEY": "Google Maps API key"},
	},
	{
		Name:        "gdrive",
		Description: "Access and manage files in Google Drive",
		Command:     "npx",
		Args:        []string{"-y", "@modelcontextprotocol/server-gdrive"},
		Transport:   "stdio",
		Category:    "storage",
		Maintainer:  "Anthropic",
		Tags:        []string{"google", "drive", "files", "cloud"},
	},
	{
		Name:        "google-calendar",
		Description: "Read and create Google Calendar events",
		Command:     "npx",
		Args:        []string{"-y", "@modelcontextprotocol/server-google-calendar"},
		Transport:   "stdio",
		Category:    "productivity",
		Maintainer:  "Community",
		Tags:        []string{"calendar", "google", "scheduling"},
		EnvRequired: map[string]string{"GOOGLE_CALENDAR_CREDENTIALS": "Google OAuth credentials JSON"},
	},
	{
		Name:        "aws-kb-retrieval",
		Description: "Retrieve data from AWS Knowledge Base using Bedrock Agent Runtime",
		Command:     "npx",
		Args:        []string{"-y", "@modelcontextprotocol/server-aws-kb-retrieval"},
		Transport:   "stdio",
		Category:    "cloud",
		Maintainer:  "Anthropic",
		Tags:        []string{"aws", "bedrock", "knowledge-base", "rag"},
		EnvRequired: map[string]string{
			"AWS_ACCESS_KEY_ID":     "AWS access key ID",
			"AWS_SECRET_ACCESS_KEY": "AWS secret access key",
			"AWS_REGION":            "AWS region",
		},
	},
	{
		Name:        "sentry",
		Description: "Retrieve and analyze issues from Sentry error tracking",
		Command:     "npx",
		Args:        []string{"-y", "@modelcontextprotocol/server-sentry"},
		Transport:   "stdio",
		Category:    "development",
		Maintainer:  "Anthropic",
		Tags:        []string{"sentry", "errors", "monitoring", "debugging"},
		EnvRequired: map[string]string{"SENTRY_AUTH_TOKEN": "Sentry authentication token"},
	},
	{
		Name:        "everart",
		Description: "Generate images using EverArt AI image generation API",
		Command:     "npx",
		Args:        []string{"-y", "@modelcontextprotocol/server-everart"},
		Transport:   "stdio",
		Category:    "ai",
		Maintainer:  "Anthropic",
		Tags:        []string{"images", "ai", "generation", "art"},
		EnvRequired: map[string]string{"EVERART_API_KEY": "EverArt API key"},
	},
	{
		Name:        "sequential-thinking",
		Description: "Structured problem-solving with iterative thought chains",
		Command:     "npx",
		Args:        []string{"-y", "@modelcontextprotocol/server-sequential-thinking"},
		Transport:   "stdio",
		Category:    "ai",
		Maintainer:  "Anthropic",
		Tags:        []string{"thinking", "reasoning", "chain-of-thought"},
	},
	{
		Name:        "fetch",
		Description: "Fetch and extract content from URLs for web research",
		Command:     "npx",
		Args:        []string{"-y", "@modelcontextprotocol/server-fetch"},
		Transport:   "stdio",
		Category:    "search",
		Maintainer:  "Anthropic",
		Tags:        []string{"http", "fetch", "web", "scraping"},
	},
	{
		Name:        "redis",
		Description: "Interact with Redis key-value stores for caching and data access",
		Command:     "npx",
		Args:        []string{"-y", "@modelcontextprotocol/server-redis"},
		Transport:   "stdio",
		Category:    "database",
		Maintainer:  "Community",
		Tags:        []string{"redis", "cache", "key-value", "database"},
		EnvRequired: map[string]string{"REDIS_URL": "Redis connection URL"},
	},
	{
		Name:        "git",
		Description: "Interact with local Git repositories for history and diffs",
		Command:     "uvx",
		Args:        []string{"mcp-server-git", "--repository", "/path/to/repo"},
		Transport:   "stdio",
		Category:    "development",
		Maintainer:  "Community",
		Tags:        []string{"git", "version-control", "code"},
	},
	{
		Name:        "docker",
		Description: "Manage Docker containers, images, and networks",
		Command:     "npx",
		Args:        []string{"-y", "@modelcontextprotocol/server-docker"},
		Transport:   "stdio",
		Category:    "devops",
		Maintainer:  "Community",
		Tags:        []string{"docker", "containers", "devops"},
	},
	{
		Name:        "notion",
		Description: "Read and write pages and databases in Notion workspaces",
		Command:     "npx",
		Args:        []string{"-y", "@modelcontextprotocol/server-notion"},
		Transport:   "stdio",
		Category:    "productivity",
		Maintainer:  "Community",
		Tags:        []string{"notion", "notes", "productivity", "wiki"},
		EnvRequired: map[string]string{"NOTION_API_KEY": "Notion integration token"},
	},
}

// Fetcher fetches RegistryEntry lists from remote sources.
type Fetcher struct {
	client *http.Client
}

// NewFetcher creates a new Fetcher with a sensible HTTP timeout.
func NewFetcher() *Fetcher {
	return &Fetcher{
		client: &http.Client{Timeout: 15 * time.Second},
	}
}

// FetchAll fetches from all enabled sources in parallel and merges the results.
func (f *Fetcher) FetchAll(sources []RegistrySource) []RegistryEntry {
	type result struct {
		entries []RegistryEntry
	}

	var wg sync.WaitGroup
	results := make(chan result, len(sources))

	for _, src := range sources {
		if !src.Enabled {
			continue
		}
		wg.Add(1)
		go func(s RegistrySource) {
			defer wg.Done()
			entries, err := f.FetchSource(s)
			if err == nil {
				results <- result{entries}
			} else {
				results <- result{}
			}
		}(src)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	var all []RegistryEntry
	for r := range results {
		all = append(all, r.entries...)
	}
	return all
}

// FetchSource fetches entries from a single registry source.
func (f *Fetcher) FetchSource(src RegistrySource) ([]RegistryEntry, error) {
	switch src.SourceType {
	case "builtin":
		entries := make([]RegistryEntry, len(builtinServers))
		copy(entries, builtinServers)
		for i := range entries {
			entries[i].Source = src.Name
		}
		return entries, nil
	case "github":
		url := githubRawURL(src.URL)
		return f.fetchURL(url, src.Name)
	case "url":
		return f.fetchURL(src.URL, src.Name)
	default:
		return nil, fmt.Errorf("unknown source type: %s", src.SourceType)
	}
}

func (f *Fetcher) fetchURL(url, sourceName string) ([]RegistryEntry, error) {
	resp, err := f.client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("HTTP GET failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected HTTP status %d from %s", resp.StatusCode, url)
	}

	var reg RemoteRegistry
	if err := json.NewDecoder(resp.Body).Decode(&reg); err != nil {
		return nil, fmt.Errorf("failed to decode registry JSON from %s: %w", url, err)
	}

	for i := range reg.Servers {
		reg.Servers[i].Source = sourceName
	}
	return reg.Servers, nil
}

// githubRawURL converts a "user/repo" GitHub shorthand to the raw JSON registry URL.
// If the input is already a full URL it is returned unchanged.
func githubRawURL(shorthand string) string {
	if strings.HasPrefix(shorthand, "http://") || strings.HasPrefix(shorthand, "https://") {
		return shorthand
	}
	return fmt.Sprintf("https://raw.githubusercontent.com/%s/main/registry.json", shorthand)
}

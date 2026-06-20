package mcphttp

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/mcp"
)

// maxReadmeBytes caps how much README content we hold in memory / return to the
// browser, guarding against pathologically large files.
const maxReadmeBytes = 256 * 1024

// readmeInfo carries fetched README content plus where it came from.
type readmeInfo struct {
	Markdown  string `json:"markdown"`
	Source    string `json:"source"`     // "npm" or "github"
	SourceURL string `json:"source_url"` // human-facing link to the source
}

// serverDetailsResponse is the payload for GET /api/mcp/servers/{name}/details.
// It bundles everything the detail modal needs in a single round-trip.
type serverDetailsResponse struct {
	Server       string              `json:"server"`
	Status       mcp.ServerStatus    `json:"status"`
	StartError   string              `json:"start_error,omitempty"`
	Command      string              `json:"command"`
	Args         []string            `json:"args"`
	Transport    string              `json:"transport"`
	Enabled      bool                `json:"enabled"`
	EnvKeys      []string            `json:"env_keys,omitempty"` // keys only — values are never exposed
	Instructions string              `json:"instructions,omitempty"`
	ServerInfo   *mcp.Implementation `json:"server_info,omitempty"`
	Readme       *readmeInfo         `json:"readme,omitempty"`
	Tools        []mcp.Tool          `json:"tools"`
}

// GetServerDetailsHandler returns a consolidated detail view for a single MCP
// server: config summary, the server's native initialize instructions, a
// best-effort fetched README, and the live tool list.
// GET /api/mcp/servers/{name}/details
func (h *Handler) GetServerDetailsHandler(w http.ResponseWriter, r *http.Request) {
	serverName := r.PathValue("name")
	if serverName == "" {
		orihttp.BadRequest(w, "Server name required")
		return
	}

	server, err := h.registry.GetServer(serverName)
	if err != nil {
		orihttp.NotFound(w, err.Error())
		return
	}

	// The README, config, and identity don't need a running server, so merely
	// opening the detail view must not spawn a process. Tools and native
	// instructions are only available from a live session, so we start the
	// server only when the caller explicitly opts in via ?start=true.
	status := server.GetStatus()
	var startErr string
	if r.URL.Query().Get("start") == "true" && (status == mcp.StatusStopped || status == mcp.StatusError) {
		if status == mcp.StatusError {
			_ = h.registry.StopServer(serverName)
		}
		if err := h.registry.StartServer(serverName); err != nil {
			startErr = err.Error()
			logger.Warn("Failed to start MCP server while loading details", logger.Fields{
				"server": serverName,
				"error":  err,
			})
		}
	}

	cfg := server.GetConfig()
	resp := serverDetailsResponse{
		Server:       serverName,
		Status:       server.GetStatus(),
		StartError:   startErr,
		Command:      cfg.Command,
		Args:         cfg.Args,
		Transport:    cfg.Transport,
		Enabled:      cfg.Enabled,
		EnvKeys:      envKeys(cfg.Env),
		Instructions: server.GetInstructions(),
		ServerInfo:   server.GetServerInfo(),
		Tools:        server.GetTools(),
	}

	// README is a best-effort enrichment; never fail the whole request over it.
	resp.Readme = h.resolveReadme(cfg)

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(resp); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// envKeys returns the sorted set of configured environment variable names. Only
// names are surfaced — values may contain secrets and must never leave the server.
func envKeys(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// resolveReadme tries, in order, the npm registry (for npx-style servers) and
// then a GitHub homepage discovered from the registry cache. Returns nil when no
// README can be found.
func (h *Handler) resolveReadme(cfg mcp.ServerConfig) *readmeInfo {
	client := &http.Client{Timeout: 8 * time.Second}

	if pkg := npmPackageFromConfig(cfg); pkg != "" {
		if rm := fetchNpmReadme(client, pkg); rm != nil {
			return rm
		}
	}

	if home := h.homepageFromRegistry(cfg.Name); home != "" {
		if rm := fetchGithubReadme(client, home); rm != nil {
			return rm
		}
	}

	return nil
}

// homepageFromRegistry looks up a server's homepage URL from the cached registry
// entries by name (case-insensitive). Returns "" when unknown.
func (h *Handler) homepageFromRegistry(name string) string {
	if h.regStore == nil {
		return ""
	}
	for _, e := range h.regStore.GetCachedEntries() {
		if strings.EqualFold(e.Name, name) && strings.TrimSpace(e.Homepage) != "" {
			return e.Homepage
		}
	}
	return ""
}

// npmPackageFromConfig extracts the npm package name from a command like
// `npx -y @scope/pkg ...`. Returns "" when the command is not an npm-style
// runner or no package can be identified.
func npmPackageFromConfig(cfg mcp.ServerConfig) string {
	base := strings.ToLower(strings.TrimSuffix(filepath.Base(cfg.Command), ".exe"))
	runners := map[string]bool{"npx": true, "npm": true, "pnpm": true, "bunx": true, "yarn": true}
	if !runners[base] {
		return ""
	}

	for _, arg := range cfg.Args {
		t := strings.TrimSpace(arg)
		if t == "" || strings.HasPrefix(t, "-") {
			continue
		}
		// Skip subcommand keywords used by some runners.
		switch t {
		case "dlx", "exec", "create", "run", "x":
			continue
		}
		// Skip filesystem paths (allowed dirs etc.) — these aren't packages.
		if strings.HasPrefix(t, "/") || strings.HasPrefix(t, ".") || strings.HasPrefix(t, "~") {
			continue
		}
		return stripNpmVersion(t)
	}
	return ""
}

// stripNpmVersion drops a trailing "@version" specifier, preserving the leading
// "@" of scoped package names (e.g. "@scope/pkg@1.2.3" -> "@scope/pkg").
func stripNpmVersion(pkg string) string {
	if strings.HasPrefix(pkg, "@") {
		if i := strings.Index(pkg[1:], "@"); i >= 0 {
			return pkg[:i+1]
		}
		return pkg
	}
	if before, _, found := strings.Cut(pkg, "@"); found {
		return before
	}
	return pkg
}

// npmPackageNamePattern matches a valid (optionally scoped) npm package name.
// Restricting to this set keeps untrusted config from injecting anything other
// than a registry path segment into the README fetch URL.
var npmPackageNamePattern = regexp.MustCompile(`^(@[a-z0-9][a-z0-9._-]*/)?[a-z0-9][a-z0-9._-]*$`)

// fetchNpmReadme retrieves the README markdown for an npm package from the public
// registry. Returns nil on any error or when no README is published.
func fetchNpmReadme(client *http.Client, pkg string) *readmeInfo {
	if !npmPackageNamePattern.MatchString(pkg) {
		return nil
	}

	escaped := pkg
	if strings.HasPrefix(pkg, "@") {
		// Scoped packages must have their slash percent-encoded.
		escaped = strings.Replace(pkg, "/", "%2f", 1)
	}

	// #nosec G704 -- the host is a constant literal; only the URL path comes from
	// pkg, which is validated above against a strict npm-name allowlist (no host,
	// scheme, or traversal characters can be injected).
	resp, err := client.Get("https://registry.npmjs.org/" + escaped)
	if err != nil {
		logger.Debug("npm README fetch failed", logger.Fields{"package": pkg, "error": err})
		return nil
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil
	}

	var doc struct {
		Readme   string `json:"readme"`
		Homepage string `json:"homepage"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4*maxReadmeBytes)).Decode(&doc); err != nil {
		return nil
	}

	md := strings.TrimSpace(doc.Readme)
	if md == "" || strings.EqualFold(md, "ERROR: No README data found!") {
		return nil
	}

	sourceURL := strings.TrimSpace(doc.Homepage)
	if sourceURL == "" {
		sourceURL = "https://www.npmjs.com/package/" + pkg
	}

	return &readmeInfo{
		Markdown:  capString(md, maxReadmeBytes),
		Source:    "npm",
		SourceURL: sourceURL,
	}
}

// fetchGithubReadme retrieves the raw README for a GitHub repository identified
// by a homepage URL. Returns nil when the URL isn't a GitHub repo or the fetch
// fails.
func fetchGithubReadme(client *http.Client, homepage string) *readmeInfo {
	owner, repo := parseGitHubRepo(homepage)
	if owner == "" || repo == "" {
		return nil
	}

	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/readme", owner, repo)
	req, err := http.NewRequest(http.MethodGet, apiURL, nil)
	if err != nil {
		return nil
	}
	// Ask for the raw README contents regardless of filename/branch.
	req.Header.Set("Accept", "application/vnd.github.raw")

	resp, err := client.Do(req)
	if err != nil {
		logger.Debug("GitHub README fetch failed", logger.Fields{"repo": owner + "/" + repo, "error": err})
		return nil
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxReadmeBytes))
	if err != nil {
		return nil
	}
	md := strings.TrimSpace(string(body))
	if md == "" {
		return nil
	}

	return &readmeInfo{
		Markdown:  md,
		Source:    "github",
		SourceURL: homepage,
	}
}

// parseGitHubRepo extracts owner/repo from a github.com URL. Returns empty
// strings when the URL is not a recognizable GitHub repository.
func parseGitHubRepo(raw string) (owner, repo string) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", ""
	}
	host := strings.ToLower(u.Host)
	if host != "github.com" && host != "www.github.com" {
		return "", ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", ""
	}
	return parts[0], strings.TrimSuffix(parts[1], ".git")
}

// capString truncates s to at most n bytes, appending a notice when trimmed.
func capString(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n\n_…(truncated)_"
}

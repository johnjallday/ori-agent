package plugin

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/johnjallday/ori-agent/internal/mcp"
)

// claudePluginRootVar is Claude's plugin-root placeholder, expanded to the
// plugin's install directory at registration time.
const claudePluginRootVar = "${CLAUDE_PLUGIN_ROOT}"

// NamespacedServerName returns the plugin-scoped MCP server name used in Ori's
// registry so a plugin's server cannot collide with a user's global server
// (PRD reqs #11/#14). Format: "<plugin>/<server>".
func NamespacedServerName(plugin, server string) string {
	return plugin + "/" + server
}

// ToServerConfig maps a plugin MCP server spec to a runtime mcp.ServerConfig
// with a namespaced name and an absolute command. It is left disabled; the
// trust gate / per-workspace binding (task 3.x) enables it.
func ToServerConfig(pluginName string, spec MCPServerSpec, installDir string) mcp.ServerConfig {
	cmd, args := resolveCommand(spec, installDir)
	return mcp.ServerConfig{
		Name:      NamespacedServerName(pluginName, spec.Name),
		Command:   cmd,
		Args:      args,
		Env:       spec.Env,
		Transport: "stdio",
		Enabled:   false,
	}
}

// resolveCommand returns the absolute command + args for an MCP server spec.
// It expands Claude's ${CLAUDE_PLUGIN_ROOT} and resolves Codex's relative
// command (plus its cwd) against installDir, because mcp.ServerConfig has no
// cwd field. Bare PATH commands (e.g. "npx", "uvx") are left untouched.
func resolveCommand(spec MCPServerSpec, installDir string) (string, []string) {
	// Track whether the command used ${CLAUDE_PLUGIN_ROOT}: after expansion such
	// a command is already rooted at installDir, so it must NOT be re-joined to
	// installDir below — doing so doubles the path (e.g.
	// "plugins/src/x/plugins/src/x/bin/...") whenever installDir is relative and
	// thus slips past the IsAbs guard.
	usedPluginRoot := strings.Contains(spec.Command, claudePluginRootVar)

	cmd := strings.ReplaceAll(spec.Command, claudePluginRootVar, installDir)

	args := make([]string, len(spec.Args))
	for i, a := range spec.Args {
		args[i] = strings.ReplaceAll(a, claudePluginRootVar, installDir)
	}

	// Only a *bare* relative command (e.g. Codex's relative "command" resolved
	// against its "cwd") needs joining to installDir. A ${CLAUDE_PLUGIN_ROOT}
	// command is already rooted by the expansion above.
	if !usedPluginRoot && !filepath.IsAbs(cmd) && !looksLikePATHCommand(cmd) {
		base := installDir
		if strings.TrimSpace(spec.Cwd) != "" {
			base = filepath.Join(installDir, spec.Cwd)
		}
		cmd = filepath.Join(base, cmd)
	}

	// Anchor a relative path command to an absolute path so it does not depend on
	// the MCP server process's working directory. PATH commands (npx, uvx) and
	// already-absolute commands are left untouched.
	if !looksLikePATHCommand(cmd) && !filepath.IsAbs(cmd) {
		if abs, err := filepath.Abs(cmd); err == nil {
			cmd = abs
		}
	}

	return cmd, args
}

// looksLikePATHCommand reports whether cmd is a bare command name to be found on
// PATH rather than a path relative to (or inside) the plugin directory.
func looksLikePATHCommand(cmd string) bool {
	return !strings.ContainsAny(cmd, `/\`) && !strings.HasPrefix(cmd, ".")
}

// CommandAvailable reports whether an MCP server's command is present and
// runnable on this host (closes the binary-delivery gap — a clear status beats
// a cryptic runtime "connection refused"). PATH commands are looked up; path
// commands are stat'd and checked for an executable bit.
func CommandAvailable(cmd string) bool {
	if strings.TrimSpace(cmd) == "" {
		return false
	}
	if looksLikePATHCommand(cmd) {
		_, err := exec.LookPath(cmd)
		return err == nil
	}
	info, err := os.Stat(cmd)
	if err != nil || info.IsDir() {
		return false
	}
	return info.Mode()&0o111 != 0
}

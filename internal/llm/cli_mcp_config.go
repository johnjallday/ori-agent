package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// CLIAgentPosture is the resolved autonomy posture applied to native-MCP CLI
// runs. Defaults match PRD req #15: a workspace-scoped sandbox with full access
// inside it and no approval prompts. (Task 5.4 centralizes policy resolution;
// these are the defaults baked into the generated codex profile.)
type CLIAgentPosture struct {
	// CodexSandbox is the codex `sandbox_mode` (e.g. "workspace-write").
	CodexSandbox string
	// CodexApproval is the codex `approval_policy` (e.g. "never").
	CodexApproval string
}

// DefaultCLIAgentPosture returns the decided posture: workspace-scoped sandbox,
// auto-approved.
func DefaultCLIAgentPosture() CLIAgentPosture {
	return CLIAgentPosture{CodexSandbox: "workspace-write", CodexApproval: "never"}
}

// CLIMCPConfigStore writes and locates persistent per-workspace MCP config for
// the CLI providers: a Claude `--mcp-config` JSON file and a Codex profile TOML
// loaded via `--profile` from CODEX_HOME. Files are keyed by workspace ID and
// rewritten only when their content changes (idempotent), so they persist
// across runs and regenerate only when the workspace's bindings change.
type CLIMCPConfigStore struct {
	claudeDir string // base dir for "<ws>.mcp.json"
	codexHome string // CODEX_HOME for "<profile>.config.toml"
}

// NewCLIMCPConfigStore returns a store rooted at the data dir (claude config)
// and CODEX_HOME (codex profiles).
func NewCLIMCPConfigStore() *CLIMCPConfigStore {
	return &CLIMCPConfigStore{
		claudeDir: defaultCLIMCPDir(),
		codexHome: codexHomeDir(),
	}
}

// newCLIMCPConfigStoreAt builds a store at explicit dirs (tests).
func newCLIMCPConfigStoreAt(claudeDir, codexHome string) *CLIMCPConfigStore {
	return &CLIMCPConfigStore{claudeDir: claudeDir, codexHome: codexHome}
}

// defaultCLIMCPDir resolves the per-workspace Claude MCP-config directory,
// mirroring the repo's ORI_DATA_DIR-scoped subdir pattern.
func defaultCLIMCPDir() string {
	if dir := strings.TrimSpace(os.Getenv("ORI_DATA_DIR")); dir != "" {
		if abs, err := filepath.Abs(dir); err == nil {
			return filepath.Join(abs, "cli-mcp")
		}
		return filepath.Join(dir, "cli-mcp")
	}
	if cwd, err := os.Getwd(); err == nil {
		if abs, absErr := filepath.Abs(cwd); absErr == nil {
			return filepath.Join(abs, "cli-mcp")
		}
		return filepath.Join(cwd, "cli-mcp")
	}
	return filepath.Join(".", "cli-mcp")
}

// ClaudeConfigPath returns the per-workspace Claude `--mcp-config` file path.
func (s *CLIMCPConfigStore) ClaudeConfigPath(workspaceID string) string {
	return filepath.Join(s.claudeDir, cliSafeName(workspaceID)+".mcp.json")
}

// CodexProfileName returns the per-workspace codex `--profile` name.
func (s *CLIMCPConfigStore) CodexProfileName(workspaceID string) string {
	return "ori-ws-" + cliSafeName(workspaceID)
}

// CodexProfilePath returns the per-workspace codex profile file path under
// CODEX_HOME (where `--profile <name>` loads "<name>.config.toml").
func (s *CLIMCPConfigStore) CodexProfilePath(workspaceID string) string {
	return filepath.Join(s.codexHome, s.CodexProfileName(workspaceID)+".config.toml")
}

// EnsureClaudeConfig writes the per-workspace Claude MCP config when its content
// changed and returns the file path. With no specs it removes any stale file and
// returns "".
func (s *CLIMCPConfigStore) EnsureClaudeConfig(workspaceID string, specs []MCPServerSpec) (string, error) {
	path := s.ClaudeConfigPath(workspaceID)
	if len(specs) == 0 {
		_ = os.Remove(path)
		return "", nil
	}
	content, err := buildClaudeMCPConfigJSON(specs)
	if err != nil {
		return "", err
	}
	if _, err := writeIfChanged(path, content); err != nil {
		return "", err
	}
	return path, nil
}

// EnsureCodexProfile writes the per-workspace codex profile when its content
// changed and returns the profile name (for `--profile`). With no specs it
// removes any stale profile and returns "".
func (s *CLIMCPConfigStore) EnsureCodexProfile(workspaceID string, specs []MCPServerSpec, posture CLIAgentPosture) (string, error) {
	path := s.CodexProfilePath(workspaceID)
	if len(specs) == 0 {
		_ = os.Remove(path)
		return "", nil
	}
	content, err := buildCodexProfileTOML(specs, posture)
	if err != nil {
		return "", err
	}
	if _, err := writeIfChanged(path, content); err != nil {
		return "", err
	}
	return s.CodexProfileName(workspaceID), nil
}

// Remove deletes both per-workspace configs (best effort), e.g. on workspace
// deletion.
func (s *CLIMCPConfigStore) Remove(workspaceID string) error {
	var errs []string
	for _, path := range []string{s.ClaudeConfigPath(workspaceID), s.CodexProfilePath(workspaceID)} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("remove cli mcp config: %s", strings.Join(errs, "; "))
	}
	return nil
}

// claudeMCPConfig is the `--mcp-config` JSON shape.
type claudeMCPConfig struct {
	MCPServers map[string]claudeMCPServer `json:"mcpServers"`
}

type claudeMCPServer struct {
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

func buildClaudeMCPConfigJSON(specs []MCPServerSpec) ([]byte, error) {
	cfg := claudeMCPConfig{MCPServers: make(map[string]claudeMCPServer)}
	for _, sp := range dedupeSpecsByName(specs) {
		cfg.MCPServers[sp.Name] = claudeMCPServer{
			Command: sp.Command,
			Args:    sp.Args,
			Env:     sp.Env,
		}
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal claude mcp config: %w", err)
	}
	return append(b, '\n'), nil
}

// codexProfile is the per-workspace codex profile. Scalar fields precede the
// map so the TOML encoder emits them before the [mcp_servers.*] tables (TOML
// requires top-level keys before tables).
type codexProfile struct {
	SandboxMode    string                    `toml:"sandbox_mode,omitempty"`
	ApprovalPolicy string                    `toml:"approval_policy,omitempty"`
	MCPServers     map[string]codexMCPServer `toml:"mcp_servers"`
}

type codexMCPServer struct {
	Command string            `toml:"command"`
	Args    []string          `toml:"args"`
	Env     map[string]string `toml:"env,omitempty"`
}

func buildCodexProfileTOML(specs []MCPServerSpec, posture CLIAgentPosture) ([]byte, error) {
	prof := codexProfile{
		SandboxMode:    posture.CodexSandbox,
		ApprovalPolicy: posture.CodexApproval,
		MCPServers:     make(map[string]codexMCPServer),
	}
	for _, sp := range dedupeSpecsByName(specs) {
		prof.MCPServers[sp.Name] = codexMCPServer{
			Command: sp.Command,
			Args:    append([]string{}, sp.Args...), // non-nil so it emits args = []
			Env:     sp.Env,
		}
	}
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(prof); err != nil {
		return nil, fmt.Errorf("encode codex profile: %w", err)
	}
	return buf.Bytes(), nil
}

// dedupeSpecsByName keeps the first spec per name and sorts by name so generated
// config is deterministic (stable bytes => idempotent write-if-changed).
func dedupeSpecsByName(specs []MCPServerSpec) []MCPServerSpec {
	seen := make(map[string]bool, len(specs))
	out := make([]MCPServerSpec, 0, len(specs))
	for _, sp := range specs {
		name := strings.TrimSpace(sp.Name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, sp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// writeIfChanged writes content only when it differs from what is on disk,
// reporting whether a write happened. This keeps the persistent config stable
// across runs and only regenerates when bindings change.
func writeIfChanged(path string, content []byte) (changed bool, err error) {
	// #nosec G304 -- path derives from a sanitized workspace ID under an
	// app-owned config directory, not from user-controlled input.
	if existing, rerr := os.ReadFile(path); rerr == nil && bytes.Equal(existing, content) {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return false, err
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		return false, err
	}
	return true, nil
}

// cliSafeName maps an identifier to a filesystem/CLI-safe token by replacing any
// char outside [A-Za-z0-9_-] with "_".
func cliSafeName(id string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(id) {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	if b.Len() == 0 {
		return "default"
	}
	return b.String()
}

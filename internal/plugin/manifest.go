package plugin

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	claudeManifestDir = ".claude-plugin"
	codexManifestDir  = ".codex-plugin"
	manifestFile      = "plugin.json"
)

// rawManifest mirrors the on-disk plugin.json. Fields that vary by format
// (author may be a string or an object; mcpServers may be a path string or an
// inline map) are captured as json.RawMessage and decoded separately.
type rawManifest struct {
	Name        string          `json:"name"`
	Version     string          `json:"version"`
	Description string          `json:"description"`
	Author      json.RawMessage `json:"author"`
	Homepage    string          `json:"homepage"`
	Repository  string          `json:"repository"`
	Keywords    []string        `json:"keywords"`
	MCPServers  json.RawMessage `json:"mcpServers"`
	Skills      json.RawMessage `json:"skills"`
	Commands    json.RawMessage `json:"commands"`
	Agents      json.RawMessage `json:"agents"`
	Hooks       json.RawMessage `json:"hooks"`
	Interface   *rawInterface   `json:"interface"`
}

type rawInterface struct {
	DisplayName      string   `json:"displayName"`
	ShortDescription string   `json:"shortDescription"`
	LongDescription  string   `json:"longDescription"`
	Category         string   `json:"category"`
	Logo             string   `json:"logo"`
	DefaultPrompt    []string `json:"defaultPrompt"`
}

// locatedManifest is a parsed manifest with its format and the plugin root
// directory (the directory that contains the manifest dir and component dirs).
type locatedManifest struct {
	format SourceFormat
	root   string
	raw    rawManifest
}

// DetectManifest locates and parses a plugin manifest under root. When both a
// Claude and a Codex manifest are present, Claude wins by default; pass prefer
// to override. Codex's versioned layout (<root>/<version>/.codex-plugin/) is
// handled transparently.
func DetectManifest(root string, prefer SourceFormat) (locatedManifest, error) {
	claudePath := filepath.Join(root, claudeManifestDir, manifestFile)
	codexRoot, codexPath := findCodexManifest(root)

	hasClaude := fileExists(claudePath)
	hasCodex := codexPath != ""

	switch {
	case hasClaude && hasCodex:
		if prefer == FormatCodex {
			return loadManifest(FormatCodex, codexRoot, codexPath)
		}
		return loadManifest(FormatClaude, root, claudePath) // default precedence: Claude > Codex
	case hasClaude:
		return loadManifest(FormatClaude, root, claudePath)
	case hasCodex:
		return loadManifest(FormatCodex, codexRoot, codexPath)
	default:
		return locatedManifest{}, ErrNoManifest
	}
}

// findCodexManifest looks for .codex-plugin/plugin.json at root, then under one
// level of version subdirectories (Codex's installed layout), choosing the
// highest-sorted version.
func findCodexManifest(root string) (manifestRoot, manifestPath string) {
	direct := filepath.Join(root, codexManifestDir, manifestFile)
	if fileExists(direct) {
		return root, direct
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", ""
	}
	var versions []string
	for _, e := range entries {
		if e.IsDir() && fileExists(filepath.Join(root, e.Name(), codexManifestDir, manifestFile)) {
			versions = append(versions, e.Name())
		}
	}
	if len(versions) == 0 {
		return "", ""
	}
	sort.Strings(versions) // lexical; good enough for v1 version selection
	latest := versions[len(versions)-1]
	vroot := filepath.Join(root, latest)
	return vroot, filepath.Join(vroot, codexManifestDir, manifestFile)
}

func loadManifest(format SourceFormat, root, path string) (locatedManifest, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- path derived from a user-provided plugin directory
	if err != nil {
		return locatedManifest{}, fmt.Errorf("plugin: read manifest %s: %w", path, err)
	}
	var raw rawManifest
	if err := json.Unmarshal(data, &raw); err != nil {
		return locatedManifest{}, fmt.Errorf("plugin: parse manifest %s: %w", path, err)
	}
	if strings.TrimSpace(raw.Name) == "" {
		return locatedManifest{}, ErrNoName
	}
	return locatedManifest{format: format, root: root, raw: raw}, nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// authorName extracts a display name from the author field, which may be a bare
// string or an object with a "name" key.
func authorName(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var obj struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil {
		return obj.Name
	}
	return ""
}

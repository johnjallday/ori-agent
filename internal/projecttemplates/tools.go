package projecttemplates

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ToolDefaults declares the tools a template pre-binds onto a workspace created
// from it. Values are names only — this package never resolves what a tool does;
// apply-if-present binding happens in the workspace-creation layer, keeping the
// file-copy engine domain-blind.
type ToolDefaults struct {
	Skills     []string `json:"skills,omitempty"`
	MCPServers []string `json:"mcp_servers,omitempty"`
	Plugins    []string `json:"plugins,omitempty"`
	// PluginSources maps a declared plugin name to the exact source it installs
	// from (a git URL or local path the plugin installer accepts). It lets the
	// install UI offer a one-click, trust-previewed install without resolving a
	// marketplace or asking the user to paste a source. Optional; a plugin
	// without a declared source falls back to marketplace resolution / paste.
	// Keys are matched case-insensitively against Plugins.
	PluginSources map[string]string `json:"plugin_sources,omitempty"`
}

// IsEmpty reports whether no tools are declared. Plugin sources alone (with no
// plugins) are not meaningful and do not count.
func (t ToolDefaults) IsEmpty() bool {
	return len(t.Skills) == 0 && len(t.MCPServers) == 0 && len(t.Plugins) == 0
}

// PluginSource returns the declared install source for a plugin name
// (case-insensitive), or "" when none is declared.
func (t ToolDefaults) PluginSource(name string) string {
	if len(t.PluginSources) == 0 {
		return ""
	}
	want := strings.ToLower(strings.TrimSpace(name))
	for k, v := range t.PluginSources {
		if strings.ToLower(strings.TrimSpace(k)) == want {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func normalizeToolDefaults(t ToolDefaults) ToolDefaults {
	out := ToolDefaults{
		Skills:     normalizeNameList(t.Skills),
		MCPServers: normalizeNameList(t.MCPServers),
		Plugins:    normalizeNameList(t.Plugins),
	}
	if len(t.PluginSources) > 0 {
		out.PluginSources = make(map[string]string, len(t.PluginSources))
		for k, v := range t.PluginSources {
			k = strings.TrimSpace(k)
			v = strings.TrimSpace(v)
			if k != "" && v != "" {
				out.PluginSources[k] = v
			}
		}
		if len(out.PluginSources) == 0 {
			out.PluginSources = nil
		}
	}
	return out
}

// normalizeNameList trims, drops blanks, de-duplicates case-insensitively
// (keeping first-seen casing), and sorts for stable output.
func normalizeNameList(names []string) []string {
	seen := make(map[string]struct{}, len(names))
	out := make([]string, 0, len(names))
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		key := strings.ToLower(n)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool {
		li, lj := strings.ToLower(out[i]), strings.ToLower(out[j])
		if li == lj {
			return out[i] < out[j]
		}
		return li < lj
	})
	return out
}

// SetTools writes (or clears) the `tools` block in a template's template.json,
// preserving every other key. An all-empty ToolDefaults clears the key. Like the
// onboarding writer, this stores names only and never interprets them.
func SetTools(libDir, id string, tools ToolDefaults) (Template, error) {
	tpl, err := FindLibraryTemplate(libDir, id)
	if err != nil {
		return Template{}, err
	}

	// manifestPath is the resolved library template's folder (FindLibraryTemplate
	// rejects ids outside the library) joined with the fixed ManifestFileName.
	manifestPath := filepath.Join(tpl.Path, ManifestFileName)
	raw := map[string]any{}
	if data, err := os.ReadFile(manifestPath); err == nil { // #nosec G304 -- manifestPath is libDir/<validated id>/template.json, not user-controlled
		_ = json.Unmarshal(data, &raw)
	}

	tools = normalizeToolDefaults(tools)
	if tools.IsEmpty() {
		delete(raw, "tools")
	} else {
		encoded, err := json.Marshal(tools)
		if err != nil {
			return Template{}, fmt.Errorf("failed to encode tools: %w", err)
		}
		var v any
		_ = json.Unmarshal(encoded, &v)
		raw["tools"] = v
	}

	data, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return Template{}, fmt.Errorf("failed to encode manifest: %w", err)
	}
	if err := os.WriteFile(manifestPath, append(data, '\n'), 0o640); err != nil { // #nosec G304 G306 -- manifestPath is libDir/<validated id>/template.json; 0o640 matches the package's manifest-write convention
		return Template{}, fmt.Errorf("failed to write manifest: %w", err)
	}
	return newTemplate(tpl.Path), nil
}

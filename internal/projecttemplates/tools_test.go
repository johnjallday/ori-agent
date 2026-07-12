package projecttemplates

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeNameList(t *testing.T) {
	got := normalizeNameList([]string{" Skill-B ", "skill-a", "Skill-A", "", "skill-b"})
	want := []string{"skill-a", "Skill-B"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("normalizeNameList = %v, want %v (trim/dedupe-ci/sort)", got, want)
	}
}

func TestSetTools(t *testing.T) {
	libDir := filepath.Join(t.TempDir(), "templates")
	writeFile(t, filepath.Join(libDir, "tpl", ManifestFileName),
		`{"name":"Tpl","tags":["music"],"custom_field":42}`)

	tpl, err := SetTools(libDir, "tpl", ToolDefaults{
		Skills:     []string{"summarize", "summarize"}, // dup collapses
		MCPServers: []string{"reaper"},
		Plugins:    []string{"  "}, // blank dropped
	})
	if err != nil {
		t.Fatalf("SetTools: %v", err)
	}
	if len(tpl.Tools.Skills) != 1 || tpl.Tools.Skills[0] != "summarize" {
		t.Fatalf("unexpected skills: %v", tpl.Tools.Skills)
	}
	if len(tpl.Tools.MCPServers) != 1 || tpl.Tools.MCPServers[0] != "reaper" {
		t.Fatalf("unexpected mcp_servers: %v", tpl.Tools.MCPServers)
	}
	if len(tpl.Tools.Plugins) != 0 {
		t.Fatalf("blank plugin should be dropped: %v", tpl.Tools.Plugins)
	}

	// Block written; every other key survives.
	data, err := os.ReadFile(filepath.Join(libDir, "tpl", ManifestFileName))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"tools"`, `"mcp_servers"`, `"custom_field"`, `"tags"`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("manifest missing %s: %s", want, data)
		}
	}

	// Re-reading the template round-trips the tools.
	reread, err := FindLibraryTemplate(libDir, "tpl")
	if err != nil {
		t.Fatal(err)
	}
	if reread.Tools.IsEmpty() {
		t.Fatalf("re-read template lost its tools")
	}

	// An all-empty ToolDefaults clears the key; unknown keys remain.
	if _, err := SetTools(libDir, "tpl", ToolDefaults{}); err != nil {
		t.Fatalf("SetTools(clear): %v", err)
	}
	data, _ = os.ReadFile(filepath.Join(libDir, "tpl", ManifestFileName))
	if strings.Contains(string(data), `"tools"`) {
		t.Errorf("tools key should be removed: %s", data)
	}
	if !strings.Contains(string(data), `"custom_field"`) {
		t.Errorf("unknown key dropped on clear: %s", data)
	}

	// Unknown template id is rejected.
	if _, err := SetTools(libDir, "missing", ToolDefaults{Skills: []string{"x"}}); !errors.Is(err, ErrTemplateNotFound) {
		t.Errorf("unknown template = %v, want ErrTemplateNotFound", err)
	}
}

func TestToolDefaults_PluginSourceAndNormalize(t *testing.T) {
	td := normalizeToolDefaults(ToolDefaults{
		Plugins:       []string{"reaper-plugin"},
		PluginSources: map[string]string{"  Reaper-Plugin ": "  https://example.com/x.git ", "blank": "  "},
	})
	// Case-insensitive lookup, trimmed value.
	if got := td.PluginSource("reaper-plugin"); got != "https://example.com/x.git" {
		t.Fatalf("PluginSource = %q", got)
	}
	// Blank-valued entries are dropped by normalize.
	if _, ok := td.PluginSources["blank"]; ok {
		t.Fatalf("blank-valued source should be dropped: %+v", td.PluginSources)
	}
	// Undeclared plugin returns empty.
	if got := td.PluginSource("other"); got != "" {
		t.Fatalf("undeclared plugin source = %q, want empty", got)
	}
	// Sources-only (no plugins) is still IsEmpty.
	if !(ToolDefaults{PluginSources: map[string]string{"a": "b"}}).IsEmpty() {
		t.Fatalf("plugin_sources without plugins must be IsEmpty")
	}
}

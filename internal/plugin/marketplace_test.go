package plugin

import (
	"path/filepath"
	"testing"
)

func TestParseMarketplace(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "marketplace.json"),
		`{"name":"acme","plugins":[{"name":"reaper","source":"./reaper","description":"d"}]}`)
	mp, err := ParseMarketplace(dir)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if mp.Name != "acme" {
		t.Errorf("name = %q", mp.Name)
	}
	if len(mp.Plugins) != 1 || mp.Plugins[0].Name != "reaper" {
		t.Fatalf("plugins = %+v", mp.Plugins)
	}
	if mp.Dir != dir {
		t.Errorf("dir = %q, want %q", mp.Dir, dir)
	}
}

func TestParseMarketplaceClaudeDir(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".claude-plugin", "marketplace.json"), `{"name":"acme","plugins":[]}`)
	mp, err := ParseMarketplace(dir)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if mp.Name != "acme" {
		t.Errorf("name = %q", mp.Name)
	}
}

func TestMarketplaceAddAndInstall(t *testing.T) {
	// A catalog dir with a marketplace.json + a bundled Claude plugin under ./reaper.
	catalog := t.TempDir()
	writeFile(t, filepath.Join(catalog, "marketplace.json"),
		`{"name":"acme","plugins":[{"name":"reaper","source":"./reaper"}]}`)
	writeFile(t, filepath.Join(catalog, "reaper", ".claude-plugin", "plugin.json"), `{"name":"reaper","version":"0.1.0"}`)
	writeFile(t, filepath.Join(catalog, "reaper", ".mcp.json"), `{"ori-reaper":{"command":"/usr/bin/true"}}`)
	writeFile(t, filepath.Join(catalog, "reaper", "skills", "s1", "SKILL.md"), "---\nname: s1\n---\n")

	reg := &fakeRegistrar{}
	sk := &fakeSkills{}
	m := NewManager(reg, sk, t.TempDir(), "")

	mp, err := m.AddMarketplace(catalog)
	if err != nil {
		t.Fatalf("add marketplace: %v", err)
	}
	if mp.Name != "acme" || len(mp.Plugins) != 1 {
		t.Fatalf("marketplace = %+v", mp)
	}

	list, err := m.Marketplaces()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Name != "acme" {
		t.Fatalf("marketplaces = %+v", list)
	}

	p, err := m.InstallFromMarketplace("acme", "reaper", "", func(TrustReport) bool { return true })
	if err != nil {
		t.Fatalf("install from marketplace: %v", err)
	}
	if p.Name != "reaper" {
		t.Errorf("installed = %q", p.Name)
	}
	if _, ok := reg.added["reaper/ori-reaper"]; !ok {
		t.Errorf("server not registered: %v", reg.added)
	}

	if _, err := m.InstallFromMarketplace("acme", "nope", "", nil); err == nil {
		t.Error("expected error for missing marketplace entry")
	}
}

func TestParseMarketplaceObjectSource(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "marketplace.json"), `{
	  "name": "real",
	  "plugins": [
	    {"name": "local-one", "description": "d", "source": {"source": "local", "path": "./plugins/local-one"}},
	    {"name": "gh-one", "source": {"source": "github", "repo": "acme/gh-one"}}
	  ]
	}`)
	mp, err := ParseMarketplace(dir)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(mp.Plugins) != 2 {
		t.Fatalf("plugins = %+v", mp.Plugins)
	}
	byName := map[string]string{}
	for _, p := range mp.Plugins {
		byName[p.Name] = p.Source
	}
	if byName["local-one"] != "./plugins/local-one" {
		t.Errorf("local object source = %q, want ./plugins/local-one", byName["local-one"])
	}
	if byName["gh-one"] != "https://github.com/acme/gh-one.git" {
		t.Errorf("github object source = %q", byName["gh-one"])
	}
}

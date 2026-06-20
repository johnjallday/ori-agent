package plugin

import (
	"fmt"
	"os/exec"
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

func TestParseMarketplaceMetadata(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "marketplace.json"), `{
	  "name": "official",
	  "plugins": [
	    {
	      "name": "rich",
	      "description": "d",
	      "category": "security",
	      "tags": ["a", "b"],
	      "keywords": ["k1"],
	      "homepage": "https://example.com",
	      "author": {"name": "Acme", "email": "x@acme.io"},
	      "source": "./plugins/rich"
	    },
	    {"name": "legacy", "source": "./plugins/legacy"},
	    {"name": "str-author", "author": "Just A Name", "source": "./plugins/sa"}
	  ]
	}`)
	mp, err := ParseMarketplace(dir)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	byName := map[string]MarketplaceEntry{}
	for _, p := range mp.Plugins {
		byName[p.Name] = p
	}

	rich := byName["rich"]
	if rich.Category != "security" {
		t.Errorf("category = %q", rich.Category)
	}
	if len(rich.Tags) != 2 || rich.Tags[0] != "a" {
		t.Errorf("tags = %v", rich.Tags)
	}
	if len(rich.Keywords) != 1 || rich.Keywords[0] != "k1" {
		t.Errorf("keywords = %v", rich.Keywords)
	}
	if rich.Homepage != "https://example.com" {
		t.Errorf("homepage = %q", rich.Homepage)
	}
	if rich.Author.Name != "Acme" || rich.Author.Email != "x@acme.io" {
		t.Errorf("author = %+v", rich.Author)
	}

	// Legacy minimal entry still parses with zero-value metadata.
	legacy := byName["legacy"]
	if legacy.Source != "./plugins/legacy" || legacy.Category != "" || len(legacy.Tags) != 0 {
		t.Errorf("legacy = %+v", legacy)
	}

	// Bare-string author normalizes to {Name}.
	if byName["str-author"].Author.Name != "Just A Name" {
		t.Errorf("string author = %+v", byName["str-author"].Author)
	}
}

func TestNormalizeGitSubdirEntry(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "marketplace.json"), `{
	  "name": "official",
	  "plugins": [
	    {"name": "sub", "source": {"source": "git-subdir", "url": "https://github.com/x/y.git", "path": "plugins/sub", "ref": "v1.5.5", "sha": "deadbeef"}},
	    {"name": "pinned-repo", "source": {"source": "url", "url": "https://github.com/x/z.git", "sha": "cafe"}}
	  ]
	}`)
	mp, err := ParseMarketplace(dir)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	byName := map[string]string{}
	for _, p := range mp.Plugins {
		byName[p.Name] = p.Source
	}

	g, ok := parseGitSubdir(byName["sub"])
	if !ok {
		t.Fatalf("sub source not a git-subdir: %q", byName["sub"])
	}
	if g.URL != "https://github.com/x/y.git" || g.Subdir != "plugins/sub" || g.Ref != "v1.5.5" || g.Sha != "deadbeef" {
		t.Errorf("git-subdir = %+v", g)
	}

	g2, ok := parseGitSubdir(byName["pinned-repo"])
	if !ok {
		t.Fatalf("pinned-repo not parsed: %q", byName["pinned-repo"])
	}
	if g2.URL != "https://github.com/x/z.git" || g2.Subdir != "" || g2.Sha != "cafe" {
		t.Errorf("pinned-repo = %+v", g2)
	}
}

func TestMarketplaceInstallGitSubdir(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo, sha := gitInitPluginRepo(t)

	catalog := t.TempDir()
	entry := fmt.Sprintf(`{"name":"cat","plugins":[{"name":"demo","source":{"source":"git-subdir","url":%q,"path":"plugins/demo","sha":%q}}]}`, repo, sha)
	writeFile(t, filepath.Join(catalog, "marketplace.json"), entry)

	reg := &fakeRegistrar{}
	sk := &fakeSkills{}
	m := NewManager(reg, sk, t.TempDir(), t.TempDir())

	if _, err := m.AddMarketplace(catalog); err != nil {
		t.Fatalf("add marketplace: %v", err)
	}

	// Preview should disclose the MCP server without registering it.
	if _, err := m.PreviewFromMarketplace("cat", "demo", ""); err != nil {
		t.Fatalf("preview: %v", err)
	}
	if len(reg.added) != 0 {
		t.Errorf("preview should not register: %v", reg.added)
	}

	p, err := m.InstallFromMarketplace("cat", "demo", "", func(TrustReport) bool { return true })
	if err != nil {
		t.Fatalf("install from marketplace: %v", err)
	}
	if p.Name != "demo" {
		t.Errorf("installed = %q", p.Name)
	}
	if _, ok := reg.added["demo/demo-mcp"]; !ok {
		t.Errorf("server not registered: %v", reg.added)
	}
}

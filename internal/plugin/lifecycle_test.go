package plugin

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/mcp"
)

func makeClaudeBundle(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".claude-plugin", "plugin.json"), `{"name":"reaper","version":"0.1.0"}`)
	writeFile(t, filepath.Join(root, ".mcp.json"), `{"ori-reaper":{"command":"/usr/bin/true"}}`)
	writeFile(t, filepath.Join(root, "skills", "reaper-session-setup", "SKILL.md"), "---\nname: x\n---\n")
	return root
}

func commitGitAll(t *testing.T, repo, message string) {
	t.Helper()
	cmd := exec.Command("git", "add", "-A")
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v: %s", err, out)
	}
	cmd = exec.Command("git", "commit", "-q", "-m", message)
	cmd.Dir = repo
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v: %s", err, out)
	}
}

func TestFreshPersistencePathsContainOnlyManagedPluginState(t *testing.T) {
	root := t.TempDir()
	pluginsDir := filepath.Join(root, "plugins")
	cloneDir := filepath.Join(pluginsDir, "src")
	manager := NewManager(&fakeRegistrar{}, pluginsDir, cloneDir)
	paths := manager.FreshPersistencePaths()
	want := map[string]string{
		"plugin_registry": filepath.Join(pluginsDir, "installed.json"), "plugin_marketplaces": filepath.Join(pluginsDir, "marketplaces.json"),
		"plugin_clones": cloneDir, "plugin_state": filepath.Join(pluginsDir, "state"), "plugin_artifacts": filepath.Join(pluginsDir, "artifacts"),
		"plugin_preview": filepath.Join(pluginsDir, "preview"),
	}
	for kind, path := range want {
		if paths[kind] != path {
			t.Fatalf("fresh plugin path %s = %q, want %q", kind, paths[kind], path)
		}
	}
	if len(paths) != len(want) {
		t.Fatalf("fresh plugin paths = %#v", paths)
	}
}

func TestStoreRoundTrip(t *testing.T) {
	s := NewStore(t.TempDir())
	if list, _ := s.List(); len(list) != 0 {
		t.Fatalf("new store should be empty, got %d", len(list))
	}
	p := InstalledPlugin{
		Name:       "reaper",
		Format:     FormatClaude,
		MCPServers: []string{"reaper/ori-reaper"},
		Skills:     []string{"reaper-session-setup"},
	}
	if err := s.Put(p); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.Get("reaper")
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if len(got.MCPServers) != 1 {
		t.Errorf("servers = %v", got.MCPServers)
	}
	if err := s.SetEnabled("reaper", true); err != nil {
		t.Fatal(err)
	}
	if got, _, _ = s.Get("reaper"); !got.Enabled {
		t.Error("expected enabled after SetEnabled")
	}
	if err := s.Delete("reaper"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.Get("reaper"); ok {
		t.Error("expected plugin gone after Delete")
	}
}

type recordingContributionLifecycle struct {
	events   []string
	previous InstalledPlugin
	next     InstalledPlugin
}

func (l *recordingContributionLifecycle) RegisterInstalled(plugin InstalledPlugin) error {
	l.events = append(l.events, "register:"+plugin.Name)
	return nil
}
func (l *recordingContributionLifecycle) Replace(previous, next InstalledPlugin) error {
	l.events = append(l.events, "replace:"+previous.Name)
	l.previous, l.next = previous, next
	return nil
}
func (l *recordingContributionLifecycle) Unregister(pluginID string, _ uint64) error {
	l.events = append(l.events, "unregister:"+pluginID)
	return nil
}
func (l *recordingContributionLifecycle) DeleteState(pluginID string) error {
	l.events = append(l.events, "delete-state:"+pluginID)
	return nil
}

func TestManagerSetEnabledReplacesContributionGenerationBeforeCommit(t *testing.T) {
	m := NewManager(&fakeRegistrar{}, t.TempDir(), "")
	installed := InstalledPlugin{Name: "surface-tools", Enabled: false, Generation: 4}
	if err := m.store.Put(installed); err != nil {
		t.Fatal(err)
	}
	lifecycle := &recordingContributionLifecycle{}
	m.SetSurfaceLifecycle(lifecycle)
	if err := m.SetEnabled(installed.Name, true); err != nil {
		t.Fatal(err)
	}
	got, ok, err := m.store.Get(installed.Name)
	if err != nil || !ok {
		t.Fatalf("Get() ok=%v err=%v", ok, err)
	}
	if !got.Enabled || got.Generation != 5 || got.ContentGeneration != 4 || got.EvidenceGeneration() != 4 {
		t.Fatalf("enabled plugin = %+v", got)
	}
	if len(lifecycle.events) != 1 || lifecycle.events[0] != "replace:surface-tools" {
		t.Fatalf("lifecycle events = %v", lifecycle.events)
	}
	if lifecycle.previous.Generation != 4 || lifecycle.next.Generation != 5 || !lifecycle.next.Enabled {
		t.Fatalf("replacement = %+v -> %+v", lifecycle.previous, lifecycle.next)
	}
}

// sourceMutatingRegistrar changes a skill in the plugin's source while its
// MCP server is being registered.
type sourceMutatingRegistrar struct {
	fakeRegistrar
	path string
}

func (registrar *sourceMutatingRegistrar) AddServer(cfg mcp.ServerConfig) error {
	if err := registrar.fakeRegistrar.AddServer(cfg); err != nil {
		return err
	}
	return os.WriteFile(registrar.path, []byte("---\nname: changed\n---\n"), 0o600)
}

func TestManagerInstallRejectsSkillSourceMutationDuringRegistration(t *testing.T) {
	root := makeClaudeBundle(t)
	skillPath := filepath.Join(root, "skills", "reaper-session-setup", "SKILL.md")
	registrar := &sourceMutatingRegistrar{path: skillPath}
	manager := NewManager(registrar, t.TempDir(), "")
	if _, err := manager.Install(root, "", func(TrustReport) bool { return true }); !errors.Is(err, ErrSourceChanged) {
		t.Fatalf("install mutation error = %v", err)
	}
	if installed, err := manager.List(); err != nil || len(installed) != 0 {
		t.Fatalf("mutated install was recorded: %#v, %v", installed, err)
	}
	if len(registrar.removed) != 1 || registrar.removed[0] != "reaper/ori-reaper" {
		t.Fatalf("mutated install rollback = %#v", registrar.removed)
	}
}

func TestManagerInstallAndUninstall(t *testing.T) {
	root := makeClaudeBundle(t)
	reg := &fakeRegistrar{}
	m := NewManager(reg, t.TempDir(), "")

	confirmed := false
	p, err := m.Install(root, "", func(TrustReport) bool { confirmed = true; return true })
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if !confirmed {
		t.Error("confirm callback not invoked")
	}
	if p.Enabled {
		t.Error("installed plugin should start disabled")
	}
	// The skill is recorded where it lives inside the install folder, and no
	// ownership receipt schema is written any more.
	if p.SkillOwnershipSchema != 0 || p.SkillPaths["reaper-session-setup"] != "skills/reaper-session-setup" {
		t.Fatalf("installed skill record = schema %d paths %v", p.SkillOwnershipSchema, p.SkillPaths)
	}
	if _, err := os.Stat(filepath.Join(root, "skills", "reaper-session-setup", legacySkillReceiptFileName)); !os.IsNotExist(err) {
		t.Fatalf("install wrote a skill receipt: %v", err)
	}
	if _, ok := reg.added["reaper/ori-reaper"]; !ok {
		t.Errorf("server not registered: %v", reg.added)
	}
	if list, _ := m.List(); len(list) != 1 {
		t.Errorf("store should have 1 entry, got %d", len(list))
	}

	lifecycle := &recordingContributionLifecycle{}
	m.SetSurfaceLifecycle(lifecycle)
	if err := m.Uninstall("reaper"); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if len(reg.added) != 0 {
		t.Errorf("server not removed on uninstall: %v", reg.added)
	}
	if list, _ := m.List(); len(list) != 0 {
		t.Errorf("store entry not removed, got %d", len(list))
	}
	if len(lifecycle.events) != 1 || lifecycle.events[0] != "delete-state:reaper" {
		t.Fatalf("uninstall lifecycle events = %v", lifecycle.events)
	}
}

// FR 28: a plugin installed before #527 has no skill receipts anywhere. Update
// and uninstall used to refuse it with a 409; now its skills are simply read
// from its folder, so both work.
func TestAPluginInstalledBeforeSkillReceiptsUpdatesAndUninstalls(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := makeClaudeBundle(t)
	registrar := &fakeRegistrar{}
	manager := NewManager(registrar, t.TempDir(), "")
	installed, err := manager.Install(root, "", func(TrustReport) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	// Rewrite the record the way an old install left it: no skill paths, no
	// ownership schema, and a copy in ~/.agents/skills with no receipt.
	legacy := installed
	legacy.SkillPaths = nil
	legacy.SkillOwnershipSchema = 0
	if err := manager.store.Put(legacy); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(home, ".agents", "skills", "reaper-session-setup", "SKILL.md"), "---\nname: x\n---\n")

	writeFile(t, filepath.Join(root, ".claude-plugin", "plugin.json"), `{"name":"reaper","version":"0.2.0"}`)
	updated, err := manager.Update("reaper", func(TrustReport) bool { return true })
	if err != nil {
		t.Fatalf("updating a pre-receipt plugin: %v", err)
	}
	if updated.Version != "0.2.0" || updated.SkillPaths["reaper-session-setup"] == "" {
		t.Fatalf("updated record = %+v", updated)
	}
	if err := manager.Uninstall("reaper"); err != nil {
		t.Fatalf("uninstalling a pre-receipt plugin: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".agents", "skills", "reaper-session-setup", "SKILL.md")); err != nil {
		t.Fatalf("uninstall touched ~/.agents/skills: %v", err)
	}
}

func TestInstallRefusesASkillNameAnotherPluginProvides(t *testing.T) {
	first := makeClaudeBundle(t)
	manager := NewManager(&fakeRegistrar{}, t.TempDir(), "")
	if _, err := manager.Install(first, "", func(TrustReport) bool { return true }); err != nil {
		t.Fatal(err)
	}
	second := t.TempDir()
	writeFile(t, filepath.Join(second, ".claude-plugin", "plugin.json"), `{"name":"other","version":"1.0.0"}`)
	writeFile(t, filepath.Join(second, "skills", "reaper-session-setup", "SKILL.md"), "---\nname: x\n---\n")
	_, err := manager.Install(second, "", func(TrustReport) bool { return true })
	if !errors.Is(err, ErrSkillNameTaken) || !strings.Contains(err.Error(), `"reaper-session-setup"`) || !strings.Contains(err.Error(), `"reaper"`) {
		t.Fatalf("install err = %v, want a clash naming the skill and the plugin", err)
	}
	if list, _ := manager.List(); len(list) != 1 {
		t.Fatalf("the refused plugin was recorded: %+v", list)
	}
}

func TestInstallRefusesASkillNameTheGuardReports(t *testing.T) {
	manager := NewManager(&fakeRegistrar{}, t.TempDir(), "")
	manager.SetSkillNameGuard(func(_ string, names []string) error {
		return fmt.Errorf("%w: a skill named %q is already in your Skills folder", ErrSkillNameTaken, names[0])
	})
	reg := &fakeRegistrar{}
	manager.reg = reg
	if _, err := manager.Install(makeClaudeBundle(t), "", func(TrustReport) bool { return true }); !errors.Is(err, ErrSkillNameTaken) {
		t.Fatalf("install err = %v", err)
	}
	if len(reg.added) != 0 {
		t.Fatalf("a refused install registered servers: %v", reg.added)
	}
}

func TestManagerInstallDeclinedMakesNoChanges(t *testing.T) {
	root := makeClaudeBundle(t)
	reg := &fakeRegistrar{}
	m := NewManager(reg, t.TempDir(), "")

	_, err := m.Install(root, "", func(TrustReport) bool { return false })
	if !errors.Is(err, ErrInstallDeclined) {
		t.Fatalf("err = %v, want ErrInstallDeclined", err)
	}
	if len(reg.added) != 0 {
		t.Error("declined install must register nothing")
	}
	if list, _ := m.List(); len(list) != 0 {
		t.Error("declined install must record nothing")
	}
}

func TestBuildTrustReport(t *testing.T) {
	d := PluginDescriptor{
		Name: "p", SourceFormat: FormatCodex, InstallDir: "/p",
		MCPServers:  []MCPServerSpec{{Name: "srv", Command: "/nope/missing"}},
		Skills:      []SkillSpec{{Name: "s1"}},
		Unsupported: []UnsupportedComponent{{Kind: "hook", Detail: "deferred"}},
	}
	r := BuildTrustReport(d)
	if len(r.MCPCommands) != 1 {
		t.Errorf("mcp commands = %v", r.MCPCommands)
	}
	if len(r.Skills) != 1 {
		t.Errorf("skills = %v", r.Skills)
	}
	if len(r.Warnings) != 1 {
		t.Errorf("expected binary-missing warning, got %v", r.Warnings)
	}
	if len(r.Unsupported) != 1 {
		t.Errorf("unsupported = %v", r.Unsupported)
	}
	if r.String() == "" {
		t.Error("disclosure string should not be empty")
	}
}

func TestPreviewReplacementDisclosesTheReplacementSourceWithoutInstalling(t *testing.T) {
	root := makeClaudeBundle(t)
	m := NewManager(&fakeRegistrar{}, t.TempDir(), "")
	installed, err := m.Install(root, "", func(TrustReport) bool { return true })
	if err != nil {
		t.Fatalf("install: %v", err)
	}

	replacement := makeClaudeBundle(t)
	writeFile(t, filepath.Join(replacement, ".claude-plugin", "plugin.json"), `{"name":"reaper","version":"0.2.0"}`)
	report, changed, err := m.PreviewReplacement(installed.Name, replacement, FormatClaude)
	if err != nil || report.Name != installed.Name || changed {
		t.Fatalf("version-only replacement preview = (%+v, %v) err=%v", report, changed, err)
	}

	writeFile(t, filepath.Join(replacement, ".mcp.json"), `{"ori-other":{"command":"/usr/bin/false"}}`)
	report, changed, err = m.PreviewReplacement(installed.Name, replacement, FormatClaude)
	if err != nil || !changed || !strings.Contains(strings.Join(report.MCPCommands, " "), "/usr/bin/false") {
		t.Fatalf("component-changing replacement preview = (%+v, %v) err=%v", report, changed, err)
	}
	after, _ := m.List()
	if len(after) != 1 || after[0].Source != installed.Source || after[0].Version != "0.1.0" || after[0].Generation != installed.Generation {
		t.Fatalf("preview changed the installed plugin: %+v", after)
	}

	other := makeClaudeBundle(t)
	writeFile(t, filepath.Join(other, ".claude-plugin", "plugin.json"), `{"name":"other","version":"0.2.0"}`)
	if _, _, err := m.PreviewReplacement(installed.Name, other, FormatClaude); err == nil {
		t.Fatal("a replacement with another plugin identity was previewed")
	}
	if _, _, err := m.PreviewReplacement("missing", replacement, FormatClaude); err == nil {
		t.Fatal("a replacement for an uninstalled plugin was previewed")
	}
}

func TestResolveUpdatePreviewReturnsVersionTrustAndComponentChange(t *testing.T) {
	root := makeClaudeBundle(t)
	m := NewManager(&fakeRegistrar{}, t.TempDir(), "")
	installed, err := m.Install(root, "", func(TrustReport) bool { return true })
	if err != nil {
		t.Fatalf("install: %v", err)
	}

	writeFile(t, filepath.Join(root, ".claude-plugin", "plugin.json"), `{"name":"reaper","version":"0.2.0"}`)
	preview, err := m.resolveUpdatePreview(installed)
	if err != nil {
		t.Fatalf("resolve update preview: %v", err)
	}
	if preview.sourceVersion != "0.2.0" {
		t.Errorf("source version = %q, want 0.2.0", preview.sourceVersion)
	}
	if preview.trustReport.Name != installed.Name {
		t.Errorf("trust report name = %q, want %q", preview.trustReport.Name, installed.Name)
	}
	if preview.componentsChanged {
		t.Error("version-only update reported a trusted-component change")
	}

	report, changed, err := m.UpdatePreview(installed.Name)
	if err != nil {
		t.Fatalf("public update preview: %v", err)
	}
	if report.Name != preview.trustReport.Name || changed != preview.componentsChanged {
		t.Fatalf("public preview = (%+v, %v), want canonical result (%+v, %v)", report, changed, preview.trustReport, preview.componentsChanged)
	}

	availability, err := m.CheckUpdate(installed.Name)
	if err != nil {
		t.Fatalf("check update: %v", err)
	}
	if availability.Name != installed.Name || availability.InstalledVersion != "0.1.0" || availability.AvailableVersion != "0.2.0" {
		t.Fatalf("availability versions = %+v", availability)
	}
	if !availability.Available || availability.ComponentsChanged {
		t.Fatalf("version-only availability = %+v", availability)
	}

	// Availability is source-difference based, not semver ordered.
	writeFile(t, filepath.Join(root, ".claude-plugin", "plugin.json"), `{"name":"reaper","version":"0.0.1"}`)
	availability, err = m.CheckUpdate(installed.Name)
	if err != nil {
		t.Fatalf("check lower source version: %v", err)
	}
	if !availability.Available || availability.AvailableVersion != "0.0.1" {
		t.Fatalf("lower but different source version was not available: %+v", availability)
	}
}

func TestUpdatePreviewGitDoesNotMutateInstalledCheckout(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo, originalRevision := gitInitPluginRepo(t)
	source := encodeGitSubdir(repo, "plugins/demo", "", "")
	pluginsDir := t.TempDir()
	m := NewManager(&fakeRegistrar{}, pluginsDir, filepath.Join(pluginsDir, "src"))
	installed, err := m.Install(source, "", func(TrustReport) bool { return true })
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	installed.Enabled = true
	if err := m.store.Put(installed); err != nil {
		t.Fatalf("mark installed plugin enabled: %v", err)
	}

	writeFile(t, filepath.Join(repo, "plugins", "demo", ".claude-plugin", "plugin.json"), `{"name":"demo","version":"0.2.0"}`)
	commitGitAll(t, repo, "version 0.2.0")

	preview, err := m.resolveUpdatePreview(installed)
	if err != nil {
		t.Fatalf("resolve update preview: %v", err)
	}
	if preview.sourceVersion != "0.2.0" {
		t.Fatalf("source version = %q, want 0.2.0", preview.sourceVersion)
	}

	head, err := exec.Command("git", "-C", installed.InstallDir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("read installed revision: %v", err)
	}
	if got := strings.TrimSpace(string(head)); got != originalRevision {
		t.Fatalf("installed checkout moved from %s to %s during preview", originalRevision, got)
	}
	manifest, err := os.ReadFile(filepath.Join(installed.InstallDir, ".claude-plugin", "plugin.json"))
	if err != nil {
		t.Fatalf("read installed manifest: %v", err)
	}
	if strings.Contains(string(manifest), "0.2.0") {
		t.Fatalf("installed manifest changed during preview: %s", manifest)
	}
	stored, ok, err := m.store.Get(installed.Name)
	if err != nil || !ok {
		t.Fatalf("read installed record: ok=%v err=%v", ok, err)
	}
	if !stored.Enabled || stored.Version != installed.Version || stored.Generation != installed.Generation {
		t.Fatalf("preview altered installed record: before=%+v after=%+v", installed, stored)
	}
}

func TestCheckUpdatePreservesPinnedGitSource(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo, pinnedRevision := gitInitPluginRepo(t)
	source := encodeGitSubdir(repo, "plugins/demo", "", pinnedRevision)
	pluginsDir := t.TempDir()
	m := NewManager(&fakeRegistrar{}, pluginsDir, filepath.Join(pluginsDir, "src"))
	installed, err := m.Install(source, "", func(TrustReport) bool { return true })
	if err != nil {
		t.Fatalf("install pinned source: %v", err)
	}

	writeFile(t, filepath.Join(repo, "plugins", "demo", ".claude-plugin", "plugin.json"), `{"name":"demo","version":"0.2.0"}`)
	commitGitAll(t, repo, "version 0.2.0")

	availability, err := m.CheckUpdate(installed.Name)
	if err != nil {
		t.Fatalf("check pinned source: %v", err)
	}
	if availability.Available || availability.AvailableVersion != installed.Version {
		t.Fatalf("pinned source moved: %+v", availability)
	}
	head, err := exec.Command("git", "-C", installed.InstallDir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("read pinned installed revision: %v", err)
	}
	if got := strings.TrimSpace(string(head)); got != pinnedRevision {
		t.Fatalf("pinned installed checkout moved from %s to %s", pinnedRevision, got)
	}
}

func TestCheckUpdateReadsChangedLocalSourceWithoutWritingIt(t *testing.T) {
	root := makeClaudeBundle(t)
	m := NewManager(&fakeRegistrar{}, t.TempDir(), "")
	installed, err := m.Install(root, "", func(TrustReport) bool { return true })
	if err != nil {
		t.Fatalf("install local source: %v", err)
	}
	manifestPath := filepath.Join(root, ".claude-plugin", "plugin.json")
	changedManifest := []byte(`{"name":"reaper","version":"0.2.0"}`)
	if err := os.WriteFile(manifestPath, changedManifest, 0o640); err != nil {
		t.Fatalf("change local source: %v", err)
	}
	before, err := os.Stat(manifestPath)
	if err != nil {
		t.Fatal(err)
	}

	availability, err := m.CheckUpdate(installed.Name)
	if err != nil {
		t.Fatalf("check local source: %v", err)
	}
	if !availability.Available || availability.AvailableVersion != "0.2.0" || availability.ComponentsChanged {
		t.Fatalf("local source availability = %+v", availability)
	}
	after, err := os.Stat(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(changedManifest) || before.Mode() != after.Mode() || !before.ModTime().Equal(after.ModTime()) {
		t.Fatalf("check wrote local source: content=%q mode=%v modtime=%v (before mode=%v modtime=%v)", got, after.Mode(), after.ModTime(), before.Mode(), before.ModTime())
	}
}

func TestCheckUpdateAvailabilityDifferences(t *testing.T) {
	tests := []struct {
		name                  string
		mutate                func(*testing.T, string)
		wantAvailable         bool
		wantVersion           string
		wantComponentsChanged bool
	}{
		{
			name:          "unchanged",
			mutate:        func(*testing.T, string) {},
			wantVersion:   "0.1.0",
			wantAvailable: false,
		},
		{
			name: "version only",
			mutate: func(t *testing.T, root string) {
				writeFile(t, filepath.Join(root, ".claude-plugin", "plugin.json"), `{"name":"reaper","version":"0.2.0"}`)
			},
			wantAvailable: true,
			wantVersion:   "0.2.0",
		},
		{
			name: "components only",
			mutate: func(t *testing.T, root string) {
				writeFile(t, filepath.Join(root, "skills", "extra", "SKILL.md"), "---\nname: extra\n---\n")
			},
			wantAvailable:         true,
			wantVersion:           "0.1.0",
			wantComponentsChanged: true,
		},
		{
			name: "version and components",
			mutate: func(t *testing.T, root string) {
				writeFile(t, filepath.Join(root, ".claude-plugin", "plugin.json"), `{"name":"reaper","version":"0.2.0"}`)
				writeFile(t, filepath.Join(root, "skills", "extra", "SKILL.md"), "---\nname: extra\n---\n")
			},
			wantAvailable:         true,
			wantVersion:           "0.2.0",
			wantComponentsChanged: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := makeClaudeBundle(t)
			m := NewManager(&fakeRegistrar{}, t.TempDir(), "")
			installed, err := m.Install(root, "", func(TrustReport) bool { return true })
			if err != nil {
				t.Fatalf("install: %v", err)
			}
			tc.mutate(t, root)

			got, err := m.CheckUpdate(installed.Name)
			if err != nil {
				t.Fatalf("check update: %v", err)
			}
			if got.Available != tc.wantAvailable || got.AvailableVersion != tc.wantVersion || got.ComponentsChanged != tc.wantComponentsChanged {
				t.Fatalf("availability = %+v, want available=%v version=%q componentsChanged=%v", got, tc.wantAvailable, tc.wantVersion, tc.wantComponentsChanged)
			}
		})
	}
}

func TestCheckUpdateReportsMalformedAndUnreachableSources(t *testing.T) {
	t.Run("malformed manifest", func(t *testing.T) {
		root := makeClaudeBundle(t)
		m := NewManager(&fakeRegistrar{}, t.TempDir(), "")
		installed, err := m.Install(root, "", func(TrustReport) bool { return true })
		if err != nil {
			t.Fatalf("install: %v", err)
		}
		writeFile(t, filepath.Join(root, ".claude-plugin", "plugin.json"), `{not json`)
		if _, err := m.CheckUpdate(installed.Name); err == nil {
			t.Fatal("malformed source check succeeded")
		}
	})

	t.Run("unreachable git source", func(t *testing.T) {
		pluginsDir := t.TempDir()
		m := NewManager(&fakeRegistrar{}, pluginsDir, filepath.Join(pluginsDir, "src"))
		missingSource := filepath.Join(t.TempDir(), "missing.git")
		if err := m.store.Put(InstalledPlugin{
			Name:       "missing",
			Version:    "0.1.0",
			Source:     missingSource,
			Format:     FormatClaude,
			InstallDir: filepath.Join(pluginsDir, "src", "missing"),
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := m.CheckUpdate("missing"); err == nil {
			t.Fatal("unreachable git source check succeeded")
		}
	})
}

func TestManagerUpdateFromReviewedSourceConfirmsAndKeepsEnablementSeparate(t *testing.T) {
	oldRoot := makeClaudeBundle(t)
	reg := &fakeRegistrar{}
	manager := NewManager(reg, t.TempDir(), "")
	installed, err := manager.Install(oldRoot, FormatClaude, func(TrustReport) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.SetEnabled(installed.Name, true); err != nil {
		t.Fatal(err)
	}
	candidate := makeClaudeBundle(t)
	manifest := filepath.Join(candidate, ".claude-plugin", "plugin.json")
	writeFile(t, manifest, `{"name":"reaper","version":"0.5.0"}`)
	if _, err := manager.UpdateFromSource(installed.Name, candidate, FormatClaude, func(TrustReport) bool { return false }); !errors.Is(err, ErrInstallDeclined) {
		t.Fatalf("declined reviewed replacement error = %v", err)
	}
	before, _, _ := manager.store.Get(installed.Name)
	if before.Source != oldRoot || before.Version != "0.1.0" || !before.Enabled {
		t.Fatalf("declined replacement changed existing generation: %#v", before)
	}
	updated, err := manager.UpdateFromSource(installed.Name, candidate, FormatClaude, func(report TrustReport) bool {
		return report.Name == installed.Name
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Source != candidate || updated.Version != "0.5.0" || !updated.Enabled || updated.Generation <= installed.Generation {
		t.Fatalf("unexpected reviewed replacement: %#v", updated)
	}
}

func TestManagerUpdateFromReviewedSourceRestoresOldRegistrationOnFailure(t *testing.T) {
	oldRoot := makeClaudeBundle(t)
	reg := &fakeRegistrar{}
	manager := NewManager(reg, t.TempDir(), "")
	installed, err := manager.Install(oldRoot, FormatClaude, func(TrustReport) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	candidate := makeClaudeBundle(t)
	writeFile(t, filepath.Join(candidate, ".claude-plugin", "plugin.json"), `{"name":"reaper","version":"0.5.0"}`)
	writeFile(t, filepath.Join(candidate, ".mcp.json"), `{"ori-reaper":{"command":"/usr/bin/true"},"fails":{"command":"/usr/bin/true"}}`)
	reg.failOn = "reaper/fails"
	if _, err := manager.UpdateFromSource(installed.Name, candidate, FormatClaude, func(TrustReport) bool { return true }); err == nil {
		t.Fatal("replacement registration failure was accepted")
	}
	persisted, _, _ := manager.store.Get(installed.Name)
	if persisted.Source != oldRoot || persisted.Version != installed.Version || persisted.Generation != installed.Generation {
		t.Fatalf("failed replacement changed stored generation: %#v", persisted)
	}
	if _, ok := reg.added["reaper/ori-reaper"]; !ok {
		t.Fatal("failed replacement did not restore existing MCP registration")
	}
}

// partialRemoveRegistrar fails to remove one named server.
type partialRemoveRegistrar struct {
	fakeRegistrar
	removeFailOn string
}

func (registrar *partialRemoveRegistrar) RemoveServer(name string) error {
	if name == registrar.removeFailOn {
		return errors.New("injected removal failure")
	}
	return registrar.fakeRegistrar.RemoveServer(name)
}

func TestManagerUpdateRestoresOnlyComponentsRemovedBeforeFailure(t *testing.T) {
	root := makeClaudeBundle(t)
	writeFile(t, filepath.Join(root, ".mcp.json"), `{"a-first":{"command":"/usr/bin/true"},"z-last":{"command":"/usr/bin/true"}}`)
	registrar := &partialRemoveRegistrar{}
	manager := NewManager(registrar, t.TempDir(), "")
	installed, err := manager.Install(root, "", func(TrustReport) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	registrar.removeFailOn = "reaper/z-last"
	if _, err := manager.Update(installed.Name, func(TrustReport) bool { return true }); err == nil {
		t.Fatal("partial removal failure was accepted")
	}
	if _, ok := registrar.added["reaper/a-first"]; !ok || len(registrar.added) != 2 {
		t.Fatalf("failed update did not restore exactly the old servers: %v", registrar.added)
	}
	persisted, ok, err := manager.store.Get(installed.Name)
	if err != nil || !ok || persisted.Generation != installed.Generation {
		t.Fatalf("failed update changed the durable generation: %#v, %t, %v", persisted, ok, err)
	}
}

func TestManagerUpdateRestoresRecordedComponentsAfterRegistrationFailure(t *testing.T) {
	root := makeClaudeBundle(t)
	registrar := &fakeRegistrar{}
	manager := NewManager(registrar, t.TempDir(), "")
	installed, err := manager.Install(root, "", func(TrustReport) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, ".mcp.json"), `{"ori-reaper":{"command":"/usr/bin/true"},"fails":{"command":"/usr/bin/true"}}`)
	registrar.failOn = "reaper/fails"
	if _, err := manager.Update(installed.Name, func(TrustReport) bool { return true }); err == nil {
		t.Fatal("update registration failure was accepted")
	}
	persisted, ok, err := manager.store.Get(installed.Name)
	if err != nil || !ok || persisted.Generation != installed.Generation || persisted.Version != installed.Version {
		t.Fatalf("failed update changed stored generation: %#v ok=%t err=%v", persisted, ok, err)
	}
	if _, ok := registrar.added["reaper/ori-reaper"]; !ok {
		t.Fatal("failed update did not restore the recorded MCP server")
	}
}

func TestManagerUpdate(t *testing.T) {
	root := makeClaudeBundle(t)
	reg := &fakeRegistrar{}
	m := NewManager(reg, t.TempDir(), "")
	installed, err := m.Install(root, "", func(TrustReport) bool { return true })
	if err != nil {
		t.Fatalf("install: %v", err)
	}

	// A no-change update keeps the server registered and reports no change.
	if _, changed, err := m.UpdatePreview("reaper"); err != nil || changed {
		t.Errorf("no-op update preview: changed=%v err=%v", changed, err)
	}
	unchanged, err := m.Update("reaper", func(TrustReport) bool { return true })
	if err != nil {
		t.Fatalf("update (no change): %v", err)
	}
	if unchanged.ContentGeneration != installed.ContentGeneration || unchanged.Generation <= installed.Generation {
		t.Fatalf("no-change update generations = content %d runtime %d", unchanged.ContentGeneration, unchanged.Generation)
	}
	if _, ok := reg.added["reaper/ori-reaper"]; !ok {
		t.Error("server missing after no-op update")
	}

	// Changing packaged skill bytes is a trusted content change even when the
	// component name stays stable.
	writeFile(t, filepath.Join(root, "skills", "reaper-session-setup", "SKILL.md"), "---\nname: x\n---\nupdated instructions\n")
	if _, changed, err := m.UpdatePreview("reaper"); err != nil || !changed {
		t.Fatalf("expected changed=true after changing skill bytes: changed=%v err=%v", changed, err)
	}
	contentChanged, err := m.Update("reaper", func(TrustReport) bool { return true })
	if err != nil {
		t.Fatalf("update (skill content changed): %v", err)
	}
	if contentChanged.ContentGeneration <= unchanged.ContentGeneration {
		t.Fatalf("skill content update did not advance content generation: %+v", contentChanged)
	}

	// Add a skill to the bundle; update detects the changed component set too.
	writeFile(t, filepath.Join(root, "skills", "extra", "SKILL.md"), "---\nname: extra\n---\n")
	if _, changed, err := m.UpdatePreview("reaper"); err != nil || !changed {
		t.Fatalf("expected changed=true after adding a skill: changed=%v err=%v", changed, err)
	}
	changedPlugin, err := m.Update("reaper", func(TrustReport) bool { return true })
	if err != nil {
		t.Fatalf("update (changed): %v", err)
	}
	if changedPlugin.ContentGeneration <= contentChanged.ContentGeneration {
		t.Fatalf("changed update did not advance content generation: %+v", changedPlugin)
	}
	list, _ := m.List()
	if len(list) != 1 || len(list[0].Skills) != 2 {
		t.Errorf("expected 2 skills after update, got %+v", list)
	}
}

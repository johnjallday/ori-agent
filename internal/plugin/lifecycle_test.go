package plugin

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
	m := NewManager(&fakeRegistrar{}, &fakeSkills{}, t.TempDir(), "")
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
	if !got.Enabled || got.Generation != 5 {
		t.Fatalf("enabled plugin = %+v", got)
	}
	if len(lifecycle.events) != 1 || lifecycle.events[0] != "replace:surface-tools" {
		t.Fatalf("lifecycle events = %v", lifecycle.events)
	}
	if lifecycle.previous.Generation != 4 || lifecycle.next.Generation != 5 || !lifecycle.next.Enabled {
		t.Fatalf("replacement = %+v -> %+v", lifecycle.previous, lifecycle.next)
	}
}

func TestManagerInstallAndUninstall(t *testing.T) {
	root := makeClaudeBundle(t)
	reg := &fakeRegistrar{}
	sk := &fakeSkills{}
	m := NewManager(reg, sk, t.TempDir(), "")

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

func TestManagerInstallDeclinedMakesNoChanges(t *testing.T) {
	root := makeClaudeBundle(t)
	reg := &fakeRegistrar{}
	sk := &fakeSkills{}
	m := NewManager(reg, sk, t.TempDir(), "")

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

func TestResolveUpdatePreviewReturnsVersionTrustAndComponentChange(t *testing.T) {
	root := makeClaudeBundle(t)
	m := NewManager(&fakeRegistrar{}, &fakeSkills{}, t.TempDir(), "")
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
	m := NewManager(&fakeRegistrar{}, &fakeSkills{}, pluginsDir, filepath.Join(pluginsDir, "src"))
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
	m := NewManager(&fakeRegistrar{}, &fakeSkills{}, pluginsDir, filepath.Join(pluginsDir, "src"))
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
	m := NewManager(&fakeRegistrar{}, &fakeSkills{}, t.TempDir(), "")
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
			m := NewManager(&fakeRegistrar{}, &fakeSkills{}, t.TempDir(), "")
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
		m := NewManager(&fakeRegistrar{}, &fakeSkills{}, t.TempDir(), "")
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
		m := NewManager(&fakeRegistrar{}, &fakeSkills{}, pluginsDir, filepath.Join(pluginsDir, "src"))
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

func TestManagerUpdate(t *testing.T) {
	root := makeClaudeBundle(t)
	reg := &fakeRegistrar{}
	sk := &fakeSkills{}
	m := NewManager(reg, sk, t.TempDir(), "")
	if _, err := m.Install(root, "", func(TrustReport) bool { return true }); err != nil {
		t.Fatalf("install: %v", err)
	}

	// A no-change update keeps the server registered and reports no change.
	if _, changed, err := m.UpdatePreview("reaper"); err != nil || changed {
		t.Errorf("no-op update preview: changed=%v err=%v", changed, err)
	}
	if _, err := m.Update("reaper", func(TrustReport) bool { return true }); err != nil {
		t.Fatalf("update (no change): %v", err)
	}
	if _, ok := reg.added["reaper/ori-reaper"]; !ok {
		t.Error("server missing after no-op update")
	}

	// Add a skill to the bundle; update detects the change and records it.
	writeFile(t, filepath.Join(root, "skills", "extra", "SKILL.md"), "---\nname: extra\n---\n")
	if _, changed, err := m.UpdatePreview("reaper"); err != nil || !changed {
		t.Fatalf("expected changed=true after adding a skill: changed=%v err=%v", changed, err)
	}
	if _, err := m.Update("reaper", func(TrustReport) bool { return true }); err != nil {
		t.Fatalf("update (changed): %v", err)
	}
	list, _ := m.List()
	if len(list) != 1 || len(list[0].Skills) != 2 {
		t.Errorf("expected 2 skills after update, got %+v", list)
	}
}

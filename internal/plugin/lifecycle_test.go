package plugin

import (
	"errors"
	"path/filepath"
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

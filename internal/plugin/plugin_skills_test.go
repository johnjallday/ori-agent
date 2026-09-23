package plugin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestEnabledSkillsReadsSkillsFromTheInstallFolder(t *testing.T) {
	root := makeClaudeBundle(t)
	manager := NewManager(&fakeRegistrar{}, t.TempDir(), "")
	installed, err := manager.Install(root, "", func(TrustReport) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	records, err := manager.Installed()
	if err != nil {
		t.Fatal(err)
	}
	if got := manager.EnabledSkills(records); len(got) != 0 {
		t.Fatalf("a disabled plugin's skills were listed: %+v", got)
	}

	if err := manager.SetEnabled(installed.Name, true); err != nil {
		t.Fatal(err)
	}
	records, _ = manager.Installed()
	want := []SkillLocation{{Plugin: "reaper", Name: "reaper-session-setup", Dir: filepath.Join(root, "skills", "reaper-session-setup")}}
	if got := manager.EnabledSkills(records); !slices.Equal(got, want) {
		t.Fatalf("enabled skills = %+v, want %+v", got, want)
	}

	if err := manager.Uninstall(installed.Name); err != nil {
		t.Fatal(err)
	}
	records, _ = manager.Installed()
	if got := manager.EnabledSkills(records); len(got) != 0 {
		t.Fatalf("an uninstalled plugin's skills were listed: %+v", got)
	}
}

// A record written before skill paths were recorded finds its skills from the
// plugin's manifest.
func TestEnabledSkillsWorksOutFoldersForOlderRecords(t *testing.T) {
	root := makeClaudeBundle(t)
	manager := NewManager(&fakeRegistrar{}, t.TempDir(), "")
	record := InstalledPlugin{
		Name: "reaper", Source: root, Format: FormatClaude, InstallDir: root,
		Skills: []string{"reaper-session-setup", "gone"}, Enabled: true, Generation: 1,
	}
	got := manager.EnabledSkills([]InstalledPlugin{record})
	want := []SkillLocation{{Plugin: "reaper", Name: "reaper-session-setup", Dir: filepath.Join(root, "skills", "reaper-session-setup")}}
	if !slices.Equal(got, want) {
		t.Fatalf("enabled skills = %+v, want %+v", got, want)
	}
}

func TestEnabledSkillsIgnoresRecordedPathsOutsideTheInstallFolder(t *testing.T) {
	root := makeClaudeBundle(t)
	manager := NewManager(&fakeRegistrar{}, t.TempDir(), "")
	record := InstalledPlugin{
		Name: "reaper", Source: root, Format: FormatClaude, InstallDir: root, Enabled: true,
		Skills:     []string{"escape"},
		SkillPaths: map[string]string{"escape": "../../etc"},
	}
	if got := manager.EnabledSkills([]InstalledPlugin{record}); len(got) != 0 {
		t.Fatalf("a path outside the install folder was used: %+v", got)
	}
}

// --- FR 27: the one-time cleanup of old copies in ~/.agents/skills ---------

type legacyCopyFixture struct {
	skillsRoot string
	dataDir    string
}

func newLegacyCopyFixture(t *testing.T) legacyCopyFixture {
	t.Helper()
	base := t.TempDir()
	f := legacyCopyFixture{skillsRoot: filepath.Join(base, "home", ".agents", "skills"), dataDir: filepath.Join(base, "data")}
	if err := os.MkdirAll(f.skillsRoot, 0o750); err != nil {
		t.Fatal(err)
	}
	return f
}

// copyWithReceipt writes a plugin skill copy the way older versions did.
func (f legacyCopyFixture) copyWithReceipt(t *testing.T, pluginName, skillName string) string {
	t.Helper()
	dir := filepath.Join(f.skillsRoot, skillName)
	writeFile(t, filepath.Join(dir, "SKILL.md"), "---\nname: "+skillName+"\n---\nbody\n")
	digest, err := SkillTreeDigest(dir)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := json.Marshal(legacySkillReceipt{SchemaVersion: 1, PluginName: pluginName, SkillName: skillName, TreeDigest: digest})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, legacySkillReceiptFileName), string(receipt))
	return dir
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func TestLegacyCleanupRemovesOnlyUneditedCopiesOfPluginsInstalledHere(t *testing.T) {
	f := newLegacyCopyFixture(t)
	removable := f.copyWithReceipt(t, "reaper", "reaper-mixing")
	edited := f.copyWithReceipt(t, "reaper", "reaper-edited")
	writeFile(t, filepath.Join(edited, "SKILL.md"), "---\nname: reaper-edited\n---\nmy own notes\n")
	otherDataDir := f.copyWithReceipt(t, "not-installed-here", "foreign-skill")
	userOwn := filepath.Join(f.skillsRoot, "claude-code-skill")
	writeFile(t, filepath.Join(userOwn, "SKILL.md"), "---\nname: claude-code-skill\n---\n")
	renamed := f.copyWithReceipt(t, "reaper", "reaper-renamed")
	renamedTo := filepath.Join(f.skillsRoot, "moved-by-user")
	if err := os.Rename(renamed, renamedTo); err != nil {
		t.Fatal(err)
	}
	broken := filepath.Join(f.skillsRoot, "broken-receipt")
	writeFile(t, filepath.Join(broken, "SKILL.md"), "x")
	writeFile(t, filepath.Join(broken, legacySkillReceiptFileName), "{not json")

	installed := []InstalledPlugin{{Name: "reaper"}}
	result, ran, err := CleanLegacyPluginSkillsOnce(f.dataDir, f.skillsRoot, installed)
	if err != nil || !ran {
		t.Fatalf("cleanup = ran %v err %v", ran, err)
	}
	if !slices.Equal(result.Removed, []string{"reaper-mixing"}) {
		t.Fatalf("removed = %v", result.Removed)
	}
	if exists(removable) {
		t.Error("the unedited copy of an installed plugin's skill was kept")
	}
	for _, kept := range []string{edited, otherDataDir, userOwn, renamedTo, broken} {
		if !exists(kept) {
			t.Errorf("%s was removed", kept)
		}
	}
	if len(result.Kept) != 4 {
		t.Errorf("kept = %v, want the four receipt-bearing folders with reasons", result.Kept)
	}
	if !exists(filepath.Join(f.dataDir, LegacySkillCleanupMarkerName)) {
		t.Fatal("no marker was written")
	}

	// A second start does nothing, even with a new removable copy present.
	again := f.copyWithReceipt(t, "reaper", "reaper-later")
	if _, ran, err := CleanLegacyPluginSkillsOnce(f.dataDir, f.skillsRoot, installed); err != nil || ran {
		t.Fatalf("second run = ran %v err %v", ran, err)
	}
	if !exists(again) {
		t.Error("the cleanup ran twice")
	}
}

func TestLegacyCleanupLeavesSymlinksAlone(t *testing.T) {
	f := newLegacyCopyFixture(t)
	target := filepath.Join(t.TempDir(), "real-skill")
	writeFile(t, filepath.Join(target, "SKILL.md"), "x")
	link := filepath.Join(f.skillsRoot, "linked")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, _, err := CleanLegacyPluginSkillsOnce(f.dataDir, f.skillsRoot, []InstalledPlugin{{Name: "reaper"}}); err != nil {
		t.Fatal(err)
	}
	if !exists(link) || !exists(filepath.Join(target, "SKILL.md")) {
		t.Fatal("a symlinked skill folder was touched")
	}
}

func TestLegacyCleanupWithNoSkillsFolderStillRecordsThatItRan(t *testing.T) {
	dataDir := t.TempDir()
	result, ran, err := CleanLegacyPluginSkillsOnce(dataDir, filepath.Join(t.TempDir(), "missing"), nil)
	if err != nil || !ran || len(result.Removed) != 0 {
		t.Fatalf("cleanup = %+v ran %v err %v", result, ran, err)
	}
	if !exists(filepath.Join(dataDir, LegacySkillCleanupMarkerName)) {
		t.Fatal("no marker was written")
	}
}

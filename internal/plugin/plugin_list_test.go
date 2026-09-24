package plugin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var listNow = time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)

func pinned(sha string) string {
	return "https://github.com/johnjallday/reaper-plugin#sha=" + sha
}

func TestWritePluginListWritesOnlyWhenTheBytesChange(t *testing.T) {
	root := t.TempDir()
	list := PluginList{}
	list.RecordInstalled(InstalledPlugin{Name: "reaper-plugin", Version: "0.8.0", Source: pinned("aaa"), Format: FormatClaude}, listNow)
	if wrote, err := WritePluginList(root, list); err != nil || !wrote {
		t.Fatalf("first write = %v %v", wrote, err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(PluginListPath(root), old, old); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(PluginListPath(root))
	if wrote, err := WritePluginList(root, list); err != nil || wrote {
		t.Fatalf("an unchanged list was rewritten: %v %v", wrote, err)
	}
	if after, _ := os.Stat(PluginListPath(root)); !after.ModTime().Equal(before.ModTime()) {
		t.Fatalf("the file was touched: %v -> %v", before.ModTime(), after.ModTime())
	}
	read, found, err := ReadPluginList(root)
	if err != nil || !found || len(read.Plugins) != 1 || read.Plugins[0].Source != pinned("aaa") || read.SchemaVersion != 1 {
		t.Fatalf("read back = %+v %v %v", read, found, err)
	}
	entries, _ := os.ReadDir(root)
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".tmp") {
			t.Fatalf("a temp file was left: %s", entry.Name())
		}
	}
}

func TestRecordingKeepsAnUnchangedEntryAndMovesUninstallsToRemoved(t *testing.T) {
	list := PluginList{}
	record := InstalledPlugin{Name: "reaper-plugin", Version: "0.8.0", Source: pinned("aaa"), Format: FormatClaude}
	list.RecordInstalled(record, listNow)
	list.RecordInstalled(record, listNow.Add(time.Hour)) // a no-op update
	if !list.Plugins[0].UpdatedAt.Equal(listNow) {
		t.Fatalf("a no-op update changed updated_at: %v", list.Plugins[0].UpdatedAt)
	}
	list.RecordUninstalled("reaper-plugin", listNow)
	if len(list.Plugins) != 0 || len(list.Removed) != 1 || list.Removed[0].Name != "reaper-plugin" {
		t.Fatalf("after uninstall = %+v", list)
	}
	list.RecordInstalled(record, listNow)
	if len(list.Plugins) != 1 || len(list.Removed) != 0 {
		t.Fatalf("reinstalling did not drop the removed entry: %+v", list)
	}
}

func TestReadPluginListReportsAnInvalidFile(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(PluginListPath(root), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, found, err := ReadPluginList(root); err == nil || !found || !strings.Contains(err.Error(), "not valid JSON") {
		t.Fatalf("read = %v %v", found, err)
	}
	if list, found, err := ReadPluginList(t.TempDir()); err != nil || found || len(list.Plugins) != 0 {
		t.Fatalf("a missing list = %+v %v %v", list, found, err)
	}
}

// FR 31: every row of the pending-changes table.
func TestPendingCoversEveryRowOfTheTable(t *testing.T) {
	localHere := t.TempDir()
	list := PluginList{
		Plugins: []PluginListEntry{
			{Name: "missing-here", Version: "1.0.0", Source: pinned("m1"), Format: FormatClaude},
			{Name: "newer-listed", Version: "0.9.0", Source: pinned("n2")},
			{Name: "older-listed", Version: "0.7.0", Source: pinned("o1")},
			{Name: "same-commit", Version: "0.8.0", Source: pinned("s1")},
			{Name: "local-elsewhere", Version: "0.1.0", Source: "/Users/someone-else/plugins/local-thing"},
			{Name: "local-here", Version: "0.1.0", Source: localHere},
		},
		Removed: []RemovedPluginEntry{
			{Name: "removed-still-here", RemovedAt: listNow},
			{Name: "removed-gone", RemovedAt: listNow},
		},
	}
	installed := []InstalledPlugin{
		{Name: "newer-listed", Version: "0.8.0", Source: pinned("n1")},
		{Name: "older-listed", Version: "0.8.0", Source: pinned("o2")},
		{Name: "same-commit", Version: "0.8.0", Source: pinned("s1")},
		{Name: "removed-still-here", Version: "2.0.0", Source: pinned("r1")},
		{Name: "only-here", Version: "1.0.0", Source: pinned("h1")},
	}
	byName := map[string]PendingChange{}
	for _, change := range Pending(list, installed) {
		byName[change.Name] = change
	}

	if got := byName["missing-here"]; got.Kind != PendingInstall || !got.Installable || got.Source != pinned("m1") || got.Format != FormatClaude {
		t.Errorf("listed, not installed = %+v", got)
	}
	if got := byName["newer-listed"]; got.Kind != PendingSwitch || got.Direction != "update" || got.InstalledVersion != "0.8.0" || got.ListedVersion != "0.9.0" {
		t.Errorf("listed newer = %+v", got)
	}
	if got := byName["older-listed"]; got.Kind != PendingSwitch || got.Direction != "change" {
		t.Errorf("listed older = %+v", got)
	}
	if _, pending := byName["same-commit"]; pending {
		t.Error("the same commit is not a change")
	}
	if got := byName["local-elsewhere"]; got.Kind != PendingInstall || got.Installable || got.Reason != LocalFolderNotHereReason {
		t.Errorf("a local folder that is not here = %+v", got)
	}
	if got := byName["local-here"]; !got.Installable {
		t.Errorf("a local folder that is here = %+v", got)
	}
	if got := byName["removed-still-here"]; got.Kind != PendingUninstall || got.InstalledVersion != "2.0.0" {
		t.Errorf("removed, still installed = %+v", got)
	}
	if _, pending := byName["removed-gone"]; pending {
		t.Error("a removed plugin that is not installed is not a change")
	}
	if _, pending := byName["only-here"]; pending {
		t.Error("a plugin in neither list is added automatically, not offered")
	}
	if len(byName) != 6 {
		t.Errorf("pending = %v", byName)
	}
	if byName["missing-here"].Fingerprint == "" || byName["missing-here"].Fingerprint == byName["newer-listed"].Fingerprint {
		t.Error("fingerprints must identify their entries")
	}
}

func TestAddUnlistedFillsOnlyWhatIsInNeitherList(t *testing.T) {
	list := PluginList{
		Plugins: []PluginListEntry{{Name: "listed", Source: pinned("l1")}},
		Removed: []RemovedPluginEntry{{Name: "removed", RemovedAt: listNow}},
	}
	installed := []InstalledPlugin{
		{Name: "listed", Source: pinned("l2")},
		{Name: "removed", Source: pinned("r1")},
		{Name: "new", Version: "1.0.0", Source: pinned("n1"), Format: FormatCodex},
	}
	if !list.AddUnlisted(installed, listNow) {
		t.Fatal("nothing was added")
	}
	if len(list.Plugins) != 2 || list.Plugins[1].Name != "new" || list.Plugins[1].Format != FormatCodex || list.Plugins[0].Source != pinned("l1") {
		t.Fatalf("after fill = %+v", list.Plugins)
	}
	if list.AddUnlisted(installed, listNow) {
		t.Fatal("a second fill added again")
	}
}

func TestTheManagerReportsInstallUpdateAndUninstallButNotEnable(t *testing.T) {
	root := makeClaudeBundle(t)
	manager := NewManager(&fakeRegistrar{}, t.TempDir(), "")
	var kinds []PluginChangeKind
	manager.SetChangeObserver(func(change PluginChange) { kinds = append(kinds, change.Kind) })

	if _, err := manager.Install(root, "", func(TrustReport) bool { return true }); err != nil {
		t.Fatal(err)
	}
	if err := manager.SetEnabled("reaper", true); err != nil {
		t.Fatal(err)
	}
	if err := manager.SetEnabled("reaper", false); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Update("reaper", func(TrustReport) bool { return true }); err != nil {
		t.Fatal(err)
	}
	if err := manager.Uninstall("reaper"); err != nil {
		t.Fatal(err)
	}
	want := []PluginChangeKind{PluginInstalled, PluginUpdated, PluginUninstalled}
	if strings.Join(kindStrings(kinds), ",") != strings.Join(kindStrings(want), ",") {
		t.Fatalf("changes = %v, want %v", kinds, want)
	}
	if _, err := manager.Install(filepath.Join(t.TempDir(), "missing"), "", func(TrustReport) bool { return true }); err == nil {
		t.Fatal("a missing source installed")
	}
	if len(kinds) != 3 {
		t.Fatalf("a failed install was reported: %v", kinds)
	}
}

func kindStrings(kinds []PluginChangeKind) []string {
	out := make([]string, len(kinds))
	for index, kind := range kinds {
		out[index] = string(kind)
	}
	return out
}

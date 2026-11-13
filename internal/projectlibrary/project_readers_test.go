package projectlibrary

import (
	"testing"

	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
)

// assistant-reads-projects 4.1 (FR21, G6): only reviewed plugins' readers are
// ever chosen, and a format is readable because a plugin declares it, with no
// host code naming that format.

// readerPlugin is an installed plugin whose manifest declares a reader for
// formats, with an output limit that fits maxOutput/32768 files.
func readerPlugin(name string, enabled bool, maxOutput int, formats ...string) plugin.InstalledPlugin {
	return plugin.InstalledPlugin{Name: name, Enabled: enabled, WorkspaceSurfaces: &plugin.SurfaceContribution{
		Services: []plugin.ContributedService{{ID: "svc", Operations: []plugin.ContributedOperation{{
			ID: "project.facts.read", Policy: "read_only", TimeoutClass: "long", MaxOutputBytes: maxOutput}}}},
		ProjectFacts: &plugin.ProjectFactsRef{ServiceID: "svc", Operation: "project.facts.read", Formats: formats},
	}}
}

func reviewedPluginID(t *testing.T) string {
	t.Helper()
	entry, ok := reviewedintegration.Get("ori_reaper")
	if !ok {
		t.Fatal("no reviewed integration fixture")
	}
	return entry.PluginID
}

func TestProjectReaders_OnlyReviewedPluginsAreChosen(t *testing.T) {
	reviewed := reviewedPluginID(t)
	installed := []plugin.InstalledPlugin{
		readerPlugin("someone-elses-plugin", true, 262144, "zzfmt", "other"),
		readerPlugin(reviewed, true, 262144, "zzfmt"),
	}
	readers := projectReadersFrom(installed)
	if reader, ok := readers["zzfmt"]; !ok || reader.plugin.Name != reviewed || reader.batch != 8 {
		t.Fatalf("zzfmt reader = %+v ok=%v", reader, ok)
	}
	// A format only an unreviewed plugin declares has no reader at all.
	if _, ok := readers["other"]; ok {
		t.Fatal("an unreviewed plugin's reader was chosen")
	}
	// Disabled, or without room for one file's output: not a reader.
	for name, candidate := range map[string]plugin.InstalledPlugin{
		"disabled":         readerPlugin(reviewed, false, 262144, "zzfmt"),
		"output too small": readerPlugin(reviewed, true, 1024, "zzfmt"),
	} {
		if len(projectReadersFrom([]plugin.InstalledPlugin{candidate})) != 0 {
			t.Errorf("%s gave a reader", name)
		}
	}
}

// FR49: a format's reviewed plugin that is installed without a reader is told as
// an update, by its reviewed name; nothing else carries a name.
func TestReaderFor_NamesThePluginToUpdate(t *testing.T) {
	entry, _ := reviewedintegration.Get("ori_reaper")
	old := plugin.InstalledPlugin{Name: entry.PluginID, Enabled: true}
	if status, name := ReaderFor([]plugin.InstalledPlugin{old}, "reaper"); status != ReaderUpdatePlugin || name != entry.DisplayName || name == "" {
		t.Fatalf("installed without a reader = %q, %q", status, name)
	}
	if status, name := ReaderFor([]plugin.InstalledPlugin{readerPlugin(entry.PluginID, true, 262144, "reaper")}, "reaper"); status != ReaderReady || name != "" {
		t.Fatalf("a reader = %q, %q", status, name)
	}
	if status, name := ReaderFor(nil, "reaper"); status != ReaderNone || name != "" {
		t.Fatalf("nothing installed = %q, %q", status, name)
	}
	// A format no reviewed plugin owns cannot be updated into readability.
	if status, name := ReaderFor([]plugin.InstalledPlugin{old}, "logic"); status != ReaderNone || name != "" {
		t.Fatalf("a format no plugin owns = %q, %q", status, name)
	}
}

func TestReaderUpdateName(t *testing.T) {
	entry, _ := reviewedintegration.Get("ori_reaper")
	old := plugin.InstalledPlugin{Name: entry.PluginID, Enabled: true}
	if got := ReaderUpdateName([]plugin.InstalledPlugin{old}); got != entry.DisplayName {
		t.Fatalf("a reviewed plugin without a reader = %q, want %q", got, entry.DisplayName)
	}
	for name, installed := range map[string][]plugin.InstalledPlugin{
		"nothing installed":     nil,
		"disabled":              {{Name: entry.PluginID, Enabled: false}},
		"not a reviewed plugin": {{Name: "someone-elses-plugin", Enabled: true}},
		"a plugin reads":        {readerPlugin(entry.PluginID, true, 262144, "reaper")},
	} {
		if got := ReaderUpdateName(installed); got != "" {
			t.Errorf("%s: %q, want nothing to update", name, got)
		}
	}
}

// G6: a made-up format becomes readable by a reviewed plugin declaring it.
func TestReaderStatus_FollowsWhatPluginsDeclare(t *testing.T) {
	reviewed := reviewedPluginID(t)
	if got := ReaderStatus([]plugin.InstalledPlugin{readerPlugin(reviewed, true, 262144, "zzfmt")}, "zzfmt"); got != ReaderReady {
		t.Fatalf("made-up format = %q", got)
	}
	if got := ReaderStatus(nil, "zzfmt"); got != ReaderNone {
		t.Fatalf("nothing installed = %q", got)
	}
	// FR49: the format's reviewed app plugin is installed but has no reader yet.
	entry, _ := reviewedintegration.Get("ori_reaper")
	old := plugin.InstalledPlugin{Name: entry.PluginID, Enabled: true}
	if got := ReaderStatus([]plugin.InstalledPlugin{old}, "reaper"); got != ReaderUpdatePlugin {
		t.Fatalf("installed without a reader = %q", got)
	}
}

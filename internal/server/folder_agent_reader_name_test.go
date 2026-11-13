package server

import (
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/folderagent"
	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
)

// assistant-reads-projects 6.4 (FR44): the folder agent's offer names the app
// plugin that reads the songs, from the installed plugins, and says nothing when
// none does.

var _ folderagent.SongsReaderNamer = folderAgentSongs{}
var _ folderagent.SongsReaders = folderAgentSongs{}

// FR49: the offer's place can tell a plugin that reads from one that needs an
// update, and the host reads nothing itself when it cannot tell.
func TestFolderAgentSongs_ReaderForSaysWhichPluginReadsAndWhichToUpdate(t *testing.T) {
	entry, ok := reviewedintegration.Get("ori_reaper")
	if !ok || entry.DisplayName == "" {
		t.Fatalf("no reviewed display name: %+v", entry)
	}
	reader := plugin.InstalledPlugin{Name: entry.PluginID, Enabled: true, WorkspaceSurfaces: &plugin.SurfaceContribution{
		Services: []plugin.ContributedService{{ID: "svc", Operations: []plugin.ContributedOperation{{
			ID: "project.facts.read", Policy: "read_only", TimeoutClass: "long", MaxOutputBytes: 262144}}}},
		ProjectFacts: &plugin.ProjectFactsRef{ServiceID: "svc", Operation: "project.facts.read", Formats: []string{"reaper"}},
	}}
	cases := map[string]struct {
		installed  func() ([]plugin.InstalledPlugin, error)
		wantStatus string
		wantName   string
	}{
		"a reviewed plugin with a reader": {func() ([]plugin.InstalledPlugin, error) { return []plugin.InstalledPlugin{reader}, nil }, folderagent.ReaderReady, ""},
		"the plugin without a reader yet": {func() ([]plugin.InstalledPlugin, error) {
			return []plugin.InstalledPlugin{{Name: entry.PluginID, Enabled: true}}, nil
		}, folderagent.ReaderUpdatePlugin, entry.DisplayName},
		"nothing installed":       {func() ([]plugin.InstalledPlugin, error) { return nil, nil }, "none", ""},
		"the list cannot be read": {func() ([]plugin.InstalledPlugin, error) { return []plugin.InstalledPlugin{reader}, errors.New("boom") }, "none", ""},
		"no way to list plugins":  {nil, "none", ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			status, plugin := (folderAgentSongs{installed: tc.installed}).ReaderFor("reaper")
			if status != tc.wantStatus || plugin != tc.wantName {
				t.Fatalf("ReaderFor(reaper) = %q, %q; want %q, %q", status, plugin, tc.wantStatus, tc.wantName)
			}
		})
	}
}

func TestFolderAgentSongs_ReaderNameIsTheReviewedListsNameForTheReadingPlugin(t *testing.T) {
	entry, ok := reviewedintegration.Get("ori_reaper")
	if !ok || entry.DisplayName == "" {
		t.Fatalf("no reviewed display name: %+v", entry)
	}
	reader := plugin.InstalledPlugin{Name: entry.PluginID, Enabled: true, WorkspaceSurfaces: &plugin.SurfaceContribution{
		Services: []plugin.ContributedService{{ID: "svc", Operations: []plugin.ContributedOperation{{
			ID: "project.facts.read", Policy: "read_only", TimeoutClass: "long", MaxOutputBytes: 262144}}}},
		ProjectFacts: &plugin.ProjectFactsRef{ServiceID: "svc", Operation: "project.facts.read", Formats: []string{"zzfmt"}},
	}}
	cases := map[string]struct {
		installed func() ([]plugin.InstalledPlugin, error)
		want      string
	}{
		"a reviewed plugin with a reader": {func() ([]plugin.InstalledPlugin, error) { return []plugin.InstalledPlugin{reader}, nil }, entry.DisplayName},
		"the plugin without a reader yet": {func() ([]plugin.InstalledPlugin, error) {
			return []plugin.InstalledPlugin{{Name: entry.PluginID, Enabled: true}}, nil
		}, ""},
		"nothing installed":       {func() ([]plugin.InstalledPlugin, error) { return nil, nil }, ""},
		"the list cannot be read": {func() ([]plugin.InstalledPlugin, error) { return []plugin.InstalledPlugin{reader}, errors.New("boom") }, ""},
		"no way to list plugins":  {nil, ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := (folderAgentSongs{installed: tc.installed}).ReaderName(); got != tc.want {
				t.Fatalf("ReaderName() = %q, want %q", got, tc.want)
			}
		})
	}
}

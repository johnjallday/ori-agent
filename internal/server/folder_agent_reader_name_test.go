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

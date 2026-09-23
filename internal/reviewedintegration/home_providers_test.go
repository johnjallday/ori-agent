package reviewedintegration

import (
	"errors"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
)

func TestBuiltInHomeProviderMatchesTheProjectIntegrationThatReferencesIt(t *testing.T) {
	provider, ok := HomeProviderFor(" Music-Project-Management ")
	if !ok {
		t.Fatal("reviewed Home provider is missing")
	}
	// The reviewed project integration references this exact Home program; a
	// provider for any other program could never satisfy it.
	integration, _ := Get("ori_reaper")
	if provider.ProgramID != integration.ExpectedProgramID || provider.HomeSchemaVersion != integration.ExpectedProgramSchema {
		t.Fatalf("Home provider %s/%d does not match the integration's Home %s/%d",
			provider.ProgramID, provider.HomeSchemaVersion, integration.ExpectedProgramID, integration.ExpectedProgramSchema)
	}
	if provider.MinimumVersion != "0.1.0" || provider.FallbackCommit != "5f748d2de4457ac9dd02ea1ec31e34e1493744cf" ||
		provider.ExpectedProtocol != plugin.SurfaceProtocolVersion {
		t.Fatalf("reviewed Home provider floor drifted: %#v", provider)
	}
	if strings.Join(provider.RequiredHostFeatures, ",") != plugin.HostFeatureIndependentProgramHomesV1 {
		t.Fatalf("required host features = %v", provider.RequiredHostFeatures)
	}
	release := provider.ReleaseEntry()
	if release.FallbackSource() != provider.SourceRepository+"#sha="+provider.FallbackCommit ||
		!release.IsPinnedSource(release.FallbackSource()) || !release.IsUnpinnedOfficialSource(provider.SourceRepository+".git") {
		t.Fatalf("release identity = %#v", release)
	}
	if provider.DisplayName != "Music Project Management" {
		t.Fatalf("display name = %q", provider.DisplayName)
	}
}

func TestHomeProvidersNeverAnswerForAProjectIntegration(t *testing.T) {
	for _, provider := range HomeProviders() {
		if _, ok := Get(provider.Key); ok {
			t.Errorf("Home provider key %q is also a project integration", provider.Key)
		}
		if _, ok := ForPlugin(provider.PluginID); ok {
			t.Errorf("Home provider plugin %q is also a project integration", provider.PluginID)
		}
	}
	for _, entry := range All() {
		if _, ok := HomeProviderFor(entry.PluginID); ok {
			t.Errorf("project integration %q is also a Home provider", entry.PluginID)
		}
	}
	integration, _ := Get("ori_reaper")
	collision := HomeProviders()[0]
	collision.PluginID = integration.PluginID
	assertPanics(t, "plugin ID shared with a project integration", func() {
		mustHomeProviders([]HomeProvider{collision}, []Entry{integration})
	})
	duplicate := HomeProviders()[0]
	assertPanics(t, "duplicate Home provider", func() {
		mustHomeProviders([]HomeProvider{duplicate, duplicate}, nil)
	})
}

func TestHomeProviderNormalizationRejectsInvalidEntries(t *testing.T) {
	base := HomeProviders()[0]
	cases := map[string]func(*HomeProvider){
		"empty display name":       func(provider *HomeProvider) { provider.DisplayName = " " },
		"markup display name":      func(provider *HomeProvider) { provider.DisplayName = "<b>Home</b>" },
		"url display name":         func(provider *HomeProvider) { provider.DisplayName = "https://example.invalid" },
		"multi-line display name":  func(provider *HomeProvider) { provider.DisplayName = "Home\nProvider" },
		"missing program":          func(provider *HomeProvider) { provider.ProgramID = "" },
		"missing Home schema":      func(provider *HomeProvider) { provider.HomeSchemaVersion = 0 },
		"missing protocol":         func(provider *HomeProvider) { provider.ExpectedProtocol = 0 },
		"invalid floor":            func(provider *HomeProvider) { provider.MinimumVersion = "latest" },
		"non-GitHub repository":    func(provider *HomeProvider) { provider.SourceRepository = "https://example.invalid/home" },
		"other format":             func(provider *HomeProvider) { provider.SourceFormat = plugin.FormatCodex },
		"short fallback commit":    func(provider *HomeProvider) { provider.FallbackCommit = "5f748d2" },
		"ready without a fallback": func(provider *HomeProvider) { provider.FallbackCommit = "" },
		"no host features":         func(provider *HomeProvider) { provider.RequiredHostFeatures = nil },
		"duplicate host feature": func(provider *HomeProvider) {
			provider.RequiredHostFeatures = []string{"independent_program_homes_v1", "independent_program_homes_v1"}
		},
		"missing publisher": func(provider *HomeProvider) { provider.PublisherLabel = "" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			provider := base.Clone()
			mutate(&provider)
			if _, err := normalizeHomeProvider(provider); err == nil {
				t.Fatal("invalid Home provider was accepted")
			}
		})
	}
	if _, err := normalizeHomeProvider(base.Clone()); err != nil {
		t.Fatalf("built-in Home provider no longer normalizes: %v", err)
	}
}

func TestHomeProviderCheckReleaseAcceptsOnlyTheReviewedHome(t *testing.T) {
	provider := HomeProviders()[0]
	const version = "0.2.0"
	source := provider.ReleaseEntry().PinnedSource(strings.Repeat("a", 40))
	loadable := func() (plugin.PluginDescriptor, plugin.TrustReport) {
		descriptor := plugin.PluginDescriptor{
			Name: provider.PluginID, Version: version, SourceLocation: source, SourceFormat: provider.SourceFormat,
			Skills: []plugin.SkillSpec{{Name: provider.PluginID}},
			WorkspaceSurfaces: &plugin.SurfaceContribution{
				Name: provider.PluginID, Version: version,
				Protocol:              plugin.ProtocolRange{Min: 1, Max: 1},
				RequiresHostFeatures:  []string{plugin.HostFeatureIndependentProgramHomesV1},
				AssistantProgramHomes: []projecttemplates.AssistantProgramHome{{ID: provider.ProgramID, SchemaVersion: provider.HomeSchemaVersion, Version: 1}},
			},
		}
		return descriptor, plugin.TrustReport{Name: provider.PluginID, Format: provider.SourceFormat}
	}
	descriptor, report := loadable()
	if got := provider.CheckRelease(version, source, descriptor, report, nil); got != ReleaseLoadable {
		t.Fatalf("reviewed Home release = %v, want loadable", got)
	}

	cases := map[string]struct {
		mutate func(*plugin.PluginDescriptor, *plugin.TrustReport)
		want   ReleaseCheck
	}{
		"another plugin":       {func(d *plugin.PluginDescriptor, _ *plugin.TrustReport) { d.Name = "other" }, ReleaseRejected},
		"another version":      {func(d *plugin.PluginDescriptor, _ *plugin.TrustReport) { d.Version = "0.3.0" }, ReleaseRejected},
		"another source":       {func(d *plugin.PluginDescriptor, _ *plugin.TrustReport) { d.SourceLocation = "elsewhere" }, ReleaseRejected},
		"trust report differs": {func(_ *plugin.PluginDescriptor, r *plugin.TrustReport) { r.Name = "other" }, ReleaseRejected},
		"no contribution":      {func(d *plugin.PluginDescriptor, _ *plugin.TrustReport) { d.WorkspaceSurfaces = nil }, ReleaseRejected},
		"newer protocol": {func(d *plugin.PluginDescriptor, _ *plugin.TrustReport) {
			d.WorkspaceSurfaces.Protocol = plugin.ProtocolRange{Min: 2, Max: 2}
		}, ReleaseNeedsNewerHost},
		"missing host feature": {func(d *plugin.PluginDescriptor, _ *plugin.TrustReport) {
			d.WorkspaceSurfaces.RequiresHostFeatures = nil
		}, ReleaseRejected},
		"another Home": {func(d *plugin.PluginDescriptor, _ *plugin.TrustReport) {
			d.WorkspaceSurfaces.AssistantProgramHomes[0].ID = "another-home"
		}, ReleaseRejected},
		"second Home": {func(d *plugin.PluginDescriptor, _ *plugin.TrustReport) {
			d.WorkspaceSurfaces.AssistantProgramHomes = append(d.WorkspaceSurfaces.AssistantProgramHomes, projecttemplates.AssistantProgramHome{ID: "second", SchemaVersion: 1})
		}, ReleaseRejected},
		"runs a service": {func(d *plugin.PluginDescriptor, _ *plugin.TrustReport) {
			d.WorkspaceSurfaces.Services = []plugin.ContributedService{{ID: "service"}}
		}, ReleaseRejected},
		"adds an MCP server": {func(d *plugin.PluginDescriptor, _ *plugin.TrustReport) {
			d.MCPServers = []plugin.MCPServerSpec{{Name: "server"}}
		}, ReleaseRejected},
		"contributes a blueprint": {func(d *plugin.PluginDescriptor, _ *plugin.TrustReport) {
			d.ResolvedBlueprints = []plugin.ResolvedBlueprint{{ID: "project"}}
		}, ReleaseRejected},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			descriptor, report := loadable()
			tc.mutate(&descriptor, &report)
			if got := provider.CheckRelease(version, source, descriptor, report, nil); got != tc.want {
				t.Fatalf("CheckRelease = %v, want %v", got, tc.want)
			}
		})
	}

	if got := provider.CheckRelease(version, source, plugin.PluginDescriptor{}, plugin.TrustReport{}, errors.New("clone failed")); got != ReleaseRejected {
		t.Fatalf("failed read = %v, want rejected", got)
	}
}

func assertPanics(t *testing.T, name string, fn func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Errorf("%s: expected a panic", name)
		}
	}()
	fn()
}

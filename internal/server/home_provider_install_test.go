package server

// A reviewed Home provider, installed from a split project blueprint's
// readiness card and then kept on its published releases.
//
// Nothing here reaches the network: the release list and each release's
// inspection are fixed, which is also why the confirmed-install success path
// is exercised by the live demo rather than here — a reviewed source is always
// an exact commit of the real repository.

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/integrationrelease"
	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
)

// homeReleases publishes a fixed list of Home provider releases, newest first.
// Each inspects as the reviewed Home unless a test says otherwise.
type homeReleases struct {
	provider   reviewedintegration.HomeProvider
	candidates []integrationrelease.Resolution
	newerHost  map[string]bool
	impostor   map[string]bool
	inspectErr map[string]error
	inspected  []string
}

func newHomeReleases(t *testing.T, versions ...string) *homeReleases {
	t.Helper()
	provider, ok := reviewedintegration.HomeProviderFor("music-project-management")
	if !ok {
		t.Fatal("reviewed Home provider fixture is missing")
	}
	releases := &homeReleases{provider: provider, newerHost: map[string]bool{}, impostor: map[string]bool{}, inspectErr: map[string]error{}}
	for index, version := range versions {
		commit := strings.Repeat(string(rune('a'+index)), 40)
		releases.candidates = append(releases.candidates, integrationrelease.Resolution{
			Version: version, Tag: "v" + version, Commit: commit, Source: provider.ReleaseEntry().PinnedSource(commit),
		})
	}
	return releases
}

func (releases *homeReleases) Resolve(ctx context.Context, entry reviewedintegration.Entry) integrationrelease.Resolution {
	return releases.Candidates(ctx, entry)[0]
}

func (releases *homeReleases) Candidates(context.Context, reviewedintegration.Entry) []integrationrelease.Resolution {
	return append([]integrationrelease.Resolution(nil), releases.candidates...)
}

func (releases *homeReleases) inspect(source string, _ plugin.SourceFormat) (plugin.PluginDescriptor, plugin.TrustReport, error) {
	releases.inspected = append(releases.inspected, source)
	if err := releases.inspectErr[source]; err != nil {
		return plugin.PluginDescriptor{}, plugin.TrustReport{}, err
	}
	version := ""
	for _, candidate := range releases.candidates {
		if candidate.Source == source {
			version = candidate.Version
		}
	}
	provider := releases.provider
	descriptor := plugin.PluginDescriptor{
		Name: provider.PluginID, Version: version, SourceLocation: source, SourceFormat: provider.SourceFormat,
		Skills: []plugin.SkillSpec{{Name: provider.PluginID}},
		WorkspaceSurfaces: &plugin.SurfaceContribution{
			Name: provider.PluginID, Version: version,
			Protocol:              plugin.ProtocolRange{Min: provider.ExpectedProtocol, Max: provider.ExpectedProtocol},
			RequiresHostFeatures:  append([]string(nil), provider.RequiredHostFeatures...),
			AssistantProgramHomes: []projecttemplates.AssistantProgramHome{{ID: provider.ProgramID, SchemaVersion: provider.HomeSchemaVersion, Version: 1}},
		},
	}
	if releases.newerHost[source] {
		descriptor.WorkspaceSurfaces.Protocol = plugin.ProtocolRange{Min: provider.ExpectedProtocol + 1, Max: provider.ExpectedProtocol + 1}
	}
	if releases.impostor[source] {
		descriptor.WorkspaceSurfaces.Services = []plugin.ContributedService{{ID: "service"}}
	}
	return descriptor, plugin.BuildTrustReport(descriptor), nil
}

func (releases *homeReleases) updates() *reviewedIntegrationUpdates {
	return &reviewedIntegrationUpdates{
		releases: releases, entryFor: reviewedintegration.ForPlugin, homeProviderFor: reviewedintegration.HomeProviderFor,
		inspect: releases.inspect, platform: "darwin/arm64",
	}
}

func TestReviewedHomeProviderUpdatesFollowItsPublishedReleases(t *testing.T) {
	releases := newHomeReleases(t, "0.3.0", "0.2.0", "0.1.0")
	releases.newerHost[releases.candidates[0].Source] = true
	updates := releases.updates()
	provider := releases.provider
	pinned := plugin.InstalledPlugin{
		Name: provider.PluginID, Version: "0.1.0", Format: provider.SourceFormat,
		Source: provider.ReleaseEntry().PinnedSource(provider.FallbackCommit),
	}

	// 0.3.0 needs a newer host, so the newest release this build can load is
	// offered instead — never the one it would refuse to install.
	result, handled, err := updates.availability(pinned)
	if err != nil || !handled || !result.Available || !result.ReviewedRelease || result.AvailableVersion != "0.2.0" {
		t.Fatalf("availability = %#v handled=%v err=%v", result, handled, err)
	}
	if replacement := updates.replacement(context.Background(), pinned); replacement.Source != releases.candidates[1].Source {
		t.Fatalf("replacement = %#v, want the 0.2.0 commit", replacement)
	}

	current := pinned
	current.Version = "0.2.0"
	if result, handled, err := updates.availability(current); err != nil || !handled || result.Available {
		t.Fatalf("an up-to-date Home provider was offered an update: %#v %v %v", result, handled, err)
	}

	// A release that is no longer only a Home is refused outright, not
	// stepped over to an older one.
	releases.newerHost = map[string]bool{}
	releases.impostor[releases.candidates[0].Source] = true
	if _, handled, err := updates.availability(pinned); !handled || err == nil {
		t.Fatalf("a release beyond a Home was not refused: handled=%v err=%v", handled, err)
	}

	// A local copy keeps following its recorded source.
	local := pinned
	local.Source = filepath.Join(t.TempDir(), "music")
	if _, handled, _ := updates.availability(local); handled {
		t.Fatal("a local Home provider copy was treated as reviewed")
	}
}

// homeRecoveryServer serves one plugin-owned project blueprint whose project
// joins a Home the reviewed provider supplies. The provider is not installed.
func homeRecoveryServer(t *testing.T, releases *homeReleases) (*Server, string) {
	t.Helper()
	project := endpointPluginRecord(t, "project-plugin", "song", true, availableArtifacts())
	project.WorkspaceSurfaces.RequiresHostFeatures = []string{plugin.HostFeatureIndependentProgramHomesV1}
	blueprint := &project.ResolvedBlueprints[0].Template
	blueprint.Name = "Song"
	blueprint.GroupRequirement = &projecttemplates.GroupRequirement{
		SchemaVersion: 2, Policy: projecttemplates.GroupPolicyRequired, DefaultHomeName: "Studio Home",
	}
	blueprint.AssistantProject = &projecttemplates.AssistantProjectDeclaration{
		SchemaVersion: projecttemplates.AssistantProjectSchemaVersion, Version: 1, ID: "song-team",
		Home: projecttemplates.AssistantProjectHomeReference{
			ProviderPluginID: "music-project-management", ProgramID: "music-producer-assistant",
			HomeSchemaVersion: 1, MinHomeVersion: 1, MaxHomeVersion: 1,
		},
	}
	s := catalogEndpointServer(t, t.TempDir(), filepath.Join(t.TempDir(), "plugins"), []plugin.InstalledPlugin{project})
	if releases != nil {
		s.reviewedReleases = releases.updates()
	}
	return s, project.ResolvedBlueprints[0].QualifiedID
}

func installedPluginNames(t *testing.T, s *Server) []string {
	t.Helper()
	list, err := s.Handlers.Plugin.Manager().List()
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(list))
	for _, installed := range list {
		names = append(names, installed.Name)
	}
	return names
}

func TestHomeProviderRecoveryPreviewDisclosesTheReviewedRelease(t *testing.T) {
	releases := newHomeReleases(t, "0.2.0", "0.1.0")
	s, blueprintID := homeRecoveryServer(t, releases)

	w, resp := postRecovery(t, s, blueprintID,
		`{"action":"install_plugin","plugin":"music-project-management","confirm":false}`)
	if w.Code != http.StatusOK {
		t.Fatalf("preview = %d: %s", w.Code, w.Body.String())
	}
	if resp["trust"] == nil || resp["source"] != releases.candidates[0].Source || resp["release"] != "0.2.0" {
		t.Fatalf("preview did not disclose the newest reviewed release: %v", resp)
	}
	readiness, _ := resp["readiness"].(map[string]any)
	dependency, _ := readiness["dependency"].(map[string]any)
	if readiness["summary"] != "Song needs Studio Home, which comes from a separate plugin." ||
		dependency["display_name"] != "Music Project Management" {
		t.Fatalf("readiness = %v", readiness)
	}
	if names := installedPluginNames(t, s); len(names) != 1 {
		t.Fatalf("a preview installed something: %v", names)
	}
}

func TestHomeProviderRecoveryRefusesAConfirmationForAnotherRelease(t *testing.T) {
	releases := newHomeReleases(t, "0.2.0", "0.1.0")
	s, blueprintID := homeRecoveryServer(t, releases)

	for _, body := range []string{
		`{"action":"install_plugin","plugin":"music-project-management","confirm":true,"release":"0.1.0"}`,
		`{"action":"install_plugin","plugin":"music-project-management","confirm":true}`,
	} {
		w, resp := postRecovery(t, s, blueprintID, body)
		if w.Code != http.StatusConflict {
			t.Fatalf("%s = %d: %s", body, w.Code, w.Body.String())
		}
		outcome, _ := resp["outcome"].(map[string]any)
		if summary, _ := outcome["summary"].(string); !strings.Contains(summary, "appeared while you were reviewing") {
			t.Fatalf("stale release copy = %q", summary)
		}
		if resp["source"] != nil || resp["release"] != nil {
			t.Fatalf("a refusal disclosed a source: %v", resp)
		}
	}
	if names := installedPluginNames(t, s); len(names) != 1 {
		t.Fatalf("a refused confirmation installed something: %v", names)
	}
}

func TestHomeProviderRecoveryWithoutAVerifiableReleaseInstallsNothing(t *testing.T) {
	releases := newHomeReleases(t, "0.2.0")
	releases.impostor[releases.candidates[0].Source] = true
	s, blueprintID := homeRecoveryServer(t, releases)
	unwired, unwiredID := homeRecoveryServer(t, nil)

	for _, tc := range []struct {
		server *Server
		id     string
	}{{s, blueprintID}, {unwired, unwiredID}} {
		w, resp := postRecovery(t, tc.server, tc.id,
			`{"action":"install_plugin","plugin":"music-project-management","confirm":false}`)
		if w.Code != http.StatusConflict {
			t.Fatalf("unverifiable release = %d: %s", w.Code, w.Body.String())
		}
		outcome, _ := resp["outcome"].(map[string]any)
		if summary, _ := outcome["summary"].(string); summary != "Ori could not find a release of Music Project Management to install." {
			t.Fatalf("summary = %q", summary)
		}
		if resp["trust"] != nil || strings.Contains(w.Body.String(), "://") {
			t.Fatalf("an unverified release was disclosed: %s", w.Body.String())
		}
	}

	releases.impostor = map[string]bool{}
	releases.inspectErr[releases.candidates[0].Source] = errors.New("clone /private/path failed")
	w, _ := postRecovery(t, s, blueprintID,
		`{"action":"install_plugin","plugin":"music-project-management","confirm":false}`)
	if w.Code != http.StatusConflict || strings.Contains(w.Body.String(), "/private/path") {
		t.Fatalf("a failed read leaked detail or was accepted: %d %s", w.Code, w.Body.String())
	}
}

func TestBlueprintRecoveryAcceptsOnlyThisBlueprintsHomeProvider(t *testing.T) {
	releases := newHomeReleases(t, "0.2.0")
	s, blueprintID := homeRecoveryServer(t, releases)

	// Another blueprint's Home provider, or any other reviewed plugin, is not
	// this blueprint's dependency.
	for _, name := range []string{"reaper-plugin", "other-home"} {
		w, _ := postRecovery(t, s, blueprintID,
			`{"action":"install_plugin","plugin":"`+name+`","confirm":false}`)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s = %d, want 400: %s", name, w.Code, w.Body.String())
		}
	}
	if len(releases.inspected) != 0 {
		t.Fatalf("an undeclared plugin was inspected: %v", releases.inspected)
	}
}

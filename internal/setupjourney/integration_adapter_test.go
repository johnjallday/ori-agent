package setupjourney

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/integrationrelease"
	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
	"github.com/johnjallday/ori-agent/internal/specialist"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type fakeReviewedIntegrationManager struct {
	installed       []plugin.InstalledPlugin
	descriptor      plugin.PluginDescriptor
	report          plugin.TrustReport
	listErr         error
	inspectErr      error
	inspections     int
	inspectSources  []string
	updateCalls     int
	updateErr       error
	installCalls    int
	confirmAccepted bool
}

func (manager *fakeReviewedIntegrationManager) List() ([]plugin.InstalledPlugin, error) {
	return append([]plugin.InstalledPlugin(nil), manager.installed...), manager.listErr
}
func (manager *fakeReviewedIntegrationManager) Inspect(source string, _ plugin.SourceFormat) (plugin.PluginDescriptor, plugin.TrustReport, error) {
	manager.inspections++
	manager.inspectSources = append(manager.inspectSources, source)
	return manager.descriptor, manager.report, manager.inspectErr
}
func (manager *fakeReviewedIntegrationManager) Install(source string, format plugin.SourceFormat, confirm plugin.ConfirmFunc) (plugin.InstalledPlugin, error) {
	manager.installCalls++
	manager.confirmAccepted = confirm != nil && confirm(manager.report)
	if !manager.confirmAccepted {
		return plugin.InstalledPlugin{}, plugin.ErrInstallDeclined
	}
	installed := installedFromFixture(manager.descriptor, source, format, false, 1)
	manager.installed = []plugin.InstalledPlugin{installed}
	return installed, nil
}
func (manager *fakeReviewedIntegrationManager) SetEnabled(name string, enabled bool) error {
	for index := range manager.installed {
		if manager.installed[index].Name == name {
			manager.installed[index].Enabled = enabled
			return nil
		}
	}
	return errors.New("not installed")
}
func (manager *fakeReviewedIntegrationManager) UpdateFromSource(_ string, source string, format plugin.SourceFormat, confirm plugin.ConfirmFunc) (plugin.InstalledPlugin, error) {
	manager.updateCalls++
	if manager.updateErr != nil {
		return plugin.InstalledPlugin{}, manager.updateErr
	}
	if confirm == nil || !confirm(manager.report) {
		return plugin.InstalledPlugin{}, plugin.ErrInstallDeclined
	}
	enabled := len(manager.installed) == 1 && manager.installed[0].Enabled
	installed := installedFromFixture(manager.descriptor, source, format, enabled, 2)
	manager.installed = []plugin.InstalledPlugin{installed}
	return installed, nil
}

func installedFromFixture(descriptor plugin.PluginDescriptor, source string, format plugin.SourceFormat, enabled bool, generation uint64) plugin.InstalledPlugin {
	return plugin.InstalledPlugin{
		Name: descriptor.Name, Version: descriptor.Version, Source: source, Format: format,
		WorkspaceSurfaces: descriptor.WorkspaceSurfaces, ResolvedBlueprints: descriptor.ResolvedBlueprints,
		ComponentFingerprint: "trusted", Generation: generation, Enabled: enabled,
		InstalledAt: time.Now().UTC(),
	}
}

func readyIntegrationFixture(t *testing.T) (reviewedintegration.Entry, plugin.PluginDescriptor, plugin.TrustReport, ReadScope) {
	t.Helper()
	entry := reviewedintegration.Entry{
		Key: "ori_reaper", PluginID: "reaper-plugin", MinimumVersion: "0.5.0",
		SourceRepository: "https://github.com/example/reaper-plugin",
		FallbackCommit:   strings.Repeat("a", 40), SourceFormat: plugin.FormatClaude,
		PublisherLabel: "Ori", SourceLabel: "example/reaper-plugin",
		ExpectedBlueprintID: "reaper-song", MinimumBlueprintVersion: 4,
		ExpectedProgramID: "music-producer-assistant", ExpectedProgramSchema: 2,
		RequiredHostFeatures: []string{plugin.HostFeatureAssistantProgramV1, plugin.HostFeatureSpecialistSetupJourneyV1},
		ExpectedProtocol:     1, SupportedPlatforms: []string{"darwin/arm64"}, ReleaseReady: true,
	}
	descriptor := plugin.PluginDescriptor{
		Name: entry.PluginID, Version: entry.MinimumVersion, SourceLocation: entry.FallbackSource(),
		SourceFormat: plugin.FormatClaude, InstallDir: t.TempDir(),
		WorkspaceSurfaces: &plugin.SurfaceContribution{
			Name: entry.PluginID, Version: entry.MinimumVersion,
			Protocol:             plugin.ProtocolRange{Min: 1, Max: 1},
			RequiresHostFeatures: append([]string(nil), entry.RequiredHostFeatures...),
			Services: []plugin.ContributedService{{
				ID: "reaper-service", Artifacts: []plugin.ContributedArtifact{{
					ID: "service", OS: "darwin", Arch: "arm64", Size: 20,
					SHA256: strings.Repeat("b", 64),
					Source: plugin.ArtifactSource{Kind: "url", URL: "https://example.invalid/reaper-service"},
				}},
			}},
		},
		ResolvedBlueprints: []plugin.ResolvedBlueprint{{
			ID: entry.ExpectedBlueprintID, Version: entry.MinimumBlueprintVersion,
			Template: projecttemplates.Template{AssistantProgram: &workspace.AssistantProgramDeclaration{
				ID: entry.ExpectedProgramID, SchemaVersion: entry.ExpectedProgramSchema,
			}},
		}},
	}
	report := plugin.BuildTrustReport(descriptor)
	scope := ReadScope{
		IntegrationKey: entry.Key, ExpectedBlueprintID: entry.ExpectedBlueprintID,
		ExpectedAssistantProgramID: entry.ExpectedProgramID,
	}
	return entry, descriptor, report, scope
}

func integrationResolver(entry reviewedintegration.Entry) IntegrationEntryResolver {
	return func(key string) (reviewedintegration.Entry, bool) {
		return entry.Clone(), key == entry.Key
	}
}

// stubReleases is a controllable latest-release resolver that counts lookups.
type stubReleases struct {
	mu         sync.Mutex
	resolution integrationrelease.Resolution
	calls      int
}

func (stub *stubReleases) Resolve(_ context.Context, _ reviewedintegration.Entry) integrationrelease.Resolution {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	stub.calls++
	return stub.resolution
}

func (stub *stubReleases) set(resolution integrationrelease.Resolution) {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	stub.resolution = resolution
}

func (stub *stubReleases) callCount() int {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	return stub.calls
}

// latestRelease is a checked resolution of one official release.
func latestRelease(entry reviewedintegration.Entry, version, commitChar string) integrationrelease.Resolution {
	commit := strings.Repeat(commitChar, 40)
	return integrationrelease.Resolution{
		Version: version, Tag: "v" + version, Commit: commit, Source: entry.PinnedSource(commit),
		CheckedAt: time.Now().UTC(),
	}
}

// releaseDescriptor re-labels the fixture descriptor as another release, so
// the fake manager inspects and installs that release.
func releaseDescriptor(descriptor plugin.PluginDescriptor, version, source string) (plugin.PluginDescriptor, plugin.TrustReport) {
	release := descriptor
	contribution := *descriptor.WorkspaceSurfaces
	contribution.Version = version
	release.WorkspaceSurfaces = &contribution
	release.Version = version
	release.SourceLocation = source
	release.ResolvedBlueprints = append([]plugin.ResolvedBlueprint(nil), descriptor.ResolvedBlueprints...)
	return release, plugin.BuildTrustReport(release)
}

func TestReviewedIntegrationFreshInstallTargetsTheLatestRelease(t *testing.T) {
	entry, descriptor, _, scope := readyIntegrationFixture(t)
	target := latestRelease(entry, "0.6.0", "e")
	latest, latestReport := releaseDescriptor(descriptor, target.Version, target.Source)
	manager := &fakeReviewedIntegrationManager{descriptor: latest, report: latestReport}
	releases := &stubReleases{resolution: target}
	adapter := newReviewedIntegrationAdapter(manager, integrationResolver(entry), "darwin/arm64")
	adapter.releases = releases

	read, err := adapter.Read(context.Background(), scope)
	if err != nil || read.BlockedReason != "" || !containsAction(read.AvailableActions, ActionReviewInstall) {
		t.Fatalf("latest release was not offered: %#v err=%v", read, err)
	}
	integration := read.Integration
	if integration.ExpectedVersion != "0.6.0" || integration.MinimumVersion != "0.5.0" || !integration.ReleaseChecked ||
		len(manager.inspectSources) != 1 || manager.inspectSources[0] != target.Source {
		t.Fatalf("install offer did not target the latest release: %#v inspected=%v", integration, manager.inspectSources)
	}
	encoded, _ := json.Marshal(integration)
	if strings.Contains(string(encoded), target.Commit) || !strings.Contains(string(encoded), `"minimum_version":"0.5.0"`) ||
		!strings.Contains(string(encoded), `"release_checked":true`) {
		t.Fatalf("projection JSON = %s", encoded)
	}
	prepared, err := adapter.PrepareCommit(context.Background(), scope, ActionInstall, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Commit(context.Background(), scope, ActionInstall, json.RawMessage(`{}`), prepared); err != nil {
		t.Fatal(err)
	}
	if len(manager.installed) != 1 || manager.installed[0].Source != target.Source || manager.installed[0].Version != "0.6.0" {
		t.Fatalf("install did not record the latest release's exact commit: %#v", manager.installed)
	}
	after, err := adapter.Read(context.Background(), scope)
	if err != nil || !after.Integration.Verified || after.Integration.ExpectedVersion != "0.6.0" ||
		!containsAction(after.AvailableActions, ActionReviewEnable) || !adapter.ConsequenceObserved(ActionInstall, after) {
		t.Fatalf("installed latest release was not verified: %#v err=%v", after, err)
	}
}

func TestReviewedIntegrationFallbackInstallsTheFloorAndSaysSo(t *testing.T) {
	entry, descriptor, report, scope := readyIntegrationFixture(t)
	manager := &fakeReviewedIntegrationManager{descriptor: descriptor, report: report}
	adapter := newReviewedIntegrationAdapter(manager, integrationResolver(entry), "darwin/arm64")
	adapter.releases = &stubReleases{resolution: integrationrelease.Floor(entry)}

	read, err := adapter.Read(context.Background(), scope)
	if err != nil || !containsAction(read.AvailableActions, ActionReviewInstall) ||
		read.Integration.ExpectedVersion != entry.MinimumVersion || read.Integration.ReleaseChecked {
		t.Fatalf("fallback install was not the floor with release_checked=false: %#v err=%v", read.Integration, err)
	}
	prepared, err := adapter.PrepareCommit(context.Background(), scope, ActionInstall, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Commit(context.Background(), scope, ActionInstall, json.RawMessage(`{}`), prepared); err != nil ||
		manager.installed[0].Source != entry.FallbackSource() {
		t.Fatalf("fallback install source = %#v err=%v", manager.installed, err)
	}
}

func TestReviewedIntegrationRejectsResolverTargetsOutsideTheReviewedRepositoryOrFloor(t *testing.T) {
	entry, descriptor, report, scope := readyIntegrationFixture(t)
	for name, target := range map[string]integrationrelease.Resolution{
		"below the floor":  latestRelease(entry, "0.4.9", "e"),
		"other repository": {Version: "0.6.0", Commit: strings.Repeat("e", 40), Source: "https://github.com/attacker/reaper-plugin#sha=" + strings.Repeat("e", 40)},
		"mutable source":   {Version: "0.6.0", Source: entry.SourceRepository + ".git"},
	} {
		t.Run(name, func(t *testing.T) {
			manager := &fakeReviewedIntegrationManager{descriptor: descriptor, report: report}
			adapter := newReviewedIntegrationAdapter(manager, integrationResolver(entry), "darwin/arm64")
			adapter.releases = &stubReleases{resolution: target}
			read, err := adapter.Read(context.Background(), scope)
			if err != nil || read.BlockedReason != ReasonOwnerUnavailable || manager.inspections != 0 || len(read.AvailableActions) != 0 {
				t.Fatalf("untrusted resolver target was inspected or offered: %#v err=%v", read, err)
			}
		})
	}
}

func TestReviewedIntegrationBlueprintVersionIsAFloor(t *testing.T) {
	entry, descriptor, _, scope := readyIntegrationFixture(t)
	for version, want := range map[int]ReasonCode{
		entry.MinimumBlueprintVersion + 1: "",
		entry.MinimumBlueprintVersion - 1: ReasonIntegrationUnsupported,
	} {
		release, _ := releaseDescriptor(descriptor, entry.MinimumVersion, entry.FallbackSource())
		release.ResolvedBlueprints[0].Version = version
		manager := &fakeReviewedIntegrationManager{descriptor: release, report: plugin.BuildTrustReport(release)}
		read, err := newReviewedIntegrationAdapter(manager, integrationResolver(entry), "darwin/arm64").Read(context.Background(), scope)
		if err != nil || read.BlockedReason != want {
			t.Fatalf("blueprint version %d: reason = %q, want %q (err=%v)", version, read.BlockedReason, want, err)
		}
	}
}

func TestReviewedIntegrationReviewGoesStaleWhenTheLatestReleaseChanges(t *testing.T) {
	entry, descriptor, _, _ := readyIntegrationFixture(t)
	target := latestRelease(entry, "0.6.0", "e")
	latest, latestReport := releaseDescriptor(descriptor, target.Version, target.Source)
	manager := &fakeReviewedIntegrationManager{descriptor: latest, report: latestReport}
	releases := &stubReleases{resolution: target}
	adapter := newReviewedIntegrationAdapter(manager, integrationResolver(entry), "darwin/arm64")
	adapter.releases = releases
	service := integrationServiceForReplacementTest(t, adapter)
	ctx := context.Background()
	journey, err := service.Read(ctx, "local", "")
	if err != nil {
		t.Fatal(err)
	}
	review, err := service.Mutate(ctx, "local", journey.RunID, ActionReviewInstall, ActionMutation{
		IfRevision: journey.StateRevision, IdempotencyKey: "review-latest", Input: json.RawMessage(`{}`),
	})
	if err != nil || review.Review == nil || review.Review.Integration.ExpectedVersion != "0.6.0" {
		t.Fatalf("latest release review: %#v err=%v", review, err)
	}
	newer := latestRelease(entry, "0.6.1", "f")
	releases.set(newer)
	manager.descriptor, manager.report = releaseDescriptor(descriptor, newer.Version, newer.Source)
	_, err = service.Mutate(ctx, "local", journey.RunID, ActionInstall, ActionMutation{
		IfRevision: journey.StateRevision, IdempotencyKey: "commit-latest",
		ReviewToken: review.Review.Token, Input: json.RawMessage(`{}`),
	})
	var failure *Failure
	if !errors.As(err, &failure) || failure.ReasonCode != ReasonReviewStale || manager.installCalls != 0 {
		t.Fatalf("review of a superseded release was committed: failure=%#v err=%v installs=%d", failure, err, manager.installCalls)
	}
}

func TestReviewedIntegrationCommitInstallsTheReviewedSourceNotAFreshResolution(t *testing.T) {
	entry, descriptor, _, scope := readyIntegrationFixture(t)
	target := latestRelease(entry, "0.6.0", "e")
	latest, latestReport := releaseDescriptor(descriptor, target.Version, target.Source)
	manager := &fakeReviewedIntegrationManager{descriptor: latest, report: latestReport}
	releases := &stubReleases{resolution: target}
	adapter := newReviewedIntegrationAdapter(manager, integrationResolver(entry), "darwin/arm64")
	adapter.releases = releases
	prepared, err := adapter.PrepareCommit(context.Background(), scope, ActionInstall, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	releases.set(latestRelease(entry, "0.6.1", "f"))
	calls := releases.callCount()
	if _, err := adapter.Commit(context.Background(), scope, ActionInstall, json.RawMessage(`{}`), prepared); err != nil {
		t.Fatal(err)
	}
	if manager.installed[0].Source != target.Source || releases.callCount() != calls {
		t.Fatalf("commit re-resolved instead of installing the reviewed source: %#v resolves=%d", manager.installed, releases.callCount()-calls)
	}
	// Material without a reviewed source (for example forged or from another
	// read) is refused rather than guessed.
	prepared.Integration.reviewedSource = ""
	manager.installed = nil
	if _, err := adapter.Commit(context.Background(), scope, ActionInstall, json.RawMessage(`{}`), prepared); !errors.Is(err, ErrConflict) || manager.installCalls != 1 {
		t.Fatalf("commit without a reviewed source = %v installs=%d", err, manager.installCalls)
	}
}

// TestReviewedIntegrationReadSeparatesReviewedSoftwareFromUserTemplateTarget
// proves a host-reviewed key still owns software identity while the bound local
// template independently owns blueprint/program target identity.
func TestReviewedIntegrationReadSeparatesReviewedSoftwareFromUserTemplateTarget(t *testing.T) {
	entry, descriptor, report, scope := readyIntegrationFixture(t)
	manager := &fakeReviewedIntegrationManager{descriptor: descriptor, report: report}
	adapter := newReviewedIntegrationAdapter(manager, integrationResolver(entry), "darwin/arm64")
	scope.QuestSource = QuestSourceUserTemplate
	scope.UserTemplateID = "user-setup-quest-eligible"
	scope.ExpectedBlueprintID = "user-setup-quest-eligible"
	scope.ExpectedAssistantProgramID = "user-music-team"

	read, err := adapter.Read(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	if read.BlockedReason != "" || len(read.AvailableActions) != 1 || read.AvailableActions[0] != ActionReviewInstall || manager.inspections != 1 {
		t.Fatalf("reviewed software was not kept independent: read=%+v inspections=%d", read, manager.inspections)
	}
}

func TestReviewedIntegrationReadAbsentSurfacesExactReview(t *testing.T) {
	entry, descriptor, report, scope := readyIntegrationFixture(t)
	manager := &fakeReviewedIntegrationManager{descriptor: descriptor, report: report}
	adapter := newReviewedIntegrationAdapter(manager, integrationResolver(entry), "darwin/arm64")
	read, err := adapter.Read(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	if read.Complete || read.BlockedReason != "" || len(read.AvailableActions) != 1 ||
		read.AvailableActions[0] != ActionReviewInstall {
		t.Fatalf("unexpected absent read: %#v", read)
	}
	if read.Integration == nil || read.Integration.Trust == nil ||
		read.Integration.ExpectedVersion != "0.5.0" || read.Integration.StateRevision == "" {
		t.Fatalf("missing reviewed disclosure: %#v", read.Integration)
	}
	// Without a latest-release resolver the adapter installs the floor and
	// reports that no release was checked.
	if read.Integration.MinimumVersion != "0.5.0" || read.Integration.ReleaseChecked {
		t.Fatalf("floor target not disclosed: %#v", read.Integration)
	}
	// The reviewed repository is disclosed so a person can inspect it.
	if read.Integration.SourceURL != "https://github.com/example/reaper-plugin" {
		t.Fatalf("source URL = %q", read.Integration.SourceURL)
	}
}

func TestIntegrationSourceURLAcceptsOnlyPlainHTTPSLinks(t *testing.T) {
	for value, want := range map[string]bool{
		"": true,
		"https://github.com/example/reaper-plugin": true,
		"http://github.com/example/reaper-plugin":  false,
		"javascript:alert(1)":                      false,
		"https://user:pass@github.com/example":     false,
		"https://github.com/example?x=1":           false,
		"https://github.com/example#readme":        false,
		"https:///example":                         false,
		"github.com/example/reaper-plugin":         false,
		"https://github.com/exa mple":              false,
	} {
		if got := safeIntegrationSourceURL(value); got != want {
			t.Errorf("safeIntegrationSourceURL(%q) = %v, want %v", value, got, want)
		}
	}
}

func TestReviewedIntegrationReadInstalledSeparatesEnablement(t *testing.T) {
	entry, descriptor, report, scope := readyIntegrationFixture(t)
	installed := plugin.InstalledPlugin{
		Name: entry.PluginID, Version: entry.MinimumVersion, Source: entry.FallbackSource(),
		Format: entry.SourceFormat, WorkspaceSurfaces: descriptor.WorkspaceSurfaces,
		ResolvedBlueprints: descriptor.ResolvedBlueprints, ComponentFingerprint: "trusted",
		Generation: 7, Enabled: false, InstalledAt: time.Now().UTC(),
	}
	manager := &fakeReviewedIntegrationManager{installed: []plugin.InstalledPlugin{installed}, descriptor: descriptor, report: report}
	adapter := newReviewedIntegrationAdapter(manager, integrationResolver(entry), "darwin/arm64")
	read, err := adapter.Read(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	if read.Complete || !read.Integration.Verified || read.BlockedReason != "" || !containsAction(read.AvailableActions, ActionReviewEnable) {
		t.Fatalf("disabled installation was not independently actionable: %#v", read)
	}
	manager.installed[0].Enabled = true
	read, err = adapter.Read(context.Background(), scope)
	if err != nil || !read.Complete || !containsAction(read.AvailableActions, ActionManageIntegration) {
		t.Fatalf("enabled reviewed installation not complete: %#v err=%v", read, err)
	}
	if read.Result.IntegrationPluginID != entry.PluginID || read.Result.OwnerRevisions[0].Revision != 7 {
		t.Fatalf("canonical plugin receipt missing: %#v", read.Result)
	}
}

func TestReviewedIntegrationReadOffersPinnedUpdateForOlderAcceptedInstall(t *testing.T) {
	entry, descriptor, report, scope := readyIntegrationFixture(t)
	installed := plugin.InstalledPlugin{
		Name: entry.PluginID, Version: "0.4.1",
		Source: entry.SourceRepository + "#sha=" + strings.Repeat("c", 40),
		Format: entry.SourceFormat, Generation: 2, ComponentFingerprint: "older",
	}
	manager := &fakeReviewedIntegrationManager{installed: []plugin.InstalledPlugin{installed}, descriptor: descriptor, report: report}
	adapter := newReviewedIntegrationAdapter(manager, integrationResolver(entry), "darwin/arm64")
	read, err := adapter.Read(context.Background(), scope)
	if err != nil || read.BlockedReason != "" || !containsAction(read.AvailableActions, ActionReviewUpdate) ||
		read.Integration == nil || read.Integration.Trust == nil {
		t.Fatalf("older accepted integration did not offer reviewed replacement: %#v err=%v", read, err)
	}
}

func TestReviewedIntegrationUpdateReviewIsNonMutatingAndCommitUsesPinnedReplacement(t *testing.T) {
	entry, descriptor, report, scope := readyIntegrationFixture(t)
	olderSource := entry.SourceRepository + "#sha=" + strings.Repeat("c", 40)
	manager := &fakeReviewedIntegrationManager{
		installed: []plugin.InstalledPlugin{{
			Name: entry.PluginID, Version: "0.4.1", Source: olderSource,
			Format: entry.SourceFormat, Generation: 1, ComponentFingerprint: "older", Enabled: true,
		}},
		descriptor: descriptor, report: report,
	}
	adapter := newReviewedIntegrationAdapter(manager, integrationResolver(entry), "darwin/arm64")
	reviewed, err := adapter.Review(context.Background(), scope, ActionReviewUpdate, json.RawMessage(`{}`))
	if err != nil || reviewed.CommitAction != ActionUpdate || manager.installed[0].Source != olderSource {
		t.Fatalf("update review mutated canonical owner: %#v err=%v plugins=%#v", reviewed, err, manager.installed)
	}
	prepared, err := adapter.PrepareCommit(context.Background(), scope, ActionUpdate, json.RawMessage(`{}`))
	if err != nil || prepared.DisclosureDigest != reviewed.DisclosureDigest {
		t.Fatalf("update review changed before commit: %#v err=%v", prepared, err)
	}
	result, err := adapter.Commit(context.Background(), scope, ActionUpdate, json.RawMessage(`{}`), prepared)
	if err != nil || result.IntegrationVersion != entry.MinimumVersion ||
		manager.installed[0].Source != entry.FallbackSource() || !manager.installed[0].Enabled {
		t.Fatalf("pinned update did not preserve enablement: %#v err=%v plugins=%#v", result, err, manager.installed)
	}
}

func TestReviewedIntegrationReadFailsClosedForIdentityAndContributionMismatch(t *testing.T) {
	entry, descriptor, report, scope := readyIntegrationFixture(t)
	t.Run("explicit development source can satisfy the prerequisite without claiming a release", func(t *testing.T) {
		developmentEntry := entry
		developmentEntry.ReleaseReady = false
		source := t.TempDir()
		manager := &fakeReviewedIntegrationManager{installed: []plugin.InstalledPlugin{
			installedFromFixture(descriptor, source, entry.SourceFormat, true, 1),
		}}
		adapter := newReviewedIntegrationAdapter(manager, integrationResolver(developmentEntry), "darwin/arm64")
		adapter.developmentSource = normalizedLocalDevelopmentSource(source)
		read, err := adapter.Read(context.Background(), scope)
		if err != nil || !read.Complete || read.BlockedReason != "" || manager.inspections != 0 {
			t.Fatalf("explicit development copy was not accepted: %#v err=%v", read, err)
		}
		if read.Integration == nil || !read.Integration.DevelopmentCopy || read.Integration.ReleaseReady || read.Integration.Verified {
			t.Fatalf("development provenance was not retained: %#v", read.Integration)
		}
		encoded, marshalErr := json.Marshal(read.Integration)
		if marshalErr != nil || strings.Contains(string(encoded), source) {
			t.Fatalf("development path leaked into projection: %s err=%v", encoded, marshalErr)
		}
	})
	t.Run("a different local development copy has specific guidance", func(t *testing.T) {
		manager := &fakeReviewedIntegrationManager{installed: []plugin.InstalledPlugin{{
			Name: entry.PluginID, Version: entry.MinimumVersion,
			Source: t.TempDir(), Format: entry.SourceFormat, Generation: 1,
		}}}
		adapter := newReviewedIntegrationAdapter(manager, integrationResolver(entry), "darwin/arm64")
		adapter.developmentSource = normalizedLocalDevelopmentSource(t.TempDir())
		read, err := adapter.Read(context.Background(), scope)
		if err != nil || read.BlockedReason != ReasonIntegrationLocalUnverified || manager.inspections != 0 {
			t.Fatalf("local copy did not receive bounded guidance: %#v err=%v", read, err)
		}
		if guidance := safeGuidance[read.BlockedReason]; guidance != "Local development copy installed; not release-verified." {
			t.Fatalf("local copy guidance = %q", guidance)
		}
	})
	t.Run("same name wrong source", func(t *testing.T) {
		manager := &fakeReviewedIntegrationManager{installed: []plugin.InstalledPlugin{{
			Name: entry.PluginID, Version: entry.MinimumVersion,
			Source: "https://github.com/attacker/reaper-plugin#sha=" + strings.Repeat("d", 40),
			Format: entry.SourceFormat, Generation: 1,
		}}}
		read, err := newReviewedIntegrationAdapter(manager, integrationResolver(entry), "darwin/arm64").Read(context.Background(), scope)
		if err != nil || read.BlockedReason != ReasonIntegrationIdentityMismatch || manager.inspections != 0 {
			t.Fatalf("identity confusion was not rejected before preview: %#v err=%v", read, err)
		}
	})
	t.Run("candidate missing required host feature", func(t *testing.T) {
		oldDescriptor := descriptor
		contribution := *descriptor.WorkspaceSurfaces
		contribution.RequiresHostFeatures = []string{plugin.HostFeatureAssistantProgramV1}
		oldDescriptor.WorkspaceSurfaces = &contribution
		oldReport := plugin.BuildTrustReport(oldDescriptor)
		manager := &fakeReviewedIntegrationManager{descriptor: oldDescriptor, report: oldReport}
		read, err := newReviewedIntegrationAdapter(manager, integrationResolver(entry), "darwin/arm64").Read(context.Background(), scope)
		if err != nil || read.BlockedReason != ReasonIntegrationUnsupported {
			t.Fatalf("old plugin was accepted by new journey host: %#v err=%v", read, err)
		}
	})
	t.Run("candidate missing program", func(t *testing.T) {
		descriptor.ResolvedBlueprints[0].Template.AssistantProgram = nil
		report = plugin.BuildTrustReport(descriptor)
		manager := &fakeReviewedIntegrationManager{descriptor: descriptor, report: report}
		read, err := newReviewedIntegrationAdapter(manager, integrationResolver(entry), "darwin/arm64").Read(context.Background(), scope)
		if err != nil || read.BlockedReason != ReasonIntegrationUnsupported {
			t.Fatalf("invalid candidate program was not rejected: %#v err=%v", read, err)
		}
	})
}

func TestReviewedIntegrationUnsupportedPlatformDoesNotInspectOrInstall(t *testing.T) {
	entry, descriptor, report, scope := readyIntegrationFixture(t)
	manager := &fakeReviewedIntegrationManager{descriptor: descriptor, report: report}
	read, err := newReviewedIntegrationAdapter(manager, integrationResolver(entry), "linux/amd64").Read(context.Background(), scope)
	if err != nil || read.BlockedReason != ReasonIntegrationUnsupported || manager.inspections != 0 || len(manager.installed) != 0 {
		t.Fatalf("unsupported platform was not blocked before preview: %#v err=%v", read, err)
	}
}

func TestReviewedIntegrationPendingReleaseNeverResolvesMutableSource(t *testing.T) {
	entry, descriptor, report, scope := readyIntegrationFixture(t)
	entry.ReleaseReady = false
	entry.FallbackCommit = ""
	manager := &fakeReviewedIntegrationManager{descriptor: descriptor, report: report}
	read, err := newReviewedIntegrationAdapter(manager, integrationResolver(entry), "darwin/arm64").Read(context.Background(), scope)
	if err != nil || read.BlockedReason != ReasonIntegrationReleaseNotReady || manager.inspections != 0 ||
		read.Integration == nil || read.Integration.ReleaseReady {
		t.Fatalf("pending release was not inert: %#v err=%v inspections=%d", read, err, manager.inspections)
	}
}

func TestReviewedIntegrationReviewInstallEnableAndReplayThroughService(t *testing.T) {
	entry, descriptor, report, _ := readyIntegrationFixture(t)
	manager := &fakeReviewedIntegrationManager{descriptor: descriptor, report: report}
	adapter := newReviewedIntegrationAdapter(manager, integrationResolver(entry), "darwin/arm64")
	readers := make(map[specialist.SetupStepKind]CanonicalReader, len(actionDefinitionsByKind))
	for kind := range actionDefinitionsByKind {
		if kind == specialist.SetupStepIntegrationInstall {
			readers[kind] = adapter
			continue
		}
		readers[kind] = CanonicalReaderFunc(func(context.Context, ReadScope) (CanonicalStepRead, error) {
			return CanonicalStepRead{}, nil
		})
	}
	registry, err := NewReaderRegistry(readers)
	if err != nil {
		t.Fatal(err)
	}
	_, store := openTestStore(t)
	service, err := NewService(store, &relationshipStub{state: acceptedRelationship()}, registry)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.SetActionAdapter(specialist.SetupStepIntegrationInstall, adapter); err != nil {
		t.Fatal(err)
	}
	service = installQuestScope(t, service)
	ctx := context.Background()
	journey, err := service.Read(ctx, "local", "")
	if err != nil || !projectionHasAction(journey, ActionReviewInstall) {
		t.Fatalf("initial journey: %#v err=%v", journey, err)
	}
	review, err := service.Mutate(ctx, "local", journey.RunID, ActionReviewInstall, ActionMutation{
		IfRevision: journey.StateRevision, IdempotencyKey: "review-install-1", Input: json.RawMessage(`{}`),
	})
	if err != nil || review.Review == nil || review.Review.CommitAction != ActionInstall ||
		review.Review.Integration == nil || review.Review.Integration.Trust == nil || len(manager.installed) != 0 {
		t.Fatalf("install review mutated or omitted disclosure: %#v err=%v installed=%d", review, err, len(manager.installed))
	}
	commitRequest := ActionMutation{
		IfRevision: journey.StateRevision, IdempotencyKey: "commit-install-1",
		ReviewToken: review.Review.Token, Input: json.RawMessage(`{}`),
	}
	installedResult, err := service.Mutate(ctx, "local", journey.RunID, ActionInstall, commitRequest)
	if err != nil || len(manager.installed) != 1 || manager.installed[0].Enabled ||
		!projectionHasAction(installedResult.Journey, ActionReviewEnable) {
		t.Fatalf("reviewed install did not remain disabled: %#v err=%v plugins=%#v install_calls=%d confirmed=%v", installedResult, err, manager.installed, manager.installCalls, manager.confirmAccepted)
	}
	// An exact retry uses the terminal receipt even though the run revision has
	// advanced past the original If-Match value.
	replayed, err := service.Mutate(ctx, "local", journey.RunID, ActionInstall, commitRequest)
	if err != nil || replayed.Journey.StateRevision != installedResult.Journey.StateRevision || len(manager.installed) != 1 {
		t.Fatalf("exact install replay was not stable: %#v err=%v", replayed, err)
	}

	enableReview, err := service.Mutate(ctx, "local", journey.RunID, ActionReviewEnable, ActionMutation{
		IfRevision: installedResult.Journey.StateRevision, IdempotencyKey: "review-enable-1", Input: json.RawMessage(`{}`),
	})
	if err != nil || enableReview.Review == nil {
		t.Fatalf("enable review: %#v err=%v", enableReview, err)
	}
	enabledResult, err := service.Mutate(ctx, "local", journey.RunID, ActionEnable, ActionMutation{
		IfRevision: installedResult.Journey.StateRevision, IdempotencyKey: "commit-enable-1",
		ReviewToken: enableReview.Review.Token, Input: json.RawMessage(`{}`),
	})
	if err != nil || !manager.installed[0].Enabled || enabledResult.Journey.Steps[0].Status != StepComplete {
		t.Fatalf("reviewed enable did not complete integration: %#v err=%v", enabledResult, err)
	}
}

func TestReviewedIntegrationCommitRejectsStaleOwnerReviewWithoutClaim(t *testing.T) {
	entry, descriptor, report, _ := readyIntegrationFixture(t)
	installed := installedFromFixture(descriptor, entry.FallbackSource(), entry.SourceFormat, false, 3)
	manager := &fakeReviewedIntegrationManager{installed: []plugin.InstalledPlugin{installed}, descriptor: descriptor, report: report}
	adapter := newReviewedIntegrationAdapter(manager, integrationResolver(entry), "darwin/arm64")
	readers := make(map[specialist.SetupStepKind]CanonicalReader, len(actionDefinitionsByKind))
	for kind := range actionDefinitionsByKind {
		if kind == specialist.SetupStepIntegrationInstall {
			readers[kind] = adapter
		} else {
			readers[kind] = CanonicalReaderFunc(func(context.Context, ReadScope) (CanonicalStepRead, error) { return CanonicalStepRead{}, nil })
		}
	}
	registry, _ := NewReaderRegistry(readers)
	_, store := openTestStore(t)
	service, _ := NewService(store, &relationshipStub{state: acceptedRelationship()}, registry)
	if err := service.SetActionAdapter(specialist.SetupStepIntegrationInstall, adapter); err != nil {
		t.Fatal(err)
	}
	service = installQuestScope(t, service)
	journey, _ := service.Read(context.Background(), "local", "")
	review, err := service.Mutate(context.Background(), "local", journey.RunID, ActionReviewEnable, ActionMutation{
		IfRevision: journey.StateRevision, IdempotencyKey: "review-stale-owner", Input: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	manager.installed[0].Generation++
	_, err = service.Mutate(context.Background(), "local", journey.RunID, ActionEnable, ActionMutation{
		IfRevision: journey.StateRevision, IdempotencyKey: "commit-stale-owner",
		ReviewToken: review.Review.Token, Input: json.RawMessage(`{}`),
	})
	var public *Failure
	if !errors.As(err, &public) || public.ReasonCode != ReasonReviewStale || manager.installed[0].Enabled {
		t.Fatalf("stale owner review was not rejected: failure=%#v enabled=%v", public, manager.installed[0].Enabled)
	}
	if _, receiptErr := store.GetOperationReceipt(context.Background(), RunKindRoot, journey.RunID, "commit-stale-owner"); !errors.Is(receiptErr, ErrNotFound) {
		t.Fatalf("stale review created operation claim: %v", receiptErr)
	}
}

func containsAction(actions []ActionID, expected ActionID) bool {
	for _, action := range actions {
		if action == expected {
			return true
		}
	}
	return false
}

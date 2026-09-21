package server

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/integrationrelease"
	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/pluginhttp"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// fixedReleases is a fixed set of published releases: what the resolver answers
// and what inspecting each release's exact commit yields. Given only resolution
// it is a single-answer resolver; candidates makes it hand over an ordered list
// the way the real resolver does. Every release inspects as loadable by this
// build unless inspectErr or incompatible names its source.
type fixedReleases struct {
	entry        reviewedintegration.Entry
	platform     string
	resolution   integrationrelease.Resolution
	candidates   []integrationrelease.Resolution
	calls        int
	inspectErr   map[string]error
	incompatible map[string]bool
	inspected    []string
}

func (releases *fixedReleases) Resolve(context.Context, reviewedintegration.Entry) integrationrelease.Resolution {
	releases.calls++
	if len(releases.candidates) > 0 {
		return releases.candidates[0]
	}
	return releases.resolution
}

func (releases *fixedReleases) Candidates(context.Context, reviewedintegration.Entry) []integrationrelease.Resolution {
	releases.calls++
	if len(releases.candidates) > 0 {
		return append([]integrationrelease.Resolution(nil), releases.candidates...)
	}
	return []integrationrelease.Resolution{releases.resolution}
}

func (releases *fixedReleases) inspect(source string, _ plugin.SourceFormat) (plugin.PluginDescriptor, plugin.TrustReport, error) {
	releases.inspected = append(releases.inspected, source)
	if err := releases.inspectErr[source]; err != nil {
		return plugin.PluginDescriptor{}, plugin.TrustReport{}, err
	}
	version := ""
	for _, release := range append([]integrationrelease.Resolution{releases.resolution}, releases.candidates...) {
		if release.Source == source {
			version = release.Version
		}
	}
	descriptor := loadableRelease(releases.entry, version, source, releases.platform)
	if releases.incompatible[source] {
		// A program schema this build does not run: inspection succeeds, and
		// the descriptor check refuses it as unloadable.
		descriptor.ResolvedBlueprints[0].Template.AssistantProgram.SchemaVersion++
	}
	return descriptor, plugin.BuildTrustReport(descriptor), nil
}

// loadableRelease is a descriptor this build accepts for one release of entry.
func loadableRelease(entry reviewedintegration.Entry, version, source, platform string) plugin.PluginDescriptor {
	goos, goarch, _ := strings.Cut(platform, "/")
	return plugin.PluginDescriptor{
		Name: entry.PluginID, Version: version, SourceLocation: source, SourceFormat: entry.SourceFormat,
		WorkspaceSurfaces: &plugin.SurfaceContribution{
			Name: entry.PluginID, Version: version,
			Protocol:             plugin.ProtocolRange{Min: entry.ExpectedProtocol, Max: entry.ExpectedProtocol},
			RequiresHostFeatures: append([]string(nil), entry.RequiredHostFeatures...),
			Services: []plugin.ContributedService{{
				ID: "service", Artifacts: []plugin.ContributedArtifact{{
					ID: "service", OS: goos, Arch: goarch, Size: 20, SHA256: strings.Repeat("d", 64),
					Source: plugin.ArtifactSource{Kind: "url", URL: "https://example.invalid/service"},
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
}

// officialRelease is a resolved release of entry at one exact commit.
func officialRelease(entry reviewedintegration.Entry, version, commitChar string) integrationrelease.Resolution {
	commit := strings.Repeat(commitChar, 40)
	return integrationrelease.Resolution{Version: version, Tag: "v" + version, Commit: commit, Source: entry.PinnedSource(commit)}
}

func reviewedUpdatesFixture(target string) (reviewedIntegrationUpdates, *fixedReleases, reviewedintegration.Entry) {
	entry, ok := reviewedintegration.Get("ori_reaper")
	if !ok {
		panic("reviewed integration fixture is missing")
	}
	releases := &fixedReleases{
		entry: entry, platform: entry.SupportedPlatforms[0], resolution: officialRelease(entry, target, "b"),
		inspectErr: map[string]error{}, incompatible: map[string]bool{},
	}
	updates := reviewedIntegrationUpdates{
		releases: releases, entryFor: reviewedintegration.ForPlugin, inspect: releases.inspect, platform: releases.platform,
	}
	return updates, releases, entry
}

func reviewedInstall(entry reviewedintegration.Entry, version, source string) plugin.InstalledPlugin {
	return plugin.InstalledPlugin{Name: entry.PluginID, Version: version, Source: source, Format: entry.SourceFormat}
}

func TestReviewedIntegrationUpdatesOfferOnlyANewerReleaseForExactOfficialCommits(t *testing.T) {
	updates, releases, entry := reviewedUpdatesFixture("0.6.2")
	older := reviewedInstall(entry, "0.6.0", entry.PinnedSource(strings.Repeat("a", 40)))

	availability, ok, err := updates.availability(older)
	if err != nil || !ok || availability != (plugin.UpdateAvailability{
		Name: entry.PluginID, InstalledVersion: "0.6.0", AvailableVersion: "0.6.2", Available: true, ReviewedRelease: true,
	}) {
		t.Fatalf("availability = %+v ok=%v err=%v", availability, ok, err)
	}
	if got := updates.replacement(context.Background(), older); got != (pluginhttp.ReviewedUpdate{Source: releases.resolution.Source, Format: entry.SourceFormat}) {
		t.Fatalf("replacement = %+v", got)
	}

	// Same or newer installed versions are reviewed but have nothing to offer,
	// and the handler keeps its recorded-source path instead of downgrading.
	for _, version := range []string{"0.6.2", "0.7.0", "unknown"} {
		installed := reviewedInstall(entry, version, entry.PinnedSource(strings.Repeat("c", 40)))
		availability, ok, err := updates.availability(installed)
		if err != nil || !ok || availability.Available || availability.AvailableVersion != version {
			t.Fatalf("installed %s availability = %+v ok=%v err=%v", version, availability, ok, err)
		}
		// An exact-commit install with nothing newer keeps its recorded source: it
		// is immutable, so following it is harmless. It is never refused.
		if got := updates.replacement(context.Background(), installed); got != (pluginhttp.ReviewedUpdate{}) {
			t.Fatalf("installed %s pinned answer = %+v, want the zero value", version, got)
		}
	}
}

// The Issue #530 report: 0.6.1 installed from the official unpinned URL, v0.7.0
// published, main staging 0.8.0. Both legacy spellings follow the release.
func TestReviewedIntegrationUpdatesOfferTheReleaseToUnpinnedOfficialURLs(t *testing.T) {
	for _, suffix := range []string{"", ".git"} {
		t.Run("repository"+suffix, func(t *testing.T) {
			updates, releases, entry := reviewedUpdatesFixture("0.7.0")
			installed := reviewedInstall(entry, "0.6.1", entry.SourceRepository+suffix)

			availability, ok, err := updates.availability(installed)
			if err != nil || !ok || availability != (plugin.UpdateAvailability{
				Name: entry.PluginID, InstalledVersion: "0.6.1", AvailableVersion: "0.7.0", Available: true, ReviewedRelease: true,
			}) {
				t.Fatalf("availability = %+v ok=%v err=%v", availability, ok, err)
			}
			got := updates.replacement(context.Background(), installed)
			if got != (pluginhttp.ReviewedUpdate{Source: releases.resolution.Source, Format: entry.SourceFormat}) || !entry.IsPinnedSource(got.Source) {
				t.Fatalf("replacement = %+v, want the resolver's exact commit", got)
			}
		})
	}
}

// Every answer for an unpinned official install is terminal (ok=true): falling
// through would read the mutable default branch.
func TestReviewedIntegrationUpdatesAnswerUnpinnedOfficialInstallsFromReleasesOnly(t *testing.T) {
	floorOf := func(entry reviewedintegration.Entry) integrationrelease.Resolution {
		return integrationrelease.Floor(entry)
	}
	cases := []struct {
		name          string
		installed     string
		resolution    func(reviewedintegration.Entry) integrationrelease.Resolution
		wantAvailable string // "" when nothing is offered
		wantSource    func(reviewedintegration.Entry) string
	}{
		{name: "current release while main is ahead", installed: "0.7.0"},
		{name: "ahead of the latest release", installed: "0.8.0"},
		{name: "installed version not comparable", installed: "unknown"},
		{name: "resolver fallback at the floor", installed: "0.6.1", resolution: floorOf},
		{
			name: "resolver fallback above the install", installed: "0.6.0", resolution: floorOf,
			wantAvailable: "0.6.1", wantSource: func(entry reviewedintegration.Entry) string { return entry.FallbackSource() },
		},
		{
			name: "resolver source outside the repository", installed: "0.6.1",
			resolution: func(entry reviewedintegration.Entry) integrationrelease.Resolution {
				return integrationrelease.Resolution{
					Version: "0.7.0", Tag: "v0.7.0", Commit: strings.Repeat("b", 40),
					Source: "https://github.com/attacker/plugin#sha=" + strings.Repeat("b", 40),
				}
			},
		},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			updates, releases, entry := reviewedUpdatesFixture("0.7.0")
			if item.resolution != nil {
				releases.resolution = item.resolution(entry)
			}
			installed := reviewedInstall(entry, item.installed, entry.SourceRepository+".git")

			want := plugin.UpdateAvailability{
				Name: entry.PluginID, InstalledVersion: item.installed, AvailableVersion: item.installed, ReviewedRelease: true,
			}
			if item.wantAvailable != "" {
				want.AvailableVersion, want.Available = item.wantAvailable, true
			}
			availability, ok, err := updates.availability(installed)
			if err != nil || !ok || availability != want {
				t.Fatalf("availability = %+v ok=%v err=%v, want terminal %+v", availability, ok, err, want)
			}
			got := updates.replacement(context.Background(), installed)
			switch {
			case item.wantSource == nil && got != (pluginhttp.ReviewedUpdate{Refuse: true}):
				t.Fatalf("replacement = %+v with nothing newer, want a refusal", got)
			case item.wantSource != nil && (got.Refuse || got.Source != item.wantSource(entry)):
				t.Fatalf("replacement = %+v, want %q", got, item.wantSource(entry))
			}
		})
	}
}

// mutableHead is a recorded-source check whose head has moved past every
// release, as the official repository's main does while it stages 0.8.0.
type mutableHead struct {
	installed []plugin.InstalledPlugin
	checks    int
}

func (head *mutableHead) List() ([]plugin.InstalledPlugin, error) {
	return append([]plugin.InstalledPlugin(nil), head.installed...), nil
}

func (head *mutableHead) CheckUpdate(name string) (plugin.UpdateAvailability, error) {
	head.checks++
	return plugin.UpdateAvailability{Name: name, InstalledVersion: "0.6.1", AvailableVersion: "0.8.0", Available: true}, nil
}

func TestReviewedIntegrationUpdateCheckNeverReadsTheMutableHeadOfAnOfficialInstall(t *testing.T) {
	updates, _, entry := reviewedUpdatesFixture("0.7.0")
	head := &mutableHead{installed: []plugin.InstalledPlugin{reviewedInstall(entry, "0.6.1", entry.SourceRepository+".git")}}
	checker := plugin.NewUpdateChecker(head)
	checker.SetAvailabilityOverride(updates.availability)
	checker.Start(time.Hour)
	deadline := time.Now().Add(time.Second)
	for checker.Snapshot().LastSuccessfulCheckAt == nil && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	checker.Stop()

	snapshot := checker.Snapshot()
	if len(snapshot.Updates) != 1 || snapshot.Updates[0].AvailableVersion != "0.7.0" || !snapshot.Updates[0].Available {
		t.Fatalf("snapshot = %+v, want reviewed release 0.7.0", snapshot)
	}
	if head.checks != 0 {
		t.Fatalf("the recorded mutable source was checked %d times", head.checks)
	}
	if head.installed[0].Source != entry.SourceRepository+".git" || head.installed[0].Version != "0.6.1" {
		t.Fatalf("the check rewrote the installed record: %+v", head.installed[0])
	}
}

func TestReviewedIntegrationUpdatesLeaveOtherInstallsToTheirRecordedSource(t *testing.T) {
	updates, releases, entry := reviewedUpdatesFixture("0.6.2")
	pinned := entry.PinnedSource(strings.Repeat("a", 40))
	cases := map[string]plugin.InstalledPlugin{
		"local copy":        reviewedInstall(entry, "0.6.0", "/Users/example/plugin"),
		"other repository":  reviewedInstall(entry, "0.6.0", "https://github.com/attacker/plugin#sha="+strings.Repeat("a", 40)),
		"other spelling":    reviewedInstall(entry, "0.6.0", entry.SourceRepository+"#ref=main"),
		"wrong format":      {Name: entry.PluginID, Version: "0.6.0", Source: pinned, Format: plugin.FormatCodex},
		"unreviewed plugin": {Name: "other-plugin", Version: "0.6.0", Source: pinned, Format: entry.SourceFormat},
		"case-folded name":  {Name: strings.ToUpper(entry.PluginID), Version: "0.6.0", Source: pinned, Format: entry.SourceFormat},
		"unpinned wrong format": {
			Name: entry.PluginID, Version: "0.6.0", Source: entry.SourceRepository + ".git", Format: plugin.FormatCodex,
		},
	}
	for name, installed := range cases {
		t.Run(name, func(t *testing.T) {
			if _, ok, err := updates.availability(installed); ok || err != nil {
				t.Fatalf("availability override claimed %s (err=%v)", name, err)
			}
			// Local copies and other repositories follow their recorded source:
			// neither a replacement nor a refusal.
			if got := updates.replacement(context.Background(), installed); got != (pluginhttp.ReviewedUpdate{}) {
				t.Fatalf("replacement hook claimed %s: %+v", name, got)
			}
		})
	}
	if releases.calls != 0 {
		t.Fatalf("non-reviewed installs resolved the latest release %d times", releases.calls)
	}
}

// installForms are the two reviewed install forms every release-walk case runs
// against, with the answer replacement gives each when nothing is offered.
var installForms = []struct {
	name    string
	source  func(reviewedintegration.Entry) string
	nothing pluginhttp.ReviewedUpdate
}{
	// An exact commit keeps following its recorded, immutable source.
	{
		"pinned",
		func(entry reviewedintegration.Entry) string { return entry.PinnedSource(strings.Repeat("a", 40)) },
		pluginhttp.ReviewedUpdate{},
	},
	// The mutable official URL is refused rather than updated from main.
	{
		"unpinned",
		func(entry reviewedintegration.Entry) string { return entry.SourceRepository + ".git" },
		pluginhttp.ReviewedUpdate{Refuse: true},
	},
}

// walkFixture publishes 0.8.0 (c), 0.7.0 (b) and the 0.6.1 floor, newest first.
func walkFixture() (reviewedIntegrationUpdates, *fixedReleases, reviewedintegration.Entry, []integrationrelease.Resolution) {
	updates, releases, entry := reviewedUpdatesFixture("0.8.0")
	releases.candidates = []integrationrelease.Resolution{
		officialRelease(entry, "0.8.0", "c"), officialRelease(entry, "0.7.0", "b"), integrationrelease.Floor(entry),
	}
	return updates, releases, entry, releases.candidates
}

func TestReviewedIntegrationUpdatesStepOverReleasesThisBuildCannotLoad(t *testing.T) {
	refusals := map[string]func(*fixedReleases, string){
		"needs a newer host feature": func(releases *fixedReleases, source string) {
			releases.inspectErr[source] = &plugin.ContributionError{Code: plugin.CodeHostFeatureUnsupported, Reason: "a required host feature is unavailable"}
		},
		"needs a newer protocol": func(releases *fixedReleases, source string) {
			releases.inspectErr[source] = &plugin.ContributionError{Code: plugin.CodeProtocolIncompatible, Reason: "protocol range does not include the host version"}
		},
		"declares a program schema this build does not run": func(releases *fixedReleases, source string) {
			releases.incompatible[source] = true
		},
	}
	for refusalName, refuse := range refusals {
		for _, form := range installForms {
			t.Run(refusalName+"/"+form.name, func(t *testing.T) {
				updates, releases, entry, published := walkFixture()
				refuse(releases, published[0].Source)
				installed := reviewedInstall(entry, "0.6.1", form.source(entry))

				availability, ok, err := updates.availability(installed)
				if err != nil || !ok || availability != (plugin.UpdateAvailability{
					Name: entry.PluginID, InstalledVersion: "0.6.1", AvailableVersion: "0.7.0", Available: true, ReviewedRelease: true,
				}) {
					t.Fatalf("availability = %+v ok=%v err=%v, want the loadable 0.7.0", availability, ok, err)
				}
				if got := updates.replacement(context.Background(), installed); got != (pluginhttp.ReviewedUpdate{Source: published[1].Source, Format: entry.SourceFormat}) {
					t.Fatalf("replacement = %+v, want 0.7.0's exact commit", got)
				}
				// Each hook walks 0.8.0 then 0.7.0 and stops at the first loadable.
				want := []string{published[0].Source, published[1].Source, published[0].Source, published[1].Source}
				if strings.Join(releases.inspected, " ") != strings.Join(want, " ") {
					t.Fatalf("inspected %v, want %v", releases.inspected, want)
				}
			})
		}
	}
}

func TestReviewedIntegrationUpdatesOfferNothingWhenNoNewerReleaseCanLoad(t *testing.T) {
	for _, form := range installForms {
		t.Run(form.name, func(t *testing.T) {
			updates, releases, entry, published := walkFixture()
			releases.inspectErr[published[0].Source] = &plugin.ContributionError{Code: plugin.CodeHostFeatureUnsupported}
			releases.incompatible[published[1].Source] = true
			installed := reviewedInstall(entry, "0.6.1", form.source(entry))

			availability, ok, err := updates.availability(installed)
			if err != nil || !ok || availability != (plugin.UpdateAvailability{
				Name: entry.PluginID, InstalledVersion: "0.6.1", AvailableVersion: "0.6.1", ReviewedRelease: true,
			}) {
				t.Fatalf("availability = %+v ok=%v err=%v, want a terminal nothing-available", availability, ok, err)
			}
			if got := updates.replacement(context.Background(), installed); got != form.nothing {
				t.Fatalf("replacement = %+v, want %+v", got, form.nothing)
			}
			// The walk ends at the floor, which is not newer than 0.6.1: it is
			// never inspected.
			for _, source := range releases.inspected {
				if source == published[2].Source {
					t.Fatal("the floor, equal to the installed version, was inspected")
				}
			}
		})
	}
}

func TestReviewedIntegrationUpdatesNeverSlideOverAFailedInspection(t *testing.T) {
	failures := map[string]func(*fixedReleases, string){
		"unreachable source": func(releases *fixedReleases, source string) {
			releases.inspectErr[source] = errors.New("clone failed")
		},
		"malformed manifest": func(releases *fixedReleases, source string) {
			releases.inspectErr[source] = &plugin.ContributionError{Code: plugin.CodeContributionInvalid, Reason: "manifest is invalid"}
		},
	}
	for failureName, fail := range failures {
		for _, form := range installForms {
			t.Run(failureName+"/"+form.name, func(t *testing.T) {
				updates, releases, entry, published := walkFixture()
				fail(releases, published[0].Source)
				installed := reviewedInstall(entry, "0.6.1", form.source(entry))

				if _, ok, err := updates.availability(installed); !ok || err == nil {
					t.Fatalf("availability ok=%v err=%v, want a terminal error", ok, err)
				} else if strings.Contains(err.Error(), "clone failed") {
					t.Fatalf("the error carried the raw inspection failure: %v", err)
				}
				if got := updates.replacement(context.Background(), installed); got != form.nothing {
					t.Fatalf("replacement = %+v, want %+v", got, form.nothing)
				}
				for _, source := range releases.inspected {
					if source != published[0].Source {
						t.Fatalf("the walk slid past a failed inspection to %s", source)
					}
				}
			})
		}
	}
}

// An inspection that succeeds but names another plugin is an identity failure,
// not an incompatibility, and stops the walk like a failed read.
func TestReviewedIntegrationUpdatesTreatAnIdentityMismatchAsAFailure(t *testing.T) {
	updates, releases, entry, published := walkFixture()
	updates.inspect = func(source string, format plugin.SourceFormat) (plugin.PluginDescriptor, plugin.TrustReport, error) {
		descriptor, _, err := releases.inspect(source, format)
		descriptor.Name = "impostor"
		return descriptor, plugin.BuildTrustReport(descriptor), err
	}
	installed := reviewedInstall(entry, "0.6.1", entry.SourceRepository)
	if _, ok, err := updates.availability(installed); !ok || err == nil {
		t.Fatalf("availability ok=%v err=%v, want a terminal error", ok, err)
	}
	if got := updates.replacement(context.Background(), installed); got != (pluginhttp.ReviewedUpdate{Refuse: true}) {
		t.Fatalf("replacement = %+v, want a refusal", got)
	}
	if len(releases.inspected) != 2 || releases.inspected[0] != published[0].Source || releases.inspected[1] != published[0].Source {
		t.Fatalf("inspected %v, want only 0.8.0 once per hook", releases.inspected)
	}
}

func TestReviewedIntegrationUpdatesInspectNothingForAnUpToDateInstall(t *testing.T) {
	for _, form := range installForms {
		t.Run(form.name, func(t *testing.T) {
			updates, releases, entry, _ := walkFixture()
			installed := reviewedInstall(entry, "0.8.0", form.source(entry))
			if availability, ok, err := updates.availability(installed); err != nil || !ok || availability.Available {
				t.Fatalf("availability = %+v ok=%v err=%v", availability, ok, err)
			}
			if got := updates.replacement(context.Background(), installed); got != form.nothing {
				t.Fatalf("replacement = %+v, want %+v", got, form.nothing)
			}
			if len(releases.inspected) != 0 {
				t.Fatalf("an up-to-date install inspected %v", releases.inspected)
			}
		})
	}
}

func TestReviewedIntegrationUpdatesFailClosedWithoutAnInspector(t *testing.T) {
	updates, _, entry := reviewedUpdatesFixture("0.7.0")
	updates.inspect = nil
	installed := reviewedInstall(entry, "0.6.1", entry.SourceRepository+".git")
	if _, ok, err := updates.availability(installed); !ok || err == nil {
		t.Fatalf("availability ok=%v err=%v, want a terminal error", ok, err)
	}
	if got := updates.replacement(context.Background(), installed); got != (pluginhttp.ReviewedUpdate{Refuse: true}) {
		t.Fatalf("replacement = %+v, want a refusal", got)
	}
}

// A hard inspection failure keeps the checker's last result, and the mutable
// source is still never read.
func TestReviewedIntegrationUpdateCheckKeepsTheLastResultWhenAReleaseCannotBeVerified(t *testing.T) {
	for _, form := range installForms {
		t.Run(form.name, func(t *testing.T) {
			updates, releases, entry, published := walkFixture()
			head := &mutableHead{installed: []plugin.InstalledPlugin{reviewedInstall(entry, "0.6.1", form.source(entry))}}
			checker := plugin.NewUpdateChecker(head)
			checker.SetAvailabilityOverride(updates.availability)
			runCheck := func() {
				t.Helper()
				checker.Start(time.Hour)
				checker.Stop()
			}
			runCheck()
			first := checker.Snapshot()
			if len(first.Updates) != 1 || first.Updates[0].AvailableVersion != "0.8.0" || !first.Updates[0].ReviewedRelease {
				t.Fatalf("first check = %+v", first)
			}

			releases.inspectErr[published[0].Source] = errors.New("clone failed")
			runCheck()
			if second := checker.Snapshot(); len(second.Updates) != 1 || second.Updates[0] != first.Updates[0] {
				t.Fatalf("a failed verification replaced the last result: %+v -> %+v", first.Updates, second.Updates)
			}
			if head.checks != 0 {
				t.Fatalf("the recorded source was checked %d times", head.checks)
			}
		})
	}
}

func TestReviewedIntegrationUpdatesRejectAResolverSourceOutsideTheRepository(t *testing.T) {
	updates, releases, entry := reviewedUpdatesFixture("0.6.2")
	releases.resolution.Source = "https://github.com/attacker/plugin#sha=" + strings.Repeat("b", 40)
	installed := reviewedInstall(entry, "0.6.0", entry.PinnedSource(strings.Repeat("a", 40)))
	if availability, _, _ := updates.availability(installed); availability.Available {
		t.Fatalf("untrusted resolver source was offered: %+v", availability)
	}
	if got := updates.replacement(context.Background(), installed); got.Source != "" {
		t.Fatalf("untrusted resolver source became the replacement: %+v", got)
	}
}

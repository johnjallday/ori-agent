package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/integrationrelease"
	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/pluginhttp"
	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
)

type fixedReleases struct {
	resolution integrationrelease.Resolution
	calls      int
}

func (releases *fixedReleases) Resolve(context.Context, reviewedintegration.Entry) integrationrelease.Resolution {
	releases.calls++
	return releases.resolution
}

func (releases *fixedReleases) Candidates(ctx context.Context, entry reviewedintegration.Entry) []integrationrelease.Resolution {
	return []integrationrelease.Resolution{releases.Resolve(ctx, entry)}
}

func reviewedUpdatesFixture(target string) (reviewedIntegrationUpdates, *fixedReleases, reviewedintegration.Entry) {
	entry, ok := reviewedintegration.Get("ori_reaper")
	if !ok {
		panic("reviewed integration fixture is missing")
	}
	commit := strings.Repeat("b", 40)
	releases := &fixedReleases{resolution: integrationrelease.Resolution{
		Version: target, Tag: "v" + target, Commit: commit, Source: entry.PinnedSource(commit),
	}}
	return reviewedIntegrationUpdates{releases: releases, entryFor: reviewedintegration.ForPlugin}, releases, entry
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

package server

import (
	"context"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/integrationrelease"
	"github.com/johnjallday/ori-agent/internal/plugin"
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
		Name: entry.PluginID, InstalledVersion: "0.6.0", AvailableVersion: "0.6.2", Available: true,
	}) {
		t.Fatalf("availability = %+v ok=%v err=%v", availability, ok, err)
	}
	source, format, ok := updates.replacement(context.Background(), older)
	if !ok || source != releases.resolution.Source || format != entry.SourceFormat {
		t.Fatalf("replacement = %q %q %v", source, format, ok)
	}

	// Same or newer installed versions are reviewed but have nothing to offer,
	// and the handler keeps its recorded-source path instead of downgrading.
	for _, version := range []string{"0.6.2", "0.7.0", "unknown"} {
		installed := reviewedInstall(entry, version, entry.PinnedSource(strings.Repeat("c", 40)))
		availability, ok, err := updates.availability(installed)
		if err != nil || !ok || availability.Available || availability.AvailableVersion != version {
			t.Fatalf("installed %s availability = %+v ok=%v err=%v", version, availability, ok, err)
		}
		if _, _, replace := updates.replacement(context.Background(), installed); replace {
			t.Fatalf("installed %s was offered a replacement that is not newer", version)
		}
	}
}

func TestReviewedIntegrationUpdatesLeaveOtherInstallsToTheirRecordedSource(t *testing.T) {
	updates, releases, entry := reviewedUpdatesFixture("0.6.2")
	pinned := entry.PinnedSource(strings.Repeat("a", 40))
	cases := map[string]plugin.InstalledPlugin{
		"mutable official URL": reviewedInstall(entry, "0.6.0", entry.SourceRepository+".git"),
		"local copy":           reviewedInstall(entry, "0.6.0", "/Users/example/plugin"),
		"other repository":     reviewedInstall(entry, "0.6.0", "https://github.com/attacker/plugin#sha="+strings.Repeat("a", 40)),
		"wrong format":         {Name: entry.PluginID, Version: "0.6.0", Source: pinned, Format: plugin.FormatCodex},
		"unreviewed plugin":    {Name: "other-plugin", Version: "0.6.0", Source: pinned, Format: entry.SourceFormat},
		"case-folded name":     {Name: strings.ToUpper(entry.PluginID), Version: "0.6.0", Source: pinned, Format: entry.SourceFormat},
	}
	for name, installed := range cases {
		t.Run(name, func(t *testing.T) {
			if _, ok, err := updates.availability(installed); ok || err != nil {
				t.Fatalf("availability override claimed %s (err=%v)", name, err)
			}
			if _, _, ok := updates.replacement(context.Background(), installed); ok {
				t.Fatalf("replacement hook claimed %s", name)
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
	if _, _, ok := updates.replacement(context.Background(), installed); ok {
		t.Fatal("untrusted resolver source became the replacement")
	}
}

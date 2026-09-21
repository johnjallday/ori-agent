package server

import (
	"context"

	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/pluginhttp"
	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
	"github.com/johnjallday/ori-agent/internal/setupjourney"
)

// reviewedIntegrationUpdates offers the latest reviewed release as an ordinary
// Plugins page update. internal/plugin and internal/pluginhttp never import the
// registry or the resolver; they receive these methods as hooks.
type reviewedIntegrationUpdates struct {
	releases setupjourney.IntegrationReleaseResolver
	entryFor func(pluginID string) (reviewedintegration.Entry, bool)
}

// installReviewedIntegrationUpdates wires the hooks. It runs only after both the
// plugin handler and the release resolver exist, so neither hook captures nil.
func (b *ServerBuilder) installReviewedIntegrationUpdates() {
	if b == nil || b.pluginHandler == nil || b.integrationReleases == nil {
		return
	}
	updates := reviewedIntegrationUpdates{releases: b.integrationReleases, entryFor: reviewedintegration.ForPlugin}
	b.pluginHandler.UpdateChecker().SetAvailabilityOverride(updates.availability)
	b.pluginHandler.SetReviewedReplacement(updates.replacement)
}

// reviewedEntry returns the registry entry for an installed plugin recorded
// against the official repository of a release-ready entry — an exact commit or
// one of its legacy unpinned URLs. Such an install follows the entry's published
// releases only; its recorded source is never read, because an unpinned one
// names the mutable default branch. Local copies, other spellings of the
// repository and other plugins keep their recorded-source updates.
func (u reviewedIntegrationUpdates) reviewedEntry(installed plugin.InstalledPlugin) (reviewedintegration.Entry, bool) {
	if u.releases == nil || u.entryFor == nil {
		return reviewedintegration.Entry{}, false
	}
	entry, ok := u.entryFor(installed.Name)
	if !ok || entry.PluginID != installed.Name || entry.FallbackSource() == "" ||
		installed.Format != entry.SourceFormat ||
		!(entry.IsPinnedSource(installed.Source) || entry.IsUnpinnedOfficialSource(installed.Source)) {
		return reviewedintegration.Entry{}, false
	}
	return entry, true
}

// newerRelease resolves the latest release and reports its exact source only
// when it is newer than the installed version, so an update never downgrades.
func (u reviewedIntegrationUpdates) newerRelease(ctx context.Context, entry reviewedintegration.Entry, installed plugin.InstalledPlugin) (version, source string, ok bool) {
	target := u.releases.Resolve(ctx, entry)
	order, comparable := reviewedintegration.CompareVersions(target.Version, installed.Version)
	if !comparable || order <= 0 || !entry.IsPinnedSource(target.Source) {
		return "", "", false
	}
	return target.Version, target.Source, true
}

// availability is the update checker override for reviewed installs.
func (u reviewedIntegrationUpdates) availability(installed plugin.InstalledPlugin) (plugin.UpdateAvailability, bool, error) {
	entry, ok := u.reviewedEntry(installed)
	if !ok {
		return plugin.UpdateAvailability{}, false, nil
	}
	result := plugin.UpdateAvailability{
		Name: installed.Name, InstalledVersion: installed.Version, AvailableVersion: installed.Version,
	}
	if version, _, newer := u.newerRelease(context.Background(), entry, installed); newer {
		result.AvailableVersion = version
		result.Available = true
	}
	return result, true, nil
}

// replacement is the Plugins update handler hook for reviewed installs. A newer
// release is installed from its exact commit. With nothing newer, an install
// recorded against the mutable official URL is refused — its recorded source is
// the development branch, which is never installed — while an exact-commit
// install keeps following its recorded source, an immutable commit that changes
// nothing. Anything the host does not review answers the zero value.
func (u reviewedIntegrationUpdates) replacement(ctx context.Context, installed plugin.InstalledPlugin) pluginhttp.ReviewedUpdate {
	entry, ok := u.reviewedEntry(installed)
	if !ok {
		return pluginhttp.ReviewedUpdate{}
	}
	if _, source, newer := u.newerRelease(ctx, entry, installed); newer {
		return pluginhttp.ReviewedUpdate{Source: source, Format: entry.SourceFormat}
	}
	if entry.IsUnpinnedOfficialSource(installed.Source) {
		return pluginhttp.ReviewedUpdate{Refuse: true}
	}
	return pluginhttp.ReviewedUpdate{}
}

package server

import (
	"context"
	"errors"
	"fmt"
	"runtime"

	"github.com/johnjallday/ori-agent/internal/integrationrelease"
	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/pluginhttp"
	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
	"github.com/johnjallday/ori-agent/internal/setupjourney"
)

// releaseInspector reads one release's descriptor and trust report without
// installing anything. plugin.Manager.Inspect is the production inspector.
type releaseInspector func(source string, prefer plugin.SourceFormat) (plugin.PluginDescriptor, plugin.TrustReport, error)

// errNoReleaseInspector fails a reviewed check closed when no inspector is wired:
// a release that cannot be inspected cannot be shown to be loadable.
var errNoReleaseInspector = errors.New("reviewed release inspector is not configured")

// reviewedIntegrationUpdates offers the latest reviewed release as an ordinary
// Plugins page update. internal/plugin and internal/pluginhttp never import the
// registry or the resolver; they receive these methods as hooks.
type reviewedIntegrationUpdates struct {
	releases setupjourney.IntegrationReleaseResolver
	entryFor func(pluginID string) (reviewedintegration.Entry, bool)
	inspect  releaseInspector
	platform string
}

// installReviewedIntegrationUpdates wires the hooks. It runs only after both the
// plugin handler and the release resolver exist, so neither hook captures nil.
func (b *ServerBuilder) installReviewedIntegrationUpdates() {
	if b == nil || b.pluginHandler == nil || b.integrationReleases == nil {
		return
	}
	updates := reviewedIntegrationUpdates{
		releases: b.integrationReleases,
		entryFor: reviewedintegration.ForPlugin,
		inspect:  b.pluginHandler.Manager().Inspect,
		platform: runtime.GOOS + "/" + runtime.GOARCH,
	}
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
		(!entry.IsPinnedSource(installed.Source) && !entry.IsUnpinnedOfficialSource(installed.Source)) {
		return reviewedintegration.Entry{}, false
	}
	return entry, true
}

// newerRelease walks the resolver's candidates, newest first, and reports the
// exact source of the newest release that is newer than the installed version
// and that this build can load. An update therefore never downgrades and never
// offers a release that would be refused on install.
//
// The walk stops, offering nothing, at the first candidate that is not newer
// than the installed version — the list is ordered, so an up-to-date install
// performs no inspection at all — and at a candidate that is not an exact
// official commit at or above the floor, which the resolver never produces.
// Only a "this build cannot load it" refusal (setupjourney.InspectedReleaseReason
// answering ReasonIntegrationUnsupported) is stepped over to the next release.
//
// Any other inspection failure — an unreachable source, a bad clone, a
// malformed manifest, an identity or trust mismatch — is returned as an error.
// It never slides silently to an older release: availability reports it, so the
// update checker keeps its last result, and replacement offers nothing.
//
// Locking: inspect is plugin.Manager.Inspect, which takes the manager's
// operation lock. Neither hook runs under it: UpdateChecker.checkOne and
// pluginhttp's replacementFor both call their hook after Manager.List has
// returned and released the lock, so the inspection cannot deadlock.
func (u reviewedIntegrationUpdates) newerRelease(ctx context.Context, entry reviewedintegration.Entry, installed plugin.InstalledPlugin) (version, source string, ok bool, err error) {
	candidates := u.releases.Candidates(ctx, entry)
	if len(candidates) == 0 {
		candidates = []integrationrelease.Resolution{u.releases.Resolve(ctx, entry)}
	}
	for index, target := range candidates {
		if index >= integrationrelease.MaxCandidates {
			break
		}
		order, comparable := reviewedintegration.CompareVersions(target.Version, installed.Version)
		if !comparable || order <= 0 {
			return "", "", false, nil
		}
		if !entry.IsPinnedSource(target.Source) || !reviewedintegration.AtLeast(target.Version, entry.MinimumVersion) {
			return "", "", false, nil
		}
		if u.inspect == nil {
			return "", "", false, errNoReleaseInspector
		}
		descriptor, report, inspectErr := u.inspect(target.Source, entry.SourceFormat)
		switch reason := setupjourney.InspectedReleaseReason(entry, target.Version, target.Source, descriptor, report, inspectErr, u.platform); reason {
		case "":
			return target.Version, target.Source, true, nil
		case setupjourney.ReasonIntegrationUnsupported:
			continue
		default:
			// Deliberately omit the inspection error: it can carry a clone path.
			return "", "", false, fmt.Errorf("reviewed release %s could not be verified: %s", target.Version, reason)
		}
	}
	return "", "", false, nil
}

// availability is the update checker override for reviewed installs. Every
// answer is terminal. A failed verification is an error, so the checker keeps
// the last result rather than reading the recorded source.
func (u reviewedIntegrationUpdates) availability(installed plugin.InstalledPlugin) (plugin.UpdateAvailability, bool, error) {
	entry, ok := u.reviewedEntry(installed)
	if !ok {
		return plugin.UpdateAvailability{}, false, nil
	}
	version, _, newer, err := u.newerRelease(context.Background(), entry, installed)
	if err != nil {
		return plugin.UpdateAvailability{}, true, err
	}
	result := plugin.UpdateAvailability{
		Name: installed.Name, InstalledVersion: installed.Version, AvailableVersion: installed.Version,
		ReviewedRelease: true,
	}
	if newer {
		result.AvailableVersion = version
		result.Available = true
	}
	return result, true, nil
}

// replacement is the Plugins update handler hook for reviewed installs. A newer
// loadable release is installed from its exact commit. Otherwise — nothing
// newer, nothing loadable, or a release that could not be verified — an install
// recorded against the mutable official URL is refused, because its recorded
// source is the development branch, which is never installed; an exact-commit
// install keeps following its recorded source, an immutable commit that changes
// nothing. Anything the host does not review answers the zero value.
func (u reviewedIntegrationUpdates) replacement(ctx context.Context, installed plugin.InstalledPlugin) pluginhttp.ReviewedUpdate {
	entry, ok := u.reviewedEntry(installed)
	if !ok {
		return pluginhttp.ReviewedUpdate{}
	}
	if _, source, newer, err := u.newerRelease(ctx, entry, installed); err == nil && newer {
		return pluginhttp.ReviewedUpdate{Source: source, Format: entry.SourceFormat}
	}
	if entry.IsUnpinnedOfficialSource(installed.Source) {
		return pluginhttp.ReviewedUpdate{Refuse: true}
	}
	return pluginhttp.ReviewedUpdate{}
}

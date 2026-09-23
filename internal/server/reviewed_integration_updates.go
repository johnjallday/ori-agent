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
// Plugins page update, for reviewed project integrations and reviewed Home
// providers alike. internal/plugin and internal/pluginhttp never import the
// registry or the resolver; they receive these methods as hooks.
type reviewedIntegrationUpdates struct {
	releases        setupjourney.IntegrationReleaseResolver
	entryFor        func(pluginID string) (reviewedintegration.Entry, bool)
	homeProviderFor func(pluginID string) (reviewedintegration.HomeProvider, bool)
	inspect         releaseInspector
	platform        string
}

// installReviewedIntegrationUpdates wires the hooks. It runs only after both the
// plugin handler and the release resolver exist, so neither hook captures nil.
func (b *ServerBuilder) installReviewedIntegrationUpdates() {
	if b == nil || b.pluginHandler == nil || b.integrationReleases == nil {
		return
	}
	updates := &reviewedIntegrationUpdates{
		releases:        b.integrationReleases,
		entryFor:        reviewedintegration.ForPlugin,
		homeProviderFor: reviewedintegration.HomeProviderFor,
		inspect:         b.pluginHandler.Manager().Inspect,
		platform:        runtime.GOOS + "/" + runtime.GOARCH,
	}
	b.pluginHandler.UpdateChecker().SetAvailabilityOverride(updates.availability)
	b.pluginHandler.SetReviewedReplacement(updates.replacement)
	if b.server != nil {
		b.server.reviewedReleases = updates
	}
}

// releaseVerdict classifies one inspected candidate release.
type releaseVerdict int

const (
	releaseLoadable releaseVerdict = iota
	// releaseNeedsNewerHost: this build cannot load the release; an older one is
	// worth trying.
	releaseNeedsNewerHost
	// releaseRefused: the release could not be verified. Nothing older is tried
	// in its place.
	releaseRefused
)

// reviewedRelease is how one reviewed plugin's releases are found and checked:
// the release identity the resolver reads, and the check an inspected
// candidate must pass. A project integration and a Home provider differ only
// in the check.
type reviewedRelease struct {
	entry  reviewedintegration.Entry
	verify func(version, source string, descriptor plugin.PluginDescriptor, report plugin.TrustReport, inspectErr error) releaseVerdict
}

// verifiedRelease is one candidate this build can install, with the disclosure
// read from exactly that release.
type verifiedRelease struct {
	version string
	source  string
	report  plugin.TrustReport
}

func (u *reviewedIntegrationUpdates) integrationRelease(entry reviewedintegration.Entry) reviewedRelease {
	return reviewedRelease{entry: entry, verify: func(version, source string, descriptor plugin.PluginDescriptor, report plugin.TrustReport, inspectErr error) releaseVerdict {
		switch setupjourney.InspectedReleaseReason(entry, version, source, descriptor, report, inspectErr, u.platform) {
		case "":
			return releaseLoadable
		case setupjourney.ReasonIntegrationUnsupported:
			return releaseNeedsNewerHost
		default:
			return releaseRefused
		}
	}}
}

func homeProviderRelease(provider reviewedintegration.HomeProvider) reviewedRelease {
	return reviewedRelease{entry: provider.ReleaseEntry(), verify: func(version, source string, descriptor plugin.PluginDescriptor, report plugin.TrustReport, inspectErr error) releaseVerdict {
		switch provider.CheckRelease(version, source, descriptor, report, inspectErr) {
		case reviewedintegration.ReleaseLoadable:
			return releaseLoadable
		case reviewedintegration.ReleaseNeedsNewerHost:
			return releaseNeedsNewerHost
		default:
			return releaseRefused
		}
	}}
}

// reviewedEntry returns the reviewed release rules for an installed plugin
// recorded against the official repository of a release-ready entry — an exact
// commit or one of its legacy unpinned URLs. Such an install follows the
// entry's published releases only; its recorded source is never read, because
// an unpinned one names the mutable default branch. Local copies, other
// spellings of the repository and other plugins keep their recorded-source
// updates.
func (u *reviewedIntegrationUpdates) reviewedEntry(installed plugin.InstalledPlugin) (reviewedRelease, bool) {
	if u == nil || u.releases == nil {
		return reviewedRelease{}, false
	}
	var target reviewedRelease
	if entry, ok := u.lookupEntry(installed.Name); ok {
		target = u.integrationRelease(entry)
	} else if provider, ok := u.lookupHomeProvider(installed.Name); ok {
		target = homeProviderRelease(provider)
	} else {
		return reviewedRelease{}, false
	}
	entry := target.entry
	if entry.PluginID != installed.Name || entry.FallbackSource() == "" ||
		installed.Format != entry.SourceFormat ||
		(!entry.IsPinnedSource(installed.Source) && !entry.IsUnpinnedOfficialSource(installed.Source)) {
		return reviewedRelease{}, false
	}
	return target, true
}

func (u *reviewedIntegrationUpdates) lookupEntry(pluginID string) (reviewedintegration.Entry, bool) {
	if u.entryFor == nil {
		return reviewedintegration.Entry{}, false
	}
	return u.entryFor(pluginID)
}

func (u *reviewedIntegrationUpdates) lookupHomeProvider(pluginID string) (reviewedintegration.HomeProvider, bool) {
	if u.homeProviderFor == nil {
		return reviewedintegration.HomeProvider{}, false
	}
	return u.homeProviderFor(pluginID)
}

// newestLoadable walks the resolver's candidates, newest first, and returns the
// newest release that wanted accepts and that this build can load. The walk
// stops, offering nothing, at the first candidate wanted refuses — the list is
// ordered, so an up-to-date install performs no inspection at all — and at a
// candidate that is not an exact official commit at or above the floor, which
// the resolver never produces. Only a "this build cannot load it" verdict is
// stepped over to the next release.
//
// Any other inspection failure — an unreachable source, a bad clone, a
// malformed manifest, an identity or trust mismatch — is returned as an error.
// It never slides silently to an older release.
//
// Locking: inspect is plugin.Manager.Inspect, which takes the manager's
// operation lock. No caller holds it: UpdateChecker.checkOne and pluginhttp's
// replacementFor call their hook after Manager.List has returned, and
// blueprint recovery inspects before it installs.
func (u *reviewedIntegrationUpdates) newestLoadable(ctx context.Context, target reviewedRelease, wanted func(version string) bool) (verifiedRelease, bool, error) {
	entry := target.entry
	candidates := u.releases.Candidates(ctx, entry)
	if len(candidates) == 0 {
		candidates = []integrationrelease.Resolution{u.releases.Resolve(ctx, entry)}
	}
	for index, candidate := range candidates {
		if index >= integrationrelease.MaxCandidates {
			break
		}
		if !wanted(candidate.Version) {
			return verifiedRelease{}, false, nil
		}
		if !entry.IsPinnedSource(candidate.Source) || !reviewedintegration.AtLeast(candidate.Version, entry.MinimumVersion) {
			return verifiedRelease{}, false, nil
		}
		if u.inspect == nil {
			return verifiedRelease{}, false, errNoReleaseInspector
		}
		descriptor, report, inspectErr := u.inspect(candidate.Source, entry.SourceFormat)
		switch target.verify(candidate.Version, candidate.Source, descriptor, report, inspectErr) {
		case releaseLoadable:
			return verifiedRelease{version: candidate.Version, source: candidate.Source, report: report}, true, nil
		case releaseNeedsNewerHost:
			continue
		default:
			// Deliberately omit the inspection error: it can carry a clone path.
			return verifiedRelease{}, false, fmt.Errorf("reviewed release %s could not be verified", candidate.Version)
		}
	}
	return verifiedRelease{}, false, nil
}

// newerRelease reports the exact source of the newest loadable release that is
// newer than the installed version. An update therefore never downgrades and
// never offers a release that would be refused on install.
func (u *reviewedIntegrationUpdates) newerRelease(ctx context.Context, target reviewedRelease, installed plugin.InstalledPlugin) (version, source string, ok bool, err error) {
	release, ok, err := u.newestLoadable(ctx, target, func(version string) bool {
		order, comparable := reviewedintegration.CompareVersions(version, installed.Version)
		return comparable && order > 0
	})
	return release.version, release.source, ok, err
}

// homeProviderInstall returns the newest reviewed release of a Home provider
// that this build can install, for a first install. ok is false when nothing
// can be offered.
func (u *reviewedIntegrationUpdates) homeProviderInstall(ctx context.Context, provider reviewedintegration.HomeProvider) (verifiedRelease, bool, error) {
	if u == nil || u.releases == nil {
		return verifiedRelease{}, false, nil
	}
	return u.newestLoadable(ctx, homeProviderRelease(provider), func(string) bool { return true })
}

// availability is the update checker override for reviewed installs. Every
// answer is terminal. A failed verification is an error, so the checker keeps
// the last result rather than reading the recorded source.
func (u *reviewedIntegrationUpdates) availability(installed plugin.InstalledPlugin) (plugin.UpdateAvailability, bool, error) {
	target, ok := u.reviewedEntry(installed)
	if !ok {
		return plugin.UpdateAvailability{}, false, nil
	}
	version, _, newer, err := u.newerRelease(context.Background(), target, installed)
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
func (u *reviewedIntegrationUpdates) replacement(ctx context.Context, installed plugin.InstalledPlugin) pluginhttp.ReviewedUpdate {
	target, ok := u.reviewedEntry(installed)
	if !ok {
		return pluginhttp.ReviewedUpdate{}
	}
	if _, source, newer, err := u.newerRelease(ctx, target, installed); err == nil && newer {
		return pluginhttp.ReviewedUpdate{Source: source, Format: target.entry.SourceFormat}
	}
	if target.entry.IsUnpinnedOfficialSource(installed.Source) {
		return pluginhttp.ReviewedUpdate{Refuse: true}
	}
	return pluginhttp.ReviewedUpdate{}
}

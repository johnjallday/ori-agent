package reviewedintegration

import (
	"errors"
	"sort"
	"strings"

	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/specialist"
)

// MaxHomeProviders bounds the built-in Home provider allowlist.
const MaxHomeProviders = 16

// HomeProvider is one host-reviewed plugin that contributes an independent
// Assistant Program Home and nothing a project runs: no blueprint, service,
// MCP server, or platform artifact. A project blueprint names its Home provider
// only by plugin ID. This allowlist is what lets Ori offer to install that
// provider from a reviewed release instead of sending the user to find it.
//
// It is deliberately separate from Entry. An Entry describes a project
// integration and drives install quests, setup journeys, and blueprint checks;
// a Home provider takes part in none of those.
type HomeProvider struct {
	Key      string
	PluginID string
	// DisplayName is inert plain-text copy naming the plugin to the user.
	DisplayName          string
	MinimumVersion       string
	SourceRepository     string
	FallbackCommit       string
	SourceFormat         plugin.SourceFormat
	PublisherLabel       string
	SourceLabel          string
	ProgramID            string
	HomeSchemaVersion    int
	RequiredHostFeatures []string
	ExpectedProtocol     int
	ReleaseReady         bool
}

// ReleaseEntry carries only the provider's release identity — key, plugin,
// repository, floor, fallback commit, and format — in the shape the release
// resolver and the source classifiers read. It is not a project integration
// and must never be passed to anything that checks blueprints or platforms.
func (provider HomeProvider) ReleaseEntry() Entry {
	return Entry{
		Key: provider.Key, PluginID: provider.PluginID, DisplayName: provider.DisplayName,
		MinimumVersion: provider.MinimumVersion, SourceRepository: provider.SourceRepository,
		FallbackCommit: provider.FallbackCommit, SourceFormat: provider.SourceFormat,
		PublisherLabel: provider.PublisherLabel, SourceLabel: provider.SourceLabel,
		ReleaseReady: provider.ReleaseReady,
	}
}

func (provider HomeProvider) Clone() HomeProvider {
	provider.RequiredHostFeatures = append([]string(nil), provider.RequiredHostFeatures...)
	return provider
}

// HomeProviderFor returns the reviewed Home provider with this plugin ID.
func HomeProviderFor(pluginID string) (HomeProvider, bool) {
	pluginID = strings.ToLower(strings.TrimSpace(pluginID))
	for _, provider := range builtInHomeProviders {
		if provider.PluginID == pluginID {
			return provider.Clone(), true
		}
	}
	return HomeProvider{}, false
}

// HomeProviders returns every reviewed Home provider.
func HomeProviders() []HomeProvider {
	providers := make([]HomeProvider, len(builtInHomeProviders))
	for index := range builtInHomeProviders {
		providers[index] = builtInHomeProviders[index].Clone()
	}
	return providers
}

// ReleaseCheck classifies one inspected Home provider release.
type ReleaseCheck int

const (
	// ReleaseLoadable means this build can install the release as reviewed.
	ReleaseLoadable ReleaseCheck = iota
	// ReleaseNeedsNewerHost means the release asks for a protocol or host
	// feature this build lacks. An older release is worth trying.
	ReleaseNeedsNewerHost
	// ReleaseRejected means the release is not the reviewed provider: a failed
	// read, another plugin, or a contribution beyond a Home. Nothing older is
	// tried in its place.
	ReleaseRejected
)

// CheckRelease verifies one inspected release against the reviewed provider.
// The release must be exactly the plugin, version, source, and format that
// were resolved, and must contribute exactly the reviewed Home and nothing a
// project runs.
func (provider HomeProvider) CheckRelease(version, source string, descriptor plugin.PluginDescriptor, report plugin.TrustReport, inspectErr error) ReleaseCheck {
	if inspectErr != nil {
		if plugin.ContributionErrorIs(inspectErr, plugin.CodeHostFeatureUnsupported) ||
			plugin.ContributionErrorIs(inspectErr, plugin.CodeProtocolIncompatible) {
			return ReleaseNeedsNewerHost
		}
		return ReleaseRejected
	}
	if descriptor.Name != provider.PluginID || descriptor.Version != version ||
		descriptor.SourceLocation != source || descriptor.SourceFormat != provider.SourceFormat ||
		report.Name != provider.PluginID || report.Format != provider.SourceFormat {
		return ReleaseRejected
	}
	contribution := descriptor.WorkspaceSurfaces
	if contribution == nil || contribution.Name != provider.PluginID || contribution.Version != version {
		return ReleaseRejected
	}
	if contribution.Protocol.Min > provider.ExpectedProtocol ||
		(contribution.Protocol.Max != 0 && contribution.Protocol.Max < provider.ExpectedProtocol) {
		return ReleaseNeedsNewerHost
	}
	for _, feature := range provider.RequiredHostFeatures {
		if !containsString(contribution.RequiresHostFeatures, feature) {
			return ReleaseRejected
		}
	}
	if len(descriptor.MCPServers) != 0 || len(descriptor.ResolvedBlueprints) != 0 ||
		len(contribution.Services) != 0 || len(contribution.Capabilities) != 0 ||
		len(contribution.Blueprints) != 0 || len(contribution.SetupQuests) != 0 ||
		len(report.Unsupported) != 0 {
		return ReleaseRejected
	}
	homes := 0
	for _, home := range contribution.AssistantProgramHomes {
		if home.ID == provider.ProgramID && home.SchemaVersion == provider.HomeSchemaVersion {
			homes++
		}
	}
	if homes != 1 || len(contribution.AssistantProgramHomes) != 1 {
		return ReleaseRejected
	}
	return ReleaseLoadable
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func normalizeHomeProvider(provider HomeProvider) (HomeProvider, error) {
	provider.Key = strings.ToLower(strings.TrimSpace(provider.Key))
	provider.PluginID = strings.ToLower(strings.TrimSpace(provider.PluginID))
	provider.DisplayName = strings.TrimSpace(provider.DisplayName)
	provider.MinimumVersion = strings.TrimSpace(provider.MinimumVersion)
	provider.SourceRepository = strings.TrimSuffix(strings.TrimSpace(provider.SourceRepository), "/")
	provider.FallbackCommit = strings.ToLower(strings.TrimSpace(provider.FallbackCommit))
	provider.PublisherLabel = strings.TrimSpace(provider.PublisherLabel)
	provider.SourceLabel = strings.TrimSpace(provider.SourceLabel)
	provider.ProgramID = strings.ToLower(strings.TrimSpace(provider.ProgramID))
	if specialist.ValidateSetupJourneyText("display_name", provider.DisplayName, MaxDisplayNameBytes) != nil ||
		strings.ContainsAny(provider.DisplayName, "\n\t") {
		return HomeProvider{}, errors.New("reviewed Home provider display name is invalid")
	}
	if !registryIDPattern.MatchString(provider.Key) || !registryIDPattern.MatchString(provider.PluginID) ||
		!registryIDPattern.MatchString(provider.ProgramID) || !versionPattern.MatchString(provider.MinimumVersion) ||
		provider.PublisherLabel == "" || len(provider.PublisherLabel) > 100 ||
		provider.SourceLabel == "" || len(provider.SourceLabel) > 200 ||
		!strings.HasPrefix(provider.SourceRepository, "https://github.com/") ||
		provider.SourceFormat != plugin.FormatClaude || provider.HomeSchemaVersion <= 0 || provider.ExpectedProtocol <= 0 {
		return HomeProvider{}, errors.New("reviewed Home provider is invalid")
	}
	if provider.FallbackCommit != "" && !ValidCommit(provider.FallbackCommit) {
		return HomeProvider{}, errors.New("reviewed Home provider fallback commit is invalid")
	}
	if provider.ReleaseReady && provider.FallbackCommit == "" {
		return HomeProvider{}, errors.New("release-ready reviewed Home provider has no fallback commit")
	}
	if len(provider.RequiredHostFeatures) == 0 || len(provider.RequiredHostFeatures) > 8 {
		return HomeProvider{}, errors.New("reviewed Home provider compatibility bounds are invalid")
	}
	seen := make(map[string]struct{}, len(provider.RequiredHostFeatures))
	for index, feature := range provider.RequiredHostFeatures {
		feature = strings.ToLower(strings.TrimSpace(feature))
		if !registryIDPattern.MatchString(feature) {
			return HomeProvider{}, errors.New("reviewed Home provider host feature is invalid")
		}
		if _, duplicate := seen[feature]; duplicate {
			return HomeProvider{}, errors.New("reviewed Home provider host feature is duplicated")
		}
		seen[feature] = struct{}{}
		provider.RequiredHostFeatures[index] = feature
	}
	sort.Strings(provider.RequiredHostFeatures)
	return provider, nil
}

// mustHomeProviders validates the built-in list. A provider may not share a
// key or plugin ID with a project integration: one plugin is reviewed as one
// kind of thing, so no lookup can answer for both.
func mustHomeProviders(providers []HomeProvider, integrations []Entry) []HomeProvider {
	if len(providers) > MaxHomeProviders {
		panic("invalid built-in reviewed Home provider list size")
	}
	taken := make(map[string]struct{}, 2*(len(providers)+len(integrations)))
	for _, entry := range integrations {
		taken["key:"+entry.Key] = struct{}{}
		taken["plugin:"+entry.PluginID] = struct{}{}
	}
	normalized := make([]HomeProvider, len(providers))
	for index, provider := range providers {
		value, err := normalizeHomeProvider(provider)
		if err != nil {
			panic("invalid built-in reviewed Home provider")
		}
		for _, claim := range []string{"key:" + value.Key, "plugin:" + value.PluginID} {
			if _, duplicate := taken[claim]; duplicate {
				panic("duplicate built-in reviewed Home provider")
			}
			taken[claim] = struct{}{}
		}
		normalized[index] = value
	}
	sort.Slice(normalized, func(i, j int) bool { return normalized[i].Key < normalized[j].Key })
	return normalized
}

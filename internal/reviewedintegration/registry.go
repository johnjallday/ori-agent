// Package reviewedintegration owns Ori's immutable pre-install integration
// allowlist. Journey declarations contain only a stable key; download/source
// identity is compiled here and never accepted from a browser or plugin.
package reviewedintegration

import (
	"errors"
	"regexp"
	"sort"
	"strings"

	"github.com/johnjallday/ori-agent/internal/plugin"
)

const (
	RegistryRevision     = 1
	MaxRegistryItems     = 16
	reviewedClaudeFormat = plugin.FormatClaude
)

// Entry is one host-reviewed immutable integration identity.
type Entry struct {
	Key                      string
	PluginID                 string
	ExpectedVersion          string
	SourceRepository         string
	SourceCommit             string
	SourceFormat             plugin.SourceFormat
	PublisherLabel           string
	SourceLabel              string
	ExpectedBlueprintID      string
	ExpectedBlueprintVersion int
	ExpectedProgramID        string
	ExpectedProgramSchema    int
	RequiredHostFeatures     []string
	ExpectedProtocol         int
	SupportedPlatforms       []string
	ReleaseReady             bool
}

// Source returns the immutable plugin.Manager source only after a human release
// owner has made the reviewed candidate reachable. A pending candidate has no
// install source rather than silently falling back to mutable main.
func (entry Entry) Source() string {
	if !entry.ReleaseReady || entry.SourceCommit == "" {
		return ""
	}
	return entry.SourceRepository + "#sha=" + entry.SourceCommit
}

func (entry Entry) Clone() Entry {
	entry.RequiredHostFeatures = append([]string(nil), entry.RequiredHostFeatures...)
	entry.SupportedPlatforms = append([]string(nil), entry.SupportedPlatforms...)
	return entry
}

func Get(key string) (Entry, bool) {
	key = strings.ToLower(strings.TrimSpace(key))
	for _, entry := range builtInEntries {
		if entry.Key == key {
			return entry.Clone(), true
		}
	}
	return Entry{}, false
}

func All() []Entry {
	entries := make([]Entry, len(builtInEntries))
	for index := range builtInEntries {
		entries[index] = builtInEntries[index].Clone()
	}
	return entries
}

var (
	registryIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	versionPattern    = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:[-+][A-Za-z0-9.-]+)?$`)
	commitPattern     = regexp.MustCompile(`^[a-f0-9]{40}$`)
	platformPattern   = regexp.MustCompile(`^[a-z0-9]+/[a-z0-9_]+$`)
)

func normalize(entry Entry) (Entry, error) {
	entry.Key = strings.ToLower(strings.TrimSpace(entry.Key))
	entry.PluginID = strings.ToLower(strings.TrimSpace(entry.PluginID))
	entry.ExpectedVersion = strings.TrimSpace(entry.ExpectedVersion)
	entry.SourceRepository = strings.TrimSuffix(strings.TrimSpace(entry.SourceRepository), "/")
	entry.SourceCommit = strings.ToLower(strings.TrimSpace(entry.SourceCommit))
	entry.PublisherLabel = strings.TrimSpace(entry.PublisherLabel)
	entry.SourceLabel = strings.TrimSpace(entry.SourceLabel)
	entry.ExpectedBlueprintID = strings.ToLower(strings.TrimSpace(entry.ExpectedBlueprintID))
	entry.ExpectedProgramID = strings.ToLower(strings.TrimSpace(entry.ExpectedProgramID))
	if !registryIDPattern.MatchString(entry.Key) || !registryIDPattern.MatchString(entry.PluginID) ||
		!registryIDPattern.MatchString(entry.ExpectedBlueprintID) || !registryIDPattern.MatchString(entry.ExpectedProgramID) ||
		!versionPattern.MatchString(entry.ExpectedVersion) || entry.PublisherLabel == "" || len(entry.PublisherLabel) > 100 ||
		entry.SourceLabel == "" || len(entry.SourceLabel) > 200 ||
		!strings.HasPrefix(entry.SourceRepository, "https://github.com/") ||
		entry.SourceFormat != plugin.FormatClaude || entry.ExpectedBlueprintVersion <= 0 ||
		entry.ExpectedProgramSchema <= 0 || entry.ExpectedProtocol <= 0 {
		return Entry{}, errors.New("reviewed integration entry is invalid")
	}
	if entry.SourceCommit != "" && !commitPattern.MatchString(entry.SourceCommit) {
		return Entry{}, errors.New("reviewed integration source commit is invalid")
	}
	if entry.ReleaseReady && entry.SourceCommit == "" {
		return Entry{}, errors.New("release-ready reviewed integration has no source commit")
	}
	if len(entry.RequiredHostFeatures) == 0 || len(entry.RequiredHostFeatures) > 8 ||
		len(entry.SupportedPlatforms) == 0 || len(entry.SupportedPlatforms) > 8 {
		return Entry{}, errors.New("reviewed integration compatibility bounds are invalid")
	}
	seen := make(map[string]struct{}, len(entry.RequiredHostFeatures))
	for index, feature := range entry.RequiredHostFeatures {
		feature = strings.ToLower(strings.TrimSpace(feature))
		if !registryIDPattern.MatchString(feature) {
			return Entry{}, errors.New("reviewed integration host feature is invalid")
		}
		if _, duplicate := seen[feature]; duplicate {
			return Entry{}, errors.New("reviewed integration host feature is duplicated")
		}
		seen[feature] = struct{}{}
		entry.RequiredHostFeatures[index] = feature
	}
	seen = make(map[string]struct{}, len(entry.SupportedPlatforms))
	for index, platform := range entry.SupportedPlatforms {
		platform = strings.ToLower(strings.TrimSpace(platform))
		if !platformPattern.MatchString(platform) {
			return Entry{}, errors.New("reviewed integration platform is invalid")
		}
		if _, duplicate := seen[platform]; duplicate {
			return Entry{}, errors.New("reviewed integration platform is duplicated")
		}
		seen[platform] = struct{}{}
		entry.SupportedPlatforms[index] = platform
	}
	sort.Strings(entry.RequiredHostFeatures)
	sort.Strings(entry.SupportedPlatforms)
	return entry, nil
}

func mustRegistry(entries []Entry) []Entry {
	if len(entries) == 0 || len(entries) > MaxRegistryItems {
		panic("invalid built-in reviewed integration registry size")
	}
	normalized := make([]Entry, len(entries))
	seen := make(map[string]struct{}, len(entries))
	for index, entry := range entries {
		value, err := normalize(entry)
		if err != nil {
			panic("invalid built-in reviewed integration registry")
		}
		if _, duplicate := seen[value.Key]; duplicate {
			panic("duplicate built-in reviewed integration key")
		}
		seen[value.Key] = struct{}{}
		normalized[index] = value
	}
	sort.Slice(normalized, func(i, j int) bool { return normalized[i].Key < normalized[j].Key })
	return normalized
}

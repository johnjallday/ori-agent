// Package reviewedintegration owns Ori's immutable pre-install integration
// allowlist. Journey declarations contain only a stable key; download/source
// identity is compiled here and never accepted from a browser or plugin.
package reviewedintegration

import (
	"errors"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/specialist"
)

const (
	RegistryRevision     = 3
	MaxRegistryItems     = 16
	reviewedClaudeFormat = plugin.FormatClaude

	// Display copy feeds the host-generated install quest, whose step titles
	// append a short suffix to DisplayName.
	MaxDisplayNameBytes        = 100
	MaxInstallTitleBytes       = specialist.MaxSetupJourneyTitleBytes
	MaxInstallDescriptionBytes = specialist.MaxSetupJourneyTextBytes
)

// Entry is one host-reviewed integration floor. A human reviewed the repository
// and the minimum release; later stable releases from the same repository are
// accepted once the identity, contribution and artifact checks pass.
// ExpectedProgramSchema and ExpectedProtocol are exact: they describe what this
// Ori build can run, not a property of one release.
type Entry struct {
	Key      string
	PluginID string
	// DisplayName, InstallTitle and InstallDescription are inert plain-text
	// copy for the generated install quest. They select no behavior.
	DisplayName             string
	InstallTitle            string
	InstallDescription      string
	MinimumVersion          string
	SourceRepository        string
	FallbackCommit          string
	SourceFormat            plugin.SourceFormat
	PublisherLabel          string
	SourceLabel             string
	ExpectedBlueprintID     string
	MinimumBlueprintVersion int
	ExpectedProgramID       string
	ExpectedProgramSchema   int
	RequiredHostFeatures    []string
	ExpectedProtocol        int
	SupportedPlatforms      []string
	ReleaseReady            bool
}

// FallbackSource returns the immutable plugin.Manager source of the reviewed
// minimum release, used when the latest release cannot be resolved. It exists
// only after a human release owner has made the reviewed candidate reachable. A
// pending candidate has no install source rather than silently falling back to
// mutable main.
func (entry Entry) FallbackSource() string {
	if !entry.ReleaseReady || entry.FallbackCommit == "" {
		return ""
	}
	return entry.PinnedSource(entry.FallbackCommit)
}

// PinnedSource is the plugin.Manager source for one exact commit of the
// reviewed repository.
func (entry Entry) PinnedSource(commit string) string {
	return entry.SourceRepository + "#sha=" + commit
}

// InstallQuestPrefix prefixes an integration key to form the ID of the install
// quest Ori generates for it.
const InstallQuestPrefix = "install_"

// InstallQuestID is the host quest ID generated for this integration.
func (entry Entry) InstallQuestID() string {
	return InstallQuestPrefix + strings.ToLower(strings.TrimSpace(entry.Key))
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

// ValidVersion reports whether value is a registry version: MAJOR.MINOR.PATCH
// with an optional prerelease or build suffix.
func ValidVersion(value string) bool {
	return versionPattern.MatchString(value)
}

// StableVersion reports whether value is a registry version without a
// prerelease suffix. Build metadata does not make a version a prerelease.
func StableVersion(value string) bool {
	return ValidVersion(value) && !strings.Contains(strings.SplitN(value, "+", 2)[0], "-")
}

// ValidCommit reports whether value is a full lowercase hex commit ID.
func ValidCommit(value string) bool {
	return commitPattern.MatchString(value)
}

// CompareVersions orders two registry versions by semantic-version precedence:
// numeric MAJOR.MINOR.PATCH, then a prerelease sorts before its release. Build
// metadata is ignored. ok is false when either value is not a registry version,
// so an unparseable version never compares as equal to a floor.
func CompareVersions(left, right string) (order int, ok bool) {
	if !ValidVersion(left) || !ValidVersion(right) {
		return 0, false
	}
	split := func(value string) (core []int, prerelease []string) {
		value = strings.SplitN(value, "+", 2)[0]
		parts := strings.SplitN(value, "-", 2)
		for _, segment := range strings.Split(parts[0], ".") {
			number, err := strconv.Atoi(segment)
			if err != nil {
				number = math.MaxInt
			}
			core = append(core, number)
		}
		if len(parts) == 2 {
			prerelease = strings.Split(parts[1], ".")
		}
		return core, prerelease
	}
	leftCore, leftPre := split(left)
	rightCore, rightPre := split(right)
	for index := range leftCore {
		if order := compareInts(leftCore[index], rightCore[index]); order != 0 {
			return order, true
		}
	}
	switch {
	case len(leftPre) == 0 && len(rightPre) == 0:
		return 0, true
	case len(leftPre) == 0:
		return 1, true
	case len(rightPre) == 0:
		return -1, true
	}
	for index := 0; index < len(leftPre) && index < len(rightPre); index++ {
		leftNumber, leftErr := strconv.Atoi(leftPre[index])
		rightNumber, rightErr := strconv.Atoi(rightPre[index])
		switch {
		case leftErr == nil && rightErr == nil:
			if order := compareInts(leftNumber, rightNumber); order != 0 {
				return order, true
			}
		case leftErr == nil:
			return -1, true
		case rightErr == nil:
			return 1, true
		default:
			if order := strings.Compare(leftPre[index], rightPre[index]); order != 0 {
				return order, true
			}
		}
	}
	return compareInts(len(leftPre), len(rightPre)), true
}

// AtLeast reports whether version is a registry version at or above minimum.
func AtLeast(version, minimum string) bool {
	order, ok := CompareVersions(version, minimum)
	return ok && order >= 0
}

func compareInts(left, right int) int {
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}

func normalize(entry Entry) (Entry, error) {
	entry.Key = strings.ToLower(strings.TrimSpace(entry.Key))
	entry.PluginID = strings.ToLower(strings.TrimSpace(entry.PluginID))
	entry.MinimumVersion = strings.TrimSpace(entry.MinimumVersion)
	entry.SourceRepository = strings.TrimSuffix(strings.TrimSpace(entry.SourceRepository), "/")
	entry.FallbackCommit = strings.ToLower(strings.TrimSpace(entry.FallbackCommit))
	entry.PublisherLabel = strings.TrimSpace(entry.PublisherLabel)
	entry.SourceLabel = strings.TrimSpace(entry.SourceLabel)
	entry.ExpectedBlueprintID = strings.ToLower(strings.TrimSpace(entry.ExpectedBlueprintID))
	entry.ExpectedProgramID = strings.ToLower(strings.TrimSpace(entry.ExpectedProgramID))
	entry.DisplayName = strings.TrimSpace(entry.DisplayName)
	entry.InstallTitle = strings.TrimSpace(entry.InstallTitle)
	entry.InstallDescription = strings.TrimSpace(entry.InstallDescription)
	if specialist.ValidateSetupJourneyText("display_name", entry.DisplayName, MaxDisplayNameBytes) != nil ||
		strings.ContainsAny(entry.DisplayName, "\n\t") ||
		specialist.ValidateSetupJourneyText("install_title", entry.InstallTitle, MaxInstallTitleBytes) != nil ||
		strings.ContainsAny(entry.InstallTitle, "\n\t") ||
		specialist.ValidateSetupJourneyText("install_description", entry.InstallDescription, MaxInstallDescriptionBytes) != nil {
		return Entry{}, errors.New("reviewed integration display copy is invalid")
	}
	if !registryIDPattern.MatchString(entry.Key) || !registryIDPattern.MatchString(entry.PluginID) ||
		!registryIDPattern.MatchString(entry.ExpectedBlueprintID) || !registryIDPattern.MatchString(entry.ExpectedProgramID) ||
		!versionPattern.MatchString(entry.MinimumVersion) || entry.PublisherLabel == "" || len(entry.PublisherLabel) > 100 ||
		entry.SourceLabel == "" || len(entry.SourceLabel) > 200 ||
		!strings.HasPrefix(entry.SourceRepository, "https://github.com/") ||
		entry.SourceFormat != plugin.FormatClaude || entry.MinimumBlueprintVersion <= 0 ||
		entry.ExpectedProgramSchema <= 0 || entry.ExpectedProtocol <= 0 {
		return Entry{}, errors.New("reviewed integration entry is invalid")
	}
	if entry.FallbackCommit != "" && !ValidCommit(entry.FallbackCommit) {
		return Entry{}, errors.New("reviewed integration fallback commit is invalid")
	}
	if entry.ReleaseReady && entry.FallbackCommit == "" {
		return Entry{}, errors.New("release-ready reviewed integration has no fallback commit")
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

package registry

import (
	"regexp"
	"sort"
	"strings"
)

// platformPattern matches platform strings in filenames like "darwin-arm64", "linux_amd64", etc.
var platformPattern = regexp.MustCompile(`(darwin|linux|windows|freebsd)[-_](amd64|arm64|386|arm)`)

// extractPlatformsFromAssets extracts platform strings from GitHub release asset filenames
// Example filenames:
// - "plugin-name-v1.0.0-darwin-arm64"
// - "plugin-name-v1.0.0-linux_amd64.tar.gz"
// - "plugin-name-darwin-arm64"
func extractPlatformsFromAssets(assets []string) []string {
	platformsMap := make(map[string]bool)

	for _, asset := range assets {
		matches := platformPattern.FindAllStringSubmatch(asset, -1)
		for _, match := range matches {
			if len(match) >= 3 {
				// match[1] is OS, match[2] is arch
				platform := match[1] + "-" + match[2]
				platformsMap[platform] = true
			}
		}
	}

	// Convert map to sorted slice
	platforms := make([]string, 0, len(platformsMap))
	for platform := range platformsMap {
		platforms = append(platforms, platform)
	}
	sort.Strings(platforms)

	return platforms
}

// extractOSAndArch separates a list of platform strings into OS and architecture lists
// Example: ["darwin-arm64", "darwin-amd64", "linux-amd64"] ->
//
//	(["darwin", "linux"], ["amd64", "arm64"])
func extractOSAndArch(platforms []string) ([]string, []string) {
	osMap := make(map[string]bool)
	archMap := make(map[string]bool)

	for _, platform := range platforms {
		parts := strings.Split(platform, "-")
		if len(parts) == 2 {
			osMap[parts[0]] = true
			archMap[parts[1]] = true
		}
	}

	// Convert maps to sorted slices
	osList := make([]string, 0, len(osMap))
	for os := range osMap {
		osList = append(osList, os)
	}
	sort.Strings(osList)

	archList := make([]string, 0, len(archMap))
	for arch := range archMap {
		archList = append(archList, arch)
	}
	sort.Strings(archList)

	return osList, archList
}

package health

import (
	"fmt"
	"strconv"
	"strings"
)

// CompareVersions compares two semantic version strings
// Returns:
//
//	 1 if v1 > v2
//	 0 if v1 == v2
//	-1 if v1 < v2
//	error if versions are invalid
func CompareVersions(v1, v2 string) (int, error) {
	// Remove 'v' prefix if present
	v1 = strings.TrimPrefix(v1, "v")
	v2 = strings.TrimPrefix(v2, "v")

	// Handle special cases
	if v1 == "dev" || v2 == "dev" {
		return 0, nil // Treat dev as compatible
	}

	// Parse versions
	parts1, err := parseVersion(v1)
	if err != nil {
		return 0, fmt.Errorf("invalid version v1 '%s': %w", v1, err)
	}

	parts2, err := parseVersion(v2)
	if err != nil {
		return 0, fmt.Errorf("invalid version v2 '%s': %w", v2, err)
	}

	// Compare major.minor.patch
	for i := 0; i < 3; i++ {
		if parts1[i] > parts2[i] {
			return 1, nil
		}
		if parts1[i] < parts2[i] {
			return -1, nil
		}
	}

	return 0, nil
}

// parseVersion parses a semantic version string into [major, minor, patch]
func parseVersion(version string) ([3]int, error) {
	var result [3]int

	// Remove any pre-release or metadata suffix (e.g., "1.2.3-beta" -> "1.2.3")
	if idx := strings.IndexAny(version, "-+"); idx != -1 {
		version = version[:idx]
	}

	parts := strings.Split(version, ".")
	if len(parts) != 3 {
		return result, fmt.Errorf("version must have 3 parts (major.minor.patch), got: %s", version)
	}

	for i, part := range parts {
		num, err := strconv.Atoi(part)
		if err != nil {
			return result, fmt.Errorf("invalid version number '%s': %w", part, err)
		}
		result[i] = num
	}

	return result, nil
}

// IsCompatible checks if a plugin version is compatible with the agent version
// based on min/max requirements
func IsCompatible(agentVersion, pluginVersion, minVersion, maxVersion string) (bool, string) {
	// If no requirements specified, assume compatible
	if minVersion == "" && maxVersion == "" {
		return true, "No version requirements specified"
	}

	// Check minimum version
	if minVersion != "" {
		cmp, err := CompareVersions(agentVersion, minVersion)
		if err != nil {
			return false, fmt.Sprintf("Version comparison error: %v", err)
		}
		if cmp < 0 {
			return false, fmt.Sprintf("Agent version %s is below minimum required %s", agentVersion, minVersion)
		}
	}

	// Check maximum version
	if maxVersion != "" {
		cmp, err := CompareVersions(agentVersion, maxVersion)
		if err != nil {
			return false, fmt.Sprintf("Version comparison error: %v", err)
		}
		if cmp > 0 {
			return false, fmt.Sprintf("Agent version %s exceeds maximum compatible %s", agentVersion, maxVersion)
		}
	}

	return true, "Version requirements met"
}

// IsAPICompatible checks if plugin API version matches agent API version
func IsAPICompatible(agentAPIVersion, pluginAPIVersion string) (bool, string) {
	if agentAPIVersion == pluginAPIVersion {
		return true, fmt.Sprintf("API version %s matches", agentAPIVersion)
	}
	return false, fmt.Sprintf("API version mismatch: agent=%s, plugin=%s", agentAPIVersion, pluginAPIVersion)
}

// IsVersionOlder checks if v1 is older than v2
// Returns (true, nil) if v1 < v2
// Returns (false, nil) if v1 >= v2
// Returns (false, error) if versions cannot be compared
func IsVersionOlder(v1, v2 string) (bool, error) {
	cmp, err := CompareVersions(v1, v2)
	if err != nil {
		return false, err
	}
	return cmp < 0, nil
}

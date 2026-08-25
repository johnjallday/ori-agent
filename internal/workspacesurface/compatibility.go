package workspacesurface

import (
	"fmt"
	"strings"
)

// PlatformArtifact is the inert compatibility portion of a trusted artifact
// descriptor. Download source, digest, size, and managed executable path remain
// in the plugin installer/installed registry and are intentionally absent here.
type PlatformArtifact struct {
	ID   string
	OS   string
	Arch string
}

// SelectPlatformArtifact requires one exact OS/architecture match and rejects
// ambiguous duplicate tuples. Unsupported platforms never fall back to another
// architecture or a source build.
func SelectPlatformArtifact(artifacts []PlatformArtifact, goos, goarch string) (PlatformArtifact, bool, error) {
	goos = strings.ToLower(strings.TrimSpace(goos))
	goarch = strings.ToLower(strings.TrimSpace(goarch))
	if goos == "" || goarch == "" {
		return PlatformArtifact{}, false, fmt.Errorf("workspace surface platform is invalid")
	}
	seen := make(map[string]struct{}, len(artifacts))
	var selected PlatformArtifact
	found := false
	for _, artifact := range artifacts {
		if err := validateID("artifact", artifact.ID); err != nil {
			return PlatformArtifact{}, false, err
		}
		osName := strings.ToLower(strings.TrimSpace(artifact.OS))
		arch := strings.ToLower(strings.TrimSpace(artifact.Arch))
		if osName == "" || arch == "" {
			return PlatformArtifact{}, false, fmt.Errorf("workspace surface artifact %q platform is invalid", artifact.ID)
		}
		tuple := osName + "\x00" + arch
		if _, duplicate := seen[tuple]; duplicate {
			return PlatformArtifact{}, false, fmt.Errorf("workspace surface artifact platform %s/%s is declared twice", osName, arch)
		}
		seen[tuple] = struct{}{}
		if osName == goos && arch == goarch {
			selected = artifact
			selected.OS = osName
			selected.Arch = arch
			found = true
		}
	}
	return selected, found, nil
}

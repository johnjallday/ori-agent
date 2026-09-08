package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const (
	maxResetAgentEntries = 10000
	maxResetAgentFile    = 1024 * 1024
)

// ResetAgentPersistence removes exactly the entries interpreted as global
// agents by fileStore.load: each immediate profile directory and each valid
// legacy flat JSON profile. Other files and symlinks in the profiles root are
// preserved. Index and compatibility projection files are removed separately
// because either may alias the same path.
func ResetAgentPersistence(indexPath, profilesPath, projectionPath string) (int, error) {
	removed, err := resetAgentProfiles(profilesPath)
	if err != nil {
		return removed, err
	}
	seen := make(map[string]bool)
	for _, path := range []string{indexPath, projectionPath} {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return removed, fmt.Errorf("resolve agent index: %w", err)
		}
		if seen[absolute] {
			continue
		}
		seen[absolute] = true
		if err := os.Remove(absolute); err != nil && !errors.Is(err, os.ErrNotExist) {
			return removed, fmt.Errorf("remove agent index: %w", err)
		}
	}
	return removed, nil
}

func resetAgentProfiles(path string) (int, error) {
	root, err := os.OpenRoot(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("open agent profiles: %w", err)
	}
	defer func() { _ = root.Close() }()
	directory, err := root.Open(".")
	if err != nil {
		return 0, fmt.Errorf("inspect agent profiles: %w", err)
	}
	entries, readErr := directory.ReadDir(maxResetAgentEntries + 1)
	closeErr := directory.Close()
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return 0, fmt.Errorf("list agent profiles: %w", readErr)
	}
	if closeErr != nil {
		return 0, fmt.Errorf("close agent profiles: %w", closeErr)
	}
	if len(entries) > maxResetAgentEntries {
		return 0, errors.New("agent reset exceeds the bounded entry limit")
	}

	owned := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !filepath.IsLocal(entry.Name()) || entry.Name() == "." {
			continue
		}
		if entry.IsDir() {
			owned = append(owned, entry.Name())
			continue
		}
		if filepath.Ext(entry.Name()) != ".json" || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		data, err := root.ReadFile(entry.Name())
		if err != nil {
			return 0, fmt.Errorf("inspect legacy agent profile: %w", err)
		}
		if len(data) == 0 || len(data) > maxResetAgentFile {
			continue
		}
		var profile map[string]any
		if json.Unmarshal(data, &profile) == nil && profile != nil {
			owned = append(owned, entry.Name())
		}
	}
	for _, name := range owned {
		if err := root.RemoveAll(name); err != nil {
			return 0, fmt.Errorf("remove owned agent profile: %w", err)
		}
	}
	return len(owned), nil
}

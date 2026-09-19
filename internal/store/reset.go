package store

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	maxResetAgentEntries = 10000
	maxResetAgentFile    = 1024 * 1024
)

// ResetLocations is everything an agents reset removes.
type ResetLocations struct {
	// Index, Profiles, and Projection are the data-dir store's own files
	// (see PersistencePaths): the built-in assistant, and any agent not yet
	// moved into the Workspace Directory.
	Index, Profiles, Projection string
	// State is <data dir>/agent_state, every agent's runtime state.
	State string
	// RootAgents is <root>/Agents, the user's agents in the Workspace
	// Directory. Empty when agents live only in the data dir
	// (AGENT_STORE_PATH).
	RootAgents string
}

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

// ResetRootAgents removes the user's agents from a Workspace Directory's
// Agents folder: each immediate folder holding an agent definition, which
// takes the agent's image, skills, and tool settings with it. Every other file
// and folder there — and the Agents folder itself — is kept. Symbolic links
// are never followed or removed.
func ResetRootAgents(agentsDir string) (int, error) {
	return removeOwnedEntries(agentsDir, "agents folder", func(root *os.Root, entry os.DirEntry) bool {
		if !entry.IsDir() {
			return false
		}
		info, err := root.Lstat(filepath.Join(entry.Name(), definitionFileName))
		return err == nil && info.Mode().IsRegular()
	})
}

// ResetAgentState removes every agent's runtime state: each root-key folder
// under the agent_state folder. Other files there are kept.
func ResetAgentState(stateDir string) (int, error) {
	return removeOwnedEntries(stateDir, "agent state", func(_ *os.Root, entry os.DirEntry) bool {
		return entry.IsDir() && isRootKey(entry.Name())
	})
}

// isRootKey reports whether name has the form RootKey produces.
func isRootKey(name string) bool {
	if len(name) != rootKeyLength {
		return false
	}
	_, err := hex.DecodeString(name)
	return err == nil && strings.ToLower(name) == name
}

// removeOwnedEntries removes the immediate entries of dir that owned accepts,
// through an os.Root confined to dir. A missing dir has nothing to remove.
func removeOwnedEntries(dir, label string, owned func(*os.Root, os.DirEntry) bool) (int, error) {
	root, err := os.OpenRoot(dir)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("open %s: %w", label, err)
	}
	defer func() { _ = root.Close() }()
	directory, err := root.Open(".")
	if err != nil {
		return 0, fmt.Errorf("inspect %s: %w", label, err)
	}
	entries, readErr := directory.ReadDir(maxResetAgentEntries + 1)
	closeErr := directory.Close()
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return 0, fmt.Errorf("list %s: %w", label, readErr)
	}
	if closeErr != nil {
		return 0, fmt.Errorf("close %s: %w", label, closeErr)
	}
	if len(entries) > maxResetAgentEntries {
		return 0, fmt.Errorf("%s reset exceeds the bounded entry limit", label)
	}
	removed := 0
	for _, entry := range entries {
		if !filepath.IsLocal(entry.Name()) || entry.Type()&os.ModeSymlink != 0 || !owned(root, entry) {
			continue
		}
		if err := root.RemoveAll(entry.Name()); err != nil {
			return removed, fmt.Errorf("remove from %s: %w", label, err)
		}
		removed++
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

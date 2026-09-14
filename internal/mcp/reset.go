package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
)

// Offline registry maintenance for the staged Settings reset. These helpers read
// and rewrite the persisted global registry document only. They never construct
// a Registry, start or connect to a server, contact a catalog, or touch an
// external CLI's own configuration, so they are safe at the pre-store recovery
// boundary where no runtime owner exists yet.

const maxRegistryBytes = 8 << 20

// ErrRegistryUnreadable reports a global registry document that cannot be
// interpreted. Reset must block on it rather than treat it as "no servers".
var ErrRegistryUnreadable = errors.New("mcp: global registry document cannot be read safely")

// RegisteredServerNames lists the persisted server names at path. A missing
// document is an empty registry; an unreadable or malformed one is an error,
// never an empty result.
func RegisteredServerNames(path string) ([]string, error) {
	config, _, err := readGlobalConfigFile(path)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(config.Servers))
	for _, server := range config.Servers {
		names = append(names, server.Name)
	}
	sort.Strings(names)
	return names, nil
}

// RemoveRegisteredServers deletes exactly the named entries from the persisted
// registry at path and reports how many were present. Names that are absent are
// not an error: removal is idempotent so a retry after a partial reset
// converges. Unrelated entries are preserved byte-for-byte in field content and
// order, and the document is rewritten atomically.
func RemoveRegisteredServers(path string, names []string) (int, error) {
	if len(names) == 0 {
		return 0, nil
	}
	config, mode, err := readGlobalConfigFile(path)
	if err != nil {
		return 0, err
	}
	if len(config.Servers) == 0 {
		return 0, nil
	}
	remaining := make([]ServerConfig, 0, len(config.Servers))
	removed := 0
	for _, server := range config.Servers {
		if slices.Contains(names, server.Name) {
			removed++
			continue
		}
		remaining = append(remaining, server)
	}
	if removed == 0 {
		return 0, nil
	}
	config.Servers = remaining
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return 0, fmt.Errorf("%w: encode", ErrRegistryUnreadable)
	}
	if err := writeFileAtomic(path, data, mode); err != nil {
		return 0, err
	}
	return removed, nil
}

// readGlobalConfigFile returns the parsed document and the mode to preserve when
// rewriting it. A missing file yields an empty registry and the default mode.
func readGlobalConfigFile(path string) (GlobalConfig, os.FileMode, error) {
	empty := GlobalConfig{Servers: []ServerConfig{}}
	if filepath.Clean(path) != path || !filepath.IsAbs(path) {
		return empty, 0, fmt.Errorf("%w: location is not canonical", ErrRegistryUnreadable)
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return empty, 0o600, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxRegistryBytes {
		return empty, 0, fmt.Errorf("%w: unexpected file type or size", ErrRegistryUnreadable)
	}
	data, err := os.ReadFile(path) // #nosec G304 -- canonical absolute registry document resolved by the caller, never client supplied
	if err != nil {
		return empty, 0, fmt.Errorf("%w: read", ErrRegistryUnreadable)
	}
	var config GlobalConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return empty, 0, fmt.Errorf("%w: parse", ErrRegistryUnreadable)
	}
	if config.Servers == nil {
		config.Servers = []ServerConfig{}
	}
	return config, info.Mode().Perm(), nil
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	if mode == 0 {
		mode = 0o600
	}
	directory := filepath.Dir(path)
	temp, err := os.CreateTemp(directory, ".mcp-registry-*.tmp")
	if err != nil {
		return fmt.Errorf("%w: stage", ErrRegistryUnreadable)
	}
	tempPath := temp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tempPath)
		}
	}()
	if err := temp.Chmod(mode); err != nil {
		_ = temp.Close()
		return fmt.Errorf("%w: stage permissions", ErrRegistryUnreadable)
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return fmt.Errorf("%w: stage write", ErrRegistryUnreadable)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return fmt.Errorf("%w: stage sync", ErrRegistryUnreadable)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("%w: stage close", ErrRegistryUnreadable)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("%w: publish", ErrRegistryUnreadable)
	}
	committed = true
	if parent, err := os.Open(directory); err == nil { // #nosec G304 -- parent of the canonical registry document
		syncErr := parent.Sync()
		closeErr := parent.Close()
		return errors.Join(syncErr, closeErr)
	}
	return nil
}

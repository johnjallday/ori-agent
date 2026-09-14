package mcp

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func writeTestRegistry(t *testing.T, path string, names ...string) {
	t.Helper()
	config := GlobalConfig{Servers: []ServerConfig{}}
	for _, name := range names {
		config.Servers = append(config.Servers, ServerConfig{
			Name: name, Command: "offline-reset-never-runs-this", Transport: "stdio",
		})
	}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func registryPath(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve root: %v", err)
	}
	return filepath.Join(root, "mcp_registry.json")
}

func TestRemoveRegisteredServersRemovesOnlyNamedEntries(t *testing.T) {
	path := registryPath(t)
	writeTestRegistry(t, path, "demo-plugin/tools", "user-owned", "other-plugin/tools")

	removed, err := RemoveRegisteredServers(path, []string{"demo-plugin/tools"})
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if removed != 1 {
		t.Fatalf("expected one removal, got %d", removed)
	}
	names, err := RegisteredServerNames(path)
	if err != nil {
		t.Fatalf("read names: %v", err)
	}
	if !slices.Equal(names, []string{"other-plugin/tools", "user-owned"}) {
		t.Fatalf("unrelated registrations were not preserved: %v", names)
	}
}

func TestRemoveRegisteredServersIsIdempotent(t *testing.T) {
	path := registryPath(t)
	writeTestRegistry(t, path, "demo-plugin/tools", "user-owned")
	for attempt := range 3 {
		removed, err := RemoveRegisteredServers(path, []string{"demo-plugin/tools"})
		if err != nil {
			t.Fatalf("attempt %d: %v", attempt, err)
		}
		if attempt > 0 && removed != 0 {
			t.Fatalf("attempt %d re-removed an absent entry: %d", attempt, removed)
		}
	}
	names, err := RegisteredServerNames(path)
	if err != nil {
		t.Fatalf("read names: %v", err)
	}
	if !slices.Equal(names, []string{"user-owned"}) {
		t.Fatalf("unrelated registration was lost: %v", names)
	}
}

func TestRemoveRegisteredServersPreservesUnrelatedFieldsAndMode(t *testing.T) {
	path := registryPath(t)
	document := `{
  "servers": [
    {
      "name": "user-owned",
      "command": "user-command",
      "args": [
        "--flag"
      ],
      "transport": "stdio",
      "enabled": true
    },
    {
      "name": "demo-plugin/tools",
      "command": "plugin-command",
      "transport": "stdio"
    }
  ]
}`
	if err := os.WriteFile(path, []byte(document), 0o640); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := RemoveRegisteredServers(path, []string{"demo-plugin/tools"}); err != nil {
		t.Fatalf("remove: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("existing registry permissions changed to %v", info.Mode().Perm())
	}
	config, _, err := readGlobalConfigFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(config.Servers) != 1 {
		t.Fatalf("expected one remaining server: %+v", config.Servers)
	}
	remaining := config.Servers[0]
	if remaining.Name != "user-owned" || remaining.Command != "user-command" ||
		!slices.Equal(remaining.Args, []string{"--flag"}) || !remaining.Enabled {
		t.Fatalf("a preserved registration lost field content: %+v", remaining)
	}
}

func TestRemoveRegisteredServersAcceptsAnAbsentDocument(t *testing.T) {
	path := registryPath(t)
	removed, err := RemoveRegisteredServers(path, []string{"demo-plugin/tools"})
	if err != nil || removed != 0 {
		t.Fatalf("absent registry should be an empty no-op: %d %v", removed, err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("an absent registry was created by a removal")
	}
	names, err := RegisteredServerNames(path)
	if err != nil || len(names) != 0 {
		t.Fatalf("absent registry should list no servers: %v %v", names, err)
	}
}

func TestRegistryProblemsBlockRatherThanReadAsEmpty(t *testing.T) {
	t.Run("corrupt document", func(t *testing.T) {
		path := registryPath(t)
		if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		if _, err := RegisteredServerNames(path); !errors.Is(err, ErrRegistryUnreadable) {
			t.Fatalf("expected an unreadable-registry error, got %v", err)
		}
		if _, err := RemoveRegisteredServers(path, []string{"demo-plugin/tools"}); !errors.Is(err, ErrRegistryUnreadable) {
			t.Fatalf("expected removal to refuse a corrupt registry, got %v", err)
		}
	})
	t.Run("directory in place of the document", func(t *testing.T) {
		path := registryPath(t)
		if err := os.MkdirAll(path, 0o750); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if _, err := RegisteredServerNames(path); !errors.Is(err, ErrRegistryUnreadable) {
			t.Fatalf("expected an unreadable-registry error, got %v", err)
		}
	})
	t.Run("non-canonical location", func(t *testing.T) {
		if _, err := RegisteredServerNames("relative/mcp_registry.json"); !errors.Is(err, ErrRegistryUnreadable) {
			t.Fatalf("expected a canonical-location refusal, got %v", err)
		}
	})
}

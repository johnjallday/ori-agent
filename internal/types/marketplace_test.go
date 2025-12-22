package types

import (
	"path/filepath"
	"runtime"
	"testing"
)

func TestDetectMarketplaceSourceType_LocalFile(t *testing.T) {
	absPath := filepath.Join(string(filepath.Separator), "tmp", "plugin_registry.json")
	if runtime.GOOS == "windows" {
		absPath = filepath.Join("C:\\", "tmp", "plugin_registry.json")
	}

	if got := DetectMarketplaceSourceType(absPath); got != "file" {
		t.Fatalf("expected file source type, got %q", got)
	}

	if runtime.GOOS != "windows" {
		if got := DetectMarketplaceSourceType("file:///tmp/plugin_registry.json"); got != "file" {
			t.Fatalf("expected file source type for file URL, got %q", got)
		}
	}
}

func TestResolveLocalMarketplacePath(t *testing.T) {
	absPath := filepath.Join(string(filepath.Separator), "tmp", "plugin_registry.json")
	if runtime.GOOS == "windows" {
		absPath = filepath.Join("C:\\", "tmp", "plugin_registry.json")
	}

	path, err := ResolveLocalMarketplacePath(absPath)
	if err != nil {
		t.Fatalf("unexpected error for absolute path: %v", err)
	}
	if path == "" {
		t.Fatal("expected resolved path to be non-empty")
	}

	if runtime.GOOS != "windows" {
		path, err = ResolveLocalMarketplacePath("file:///tmp/plugin_registry.json")
		if err != nil {
			t.Fatalf("unexpected error for file URL: %v", err)
		}
		if path != "/tmp/plugin_registry.json" {
			t.Fatalf("expected resolved file URL path, got %q", path)
		}
	}

	if _, err := ResolveLocalMarketplacePath("relative/path.json"); err == nil {
		t.Fatal("expected error for relative path")
	}
}

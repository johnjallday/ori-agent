package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveFilesystemBasePath(t *testing.T) {
	cfg := ServerConfig{
		Name: "filesystem",
		Args: []string{"-y", "@modelcontextprotocol/server-filesystem", "/tmp/allowed-root"},
	}

	base := resolveFilesystemBasePath(cfg)
	if got, want := base, filepath.Clean("/tmp/allowed-root"); got != want {
		t.Fatalf("unexpected base path: got=%q want=%q", got, want)
	}
}

func TestNormalizeFilesystemArguments_RelativePathUsesAllowedRoot(t *testing.T) {
	cfg := ServerConfig{
		Name: "filesystem",
		Args: []string{"-y", "@modelcontextprotocol/server-filesystem", "/tmp/allowed-root"},
	}

	input := map[string]any{
		"path": "Downloads",
	}

	out := normalizeFilesystemArguments("list_directory", input, cfg)
	got, _ := out["path"].(string)
	want := filepath.Join(filepath.Clean("/tmp/allowed-root"), "Downloads")
	if got != want {
		t.Fatalf("unexpected normalized path: got=%q want=%q", got, want)
	}
}

func TestNormalizeFilesystemArguments_AbsolutePathUnchanged(t *testing.T) {
	cfg := ServerConfig{
		Name: "filesystem",
		Args: []string{"-y", "@modelcontextprotocol/server-filesystem", "/tmp/allowed-root"},
	}

	input := map[string]any{
		"path": "/Users/tester/Downloads",
	}

	out := normalizeFilesystemArguments("list_directory", input, cfg)
	got, _ := out["path"].(string)
	want := filepath.Clean("/Users/tester/Downloads")
	if got != want {
		t.Fatalf("unexpected normalized path: got=%q want=%q", got, want)
	}
}

func TestNormalizeFilesystemArguments_RootAliasFallsBackToAllowedRoot(t *testing.T) {
	tempDir := t.TempDir()
	baseRoot := filepath.Join(tempDir, "Documents")
	if err := os.MkdirAll(baseRoot, 0o755); err != nil {
		t.Fatalf("failed to create base root: %v", err)
	}

	cfg := ServerConfig{
		Name: "filesystem",
		Args: []string{"-y", "@modelcontextprotocol/server-filesystem", baseRoot},
	}

	input := map[string]any{
		"path": "Documents",
	}

	out := normalizeFilesystemArguments("list_directory", input, cfg)
	got, _ := out["path"].(string)
	if got != filepath.Clean(baseRoot) {
		t.Fatalf("expected root alias to resolve to base root, got=%q want=%q", got, filepath.Clean(baseRoot))
	}
}

func TestNormalizeFilesystemArguments_RedundantRootPrefixIsCollapsed(t *testing.T) {
	tempDir := t.TempDir()
	baseRoot := filepath.Join(tempDir, "Documents")
	if err := os.MkdirAll(filepath.Join(baseRoot, "DNM"), 0o755); err != nil {
		t.Fatalf("failed to create test directories: %v", err)
	}

	cfg := ServerConfig{
		Name: "filesystem",
		Args: []string{"-y", "@modelcontextprotocol/server-filesystem", baseRoot},
	}

	input := map[string]any{
		"path": filepath.Join("Documents", "DNM"),
	}

	out := normalizeFilesystemArguments("list_directory", input, cfg)
	got, _ := out["path"].(string)
	want := filepath.Join(filepath.Clean(baseRoot), "DNM")
	if got != want {
		t.Fatalf("expected redundant root prefix to be collapsed, got=%q want=%q", got, want)
	}
}

func TestNormalizeFilesystemArguments_NonFilesystemToolUnchanged(t *testing.T) {
	cfg := ServerConfig{
		Name: "filesystem",
		Args: []string{"-y", "@modelcontextprotocol/server-filesystem", "/tmp/allowed-root"},
	}

	input := map[string]any{
		"path": "Downloads",
	}

	out := normalizeFilesystemArguments("web_search", input, cfg)
	got, _ := out["path"].(string)
	if got != "Downloads" {
		t.Fatalf("expected unchanged argument for non-filesystem tool, got=%q", got)
	}
}

func TestAnnotateGetFileInfoResult_AddsPathAndName(t *testing.T) {
	result := "size: 10\nmodified: today"
	args := map[string]any{
		"path": "/Users/tester/Downloads/example.txt",
	}

	out := annotateGetFileInfoResult("get_file_info", result, args)
	if !strings.Contains(out, "path: /Users/tester/Downloads/example.txt") {
		t.Fatalf("expected path annotation, got=%q", out)
	}
	if !strings.Contains(out, "name: example.txt") {
		t.Fatalf("expected name annotation, got=%q", out)
	}
}

func TestAnnotateGetFileInfoResult_DoesNotDuplicateExistingNameOrPath(t *testing.T) {
	result := "path: /Users/tester/Downloads/example.txt\nname: example.txt\nsize: 10"
	args := map[string]any{
		"path": "/Users/tester/Downloads/example.txt",
	}

	out := annotateGetFileInfoResult("get_file_info", result, args)
	if out != result {
		t.Fatalf("expected unchanged result when already annotated")
	}
}

package mcp

import (
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

	input := map[string]interface{}{
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

	input := map[string]interface{}{
		"path": "/Users/tester/Downloads",
	}

	out := normalizeFilesystemArguments("list_directory", input, cfg)
	got, _ := out["path"].(string)
	want := filepath.Clean("/Users/tester/Downloads")
	if got != want {
		t.Fatalf("unexpected normalized path: got=%q want=%q", got, want)
	}
}

func TestNormalizeFilesystemArguments_NonFilesystemToolUnchanged(t *testing.T) {
	cfg := ServerConfig{
		Name: "filesystem",
		Args: []string{"-y", "@modelcontextprotocol/server-filesystem", "/tmp/allowed-root"},
	}

	input := map[string]interface{}{
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
	args := map[string]interface{}{
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
	args := map[string]interface{}{
		"path": "/Users/tester/Downloads/example.txt",
	}

	out := annotateGetFileInfoResult("get_file_info", result, args)
	if out != result {
		t.Fatalf("expected unchanged result when already annotated")
	}
}

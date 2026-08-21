package workspaceplan

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestArtifactWriter(t *testing.T) (*FileArtifactWriter, string) {
	t.Helper()
	root := t.TempDir()
	workspaceRoot := filepath.Join(root, "workspace")
	if err := os.MkdirAll(workspaceRoot, 0o755); err != nil {
		t.Fatalf("create workspace root: %v", err)
	}
	return NewFileArtifactWriter(func(string, string) string { return workspaceRoot }), workspaceRoot
}

func TestFileArtifactWriterWritesAndRemoves(t *testing.T) {
	ctx := context.Background()
	writer, root := newTestArtifactWriter(t)

	if err := writer.WriteArtifact(ctx, "ws-1", "tasks/prd.md", []byte("# PRD")); err != nil {
		t.Fatalf("write: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(root, "tasks", "prd.md"))
	if err != nil {
		t.Fatalf("read written artifact: %v", err)
	}
	if string(content) != "# PRD" {
		t.Errorf("content = %q", content)
	}

	if err := writer.RemoveArtifact(ctx, "ws-1", "tasks/prd.md"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "tasks", "prd.md")); !os.IsNotExist(err) {
		t.Error("the artifact was not removed")
	}
	// Removing something already gone is not an error: compensation runs after
	// a failure and must not fail itself.
	if err := writer.RemoveArtifact(ctx, "ws-1", "tasks/prd.md"); err != nil {
		t.Errorf("removing a missing artifact errored: %v", err)
	}
}

func TestFileArtifactWriterRefusesEscapes(t *testing.T) {
	ctx := context.Background()
	writer, root := newTestArtifactWriter(t)

	for _, path := range []string{
		"../escape.md",
		"tasks/../../escape.md",
		"/etc/passwd",
		"",
	} {
		err := writer.WriteArtifact(ctx, "ws-1", path, []byte("nope"))
		if err == nil {
			t.Errorf("unsafe path %q was written", path)
			continue
		}
		if !errors.Is(err, ErrUnsafePath) {
			t.Errorf("path %q error = %v, want ErrUnsafePath", path, err)
		}
	}

	// Nothing escaped the workspace.
	parent := filepath.Dir(root)
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatalf("read parent: %v", err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), "escape") {
			t.Errorf("a file escaped the workspace: %s", entry.Name())
		}
	}
}

// A symlinked directory pointing out of the workspace must not become an
// escape hatch. A purely textual containment check would miss this.
func TestFileArtifactWriterRefusesASymlinkedEscape(t *testing.T) {
	ctx := context.Background()
	writer, root := newTestArtifactWriter(t)

	outside := filepath.Join(filepath.Dir(root), "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatalf("create outside dir: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	err := writer.WriteArtifact(ctx, "ws-1", "escape/leaked.md", []byte("leaked"))
	if err == nil {
		t.Fatal("a symlinked path escaped the workspace")
	}
	if !errors.Is(err, ErrUnsafePath) {
		t.Errorf("error = %v, want ErrUnsafePath", err)
	}
	if _, statErr := os.Stat(filepath.Join(outside, "leaked.md")); !os.IsNotExist(statErr) {
		t.Error("content was written outside the workspace through a symlink")
	}
}

func TestFileArtifactWriterRequiresAConfiguredRoot(t *testing.T) {
	ctx := context.Background()

	unconfigured := NewFileArtifactWriter(nil)
	if err := unconfigured.WriteArtifact(ctx, "ws-1", "a.md", nil); !errors.Is(err, ErrValidation) {
		t.Errorf("error = %v, want ErrValidation", err)
	}

	empty := NewFileArtifactWriter(func(string, string) string { return "" })
	if err := empty.WriteArtifact(ctx, "ws-1", "a.md", nil); !errors.Is(err, ErrValidation) {
		t.Errorf("error = %v, want ErrValidation", err)
	}
}

func TestFileArtifactWriterCreatesNestedDirectories(t *testing.T) {
	ctx := context.Background()
	writer, root := newTestArtifactWriter(t)

	if err := writer.WriteArtifact(ctx, "ws-1", "a/b/c/deep.md", []byte("deep")); err != nil {
		t.Fatalf("write nested: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "a", "b", "c", "deep.md")); err != nil {
		t.Errorf("nested artifact not written: %v", err)
	}
}

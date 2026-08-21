package workspaceplan

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// FileArtifactWriter writes planning artifacts into a resolved workspace root.
// For software projects this may be the project repository; other workspaces
// use their own files directory.
//
// It re-checks containment after resolving the path, rather than trusting the
// normalization done earlier. A path is untrusted input wherever it arrives
// from, and this is the boundary that actually touches the disk — the last
// place a mistake is still cheap (FR-97, FR-169).
type FileArtifactWriter struct {
	// root resolves the approved root for one artifact path. A software Plan
	// may route repository planning files to its project while keeping notes in
	// the workspace's own files directory.
	root func(workspaceID, relativePath string) string
}

// NewFileArtifactWriter returns an artifact writer over a path-aware
// workspace-root resolver.
func NewFileArtifactWriter(root func(workspaceID, relativePath string) string) *FileArtifactWriter {
	return &FileArtifactWriter{root: root}
}

var _ ArtifactWriter = (*FileArtifactWriter)(nil)

// WriteArtifact writes content to a workspace-relative path.
func (w *FileArtifactWriter) WriteArtifact(_ context.Context, workspaceID, relativePath string, content []byte) error {
	target, err := w.resolve(workspaceID, relativePath)
	if err != nil {
		return err
	}
	// #nosec G301 G703 -- 0755 matches the permissions used for every other
	// user-facing workspace folder (see workspace/backlog_markdown_synchronizer.go
	// and sessionhttp/workspace_handler_sync.go); target came from resolve(),
	// which normalizes the path, re-checks containment against the
	// symlink-evaluated root, and re-checks it again against the resolved
	// parent -- gosec's taint analysis cannot follow through that helper.
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("create artifact directory: %w", err)
	}
	// #nosec G306 G703 -- 0644 keeps plan artifacts (PRDs, task lists, notes)
	// openable in the user's own editor like every other workspace file;
	// target is containment-checked by resolve() as above.
	if err := os.WriteFile(target, content, 0o644); err != nil {
		return fmt.Errorf("write artifact: %w", err)
	}
	return nil
}

// RemoveArtifact deletes a previously written artifact, used to compensate a
// failed materialization.
func (w *FileArtifactWriter) RemoveArtifact(_ context.Context, workspaceID, relativePath string) error {
	target, err := w.resolve(workspaceID, relativePath)
	if err != nil {
		return err
	}
	// #nosec G703 -- target is containment-checked by resolve() (normalize,
	// symlink-evaluated root, re-checked resolved parent) before any removal.
	if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove artifact: %w", err)
	}
	return nil
}

// resolve turns a workspace-relative artifact path into an absolute one and
// proves it stays inside the workspace.
//
// The containment check is done on the RESOLVED, symlink-evaluated parent so a
// symlink pointing out of the workspace cannot be used to escape it. Checking
// only the textual path would miss that entirely.
func (w *FileArtifactWriter) resolve(workspaceID, relativePath string) (string, error) {
	if w.root == nil {
		return "", fmt.Errorf("%w: no workspace files root is configured", ErrValidation)
	}
	cleanedRelative, err := NormalizeArtifactPath(relativePath)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrUnsafePath, err)
	}

	root := strings.TrimSpace(w.root(workspaceID, cleanedRelative))
	if root == "" {
		return "", fmt.Errorf("%w: workspace %s has no files root", ErrValidation, workspaceID)
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve workspace files root: %w", err)
	}
	// EvalSymlinks fails when the root does not exist yet, which is fine: an
	// unresolvable root cannot be escaped through a symlink either.
	if resolved, evalErr := filepath.EvalSymlinks(absoluteRoot); evalErr == nil {
		absoluteRoot = resolved
	}

	target := filepath.Join(absoluteRoot, filepath.FromSlash(cleanedRelative))
	if err := ensureWithin(absoluteRoot, target); err != nil {
		return "", err
	}

	// If any existing parent directory is a symlink out of the workspace,
	// joining looked safe but writing would not be.
	if parent, evalErr := filepath.EvalSymlinks(filepath.Dir(target)); evalErr == nil {
		if err := ensureWithin(absoluteRoot, filepath.Join(parent, filepath.Base(target))); err != nil {
			return "", err
		}
	}
	return target, nil
}

func ensureWithin(root, target string) error {
	relative, err := filepath.Rel(root, target)
	if err != nil {
		return fmt.Errorf("%w: artifact path %q is not within the workspace", ErrUnsafePath, target)
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%w: artifact path %q escapes the workspace", ErrUnsafePath, target)
	}
	return nil
}

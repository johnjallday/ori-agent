package workspace

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const maxProjectFileFallbackBytes int64 = 64 << 20

var (
	ErrProjectFileFallbackUnavailable = errors.New("project-file fallback is unavailable")
	ErrProjectFileFallbackConflict    = errors.New("project changed during file fallback")
	ErrProjectFileFallbackScope       = errors.New("project-file fallback left its allowed scope")
)

// ProjectFileFallbackSource resolves only persisted workspace metadata and its
// trusted folder. Browser, template, task, and plugin input cannot supply a
// path to the fallback.
type ProjectFileFallbackSource interface {
	GetFolderWorkspace(string) (*Workspace, error)
	GetFolderPath(string) (string, error)
}

type ProjectFileFallbackPreparer struct {
	source ProjectFileFallbackSource
}

func NewProjectFileFallbackPreparer(source ProjectFileFallbackSource) *ProjectFileFallbackPreparer {
	return &ProjectFileFallbackPreparer{source: source}
}

func (p *ProjectFileFallbackPreparer) PrepareTaskFileFallback(_ context.Context, workspaceID string, task Task, capability string) (TaskFileFallbackRun, error) {
	capability = NormalizeRuntimeIdentifier(capability)
	if p == nil || p.source == nil || capability == "" || !task.AllowsFileFallback(capability) {
		return nil, ErrProjectFileFallbackUnavailable
	}
	sourcePath, err := authoritativeProjectEntry(p.source, workspaceID)
	if err != nil {
		return nil, ErrProjectFileFallbackUnavailable
	}
	original, mode, err := readProjectFallbackFile(sourcePath)
	if err != nil {
		return nil, ErrProjectFileFallbackUnavailable
	}
	stage, err := os.MkdirTemp("", "ori-project-file-fallback-")
	if err != nil {
		return nil, ErrProjectFileFallbackUnavailable
	}
	filename := filepath.Base(sourcePath)
	stagedPath := filepath.Join(stage, filename)
	if err := os.WriteFile(stagedPath, original, 0o600); err != nil {
		_ = os.RemoveAll(stage)
		return nil, ErrProjectFileFallbackUnavailable
	}

	prepared := task
	prepared.RequiredCapabilities = withoutRuntimeCapability(task.RequiredCapabilities, capability)
	prepared.RuntimeExecution = &TaskRuntimeExecution{
		WorkspaceRoot: stage, DisableTools: true, FileOnly: true, Filename: filename,
	}
	instruction := "Explicit file-only fallback approved: work only on " + filename + " in the current confined folder. Do not create another project or claim a verified live-application change."
	prepared.Details = strings.TrimSpace(prepared.Details + "\n\n" + instruction)

	return &projectFileFallbackRun{
		source: p.source, workspaceID: workspaceID, sourcePath: sourcePath,
		stageRoot: stage, stagedPath: stagedPath, filename: filename,
		originalMode: mode, originalHash: sha256.Sum256(original), prepared: prepared,
	}, nil
}

type projectFileFallbackRun struct {
	source       ProjectFileFallbackSource
	workspaceID  string
	sourcePath   string
	stageRoot    string
	stagedPath   string
	filename     string
	originalMode os.FileMode
	originalHash [sha256.Size]byte
	prepared     Task
}

func (r *projectFileFallbackRun) PreparedTask() Task { return r.prepared }

func (r *projectFileFallbackRun) Commit() error {
	if r == nil || strings.TrimSpace(r.stageRoot) == "" {
		return ErrProjectFileFallbackUnavailable
	}
	entries, err := os.ReadDir(r.stageRoot)
	if err != nil || len(entries) != 1 || entries[0].Name() != r.filename || entries[0].IsDir() || entries[0].Type()&os.ModeSymlink != 0 {
		return ErrProjectFileFallbackScope
	}
	currentSource, err := authoritativeProjectEntry(r.source, r.workspaceID)
	if err != nil || !samePortableProjectPath(currentSource, r.sourcePath) {
		return ErrProjectFileFallbackConflict
	}
	current, _, err := readProjectFallbackFile(currentSource)
	if err != nil || sha256.Sum256(current) != r.originalHash {
		return ErrProjectFileFallbackConflict
	}
	updated, _, err := readProjectFallbackFile(r.stagedPath)
	if err != nil {
		return ErrProjectFileFallbackScope
	}
	return atomicProjectFallbackWrite(currentSource, updated, r.originalMode)
}

func (r *projectFileFallbackRun) Abort() {
	if r != nil && strings.TrimSpace(r.stageRoot) != "" {
		_ = os.RemoveAll(r.stageRoot)
		r.stageRoot = ""
	}
}

func authoritativeProjectEntry(source ProjectFileFallbackSource, workspaceID string) (string, error) {
	if source == nil || strings.TrimSpace(workspaceID) == "" {
		return "", ErrProjectFileFallbackUnavailable
	}
	ws, err := source.GetFolderWorkspace(strings.TrimSpace(workspaceID))
	if err != nil || ws == nil {
		return "", ErrProjectFileFallbackUnavailable
	}
	root, err := source.GetFolderPath(ws.ID)
	if err != nil || !filepath.IsAbs(root) {
		return "", ErrProjectFileFallbackUnavailable
	}
	entry, err := GetProjectEntryPath(ws.SharedData)
	if err != nil || entry == "" {
		return "", ErrProjectFileFallbackUnavailable
	}
	projectRoot, err := inspectFallbackRelativePath(root, ws.ProjectPath, true)
	if err != nil {
		return "", err
	}
	target, err := inspectFallbackRelativePath(projectRoot, entry, false)
	if err != nil || !fallbackPathInside(target, projectRoot) || !fallbackPathInside(projectRoot, root) {
		return "", ErrProjectFileFallbackScope
	}
	return filepath.Clean(target), nil
}

func inspectFallbackRelativePath(root, portable string, wantDirectory bool) (string, error) {
	root, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return "", ErrProjectFileFallbackScope
	}
	portable = strings.TrimSpace(portable)
	if portable == "" || filepath.IsAbs(portable) || strings.ContainsRune(portable, '\x00') || strings.Contains(portable, `\`) {
		return "", ErrProjectFileFallbackScope
	}
	current := root
	for index, segment := range strings.Split(filepath.ToSlash(portable), "/") {
		if segment == "" || segment == "." || segment == ".." {
			return "", ErrProjectFileFallbackScope
		}
		current = filepath.Join(current, filepath.FromSlash(segment))
		if !fallbackPathInsideLexically(current, root) || current == root {
			return "", ErrProjectFileFallbackScope
		}
		info, statErr := os.Lstat(current) // #nosec G304 -- trusted segment-checked workspace metadata
		if statErr != nil || info.Mode()&os.ModeSymlink != 0 {
			return "", ErrProjectFileFallbackUnavailable
		}
		last := index == len(strings.Split(filepath.ToSlash(portable), "/"))-1
		if !last && !info.IsDir() || last && wantDirectory && !info.IsDir() || last && !wantDirectory && !info.Mode().IsRegular() {
			return "", ErrProjectFileFallbackScope
		}
	}
	return filepath.Clean(current), nil
}

func readProjectFallbackFile(path string) ([]byte, os.FileMode, error) {
	info, err := os.Lstat(path) // #nosec G304 -- canonical authoritative/staging project path
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() > maxProjectFileFallbackBytes {
		return nil, 0, ErrProjectFileFallbackUnavailable
	}
	file, err := os.Open(path) // #nosec G304 -- bounded checked project path
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, maxProjectFileFallbackBytes+1))
	if err != nil || int64(len(data)) > maxProjectFileFallbackBytes {
		return nil, 0, ErrProjectFileFallbackUnavailable
	}
	return data, info.Mode().Perm(), nil
}

func atomicProjectFallbackWrite(destination string, data []byte, mode os.FileMode) error {
	parent := filepath.Dir(destination)
	if !fallbackPathInsideLexically(destination, parent) || filepath.Base(destination) == "." {
		return ErrProjectFileFallbackScope
	}
	temp, err := os.CreateTemp(parent, ".ori-project-fallback-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer func() { _ = os.Remove(tempPath) }()
	if err := temp.Chmod(mode.Perm()); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, destination); err != nil {
		return fmt.Errorf("promote project fallback: %w", err)
	}
	return nil
}

func withoutRuntimeCapability(capabilities []string, excluded string) []string {
	out := make([]string, 0, len(capabilities))
	for _, capability := range NormalizeCapabilityKeys(capabilities) {
		if capability != excluded {
			out = append(out, capability)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func fallbackPathInsideLexically(path, root string) bool {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func fallbackPathInside(path, root string) bool {
	canonicalPath, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		return false
	}
	canonicalRoot, err := filepath.EvalSymlinks(filepath.Clean(root))
	return err == nil && fallbackPathInsideLexically(canonicalPath, canonicalRoot)
}

func samePortableProjectPath(current, expected string) bool {
	currentCanonical, currentErr := filepath.EvalSymlinks(filepath.Clean(current))
	expectedCanonical, expectedErr := filepath.EvalSymlinks(filepath.Clean(expected))
	if currentErr != nil || expectedErr != nil {
		return false
	}
	if runtime.GOOS == "darwin" {
		return strings.EqualFold(currentCanonical, expectedCanonical)
	}
	return currentCanonical == expectedCanonical
}

var _ TaskFileFallbackPreparer = (*ProjectFileFallbackPreparer)(nil)

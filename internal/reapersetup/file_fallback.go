package reapersetup

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

const maxFallbackProjectBytes int64 = 64 << 20

var (
	ErrFileFallbackUnavailable = errors.New("REAPER project-file fallback is unavailable")
	ErrFileFallbackConflict    = errors.New("REAPER project changed during file fallback")
	ErrFileFallbackScope       = errors.New("REAPER project-file fallback left its allowed scope")
)

type FileFallbackPreparer struct {
	source RuntimeWorkspaceSource
}

func NewFileFallbackPreparer(source RuntimeWorkspaceSource) *FileFallbackPreparer {
	return &FileFallbackPreparer{source: source}
}

func (p *FileFallbackPreparer) PrepareTaskFileFallback(_ context.Context, workspaceID string, task workspace.Task, capability string) (workspace.TaskFileFallbackRun, error) {
	capability = workspace.NormalizeRuntimeIdentifier(capability)
	if p == nil || p.source == nil || capability != ReaperLiveControlCapability || !task.AllowsFileFallback(capability) {
		return nil, ErrFileFallbackUnavailable
	}
	sourcePath, err := AuthoritativeProject(p.source, workspaceID)
	if err != nil {
		return nil, ErrFileFallbackUnavailable
	}
	original, mode, err := readFallbackFile(sourcePath)
	if err != nil {
		return nil, ErrFileFallbackUnavailable
	}
	stage, err := os.MkdirTemp("", "ori-reaper-file-fallback-")
	if err != nil {
		return nil, ErrFileFallbackUnavailable
	}
	filename := filepath.Base(sourcePath)
	stagedPath := filepath.Join(stage, filename)
	if err := os.WriteFile(stagedPath, original, 0o600); err != nil {
		_ = os.RemoveAll(stage)
		return nil, ErrFileFallbackUnavailable
	}

	prepared := task
	prepared.RequiredCapabilities = withoutCapability(task.RequiredCapabilities, capability)
	prepared.RuntimeExecution = &workspace.TaskRuntimeExecution{
		WorkspaceRoot: stage,
		DisableTools:  true,
		FileOnly:      true,
		Filename:      filename,
	}
	instruction := "Explicit file-only fallback approved: work only on " + filename + " in the current confined folder. Do not create another project or claim a live REAPER change."
	prepared.Details = strings.TrimSpace(prepared.Details + "\n\n" + instruction)

	return &fileFallbackRun{
		source:       p.source,
		workspaceID:  workspaceID,
		sourcePath:   sourcePath,
		stageRoot:    stage,
		stagedPath:   stagedPath,
		filename:     filename,
		originalMode: mode,
		originalHash: sha256.Sum256(original),
		prepared:     prepared,
	}, nil
}

type fileFallbackRun struct {
	source       RuntimeWorkspaceSource
	workspaceID  string
	sourcePath   string
	stageRoot    string
	stagedPath   string
	filename     string
	originalMode os.FileMode
	originalHash [sha256.Size]byte
	prepared     workspace.Task
}

func (r *fileFallbackRun) PreparedTask() workspace.Task { return r.prepared }

func (r *fileFallbackRun) Commit() error {
	if r == nil || strings.TrimSpace(r.stageRoot) == "" {
		return ErrFileFallbackUnavailable
	}
	entries, err := os.ReadDir(r.stageRoot)
	if err != nil || len(entries) != 1 || entries[0].Name() != r.filename || entries[0].IsDir() || entries[0].Type()&os.ModeSymlink != 0 {
		return ErrFileFallbackScope
	}
	currentSource, err := AuthoritativeProject(r.source, r.workspaceID)
	if err != nil || !sameProjectPath(currentSource, r.sourcePath) {
		return ErrFileFallbackConflict
	}
	current, _, err := readFallbackFile(currentSource)
	if err != nil || sha256.Sum256(current) != r.originalHash {
		return ErrFileFallbackConflict
	}
	updated, _, err := readFallbackFile(r.stagedPath)
	if err != nil {
		return ErrFileFallbackScope
	}
	return atomicProjectWrite(currentSource, updated, r.originalMode)
}

func (r *fileFallbackRun) Abort() {
	if r != nil && strings.TrimSpace(r.stageRoot) != "" {
		_ = os.RemoveAll(r.stageRoot)
		r.stageRoot = ""
	}
}

func readFallbackFile(path string) ([]byte, os.FileMode, error) {
	info, err := os.Lstat(path) // #nosec G304 -- canonical authoritative/staging project path
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() > maxFallbackProjectBytes {
		return nil, 0, ErrFileFallbackUnavailable
	}
	file, err := os.Open(path) // #nosec G304 -- canonical authoritative/staging project path, checked above
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, maxFallbackProjectBytes+1))
	if err != nil || int64(len(data)) > maxFallbackProjectBytes {
		return nil, 0, ErrFileFallbackUnavailable
	}
	return data, info.Mode().Perm(), nil
}

func atomicProjectWrite(destination string, data []byte, mode os.FileMode) error {
	parent := filepath.Dir(destination)
	if !pathInsideLexically(destination, parent) || filepath.Base(destination) == "." {
		return ErrFileFallbackScope
	}
	temp, err := os.CreateTemp(parent, ".ori-reaper-fallback-*")
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
		return fmt.Errorf("promote REAPER project fallback: %w", err)
	}
	return nil
}

func withoutCapability(capabilities []string, excluded string) []string {
	out := make([]string, 0, len(capabilities))
	for _, capability := range workspace.NormalizeCapabilityKeys(capabilities) {
		if capability != excluded {
			out = append(out, capability)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

var _ workspace.TaskFileFallbackPreparer = (*FileFallbackPreparer)(nil)

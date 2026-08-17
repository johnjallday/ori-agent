package reapersetup

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

const (
	runnerDirectoryName  = ".ori-reaper"
	maxRunnerRootEntries = 128
)

var (
	ErrRunnerRootUnavailable = errors.New("REAPER runner directory is unavailable")
	ErrRunnerRootUnsafe      = errors.New("REAPER runner directory is unsafe")
)

// RunnerRootResolver resolves the one capability-owned runner exchange root
// from trusted application configuration. Templates, browser requests, tasks,
// prompts, and model output never provide this value.
type RunnerRootResolver interface {
	Resolve() (string, error)
}

type defaultRunnerRootResolver struct {
	homeDir func() (string, error)
}

func NewRunnerRootResolver() RunnerRootResolver {
	return &defaultRunnerRootResolver{homeDir: os.UserHomeDir}
}

func (r *defaultRunnerRootResolver) Resolve() (string, error) {
	if r == nil || r.homeDir == nil {
		return "", ErrRunnerRootUnavailable
	}
	home, err := r.homeDir()
	if err != nil || home == "" {
		return "", ErrRunnerRootUnavailable
	}
	return canonicalRunnerRoot(filepath.Join(home, runnerDirectoryName))
}

// canonicalRunnerRoot validates a trusted configured root for grant/execution
// use. The directory must already exist, be a real directory rather than a
// symlink, and contain no symlink that could redirect a provider write outside
// the capability-owned tree. Parent-directory aliases (such as macOS /var →
// /private/var) are resolved, and the evaluated absolute path is returned.
func canonicalRunnerRoot(root string) (string, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", ErrRunnerRootUnavailable
	}
	absolute = filepath.Clean(absolute)
	info, err := os.Lstat(absolute)
	if err != nil {
		return "", ErrRunnerRootUnavailable
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", ErrRunnerRootUnsafe
	}
	if !info.IsDir() {
		return "", ErrRunnerRootUnavailable
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", ErrRunnerRootUnsafe
	}
	canonical = filepath.Clean(canonical)

	entries := 0
	err = filepath.WalkDir(canonical, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return ErrRunnerRootUnavailable
		}
		entries++
		if entries > maxRunnerRootEntries {
			return ErrRunnerRootUnsafe
		}
		if path != canonical && entry.Type()&os.ModeSymlink != 0 {
			return ErrRunnerRootUnsafe
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return canonical, nil
}

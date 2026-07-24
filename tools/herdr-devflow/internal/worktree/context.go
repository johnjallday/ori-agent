// Package worktree resolves repository and user-local runtime locations.
package worktree

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	HomeOverrideEnv   = "HERDR_DEVFLOW_HOME"
	ConfigOverrideEnv = "HERDR_DEVFLOW_CONFIG"
	RepoOverrideEnv   = "HERDR_DEVFLOW_REPO_ROOT"
)

type Paths struct {
	RepoRoot         string
	RepositoryID     string
	GitCommonDir     string
	ConfigPath       string
	RuntimeRoot      string
	StateDir         string
	LogDir           string
	PluginRuntimeDir string
	HelperPath       string
	PluginSourceDir  string
}

// FindRepoRoot walks upward until it finds the .git directory or gitfile used
// by a linked worktree. It avoids shelling out so doctor remains useful when
// Git itself is unavailable or misconfigured.
func FindRepoRoot(start string) (string, error) {
	current, err := canonicalPath(start)
	if err != nil {
		return "", err
	}
	if info, err := os.Stat(current); err == nil && !info.IsDir() {
		current = filepath.Dir(current)
	}
	for {
		if _, err := os.Lstat(filepath.Join(current, ".git")); err == nil {
			return current, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("no Git worktree found above %s", start)
		}
		current = parent
	}
}

// Resolve creates no directories. It only determines canonical paths and
// rejects configurations that would put mutable state inside a Git checkout.
func Resolve(repoRoot string, lookupEnv func(string) (string, bool)) (Paths, error) {
	if lookupEnv == nil {
		lookupEnv = os.LookupEnv
	}
	canonicalRepo, err := canonicalPath(repoRoot)
	if err != nil {
		return Paths{}, fmt.Errorf("resolve repository root: %w", err)
	}

	configPath := filepath.Join(canonicalRepo, ".herdr", "devflow.toml")
	if raw, ok := lookupEnv(ConfigOverrideEnv); ok && strings.TrimSpace(raw) != "" {
		configPath, err = canonicalPath(raw)
		if err != nil {
			return Paths{}, fmt.Errorf("resolve %s: %w", ConfigOverrideEnv, err)
		}
	}

	runtimeRoot, err := runtimeRoot(lookupEnv)
	if err != nil {
		return Paths{}, err
	}
	if within(canonicalRepo, runtimeRoot) {
		return Paths{}, fmt.Errorf("%s must be outside the Git checkout", HomeOverrideEnv)
	}

	commonDir := gitCommonDir(canonicalRepo)
	return Paths{
		RepoRoot:         canonicalRepo,
		RepositoryID:     RepositoryID(commonDir),
		GitCommonDir:     commonDir,
		ConfigPath:       configPath,
		RuntimeRoot:      runtimeRoot,
		StateDir:         filepath.Join(runtimeRoot, "state"),
		LogDir:           filepath.Join(runtimeRoot, "logs"),
		PluginRuntimeDir: filepath.Join(runtimeRoot, "plugin"),
		HelperPath:       filepath.Join(runtimeRoot, "bin", "herdr-devflow"),
		PluginSourceDir:  filepath.Join(canonicalRepo, "tools", "herdr-devflow"),
	}, nil
}

func runtimeRoot(lookupEnv func(string) (string, bool)) (string, error) {
	if raw, ok := lookupEnv(HomeOverrideEnv); ok && strings.TrimSpace(raw) != "" {
		path, err := canonicalPath(raw)
		if err != nil {
			return "", fmt.Errorf("resolve %s: %w", HomeOverrideEnv, err)
		}
		return path, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config directory: %w", err)
	}
	return filepath.Join(base, "herdr", "ori-devflow"), nil
}

func canonicalPath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("path is required")
	}
	for _, value := range path {
		if value == 0 || value < 32 || value == 127 {
			return "", fmt.Errorf("path contains a control character")
		}
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	// EvalSymlinks only succeeds once every path component exists. Runtime
	// roots commonly do not exist before setup, so resolve the deepest existing
	// ancestor and reattach the missing tail. This keeps /var and /private/var
	// aliases from producing different identities on macOS.
	probe := abs
	for {
		if resolved, err := filepath.EvalSymlinks(probe); err == nil {
			rel, relErr := filepath.Rel(probe, abs)
			if relErr != nil {
				return "", relErr
			}
			return filepath.Clean(filepath.Join(resolved, rel)), nil
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			break
		}
		probe = parent
	}
	return filepath.Clean(abs), nil
}

func within(parent, candidate string) bool {
	rel, err := filepath.Rel(parent, candidate)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// RepositoryID creates a stable local identity from a common Git directory.
// All linked worktrees of one repository share that directory, unlike their
// separate checkout roots.
func RepositoryID(gitCommonDir string) string {
	sum := sha256.Sum256([]byte(gitCommonDir))
	short := hex.EncodeToString(sum[:])[:10]
	base := strings.ToLower(filepath.Base(gitCommonDir))
	base = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, base)
	base = strings.Trim(base, "-_")
	if base == "" {
		base = "repo"
	}
	return base + "-" + short
}

func gitCommonDir(repoRoot string) string {
	gitPath := filepath.Join(repoRoot, ".git")
	info, err := os.Stat(gitPath)
	if err != nil {
		return repoRoot
	}
	if info.IsDir() {
		if canonical, err := canonicalPath(gitPath); err == nil {
			return canonical
		}
		return gitPath
	}
	// #nosec G304 -- gitPath is under the canonical repository root passed through Resolve.
	contents, err := os.ReadFile(gitPath)
	if err != nil {
		return repoRoot
	}
	line := strings.TrimSpace(string(contents))
	const prefix = "gitdir:"
	if !strings.HasPrefix(line, prefix) {
		return repoRoot
	}
	gitDir := strings.TrimSpace(strings.TrimPrefix(line, prefix))
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(repoRoot, gitDir)
	}
	gitDir, err = canonicalPath(gitDir)
	if err != nil {
		return repoRoot
	}
	commonFile := filepath.Join(gitDir, "commondir")
	// #nosec G304 G703 -- gitDir was canonicalized above and commondir is a fixed Git metadata filename.
	commonContents, err := os.ReadFile(commonFile)
	if err != nil {
		return gitDir
	}
	commonDir := strings.TrimSpace(string(commonContents))
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(gitDir, commonDir)
	}
	canonical, err := canonicalPath(commonDir)
	if err != nil {
		return commonDir
	}
	return canonical
}

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

	return Paths{
		RepoRoot:         canonicalRepo,
		RepositoryID:     repositoryID(canonicalRepo),
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

func repositoryID(repoRoot string) string {
	sum := sha256.Sum256([]byte(repoRoot))
	short := hex.EncodeToString(sum[:])[:10]
	base := strings.ToLower(filepath.Base(repoRoot))
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

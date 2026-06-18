package plugin

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Load resolves a plugin source, detects and parses its manifest, and returns a
// normalized descriptor. cloneDir is used only when source is a git URL.
func Load(source, cloneDir string, prefer SourceFormat) (PluginDescriptor, error) {
	root, err := ResolveSource(source, cloneDir)
	if err != nil {
		return PluginDescriptor{}, err
	}
	m, err := DetectManifest(root, prefer)
	if err != nil {
		return PluginDescriptor{}, err
	}
	return Normalize(m, source)
}

// ResolveSource returns a local directory containing the plugin bundle. A local
// path is returned as-is; a git URL is cloned into cloneDir/<repo>. The caller
// owns cloneDir (e.g. a managed plugins directory).
func ResolveSource(source, cloneDir string) (string, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return "", ErrEmptySource
	}
	if isGitURL(source) {
		return cloneGit(source, cloneDir)
	}
	info, err := os.Stat(source)
	if err != nil {
		return "", fmt.Errorf("plugin: source %q not found: %w", source, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("plugin: source %q is not a directory", source)
	}
	return source, nil
}

func isGitURL(s string) bool {
	switch {
	case strings.HasPrefix(s, "git@"):
		return true
	case strings.HasSuffix(s, ".git"):
		return true
	case strings.HasPrefix(s, "https://"), strings.HasPrefix(s, "http://"):
		return true
	default:
		return false
	}
}

func cloneGit(url, cloneDir string) (string, error) {
	if strings.TrimSpace(cloneDir) == "" {
		return "", fmt.Errorf("plugin: git source requires a clone directory")
	}
	if err := os.MkdirAll(cloneDir, 0o750); err != nil {
		return "", fmt.Errorf("plugin: create clone dir: %w", err)
	}
	dest := filepath.Join(cloneDir, repoName(url))
	if _, err := os.Stat(dest); err == nil {
		return dest, nil // already cloned; update flow handles refresh
	}
	cmd := exec.Command("git", "clone", "--depth", "1", url, dest) // #nosec G204 -- url supplied by the user installing the plugin
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("plugin: git clone failed: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return dest, nil
}

func repoName(url string) string {
	url = strings.TrimSuffix(strings.TrimSpace(url), ".git")
	url = strings.TrimSuffix(url, "/")
	if i := strings.LastIndexAny(url, "/:"); i >= 0 {
		return url[i+1:]
	}
	return url
}

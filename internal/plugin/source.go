package plugin

import (
	"fmt"
	neturl "net/url"
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
// path is returned as-is; a git URL is cloned into cloneDir/<repo>; a git repo +
// subdirectory (encoded by encodeGitSubdir) is cloned/fetched — pinned to its
// ref/sha when present — and the subdirectory is returned. The caller owns
// cloneDir (e.g. a managed plugins directory).
func ResolveSource(source, cloneDir string) (string, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return "", ErrEmptySource
	}
	if g, ok := parseGitSubdir(source); ok {
		return resolveGitSubdir(g, cloneDir)
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

// pullGit fast-forwards an existing git clone in place (used when updating a
// plugin installed from a git source).
func pullGit(dir string) error {
	cmd := exec.Command("git", "-C", dir, "pull", "--ff-only") // #nosec G204 -- dir is a managed clone path
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("plugin: git pull failed: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// gitSubdirSource is a git repo plus an optional subdirectory and pinned commit.
type gitSubdirSource struct {
	URL    string
	Subdir string
	Ref    string
	Sha    string
}

// encodeGitSubdir packs a git repo + subdirectory (+ optional ref/sha) into one
// source string of the form "<url>#<query>", so it can be recorded as a plugin's
// source and re-resolved on update. Returns the bare URL when there is nothing
// extra to encode. The "https://"/".git" prefix is preserved so isGitURL still
// treats the result as a git source.
func encodeGitSubdir(rawURL, subdir, ref, sha string) string {
	v := neturl.Values{}
	if s := strings.TrimSpace(subdir); s != "" {
		v.Set("subdir", filepath.ToSlash(filepath.Clean(s)))
	}
	if ref != "" {
		v.Set("ref", ref)
	}
	if sha != "" {
		v.Set("sha", sha)
	}
	if len(v) == 0 {
		return rawURL
	}
	return rawURL + "#" + v.Encode()
}

// parseGitSubdir reverses encodeGitSubdir. ok is false for a plain source (one
// with no "#subdir=/ref=/sha=" fragment), which callers resolve the existing way.
func parseGitSubdir(s string) (gitSubdirSource, bool) {
	base, frag, ok := strings.Cut(s, "#")
	if !ok {
		return gitSubdirSource{}, false
	}
	v, err := neturl.ParseQuery(frag)
	if err != nil {
		return gitSubdirSource{}, false
	}
	g := gitSubdirSource{
		URL:    base,
		Subdir: v.Get("subdir"),
		Ref:    v.Get("ref"),
		Sha:    v.Get("sha"),
	}
	if g.Subdir == "" && g.Ref == "" && g.Sha == "" {
		return gitSubdirSource{}, false
	}
	return g, true
}

// resolveGitSubdir clones (or pin-fetches) the repo and returns the requested
// subdirectory. Pinned commits get a per-commit clone dir so two plugins from
// the same repo at different pins don't collide, and so the existing-dir
// shortcut is correct.
func resolveGitSubdir(g gitSubdirSource, cloneDir string) (string, error) {
	if strings.TrimSpace(cloneDir) == "" {
		return "", fmt.Errorf("plugin: git source requires a clone directory")
	}
	if err := os.MkdirAll(cloneDir, 0o750); err != nil {
		return "", fmt.Errorf("plugin: create clone dir: %w", err)
	}

	pin := g.Sha // most specific identifier wins for the directory name
	if pin == "" {
		pin = g.Ref
	}
	dirName := repoName(g.URL)
	if pin != "" {
		dirName += "@" + sanitizeRef(pin)
	}
	dest := filepath.Join(cloneDir, dirName)

	if _, err := os.Stat(dest); err != nil {
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("plugin: stat clone dir: %w", err)
		}
		if pin == "" {
			if err := runGit("clone", "--depth", "1", g.URL, dest); err != nil {
				return "", err
			}
		} else if err := fetchPinned(g.URL, pin, dest); err != nil {
			return "", err
		}
	}

	root := dest
	if g.Subdir != "" {
		// Force-root the subpath so a crafted "../" entry can't escape the clone.
		clean := filepath.Clean(string(filepath.Separator) + filepath.FromSlash(g.Subdir))
		root = filepath.Join(dest, clean)
		info, err := os.Stat(root)
		if err != nil || !info.IsDir() {
			return "", fmt.Errorf("plugin: subdirectory %q not found in %s", g.Subdir, g.URL)
		}
	}
	return root, nil
}

// fetchPinned shallow-fetches a single commit/ref into a fresh repo and checks
// it out detached. git clone --depth 1 cannot target an arbitrary commit, so an
// init + fetch <pin> + checkout FETCH_HEAD is used instead (GitHub serves
// reachable shas via allowReachableSHA1InWant; tags/branches always work).
func fetchPinned(url, pin, dest string) error {
	if err := os.MkdirAll(dest, 0o750); err != nil {
		return fmt.Errorf("plugin: create clone dir: %w", err)
	}
	steps := [][]string{
		{"-C", dest, "init", "-q"},
		{"-C", dest, "fetch", "--depth", "1", url, pin},
		{"-C", dest, "checkout", "-q", "FETCH_HEAD"},
	}
	for _, args := range steps {
		if err := runGit(args...); err != nil {
			_ = os.RemoveAll(dest) // don't leave a half-built repo that blocks retries
			return err
		}
	}
	return nil
}

// runGit runs a git command and wraps a non-zero exit with its output.
func runGit(args ...string) error {
	cmd := exec.Command("git", args...) // #nosec G204 -- git args derived from a user-added marketplace catalog
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("plugin: git %s failed: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// sanitizeRef makes a ref/sha safe to use as a directory-name suffix.
func sanitizeRef(s string) string {
	mapped := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			return r
		default:
			return '-'
		}
	}, s)
	if len(mapped) > 40 {
		mapped = mapped[:40]
	}
	return mapped
}

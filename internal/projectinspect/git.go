package projectinspect

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// Git layouts an inspection can report.
const (
	// GitNone: the project has no .git entry.
	GitNone = "none"
	// GitRepository: an ordinary checkout whose .git is a directory.
	GitRepository = "repository"
	// GitWorktree: a linked worktree whose .git file points at a directory that
	// points back at it.
	GitWorktree = "worktree"
	// GitUnverified: a .git entry Ori did not follow, because it is a link, is
	// malformed, or names a directory that does not point back at this project
	// (a submodule, or a pointer that leads somewhere unexpected).
	GitUnverified = "unverified"
)

const gitMetadataMaxBytes = 64 * 1024

// Git is what an inspection learned about a project's version control from
// Git's own metadata files. No git command is run, no hook or script is
// executed, and nothing is fetched.
type Git struct {
	Layout string
	// Branch is the checked-out branch, empty for a detached HEAD.
	Branch string
	// Remote is the origin as a host and a path, with any user name, password,
	// query or fragment removed. It is a label. It does not say Ori can reach
	// the repository, and it is never used to decide two folders are the same.
	Remote string
	// RepositoryKey is a digest of the repository's shared Git directory, so two
	// checkouts of one repository can be told to be related without storing a
	// path. It is a hint only.
	RepositoryKey string
}

// inspectGit reads fixed-name metadata files only: HEAD and config, and for a
// worktree the gitdir and commondir files that tie it to the main checkout. A
// .git file is a pointer anyone can write, so it is followed only when the
// directory it names points back at this exact .git file; otherwise nothing
// beyond the pointer is read and the layout is reported as unverified.
func inspectGit(root string, gaps *[]Gap) Git {
	marker := filepath.Join(root, ".git")
	info, err := os.Lstat(marker) // #nosec G304 G703 -- the server-held root joined with a fixed name
	if err != nil {
		return Git{Layout: GitNone}
	}
	unverified := func() Git {
		*gaps = append(*gaps, Gap{Code: GapGitUnverified, Source: ".git"})
		return Git{Layout: GitUnverified}
	}
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		return unverified()
	case info.IsDir():
		git := Git{Layout: GitRepository, Branch: readBranch(marker), RepositoryKey: repositoryKey(marker)}
		git.Remote = readRemote(marker, gaps)
		return git
	case !info.Mode().IsRegular():
		return unverified()
	}

	pointer, ok := readPointer(marker, root, "gitdir:")
	if !ok || !realDirectory(pointer) {
		return unverified()
	}
	// The administrative directory must name this worktree's .git file.
	back, ok := readPointer(filepath.Join(pointer, "gitdir"), pointer, "")
	if !ok || !samePath(back, marker) {
		return unverified()
	}
	common, ok := readPointer(filepath.Join(pointer, "commondir"), pointer, "")
	if !ok || !realDirectory(common) {
		return unverified()
	}
	git := Git{Layout: GitWorktree, Branch: readBranch(pointer), RepositoryKey: repositoryKey(common)}
	git.Remote = readRemote(common, gaps)
	return git
}

// readPointer reads a one-line file that names a path, optionally after a
// prefix, and resolves it against base when it is relative.
func readPointer(file, base, prefix string) (string, bool) {
	data, _, err := readRegularFile(file, 4096)
	if err != nil {
		return "", false
	}
	line := strings.TrimSpace(strings.SplitN(string(data), "\n", 2)[0])
	if prefix != "" {
		rest, found := strings.CutPrefix(line, prefix)
		if !found {
			return "", false
		}
		line = strings.TrimSpace(rest)
	}
	if line == "" || strings.ContainsRune(line, 0) {
		return "", false
	}
	if !filepath.IsAbs(line) {
		line = filepath.Join(base, line)
	}
	return filepath.Clean(line), true
}

// samePath compares two paths as written and, failing that, after resolving
// links, so a pointer written through a linked temporary directory still
// matches the canonical path the server holds.
func samePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if a == b {
		return true
	}
	resolvedA, errA := filepath.EvalSymlinks(a)
	resolvedB, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && resolvedA == resolvedB
}

func repositoryKey(commonDir string) string {
	canonical := commonDir
	if resolved, err := filepath.EvalSymlinks(commonDir); err == nil {
		canonical = resolved
	}
	sum := sha256.Sum256([]byte(filepath.Clean(canonical)))
	return hex.EncodeToString(sum[:16])
}

// readBranch reads the checked-out branch from HEAD. A detached HEAD has none.
func readBranch(gitDir string) string {
	data, _, err := readRegularFile(filepath.Join(gitDir, "HEAD"), 4096)
	if err != nil {
		return ""
	}
	ref, found := strings.CutPrefix(strings.TrimSpace(string(data)), "ref:")
	if !found {
		return ""
	}
	branch := strings.TrimPrefix(strings.TrimSpace(ref), "refs/heads/")
	if len(branch) > 200 || strings.ContainsAny(branch, "\x00\r\n ") {
		return ""
	}
	return branch
}

// readRemote returns the origin remote, sanitized, or the first remote when
// there is no origin.
func readRemote(gitDir string, gaps *[]Gap) string {
	data, _, err := readRegularFile(filepath.Join(gitDir, "config"), gitMetadataMaxBytes)
	if err != nil {
		return ""
	}
	first, origin, section := "", "", ""
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "[") {
			section = strings.ToLower(strings.Join(strings.Fields(line), " "))
			continue
		}
		if !strings.HasPrefix(section, `[remote "`) {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found || strings.TrimSpace(strings.ToLower(key)) != "url" {
			continue
		}
		value = strings.TrimSpace(value)
		if first == "" {
			first = value
		}
		if section == `[remote "origin"]` && origin == "" {
			origin = value
		}
	}
	raw := origin
	if raw == "" {
		raw = first
	}
	if raw == "" {
		return ""
	}
	remote := SanitizeRemote(raw)
	if remote == "" {
		*gaps = append(*gaps, Gap{Code: GapRemoteUnread, Source: ".git"})
	}
	return remote
}

// SanitizeRemote reduces a Git remote URL to its host and path. A user name, a
// password or token, a query and a fragment are removed; a remote that is a
// local path, or that cannot be read as host and path, yields the empty
// string. The result names a repository; it is not a credential and cannot be
// used as one.
func SanitizeRemote(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.ContainsAny(raw, "\x00\r\n\t ") {
		return ""
	}
	host, path := "", ""
	switch {
	case strings.Contains(raw, "://"):
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Host == "" || parsed.Scheme == "file" {
			return ""
		}
		host, path = parsed.Hostname(), parsed.Path
	default:
		// The scp-like form: [user@]host:path.
		rest := raw
		if at := strings.LastIndex(rest, "@"); at >= 0 {
			rest = rest[at+1:]
		}
		before, after, found := strings.Cut(rest, ":")
		if !found || strings.ContainsAny(before, "/\\") {
			return "" // a local path
		}
		host, path = before, after
	}
	host = strings.ToLower(strings.TrimSpace(host))
	path = strings.Trim(strings.TrimSpace(path), "/")
	if host == "" || path == "" || len(host)+len(path) > 280 || strings.ContainsAny(host+path, "@?#\\ ") {
		return ""
	}
	return host + "/" + path
}

// Package filesearch indexes names for the local UI, never on behalf of an agent.
package filesearch

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

var (
	ErrUnavailable = errors.New("folder search is unavailable")
	ErrRoot        = errors.New("choose a local folder outside system folders, workspaces and Ori data")
	ErrOverlap     = errors.New("this folder overlaps an existing search folder")
	ErrLimit       = errors.New("remove a search folder before adding another")
	ErrChoice      = errors.New("this choice expired; choose the folder again")
	ErrMissing     = errors.New("this search source has changed or was removed")
	ErrRequest     = errors.New("invalid search request")
	// ErrLink: the folder a workspace link names is not there now, or is not
	// the folder the link was made for. Reconnecting or relinking it is the fix.
	ErrLink = errors.New("this folder is missing or is not the one the workspace links to")
)

type Policy struct {
	Home string
	// Protected includes active and default app data, workspace, vault and template roots.
	Protected []string
}

func folded(s string) string { return cases.Fold().String(norm.NFC.String(s)) }
func inside(path, parent string) bool {
	p, q := folded(path), folded(parent)
	return p == q || strings.HasPrefix(p, strings.TrimSuffix(q, "/")+"/")
}
func overlap(p, q string) bool { return inside(p, q) || inside(q, p) }
func goodName(s string) bool {
	return s != "" && utf8.ValidString(s) && strings.IndexFunc(s, unicode.IsControl) < 0
}

// Resolve a host-owned guard even if its last components haven't been created.
// Never apply this to a selection: a symlink must not broaden a grant.
func physicalGuard(p string) (string, error) {
	if !filepath.IsAbs(p) {
		return "", ErrRoot
	}
	p = filepath.Clean(p)
	for base := p; ; base = filepath.Dir(base) {
		_, err := os.Lstat(base)
		if err == nil {
			real, e := filepath.EvalSymlinks(base)
			if e != nil {
				return "", ErrRoot
			}
			suffix, _ := filepath.Rel(base, p)
			return filepath.Join(real, suffix), nil
		}
		if !os.IsNotExist(err) || base == filepath.Dir(base) {
			return "", ErrRoot
		}
	}
}

func (g Policy) allows(path string) bool {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || !goodName(path) || len(path) > 4096 || len(g.Protected) == 0 {
		return false
	}
	home, err := physicalGuard(g.Home)
	if err != nil || home == "/" {
		return false
	}
	// A custom selection may also be a shared user folder or a directory on a local external disk.
	switch {
	// Be conservative on case-sensitive APFS: a differently cased sibling
	// isn't authority under the user's physical home. Fold only for denials.
	case strings.HasPrefix(path, home+"/"):
	case strings.HasPrefix(path, "/Users/Shared/"):
	case strings.HasPrefix(path, "/Volumes/") && strings.Count(path, "/") >= 3:
	default:
		return false
	}
	if overlap(path, filepath.Join(home, "Library")) {
		return false
	}
	for _, p := range strings.Split(path, "/") {
		if strings.HasPrefix(p, ".") || skipDirectory(p) {
			return false
		}
	}
	for _, guard := range g.Protected {
		p, e := physicalGuard(guard)
		if e != nil || overlap(path, p) {
			return false
		}
	}
	return true
}

func skipDirectory(name string) bool {
	n := strings.ToLower(name)
	switch n {
	case "node_modules", "vendor", "__pycache__", "deriveddata", "system volume information", "$recycle.bin":
		return true
	}
	switch filepath.Ext(n) {
	case ".app", ".bundle", ".framework", ".plugin", ".appex", ".pkg", ".xpc", ".photoslibrary", ".photolibrary", ".rtfd", ".band", ".logicx", ".playground":
		return true
	}
	return false
}

package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Bounds for one listing of a linked directory (FR54).
const (
	DirectoryListMaxDepth   = 3
	DirectoryListMaxEntries = 500
)

// Errors a linked-directory access can fail with. Their messages carry the
// words the Files explorer's HTTP handler maps to 403/404 ("access denied",
// "not allowed", "not found").
var (
	ErrDirectoryInternal    = errors.New("access denied: this directory is managed by a capability and is not readable here")
	ErrDirectoryPathOutside = errors.New("access denied: path is outside directory")
	ErrDirectoryNotFound    = errors.New("file not found")
)

// DirectoryEntry is one entry of a linked directory listing.
type DirectoryEntry struct {
	Name         string    `json:"name"`
	RelativePath string    `json:"path"`
	Kind         string    `json:"kind"` // file | folder | link
	Size         int64     `json:"size,omitempty"`
	ModTime      time.Time `json:"modified"`
}

// DirectoryListing is a bounded listing.
type DirectoryListing struct {
	Entries []DirectoryEntry
	// Truncated reports that the entry cap stopped the listing early.
	Truncated bool
}

// ResolveDirectoryPath resolves relativePath under a linked directory and
// returns the reference and the absolute path, refusing anything that would
// leave the directory (FR57): absolute paths, ".." segments, and symbolic
// links that resolve outside it. An empty path names the directory itself.
// Directories with an internal purpose are never resolved.
func (w *Workspace) ResolveDirectoryPath(dirID, relativePath string) (*DirectoryReference, string, error) {
	dir, err := w.GetDirectoryReference(dirID)
	if err != nil {
		return nil, "", err
	}
	if strings.TrimSpace(dir.Purpose) != "" {
		return nil, "", ErrDirectoryInternal
	}
	rel, err := sanitizeDirectoryRelativePath(relativePath)
	if err != nil {
		return nil, "", err
	}
	root, err := filepath.Abs(dir.Path)
	if err != nil {
		return nil, "", fmt.Errorf("failed to resolve directory path: %w", err)
	}
	abs := root
	if rel != "" {
		abs = filepath.Join(root, rel)
	}
	if !pathWithinRootAfterSymlinks(abs, root) {
		return nil, "", ErrDirectoryPathOutside
	}
	return dir, abs, nil
}

// sanitizeDirectoryRelativePath normalises a caller-supplied relative path.
// "" and "." mean the root; anything absolute or climbing out is refused.
func sanitizeDirectoryRelativePath(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || trimmed == "." {
		return "", nil
	}
	if err := validateRelativePath(trimmed); err != nil {
		return "", err
	}
	clean := filepath.Clean(trimmed)
	if clean == "." {
		return "", nil
	}
	return clean, nil
}

// ListDirectoryEntries lists a linked directory below relativePath, at most
// depth levels deep (capped at DirectoryListMaxDepth) and at most
// DirectoryListMaxEntries entries. Hidden entries are skipped and symbolic
// links are listed as links without being followed (FR54).
func (w *Workspace) ListDirectoryEntries(dirID, relativePath string, depth int) (DirectoryListing, error) {
	dir, start, err := w.ResolveDirectoryPath(dirID, relativePath)
	if err != nil {
		return DirectoryListing{}, err
	}
	info, err := os.Stat(start)
	if err != nil {
		if os.IsNotExist(err) {
			return DirectoryListing{}, ErrDirectoryNotFound
		}
		return DirectoryListing{}, fmt.Errorf("failed to read directory: %w", err)
	}
	if !info.IsDir() {
		return DirectoryListing{}, fmt.Errorf("%w: not a folder", ErrDirectoryNotFound)
	}
	if depth <= 0 || depth > DirectoryListMaxDepth {
		depth = DirectoryListMaxDepth
	}
	base, err := filepath.Abs(dir.Path)
	if err != nil {
		return DirectoryListing{}, err
	}

	var listing DirectoryListing
	var walk func(dir string, level int) error
	walk = func(dir string, level int) error {
		entries, err := os.ReadDir(dir)
		if err != nil {
			// A subfolder that cannot be read is skipped; the root's error
			// surfaces.
			if level == 1 {
				return fmt.Errorf("failed to read directory: %w", err)
			}
			return nil
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		for _, entry := range entries {
			name := entry.Name()
			if strings.HasPrefix(name, ".") {
				continue
			}
			if len(listing.Entries) >= DirectoryListMaxEntries {
				listing.Truncated = true
				return nil
			}
			full := filepath.Join(dir, name)
			rel, err := filepath.Rel(base, full)
			if err != nil {
				continue
			}
			item := DirectoryEntry{Name: name, RelativePath: filepath.ToSlash(rel)}
			mode := entry.Type()
			switch {
			case mode&os.ModeSymlink != 0:
				item.Kind = "link"
			case entry.IsDir():
				item.Kind = "folder"
			default:
				item.Kind = "file"
			}
			if fi, err := entry.Info(); err == nil {
				item.ModTime = fi.ModTime()
				if item.Kind == "file" {
					item.Size = fi.Size()
				}
			}
			listing.Entries = append(listing.Entries, item)
			if item.Kind == "folder" && level < depth {
				if err := walk(full, level+1); err != nil {
					return err
				}
				if listing.Truncated {
					return nil
				}
			}
		}
		return nil
	}
	if err := walk(start, 1); err != nil {
		return DirectoryListing{}, err
	}
	return listing, nil
}

// OpenDirectoryFile resolves a file inside a linked directory (FR57) and
// returns the reference, the path to read, and the file's info. The target
// must be a regular file once symbolic links are resolved.
func (w *Workspace) OpenDirectoryFile(dirID, relativePath string) (*DirectoryReference, string, os.FileInfo, error) {
	dir, abs, err := w.ResolveDirectoryPath(dirID, relativePath)
	if err != nil {
		return nil, "", nil, err
	}
	if strings.TrimSpace(relativePath) == "" || filepath.Clean(relativePath) == "." {
		return nil, "", nil, fmt.Errorf("%w: a file path is required", ErrDirectoryNotFound)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, "", nil, ErrDirectoryNotFound
		}
		return nil, "", nil, fmt.Errorf("failed to resolve file: %w", err)
	}
	root, err := filepath.EvalSymlinks(dir.Path)
	if err != nil {
		return nil, "", nil, fmt.Errorf("failed to resolve directory path: %w", err)
	}
	if !isPathWithin(resolved, root) {
		return nil, "", nil, ErrDirectoryPathOutside
	}
	info, err := os.Stat(resolved) // #nosec G304 G703 -- symlink-resolved and contained in the linked directory just above
	if err != nil {
		if os.IsNotExist(err) {
			return nil, "", nil, ErrDirectoryNotFound
		}
		return nil, "", nil, fmt.Errorf("failed to read file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, "", nil, fmt.Errorf("%w: not a regular file", ErrDirectoryNotFound)
	}
	return dir, resolved, info, nil
}
